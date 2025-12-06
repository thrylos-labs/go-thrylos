package sync

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/chain"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/network/p2p"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
)

// TestStateSnapshotSecurity verifies that snapshots are checked against trusted block headers (Fix H-01)
func TestStateSnapshotSecurity(t *testing.T) {
	// 1. Setup Temporary Environment
	tmpDir, err := os.MkdirTemp("", "thrylos-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Initialize Storage & WorldState
	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	require.NoError(t, err)
	defer badgerStore.Close()

	cfg := &config.Config{} // Empty config is fine for this unit test

	// We need a real WorldState to initialize the Blockchain
	ws, err := state.NewWorldState(tmpDir, 0, 1, cfg, badgerStore)
	require.NoError(t, err)

	// Initialize Blockchain
	bcConfig := &chain.BlockchainConfig{
		Config:            cfg,
		WorldState:        ws,
		ShardID:           0,
		TotalShards:       1,
		CrossShardEnabled: false,
	}
	bc, err := chain.NewBlockchain(bcConfig)
	require.NoError(t, err)

	// 2. Create a "Trusted" Block in the Chain
	// We simulate that the node has already synced headers up to height 100
	trustedHeight := int64(100)
	trustedStateRoot := "0x1234567890abcdef_TRUSTED_ROOT"

	trustedBlock := &core.Block{
		Header: &core.BlockHeader{
			Index:     trustedHeight,
			StateRoot: trustedStateRoot, // The root we expect
			Timestamp: time.Now().Unix(),
		},
		Hash: "0xBlockHash100",
	}

	// Manually inject this block into the DB to bypass execution logic
	db := storage.NewDB(badgerStore)
	err = db.SaveBlockByHeight(trustedBlock)
	require.NoError(t, err)

	// 3. Initialize StateSyncer
	syncer := NewStateSyncer(ws, bc, nil, &SyncConfig{})

	// 4. TEST CASE A: Valid Snapshot (Matching Root)
	validSnapshot := &p2p.StateSnapshot{
		Height:    trustedHeight,
		StateRoot: trustedStateRoot,                                  // MATCHES
		Accounts:  map[string]*core.Account{"0x1": {Address: "0x1"}}, // Dummy data
		Timestamp: 0,
	}
	// [FIX] Use the Syncer's actual method to generate the valid checksum
	// instead of manually constructing the string.
	validSnapshot.Checksum = syncer.calculateChecksum(validSnapshot)

	// validateSnapshot is private...
	err = syncer.validateSnapshot(validSnapshot)
	assert.NoError(t, err, "Should accept snapshot with matching state root and valid checksum")

	// 5. TEST CASE B: Malicious Snapshot (Mismatched Root)
	maliciousSnapshot := &p2p.StateSnapshot{
		Height:    trustedHeight,
		StateRoot: "0x666_MALICIOUS_ROOT_666", // MISMATCH
		Accounts:  map[string]*core.Account{"0x1": {Address: "0x1", Balance: 1000000000}},
		Timestamp: 0,
	}
	// Calculate checksum based on malicious data (integrity check passes, but root check fails)
	maliciousSnapshot.Checksum = syncer.calculateChecksum(maliciousSnapshot)

	err = syncer.validateSnapshot(maliciousSnapshot)
	assert.Error(t, err, "Should reject snapshot with mismatched state root")
	assert.Contains(t, err.Error(), "snapshot state root mismatch", "Error message should indicate root mismatch")

	// 6. TEST CASE C: Snapshot for Unknown Block
	unknownBlockSnapshot := &p2p.StateSnapshot{
		Height:    999999, // Block doesn't exist in our chain
		StateRoot: "0xSomeRoot",
		Accounts:  map[string]*core.Account{"0x1": {}},
	}

	err = syncer.validateSnapshot(unknownBlockSnapshot)
	assert.Error(t, err, "Should reject snapshot for unknown block height")
	assert.Contains(t, err.Error(), "trusted block at height", "Error should mention missing block")

	fmt.Println("✅ Security Test H-01 Passed: State Snapshots are cryptographically verified against chain history.")
}
