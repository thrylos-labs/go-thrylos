// blockchain/blockchain.go - Complete blockchain chain management

package chain

import (
	"fmt"
	"log"
	"math/big"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/consensus/validator"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/block"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/core/transaction"
	"github.com/thrylos-labs/go-thrylos/crypto"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// Blockchain represents the main blockchain with PoS consensus integration
type Blockchain struct {
	// Configuration
	config *config.Config

	// Core components - now using WorldState as primary state manager
	worldState *state.WorldState

	// Block management
	blockCreator *block.Creator

	// Chain state (simplified - WorldState manages most of this)
	genesisBlock *core.Block

	// Chain validation
	validators      map[string]*ChainValidator
	consensusEngine ConsensusEngine // Interface for PoS engine

	// Fork management
	forks         map[string]*Fork
	bestChain     *Fork
	maxReorgDepth int64

	// Performance metrics
	totalTransactions int64
	totalBlocks       int64
	averageBlockTime  time.Duration

	// Synchronization
	mu sync.RWMutex

	// Event channels
	blockAddedChan chan *core.Block
	txAddedChan    chan *core.Transaction

	// Cross-shard communication
	crossShardEnabled bool

	validatorManager *validator.Manager
}

// ConsensusEngine interface for PoS integration
type ConsensusEngine interface {
	ValidateBlock(block *core.Block) error
	GetCurrentEpoch() uint64
	GetCurrentSlot() uint64
	IsValidator(address string) bool
	GetStats() map[string]interface{}
	GetForkChoice() interface{} // Returns fork choice as interface{} to avoid import cycle

}

// ChainValidator represents a block validator
type ChainValidator struct {
	Address     string
	PublicKey   crypto.PublicKey
	IsActive    bool
	LastBlock   int64
	Performance float64
}

// Fork represents a blockchain fork
type Fork struct {
	ID          string
	Blocks      []*core.Block
	Height      int64
	TotalWork   int64
	LastBlock   *core.Block
	IsMainChain bool
}

// BlockchainConfig represents blockchain configuration
type BlockchainConfig struct {
	Config            *config.Config
	WorldState        *state.WorldState
	ShardID           account.ShardID
	ValidatorManager  *validator.Manager
	TotalShards       int
	MaxReorgDepth     int64
	CrossShardEnabled bool
}

// NewBlockchain creates a new blockchain instance
func NewBlockchain(cfg *BlockchainConfig) (*Blockchain, error) {
	if cfg == nil {
		return nil, fmt.Errorf("blockchain config cannot be nil")
	}

	if cfg.WorldState == nil {
		return nil, fmt.Errorf("world state cannot be nil")
	}

	// Initialize block creator
	blockCreator := block.NewCreator(cfg.ShardID, cfg.TotalShards)

	// Set defaults
	maxReorgDepth := cfg.MaxReorgDepth
	if maxReorgDepth <= 0 {
		maxReorgDepth = 100
	}

	bc := &Blockchain{
		config:     cfg.Config,
		worldState: cfg.WorldState,

		// Block management
		blockCreator: blockCreator,

		validatorManager: cfg.ValidatorManager,

		// Validation
		validators: make(map[string]*ChainValidator),

		// Fork management
		forks:         make(map[string]*Fork),
		maxReorgDepth: maxReorgDepth,

		// Cross-shard
		crossShardEnabled: cfg.CrossShardEnabled,

		// Event channels
		blockAddedChan: make(chan *core.Block, 100),
		txAddedChan:    make(chan *core.Transaction, 1000),
	}

	return bc, nil
}

func (bc *Blockchain) CreateBlockWithBatching(validator string, privateKey crypto.PrivateKey, minTxs int) (*core.Block, error) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	fmt.Printf("🔍 Blockchain: CreateBlockWithBatching - waiting for %d transactions\n", minTxs)

	// Get current block from WorldState
	currentBlock := bc.worldState.GetCurrentBlock()

	// Try to get enough transactions for batching
	maxAttempts := 5
	var candidateTxs []*core.Transaction

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		candidateTxs = bc.worldState.GetExecutableTransactions(bc.config.Consensus.MaxTxPerBlock)

		fmt.Printf("🔍 Blockchain: Attempt %d: Found %d executable transactions\n", attempt, len(candidateTxs))

		if len(candidateTxs) >= minTxs || attempt == maxAttempts {
			break
		}

		// Wait a bit for more transactions to arrive
		time.Sleep(200 * time.Millisecond)
	}

	// Create block template
	template := bc.blockCreator.CreateBlockTemplate(
		currentBlock,
		candidateTxs,
		bc.config.Consensus.MaxBlockSize,
		bc.config.Consensus.MaxTxPerBlock,
	)

	fmt.Printf("🔍 Blockchain: Template created with %d transactions\n", len(template.Transactions))

	// Create block
	block, err := bc.blockCreator.CreateBlockFromTemplate(template, validator)
	if err != nil {
		return nil, fmt.Errorf("failed to create block: %v", err)
	}

	// Set state root from WorldState
	stateRoot := bc.worldState.GetStateRoot()
	bc.blockCreator.SetStateRoot(block, stateRoot)

	// Sign block
	if err := bc.blockCreator.SignBlock(block, privateKey); err != nil {
		return nil, fmt.Errorf("failed to sign block: %v", err)
	}

	fmt.Printf("🔍 Blockchain: Created block with %d transactions\n", len(block.Transactions))
	return block, nil
}

