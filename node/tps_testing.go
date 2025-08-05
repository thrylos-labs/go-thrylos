package node

// tps_testing.go - TPS Performance Testing for THRYLOS Blockchain

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/crypto/hash"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// TPSTestResult contains the results of a TPS test
type TPSTestResult struct {
	TestName          string        `json:"test_name"`
	Duration          time.Duration `json:"duration"`
	TotalTransactions int64         `json:"total_transactions"`
	SuccessfulTxs     int64         `json:"successful_txs"`
	FailedTxs         int64         `json:"failed_txs"`
	AverageTPS        float64       `json:"average_tps"`
	PeakTPS           float64       `json:"peak_tps"`
	MinTPS            float64       `json:"min_tps"`
	AverageLatency    time.Duration `json:"average_latency"`
	MaxLatency        time.Duration `json:"max_latency"`
	MinLatency        time.Duration `json:"min_latency"`
	BlocksProduced    int64         `json:"blocks_produced"`
	AverageTxPerBlock float64       `json:"average_tx_per_block"`
	ThroughputMBps    float64       `json:"throughput_mbps"`
	ErrorRate         float64       `json:"error_rate"`
	StartTime         time.Time     `json:"start_time"`
	EndTime           time.Time     `json:"end_time"`
}

// TPSTransactionTester provides TPS performance testing utilities
type TPSTransactionTester struct {
	node        *Node
	privateKey  crypto.PrivateKey
	address     string
	testResults []TPSTestResult
	mu          sync.RWMutex
}

// NewTPSTransactionTester creates a new TPS testing utility
func NewTPSTransactionTester(node *Node, privateKey crypto.PrivateKey, address string) *TPSTransactionTester {
	return &TPSTransactionTester{
		node:        node,
		privateKey:  privateKey,
		address:     address,
		testResults: make([]TPSTestResult, 0),
	}
}

// TPSTestConfig contains configuration for TPS tests
type TPSTestConfig struct {
	Duration           time.Duration `json:"duration"`
	TargetTPS          int           `json:"target_tps"`
	MaxConcurrency     int           `json:"max_concurrency"`
	TransactionAmount  int64         `json:"transaction_amount"`
	GasPrice           int64         `json:"gas_price"`
	WarmupDuration     time.Duration `json:"warmup_duration"`
	ReportInterval     time.Duration `json:"report_interval"`
	TestName           string        `json:"test_name"`
	GenerateRecipients bool          `json:"generate_recipients"`
	NumRecipients      int           `json:"num_recipients"`
}

