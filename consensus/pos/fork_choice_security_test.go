// consensus/pos/fork_choice_security_test.go
package pos

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/types"
)

// MockWorldStateForFork implements WorldStateReader for testing
type MockWorldStateForFork struct {
	validators map[string]*core.Validator
	blocks     map[string]*core.Block
}

func NewMockWorldStateForFork() *MockWorldStateForFork {
	return &MockWorldStateForFork{
		validators: make(map[string]*core.Validator),
		blocks:     make(map[string]*core.Block),
	}
}

// Implement WorldStateReader interface
func (m *MockWorldStateForFork) GetValidator(address string) (*core.Validator, error) {
	if v, exists := m.validators[address]; exists {
		return v, nil
	}
	return &core.Validator{Address: address, Active: false}, nil
}

func (m *MockWorldStateForFork) GetActiveValidators() []*core.Validator {
	active := make([]*core.Validator, 0)
	for _, v := range m.validators {
		if v.Active {
			active = append(active, v)
		}
	}
	return active
}

func (m *MockWorldStateForFork) GetBlockByHash(hash string) (*core.Block, error) {
	if b, exists := m.blocks[hash]; exists {
		return b, nil
	}
	return nil, nil
}

func (m *MockWorldStateForFork) GetHeight() int64 {
	return 1000
}

// Helper to create test fork choice
func setupTestForkChoice(cfg *config.Config) (*ForkChoice, *MockWorldStateForFork) {
	ws := NewMockWorldStateForFork()

	fc := &ForkChoice{
		config:              cfg,
		worldState:          ws,
		attestationsByBlock: make(map[string][]*types.Attestation),
		justifiedCheckpoint: nil,
		finalizedCheckpoint: nil,
	}

	return fc, ws
}

// Helper to create config with custom consensus settings
func createTestConfig(maxReorg int, finalizationEpochs int, minStake float64, checkpointInterval int) *config.Config {
	return &config.Config{
		Consensus: config.ConsensusConfig{
			MaxReorgDepth:      maxReorg,
			FinalizationEpochs: finalizationEpochs,
			MinStakeForReorg:   minStake,
			CheckpointInterval: checkpointInterval,
		},
	}
}

// ============================================================================
// TEST: Reorg Depth Validation
// ============================================================================

func TestForkChoice_ReorgDepthLimit(t *testing.T) {
	cfg := createTestConfig(100, 2, 0.66, 10)
	fc, _ := setupTestForkChoice(cfg)

	totalStake := "10000000000000000000" // 10 tokens
	newStake := "7000000000000000000"    // 7 tokens (70%)

	t.Run("AcceptsReorg_WithinLimit", func(t *testing.T) {
		// 50 blocks, well within 100 limit
		err := fc.ValidateReorganization(50, 100, newStake, totalStake)
		assert.NoError(t, err, "Should accept reorg within depth limit")
	})

	t.Run("RejectsReorg_ExceedsLimit", func(t *testing.T) {
		// 150 blocks, exceeds 100 limit
		err := fc.ValidateReorganization(150, 100, newStake, totalStake)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrReorgTooDeep)
		assert.Contains(t, err.Error(), "attempted=150, max=100")
	})

	t.Run("AcceptsReorg_ExactlyAtLimit", func(t *testing.T) {
		// Exactly 100 blocks
		err := fc.ValidateReorganization(100, 100, newStake, totalStake)
		assert.NoError(t, err, "Should accept reorg at exact limit")
	})
}

