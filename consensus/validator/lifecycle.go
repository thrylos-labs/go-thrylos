// consensus/validator/lifecycle.go

package validator

import (
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/math"
	"github.com/thrylos-labs/go-thrylos/core/state"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// Rate limiting and unbonding constants
const (
	MaxValidatorChangePercent = 10.0   // Max 10% change per epoch
	MaxValidatorsPerEpoch     = 100    // Absolute cap
	UnbondingPeriodBlocks     = 201600 // ~7 days at 3s/block
	ActivationDelayEpochs     = 900    // ~27 hours
	SlotsPerEpoch             = 32     // Match consensus.go
)

// LifecycleManager handles validator lifecycle transitions
type LifecycleManager struct {
	config     *config.Config
	worldState *state.WorldState
	manager    *Manager

	// Lifecycle tracking
	lifecycleEvents map[string][]*LifecycleEvent
	transitionQueue chan *LifecycleTransition

	// Performance thresholds
	activationThreshold   float64
	deactivationThreshold float64
	removalThreshold      float64

	// 🔴 H-05 FIX: Simple epoch tracking
	registrationEpoch  map[string]int64 // address -> epoch registered
	epochRegistrations map[int64]int    // epoch -> registration count

	// 🔴 H-05 FIX: Unbonding system
	unbondingValidators map[string]*UnbondingEntry
	unbondingPeriod     int64 // blocks

	// 🔴 H-05 FIX: Consensus lock
	votingInProgress bool
	votingLock       sync.RWMutex

	// Synchronization
	mu sync.RWMutex

	// Lifecycle worker
	isRunning bool
	stopChan  chan struct{}
}

// ValidatorState represents the current state of a validator
type ValidatorState string

const (
	StateRegistered   ValidatorState = "registered"
	StatePending      ValidatorState = "pending"
	StateActive       ValidatorState = "active"
	StateJailed       ValidatorState = "jailed"
	StateSlashed      ValidatorState = "slashed"
	StateDeactivating ValidatorState = "deactivating"
	StateInactive     ValidatorState = "inactive"
	StateRemoving     ValidatorState = "removing"
	StateRemoved      ValidatorState = "removed"
)

// LifecycleEvent represents a validator lifecycle event
type LifecycleEvent struct {
	ValidatorAddress string                 `json:"validator_address"`
	EventType        LifecycleEventType     `json:"event_type"`
	FromState        ValidatorState         `json:"from_state"`
	ToState          ValidatorState         `json:"to_state"`
	Timestamp        int64                  `json:"timestamp"`
	BlockHeight      int64                  `json:"block_height"`
	Reason           string                 `json:"reason"`
	Data             map[string]interface{} `json:"data"`
	TxHash           string                 `json:"tx_hash"`
}

// LifecycleEventType represents types of lifecycle events
type LifecycleEventType string

const (
	EventRegistration      LifecycleEventType = "registration"
	EventActivation        LifecycleEventType = "activation"
	EventDeactivation      LifecycleEventType = "deactivation"
	EventJailing           LifecycleEventType = "jailing"
	EventUnjailing         LifecycleEventType = "unjailing"
	EventSlashing          LifecycleEventType = "slashing"
	EventStakeIncrease     LifecycleEventType = "stake_increase"
	EventStakeDecrease     LifecycleEventType = "stake_decrease"
	EventPerformanceUpdate LifecycleEventType = "performance_update"
	EventRemoval           LifecycleEventType = "removal"
	EventRejoin            LifecycleEventType = "rejoin"
	EventUnbondingComplete LifecycleEventType = "unbonding_complete"
)

// LifecycleTransition represents a pending state transition
type LifecycleTransition struct {
	ValidatorAddress string         `json:"validator_address"`
	TargetState      ValidatorState `json:"target_state"`
	Reason           string         `json:"reason"`
	ScheduledBlock   int64          `json:"scheduled_block"`
}

// 🔴 H-05 FIX: UnbondingEntry tracks stake being unbonded
type UnbondingEntry struct {
	ValidatorAddress string `json:"validator_address"`
	DelegatorAddress string `json:"delegator_address"` // ✅ ADD THIS LINE
	Amount           string `json:"amount"`
	CompletionBlock  int64  `json:"completion_block"` // Keep this (block-based is fine)
	CreatedAt        int64  `json:"created_at"`
}

// NewLifecycleManager creates a new validator lifecycle manager
func NewLifecycleManager(config *config.Config, worldState *state.WorldState, manager *Manager) *LifecycleManager {
	return &LifecycleManager{
		config:          config,
		worldState:      worldState,
		manager:         manager,
		lifecycleEvents: make(map[string][]*LifecycleEvent),
		transitionQueue: make(chan *LifecycleTransition, 1000),

		// Performance thresholds
		activationThreshold:   0.8,
		deactivationThreshold: 0.3,
		removalThreshold:      0.1,

		// 🔴 H-05 FIX: Initialize tracking
		registrationEpoch:   make(map[string]int64),
		epochRegistrations:  make(map[int64]int),
		unbondingValidators: make(map[string]*UnbondingEntry),
		unbondingPeriod:     UnbondingPeriodBlocks,

		stopChan: make(chan struct{}),
	}
}

// Start begins the lifecycle management process
func (lm *LifecycleManager) Start() error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if lm.isRunning {
		return fmt.Errorf("lifecycle manager is already running")
	}

	lm.isRunning = true
	go lm.lifecycleWorker()

	return nil
}