// RunTPSTest performs a comprehensive TPS test
func (tps *TPSTransactionTester) RunTPSTest(config TPSTestConfig) (*TPSTestResult, error) {
	fmt.Printf("🚀 Starting TPS Test: %s\n", config.TestName)
	fmt.Printf("   Duration: %v\n", config.Duration)
	fmt.Printf("   Target TPS: %d\n", config.TargetTPS)
	fmt.Printf("   Max Concurrency: %d\n", config.MaxConcurrency)
	fmt.Printf("   Transaction Amount: %d tokens\n", config.TransactionAmount)

	// Check initial balance
	balance, err := tps.GetBalance(tps.address)
	if err != nil {
		return nil, fmt.Errorf("failed to get balance: %v", err)
	}

	estimatedCost := config.TransactionAmount * int64(config.TargetTPS) * int64(config.Duration.Seconds())
	if balance < estimatedCost {
		return nil, fmt.Errorf("insufficient balance: have %d, need ~%d", balance, estimatedCost)
	}

	fmt.Printf("   Initial Balance: %d tokens\n", balance)
	fmt.Printf("   Estimated Cost: %d tokens\n", estimatedCost)

	// Generate recipient addresses if needed
	recipients, err := tps.generateRecipients(config.NumRecipients)
	if err != nil {
		return nil, fmt.Errorf("failed to generate recipients: %v", err)
	}

	// Initialize metrics
	var (
		totalTxs      int64
		successfulTxs int64
		failedTxs     int64
		latencies     []time.Duration
		latencyMu     sync.Mutex
		tpsReadings   []float64
		tpsReadingsMu sync.Mutex
	)

	startTime := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), config.Duration)
	defer cancel()

	// Warmup phase
	if config.WarmupDuration > 0 {
		fmt.Printf("🔥 Warmup phase: %v\n", config.WarmupDuration)
		warmupCtx, warmupCancel := context.WithTimeout(context.Background(), config.WarmupDuration)
		tps.runWarmup(warmupCtx, recipients, config)
		warmupCancel()
		fmt.Printf("✅ Warmup completed\n")
	}

	// Start initial blockchain height tracking
	initialHeight := tps.getCurrentHeight()

	// TPS measurement goroutine
	tpsTicker := time.NewTicker(time.Second)
	defer tpsTicker.Stop()

	var lastTxCount int64
	go func() {
		for {
			select {
			case <-tpsTicker.C:
				currentTxs := atomic.LoadInt64(&totalTxs)
				currentTPS := float64(currentTxs - lastTxCount)
				lastTxCount = currentTxs

				tpsReadingsMu.Lock()
				tpsReadings = append(tpsReadings, currentTPS)
				tpsReadingsMu.Unlock()

			case <-ctx.Done():
				return
			}
		}
	}()

	// Progress reporting goroutine
	reportTicker := time.NewTicker(config.ReportInterval)
	defer reportTicker.Stop()

	go func() {
		for {
			select {
			case <-reportTicker.C:
				elapsed := time.Since(startTime)
				currentTxs := atomic.LoadInt64(&totalTxs)
				currentTPS := float64(currentTxs) / elapsed.Seconds()

				fmt.Printf("📊 Progress: %v elapsed, %d txs, %.2f TPS\n",
					elapsed.Truncate(time.Second), currentTxs, currentTPS)

			case <-ctx.Done():
				return
			}
		}
	}()

	// Transaction generation with controlled rate
	interval := time.Second / time.Duration(config.TargetTPS)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Semaphore for concurrency control
	sem := make(chan struct{}, config.MaxConcurrency)

	fmt.Printf("🎯 Starting transaction generation (interval: %v)\n", interval)

mainLoop:
	for {
		select {
		case <-ticker.C:
			select {
			case sem <- struct{}{}:
				go func() {
					defer func() { <-sem }()

					txStart := time.Now()
					recipient := recipients[atomic.LoadInt64(&totalTxs)%int64(len(recipients))]

					tx, err := tps.CreateTestTransaction(tps.address, recipient, config.TransactionAmount, config.GasPrice)
					if err != nil {
						atomic.AddInt64(&failedTxs, 1)
						return
					}

					err = tps.node.SubmitTransaction(tx)
					txEnd := time.Now()

					atomic.AddInt64(&totalTxs, 1)

					if err != nil {
						atomic.AddInt64(&failedTxs, 1)
					} else {
						atomic.AddInt64(&successfulTxs, 1)
					}

					latency := txEnd.Sub(txStart)
					latencyMu.Lock()
					latencies = append(latencies, latency)
					latencyMu.Unlock()
				}()
			default:
				// Skip if we've hit concurrency limit
				atomic.AddInt64(&failedTxs, 1)
			}

		case <-ctx.Done():
			break mainLoop
		}
	}

	endTime := time.Now()
	finalHeight := tps.getCurrentHeight()

	// Wait for remaining transactions to complete
	fmt.Printf("⏳ Waiting for remaining transactions to complete...\n")
	for i := 0; i < cap(sem); i++ {
		sem <- struct{}{}
	}

	// Calculate results
	result := tps.calculateResults(TPSTestResult{
		TestName:          config.TestName,
		Duration:          endTime.Sub(startTime),
		TotalTransactions: atomic.LoadInt64(&totalTxs),
		SuccessfulTxs:     atomic.LoadInt64(&successfulTxs),
		FailedTxs:         atomic.LoadInt64(&failedTxs),
		BlocksProduced:    finalHeight - initialHeight,
		StartTime:         startTime,
		EndTime:           endTime,
	}, latencies, tpsReadings)

	// Store result
	tps.mu.Lock()
	tps.testResults = append(tps.testResults, *result)
	tps.mu.Unlock()

	tps.printResults(result)
	return result, nil
}

