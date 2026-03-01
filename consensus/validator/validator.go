// consensus/validator/validator.go

// Validator management for Proof of Stake consensus
// Features:
// - Validator lifecycle management (registration, activation, jailing, slashing)
// - Stake management with delegation support
// - Performance tracking and slashing conditions
// - Validator set management with dynamic updates
// - Commission handling and reward distribution
// - Jail and unjail mechanisms for misbehaving validators

package validator

import (
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/math"
	"github.com/thrylos-labs/go-thrylos/core/security"
	"github.com/thrylos-labs/go-thrylos/core/state"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

const (
	// Default slashing enforcement values
	DefaultMaxSlashingEvents    = 3
	DefaultMinStakeRetention    = 0.5 // 50%
	DefaultAutoRemoveDoubleSign = true
	defaultUnbondingBlockTime   = 3 * time.Second
	commissionUpdateInterval    = 24 * time.Hour
	unbondingQueueMetadataKey   = "validator/unbonding_queue"
)

// Manager handles validator operations
type Manager struct {
	config     *config.Config
	worldState *state.WorldState
	mu         sync.RWMutex

	// Validator tracking
	validatorMetrics map[string]*ValidatorMetrics

	// Slashing tracking
	slashingEvents map[string][]*SlashingEvent

	unbondingQueue map[string][]*UnbondingEntry // key: delegatorAddr

	commissionUpdateTimes map[string]int64

	// Performance tracking
	performanceWindow int64 // Number of blocks to track for performance
}

// ValidatorMetrics tracks validator performance
type ValidatorMetrics struct {
	Address            string      `json:"address"`
	BlocksProposed     uint64      `json:"blocks_proposed"`
	BlocksMissed       uint64      `json:"blocks_missed"`
	AttestationsMade   uint64      `json:"attestations_made"`
	AttestationsMissed uint64      `json:"attestations_missed"`
	LastActivity       int64       `json:"last_activity"`
	UptimePercentage   float64     `json:"uptime_percentage"`
	SlashCount         int         `json:"slash_count"`
	TotalSlashed       string      `json:"total_slashed"`
	JailHistory        []JailEvent `json:"jail_history"`
}

// SlashingEvent represents a slashing incident
type SlashingEvent struct {
	ValidatorAddress string         `json:"validator_address"`
	Reason           SlashingReason `json:"reason"`

	// ✅ Change int64 -> string
	Amount string `json:"amount"`

	BlockHeight int64  `json:"block_height"`
	Timestamp   int64  `json:"timestamp"`
	Evidence    []byte `json:"evidence"`
}

// SlashingReason represents why a validator was slashed
type SlashingReason string

const (
	SlashingDoubleSign   SlashingReason = "double_sign"
	SlashingDowntime     SlashingReason = "downtime"
	SlashingInvalidBlock SlashingReason = "invalid_block"
)

// JailEvent represents a jailing incident
type JailEvent struct {
	Reason     string `json:"reason"`
	JailTime   int64  `json:"jail_time"`
	UnjailTime int64  `json:"unjail_time"`
	Duration   int64  `json:"duration"`
}

// NewManager creates a new validator manager
func NewManager(config *config.Config, worldState *state.WorldState) *Manager {
	vm := &Manager{
		config:           config,
		worldState:       worldState,
		validatorMetrics: make(map[string]*ValidatorMetrics),
		slashingEvents:   make(map[string][]*SlashingEvent),

		unbondingQueue:        make(map[string][]*UnbondingEntry),
		commissionUpdateTimes: make(map[string]int64),

		performanceWindow: config.Staking.SignedBlocksWindow,
	}

	if err := vm.loadUnbondingQueue(); err != nil {
		log.Printf("⚠️ Failed to load persisted unbonding queue: %v", err)
	}

	return vm
}

// RemoveValidator permanently removes a validator from the active set
// RemoveValidator permanently removes a validator from the active set
func (vm *Manager) RemoveValidator(address string, reason string) error {
	validator, err := vm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %w", err)
	}

	// Mark validator as inactive and jail permanently
	validator.Active = false
	validator.JailUntil = time.Now().Add(365 * 24 * time.Hour).Unix()

	// Update validator in world state
	if err := vm.worldState.SetValidator(address, validator); err != nil {
		return fmt.Errorf("failed to update validator: %w", err)
	}

	// Log removal
	log.Printf("🚨 VALIDATOR REMOVED: %s (reason: %s)", address, reason)

	return nil
}

// BeginUnbonding initiates unbonding process
func (vm *Manager) BeginUnbonding(validatorAddr, delegatorAddr string, amount string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	return vm.queueUnbondingLocked(validatorAddr, delegatorAddr, amount)
}

