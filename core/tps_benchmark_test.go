package core

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/crypto/address"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
)

// TPSTestConfig defines parameters for TPS testing
type TPSTestConfig struct {
	TotalTransactions    int
	TransactionsPerBlock int
	Name                 string
}

// TPSResult captures benchmark results
type TPSResult struct {
	TotalTransactions int
	TotalBlocks       int
	Duration          time.Duration
	TPS               float64
	AvgTxPerBlock     float64
}

// TestSimpleTPS - Minimal working TPS test
func TestSimpleTPS(t *testing.T) {
	config := TPSTestConfig{
		TotalTransactions:    100,
		TransactionsPerBlock: 10,
		Name:                 "Simple_100tx",
	}

	result := runTPSTest(t, config)
	printTPSResult(t, config.Name, result)

	// Assertions
	require.Equal(t, config.TotalTransactions, result.TotalTransactions, "All transactions should succeed")
	require.Greater(t, result.TPS, 100.0, "TPS should be > 100")
}

// TestTPSBenchmarkSuite - Comprehensive TPS benchmark
// TestTPSBenchmarkSuite - Comprehensive TPS benchmark
func TestTPSBenchmarkSuite(t *testing.T) {
	tests := []TPSTestConfig{
		{
			TotalTransactions:    100,
			TransactionsPerBlock: 10,
			Name:                 "Small_Load_100tx",
		},
		{
			TotalTransactions:    500,
			TransactionsPerBlock: 25, // Reduced from 50
			Name:                 "Medium_Load_500tx",
		},
		{
			TotalTransactions:    1000,
			TransactionsPerBlock: 50, // Reduced from 100
			Name:                 "Large_Load_1000tx",
		},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			result := runTPSTest(t, tc)
			printTPSResult(t, tc.Name, result)

			// Assertions
			require.Equal(t, tc.TotalTransactions, result.TotalTransactions, "All transactions should succeed")
			require.Greater(t, result.TPS, 100.0, "TPS should be > 100")
		})
	}
}

// TestTPSStress - Stress test with high transaction volume
// TestTPSStress - Stress test with high transaction volume
func TestTPSStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	config := TPSTestConfig{
		TotalTransactions:    5000,
		TransactionsPerBlock: 50, // Reduced from 200 to stay under block size limit
		Name:                 "Stress_5000tx",
	}

	result := runTPSTest(t, config)
	printTPSResult(t, config.Name, result)

	// Stress test assertions
	require.Equal(t, config.TotalTransactions, result.TotalTransactions, "All transactions should succeed")
	require.Greater(t, result.TPS, 50.0, "Stress test TPS should be > 50")
}

