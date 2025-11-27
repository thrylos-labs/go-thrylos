// consensus/pos/withholding_test.go

package pos

import (
	"testing"

	"github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
	"github.com/thrylos-labs/go-thrylos/types"
)

// 1. Mock World State for Testing
type MockWorldStateForWithholding struct {
	balances   map[string]int64
	validators map[string]*core.Validator
}

func (m *MockWorldStateForWithholding) GetBalance(address string) (int64, error) {
	if bal, ok := m.balances[address]; ok {
		return bal, nil
	}
	return 0, nil
}

func (m *MockWorldStateForWithholding) UpdateBalance(address string, newBalance int64) error {
	m.balances[address] = newBalance
	return nil
}

// Test 1: Verify logic resets on success
func TestWithholding_ResetOnSuccess(t *testing.T) {
	// Setup Engine
	engine := &ConsensusEngine{
		validatorActivity: make(map[string]*ValidatorActivity),
	}

	validator := "val_test_1"

	// Simulate 5 misses
	for i := 0; i < 5; i++ {
		engine.updateValidatorActivity(validator, false) // false = no block produced
	}

	activity := engine.validatorActivity[validator]
	if activity.MissedProposals != 5 {
		t.Fatalf("Expected 5 missed proposals, got %d", activity.MissedProposals)
	}

	// Simulate 1 Success
	engine.updateValidatorActivity(validator, true) // true = block produced

	if activity.MissedProposals != 0 {
		t.Errorf("Expected MissedProposals to reset to 0 after success, got %d", activity.MissedProposals)
	}
	t.Log("✅ Counter correctly resets on successful proposal")
}

// Test 2: Verify Slashing Trigger
func TestWithholding_SlashingTrigger(t *testing.T) {
	// 1. Setup Mock State
	validatorAddr := "val_slasher"
	initialBalance := int64(1000)

	mockState := &MockWorldStateForWithholding{
		balances: map[string]int64{validatorAddr: initialBalance},
	}

	// 2. Setup Slashing Manager
	slashingConfig := &storage.SlashingConfig{
		SlashingDowntime:      5, // 5% penalty
		JailDurationHours:     1,
		MaxMissedAttestations: 100, // Not used here directly but required for init
	}

	slashingManager := NewSlashingManager(slashingConfig, mockState, nil)

	// 3. Setup Engine
	engine := &ConsensusEngine{
		validatorActivity: make(map[string]*ValidatorActivity),
		slashingManager:   slashingManager,
	}

	// 4. Run the test: Miss 9 times (Safe zone)
	for i := 0; i < 9; i++ {
		engine.updateValidatorActivity(validatorAddr, false)
	}

	// Check pre-conditions
	if mockState.balances[validatorAddr] != initialBalance {
		t.Fatal("Validator should not be slashed yet (9 misses)")
	}
	if slashingManager.isValidatorJailed(validatorAddr) {
		t.Fatal("Validator should not be jailed yet")
	}

	// 5. The 10th Miss (Trigger zone)
	engine.updateValidatorActivity(validatorAddr, false)

	// 6. Assertions

	// Check 1: Balance slashed?
	expectedPenalty := initialBalance * int64(slashingConfig.SlashingDowntime) / 100 // 5% of 1000 = 50
	expectedBalance := initialBalance - expectedPenalty

	if mockState.balances[validatorAddr] != expectedBalance {
		t.Errorf("Slashing failed. Expected balance %d, got %d", expectedBalance, mockState.balances[validatorAddr])
	}

	// Check 2: Validator Jailed?
	if !slashingManager.isValidatorJailed(validatorAddr) {
		t.Error("Validator was not jailed after 10 consecutive misses")
	}

	// Check 3: Counter Reset?
	// The logic resets the counter after slashing to prevent double-dipping immediately
	if engine.validatorActivity[validatorAddr].MissedProposals != 0 {
		t.Error("MissedProposals counter did not reset after slashing")
	}

	t.Logf("✅ Validator correctly slashed %d coins and jailed after 10 misses", expectedPenalty)
}

// Test 3: Integration with Slashing Evidence
func TestWithholding_GeneratesEvidence(t *testing.T) {
	// Setup basic mocks
	validatorAddr := "val_evidence"
	mockState := &MockWorldStateForWithholding{
		balances: map[string]int64{validatorAddr: 1000},
	}
	sm := NewSlashingManager(nil, mockState, nil)

	// Call the specific report function directly
	err := sm.ReportBlockWithholding(validatorAddr)
	if err != nil {
		t.Fatalf("ReportBlockWithholding failed: %v", err)
	}

	// Check if record exists
	records := sm.GetSlashingRecords(validatorAddr)
	if len(records) == 0 {
		t.Fatal("No slashing record created")
	}

	record := records[0]
	if record.Condition != types.Downtime {
		t.Errorf("Expected condition 'Downtime', got %v", record.Condition)
	}

	// Verify Evidence Type
	if record.Evidence.MissedSlots == nil {
		t.Error("Evidence structure is missing details")
	}

	t.Log("✅ Slashing record created successfully for block withholding")
}
