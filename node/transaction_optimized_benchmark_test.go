// File: node/transaction_optimized_benchmark_test.go
// Optimized benchmarks with shared node setup to measure true TPS

package node

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

var (
	sharedTestNode    *Node
	sharedPrivateKey  crypto.PrivateKey
	sharedNodeAddress string
	globalNonce       uint64
	nonceMutex        sync.Mutex
	setupOnce         sync.Once
)

// getNextGlobalNonce returns a unique nonce across all benchmark iterations
func getNextGlobalNonce() uint64 {
	nonceMutex.Lock()
	defer nonceMutex.Unlock()
	nonce := globalNonce
	globalNonce++
	return nonce
}

// ImprovedTPSTransactionTester with proper blockchain nonce management
type ImprovedTPSTransactionTester struct {
	node       *Node
	privateKey crypto.PrivateKey
	address    string
	nonceMu    sync.Mutex
}

// NewImprovedTPSTransactionTester creates an improved TPS tester
func NewImprovedTPSTransactionTester(node *Node, privateKey crypto.PrivateKey, address string) *ImprovedTPSTransactionTester {
	return &ImprovedTPSTransactionTester{
		node:       node,
		privateKey: privateKey,
		address:    address,
	}
}

// createBenchmarkTransactionWithBlockchainNonce creates a transaction using the blockchain's actual nonce
func (tps *ImprovedTPSTransactionTester) createBenchmarkTransactionWithBlockchainNonce(to string, amount int64) (*core.Transaction, error) {
	tps.nonceMu.Lock()
	defer tps.nonceMu.Unlock()

	// Get the current nonce from the blockchain
	nonce, err := tps.node.blockchain.GetNonce(tps.address)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %v", err)
	}

	timestamp := time.Now().UnixNano()
	txID := fmt.Sprintf("blockchain_tx_%d_%d", timestamp, nonce)

	tx := &core.Transaction{
		Id:        txID,
		From:      tps.address,
		To:        to,
		Amount:    amount,
		Gas:       21000,
		GasPrice:  1000000000, // Fixed gas price to avoid conflicts
		Nonce:     nonce,
		Type:      core.TransactionType_TRANSFER,
		Data:      nil,
		Timestamp: time.Now().Unix(),
	}

	// Use the exact hash calculation from TransactionTester
	tester := &TransactionTester{
		node:       tps.node,
		privateKey: tps.privateKey,
		address:    tps.address,
	}
	tx.Hash = tester.calculateTransactionHashExact(tx)

	// Sign the transaction
	signature, err := tester.signTransaction(tx)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %v", err)
	}
	tx.Signature = signature

	return tx, nil
}
func setupSharedTestNode(b *testing.B) (*Node, crypto.PrivateKey, string) {
	setupOnce.Do(func() {
		// Generate test private key
		nodePrivateKey, err := generateConsistentTestKey("shared-benchmark-key")
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
			DataDir:           b.TempDir(),
			CrossShardEnabled: false,
			GenesisAccount:    nodeAddress,
			GenesisSupply:     1000000000000000000, // 1B THRYLOS

			// Disable all external services
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

		// Set up the node
		testNode.isRunning = true
		if err := testNode.initializeGenesis(); err != nil {
			b.Fatalf("Failed to initialize genesis: %v", err)
		}

		// Add balance for testing
		account, err := testNode.GetAccount(nodeAddress)
		if err != nil {
			account = &core.Account{
				Address:      nodeAddress,
				Balance:      0,
				Nonce:        0,
				StakedAmount: 0,
				DelegatedTo:  make(map[string]int64),
				Rewards:      0,
			}
		}

		testBalance := int64(1000000000000000000) // 1B THRYLOS
		account.Balance += testBalance
		if err := testNode.worldState.UpdateAccountWithStorage(account); err != nil {
			b.Fatalf("Failed to add test balance: %v", err)
		}

		// Store globally
		sharedTestNode = testNode
		sharedPrivateKey = nodePrivateKey
		sharedNodeAddress = nodeAddress

		b.Logf("Shared test node initialized successfully")
	})

	return sharedTestNode, sharedPrivateKey, sharedNodeAddress
}

// BenchmarkOptimizedSequentialTPS measures sequential TPS with shared setup
func BenchmarkOptimizedSequentialTPS(b *testing.B) {
	testNode, nodePrivateKey, nodeAddress := setupSharedTestNode(b)

	// Generate recipient address
	recipientKey, _ := generateConsistentTestKey("recipient-test-key")
	recipientAddress, _ := account.GenerateAddress(recipientKey.PublicKey())

	// Create improved TPS tester
	tpsTester := NewImprovedTPSTransactionTester(testNode, nodePrivateKey, nodeAddress)

	b.ResetTimer()
	b.ReportAllocs()

	start := time.Now()

	for i := 0; i < b.N; i++ {
		tx, err := tpsTester.createBenchmarkTransactionWithBlockchainNonce(recipientAddress, 1000000000)
		if err != nil {
			b.Fatalf("Failed to create transaction: %v", err)
		}

		if err := testNode.SubmitTransaction(tx); err != nil {
			b.Fatalf("Failed to submit transaction: %v", err)
		}
	}

	duration := time.Since(start)
	if b.N > 0 && duration > 0 {
		tps := float64(b.N) / duration.Seconds()
		b.ReportMetric(tps, "tps")
		b.Logf("Sequential TPS: %.2f (processed %d transactions in %v)", tps, b.N, duration)
	}
}

