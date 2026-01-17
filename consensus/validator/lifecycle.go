// consensus/validator/lifecycle.go

package validator

import (
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/state"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
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
)

// LifecycleTransition represents a pending state transition
type LifecycleTransition struct {
	ValidatorAddress string         `json:"validator_address"`
	TargetState      ValidatorState `json:"target_state"`
	Reason           string         `json:"reason"`

	// 🛡️ FIX CK-04: Use Block Height instead of Time
	ScheduledBlock int64 `json:"scheduled_block"`
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

	// Start lifecycle worker
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
func (lm *LifecycleManager) RegisterValidator(
	address string,
	pubkey []byte,
	stake string,
	commission float64,
	selfDelegation string,
) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if err := lm.validateRegistrationRequirements(address, stake, commission, selfDelegation); err != nil {
		return fmt.Errorf("registration validation failed: %v", err)
	}

	if err := lm.manager.RegisterValidator(address, pubkey, stake, commission); err != nil {
		return fmt.Errorf("validator registration failed: %v", err)
	}

	// Record lifecycle event
	lm.recordLifecycleEvent(address, &LifecycleEvent{
		ValidatorAddress: address,
		EventType:        EventRegistration,
		ToState:          StateRegistered,
		Timestamp:        time.Now().Unix(),
		BlockHeight:      lm.worldState.GetHeight(),
		Reason:           "New validator registration",
		Data: map[string]interface{}{
			"stake":      stake,
			"commission": commission,
		},
	})

	// Schedule activation check for next block
	lm.scheduleTransition(address, StatePending, "Pending activation review", lm.worldState.GetHeight()+1)

	return nil
}

// ProcessActivation handles validator activation
func (lm *LifecycleManager) ProcessActivation(address string) error {
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
// ProcessJailing handles validator jailing
func (lm *LifecycleManager) ProcessJailing(address string, reason SlashingReason, duration time.Duration) error {
	// 1. Get current validator state
	validator, err := lm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	currentState := lm.getValidatorState(validator)

	// 2. Calculate Block-Based Jail Duration (CK-04 Fix)
	// Convert time duration to blocks (assuming ~3s per block)
	const AvgBlockTime = 3 * time.Second
	blocksToJail := int64(duration / AvgBlockTime)
	if blocksToJail < 1 {
		blocksToJail = 1
	}

	jailUntilBlock := lm.worldState.GetHeight() + blocksToJail

	// 3. Call SlashValidator (Pass 'nil' for evidence as per signature)
	if err := lm.manager.SlashValidator(address, reason, nil); err != nil {
		return fmt.Errorf("slashing failed: %v", err)
	}

	// 4. FORCE UPDATE: Overwrite the 'JailUntil' field with our Block Height
	// We must reload the validator in case SlashValidator modified it
	validator, err = lm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("failed to reload validator after slashing: %v", err)
	}

	// Set the block height (this field is int64, so it fits)
	validator.JailUntil = jailUntilBlock

	// Save the updated jail time back to WorldState
	if err := lm.worldState.UpdateValidator(validator); err != nil {
		return fmt.Errorf("failed to update validator jail duration: %v", err)
	}

	// 5. Record Event & Schedule Unjailing
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

	// Schedule unjailing check at exact block
	lm.scheduleTransition(address, StateActive, "Scheduled unjailing", jailUntilBlock)

	return nil
}

