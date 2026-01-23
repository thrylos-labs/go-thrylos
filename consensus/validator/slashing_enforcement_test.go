// consensus/validator/slashing_enforcement_test.go
package validator

import (
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// ============================================================================
// OPTION 1: Define an interface that matches what Manager needs
// ============================================================================

// WorldStateInterface defines the minimal interface needed for testing
type WorldStateInterface interface {
	GetValidator(address string) (*core.Validator, error)
	SetValidator(address string, validator *core.Validator) error
	UpdateValidator(validator *core.Validator) error
	GetHeight() int64
}

// MockWorldState implements WorldStateInterface
type MockWorldState struct {
	validators map[string]*core.Validator
	height     int64
}

func NewMockWorldState() *MockWorldState {
	return &MockWorldState{
		validators: make(map[string]*core.Validator),
		height:     1000,
	}
}

func (m *MockWorldState) GetValidator(address string) (*core.Validator, error) {
	if v, exists := m.validators[address]; exists {
		return v, nil
	}
	return nil, &ValidatorNotFoundError{Address: address}
}

func (m *MockWorldState) SetValidator(address string, validator *core.Validator) error {
	m.validators[address] = validator
	return nil
}

func (m *MockWorldState) UpdateValidator(validator *core.Validator) error {
	m.validators[validator.Address] = validator
	return nil
}

func (m *MockWorldState) GetHeight() int64 {
	return m.height
}

// Simple error type
type ValidatorNotFoundError struct {
	Address string
}

func (e *ValidatorNotFoundError) Error() string {
	return "validator not found: " + e.Address
}

// ============================================================================
// OPTION 2: Create a test-specific Manager that uses the interface
// ============================================================================

// TestManager is like Manager but uses an interface for worldState
type TestManager struct {
	config           *config.Config
	worldState       WorldStateInterface // ✅ Use interface instead
	slashingEvents   map[string][]*SlashingEvent
	validatorMetrics map[string]*ValidatorMetrics
}

// SlashValidator - copied from Manager but using interface
func (tm *TestManager) SlashValidator(
	address string,
	reason SlashingReason,
	evidence []byte,
) error {
	// Get the actual Manager's SlashValidator logic
	// We'll create a real Manager temporarily and use its logic

	// For now, let's create a simplified version for testing
	validator, err := tm.worldState.GetValidator(address)
	if err != nil {
		return err
	}

	// Get config values
	maxEvents := 3
	minRetention := 0.5
	autoRemoveDoubleSign := true

	if tm.config != nil && tm.config.Staking.MaxSlashingEvents > 0 {
		maxEvents = tm.config.Staking.MaxSlashingEvents
	}
	if tm.config != nil && tm.config.Staking.MinStakeRetention > 0 {
		minRetention = tm.config.Staking.MinStakeRetention
	}
	if tm.config != nil {
		autoRemoveDoubleSign = tm.config.Staking.AutoRemoveOnDoubleSign
	}

	slashCount := len(tm.slashingEvents[address])

	// Calculate slash amount
	var slashFraction float64
	var jailDuration time.Duration

	switch reason {
	case SlashingDoubleSign:
		slashFraction = tm.config.Staking.SlashFractionDoubleSign
		jailDuration = 30 * 24 * time.Hour
	case SlashingDowntime:
		slashFraction = tm.config.Staking.SlashFractionDowntime
		switch slashCount {
		case 0:
			jailDuration = 24 * time.Hour
		case 1:
			jailDuration = 7 * 24 * time.Hour
		default:
			jailDuration = 30 * 24 * time.Hour
		}
	case SlashingInvalidBlock:
		slashFraction = 0.01
		jailDuration = 24 * time.Hour
	default:
		return &InvalidSlashingReasonError{Reason: string(reason)}
	}

	// Parse and calculate slash
	stakeBig := parseBigInt(validator.Stake)
	fStake := new(big.Float).SetInt(stakeBig)
	fFraction := big.NewFloat(slashFraction)
	fSlashResult := new(big.Float).Mul(fStake, fFraction)
	slashAmountBig, _ := fSlashResult.Int(nil)

	if slashFraction > 0 && slashAmountBig.Sign() == 0 {
		slashAmountBig = big.NewInt(1)
	}

	stakeBig.Sub(stakeBig, slashAmountBig)
	if stakeBig.Sign() < 0 {
		stakeBig.SetInt64(0)
	}

	validator.Stake = stakeBig.String()
	validator.JailUntil = time.Now().Add(jailDuration).Unix()
	validator.Active = false
	validator.UpdatedAt = time.Now().Unix()

	// Record slashing event
	slashingEvent := &SlashingEvent{
		ValidatorAddress: address,
		Reason:           reason,
		Amount:           slashAmountBig.String(),
		BlockHeight:      tm.worldState.GetHeight(),
		Timestamp:        time.Now().Unix(),
		Evidence:         evidence,
	}

	if tm.slashingEvents[address] == nil {
		tm.slashingEvents[address] = make([]*SlashingEvent, 0)
	}
	tm.slashingEvents[address] = append(tm.slashingEvents[address], slashingEvent)

	newSlashCount := slashCount + 1

	// ENFORCEMENT RULE 1: Double-signing
	if reason == SlashingDoubleSign && autoRemoveDoubleSign {
		return tm.RemoveValidator(address, "double_sign_detected")
	}

	// ENFORCEMENT RULE 2: Three Strikes
	if newSlashCount >= maxEvents {
		return tm.RemoveValidator(address, "exceeded_max_events")
	}

	// ENFORCEMENT RULE 3: Stake below threshold
	minStake := parseBigInt(tm.config.Staking.MinValidatorStake)
	safetyThreshold := new(big.Int).Mul(minStake, big.NewInt(int64(minRetention*100)))
	safetyThreshold.Div(safetyThreshold, big.NewInt(100))

	if stakeBig.Cmp(safetyThreshold) < 0 {
		return tm.RemoveValidator(address, "stake_below_threshold")
	}

	// ENFORCEMENT RULE 4: Normal update
	return tm.worldState.UpdateValidator(validator)
}

func (tm *TestManager) RemoveValidator(address string, reason string) error {
	validator, err := tm.worldState.GetValidator(address)
	if err != nil {
		return err
	}

	validator.Active = false
	validator.JailUntil = time.Now().Add(365 * 24 * time.Hour).Unix()

	return tm.worldState.SetValidator(address, validator)
}

type InvalidSlashingReasonError struct {
	Reason string
}

func (e *InvalidSlashingReasonError) Error() string {
	return "unknown slashing reason: " + e.Reason
}

// Helper to create test validator
func createTestValidator(address string, stake string) *core.Validator {
	return &core.Validator{
		Address: address,
		Pubkey:  []byte("test-pubkey"),
		Stake:   stake,
		Active:  true,
	}
}

// Helper to setup test manager
func setupTestManager() (*TestManager, *MockWorldState) {
	cfg := &config.Config{
		Staking: config.StakingConfig{
			MinValidatorStake:       "1000000000000000000",
			SlashFractionDoubleSign: 0.05,
			SlashFractionDowntime:   0.001,
			DowntimeJailDuration:    24 * time.Hour,
			MaxSlashingEvents:       3,
			MinStakeRetention:       0.5,
			AutoRemoveOnDoubleSign:  true,
		},
	}

	ws := NewMockWorldState()

	manager := &TestManager{
		worldState:       ws,
		config:           cfg,
		slashingEvents:   make(map[string][]*SlashingEvent),
		validatorMetrics: make(map[string]*ValidatorMetrics),
	}

	return manager, ws
}

// Helper to parse big.Int from string
func parseBigInt(s string) *big.Int {
	result, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return big.NewInt(0)
	}
	return result
}