// runTPSTest executes a TPS test with given configuration
func runTPSTest(t *testing.T, cfg TPSTestConfig) TPSResult {
	// Setup
	testConfig, err := config.Load()
	require.NoError(t, err)

	dataDir := fmt.Sprintf("/tmp/tps_test_%d", time.Now().UnixNano())
	badgerStorage, err := storage.NewBadgerStorage(dataDir)
	require.NoError(t, err)
	defer badgerStorage.Close()

	worldState, err := state.NewWorldState(dataDir, account.ShardID(0), 1, testConfig, badgerStorage)
	require.NoError(t, err)
	defer worldState.Close()

	genesisAddress := testConfig.Genesis.Accounts[0].Address

	// Create validator
	validator := &core.Validator{
		Address:        genesisAddress,
		Pubkey:         []byte("test_pubkey"),
		Stake:          100000 * config.BaseUnit,
		SelfStake:      100000 * config.BaseUnit,
		DelegatedStake: 0,
		Delegators:     make(map[string]int64),
		Commission:     0.05,
		Active:         true,
		CreatedAt:      time.Now().Unix(),
		UpdatedAt:      time.Now().Unix(),
	}
	err = worldState.AddValidator(validator)
	require.NoError(t, err)

	startTime := time.Now()
	successfulTxs := 0
	totalBlocks := 0

	numBlocks := cfg.TotalTransactions / cfg.TransactionsPerBlock

	// Create and process blocks
	for blockNum := 0; blockNum < numBlocks; blockNum++ {
		var blockTransactions []*core.Transaction

		// Create transactions for this block
		for i := 0; i < cfg.TransactionsPerBlock; i++ {
			privKey, err := crypto.NewPrivateKey()
			require.NoError(t, err)
			recipient, err := address.GenerateAddress(privKey.PublicKey().Bytes())
			require.NoError(t, err)

			txID := fmt.Sprintf("tx-%d-%d", blockNum, i)
			nonce := uint64(blockNum*cfg.TransactionsPerBlock + i)

			tx := &core.Transaction{
				Id:        txID,
				From:      genesisAddress,
				To:        recipient,
				Amount:    1000 * config.BaseUnit,
				Timestamp: time.Now().Unix(),
				Nonce:     nonce,
				Gas:       21000,
				GasPrice:  1000,
				Signature: []byte("test_signature"),
			}

			blockTransactions = append(blockTransactions, tx)
		}

		// Create and add block
		currentBlock := worldState.GetCurrentBlock()
		var prevHash string
		var blockIndex int64 = 1
		var blockTimestamp int64

		if currentBlock != nil {
			prevHash = currentBlock.Hash
			blockIndex = currentBlock.Header.Index + 1
			blockTimestamp = currentBlock.Header.Timestamp + 1
		} else {
			blockTimestamp = time.Now().Unix()
		}

		block := &core.Block{
			Header: &core.BlockHeader{
				Index:     blockIndex,
				PrevHash:  prevHash,
				Timestamp: blockTimestamp,
				Validator: validator.Address,
				GasLimit:  10000000,
				GasUsed:   int64(len(blockTransactions) * 21000),
				StateRoot: "",
			},
			Transactions: blockTransactions,
		}
		block.Hash = fmt.Sprintf("block_%d_%x", blockIndex, blockTimestamp)

		err = worldState.AddBlock(block)
		require.NoError(t, err)

		successfulTxs += len(blockTransactions)
		totalBlocks++
	}

	duration := time.Since(startTime)
	tps := float64(successfulTxs) / duration.Seconds()
	avgTxPerBlock := float64(successfulTxs) / float64(totalBlocks)

	return TPSResult{
		TotalTransactions: successfulTxs,
		TotalBlocks:       totalBlocks,
		Duration:          duration,
		TPS:               tps,
		AvgTxPerBlock:     avgTxPerBlock,
	}
}

// printTPSResult prints formatted test results
func printTPSResult(t *testing.T, testName string, result TPSResult) {
	separator := strings.Repeat("=", 60)
	t.Logf("\n%s", separator)
	t.Logf("📊 TPS TEST: %s", testName)
	t.Logf("%s", separator)
	t.Logf("✅ Transactions: %d successful", result.TotalTransactions)
	t.Logf("⛓️  Blocks: %d (avg %.1f tx/block)", result.TotalBlocks, result.AvgTxPerBlock)
	t.Logf("⏱️  Duration: %v", result.Duration)
	t.Logf("🚀 TPS: %.2f", result.TPS)
	t.Logf("%s", separator)

	// Performance rating
	rating := getPerformanceRating(result.TPS)
	t.Logf("🏆 Rating: %s", rating)
	t.Logf("%s\n", separator)
}

// getPerformanceRating returns performance rating based on TPS
func getPerformanceRating(tps float64) string {
	switch {
	case tps >= 10000:
		return "⭐⭐⭐⭐⭐ EXCEPTIONAL - Enterprise-grade!"
	case tps >= 5000:
		return "⭐⭐⭐⭐ EXCELLENT - High-performance!"
	case tps >= 1000:
		return "⭐⭐⭐ VERY GOOD - Strong performance!"
	case tps >= 500:
		return "⭐⭐ GOOD - Solid performance!"
	case tps >= 100:
		return "⭐ FAIR - Functional performance"
	default:
		return "⚠️ NEEDS IMPROVEMENT"
	}
}

// TestTPSWithVariableBlockSizes - Test different block sizes
func TestTPSWithVariableBlockSizes(t *testing.T) {
	blockSizes := []int{5, 10, 25, 50}
	totalTx := 1000

	var results []struct {
		BlockSize int
		Result    TPSResult
	}

	for _, blockSize := range blockSizes {
		config := TPSTestConfig{
			TotalTransactions:    totalTx,
			TransactionsPerBlock: blockSize,
			Name:                 fmt.Sprintf("BlockSize_%d", blockSize),
		}

		result := runTPSTest(t, config)
		results = append(results, struct {
			BlockSize int
			Result    TPSResult
		}{blockSize, result})
	}

	// Print comparison
	separator := strings.Repeat("=", 80)
	t.Logf("\n%s", separator)
	t.Logf("📊 BLOCK SIZE COMPARISON (%d transactions)", totalTx)
	t.Logf("%s", separator)
	t.Logf("%-15s | %-10s | %-15s | %-10s", "Block Size", "Blocks", "Duration", "TPS")
	t.Logf("%s", strings.Repeat("-", 80))

	for _, r := range results {
		t.Logf("%-15d | %-10d | %-15v | %-10.2f",
			r.BlockSize,
			r.Result.TotalBlocks,
			r.Result.Duration,
			r.Result.TPS)
	}
	t.Logf("%s\n", separator)
}

