// consensus/validator/lifecycle.go

// Validator lifecycle management for complete state transitions
// Features:
// - Complete validator lifecycle from registration to removal
// - State transition validation and enforcement
// - Automatic lifecycle event processing
// - Delegation lifecycle management
// - Performance-based lifecycle decisions
// - Economic lifecycle incentives and penalties
// - Cross-shard validator coordination

package validator

import (
	"fmt"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
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

	// State tracking
	currentEpoch  uint64
	epochDuration time.Duration

	// Performance thresholds
	activationThreshold   float64 // Performance score needed for activation
	deactivationThreshold float64 // Performance score triggering deactivation
	removalThreshold      float64 // Performance score triggering removal

	// Synchronization
	mu sync.RWMutex

	// Lifecycle worker
	isRunning bool
	stopChan  chan struct{}
}

// ValidatorState represents the current state of a validator
type ValidatorState string

const (
	StateRegistered   ValidatorState = "registered"   // Registered but not active
	StatePending      ValidatorState = "pending"      // Waiting for activation
	StateActive       ValidatorState = "active"       // Actively validating
	StateJailed       ValidatorState = "jailed"       // Temporarily jailed
	StateSlashed      ValidatorState = "slashed"      // Slashed and deactivated
	StateDeactivating ValidatorState = "deactivating" // In process of deactivation
	StateInactive     ValidatorState = "inactive"     // Deactivated but can return
	StateRemoving     ValidatorState = "removing"     // Being removed from set
	StateRemoved      ValidatorState = "removed"      // Completely removed
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
	EventCommissionChange  LifecycleEventType = "commission_change"
	EventPerformanceUpdate LifecycleEventType = "performance_update"
	EventRemoval           LifecycleEventType = "removal"
	EventRejoin            LifecycleEventType = "rejoin"
)

// LifecycleTransition represents a pending state transition
type LifecycleTransition struct {
	ValidatorAddress string                 `json:"validator_address"`
	TargetState      ValidatorState         `json:"target_state"`
	Reason           string                 `json:"reason"`
	ScheduledTime    int64                  `json:"scheduled_time"`
	Data             map[string]interface{} `json:"data"`
}

// LifecycleRule defines rules for state transitions
type LifecycleRule struct {
	FromState      ValidatorState       `json:"from_state"`
	ToState        ValidatorState       `json:"to_state"`
	Conditions     []LifecycleCondition `json:"conditions"`
	AutoTrigger    bool                 `json:"auto_trigger"`
	CooldownPeriod time.Duration        `json:"cooldown_period"`
}

// LifecycleCondition defines conditions for state transitions
type LifecycleCondition struct {
	Type     ConditionType `json:"type"`
	Operator string        `json:"operator"` // ">=", "<=", "==", "!=", ">", "<"
	Value    interface{}   `json:"value"`
	Field    string        `json:"field"`
}

// ConditionType represents types of lifecycle conditions
type ConditionType string

const (
	ConditionStake       ConditionType = "stake"
	ConditionPerformance ConditionType = "performance"
	ConditionTime        ConditionType = "time"
	ConditionBlocks      ConditionType = "blocks"
	ConditionSlashCount  ConditionType = "slash_count"
	ConditionDelegations ConditionType = "delegations"
	ConditionAge         ConditionType = "age"
)

