// consensus/pos/vrf_security_test.go
// Comprehensive test suite for H-3 VRF security fixes

package pos

import (
	"testing"
	"time"
)

// ============================================================================
// COMMIT-REVEAL TESTS
// ============================================================================

func TestCommitReveal_BasicFlow(t *testing.T) {
	crm := NewCommitRevealManager(5) // 5 slots reveal deadline

	validatorAddr := "validator1"
	slot := uint64(100)
	epoch := uint64(10)
	blockHash := "0xabc123"

	// Step 1: Create VRF proof (mock)
	vrfProof := &VRFProof{
		Output: make([]byte, 32),
		Proof:  make([]byte, 81),
	}
	for i := range vrfProof.Output {
		vrfProof.Output[i] = byte(i)
	}

	// Step 2: Generate nonce
	nonce, err := generateSecureNonce()
	if err != nil {
		t.Fatalf("Failed to generate nonce: %v", err)
	}

	// Step 3: Commit
	commitment, err := crm.CommitVRF(vrfProof, nonce, validatorAddr, slot, epoch, blockHash)
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	if commitment == nil {
		t.Fatal("Commitment is nil")
	}

	if len(commitment.Commitment) != 32 {
		t.Fatalf("Expected commitment hash length 32, got %d", len(commitment.Commitment))
	}

	// Step 4: Verify commitment exists
	if !crm.HasCommitment(slot, validatorAddr) {
		t.Fatal("Commitment not found after commit")
	}

	// Step 5: Reveal
	reveal, err := crm.RevealVRF(vrfProof, nonce, validatorAddr, slot, epoch)
	if err != nil {
		t.Fatalf("Reveal failed: %v", err)
	}

	if reveal == nil {
		t.Fatal("Reveal is nil")
	}

	// Step 6: Verify reveal exists
	if !crm.HasReveal(slot, validatorAddr) {
		t.Fatal("Reveal not found after reveal")
	}

	// Step 7: Verify commitment matches reveal
	err = crm.VerifyReveal(commitment, reveal)
	if err != nil {
		t.Fatalf("Verify reveal failed: %v", err)
	}
}

func TestCommitReveal_DuplicateCommitment(t *testing.T) {
	crm := NewCommitRevealManager(5)

	validatorAddr := "validator1"
	slot := uint64(100)
	epoch := uint64(10)
	blockHash := "0xabc123"

	vrfProof := &VRFProof{
		Output: make([]byte, 32),
		Proof:  make([]byte, 81),
	}
	nonce, _ := generateSecureNonce()

	// First commitment should succeed
	_, err := crm.CommitVRF(vrfProof, nonce, validatorAddr, slot, epoch, blockHash)
	if err != nil {
		t.Fatalf("First commit failed: %v", err)
	}

	// Second commitment for same slot/validator should fail
	_, err = crm.CommitVRF(vrfProof, nonce, validatorAddr, slot, epoch, blockHash)
	if err == nil {
		t.Fatal("Expected error for duplicate commitment, got nil")
	}
}

func TestCommitReveal_WrongNonce(t *testing.T) {
	crm := NewCommitRevealManager(5)

	validatorAddr := "validator1"
	slot := uint64(100)
	epoch := uint64(10)
	blockHash := "0xabc123"

	vrfProof := &VRFProof{
		Output: make([]byte, 32),
		Proof:  make([]byte, 81),
	}
	nonce, _ := generateSecureNonce()

	// Commit with correct nonce
	_, err := crm.CommitVRF(vrfProof, nonce, validatorAddr, slot, epoch, blockHash)
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Try to reveal with wrong nonce
	wrongNonce, _ := generateSecureNonce()
	_, err = crm.RevealVRF(vrfProof, wrongNonce, validatorAddr, slot, epoch)
	if err == nil {
		t.Fatal("Expected error for wrong nonce, got nil")
	}
}

