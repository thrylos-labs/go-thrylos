package chain

// import (
// 	"math/big"
// 	"testing"
// 	"time"

// 	"github.com/stretchr/testify/assert"
// )

// func TestBlockProcessing_ProcessesUnbondingQueue(t *testing.T) {
// 	blockchain := setupBlockchain(t)

// 	// Setup delegation
// 	setupDelegation(t, "delegator1", "validator1", "10000")
// //
// 	// Undelegate
// 	amount := big.NewInt(1000000000000000000000)
// 	err := blockchain.worldState.GetStakingManager().Undelegate(
// 		"delegator1", "validator1", amount)
// 	assert.NoError(t, err)

// 	// Verify entry in queue
// 	entries := blockchain.worldState.GetUnbondingEntries("delegator1")
// 	assert.Len(t, entries, 1)

// 	// Set completion time to past
// 	blockchain.worldState.unbondingMu.Lock()
// 	blockchain.worldState.unbondingQueue[0].CompletionTime = time.Now().Add(-1 * time.Hour).Unix()
// 	blockchain.worldState.unbondingMu.Unlock()

// 	// Create and add a block (this should trigger processing)
// 	block := createTestBlock(t)
// 	err = blockchain.AddBlock(block)
// 	assert.NoError(t, err)

// 	// Verify funds were released
// 	delegator, _ := blockchain.worldState.GetAccount("delegator1")
// 	assert.Equal(t, amount.String(), delegator.Balance)

// 	// Verify queue is empty
// 	entries = blockchain.worldState.GetUnbondingEntries("delegator1")
// 	assert.Len(t, entries, 0)
// }
