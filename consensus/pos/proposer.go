// consensus/pos/proposer.go

// Block proposer for Proof of Stake consensus
package pos

import (
	"fmt"
	"log"
	"math/big"
	"sort"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	block2 "github.com/thrylos-labs/go-thrylos/core/block"
	"github.com/thrylos-labs/go-thrylos/core/math"
	coremath "github.com/thrylos-labs/go-thrylos/core/math" // Safe BigInt math
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/crypto/hash"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// BlockProposer handles block creation and proposal optimization
type BlockProposer struct {
	config      *config.Config
	worldState  *state.WorldState
	nodeAddress string

	// Block construction optimization
	maxBlockSize    int64
	maxTransactions int
	minGasPrice     string // BigInt string

	// Performance metrics
	blocksProposed      uint64
	avgBlockTime        time.Duration
	avgTransactionCount int
	totalFeesCollected  string // BigInt string

	// Transaction selection strategy
	selectionStrategy TransactionSelectionStrategy
}

// TransactionSelectionStrategy defines how transactions are selected for blocks
type TransactionSelectionStrategy string

const (
	StrategyHighestGasPrice TransactionSelectionStrategy = "highest_gas_price"
	StrategyFIFO            TransactionSelectionStrategy = "fifo"
	StrategyBalanced        TransactionSelectionStrategy = "balanced"
	StrategyOptimalPacking  TransactionSelectionStrategy = "optimal_packing"
)

// BlockConstructionResult contains the result of block construction
type BlockConstructionResult struct {
	Block             *core.Block         `json:"block"`
	IncludedTxs       []*core.Transaction `json:"included_txs"`
	ExcludedTxs       []*core.Transaction `json:"excluded_txs"`
	TotalGasUsed      int64               `json:"total_gas_used"`
	TotalFees         string              `json:"total_fees"` // BigInt string
	ConstructionTime  time.Duration       `json:"construction_time"`
	TransactionCount  int                 `json:"transaction_count"`
	BlockSize         int                 `json:"block_size"`
	OptimizationScore float64             `json:"optimization_score"`
}

// TransactionWithPriority wraps a transaction with selection priority
type TransactionWithPriority struct {
	Transaction *core.Transaction `json:"transaction"`
	Priority    float64           `json:"priority"`
	GasRatio    float64           `json:"gas_ratio"`
	FeePerGas   string            `json:"fee_per_gas"`
	Age         time.Duration     `json:"age"`
}

// NewBlockProposer creates a new optimized block proposer
func NewBlockProposer(config *config.Config, worldState *state.WorldState, nodeAddress string) *BlockProposer {
	return &BlockProposer{
		config:             config,
		worldState:         worldState,
		nodeAddress:        nodeAddress,
		maxBlockSize:       config.Consensus.MaxBlockSize,
		maxTransactions:    config.Consensus.MaxTxPerBlock,
		minGasPrice:        config.Consensus.MinGasPrice,
		selectionStrategy:  StrategyBalanced,
		totalFeesCollected: "0",
	}
}

// SetSelectionStrategy sets the transaction selection strategy
func (bp *BlockProposer) SetSelectionStrategy(strategy TransactionSelectionStrategy) {
	if bp.config != nil && bp.config.Environment != "development" {
		if strategy == StrategyHighestGasPrice || strategy == StrategyOptimalPacking {
			log.Printf("⚠️ Unsafe selection strategy %q disabled outside development; using balanced", strategy)
			bp.selectionStrategy = StrategyBalanced
			return
		}
	}
	bp.selectionStrategy = strategy
}

// ProposeBlock creates and proposes a new block with optimal transaction selection
func (bp *BlockProposer) ProposeBlock(slot uint64, epoch uint64) (*BlockConstructionResult, error) {
	startTime := time.Now()

	// Get available transactions from the pool
	availableTxs := bp.worldState.GetPendingTransactions()
	if len(availableTxs) == 0 {
		return bp.createEmptyBlock(slot, epoch, startTime)
	}

	// Select transactions for the block based on strategy
	selectedTxs, excludedTxs, err := bp.selectTransactions(availableTxs)
	if err != nil {
		return nil, fmt.Errorf("transaction selection failed: %v", err)
	}

	// Construct the block
	block, err := bp.constructBlock(selectedTxs, slot, epoch)
	if err != nil {
		return nil, fmt.Errorf("block construction failed: %v", err)
	}

	// Calculate metrics
	constructionTime := time.Since(startTime)
	totalGasUsed, err := bp.calculateTotalGas(selectedTxs)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate total gas: %v", err)
	}
	totalFees := bp.calculateTotalFees(selectedTxs)
	optimizationScore := bp.calculateOptimizationScore(selectedTxs, constructionTime)

	// Update proposer metrics
	bp.updateMetrics(constructionTime, len(selectedTxs), totalFees)

	return &BlockConstructionResult{
		Block:             block,
		IncludedTxs:       selectedTxs,
		ExcludedTxs:       excludedTxs,
		TotalGasUsed:      totalGasUsed,
		TotalFees:         totalFees,
		ConstructionTime:  constructionTime,
		TransactionCount:  len(selectedTxs),
		BlockSize:         bp.estimateBlockSize(selectedTxs),
		OptimizationScore: optimizationScore,
	}, nil
}