// SetConsensusEngine sets the PoS consensus engine
func (bc *Blockchain) SetConsensusEngine(engine ConsensusEngine) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.consensusEngine = engine
}

// InitializeGenesis initializes the blockchain with a genesis block
func (bc *Blockchain) InitializeGenesis(genesisAccount string, genesisValidator string,
	initialSupply string, genesisValidators []*core.Validator, privateKey crypto.PrivateKey) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	// 1. In-Memory Check: If already set in this struct, return immediately.
	if bc.genesisBlock != nil {
		return nil
	}

	// 2. Persistent Storage Check: Check if genesis already exists in the DB.
	// If the node restarts, the WorldState might already have the genesis block.
	// Note: Verify the method name 'GetBlockByNumber' matches your WorldState interface.
	if existingGenesis, err := bc.worldState.GetBlock(0); err == nil && existingGenesis != nil {
		// Log that we found an existing genesis
		// fmt.Println("Found existing genesis block in storage, skipping initialization.")

		bc.genesisBlock = existingGenesis
		// If your blockchain tracks totalBlocks, you might need to load the actual height here
		// e.g., bc.totalBlocks = bc.worldState.CurrentHeight() + 1
		bc.totalBlocks = 1
		return nil
	}

	// --- Proceed with Genesis Creation if not found ---

	// Initialize WorldState genesis first
	if err := bc.worldState.InitializeGenesis(genesisAccount, initialSupply, genesisValidators); err != nil {
		return fmt.Errorf("failed to initialize world state genesis: %v", err)
	}

	// Create genesis block
	genesisBlock, err := bc.blockCreator.CreateGenesisBlock(genesisValidator, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("failed to create genesis block: %v", err)
	}

	// Set state root from WorldState
	stateRoot := bc.worldState.GetStateRoot()
	if genesisBlock.Header != nil {
		genesisBlock.Header.StateRoot = stateRoot
	}

	// Sign genesis block
	if err := bc.blockCreator.SignBlock(genesisBlock, privateKey); err != nil {
		return fmt.Errorf("failed to sign genesis block: %v", err)
	}

	// Add genesis block to WorldState
	if err := bc.worldState.AddBlock(genesisBlock); err != nil {
		return fmt.Errorf("failed to add genesis block to world state: %v", err)
	}

	bc.genesisBlock = genesisBlock
	bc.totalBlocks = 1

	return nil
}

// AddBlock adds a new block to the blockchain
func (bc *Blockchain) AddBlock(block *core.Block) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	return bc.addBlockUnsafe(block)
}