// RunStressTest performs a stress test with increasing load
func (tps *TPSTransactionTester) RunStressTest(maxTPS int, stepSize int, stepDuration time.Duration) ([]*TPSTestResult, error) {
	fmt.Printf("🔥 Starting Stress Test: 0 -> %d TPS\n", maxTPS)

	var results []*TPSTestResult

	for targetTPS := stepSize; targetTPS <= maxTPS; targetTPS += stepSize {
		config := DefaultTPSTestConfig()
		config.TargetTPS = targetTPS
		config.Duration = stepDuration
		config.TestName = fmt.Sprintf("Stress Test %d TPS", targetTPS)
		config.WarmupDuration = 5 * time.Second

		fmt.Printf("\n📈 Testing %d TPS...\n", targetTPS)

		result, err := tps.RunTPSTest(config)
		if err != nil {
			fmt.Printf("❌ Failed at %d TPS: %v\n", targetTPS, err)
			break
		}

		results = append(results, result)

		// Brief cooldown between stress levels
		time.Sleep(2 * time.Second)
	}

	tps.printStressTestSummary(results)
	return results, nil
}

// RunSustainedLoadTest runs a long-duration test at target TPS
func (tps *TPSTransactionTester) RunSustainedLoadTest(targetTPS int, duration time.Duration) (*TPSTestResult, error) {
	config := DefaultTPSTestConfig()
	config.TargetTPS = targetTPS
	config.Duration = duration
	config.TestName = fmt.Sprintf("Sustained Load Test %d TPS", targetTPS)
	config.WarmupDuration = 30 * time.Second
	config.ReportInterval = 10 * time.Second

	return tps.RunTPSTest(config)
}

// Helper methods

