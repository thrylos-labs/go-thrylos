// consensus/pos/fork_choice_test.go
// Tests for stake-weighted quorum checking

package pos

import (
	"testing"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// MockWorldState provides a test implementation of WorldState
type MockWorldState struct {
	validators map[string]*core.Validator
}

func NewMockWorldState() *MockWorldState {
	return &MockWorldState{
		validators: make(map[string]*core.Validator),
	}
}

func (m *MockWorldState) GetValidator(address string) (*core.Validator, error) {
	if val, exists := m.validators[address]; exists {
		return val, nil
	}
	return nil, nil
}

func (m *MockWorldState) GetActiveValidators() []*core.Validator {
	active := make([]*core.Validator, 0)
	for _, val := range m.validators {
		if val.Active {
			active = append(active, val)
		}
	}
	return active
}

func (m *MockWorldState) AddValidator(address string, stake int64, active bool) {
	m.validators[address] = &core.Validator{
		Address: address,
		Stake:   stake,
		Active:  active,
		Pubkey:  []byte("mock_pubkey_" + address),
	}
}

func (m *MockWorldState) GetCurrentBlock() *core.Block {
	return nil
}

func (m *MockWorldState) AddBlock(block *core.Block) error {
	return nil
}

func (m *MockWorldState) ValidateTransaction(tx *core.Transaction) error {
	return nil
}

// TestQuorumCalculation tests basic 2/3 quorum threshold calculation
func TestQuorumCalculation(t *testing.T) {
	tests := []struct {
		name           string
		totalStake     int64
		attestingStake int64
		expectedQuorum bool
		description    string
	}{
		{
			name:           "Exactly 2/3 stake",
			totalStake:     10000,
			attestingStake: 6667, // Exactly 2/3 (rounded up)
			expectedQuorum: true,
			description:    "Should reach quorum with exactly 2/3 stake",
		},
		{
			name:           "Just above 2/3",
			totalStake:     10000,
			attestingStake: 6700,
			expectedQuorum: true,
			description:    "Should reach quorum with >2/3 stake",
		},
		{
			name:           "Just below 2/3",
			totalStake:     10000,
			attestingStake: 6666,
			expectedQuorum: false,
			description:    "Should NOT reach quorum with <2/3 stake",
		},
		{
			name:           "Half stake (50%)",
			totalStake:     10000,
			attestingStake: 5000,
			expectedQuorum: false,
			description:    "50% is insufficient for quorum",
		},
		{
			name:           "All stake (100%)",
			totalStake:     10000,
			attestingStake: 10000,
			expectedQuorum: true,
			description:    "100% should definitely reach quorum",
		},
		{
			name:           "Zero stake",
			totalStake:     10000,
			attestingStake: 0,
			expectedQuorum: false,
			description:    "No attestations should not reach quorum",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Calculate quorum threshold
			quorumThreshold := (tt.totalStake*2)/3 + 1
			hasQuorum := tt.attestingStake >= quorumThreshold

			if hasQuorum != tt.expectedQuorum {
				t.Errorf("%s: expected quorum=%v, got quorum=%v (attesting=%d, total=%d, threshold=%d)",
					tt.description, tt.expectedQuorum, hasQuorum, tt.attestingStake, tt.totalStake, quorumThreshold)
			}

			// Calculate percentage
			percentage := float64(tt.attestingStake) / float64(tt.totalStake) * 100
			t.Logf("  Attesting: %d/%d (%.1f%%) - Quorum: %v", tt.attestingStake, tt.totalStake, percentage, hasQuorum)
		})
	}
}

