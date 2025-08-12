package core

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/core/transaction"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/crypto/address"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
)

const BaseUnit = int64(1000000000) // 1 THRYLOS

// FinalityResult contains comprehensive finality metrics
type FinalityResult struct {
	TransactionID       string        `json:"transaction_id"`
	SubmissionTime      time.Time     `json:"submission_time"`
	InclusionTime       time.Time     `json:"inclusion_time"`
	FinalityTime        time.Time     `json:"finality_time"`
	InclusionDelay      time.Duration `json:"inclusion_delay"`       // Time to first block
	FinalityDelay       time.Duration `json:"finality_delay"`        // Time from inclusion to finality
	TotalFinalityTime   time.Duration `json:"total_finality_time"`   // Total time to finality
	ConfirmationBlocks  int           `json:"confirmation_blocks"`   // Number of confirming blocks
	IncludedInBlock     int64         `json:"included_in_block"`     // Block number where included
	FinalizedAtBlock    int64         `json:"finalized_at_block"`    // Block number where finalized
	TransactionPosition int           `json:"transaction_position"`  // Position in block
	BlockProcessingTime time.Duration `json:"block_processing_time"` // Time to process each block
}

// FinalityTestManager manages comprehensive finality testing
type FinalityTestManager struct {
	worldState       *state.WorldState
	txValidator      *transaction.Validator
	genesisAddress   string
	validators       []*core.Validator
	results          []*FinalityResult
	mu               sync.RWMutex
	blockInterval    time.Duration
	requiredConfirms int
}

// TestComprehensiveFinality - Real-world time to finality test
func TestComprehensiveFinality(t *testing.T) {
	// Test Configuration
	testConfig := map[string]interface{}{
		"transaction_count":      5,                // Number of transactions to test
		"required_confirmations": 3,                // Blocks needed for finality
		"block_interval":         1 * time.Second,  // Time between blocks
		"max_finality_time":      30 * time.Second, // Maximum expected finality time
		"validator_count":        4,                // Number of validators
	}

	t.Logf("🚀 Starting Comprehensive Time-to-Finality Test")
	t.Logf("   Transactions: %d", testConfig["transaction_count"])
	t.Logf("   Required Confirmations: %d", testConfig["required_confirmations"])
	t.Logf("   Block Interval: %v", testConfig["block_interval"])
	t.Logf("   Max Finality Time: %v", testConfig["max_finality_time"])

	// Initialize test environment
	manager, err := setupFinalityTestEnvironment(t, testConfig)
	require.NoError(t, err)
	defer manager.cleanup()

	// Run the comprehensive finality test
	ctx, cancel := context.WithTimeout(context.Background(), testConfig["max_finality_time"].(time.Duration)*2)
	defer cancel()

	results, err := manager.runFinalityTest(ctx, testConfig)
	require.NoError(t, err)
	require.Len(t, results, testConfig["transaction_count"].(int))

	// Analyze and report results
	manager.analyzeResults(t, results, testConfig)
}

// setupFinalityTestEnvironment initializes the test environment
func setupFinalityTestEnvironment(t *testing.T, testConfig map[string]interface{}) (*FinalityTestManager, error) {
	// Load blockchain config
	config, err := config.Load()
	if err != nil {
		return nil, err
	}

	// Use existing genesis account
	genesisAddress := config.Genesis.Accounts[0].Address

	// Create fresh storage
	dataDir := fmt.Sprintf("/tmp/finality_test_%d", time.Now().UnixNano())
	badgerStorage, err := storage.NewBadgerStorage(dataDir)
	if err != nil {
		return nil, err
	}

	// Initialize WorldState
	worldState, err := state.NewWorldState(dataDir, account.ShardID(0), 1, config, badgerStorage)
	if err != nil {
		badgerStorage.Close()
		return nil, err
	}

	// Verify genesis account balance
	balance, err := worldState.GetBalance(genesisAddress)
	if err != nil {
		return nil, err
	}
	t.Logf("💰 Genesis account balance: %d THRYLOS", balance/BaseUnit)
	require.Greater(t, balance, int64(0), "Genesis account should have balance")

	// Create transaction validator
	txValidator := transaction.NewValidator(account.ShardID(0), 1, config)

	// Create validators for the test
	validatorCount := testConfig["validator_count"].(int)
	validators := make([]*core.Validator, validatorCount)

	for i := 0; i < validatorCount; i++ {
		validator := &core.Validator{
			Address:        fmt.Sprintf("validator_%d_%s", i, genesisAddress),
			Pubkey:         []byte(fmt.Sprintf("pubkey_%d", i)),
			Stake:          100000 * BaseUnit,
			SelfStake:      100000 * BaseUnit,
			DelegatedStake: 0,
			Delegators:     make(map[string]int64),
			Commission:     0.05,
			Active:         true,
			CreatedAt:      time.Now().Unix(),
			UpdatedAt:      time.Now().Unix(),
		}

		err = worldState.AddValidator(validator)
		if err != nil {
			return nil, fmt.Errorf("failed to add validator %d: %v", i, err)
		}

		validators[i] = validator
	}

	return &FinalityTestManager{
		worldState:       worldState,
		txValidator:      txValidator,
		genesisAddress:   genesisAddress,
		validators:       validators,
		results:          make([]*FinalityResult, 0),
		blockInterval:    testConfig["block_interval"].(time.Duration),
		requiredConfirms: testConfig["required_confirmations"].(int),
	}, nil
}