func (tps *TPSTransactionTester) runWarmup(ctx context.Context, recipients []string, config TPSTestConfig) {
	ticker := time.NewTicker(time.Second / time.Duration(config.TargetTPS/4)) // 1/4 target rate for warmup
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			recipient := recipients[time.Now().UnixNano()%int64(len(recipients))]
			tx, err := tps.CreateTestTransaction(tps.address, recipient, config.TransactionAmount/10, config.GasPrice)
			if err == nil {
				tps.node.SubmitTransaction(tx)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (tps *TPSTransactionTester) getCurrentHeight() int64 {
	status := tps.node.GetNodeStatus()
	if blockchain, ok := status["blockchain"].(map[string]interface{}); ok {
		if height, ok := blockchain["height"].(int64); ok {
			return height
		}
	}
	return 0
}

func (tps *TPSTransactionTester) calculateResults(base TPSTestResult, latencies []time.Duration, tpsReadings []float64) *TPSTestResult {
	result := base

	// Calculate TPS metrics
	if result.Duration.Seconds() > 0 {
		result.AverageTPS = float64(result.TotalTransactions) / result.Duration.Seconds()
	}

	if len(tpsReadings) > 0 {
		result.PeakTPS = tpsReadings[0]
		result.MinTPS = tpsReadings[0]

		for _, tps := range tpsReadings {
			if tps > result.PeakTPS {
				result.PeakTPS = tps
			}
			if tps < result.MinTPS {
				result.MinTPS = tps
			}
		}
	}

	// Calculate latency metrics
	if len(latencies) > 0 {
		var totalLatency time.Duration
		result.MaxLatency = latencies[0]
		result.MinLatency = latencies[0]

		for _, latency := range latencies {
			totalLatency += latency
			if latency > result.MaxLatency {
				result.MaxLatency = latency
			}
			if latency < result.MinLatency {
				result.MinLatency = latency
			}
		}

		result.AverageLatency = totalLatency / time.Duration(len(latencies))
	}

	// Calculate other metrics
	if result.BlocksProduced > 0 {
		result.AverageTxPerBlock = float64(result.SuccessfulTxs) / float64(result.BlocksProduced)
	}

	if result.TotalTransactions > 0 {
		result.ErrorRate = float64(result.FailedTxs) / float64(result.TotalTransactions) * 100
	}

	// Estimate throughput in MB/s (assuming ~200 bytes per transaction)
	avgTxSize := 200.0 // bytes
	result.ThroughputMBps = result.AverageTPS * avgTxSize / (1024 * 1024)

	return &result
}

func (tps *TPSTransactionTester) printResults(result *TPSTestResult) {
	fmt.Printf("\n📊 === TPS TEST RESULTS: %s ===\n", result.TestName)
	fmt.Printf("Duration: %v\n", result.Duration.Truncate(time.Millisecond))
	fmt.Printf("Total Transactions: %d\n", result.TotalTransactions)
	fmt.Printf("Successful: %d (%.1f%%)\n", result.SuccessfulTxs,
		float64(result.SuccessfulTxs)/float64(result.TotalTransactions)*100)
	fmt.Printf("Failed: %d (%.1f%%)\n", result.FailedTxs, result.ErrorRate)
	fmt.Printf("\n🎯 TPS METRICS:\n")
	fmt.Printf("Average TPS: %.2f\n", result.AverageTPS)
	fmt.Printf("Peak TPS: %.2f\n", result.PeakTPS)
	fmt.Printf("Min TPS: %.2f\n", result.MinTPS)
	fmt.Printf("\n⏱️  LATENCY METRICS:\n")
	fmt.Printf("Average Latency: %v\n", result.AverageLatency.Truncate(time.Microsecond))
	fmt.Printf("Max Latency: %v\n", result.MaxLatency.Truncate(time.Microsecond))
	fmt.Printf("Min Latency: %v\n", result.MinLatency.Truncate(time.Microsecond))
	fmt.Printf("\n🧱 BLOCKCHAIN METRICS:\n")
	fmt.Printf("Blocks Produced: %d\n", result.BlocksProduced)
	fmt.Printf("Avg Tx per Block: %.2f\n", result.AverageTxPerBlock)
	fmt.Printf("Throughput: %.3f MB/s\n", result.ThroughputMBps)
	fmt.Printf("========================================\n\n")
}

func (tps *TPSTransactionTester) printStressTestSummary(results []*TPSTestResult) {
	fmt.Printf("\n🔥 === STRESS TEST SUMMARY ===\n")
	fmt.Printf("Tested %d different TPS levels\n", len(results))

	maxTPS := 0.0
	maxSuccessRate := 0.0

	for _, result := range results {
		successRate := float64(result.SuccessfulTxs) / float64(result.TotalTransactions) * 100

		fmt.Printf("TPS %3.0f: Success %.1f%%, Latency %v\n",
			result.AverageTPS, successRate, result.AverageLatency.Truncate(time.Millisecond))

		if result.AverageTPS > maxTPS && successRate > 95 {
			maxTPS = result.AverageTPS
			maxSuccessRate = successRate
		}
	}

	fmt.Printf("\n🏆 Peak Performance: %.2f TPS with %.1f%% success rate\n", maxTPS, maxSuccessRate)
	fmt.Printf("==============================\n\n")
}

// Transaction creation (matching existing implementation)
// Fix for tps_testing.go - Replace the CreateTestTransaction method with this corrected version:

func (tps *TPSTransactionTester) CreateTestTransaction(from, to string, amount int64, gasPrice int64) (*core.Transaction, error) {
	nonce, err := tps.node.blockchain.GetNonce(from)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %v", err)
	}

	timestamp := time.Now().UnixNano()
	txID := fmt.Sprintf("tps_tx_%d_%d", timestamp, nonce)

	tx := &core.Transaction{
		Id:        txID,
		From:      from,
		To:        to,
		Amount:    amount,
		Gas:       21000,
		GasPrice:  gasPrice,
		Nonce:     nonce,
		Type:      core.TransactionType_TRANSFER, // ⚠️  THIS WAS MISSING!
		Data:      nil,                           // ⚠️  THIS WAS MISSING!
		Timestamp: time.Now().Unix(),
	}

	tx.Hash = tps.calculateTransactionHashExact(tx)

	signature, err := tps.signTransaction(tx)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %v", err)
	}
	tx.Signature = signature

	return tx, nil
}

// Also ensure the hash calculation method is identical to the working version:
func (tps *TPSTransactionTester) calculateTransactionHashExact(tx *core.Transaction) string {
	var buf bytes.Buffer

	buf.WriteString(tx.Id)
	buf.WriteString(tx.From)
	buf.WriteString(tx.To)

	amountBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(amountBytes, uint64(tx.Amount))
	buf.Write(amountBytes)

	gasBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasBytes, uint64(tx.Gas))
	buf.Write(gasBytes)

	gasPriceBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasPriceBytes, uint64(tx.GasPrice))
	buf.Write(gasPriceBytes)

	nonceBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(nonceBytes, tx.Nonce)
	buf.Write(nonceBytes)

	// ⚠️  CRITICAL: Include Type and Data in hash calculation
	typeBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(typeBytes, uint32(tx.Type))
	buf.Write(typeBytes)

	timestampBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(timestampBytes, uint64(tx.Timestamp))
	buf.Write(timestampBytes)

	// ⚠️  CRITICAL: Include Data field even if nil
	buf.Write(tx.Data)

	hashBytes := hash.HashData(buf.Bytes())
	return fmt.Sprintf("%x", hashBytes)
}