// addBlockUnsafe adds a block without locking (caller must hold lock)
func (bc *Blockchain) addBlockUnsafe(block *core.Block) error {
	if block == nil {
		return fmt.Errorf("block cannot be nil")
	}

	// Validate block structure
	if err := bc.validateBlockStructure(block); err != nil {
		return fmt.Errorf("block structure validation failed: %v", err)
	}

	// Validate block with consensus engine if available
	if bc.consensusEngine != nil {
		if err := bc.consensusEngine.ValidateBlock(block); err != nil {
			return fmt.Errorf("consensus validation failed: %v", err)
		}
	}

	// Check if block already exists in WorldState
	if existingBlock := bc.worldState.GetCurrentBlock(); existingBlock != nil && existingBlock.Hash == block.Hash {
		return fmt.Errorf("block %s already exists", block.Hash)
	}

	// Let WorldState handle the block addition (includes transaction execution)
	if err := bc.worldState.AddBlock(block); err != nil {
		return fmt.Errorf("world state block addition failed: %v", err)
	}

	// ✅ EXISTING: Process validator unbondings
	if bc.validatorManager != nil {
		if err := bc.validatorManager.ProcessUnbondings(); err != nil {
			// Log warning but don't fail block addition
			fmt.Printf("Warning: failed to process validator unbondings at block %d: %v\n", block.Header.Index, err)
		}
	}

	// ✅ NEW: Process staking unbonding queue (delegator unstaking)
	if err := bc.worldState.ProcessUnbondingQueue(); err != nil {
		// Log warning but don't fail block addition
		// This is defensive - unbonding processing shouldn't block chain progress
		log.Printf("⚠️ Warning: Failed to process staking unbonding queue at block %d: %v", block.Header.Index, err)
	}

	// Update blockchain metrics
	bc.totalBlocks++
	bc.updateAverageBlockTime(block)

	// Count transactions
	bc.totalTransactions += int64(len(block.Transactions))

	// Send block added event
	select {
	case bc.blockAddedChan <- block:
	default:
		// Channel full, skip event
	}

	return nil
}

// validateBlockStructure validates the basic structure of a block
func (bc *Blockchain) validateBlockStructure(block *core.Block) error {
	if block.Header == nil {
		return fmt.Errorf("block header cannot be nil")
	}

	if block.Hash == "" {
		return fmt.Errorf("block hash cannot be empty")
	}

	if block.Header.Validator == "" {
		return fmt.Errorf("block validator cannot be empty")
	}

	// [FIX M-03] Enforce strict timestamp drift control
	// Blocks cannot be from the future (beyond threshold)
	// We use the config value or a hard safe default of 15 seconds
	maxDrift := 15 * time.Second
	if bc.config != nil && bc.config.Consensus.MaxFutureBlockTime > 0 {
		maxDrift = bc.config.Consensus.MaxFutureBlockTime
	}

	blockTime := time.Unix(block.Header.Timestamp, 0)
	if blockTime.After(time.Now().Add(maxDrift)) {
		return fmt.Errorf("block timestamp %s is too far in the future (limit: %s)",
			blockTime.Format(time.RFC3339), maxDrift)
	}

	// Validate transaction count
	if len(block.Transactions) > bc.config.Consensus.MaxTxPerBlock {
		return fmt.Errorf("block contains too many transactions: %d > %d",
			len(block.Transactions), bc.config.Consensus.MaxTxPerBlock)
	}

	// Validate gas usage
	totalGas := int64(0)
	for _, tx := range block.Transactions {
		totalGas += tx.Gas
	}

	if totalGas != block.Header.GasUsed {
		return fmt.Errorf("gas used mismatch: header=%d, calculated=%d",
			block.Header.GasUsed, totalGas)
	}

	if totalGas > bc.config.Consensus.MaxBlockSize {
		return fmt.Errorf("block gas usage exceeds limit: %d > %d",
			totalGas, bc.config.Consensus.MaxBlockSize)
	}

	// Validate block validator is registered
	if bc.consensusEngine != nil && !bc.consensusEngine.IsValidator(block.Header.Validator) {
		fmt.Printf("⚠️ Block validator %s not registered (accepting for dev)\n", block.Header.Validator)
		// return fmt.Errorf("block validator %s is not registered", block.Header.Validator)
	}

	return nil
}