// TestProcessAttestationWithStakeWeight tests stake-weighted attestation processing
func TestProcessAttestationWithStakeWeight(t *testing.T) {
	// Setup
	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			BlockTime: 5 * time.Second,
		},
	}

	mockState := NewMockWorldState()

	// Add validators with different stakes
	mockState.AddValidator("validator1", 1000, true) // 10%
	mockState.AddValidator("validator2", 2000, true) // 20%
	mockState.AddValidator("validator3", 3000, true) // 30%
	mockState.AddValidator("validator4", 4000, true) // 40%
	// Total: 10,000 stake

	fc := NewForkChoice(cfg, mockState)

	blockHash := "block123"
	epoch := uint64(10)

	// Test: Single attestation from validator with 1000 stake
	t.Run("Single attestation - 10% stake", func(t *testing.T) {
		attestation := &Attestation{
			ValidatorAddress: "validator1",
			BlockHash:        blockHash,
			BlockHeight:      100,
			Epoch:            epoch,
			Slot:             320,
			Timestamp:        time.Now().Unix(),
		}

		fc.ProcessAttestation(attestation)

		// Check score
		score := fc.GetBlockScore(blockHash)
		if score != 1000 {
			t.Errorf("Expected score 1000, got %d", score)
		}

		// Should NOT have quorum yet (10% < 66.7%)
		if fc.HasQuorum(blockHash) {
			t.Error("Should not have quorum with only 10% stake")
		}

		percentage := fc.GetQuorumPercentage(blockHash)
		t.Logf("  After 1 attestation: %.1f%% (expected ~10%%)", percentage)
	})

	// Test: Add second attestation from validator with 2000 stake
	t.Run("Two attestations - 30% stake", func(t *testing.T) {
		attestation := &Attestation{
			ValidatorAddress: "validator2",
			BlockHash:        blockHash,
			BlockHeight:      100,
			Epoch:            epoch,
			Slot:             320,
			Timestamp:        time.Now().Unix(),
		}

		fc.ProcessAttestation(attestation)

		// Check score (1000 + 2000 = 3000)
		score := fc.GetBlockScore(blockHash)
		if score != 3000 {
			t.Errorf("Expected score 3000, got %d", score)
		}

		// Should NOT have quorum yet (30% < 66.7%)
		if fc.HasQuorum(blockHash) {
			t.Error("Should not have quorum with only 30% stake")
		}

		percentage := fc.GetQuorumPercentage(blockHash)
		t.Logf("  After 2 attestations: %.1f%% (expected ~30%%)", percentage)
	})

	// Test: Add third attestation from validator with 3000 stake
	t.Run("Three attestations - 60% stake", func(t *testing.T) {
		attestation := &Attestation{
			ValidatorAddress: "validator3",
			BlockHash:        blockHash,
			BlockHeight:      100,
			Epoch:            epoch,
			Slot:             320,
			Timestamp:        time.Now().Unix(),
		}

		fc.ProcessAttestation(attestation)

		// Check score (1000 + 2000 + 3000 = 6000)
		score := fc.GetBlockScore(blockHash)
		if score != 6000 {
			t.Errorf("Expected score 6000, got %d", score)
		}

		// Should NOT have quorum yet (60% < 66.7%)
		if fc.HasQuorum(blockHash) {
			t.Error("Should not have quorum with only 60% stake")
		}

		percentage := fc.GetQuorumPercentage(blockHash)
		t.Logf("  After 3 attestations: %.1f%% (expected ~60%%)", percentage)
	})

	// Test: Add fourth attestation from validator with 4000 stake
	t.Run("Four attestations - 100% stake - HAS QUORUM", func(t *testing.T) {
		attestation := &Attestation{
			ValidatorAddress: "validator4",
			BlockHash:        blockHash,
			BlockHeight:      100,
			Epoch:            epoch,
			Slot:             320,
			Timestamp:        time.Now().Unix(),
		}

		fc.ProcessAttestation(attestation)

		// Check score (1000 + 2000 + 3000 + 4000 = 10000)
		score := fc.GetBlockScore(blockHash)
		if score != 10000 {
			t.Errorf("Expected score 10000, got %d", score)
		}

		// Should HAVE quorum (100% > 66.7%)
		if !fc.HasQuorum(blockHash) {
			t.Error("Should have quorum with 100% stake")
		}

		percentage := fc.GetQuorumPercentage(blockHash)
		t.Logf("  After 4 attestations: %.1f%% (expected 100%%) - ✅ HAS QUORUM", percentage)

		if percentage != 100.0 {
			t.Errorf("Expected 100%%, got %.1f%%", percentage)
		}
	})
}