// TestTPSConsistency - Run same test multiple times to check consistency
// TestTPSConsistency - Run same test multiple times to check consistency
// TestTPSConsistency - Run same test multiple times to check consistency
func TestTPSConsistency(t *testing.T) {
	runs := 5
	config := TPSTestConfig{
		TotalTransactions:    500,
		TransactionsPerBlock: 25,
		Name:                 "Consistency_Check",
	}

	var results []TPSResult
	var tpsValues []float64

	for i := 0; i < runs; i++ {
		result := runTPSTest(t, config)
		results = append(results, result)
		tpsValues = append(tpsValues, result.TPS)
	}

	// Calculate statistics
	avgTPS := average(tpsValues)
	minTPS := min(tpsValues)
	maxTPS := max(tpsValues)
	variance := calculateVariance(tpsValues, avgTPS)
	stdDev := math.Sqrt(variance)
	variancePercent := ((maxTPS - minTPS) / avgTPS) * 100

	separator := strings.Repeat("=", 60)
	t.Logf("\n%s", separator)
	t.Logf("📊 CONSISTENCY TEST (%d runs)", runs)
	t.Logf("%s", separator)
	t.Logf("Average TPS:    %.2f", avgTPS)
	t.Logf("Min TPS:        %.2f", minTPS)
	t.Logf("Max TPS:        %.2f", maxTPS)
	t.Logf("Std Deviation:  %.2f", stdDev)
	t.Logf("Variance:       %.2f (%.1f%%)", maxTPS-minTPS, variancePercent)

	// Performance rating based on average
	var rating string
	if avgTPS >= 10000 {
		rating = "⭐⭐⭐⭐⭐ EXCEPTIONAL"
	} else if avgTPS >= 5000 {
		rating = "⭐⭐⭐⭐ EXCELLENT"
	} else if avgTPS >= 1000 {
		rating = "⭐⭐⭐ VERY GOOD"
	} else {
		rating = "⭐⭐ GOOD"
	}
	t.Logf("Rating:         %s", rating)
	t.Logf("%s", separator)

	t.Logf("\nIndividual runs:")
	for i, tps := range tpsValues {
		deviation := ((tps - avgTPS) / avgTPS) * 100
		t.Logf("  Run %d: %.2f TPS (%+.1f%% from avg)", i+1, tps, deviation)
	}
	t.Logf("%s\n", separator)

	// More realistic assertions for real-world conditions
	// 1. Average should be excellent (> 5000 TPS for 500 transactions)
	require.Greater(t, avgTPS, 5000.0, "Average TPS should be > 5000")

	// 2. All runs should be within reasonable range
	// Allow up to 60% variance to account for system variability and warmup
	require.Less(t, variancePercent, 60.0, "TPS variance should be < 60%")

	// 3. Analyze warm runs (excluding first 2 for warmup)
	if len(tpsValues) > 2 {
		warmRunsOnly := tpsValues[2:] // Skip first 2 runs for warmup
		warmAvg := average(warmRunsOnly)
		warmMin := min(warmRunsOnly)
		warmMax := max(warmRunsOnly)
		warmVariance := ((warmMax - warmMin) / warmAvg) * 100

		t.Logf("\n📊 WARM RUN ANALYSIS (runs 3-5):")
		t.Logf("  Warm Average:  %.2f TPS", warmAvg)
		t.Logf("  Warm Min:      %.2f TPS", warmMin)
		t.Logf("  Warm Max:      %.2f TPS", warmMax)
		t.Logf("  Warm Variance: %.1f%%", warmVariance)

		// After warmup, variance should be much better (< 15%)
		// This shows true performance consistency
		require.Less(t, warmVariance, 15.0,
			"TPS variance after warmup (runs 3-5) should be < 15%")

		// Warm runs should maintain high performance
		require.Greater(t, warmAvg, avgTPS*0.9,
			"Warm run average should be within 90% of overall average")
	}
}

