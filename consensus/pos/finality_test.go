// consensus/pos/finality_test.go
// Tests for finalization and justification logic

package pos

import (
	"fmt"
	"testing"

	"github.com/thrylos-labs/go-thrylos/config"
)

// TestCheckJustification tests the justification logic
func TestCheckJustification(t *testing.T) {
	cfg := &config.Config{}
	mockState := NewMockWorldState()

	// Setup validators with enough stake for quorum
	mockState.AddValidator("val1", 4000, true)
	mockState.AddValidator("val2", 3000, true)
	mockState.AddValidator("val3", 3000, true)
	// Total: 10,000 stake

	fc := NewForkChoice(cfg, mockState)

	// Test justification at epoch 10
	epoch := uint64(10)
	blockHash := "block_to_justify"

	// Add attestations to reach quorum (7000 > 6667)
	fc.ProcessAttestation(&Attestation{
		ValidatorAddress: "val1",
		BlockHash:        blockHash,
		Epoch:            epoch,
		Slot:             320,
	})
	fc.ProcessAttestation(&Attestation{
		ValidatorAddress: "val2",
		BlockHash:        blockHash,
		Epoch:            epoch,
		Slot:             320,
	})

	// Check justification
	t.Run("Block becomes justified with quorum", func(t *testing.T) {
		attestingStake := fc.GetAttestingStake(blockHash)
		totalStake := fc.getTotalActiveStake()

		fc.checkJustification(epoch, blockHash, attestingStake, totalStake)

		justified := fc.GetJustifiedCheckpoint()
		if justified == nil {
			t.Fatal("Expected justified checkpoint, got nil")
		}

		if justified.BlockHash != blockHash {
			t.Errorf("Expected justified block %s, got %s", blockHash, justified.BlockHash)
		}

		if justified.Epoch != epoch {
			t.Errorf("Expected epoch %d, got %d", epoch, justified.Epoch)
		}

		t.Logf("✅ Block justified at epoch %d with %d stake", epoch, justified.AttestingStake)
	})

	// Test that justified checkpoint is stored
	t.Run("Justified checkpoint is retrievable", func(t *testing.T) {
		checkpoint := fc.GetJustifiedCheckpoint()

		if checkpoint.BlockHash != blockHash {
			t.Error("Justified checkpoint not stored correctly")
		}

		t.Logf("✅ Justified checkpoint stored: epoch=%d, block=%s",
			checkpoint.Epoch, checkpoint.BlockHash)
	})
}