// ProcessUnbondings processes completed unbonding entries
func (vm *Manager) ProcessUnbondings() error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	currentBlock := vm.worldState.GetHeight()

	for delegatorAddr, entries := range vm.unbondingQueue {
		for i := len(entries) - 1; i >= 0; i-- {
			entry := entries[i]

			// Check if unbonding period completed
			if currentBlock >= entry.CompletionBlock {
				// Complete the unbonding
				validator, err := vm.worldState.GetValidator(entry.ValidatorAddress)
				if err != nil {
					continue
				}

				amountBig := math.ParseBigInt(entry.Amount)

				// Remove delegation
				currentBig := math.ParseBigInt(validator.Delegators[delegatorAddr])
				currentBig = math.Sub(currentBig, amountBig)

				if currentBig.Sign() == 0 {
					delete(validator.Delegators, delegatorAddr)
				} else {
					validator.Delegators[delegatorAddr] = currentBig.Bytes()
				}

				// Update totals
				delegatedBig := math.ParseBigInt(validator.DelegatedStake)
				delegatedBig = math.Sub(delegatedBig, amountBig)
				validator.DelegatedStake = delegatedBig.Bytes()

				stakeBig := math.ParseBigInt(validator.Stake)
				stakeBig = math.Sub(stakeBig, amountBig)
				validator.Stake = stakeBig.Bytes()

				validator.UpdatedAt = time.Now().Unix()

				// Check minimum stake
				minStakeBig := math.ParseBigInt(vm.config.Staking.MinValidatorStake)
				if validator.Active && math.Cmp(stakeBig, minStakeBig) < 0 {
					validator.Active = false
				}

				vm.worldState.UpdateValidator(validator)

				// Return principal directly to spendable balance.
				delegator, err := vm.worldState.GetAccount(delegatorAddr)
				if err != nil {
					continue
				}

				balanceBig := math.ParseBigInt(delegator.Balance)
				balanceBig = math.Add(balanceBig, amountBig)
				delegator.Balance = balanceBig.Bytes()

				if err := vm.worldState.GetAccountManager().UpdateAccount(delegator); err != nil {
					continue
				}

				// Remove from queue
				entries = append(entries[:i], entries[i+1:]...)
			}
		}

		if len(entries) == 0 {
			delete(vm.unbondingQueue, delegatorAddr)
		} else {
			vm.unbondingQueue[delegatorAddr] = entries
		}
	}

	if err := vm.persistUnbondingQueueLocked(); err != nil {
		return fmt.Errorf("failed to persist unbonding queue: %w", err)
	}

	return nil
}

// RegisterValidator registers a new validator
// ✅ UPDATE: stake changed from int64 -> string
func (vm *Manager) RegisterValidator(
	address string,
	pubkey []byte,
	stake string,
	commission float64,
) error {
	return vm.RegisterValidatorWithDomain(address, pubkey, stake, commission, "")
}

// RegisterValidatorWithDomain registers a validator and links it to an ownership domain.
func (vm *Manager) RegisterValidatorWithDomain(
	address string,
	pubkey []byte,
	stake string,
	commission float64,
	stakeDomain string,
) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	// Validate inputs
	if err := vm.validateAddress(address); err != nil {
		return fmt.Errorf("invalid validator address: %v", err)
	}

	if len(pubkey) == 0 {
		return fmt.Errorf("public key cannot be empty")
	}

	// 1. Validate Stake Amount (String Comparison)
	stakeBig := math.ParseBigInt(stake)
	minStakeBig := math.ParseBigInt(vm.config.Staking.MinValidatorStake)
	maxStakeBig := math.ParseBigInt(vm.config.Staking.MaxValidatorStake)

	// Compare: if stake < minStake
	if stakeBig.Cmp(minStakeBig) < 0 {
		return fmt.Errorf("stake %s below minimum %s",
			stake, vm.config.Staking.MinValidatorStake)
	}

	if maxStakeBig.Sign() > 0 && stakeBig.Cmp(maxStakeBig) > 0 {
		return fmt.Errorf("stake %s exceeds maximum %s", stake, vm.config.Staking.MaxValidatorStake)
	}

	totalNetworkStake := vm.worldState.GetTotalStaked()
	if totalNetworkStake != nil && totalNetworkStake.Sign() > 0 && vm.config.Staking.MaxStakePercentage > 0 {
		postRegistrationStake := math.Add(totalNetworkStake, stakeBig)
		stakeFraction := new(big.Float).Quo(new(big.Float).SetInt(stakeBig), new(big.Float).SetInt(postRegistrationStake))
		stakePercentage, _ := stakeFraction.Float64()
		if stakePercentage > vm.config.Staking.MaxStakePercentage {
			return fmt.Errorf("validator stake would exceed concentration limit: %.2f%% > %.2f%%",
				stakePercentage*100, vm.config.Staking.MaxStakePercentage*100)
		}
	}

	if vm.config.Governance.OwnershipDomainsEnabled &&
		vm.config.Staking.MaxStakePercentage > 0 &&
		totalNetworkStake != nil &&
		totalNetworkStake.Sign() > 0 {
		domainID := address
		if stakeDomain != "" {
			domainID = stakeDomain
		}

		currentDomainStake, err := vm.worldState.GetStakeDomainTotalStake(domainID)
		if err != nil {
			return fmt.Errorf("failed to load stake domain %s: %w", domainID, err)
		}

		postRegistrationStake := math.Add(totalNetworkStake, stakeBig)
		postDomainStake := math.Add(currentDomainStake, stakeBig)
		if postRegistrationStake.Sign() > 0 {
			stakeFraction := new(big.Float).Quo(new(big.Float).SetInt(postDomainStake), new(big.Float).SetInt(postRegistrationStake))
			stakePercentage, _ := stakeFraction.Float64()
			if stakePercentage > vm.config.Staking.MaxStakePercentage {
				return fmt.Errorf("stake domain would exceed concentration limit: %.2f%% > %.2f%%",
					stakePercentage*100, vm.config.Staking.MaxStakePercentage*100)
			}
		}
	}

	if commission < 0 || commission > vm.config.Staking.MaxCommission {
		return fmt.Errorf("commission %.4f outside valid range [0, %.4f]",
			commission, vm.config.Staking.MaxCommission)
	}

	// Check if validator already exists
	if _, err := vm.worldState.GetValidator(address); err == nil {
		return fmt.Errorf("validator %s already exists", address)
	}

	// Create validator
	validator := &core.Validator{
		Address: address,
		Pubkey:  pubkey,

		// ✅ Fix: Assign string directly
		Stake:     math.ParseBigInt(stake).Bytes(),
		SelfStake: math.ParseBigInt(stake).Bytes(), // Initially all stake is self-stake

		// ✅ Fix: Use "0" string instead of int 0
		DelegatedStake: nil,

		// ✅ Fix: Initialize map as map[string]string
		Delegators: make(map[string][]byte),

		Commission:     commission,
		Active:         false, // Not active until meeting requirements
		BlocksProposed: 0,
		BlocksMissed:   0,
		JailUntil:      0,
		CreatedAt:      time.Now().Unix(),
		UpdatedAt:      time.Now().Unix(),
	}

	// Add to world state
	if err := vm.worldState.AddValidatorWithStakeDomain(validator, stakeDomain); err != nil {
		return fmt.Errorf("failed to add validator to world state: %v", err)
	}

	// Initialize metrics
	vm.validatorMetrics[address] = &ValidatorMetrics{
		Address:      address,
		LastActivity: time.Now().Unix(),
		JailHistory:  make([]JailEvent, 0),
	}

	return nil
}

