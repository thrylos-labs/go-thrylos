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

	// Add these fields for nonce tracking
	currentNonce uint64
	nonceMu      sync.Mutex
}

func (tps *TPSTransactionTester) CreateTransactionBurst(burstSize int, recipients []string) ([]*core.Transaction, error) {
	var transactions []*core.Transaction

	// Get the starting nonce once
	startingNonce, err := tps.node.blockchain.GetNonce(tps.address)
	if err != nil {
		return nil, fmt.Errorf("failed to get starting nonce: %v", err)
	}

	fmt.Printf("🔍 Creating burst with starting nonce: %d\n", startingNonce)

	for i := 0; i < burstSize; i++ {
		recipient := recipients[i%len(recipients)]

		// Use sequential nonces starting from the current nonce
		txNonce := startingNonce + uint64(i)

		tx, err := tps.CreateTestTransactionWithNonce(tps.address, recipient, 15000000, 1000, txNonce)
		if err != nil {
			return nil, fmt.Errorf("failed to create transaction %d: %v", i, err)
		}

		transactions = append(transactions, tx)

		// Minimal delay to ensure unique timestamps
		time.Sleep(time.Millisecond * 2)
	}

	fmt.Printf("🔍 Created %d transactions with nonces %d-%d\n", len(transactions), startingNonce, startingNonce+uint64(burstSize)-1)
	return transactions, nil
}

// Alternative: Even simpler approach - don't track nonces, just get fresh ones
func (tps *TPSTransactionTester) getNextNonceSimple() (uint64, error) {
	// Just get the current nonce from blockchain each time
	// This is slower but guaranteed to be correct
	return tps.node.blockchain.GetNonce(tps.address)
}

