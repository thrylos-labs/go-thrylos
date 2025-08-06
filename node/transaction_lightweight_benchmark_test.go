// File: node/transaction_lightweight_benchmark_test.go
// Lightweight benchmarks that focus on pure transaction processing without full node setup

package node

import (
	"fmt"
	"testing"
	"time"

	"github.com/thrylos-labs/go-thrylos/core/account"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// BenchmarkPureTransactionCreation tests just transaction object creation
func BenchmarkPureTransactionCreation(b *testing.B) {
	// Generate test keys once
	nodePrivateKey, _ := generateConsistentTestKey("benchmark-test-key")
	nodeAddress, _ := account.GenerateAddress(nodePrivateKey.PublicKey())
	recipientKey, _ := generateConsistentTestKey("recipient-test-key")
	recipientAddress, _ := account.GenerateAddress(recipientKey.PublicKey())

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		timestamp := time.Now().UnixNano()
		txID := fmt.Sprintf("pure_tx_%d_%d", timestamp, i)

		tx := &core.Transaction{
			Id:        txID,
			From:      nodeAddress,
			To:        recipientAddress,
			Amount:    1000000000,
			Gas:       21000,
			GasPrice:  1000000000,
			Nonce:     uint64(i),
			Type:      core.TransactionType_TRANSFER,
			Data:      nil,
			Timestamp: time.Now().Unix(),
		}

		// Just create, don't hash or sign
		_ = tx
	}
}

// BenchmarkPureTransactionHashing tests just the hashing algorithm
func BenchmarkPureTransactionHashing(b *testing.B) {
	// Create a sample transaction once
	tx := &core.Transaction{
		Id:        "benchmark_hash_test",
		From:      "tl1sender123",
		To:        "tl1recipient123",
		Amount:    1000000000,
		Gas:       21000,
		GasPrice:  1000000000,
		Nonce:     0,
		Type:      core.TransactionType_TRANSFER,
		Data:      nil,
		Timestamp: time.Now().Unix(),
	}

	// Create a lightweight tester without full node setup
	dummyTester := &TransactionTester{
		node:       nil, // We don't need the node for pure hashing
		privateKey: nil,
		address:    "",
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = dummyTester.calculateTransactionHashExact(tx)
	}
}

// BenchmarkPureTransactionSigning tests just the signing process
func BenchmarkPureTransactionSigning(b *testing.B) {
	// Generate test key once
	nodePrivateKey, _ := generateConsistentTestKey("benchmark-test-key")

	// Create and hash a sample transaction once
	tx := &core.Transaction{
		Id:        "benchmark_sign_test",
		From:      "tl1sender123",
		To:        "tl1recipient123",
		Amount:    1000000000,
		Gas:       21000,
		GasPrice:  1000000000,
		Nonce:     0,
		Type:      core.TransactionType_TRANSFER,
		Data:      nil,
		Timestamp: time.Now().Unix(),
	}

	dummyTester := &TransactionTester{
		node:       nil,
		privateKey: nodePrivateKey,
		address:    "",
	}

	tx.Hash = dummyTester.calculateTransactionHashExact(tx)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := dummyTester.signTransaction(tx)
		if err != nil {
			b.Fatalf("Failed to sign transaction: %v", err)
		}
	}
}

// BenchmarkFullTransactionPipeline tests the complete transaction creation pipeline
func BenchmarkFullTransactionPipeline(b *testing.B) {
	// Generate test keys once
	nodePrivateKey, _ := generateConsistentTestKey("benchmark-test-key")
	nodeAddress, _ := account.GenerateAddress(nodePrivateKey.PublicKey())
	recipientKey, _ := generateConsistentTestKey("recipient-test-key")
	recipientAddress, _ := account.GenerateAddress(recipientKey.PublicKey())

	dummyTester := &TransactionTester{
		node:       nil,
		privateKey: nodePrivateKey,
		address:    nodeAddress,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Create transaction
		timestamp := time.Now().UnixNano()
		txID := fmt.Sprintf("pipeline_tx_%d_%d", timestamp, i)

		tx := &core.Transaction{
			Id:        txID,
			From:      nodeAddress,
			To:        recipientAddress,
			Amount:    1000000000,
			Gas:       21000,
			GasPrice:  1000000000,
			Nonce:     uint64(i),
			Type:      core.TransactionType_TRANSFER,
			Data:      nil,
			Timestamp: time.Now().Unix(),
		}

		// Hash transaction
		tx.Hash = dummyTester.calculateTransactionHashExact(tx)

		// Sign transaction
		signature, err := dummyTester.signTransaction(tx)
		if err != nil {
			b.Fatalf("Failed to sign transaction: %v", err)
		}
		tx.Signature = signature
	}
}

// BenchmarkMLDSAKeyGeneration tests key generation performance
func BenchmarkMLDSAKeyGeneration(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		seed := fmt.Sprintf("test-seed-%d", i)
		_, err := generateConsistentTestKey(seed)
		if err != nil {
			b.Fatalf("Failed to generate key: %v", err)
		}
	}
}

// BenchmarkAddressGeneration tests address generation from public key
func BenchmarkAddressGeneration(b *testing.B) {
	// Generate one key to reuse
	privateKey, _ := generateConsistentTestKey("benchmark-test-key")
	publicKey := privateKey.PublicKey()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := account.GenerateAddress(publicKey)
		if err != nil {
			b.Fatalf("Failed to generate address: %v", err)
		}
	}
}