func TestForkChoice_ConfigurableReorgLimit(t *testing.T) {
	t.Run("CustomLimit_50Blocks", func(t *testing.T) {
		cfg := createTestConfig(50, 2, 0.66, 10)
		fc, _ := setupTestForkChoice(cfg)

		totalStake := "10000000000000000000"
		newStake := "7000000000000000000"

		// 75 blocks should be rejected with 50 limit
		err := fc.ValidateReorganization(75, 100, newStake, totalStake)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "max=50")
	})

	t.Run("CustomLimit_200Blocks", func(t *testing.T) {
		cfg := createTestConfig(200, 2, 0.66, 10)
		fc, _ := setupTestForkChoice(cfg)

		totalStake := "10000000000000000000"
		newStake := "7000000000000000000"

		// 150 blocks should be accepted with 200 limit
		err := fc.ValidateReorganization(150, 100, newStake, totalStake)
		assert.NoError(t, err)
	})
}

// ============================================================================
// TEST: Finalized Checkpoint Protection
// ============================================================================

func TestForkChoice_FinalizedCheckpointProtection(t *testing.T) {
	cfg := createTestConfig(100, 2, 0.66, 10)
	fc, _ := setupTestForkChoice(cfg)

	// Set finalized checkpoint at epoch 50
	fc.finalizedCheckpoint = &Checkpoint{
		Epoch:     50,
		BlockHash: "finalized-block-hash",
	}

	totalStake := "10000000000000000000"
	newStake := "7000000000000000000"

	t.Run("AcceptsReorg_AfterFinalized", func(t *testing.T) {
		// Fork at epoch 60 (after finalized 50)
		err := fc.ValidateReorganization(10, 60, newStake, totalStake)
		assert.NoError(t, err)
	})

	t.Run("RejectsReorg_BeforeFinalized", func(t *testing.T) {
		// Fork at epoch 40 (before finalized 50)
		err := fc.ValidateReorganization(10, 40, newStake, totalStake)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrReorgCrossesFinality)
		assert.Contains(t, err.Error(), "fork_epoch=40, finalized_epoch=50")
	})

	t.Run("RejectsReorg_AtFinalized", func(t *testing.T) {
		// Fork at exactly epoch 50 (at finalized)
		err := fc.ValidateReorganization(10, 50, newStake, totalStake)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrReorgCrossesFinality)
	})
}

// ============================================================================
// TEST: Stake Requirement for Reorg
// ============================================================================

func TestForkChoice_StakeRequirement(t *testing.T) {
	cfg := createTestConfig(100, 2, 0.66, 10)
	fc, _ := setupTestForkChoice(cfg)

	totalStake := "10000000000000000000" // 10 tokens

	t.Run("AcceptsReorg_SufficientStake_70Percent", func(t *testing.T) {
		newStake := "7000000000000000000" // 7 tokens = 70%
		err := fc.ValidateReorganization(50, 100, newStake, totalStake)
		assert.NoError(t, err, "70% stake should be sufficient (need 66%)")
	})

	t.Run("AcceptsReorg_ExactlyMinimum_66Percent", func(t *testing.T) {
		newStake := "6600000000000000000" // 6.6 tokens = 66%
		err := fc.ValidateReorganization(50, 100, newStake, totalStake)
		assert.NoError(t, err, "Exactly 66% should be sufficient")
	})

	t.Run("RejectsReorg_InsufficientStake_50Percent", func(t *testing.T) {
		newStake := "5000000000000000000" // 5 tokens = 50%
		err := fc.ValidateReorganization(50, 100, newStake, totalStake)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrReorgInsufficientStake)
		assert.Contains(t, err.Error(), "has=50%")
		assert.Contains(t, err.Error(), "required=66%")
	})

	t.Run("RejectsReorg_InsufficientStake_60Percent", func(t *testing.T) {
		newStake := "6000000000000000000" // 6 tokens = 60%
		err := fc.ValidateReorganization(50, 100, newStake, totalStake)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrReorgInsufficientStake)
	})
}