// Stop halts the lifecycle management process
func (lm *LifecycleManager) Stop() error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if !lm.isRunning {
		return fmt.Errorf("lifecycle manager is not running")
	}

	lm.isRunning = false
	close(lm.stopChan)
	return nil
}

// RegisterValidator handles new validator registration
// 🔴 H-05 FIX: Added epoch-based rate limiting and stake verification
func (lm *LifecycleManager) RegisterValidator(
	address string,
	pubkey []byte,
	stake string,
	commission float64,
	selfDelegation string,
) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	// 1. Validate registration
	if err := lm.validateRegistrationRequirements(address, stake, commission, selfDelegation); err != nil {
		return fmt.Errorf("registration validation failed: %v", err)
	}

	// 🔴 H-05 FIX: Verify stake actually exists in account
	account, err := lm.worldState.GetAccount(address)
	if err != nil {
		return fmt.Errorf("account not found: %v", err)
	}

	accountBalance := math.ParseBigInt(account.Balance)

	requiredStake, _ := new(big.Int).SetString(stake, 10)
	if requiredStake == nil {
		return fmt.Errorf("invalid stake amount")
	}

	if accountBalance.Cmp(requiredStake) < 0 {
		return fmt.Errorf("insufficient account balance for stake: have %s, need %s",
			account.Balance, stake)
	}

	// 🔴 H-05 FIX: Check rate limit for current epoch
	currentEpoch := lm.getCurrentEpoch()

	if lm.epochRegistrations[currentEpoch] >= MaxValidatorsPerEpoch {
		return fmt.Errorf("maximum validator registrations (%d) reached for epoch %d",
			MaxValidatorsPerEpoch, currentEpoch)
	}

	// 🔴 H-05 FIX: Check % change limit
	if err := lm.checkPercentageChangeLimit(); err != nil {
		return fmt.Errorf("rate limit exceeded: %v", err)
	}

	// 2. Register validator (inactive until delay passes)
	if err := lm.manager.RegisterValidator(address, pubkey, stake, commission); err != nil {
		return fmt.Errorf("validator registration failed: %v", err)
	}

	validator, err := lm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("failed to get validator after registration: %v", err)
	}

	validator.Active = false
	if err := lm.worldState.UpdateValidator(validator); err != nil {
		return fmt.Errorf("failed to mark validator inactive: %v", err)
	}

	// 🔴 H-05 FIX: Track registration epoch
	lm.registrationEpoch[address] = currentEpoch
	lm.epochRegistrations[currentEpoch]++

	activationEpoch := currentEpoch + ActivationDelayEpochs

	lm.recordLifecycleEvent(address, &LifecycleEvent{
		ValidatorAddress: address,
		EventType:        EventRegistration,
		ToState:          StateRegistered,
		Timestamp:        time.Now().Unix(),
		BlockHeight:      lm.worldState.GetHeight(),
		Reason:           fmt.Sprintf("Registered (can activate at epoch %d)", activationEpoch),
		Data: map[string]interface{}{
			"stake":              stake,
			"commission":         commission,
			"registration_epoch": currentEpoch,
			"activation_epoch":   activationEpoch,
		},
	})

	return nil
}

