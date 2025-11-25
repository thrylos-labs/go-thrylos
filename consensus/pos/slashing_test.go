package pos

// import (
// 	"testing"
// 	"time"

// 	core "github.com/thrylos-labs/go-thrylos/proto/core"
// )

// // MockWorldState for testing
// type MockWorldState struct {
// 	balances map[string]int64
// }

// func NewMockWorldState() *MockWorldState {
// 	return &MockWorldState{
// 		balances: make(map[string]int64),
// 	}
// }

// func (m *MockWorldState) GetBalance(address string) (int64, error) {
// 	balance, exists := m.balances[address]
// 	if !exists {
// 		return 0, nil
// 	}
// 	return balance, nil
// }

// func (m *MockWorldState) UpdateBalance(address string, newBalance int64) error {
// 	m.balances[address] = newBalance
// 	return nil
// }

// func (m *MockWorldState) SetBalance(address string, balance int64) {
// 	m.balances[address] = balance
// }

// // Test helper to create attestation
// func createTestAttestation(validatorAddress string, epoch uint64, blockHash string) *Attestation {
// 	return &Attestation{
// 		ValidatorAddress: validatorAddress,
// 		BlockHash:        blockHash,
// 		BlockHeight:      int64(epoch * 100),
// 		Epoch:            epoch,
// 		Slot:             epoch * 32,
// 		Signature:        []byte("test_signature"),
// 		Timestamp:        time.Now().Unix(),
// 	}
// }

// // Test 1: Double Voting Detection
// func TestDoubleVoting(t *testing.T) {
// 	worldState := NewMockWorldState()
// 	worldState.SetBalance("validator1", 10000)

// 	config := DefaultSlashingConfig()
// 	sm := NewSlashingManager(config, worldState)

// 	// First attestation for epoch 10
// 	att1 := createTestAttestation("validator1", 10, "block_hash_A")
// 	err := sm.ProcessAttestation(att1)
// 	if err != nil {
// 		t.Fatalf("First attestation should not cause error: %v", err)
// 	}

// 	// Second attestation for same epoch but different block (DOUBLE VOTE)
// 	att2 := createTestAttestation("validator1", 10, "block_hash_B")
// 	err = sm.ProcessAttestation(att2)
// 	// Note: ProcessAttestation returns nil when slashing succeeds
// 	// It only returns an error if the validator is jailed/inactive

// 	// Verify slashing was applied by checking balance
// 	balance, _ := worldState.GetBalance("validator1")
// 	expectedBalance := int64(10000 - (10000 * int(config.DoubleVotingPenalty) / 100))
// 	if balance != expectedBalance {
// 		t.Errorf("Expected balance %d after slashing, got %d", expectedBalance, balance)
// 	}

// 	// Check validator is jailed
// 	if !sm.isValidatorJailed("validator1") {
// 		t.Error("Validator should be jailed after double voting")
// 	}

// 	// Check slashing record exists
// 	records := sm.GetSlashingRecords("validator1")
// 	if len(records) != 1 {
// 		t.Errorf("Expected 1 slashing record, got %d", len(records))
// 	}
// 	if len(records) > 0 && records[0].Condition != DoubleVoting {
// 		t.Error("Slashing condition should be DoubleVoting")
// 	}
// }

// // Test 2: Valid Sequential Attestations (No Slashing)
// func TestValidAttestations(t *testing.T) {
// 	worldState := NewMockWorldState()
// 	worldState.SetBalance("validator3", 10000)

// 	config := DefaultSlashingConfig()
// 	sm := NewSlashingManager(config, worldState)

// 	// Process multiple valid attestations with different epochs
// 	for i := uint64(1); i <= 10; i++ {
// 		att := createTestAttestation("validator3", i, "block_hash")
// 		err := sm.ProcessAttestation(att)
// 		if err != nil {
// 			t.Fatalf("Valid attestation %d should not cause error: %v", i, err)
// 		}
// 	}

// 	// Balance should remain unchanged
// 	balance, _ := worldState.GetBalance("validator3")
// 	if balance != 10000 {
// 		t.Errorf("Balance should remain 10000, got %d", balance)
// 	}