// TestCheckFinalization tests the finalization logic
func TestCheckFinalization(t *testing.T) {
	cfg := &config.Config{}
	mockState := NewMockWorldState()

	mockState.AddValidator("val1", 4000, true)
	mockState.AddValidator("val2", 3000, true)
	mockState.AddValidator("val3", 3000, true)

	fc := NewForkChoice(cfg, mockState)

	epoch10Block := "block_epoch_10"
	epoch11Block := "block_epoch_11"
	epoch12Block := "block_epoch_12"

	// Justify block at epoch 10
	t.Run("Setup: Justify epoch 10", func(t *testing.T) {
		fc.ProcessAttestation(&Attestation{
			ValidatorAddress: "val1",
			BlockHash:        epoch10Block,
			Epoch:            10,
			Slot:             320,
		})
		fc.ProcessAttestation(&Attestation{
			ValidatorAddress: "val2",
			BlockHash:        epoch10Block,
			Epoch:            10,
			Slot:             320,
		})

		attestingStake := fc.GetAttestingStake(epoch10Block)
		totalStake := fc.getTotalActiveStake()
		fc.checkJustification(10, epoch10Block, attestingStake, totalStake)

		if fc.GetJustifiedCheckpoint() == nil {
			t.Fatal("Failed to justify epoch 10")
		}
		t.Logf("✅ Epoch 10 justified")
	})

	// Justify block at epoch 11
	t.Run("Setup: Justify epoch 11", func(t *testing.T) {
		fc.ProcessAttestation(&Attestation{
			ValidatorAddress: "val1",
			BlockHash:        epoch11Block,
			Epoch:            11,
			Slot:             352,
		})
		fc.ProcessAttestation(&Attestation{
			ValidatorAddress: "val2",
			BlockHash:        epoch11Block,
			Epoch:            11,
			Slot:             352,
		})

		attestingStake := fc.GetAttestingStake(epoch11Block)
		totalStake := fc.getTotalActiveStake()
		fc.checkJustification(11, epoch11Block, attestingStake, totalStake)

		justified := fc.GetJustifiedCheckpoint()
		if justified.Epoch != 11 {
			t.Fatal("Failed to justify epoch 11")
		}
		t.Logf("✅ Epoch 11 justified")
	})

	// Now finalize epoch 10 (when epoch 12 is justified)
	t.Run("Finalize epoch 10 with epoch 12 justification", func(t *testing.T) {
		fc.ProcessAttestation(&Attestation{
			ValidatorAddress: "val1",
			BlockHash:        epoch12Block,
			Epoch:            12,
			Slot:             384,
		})
		fc.ProcessAttestation(&Attestation{
			ValidatorAddress: "val2",
			BlockHash:        epoch12Block,
			Epoch:            12,
			Slot:             384,
		})

		attestingStake := fc.GetAttestingStake(epoch12Block)
		totalStake := fc.getTotalActiveStake()
		fc.checkJustification(12, epoch12Block, attestingStake, totalStake)
		fc.checkFinalization() // No arguments!

		finalized := fc.GetFinalizedCheckpoint()
		if finalized == nil {
			t.Fatal("Expected finalized checkpoint, got nil")
		}

		if finalized.Epoch != 10 {
			t.Errorf("Expected finalized epoch 10, got %d", finalized.Epoch)
		}

		t.Logf("✅ Epoch 10 finalized at epoch 12")
	})
}

// TestIsBlockFinalized tests the finalization check
func TestIsBlockFinalized(t *testing.T) {
	cfg := &config.Config{}
	mockState := NewMockWorldState()
	mockState.AddValidator("val1", 10000, true)

	fc := NewForkChoice(cfg, mockState)

	blockHash := "finalized_block"
	epoch := uint64(10)

	// Block is not finalized initially
	t.Run("Block not finalized initially", func(t *testing.T) {
		if fc.IsBlockFinalized(blockHash) {
			t.Error("Block should not be finalized yet")
		}
	})

	// Manually set finalized checkpoint
	t.Run("Block finalized after checkpoint update", func(t *testing.T) {
		fc.UpdateFinalizedCheckpoint(epoch, blockHash)

		if !fc.IsBlockFinalized(blockHash) {
			t.Error("Block should be finalized")
		}

		t.Logf("✅ Block correctly identified as finalized")
	})

	// Different block is not finalized
	t.Run("Different block not finalized", func(t *testing.T) {
		if fc.IsBlockFinalized("different_block") {
			t.Error("Different block should not be finalized")
		}
	})
}

