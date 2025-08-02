// consensus/pos/proposer.go

// Block proposer for Proof of Stake consensus
// Features:
// - Optimized block construction with transaction selection
// - Gas optimization and fee calculation
// - Merkle tree construction for transaction integrity
// - Block timing and validation
// - Reward distribution and fee collection
// - Transaction ordering and prioritization

package pos

import (
	"encoding/binary"
	"fmt"
	"sort"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/state"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"golang.org/x/crypto/blake2b"
)

// BlockProposer handles block creation and proposal optimization
type BlockProposer struct {
	config      *config.Config
	worldState  *state.WorldState
	nodeAddress string

	// Block construction optimization
	maxBlockSize    int64
	maxTransactions int
	minGasPrice     int64

	// Performance metrics
	blocksProposed      uint64
	avgBlockTime        time.Duration
	avgTransactionCount int
	totalFeesCollected  int64

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
	TotalFees         int64               `json:"total_fees"`
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
	FeePerGas   int64             `json:"fee_per_gas"`
	Age         time.Duration     `json:"age"`
}

// NewBlockProposer creates a new optimized block proposer
func NewBlockProposer(config *config.Config, worldState *state.WorldState, nodeAddress string) *BlockProposer {
	return &BlockProposer{
		config:            config,
		worldState:        worldState,
		nodeAddress:       nodeAddress,
		maxBlockSize:      config.Consensus.MaxBlockSize,
		maxTransactions:   config.Consensus.MaxTxPerBlock,
		minGasPrice:       config.Consensus.MinGasPrice,
		selectionStrategy: StrategyBalanced, // Default strategy
	}
}

// SetSelectionStrategy sets the transaction selection strategy
func (bp *BlockProposer) SetSelectionStrategy(strategy TransactionSelectionStrategy) {
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
	totalGasUsed := bp.calculateTotalGas(selectedTxs)
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
	// Sort by gas price (descending)
	sort.Slice(availableTxs, func(i, j int) bool {
		return availableTxs[i].GasPrice > availableTxs[j].GasPrice
	})

	return bp.packTransactions(availableTxs)
}

// selectByFIFO selects transactions in first-in-first-out order
func (bp *BlockProposer) selectByFIFO(availableTxs []*core.Transaction) ([]*core.Transaction, []*core.Transaction, error) {
	// Sort by timestamp (ascending)
	sort.Slice(availableTxs, func(i, j int) bool {
		return availableTxs[i].Timestamp < availableTxs[j].Timestamp
	})

	return bp.packTransactions(availableTxs)
}

// selectBalanced uses a balanced approach considering gas price, age, and account distribution
func (bp *BlockProposer) selectBalanced(availableTxs []*core.Transaction) ([]*core.Transaction, []*core.Transaction, error) {
	// Calculate priorities for all transactions
	txsWithPriority := make([]*TransactionWithPriority, 0, len(availableTxs))
	currentTime := time.Now().Unix()

	for _, tx := range availableTxs {
		priority := bp.calculateTransactionPriority(tx, currentTime)
		txWithPriority := &TransactionWithPriority{
			Transaction: tx,
			Priority:    priority,
			GasRatio:    float64(tx.GasPrice) / float64(bp.minGasPrice),
			FeePerGas:   tx.GasPrice,
			Age:         time.Duration(currentTime-tx.Timestamp) * time.Second,
		}
		txsWithPriority = append(txsWithPriority, txWithPriority)
	}

	// Sort by priority (descending)
	sort.Slice(txsWithPriority, func(i, j int) bool {
		return txsWithPriority[i].Priority > txsWithPriority[j].Priority
	})

	// Extract transactions
	sortedTxs := make([]*core.Transaction, len(txsWithPriority))
	for i, txWithPriority := range txsWithPriority {
		sortedTxs[i] = txWithPriority.Transaction
	}

	return bp.packTransactions(sortedTxs)
}

// selectOptimalPacking uses knapsack-like optimization for maximum value
func (bp *BlockProposer) selectOptimalPacking(availableTxs []*core.Transaction) ([]*core.Transaction, []*core.Transaction, error) {
	// Use dynamic programming approach for optimal transaction packing
	return bp.knapsackTransactionSelection(availableTxs)
}