// Add this method to your TPS tester to generate valid recipient addresses:

// Fix 1: Update DebugTransactionCreation to use valid amount
func (tps *TPSTransactionTester) DebugTransactionCreation() {
	fmt.Println("🔍 === TPS TRANSACTION CREATION DEBUG ===")

	// Generate a VALID recipient address
	recipientKey, err := crypto.NewPrivateKey()
	if err != nil {
		fmt.Printf("❌ Failed to generate recipient key: %v\n", err)
		return
	}

	recipientAddress, err := account.GenerateAddress(recipientKey.PublicKey())
	if err != nil {
		fmt.Printf("❌ Failed to generate recipient address: %v\n", err)
		return
	}

	fmt.Printf("✅ Generated valid recipient address: %s\n", recipientAddress)

	// Create a test transaction with VALID amount (10,000,000+ tokens = 0.01+ THRYLOS)
	validAmount := int64(15000000) // 0.015 THRYLOS - above minimum
	tx, err := tps.CreateTestTransaction(tps.address, recipientAddress, validAmount, 1000)
	if err != nil {
		fmt.Printf("❌ Failed to create transaction: %v\n", err)
		return
	}

	fmt.Printf("Created transaction:\n")
	fmt.Printf("  ID: %s\n", tx.Id)
	fmt.Printf("  From: %s\n", tx.From)
	fmt.Printf("  To: %s\n", tx.To)
	fmt.Printf("  Amount: %d (%.3f THRYLOS)\n", tx.Amount, float64(tx.Amount)/1000000000)
	fmt.Printf("  Gas: %d\n", tx.Gas)
	fmt.Printf("  GasPrice: %d\n", tx.GasPrice)
	fmt.Printf("  Nonce: %d\n", tx.Nonce)
	fmt.Printf("  Type: %v\n", tx.Type)
	fmt.Printf("  Data: %v\n", tx.Data)
	fmt.Printf("  Hash: %s\n", tx.Hash)
	fmt.Printf("  Signature length: %d\n", len(tx.Signature))

	// Test validation
	fmt.Printf("\nTesting validation...\n")
	err = tps.node.blockchain.ValidateTransaction(tx)
	if err != nil {
		fmt.Printf("❌ Validation failed: %v\n", err)
		return
	} else {
		fmt.Printf("✅ Validation passed!\n")
	}

	// Test submission
	fmt.Printf("\nTesting submission...\n")
	err = tps.node.SubmitTransaction(tx)
	if err != nil {
		fmt.Printf("❌ Submission failed: %v\n", err)
		return
	} else {
		fmt.Printf("✅ Submission passed!\n")
	}

	// Check pool status after a short delay
	time.Sleep(100 * time.Millisecond)
	pendingTxs := tps.node.blockchain.GetPendingTransactions()
	fmt.Printf("📊 Pool now has %d pending transactions\n", len(pendingTxs))

	if len(pendingTxs) > 0 {
		fmt.Printf("🚀 SUCCESS: Transaction is in the pool!\n")

		// Check if it's executable
		executableTxs := tps.node.blockchain.GetExecutableTransactions(10)
		fmt.Printf("📊 Executable transactions: %d\n", len(executableTxs))

		if len(executableTxs) > 0 {
			fmt.Printf("🎉 EXCELLENT: Transaction is executable and ready for blocks!\n")
		} else {
			fmt.Printf("⚠️  Transaction is pending but not executable yet\n")
		}
	} else {
		fmt.Printf("❌ Transaction not found in pool\n")
	}

	fmt.Println("=========================================")
}