// ProcessActivation handles validator activation
// 🔴 H-05 FIX: Checks activation delay and consensus lock
func (lm *LifecycleManager) ProcessActivation(address string) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	// 🔴 H-05 FIX: Check consensus lock
	if err := lm.canModifyValidatorSet(); err != nil {
		return err
	}

	// 🔴 H-05 FIX: Check epoch delay
	registeredEpoch, exists := lm.registrationEpoch[address]
	if !exists {
		return fmt.Errorf("validator %s registration epoch not found", address)
	}

	currentEpoch := lm.getCurrentEpoch()
	if currentEpoch < registeredEpoch+ActivationDelayEpochs {
		return fmt.Errorf("must wait %d epochs before activation (registered: %d, current: %d)",
			ActivationDelayEpochs, registeredEpoch, currentEpoch)
	}

	validator, err := lm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	currentState := lm.getValidatorState(validator)
	if currentState != StateRegistered && currentState != StatePending {
		return fmt.Errorf("validator %s cannot be activated from state %s", address, currentState)
	}

	if err := lm.validateActivationRequirements(validator); err != nil {
		return fmt.Errorf("activation requirements not met: %v", err)
	}

	if err := lm.manager.ActivateValidator(address); err != nil {
		return fmt.Errorf("activation failed: %v", err)
	}

	lm.recordLifecycleEvent(address, &LifecycleEvent{
		ValidatorAddress: address,
		EventType:        EventActivation,
		FromState:        currentState,
		ToState:          StateActive,
		Timestamp:        time.Now().Unix(),
		BlockHeight:      lm.worldState.GetHeight(),
		Reason:           "Validator activated",
	})

	return nil
}

// ProcessDeactivation handles validator deactivation
func (lm *LifecycleManager) ProcessDeactivation(address string, reason string) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	// 🔴 H-05 FIX: Check consensus lock
	if err := lm.canModifyValidatorSet(); err != nil {
		return err
	}

	validator, err := lm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	currentState := lm.getValidatorState(validator)

	if err := lm.manager.DeactivateValidator(address, reason); err != nil {
		return fmt.Errorf("deactivation failed: %v", err)
	}

	lm.recordLifecycleEvent(address, &LifecycleEvent{
		ValidatorAddress: address,
		EventType:        EventDeactivation,
		FromState:        currentState,
		ToState:          StateInactive,
		Timestamp:        time.Now().Unix(),
		BlockHeight:      lm.worldState.GetHeight(),
		Reason:           reason,
	})

	return nil
}

// ProcessJailing handles validator jailing
func (lm *LifecycleManager) ProcessJailing(address string, reason SlashingReason, duration time.Duration) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	validator, err := lm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	currentState := lm.getValidatorState(validator)

	// Calculate block-based jail duration
	const AvgBlockTime = 3 * time.Second
	blocksToJail := int64(duration / AvgBlockTime)
	if blocksToJail < 1 {
		blocksToJail = 1
	}

	jailUntilBlock := lm.worldState.GetHeight() + blocksToJail

	if err := lm.manager.SlashValidator(address, reason, nil); err != nil {
		return fmt.Errorf("slashing failed: %v", err)
	}

	validator, err = lm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("failed to reload validator after slashing: %v", err)
	}

	validator.JailUntil = jailUntilBlock

	if err := lm.worldState.UpdateValidator(validator); err != nil {
		return fmt.Errorf("failed to update validator jail duration: %v", err)
	}

	lm.recordLifecycleEvent(address, &LifecycleEvent{
		ValidatorAddress: address,
		EventType:        EventJailing,
		FromState:        currentState,
		ToState:          StateJailed,
		Timestamp:        time.Now().Unix(),
		BlockHeight:      lm.worldState.GetHeight(),
		Reason:           fmt.Sprintf("Jailed for %s", reason),
		Data: map[string]interface{}{
			"jail_blocks":      blocksToJail,
			"jail_until_block": jailUntilBlock,
			"slashing_reason":  string(reason),
		},
	})

	lm.scheduleTransition(address, StateActive, "Scheduled unjailing", jailUntilBlock)

	return nil
}