// selectTransactions selects transactions based on the configured strategy
func (bp *BlockProposer) selectTransactions(availableTxs []*core.Transaction) ([]*core.Transaction, []*core.Transaction, error) {
	switch bp.selectionStrategy {
	case StrategyHighestGasPrice:
		return bp.selectByHighestGasPrice(availableTxs)
	case StrategyFIFO:
		return bp.selectByFIFO(availableTxs)
	case StrategyBalanced:
		return bp.selectBalanced(availableTxs)
	case StrategyOptimalPacking:
		return bp.selectOptimalPacking(availableTxs)
	default:
		return bp.selectBalanced(availableTxs)
	}
}

// selectByHighestGasPrice selects transactions with highest gas prices first
func (bp *BlockProposer) selectByHighestGasPrice(availableTxs []*core.Transaction) ([]*core.Transaction, []*core.Transaction, error) {
	sort.Slice(availableTxs, func(i, j int) bool {
		gasI := coremath.ParseBigInt(availableTxs[i].GasPrice)
		gasJ := coremath.ParseBigInt(availableTxs[j].GasPrice)
		return gasI.Cmp(gasJ) > 0
	})

	return bp.packTransactions(availableTxs)
}

// selectByFIFO selects transactions in first-in-first-out order
func (bp *BlockProposer) selectByFIFO(availableTxs []*core.Transaction) ([]*core.Transaction, []*core.Transaction, error) {
	sort.Slice(availableTxs, func(i, j int) bool {
		return availableTxs[i].Timestamp < availableTxs[j].Timestamp
	})

	return bp.packTransactions(availableTxs)
}

// selectBalanced uses a balanced approach considering gas price, age, and account distribution
func (bp *BlockProposer) selectBalanced(availableTxs []*core.Transaction) ([]*core.Transaction, []*core.Transaction, error) {
	txsWithPriority := make([]*TransactionWithPriority, 0, len(availableTxs))
	currentTime := time.Now().Unix()

	for _, tx := range availableTxs {
		priority := bp.calculateTransactionPriority(tx, currentTime)

		gasPriceBig := coremath.ParseBigInt(tx.GasPrice)
		minGasPriceBig := coremath.ParseBigInt(bp.minGasPrice)

		gasRatio := 0.0
		if minGasPriceBig.Sign() > 0 {
			gpF := new(big.Float).SetInt(gasPriceBig)
			minF := new(big.Float).SetInt(minGasPriceBig)
			res := new(big.Float).Quo(gpF, minF)
			gasRatio, _ = res.Float64()
		}

		txWithPriority := &TransactionWithPriority{
			Transaction: tx,
			Priority:    priority,
			GasRatio:    gasRatio,
			FeePerGas:   coremath.BigIntToString(coremath.ParseBigInt(tx.GasPrice)),
			Age:         time.Duration(currentTime-tx.Timestamp) * time.Second,
		}
		txsWithPriority = append(txsWithPriority, txWithPriority)
	}

	sort.Slice(txsWithPriority, func(i, j int) bool {
		return txsWithPriority[i].Priority > txsWithPriority[j].Priority
	})

	sortedTxs := make([]*core.Transaction, len(txsWithPriority))
	for i, txWithPriority := range txsWithPriority {
		sortedTxs[i] = txWithPriority.Transaction
	}

	return bp.packTransactions(sortedTxs)
}