// TestCleanupOldEpochs tests epoch cleanup
func TestCleanupOldEpochs(t *testing.T) {
	cfg := &config.Config{}
	mockState := NewMockWorldState()
	mockState.AddValidator("val1", 5000, true)

	fc := NewForkChoice(cfg, mockState)

	// Add attestations across multiple epochs
	for epoch := uint64(1); epoch <= 20; epoch++ {
		blockHash := fmt.Sprintf("block_%d", epoch) // Fixed: proper string formatting
		fc.ProcessAttestation(&Attestation{
			ValidatorAddress: "val1",
			BlockHash:        blockHash,
			Epoch:            epoch,
			Slot:             epoch * 32,
		})
	}

	// Check that all epochs have data
	t.Run("All epochs have attestations", func(t *testing.T) {
		fc.mu.RLock()
		epochCount := len(fc.epochAttestations)
		fc.mu.RUnlock()

		if epochCount != 20 {
			t.Errorf("Expected 20 epochs, got %d", epochCount)
		}
		t.Logf("✅ Created attestations for %d epochs", epochCount)
	})

	// Finalize epoch 15
	t.Run("Finalize epoch 15", func(t *testing.T) {
		fc.UpdateFinalizedCheckpoint(15, "block_15")

		t.Log("✅ Epoch 15 finalized")
	})

	// Cleanup should remove epochs < 13 (finalized - 2)
	t.Run("Cleanup removes old epochs", func(t *testing.T) {
		fc.CleanupOldEpochs()

		fc.mu.RLock()
		epochCount := len(fc.epochAttestations)

		// Should keep epochs 13-20 (8 epochs)
		expectedEpochs := 8

		// Check specific epochs are removed
		for epoch := uint64(1); epoch < 13; epoch++ {
			if _, exists := fc.epochAttestations[epoch]; exists {
				t.Errorf("Epoch %d should have been cleaned up", epoch)
			}
		}

		// Check specific epochs are kept
		for epoch := uint64(13); epoch <= 20; epoch++ {
			if _, exists := fc.epochAttestations[epoch]; !exists {
				t.Errorf("Epoch %d should have been kept", epoch)
			}
		}

		fc.mu.RUnlock()

		if epochCount != expectedEpochs {
			t.Errorf("Expected %d epochs after cleanup, got %d", expectedEpochs, epochCount)
		}

		t.Logf("✅ Cleanup removed old epochs, kept %d recent epochs", epochCount)
	})
}

// TestGetFinalityStatus tests the finality status reporting
func TestGetFinalityStatus(t *testing.T) {
	cfg := &config.Config{}
	mockState := NewMockWorldState()
	mockState.AddValidator("val1", 10000, true)

	fc := NewForkChoice(cfg, mockState)

	// Set up justified and finalized checkpoints
	fc.UpdateJustifiedCheckpoint(15, "justified_block")
	fc.UpdateFinalizedCheckpoint(10, "finalized_block")

	// Get status
	status := fc.GetFinalityStatus()

	t.Run("Status contains justified info", func(t *testing.T) {
		justifiedEpoch, ok := status["justified_epoch"].(uint64)
		if !ok || justifiedEpoch != 15 {
			t.Errorf("Expected justified epoch 15, got %v", status["justified_epoch"])
		}

		justifiedBlock, ok := status["justified_block"].(string)
		if !ok || justifiedBlock != "justified_block" {
			t.Errorf("Expected justified block hash, got %v", status["justified_block"])
		}

		t.Logf("✅ Justified: epoch=%d, block=%s", justifiedEpoch, justifiedBlock)
	})

	t.Run("Status contains finalized info", func(t *testing.T) {
		finalizedEpoch, ok := status["finalized_epoch"].(uint64)
		if !ok || finalizedEpoch != 10 {
			t.Errorf("Expected finalized epoch 10, got %v", status["finalized_epoch"])
		}

		finalizedBlock, ok := status["finalized_block"].(string)
		if !ok || finalizedBlock != "finalized_block" {
			t.Errorf("Expected finalized block hash, got %v", status["finalized_block"])
		}

		t.Logf("✅ Finalized: epoch=%d, block=%s", finalizedEpoch, finalizedBlock)
	})

	t.Run("Status contains distance metrics", func(t *testing.T) {
		distance, ok := status["epochs_since_finalized"].(uint64)
		if !ok || distance != 5 { // 15 - 10 = 5
			t.Errorf("Expected distance 5, got %v", status["epochs_since_finalized"])
		}

		t.Logf("✅ Distance between finalized and justified: %d epochs", distance)
	})
}

// TestManualCheckpointUpdates tests manual checkpoint setting
func TestManualCheckpointUpdates(t *testing.T) {
	cfg := &config.Config{}
	mockState := NewMockWorldState()
	fc := NewForkChoice(cfg, mockState)

	t.Run("Update justified checkpoint manually", func(t *testing.T) {
		fc.UpdateJustifiedCheckpoint(20, "manual_justified")

		retrieved := fc.GetJustifiedCheckpoint()
		if retrieved.Epoch != 20 {
			t.Errorf("Expected epoch 20, got %d", retrieved.Epoch)
		}
		if retrieved.BlockHash != "manual_justified" {
			t.Errorf("Expected block hash manual_justified, got %s", retrieved.BlockHash)
		}

		t.Logf("✅ Manually updated justified checkpoint: epoch=%d", retrieved.Epoch)
	})

	t.Run("Update finalized checkpoint manually", func(t *testing.T) {
		fc.UpdateFinalizedCheckpoint(18, "manual_finalized")

		retrieved := fc.GetFinalizedCheckpoint()
		if retrieved.Epoch != 18 {
			t.Errorf("Expected epoch 18, got %d", retrieved.Epoch)
		}
		if retrieved.BlockHash != "manual_finalized" {
			t.Errorf("Expected block hash manual_finalized, got %s", retrieved.BlockHash)
		}

		t.Logf("✅ Manually updated finalized checkpoint: epoch=%d", retrieved.Epoch)
	})
}