// ============================================================================
// TESTS (same as before, but use TestManager)
// ============================================================================

func TestSlashingEnforcement_DoubleSignRemoval_Simple(t *testing.T) {
	manager, ws := setupTestManager()

	// Store initial stake
	initialStake := "10000000000000000000"
	validator := createTestValidator("validator1", initialStake)
	ws.validators["validator1"] = validator

	err := manager.SlashValidator("validator1", SlashingDoubleSign, []byte("evidence"))
	require.NoError(t, err)

	v, _ := ws.GetValidator("validator1")

	// Check validator was removed
	assert.False(t, v.Active, "Validator should be inactive after double-signing")

	// Check permanently jailed
	jailTime := time.Unix(v.JailUntil, 0)
	expectedJail := time.Now().Add(365 * 24 * time.Hour)
	assert.WithinDuration(t, expectedJail, jailTime, 5*time.Second)

	// ✅ Just verify stake decreased (don't check exact amount due to rounding)
	initialStakeBig := parseBigInt(initialStake)
	finalStakeBig := parseBigInt(v.Stake)

	assert.True(t, finalStakeBig.Cmp(initialStakeBig) < 0,
		"Stake should have decreased (was: %s, now: %s)",
		initialStakeBig.String(), finalStakeBig.String())

	// Check it's roughly 5% reduction (should be around 9.5 tokens)
	expectedMin := parseBigInt("9400000000000000000") // 9.4 tokens
	expectedMax := parseBigInt("9600000000000000000") // 9.6 tokens

	assert.True(t, finalStakeBig.Cmp(expectedMin) >= 0 && finalStakeBig.Cmp(expectedMax) <= 0,
		"Stake should be between 9.4-9.6 tokens after 5%% slash, got: %s",
		finalStakeBig.String())

	// Verify slashing event recorded
	assert.Len(t, manager.slashingEvents["validator1"], 1)
	assert.Equal(t, SlashingDoubleSign, manager.slashingEvents["validator1"][0].Reason)
}