// Updated CreateTestTransactionWithNonce to use fresh nonce if needed
func (tps *TPSTransactionTester) CreateTestTransactionWithNonce(from, to string, amount int64, gasPrice int64, nonce uint64) (*core.Transaction, error) {
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
		Type:      core.TransactionType_TRANSFER,
		Data:      nil,
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

func (tps *TPSTransactionTester) RunCustomSlowTPSTestWithMetrics(targetTPS float64, duration time.Duration) (*TPSTestResult, error) {
	fmt.Printf("🚀 Starting Enhanced TPS Test (Target: %.2f TPS)\n", targetTPS)

	recipients, err := tps.generateRecipients(20)
	if err != nil {
		return nil, fmt.Errorf("failed to generate recipients: %v", err)
	}

	var totalTxs int64
	var successfulTxs int64
	var consecutiveFailures int64
	var maxConsecutiveFailures int64

	startTime := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	// Get starting nonce ONCE at the beginning
	startingNonce, err := tps.node.blockchain.GetNonce(tps.address)
	if err != nil {
		return nil, fmt.Errorf("failed to get starting nonce: %v", err)
	}

	fmt.Printf("🔍 Starting with nonce: %d\n", startingNonce)

	// Calculate interval for fractional TPS
	var interval time.Duration
	if targetTPS >= 1.0 {
		interval = time.Duration(float64(time.Second) / targetTPS)
	} else {
		secondsPerTx := 1.0 / targetTPS
		interval = time.Duration(secondsPerTx * float64(time.Second))
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	fmt.Printf("🎯 Creating transactions every %v (%.2f TPS target)\n", interval, targetTPS)

	lastReportTime := startTime
	reportInterval := 10 * time.Second
	if targetTPS >= 2.0 {
		reportInterval = 5 * time.Second // More frequent reporting for high TPS
	}

	for {
		select {
		case <-ticker.C:
			// Use sequential nonce
			currentNonce := startingNonce + uint64(totalTxs)

			// Create transaction
			recipient := recipients[totalTxs%int64(len(recipients))]
			tx, err := tps.CreateTestTransactionWithNonce(tps.address, recipient, 15000000, 1000, currentNonce)
			if err != nil {
				fmt.Printf("❌ Failed to create transaction: %v\n", err)
				consecutiveFailures++
				totalTxs++
				continue
			}

			// Submit transaction
			if err := tps.node.SubmitTransaction(tx); err == nil {
				successfulTxs++
				consecutiveFailures = 0 // Reset consecutive failures
				// Only log success for very high TPS or first few
				if targetTPS < 1.0 || successfulTxs <= 3 {
					fmt.Printf("✅ TX success (nonce %d)\n", currentNonce)
				}
			} else {
				consecutiveFailures++
				if consecutiveFailures > maxConsecutiveFailures {
					maxConsecutiveFailures = consecutiveFailures
				}
				// Log failures more selectively based on TPS
				if targetTPS < 1.0 || consecutiveFailures <= 3 {
					fmt.Printf("🔍 TX failed (nonce %d): %v\n", currentNonce, err)
				}
			}
			totalTxs++

			// Progress reporting
			now := time.Now()
			if now.Sub(lastReportTime) >= reportInterval {
				elapsed := now.Sub(startTime)
				currentTPS := float64(successfulTxs) / elapsed.Seconds()
				successRate := float64(successfulTxs) / float64(totalTxs) * 100

				fmt.Printf("📊 Progress: %d/%d txs (%.1f%%), %.3f TPS, %d consecutive failures\n",
					successfulTxs, totalTxs, successRate, currentTPS, consecutiveFailures)

				lastReportTime = now
			}

		case <-ctx.Done():
			goto testComplete
		}
	}

testComplete:
	endTime := time.Now()
	actualDuration := endTime.Sub(startTime)

	result := &TPSTestResult{
		TestName:          fmt.Sprintf("Custom Slow TPS Test (Target: %.2f)", targetTPS),
		Duration:          actualDuration,
		TotalTransactions: totalTxs,
		SuccessfulTxs:     successfulTxs,
		FailedTxs:         totalTxs - successfulTxs,
		AverageTPS:        float64(successfulTxs) / actualDuration.Seconds(),
		ErrorRate:         float64(totalTxs-successfulTxs) / float64(totalTxs) * 100,
		StartTime:         startTime,
		EndTime:           endTime,
	}

	// Enhanced results display
	fmt.Printf("\n📊 === ENHANCED TPS RESULTS ===\n")
	fmt.Printf("Target: %.2f TPS | Achieved: %.3f TPS (%.1f%% efficiency)\n",
		targetTPS, result.AverageTPS, (result.AverageTPS/targetTPS)*100)
	fmt.Printf("Duration: %v\n", result.Duration.Truncate(time.Millisecond))
	fmt.Printf("Success: %d/%d (%.1f%%)\n", result.SuccessfulTxs, result.TotalTransactions,
		float64(result.SuccessfulTxs)/float64(result.TotalTransactions)*100)
	fmt.Printf("Max Consecutive Failures: %d\n", maxConsecutiveFailures)
	fmt.Printf("===========================\n")

	return result, nil
}

// Method 3: Alternative - Modify your existing RunQuickSimpleTests
func (tps *TPSTransactionTester) RunQuickSimpleTests() ([]*TPSTestResult, error) {
	fmt.Printf("🚀 === QUICK SIMPLE TPS TESTS ===\n")

	var results []*TPSTestResult

	// Test with very slow rates first to establish baseline
	slowTests := []struct {
		tps      float64
		duration time.Duration
		name     string
	}{
		{0.25, 20 * time.Second, "Ultra Slow (0.25 TPS)"}, // 1 tx every 4 seconds
		{0.33, 20 * time.Second, "Very Slow (0.33 TPS)"},  // 1 tx every 3 seconds
		{0.5, 20 * time.Second, "Slow (0.5 TPS)"},         // 1 tx every 2 seconds
		{1.0, 20 * time.Second, "Normal (1 TPS)"},         // 1 tx every 1 second
	}

	for i, test := range slowTests {
		fmt.Printf("\n%d️⃣ Testing %s...\n", i+1, test.name)

		result, err := tps.RunCustomSlowTPSTestWithMetrics(test.tps, test.duration)
		if err != nil {
			fmt.Printf("❌ Test failed: %v\n", err)
			continue
		}

		results = append(results, result)

		efficiency := (result.AverageTPS / test.tps) * 100
		successRate := float64(result.SuccessfulTxs) / float64(result.TotalTransactions) * 100

		fmt.Printf("✅ Result: %.3f TPS (%.1f%% efficiency, %.1f%% success)\n",
			result.AverageTPS, efficiency, successRate)

		// Brief pause between tests
		if i < len(slowTests)-1 {
			fmt.Printf("⏳ Pausing 3 seconds...\n")
			time.Sleep(3 * time.Second)
		}
	}

	return results, nil
}
func (tps *TPSTransactionTester) RunOptimalBurstTPSTest(config TPSTestConfig) (*TPSTestResult, error) {
	fmt.Printf("🚀 Starting Optimal Burst TPS Test: %s\n", config.TestName)

	recipients, err := tps.generateRecipients(config.NumRecipients)
	if err != nil {
		return nil, fmt.Errorf("failed to generate recipients: %v", err)
	}

	var totalTxs int64
	var successfulTxs int64

	startTime := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), config.Duration)
	defer cancel()

	// Smaller, more frequent bursts
	burstInterval := 1 * time.Second // Create burst every second
	burstSize := config.TargetTPS    // 1 second worth of transactions per burst

	burstTicker := time.NewTicker(burstInterval)
	defer burstTicker.Stop()

	fmt.Printf("🎯 Creating optimal bursts: %d transactions every %v\n", burstSize, burstInterval)

	for {
		select {
		case <-burstTicker.C:
			// Create a smaller burst of transactions
			transactions, err := tps.CreateTransactionBurst(burstSize, recipients)
			if err != nil {
				fmt.Printf("❌ Failed to create transaction burst: %v\n", err)
				continue
			}

			// Submit all transactions in the burst rapidly
			successCount := 0
			for _, tx := range transactions {
				if err := tps.node.SubmitTransaction(tx); err == nil {
					successCount++
				}
			}

			totalTxs += int64(len(transactions))
			successfulTxs += int64(successCount)

			elapsed := time.Since(startTime)
			currentTPS := float64(totalTxs) / elapsed.Seconds()

			fmt.Printf("📊 Burst: %d/%d successful (%.1f%%), %.2f TPS overall\n",
				successCount, len(transactions), float64(successCount)/float64(len(transactions))*100, currentTPS)

		case <-ctx.Done():
			goto testComplete
		}
	}

testComplete:
	endTime := time.Now()

	result := &TPSTestResult{
		TestName:          config.TestName,
		Duration:          endTime.Sub(startTime),
		TotalTransactions: totalTxs,
		SuccessfulTxs:     successfulTxs,
		FailedTxs:         totalTxs - successfulTxs,
		AverageTPS:        float64(totalTxs) / endTime.Sub(startTime).Seconds(),
		StartTime:         startTime,
		EndTime:           endTime,
	}

	tps.printResults(result)
	return result, nil
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

// Fixed RunSimpleSequentialTest method - replace your current version with this
func (tps *TPSTransactionTester) RunSimpleSequentialTest(targetTPS int, duration time.Duration) (*TPSTestResult, error) {
	fmt.Printf("🚀 Starting Simple Sequential TPS Test (Target: %d TPS)\n", targetTPS)

	recipients, err := tps.generateRecipients(20)
	if err != nil {
		return nil, fmt.Errorf("failed to generate recipients: %v", err)
	}

	var totalTxs int64
	var successfulTxs int64

	startTime := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	// ⚠️ CRITICAL FIX: Get starting nonce ONCE at the beginning
	startingNonce, err := tps.node.blockchain.GetNonce(tps.address)
	if err != nil {
		return nil, fmt.Errorf("failed to get starting nonce: %v", err)
	}

	fmt.Printf("🔍 Starting with nonce: %d\n", startingNonce)

	// Create transactions at steady rate
	interval := time.Second / time.Duration(targetTPS)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	fmt.Printf("🎯 Creating transactions every %v\n", interval)

	lastReportTime := startTime

	for {
		select {
		case <-ticker.C:
			// ⚠️ CRITICAL FIX: Use sequential nonce, don't query blockchain each time
			currentNonce := startingNonce + uint64(totalTxs)

			// Create single transaction with sequential nonce
			recipient := recipients[totalTxs%int64(len(recipients))]
			tx, err := tps.CreateTestTransactionWithNonce(tps.address, recipient, 15000000, 1000, currentNonce)
			if err != nil {
				fmt.Printf("❌ Failed to create transaction: %v\n", err)
				totalTxs++
				continue
			}

			// Submit transaction
			if err := tps.node.SubmitTransaction(tx); err == nil {
				successfulTxs++
				// Only log success for first few to avoid spam
				if successfulTxs <= 3 {
					fmt.Printf("✅ TX success (nonce %d): %s\n", currentNonce, tx.Id)
				}
			} else {
				// Only log first few failures
				if totalTxs < 5 {
					fmt.Printf("🔍 TX failed (nonce %d): %v\n", currentNonce, err)
				}
			}
			totalTxs++

			// Progress reporting every 5 seconds
			now := time.Now()
			if now.Sub(lastReportTime) >= 5*time.Second {
				elapsed := now.Sub(startTime)
				currentTPS := float64(successfulTxs) / elapsed.Seconds()
				successRate := float64(successfulTxs) / float64(totalTxs) * 100

				fmt.Printf("📊 Progress: %d/%d txs (%.1f%%), %.2f TPS\n",
					successfulTxs, totalTxs, successRate, currentTPS)

				lastReportTime = now
			}

		case <-ctx.Done():
			goto testComplete
		}
	}

testComplete:
	endTime := time.Now()
	actualDuration := endTime.Sub(startTime)

	result := &TPSTestResult{
		TestName:          fmt.Sprintf("Simple Sequential (Target: %d)", targetTPS),
		Duration:          actualDuration,
		TotalTransactions: totalTxs,
		SuccessfulTxs:     successfulTxs,
		FailedTxs:         totalTxs - successfulTxs,
		AverageTPS:        float64(successfulTxs) / actualDuration.Seconds(),
		ErrorRate:         float64(totalTxs-successfulTxs) / float64(totalTxs) * 100,
		StartTime:         startTime,
		EndTime:           endTime,
	}

	// Print results
	fmt.Printf("\n📊 === SIMPLE SEQUENTIAL RESULTS ===\n")
	fmt.Printf("Target TPS: %d\n", targetTPS)
	fmt.Printf("Duration: %v\n", result.Duration.Truncate(time.Millisecond))
	fmt.Printf("Total Transactions: %d\n", result.TotalTransactions)
	fmt.Printf("Successful: %d (%.1f%%)\n", result.SuccessfulTxs,
		float64(result.SuccessfulTxs)/float64(result.TotalTransactions)*100)
	fmt.Printf("Achieved TPS: %.2f\n", result.AverageTPS)
	fmt.Printf("Efficiency: %.1f%% of target\n", (result.AverageTPS/float64(targetTPS))*100)
	fmt.Printf("===================================\n")

	return result, nil
}

// Also update your main CreateTestTransaction method to fix the same issue:
func (tps *TPSTransactionTester) CreateTestTransaction(from, to string, amount int64, gasPrice int64) (*core.Transaction, error) {
	// ⚠️ PROBLEM: This calls GetNonce every time, causing conflicts
	// For TPS testing, use CreateTestTransactionWithNonce instead
	nonce, err := tps.node.blockchain.GetNonce(from)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %v", err)
	}

	return tps.CreateTestTransactionWithNonce(from, to, amount, gasPrice, nonce)
}