// runFinalityTest executes the comprehensive finality test
func (m *FinalityTestManager) runFinalityTest(ctx context.Context, testConfig map[string]interface{}) ([]*FinalityResult, error) {
	transactionCount := testConfig["transaction_count"].(int)

	// Start block production in background
	go m.simulateBlockProduction(ctx)

	// Create and submit transactions
	var wg sync.WaitGroup
	for i := 0; i < transactionCount; i++ {
		wg.Add(1)
		go func(txIndex int) {
			defer wg.Done()

			// Create transaction
			tx, err := m.createTestTransaction(txIndex)
			if err != nil {
				fmt.Printf("❌ Failed to create transaction %d: %v\n", txIndex, err)
				return
			}

			// Submit transaction and track finality
			result, err := m.submitAndTrackTransaction(ctx, tx)
			if err != nil {
				fmt.Printf("❌ Failed to track transaction %d: %v\n", txIndex, err)
				return
			}

			m.mu.Lock()
			m.results = append(m.results, result)
			m.mu.Unlock()
		}(i)

		// Stagger transaction submissions
		time.Sleep(200 * time.Millisecond)
	}

	// Wait for all transactions to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return m.results, nil
	case <-ctx.Done():
		return m.results, fmt.Errorf("test timeout: %v", ctx.Err())
	}
}

// createTestTransaction creates a test transaction
func (m *FinalityTestManager) createTestTransaction(index int) (*core.Transaction, error) {
	// Generate recipient address
	recipientPrivKey, err := crypto.NewPrivateKey()
	if err != nil {
		return nil, err
	}

	recipientAddress, err := address.GenerateAddress(recipientPrivKey.PublicKey().Bytes())
	if err != nil {
		return nil, err
	}

	// Create transaction
	tx, err := m.txValidator.CreateTransferTransaction(
		m.genesisAddress,
		recipientAddress,
		1000*config.BaseUnit, // 1000 THRYLOS
		21000,                // gas
		1000,                 // gas price
		uint64(index),        // nonce
	)
	if err != nil {
		return nil, err
	}

	// Set dummy signature for testing
	tx.Signature = []byte(fmt.Sprintf("test_signature_%d", index))

	return tx, nil
}

// submitAndTrackTransaction submits a transaction and tracks its finality
func (m *FinalityTestManager) submitAndTrackTransaction(ctx context.Context, tx *core.Transaction) (*FinalityResult, error) {
	result := &FinalityResult{
		TransactionID:  tx.Id,
		SubmissionTime: time.Now(),
	}

	// Submit transaction
	err := m.worldState.AddTransaction(tx)
	if err != nil {
		return nil, fmt.Errorf("failed to submit transaction: %v", err)
	}

	fmt.Printf("📤 Transaction %s submitted\n", tx.Id[:8])

	// Track until finality is achieved
	return m.trackTransactionFinality(ctx, tx.Id, result)
}