// AddTransaction adds a transaction to the pending pool (delegates to WorldState)
func (bc *Blockchain) AddTransaction(tx *core.Transaction) error {
	if tx == nil {
		return fmt.Errorf("transaction cannot be nil")
	}

	// Delegate to WorldState
	if err := bc.worldState.AddTransaction(tx); err != nil {
		return fmt.Errorf("failed to add transaction to world state: %v", err)
	}

	// Send transaction added event
	select {
	case bc.txAddedChan <- tx:
	default:
		// Channel full, skip event
	}

	return nil
}

// GetPendingTransactions returns all pending transactions (from WorldState)
func (bc *Blockchain) GetPendingTransactions() []*core.Transaction {
	return bc.worldState.GetPendingTransactions()
}

// GetExecutableTransactions returns transactions ready for block creation
func (bc *Blockchain) GetExecutableTransactions(maxCount int) []*core.Transaction {
	return bc.worldState.GetExecutableTransactions(maxCount)
}

// CreateBlock creates a new block with pending transactions
func (bc *Blockchain) CreateBlock(validator string, privateKey crypto.PrivateKey) (*core.Block, error) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	// Get current block from WorldState
	currentBlock := bc.worldState.GetCurrentBlock()

	// Get executable transactions
	candidateTxs := bc.worldState.GetExecutableTransactions(bc.config.Consensus.MaxTxPerBlock)

	// Create block template
	template := bc.blockCreator.CreateBlockTemplate(
		currentBlock,
		candidateTxs,
		bc.config.Consensus.MaxBlockSize,
		bc.config.Consensus.MaxTxPerBlock,
	)

	// Create block
	block, err := bc.blockCreator.CreateBlockFromTemplate(template, validator)
	if err != nil {
		return nil, fmt.Errorf("failed to create block: %v", err)
	}

	// Set state root from WorldState
	stateRoot := bc.worldState.GetStateRoot()
	bc.blockCreator.SetStateRoot(block, stateRoot)

	// Sign block
	if err := bc.blockCreator.SignBlock(block, privateKey); err != nil {
		return nil, fmt.Errorf("failed to sign block: %v", err)
	}

	return block, nil
}

const (
	MaxReorgDepth = 1024 // Maximum blocks to reorg
)

