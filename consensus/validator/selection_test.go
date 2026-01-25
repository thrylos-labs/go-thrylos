// consensus/validator/selection_test.go
package validator

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/thrylos-labs/go-thrylos/crypto/hash"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

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