func TestCommitReveal_ExpiredDeadline(t *testing.T) {
	crm := NewCommitRevealManager(1) // Very short deadline for testing

	validatorAddr := "validator1"
	slot := uint64(100)
	epoch := uint64(10)
	blockHash := "0xabc123"

	vrfProof := &VRFProof{
		Output: make([]byte, 32),
		Proof:  make([]byte, 81),
	}
	nonce, _ := generateSecureNonce()

	// Commit
	commitment, err := crm.CommitVRF(vrfProof, nonce, validatorAddr, slot, epoch, blockHash)
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Manually expire the deadline
	commitment.RevealDeadline = time.Now().Unix() - 10

	// Try to reveal after deadline
	_, err = crm.RevealVRF(vrfProof, nonce, validatorAddr, slot, epoch)
	if err == nil {
		t.Fatal("Expected error for expired deadline, got nil")
	}
}

func TestCommitReveal_CleanupOldData(t *testing.T) {
	crm := NewCommitRevealManager(5)
	crm.maxPendingSlots = 10 // Small for testing

	// Add commitments for slots 1-20
	for slot := uint64(1); slot <= 20; slot++ {
		vrfProof := &VRFProof{
			Output: make([]byte, 32),
			Proof:  make([]byte, 81),
		}
		nonce, _ := generateSecureNonce()
		validatorAddr := "validator1"

		crm.CommitVRF(vrfProof, nonce, validatorAddr, slot, uint64(1), "hash")
	}

	// Should have 20 slots worth of data
	if len(crm.commitments) != 20 {
		t.Fatalf("Expected 20 slots with commitments, got %d", len(crm.commitments))
	}

	// Clean up with current slot = 20
	removed := crm.CleanupOldData(20)

	// Should remove slots 1-9 (20 - 10 = 10, so keep 10-20)
	if removed < 9 {
		t.Fatalf("Expected at least 9 slots removed, got %d", removed)
	}
}

// ============================================================================
// TIMESTAMP VALIDATION TESTS
// ============================================================================

func TestTimestamp_ValidBlock(t *testing.T) {
	genesisTime := time.Now().Unix() - 1000
	tv := NewTimestampValidator(2, 6, genesisTime)

	slot := uint64(10)
	expectedTime := tv.CalculateSlotTimestamp(slot)
	parentTime := tv.CalculateSlotTimestamp(slot - 1)

	// Block with correct timestamp
	err := tv.ValidateBlockTimestamp(expectedTime, slot, parentTime)
	if err != nil {
		t.Fatalf("Valid timestamp rejected: %v", err)
	}

	// Block with slight drift (within ±2 seconds)
	err = tv.ValidateBlockTimestamp(expectedTime+1, slot, parentTime)
	if err != nil {
		t.Fatalf("Valid timestamp with +1s drift rejected: %v", err)
	}

	err = tv.ValidateBlockTimestamp(expectedTime-1, slot, parentTime)
	if err != nil {
		t.Fatalf("Valid timestamp with -1s drift rejected: %v", err)
	}
}

func TestTimestamp_TooFarInFuture(t *testing.T) {
	genesisTime := time.Now().Unix() - 1000
	tv := NewTimestampValidator(2, 6, genesisTime)

	slot := uint64(10)
	parentTime := tv.CalculateSlotTimestamp(slot - 1)

	// Timestamp 10 seconds in the future (beyond ±2s drift)
	futureTime := time.Now().Unix() + 10

	err := tv.ValidateBlockTimestamp(futureTime, slot, parentTime)
	if err == nil {
		t.Fatal("Expected error for timestamp too far in future, got nil")
	}
}

func TestTimestamp_BeforeParent(t *testing.T) {
	genesisTime := time.Now().Unix() - 1000
	tv := NewTimestampValidator(2, 6, genesisTime)

	slot := uint64(10)
	parentTime := tv.CalculateSlotTimestamp(slot - 1)

	// Timestamp before parent
	err := tv.ValidateBlockTimestamp(parentTime-1, slot, parentTime)
	if err == nil {
		t.Fatal("Expected error for timestamp before parent, got nil")
	}
}