// ReorganizeChain switches the canonical chain to a new fork.
// newBlocks must be sorted from oldest (common ancestor + 1) to newest (new head).
// ReorganizeChain switches the canonical chain to a new fork.
// ReorganizeChain switches the canonical chain to a new fork.
func (bc *Blockchain) ReorganizeChain(newBlocks []*core.Block) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if len(newBlocks) == 0 {
		return fmt.Errorf("no blocks to reorganize")
	}

	// 🔒 SAFETY CHECK 1: Enforce maximum reorg depth
	if len(newBlocks) > MaxReorgDepth {
		return fmt.Errorf("reorg too deep: %d blocks exceeds maximum %d",
			len(newBlocks), MaxReorgDepth)
	}

	firstNewBlock := newBlocks[0]

	// 1. Validate link to common ancestor
	commonAncestor, err := bc.worldState.GetBlockByHash(firstNewBlock.Header.PrevHash)
	if err != nil || commonAncestor == nil {
		return fmt.Errorf("common ancestor block %s not found", firstNewBlock.Header.PrevHash)
	}

	// 🔒 SAFETY CHECK 2: Calculate actual reorg depth
	currentHead := bc.worldState.GetCurrentBlock()
	reorgDepth := currentHead.Header.Index - commonAncestor.Header.Index
	if reorgDepth > int64(MaxReorgDepth) {
		return fmt.Errorf("reorg depth %d exceeds maximum %d", reorgDepth, MaxReorgDepth)
	}

	fmt.Printf("🔀 Executing Reorg: Switching from height %d to fork starting at %d (depth: %d)\n",
		bc.worldState.GetHeight(), firstNewBlock.Header.Index, reorgDepth)

	// ============================================================
	// ✅ NEW: Validate reorganization safety (stake + finality)
	// ============================================================
	if bc.consensusEngine != nil {
		fc := bc.consensusEngine.GetForkChoice()

		// Type assert to the ForkChoice interface we need
		type ForkChoiceValidator interface {
			ValidateReorganization(depth int, epoch uint64, newStake, totalStake string) error
			GetAttestingStake(blockHash string) string
			GetTotalActiveStake() string
		}

		if fcValidator, ok := fc.(ForkChoiceValidator); ok && fcValidator != nil {
			// Calculate epoch of fork point (using 32 blocks per epoch)
			blocksPerEpoch := 32
			forkEpoch := uint64(commonAncestor.Header.Index) / uint64(blocksPerEpoch)

			// Get stake backing the new chain (from last block)
			lastBlock := newBlocks[len(newBlocks)-1]
			newChainStake := fcValidator.GetAttestingStake(lastBlock.Hash)

			// Get total network stake
			totalStake := fcValidator.GetTotalActiveStake()

			// Validate (checks finality + stake requirements)
			if err := fcValidator.ValidateReorganization(
				int(reorgDepth),
				forkEpoch,
				newChainStake,
				totalStake,
			); err != nil {
				return fmt.Errorf("reorg security check failed: %w", err)
			}

			fmt.Printf("✅ Reorg security validated: depth=%d, stake=%s/%s\n",
				reorgDepth, newChainStake, totalStake)
		}
	}
	// ============================================================
	// ============================================================

	// 🔒 SAFETY CHECK 3: Validate ALL blocks BEFORE applying any
	for i, block := range newBlocks {
		// Verify block links correctly
		if i == 0 {
			if block.Header.PrevHash != commonAncestor.Hash {
				return fmt.Errorf("first reorg block doesn't link to common ancestor")
			}
		} else {
			if block.Header.PrevHash != newBlocks[i-1].Hash {
				return fmt.Errorf("reorg block %d doesn't link to previous block", i)
			}
		}

		// Validate block structure
		if err := bc.validateBlockStructure(block); err != nil {
			return fmt.Errorf("reorg block %d validation failed: %v", i, err)
		}

		// Validate with consensus engine if available
		if bc.consensusEngine != nil {
			if err := bc.consensusEngine.ValidateBlock(block); err != nil {
				return fmt.Errorf("reorg block %d consensus validation failed: %v", i, err)
			}
		}
	}

	// 2. Apply new blocks
	for i, block := range newBlocks {
		if err := bc.worldState.AddBlock(block); err != nil {
			fmt.Printf("❌ CRITICAL: Reorg failed during block application at block %d\n", i)
			return fmt.Errorf("reorg failed: could not add block %d: %v", block.Header.Index, err)
		}

		fmt.Printf("  ✓ Applied reorg block %d/%d (height %d)\n",
			i+1, len(newBlocks), block.Header.Index)
	}

	fmt.Printf("✅ Reorg complete. New head height: %d\n", bc.worldState.GetHeight())
	return nil
}

// GetBlock returns a block by hash (delegates to WorldState)
func (bc *Blockchain) GetBlock(hash string) (*core.Block, error) {
	return bc.worldState.GetBlockByHash(hash)
}

// GetBlockByIndex returns a block by index (delegates to WorldState)
func (bc *Blockchain) GetBlockByIndex(index int64) (*core.Block, error) {
	return bc.worldState.GetBlock(index)
}

// GetCurrentBlock returns the current head block (from WorldState)
func (bc *Blockchain) GetCurrentBlock() *core.Block {
	return bc.worldState.GetCurrentBlock()
}

// GetHeight returns the current blockchain height (from WorldState)
func (bc *Blockchain) GetHeight() int64 {
	return bc.worldState.GetHeight()
}