// selectOptimalPacking uses knapsack-like optimization for maximum value
func (bp *BlockProposer) selectOptimalPacking(availableTxs []*core.Transaction) ([]*core.Transaction, []*core.Transaction, error) {
	return bp.knapsackTransactionSelection(availableTxs)
}

// calculateTransactionPriority calculates priority score for balanced selection
func (bp *BlockProposer) calculateTransactionPriority(tx *core.Transaction, currentTime int64) float64 {
	gasPriceBig := coremath.ParseBigInt(tx.GasPrice)
	minGasPriceBig := coremath.ParseBigInt(bp.minGasPrice)

	gasPriorityScore := 0.0
	if minGasPriceBig.Sign() > 0 {
		gpF := new(big.Float).SetInt(gasPriceBig)
		minF := new(big.Float).SetInt(minGasPriceBig)
		res := new(big.Float).Quo(gpF, minF)
		gasPriorityScore, _ = res.Float64()
	}

	age := currentTime - tx.Timestamp
	ageBonusScore := float64(age) / 3600.0

	typeBonusScore := bp.getTransactionTypeBonus(tx)
	diversityBonusScore := bp.getAccountDiversityBonus(tx.From)

	priority := (gasPriorityScore * 0.4) +
		(ageBonusScore * 0.2) +
		(typeBonusScore * 0.2) +
		(diversityBonusScore * 0.2)

	return priority
}

// getTransactionTypeBonus returns bonus based on transaction type
func (bp *BlockProposer) getTransactionTypeBonus(tx *core.Transaction) float64 {
	switch tx.Type {
	case core.TransactionType_STAKE:
		return 1.2
	case core.TransactionType_UNSTAKE:
		return 1.1
	case core.TransactionType_DELEGATE:
		return 1.1
	case core.TransactionType_TRANSFER:
		return 1.0
	default:
		return 1.0
	}
}

// getAccountDiversityBonus returns bonus for account diversity
func (bp *BlockProposer) getAccountDiversityBonus(fromAddress string) float64 {
	pendingFromAccount := 0
	pendingTxs := bp.worldState.GetPendingTransactions()

	for _, tx := range pendingTxs {
		if tx.From == fromAddress {
			pendingFromAccount++
		}
	}

	if pendingFromAccount > 10 {
		return 0.5
	} else if pendingFromAccount > 5 {
		return 0.8
	} else {
		return 1.0
	}
}

