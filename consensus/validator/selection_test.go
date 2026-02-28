// consensus/validator/selection_test.go
package validator

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/thrylos-labs/go-thrylos/crypto/hash"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

type mockProposerHistory struct {
	blocks map[int64]*core.Block
	height int64
}

func (m *mockProposerHistory) GetBlock(index int64) (*core.Block, error) {
	return m.blocks[index], nil
}

func (m *mockProposerHistory) GetHeight() int64 {
	return m.height
}

// TestGenerateSeedFromBlocks tests the block hash accumulator
func TestGenerateSeedFromBlocks(t *testing.T) {
	t.Run("DifferentBlocks_DifferentSeeds", func(t *testing.T) {
		// Create two different sets of block hashes
		blocks1 := [][]byte{
			[]byte("block1hash"),
			[]byte("block2hash"),
			[]byte("block3hash"),
		}

		blocks2 := [][]byte{
			[]byte("block1hash"),
			[]byte("block2hash"),
			[]byte("block4hash"), // Different!
		}

		slot := uint64(100)

		seed1 := GenerateSeedFromBlocks(blocks1, slot)
		seed2 := GenerateSeedFromBlocks(blocks2, slot)

		// Seeds should be different
		assert.NotEqual(t, seed1, seed2, "Different block hashes should produce different seeds")
		assert.Len(t, seed1, 32, "Seed should be 32 bytes")
		assert.Len(t, seed2, 32, "Seed should be 32 bytes")
	})

	t.Run("SameBlocks_SameSeeds", func(t *testing.T) {
		blocks := [][]byte{
			[]byte("block1hash"),
			[]byte("block2hash"),
			[]byte("block3hash"),
		}

		slot := uint64(100)

		seed1 := GenerateSeedFromBlocks(blocks, slot)
		seed2 := GenerateSeedFromBlocks(blocks, slot)

		// Seeds should be identical
		assert.Equal(t, seed1, seed2, "Same blocks should produce same seed")
	})

	t.Run("DifferentSlots_DifferentSeeds", func(t *testing.T) {
		blocks := [][]byte{
			[]byte("block1hash"),
			[]byte("block2hash"),
		}

		seed1 := GenerateSeedFromBlocks(blocks, 100)
		seed2 := GenerateSeedFromBlocks(blocks, 101)

		// Different slots should produce different seeds
		assert.NotEqual(t, seed1, seed2, "Different slots should produce different seeds")
	})

	t.Run("EmptyBlocks_UsesSlotFallback", func(t *testing.T) {
		emptyBlocks := [][]byte{}
		slot := uint64(100)

		seed := GenerateSeedFromBlocks(emptyBlocks, slot)

		// Should still produce valid 32-byte seed
		assert.Len(t, seed, 32, "Should produce valid seed even with no blocks")
		assert.NotEqual(t, make([]byte, 32), seed, "Seed should not be all zeros")
	})

	t.Run("NoGrindingAttack", func(t *testing.T) {
		// Simulate attacker trying to grind by changing last block
		baseBlocks := [][]byte{
			[]byte("block1"),
			[]byte("block2"),
			[]byte("block3"),
			[]byte("block4"),
			[]byte("block5"),
			[]byte("block6"),
			[]byte("block7"),
			[]byte("block8"),
			[]byte("block9"),
		}

		slot := uint64(100)

		// Try 100 different "last blocks" to see if attacker can predict
		seeds := make(map[string]bool)

		for i := 0; i < 100; i++ {
			blocks := make([][]byte, len(baseBlocks)+1)
			copy(blocks, baseBlocks)

			// Attacker tries different last block
			lastBlock := make([]byte, 8)
			binary.BigEndian.PutUint64(lastBlock, uint64(i))
			blocks[len(blocks)-1] = lastBlock

			seed := GenerateSeedFromBlocks(blocks, slot)
			seedStr := string(seed)

			// Each seed should be unique
			assert.False(t, seeds[seedStr], "Seed collision detected - grinding might be possible")
			seeds[seedStr] = true
		}

		// Should have 100 unique seeds
		assert.Len(t, seeds, 100, "All seeds should be unique")
	})

	t.Run("ValidatorSelection_Unpredictable", func(t *testing.T) {
		// Create validator set
		set := NewSet(100)

		// Add 10 validators with equal stake
		for i := 0; i < 10; i++ {
			set.AddValidator(&core.Validator{
				Address: string(rune('A' + i)),
				Stake:   "1000000000000000000", // 1 token each
				Active:  true,
			})
		}

		// Generate seed from fake blocks
		blocks := [][]byte{
			[]byte("block1"),
			[]byte("block2"),
			[]byte("block3"),
		}

		seed := GenerateSeedFromBlocks(blocks, 100)

		// Select proposer
		result, err := set.SelectProposer(seed, 100)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.NotNil(t, result.SelectedValidator)

		// Try with different blocks - should select different validator (usually)
		blocks2 := [][]byte{
			[]byte("block1"),
			[]byte("block2"),
			[]byte("block4"), // Different
		}

		seed2 := GenerateSeedFromBlocks(blocks2, 100)
		result2, err := set.SelectProposer(seed2, 100)
		assert.NoError(t, err)

		// Different seeds should (usually) select different validators
		// Note: Could occasionally be same due to randomness, but unlikely
		t.Logf("First selection: %s", result.SelectedValidator.Address)
		t.Logf("Second selection: %s", result2.SelectedValidator.Address)
	})

	t.Run("ValidatorSelection_UsesCanonicalHistoryPenalty", func(t *testing.T) {
		set := NewSet(10)
		assert.NoError(t, set.AddValidator(&core.Validator{
			Address: "A",
			Stake:   "800",
			Active:  true,
		}))
		assert.NoError(t, set.AddValidator(&core.Validator{
			Address: "B",
			Stake:   "200",
			Active:  true,
		}))

		blocks := make(map[int64]*core.Block)
		for i := int64(0); i < recentProposerWindow; i++ {
			blocks[i] = &core.Block{
				Header: &core.BlockHeader{
					Index:     i,
					Validator: "A",
				},
			}
		}
		set.SetHistoryReader(&mockProposerHistory{
			blocks: blocks,
			height: recentProposerWindow - 1,
		})

		adjusted := set.calculateAdjustedStakes()
		assert.Less(t, adjusted["A"], adjusted["B"], "recent proposer should receive a deterministic chain-history penalty")
	})
}

