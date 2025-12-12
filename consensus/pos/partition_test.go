package pos

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/thrylos-labs/go-thrylos/config"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// MockWorldStateReader implements WorldStateReader for testing
type MockWorldStateReader struct {
	blocks map[string]*core.Block
}

func (m *MockWorldStateReader) GetBlockByHash(hash string) (*core.Block, error) {
	if block, ok := m.blocks[hash]; ok {
		return block, nil
	}
	return nil, nil // Not found
}

// Implement other interface methods as no-ops or simple returns...
func (m *MockWorldStateReader) GetValidator(addr string) (*core.Validator, error) { return nil, nil }
func (m *MockWorldStateReader) GetActiveValidators() []*core.Validator            { return nil }

func TestNetworkPartitionAndReorg(t *testing.T) {
	// 1. SETUP: Create config and mocks
	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			BlockTime:     1 * time.Second,
			StakeCacheTTL: 1 * time.Hour, // Ensure cache doesn't expire during test
		},
	}

	// Create a shared genesis
	genesisHash := "0xGenesis"
	mockState := &MockWorldStateReader{
		blocks: map[string]*core.Block{
			genesisHash: {Hash: genesisHash, Header: &core.BlockHeader{Index: 0, PrevHash: ""}},
			"0xBlock1A": {Hash: "0xBlock1A", Header: &core.BlockHeader{Index: 1, PrevHash: genesisHash}},
			"0xBlock1B": {Hash: "0xBlock1B", Header: &core.BlockHeader{Index: 1, PrevHash: genesisHash}},
		},
	}

	// 2. Initialize Engines for two isolated nodes

	// Engine A (Majority: 70% stake)
	// We pass 'nil' for the slashing manager and blockchain as they aren't needed for this specific logic test
	engineA := NewForkChoice(cfg, mockState, nil)
	// ✅ FIX: Assign string value
	engineA.totalActiveStake = "10000"
	engineA.totalActiveStakeTime = time.Now()

	// Engine B (Minority: 30% stake)
	engineB := NewForkChoice(cfg, mockState, nil)
	// ✅ FIX: Assign string value
	engineB.totalActiveStake = "10000"
	engineB.totalActiveStakeTime = time.Now()

	// 3. SIMULATE PARTITION
	// Group A votes for Block 1A (7000 stake)
	block1A_Hash := "0xBlock1A"
	// ✅ FIX: Assign string value
	engineA.blockScores[block1A_Hash] = "7000"

	// Group B votes for Block 1B (3000 stake)
	block1B_Hash := "0xBlock1B"
	// ✅ FIX: Assign string value
	engineB.blockScores[block1B_Hash] = "3000"

	// 4. ASSERT INDEPENDENT HEADS
	// A should choose 1A (Has >66% quorum)
	// 7000 >= (10000 * 2/3) + 1 => 7000 >= 6667 => True
	assert.Equal(t, block1A_Hash, engineA.GetHead(), "Majority chain should accept 1A")
	assert.True(t, engineA.HasQuorum(block1A_Hash), "Majority should have quorum")

	// B might tentatively choose 1B based on local weight, but SHOULD NOT have quorum
	// 3000 >= 6667 => False
	assert.Equal(t, block1B_Hash, engineB.GetHead(), "Minority chain sees 1B locally")
	assert.False(t, engineB.HasQuorum(block1B_Hash), "Minority should NOT have quorum")

	// 5. RECONNECT (The Partition Heals)
	// Engine B receives the attestations from Group A for Block 1A
	// We simulate B receiving the heavy votes
	// ✅ FIX: Assign string value
	engineB.blockScores[block1A_Hash] = "7000"

	// 6. ASSERT CONVERGENCE
	// Engine B should now switch its head to 1A because it has 7000 weight vs 1B's 3000
	newHeadB := engineB.GetHead()

	assert.Equal(t, block1A_Hash, newHeadB, "Minority node MUST switch to majority chain (1A) after partition heals")
	assert.True(t, engineB.HasQuorum(block1A_Hash), "Minority node should now recognize quorum on 1A")
}