// packTransactions packs transactions into a block respecting gas and count limits
func (bp *BlockProposer) packTransactions(sortedTxs []*core.Transaction) ([]*core.Transaction, []*core.Transaction, error) {
	var selectedTxs []*core.Transaction
	var excludedTxs []*core.Transaction

	totalGasUsed := int64(0)
	accountNonces := make(map[string]uint64)

	for _, tx := range sortedTxs {
		if _, exists := accountNonces[tx.From]; !exists {
			nonce, err := bp.worldState.GetNonce(tx.From)
			if err != nil {
				excludedTxs = append(excludedTxs, tx)
				continue
			}
			accountNonces[tx.From] = nonce
		}
	}

	minGasPriceBig := coremath.ParseBigInt(bp.minGasPrice)

	for _, tx := range sortedTxs {
		if len(selectedTxs) >= bp.maxTransactions {
			excludedTxs = append(excludedTxs, tx)
			continue
		}

		// ✅ SECURITY FIX: Safe gas calculation with overflow check
		newTotal, err := math.SafeAdd(totalGasUsed, tx.Gas)
		if err != nil {
			// Gas overflow - reject transaction
			log.Printf("Warning: transaction from %s would cause gas overflow, excluding", tx.From)
			excludedTxs = append(excludedTxs, tx)
			continue
		}

		if newTotal > bp.maxBlockSize {
			excludedTxs = append(excludedTxs, tx)
			continue
		}

		expectedNonce := accountNonces[tx.From]
		if tx.Nonce != expectedNonce {
			excludedTxs = append(excludedTxs, tx)
			continue
		}

		txGasPriceBig := coremath.ParseBigInt(tx.GasPrice)
		if txGasPriceBig.Cmp(minGasPriceBig) < 0 {
			excludedTxs = append(excludedTxs, tx)
			continue
		}

		if err := bp.worldState.ValidateTransactionExecution(tx); err != nil {
			excludedTxs = append(excludedTxs, tx)
			continue
		}

		selectedTxs = append(selectedTxs, tx)
		totalGasUsed = newTotal // ✅ Use safely calculated value
		accountNonces[tx.From]++
	}

	return selectedTxs, excludedTxs, nil
}

// knapsackTransactionSelection uses dynamic programming for optimal selection
func (bp *BlockProposer) knapsackTransactionSelection(availableTxs []*core.Transaction) ([]*core.Transaction, []*core.Transaction, error) {
	n := len(availableTxs)
	if n == 0 {
		return []*core.Transaction{}, []*core.Transaction{}, nil
	}

	type txValue struct {
		tx    *core.Transaction
		ratio float64
		index int
	}

	txValues := make([]txValue, n)
	for i, tx := range availableTxs {
		gasPriceBig := coremath.ParseBigInt(tx.GasPrice)
		gasBig := big.NewInt(tx.Gas)

		totalFeeBig := new(big.Int).Mul(gasPriceBig, gasBig)
		totalFeeF := new(big.Float).SetInt(totalFeeBig)

		weightF := new(big.Float).SetInt64(tx.Gas)

		ratioF := new(big.Float).Quo(totalFeeF, weightF)
		ratio, _ := ratioF.Float64()

		txValues[i] = txValue{
			tx:    tx,
			ratio: ratio,
			index: i,
		}
	}

	sort.Slice(txValues, func(i, j int) bool {
		return txValues[i].ratio > txValues[j].ratio
	})

	sortedTxs := make([]*core.Transaction, n)
	for i, tv := range txValues {
		sortedTxs[i] = tv.tx
	}

	return bp.packTransactions(sortedTxs)
}

// constructBlock creates a block with the selected transactions
func (bp *BlockProposer) constructBlock(transactions []*core.Transaction, slot uint64, epoch uint64) (*core.Block, error) {
	currentBlock := bp.worldState.GetCurrentBlock()
	var prevHash string
	var blockIndex int64
	var prevTimestamp int64

	if currentBlock != nil {
		prevHash = currentBlock.Hash
		blockIndex = currentBlock.Header.Index + 1
		prevTimestamp = currentBlock.Header.Timestamp
	} else {
		prevHash = ""
		blockIndex = 0
		prevTimestamp = 0
	}

	totalGasUsed, err := bp.calculateTotalGas(transactions)
	if err != nil {
		return nil, err
	}

	totalFees := bp.calculateTotalFees(transactions)
	merkleRoot := bp.calculateMerkleRoot(transactions)

	// 1. Get current system time
	now := time.Now().Unix()

	// 2. FORCE INCREMENT: If the clock is at or behind the parent, push it forward.
	if now <= prevTimestamp {
		now = prevTimestamp + 1
	}

	// ✅ DEBUG LOG: This will prove the logic is working in your terminal
	if now == prevTimestamp+1 && time.Now().Unix() <= prevTimestamp {
		log.Printf("⏰ TIMESTAMP GUARD TRIGGERED: Parent=%d, New=%d", prevTimestamp, now)
	}

	header := &core.BlockHeader{
		Index:      blockIndex,
		Timestamp:  now,
		PrevHash:   prevHash,
		Validator:  bp.nodeAddress,
		TxRoot:     merkleRoot,
		StateRoot:  bp.worldState.GetStateRoot(),
		GasUsed:    totalGasUsed,
		GasLimit:   bp.maxBlockSize,
		Slot:       slot,
		Epoch:      epoch,
		TotalFees:  coremath.ParseBigInt(totalFees).Bytes(),
		MerkleRoot: merkleRoot,
	}

	block := &core.Block{
		Header:       header,
		Transactions: transactions,
	}

	block.Hash = bp.calculateBlockHash(block)
	return block, nil
}

