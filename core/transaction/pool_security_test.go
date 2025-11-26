package transaction

import (
	"testing"
	"time"

	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

func TestDoubleSpendProtection(t *testing.T) {
	// 1. Setup - Use BeaconShardID (-1) to bypass shard ownership checks for this test
	shardID := account.BeaconShardID
	am := account.NewAccountManager(shardID, 10)

	// ✅ FIX: Add "0x" prefix + 40 hex characters
	sender := "0x1111111111111111111111111111111111111111"
	recipient := "0x2222222222222222222222222222222222222222"

	// Create an account with balance so balance checks pass
	am.SetAccount(sender, &core.Account{
		Address: sender,
		Balance: 1000000000, // Plenty of balance
		Nonce:   0,
	})

	// Create Pool (Max 100 txs, MinGas 1)
	pool := NewPool(shardID, 10, 100, 1, am)

	// 2. Create Transaction A (Nonce 1, GasPrice 10)
	txA := &core.Transaction{
		Id:        "tx_A",
		Hash:      "hash_A",
		From:      sender,
		To:        recipient,
		Nonce:     1,
		Amount:    100,
		Gas:       21000,
		GasPrice:  10,
		Timestamp: time.Now().Unix(),
		Signature: []byte("sig_A"), // Mock signature
	}

	// 3. Create Transaction B (Nonce 1, GasPrice 10) - CONFLICTING NONCE
	txB := &core.Transaction{
		Id:        "tx_B",
		Hash:      "hash_B",
		From:      sender,
		To:        recipient,
		Nonce:     1, // SAME NONCE AS A
		Amount:    100,
		Gas:       21000,
		GasPrice:  10, // SAME PRICE
		Timestamp: time.Now().Unix(),
		Signature: []byte("sig_B"),
	}

	// 4. Create Transaction C (Nonce 1, GasPrice 20) - REPLACE BY FEE
	txC := &core.Transaction{
		Id:        "tx_C",
		Hash:      "hash_C",
		From:      sender,
		To:        recipient,
		Nonce:     1, // SAME NONCE
		Amount:    100,
		Gas:       21000,
		GasPrice:  20, // HIGHER PRICE
		Timestamp: time.Now().Unix(),
		Signature: []byte("sig_C"),
	}

	// --- TEST EXECUTION ---

	// Step 1: Add First Transaction (Should Succeed)
	if err := pool.AddTransaction(txA); err != nil {
		t.Fatalf("Failed to add valid Tx A: %v", err)
	}
	if !pool.HasTransaction("tx_A") {
		t.Fatal("Pool should contain Tx A")
	}

	// Step 2: Add Duplicate Nonce (Should Fail)
	err := pool.AddTransaction(txB)
	if err == nil {
		t.Fatal("SECURITY FAIL: Pool accepted duplicate nonce with same gas price!")
	} else {
		t.Logf("✅ Passed: Rejected duplicate nonce as expected: %v", err)
	}

	// Step 3: Add Replacement (Should Succeed because Price is higher)
	if err := pool.AddTransaction(txC); err != nil {
		t.Fatalf("Failed to replace transaction with higher gas price: %v", err)
	}

	// Step 4: Verify State
	if pool.HasTransaction("tx_A") {
		t.Fatal("Tx A should have been removed (replaced)")
	}
	if !pool.HasTransaction("tx_C") {
		t.Fatal("Tx C should be in the pool")
	}

	t.Log("✅ Double Spend Protection & Replace-By-Fee logic verified.")
}