// trackTransactionFinality tracks a transaction until it achieves finality
func (m *FinalityTestManager) trackTransactionFinality(ctx context.Context, txID string, result *FinalityResult) (*FinalityResult, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	inclusionDetected := false

	for {
		select {
		case <-ctx.Done():
			return result, fmt.Errorf("tracking timeout")
		case <-ticker.C:
			// Check if transaction is included in a block
			if !inclusionDetected {
				blockNum, position := m.findTransactionInBlock(txID)
				if blockNum >= 0 {
					result.InclusionTime = time.Now()
					result.InclusionDelay = result.InclusionTime.Sub(result.SubmissionTime)
					result.IncludedInBlock = blockNum
					result.TransactionPosition = position
					inclusionDetected = true

					fmt.Printf("📦 Transaction %s included in block %d (position %d) after %v\n",
						txID[:8], blockNum, position, result.InclusionDelay)
				}
			}

			// Check for finality (required confirmations)
			if inclusionDetected {
				currentHeight := m.worldState.GetHeight()
				confirmations := int(currentHeight - result.IncludedInBlock)

				if confirmations >= m.requiredConfirms {
					result.FinalityTime = time.Now()
					result.FinalityDelay = result.FinalityTime.Sub(result.InclusionTime)
					result.TotalFinalityTime = result.FinalityTime.Sub(result.SubmissionTime)
					result.ConfirmationBlocks = confirmations
					result.FinalizedAtBlock = currentHeight

					fmt.Printf("✅ Transaction %s achieved finality at block %d (%d confirmations) - Total: %v\n",
						txID[:8], currentHeight, confirmations, result.TotalFinalityTime)

					return result, nil
				}
			}
		}
	}
}

// findTransactionInBlock searches for a transaction in the blockchain
func (m *FinalityTestManager) findTransactionInBlock(txID string) (int64, int) {
	currentHeight := m.worldState.GetHeight()

	// Search recent blocks
	for i := currentHeight; i >= 0 && i > currentHeight-10; i-- {
		block, err := m.worldState.GetBlock(i)
		if err != nil {
			continue
		}

		for pos, tx := range block.Transactions {
			if tx.Id == txID {
				return i, pos
			}
		}
	}

	return -1, -1
}

// simulateBlockProduction simulates regular block production
func (m *FinalityTestManager) simulateBlockProduction(ctx context.Context) {
	ticker := time.NewTicker(m.blockInterval)
	defer ticker.Stop()

	validatorIndex := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := m.produceBlock(validatorIndex)
			if err != nil {
				fmt.Printf("⚠️  Block production failed: %v\n", err)
			}
			validatorIndex = (validatorIndex + 1) % len(m.validators)
		}
	}
}

// produceBlock creates and adds a new block
func (m *FinalityTestManager) produceBlock(validatorIndex int) error {
	validator := m.validators[validatorIndex]

	// Get pending transactions
	pendingTxs := m.worldState.GetPendingTransactions()
	if len(pendingTxs) == 0 {
		return nil // No transactions to include
	}

	// Get current block info
	currentBlock := m.worldState.GetCurrentBlock()
	var prevHash string
	var blockIndex int64 = 1
	var blockTimestamp int64 = time.Now().Unix()

	if currentBlock != nil {
		prevHash = currentBlock.Hash
		blockIndex = currentBlock.Header.Index + 1
		if blockTimestamp <= currentBlock.Header.Timestamp {
			blockTimestamp = currentBlock.Header.Timestamp + 1
		}
	}

	// Create block
	block := &core.Block{
		Header: &core.BlockHeader{
			Index:     blockIndex,
			PrevHash:  prevHash,
			Timestamp: blockTimestamp,
			Validator: validator.Address,
			GasLimit:  1000000,
			GasUsed:   int64(len(pendingTxs) * 21000),
			StateRoot: "",
		},
		Transactions: pendingTxs,
		Hash:         fmt.Sprintf("block_%d_%d", blockIndex, time.Now().UnixNano()),
	}

	// Add block to blockchain
	startTime := time.Now()
	err := m.worldState.AddBlock(block)
	processingTime := time.Since(startTime)

	if err != nil {
		return fmt.Errorf("failed to add block: %v", err)
	}

	fmt.Printf("⛏️  Block %d produced by %s with %d transactions (processed in %v)\n",
		blockIndex, validator.Address[:12], len(pendingTxs), processingTime)

	return nil
}