// ProcessUnjailing handles validator unjailing
func (lm *LifecycleManager) ProcessUnjailing(address string) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	validator, err := lm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	currentState := lm.getValidatorState(validator)

	currentBlock := lm.worldState.GetHeight()
	if validator.JailUntil > currentBlock {
		return fmt.Errorf("validator %s is still jailed until block %d (current: %d)",
			address, validator.JailUntil, currentBlock)
	}

	if err := lm.manager.UnjailValidator(address); err != nil {
		return fmt.Errorf("unjailing failed: %v", err)
	}

	if err := lm.validateActivationRequirements(validator); err == nil {
		lm.manager.ActivateValidator(address)
	}

	lm.recordLifecycleEvent(address, &LifecycleEvent{
		ValidatorAddress: address,
		EventType:        EventUnjailing,
		FromState:        currentState,
		ToState:          StateActive,
		Timestamp:        time.Now().Unix(),
		BlockHeight:      lm.worldState.GetHeight(),
		Reason:           "Validator unjailed",
	})

	return nil
}

// ProcessStakeChange handles stake updates
// 🔴 H-05 FIX: Uses unbonding for decreases
func (lm *LifecycleManager) ProcessStakeChange(address string, oldStake, newStake string) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	validator, err := lm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	currentState := lm.getValidatorState(validator)

	oldStakeBig, _ := new(big.Int).SetString(oldStake, 10)
	newStakeBig, _ := new(big.Int).SetString(newStake, 10)
	minStakeBig, _ := new(big.Int).SetString(lm.config.Staking.MinValidatorStake, 10)

	if oldStakeBig == nil {
		oldStakeBig = big.NewInt(0)
	}
	if newStakeBig == nil {
		newStakeBig = big.NewInt(0)
	}
	if minStakeBig == nil {
		minStakeBig = big.NewInt(0)
	}

	stakeDelta := new(big.Int).Sub(newStakeBig, oldStakeBig)

	var eventType LifecycleEventType
	var reason string

	if stakeDelta.Sign() > 0 {
		// Stake increase - immediate
		eventType = EventStakeIncrease
		reason = fmt.Sprintf("Stake increased by %s", stakeDelta.String())
	} else {
		// 🔴 H-05 FIX: Stake decrease - trigger unbonding
		eventType = EventStakeDecrease
		decreaseAmount := new(big.Int).Neg(stakeDelta)
		reason = fmt.Sprintf("Stake decreased by %s (unbonding)", decreaseAmount.String())

		if err := lm.startUnbonding(address, decreaseAmount.String()); err != nil {
			return fmt.Errorf("failed to start unbonding: %v", err)
		}
	}

	// Check thresholds
	if newStakeBig.Cmp(minStakeBig) < 0 {
		if currentState == StateActive {
			lm.mu.Unlock()
			lm.ProcessDeactivation(address, "Stake below minimum threshold")
			lm.mu.Lock()
		}
	}

	lm.recordLifecycleEvent(address, &LifecycleEvent{
		ValidatorAddress: address,
		EventType:        eventType,
		FromState:        currentState,
		ToState:          currentState,
		Timestamp:        time.Now().Unix(),
		BlockHeight:      lm.worldState.GetHeight(),
		Reason:           reason,
		Data: map[string]interface{}{
			"old_stake": oldStake,
			"new_stake": newStake,
			"delta":     stakeDelta.String(),
		},
	})

	return nil
}