// NewLifecycleManager creates a new validator lifecycle manager
func NewLifecycleManager(config *config.Config, worldState *state.WorldState, manager *Manager) *LifecycleManager {
	return &LifecycleManager{
		config:          config,
		worldState:      worldState,
		manager:         manager,
		lifecycleEvents: make(map[string][]*LifecycleEvent),
		transitionQueue: make(chan *LifecycleTransition, 1000),
		epochDuration:   24 * time.Hour, // Default epoch duration

		// Performance thresholds
		activationThreshold:   0.8, // 80% performance for activation
		deactivationThreshold: 0.3, // 30% performance triggers deactivation
		removalThreshold:      0.1, // 10% performance triggers removal

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

	// Start periodic checks
	go lm.periodicLifecycleCheck()

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
	stake int64,
	commission float64,
	selfDelegation int64,
) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	// Validate registration requirements
	if err := lm.validateRegistrationRequirements(address, stake, commission, selfDelegation); err != nil {
		return fmt.Errorf("registration validation failed: %v", err)
	}

	// Register with validator manager
	if err := lm.manager.RegisterValidator(address, pubkey, stake, commission); err != nil {
		return fmt.Errorf("validator registration failed: %v", err)
	}

	// Record lifecycle event
	event := &LifecycleEvent{
		ValidatorAddress: address,
		EventType:        EventRegistration,
		FromState:        "",
		ToState:          StateRegistered,
		Timestamp:        time.Now().Unix(),
		BlockHeight:      lm.worldState.GetHeight(),
		Reason:           "New validator registration",
		Data: map[string]interface{}{
			"stake":           stake,
			"commission":      commission,
			"self_delegation": selfDelegation,
		},
	}

	lm.recordLifecycleEvent(address, event)

	// Schedule activation check
	lm.scheduleTransition(address, StatePending, "Pending activation review", 0)

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

	// Check activation requirements
	if err := lm.validateActivationRequirements(validator); err != nil {
		return fmt.Errorf("activation requirements not met: %v", err)
	}

	// Activate validator
	if err := lm.manager.ActivateValidator(address); err != nil {
		return fmt.Errorf("activation failed: %v", err)
	}

	// Record lifecycle event
	event := &LifecycleEvent{
		ValidatorAddress: address,
		EventType:        EventActivation,
		FromState:        currentState,
		ToState:          StateActive,
		Timestamp:        time.Now().Unix(),
		BlockHeight:      lm.worldState.GetHeight(),
		Reason:           "Validator activated",
	}

	lm.recordLifecycleEvent(address, event)

	return nil
}

// ProcessDeactivation handles validator deactivation
func (lm *LifecycleManager) ProcessDeactivation(address string, reason string) error {
	validator, err := lm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	currentState := lm.getValidatorState(validator)
	if currentState != StateActive {
		return fmt.Errorf("validator %s cannot be deactivated from state %s", address, currentState)
	}

	// Deactivate validator
	if err := lm.manager.DeactivateValidator(address, reason); err != nil {
		return fmt.Errorf("deactivation failed: %v", err)
	}

	// Record lifecycle event
	event := &LifecycleEvent{
		ValidatorAddress: address,
		EventType:        EventDeactivation,
		FromState:        currentState,
		ToState:          StateInactive,
		Timestamp:        time.Now().Unix(),
		BlockHeight:      lm.worldState.GetHeight(),
		Reason:           reason,
	}

	lm.recordLifecycleEvent(address, event)

	return nil
}

// ProcessJailing handles validator jailing
func (lm *LifecycleManager) ProcessJailing(address string, reason SlashingReason, duration time.Duration) error {
	validator, err := lm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	currentState := lm.getValidatorState(validator)

	// Slash validator (which includes jailing)
	if err := lm.manager.SlashValidator(address, reason, nil); err != nil {
		return fmt.Errorf("slashing failed: %v", err)
	}

	// Record lifecycle event
	event := &LifecycleEvent{
		ValidatorAddress: address,
		EventType:        EventJailing,
		FromState:        currentState,
		ToState:          StateJailed,
		Timestamp:        time.Now().Unix(),
		BlockHeight:      lm.worldState.GetHeight(),
		Reason:           fmt.Sprintf("Jailed for %s", reason),
		Data: map[string]interface{}{
			"jail_duration":   duration.Seconds(),
			"slashing_reason": string(reason),
		},
	}

	lm.recordLifecycleEvent(address, event)

	// Schedule unjailing check
	unjailTime := time.Now().Add(duration).Unix()
	lm.scheduleTransition(address, StateActive, "Scheduled unjailing", unjailTime)

	return nil
}