// LinkValidatorToStakeDomain updates the ownership domain used for concentration checks and governance.
func (vm *Manager) LinkValidatorToStakeDomain(address, stakeDomain string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if _, err := vm.worldState.GetValidator(address); err != nil {
		return fmt.Errorf("validator not found: %w", err)
	}

	return vm.worldState.SetValidatorStakeDomain(address, stakeDomain)
}

// validateAddress validates a validator address
func (vm *Manager) validateAddress(address string) error {
	// Use account.ValidateAddress if available, otherwise basic validation
	if err := account.ValidateAddress(address); err != nil {
		return err
	}
	return nil
}

// ActivateValidator activates a validator if it meets requirements
func (vm *Manager) ActivateValidator(address string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	validator, err := vm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	// ✅ FIX: Check minimum stake requirement with BigInt comparison
	stakeBig := math.ParseBigInt(validator.Stake)
	minStakeBig := math.ParseBigInt(vm.config.Staking.MinValidatorStake)

	if stakeBig.Cmp(minStakeBig) < 0 {
		return fmt.Errorf("validator stake %s below minimum %s",
			validator.Stake, vm.config.Staking.MinValidatorStake)
	}

	// ✅ FIX: Check minimum self-stake requirement with BigInt comparison
	selfStakeBig := math.ParseBigInt(validator.SelfStake)
	minSelfStakeBig := math.ParseBigInt(vm.config.Staking.MinSelfStake)

	if selfStakeBig.Cmp(minSelfStakeBig) < 0 {
		return fmt.Errorf("validator self-stake %s below minimum %s",
			validator.SelfStake, vm.config.Staking.MinSelfStake)
	}

	// Check if jailed
	if vm.isJailed(validator) {
		return fmt.Errorf("validator is jailed until %d", validator.JailUntil)
	}

	// Activate validator
	validator.Active = true
	validator.UpdatedAt = time.Now().Unix()

	return vm.worldState.UpdateValidator(validator)
}

// DeactivateValidator deactivates a validator
func (vm *Manager) DeactivateValidator(address string, reason string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	validator, err := vm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	validator.Active = false
	validator.UpdatedAt = time.Now().Unix()

	return vm.worldState.UpdateValidator(validator)
}