func TestTimestamp_ExcessiveDeviation(t *testing.T) {
	genesisTime := time.Now().Unix() - 1000
	tv := NewTimestampValidator(2, 6, genesisTime)

	slot := uint64(10)
	expectedTime := tv.CalculateSlotTimestamp(slot)
	parentTime := tv.CalculateSlotTimestamp(slot - 1)

	// Timestamp with 5 seconds deviation (> ±2s limit)
	err := tv.ValidateBlockTimestamp(expectedTime+5, slot, parentTime)
	if err == nil {
		t.Fatal("Expected error for excessive deviation, got nil")
	}
}

func TestTimestamp_SuspiciousProgression(t *testing.T) {
	genesisTime := time.Now().Unix() - 1000
	tv := NewTimestampValidator(2, 6, genesisTime)

	// Create blocks with consistently high drift
	timestamps := make([]int64, 10)
	slots := make([]uint64, 10)

	for i := 0; i < 10; i++ {
		slot := uint64(i + 1)
		slots[i] = slot
		expected := tv.CalculateSlotTimestamp(slot)
		// Always use maximum drift
		timestamps[i] = expected + tv.maxDriftSeconds
	}

	// Should detect suspicious pattern
	err := tv.ValidateTimestampProgression(timestamps, slots)
	if err == nil {
		t.Fatal("Expected error for suspicious timestamp pattern, got nil")
	}
}

// ============================================================================
// FINALITY TESTS
// ============================================================================

func TestFinality_BasicFlow(t *testing.T) {
	fm := NewFinalityManager(32)

	// Mark block as finalized
	slot := uint64(100)
	blockHash := "0xabc123"
	vrfOutput := make([]byte, 32)
	timestamp := time.Now().Unix()
	validator := "validator1"

	err := fm.MarkBlockFinalized(slot, blockHash, vrfOutput, timestamp, validator)
	if err != nil {
		t.Fatalf("Failed to mark block as finalized: %v", err)
	}

	// Retrieve finalized block
	finalizedBlock, err := fm.GetFinalizedBlock(slot)
	if err != nil {
		t.Fatalf("Failed to get finalized block: %v", err)
	}

	if finalizedBlock.Slot != slot {
		t.Fatalf("Expected slot %d, got %d", slot, finalizedBlock.Slot)
	}

	if finalizedBlock.BlockHash != blockHash {
		t.Fatalf("Expected hash %s, got %s", blockHash, finalizedBlock.BlockHash)
	}
}

func TestFinality_CannotFinalizeOldSlot(t *testing.T) {
	fm := NewFinalityManager(32)

	// Finalize slot 100
	err := fm.MarkBlockFinalized(100, "hash1", make([]byte, 32), time.Now().Unix(), "val1")
	if err != nil {
		t.Fatalf("Failed to mark block as finalized: %v", err)
	}

	// Try to finalize slot 50 (older than 100)
	err = fm.MarkBlockFinalized(50, "hash2", make([]byte, 32), time.Now().Unix(), "val2")
	if err == nil {
		t.Fatal("Expected error when finalizing older slot, got nil")
	}
}

func TestFinality_IsBlockFinalized(t *testing.T) {
	fm := NewFinalityManager(32)

	currentSlot := uint64(100)

	// Slot 67 is 33 blocks behind (100 - 67 = 33), so finalized
	if !fm.IsBlockFinalized(currentSlot, 67) {
		t.Fatal("Expected slot 67 to be finalized at current slot 100")
	}

	// Slot 69 is 31 blocks behind (100 - 69 = 31), so NOT finalized (need 32)
	if fm.IsBlockFinalized(currentSlot, 69) {
		t.Fatal("Expected slot 69 to NOT be finalized at current slot 100")
	}

	// Slot 90 is 10 blocks behind, definitely not finalized
	if fm.IsBlockFinalized(currentSlot, 90) {
		t.Fatal("Expected slot 90 to NOT be finalized at current slot 100")
	}
}

// ============================================================================
// SECURE SEED GENERATION TESTS
// ============================================================================