// Enhanced version that's better for TPS testing:
func (tps *TPSTransactionTester) CreateTestTransactionBatch(from string, recipients []string, amount int64, gasPrice int64, batchSize int) ([]*core.Transaction, error) {
	// Get starting nonce once
	startingNonce, err := tps.node.blockchain.GetNonce(from)
	if err != nil {
		return nil, fmt.Errorf("failed to get starting nonce: %v", err)
	}

	var transactions []*core.Transaction

	for i := 0; i < batchSize; i++ {
		recipient := recipients[i%len(recipients)]
		nonce := startingNonce + uint64(i)

		tx, err := tps.CreateTestTransactionWithNonce(from, recipient, amount, gasPrice, nonce)
		if err != nil {
			return nil, fmt.Errorf("failed to create transaction %d: %v", i, err)
		}

		transactions = append(transactions, tx)
	}

	return transactions, nil
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

// Add these methods to your TPSTransactionTester struct

// Thread-safe nonce management
func (tps *TPSTransactionTester) getNextNonce() (uint64, error) {
	tps.nonceMu.Lock()
	defer tps.nonceMu.Unlock()

	// Initialize nonce on first use
	if tps.currentNonce == 0 {
		nonce, err := tps.node.blockchain.GetNonce(tps.address)
		if err != nil {
			return 0, fmt.Errorf("failed to initialize nonce: %v", err)
		}
		tps.currentNonce = nonce
	}

	nextNonce := tps.currentNonce
	tps.currentNonce++ // Increment for next use
	return nextNonce, nil
}

// Reset nonce tracking for TPSTransactionTester
func (tps *TPSTransactionTester) resetNonceTracking() error {
	tps.nonceMu.Lock()
	defer tps.nonceMu.Unlock()

	nonce, err := tps.node.blockchain.GetNonce(tps.address)
	if err != nil {
		return err
	}
	tps.currentNonce = nonce
	return nil
}

// Updated Sequential TPS Test - Clean and Simple
func (tps *TPSTransactionTester) RunSequentialTPSTest(targetTPS int, duration time.Duration) (*TPSTestResult, error) {
	fmt.Printf("🚀 Starting Sequential TPS Test (Target: %d TPS)\n", targetTPS)

	// Reset nonce tracking
	if err := tps.resetNonceTracking(); err != nil {
		return nil, fmt.Errorf("failed to reset nonce tracking: %v", err)
	}

	recipients, err := tps.generateRecipients(20)
	if err != nil {
		return nil, fmt.Errorf("failed to generate recipients: %v", err)
	}

	var totalTxs int64
	var successfulTxs int64
	var failedTxs int64

	startTime := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	// Create transactions at steady rate
	interval := time.Second / time.Duration(targetTPS)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	fmt.Printf("🎯 Creating transactions every %v (target %d TPS)\n", interval, targetTPS)
	fmt.Printf("⏱️  Test duration: %v\n", duration)

	lastReportTime := startTime
	reportInterval := 5 * time.Second

	for {
		select {
		case <-ticker.C:
			// Get next nonce
			nonce, err := tps.getNextNonce()
			if err != nil {
				fmt.Printf("❌ Failed to get nonce: %v\n", err)
				failedTxs++
				totalTxs++
				continue
			}

			// Create single transaction
			recipient := recipients[totalTxs%int64(len(recipients))]
			tx, err := tps.CreateTestTransactionWithNonce(tps.address, recipient, 15000000, 1000, nonce)
			if err != nil {
				fmt.Printf("❌ Failed to create transaction: %v\n", err)
				failedTxs++
				totalTxs++
				continue
			}

			// Submit transaction
			if err := tps.node.SubmitTransaction(tx); err == nil {
				successfulTxs++
			} else {
				failedTxs++
				// Log first few failures for debugging
				if failedTxs <= 3 {
					fmt.Printf("🔍 Transaction failed: %v\n", err)
				}
			}
			totalTxs++

			// Progress reporting every 5 seconds
			now := time.Now()
			if now.Sub(lastReportTime) >= reportInterval {
				elapsed := now.Sub(startTime)
				currentTPS := float64(successfulTxs) / elapsed.Seconds()
				successRate := float64(successfulTxs) / float64(totalTxs) * 100

				fmt.Printf("📊 Progress: %d successful txs, %.2f TPS, %.1f%% success rate\n",
					successfulTxs, currentTPS, successRate)

				// Show pool status
				pendingTxs := tps.node.blockchain.GetPendingTransactions()
				fmt.Printf("📋 Pool: %d pending transactions\n", len(pendingTxs))

				lastReportTime = now
			}

		case <-ctx.Done():
			goto testComplete
		}
	}

testComplete:
	endTime := time.Now()
	actualDuration := endTime.Sub(startTime)

	result := &TPSTestResult{
		TestName:          fmt.Sprintf("Sequential TPS Test (Target: %d)", targetTPS),
		Duration:          actualDuration,
		TotalTransactions: totalTxs,
		SuccessfulTxs:     successfulTxs,
		FailedTxs:         failedTxs,
		AverageTPS:        float64(successfulTxs) / actualDuration.Seconds(),
		ErrorRate:         float64(failedTxs) / float64(totalTxs) * 100,
		StartTime:         startTime,
		EndTime:           endTime,
	}

	// Print detailed results
	fmt.Printf("\n📊 === SEQUENTIAL TPS RESULTS ===\n")
	fmt.Printf("Target TPS: %d\n", targetTPS)
	fmt.Printf("Duration: %v\n", result.Duration.Truncate(time.Millisecond))
	fmt.Printf("Total Transactions: %d\n", result.TotalTransactions)
	fmt.Printf("Successful: %d (%.1f%%)\n", result.SuccessfulTxs,
		float64(result.SuccessfulTxs)/float64(result.TotalTransactions)*100)
	fmt.Printf("Failed: %d (%.1f%%)\n", result.FailedTxs, result.ErrorRate)
	fmt.Printf("Achieved TPS: %.2f\n", result.AverageTPS)
	fmt.Printf("Efficiency: %.1f%% of target\n", (result.AverageTPS/float64(targetTPS))*100)
	fmt.Printf("===============================\n")

	return result, nil
}

// Comprehensive TPS Testing Suite with Sequential Tests
func (tps *TPSTransactionTester) RunComprehensiveSequentialTests() ([]*TPSTestResult, error) {
	fmt.Printf("🚀 === COMPREHENSIVE SEQUENTIAL TPS TESTING ===\n")

	var results []*TPSTestResult

	// Test different TPS targets to find the sweet spot
	testTargets := []int{0} // We'll manually set a custom rate
	testDuration := 30 * time.Second

	for i, targetTPS := range testTargets {
		fmt.Printf("\n%d️⃣ Testing %d TPS target...\n", i+1, targetTPS)

		result, err := tps.RunSequentialTPSTest(targetTPS, testDuration)
		if err != nil {
			fmt.Printf("❌ Test %d TPS failed: %v\n", targetTPS, err)
			continue
		}

		results = append(results, result)

		// Analyze result
		efficiency := (result.AverageTPS / float64(targetTPS)) * 100
		if efficiency > 95 {
			fmt.Printf("✅ Excellent: %.1f%% efficiency\n", efficiency)
		} else if efficiency > 80 {
			fmt.Printf("🟡 Good: %.1f%% efficiency\n", efficiency)
		} else {
			fmt.Printf("🔴 Poor: %.1f%% efficiency - may have hit capacity\n", efficiency)
		}

		// Brief pause between tests
		if i < len(testTargets)-1 {
			fmt.Printf("⏳ Cooling down for 3 seconds...\n")
			time.Sleep(3 * time.Second)
		}
	}

	// Print comprehensive summary
	tps.printSequentialTestSummary(results)

	return results, nil
}

// Print summary of sequential tests
func (tps *TPSTransactionTester) printSequentialTestSummary(results []*TPSTestResult) {
	if len(results) == 0 {
		return
	}

	fmt.Printf("\n🏆 === SEQUENTIAL TPS TESTING SUMMARY ===\n")
	fmt.Printf("Completed %d tests\n", len(results))

	var bestTPS float64
	var bestEfficiency float64
	var bestTarget int

	fmt.Printf("\n📈 Results by Target TPS:\n")
	for _, result := range results {
		// Extract target from test name
		var target int
		fmt.Sscanf(result.TestName, "Sequential TPS Test (Target: %d)", &target)

		efficiency := (result.AverageTPS / float64(target)) * 100

		fmt.Printf("Target %3d TPS: Achieved %6.2f TPS (%.1f%% efficiency, %.1f%% success)\n",
			target, result.AverageTPS, efficiency,
			float64(result.SuccessfulTxs)/float64(result.TotalTransactions)*100)

		if result.AverageTPS > bestTPS {
			bestTPS = result.AverageTPS
			bestTarget = target
		}

		if efficiency > bestEfficiency && efficiency <= 100 {
			bestEfficiency = efficiency
		}
	}

	fmt.Printf("\n🎯 Peak Performance:\n")
	fmt.Printf("Highest TPS: %.2f (target %d TPS)\n", bestTPS, bestTarget)
	fmt.Printf("Best Efficiency: %.1f%%\n", bestEfficiency)

	// Determine sustainable TPS (where efficiency > 95%)
	var sustainableTPS float64
	for _, result := range results {
		var target int
		fmt.Sscanf(result.TestName, "Sequential TPS Test (Target: %d)", &target)
		efficiency := (result.AverageTPS / float64(target)) * 100

		if efficiency > 95 {
			sustainableTPS = result.AverageTPS
		}
	}

	if sustainableTPS > 0 {
		fmt.Printf("Sustainable TPS: %.2f (>95%% efficiency)\n", sustainableTPS)
	}

	fmt.Printf("=====================================\n")
}

// Update your main.go test suite to use sequential testing:
func runOptimizedSequentialTestSuite(tester *TPSTransactionTester) {
	fmt.Printf("\n🧪 === OPTIMIZED SEQUENTIAL TPS TEST SUITE ===\n")

	// Test 1: Find maximum sustainable TPS
	fmt.Printf("\n1️⃣ Running comprehensive sequential tests...\n")
	results, err := tester.RunComprehensiveSequentialTests()
	if err != nil {
		fmt.Printf("❌ Comprehensive tests failed: %v\n", err)
		return
	}

	// Test 2: Extended test at optimal TPS
	if len(results) > 0 {
		// Find the best performing test
		var bestResult *TPSTestResult
		var bestEfficiency float64

		for _, result := range results {
			var target int
			fmt.Sscanf(result.TestName, "Sequential TPS Test (Target: %d)", &target)
			efficiency := (result.AverageTPS / float64(target)) * 100

			if efficiency > bestEfficiency && efficiency <= 100 {
				bestEfficiency = efficiency
				bestResult = result
			}
		}

		if bestResult != nil {
			var optimalTarget int
			fmt.Sscanf(bestResult.TestName, "Sequential TPS Test (Target: %d)", &optimalTarget)

			fmt.Printf("\n2️⃣ Running extended test at optimal %d TPS...\n", optimalTarget)
			extendedResult, err := tester.RunSequentialTPSTest(optimalTarget, 60*time.Second)
			if err != nil {
				fmt.Printf("❌ Extended test failed: %v\n", err)
			} else {
				fmt.Printf("🎉 Extended test: %.2f TPS sustained over 60 seconds\n", extendedResult.AverageTPS)
			}
		}
	}
}
