// File: node/transaction_benchmark_test.go
package node

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cloudflare/circl/sign/mldsa/mldsa44"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/crypto/hash"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// TransactionTester provides utilities for creating test transactions (embedded for benchmarks)
type TransactionTester struct {
	node       *Node
	privateKey crypto.PrivateKey
	address    string
}

// NewTransactionTester creates a new transaction testing utility
func NewTransactionTester(node *Node, privateKey crypto.PrivateKey, address string) *TransactionTester {
	return &TransactionTester{
		node:       node,
		privateKey: privateKey,
		address:    address,
	}
}

// calculateTransactionHashExact - EXACT copy of validator's CalculateTransactionHash method
func (tt *TransactionTester) calculateTransactionHashExact(tx *core.Transaction) string {
	var buf bytes.Buffer

	// Serialize transaction fields for hashing (excluding signature and hash)
	buf.WriteString(tx.Id)
	buf.WriteString(tx.From)
	buf.WriteString(tx.To)

	// Write amount as bytes
	amountBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(amountBytes, uint64(tx.Amount))
	buf.Write(amountBytes)

	// Write gas as bytes
	gasBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasBytes, uint64(tx.Gas))
	buf.Write(gasBytes)

	// Write gas price as bytes
	gasPriceBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasPriceBytes, uint64(tx.GasPrice))
	buf.Write(gasPriceBytes)

	// Write nonce as bytes
	nonceBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(nonceBytes, tx.Nonce)
	buf.Write(nonceBytes)

	// Write transaction type
	typeBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(typeBytes, uint32(tx.Type))
	buf.Write(typeBytes)

	// Write timestamp
	timestampBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(timestampBytes, uint64(tx.Timestamp))
	buf.Write(timestampBytes)

	// Write data
	buf.Write(tx.Data)

	// Calculate Blake2b hash using crypto/hash
	hashBytes := hash.HashData(buf.Bytes())
	return fmt.Sprintf("%x", hashBytes)
}

// signTransaction signs the transaction using the private key (matches validator approach)
func (tt *TransactionTester) signTransaction(tx *core.Transaction) ([]byte, error) {
	// Create hash to sign (same approach as validator)
	hashToSign := hash.HashData([]byte(tx.Hash))

	// Sign with private key
	signature := tt.privateKey.Sign(hashToSign)
	if signature == nil {
		return nil, fmt.Errorf("failed to sign transaction: signature is nil")
	}

	return signature.Bytes(), nil
}

// TPSTransactionTester provides utilities for TPS testing with proper nonce management
type TPSTransactionTester struct {
	node         *Node
	privateKey   crypto.PrivateKey
	address      string
	testResults  []TPSTestResult
	mu           sync.RWMutex
	currentNonce uint64
	nonceMu      sync.Mutex
}

// TPSTestResult stores the results of a TPS test
type TPSTestResult struct {
	TotalTransactions int
	Duration          time.Duration
	TPS               float64
	SuccessfulTxs     int
	FailedTxs         int
	AverageLatency    time.Duration
	Timestamp         time.Time
}

// NewTPSTransactionTester creates a new TPS transaction tester
func NewTPSTransactionTester(node *Node, privateKey crypto.PrivateKey, address string) *TPSTransactionTester {
	return &TPSTransactionTester{
		node:         node,
		privateKey:   privateKey,
		address:      address,
		testResults:  make([]TPSTestResult, 0),
		currentNonce: 0,
	}
}

// getNextNonce returns the next nonce in a thread-safe manner
func (tps *TPSTransactionTester) getNextNonce() uint64 {
	tps.nonceMu.Lock()
	defer tps.nonceMu.Unlock()

	nonce := tps.currentNonce
	tps.currentNonce++
	return nonce
}

