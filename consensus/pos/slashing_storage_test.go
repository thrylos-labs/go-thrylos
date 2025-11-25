// consensus/pos/slashing_storage_test.go
// Tests for slashing data persistence

package pos

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thrylos-labs/go-thrylos/storage"
	"github.com/thrylos-labs/go-thrylos/types"
)

// TestSlashingStoragePersistence tests that data survives database close/reopen
func TestSlashingStoragePersistence(t *testing.T) {
	// Create temporary directory
	tmpDir := filepath.Join(os.TempDir(), "slashing-storage-test")
	os.RemoveAll(tmpDir) // Clean start
	defer os.RemoveAll(tmpDir)

	validatorAddr := "test-validator-persistence"

	// === PHASE 1: Write data ===
	t.Log("Phase 1: Writing data to storage...")

	// Create storage
	badgerStorage, err := storage.NewBadgerStorage(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	slashingStorage := storage.NewSlashingStorage(badgerStorage.GetDB())

	// Save a jailed validator
	jailTime := time.Now()
	jail := &storage.JailedValidator{
		ValidatorAddress: validatorAddr,
		JailTime:         jailTime,
		ReleaseTime:      jailTime.Add(7 * 24 * time.Hour),
		Reason:           types.DoubleVoting,
	}

	if err := slashingStorage.SaveJailedValidator(validatorAddr, jail); err != nil {
		t.Fatalf("Failed to save jailed validator: %v", err)
	}
	t.Log("✅ Saved jailed validator")

	// Save processed evidence
	evidenceHash := "test-evidence-hash-123"
	if err := slashingStorage.SaveProcessedEvidence(evidenceHash); err != nil {
		t.Fatalf("Failed to save evidence: %v", err)
	}
	t.Log("✅ Saved processed evidence")

	// Save validator status
	if err := slashingStorage.SaveValidatorStatus(validatorAddr, storage.ValidatorJailed); err != nil {
		t.Fatalf("Failed to save validator status: %v", err)
	}
	t.Log("✅ Saved validator status")

	// Save slashing record
	record := &types.SlashingRecord{
		ValidatorAddress: validatorAddr,
		Condition:        types.DoubleVoting,
		Epoch:            100,
		Timestamp:        time.Now(),
		Evidence:         types.SlashingEvidence{},
		SlashedAmount:    5000,
		Reason:           "Test double voting",
	}

	if err := slashingStorage.SaveSlashingRecord(validatorAddr, record); err != nil {
		t.Fatalf("Failed to save slashing record: %v", err)
	}
	t.Log("✅ Saved slashing record")

	// Close storage (simulates node restart)
	badgerStorage.Close()
	t.Log("💾 Closed storage (simulating restart)")

	// === PHASE 2: Reopen and verify ===
	t.Log("\nPhase 2: Reopening storage and verifying data...")

	// Reopen storage
	badgerStorage2, err := storage.NewBadgerStorage(tmpDir)
	if err != nil {
		t.Fatalf("Failed to reopen storage: %v", err)
	}
	defer badgerStorage2.Close()

	slashingStorage2 := storage.NewSlashingStorage(badgerStorage2.GetDB())

	// Verify jailed validator
	loadedJail, err := slashingStorage2.GetJailedValidator(validatorAddr)
	if err != nil {
		t.Fatalf("Failed to load jailed validator: %v", err)
	}
	if loadedJail == nil {
		t.Fatal("Jailed validator not found after restart")
	}
	if loadedJail.ValidatorAddress != validatorAddr {
		t.Errorf("Wrong validator address: got %s, want %s", loadedJail.ValidatorAddress, validatorAddr)
	}
	if loadedJail.Reason != types.DoubleVoting {
		t.Errorf("Wrong jail reason: got %v, want %v", loadedJail.Reason, types.DoubleVoting)
	}
	t.Log("✅ Jailed validator persisted correctly")

	// Verify processed evidence
	isProcessed, err := slashingStorage2.IsEvidenceProcessed(evidenceHash)
	if err != nil {
		t.Fatalf("Failed to check evidence: %v", err)
	}
	if !isProcessed {
		t.Error("Evidence not marked as processed after restart")
	}
	t.Log("✅ Processed evidence persisted correctly")

	// Verify validator status
	status, err := slashingStorage2.GetValidatorStatus(validatorAddr)
	if err != nil {
		t.Fatalf("Failed to get validator status: %v", err)
	}
	if status != storage.ValidatorJailed {
		t.Errorf("Wrong validator status: got %v, want %v", status, storage.ValidatorJailed)
	}
	t.Log("✅ Validator status persisted correctly")

	// Verify slashing record
	records, err := slashingStorage2.GetSlashingRecords(validatorAddr)
	if err != nil {
		t.Fatalf("Failed to get slashing records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("Expected 1 slashing record, got %d", len(records))
	}
	if records[0].ValidatorAddress != validatorAddr {
		t.Errorf("Wrong validator in record: got %s, want %s", records[0].ValidatorAddress, validatorAddr)
	}
	if records[0].SlashedAmount != 5000 {
		t.Errorf("Wrong slashed amount: got %d, want 5000", records[0].SlashedAmount)
	}
	t.Log("✅ Slashing record persisted correctly")

	t.Log("\n🎉 All persistence checks passed! Data survived restart.")
}

// TestSlashingStorageMultipleValidators tests handling multiple validators
func TestSlashingStorageMultipleValidators(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "slashing-multi-test")
	os.RemoveAll(tmpDir)
	defer os.RemoveAll(tmpDir)

	badgerStorage, err := storage.NewBadgerStorage(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer badgerStorage.Close()

	slashingStorage := storage.NewSlashingStorage(badgerStorage.GetDB())

	// Jail 3 validators
	validators := []string{"validator-1", "validator-2", "validator-3"}
	for _, addr := range validators {
		jail := &storage.JailedValidator{
			ValidatorAddress: addr,
			JailTime:         time.Now(),
			ReleaseTime:      time.Now().Add(7 * 24 * time.Hour),
			Reason:           types.DoubleVoting,
		}
		if err := slashingStorage.SaveJailedValidator(addr, jail); err != nil {
			t.Fatalf("Failed to save validator %s: %v", addr, err)
		}
	}
	t.Log("✅ Saved 3 jailed validators")

	// Load all jailed validators
	allJailed, err := slashingStorage.GetAllJailedValidators()
	if err != nil {
		t.Fatalf("Failed to get all jailed validators: %v", err)
	}

	if len(allJailed) != 3 {
		t.Errorf("Expected 3 jailed validators, got %d", len(allJailed))
	}

	// Verify each one exists
	for _, addr := range validators {
		if _, exists := allJailed[addr]; !exists {
			t.Errorf("Validator %s not found in jailed list", addr)
		}
	}
	t.Log("✅ All 3 validators retrieved correctly")

	// Release one validator
	if err := slashingStorage.DeleteJailedValidator("validator-2"); err != nil {
		t.Fatalf("Failed to delete validator: %v", err)
	}
	t.Log("✅ Released validator-2")

	// Verify only 2 remain
	allJailed2, err := slashingStorage.GetAllJailedValidators()
	if err != nil {
		t.Fatalf("Failed to get jailed validators: %v", err)
	}

	if len(allJailed2) != 2 {
		t.Errorf("Expected 2 jailed validators after release, got %d", len(allJailed2))
	}

	if _, exists := allJailed2["validator-2"]; exists {
		t.Error("validator-2 should have been released")
	}

	t.Log("🎉 Multi-validator test passed!")
}

// TestSlashingStorageLoadAll tests the LoadAllSlashingData method
func TestSlashingStorageLoadAll(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "slashing-load-all-test")
	os.RemoveAll(tmpDir)
	defer os.RemoveAll(tmpDir)

	badgerStorage, err := storage.NewBadgerStorage(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer badgerStorage.Close()

	slashingStorage := storage.NewSlashingStorage(badgerStorage.GetDB())

	// Save some data
	jail := &storage.JailedValidator{
		ValidatorAddress: "validator-1",
		JailTime:         time.Now(),
		ReleaseTime:      time.Now().Add(7 * 24 * time.Hour),
		Reason:           types.DoubleVoting,
	}
	slashingStorage.SaveJailedValidator("validator-1", jail)

	slashingStorage.SaveProcessedEvidence("evidence-1")
	slashingStorage.SaveProcessedEvidence("evidence-2")

	slashingStorage.SaveValidatorStatus("validator-1", storage.ValidatorJailed)
	slashingStorage.SaveValidatorStatus("validator-2", storage.ValidatorActive)

	t.Log("✅ Saved test data")

	// Load all data
	data, err := slashingStorage.LoadAllSlashingData()
	if err != nil {
		t.Fatalf("Failed to load all data: %v", err)
	}

	// Verify counts
	if len(data.JailedValidators) != 1 {
		t.Errorf("Expected 1 jailed validator, got %d", len(data.JailedValidators))
	}

	if len(data.ProcessedEvidence) != 2 {
		t.Errorf("Expected 2 evidence records, got %d", len(data.ProcessedEvidence))
	}

	if len(data.ValidatorStatuses) != 2 {
		t.Errorf("Expected 2 validator statuses, got %d", len(data.ValidatorStatuses))
	}

	t.Log("✅ Loaded all data correctly:")
	t.Logf("   - %d jailed validators", len(data.JailedValidators))
	t.Logf("   - %d evidence records", len(data.ProcessedEvidence))
	t.Logf("   - %d validator statuses", len(data.ValidatorStatuses))

	t.Log("🎉 LoadAll test passed!")
}