// SlashValidator slashes a validator for misbehavior
func (vm *Manager) SlashValidator(
	address string,
	reason SlashingReason,
	evidence []byte,
) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	validator, err := vm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	// ✅ ISSUE #7 FIX: Check if validator is already inactive
	if !validator.Active {
		log.Printf("⚠️ Warning: Attempted to slash inactive validator %s (reason: %v)", address, reason)
		return fmt.Errorf("cannot slash validator %s: validator is not active", address)
	}

	// Get config values (with defaults if not set)
	maxEvents := DefaultMaxSlashingEvents
	minRetention := DefaultMinStakeRetention
	autoRemoveDoubleSign := DefaultAutoRemoveDoubleSign

	if vm.config != nil && vm.config.Staking.MaxSlashingEvents > 0 {
		maxEvents = vm.config.Staking.MaxSlashingEvents
	}
	if vm.config != nil && vm.config.Staking.MinStakeRetention > 0 {
		minRetention = vm.config.Staking.MinStakeRetention
	}
	if vm.config != nil {
		autoRemoveDoubleSign = vm.config.Staking.AutoRemoveOnDoubleSign
	}

	// Count total slashing events for this validator (before adding new one)
	slashCount := len(vm.slashingEvents[address])

	// Calculate slash amount based on reason
	var slashFraction float64
	var jailDuration time.Duration

	switch reason {
	case SlashingDoubleSign:
		slashFraction = vm.config.Staking.SlashFractionDoubleSign
		jailDuration = 30 * 24 * time.Hour
	case SlashingDowntime:
		slashFraction = vm.config.Staking.SlashFractionDowntime
		// ✅ Graduated jailing based on offense count
		switch slashCount {
		case 0:
			jailDuration = 24 * time.Hour // First offense: 1 day
		case 1:
			jailDuration = 7 * 24 * time.Hour // Second offense: 1 week
		default:
			jailDuration = 30 * 24 * time.Hour // Third+ offense: 30 days
		}
	case SlashingInvalidBlock:
		slashFraction = 0.01 // 1%
		jailDuration = 24 * time.Hour
	default:
		return fmt.Errorf("unknown slashing reason: %s", reason)
	}

	// 1. Parse Stake to BigInt
	stakeBig := math.ParseBigInt(validator.Stake)

	// 2. Calculate Slash Amount: (Stake * Fraction)
	fStake := new(big.Float).SetInt(stakeBig)
	fFraction := big.NewFloat(slashFraction)
	fSlashResult := new(big.Float).Mul(fStake, fFraction)
	slashAmountBig, _ := fSlashResult.Int(nil)

	// Ensure minimum slash of 1 Wei if fraction > 0 but result is 0
	if slashFraction > 0 && slashAmountBig.Sign() == 0 {
		slashAmountBig = big.NewInt(1)
	}

	if err := vm.applySlashToDelegatedStake(validator, slashAmountBig); err != nil {
		return fmt.Errorf("failed to apportion slash across bonded stake: %v", err)
	}

	// 3. Apply Slashing: Stake - SlashAmount
	stakeBig.Sub(stakeBig, slashAmountBig)

	// Clamp to 0 if negative
	if stakeBig.Sign() < 0 {
		stakeBig.SetInt64(0)
	}

	// 4. Update Validator State
	validator.Stake = stakeBig.Bytes()
	validator.JailUntil = time.Now().Add(jailDuration).Unix()
	validator.Active = false
	validator.UpdatedAt = time.Now().Unix()

	// 5. Record slashing event
	slashingEvent := &SlashingEvent{
		ValidatorAddress: address,
		Reason:           reason,
		Amount:           slashAmountBig.String(),
		BlockHeight:      vm.worldState.GetHeight(),
		Timestamp:        time.Now().Unix(),
		Evidence:         evidence,
	}

	if vm.slashingEvents[address] == nil {
		vm.slashingEvents[address] = make([]*SlashingEvent, 0)
	}
	vm.slashingEvents[address] = append(vm.slashingEvents[address], slashingEvent)

	// Log the slashing event for security audit
	security.LogSlashing(address, string(reason), slashAmountBig.String())

	// 6. Update metrics
	if metrics, exists := vm.validatorMetrics[address]; exists {
		metrics.SlashCount++
		currentSlashed := math.ParseBigInt(metrics.TotalSlashed)
		currentSlashed.Add(currentSlashed, slashAmountBig)
		metrics.TotalSlashed = currentSlashed.String()

		metrics.JailHistory = append(metrics.JailHistory, JailEvent{
			Reason:   string(reason),
			JailTime: validator.JailUntil,
			Duration: int64(jailDuration.Seconds()),
		})
	}

	// ============================================================================
	// ENFORCEMENT RULES (using slashCount + 1 for new total)
	// ============================================================================
	newSlashCount := slashCount + 1 // Include the event we just added

	// ENFORCEMENT RULE 1: Double-signing → Immediate removal
	if reason == SlashingDoubleSign && autoRemoveDoubleSign {
		if err := vm.RemoveValidator(address, "double_sign_detected"); err != nil {
			log.Printf("⚠️ Failed to remove double-signer: %v", err)
			// Still update the validator even if removal fails
			vm.worldState.UpdateValidator(validator)
		} else {
			log.Printf("🚨 VALIDATOR REMOVED: %s (double-signing)", address)
		}
		return nil // Exit after removal attempt
	}

	// ENFORCEMENT RULE 2: Three Strikes → Permanent removal
	if newSlashCount >= maxEvents {
		if err := vm.RemoveValidator(address, fmt.Sprintf("exceeded_max_events_%d", maxEvents)); err != nil {
			log.Printf("⚠️ Failed to remove repeat offender: %v", err)
			vm.worldState.UpdateValidator(validator)
		} else {
			log.Printf("🚨 VALIDATOR REMOVED: %s (%d slashing events)", address, newSlashCount)
		}
		return nil
	}

	// ENFORCEMENT RULE 3: Stake below safety threshold → Remove
	minStake := math.ParseBigInt(vm.config.Staking.MinValidatorStake)
	safetyThreshold := new(big.Int).Mul(minStake, big.NewInt(int64(minRetention*100)))
	safetyThreshold.Div(safetyThreshold, big.NewInt(100))

	if stakeBig.Cmp(safetyThreshold) < 0 {
		if err := vm.RemoveValidator(address, "stake_below_threshold"); err != nil {
			log.Printf("⚠️ Failed to remove underfunded validator: %v", err)
			vm.worldState.UpdateValidator(validator)
		} else {
			log.Printf("🚨 VALIDATOR REMOVED: %s (insufficient stake after slashing)", address)
		}
		return nil
	}

	// ENFORCEMENT RULE 4: If not removed, update validator normally
	if err := vm.worldState.UpdateValidator(validator); err != nil {
		return fmt.Errorf("failed to update slashed validator: %v", err)
	}

	log.Printf("⚙️ VALIDATOR SLASHED: %s (reason: %s, count: %d, jail: %v)",
		address, reason, newSlashCount, jailDuration)

	return nil
}

// UnjailValidator unjails a validator after serving jail time
func (vm *Manager) UnjailValidator(address string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	validator, err := vm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	// Check if still jailed
	if !vm.isJailed(validator) {
		return fmt.Errorf("validator is not jailed")
	}

	// Check if jail time has passed
	if time.Now().Unix() < validator.JailUntil {
		return fmt.Errorf("validator jail time has not expired")
	}

	// ✅ FIX: Check if validator still meets minimum requirements with BigInt comparison
	stakeBig := math.ParseBigInt(validator.Stake)
	minStakeBig := math.ParseBigInt(vm.config.Staking.MinValidatorStake)

	if stakeBig.Cmp(minStakeBig) < 0 {
		return fmt.Errorf("validator stake %s below minimum %s after slashing",
			validator.Stake, vm.config.Staking.MinValidatorStake)
	}

	// Unjail validator
	validator.JailUntil = 0
	validator.UpdatedAt = time.Now().Unix()

	// Update jail history
	if metrics, exists := vm.validatorMetrics[address]; exists {
		if len(metrics.JailHistory) > 0 {
			lastJail := &metrics.JailHistory[len(metrics.JailHistory)-1]
			lastJail.UnjailTime = time.Now().Unix()
		}
	}

	return vm.worldState.UpdateValidator(validator)
}