// TestFinalityCasperFFG tests Casper FFG finality rules
func TestFinalityCasperFFG(t *testing.T) {
	cfg := &config.Config{}
	mockState := NewMockWorldState()
	mockState.AddValidator("val1", 7000, true)
	mockState.AddValidator("val2", 3000, true)

	fc := NewForkChoice(cfg, mockState)

	t.Run("Casper FFG: Justify adjacent epochs", func(t *testing.T) {
		// Epoch 10 justified
		fc.ProcessAttestation(&Attestation{
			ValidatorAddress: "val1",
			BlockHash:        "block_10",
			Epoch:            10,
			Slot:             320,
		})
		attestingStake := fc.GetAttestingStake("block_10")
		totalStake := fc.getTotalActiveStake()
		fc.checkJustification(10, "block_10", attestingStake, totalStake)

		if fc.GetJustifiedCheckpoint().Epoch != 10 {
			t.Fatal("Failed to justify epoch 10")
		}

		// Epoch 11 justified (child of epoch 10)
		fc.ProcessAttestation(&Attestation{
			ValidatorAddress: "val1",
			BlockHash:        "block_11",
			Epoch:            11,
			Slot:             352,
		})
		attestingStake = fc.GetAttestingStake("block_11")
		fc.checkJustification(11, "block_11", attestingStake, totalStake)

		if fc.GetJustifiedCheckpoint().Epoch != 11 {
			t.Fatal("Failed to justify epoch 11")
		}

		// Now when epoch 12 is justified, epoch 10 should finalize
		fc.ProcessAttestation(&Attestation{
			ValidatorAddress: "val1",
			BlockHash:        "block_12",
			Epoch:            12,
			Slot:             384,
		})
		attestingStake = fc.GetAttestingStake("block_12")
		fc.checkJustification(12, "block_12", attestingStake, totalStake)
		fc.checkFinalization() // No arguments!

		finalized := fc.GetFinalizedCheckpoint()
		if finalized == nil || finalized.Epoch != 10 {
			t.Errorf("Expected epoch 10 to be finalized, got %v", finalized)
		}

		t.Logf("✅ Casper FFG finality: epoch 10 finalized when epoch 12 justified")
	})
}

// BenchmarkCheckJustification benchmarks justification checking
func BenchmarkCheckJustification(b *testing.B) {
	cfg := &config.Config{}
	mockState := NewMockWorldState()
	mockState.AddValidator("val1", 10000, true)

	fc := NewForkChoice(cfg, mockState)

	// Add attestation
	fc.ProcessAttestation(&Attestation{
		ValidatorAddress: "val1",
		BlockHash:        "benchmark_block",
		Epoch:            10,
		Slot:             320,
	})

	attestingStake := fc.GetAttestingStake("benchmark_block")
	totalStake := fc.getTotalActiveStake()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fc.checkJustification(10, "benchmark_block", attestingStake, totalStake)
	}
}

// BenchmarkCheckFinalization benchmarks finalization checking
func BenchmarkCheckFinalization(b *testing.B) {
	cfg := &config.Config{}
	mockState := NewMockWorldState()
	mockState.AddValidator("val1", 10000, true)

	fc := NewForkChoice(cfg, mockState)

	// Setup justified checkpoints
	fc.UpdateJustifiedCheckpoint(10, "block_10")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fc.checkFinalization() // No arguments!
	}
}