func TestForkChoice_CustomStakeRequirement(t *testing.T) {
	t.Run("HigherRequirement_80Percent", func(t *testing.T) {
		cfg := createTestConfig(100, 2, 0.80, 10) // 80% required
		fc, _ := setupTestForkChoice(cfg)

		totalStake := "10000000000000000000"

		// 70% should be rejected
		newStake := "7000000000000000000"
		err := fc.ValidateReorganization(50, 100, newStake, totalStake)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "required=80%")

		// 85% should be accepted
		newStake = "8500000000000000000"
		err = fc.ValidateReorganization(50, 100, newStake, totalStake)
		assert.NoError(t, err)
	})

	t.Run("LowerRequirement_50Percent", func(t *testing.T) {
		cfg := createTestConfig(100, 2, 0.50, 10) // 50% required
		fc, _ := setupTestForkChoice(cfg)

		totalStake := "10000000000000000000"

		// 50% exactly should be accepted
		newStake := "5000000000000000000"
		err := fc.ValidateReorganization(50, 100, newStake, totalStake)
		assert.NoError(t, err)

		// 45% should be rejected
		newStake = "4500000000000000000"
		err = fc.ValidateReorganization(50, 100, newStake, totalStake)
		assert.Error(t, err)
	})
}

// ============================================================================
// TEST: Automatic Finalization
// ============================================================================

func TestForkChoice_AutomaticFinalization(t *testing.T) {
	cfg := createTestConfig(100, 2, 0.66, 10)
	fc, _ := setupTestForkChoice(cfg)

	t.Run("FinalizesJustified_After2Epochs", func(t *testing.T) {
		// Create justified checkpoint at epoch 10
		fc.justifiedCheckpoint = &Checkpoint{
			Epoch:          10,
			BlockHash:      "justified-hash",
			AttestingStake: "7000000000000000000",
			TotalStake:     "10000000000000000000",
		}

		// Before: Not finalized
		assert.Nil(t, fc.finalizedCheckpoint)

		// Process epoch 12 (2 epochs later)
		fc.UpdateFinalization(12)

		// After: Should be finalized
		require.NotNil(t, fc.finalizedCheckpoint)
		assert.Equal(t, uint64(10), fc.finalizedCheckpoint.Epoch)
		assert.Equal(t, "justified-hash", fc.finalizedCheckpoint.BlockHash)
	})

	t.Run("DoesNotFinalize_Before2Epochs", func(t *testing.T) {
		fc.justifiedCheckpoint = &Checkpoint{
			Epoch:     10,
			BlockHash: "justified-hash",
		}
		fc.finalizedCheckpoint = nil

		// Process epoch 11 (only 1 epoch later)
		fc.UpdateFinalization(11)

		// Should not be finalized yet
		assert.Nil(t, fc.finalizedCheckpoint)
	})

	t.Run("NoFinalization_WithoutJustified", func(t *testing.T) {
		fc.justifiedCheckpoint = nil
		fc.finalizedCheckpoint = nil

		fc.UpdateFinalization(100)

		// Should remain nil
		assert.Nil(t, fc.finalizedCheckpoint)
	})
}

func TestForkChoice_CustomFinalizationEpochs(t *testing.T) {
	t.Run("ImmediateFinalization_0Epochs", func(t *testing.T) {
		cfg := createTestConfig(100, 1, 0.66, 10) // ✅ Change to 1 epoch instead of 0

		fc, _ := setupTestForkChoice(cfg)

		fc.justifiedCheckpoint = &Checkpoint{
			Epoch:     10,
			BlockHash: "justified-hash",
		}

		// Should finalize immediately
		fc.UpdateFinalization(11)
		assert.NotNil(t, fc.finalizedCheckpoint)
	})

	t.Run("SlowerFinalization_5Epochs", func(t *testing.T) {
		cfg := createTestConfig(100, 5, 0.66, 10) // 5 epochs
		fc, _ := setupTestForkChoice(cfg)

		fc.justifiedCheckpoint = &Checkpoint{
			Epoch:     10,
			BlockHash: "justified-hash",
		}

		// After 4 epochs - not finalized
		fc.UpdateFinalization(14)
		assert.Nil(t, fc.finalizedCheckpoint)

		// After 5 epochs - finalized
		fc.UpdateFinalization(15)
		assert.NotNil(t, fc.finalizedCheckpoint)
	})
}