// ProcessUnjailing handles validator unjailing
func (lm *LifecycleManager) ProcessUnjailing(address string) error {
	validator, err := lm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	currentState := lm.getValidatorState(validator)
	if currentState != StateJailed {
		return fmt.Errorf("validator %s is not jailed", address)
	}

	// Unjail validator
	if err := lm.manager.UnjailValidator(address); err != nil {
		return fmt.Errorf("unjailing failed: %v", err)
	}

	// Check if validator can be reactivated
	if err := lm.validateActivationRequirements(validator); err == nil {
		lm.manager.ActivateValidator(address)
	}

	// Record lifecycle event
	event := &LifecycleEvent{
		ValidatorAddress: address,
		EventType:        EventUnjailing,
		FromState:        currentState,
		ToState:          StateActive,
		Timestamp:        time.Now().Unix(),
		BlockHeight:      lm.worldState.GetHeight(),
		Reason:           "Validator unjailed",
	}

	lm.recordLifecycleEvent(address, event)

	return nil
}

// ProcessStakeChange handles stake changes and their lifecycle implications
func (lm *LifecycleManager) ProcessStakeChange(address string, oldStake, newStake int64) error {
	validator, err := lm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	currentState := lm.getValidatorState(validator)
	stakeDelta := newStake - oldStake

	var eventType LifecycleEventType
	var reason string

	if stakeDelta > 0 {
		eventType = EventStakeIncrease
		reason = fmt.Sprintf("Stake increased by %d", stakeDelta)
	} else {
		eventType = EventStakeDecrease
		reason = fmt.Sprintf("Stake decreased by %d", -stakeDelta)
	}

	// Check if stake change affects validator status
	if newStake < lm.config.Staking.MinValidatorStake {
		if currentState == StateActive {
			lm.ProcessDeactivation(address, "Stake below minimum threshold")
		}
	} else if oldStake < lm.config.Staking.MinValidatorStake && newStake >= lm.config.Staking.MinValidatorStake {
		if currentState == StateInactive || currentState == StateRegistered {
			lm.scheduleTransition(address, StatePending, "Stake meets minimum threshold", 0)
		}
	}

	// Record lifecycle event
	event := &LifecycleEvent{
		ValidatorAddress: address,
		EventType:        eventType,
		FromState:        currentState,
		ToState:          currentState, // State might not change
		Timestamp:        time.Now().Unix(),
		BlockHeight:      lm.worldState.GetHeight(),
		Reason:           reason,
		Data: map[string]interface{}{
			"old_stake": oldStake,
			"new_stake": newStake,
			"delta":     stakeDelta,
		},
	}

	lm.recordLifecycleEvent(address, event)

	return nil
}

// ProcessPerformanceUpdate handles performance-based lifecycle decisions
func (lm *LifecycleManager) ProcessPerformanceUpdate(address string, performanceScore float64) error {
	validator, err := lm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	currentState := lm.getValidatorState(validator)

	// Performance-based state transitions
	switch currentState {
	case StateActive:
		if performanceScore < lm.deactivationThreshold {
			lm.scheduleTransition(address, StateDeactivating,
				fmt.Sprintf("Poor performance: %.2f", performanceScore), 0)
		}
	case StateInactive, StateRegistered:
		if performanceScore >= lm.activationThreshold {
			lm.scheduleTransition(address, StatePending,
				fmt.Sprintf("Good performance: %.2f", performanceScore), 0)
		}
	}

	// Record lifecycle event
	event := &LifecycleEvent{
		ValidatorAddress: address,
		EventType:        EventPerformanceUpdate,
		FromState:        currentState,
		ToState:          currentState,
		Timestamp:        time.Now().Unix(),
		BlockHeight:      lm.worldState.GetHeight(),
		Reason:           fmt.Sprintf("Performance score updated to %.2f", performanceScore),
		Data: map[string]interface{}{
			"performance_score": performanceScore,
		},
	}

	lm.recordLifecycleEvent(address, event)

	return nil
}

// getValidatorState determines the current state of a validator
func (lm *LifecycleManager) getValidatorState(validator *core.Validator) ValidatorState {
	currentTime := time.Now().Unix()

	// Check if jailed
	if validator.JailUntil > currentTime {
		return StateJailed
	}

	// Check if active
	if validator.Active {
		return StateActive
	}

	// Check if meets minimum requirements
	if validator.Stake < lm.config.Staking.MinValidatorStake {
		return StateInactive
	}

	// Default to registered
	return StateRegistered
}