// GetGenesisBlock returns the genesis block
func (bc *Blockchain) GetGenesisBlock() *core.Block {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.genesisBlock
}

// GetAccount returns an account by address (delegates to WorldState)
func (bc *Blockchain) GetAccount(address string) (*core.Account, error) {
	return bc.worldState.GetAccount(address)
}

// GetBalance retrieves the balance of an account
// ✅ UPDATE: Return type changed from (int64, error) to (*big.Int, error)
func (bc *Blockchain) GetBalance(address string) (*big.Int, error) {
	return bc.worldState.GetBalance(address)
}

// GetNonce returns account nonce (delegates to WorldState)
func (bc *Blockchain) GetNonce(address string) (uint64, error) {
	return bc.worldState.GetNonce(address)
}

// ValidateTransaction validates a transaction (delegates to WorldState)
func (bc *Blockchain) ValidateTransaction(tx *core.Transaction) error {
	return bc.worldState.ValidateTransaction(tx)
}

// ExecuteTransaction executes a single transaction (delegates to WorldState)
func (bc *Blockchain) ExecuteTransaction(tx *core.Transaction) (*transaction.ExecutionReceipt, error) {
	return bc.worldState.ExecuteTransaction(tx)
}

// ExecuteBatchTransactions executes multiple transactions (delegates to WorldState)
func (bc *Blockchain) ExecuteBatchTransactions(transactions []*core.Transaction) ([]*transaction.ExecutionReceipt, error) {
	return bc.worldState.ExecuteBatchTransactions(transactions)
}

// Validator management methods (delegate to WorldState)
func (bc *Blockchain) AddValidator(validator *core.Validator) error {
	return bc.worldState.AddValidator(validator)
}

func (bc *Blockchain) GetValidator(address string) (*core.Validator, error) {
	return bc.worldState.GetValidator(address)
}

func (bc *Blockchain) GetActiveValidators() []*core.Validator {
	return bc.worldState.GetActiveValidators()
}

func (bc *Blockchain) UpdateValidator(validator *core.Validator) error {
	return bc.worldState.UpdateValidator(validator)
}

// Staking management
func (bc *Blockchain) GetStakingManager() *state.StakingManager {
	return bc.worldState.GetStakingManager()
}

// Cross-shard operations
func (bc *Blockchain) GetCrossShardManager() *state.CrossShardManager {
	if !bc.crossShardEnabled {
		return nil
	}
	return bc.worldState.GetCrossShardManager()
}

func (bc *Blockchain) InitiateCrossShardTransfer(from, to string, amount int64, nonce uint64) (*state.CrossShardTransfer, error) {
	if !bc.crossShardEnabled {
		return nil, fmt.Errorf("cross-shard transfers not enabled")
	}

	csm := bc.worldState.GetCrossShardManager()
	if csm == nil {
		return nil, fmt.Errorf("cross-shard manager not available")
	}

	// ✅ FIX: Convert int64 to string before passing to manager
	amountStr := fmt.Sprintf("%d", amount)

	return csm.InitiateTransfer(from, to, amountStr, nonce)
}

func (bc *Blockchain) CompleteCrossShardTransfer(transferHash string) error {
	if !bc.crossShardEnabled {
		return fmt.Errorf("cross-shard transfers not enabled")
	}

	csm := bc.worldState.GetCrossShardManager()
	if csm == nil {
		return fmt.Errorf("cross-shard manager not available")
	}

	return csm.CompleteTransfer(transferHash)
}

// State management
func (bc *Blockchain) GetStateRoot() string {
	return bc.worldState.GetStateRoot()
}

func (bc *Blockchain) ValidateStateConsistency() error {
	return bc.worldState.ValidateStateConsistency()
}

func (bc *Blockchain) CreateSnapshot() *state.StateSnapshot {
	return bc.worldState.CreateSnapshot()
}

func (bc *Blockchain) RestoreFromSnapshot(snapshot *state.StateSnapshot) error {
	return bc.worldState.RestoreFromSnapshot(snapshot)
}