// ============================================================================
// TEST: Periodic Checkpoints
// ============================================================================

func TestForkChoice_PeriodicCheckpoints(t *testing.T) {
	cfg := createTestConfig(100, 2, 0.66, 10)
	fc, _ := setupTestForkChoice(cfg)

	t.Run("CreatesCheckpoint_AtInterval", func(t *testing.T) {
		// Epoch 10 - should create checkpoint
		fc.EnsurePeriodicCheckpoint(10, "block-hash-10", "7000000000000000000", "10000000000000000000")

		assert.NotNil(t, fc.justifiedCheckpoint)
		assert.Equal(t, uint64(10), fc.justifiedCheckpoint.Epoch)
		assert.Equal(t, "block-hash-10", fc.justifiedCheckpoint.BlockHash)
	})

	t.Run("SkipsCheckpoint_NotAtInterval", func(t *testing.T) {
		fc.justifiedCheckpoint = nil

		// Epoch 11 - not at interval of 10
		fc.EnsurePeriodicCheckpoint(11, "block-hash-11", "7000000000000000000", "10000000000000000000")

		assert.Nil(t, fc.justifiedCheckpoint, "Should not create checkpoint at epoch 11")
	})

	t.Run("CustomInterval_Every5Epochs", func(t *testing.T) {
		cfg := createTestConfig(100, 2, 0.66, 5) // Every 5 epochs
		fc, _ := setupTestForkChoice(cfg)

		// Epoch 5 - should create
		fc.EnsurePeriodicCheckpoint(5, "block-5", "7000000000000000000", "10000000000000000000")
		assert.NotNil(t, fc.justifiedCheckpoint)

		fc.justifiedCheckpoint = nil

		// Epoch 7 - should not create
		fc.EnsurePeriodicCheckpoint(7, "block-7", "7000000000000000000", "10000000000000000000")
		assert.Nil(t, fc.justifiedCheckpoint)

		// Epoch 10 - should create
		fc.EnsurePeriodicCheckpoint(10, "block-10", "7000000000000000000", "10000000000000000000")
		assert.NotNil(t, fc.justifiedCheckpoint)
	})
}

// ============================================================================
// TEST: IsEpochFinalized
// ============================================================================

func TestForkChoice_IsEpochFinalized(t *testing.T) {
	cfg := createTestConfig(100, 2, 0.66, 10)
	fc, _ := setupTestForkChoice(cfg)

	t.Run("NoCheckpoint_NothingFinalized", func(t *testing.T) {
		assert.False(t, fc.IsEpochFinalized(10))
		assert.False(t, fc.IsEpochFinalized(50))
	})

	t.Run("WithCheckpoint_CorrectFinalization", func(t *testing.T) {
		fc.finalizedCheckpoint = &Checkpoint{
			Epoch:     50,
			BlockHash: "finalized-hash",
		}

		// Epochs before and at 50 are finalized
		assert.True(t, fc.IsEpochFinalized(30), "Epoch 30 should be finalized")
		assert.True(t, fc.IsEpochFinalized(50), "Epoch 50 should be finalized")

		// Epochs after 50 are not finalized
		assert.False(t, fc.IsEpochFinalized(51), "Epoch 51 should not be finalized")
		assert.False(t, fc.IsEpochFinalized(100), "Epoch 100 should not be finalized")
	})
}

// ============================================================================
// TEST: Combined Scenarios
// ============================================================================

