package transaction

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// Helper to create a minimal valid transaction for testing
// CHANGED: Now accepts a nonce to prevent collisions
func createTestTx(id string, nonce uint64) *core.Transaction {
	return &core.Transaction{
		Id:        id,
		Hash:      id + "_hash", // distinct hash
		From:      "0xSenderAddress",
		To:        "0xRecipientAddress",
		Gas:       21000,
		GasPrice:  10,
		Amount:    100,
		Signature: []byte("valid_signature"),
		Timestamp: time.Now().Unix(),
		Nonce:     nonce, // Unique nonce
	}
}

func TestCleanupExpired(t *testing.T) {
	// 1. Setup Pool
	// Shard 0, Total 1, Max 100 txs, MinGas 1, No AccountManager needed for this specific test
	pool := NewPool(0, 1, 100, 1, nil)

	// 2. Create two transactions with DIFFERENT NONCES
	// If nonces are the same, the pool thinks we are trying to replace the transaction
	txFresh := createTestTx("tx_fresh", 1)
	txExpired := createTestTx("tx_expired", 2)

	// 3. Add both to the pool
	err := pool.AddTransaction(txFresh)
	assert.NoError(t, err)

	err = pool.AddTransaction(txExpired)
	assert.NoError(t, err)

	// Verify both are currently in the pool
	assert.True(t, pool.HasTransaction(txFresh.Id))
	assert.True(t, pool.HasTransaction(txExpired.Id))

	// 4. MANIPULATE TIME
	// Access the private 'pending' map to simulate aging
	pool.mu.Lock()
	if entry, ok := pool.pending[txExpired.Id]; ok {
		// Set it to 25 hours ago (older than 24h TTL)
		entry.ReceivedAt = time.Now().Add(-(config.TransactionPoolTTL + time.Hour))
		fmt.Printf("DEBUG: Manually aged tx_expired to: %v\n", entry.ReceivedAt)
	}
	pool.mu.Unlock()

	// 5. Execute Cleanup
	pool.CleanupExpired()

	// 6. Assertions
	// The fresh transaction should still be there
	_, err = pool.GetTransaction(txFresh.Id)
	assert.NoError(t, err, "Fresh transaction should remain in pool")

	// The expired transaction should be gone
	_, err = pool.GetTransaction(txExpired.Id)
	assert.Error(t, err, "Expired transaction should have been removed")
	assert.Contains(t, err.Error(), "not found")

	// Verify stats updated
	stats := pool.GetStats()
	assert.Equal(t, 1, stats.PendingCount, "Pool should have exactly 1 transaction left")
}

func TestCleanupStaleTransactions(t *testing.T) {
	pool := NewPool(0, 1, 100, 1, nil)

	txOldTimestamp := createTestTx("tx_old_timestamp", 5)
	// Set the signature timestamp to 2 hours ago
	txOldTimestamp.Timestamp = time.Now().Add(-2 * time.Hour).Unix()

	err := pool.AddTransaction(txOldTimestamp)
	assert.NoError(t, err)

	// Cleanup anything older than 1 hour
	removedCount := pool.CleanupStaleTransactions(1 * time.Hour)

	assert.Equal(t, 1, removedCount)
	assert.False(t, pool.HasTransaction("tx_old_timestamp"))
}
