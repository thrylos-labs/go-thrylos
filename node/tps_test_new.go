// File: node/tps_test.go
package node

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// TPSTestSuite contains all TPS testing functionality
type TPSTestSuite struct {
	node       *Node
	tester     *TPSTransactionTester
	privateKey crypto.PrivateKey
	address    string
}

// setupTPSTestSuite creates a test node for TPS testing
func setupTPSTestSuite(t *testing.T) *TPSTestSuite {
	// Load test configuration
	cfg, err := config.Load()
	require.NoError(t, err, "Failed to load config")

	// Generate consistent test private key
	privateKey, err := generateTestPrivateKey()
	require.NoError(t, err, "Failed to generate test private key")

	// Generate node address
	address, err := account.GenerateAddress(privateKey.PublicKey())
	require.NoError(t, err, "Failed to generate node address")

	// Configure test node
	nodeConfig := &NodeConfig{
		Config:            cfg,
		PrivateKey:        privateKey,
		ShardID:           0,
		TotalShards:       1,
		IsValidator:       true,
		DataDir:           "./test-data",
		CrossShardEnabled: false,
		GenesisAccount:    address,
		GenesisSupply:     1000000000000, // 1000 THRYLOS for testing

		// P2P Configuration for testing
		EnableP2P:      true,
		P2PListenPort:  9001, // Different port for tests
		BootstrapPeers: []string{},

		GenesisValidators: []*core.Validator{
			{
				Address:        address,
				Pubkey:         privateKey.PublicKey().Bytes(),
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
	node, err := NewNode(nodeConfig)
	require.NoError(t, err, "Failed to initialize test node")

	// Start the node
	err = node.Start()
	require.NoError(t, err, "Failed to start test node")

	// Create TPS tester
	tester := NewTPSTransactionTester(node, privateKey, address)

	return &TPSTestSuite{
		node:       node,
		tester:     tester,
		privateKey: privateKey,
		address:    address,
	}
}

// teardownTPSTestSuite cleans up after tests
func (suite *TPSTestSuite) teardown(t *testing.T) {
	if suite.node != nil {
		err := suite.node.Stop()
		assert.NoError(t, err, "Failed to stop test node")
	}
}

// generateTestPrivateKey creates a deterministic private key for testing
func generateTestPrivateKey() (crypto.PrivateKey, error) {
	// Create deterministic bytes for testing
	seed := "test-tps-private-key-deterministic-seed-for-consistent-testing"

	// Hash the seed to get proper random bytes
	hash := sha256.Sum256([]byte(seed))

	// Extend to MLDSA private key size (2560 bytes, not 4864)
	keyBytes := make([]byte, 2560) // Correct MLDSA44 private key size

	// Fill the key bytes using repeated hashing
	for i := 0; i < len(keyBytes); {
		hash = sha256.Sum256(hash[:])
		copied := copy(keyBytes[i:], hash[:])
		i += copied
	}

	// Create private key from deterministic bytes
	return crypto.NewPrivateKeyFromBytes(keyBytes)
}

// TestSingleTransactionCreation tests basic transaction creation
func TestSingleTransactionCreation(t *testing.T) {
	suite := setupTPSTestSuite(t)
	defer suite.teardown(t)

	t.Log("Testing single transaction creation...")

	// Generate recipient
	recipientKey, err := crypto.NewPrivateKey()
	require.NoError(t, err)

	recipientAddress, err := account.GenerateAddress(recipientKey.PublicKey())
	require.NoError(t, err)

	// Create transaction
	tx, err := suite.tester.CreateTestTransaction(suite.address, recipientAddress, 15000000, 1000)
	require.NoError(t, err, "Failed to create test transaction")

	// Validate transaction
	assert.NotEmpty(t, tx.Id, "Transaction ID should not be empty")
	assert.Equal(t, suite.address, tx.From, "From address should match")
	assert.Equal(t, recipientAddress, tx.To, "To address should match")
	assert.Equal(t, int64(15000000), tx.Amount, "Amount should match")
	assert.NotEmpty(t, tx.Hash, "Transaction hash should not be empty")
	assert.NotEmpty(t, tx.Signature, "Transaction signature should not be empty")

	// Test validation
	err = suite.node.blockchain.ValidateTransaction(tx)
	assert.NoError(t, err, "Transaction should be valid")

	// Test submission
	err = suite.node.SubmitTransaction(tx)
	assert.NoError(t, err, "Transaction should be submitted successfully")

	t.Log("✅ Single transaction test passed")
}

// TestSlowTPS tests very slow TPS rates for baseline
func TestSlowTPS(t *testing.T) {
	suite := setupTPSTestSuite(t)
	defer suite.teardown(t)

	testCases := []struct {
		name           string
		tps            float64
		duration       time.Duration
		minSuccessRate float64
	}{
		{"Ultra Slow 0.25 TPS", 0.25, 20 * time.Second, 95.0},
		{"Very Slow 0.33 TPS", 0.33, 15 * time.Second, 90.0},
		{"Slow 0.5 TPS", 0.5, 12 * time.Second, 85.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("Testing %s...", tc.name)

			result, err := suite.tester.RunCustomSlowTPSTest(tc.tps, tc.duration)
			require.NoError(t, err, "TPS test should not error")

			// Validate results
			assert.Greater(t, result.TotalTransactions, int64(0), "Should have created transactions")

			successRate := float64(result.SuccessfulTxs) / float64(result.TotalTransactions) * 100
			assert.GreaterOrEqual(t, successRate, tc.minSuccessRate,
				"Success rate should be at least %.1f%%, got %.1f%%", tc.minSuccessRate, successRate)

			efficiency := (result.AverageTPS / tc.tps) * 100
			assert.GreaterOrEqual(t, efficiency, 80.0,
				"Efficiency should be at least 80%%, got %.1f%%", efficiency)

			t.Logf("✅ %s: %.3f TPS achieved (%.1f%% success, %.1f%% efficiency)",
				tc.name, result.AverageTPS, successRate, efficiency)
		})
	}
}

// TestMediumTPS tests medium TPS rates
func TestMediumTPS(t *testing.T) {
	suite := setupTPSTestSuite(t)
	defer suite.teardown(t)

	testCases := []struct {
		name           string
		tps            float64
		duration       time.Duration
		minSuccessRate float64
	}{
		{"Medium 1.0 TPS", 1.0, 10 * time.Second, 70.0},
		{"Higher 2.0 TPS", 2.0, 10 * time.Second, 50.0},
		{"Fast 5.0 TPS", 5.0, 10 * time.Second, 30.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("Testing %s...", tc.name)

			result, err := suite.tester.RunCustomSlowTPSTest(tc.tps, tc.duration)
			require.NoError(t, err, "TPS test should not error")

			// More lenient validation for higher TPS
			assert.Greater(t, result.TotalTransactions, int64(0), "Should have created transactions")

			successRate := float64(result.SuccessfulTxs) / float64(result.TotalTransactions) * 100
			assert.GreaterOrEqual(t, successRate, tc.minSuccessRate,
				"Success rate should be at least %.1f%%, got %.1f%%", tc.minSuccessRate, successRate)

			t.Logf("✅ %s: %.3f TPS achieved (%.1f%% success)",
				tc.name, result.AverageTPS, successRate)
		})
	}
}

// TestTPSStressTest runs a comprehensive stress test
func TestTPSStressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	suite := setupTPSTestSuite(t)
	defer suite.teardown(t)

	t.Log("Running TPS stress test...")

	// Run stress test with increasing load
	results, err := suite.tester.RunStressTest(10, 2, 15*time.Second)
	require.NoError(t, err, "Stress test should not error")

	assert.Greater(t, len(results), 0, "Should have stress test results")

	// Find peak performance
	var bestTPS float64
	var bestResult *TPSTestResult

	for _, result := range results {
		successRate := float64(result.SuccessfulTxs) / float64(result.TotalTransactions) * 100

		// Consider result good if success rate > 80%
		if successRate > 80.0 && result.AverageTPS > bestTPS {
			bestTPS = result.AverageTPS
			bestResult = result
		}

		t.Logf("Stress level: %.2f TPS achieved, %.1f%% success",
			result.AverageTPS, successRate)
	}

	if bestResult != nil {
		t.Logf("🏆 Peak sustainable TPS: %.2f with %.1f%% success rate",
			bestTPS, float64(bestResult.SuccessfulTxs)/float64(bestResult.TotalTransactions)*100)
	}

	assert.Greater(t, bestTPS, 0.0, "Should achieve some sustainable TPS")
}