// createEmptyBlock creates an empty block when no transactions are available
func (bp *BlockProposer) createEmptyBlock(slot uint64, epoch uint64, startTime time.Time) (*BlockConstructionResult, error) {
	block, err := bp.constructBlock([]*core.Transaction{}, slot, epoch)
	if err != nil {
		return nil, err
	}

	return &BlockConstructionResult{
		Block:             block,
		IncludedTxs:       []*core.Transaction{},
		ExcludedTxs:       []*core.Transaction{},
		TotalGasUsed:      0,
		TotalFees:         "0",
		ConstructionTime:  time.Since(startTime),
		TransactionCount:  0,
		BlockSize:         bp.estimateBlockSize([]*core.Transaction{}),
		OptimizationScore: 1.0,
	}, nil
}

// calculateTotalGas calculates total gas used by transactions
func (bp *BlockProposer) calculateTotalGas(transactions []*core.Transaction) (int64, error) {
	totalGasUsed := int64(0)
	for i, tx := range transactions {
		newTotal, err := math.SafeAdd(totalGasUsed, tx.Gas)
		if err != nil {
			return 0, fmt.Errorf("gas overflow at transaction %d: %v", i, err)
		}
		totalGasUsed = newTotal
	}
	return totalGasUsed, nil
}

// calculateTotalFees calculates total fees from transactions (GasPrice * Gas)
func (bp *BlockProposer) calculateTotalFees(transactions []*core.Transaction) string {
	total := big.NewInt(0)
	for _, tx := range transactions {
		gasPriceBig := coremath.ParseBigInt(tx.GasPrice)
		gasBig := big.NewInt(tx.Gas)

		fee := new(big.Int).Mul(gasPriceBig, gasBig)
		total.Add(total, fee)
	}
	return total.String()
}

// estimateBlockSize estimates the serialized size of a block
func (bp *BlockProposer) estimateBlockSize(transactions []*core.Transaction) int {
	baseSize := 200
	txSize := len(transactions) * 300
	return baseSize + txSize
}

// calculateOptimizationScore calculates how well the block was optimized
func (bp *BlockProposer) calculateOptimizationScore(transactions []*core.Transaction, constructionTime time.Duration) float64 {
	if len(transactions) == 0 {
		return 1.0
	}

	// Get total gas with error handling
	totalGas, err := bp.calculateTotalGas(transactions)
	if err != nil {
		log.Printf("Error calculating total gas: %v", err)
		return 0.0
	}

	gasUtilization := float64(totalGas) / float64(bp.maxBlockSize)
	txUtilization := float64(len(transactions)) / float64(bp.maxTransactions)

	timeScore := 1.0
	if constructionTime > 100*time.Millisecond {
		timeScore = float64(100*time.Millisecond) / float64(constructionTime)
	}

	score := (gasUtilization * 0.4) + (txUtilization * 0.4) + (timeScore * 0.2)

	if score > 1.0 {
		score = 1.0
	}
	return score
}