// UpdateValidatorCommission updates a validator's commission rate
func (vm *Manager) UpdateValidatorCommission(address string, newCommission float64) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	// Validate commission rate
	if newCommission < 0 || newCommission > vm.config.Staking.MaxCommission {
		return fmt.Errorf("commission %.4f outside valid range [0, %.4f]",
			newCommission, vm.config.Staking.MaxCommission)
	}

	validator, err := vm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	// Check commission change rate limit
	commissionChange := newCommission - validator.Commission
	if commissionChange < 0 {
		commissionChange = -commissionChange
	}

	if commissionChange > vm.config.Staking.CommissionChangeMax {
		return fmt.Errorf("commission change %.4f exceeds maximum daily change %.4f",
			commissionChange, vm.config.Staking.CommissionChangeMax)
	}

	if lastUpdate, exists := vm.commissionUpdateTimes[address]; exists {
		if time.Now().Unix()-lastUpdate < int64(commissionUpdateInterval.Seconds()) {
			return fmt.Errorf("commission can only be updated once every %s", commissionUpdateInterval)
		}
	} else if validator.CreatedAt > 0 && time.Now().Unix()-validator.CreatedAt < int64(commissionUpdateInterval.Seconds()) {
		return fmt.Errorf("commission can only be updated once every %s", commissionUpdateInterval)
	}

	// Update commission
	validator.Commission = newCommission
	validator.UpdatedAt = time.Now().Unix()
	vm.commissionUpdateTimes[address] = validator.UpdatedAt

	return vm.worldState.UpdateValidator(validator)
}

func (vm *Manager) applySlashToDelegatedStake(validator *core.Validator, slashAmount *big.Int) error {
	if slashAmount == nil || slashAmount.Sign() <= 0 {
		return nil
	}

	totalStake := math.ParseBigInt(validator.Stake)
	if totalStake.Sign() <= 0 {
		return nil
	}

	selfStake := math.ParseBigInt(validator.SelfStake)
	if selfStake.Sign() < 0 {
		selfStake = big.NewInt(0)
	}

	delegatedStake := math.ParseBigInt(validator.DelegatedStake)
	if delegatedStake.Sign() < 0 {
		delegatedStake = big.NewInt(0)
	}

	delegatedSlash := big.NewInt(0)
	if delegatedStake.Sign() > 0 {
		delegatedSlash.Mul(slashAmount, delegatedStake)
		delegatedSlash.Div(delegatedSlash, totalStake)
		if delegatedSlash.Cmp(delegatedStake) > 0 {
			delegatedSlash.Set(delegatedStake)
		}
	}

	selfSlash := new(big.Int).Sub(slashAmount, delegatedSlash)
	if selfSlash.Cmp(selfStake) > 0 {
		selfSlash.Set(selfStake)
	}

	selfStake.Sub(selfStake, selfSlash)
	delegatedStake.Sub(delegatedStake, delegatedSlash)

	validator.SelfStake = selfStake.Bytes()
	validator.DelegatedStake = delegatedStake.Bytes()

	if delegatedSlash.Sign() == 0 || len(validator.Delegators) == 0 {
		return nil
	}

	type delegatorShare struct {
		address string
		amount  *big.Int
	}

	addresses := make([]string, 0, len(validator.Delegators))
	for addr := range validator.Delegators {
		addresses = append(addresses, addr)
	}
	sort.Strings(addresses)

	originalDelegatedStake := math.ParseBigInt(validator.DelegatedStake)
	originalDelegatedStake.Add(originalDelegatedStake, delegatedSlash)
	remainingSlash := new(big.Int).Set(delegatedSlash)
	remainingStake := new(big.Int).Set(originalDelegatedStake)

	for idx, addr := range addresses {
		amount := math.ParseBigInt(validator.Delegators[addr])
		if amount.Sign() <= 0 {
			delete(validator.Delegators, addr)
			continue
		}

		shareSlash := big.NewInt(0)
		if remainingSlash.Sign() > 0 {
			if idx == len(addresses)-1 || remainingStake.Cmp(amount) == 0 {
				shareSlash.Set(remainingSlash)
			} else {
				shareSlash.Mul(remainingSlash, amount)
				shareSlash.Div(shareSlash, remainingStake)
			}
			if shareSlash.Cmp(amount) > 0 {
				shareSlash.Set(amount)
			}
		}

		newAmount := new(big.Int).Sub(amount, shareSlash)
		if newAmount.Sign() <= 0 {
			delete(validator.Delegators, addr)
			newAmount = big.NewInt(0)
		} else {
			validator.Delegators[addr] = newAmount.Bytes()
		}

		vm.applySlashToPendingUnbondings(addr, validator.Address, amount, shareSlash)

		remainingSlash.Sub(remainingSlash, shareSlash)
		remainingStake.Sub(remainingStake, amount)
	}

	return nil
}