// 	// No slashing records
// 	records := sm.GetSlashingRecords("validator3")
// 	if len(records) != 0 {
// 		t.Errorf("Should have no slashing records, got %d", len(records))
// 	}

// 	// Validator should be active
// 	if !sm.IsValidatorActive("validator3") {
// 		t.Error("Validator should be active")
// 	}
// }

// // Test 3: Downtime Slashing
// func TestDowntimeSlashing(t *testing.T) {
// 	worldState := NewMockWorldState()
// 	worldState.SetBalance("validator4", 10000)

// 	config := DefaultSlashingConfig()
// 	config.MaxMissedAttestations = 10 // Slash after 10 missed
// 	sm := NewSlashingManager(config, worldState)

// 	// Report missed attestations
// 	for i := uint64(1); i <= 10; i++ {
// 		sm.ReportMissedAttestation("validator4", i)
// 	}

// 	// Check slashing was applied
// 	balance, _ := worldState.GetBalance("validator4")
// 	expectedBalance := int64(10000 - (10000 * int(config.DowntimePenalty) / 100))
// 	if balance != expectedBalance {
// 		t.Errorf("Expected balance %d after downtime slashing, got %d", expectedBalance, balance)
// 	}

// 	// Check slashing record
// 	records := sm.GetSlashingRecords("validator4")
// 	if len(records) != 1 {
// 		t.Errorf("Expected 1 slashing record, got %d", len(records))
// 	}
// 	if records[0].Condition != Downtime {
// 		t.Error("Slashing condition should be Downtime")
// 	}
// }

// // Test 4: Invalid Proposal Slashing
// func TestInvalidProposalSlashing(t *testing.T) {
// 	worldState := NewMockWorldState()
// 	worldState.SetBalance("validator5", 10000)

// 	config := DefaultSlashingConfig()
// 	sm := NewSlashingManager(config, worldState)

// 	// Create invalid proposal
// 	proposal := &BlockProposal{
// 		Block:     &core.Block{},
// 		Proposer:  "validator5",
// 		Epoch:     10,
// 		Slot:      320,
// 		Signature: []byte("test_sig"),
// 	}

// 	// Report invalid proposal
// 	err := sm.ReportInvalidProposal(proposal, "Invalid state root")
// 	if err != nil {
// 		t.Fatalf("ReportInvalidProposal should not error: %v", err)
// 	}

// 	// Check slashing was applied
// 	balance, _ := worldState.GetBalance("validator5")
// 	expectedBalance := int64(10000 - (10000 * int(config.InvalidProposalPenalty) / 100))
// 	if balance != expectedBalance {
// 		t.Errorf("Expected balance %d after invalid proposal slashing, got %d", expectedBalance, balance)
// 	}

// 	// Check slashing record
// 	records := sm.GetSlashingRecords("validator5")
// 	if len(records) != 1 {
// 		t.Errorf("Expected 1 slashing record, got %d", len(records))
// 	}
// 	if records[0].Condition != InvalidProposal {
// 		t.Error("Slashing condition should be InvalidProposal")
// 	}
// }

// // Test 5: Validator Jail Release
// func TestValidatorJailRelease(t *testing.T) {
// 	worldState := NewMockWorldState()
// 	worldState.SetBalance("validator6", 10000)

// 	config := DefaultSlashingConfig()
// 	config.JailDuration = 100 * time.Millisecond // Short jail for testing
// 	sm := NewSlashingManager(config, worldState)

// 	// Cause double voting to get jailed
// 	att1 := createTestAttestation("validator6", 10, "block_hash_A")
// 	sm.ProcessAttestation(att1)

// 	att2 := createTestAttestation("validator6", 10, "block_hash_B")
// 	sm.ProcessAttestation(att2) // This will jail the validator

// 	// Validator should be jailed
// 	if !sm.isValidatorJailed("validator6") {
// 		t.Fatal("Validator should be jailed")
// 	}

// 	// Wait for jail duration
// 	time.Sleep(150 * time.Millisecond)

// 	// Validator should be released
// 	if sm.isValidatorJailed("validator6") {
// 		t.Error("Validator should be released after jail duration")
// 	}
// }