// Benchmark to ensure performance is acceptable
func BenchmarkGenerateSeedFromBlocks(b *testing.B) {
	blocks := make([][]byte, 10)
	for i := 0; i < 10; i++ {
		blocks[i] = make([]byte, 32)
		hashBytes := hash.Keccak256([]byte{byte(i)})
		copy(blocks[i], hashBytes)
	}

	slot := uint64(100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = GenerateSeedFromBlocks(blocks, slot)
	}
}

func TestSelectionSimulation_CartelStakeSplitting(t *testing.T) {
	type simulationScenario struct {
		name         string
		cartelStakes []string
		honestCount  int
		honestStake  string
	}

	type simulationResult struct {
		cartelShare   float64
		maxRun        int
		uniqueWinners int
	}

	runScenario := func(t *testing.T, scenario simulationScenario, slots int) simulationResult {
		t.Helper()

		set := NewSet(100)
		history := &mockProposerHistory{
			blocks: make(map[int64]*core.Block),
			height: -1,
		}
		set.SetHistoryReader(history)

		cartelMembers := make(map[string]bool)
		for i, stake := range scenario.cartelStakes {
			addr := fmt.Sprintf("cartel_%d", i)
			cartelMembers[addr] = true
			assert.NoError(t, set.AddValidator(&core.Validator{
				Address: addr,
				Stake:   stake,
				Active:  true,
			}))
		}

		for i := 0; i < scenario.honestCount; i++ {
			assert.NoError(t, set.AddValidator(&core.Validator{
				Address: fmt.Sprintf("honest_%d", i),
				Stake:   scenario.honestStake,
				Active:  true,
			}))
		}

		cartelWins := 0
		currentRun := 0
		maxRun := 0
		winnerCounts := make(map[string]int)

		for slot := 0; slot < slots; slot++ {
			seed := GenerateSeedFromBlocks([][]byte{[]byte(fmt.Sprintf("slot-%d", slot))}, uint64(slot))
			result, err := set.SelectProposer(seed, uint64(slot))
			assert.NoError(t, err)
			assert.NotNil(t, result)

			winner := result.SelectedValidator.Address
			winnerCounts[winner]++
			if cartelMembers[winner] {
				cartelWins++
				currentRun++
				if currentRun > maxRun {
					maxRun = currentRun
				}
			} else {
				currentRun = 0
			}

			history.height = int64(slot)
			history.blocks[int64(slot)] = &core.Block{
				Header: &core.BlockHeader{
					Index:     int64(slot),
					Validator: winner,
				},
			}
		}

		return simulationResult{
			cartelShare:   float64(cartelWins) / float64(slots),
			maxRun:        maxRun,
			uniqueWinners: len(winnerCounts),
		}
	}

	slots := 5000
	monolithic := runScenario(t, simulationScenario{
		name:         "monolithic",
		cartelStakes: []string{"400"},
		honestCount:  6,
		honestStake:  "100",
	}, slots)
	split := runScenario(t, simulationScenario{
		name:         "split",
		cartelStakes: []string{"100", "100", "100", "100"},
		honestCount:  6,
		honestStake:  "100",
	}, slots)

	t.Logf("monolithic cartel: share=%.2f%% max_run=%d unique_winners=%d",
		monolithic.cartelShare*100, monolithic.maxRun, monolithic.uniqueWinners)
	t.Logf("split cartel:      share=%.2f%% max_run=%d unique_winners=%d",
		split.cartelShare*100, split.maxRun, split.uniqueWinners)

	// Splitting stake should not materially improve aggregate cartel capture under the
	// chain-history penalty model.
	assert.LessOrEqual(t, split.cartelShare, monolithic.cartelShare+0.03)
	assert.LessOrEqual(t, split.maxRun, monolithic.maxRun+2)
}