// TestDuplicateAttestationPrevention tests that validators can't vote twice
func TestDuplicateAttestationPrevention(t *testing.T) {
	cfg := &config.Config{}
	mockState := NewMockWorldState()
	mockState.AddValidator("validator1", 5000, true)

	fc := NewForkChoice(cfg, mockState)

	blockHash := "block456"

	// First attestation - should count
	attestation1 := &Attestation{
		ValidatorAddress: "validator1",
		BlockHash:        blockHash,
		BlockHeight:      100,
		Epoch:            10,
		Slot:             320,
		Timestamp:        time.Now().Unix(),
	}
	fc.ProcessAttestation(attestation1)

	score1 := fc.GetBlockScore(blockHash)
	if score1 != 5000 {
		t.Errorf("Expected score 5000 after first attestation, got %d", score1)
	}

	// Second attestation from SAME validator - should be ignored
	attestation2 := &Attestation{
		ValidatorAddress: "validator1",
		BlockHash:        blockHash,
		BlockHeight:      100,
		Epoch:            10,
		Slot:             321,
		Timestamp:        time.Now().Unix(),
	}
	fc.ProcessAttestation(attestation2)

	score2 := fc.GetBlockScore(blockHash)
	if score2 != 5000 {
		t.Errorf("Expected score to remain 5000 (duplicate ignored), got %d", score2)
	}

	t.Logf("✅ Duplicate attestation correctly ignored - score remained at %d", score2)
}

// TestInactiveValidatorRejection tests that inactive validators are rejected
func TestInactiveValidatorRejection(t *testing.T) {
	cfg := &config.Config{}
	mockState := NewMockWorldState()

	// Add active and inactive validators
	mockState.AddValidator("active_validator", 5000, true)
	mockState.AddValidator("inactive_validator", 5000, false)

	fc := NewForkChoice(cfg, mockState)

	blockHash := "block789"

	// Attestation from active validator - should work
	t.Run("Active validator accepted", func(t *testing.T) {
		attestation := &Attestation{
			ValidatorAddress: "active_validator",
			BlockHash:        blockHash,
			BlockHeight:      100,
			Epoch:            10,
			Slot:             320,
			Timestamp:        time.Now().Unix(),
		}
		fc.ProcessAttestation(attestation)

		score := fc.GetBlockScore(blockHash)
		if score != 5000 {
			t.Errorf("Expected score 5000 from active validator, got %d", score)
		}
		t.Logf("✅ Active validator attestation accepted - score: %d", score)
	})

	// Attestation from inactive validator - should be rejected
	t.Run("Inactive validator rejected", func(t *testing.T) {
		attestation := &Attestation{
			ValidatorAddress: "inactive_validator",
			BlockHash:        blockHash,
			BlockHeight:      100,
			Epoch:            10,
			Slot:             320,
			Timestamp:        time.Now().Unix(),
		}
		fc.ProcessAttestation(attestation)

		score := fc.GetBlockScore(blockHash)
		if score != 5000 { // Should still be 5000 (not 10000)
			t.Errorf("Expected score to remain 5000 (inactive rejected), got %d", score)
		}
		t.Logf("✅ Inactive validator attestation rejected - score remained: %d", score)
	})
}

// TestQuorumThresholdEdgeCases tests edge cases around the 2/3 threshold
func TestQuorumThresholdEdgeCases(t *testing.T) {
	cfg := &config.Config{}
	mockState := NewMockWorldState()

	// Create scenario where threshold is interesting
	// Total: 10,000 stake
	// Threshold: 6,667 (rounded)
	mockState.AddValidator("val1", 3333, true)
	mockState.AddValidator("val2", 3333, true)
	mockState.AddValidator("val3", 3334, true)

	fc := NewForkChoice(cfg, mockState)
	blockHash := "edge_block"

	// Test 1: Two validators = 6,666 stake (JUST BELOW threshold)
	t.Run("Just below threshold - no quorum", func(t *testing.T) {
		fc.ProcessAttestation(&Attestation{
			ValidatorAddress: "val1",
			BlockHash:        blockHash,
			Epoch:            10,
			Slot:             320,
		})
		fc.ProcessAttestation(&Attestation{
			ValidatorAddress: "val2",
			BlockHash:        blockHash,
			Epoch:            10,
			Slot:             320,
		})

		score := fc.GetBlockScore(blockHash)
		percentage := fc.GetQuorumPercentage(blockHash)
		hasQuorum := fc.HasQuorum(blockHash)

		t.Logf("  Score: %d, Percentage: %.2f%%, Has Quorum: %v", score, percentage, hasQuorum)

		if hasQuorum {
			t.Error("Should NOT have quorum with 6666/10000 stake")
		}
	})

	// Test 2: Add third validator = 10,000 stake (ABOVE threshold)
	t.Run("Above threshold - has quorum", func(t *testing.T) {
		fc.ProcessAttestation(&Attestation{
			ValidatorAddress: "val3",
			BlockHash:        blockHash,
			Epoch:            10,
			Slot:             320,
		})

		score := fc.GetBlockScore(blockHash)
		percentage := fc.GetQuorumPercentage(blockHash)
		hasQuorum := fc.HasQuorum(blockHash)

		t.Logf("  Score: %d, Percentage: %.2f%%, Has Quorum: %v", score, percentage, hasQuorum)

		if !hasQuorum {
			t.Error("Should HAVE quorum with 10000/10000 stake")
		}
	})
}