// TestTPSScalability - Test how TPS scales with transaction volume
// TestTPSScalability - Test how TPS scales with transaction volume
// TestTPSScalability - Test how TPS scales with transaction volume
func TestTPSScalability(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping scalability test in short mode")
	}

	volumes := []int{100, 500, 1000, 2000, 5000}

	var results []struct {
		Volume int
		Result TPSResult
	}

	for _, volume := range volumes {
		config := TPSTestConfig{
			TotalTransactions:    volume,
			TransactionsPerBlock: 50,
			Name:                 fmt.Sprintf("Scalability_%d", volume),
		}

		result := runTPSTest(t, config)
		results = append(results, struct {
			Volume int
			Result TPSResult
		}{volume, result})
	}

	// Print scalability analysis
	separator := strings.Repeat("=", 90)
	t.Logf("\n%s", separator)
	t.Logf("📊 SCALABILITY ANALYSIS - How TPS scales with transaction volume")
	t.Logf("%s", separator)
	t.Logf("%-12s | %-10s | %-15s | %-12s | %-12s | %s",
		"Volume", "Blocks", "Duration", "TPS", "Efficiency", "Rating")
	t.Logf("%s", strings.Repeat("-", 90))

	baselineTPS := results[0].Result.TPS
	var maxTPS float64

	for _, r := range results {
		efficiency := (r.Result.TPS / baselineTPS) * 100

		if r.Result.TPS > maxTPS {
			maxTPS = r.Result.TPS
		}

		// Rating based on TPS
		var rating string
		if r.Result.TPS >= 10000 {
			rating = "⭐⭐⭐⭐⭐"
		} else if r.Result.TPS >= 5000 {
			rating = "⭐⭐⭐⭐"
		} else if r.Result.TPS >= 1000 {
			rating = "⭐⭐⭐"
		} else {
			rating = "⭐⭐"
		}

		t.Logf("%-12d | %-10d | %-15v | %-12.2f | %-12.1f%% | %s",
			r.Volume,
			r.Result.TotalBlocks,
			r.Result.Duration,
			r.Result.TPS,
			efficiency,
			rating)
	}
	t.Logf("%s", separator)

	// Calculate scaling efficiency
	t.Logf("\n📈 SCALING METRICS:")
	for i := 1; i < len(results); i++ {
		prev := results[i-1]
		curr := results[i]

		volumeIncrease := float64(curr.Volume) / float64(prev.Volume)
		tpsChange := ((curr.Result.TPS - prev.Result.TPS) / prev.Result.TPS) * 100

		var trend string
		if tpsChange > 5 {
			trend = "📈 IMPROVING"
		} else if tpsChange > -5 {
			trend = "➡️  STABLE"
		} else if tpsChange > -30 {
			trend = "📉 DECLINING"
		} else {
			trend = "⚠️  DEGRADING"
		}

		t.Logf("  %d → %d tx (%.1fx volume): TPS change %+.1f%% %s",
			prev.Volume, curr.Volume, volumeIncrease, tpsChange, trend)
	}

	// Overall assessment
	lastResult := results[len(results)-1]
	t.Logf("\n🎯 SCALABILITY ASSESSMENT:")
	t.Logf("  Tested range: %d - %d transactions", volumes[0], volumes[len(volumes)-1])
	t.Logf("  Peak TPS: %.2f", maxTPS)
	t.Logf("  Sustained TPS at max volume: %.2f", lastResult.Result.TPS)

	// Find optimal range
	var optimalRange string
	if maxTPS > 15000 {
		optimalRange = "100-1000 tx (burst mode)"
	} else if maxTPS > 10000 {
		optimalRange = "100-500 tx (burst mode)"
	} else {
		optimalRange = "Small batches recommended"
	}
	t.Logf("  Optimal performance range: %s", optimalRange)

	// Performance classification
	if lastResult.Result.TPS >= 1000 {
		t.Logf("  ✅ PRODUCTION READY: Maintains good throughput at scale")
	} else {
		t.Logf("  ⚠️  OPTIMIZATION NEEDED: Performance declines at high volume")
	}

	t.Logf("%s\n", separator)

	// Realistic assertions for blockchain performance
	// 1. Peak performance should be excellent
	require.Greater(t, maxTPS, 10000.0,
		"Peak TPS should exceed 10,000 for burst loads")

	// 2. Should maintain at least 1000 TPS even at maximum tested volume
	require.Greater(t, lastResult.Result.TPS, 1000.0,
		"Should maintain > 1000 TPS even at 5000 transaction volume")

	// 3. Mid-range performance (1000 tx) should be strong
	midRangeResult := results[2] // 1000 tx test
	require.Greater(t, midRangeResult.Result.TPS, 10000.0,
		"Should maintain > 10K TPS for 1000 transaction batches")
}