// // Test 6: Multiple Validators Independence
// func TestMultipleValidatorsIndependence(t *testing.T) {
// 	worldState := NewMockWorldState()
// 	worldState.SetBalance("validator7", 10000)
// 	worldState.SetBalance("validator8", 10000)

// 	config := DefaultSlashingConfig()
// 	sm := NewSlashingManager(config, worldState)

// 	// Validator 7 double votes
// 	att1 := createTestAttestation("validator7", 10, "block_hash_A")
// 	sm.ProcessAttestation(att1)
// 	att2 := createTestAttestation("validator7", 10, "block_hash_B")
// 	sm.ProcessAttestation(att2) // Slashed

// 	// Validator 8 votes normally
// 	att3 := createTestAttestation("validator8", 10, "block_hash_A")
// 	err := sm.ProcessAttestation(att3)
// 	if err != nil {
// 		t.Error("Validator 8 should not be affected by validator 7's slashing")
// 	}

// 	// Check validator 7 is slashed
// 	balance7, _ := worldState.GetBalance("validator7")
// 	if balance7 >= 10000 {
// 		t.Error("Validator 7 should be slashed")
// 	}

// 	// Check validator 8 is not slashed
// 	balance8, _ := worldState.GetBalance("validator8")
// 	if balance8 != 10000 {
// 		t.Error("Validator 8 should not be slashed")
// 	}
// }

// // Test 7: Prevent Double Slashing
// func TestPreventDoubleSlashing(t *testing.T) {
// 	worldState := NewMockWorldState()
// 	worldState.SetBalance("validator9", 10000)

// 	config := DefaultSlashingConfig()
// 	sm := NewSlashingManager(config, worldState)

// 	// First double vote
// 	att1 := createTestAttestation("validator9", 10, "block_hash_A")
// 	sm.ProcessAttestation(att1)
// 	att2 := createTestAttestation("validator9", 10, "block_hash_B")
// 	sm.ProcessAttestation(att2) // First slash

// 	balance1, _ := worldState.GetBalance("validator9")

// 	// Try to process the same evidence again
// 	sm.ProcessAttestation(att1)
// 	sm.ProcessAttestation(att2)

// 	balance2, _ := worldState.GetBalance("validator9")

// 	// Balance should be the same (no double slashing)
// 	if balance1 != balance2 {
// 		t.Error("Validator should not be slashed twice for the same offense")
// 	}

// 	// Should only have one slashing record
// 	records := sm.GetSlashingRecords("validator9")
// 	if len(records) != 1 {
// 		t.Errorf("Expected 1 slashing record, got %d", len(records))
// 	}
// }

// // Test 8: Minimum Stake Enforcement
// func TestMinimumStakeEnforcement(t *testing.T) {
// 	worldState := NewMockWorldState()
// 	worldState.SetBalance("validator10", 2000)

// 	config := DefaultSlashingConfig()
// 	config.MinimumStake = 1000
// 	sm := NewSlashingManager(config, worldState)

// 	// Validator should be active initially
// 	if !sm.IsValidatorActive("validator10") {
// 		t.Fatal("Validator should be active with sufficient stake")
// 	}

// 	// Cause slashing that brings balance below minimum
// 	proposal := &BlockProposal{
// 		Block:     &core.Block{},
// 		Proposer:  "validator10",
// 		Epoch:     10,
// 		Slot:      320,
// 		Signature: []byte("test_sig"),
// 	}
// 	sm.ReportInvalidProposal(proposal, "test")

// 	balance, _ := worldState.GetBalance("validator10")

// 	// If balance is now below minimum, validator should not be active
// 	if balance < config.MinimumStake {
// 		if sm.IsValidatorActive("validator10") {
// 			t.Error("Validator should not be active with insufficient stake")
// 		}
// 	}
// }

// // Test 9: Attestation History Tracking
// func TestAttestationHistoryTracking(t *testing.T) {
// 	worldState := NewMockWorldState()
// 	worldState.SetBalance("validator11", 10000)

// 	config := DefaultSlashingConfig()
// 	sm := NewSlashingManager(config, worldState)

// 	// Process some attestations
// 	for i := uint64(1); i <= 5; i++ {
// 		att := createTestAttestation("validator11", i, "block_hash")
// 		sm.ProcessAttestation(att)
// 	}

