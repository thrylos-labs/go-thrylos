package core

import (
	"fmt"
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

// TestSequentialTPS - tests TPS with proper nonce management
func TestSequentialTPS(t *testing.T) {
	const (
		NUM_CYCLES    = 5  // Number of block cycles
		TXS_PER_CYCLE = 10 // Transactions per cycle (must be <= max per block)
	)

	t.Logf("🎯 SEQUENTIAL TPS TEST")
	t.Logf("   • Cycles: %d", NUM_CYCLES)
	t.Logf("   • Transactions per cycle: %d", TXS_PER_CYCLE)
	t.Logf("   • Total transactions: %d", NUM_CYCLES*TXS_PER_CYCLE)

	// Setup
	testConfig, err := config.Load()
	require.NoError(t, err)

	dataDir := fmt.Sprintf("/tmp/sequential_tps_test_%d", time.Now().UnixNano())
	badgerStorage, err := storage.NewBadgerStorage(dataDir)
	require.NoError(t, err)
	defer badgerStorage.Close()

	worldState, err := state.NewWorldState(dataDir, account.ShardID(0), 1, testConfig, badgerStorage)
	require.NoError(t, err)
	defer worldState.Close()

	// Get genesis account
	genesisAddress := testConfig.Genesis.Accounts[0].Address
	txValidator := transaction.NewValidator(account.ShardID(0), 1, testConfig)

	// Create validator
	validator := &core.Validator{
		Address:        genesisAddress,
		Pubkey:         []byte("dummy_pubkey"),
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

	// Create recipient accounts
	var recipients []string
	for i := 0; i < TXS_PER_CYCLE; i++ {
		privKey, err := crypto.NewPrivateKey()
		require.NoError(t, err)
		addr, err := address.GenerateAddress(privKey.PublicKey().Bytes())
		require.NoError(t, err)
		recipients = append(recipients, addr)
	}

	t.Logf("📬 Created %d recipient addresses", len(recipients))

	var allMetrics []CycleMetrics
	totalTxs := 0
	totalTime := time.Duration(0)

	// Run multiple cycles
	for cycle := 0; cycle < NUM_CYCLES; cycle++ {
		t.Logf("\n🔄 CYCLE %d/%d", cycle+1, NUM_CYCLES)

		metrics := runTransactionCycle(t, worldState, txValidator, validator, genesisAddress, recipients, cycle)
		allMetrics = append(allMetrics, metrics)
		totalTxs += metrics.TransactionsProcessed
		totalTime += metrics.CycleTime

		t.Logf("✅ Cycle %d: %d txs in %v (%.1f TPS)",
			cycle+1, metrics.TransactionsProcessed, metrics.CycleTime, metrics.CycleTPS)
	}

	// Calculate overall performance
	overallTPS := float64(totalTxs) / totalTime.Seconds()
	theoreticalTPS := float64(testConfig.Consensus.MaxTxPerBlock) / testConfig.Consensus.BlockTime.Seconds()
	efficiency := overallTPS / theoreticalTPS

	t.Logf("\n🏆 SEQUENTIAL TPS RESULTS:")
	t.Logf("   📊 OVERALL PERFORMANCE:")
	t.Logf("      • Total transactions: %d", totalTxs)
	t.Logf("      • Total time: %v", totalTime)
	t.Logf("      • Overall TPS: %.1f", overallTPS)
	t.Logf("      • Theoretical TPS: %.1f", theoreticalTPS)
	t.Logf("      • Efficiency: %.1f%%", efficiency*100)
	t.Logf("")
	t.Logf("   📈 CYCLE BREAKDOWN:")

	var avgSubmissionTime time.Duration
	var avgBlockTime time.Duration
	for i, metrics := range allMetrics {
		t.Logf("      Cycle %d: %d txs, %.1f TPS, submit: %v, block: %v",
			i+1, metrics.TransactionsProcessed, metrics.CycleTPS,
			metrics.SubmissionTime, metrics.BlockTime)
		avgSubmissionTime += metrics.SubmissionTime
		avgBlockTime += metrics.BlockTime
	}

	avgSubmissionTime /= time.Duration(len(allMetrics))
	avgBlockTime /= time.Duration(len(allMetrics))

	t.Logf("")
	t.Logf("   ⏱️  AVERAGE TIMINGS:")
	t.Logf("      • Avg submission time: %v", avgSubmissionTime)
	t.Logf("      • Avg block time: %v", avgBlockTime)
	t.Logf("      • Block interval: %v", testConfig.Consensus.BlockTime)

	// Performance assertions
	require.Greater(t, overallTPS, 50.0, "Should achieve at least 50 TPS overall")
	require.Greater(t, efficiency, 0.15, "Efficiency should be >15%%")

	if overallTPS >= 200 {
		t.Logf("🎉 EXCELLENT: High-performance blockchain!")
	} else if overallTPS >= 100 {
		t.Logf("✅ GOOD: Solid performance!")
	} else {
		t.Logf("✅ WORKING: Functional performance!")
	}
}

type CycleMetrics struct {
	CycleNumber           int
	TransactionsSubmitted int
	TransactionsProcessed int
	SubmissionTime        time.Duration
	BlockTime             time.Duration
	CycleTime             time.Duration
	CycleTPS              float64
}

func runTransactionCycle(t *testing.T, worldState *state.WorldState, txValidator *transaction.Validator,
	validator *core.Validator, senderAddress string, recipients []string, cycleNum int) CycleMetrics {

	// Get current nonce for sender
	currentNonce, err := worldState.GetNonce(senderAddress)
	require.NoError(t, err)

	t.Logf("   💰 Sender balance check...")
	balance, err := worldState.GetBalance(senderAddress)
	require.NoError(t, err)
	t.Logf("   💰 Current balance: %d THRYLOS, nonce: %d", balance/config.BaseUnit, currentNonce)

	cycleStart := time.Now()

	// Submit transactions
	submissionStart := time.Now()
	var submittedTxs []*core.Transaction

	for i, recipient := range recipients {
		tx, err := txValidator.CreateTransferTransaction(
			senderAddress,
			recipient,
			1000*config.BaseUnit,
			21000,
			1000,
			currentNonce+uint64(i), // Sequential nonces from current state
		)

		if err != nil {
			t.Logf("❌ Failed to create transaction %d: %v", i, err)
			continue
		}

		tx.Signature = []byte("dummy_signature")

		err = worldState.AddTransaction(tx)
		if err != nil {
			t.Logf("❌ Failed to submit transaction %d (nonce %d): %v", i, currentNonce+uint64(i), err)
			continue
		}

		submittedTxs = append(submittedTxs, tx)
	}

	submissionTime := time.Since(submissionStart)
	t.Logf("   📤 Submitted %d/%d transactions in %v", len(submittedTxs), len(recipients), submissionTime)

	// Create and process block
	blockStart := time.Now()
	pendingTxs := worldState.GetPendingTransactions()

	if len(pendingTxs) == 0 {
		t.Logf("   ❌ No pending transactions to process")
		return CycleMetrics{
			CycleNumber:           cycleNum,
			TransactionsSubmitted: len(submittedTxs),
			TransactionsProcessed: 0,
			SubmissionTime:        submissionTime,
			BlockTime:             0,
			CycleTime:             time.Since(cycleStart),
			CycleTPS:              0,
		}
	}

	block := createSequentialBlock(t, worldState, validator, pendingTxs)
	err = worldState.AddBlock(block)
	require.NoError(t, err)

	blockTime := time.Since(blockStart)
	cycleTime := time.Since(cycleStart)
	cycleTPS := float64(len(pendingTxs)) / cycleTime.Seconds()

	t.Logf("   ⛏️  Block created with %d transactions in %v", len(pendingTxs), blockTime)

	return CycleMetrics{
		CycleNumber:           cycleNum,
		TransactionsSubmitted: len(submittedTxs),
		TransactionsProcessed: len(pendingTxs),
		SubmissionTime:        submissionTime,
		BlockTime:             blockTime,
		CycleTime:             cycleTime,
		CycleTPS:              cycleTPS,
	}
}

func createSequentialBlock(t *testing.T, worldState *state.WorldState, validator *core.Validator,
	transactions []*core.Transaction) *core.Block {

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

	// Ensure timestamp is current
	currentTime := time.Now().Unix()
	if blockTimestamp < currentTime {
		blockTimestamp = currentTime
	}

	block := &core.Block{
		Header: &core.BlockHeader{
			Index:     blockIndex,
			PrevHash:  prevHash,
			Timestamp: blockTimestamp,
			Validator: validator.Address,
			GasLimit:  1000000,
			GasUsed:   int64(len(transactions) * 21000),
			StateRoot: "",
		},
		Transactions: transactions,
	}

	block.Hash = fmt.Sprintf("seq_block_%d_%x", blockIndex, blockTimestamp)
	return block
}