// validateRegistrationRequirements validates requirements for validator registration
func (lm *LifecycleManager) validateRegistrationRequirements(address string, stake int64, commission float64, selfDelegation int64) error {
	// Validate stake
	if stake < lm.config.Staking.MinValidatorStake {
		return fmt.Errorf("stake %d below minimum %d", stake, lm.config.Staking.MinValidatorStake)
	}

	// Validate commission
	if commission < 0 || commission > lm.config.Staking.MaxCommission {
		return fmt.Errorf("commission %.4f outside valid range [0, %.4f]", commission, lm.config.Staking.MaxCommission)
	}

	// Validate self-delegation
	if selfDelegation < lm.config.Staking.MinSelfStake {
		return fmt.Errorf("self-delegation %d below minimum %d", selfDelegation, lm.config.Staking.MinSelfStake)
	}

	// Validate address
	if err := account.ValidateAddress(address); err != nil {
		return fmt.Errorf("invalid address: %v", err)
	}

	return nil
}

// validateActivationRequirements validates requirements for validator activation
func (lm *LifecycleManager) validateActivationRequirements(validator *core.Validator) error {
	// Check minimum stake
	if validator.Stake < lm.config.Staking.MinValidatorStake {
		return fmt.Errorf("stake %d below minimum %d", validator.Stake, lm.config.Staking.MinValidatorStake)
	}

	// Check minimum self-stake
	if validator.SelfStake < lm.config.Staking.MinSelfStake {
		return fmt.Errorf("self-stake %d below minimum %d", validator.SelfStake, lm.config.Staking.MinSelfStake)
	}

	// Check if jailed
	if validator.JailUntil > time.Now().Unix() {
		return fmt.Errorf("validator is jailed until %d", validator.JailUntil)
	}

	// Check performance (if metrics exist)
	if metrics, err := lm.manager.GetValidatorMetrics(validator.Address); err == nil {
		if metrics.UptimePercentage < 50.0 { // 50% minimum uptime
			return fmt.Errorf("uptime %.2f%% below minimum 50%%", metrics.UptimePercentage)
		}
	}

	return nil
}

// scheduleTransition schedules a state transition
func (lm *LifecycleManager) scheduleTransition(address string, targetState ValidatorState, reason string, scheduledTime int64) {
	if scheduledTime == 0 {
		scheduledTime = time.Now().Unix()
	}

	transition := &LifecycleTransition{
		ValidatorAddress: address,
		TargetState:      targetState,
		Reason:           reason,
		ScheduledTime:    scheduledTime,
	}

	select {
	case lm.transitionQueue <- transition:
		// Successfully queued
	default:
		// Queue is full, log error
		fmt.Printf("Transition queue full, dropping transition for %s\n", address)
	}
}

// recordLifecycleEvent records a lifecycle event
func (lm *LifecycleManager) recordLifecycleEvent(address string, event *LifecycleEvent) {
	if lm.lifecycleEvents[address] == nil {
		lm.lifecycleEvents[address] = make([]*LifecycleEvent, 0)
	}

	lm.lifecycleEvents[address] = append(lm.lifecycleEvents[address], event)

	// Keep only last 100 events per validator
	if len(lm.lifecycleEvents[address]) > 100 {
		lm.lifecycleEvents[address] = lm.lifecycleEvents[address][1:]
	}
}

// lifecycleWorker processes scheduled transitions
func (lm *LifecycleManager) lifecycleWorker() {
	ticker := time.NewTicker(30 * time.Second) // Process every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-lm.stopChan:
			return
		case transition := <-lm.transitionQueue:
			lm.processScheduledTransition(transition)
		case <-ticker.C:
			lm.processPeriodicChecks()
		}
	}
}