// ProcessUnjailing handles validator unjailing
func (lm *LifecycleManager) ProcessUnjailing(address string) error {
	validator, err := lm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	currentState := lm.getValidatorState(validator)

	// 🛡️ FIX CK-04: Check Block Height, not Time
	currentBlock := lm.worldState.GetHeight()
	if validator.JailUntil > currentBlock { // Assuming JailUntil now stores BLOCK HEIGHT
		return fmt.Errorf("validator %s is still jailed until block %d (current: %d)", address, validator.JailUntil, currentBlock)
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
func (lm *LifecycleManager) ProcessStakeChange(address string, oldStake, newStake string) error {
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
		eventType = EventStakeIncrease
		reason = fmt.Sprintf("Stake increased by %s", stakeDelta.String())
	} else {
		eventType = EventStakeDecrease
		reason = fmt.Sprintf("Stake decreased by %s", new(big.Int).Neg(stakeDelta).String())
	}

	// Check thresholds
	if newStakeBig.Cmp(minStakeBig) < 0 {
		if currentState == StateActive {
			lm.ProcessDeactivation(address, "Stake below minimum threshold")
		}
	} else {
		if oldStakeBig.Cmp(minStakeBig) < 0 && (currentState == StateInactive || currentState == StateRegistered) {
			lm.scheduleTransition(address, StatePending, "Stake meets minimum threshold", lm.worldState.GetHeight()+1)
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

// ProcessPerformanceUpdate handles performance-based lifecycle decisions
func (lm *LifecycleManager) ProcessPerformanceUpdate(address string, performanceScore float64) error {
	validator, err := lm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	currentState := lm.getValidatorState(validator)

	switch currentState {
	case StateActive:
		if performanceScore < lm.deactivationThreshold {
			lm.scheduleTransition(address, StateDeactivating, fmt.Sprintf("Poor performance: %.2f", performanceScore), lm.worldState.GetHeight()+10)
		}
	case StateInactive, StateRegistered:
		if performanceScore >= lm.activationThreshold {
			lm.scheduleTransition(address, StatePending, fmt.Sprintf("Good performance: %.2f", performanceScore), lm.worldState.GetHeight()+10)
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

// getValidatorState determines the current state of a validator
func (lm *LifecycleManager) getValidatorState(validator *core.Validator) ValidatorState {
	currentBlock := lm.worldState.GetHeight()

	// 🛡️ FIX CK-04: Block Height check
	if validator.JailUntil > currentBlock {
		return StateJailed
	}

	if validator.Active {
		return StateActive
	}

	if validator.Stake < lm.config.Staking.MinValidatorStake {
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
	if validator.Stake < lm.config.Staking.MinValidatorStake {
		return fmt.Errorf("stake below minimum")
	}

	if validator.SelfStake < lm.config.Staking.MinSelfStake {
		return fmt.Errorf("self-stake below minimum")
	}

	// 🛡️ FIX CK-04: Block check
	if validator.JailUntil > lm.worldState.GetHeight() {
		return fmt.Errorf("validator is jailed until block %d", validator.JailUntil)
	}

	return nil
}

// scheduleTransition schedules a state transition at a specific BLOCK
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
		fmt.Printf("Transition queue full, dropping transition for %s\n", address)
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

// lifecycleWorker processes scheduled transitions
func (lm *LifecycleManager) lifecycleWorker() {
	ticker := time.NewTicker(3 * time.Second) // Check every block (~3s)
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

	// 🛡️ FIX CK-04: Wait for Block Height
	if transition.ScheduledBlock > currentBlock {
		// Not time yet, push back to queue (or hold in separate wait list)
		// For simplicity, we re-queue with a small sleep to avoid tight loop
		go func() {
			time.Sleep(1 * time.Second)
			lm.transitionQueue <- transition
		}()
		return
	}

	switch transition.TargetState {
	case StatePending:
		validator, err := lm.worldState.GetValidator(transition.ValidatorAddress)
		if err == nil && lm.validateActivationRequirements(validator) == nil {
			lm.ProcessActivation(transition.ValidatorAddress)
		}
	case StateActive:
		lm.ProcessUnjailing(transition.ValidatorAddress)
	case StateDeactivating:
		lm.ProcessDeactivation(transition.ValidatorAddress, transition.Reason)
	}
}

// performPeriodicChecks performs various periodic lifecycle checks
func (lm *LifecycleManager) performPeriodicChecks() {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	activeValidators := lm.worldState.GetActiveValidators()
	currentBlock := lm.worldState.GetHeight()

	for _, validator := range activeValidators {
		// Check for automatic unjailing based on BLOCK HEIGHT
		if validator.JailUntil > 0 && validator.JailUntil <= currentBlock {
			lm.ProcessUnjailing(validator.Address)
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