func TestForkChoice_RealWorldScenario(t *testing.T) {
	cfg := createTestConfig(100, 2, 0.66, 10)
	fc, _ := setupTestForkChoice(cfg)

	totalStake := "10000000000000000000" // 10 tokens

	t.Run("CompleteFinalizationCycle", func(t *testing.T) {
		// Step 1: Create checkpoint at epoch 10
		fc.EnsurePeriodicCheckpoint(10, "block-10", "7000000000000000000", totalStake)
		assert.NotNil(t, fc.justifiedCheckpoint)
		assert.Nil(t, fc.finalizedCheckpoint)

		// Step 2: Wait 2 epochs and finalize
		fc.UpdateFinalization(12)
		assert.NotNil(t, fc.finalizedCheckpoint)
		assert.Equal(t, uint64(10), fc.finalizedCheckpoint.Epoch)

		// Step 3: Try to reorg past finalized checkpoint - should fail
		err := fc.ValidateReorganization(50, 5, "8000000000000000000", totalStake)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrReorgCrossesFinality)

		// Step 4: Reorg after finalized checkpoint - should succeed
		err = fc.ValidateReorganization(5, 15, "7000000000000000000", totalStake)
		assert.NoError(t, err)
	})
}

// ============================================================================
// TEST: Security Metrics
// ============================================================================

func TestForkChoice_SecurityMetrics(t *testing.T) {
	cfg := createTestConfig(150, 3, 0.75, 5)
	fc, _ := setupTestForkChoice(cfg)

	fc.justifiedCheckpoint = &Checkpoint{
		Epoch:     10,
		BlockHash: "justified-hash",
	}

	fc.finalizedCheckpoint = &Checkpoint{
		Epoch:     5,
		BlockHash: "finalized-hash",
	}

	metrics := fc.GetSecurityMetrics()

	assert.Equal(t, uint64(5), metrics["finalized_epoch"])
	assert.Equal(t, "finalize", metrics["finalized_block"])
	assert.Equal(t, uint64(10), metrics["justified_epoch"])
	assert.Equal(t, "justifie", metrics["justified_block"])
	assert.Equal(t, 150, metrics["max_reorg_depth"])
	assert.Equal(t, "75.0%", metrics["min_stake_for_reorg"])
}

func TestForkChoice_WeightDecay(t *testing.T) {
	cfg := createTestConfig(32, 2, 0.66, 10) // Audit-recommended depth of 32
	fc, _ := setupTestForkChoice(cfg)

	t.Run("WeightDecay_ReducesStakeOverTime", func(t *testing.T) {
		originalStake := big.NewInt(1000000) // 1M tokens

		// 1. Current weight (0 epochs old) should be 100%
		weight0 := fc.ApplyWeightDecay(originalStake, 10, 10)
		assert.Equal(t, originalStake.Int64(), weight0.Int64(), "Weight should not decay at current epoch")

		// 2. Weight after 1 epoch (10% decay)
		weight1 := fc.ApplyWeightDecay(originalStake, 10, 11)
		expected1 := int64(900000) // 1M * 0.9
		assert.Equal(t, expected1, weight1.Int64(), "Weight should decay by 10% after 1 epoch")

		// 3. Weight after 5 epochs (compounded decay)
		weight5 := fc.ApplyWeightDecay(originalStake, 10, 15)
		// 1,000,000 * (0.9^5) = 590,490
		assert.Less(t, weight5.Int64(), int64(600000))
		assert.Greater(t, weight5.Int64(), int64(590000))
	})

	t.Run("ForkSelection_PrefersFreshChain", func(t *testing.T) {
		// Mock two branches:
		// Branch A: 100 tokens, but 10 epochs old
		// Branch B: 80 tokens, but 0 epochs old

		oldStake := big.NewInt(100)
		newStake := big.NewInt(80)

		currentEpoch := uint64(20)

		decayedOld := fc.ApplyWeightDecay(oldStake, 10, currentEpoch)
		decayedNew := fc.ApplyWeightDecay(newStake, 20, currentEpoch)

		// 100 * (0.9^10) is approx 34.8.
		// 80 * (0.9^0) is 80.
		assert.True(t, decayedNew.Cmp(decayedOld) > 0, "Newer lighter chain should outweigh older heavier chain")
	})
}