// processScheduledTransition processes a scheduled state transition
func (lm *LifecycleManager) processScheduledTransition(transition *LifecycleTransition) {
	currentTime := time.Now().Unix()

	// Check if it's time for the transition
	if transition.ScheduledTime > currentTime {
		// Re-queue for later
		go func() {
			time.Sleep(time.Duration(transition.ScheduledTime-currentTime) * time.Second)
			lm.transitionQueue <- transition
		}()
		return
	}

	// Process the transition based on target state
	switch transition.TargetState {
	case StatePending:
		// Check if validator can be activated
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

// periodicLifecycleCheck performs periodic lifecycle maintenance
func (lm *LifecycleManager) periodicLifecycleCheck() {
	ticker := time.NewTicker(10 * time.Minute) // Check every 10 minutes
	defer ticker.Stop()

	for {
		select {
		case <-lm.stopChan:
			return
		case <-ticker.C:
			lm.performPeriodicChecks()
		}
	}
}

// performPeriodicChecks performs various periodic lifecycle checks
func (lm *LifecycleManager) performPeriodicChecks() {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	// Get all validators
	activeValidators := lm.worldState.GetActiveValidators()

	for _, validator := range activeValidators {
		// Check performance
		if metrics, err := lm.manager.GetValidatorMetrics(validator.Address); err == nil {
			performanceScore := metrics.UptimePercentage / 100.0
			lm.ProcessPerformanceUpdate(validator.Address, performanceScore)
		}

		// Check for automatic unjailing
		if validator.JailUntil > 0 && validator.JailUntil <= time.Now().Unix() {
			lm.ProcessUnjailing(validator.Address)
		}
	}
}

// processPeriodicChecks is called by the lifecycle worker
func (lm *LifecycleManager) processPeriodicChecks() {
	// This method is called more frequently for time-sensitive checks
	currentTime := time.Now().Unix()

	// Check for validators that should be unjailed
	activeValidators := lm.worldState.GetActiveValidators()
	for _, validator := range activeValidators {
		if validator.JailUntil > 0 && validator.JailUntil <= currentTime {
			lm.scheduleTransition(validator.Address, StateActive, "Jail time expired", 0)
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

	// Return a copy
	eventsCopy := make([]*LifecycleEvent, len(events))
	copy(eventsCopy, events)
	return eventsCopy, nil
}

// GetValidatorState returns the current state of a validator
func (lm *LifecycleManager) GetValidatorState(address string) (ValidatorState, error) {
	validator, err := lm.worldState.GetValidator(address)
	if err != nil {
		return "", fmt.Errorf("validator not found: %v", err)
	}

	return lm.getValidatorState(validator), nil
}

// GetLifecycleStats returns overall lifecycle statistics
func (lm *LifecycleManager) GetLifecycleStats() map[string]interface{} {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	stats := make(map[string]interface{})

	// Count validators by state
	stateCounts := make(map[ValidatorState]int)
	activeValidators := lm.worldState.GetActiveValidators()

	for _, validator := range activeValidators {
		state := lm.getValidatorState(validator)
		stateCounts[state]++
	}

	stats["validator_states"] = stateCounts
	stats["total_validators"] = len(activeValidators)
	stats["transition_queue_size"] = len(lm.transitionQueue)
	stats["total_events"] = lm.getTotalEventCount()
	stats["performance_thresholds"] = map[string]float64{
		"activation":   lm.activationThreshold,
		"deactivation": lm.deactivationThreshold,
		"removal":      lm.removalThreshold,
	}

	return stats
}

// getTotalEventCount returns the total number of lifecycle events
func (lm *LifecycleManager) getTotalEventCount() int {
	total := 0
	for _, events := range lm.lifecycleEvents {
		total += len(events)
	}
	return total
}

// CleanupOldEvents removes old lifecycle events
func (lm *LifecycleManager) CleanupOldEvents(maxAge time.Duration) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	cutoff := time.Now().Add(-maxAge).Unix()

	for address, events := range lm.lifecycleEvents {
		var filteredEvents []*LifecycleEvent

		for _, event := range events {
			if event.Timestamp > cutoff {
				filteredEvents = append(filteredEvents, event)
			}
		}

		if len(filteredEvents) == 0 {
			delete(lm.lifecycleEvents, address)
		} else {
			lm.lifecycleEvents[address] = filteredEvents
		}
	}
}