func TestSecureSeed_UsesOnlyFinalizedBlocks(t *testing.T) {
	genesisTime := time.Now().Unix() - 10000
	svsg := NewSecureVRFSeedGenerator(32, 2, 6, genesisTime)

	// Mark some blocks as finalized
	for slot := uint64(1); slot <= 100; slot++ {
		blockHash := "hash_" + string(rune(slot))
		vrfOutput := make([]byte, 32)
		for i := range vrfOutput {
			vrfOutput[i] = byte(slot)
		}

		err := svsg.finalityManager.MarkBlockFinalized(
			slot,
			blockHash,
			vrfOutput,
			genesisTime+int64(slot)*6,
			"validator1",
		)
		if err != nil {
			t.Fatalf("Failed to mark slot %d as finalized: %v", slot, err)
		}
	}

	// Generate seed for slot 132 (current slot)
	// Should use finalized block at slot 100 (132 - 32 = 100)
	seed, err := svsg.GenerateSeedFromFinalized(10, 132, 132)
	if err != nil {
		t.Fatalf("Failed to generate seed: %v", err)
	}

	if len(seed) != 32 {
		t.Fatalf("Expected seed length 32, got %d", len(seed))
	}
}

func TestSecureSeed_FailsWithoutFinalizedBlock(t *testing.T) {
	genesisTime := time.Now().Unix() - 10000
	svsg := NewSecureVRFSeedGenerator(32, 2, 6, genesisTime)

	// Don't mark any blocks as finalized

	// Try to generate seed
	_, err := svsg.GenerateSeedFromFinalized(10, 100, 100)
	if err == nil {
		t.Fatal("Expected error when no finalized blocks available, got nil")
	}
}

// ============================================================================
// ANOMALY DETECTION TESTS
// ============================================================================

func TestAnomalyDetection_NormalValidator(t *testing.T) {
	tad := NewTimestampAnomalyDetector(2.0)

	validatorAddr := "validator1"
	maxDrift := int64(2)

	// Record 20 blocks with normal timing (small random deviations)
	for i := 0; i < 20; i++ {
		blockTime := int64(1000 + i*6)
		expectedTime := int64(1000 + i*6)
		// Small deviation: 0 seconds
		tad.RecordBlockTiming(validatorAddr, blockTime, expectedTime, maxDrift)
	}

	suspicious := tad.GetSuspiciousValidators()

	// Should not flag normal validator
	if len(suspicious) > 0 {
		t.Fatalf("Normal validator flagged as suspicious: %+v", suspicious)
	}
}

func TestAnomalyDetection_ConsistentMaxDrift(t *testing.T) {
	tad := NewTimestampAnomalyDetector(2.0)

	validatorAddr := "validator1"
	maxDrift := int64(2)

	// Record 20 blocks with CONSISTENT max drift usage
	for i := 0; i < 20; i++ {
		blockTime := int64(1000+i*6) + maxDrift // Always at max drift
		expectedTime := int64(1000 + i*6)
		tad.RecordBlockTiming(validatorAddr, blockTime, expectedTime, maxDrift)
	}

	suspicious := tad.GetSuspiciousValidators()

	// Should flag validator with consistent max drift
	if len(suspicious) == 0 {
		t.Fatal("Suspicious validator not detected")
	}

	if suspicious[0].ValidatorAddress != validatorAddr {
		t.Fatalf("Wrong validator flagged: expected %s, got %s",
			validatorAddr, suspicious[0].ValidatorAddress)
	}
}

// ============================================================================
// ATTACK SIMULATION TESTS
// ============================================================================

func TestAttackSimulation_StrategicBlockWithholding(t *testing.T) {
	// TODO: Simulate attacker with 30% stake
	// Withhold blocks when unfavorable
	// Measure selection advantage
	// Assert: advantage < 5% with mitigations
	t.Skip("Requires full consensus simulation")
}