// 🔴 H-05 FIX: startUnbonding initiates stake unbonding
func (lm *LifecycleManager) startUnbonding(address string, amount string) error {
	validator, err := lm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	completionBlock := lm.worldState.GetHeight() + lm.unbondingPeriod

	unbonding := &UnbondingEntry{
		ValidatorAddress: address,
		Amount:           amount,
		CompletionBlock:  completionBlock,
		CreatedAt:        lm.worldState.GetHeight(),
	}

	key := fmt.Sprintf("%s-%d", address, lm.worldState.GetHeight())
	lm.unbondingValidators[key] = unbonding

	// Immediately reduce voting power
	stakeBig := math.ParseBigInt(validator.Stake)
	decreaseBig, _ := new(big.Int).SetString(amount, 10)
	if decreaseBig == nil {
		return fmt.Errorf("invalid decrease amount")
	}

	newStake := new(big.Int).Sub(stakeBig, decreaseBig)
	if newStake.Sign() < 0 {
		return fmt.Errorf("cannot decrease stake below zero")
	}

	validator.Stake = newStake.Bytes()

	if err := lm.worldState.UpdateValidator(validator); err != nil {
		return fmt.Errorf("failed to update validator stake: %v", err)
	}

	return nil
}

// 🔴 H-05 FIX: processCompletedUnbonding returns stake after unbonding period
func (lm *LifecycleManager) processCompletedUnbonding() {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	currentBlock := lm.worldState.GetHeight()

	for key, unbonding := range lm.unbondingValidators {
		if unbonding.CompletionBlock <= currentBlock {
			account, err := lm.worldState.GetAccount(unbonding.ValidatorAddress)
			if err != nil {
				continue
			}

			balanceBig := math.ParseBigInt(account.Balance)
			unbondingBig, _ := new(big.Int).SetString(unbonding.Amount, 10)

			if unbondingBig == nil {
				continue
			}

			newBalance := new(big.Int).Add(balanceBig, unbondingBig)
			account.Balance = newBalance.Bytes()

			lm.worldState.UpdateAccountWithStorage(account)

			lm.recordLifecycleEvent(unbonding.ValidatorAddress, &LifecycleEvent{
				ValidatorAddress: unbonding.ValidatorAddress,
				EventType:        EventUnbondingComplete,
				Timestamp:        time.Now().Unix(),
				BlockHeight:      currentBlock,
				Reason:           fmt.Sprintf("Unbonding of %s completed", unbonding.Amount),
				Data: map[string]interface{}{
					"amount":   unbonding.Amount,
					"duration": currentBlock - unbonding.CreatedAt,
				},
			})

			delete(lm.unbondingValidators, key)
		}
	}
}

// ProcessPerformanceUpdate handles performance-based lifecycle decisions
func (lm *LifecycleManager) ProcessPerformanceUpdate(address string, performanceScore float64) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	validator, err := lm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	currentState := lm.getValidatorState(validator)

	switch currentState {
	case StateActive:
		if performanceScore < lm.deactivationThreshold {
			lm.scheduleTransition(address, StateDeactivating,
				fmt.Sprintf("Poor performance: %.2f", performanceScore),
				lm.worldState.GetHeight()+10)
		}
	case StateInactive, StateRegistered:
		if performanceScore >= lm.activationThreshold {
			lm.scheduleTransition(address, StatePending,
				fmt.Sprintf("Good performance: %.2f", performanceScore),
				lm.worldState.GetHeight()+10)
		}
	}

	lm.recordLifecycleEvent(address, &LifecycleEvent{
		ValidatorAddress: address,
		EventType:        EventPerformanceUpdate,
		FromState:        currentState,
		ToState:          currentState,
		Timestamp:        time.Now().Unix(),
		BlockHeight:      lm.worldState.GetHeight(),
		Reason:           fmt.Sprintf("Performance score updated to %.2f", performanceScore),
		Data:             map[string]interface{}{"performance_score": performanceScore},
	})

	return nil
}