// Helper function to extract TPS values from results
func extractTPS(results []struct {
	Volume int
	Result TPSResult
}) []float64 {
	tpsValues := make([]float64, len(results))
	for i, r := range results {
		tpsValues[i] = r.Result.TPS
	}
	return tpsValues
}

// TestTPSWithMetrics - Detailed performance metrics
func TestTPSWithMetrics(t *testing.T) {
	config := TPSTestConfig{
		TotalTransactions:    1000,
		TransactionsPerBlock: 50,
		Name:                 "Detailed_Metrics",
	}

	result := runTPSTestWithMetrics(t, config)
	printDetailedMetrics(t, result)
}

// Enhanced TPSResult with more metrics
type DetailedTPSResult struct {
	TPSResult
	AvgBlockTime   time.Duration
	MinBlockTime   time.Duration
	MaxBlockTime   time.Duration
	BlockTimes     []time.Duration
	TotalGasUsed   int64
	AvgGasPerBlock int64
	MemoryUsedMB   float64
}

// runTPSTestWithMetrics - Enhanced version with detailed metrics
func runTPSTestWithMetrics(t *testing.T, cfg TPSTestConfig) DetailedTPSResult {
	// Setup (same as runTPSTest)
	testConfig, err := config.Load()
	require.NoError(t, err)

	dataDir := fmt.Sprintf("/tmp/tps_metrics_%d", time.Now().UnixNano())
	badgerStorage, err := storage.NewBadgerStorage(dataDir)
	require.NoError(t, err)
	defer badgerStorage.Close()

	worldState, err := state.NewWorldState(dataDir, account.ShardID(0), 1, testConfig, badgerStorage)
	require.NoError(t, err)
	defer worldState.Close()

	genesisAddress := testConfig.Genesis.Accounts[0].Address

	validator := &core.Validator{
		Address:        genesisAddress,
		Pubkey:         []byte("test_pubkey"),
		Stake:          100000 * config.BaseUnit,
		SelfStake:      100000 * config.BaseUnit,
		DelegatedStake: 0,
		Delegators:     make(map[string]int64),
		Commission:     0.05,
		Active:         true,
		CreatedAt:      time.Now().Unix(),
		UpdatedAt:      time.Now().Unix(),
	}
	err = worldState.AddValidator(validator)
	require.NoError(t, err)

	// Track detailed metrics
	var blockTimes []time.Duration
	var totalGasUsed int64

	startTime := time.Now()
	successfulTxs := 0
	totalBlocks := 0
	numBlocks := cfg.TotalTransactions / cfg.TransactionsPerBlock

	for blockNum := 0; blockNum < numBlocks; blockNum++ {
		var blockTransactions []*core.Transaction

		for i := 0; i < cfg.TransactionsPerBlock; i++ {
			privKey, err := crypto.NewPrivateKey()
			require.NoError(t, err)
			recipient, err := address.GenerateAddress(privKey.PublicKey().Bytes())
			require.NoError(t, err)

			txID := fmt.Sprintf("tx-%d-%d", blockNum, i)
			nonce := uint64(blockNum*cfg.TransactionsPerBlock + i)

			tx := &core.Transaction{
				Id:        txID,
				From:      genesisAddress,
				To:        recipient,
				Amount:    1000 * config.BaseUnit,
				Timestamp: time.Now().Unix(),
				Nonce:     nonce,
				Gas:       21000,
				GasPrice:  1000,
				Signature: []byte("test_signature"),
			}

			blockTransactions = append(blockTransactions, tx)
		}

		currentBlock := worldState.GetCurrentBlock()
		var prevHash string
		var blockIndex int64 = 1
		var blockTimestamp int64

		if currentBlock != nil {
			prevHash = currentBlock.Hash
			blockIndex = currentBlock.Header.Index + 1
			blockTimestamp = currentBlock.Header.Timestamp + 1
		} else {
			blockTimestamp = time.Now().Unix()
		}

		block := &core.Block{
			Header: &core.BlockHeader{
				Index:     blockIndex,
				PrevHash:  prevHash,
				Timestamp: blockTimestamp,
				Validator: validator.Address,
				GasLimit:  10000000,
				GasUsed:   int64(len(blockTransactions) * 21000),
				StateRoot: "",
			},
			Transactions: blockTransactions,
		}
		block.Hash = fmt.Sprintf("block_%d_%x", blockIndex, blockTimestamp)

		// Time block addition
		blockStart := time.Now()
		err = worldState.AddBlock(block)
		require.NoError(t, err)
		blockTime := time.Since(blockStart)

		blockTimes = append(blockTimes, blockTime)
		totalGasUsed += block.Header.GasUsed

		successfulTxs += len(blockTransactions)
		totalBlocks++
	}

	duration := time.Since(startTime)
	tps := float64(successfulTxs) / duration.Seconds()
	avgTxPerBlock := float64(successfulTxs) / float64(totalBlocks)

	// Calculate block time statistics
	avgBlockTime := averageDuration(blockTimes)
	minBlockTime := minDuration(blockTimes)
	maxBlockTime := maxDuration(blockTimes)
	avgGasPerBlock := totalGasUsed / int64(totalBlocks)

	return DetailedTPSResult{
		TPSResult: TPSResult{
			TotalTransactions: successfulTxs,
			TotalBlocks:       totalBlocks,
			Duration:          duration,
			TPS:               tps,
			AvgTxPerBlock:     avgTxPerBlock,
		},
		AvgBlockTime:   avgBlockTime,
		MinBlockTime:   minBlockTime,
		MaxBlockTime:   maxBlockTime,
		BlockTimes:     blockTimes,
		TotalGasUsed:   totalGasUsed,
		AvgGasPerBlock: avgGasPerBlock,
	}
}