func (vm *Manager) applySlashToPendingUnbondings(delegatorAddr, validatorAddr string, totalDelegation, delegatorSlash *big.Int) {
	if delegatorSlash == nil || delegatorSlash.Sign() <= 0 {
		return
	}
	if totalDelegation == nil || totalDelegation.Sign() <= 0 {
		return
	}

	entries := vm.unbondingQueue[delegatorAddr]
	if len(entries) == 0 {
		return
	}

	pendingIndexes := make([]int, 0, len(entries))
	pendingTotal := big.NewInt(0)
	for idx, entry := range entries {
		if entry.ValidatorAddress != validatorAddr {
			continue
		}
		entryAmount := math.ParseBigInt(entry.Amount)
		if entryAmount.Sign() <= 0 {
			continue
		}
		pendingIndexes = append(pendingIndexes, idx)
		pendingTotal.Add(pendingTotal, entryAmount)
	}

	if len(pendingIndexes) == 0 || pendingTotal.Sign() == 0 {
		return
	}

	remainingSlash := new(big.Int).Set(delegatorSlash)
	remainingBase := new(big.Int).Set(totalDelegation)

	for pos, idx := range pendingIndexes {
		originalEntryAmount := math.ParseBigInt(entries[idx].Amount)
		if originalEntryAmount.Sign() <= 0 {
			continue
		}

		entrySlash := big.NewInt(0)
		if remainingSlash.Sign() > 0 && remainingBase.Sign() > 0 {
			if pos == len(pendingIndexes)-1 || remainingBase.Cmp(originalEntryAmount) == 0 {
				entrySlash.Mul(remainingSlash, originalEntryAmount)
				entrySlash.Div(entrySlash, remainingBase)
			} else {
				entrySlash.Mul(remainingSlash, originalEntryAmount)
				entrySlash.Div(entrySlash, remainingBase)
			}
			if entrySlash.Cmp(originalEntryAmount) > 0 {
				entrySlash.Set(originalEntryAmount)
			}
		}

		entryAmount := new(big.Int).Sub(originalEntryAmount, entrySlash)
		entries[idx].Amount = entryAmount.String()

		remainingSlash.Sub(remainingSlash, entrySlash)
		remainingBase.Sub(remainingBase, originalEntryAmount)
	}
}

// RecordBlockProposal records that a validator proposed a block
func (vm *Manager) RecordBlockProposal(address string, success bool) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	validator, err := vm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	// Update validator stats
	if success {
		validator.BlocksProposed++
	} else {
		validator.BlocksMissed++
	}
	validator.UpdatedAt = time.Now().Unix()

	// Update metrics
	if metrics, exists := vm.validatorMetrics[address]; exists {
		if success {
			metrics.BlocksProposed++
		} else {
			metrics.BlocksMissed++
		}
		metrics.LastActivity = time.Now().Unix()
		vm.updateUptimePercentage(metrics)
	}

	// Check for downtime slashing
	if !success {
		if err := vm.checkDowntimeSlashing(address); err != nil {
			return fmt.Errorf("downtime check failed: %v", err)
		}
	}

	return vm.worldState.UpdateValidator(validator)
}

// RecordAttestation records that a validator made an attestation
func (vm *Manager) RecordAttestation(address string, success bool) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if metrics, exists := vm.validatorMetrics[address]; exists {
		if success {
			metrics.AttestationsMade++
		} else {
			metrics.AttestationsMissed++
		}
		metrics.LastActivity = time.Now().Unix()
		vm.updateUptimePercentage(metrics)
	}

	return nil
}

// checkDowntimeSlashing checks if a validator should be slashed for downtime
// checkDowntimeSlashing checks if a validator should be slashed for downtime
func (vm *Manager) checkDowntimeSlashing(address string) error {
	_, err := vm.worldState.GetValidator(address)
	if err != nil {
		return err
	}

	// ✅ STARTUP GRACE PERIOD: Skip slashing if network is young (e.g., < 100 blocks)
	// This prevents nodes from being jailed while they are still syncing in Docker.
	currentHeight := vm.worldState.GetHeight()
	if currentHeight < 100 {
		return nil
	}

	metrics, exists := vm.validatorMetrics[address]
	if !exists {
		return nil
	}

	// Calculate total activity in the window
	totalActivity := metrics.BlocksProposed + metrics.BlocksMissed +
		metrics.AttestationsMade + metrics.AttestationsMissed

	// ✅ LENIENT WINDOW: Ensure we have a significant sample size before slashing
	// Increase performanceWindow logic or check against a higher threshold for dev
	if totalActivity < uint64(vm.performanceWindow) {
		return nil // Not enough data yet
	}

	// Calculate signed ratio
	signed := metrics.BlocksProposed + metrics.AttestationsMade
	signedRatio := float64(signed) / float64(totalActivity)

	// Check if below minimum threshold
	if signedRatio < vm.config.Staking.MinSignedPerWindow {
		// Slash for downtime
		return vm.SlashValidator(address, SlashingDowntime, nil)
	}

	return nil
}

// updateUptimePercentage updates a validator's uptime percentage
func (vm *Manager) updateUptimePercentage(metrics *ValidatorMetrics) {
	totalBlocks := metrics.BlocksProposed + metrics.BlocksMissed
	totalAttestations := metrics.AttestationsMade + metrics.AttestationsMissed

	if totalBlocks+totalAttestations == 0 {
		metrics.UptimePercentage = 100.0
		return
	}

	successful := metrics.BlocksProposed + metrics.AttestationsMade
	total := totalBlocks + totalAttestations

	metrics.UptimePercentage = (float64(successful) / float64(total)) * 100.0
}