// 🔴 H-05 FIX: checkPercentageChangeLimit enforces max % change per epoch
func (lm *LifecycleManager) checkPercentageChangeLimit() error {
	activeValidators := lm.worldState.GetActiveValidators()
	currentCount := len(activeValidators)

	if currentCount == 0 {
		return nil // Bootstrap case
	}

	currentEpoch := lm.getCurrentEpoch()
	newRegistrations := lm.epochRegistrations[currentEpoch]

	changePercent := (float64(newRegistrations) / float64(currentCount)) * 100
	if changePercent > MaxValidatorChangePercent {
		return fmt.Errorf("validator set change of %.2f%% exceeds maximum %.2f%%",
			changePercent, MaxValidatorChangePercent)
	}

	return nil
}

// 🔴 H-05 FIX: BeginVoting locks validator set during voting
func (lm *LifecycleManager) BeginVoting() error {
	lm.votingLock.Lock()
	defer lm.votingLock.Unlock()

	if lm.votingInProgress {
		return fmt.Errorf("voting already in progress")
	}

	lm.votingInProgress = true
	return nil
}

// 🔴 H-05 FIX: EndVoting unlocks validator set after voting
func (lm *LifecycleManager) EndVoting() error {
	lm.votingLock.Lock()
	defer lm.votingLock.Unlock()

	lm.votingInProgress = false
	return nil
}

// 🔴 H-05 FIX: canModifyValidatorSet checks if modifications are allowed
func (lm *LifecycleManager) canModifyValidatorSet() error {
	lm.votingLock.RLock()
	defer lm.votingLock.RUnlock()

	if lm.votingInProgress {
		return fmt.Errorf("cannot modify validator set during voting")
	}

	return nil
}

// 🔴 H-05 FIX: getCurrentEpoch calculates current epoch from block height
func (lm *LifecycleManager) getCurrentEpoch() int64 {
	return lm.worldState.GetHeight() / SlotsPerEpoch
}

// getValidatorState determines the current state of a validator
func (lm *LifecycleManager) getValidatorState(validator *core.Validator) ValidatorState {
	currentBlock := lm.worldState.GetHeight()

	if validator.JailUntil > currentBlock {
		return StateJailed
	}

	if validator.Active {
		return StateActive
	}

	if math.ParseBigInt(validator.Stake).Cmp(math.ParseBigInt(lm.config.Staking.MinValidatorStake)) < 0 {
		return StateInactive
	}

	return StateRegistered
}

// validateRegistrationRequirements validates requirements for validator registration
func (lm *LifecycleManager) validateRegistrationRequirements(address string, stake string, commission float64, selfDelegation string) error {
	if commission < 0 || commission > 100 {
		return fmt.Errorf("commission must be between 0 and 100")
	}

	stakeBig, ok := new(big.Int).SetString(stake, 10)
	if !ok {
		return fmt.Errorf("invalid stake amount")
	}

	selfDelegationBig, ok := new(big.Int).SetString(selfDelegation, 10)
	if !ok {
		return fmt.Errorf("invalid self-delegation amount")
	}

	minStakeBig, _ := new(big.Int).SetString(lm.config.Staking.MinValidatorStake, 10)
	if minStakeBig == nil {
		minStakeBig = big.NewInt(0)
	}

	if stakeBig.Cmp(minStakeBig) < 0 {
		return fmt.Errorf("stake %s below minimum %s", stake, lm.config.Staking.MinValidatorStake)
	}

	if selfDelegationBig.Sign() <= 0 {
		return fmt.Errorf("self-delegation must be positive")
	}

	if selfDelegationBig.Cmp(stakeBig) > 0 {
		return fmt.Errorf("self-delegation cannot exceed total stake")
	}

	return nil
}

// validateActivationRequirements validates requirements for validator activation
func (lm *LifecycleManager) validateActivationRequirements(validator *core.Validator) error {
	if math.ParseBigInt(validator.Stake).Cmp(math.ParseBigInt(lm.config.Staking.MinValidatorStake)) < 0 {
		return fmt.Errorf("stake below minimum")
	}

	if math.ParseBigInt(validator.SelfStake).Cmp(math.ParseBigInt(lm.config.Staking.MinSelfStake)) < 0 {
		return fmt.Errorf("self-stake below minimum")
	}

	if validator.JailUntil > lm.worldState.GetHeight() {
		return fmt.Errorf("validator is jailed until block %d", validator.JailUntil)
	}

	return nil
}

