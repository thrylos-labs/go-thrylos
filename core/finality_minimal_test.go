package core

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"

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

// TestFinalityWithJitter - tests finality with realistic network variance
func TestFinalityWithJitter(t *testing.T) {
	// Production-like block interval with jitter (like real networks)
	const BASE_BLOCK_INTERVAL = 12 * time.Second
	const MAX_JITTER = 2 * time.Second // ±2 seconds variance

	// Setup
	testConfig, err := config.Load()
	require.NoError(t, err)
	existingGenesisAccount := testConfig.Genesis.Accounts[0]
	genesisAddress := existingGenesisAccount.Address

	dataDir := fmt.Sprintf("/tmp/jitter_finality_test_%d", time.Now().UnixNano())
	badgerStorage, err := storage.NewBadgerStorage(dataDir)
	require.NoError(t, err)
	defer badgerStorage.Close()

	worldState, err := state.NewWorldState(dataDir, account.ShardID(0), 1, testConfig, badgerStorage)
	require.NoError(t, err)
	defer worldState.Close()

	txValidator := transaction.NewValidator(account.ShardID(0), 1, testConfig)

	// Create validator
	validator := &core.Validator{
		Address:        genesisAddress,
		Pubkey:         []byte("dummy_pubkey_for_testing"),
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

	// Create transaction
	recipientPrivKey, err := crypto.NewPrivateKey()
	require.NoError(t, err)
	recipientAddress, err := address.GenerateAddress(recipientPrivKey.PublicKey().Bytes())
	require.NoError(t, err)

	tx, err := txValidator.CreateTransferTransaction(
		genesisAddress, recipientAddress, 1000*config.BaseUnit, 21000, 1000, 0,
	)
	require.NoError(t, err)
	tx.Signature = []byte("dummy_signature_for_testing")

	startTime := time.Now()
	err = worldState.AddTransaction(tx)
	require.NoError(t, err)

	pendingTxs := worldState.GetPendingTransactions()
	require.Greater(t, len(pendingTxs), 0)

	// Add realistic network jitter (simulate real blockchain variance)
	jitter := time.Duration((rand.Float64()-0.5)*2) * MAX_JITTER // Random ±2s
	actualBlockInterval := BASE_BLOCK_INTERVAL + jitter

	t.Logf("🎲 Network jitter: %v (actual interval: %v)", jitter, actualBlockInterval)

	// Calculate next block time with jitter
	currentBlock := worldState.GetCurrentBlock()
	var nextBlockTime time.Time
	if currentBlock != nil {
		lastBlockTime := time.Unix(currentBlock.Header.Timestamp, 0)
		nextBlockTime = lastBlockTime.Add(actualBlockInterval)
	} else {
		nextBlockTime = startTime.Add(actualBlockInterval)
	}

	// Wait and create block
	waitTime := time.Until(nextBlockTime)
	if waitTime > 0 {
		t.Logf("⏰ Waiting %v for next block slot (with jitter)...", waitTime)
		time.Sleep(waitTime)
	}

	// Create block
	block := createTestBlock(t, worldState, validator, pendingTxs, nextBlockTime.Unix())

	blockStartTime := time.Now()
	err = worldState.AddBlock(block)
	require.NoError(t, err)
	blockProcessingTime := time.Since(blockStartTime)

	totalFinalityTime := time.Since(startTime)

	t.Logf("📊 FINALITY WITH JITTER:")
	t.Logf("   • Base interval: %v", BASE_BLOCK_INTERVAL)
	t.Logf("   • Network jitter: %v", jitter)
	t.Logf("   • Actual interval: %v", actualBlockInterval)
	t.Logf("   • Total finality: %v", totalFinalityTime)
	t.Logf("   • Block processing: %v", blockProcessingTime)

	// Verify finality is within reasonable bounds (considering jitter)
	minExpected := BASE_BLOCK_INTERVAL - MAX_JITTER - time.Second
	maxExpected := BASE_BLOCK_INTERVAL + MAX_JITTER + time.Second
	require.True(t, totalFinalityTime >= minExpected && totalFinalityTime <= maxExpected,
		"Finality time %v should be between %v and %v", totalFinalityTime, minExpected, maxExpected)

	t.Logf("✅ Jitter finality test completed!")
}

// TestWorstCaseFinalityLatency - tests maximum finality time (transaction submitted just after block)
func TestWorstCaseFinalityLatency(t *testing.T) {
	const BLOCK_INTERVAL = 5 * time.Second // Shorter for testing

	// Setup
	testConfig, err := config.Load()
	require.NoError(t, err)
	existingGenesisAccount := testConfig.Genesis.Accounts[0]
	genesisAddress := existingGenesisAccount.Address

	dataDir := fmt.Sprintf("/tmp/worst_case_finality_test_%d", time.Now().UnixNano())
	badgerStorage, err := storage.NewBadgerStorage(dataDir)
	require.NoError(t, err)
	defer badgerStorage.Close()

	worldState, err := state.NewWorldState(dataDir, account.ShardID(0), 1, testConfig, badgerStorage)
	require.NoError(t, err)
	defer worldState.Close()

	txValidator := transaction.NewValidator(account.ShardID(0), 1, testConfig)

	validator := &core.Validator{
		Address:        genesisAddress,
		Pubkey:         []byte("dummy_pubkey_for_testing"),
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

	// Get genesis block timestamp and create initial block with proper timing
	genesisBlock := worldState.GetCurrentBlock()
	initialBlockTime := time.Unix(genesisBlock.Header.Timestamp, 0).Add(BLOCK_INTERVAL)

	initialBlock := createTestBlock(t, worldState, validator, []*core.Transaction{}, initialBlockTime.Unix())
	err = worldState.AddBlock(initialBlock)
	require.NoError(t, err)

	t.Logf("📦 Initial block created, now testing worst-case scenario...")

	// Wait a tiny bit after block creation (simulating transaction arriving just after block)
	time.Sleep(100 * time.Millisecond)

	// Create transaction
	recipientPrivKey, err := crypto.NewPrivateKey()
	require.NoError(t, err)
	recipientAddress, err := address.GenerateAddress(recipientPrivKey.PublicKey().Bytes())
	require.NoError(t, err)

	tx, err := txValidator.CreateTransferTransaction(
		genesisAddress, recipientAddress, 1000*config.BaseUnit, 21000, 1000, 0, // nonce=0 (correct)
	)
	require.NoError(t, err)
	tx.Signature = []byte("dummy_signature_for_testing")

	// Submit transaction (this represents worst case - just missed the previous block)
	txStartTime := time.Now()
	err = worldState.AddTransaction(tx)
	require.NoError(t, err)

	t.Logf("⚡ Transaction submitted just after block creation (worst case)")

	// Calculate next block time
	currentBlock := worldState.GetCurrentBlock()
	lastBlockTime := time.Unix(currentBlock.Header.Timestamp, 0)
	nextBlockTime := lastBlockTime.Add(BLOCK_INTERVAL)

	waitTime := time.Until(nextBlockTime)
	t.Logf("⏰ Must wait %v for next block (worst case latency)", waitTime)

	// This should be close to the full block interval
	require.True(t, waitTime > BLOCK_INTERVAL-500*time.Millisecond,
		"Wait time %v should be close to full interval %v", waitTime, BLOCK_INTERVAL)

	// Wait and create block
	time.Sleep(waitTime)

	pendingTxs := worldState.GetPendingTransactions()
	block := createTestBlock(t, worldState, validator, pendingTxs, nextBlockTime.Unix())

	err = worldState.AddBlock(block)
	require.NoError(t, err)

	totalFinalityTime := time.Since(txStartTime)

	t.Logf("📊 WORST-CASE FINALITY:")
	t.Logf("   • Block interval: %v", BLOCK_INTERVAL)
	t.Logf("   • Wait time: %v", waitTime)
	t.Logf("   • Total finality: %v", totalFinalityTime)
	t.Logf("   • Percentage of interval: %.1f%%", float64(totalFinalityTime)/float64(BLOCK_INTERVAL)*100)

	// Worst case should be close to full block interval
	require.True(t, totalFinalityTime >= BLOCK_INTERVAL-500*time.Millisecond,
		"Worst case finality %v should be close to interval %v", totalFinalityTime, BLOCK_INTERVAL)

	t.Logf("✅ Worst-case finality test completed!")
}

// TestBestCaseFinalityLatency - tests minimum finality time (transaction submitted just before block)
func TestBestCaseFinalityLatency(t *testing.T) {
	const BLOCK_INTERVAL = 5 * time.Second

	// Setup
	testConfig, err := config.Load()
	require.NoError(t, err)
	existingGenesisAccount := testConfig.Genesis.Accounts[0]
	genesisAddress := existingGenesisAccount.Address

	dataDir := fmt.Sprintf("/tmp/best_case_finality_test_%d", time.Now().UnixNano())
	badgerStorage, err := storage.NewBadgerStorage(dataDir)
	require.NoError(t, err)
	defer badgerStorage.Close()

	worldState, err := state.NewWorldState(dataDir, account.ShardID(0), 1, testConfig, badgerStorage)
	require.NoError(t, err)
	defer worldState.Close()

	txValidator := transaction.NewValidator(account.ShardID(0), 1, testConfig)

	validator := &core.Validator{
		Address:        genesisAddress,
		Pubkey:         []byte("dummy_pubkey_for_testing"),
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

	// Calculate when the next block should occur
	currentBlock := worldState.GetCurrentBlock()
	lastBlockTime := time.Unix(currentBlock.Header.Timestamp, 0)
	nextBlockTime := lastBlockTime.Add(BLOCK_INTERVAL)

	// Wait until just before the next block (best case scenario)
	waitUntilNearBlock := time.Until(nextBlockTime) - 200*time.Millisecond
	if waitUntilNearBlock > 0 {
		t.Logf("⏳ Waiting %v to get close to next block slot...", waitUntilNearBlock)
		time.Sleep(waitUntilNearBlock)
	}

	// Create transaction just before block creation (best case)
	recipientPrivKey, err := crypto.NewPrivateKey()
	require.NoError(t, err)
	recipientAddress, err := address.GenerateAddress(recipientPrivKey.PublicKey().Bytes())
	require.NoError(t, err)

	tx, err := txValidator.CreateTransferTransaction(
		genesisAddress, recipientAddress, 1000*config.BaseUnit, 21000, 1000, 0, // nonce=0 (correct)
	)
	require.NoError(t, err)
	tx.Signature = []byte("dummy_signature_for_testing")

	// Submit transaction just before block slot
	txStartTime := time.Now()
	err = worldState.AddTransaction(tx)
	require.NoError(t, err)

	t.Logf("🚀 Transaction submitted just before block creation (best case)")

	// Wait for the block slot
	remainingWait := time.Until(nextBlockTime)
	if remainingWait > 0 {
		t.Logf("⏰ Waiting remaining %v for block slot", remainingWait)
		time.Sleep(remainingWait)
	}

	pendingTxs := worldState.GetPendingTransactions()
	block := createTestBlock(t, worldState, validator, pendingTxs, nextBlockTime.Unix())

	err = worldState.AddBlock(block)
	require.NoError(t, err)

	totalFinalityTime := time.Since(txStartTime)

	t.Logf("📊 BEST-CASE FINALITY:")
	t.Logf("   • Block interval: %v", BLOCK_INTERVAL)
	t.Logf("   • Total finality: %v", totalFinalityTime)
	t.Logf("   • Percentage of interval: %.1f%%", float64(totalFinalityTime)/float64(BLOCK_INTERVAL)*100)

	// Best case should be very short (< 500ms)
	require.True(t, totalFinalityTime < 500*time.Millisecond,
		"Best case finality %v should be very short", totalFinalityTime)

	t.Logf("✅ Best-case finality test completed!")
}

// TestFinalityAcrossMultipleValidators - simulates multiple validators with realistic timing
func TestFinalityAcrossMultipleValidators(t *testing.T) {
	const BLOCK_INTERVAL = 3 * time.Second
	const NUM_VALIDATORS = 3
	const BLOCKS_PER_VALIDATOR = 2

	testConfig, err := config.Load()
	require.NoError(t, err)

	dataDir := fmt.Sprintf("/tmp/multi_validator_finality_test_%d", time.Now().UnixNano())
	badgerStorage, err := storage.NewBadgerStorage(dataDir)
	require.NoError(t, err)
	defer badgerStorage.Close()

	worldState, err := state.NewWorldState(dataDir, account.ShardID(0), 1, testConfig, badgerStorage)
	require.NoError(t, err)
	defer worldState.Close()

	// Create multiple validators with proper addresses
	validators := make([]*core.Validator, NUM_VALIDATORS)
	for i := 0; i < NUM_VALIDATORS; i++ {
		// Generate proper validator addresses
		validatorPrivKey, err := crypto.NewPrivateKey()
		require.NoError(t, err)
		validatorAddress, err := address.GenerateAddress(validatorPrivKey.PublicKey().Bytes())
		require.NoError(t, err)

		validators[i] = &core.Validator{
			Address:        validatorAddress,
			Pubkey:         validatorPrivKey.PublicKey().Bytes(),
			Stake:          100000 * config.BaseUnit,
			SelfStake:      100000 * config.BaseUnit,
			DelegatedStake: 0,
			Delegators:     make(map[string]int64),
			Commission:     0.05,
			Active:         true,
			CreatedAt:      time.Now().Unix(),
			UpdatedAt:      time.Now().Unix(),
		}
		err = worldState.AddValidator(validators[i])
		require.NoError(t, err)
	}

	t.Logf("🏛️ Created %d validators, testing finality across validator rotation", NUM_VALIDATORS)

	startTime := time.Now()
	var lastBlockTime time.Time

	// Create blocks with different validators (simulating validator rotation)
	totalBlocks := NUM_VALIDATORS * BLOCKS_PER_VALIDATOR
	for i := 0; i < totalBlocks; i++ {
		validatorIndex := i % NUM_VALIDATORS
		currentValidator := validators[validatorIndex]

		// Calculate block timing
		var nextBlockTime time.Time
		if i == 0 {
			nextBlockTime = startTime.Add(BLOCK_INTERVAL)
		} else {
			nextBlockTime = lastBlockTime.Add(BLOCK_INTERVAL)
		}

		// Wait for block slot
		waitTime := time.Until(nextBlockTime)
		if waitTime > 0 {
			time.Sleep(waitTime)
		}

		// Create block
		block := createTestBlock(t, worldState, currentValidator, []*core.Transaction{}, nextBlockTime.Unix())

		blockStartTime := time.Now()
		err = worldState.AddBlock(block)
		require.NoError(t, err)
		blockProcessingTime := time.Since(blockStartTime)

		lastBlockTime = nextBlockTime
		t.Logf("🔨 Block %d created by %s (processed in %v)",
			i+1, currentValidator.Address[:10]+"...", blockProcessingTime)
	}

	totalTime := time.Since(startTime)
	expectedTime := BLOCK_INTERVAL * time.Duration(totalBlocks)
	avgBlockTime := totalTime / time.Duration(totalBlocks)

	t.Logf("📊 MULTI-VALIDATOR METRICS:")
	t.Logf("   • Total blocks: %d", totalBlocks)
	t.Logf("   • Validators: %d", NUM_VALIDATORS)
	t.Logf("   • Total time: %v", totalTime)
	t.Logf("   • Expected time: %v", expectedTime)
	t.Logf("   • Average block time: %v", avgBlockTime)
	t.Logf("   • Target interval: %v", BLOCK_INTERVAL)

	// Verify timing consistency across validators
	tolerance := 500 * time.Millisecond * time.Duration(totalBlocks)
	require.True(t, totalTime >= expectedTime-tolerance && totalTime <= expectedTime+tolerance,
		"Multi-validator timing should be consistent")

	t.Logf("✅ Multi-validator finality test completed!")
}

// Helper function to create test blocks
func createTestBlock(t *testing.T, worldState *state.WorldState, validator *core.Validator,
	transactions []*core.Transaction, timestamp int64) *core.Block {

	currentBlock := worldState.GetCurrentBlock()
	var prevHash string
	var blockIndex int64 = 1

	if currentBlock != nil {
		prevHash = currentBlock.Hash
		blockIndex = currentBlock.Header.Index + 1
	}

	block := &core.Block{
		Header: &core.BlockHeader{
			Index:     blockIndex,
			PrevHash:  prevHash,
			Timestamp: timestamp,
			Validator: validator.Address,
			GasLimit:  1000000,
			GasUsed:   int64(len(transactions) * 21000),
			StateRoot: "",
		},
		Transactions: transactions,
	}

	block.Hash = fmt.Sprintf("block_%d_%s_%x", blockIndex, validator.Address, timestamp)
	return block
}

// TestConfigDrivenFinality - uses actual blockchain config for realistic testing
func TestConfigDrivenFinality(t *testing.T) {
	// Load config
	testConfig, err := config.Load()
	require.NoError(t, err)

	// Use ACTUAL blockchain block time from config (3 seconds!)
	BLOCK_INTERVAL := testConfig.Consensus.BlockTime

	existingGenesisAccount := testConfig.Genesis.Accounts[0]
	genesisAddress := existingGenesisAccount.Address

	t.Logf("🚀 Using PRODUCTION block time: %v", BLOCK_INTERVAL)
	t.Logf("🏦 Using genesis address: %s", genesisAddress)

	// Setup (same as before)
	dataDir := fmt.Sprintf("/tmp/config_driven_finality_test_%d", time.Now().UnixNano())
	badgerStorage, err := storage.NewBadgerStorage(dataDir)
	require.NoError(t, err)
	defer badgerStorage.Close()

	worldState, err := state.NewWorldState(dataDir, account.ShardID(0), 1, testConfig, badgerStorage)
	require.NoError(t, err)
	defer worldState.Close()

	txValidator := transaction.NewValidator(account.ShardID(0), 1, testConfig)

	// Create validator
	validator := &core.Validator{
		Address:        genesisAddress,
		Pubkey:         []byte("dummy_pubkey_for_testing"),
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

	// Create transaction
	recipientPrivKey, err := crypto.NewPrivateKey()
	require.NoError(t, err)
	recipientAddress, err := address.GenerateAddress(recipientPrivKey.PublicKey().Bytes())
	require.NoError(t, err)

	tx, err := txValidator.CreateTransferTransaction(
		genesisAddress, recipientAddress, 1000*config.BaseUnit, 21000, 1000, 0,
	)
	require.NoError(t, err)
	tx.Signature = []byte("dummy_signature_for_testing")

	// Submit transaction and measure finality
	startTime := time.Now()
	err = worldState.AddTransaction(tx)
	require.NoError(t, err)

	pendingTxs := worldState.GetPendingTransactions()
	require.Greater(t, len(pendingTxs), 0)

	// Calculate next block time using ACTUAL config
	currentBlock := worldState.GetCurrentBlock()
	var nextBlockTime time.Time
	if currentBlock != nil {
		lastBlockTime := time.Unix(currentBlock.Header.Timestamp, 0)
		nextBlockTime = lastBlockTime.Add(BLOCK_INTERVAL)
	} else {
		nextBlockTime = startTime.Add(BLOCK_INTERVAL)
	}

	t.Logf("⏳ Next block in %v (production timing)", time.Until(nextBlockTime))

	// Wait for block slot
	waitTime := time.Until(nextBlockTime)
	if waitTime > 0 {
		time.Sleep(waitTime)
	}

	// Create block
	block := &core.Block{
		Header: &core.BlockHeader{
			Index:     1,
			PrevHash:  currentBlock.Hash,
			Timestamp: nextBlockTime.Unix(),
			Validator: validator.Address,
			GasLimit:  1000000,
			GasUsed:   21000,
			StateRoot: "",
		},
		Transactions: pendingTxs,
	}
	block.Hash = fmt.Sprintf("block_hash_%d", time.Now().UnixNano())

	blockStartTime := time.Now()
	err = worldState.AddBlock(block)
	require.NoError(t, err)
	blockProcessingTime := time.Since(blockStartTime)

	totalFinalityTime := time.Since(startTime)

	t.Logf("🏎️  PRODUCTION FINALITY METRICS:")
	t.Logf("   • Block interval: %v", BLOCK_INTERVAL)
	t.Logf("   • Total finality: %v", totalFinalityTime)
	t.Logf("   • Block processing: %v", blockProcessingTime)
	t.Logf("   • Finality improvement: %.1fx faster than 12s",
		float64(12*time.Second)/float64(totalFinalityTime))

	// Verify finality is fast (should be ~3 seconds)
	expectedMin := BLOCK_INTERVAL - 500*time.Millisecond
	expectedMax := BLOCK_INTERVAL + 500*time.Millisecond

	require.True(t, totalFinalityTime >= expectedMin && totalFinalityTime <= expectedMax,
		"Finality time %v should be between %v and %v", totalFinalityTime, expectedMin, expectedMax)

	// Performance assertions for fast blockchain
	require.True(t, totalFinalityTime < 4*time.Second,
		"Fast blockchain should finalize in <4 seconds, got %v", totalFinalityTime)
	require.True(t, blockProcessingTime < 10*time.Millisecond,
		"Block processing should be <10ms, got %v", blockProcessingTime)

	t.Logf("✅ FAST blockchain finality verified!")
}

// TestProductionTimingComparison - compares your blockchain to others
func TestProductionTimingComparison(t *testing.T) {
	testConfig, err := config.Load()
	require.NoError(t, err)

	yourBlockTime := testConfig.Consensus.BlockTime

	// Blockchain comparison
	competitors := map[string]time.Duration{
		"Ethereum":  12 * time.Second,
		"Polygon":   2 * time.Second,
		"BSC":       3 * time.Second,
		"Avalanche": 1 * time.Second,
		"Solana":    400 * time.Millisecond,
		"THRYLOS":   yourBlockTime, // Your blockchain
	}

	t.Logf("🏁 BLOCKCHAIN SPEED COMPARISON:")

	// Sort by speed (fastest first)
	type blockchain struct {
		name string
		time time.Duration
	}

	var chains []blockchain
	for name, blockTime := range competitors {
		chains = append(chains, blockchain{name: name, time: blockTime})
	}

	sort.Slice(chains, func(i, j int) bool {
		return chains[i].time < chains[j].time
	})

	// Show ranking
	for i, chain := range chains {
		ranking := ""
		if chain.name == "THRYLOS" {
			ranking = " ← YOUR BLOCKCHAIN 🚀"
		}
		t.Logf("   %d. %s: %v%s", i+1, chain.name, chain.time, ranking)
	}

	// Performance category
	if yourBlockTime <= 1*time.Second {
		t.Logf("🏆 CATEGORY: Ultra-Fast (like Solana)")
	} else if yourBlockTime <= 3*time.Second {
		t.Logf("🏎️  CATEGORY: Fast (like Polygon/BSC)")
	} else if yourBlockTime <= 12*time.Second {
		t.Logf("⚡ CATEGORY: Standard (like Ethereum)")
	} else {
		t.Logf("🐌 CATEGORY: Slow")
	}

	// Calculate TPS potential (rough estimate)
	maxTxPerBlock := testConfig.Consensus.MaxTxPerBlock
	tpsEstimate := float64(maxTxPerBlock) / yourBlockTime.Seconds()

	t.Logf("📊 ESTIMATED PERFORMANCE:")
	t.Logf("   • Max TPS: %.0f transactions/second", tpsEstimate)
	t.Logf("   • Max tx/block: %d", maxTxPerBlock)
	t.Logf("   • Block time: %v", yourBlockTime)

	// Finality expectations
	avgFinality := yourBlockTime / 2 // Average case
	worstFinality := yourBlockTime   // Worst case

	t.Logf("🎯 USER EXPERIENCE:")
	t.Logf("   • Average finality: %v", avgFinality)
	t.Logf("   • Worst case: %v", worstFinality)
	t.Logf("   • Best case: <500ms")
}