// analyzeResults analyzes and reports the finality test results
func (m *FinalityTestManager) analyzeResults(t *testing.T, results []*FinalityResult, testConfig map[string]interface{}) {
	maxFinalityTime := testConfig["max_finality_time"].(time.Duration)
	requiredConfirms := testConfig["required_confirmations"].(int)

	t.Logf("\n📊 FINALITY TEST RESULTS")
	t.Logf("========================")

	// Individual transaction results
	var totalFinalityTime time.Duration
	var totalInclusionTime time.Duration
	successCount := 0

	for i, result := range results {
		success := result.FinalityTime.After(result.SubmissionTime) &&
			result.TotalFinalityTime <= maxFinalityTime &&
			result.ConfirmationBlocks >= requiredConfirms

		if success {
			successCount++
			totalFinalityTime += result.TotalFinalityTime
			totalInclusionTime += result.InclusionDelay
		}

		status := "✅ PASS"
		if !success {
			status = "❌ FAIL"
		}

		t.Logf("Transaction %d: %s", i+1, status)
		t.Logf("  ID: %s", result.TransactionID[:16])
		t.Logf("  Inclusion: %v (block %d, pos %d)", result.InclusionDelay, result.IncludedInBlock, result.TransactionPosition)
		t.Logf("  Finality: %v (%d confirmations)", result.TotalFinalityTime, result.ConfirmationBlocks)
		t.Logf("  Finalized at block: %d", result.FinalizedAtBlock)

		// Assertions for each transaction
		assert.True(t, result.TotalFinalityTime <= maxFinalityTime,
			"Transaction %d exceeded max finality time: %v > %v", i+1, result.TotalFinalityTime, maxFinalityTime)

		assert.GreaterOrEqual(t, result.ConfirmationBlocks, requiredConfirms,
			"Transaction %d didn't achieve required confirmations: %d < %d", i+1, result.ConfirmationBlocks, requiredConfirms)
	}

	// Summary statistics
	t.Logf("\n📈 SUMMARY STATISTICS")
	t.Logf("====================")
	t.Logf("Successful transactions: %d/%d (%.1f%%)", successCount, len(results), float64(successCount)/float64(len(results))*100)

	if successCount > 0 {
		avgFinalityTime := totalFinalityTime / time.Duration(successCount)
		avgInclusionTime := totalInclusionTime / time.Duration(successCount)

		t.Logf("Average inclusion time: %v", avgInclusionTime)
		t.Logf("Average finality time: %v", avgFinalityTime)
		t.Logf("Required confirmations: %d blocks", requiredConfirms)
		t.Logf("Block interval: %v", m.blockInterval)

		// Performance assertions
		assert.True(t, avgFinalityTime <= maxFinalityTime,
			"Average finality time exceeded maximum: %v > %v", avgFinalityTime, maxFinalityTime)

		assert.GreaterOrEqual(t, float64(successCount)/float64(len(results)), 0.8,
			"Success rate too low: %.1f%% < 80%%", float64(successCount)/float64(len(results))*100)

		// Expected finality time should be roughly: (required_confirmations * block_interval)
		expectedMinFinality := time.Duration(requiredConfirms) * m.blockInterval
		t.Logf("Expected minimum finality time: %v", expectedMinFinality)
		t.Logf("Actual vs Expected ratio: %.2fx", float64(avgFinalityTime)/float64(expectedMinFinality))
	}

	t.Logf("\n🎯 FINALITY TEST COMPLETED SUCCESSFULLY!")
}

// cleanup cleans up test resources
func (m *FinalityTestManager) cleanup() {
	if m.worldState != nil {
		m.worldState.Close()
	}
}

// Benchmark test for finality performance
func BenchmarkFinalityPerformance(b *testing.B) {
	testConfig := map[string]interface{}{
		"transaction_count":      b.N,
		"required_confirmations": 3,
		"block_interval":         500 * time.Millisecond,
		"max_finality_time":      30 * time.Second,
		"validator_count":        4,
	}

	manager, err := setupFinalityTestEnvironment(&testing.T{}, testConfig)
	if err != nil {
		b.Fatal(err)
	}
	defer manager.cleanup()

	b.ResetTimer()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	results, err := manager.runFinalityTest(ctx, testConfig)
	if err != nil {
		b.Fatal(err)
	}

	b.StopTimer()

	// Report metrics
	if len(results) > 0 {
		var totalTime time.Duration
		successCount := 0

		for _, result := range results {
			if result.FinalityTime.After(result.SubmissionTime) {
				totalTime += result.TotalFinalityTime
				successCount++
			}
		}

		if successCount > 0 {
			avgFinalityTime := totalTime / time.Duration(successCount)
			b.ReportMetric(float64(avgFinalityTime.Milliseconds()), "avg_finality_ms")
			b.ReportMetric(float64(successCount)/float64(len(results))*100, "success_rate_pct")
			throughput := float64(successCount) / (float64(totalTime) / float64(time.Second))
			b.ReportMetric(throughput, "tx_per_second")
		}
	}
}