// scheduleTransition schedules a state transition at a specific block
func (lm *LifecycleManager) scheduleTransition(address string, targetState ValidatorState, reason string, scheduledBlock int64) {
	if scheduledBlock == 0 {
		scheduledBlock = lm.worldState.GetHeight() + 1
	}

	transition := &LifecycleTransition{
		ValidatorAddress: address,
		TargetState:      targetState,
		Reason:           reason,
		ScheduledBlock:   scheduledBlock,
	}

	select {
	case lm.transitionQueue <- transition:
	default:
		fmt.Printf("⚠️ Transition queue full, dropping transition for %s\n", address)
	}
}

// recordLifecycleEvent records a lifecycle event
func (lm *LifecycleManager) recordLifecycleEvent(address string, event *LifecycleEvent) {
	if lm.lifecycleEvents[address] == nil {
		lm.lifecycleEvents[address] = make([]*LifecycleEvent, 0)
	}
	lm.lifecycleEvents[address] = append(lm.lifecycleEvents[address], event)

	if len(lm.lifecycleEvents[address]) > 100 {
		lm.lifecycleEvents[address] = lm.lifecycleEvents[address][1:]
	}
}

// lifecycleWorker processes scheduled transitions and unbonding
func (lm *LifecycleManager) lifecycleWorker() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-lm.stopChan:
			return
		case transition := <-lm.transitionQueue:
			lm.processScheduledTransition(transition)
		case <-ticker.C:
			lm.performPeriodicChecks()
		}
	}
}

// processScheduledTransition processes a scheduled state transition
func (lm *LifecycleManager) processScheduledTransition(transition *LifecycleTransition) {
	currentBlock := lm.worldState.GetHeight()

	if transition.ScheduledBlock > currentBlock {
		go func() {
			time.Sleep(1 * time.Second)
			select {
			case lm.transitionQueue <- transition:
			default:
			}
		}()
		return
	}

	lm.mu.Lock()
	defer lm.mu.Unlock()

	switch transition.TargetState {
	case StatePending:
		validator, err := lm.worldState.GetValidator(transition.ValidatorAddress)
		if err == nil && lm.validateActivationRequirements(validator) == nil {
			lm.mu.Unlock()
			lm.ProcessActivation(transition.ValidatorAddress)
			lm.mu.Lock()
		}
	case StateActive:
		lm.mu.Unlock()
		lm.ProcessUnjailing(transition.ValidatorAddress)
		lm.mu.Lock()
	case StateDeactivating:
		lm.mu.Unlock()
		lm.ProcessDeactivation(transition.ValidatorAddress, transition.Reason)
		lm.mu.Lock()
	}
}

// performPeriodicChecks performs various periodic lifecycle checks
func (lm *LifecycleManager) performPeriodicChecks() {
	// Process completed unbonding
	lm.processCompletedUnbonding()

	// Check for automatic unjailing
	lm.mu.Lock()
	defer lm.mu.Unlock()

	currentBlock := lm.worldState.GetHeight()
	activeValidators := lm.worldState.GetActiveValidators()

	for _, validator := range activeValidators {
		if validator.JailUntil > 0 && validator.JailUntil <= currentBlock {
			lm.mu.Unlock()
			lm.ProcessUnjailing(validator.Address)
			lm.mu.Lock()
		}
	}
}

// GetLifecycleEvents returns lifecycle events for a validator
func (lm *LifecycleManager) GetLifecycleEvents(address string) ([]*LifecycleEvent, error) {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	events, exists := lm.lifecycleEvents[address]
	if !exists {
		return []*LifecycleEvent{}, nil
	}

	eventsCopy := make([]*LifecycleEvent, len(events))
	copy(eventsCopy, events)
	return eventsCopy, nil
}

// GetUnbondingEntries returns unbonding entries for a validator
func (lm *LifecycleManager) GetUnbondingEntries(address string) ([]*UnbondingEntry, error) {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	var entries []*UnbondingEntry
	for _, unbonding := range lm.unbondingValidators {
		if unbonding.ValidatorAddress == address {
			entries = append(entries, unbonding)
		}
	}

	return entries, nil
}