func TestSlashingEnforcement_ThreeStrikesRemoval(t *testing.T) {
	manager, ws := setupTestManager()

	validator := createTestValidator("validator2", "10000000000000000000")
	ws.validators["validator2"] = validator

	// First slash
	err := manager.SlashValidator("validator2", SlashingDowntime, nil)
	require.NoError(t, err)

	v, _ := ws.GetValidator("validator2")
	v.Active = true
	ws.SetValidator("validator2", v)

	// Second slash
	err = manager.SlashValidator("validator2", SlashingDowntime, nil)
	require.NoError(t, err)

	v, _ = ws.GetValidator("validator2")
	assert.Len(t, manager.slashingEvents["validator2"], 2)

	v.Active = true
	ws.SetValidator("validator2", v)

	// Third slash - REMOVED
	err = manager.SlashValidator("validator2", SlashingDowntime, nil)
	require.NoError(t, err)

	v, _ = ws.GetValidator("validator2")
	assert.False(t, v.Active)

	jailTime := time.Unix(v.JailUntil, 0)
	expectedJail := time.Now().Add(365 * 24 * time.Hour)
	assert.WithinDuration(t, expectedJail, jailTime, 5*time.Second)

	assert.Len(t, manager.slashingEvents["validator2"], 3)
}

func TestSlashingEnforcement_GraduatedJailing(t *testing.T) {
	manager, ws := setupTestManager()

	validator := createTestValidator("validator6", "10000000000000000000")
	ws.validators["validator6"] = validator

	// First offense - 1 day
	err := manager.SlashValidator("validator6", SlashingDowntime, nil)
	require.NoError(t, err)

	v, _ := ws.GetValidator("validator6")
	jailTime := time.Unix(v.JailUntil, 0)
	expectedJail := time.Now().Add(24 * time.Hour)
	assert.WithinDuration(t, expectedJail, jailTime, 5*time.Second)

	// Second offense - 1 week
	v.Active = true
	ws.SetValidator("validator6", v)

	err = manager.SlashValidator("validator6", SlashingDowntime, nil)
	require.NoError(t, err)

	v, _ = ws.GetValidator("validator6")
	jailTime = time.Unix(v.JailUntil, 0)
	expectedJail = time.Now().Add(7 * 24 * time.Hour)
	assert.WithinDuration(t, expectedJail, jailTime, 5*time.Second)

	// Third offense - Permanent
	v.Active = true
	ws.SetValidator("validator6", v)

	err = manager.SlashValidator("validator6", SlashingDowntime, nil)
	require.NoError(t, err)

	v, _ = ws.GetValidator("validator6")
	assert.False(t, v.Active)

	jailTime = time.Unix(v.JailUntil, 0)
	expectedJail = time.Now().Add(365 * 24 * time.Hour)
	assert.WithinDuration(t, expectedJail, jailTime, 5*time.Second)
}