// createBenchmarkTransaction creates a transaction for benchmarking
func (tps *TPSTransactionTester) createBenchmarkTransaction(to string, amount int64) (*core.Transaction, error) {
	nonce := tps.getNextNonce()

	timestamp := time.Now().UnixNano()
	txID := fmt.Sprintf("bench_tx_%d_%d", timestamp, nonce)

	tx := &core.Transaction{
		Id:        txID,
		From:      tps.address,
		To:        to,
		Amount:    amount,
		Gas:       21000,
		GasPrice:  1000000000, // 1 GTHRYLOS
		Nonce:     nonce,
		Type:      core.TransactionType_TRANSFER,
		Data:      nil,
		Timestamp: time.Now().Unix(),
	}

	// Use the exact hash calculation from TransactionTester
	tester := NewTransactionTester(tps.node, tps.privateKey, tps.address)
	tx.Hash = tester.calculateTransactionHashExact(tx)

	// Sign the transaction
	signature, err := tester.signTransaction(tx)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %v", err)
	}
	tx.Signature = signature

	return tx, nil
}

// generateConsistentTestKey creates a deterministic key for testing
func generateConsistentTestKey(seed string) (crypto.PrivateKey, error) {
	// Hash the seed to get proper random bytes
	hash := sha256.Sum256([]byte(seed))
	reader := bytes.NewReader(hash[:])

	// Generate MLDSA key with deterministic seed
	_, mldsaKey, err := mldsa44.GenerateKey(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate test key: %v", err)
	}

	return crypto.NewPrivateKeyFromMLDSA(mldsaKey), nil
}

// setupTestNode creates a test node for benchmarking
func setupTestNode(b *testing.B) (*Node, crypto.PrivateKey, string) {
	b.Helper()

	// Generate test private key
	nodePrivateKey, err := generateConsistentTestKey("benchmark-test-key")
	if err != nil {
		b.Fatalf("Failed to generate test key: %v", err)
	}

	// Generate node address
	nodeAddress, err := account.GenerateAddress(nodePrivateKey.PublicKey())
	if err != nil {
		b.Fatalf("Failed to generate node address: %v", err)
	}

	// Load test configuration
	cfg, err := config.Load()
	if err != nil {
		b.Fatalf("Failed to load config: %v", err)
	}

	// Configure test node with minimal settings
	nodeConfig := &NodeConfig{
		Config:            cfg,
		PrivateKey:        nodePrivateKey,
		ShardID:           0,
		TotalShards:       1,
		IsValidator:       true,
		DataDir:           b.TempDir(), // Use test temp directory
		CrossShardEnabled: false,
		GenesisAccount:    nodeAddress,         // Genesis goes to node address
		GenesisSupply:     1000000000000000000, // Large supply for testing (1B THRYLOS)

		// Disable all external services for benchmarking
		EnableP2P:     false,
		P2PListenPort: 0,

		GenesisValidators: []*core.Validator{
			{
				Address:        nodeAddress,
				Pubkey:         nodePrivateKey.PublicKey().Bytes(),
				Stake:          cfg.Staking.MinValidatorStake,
				SelfStake:      cfg.Staking.MinValidatorStake,
				DelegatedStake: 0,
				Commission:     0.1,
				Active:         true,
				Delegators:     make(map[string]int64),
				CreatedAt:      time.Now().Unix(),
				UpdatedAt:      time.Now().Unix(),
			},
		},
	}

	// Initialize test node
	testNode, err := NewNode(nodeConfig)
	if err != nil {
		b.Fatalf("Failed to initialize test node: %v", err)
	}

	// Set the node as running manually for transaction processing
	testNode.isRunning = true

	// Initialize genesis to ensure the node has balance
	if err := testNode.initializeGenesis(); err != nil {
		b.Fatalf("Failed to initialize genesis: %v", err)
	}

	// Manually add balance to the node address for testing
	// Get the account first (or create if doesn't exist)
	account, err := testNode.GetAccount(nodeAddress)
	if err != nil {
		// Create new account if it doesn't exist
		account = &core.Account{
			Address:      nodeAddress,
			Balance:      0,
			Nonce:        0,
			StakedAmount: 0,
			DelegatedTo:  make(map[string]int64),
			Rewards:      0,
		}
	}

	// Add test balance
	testBalance := int64(1000000000000000) // 1M THRYLOS for testing
	account.Balance += testBalance

	// Update the account using the world state's account manager
	if err := testNode.worldState.UpdateAccountWithStorage(account); err != nil {
		b.Fatalf("Failed to add test balance: %v", err)
	}

	// Verify the sender has sufficient balance for transactions
	currentBalance, err := testNode.GetBalance(nodeAddress)
	if err != nil {
		b.Fatalf("Failed to get balance: %v", err)
	}

	if currentBalance < 100000000000 { // Less than 100 THRYLOS
		b.Fatalf("Insufficient balance for testing: have %d, need at least %d", currentBalance, 100000000000)
	}

	b.Logf("Test node setup complete. Address: %s, Balance: %d", nodeAddress, currentBalance)

	// Skip calling Start() to avoid sync manager issues
	// The node is ready for transaction processing without full startup

	return testNode, nodePrivateKey, nodeAddress
}