func (bp *BlockProposer) calculateMerkleRoot(transactions []*core.Transaction) string {
	if len(transactions) == 0 {
		return ""
	}

	// Create hash array for proper Merkle tree
	hashes := make([]hash.Hash, len(transactions))
	for i, tx := range transactions {
		hashes[i] = hash.NewHash([]byte(tx.Hash))
	}

	// Use proper Merkle root calculation
	root := hash.MerkleRoot(hashes)
	return root.String()
}

// calculateBlockHash calculates the hash of a block
func (bp *BlockProposer) calculateBlockHash(block *core.Block) string {
	hash, err := block2.CanonicalBlockHash(block)
	if err != nil {
		panic(fmt.Sprintf("calculateBlockHash: %v", err))
	}
	return hash
}

// updateMetrics updates proposer performance metrics
func (bp *BlockProposer) updateMetrics(constructionTime time.Duration, txCount int, totalFees string) {
	bp.blocksProposed++

	currentFees := coremath.ParseBigInt(bp.totalFeesCollected)
	newFees := coremath.ParseBigInt(totalFees)
	bp.totalFeesCollected = new(big.Int).Add(currentFees, newFees).String()

	if bp.blocksProposed == 1 {
		bp.avgBlockTime = constructionTime
		bp.avgTransactionCount = txCount
	} else {
		alpha := 0.1
		bp.avgBlockTime = time.Duration(float64(bp.avgBlockTime)*(1-alpha) + float64(constructionTime)*alpha)
		bp.avgTransactionCount = int(float64(bp.avgTransactionCount)*(1-alpha) + float64(txCount)*alpha)
	}
}

// GetProposerStats returns proposer performance statistics
func (bp *BlockProposer) GetProposerStats() map[string]interface{} {
	return map[string]interface{}{
		"blocks_proposed":       bp.blocksProposed,
		"avg_block_time_ms":     bp.avgBlockTime.Milliseconds(),
		"avg_transaction_count": bp.avgTransactionCount,
		"total_fees_collected":  bp.totalFeesCollected,
		"selection_strategy":    string(bp.selectionStrategy),
		"max_block_size":        bp.maxBlockSize,
		"max_transactions":      bp.maxTransactions,
		"min_gas_price":         bp.minGasPrice,
	}
}

// GetConfig returns the proposer configuration
func (bp *BlockProposer) GetConfig() map[string]interface{} {
	return map[string]interface{}{
		"max_block_size":     bp.maxBlockSize,
		"max_transactions":   bp.maxTransactions,
		"min_gas_price":      bp.minGasPrice,
		"selection_strategy": string(bp.selectionStrategy),
		"node_address":       bp.nodeAddress,
	}
}

// SetMaxBlockSize updates the maximum block size
func (bp *BlockProposer) SetMaxBlockSize(size int64) {
	bp.maxBlockSize = size
}

// SetMaxTransactions updates the maximum transactions per block
func (bp *BlockProposer) SetMaxTransactions(count int) {
	bp.maxTransactions = count
}

// SetMinGasPrice updates the minimum gas price
func (bp *BlockProposer) SetMinGasPrice(price string) {
	bp.minGasPrice = price
}

// ResetMetrics resets the proposer metrics
func (bp *BlockProposer) ResetMetrics() {
	bp.blocksProposed = 0
	bp.avgBlockTime = 0
	bp.avgTransactionCount = 0
	bp.totalFeesCollected = "0"
}