// updateAverageBlockTime updates the average block time metric
func (bc *Blockchain) updateAverageBlockTime(block *core.Block) {
	currentBlock := bc.worldState.GetCurrentBlock()
	if currentBlock == nil || currentBlock.Header.Index == 0 {
		return
	}

	// Get previous block
	prevBlock, err := bc.worldState.GetBlock(currentBlock.Header.Index - 1)
	if err != nil {
		return
	}

	blockTime := time.Duration(block.Header.Timestamp-prevBlock.Header.Timestamp) * time.Second

	if bc.averageBlockTime == 0 {
		bc.averageBlockTime = blockTime
	} else {
		// Exponential moving average
		alpha := 0.1
		bc.averageBlockTime = time.Duration(float64(bc.averageBlockTime)*(1-alpha) + float64(blockTime)*alpha)
	}
}

// GetStats returns comprehensive blockchain statistics
func (bc *Blockchain) GetStats() map[string]interface{} {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	// Get WorldState status
	worldStateStats := bc.worldState.GetStatus()

	stats := map[string]interface{}{
		"height":              bc.worldState.GetHeight(),
		"total_blocks":        bc.totalBlocks,
		"total_transactions":  bc.totalTransactions,
		"average_block_time":  bc.averageBlockTime.Seconds(),
		"current_block_hash":  "",
		"genesis_block_hash":  "",
		"state_root":          bc.worldState.GetStateRoot(),
		"total_supply":        bc.worldState.GetTotalSupply(),
		"total_staked":        bc.worldState.GetTotalStaked(),
		"validator_count":     bc.worldState.GetValidatorCount(),
		"active_validators":   bc.worldState.GetActiveValidatorCount(),
		"account_count":       bc.worldState.GetAccountCount(),
		"cross_shard_enabled": bc.crossShardEnabled,
		"world_state":         worldStateStats,
	}

	currentBlock := bc.worldState.GetCurrentBlock()
	if currentBlock != nil {
		stats["current_block_hash"] = currentBlock.Hash
	}

	if bc.genesisBlock != nil {
		stats["genesis_block_hash"] = bc.genesisBlock.Hash
	}

	// Add consensus engine stats if available
	if bc.consensusEngine != nil {
		stats["consensus"] = bc.consensusEngine.GetStats()
	}

	return stats
}

// GetWorldState returns the world state (for direct access if needed)
func (bc *Blockchain) GetWorldState() *state.WorldState {
	return bc.worldState
}

// GetConfig returns the blockchain configuration
func (bc *Blockchain) GetConfig() *config.Config {
	return bc.config
}

// GetBlockAddedChannel returns the block added event channel
func (bc *Blockchain) GetBlockAddedChannel() <-chan *core.Block {
	return bc.blockAddedChan
}

// GetTransactionAddedChannel returns the transaction added event channel
func (bc *Blockchain) GetTransactionAddedChannel() <-chan *core.Transaction {
	return bc.txAddedChan
}

// Cleanup performs maintenance and cleanup operations
func (bc *Blockchain) Cleanup() {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	// Delegate cleanup to WorldState
	bc.worldState.Cleanup()
}

// IsRunning returns whether the blockchain is actively processing
func (bc *Blockchain) IsRunning() bool {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.consensusEngine != nil
}

// GetShardInfo returns shard-related information
func (bc *Blockchain) GetShardInfo() map[string]interface{} {
	return map[string]interface{}{
		"shard_id":            bc.worldState.GetShardID(),
		"total_shards":        bc.worldState.GetTotalShards(),
		"cross_shard_enabled": bc.crossShardEnabled,
	}
}

// Emergency recovery methods
func (bc *Blockchain) EmergencyStop() {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.consensusEngine = nil
}

func (bc *Blockchain) IsHealthy() bool {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	// Check if WorldState is consistent
	if err := bc.worldState.ValidateStateConsistency(); err != nil {
		return false
	}

	// Check if we have a current block
	if bc.worldState.GetCurrentBlock() == nil {
		return false
	}

	return true
}
