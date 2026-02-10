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
			StakeCacheTTL: 1 * time.Hour,
		},
	}

	// Create blocks
	genesisHash := "0xGenesis"
	genesisBlock := &core.Block{
		Hash:   genesisHash,
		Header: &core.BlockHeader{Index: 0, PrevHash: "", Timestamp: time.Now().Unix()},
	}

	block1A := &core.Block{
		Hash:   "0xBlock1A",
		Header: &core.BlockHeader{Index: 1, PrevHash: genesisHash, Timestamp: time.Now().Unix()},
	}

	block1B := &core.Block{
		Hash:   "0xBlock1B",
		Header: &core.BlockHeader{Index: 1, PrevHash: genesisHash, Timestamp: time.Now().Unix()},
	}

	mockState := &MockWorldStateReader{
		blocks: map[string]*core.Block{
			genesisHash: genesisBlock,
			"0xBlock1A": block1A,
			"0xBlock1B": block1B,
		},
	}

	// 2. Initialize Fork Choice Engines
	engineA := NewForkChoice(cfg, mockState, nil)
	engineA.totalActiveStake = "10000"
	engineA.totalActiveStakeTime = time.Now()

	engineB := NewForkChoice(cfg, mockState, nil)
	engineB.totalActiveStake = "10000"
	engineB.totalActiveStakeTime = time.Now()

	// 3. Register blocks in fork choice (CRITICAL!)
	// Both engines know about genesis
	engineA.OnBlockAdded(genesisBlock)
	engineB.OnBlockAdded(genesisBlock)

	// 4. SIMULATE PARTITION
	// Engine A sees Block 1A with 7000 stake
	engineA.OnBlockAdded(block1A)
	engineA.blockScores[block1A.Hash] = "7000"
	// Initialize children map for genesis if not exists
	if engineA.children == nil {
		engineA.children = make(map[string][]string)
	}
	engineA.children[genesisHash] = append(engineA.children[genesisHash], block1A.Hash)

	// Engine B sees Block 1B with 3000 stake
	engineB.OnBlockAdded(block1B)
	engineB.blockScores[block1B.Hash] = "3000"
	// Initialize children map for genesis if not exists
	if engineB.children == nil {
		engineB.children = make(map[string][]string)
	}
	engineB.children[genesisHash] = append(engineB.children[genesisHash], block1B.Hash)

	// 5. ASSERT INDEPENDENT HEADS
	headA := engineA.GetHead()
	headB := engineB.GetHead()

	assert.Equal(t, "0xBlock1A", headA, "Majority chain should accept 1A")
	assert.True(t, engineA.HasQuorum("0xBlock1A"), "Majority should have quorum")

	assert.Equal(t, "0xBlock1B", headB, "Minority chain sees 1B locally")
	assert.False(t, engineB.HasQuorum("0xBlock1B"), "Minority should NOT have quorum")

	// 6. RECONNECT (Partition Heals)
	// Engine B now learns about Block 1A and its attestations
	engineB.OnBlockAdded(block1A)
	engineB.blockScores[block1A.Hash] = "7000"
	engineB.children[genesisHash] = append(engineB.children[genesisHash], block1A.Hash)

	// 7. ASSERT CONVERGENCE
	newHeadB := engineB.GetHead()

	assert.Equal(t, "0xBlock1A", newHeadB, "Minority node MUST switch to majority chain (1A) after partition heals")
	assert.True(t, engineB.HasQuorum("0xBlock1A"), "Minority node should now recognize quorum on 1A")
}
