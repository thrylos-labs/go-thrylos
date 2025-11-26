package pos

import (
	"fmt"
	"testing"

	"github.com/thrylos-labs/go-thrylos/storage"
)

// MockWorldState implements WorldStateBalancer for testing
type MockWorldState struct {
	balances map[string]int64
}

func NewMockWorldState() *MockWorldState {
	return &MockWorldState{
		balances: make(map[string]int64),
	}
}

func (m *MockWorldState) GetBalance(address string) (int64, error) {
	if bal, ok := m.balances[address]; ok {
		return bal, nil
	}
	return 0, fmt.Errorf("account not found")
}

func (m *MockWorldState) UpdateBalance(address string, newBalance int64) error {
	m.balances[address] = newBalance
	return nil
}

func (m *MockWorldState) SetBalance(address string, amount int64) {
	m.balances[address] = amount
}

func TestProgressiveDowntimePolicy(t *testing.T) {
	// 1. Setup Configuration
	// We set MaxMissedAttestations to 100 for easy math
	config := &storage.SlashingConfig{
		MaxMissedAttestations: 100,
		SlashingDowntime:      5,  // 5% penalty
		JailDurationHours:     24, // 24 hours
		MinimumStake:          1000,
	}

	// 2. Setup Mock Dependencies
	ws := NewMockWorldState()
	validator := "0xValidator1"
	initialBalance := int64(1000000) // 1M tokens
	ws.SetBalance(validator, initialBalance)

	// 3. Initialize Manager
	// Note: NewSlashingManager calculates thresholds based on MaxMissedAttestations (100)
	// Warning (5%), Minor (10%), Major (20%), Jail (50%), Eject (100%)
	sm := NewSlashingManager(config, ws, nil)

	t.Log("🔍 Starting Progressive Downtime Test")
	t.Logf("Thresholds -> Warning: %d, Minor: %d, Major: %d, Jail: %d, Eject: %d",
		sm.policy.WarningThreshold, sm.policy.MinorSlashingStart, sm.policy.MajorSlashingStart,
		sm.policy.JailThreshold, sm.policy.EjectionThreshold)

	// --- PHASE 1: Normal Operation (0-4 Misses) ---
	for i := 1; i < 5; i++ {
		sm.ReportMissedAttestation(validator, uint64(i))
	}

	// Assert: No slashing yet
	recs := sm.GetSlashingRecords(validator)
	if len(recs) != 0 {
		t.Fatalf("❌ Should have 0 slashing records before threshold, got %d", len(recs))
	}
	t.Log("✅ Phase 1 Passed: Safe zone")

	// --- PHASE 2: Warning Threshold (Miss #5) ---
	sm.ReportMissedAttestation(validator, 5)
	// Warning logs to console, but no state change expected yet
	t.Log("✅ Phase 2 Passed: Warning zone (Console check only)")

	// --- PHASE 3: Minor Slashing (Miss #10) ---
	// Miss 6-9
	for i := 6; i < 10; i++ {
		sm.ReportMissedAttestation(validator, uint64(i))
	}

	// Miss 10 -> Should trigger Minor Slashing
	sm.ReportMissedAttestation(validator, 10)

	recs = sm.GetSlashingRecords(validator)
	if len(recs) == 0 {
		t.Fatal("❌ Phase 3 Failed: Should have triggered Minor Slashing at 10 misses")
	}
	lastRec := recs[len(recs)-1]
	if lastRec.SlashedAmount == 0 {
		t.Fatal("❌ Phase 3 Failed: Slashed amount should be > 0")
	}
	t.Logf("✅ Phase 3 Passed: Minor Slashing triggered. Balance reduced by %d", lastRec.SlashedAmount)

	// --- PHASE 4: Major Slashing (Miss #20) ---
	// Fast forward to 19
	for i := 11; i < 20; i++ {
		sm.ReportMissedAttestation(validator, uint64(i))
	}

	// Miss 20 -> Should trigger Major Slashing
	currentRecCount := len(recs)
	sm.ReportMissedAttestation(validator, 20)

	recs = sm.GetSlashingRecords(validator)
	if len(recs) <= currentRecCount {
		t.Fatal("❌ Phase 4 Failed: Should have triggered Major Slashing record")
	}
	t.Log("✅ Phase 4 Passed: Major Slashing triggered")

	// --- PHASE 5: Jail (Miss #50) ---
	// Fast forward to 49
	for i := 21; i < 50; i++ {
		sm.ReportMissedAttestation(validator, uint64(i))
	}

	// Miss 50 -> Jail
	sm.ReportMissedAttestation(validator, 50)

	if !sm.isValidatorJailed(validator) {
		t.Fatal("❌ Phase 5 Failed: Validator should be jailed at 50 misses")
	}
	status := sm.GetValidatorStatus(validator)
	if status != storage.ValidatorJailed {
		t.Fatalf("❌ Phase 5 Failed: Status should be ValidatorJailed, got %v", status)
	}
	t.Log("✅ Phase 5 Passed: Validator Jailed")

	// --- PHASE 6: Ejection (Miss #100) ---
	// Even though jailed, let's simulate the system somehow recording misses
	// (or the jail time expired but they kept missing)

	// Unjail manually for test to allow misses to continue counting towards ejection
	delete(sm.jailedValidators, validator)

	// Fast forward to 99
	for i := 51; i < 100; i++ {
		sm.ReportMissedAttestation(validator, uint64(i))
	}

	// Miss 100 -> Ejection
	sm.ReportMissedAttestation(validator, 100)

	status = sm.GetValidatorStatus(validator)
	if status != storage.ValidatorSlashed {
		t.Fatalf("❌ Phase 6 Failed: Status should be ValidatorSlashed (Ejected), got %v", status)
	}

	// Verify final balance check
	finalBal, _ := ws.GetBalance(validator)
	if finalBal >= initialBalance {
		t.Fatal("❌ Final Balance check failed: Balance should have decreased significantly")
	}

	t.Logf("✅ Phase 6 Passed: Validator Ejected. Final Balance: %d (Started: %d)", finalBal, initialBalance)
}