// Fix 2: Update DefaultTPSTestConfig to use valid amounts
func DefaultTPSTestConfig() TPSTestConfig {
	return TPSTestConfig{
		Duration:           60 * time.Second,
		TargetTPS:          10,
		MaxConcurrency:     100,
		TransactionAmount:  15000000, // 0.015 THRYLOS (above 0.01 minimum)
		GasPrice:           1000,
		WarmupDuration:     10 * time.Second,
		ReportInterval:     5 * time.Second,
		TestName:           "Standard TPS Test",
		GenerateRecipients: true,
		NumRecipients:      10,
	}
}

// Also fix the generateRecipients method in your TPS tester:
func (tps *TPSTransactionTester) generateRecipients(count int) ([]string, error) {
	recipients := make([]string, count)

	for i := 0; i < count; i++ {
		// Generate proper private key and address
		key, err := crypto.NewPrivateKey()
		if err != nil {
			return nil, fmt.Errorf("failed to generate key %d: %v", i, err)
		}

		addr, err := account.GenerateAddress(key.PublicKey())
		if err != nil {
			return nil, fmt.Errorf("failed to generate address %d: %v", i, err)
		}

		recipients[i] = addr
	}

	return recipients, nil
}

// Enhanced TPS test with better error handling and debugging:
func (tps *TPSTransactionTester) RunTPSTestWithDebugging(config TPSTestConfig) (*TPSTestResult, error) {
	fmt.Printf("🚀 Starting Enhanced TPS Test: %s\n", config.TestName)

	// Pre-flight checks
	fmt.Printf("🔍 Pre-flight checks...\n")

	// Check balance
	balance, err := tps.GetBalance(tps.address)
	if err != nil {
		return nil, fmt.Errorf("failed to get balance: %v", err)
	}
	fmt.Printf("✅ Balance: %d tokens\n", balance)

	// Test single transaction creation
	testTx, err := tps.CreateTestTransaction(tps.address, "tl1test123456789", 1000000, 1000)
	if err != nil {
		return nil, fmt.Errorf("failed to create test transaction: %v", err)
	}
	fmt.Printf("✅ Test transaction created: %s\n", testTx.Id)

	// Test validation
	err = tps.node.blockchain.ValidateTransaction(testTx)
	if err != nil {
		return nil, fmt.Errorf("test transaction validation failed: %v", err)
	}
	fmt.Printf("✅ Test transaction validation passed\n")

	// Check initial pool status
	initialPending := len(tps.node.blockchain.GetPendingTransactions())
	fmt.Printf("📊 Initial pending transactions: %d\n", initialPending)

	// Now run the actual TPS test...
	return tps.RunTPSTest(config)
}

// Add this method to monitor transaction flow during TPS testing:
func (tps *TPSTransactionTester) monitorTransactionFlow(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pendingTxs := tps.node.blockchain.GetPendingTransactions()
			executableTxs := tps.node.blockchain.GetExecutableTransactions(100)

			fmt.Printf("🔍 TX Flow: %d pending, %d executable\n",
				len(pendingTxs), len(executableTxs))

			if len(pendingTxs) > 0 {
				// Sample first transaction for debugging
				tx := pendingTxs[0]
				fmt.Printf("📋 Sample TX: ID=%s, From=%s, Nonce=%d, Type=%v\n",
					tx.Id, tx.From[:10]+"...", tx.Nonce, tx.Type)
			}
		}
	}
}

func (tps *TPSTransactionTester) signTransaction(tx *core.Transaction) ([]byte, error) {
	hashToSign := hash.HashData([]byte(tx.Hash))
	signature := tps.privateKey.Sign(hashToSign)
	if signature == nil {
		return nil, fmt.Errorf("failed to sign transaction: signature is nil")
	}
	return signature.Bytes(), nil
}

func (tps *TPSTransactionTester) GetBalance(address string) (int64, error) {
	return tps.node.GetBalance(address)
}

// GetTestResults returns all test results
func (tps *TPSTransactionTester) GetTestResults() []TPSTestResult {
	tps.mu.RLock()
	defer tps.mu.RUnlock()

	results := make([]TPSTestResult, len(tps.testResults))
	copy(results, tps.testResults)
	return results
}

// ClearTestResults clears all stored test results
func (tps *TPSTransactionTester) ClearTestResults() {
	tps.mu.Lock()
	defer tps.mu.Unlock()
	tps.testResults = tps.testResults[:0]
}