// 	// Report some misses
// 	for i := uint64(6); i <= 8; i++ {
// 		sm.ReportMissedAttestation("validator11", i)
// 	}

// 	// Check history exists
// 	history, exists := sm.attestationHistory["validator11"]
// 	if !exists {
// 		t.Fatal("Attestation history should exist")
// 	}

// 	// Check total slots and missed slots
// 	if history.TotalSlots != 8 {
// 		t.Errorf("Expected 8 total slots, got %d", history.TotalSlots)
// 	}
// 	if history.MissedSlots != 3 {
// 		t.Errorf("Expected 3 missed slots, got %d", history.MissedSlots)
// 	}

// 	// Check miss rate
// 	missRate := history.GetMissRate()
// 	expectedRate := (3.0 / 8.0) * 100
// 	if missRate != expectedRate {
// 		t.Errorf("Expected miss rate %.2f%%, got %.2f%%", expectedRate, missRate)
// 	}
// }

// // Test 10: Jailed Validator Cannot Attest
// func TestJailedValidatorCannotAttest(t *testing.T) {
// 	worldState := NewMockWorldState()
// 	worldState.SetBalance("validator12", 10000)

// 	config := DefaultSlashingConfig()
// 	sm := NewSlashingManager(config, worldState)

// 	// Get validator jailed
// 	att1 := createTestAttestation("validator12", 10, "block_hash_A")
// 	sm.ProcessAttestation(att1)
// 	att2 := createTestAttestation("validator12", 10, "block_hash_B")
// 	sm.ProcessAttestation(att2) // Gets jailed

// 	// Try to attest while jailed
// 	att3 := createTestAttestation("validator12", 11, "block_hash_C")
// 	err := sm.ProcessAttestation(att3)
// 	if err == nil {
// 		t.Error("Jailed validator should not be able to attest")
// 	}
// }

// // Test 11: Get Jailed Validators
// func TestGetJailedValidators(t *testing.T) {
// 	worldState := NewMockWorldState()
// 	worldState.SetBalance("validator13", 10000)
// 	worldState.SetBalance("validator14", 10000)

// 	config := DefaultSlashingConfig()
// 	sm := NewSlashingManager(config, worldState)

// 	// Jail validator 13
// 	att1 := createTestAttestation("validator13", 10, "block_hash_A")
// 	sm.ProcessAttestation(att1)
// 	att2 := createTestAttestation("validator13", 10, "block_hash_B")
// 	sm.ProcessAttestation(att2)

// 	// Jail validator 14
// 	att3 := createTestAttestation("validator14", 10, "block_hash_C")
// 	sm.ProcessAttestation(att3)
// 	att4 := createTestAttestation("validator14", 10, "block_hash_D")
// 	sm.ProcessAttestation(att4)

// 	// Get jailed validators
// 	jailed := sm.GetJailedValidators()
// 	if len(jailed) != 2 {
// 		t.Errorf("Expected 2 jailed validators, got %d", len(jailed))
// 	}
// }

// // Benchmark: Attestation Processing Performance
// func BenchmarkAttestationProcessing(b *testing.B) {
// 	worldState := NewMockWorldState()
// 	worldState.SetBalance("validator_bench", 10000)

// 	config := DefaultSlashingConfig()
// 	sm := NewSlashingManager(config, worldState)

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		att := createTestAttestation("validator_bench", uint64(i), "block_hash")
// 		sm.ProcessAttestation(att)
// 	}
// }

// // Benchmark: Double Voting Detection
// func BenchmarkDoubleVotingDetection(b *testing.B) {
// 	worldState := NewMockWorldState()
// 	config := DefaultSlashingConfig()
// 	sm := NewSlashingManager(config, worldState)

// 	// Pre-populate with many attestations
// 	for i := 0; i < 1000; i++ {
// 		validatorKey := "validator_bench"
// 		worldState.SetBalance(validatorKey, 10000)
// 		att := createTestAttestation(validatorKey, uint64(i), "block_hash")
// 		sm.ProcessAttestation(att)
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		att := createTestAttestation("validator_bench", 500, "different_hash")
// 		sm.ProcessAttestation(att)
// 	}
// }