// calculateTransactionPriority calculates priority score for balanced selection
func (bp *BlockProposer) calculateTransactionPriority(tx *core.Transaction, currentTime int64) float64 {
	// Base priority from gas price
	gasPriorityScore := float64(tx.GasPrice) / float64(bp.minGasPrice)

	// Age bonus (older transactions get higher priority)
	age := currentTime - tx.Timestamp
	ageBonusScore := float64(age) / 3600.0 // Hour-based age bonus

	// Transaction type bonus
	typeBonusScore := bp.getTransactionTypeBonus(tx)

	// Account diversity bonus (prevent single account spam)
	diversityBonusScore := bp.getAccountDiversityBonus(tx.From)

	// Combine scores with weights
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
		return 1.2 // Staking transactions get priority
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
	// Check how many transactions from this account are already pending
	pendingFromAccount := 0
	pendingTxs := bp.worldState.GetPendingTransactions()

	for _, tx := range pendingTxs {
		if tx.From == fromAddress {
			pendingFromAccount++
		}
	}

	// Reduce bonus for accounts with many pending transactions
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

	// Initialize account nonces
	for _, tx := range sortedTxs {
		if _, exists := accountNonces[tx.From]; !exists {
			nonce, err := bp.worldState.GetNonce(tx.From)
			if err != nil {
				// Skip transactions from unknown accounts
				excludedTxs = append(excludedTxs, tx)
				continue
			}
			accountNonces[tx.From] = nonce
		}
	}

	for _, tx := range sortedTxs {
		// Check transaction count limit
		if len(selectedTxs) >= bp.maxTransactions {
			excludedTxs = append(excludedTxs, tx)
			continue
		}

		// Check gas limit
		if totalGasUsed+tx.Gas > bp.maxBlockSize {
			excludedTxs = append(excludedTxs, tx)
			continue
		}

		// Check nonce ordering
		expectedNonce := accountNonces[tx.From]
		if tx.Nonce != expectedNonce {
			excludedTxs = append(excludedTxs, tx)
			continue
		}

		// Check minimum gas price
		if tx.GasPrice < bp.minGasPrice {
			excludedTxs = append(excludedTxs, tx)
			continue
		}

		// Validate transaction can be executed
		if err := bp.worldState.ValidateTransactionExecution(tx); err != nil {
			excludedTxs = append(excludedTxs, tx)
			continue
		}

		// Include transaction
		selectedTxs = append(selectedTxs, tx)
		totalGasUsed += tx.Gas
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

	// Simple greedy approximation for now (full DP would be too complex)
	// Calculate value-to-weight ratio for each transaction
	type txValue struct {
		tx    *core.Transaction
		ratio float64
		index int
	}

	txValues := make([]txValue, n)
	for i, tx := range availableTxs {
		value := float64(tx.GasPrice * tx.Gas) // Total fee as value
		weight := float64(tx.Gas)              // Gas as weight
		ratio := value / weight                // Value per unit weight

		txValues[i] = txValue{
			tx:    tx,
			ratio: ratio,
			index: i,
		}
	}

	// Sort by ratio (descending)
	sort.Slice(txValues, func(i, j int) bool {
		return txValues[i].ratio > txValues[j].ratio
	})

	// Pack greedily
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

	if currentBlock != nil {
		prevHash = currentBlock.Hash
		blockIndex = currentBlock.Header.Index + 1
	} else {
		prevHash = ""
		blockIndex = 0
	}

	// Calculate totals
	totalGasUsed := bp.calculateTotalGas(transactions)
	totalFees := bp.calculateTotalFees(transactions)
	merkleRoot := bp.calculateMerkleRoot(transactions)

	// Create block header using updated protobuf fields
	header := &core.BlockHeader{
		Index:     blockIndex,
		Timestamp: time.Now().Unix(),
		PrevHash:  prevHash,
		Validator: bp.nodeAddress,
		TxRoot:    merkleRoot,
		StateRoot: bp.worldState.GetStateRoot(),
		GasUsed:   totalGasUsed,
		GasLimit:  bp.maxBlockSize,
		// Add the new consensus fields if they exist in your protobuf
		Slot:       slot,
		Epoch:      epoch,
		TotalFees:  totalFees,
		MerkleRoot: merkleRoot,
	}

	// Create block
	block := &core.Block{
		Header:       header,
		Transactions: transactions,
	}

	// Calculate block hash
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
		TotalFees:         0,
		ConstructionTime:  time.Since(startTime),
		TransactionCount:  0,
		BlockSize:         bp.estimateBlockSize([]*core.Transaction{}),
		OptimizationScore: 1.0, // Perfect score for empty block
	}, nil
}