// Helper functions
func average(values []float64) float64 {
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func min(values []float64) float64 {
	minVal := values[0]
	for _, v := range values {
		if v < minVal {
			minVal = v
		}
	}
	return minVal
}

func max(values []float64) float64 {
	maxVal := values[0]
	for _, v := range values {
		if v > maxVal {
			maxVal = v
		}
	}
	return maxVal
}

func calculateVariance(values []float64, mean float64) float64 {
	sumSquares := 0.0
	for _, v := range values {
		diff := v - mean
		sumSquares += diff * diff
	}
	return sumSquares / float64(len(values))
}

func averageDuration(durations []time.Duration) time.Duration {
	total := time.Duration(0)
	for _, d := range durations {
		total += d
	}
	return total / time.Duration(len(durations))
}

func minDuration(durations []time.Duration) time.Duration {
	minD := durations[0]
	for _, d := range durations {
		if d < minD {
			minD = d
		}
	}
	return minD
}

func maxDuration(durations []time.Duration) time.Duration {
	maxD := durations[0]
	for _, d := range durations {
		if d > maxD {
			maxD = d
		}
	}
	return maxD
}

func printDetailedMetrics(t *testing.T, result DetailedTPSResult) {
	separator := strings.Repeat("=", 70)
	t.Logf("\n%s", separator)
	t.Logf("📊 DETAILED PERFORMANCE METRICS")
	t.Logf("%s", separator)

	t.Logf("\n📈 THROUGHPUT:")
	t.Logf("  Total Transactions:   %d", result.TotalTransactions)
	t.Logf("  Total Blocks:         %d", result.TotalBlocks)
	t.Logf("  Avg Tx/Block:         %.1f", result.AvgTxPerBlock)
	t.Logf("  Overall TPS:          %.2f", result.TPS)

	t.Logf("\n⏱️  TIMING:")
	t.Logf("  Total Duration:       %v", result.Duration)
	t.Logf("  Avg Block Time:       %v", result.AvgBlockTime)
	t.Logf("  Min Block Time:       %v", result.MinBlockTime)
	t.Logf("  Max Block Time:       %v", result.MaxBlockTime)
	t.Logf("  Block Time Variance:  %v", result.MaxBlockTime-result.MinBlockTime)

	t.Logf("\n⛽ GAS METRICS:")
	t.Logf("  Total Gas Used:       %d", result.TotalGasUsed)
	t.Logf("  Avg Gas/Block:        %d", result.AvgGasPerBlock)
	t.Logf("  Avg Gas/Transaction:  %d", result.TotalGasUsed/int64(result.TotalTransactions))

	// Block time distribution
	t.Logf("\n📊 BLOCK TIME DISTRIBUTION (first 10 blocks):")
	displayCount := 10
	if len(result.BlockTimes) < displayCount {
		displayCount = len(result.BlockTimes)
	}
	for i := 0; i < displayCount; i++ {
		t.Logf("  Block %2d: %v", i+1, result.BlockTimes[i])
	}

	t.Logf("\n%s\n", separator)
}