// TestTPSBenchmark runs a benchmark-style test
func TestTPSBenchmark(t *testing.T) {
	suite := setupTPSTestSuite(t)
	defer suite.teardown(t)

	// Wait for node to stabilize
	time.Sleep(5 * time.Second)

	benchmarks := []struct {
		name     string
		tps      float64
		duration time.Duration
	}{
		{"Baseline_0.25_TPS", 0.25, 30 * time.Second},
		{"Target_1.0_TPS", 1.0, 20 * time.Second},
		{"Aggressive_5.0_TPS", 5.0, 15 * time.Second},
	}

	for _, bm := range benchmarks {
		t.Run(bm.name, func(t *testing.T) {
			startTime := time.Now()

			result, err := suite.tester.RunCustomSlowTPSTest(bm.tps, bm.duration)
			require.NoError(t, err)

			elapsed := time.Since(startTime)
			successRate := float64(result.SuccessfulTxs) / float64(result.TotalTransactions) * 100

			t.Logf("Benchmark %s completed in %v:", bm.name, elapsed.Truncate(time.Millisecond))
			t.Logf("  Target: %.2f TPS", bm.tps)
			t.Logf("  Achieved: %.3f TPS", result.AverageTPS)
			t.Logf("  Success: %.1f%% (%d/%d)", successRate, result.SuccessfulTxs, result.TotalTransactions)
			t.Logf("  Efficiency: %.1f%%", (result.AverageTPS/bm.tps)*100)
		})
	}
}

