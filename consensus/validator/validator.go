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
	"fmt"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/math"
	"github.com/thrylos-labs/go-thrylos/core/state"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
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
	return &Manager{
		config:            config,
		worldState:        worldState,
		validatorMetrics:  make(map[string]*ValidatorMetrics),
		slashingEvents:    make(map[string][]*SlashingEvent),
		performanceWindow: config.Staking.SignedBlocksWindow,
	}
}

// RegisterValidator registers a new validator
// ✅ UPDATE: stake changed from int64 -> string
func (vm *Manager) RegisterValidator(
	address string,
	pubkey []byte,
	stake string,
	commission float64,
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

	// Compare: if stake < minStake
	if stakeBig.Cmp(minStakeBig) < 0 {
		return fmt.Errorf("stake %s below minimum %s",
			stake, vm.config.Staking.MinValidatorStake)
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
		Stake:     stake,
		SelfStake: stake, // Initially all stake is self-stake

		// ✅ Fix: Use "0" string instead of int 0
		DelegatedStake: "0",

		// ✅ Fix: Initialize map as map[string]string
		Delegators: make(map[string]string),

		Commission:     commission,
		Active:         false, // Not active until meeting requirements
		BlocksProposed: 0,
		BlocksMissed:   0,
		JailUntil:      0,
		CreatedAt:      time.Now().Unix(),
		UpdatedAt:      time.Now().Unix(),
	}

	// Add to world state
	if err := vm.worldState.AddValidator(validator); err != nil {
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

	// Check minimum stake requirement
	if validator.Stake < vm.config.Staking.MinValidatorStake {
		return fmt.Errorf("validator stake %d below minimum %d",
			validator.Stake, vm.config.Staking.MinValidatorStake)
	}

	// Check minimum self-stake requirement
	if validator.SelfStake < vm.config.Staking.MinSelfStake {
		return fmt.Errorf("validator self-stake %d below minimum %d",
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

	// Calculate slash amount based on reason
	var slashFraction float64
	var jailDuration time.Duration

	switch reason {
	case SlashingDoubleSign:
		slashFraction = vm.config.Staking.SlashFractionDoubleSign
		jailDuration = 30 * 24 * time.Hour
	case SlashingDowntime:
		slashFraction = vm.config.Staking.SlashFractionDowntime
		jailDuration = vm.config.Staking.DowntimeJailDuration
	case SlashingInvalidBlock:
		slashFraction = 0.01 // 1%
		jailDuration = 24 * time.Hour
	default:
		return fmt.Errorf("unknown slashing reason: %s", reason)
	}

	// 1. Parse Stake to BigInt
	stakeBig := math.ParseBigInt(validator.Stake)

	// 2. Calculate Slash Amount: (Stake * Fraction)
	// Use BigFloat for precision multiplication
	fStake := new(big.Float).SetInt(stakeBig)
	fFraction := big.NewFloat(slashFraction)
	fSlashResult := new(big.Float).Mul(fStake, fFraction)

	// Convert result back to BigInt
	slashAmountBig, _ := fSlashResult.Int(nil)

	// Ensure minimum slash of 1 Wei if fraction > 0 but result is 0
	if slashFraction > 0 && slashAmountBig.Sign() == 0 {
		slashAmountBig = big.NewInt(1)
	}

	// 3. Apply Slashing: Stake - SlashAmount
	stakeBig.Sub(stakeBig, slashAmountBig)

	// Clamp to 0 if negative
	if stakeBig.Sign() < 0 {
		stakeBig.SetInt64(0)
	}

	// 4. Update Validator State (Store as string)
	validator.Stake = stakeBig.String()

	// Jail the validator
	validator.JailUntil = time.Now().Add(jailDuration).Unix()
	validator.Active = false
	validator.UpdatedAt = time.Now().Unix()

	// 5. Record slashing event (Convert slash amount to string)
	slashingEvent := &SlashingEvent{
		ValidatorAddress: address,
		Reason:           reason,

		// ✅ Fix: Amount is likely now a string field in SlashingEvent too
		Amount: slashAmountBig.String(),

		BlockHeight: vm.worldState.GetHeight(),
		Timestamp:   time.Now().Unix(),
		Evidence:    evidence,
	}

	if vm.slashingEvents[address] == nil {
		vm.slashingEvents[address] = make([]*SlashingEvent, 0)
	}
	vm.slashingEvents[address] = append(vm.slashingEvents[address], slashingEvent)

	// 6. Update metrics
	if metrics, exists := vm.validatorMetrics[address]; exists {
		metrics.SlashCount++

		// Accumulate TotalSlashed (Parse -> Add -> Store)
		currentSlashed := math.ParseBigInt(metrics.TotalSlashed)
		currentSlashed.Add(currentSlashed, slashAmountBig)
		metrics.TotalSlashed = currentSlashed.String()

		metrics.JailHistory = append(metrics.JailHistory, JailEvent{
			Reason:   string(reason),
			JailTime: validator.JailUntil,
			Duration: int64(jailDuration.Seconds()),
		})
	}

	// Update validator in world state
	if err := vm.worldState.UpdateValidator(validator); err != nil {
		return fmt.Errorf("failed to update slashed validator: %v", err)
	}

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

	// Check if validator still meets minimum requirements
	if validator.Stake < vm.config.Staking.MinValidatorStake {
		return fmt.Errorf("validator stake %d below minimum %d after slashing",
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

	// Update commission
	validator.Commission = newCommission
	validator.UpdatedAt = time.Now().Unix()

	return vm.worldState.UpdateValidator(validator)
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
func (vm *Manager) checkDowntimeSlashing(address string) error {
	_, err := vm.worldState.GetValidator(address)
	if err != nil {
		return err
	}

	metrics, exists := vm.validatorMetrics[address]
	if !exists {
		return nil
	}

	// Calculate total activity in the window
	totalActivity := metrics.BlocksProposed + metrics.BlocksMissed +
		metrics.AttestationsMade + metrics.AttestationsMissed

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
// AddDelegation adds a delegation to a validator
// ✅ UPDATE: amount changed from int64 -> string
func (vm *Manager) AddDelegation(validatorAddr, delegatorAddr string, amount string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	validator, err := vm.worldState.GetValidator(validatorAddr)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	// Check delegation limits
	if len(validator.Delegators) >= vm.config.Staking.MaxDelegationsPerValidator {
		return fmt.Errorf("validator has reached maximum delegations (%d)",
			vm.config.Staking.MaxDelegationsPerValidator)
	}

	// Initialize map correctly if nil
	if validator.Delegators == nil {
		// ✅ Fix: Initialize as map[string]string
		validator.Delegators = make(map[string]string)
	}

	// 1. Parse Input Amount
	amountBig := math.ParseBigInt(amount)

	// 2. Update Specific Delegator Entry
	// Get current delegation (default to "0" if missing)
	currentDelegationStr := "0"
	if val, exists := validator.Delegators[delegatorAddr]; exists {
		currentDelegationStr = val
	}

	currentDelegationBig := math.ParseBigInt(currentDelegationStr)

	// Add amount
	currentDelegationBig.Add(currentDelegationBig, amountBig)

	// Store back as string
	validator.Delegators[delegatorAddr] = currentDelegationBig.String()

	// 3. Update Total Delegated Stake
	totalDelegatedBig := math.ParseBigInt(validator.DelegatedStake)
	totalDelegatedBig.Add(totalDelegatedBig, amountBig)
	validator.DelegatedStake = totalDelegatedBig.String()

	// 4. Update Total Validator Stake (Self + Delegated)
	// Note: Assuming 'Stake' includes delegated amounts. If Stake is only self-stake, remove this step.
	totalStakeBig := math.ParseBigInt(validator.Stake)
	totalStakeBig.Add(totalStakeBig, amountBig)
	validator.Stake = totalStakeBig.String()

	validator.UpdatedAt = time.Now().Unix()

	return vm.worldState.UpdateValidator(validator)
}

// RemoveDelegation removes a delegation from a validator
// ✅ UPDATE: amount changed from int64 -> string
func (vm *Manager) RemoveDelegation(validatorAddr, delegatorAddr string, amount string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	validator, err := vm.worldState.GetValidator(validatorAddr)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	// 1. Parse Input Amount
	amountBig := math.ParseBigInt(amount)

	// 2. Get Current Delegation
	currentDelegationStr := "0"
	if val, exists := validator.Delegators[delegatorAddr]; exists {
		currentDelegationStr = val
	}
	currentDelegationBig := math.ParseBigInt(currentDelegationStr)

	// 3. Check Insufficient Funds: if current < amount
	if currentDelegationBig.Cmp(amountBig) < 0 {
		return fmt.Errorf("insufficient delegation: have %s, trying to remove %s",
			currentDelegationStr, amount)
	}

	// 4. Remove Delegation: Current - Amount
	currentDelegationBig.Sub(currentDelegationBig, amountBig)

	// Update Map
	if currentDelegationBig.Sign() == 0 {
		delete(validator.Delegators, delegatorAddr)
	} else {
		validator.Delegators[delegatorAddr] = currentDelegationBig.String()
	}

	// 5. Update Total Delegated Stake
	delegatedStakeBig := math.ParseBigInt(validator.DelegatedStake)
	delegatedStakeBig.Sub(delegatedStakeBig, amountBig)
	validator.DelegatedStake = delegatedStakeBig.String()

	// 6. Update Total Stake (Self + Delegated)
	totalStakeBig := math.ParseBigInt(validator.Stake)
	totalStakeBig.Sub(totalStakeBig, amountBig)
	validator.Stake = totalStakeBig.String()

	validator.UpdatedAt = time.Now().Unix()

	// 7. Check Minimum Requirements
	minStakeBig := math.ParseBigInt(vm.config.Staking.MinValidatorStake)

	// if Stake < MinStake
	if validator.Active && totalStakeBig.Cmp(minStakeBig) < 0 {
		validator.Active = false
	}

	return vm.worldState.UpdateValidator(validator)
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

	// Sort by stake (descending)
	sort.Slice(activeValidators, func(i, j int) bool {
		return activeValidators[i].Stake > activeValidators[j].Stake
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

// CleanupOldMetrics removes old metrics data
func (vm *Manager) CleanupOldMetrics(maxAge time.Duration) {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	cutoff := time.Now().Add(-maxAge).Unix()

	for address, metrics := range vm.validatorMetrics {
		if metrics.LastActivity < cutoff {
			// Check if validator still exists
			if _, err := vm.worldState.GetValidator(address); err != nil {
				delete(vm.validatorMetrics, address)
				delete(vm.slashingEvents, address)
			}
		}
	}
}

// ValidateValidatorSet validates the current validator set
func (vm *Manager) ValidateValidatorSet() error {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	activeValidators := vm.worldState.GetActiveValidators()

	for _, validator := range activeValidators {
		// 1. Check minimum stake (String vs String)
		stakeBig := math.ParseBigInt(validator.Stake)
		minStakeBig := math.ParseBigInt(vm.config.Staking.MinValidatorStake)

		// Compare: if stake < minStake
		if stakeBig.Cmp(minStakeBig) < 0 {
			return fmt.Errorf("validator %s has insufficient stake: %s < %s",
				validator.Address, validator.Stake, vm.config.Staking.MinValidatorStake)
		}

		// Check if jailed
		if vm.isJailed(validator) {
			return fmt.Errorf("validator %s is jailed but marked as active", validator.Address)
		}

		// Validate commission (Commission is likely still float64, so this is fine)
		if validator.Commission < 0 || validator.Commission > vm.config.Staking.MaxCommission {
			return fmt.Errorf("validator %s has invalid commission: %.4f",
				validator.Address, validator.Commission)
		}

		// 2. Validate delegations (Map Summation)
		totalDelegatedBig := big.NewInt(0)

		// Iterate over map[string]string
		for _, amountStr := range validator.Delegators {
			amountBig := math.ParseBigInt(amountStr)
			totalDelegatedBig.Add(totalDelegatedBig, amountBig)
		}

		// Compare calculated vs stored
		storedDelegatedBig := math.ParseBigInt(validator.DelegatedStake)

		// Compare: if calculated != stored
		if totalDelegatedBig.Cmp(storedDelegatedBig) != 0 {
			return fmt.Errorf("validator %s delegation mismatch: calculated %s, stored %s",
				validator.Address, totalDelegatedBig.String(), validator.DelegatedStake)
		}
	}

	return nil
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
	// This would need to be implemented in worldState
	// For now, return active validators
	return vm.worldState.GetActiveValidators()
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