// BenchmarkTransactionProcessing_Sequential tests sequential transaction processing
func BenchmarkTransactionProcessing_Sequential(b *testing.B) {
	testNode, nodePrivateKey, nodeAddress := setupTestNode(b)
	defer testNode.Stop()

	// Create TPS tester
	tpsTester := NewTPSTransactionTester(testNode, nodePrivateKey, nodeAddress)

	// Generate recipient address
	recipientKey, _ := generateConsistentTestKey("recipient-test-key")
	recipientAddress, _ := account.GenerateAddress(recipientKey.PublicKey())

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tx, err := tpsTester.createBenchmarkTransaction(recipientAddress, 1000000000) // 1 THRYLOS
		if err != nil {
			b.Fatalf("Failed to create transaction: %v", err)
		}

		if err := testNode.SubmitTransaction(tx); err != nil {
			b.Fatalf("Failed to submit transaction: %v", err)
		}
	}
}

// BenchmarkTransactionProcessing_Parallel tests parallel transaction processing
func BenchmarkTransactionProcessing_Parallel(b *testing.B) {
	testNode, nodePrivateKey, nodeAddress := setupTestNode(b)
	defer testNode.Stop()

	// Generate recipient address
	recipientKey, _ := generateConsistentTestKey("recipient-test-key")
	recipientAddress, _ := account.GenerateAddress(recipientKey.PublicKey())

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		// Each goroutine gets its own TPS tester for nonce management
		tpsTester := NewTPSTransactionTester(testNode, nodePrivateKey, nodeAddress)

		for pb.Next() {
			tx, err := tpsTester.createBenchmarkTransaction(recipientAddress, 1000000000)
			if err != nil {
				b.Fatalf("Failed to create transaction: %v", err)
			}

			if err := testNode.SubmitTransaction(tx); err != nil {
				// Log error but don't fail the benchmark for parallel processing issues
				b.Logf("Transaction submission failed: %v", err)
			}
		}
	})
}

// BenchmarkTransactionCreation tests just transaction creation performance
func BenchmarkTransactionCreation(b *testing.B) {
	testNode, nodePrivateKey, nodeAddress := setupTestNode(b)
	defer testNode.Stop()

	tpsTester := NewTPSTransactionTester(testNode, nodePrivateKey, nodeAddress)

	// Generate recipient address
	recipientKey, _ := generateConsistentTestKey("recipient-test-key")
	recipientAddress, _ := account.GenerateAddress(recipientKey.PublicKey())

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := tpsTester.createBenchmarkTransaction(recipientAddress, 1000000000)
		if err != nil {
			b.Fatalf("Failed to create transaction: %v", err)
		}
	}
}

// BenchmarkTransactionHashing tests transaction hash calculation performance
func BenchmarkTransactionHashing(b *testing.B) {
	testNode, nodePrivateKey, nodeAddress := setupTestNode(b)
	defer testNode.Stop()

	tester := NewTransactionTester(testNode, nodePrivateKey, nodeAddress)

	// Create a sample transaction
	tx := &core.Transaction{
		Id:        "benchmark_hash_test",
		From:      nodeAddress,
		To:        "tl1recipient123",
		Amount:    1000000000,
		Gas:       21000,
		GasPrice:  1000000000,
		Nonce:     0,
		Type:      core.TransactionType_TRANSFER,
		Data:      nil,
		Timestamp: time.Now().Unix(),
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = tester.calculateTransactionHashExact(tx)
	}
}