// calculateTotalGas calculates total gas used by transactions
func (bp *BlockProposer) calculateTotalGas(transactions []*core.Transaction) int64 {
	total := int64(0)
	for _, tx := range transactions {
		total += tx.Gas
	}
	return total
}

// calculateTotalFees calculates total fees from transactions
func (bp *BlockProposer) calculateTotalFees(transactions []*core.Transaction) int64 {
	total := int64(0)
	for _, tx := range transactions {
		total += tx.GasPrice * tx.Gas
	}
	return total
}

// estimateBlockSize estimates the serialized size of a block
func (bp *BlockProposer) estimateBlockSize(transactions []*core.Transaction) int {
	// Rough estimation: header (200 bytes) + transactions
	baseSize := 200
	txSize := len(transactions) * 300 // Average transaction size estimate
	return baseSize + txSize
}

// calculateOptimizationScore calculates how well the block was optimized
func (bp *BlockProposer) calculateOptimizationScore(transactions []*core.Transaction, constructionTime time.Duration) float64 {
	if len(transactions) == 0 {
		return 1.0
	}

	// Gas utilization score (0-1)
	gasUtilization := float64(bp.calculateTotalGas(transactions)) / float64(bp.maxBlockSize)

	// Transaction count utilization (0-1)
	txUtilization := float64(len(transactions)) / float64(bp.maxTransactions)

	// Construction time score (faster is better)
	timeScore := 1.0
	if constructionTime > 100*time.Millisecond {
		timeScore = float64(100*time.Millisecond) / float64(constructionTime)
	}

	// Combined score
	score := (gasUtilization * 0.4) + (txUtilization * 0.4) + (timeScore * 0.2)

	// Cap at 1.0
	if score > 1.0 {
		score = 1.0
	}

	return score
}

// calculateMerkleRoot calculates the Merkle root of transactions
func (bp *BlockProposer) calculateMerkleRoot(transactions []*core.Transaction) string {
	if len(transactions) == 0 {
		return ""
	}

	// Simple implementation - would use proper Merkle tree in production
	var combined []byte
	for _, tx := range transactions {
		combined = append(combined, []byte(tx.Hash)...)
	}

	hash := blake2b.Sum256(combined)
	return fmt.Sprintf("%x", hash)
}

// calculateBlockHash calculates the hash of a block - made public for use by validator
func (bp *BlockProposer) calculateBlockHash(block *core.Block) string {
	var data []byte

	// Combine header fields for hashing using actual protobuf fields
	indexBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(indexBytes, uint64(block.Header.Index))
	data = append(data, indexBytes...)

	timestampBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(timestampBytes, uint64(block.Header.Timestamp))
	data = append(data, timestampBytes...)

	data = append(data, []byte(block.Header.PrevHash)...)
	data = append(data, []byte(block.Header.TxRoot)...)
	data = append(data, []byte(block.Header.StateRoot)...)
	data = append(data, []byte(block.Header.Validator)...)

	gasUsedBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasUsedBytes, uint64(block.Header.GasUsed))
	data = append(data, gasUsedBytes...)

	gasLimitBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasLimitBytes, uint64(block.Header.GasLimit))
	data = append(data, gasLimitBytes...)

	hash := blake2b.Sum256(data)
	return fmt.Sprintf("%x", hash)
}

// updateMetrics updates proposer performance metrics
func (bp *BlockProposer) updateMetrics(constructionTime time.Duration, txCount int, totalFees int64) {
	bp.blocksProposed++
	bp.totalFeesCollected += totalFees

	// Update moving averages
	if bp.blocksProposed == 1 {
		bp.avgBlockTime = constructionTime
		bp.avgTransactionCount = txCount
	} else {
		// Exponential moving average
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
func (bp *BlockProposer) SetMinGasPrice(price int64) {
	bp.minGasPrice = price
}

// ResetMetrics resets the proposer metrics
func (bp *BlockProposer) ResetMetrics() {
	bp.blocksProposed = 0
	bp.avgBlockTime = 0
	bp.avgTransactionCount = 0
	bp.totalFeesCollected = 0
}