// AddDelegation adds a delegation to a validator
func (vm *Manager) AddDelegation(validatorAddr, delegatorAddr string, amount string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	validator, err := vm.worldState.GetValidator(validatorAddr)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	// CHECK 1: Delegation limits
	if len(validator.Delegators) >= vm.config.Staking.MaxDelegationsPerValidator {
		return fmt.Errorf("validator has reached maximum delegations (%d)",
			vm.config.Staking.MaxDelegationsPerValidator)
	}

	// ✅ NEW CHECK 2: Maximum stake per validator
	amountBig := math.ParseBigInt(amount)
	currentStakeBig := math.ParseBigInt(validator.Stake)
	newStakeBig := math.Add(currentStakeBig, amountBig)
	maxStakeBig := math.ParseBigInt(vm.config.Staking.MaxValidatorStake)

	if math.Cmp(newStakeBig, maxStakeBig) > 0 {
		return fmt.Errorf("validator stake would exceed maximum: %s > %s",
			newStakeBig.String(), vm.config.Staking.MaxValidatorStake)
	}

	// ✅ NEW CHECK 3: Stake concentration
	totalNetworkStake := vm.worldState.GetTotalStaked()
	if totalNetworkStake != nil && totalNetworkStake.Sign() > 0 {
		// Calculate: (newValidatorStake / totalNetworkStake)
		newStakeF := new(big.Float).SetInt(newStakeBig)
		totalStakeF := new(big.Float).SetInt(totalNetworkStake)
		percentageF := new(big.Float).Quo(newStakeF, totalStakeF)
		percentage, _ := percentageF.Float64()

		if percentage > vm.config.Staking.MaxStakePercentage {
			return fmt.Errorf("delegation would exceed concentration limit: %.2f%% > %.2f%%",
				percentage*100, vm.config.Staking.MaxStakePercentage*100)
		}
	}

	if vm.config.Governance.OwnershipDomainsEnabled && vm.config.Staking.MaxStakePercentage > 0 {
		domainID, err := vm.worldState.GetValidatorStakeDomain(validatorAddr)
		if err != nil {
			return fmt.Errorf("failed to load validator stake domain: %w", err)
		}

		domainStakeBig, err := vm.worldState.GetStakeDomainTotalStake(domainID)
		if err != nil {
			return fmt.Errorf("failed to load stake domain %s: %w", domainID, err)
		}

		postDomainStake := math.Add(domainStakeBig, amountBig)
		postTotalStake := math.Add(totalNetworkStake, amountBig)
		stakeFraction := new(big.Float).Quo(new(big.Float).SetInt(postDomainStake), new(big.Float).SetInt(postTotalStake))
		stakePercentage, _ := stakeFraction.Float64()
		if stakePercentage > vm.config.Staking.MaxStakePercentage {
			return fmt.Errorf("stake domain would exceed concentration limit: %.2f%% > %.2f%%",
				stakePercentage*100, vm.config.Staking.MaxStakePercentage*100)
		}
	}

	// YOUR EXISTING CODE (unchanged):
	if validator.Delegators == nil {
		validator.Delegators = make(map[string][]byte)
	}

	var currentDelegationStr []byte
	if val, exists := validator.Delegators[delegatorAddr]; exists {
		currentDelegationStr = val
	}

	currentDelegationBig := math.ParseBigInt(currentDelegationStr)
	currentDelegationBig = math.Add(currentDelegationBig, amountBig)
	validator.Delegators[delegatorAddr] = currentDelegationBig.Bytes()

	totalDelegatedBig := math.ParseBigInt(validator.DelegatedStake)
	totalDelegatedBig = math.Add(totalDelegatedBig, amountBig)
	validator.DelegatedStake = totalDelegatedBig.Bytes()

	totalStakeBig := math.ParseBigInt(validator.Stake)
	totalStakeBig = math.Add(totalStakeBig, amountBig)
	validator.Stake = totalStakeBig.Bytes()

	validator.UpdatedAt = time.Now().Unix()

	return vm.worldState.UpdateValidator(validator)
}

// RemoveDelegation removes a delegation from a validator
// ✅ UPDATE: amount changed from int64 -> string
func (vm *Manager) RemoveDelegation(validatorAddr, delegatorAddr string, amount string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	return vm.queueUnbondingLocked(validatorAddr, delegatorAddr, amount)
}

func (vm *Manager) queueUnbondingLocked(validatorAddr, delegatorAddr string, amount string) error {
	validator, err := vm.worldState.GetValidator(validatorAddr)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	amountBig := math.ParseBigInt(amount)
	var currentDelegationStr []byte
	if val, exists := validator.Delegators[delegatorAddr]; exists {
		currentDelegationStr = val
	}
	currentDelegationBig := math.ParseBigInt(currentDelegationStr)

	if math.Cmp(currentDelegationBig, amountBig) < 0 {
		return fmt.Errorf("insufficient delegation: have %s, trying to unbond %s",
			currentDelegationStr, amount)
	}

	currentBlock := vm.worldState.GetHeight()
	blockTime := vm.config.Consensus.BlockTime
	if blockTime <= 0 {
		blockTime = defaultUnbondingBlockTime
	}
	unbondingBlocks := int64(vm.config.Staking.UnbondingPeriod / blockTime)
	if unbondingBlocks < 1 {
		unbondingBlocks = 1
	}
	completionBlock := currentBlock + unbondingBlocks

	entry := &UnbondingEntry{
		ValidatorAddress: validatorAddr,
		DelegatorAddress: delegatorAddr,
		Amount:           amountBig.String(),
		CompletionBlock:  completionBlock,
		CreatedAt:        time.Now().Unix(),
	}

	if vm.unbondingQueue[delegatorAddr] == nil {
		vm.unbondingQueue[delegatorAddr] = make([]*UnbondingEntry, 0)
	}
	vm.unbondingQueue[delegatorAddr] = append(vm.unbondingQueue[delegatorAddr], entry)

	if err := vm.persistUnbondingQueueLocked(); err != nil {
		return fmt.Errorf("failed to persist unbonding queue: %w", err)
	}

	return nil
}