// BenchmarkOptimizedParallelTPS measures parallel TPS with shared setup
func BenchmarkOptimizedParallelTPS(b *testing.B) {
	testNode, nodePrivateKey, nodeAddress := setupSharedTestNode(b)

	// Generate recipient address
	recipientKey, _ := generateConsistentTestKey("recipient-test-key")
	recipientAddress, _ := account.GenerateAddress(recipientKey.PublicKey())

	b.ResetTimer()
	b.ReportAllocs()

	start := time.Now()
	var successful int64
	var failed int64
	var mu sync.Mutex

	b.RunParallel(func(pb *testing.PB) {
		tpsTester := NewImprovedTPSTransactionTester(testNode, nodePrivateKey, nodeAddress)
		localSuccess := 0
		localFailed := 0

		for pb.Next() {
			tx, err := tpsTester.createBenchmarkTransactionWithBlockchainNonce(recipientAddress, 1000000000)
			if err != nil {
				localFailed++
				continue
			}

			if err := testNode.SubmitTransaction(tx); err != nil {
				localFailed++
			} else {
				localSuccess++
			}
		}

		mu.Lock()
		successful += int64(localSuccess)
		failed += int64(localFailed)
		mu.Unlock()
	})

	duration := time.Since(start)
	total := successful + failed
	if total > 0 && duration > 0 {
		tps := float64(successful) / duration.Seconds()
		successRate := float64(successful) / float64(total) * 100
		b.ReportMetric(tps, "tps")
		b.ReportMetric(float64(successful), "successful_txs")
		b.ReportMetric(float64(failed), "failed_txs")
		b.ReportMetric(successRate, "success_rate_percent")
		b.Logf("Parallel TPS: %.2f (processed %d successful, %d failed, %.1f%% success rate in %v)",
			tps, successful, failed, successRate, duration)
	}
}

// BenchmarkOptimizedBurstTPS measures burst TPS with shared setup
func BenchmarkOptimizedBurstTPS(b *testing.B) {
	testNode, nodePrivateKey, nodeAddress := setupSharedTestNode(b)

	burstSizes := []int{10, 50, 100} // Reduced burst sizes for better success rates

	for _, burstSize := range burstSizes {
		b.Run(fmt.Sprintf("Burst_%d", burstSize), func(b *testing.B) {
			// Generate recipient address
			recipientKey, _ := generateConsistentTestKey("recipient-test-key")
			recipientAddress, _ := account.GenerateAddress(recipientKey.PublicKey())

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				start := time.Now()
				successful := 0
				failed := 0

				tpsTester := NewImprovedTPSTransactionTester(testNode, nodePrivateKey, nodeAddress)

				// Process burst sequentially to avoid nonce conflicts
				for j := 0; j < burstSize; j++ {
					tx, err := tpsTester.createBenchmarkTransactionWithBlockchainNonce(recipientAddress, 1000000000)
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
				var tps float64
				if duration.Seconds() > 0 && successful > 0 {
					tps = float64(successful) / duration.Seconds()
				}

				successRate := float64(successful) / float64(burstSize) * 100

				// Report metrics
				b.ReportMetric(tps, "tps")
				b.ReportMetric(float64(successful), "successful_txs")
				b.ReportMetric(float64(failed), "failed_txs")
				b.ReportMetric(successRate, "success_rate_percent")
				b.ReportMetric(float64(duration.Microseconds()), "duration_us")

				if i == 0 { // Log first iteration results
					b.Logf("Burst %d: %.2f TPS, %d successful, %d failed (%.1f%% success), %v duration",
						burstSize, tps, successful, failed, successRate, duration)
				}
			}
		})
	}
}

// BenchmarkTransactionThroughput measures pure throughput without submission
func BenchmarkTransactionThroughput(b *testing.B) {
	// Reuse shared setup but don't submit to node
	_, nodePrivateKey, nodeAddress := setupSharedTestNode(b)

	// Generate recipient address
	recipientKey, _ := generateConsistentTestKey("recipient-test-key")
	recipientAddress, _ := account.GenerateAddress(recipientKey.PublicKey())

	// Create dummy tester for transaction creation
	dummyTester := &TransactionTester{
		node:       nil,
		privateKey: nodePrivateKey,
		address:    nodeAddress,
	}

	b.ResetTimer()
	b.ReportAllocs()

	start := time.Now()

	for i := 0; i < b.N; i++ {
		// Create complete transaction
		timestamp := time.Now().UnixNano()
		txID := fmt.Sprintf("throughput_tx_%d_%d", timestamp, i)

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

		// Hash and sign
		tx.Hash = dummyTester.calculateTransactionHashExact(tx)
		signature, err := dummyTester.signTransaction(tx)
		if err != nil {
			b.Fatalf("Failed to sign transaction: %v", err)
		}
		tx.Signature = signature
	}

	duration := time.Since(start)
	if b.N > 0 && duration > 0 {
		tps := float64(b.N) / duration.Seconds()
		b.ReportMetric(tps, "tps")
		b.Logf("Pure throughput: %.2f TPS (created %d transactions in %v)", tps, b.N, duration)
	}
}