// TestByzantineFaultTolerance tests that system is safe with up to 33% malicious stake
func TestByzantineFaultTolerance(t *testing.T) {
	cfg := &config.Config{}
	mockState := NewMockWorldState()

	// Scenario: 10,000 total stake
	// Honest: 7,000 (70%)
	// Malicious: 3,000 (30%)
	mockState.AddValidator("honest1", 2000, true)
	mockState.AddValidator("honest2", 2000, true)
	mockState.AddValidator("honest3", 2000, true)
	mockState.AddValidator("honest4", 1000, true)
	mockState.AddValidator("malicious1", 1000, true)
	mockState.AddValidator("malicious2", 1000, true)
	mockState.AddValidator("malicious3", 1000, true)

	fc := NewForkChoice(cfg, mockState)

	honestBlock := "honest_block"
	maliciousBlock := "malicious_block"

	// Malicious validators try to finalize their block (30% stake)
	t.Run("Malicious block cannot reach quorum", func(t *testing.T) {
		fc.ProcessAttestation(&Attestation{ValidatorAddress: "malicious1", BlockHash: maliciousBlock, Epoch: 10, Slot: 320})
		fc.ProcessAttestation(&Attestation{ValidatorAddress: "malicious2", BlockHash: maliciousBlock, Epoch: 10, Slot: 320})
		fc.ProcessAttestation(&Attestation{ValidatorAddress: "malicious3", BlockHash: maliciousBlock, Epoch: 10, Slot: 320})

		if fc.HasQuorum(maliciousBlock) {
			t.Error("Malicious block should NOT reach quorum with only 30% stake")
		}

		percentage := fc.GetQuorumPercentage(maliciousBlock)
		t.Logf("  Malicious block: %.1f%% stake (CANNOT reach quorum)", percentage)
	})

	// Honest validators finalize correct block (70% stake)
	t.Run("Honest block reaches quorum", func(t *testing.T) {
		fc.ProcessAttestation(&Attestation{ValidatorAddress: "honest1", BlockHash: honestBlock, Epoch: 10, Slot: 320})
		fc.ProcessAttestation(&Attestation{ValidatorAddress: "honest2", BlockHash: honestBlock, Epoch: 10, Slot: 320})
		fc.ProcessAttestation(&Attestation{ValidatorAddress: "honest3", BlockHash: honestBlock, Epoch: 10, Slot: 320})
		fc.ProcessAttestation(&Attestation{ValidatorAddress: "honest4", BlockHash: honestBlock, Epoch: 10, Slot: 320})

		if !fc.HasQuorum(honestBlock) {
			t.Error("Honest block SHOULD reach quorum with 70% stake")
		}

		percentage := fc.GetQuorumPercentage(honestBlock)
		t.Logf("  Honest block: %.1f%% stake (✅ HAS quorum)", percentage)
	})

	t.Log("✅ Byzantine Fault Tolerance verified: 30% malicious stake cannot finalize blocks")
}