func (vm *Manager) loadUnbondingQueue() error {
	raw, err := vm.worldState.GetMetadata(unbondingQueueMetadataKey)
	if err != nil {
		return err
	}
	if raw == "" {
		return nil
	}

	var persisted map[string][]*UnbondingEntry
	if err := json.Unmarshal([]byte(raw), &persisted); err != nil {
		return fmt.Errorf("decode unbonding queue: %w", err)
	}
	if persisted == nil {
		persisted = make(map[string][]*UnbondingEntry)
	}

	vm.unbondingQueue = persisted
	return nil
}

func (vm *Manager) persistUnbondingQueueLocked() error {
	if vm.unbondingQueue == nil {
		vm.unbondingQueue = make(map[string][]*UnbondingEntry)
	}

	data, err := json.Marshal(vm.unbondingQueue)
	if err != nil {
		return fmt.Errorf("encode unbonding queue: %w", err)
	}

	return vm.worldState.SetMetadata(unbondingQueueMetadataKey, string(data))
}

// GetValidatorMetrics returns metrics for a validator
func (vm *Manager) GetValidatorMetrics(address string) (*ValidatorMetrics, error) {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	metrics, exists := vm.validatorMetrics[address]
	if !exists {
		return nil, fmt.Errorf("metrics not found for validator %s", address)
	}

	// Return a copy
	metricsCopy := *metrics
	return &metricsCopy, nil
}

// GetSlashingHistory returns slashing history for a validator
func (vm *Manager) GetSlashingHistory(address string) ([]*SlashingEvent, error) {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	events, exists := vm.slashingEvents[address]
	if !exists {
		return []*SlashingEvent{}, nil
	}

	// Return a copy
	eventsCopy := make([]*SlashingEvent, len(events))
	copy(eventsCopy, events)
	return eventsCopy, nil
}

// GetTopValidators returns validators sorted by stake
func (vm *Manager) GetTopValidators(limit int) ([]*core.Validator, error) {
	activeValidators := vm.worldState.GetActiveValidators()

	// ✅ FIX: Sort by stake (descending) using BigInt comparison
	sort.Slice(activeValidators, func(i, j int) bool {
		stakeI := math.ParseBigInt(activeValidators[i].Stake)
		stakeJ := math.ParseBigInt(activeValidators[j].Stake)
		return stakeI.Cmp(stakeJ) > 0 // descending order
	})

	if limit > 0 && len(activeValidators) > limit {
		activeValidators = activeValidators[:limit]
	}

	return activeValidators, nil
}

// GetValidatorStats returns overall validator statistics
func (vm *Manager) GetValidatorStats() map[string]interface{} {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	allValidators := vm.worldState.GetActiveValidators()
	totalValidators := vm.worldState.GetValidatorCount()

	// 1. Initialize Accumulators as BigInts
	totalStakeBig := big.NewInt(0)
	totalDelegatedStakeBig := big.NewInt(0)
	jailedCount := 0

	// 2. Iterate and Accumulate
	for _, validator := range allValidators {
		// Parse Stake String -> BigInt
		stakeVal := math.ParseBigInt(validator.Stake)
		totalStakeBig.Add(totalStakeBig, stakeVal)

		// Parse DelegatedStake String -> BigInt
		delegatedVal := math.ParseBigInt(validator.DelegatedStake)
		totalDelegatedStakeBig.Add(totalDelegatedStakeBig, delegatedVal)

		if vm.isJailed(validator) {
			jailedCount++
		}
	}

	// 3. Calculate Average Stake
	avgStakeBig := big.NewInt(0)
	if len(allValidators) > 0 {
		// Avg = Total / Count
		validatorCountBig := big.NewInt(int64(len(allValidators)))
		avgStakeBig.Div(totalStakeBig, validatorCountBig)
	}

	// 4. Return results (as strings to preserve precision)
	return map[string]interface{}{
		"total_validators":      totalValidators,
		"active_validators":     len(allValidators),
		"jailed_validators":     jailedCount,
		"total_stake":           totalStakeBig.String(),          // ✅ Return String
		"total_delegated_stake": totalDelegatedStakeBig.String(), // ✅ Return String
		"average_stake":         avgStakeBig.String(),            // ✅ Return String
		"metrics_tracked":       len(vm.validatorMetrics),
		"slashing_events":       vm.getTotalSlashingEvents(),
	}
}

// isJailed checks if a validator is currently jailed
func (vm *Manager) isJailed(validator *core.Validator) bool {
	return validator.JailUntil > time.Now().Unix()
}

// getTotalSlashingEvents returns total number of slashing events
func (vm *Manager) getTotalSlashingEvents() int {
	total := 0
	for _, events := range vm.slashingEvents {
		total += len(events)
	}
	return total
}

// GetValidator returns a validator by address
func (vm *Manager) GetValidator(address string) (*core.Validator, error) {
	return vm.worldState.GetValidator(address)
}

// UpdateValidator updates a validator in the world state
func (vm *Manager) UpdateValidator(validator *core.Validator) error {
	return vm.worldState.UpdateValidator(validator)
}

// GetActiveValidators returns all active validators
func (vm *Manager) GetActiveValidators() []*core.Validator {
	return vm.worldState.GetActiveValidators()
}

// GetAllValidators returns all validators (active and inactive)
func (vm *Manager) GetAllValidators() []*core.Validator {
	return vm.worldState.GetAllValidators()
}

// IsActive checks if a validator is active
func (vm *Manager) IsActive(address string) bool {
	validator, err := vm.worldState.GetValidator(address)
	if err != nil {
		return false
	}
	return validator.Active && !vm.isJailed(validator)
}

// GetPerformanceScore returns the performance score for a validator
func (vm *Manager) GetPerformanceScore(address string) float64 {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	metrics, exists := vm.validatorMetrics[address]
	if !exists {
		return 1.0 // Default performance score
	}

	return metrics.UptimePercentage / 100.0
}