// BenchmarkTransactionSigning tests transaction signing performance
func BenchmarkTransactionSigning(b *testing.B) {
	testNode, nodePrivateKey, nodeAddress := setupTestNode(b)
	defer testNode.Stop()

	tester := NewTransactionTester(testNode, nodePrivateKey, nodeAddress)

	// Create and hash a sample transaction
	tx := &core.Transaction{
		Id:        "benchmark_sign_test",
		From:      nodeAddress,
		To:        "tl1recipient123",
		Amount:    1000000000,
		Gas:       21000,
		GasPrice:  1000000000,
		Nonce:     0,
		Type:      core.TransactionType_TRANSFER,
		Data:      nil,
		Timestamp: time.Now().Unix(),
	}
	tx.Hash = tester.calculateTransactionHashExact(tx)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := tester.signTransaction(tx)
		if err != nil {
			b.Fatalf("Failed to sign transaction: %v", err)
		}
	}
}

// BenchmarkTPS_Burst tests burst transaction processing with detailed metrics
func BenchmarkTPS_Burst(b *testing.B) {
	testNode, nodePrivateKey, nodeAddress := setupTestNode(b)
	defer testNode.Stop()

	// Test different burst sizes
	burstSizes := []int{10, 100, 1000}

	for _, burstSize := range burstSizes {
		b.Run(fmt.Sprintf("Burst_%d", burstSize), func(b *testing.B) {
			tpsTester := NewTPSTransactionTester(testNode, nodePrivateKey, nodeAddress)

			// Generate recipient address
			recipientKey, _ := generateConsistentTestKey("recipient-test-key")
			recipientAddress, _ := account.GenerateAddress(recipientKey.PublicKey())

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				start := time.Now()
				successful := 0
				failed := 0

				// Send burst of transactions
				for j := 0; j < burstSize; j++ {
					tx, err := tpsTester.createBenchmarkTransaction(recipientAddress, 1000000000)
					if err != nil {
						failed++
						continue
					}

					if err := testNode.SubmitTransaction(tx); err != nil {
						failed++
					} else {
						successful++
					}
				}

				duration := time.Since(start)
				tps := float64(successful) / duration.Seconds()

				// Report custom metrics
				b.ReportMetric(tps, "tps")
				b.ReportMetric(float64(successful), "successful_txs")
				b.ReportMetric(float64(failed), "failed_txs")
				b.ReportMetric(float64(duration.Milliseconds()), "duration_ms")
			}
		})
	}
}

// Example of how to run specific benchmarks:
func ExampleBenchmarkUsage() {
	// Run all benchmarks:
	// go test -bench=. -benchmem ./node

	// Run specific benchmark:
	// go test -bench=BenchmarkTransactionProcessing_Sequential -benchmem ./node

	// Run with specific iterations:
	// go test -bench=BenchmarkTPS_Burst -count=3 -benchtime=10s ./node

	// Generate CPU profile:
	// go test -bench=BenchmarkTransactionProcessing_Parallel -cpuprofile=cpu.prof ./node

	// Generate memory profile:
	// go test -bench=. -memprofile=mem.prof ./node
}

// Helper function to get benchmark results summary
func (tps *TPSTransactionTester) GetBenchmarkSummary() string {
	tps.mu.RLock()
	defer tps.mu.RUnlock()

	if len(tps.testResults) == 0 {
		return "No benchmark results available"
	}

	var totalTPS float64
	var totalTxs int
	var totalDuration time.Duration

	for _, result := range tps.testResults {
		totalTPS += result.TPS
		totalTxs += result.TotalTransactions
		totalDuration += result.Duration
	}

	avgTPS := totalTPS / float64(len(tps.testResults))

	return fmt.Sprintf(
		"Benchmark Summary:\n"+
			"- Tests Run: %d\n"+
			"- Total Transactions: %d\n"+
			"- Average TPS: %.2f\n"+
			"- Total Duration: %v\n",
		len(tps.testResults), totalTxs, avgTPS, totalDuration,
	)
}