// TestMultipleBlocksForkChoice tests fork choice with multiple competing blocks
func TestMultipleBlocksForkChoice(t *testing.T) {
	cfg := &config.Config{}
	mockState := NewMockWorldState()

	mockState.AddValidator("val1", 2500, true)
	mockState.AddValidator("val2", 2500, true)
	mockState.AddValidator("val3", 2500, true)
	mockState.AddValidator("val4", 2500, true)

	fc := NewForkChoice(cfg, mockState)

	blockA := "block_a"
	blockB := "block_b"

	// Block A gets 50% stake
	t.Run("Block A - 50% stake", func(t *testing.T) {
		fc.ProcessAttestation(&Attestation{ValidatorAddress: "val1", BlockHash: blockA, Epoch: 10, Slot: 320})
		fc.ProcessAttestation(&Attestation{ValidatorAddress: "val2", BlockHash: blockA, Epoch: 10, Slot: 320})

		if fc.HasQuorum(blockA) {
			t.Error("Block A should not have quorum with 50% stake")
		}
		t.Logf("  Block A: %.1f%% stake (no quorum)", fc.GetQuorumPercentage(blockA))
	})

	// Block B gets 50% stake
	t.Run("Block B - 50% stake", func(t *testing.T) {
		fc.ProcessAttestation(&Attestation{ValidatorAddress: "val3", BlockHash: blockB, Epoch: 10, Slot: 320})
		fc.ProcessAttestation(&Attestation{ValidatorAddress: "val4", BlockHash: blockB, Epoch: 10, Slot: 320})

		if fc.HasQuorum(blockB) {
			t.Error("Block B should not have quorum with 50% stake")
		}
		t.Logf("  Block B: %.1f%% stake (no quorum)", fc.GetQuorumPercentage(blockB))
	})

	// Neither block should be head (no quorum)
	t.Run("No clear winner without quorum", func(t *testing.T) {
		head := fc.GetHead()
		// GetHead returns highest stake if no quorum
		if head != blockA && head != blockB {
			t.Error("Head should be one of the blocks")
		}
		t.Logf("  Current head: %s (but no quorum yet)", head)
		t.Log("✅ Fork choice working correctly - no premature finalization")
	})
}

// TestStakeCaching tests that stake calculations are cached properly
func TestStakeCaching(t *testing.T) {
	cfg := &config.Config{}
	mockState := NewMockWorldState()

	mockState.AddValidator("val1", 10000, true)

	fc := NewForkChoice(cfg, mockState)

	// First call should calculate
	stake1 := fc.getTotalActiveStake()
	time1 := fc.totalActiveStakeTime

	if stake1 != 10000 {
		t.Errorf("Expected total stake 10000, got %d", stake1)
	}

	// Second call within cache window should use cached value
	time.Sleep(100 * time.Millisecond)
	fc.getTotalActiveStake()
	time2 := fc.totalActiveStakeTime

	if time1 != time2 {
		t.Error("Cache time should not have changed within cache window")
	}

	t.Logf("✅ Stake caching working: %d stake, cached for 30s", stake1)

	// After cache expiry, should recalculate
	fc.totalActiveStakeTime = time.Now().Add(-31 * time.Second)
	fc.getTotalActiveStake()
	time3 := fc.totalActiveStakeTime

	if time1 == time3 {
		t.Error("Cache time should have updated after expiry")
	}

	t.Logf("✅ Cache expiry working: recalculated after 30s")
}

// BenchmarkProcessAttestation benchmarks attestation processing performance
func BenchmarkProcessAttestation(b *testing.B) {
	cfg := &config.Config{}
	mockState := NewMockWorldState()

	// Add 100 validators
	for i := 0; i < 100; i++ {
		mockState.AddValidator(string(rune('A'+i)), 1000, true)
	}

	fc := NewForkChoice(cfg, mockState)
	blockHash := "benchmark_block"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		attestation := &Attestation{
			ValidatorAddress: string(rune('A' + (i % 100))),
			BlockHash:        blockHash,
			Epoch:            uint64(i / 100),
			Slot:             uint64(i),
		}
		fc.ProcessAttestation(attestation)
	}
}

// BenchmarkHasQuorum benchmarks quorum checking performance
func BenchmarkHasQuorum(b *testing.B) {
	cfg := &config.Config{}
	mockState := NewMockWorldState()
	mockState.AddValidator("val1", 10000, true)

	fc := NewForkChoice(cfg, mockState)
	blockHash := "benchmark_block"

	fc.ProcessAttestation(&Attestation{
		ValidatorAddress: "val1",
		BlockHash:        blockHash,
		Epoch:            10,
		Slot:             320,
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fc.HasQuorum(blockHash)
	}
}