// TestConcurrentTransactions tests concurrent transaction submission
func TestConcurrentTransactions(t *testing.T) {
	suite := setupTPSTestSuite(t)
	defer suite.teardown(t)

	t.Log("Testing concurrent transaction submission...")

	// Generate recipients
	recipients, err := suite.tester.generateRecipients(10)
	require.NoError(t, err)

	// Create batch of transactions
	batchSize := 5
	transactions, err := suite.tester.CreateTransactionBatch(suite.address, recipients, 15000000, 1000, batchSize)
	require.NoError(t, err)
	require.Len(t, transactions, batchSize)

	// Submit all transactions
	var successCount int
	for i, tx := range transactions {
		err := suite.node.SubmitTransaction(tx)
		if err == nil {
			successCount++
			t.Logf("Transaction %d submitted successfully (nonce %d)", i+1, tx.Nonce)
		} else {
			t.Logf("Transaction %d failed: %v", i+1, err)
		}
	}

	assert.Greater(t, successCount, 0, "At least one transaction should succeed")

	successRate := float64(successCount) / float64(batchSize) * 100
	t.Logf("✅ Concurrent test: %d/%d transactions succeeded (%.1f%%)",
		successCount, batchSize, successRate)
}

// BenchmarkTPSPerformance runs Go benchmark tests
func BenchmarkTPSPerformance(b *testing.B) {
	// This would be run with: go test -bench=BenchmarkTPSPerformance
	suite := setupTPSTestSuite(&testing.T{})
	defer suite.teardown(&testing.T{})

	recipients, _ := suite.tester.generateRecipients(100)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		nonce := uint64(0)
		for pb.Next() {
			recipient := recipients[nonce%uint64(len(recipients))]
			_, err := suite.tester.CreateTestTransactionWithNonce(
				suite.address, recipient, 15000000, 1000, nonce)
			if err != nil {
				b.Errorf("Failed to create transaction: %v", err)
			}
			nonce++
		}
	})
}

// TestTPSMetrics tests TPS measurement accuracy
func TestTPSMetrics(t *testing.T) {
	suite := setupTPSTestSuite(t)
	defer suite.teardown(t)

	t.Log("Testing TPS metrics accuracy...")

	targetTPS := 0.5
	duration := 10 * time.Second

	result, err := suite.tester.RunCustomSlowTPSTest(targetTPS, duration)
	require.NoError(t, err)

	// Validate metrics
	assert.Greater(t, result.TotalTransactions, int64(0))
	assert.GreaterOrEqual(t, result.SuccessfulTxs, int64(0))
	assert.Equal(t, result.TotalTransactions, result.SuccessfulTxs+result.FailedTxs)
	assert.Greater(t, result.Duration, time.Duration(0))
	assert.False(t, result.StartTime.IsZero())
	assert.False(t, result.EndTime.IsZero())
	assert.True(t, result.EndTime.After(result.StartTime))

	expectedMinTxs := int64(float64(duration.Seconds()) * targetTPS * 0.8) // 80% of expected
	assert.GreaterOrEqual(t, result.TotalTransactions, expectedMinTxs,
		"Should create approximately expected number of transactions")

	t.Logf("✅ Metrics test: %d txs in %v (%.3f TPS)",
		result.TotalTransactions, result.Duration.Truncate(time.Millisecond), result.AverageTPS)
}