func (bp *BlockProposer) ProposeBlockWithVRF(
	slot uint64,
	epoch uint64,
	vrfOutput []byte,
	vrfProof []byte,
) (*BlockConstructionResult, error) {
	startTime := time.Now()

	// Get available transactions from the pool
	availableTxs := bp.worldState.GetPendingTransactions()
	if len(availableTxs) == 0 {
		return bp.createEmptyBlockWithVRF(slot, epoch, vrfOutput, vrfProof, startTime)
	}

	// Select transactions for the block based on strategy
	selectedTxs, excludedTxs, err := bp.selectTransactions(availableTxs)
	if err != nil {
		return nil, fmt.Errorf("transaction selection failed: %v", err)
	}

	// Construct the block WITH VRF data
	block, err := bp.constructBlockWithVRF(selectedTxs, slot, epoch, vrfOutput, vrfProof)
	if err != nil {
		return nil, fmt.Errorf("block construction failed: %v", err)
	}

	// Calculate metrics
	constructionTime := time.Since(startTime)
	totalGasUsed, err := bp.calculateTotalGas(selectedTxs)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate total gas: %v", err)
	}
	totalFees := bp.calculateTotalFees(selectedTxs)
	optimizationScore := bp.calculateOptimizationScore(selectedTxs, constructionTime)

	// Update proposer metrics
	bp.updateMetrics(constructionTime, len(selectedTxs), totalFees)

	return &BlockConstructionResult{
		Block:             block,
		IncludedTxs:       selectedTxs,
		ExcludedTxs:       excludedTxs,
		TotalGasUsed:      totalGasUsed,
		TotalFees:         totalFees,
		ConstructionTime:  constructionTime,
		TransactionCount:  len(selectedTxs),
		BlockSize:         bp.estimateBlockSize(selectedTxs),
		OptimizationScore: optimizationScore,
	}, nil
}

// constructBlockWithVRF creates a block with the selected transactions and VRF data
func (bp *BlockProposer) constructBlockWithVRF(
	transactions []*core.Transaction,
	slot uint64,
	epoch uint64,
	vrfOutput []byte,
	vrfProof []byte,
) (*core.Block, error) {
	currentBlock := bp.worldState.GetCurrentBlock()
	var prevHash string
	var blockIndex int64

	if currentBlock != nil {
		prevHash = currentBlock.Hash
		blockIndex = currentBlock.Header.Index + 1
	} else {
		prevHash = ""
		blockIndex = 0
	}

	totalGasUsed, err := bp.calculateTotalGas(transactions)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate total gas: %v", err)
	}
	totalFees := bp.calculateTotalFees(transactions)
	merkleRoot := bp.calculateMerkleRoot(transactions)

	// ✅ Create header with VRF data
	header := &core.BlockHeader{
		Index:      blockIndex,
		Timestamp:  time.Now().Unix(),
		PrevHash:   prevHash,
		Validator:  bp.nodeAddress,
		TxRoot:     merkleRoot,
		StateRoot:  bp.worldState.GetStateRoot(),
		GasUsed:    totalGasUsed,
		GasLimit:   bp.maxBlockSize,
		Slot:       slot,
		Epoch:      epoch,
		TotalFees:  coremath.ParseBigInt(totalFees).Bytes(),
		MerkleRoot: merkleRoot,
		VrfOutput:  vrfOutput, // ✅ Add VRF output
		VrfProof:   vrfProof,  // ✅ Add VRF proof
	}

	block := &core.Block{
		Header:       header,
		Transactions: transactions,
	}

	block.Hash = bp.calculateBlockHash(block)

	return block, nil
}

// createEmptyBlockWithVRF creates an empty block with VRF data when no transactions are available
func (bp *BlockProposer) createEmptyBlockWithVRF(
	slot uint64,
	epoch uint64,
	vrfOutput []byte,
	vrfProof []byte,
	startTime time.Time,
) (*BlockConstructionResult, error) {
	block, err := bp.constructBlockWithVRF([]*core.Transaction{}, slot, epoch, vrfOutput, vrfProof)
	if err != nil {
		return nil, err
	}

	return &BlockConstructionResult{
		Block:             block,
		IncludedTxs:       []*core.Transaction{},
		ExcludedTxs:       []*core.Transaction{},
		TotalGasUsed:      0,
		TotalFees:         "0",
		ConstructionTime:  time.Since(startTime),
		TransactionCount:  0,
		BlockSize:         bp.estimateBlockSize([]*core.Transaction{}),
		OptimizationScore: 1.0,
	}, nil
}