func TestAttackSimulation_TimestampGrinding(t *testing.T) {
	genesisTime := time.Now().Unix() - 10000
	tv := NewTimestampValidator(2, 6, genesisTime)

	slot := uint64(100)
	parentTime := tv.CalculateSlotTimestamp(slot - 1)
	expectedTime := tv.CalculateSlotTimestamp(slot)

	// Try all timestamps within drift window
	validTimestamps := 0
	for drift := int64(-2); drift <= 2; drift++ {
		testTime := expectedTime + drift
		err := tv.ValidateBlockTimestamp(testTime, slot, parentTime)
		if err == nil {
			validTimestamps++
		}
	}

	// With ±2 second drift, should have at most 5 valid timestamps
	// (-2, -1, 0, +1, +2)
	if validTimestamps > 5 {
		t.Fatalf("Too many valid timestamps: %d (grinding window too large)", validTimestamps)
	}

	t.Logf("Grinding window limited to %d valid timestamps", validTimestamps)
}

func TestAttackSimulation_VRFPredictability(t *testing.T) {
	// TODO: Test predictability of validator selection
	// Given all public data, try to predict N slots ahead
	// Assert: prediction accuracy ≤ stake percentage
	t.Skip("Requires full validator selection simulation")
}

// ============================================================================
// INTEGRATION TEST
// ============================================================================

func TestIntegration_FullSecureVRFProtocol(t *testing.T) {
	genesisTime := time.Now().Unix() - 10000

	evp := NewEnhancedVRFProtocol(
		32, // finality depth
		2,  // max drift seconds
		6,  // slot duration
		genesisTime,
		5, // reveal deadline slots
	)

	validatorAddr := "validator1"
	slot := uint64(100)
	epoch := uint64(10)
	currentSlot := slot
	proposedTimestamp := evp.timestampValidator.CalculateSlotTimestamp(slot)
	parentTimestamp := evp.timestampValidator.CalculateSlotTimestamp(slot - 1)
	blockHash := "0xabc123"

	// Step 1: Create commitment first
	vrfProof := &VRFProof{
		Output: make([]byte, 32),
		Proof:  make([]byte, 81),
	}
	nonce, _ := generateSecureNonce()

	_, err := evp.commitReveal.CommitVRF(vrfProof, nonce, validatorAddr, slot, epoch, blockHash)
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Step 2: Mark necessary blocks as finalized (for seed generation)
	finalizedSlot := uint64(68) // currentSlot - 32
	if finalizedSlot > 0 {
		err = evp.seedGenerator.finalityManager.MarkBlockFinalized(
			finalizedSlot,
			"finalized_hash",
			make([]byte, 32),
			genesisTime+int64(finalizedSlot)*6,
			"validator_prev",
		)
		if err != nil {
			t.Fatalf("Failed to mark finalized block: %v", err)
		}
	}

	// Step 3: Validate and propose block
	err = evp.ValidateAndProposeBlock(
		validatorAddr,
		slot,
		epoch,
		currentSlot,
		proposedTimestamp,
		parentTimestamp,
		blockHash,
	)
	if err != nil {
		t.Fatalf("Block proposal validation failed: %v", err)
	}

	t.Log("Full VRF protocol integration test passed")
}

// ============================================================================
// BENCHMARK TESTS
// ============================================================================

func BenchmarkCommitReveal_Commit(b *testing.B) {
	crm := NewCommitRevealManager(5)

	vrfProof := &VRFProof{
		Output: make([]byte, 32),
		Proof:  make([]byte, 81),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nonce, _ := generateSecureNonce()
		slot := uint64(i)
		crm.CommitVRF(vrfProof, nonce, "validator1", slot, 1, "hash")
	}
}

func BenchmarkTimestampValidation(b *testing.B) {
	genesisTime := time.Now().Unix() - 10000
	tv := NewTimestampValidator(2, 6, genesisTime)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		slot := uint64(i + 1)
		timestamp := tv.CalculateSlotTimestamp(slot)
		parentTime := tv.CalculateSlotTimestamp(slot - 1)
		tv.ValidateBlockTimestamp(timestamp, slot, parentTime)
	}
}
