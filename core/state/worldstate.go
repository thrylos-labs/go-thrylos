// core/state/worldstate.go

// Complete state management for a shard
// Cross-shard transfer handling for inter-shard communication
// State root calculation using Blake2b for Merkle state trees
// Snapshot functionality for backups and fast sync
// Consistency validation to ensure state integrity

package state

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v3"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/transaction"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
	"golang.org/x/crypto/blake2b"
)

const BaseUnit = int64(1000000000) // 1 THRYLOS

// WorldState manages the global state for a shard
type WorldState struct {
	// Configuration
	config *config.Config

	db    *storage.DB           // For blocks, transactions, batch operations
	state *storage.StateStorage // For accounts, validators, height, state root

	// Account management
	accountManager *account.AccountManager

	// Transaction pool and services
	txPool            *transaction.Pool
	txValidator       *transaction.Validator
	txExecutor        *transaction.Executor
	totalTransactions int64

	// Shard configuration
	shardID     account.ShardID
	totalShards int

	// Block chain state
	blocks      []*core.Block
	currentHash string
	height      int64

	// Blockchain validators (consensus participants)
	validators map[string]*core.Validator

	// Global statistics
	totalSupply   int64
	totalStaked   int64
	lastTimestamp int64

	// State root for Merkle tree
	stateRoot string

	// Cross-shard manager
	crossShardManager *CrossShardManager

	// Synchronization
	mu sync.RWMutex

	assets        map[string]*AssetToken      // assetID -> asset
	assetBalances map[string]map[string]int64 // assetID -> (address -> balance)
	assetRegistry map[string]string           // assetID -> cr
	badgerStorage *storage.BadgerStorage
}

type AssetToken struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	AssetType        string `json:"asset_type"`     // "supply_chain", "carbon_credit", etc.
	RealWorldRef     string `json:"real_world_ref"` // Required reference to physical asset
	MaxDecimals      int32  `json:"max_decimals"`   // Capped at 4
	TotalSupply      int64  `json:"total_supply"`
	Creator          string `json:"creator"`
	Transferable     bool   `json:"transferable"`      // Some assets shouldn't be tradeable
	RequiresApproval bool   `json:"requires_approval"` // KYC/compliance gating
	ExpirationDate   *int64 `json:"expiration_date"`   // Optional expiration
	RegulatoryInfo   string `json:"regulatory_info"`   // Compliance metadata
	CreatedAt        int64  `json:"created_at"`
}

// InitializeFromConfig initializes the world state with config-driven genesis data
func (ws *WorldState) InitializeFromConfig() error {

	fmt.Printf("🔍 InitializeFromConfig: Setting up genesis state from config...\n")

	// Check if we already have accounts (existing state)
	accounts := ws.accountManager.GetAllAccounts()
	if len(accounts) > 0 {
		fmt.Printf("✅ InitializeFromConfig: Existing state found (%d accounts), skipping genesis\n", len(accounts))
		return nil
	}

	// Initialize genesis accounts from config
	totalGenesisBalance := int64(0)
	for _, genesisAccount := range ws.config.Genesis.Accounts {
		fmt.Printf("🏦 Creating genesis account: %s with %d tokens (%s)\n",
			genesisAccount.Address, genesisAccount.Balance/BaseUnit, genesisAccount.Purpose)

		account := &core.Account{
			Address:      genesisAccount.Address,
			Balance:      genesisAccount.Balance,
			Nonce:        0,
			StakedAmount: 0,
			DelegatedTo:  make(map[string]int64),
			Rewards:      0,
		}

		// Create account using account manager
		if err := ws.accountManager.UpdateAccount(account); err != nil {
			return fmt.Errorf("failed to create genesis account %s: %v", genesisAccount.Address, err)
		}

		// Save to persistent storage
		if err := ws.state.SaveAccount(account); err != nil {
			return fmt.Errorf("failed to save genesis account %s: %v", genesisAccount.Address, err)
		}

		totalGenesisBalance += genesisAccount.Balance
	}

	// Set total supply
	ws.totalSupply = totalGenesisBalance

	// Genesis block uses fixed timestamp for deterministic hash
	// This ensures all nodes agree on genesis block hash
	genesisTimestamp := int64(1700000000) // Nov 14, 2023 22:13:20 GMT

	// Initialize genesis block (block 0)
	genesisBlock := &core.Block{
		Header: &core.BlockHeader{
			Index:     0,
			PrevHash:  "",
			Timestamp: genesisTimestamp, // ✅ Always the same!
			Validator: "",               // No validator for genesis
			GasLimit:  ws.config.Consensus.MaxBlockSize,
			GasUsed:   0,
			StateRoot: "",
		},
		Transactions: []*core.Transaction{}, // No transactions in genesis
		Hash:         "",
	}

	// Calculate genesis block hash
	genesisBlock.Hash = ws.calculateBlockHash(genesisBlock)

	// Update state root
	if err := ws.updateStateRoot(); err != nil {
		return fmt.Errorf("failed to calculate initial state root: %v", err)
	}

	// Set the state root in genesis block
	genesisBlock.Header.StateRoot = ws.stateRoot

	// Add genesis block
	ws.blocks = []*core.Block{genesisBlock}
	ws.currentHash = genesisBlock.Hash
	ws.height = 0
	ws.lastTimestamp = genesisBlock.Header.Timestamp

	// Save genesis state
	if err := ws.SaveState(); err != nil {
		return fmt.Errorf("failed to save genesis state: %v", err)
	}

	// Save genesis block
	if err := ws.db.SaveBlock(genesisBlock); err != nil {
		return fmt.Errorf("failed to save genesis block: %v", err)
	}

	fmt.Printf("✅ InitializeFromConfig: Genesis state created successfully\n")
	fmt.Printf("   - Total accounts: %d\n", len(ws.config.Genesis.Accounts))
	fmt.Printf("   - Total supply: %d THRYLOS\n", totalGenesisBalance/BaseUnit)
	fmt.Printf("   - Genesis block: %s\n", genesisBlock.Hash)
	fmt.Printf("   - State root: %s\n", ws.stateRoot)

	return nil
}

// calculateBlockHash calculates the hash of a block
func (ws *WorldState) calculateBlockHash(block *core.Block) string {
	// Simple hash calculation for genesis block
	var buf []byte
	buf = append(buf, []byte(fmt.Sprintf("%d", block.Header.Index))...)
	buf = append(buf, []byte(block.Header.PrevHash)...)
	buf = append(buf, []byte(fmt.Sprintf("%d", block.Header.Timestamp))...)
	buf = append(buf, []byte(block.Header.Validator)...)
	buf = append(buf, []byte(fmt.Sprintf("%d", block.Header.GasUsed))...)

	// Add transaction hashes
	for _, tx := range block.Transactions {
		buf = append(buf, []byte(tx.Hash)...)
	}

	hash := blake2b.Sum256(buf)
	return fmt.Sprintf("%x", hash)
}

// NewWorldState creates a new world state for a shard with config-driven initialization
// NewWorldState creates a new world state for a shard with config-driven initialization
// Simplified version without debug - just add the storage parameter:

// Replace your NewWorldState function with this updated version:

func NewWorldState(dataDir string, shardID account.ShardID, totalShards int, cfg *config.Config, badgerStorage *storage.BadgerStorage) (*WorldState, error) {
	fmt.Printf("🔍 NewWorldState: Using existing BadgerStorage (no creation needed)\n")

	// Use the provided storage instead of creating a new one
	// Create high-level wrappers using the existing storage
	db := storage.NewDB(badgerStorage)
	stateStorage := storage.NewStateStorage(badgerStorage)

	ws := &WorldState{
		config:            cfg,
		db:                db,
		state:             stateStorage,
		accountManager:    account.NewAccountManager(shardID, totalShards),
		txPool:            transaction.NewPool(shardID, totalShards, cfg.Consensus.MaxTxPerBlock, cfg.Consensus.MinGasPrice),
		txValidator:       transaction.NewValidator(shardID, totalShards, cfg),
		txExecutor:        transaction.NewExecutor(shardID, totalShards),
		shardID:           shardID,
		totalShards:       totalShards,
		blocks:            make([]*core.Block, 0),
		validators:        make(map[string]*core.Validator),
		totalSupply:       cfg.Economics.GenesisSupply,
		totalStaked:       0,
		lastTimestamp:     time.Now().Unix(),
		assets:            make(map[string]*AssetToken),
		assetBalances:     make(map[string]map[string]int64),
		assetRegistry:     make(map[string]string),
		totalTransactions: 0, // ADD THIS
		badgerStorage:     badgerStorage,
	}

	// Try to load existing state from storage
	existingState := false
	if err := ws.LoadState(); err != nil {
		fmt.Printf("🔍 NewWorldState: No existing state found (fresh start): %v\n", err)
	} else {
		// Check if we actually have meaningful state (accounts/validators)
		accountCount := ws.GetAccountCount()
		validatorCount := ws.GetValidatorCount()

		if accountCount > 0 || validatorCount > 0 {
			existingState = true
			fmt.Printf("✅ NewWorldState: Loaded existing state: height=%d, accounts=%d, validators=%d\n",
				ws.height, accountCount, validatorCount)
		} else {
			fmt.Printf("🔍 NewWorldState: Found empty state (height=%d, accounts=%d, validators=%d)\n",
				ws.height, accountCount, validatorCount)
		}
	}

	if !existingState {
		// Fresh database or empty state - initialize with config genesis data
		ws.height = -1 // Genesis state
		ws.stateRoot = ""
		ws.totalSupply = cfg.Economics.GenesisSupply
		ws.totalStaked = 0

		// *** CRITICAL FIX: Initialize from config ***
		if err := ws.InitializeFromConfig(); err != nil {
			return nil, fmt.Errorf("failed to initialize genesis from config: %v", err)
		}

		fmt.Printf("✅ NewWorldState: Initialized fresh state from config\n")
	} else {
		// Successfully loaded existing state with data
		ws.UpdateTotalStaked()
	}

	// Initialize cross-shard manager
	ws.crossShardManager = NewCrossShardManager(ws)

	return ws, nil
}

func (ws *WorldState) GetBadgerDB() *badger.DB {
	if ws.badgerStorage == nil {
		return nil
	}
	return ws.badgerStorage.GetDB()
}

// InitializeGenesis initializes the world state with genesis data
func (ws *WorldState) InitializeGenesis(genesisAccount string, initialSupply int64, genesisValidators []*core.Validator) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Validate genesis account address format
	if err := account.ValidateAddress(genesisAccount); err != nil {
		return fmt.Errorf("invalid genesis account address: %v", err)
	}

	// Use config supply if not specified
	if initialSupply <= 0 {
		initialSupply = ws.config.Economics.GenesisSupply
	}

	// Create genesis account
	if err := ws.accountManager.CreateGenesisAccount(genesisAccount, initialSupply); err != nil {
		return fmt.Errorf("failed to create genesis account: %v", err)
	}

	// Initialize validators
	for _, validator := range genesisValidators {
		if err := ws.addValidator(validator); err != nil {
			return fmt.Errorf("failed to add genesis validator %s: %v", validator.Address, err)
		}
	}

	// Set initial state
	ws.totalSupply = initialSupply
	ws.height = 0

	// Calculate initial state root
	if err := ws.updateStateRoot(); err != nil {
		return fmt.Errorf("failed to calculate initial state root: %v", err)
	}

	return nil
}

func (ws *WorldState) AddBlock(block *core.Block) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Validate block can be added
	if err := ws.validateBlockForAddition(block); err != nil {
		return fmt.Errorf("block validation failed: %v", err)
	}

	// Execute all transactions in the block
	for _, tx := range block.Transactions {
		// Use the executor instance
		receipt, err := ws.txExecutor.ExecuteTransaction(tx, ws.accountManager)
		if err != nil {
			return fmt.Errorf("failed to execute transaction %s: %v", tx.Id, err)
		}

		// Check receipt status
		if receipt.Status == 0 {
			return fmt.Errorf("transaction %s failed: %s", tx.Id, receipt.Error)
		}

		// *** UPDATED: Save transaction with indexing ***
		if err := ws.db.SaveTransactionWithIndex(tx); err != nil {
			return fmt.Errorf("failed to save transaction %s with indexing: %v", tx.Id, err)
		}

		// Remove transaction from pool if it exists
		ws.txPool.RemoveTransaction(tx.Id)
	}

	// Add block to chain
	ws.blocks = append(ws.blocks, block)
	ws.currentHash = block.Hash
	ws.height = block.Header.Index
	ws.lastTimestamp = block.Header.Timestamp
	ws.totalTransactions += int64(len(block.Transactions))

	// Update state root
	if err := ws.updateStateRoot(); err != nil {
		return fmt.Errorf("failed to update state root: %v", err)
	}

	// Update block's state root
	block.Header.StateRoot = ws.stateRoot

	// Collect all accounts and validators that need to be saved
	accounts := ws.accountManager.GetAllAccounts()
	var updatedAccounts []*core.Account
	for _, account := range accounts {
		updatedAccounts = append(updatedAccounts, account)
	}

	var updatedValidators []*core.Validator
	for _, validator := range ws.validators {
		updatedValidators = append(updatedValidators, validator)
	}

	// Use atomic batch operation to save everything
	if err := ws.db.CommitBlock(block, updatedAccounts, updatedValidators, ws.totalTransactions); err != nil {
		return fmt.Errorf("failed to commit block to storage: %v", err)
	}

	return nil
}

func (ws *WorldState) GetTransactionsByAddress(address string, limit int) ([]*core.Transaction, error) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	return ws.db.GetTransactionsByAddress(address, limit)
}

// ValidateTransaction validates a transaction using the transaction validator
func (ws *WorldState) ValidateTransaction(tx *core.Transaction) error {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	return ws.txValidator.ValidateTransaction(tx, ws.accountManager)
}

// ExecuteTransaction executes a single transaction (helper method)
func (ws *WorldState) ExecuteTransaction(tx *core.Transaction) (*transaction.ExecutionReceipt, error) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	return ws.txExecutor.ExecuteTransaction(tx, ws.accountManager)
}

// ExecuteBatchTransactions executes multiple transactions
func (ws *WorldState) ExecuteBatchTransactions(transactions []*core.Transaction) ([]*transaction.ExecutionReceipt, error) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	return ws.txExecutor.ExecuteBatch(transactions, ws.accountManager)
}

// ValidateTransactionExecution validates that a transaction can be executed
func (ws *WorldState) ValidateTransactionExecution(tx *core.Transaction) error {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	return ws.txExecutor.ValidateExecution(tx, ws.accountManager)
}

// GetAccount retrieves an account by address
func (ws *WorldState) GetAccount(address string) (*core.Account, error) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	return ws.accountManager.GetAccount(address)
}

// GetBalance returns the balance of an account
func (ws *WorldState) GetBalance(address string) (int64, error) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	return ws.accountManager.GetBalance(address)
}

// UpdateBalance updates the balance for a given address (needed for slashing)
func (ws *WorldState) UpdateBalance(address string, newBalance int64) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if newBalance < 0 {
		return fmt.Errorf("cannot set negative balance")
	}

	// Get the account
	account, err := ws.accountManager.GetAccount(address)
	if err != nil {
		return fmt.Errorf("failed to get account %s: %w", address, err)
	}

	// Update the balance
	account.Balance = newBalance

	// Save back using UpdateAccount
	err = ws.accountManager.UpdateAccount(account)
	if err != nil {
		return fmt.Errorf("failed to update account %s: %w", address, err)
	}

	return nil
}

// GetNonce returns the nonce of an account
func (ws *WorldState) GetNonce(address string) (uint64, error) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	return ws.accountManager.GetNonce(address)
}

// AddTransaction adds a transaction to the pool
func (ws *WorldState) AddTransaction(tx *core.Transaction) error {
	// First validate the transaction
	if err := ws.ValidateTransaction(tx); err != nil {
		return fmt.Errorf("transaction validation failed: %v", err)
	}

	ws.mu.Lock()
	defer ws.mu.Unlock()

	return ws.txPool.AddTransaction(tx)
}

// GetPendingTransactions returns all pending transactions
func (ws *WorldState) GetPendingTransactions() []*core.Transaction {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	return ws.txPool.GetPendingTransactions()
}

// GetExecutableTransactions returns transactions ready for execution
func (ws *WorldState) GetExecutableTransactions(maxCount int) []*core.Transaction {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	return ws.txPool.GetExecutableTransactions(maxCount, ws.accountManager)
}

// GetCurrentBlock returns the current (latest) block
func (ws *WorldState) GetCurrentBlock() *core.Block {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	if len(ws.blocks) == 0 {
		return nil
	}

	return ws.blocks[len(ws.blocks)-1]
}

// GetBlock returns a block by index
func (ws *WorldState) GetBlock(index int64) (*core.Block, error) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	if index < 0 || index >= int64(len(ws.blocks)) {
		return nil, fmt.Errorf("block index %d out of range", index)
	}

	return ws.blocks[index], nil
}

// GetBlockByHash returns a block by hash
func (ws *WorldState) GetBlockByHash(hash string) (*core.Block, error) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	for _, block := range ws.blocks {
		if block.Hash == hash {
			return block, nil
		}
	}

	return nil, fmt.Errorf("block with hash %s not found", hash)
}

// GetHeight returns the current blockchain height
func (ws *WorldState) GetHeight() int64 {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	return ws.height
}

// GetStateRoot returns the current state root
func (ws *WorldState) GetStateRoot() string {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	return ws.stateRoot
}

// AddValidator adds a validator to the state
func (ws *WorldState) AddValidator(validator *core.Validator) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	return ws.addValidator(validator)
}

// GetValidator returns a validator by address
func (ws *WorldState) GetValidator(address string) (*core.Validator, error) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	validator, exists := ws.validators[address]
	if !exists {
		return nil, fmt.Errorf("validator %s not found", address)
	}

	return validator, nil
}

// GetActiveValidators returns all active validators
func (ws *WorldState) GetActiveValidators() []*core.Validator {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	var active []*core.Validator
	for _, validator := range ws.validators {
		if validator.Active && !ws.isValidatorJailed(validator) {
			active = append(active, validator)
		}
	}

	return active
}

// UpdateValidator updates an existing validator
func (ws *WorldState) UpdateValidator(validator *core.Validator) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if _, exists := ws.validators[validator.Address]; !exists {
		return fmt.Errorf("validator %s not found", validator.Address)
	}

	// Validate validator address format
	if err := account.ValidateAddress(validator.Address); err != nil {
		return fmt.Errorf("invalid validator address: %v", err)
	}

	ws.validators[validator.Address] = validator
	return nil
}

// GetTotalSupply returns the total supply of tokens
func (ws *WorldState) GetTotalSupply() int64 {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	return ws.totalSupply
}

// GetTotalStaked returns the total amount of staked tokens
func (ws *WorldState) GetTotalStaked() int64 {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	return ws.totalStaked
}

// GetShardID returns the shard ID
func (ws *WorldState) GetShardID() account.ShardID {
	return ws.shardID
}

// GetTotalShards returns the total number of shards
func (ws *WorldState) GetTotalShards() int {
	return ws.totalShards
}

// GetConfig returns the configuration
func (ws *WorldState) GetConfig() *config.Config {
	return ws.config
}

// GetCrossShardManager returns the cross-shard manager
func (ws *WorldState) GetCrossShardManager() *CrossShardManager {
	return ws.crossShardManager
}

// GetStatus returns a status summary of the world state
func (ws *WorldState) GetStatus() map[string]interface{} {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	poolStats := ws.txPool.GetStats()
	accountStats := ws.accountManager.GetAccountStats()

	return map[string]interface{}{
		"shard_id":           ws.shardID,
		"height":             ws.height,
		"current_hash":       ws.currentHash,
		"state_root":         ws.stateRoot,
		"total_supply":       ws.totalSupply,
		"total_staked":       ws.totalStaked,
		"total_transactions": ws.totalTransactions,
		"block_count":        len(ws.blocks),
		"pending_txs":        poolStats.PendingCount,
		"validator_count":    len(ws.validators),
		"last_timestamp":     ws.lastTimestamp,
		"pool_stats":         poolStats,
		"account_stats":      accountStats,
	}
}

func (ws *WorldState) recalculateTotalTransactions() {
	ws.totalTransactions = 0
	for _, block := range ws.blocks {
		ws.totalTransactions += int64(len(block.Transactions))
	}
}

// isValidatorJailed checks if a validator is currently jailed
func (ws *WorldState) isValidatorJailed(validator *core.Validator) bool {
	return validator.JailUntil > time.Now().Unix()
}

// validateBlockForAddition validates that a block can be added to the chain
func (ws *WorldState) validateBlockForAddition(block *core.Block) error {
	if block == nil {
		return fmt.Errorf("block cannot be nil")
	}

	if block.Header == nil {
		return fmt.Errorf("block header cannot be nil")
	}

	// Check if this is genesis block
	if len(ws.blocks) == 0 {
		if block.Header.Index != 0 {
			return fmt.Errorf("first block must be genesis (index 0), got %d", block.Header.Index)
		}
		if block.Header.PrevHash != "" {
			return fmt.Errorf("genesis block must have empty previous hash")
		}
		return nil
	}

	// Validate chain continuity
	currentBlock := ws.blocks[len(ws.blocks)-1]

	if block.Header.Index != currentBlock.Header.Index+1 {
		return fmt.Errorf("invalid block index: expected %d, got %d",
			currentBlock.Header.Index+1, block.Header.Index)
	}

	if block.Header.PrevHash != currentBlock.Hash {
		return fmt.Errorf("invalid previous hash: expected %s, got %s",
			currentBlock.Hash, block.Header.PrevHash)
	}

	if block.Header.Timestamp <= currentBlock.Header.Timestamp {
		return fmt.Errorf("block timestamp must be greater than previous block")
	}

	// Validate block size
	if block.Header.GasUsed > ws.config.Consensus.MaxBlockSize {
		return fmt.Errorf("block gas used (%d) exceeds maximum block size (%d)",
			block.Header.GasUsed, ws.config.Consensus.MaxBlockSize)
	}

	// Validate transaction count
	if len(block.Transactions) > ws.config.Consensus.MaxTxPerBlock {
		return fmt.Errorf("block contains %d transactions, maximum allowed is %d",
			len(block.Transactions), ws.config.Consensus.MaxTxPerBlock)
	}

	return nil
}

// addValidator adds a validator (internal method, requires lock)
func (ws *WorldState) addValidator(validator *core.Validator) error {
	if validator == nil {
		return fmt.Errorf("validator cannot be nil")
	}

	if validator.Address == "" {
		return fmt.Errorf("validator address cannot be empty")
	}

	// Validate address format
	if err := account.ValidateAddress(validator.Address); err != nil {
		return fmt.Errorf("invalid validator address: %v", err)
	}

	if len(validator.Pubkey) == 0 {
		return fmt.Errorf("validator public key cannot be empty")
	}

	if validator.Stake < ws.config.Staking.MinValidatorStake {
		return fmt.Errorf("validator stake %d below minimum %d",
			validator.Stake, ws.config.Staking.MinValidatorStake)
	}

	// Check if validator already exists
	if _, exists := ws.validators[validator.Address]; exists {
		return fmt.Errorf("validator %s already exists", validator.Address)
	}

	// Initialize validator fields if needed
	if validator.Delegators == nil {
		validator.Delegators = make(map[string]int64)
	}

	// Set creation time if not set
	if validator.CreatedAt == 0 {
		validator.CreatedAt = time.Now().Unix()
	}
	if validator.UpdatedAt == 0 {
		validator.UpdatedAt = time.Now().Unix()
	}

	ws.validators[validator.Address] = validator
	return nil
}

// updateStateRoot calculates and updates the state root
func (ws *WorldState) updateStateRoot() error {
	// Calculate state root based on all accounts and validators
	var stateData []byte

	// Get all accounts and sort by address for deterministic ordering
	accounts := ws.accountManager.GetAllAccounts()
	addresses := make([]string, 0, len(accounts))
	for addr := range accounts {
		addresses = append(addresses, addr)
	}

	// Sort addresses for deterministic state root
	for i := 0; i < len(addresses)-1; i++ {
		for j := i + 1; j < len(addresses); j++ {
			if addresses[i] > addresses[j] {
				addresses[i], addresses[j] = addresses[j], addresses[i]
			}
		}
	}

	// Serialize account data
	for _, addr := range addresses {
		account := accounts[addr]

		// Serialize account data
		stateData = append(stateData, []byte(account.Address)...)

		balanceBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(balanceBytes, uint64(account.Balance))
		stateData = append(stateData, balanceBytes...)

		nonceBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(nonceBytes, account.Nonce)
		stateData = append(stateData, nonceBytes...)

		stakedBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(stakedBytes, uint64(account.StakedAmount))
		stateData = append(stateData, stakedBytes...)

		rewardsBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(rewardsBytes, uint64(account.Rewards))
		stateData = append(stateData, rewardsBytes...)

		// Include delegation data for deterministic state
		if account.DelegatedTo != nil {
			for validator, amount := range account.DelegatedTo {
				stateData = append(stateData, []byte(validator)...)
				delegationBytes := make([]byte, 8)
				binary.BigEndian.PutUint64(delegationBytes, uint64(amount))
				stateData = append(stateData, delegationBytes...)
			}
		}
	}

	// Add validator data (sorted by address)
	validatorAddresses := make([]string, 0, len(ws.validators))
	for addr := range ws.validators {
		validatorAddresses = append(validatorAddresses, addr)
	}

	// Sort validator addresses
	for i := 0; i < len(validatorAddresses)-1; i++ {
		for j := i + 1; j < len(validatorAddresses); j++ {
			if validatorAddresses[i] > validatorAddresses[j] {
				validatorAddresses[i], validatorAddresses[j] = validatorAddresses[j], validatorAddresses[i]
			}
		}
	}

	// Serialize validator data
	for _, addr := range validatorAddresses {
		validator := ws.validators[addr]

		stateData = append(stateData, []byte(validator.Address)...)
		stateData = append(stateData, validator.Pubkey...)

		stakeBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(stakeBytes, uint64(validator.Stake))
		stateData = append(stateData, stakeBytes...)

		// Add active status
		if validator.Active {
			stateData = append(stateData, 1)
		} else {
			stateData = append(stateData, 0)
		}
	}

	// Calculate Blake2b hash of state data
	hash := blake2b.Sum256(stateData)
	ws.stateRoot = fmt.Sprintf("%x", hash)

	return nil
}

// ValidateStateConsistency validates the consistency of the world state
func (ws *WorldState) ValidateStateConsistency() error {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	// Validate account balances are non-negative
	accounts := ws.accountManager.GetAllAccounts()
	for addr, acc := range accounts {
		if acc.Balance < 0 {
			return fmt.Errorf("account %s has negative balance: %d", addr, acc.Balance)
		}
		if acc.StakedAmount < 0 {
			return fmt.Errorf("account %s has negative staked amount: %d", addr, acc.StakedAmount)
		}
		if acc.Rewards < 0 {
			return fmt.Errorf("account %s has negative rewards: %d", addr, acc.Rewards)
		}

		// Validate address format
		if err := account.ValidateAddress(addr); err != nil {
			return fmt.Errorf("account %s has invalid address format: %v", addr, err)
		}
	}

	// Validate validator stakes and addresses
	for addr, validator := range ws.validators {
		if validator.Stake < 0 {
			return fmt.Errorf("validator %s has negative stake: %d", addr, validator.Stake)
		}
		if validator.SelfStake < 0 {
			return fmt.Errorf("validator %s has negative self stake: %d", addr, validator.SelfStake)
		}
		if validator.DelegatedStake < 0 {
			return fmt.Errorf("validator %s has negative delegated stake: %d", addr, validator.DelegatedStake)
		}

		// Validate address format
		if err := account.ValidateAddress(addr); err != nil {
			return fmt.Errorf("validator %s has invalid address format: %v", addr, err)
		}

		// Validate minimum stake requirement
		if validator.Stake < ws.config.Staking.MinValidatorStake {
			return fmt.Errorf("validator %s stake %d below minimum %d",
				addr, validator.Stake, ws.config.Staking.MinValidatorStake)
		}
	}

	// Validate state root can be recalculated
	originalRoot := ws.stateRoot
	if err := ws.updateStateRoot(); err != nil {
		return fmt.Errorf("failed to recalculate state root: %v", err)
	}

	if ws.stateRoot != originalRoot {
		return fmt.Errorf("state root mismatch: stored=%s, calculated=%s", originalRoot, ws.stateRoot)
	}
	// Validate asset consistency (ADD THIS)
	if err := ws.ValidateAssetConsistency(); err != nil {
		return fmt.Errorf("asset state validation failed: %v", err)
	}

	return nil
}

// CrossShardTransfer represents a transfer between shards
type CrossShardTransfer struct {
	FromShard account.ShardID
	ToShard   account.ShardID
	From      string
	To        string
	Amount    int64
	Nonce     uint64
	Hash      string
	Timestamp int64
	Status    string // "pending", "completed", "failed"
}

// CrossShardManager manages cross-shard operations
type CrossShardManager struct {
	worldState       *WorldState
	pendingTransfers map[string]*CrossShardTransfer // hash -> transfer
	mu               sync.RWMutex
}

// NewCrossShardManager creates a new cross-shard manager
func NewCrossShardManager(worldState *WorldState) *CrossShardManager {
	return &CrossShardManager{
		worldState:       worldState,
		pendingTransfers: make(map[string]*CrossShardTransfer),
	}
}

// InitiateTransfer initiates a cross-shard transfer
func (csm *CrossShardManager) InitiateTransfer(from, to string, amount int64, nonce uint64) (*CrossShardTransfer, error) {
	csm.mu.Lock()
	defer csm.mu.Unlock()

	fromShard := account.CalculateShardID(from, csm.worldState.totalShards)
	toShard := account.CalculateShardID(to, csm.worldState.totalShards)

	if fromShard == toShard {
		return nil, fmt.Errorf("not a cross-shard transfer: both addresses in shard %d", fromShard)
	}

	// Only the sender's shard can initiate the transfer
	if fromShard != csm.worldState.shardID {
		return nil, fmt.Errorf("can only initiate transfers from local shard %d, got %d",
			csm.worldState.shardID, fromShard)
	}

	// Validate sender account
	senderAccount, err := csm.worldState.GetAccount(from)
	if err != nil {
		return nil, fmt.Errorf("failed to get sender account: %v", err)
	}

	if senderAccount.Balance < amount {
		return nil, fmt.Errorf("insufficient balance: have %d, need %d", senderAccount.Balance, amount)
	}

	if senderAccount.Nonce != nonce {
		return nil, fmt.Errorf("invalid nonce: expected %d, got %d", senderAccount.Nonce, nonce)
	}

	// Create transfer record
	transfer := &CrossShardTransfer{
		FromShard: fromShard,
		ToShard:   toShard,
		From:      from,
		To:        to,
		Amount:    amount,
		Nonce:     nonce,
		Timestamp: time.Now().Unix(),
		Status:    "pending",
	}

	// Calculate transfer hash
	var buf []byte
	buf = append(buf, []byte(from)...)
	buf = append(buf, []byte(to)...)

	amountBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(amountBytes, uint64(amount))
	buf = append(buf, amountBytes...)

	nonceBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(nonceBytes, nonce)
	buf = append(buf, nonceBytes...)

	timestampBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(timestampBytes, uint64(transfer.Timestamp))
	buf = append(buf, timestampBytes...)

	hash := blake2b.Sum256(buf)
	transfer.Hash = fmt.Sprintf("%x", hash)

	// Debit sender account
	senderAccount.Balance -= amount
	senderAccount.Nonce++

	if err := csm.worldState.accountManager.UpdateAccount(senderAccount); err != nil {
		return nil, fmt.Errorf("failed to update sender account: %v", err)
	}

	// Store pending transfer
	csm.pendingTransfers[transfer.Hash] = transfer

	return transfer, nil
}

// CompleteTransfer completes a cross-shard transfer
func (csm *CrossShardManager) CompleteTransfer(transferHash string) error {
	csm.mu.Lock()
	defer csm.mu.Unlock()

	transfer, exists := csm.pendingTransfers[transferHash]
	if !exists {
		return fmt.Errorf("transfer %s not found", transferHash)
	}

	// Only the recipient's shard can complete the transfer
	if transfer.ToShard != csm.worldState.shardID {
		return fmt.Errorf("can only complete transfers to local shard %d, got %d",
			csm.worldState.shardID, transfer.ToShard)
	}

	// Get or create recipient account
	recipientAccount, err := csm.worldState.GetAccount(transfer.To)
	if err != nil {
		// Create account if it doesn't exist
		recipientAccount = &core.Account{
			Address:      transfer.To,
			Balance:      0,
			Nonce:        0,
			StakedAmount: 0,
			DelegatedTo:  make(map[string]int64),
			Rewards:      0,
		}
	}

	// Credit recipient account
	recipientAccount.Balance += transfer.Amount

	if err := csm.worldState.accountManager.UpdateAccount(recipientAccount); err != nil {
		return fmt.Errorf("failed to update recipient account: %v", err)
	}

	// Update transfer status
	transfer.Status = "completed"

	// Remove from pending transfers after a delay (keep for history)
	go func() {
		time.Sleep(time.Hour) // Keep completed transfers for 1 hour
		csm.mu.Lock()
		delete(csm.pendingTransfers, transferHash)
		csm.mu.Unlock()
	}()

	return nil
}

// GetPendingTransfers returns all pending cross-shard transfers
func (csm *CrossShardManager) GetPendingTransfers() []*CrossShardTransfer {
	csm.mu.RLock()
	defer csm.mu.RUnlock()

	transfers := make([]*CrossShardTransfer, 0, len(csm.pendingTransfers))
	for _, transfer := range csm.pendingTransfers {
		transfers = append(transfers, transfer)
	}

	return transfers
}

// GetTransfer returns a specific cross-shard transfer
func (csm *CrossShardManager) GetTransfer(hash string) (*CrossShardTransfer, error) {
	csm.mu.RLock()
	defer csm.mu.RUnlock()

	transfer, exists := csm.pendingTransfers[hash]
	if !exists {
		return nil, fmt.Errorf("transfer %s not found", hash)
	}

	return transfer, nil
}

// StateSnapshot represents a point-in-time snapshot of the world state
type StateSnapshot struct {
	Height      int64
	StateRoot   string
	Timestamp   int64
	TotalSupply int64
	TotalStaked int64
	Accounts    map[string]*core.Account
	Validators  map[string]*core.Validator
	Config      *config.Config
}

// CreateSnapshot creates a snapshot of the current world state
func (ws *WorldState) CreateSnapshot() *StateSnapshot {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	// Copy accounts
	accounts := make(map[string]*core.Account)
	for addr, account := range ws.accountManager.GetAllAccounts() {
		// Deep copy account
		delegatedTo := make(map[string]int64)
		if account.DelegatedTo != nil {
			for k, v := range account.DelegatedTo {
				delegatedTo[k] = v
			}
		}

		accounts[addr] = &core.Account{
			Address:      account.Address,
			Balance:      account.Balance,
			Nonce:        account.Nonce,
			StakedAmount: account.StakedAmount,
			DelegatedTo:  delegatedTo,
			Rewards:      account.Rewards,
			CodeHash:     append([]byte(nil), account.CodeHash...),
			StorageRoot:  append([]byte(nil), account.StorageRoot...),
		}
	}

	// Copy validators
	validators := make(map[string]*core.Validator)
	for addr, validator := range ws.validators {
		// Deep copy validator
		delegators := make(map[string]int64)
		if validator.Delegators != nil {
			for k, v := range validator.Delegators {
				delegators[k] = v
			}
		}

		validators[addr] = &core.Validator{
			Address:        validator.Address,
			Pubkey:         append([]byte(nil), validator.Pubkey...),
			Stake:          validator.Stake,
			SelfStake:      validator.SelfStake,
			DelegatedStake: validator.DelegatedStake,
			Delegators:     delegators,
			Commission:     validator.Commission,
			Active:         validator.Active,
			BlocksProposed: validator.BlocksProposed,
			BlocksMissed:   validator.BlocksMissed,
			JailUntil:      validator.JailUntil,
			CreatedAt:      validator.CreatedAt,
			UpdatedAt:      validator.UpdatedAt,
		}
	}

	return &StateSnapshot{
		Height:      ws.height,
		StateRoot:   ws.stateRoot,
		Timestamp:   ws.lastTimestamp,
		TotalSupply: ws.totalSupply,
		TotalStaked: ws.totalStaked,
		Accounts:    accounts,
		Validators:  validators,
		Config:      ws.config, // Reference to config
	}
}

// RestoreFromSnapshot restores the world state from a snapshot
func (ws *WorldState) RestoreFromSnapshot(snapshot *StateSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("snapshot cannot be nil")
	}

	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Validate snapshot compatibility
	if snapshot.Config != nil {
		if ws.config.Economics.GenesisSupply != snapshot.Config.Economics.GenesisSupply {
			return fmt.Errorf("incompatible genesis supply: current=%d, snapshot=%d",
				ws.config.Economics.GenesisSupply, snapshot.Config.Economics.GenesisSupply)
		}
	}

	// Clear current state
	ws.accountManager = account.NewAccountManager(ws.shardID, ws.totalShards)
	ws.validators = make(map[string]*core.Validator)

	// Restore accounts
	for addr, account := range snapshot.Accounts {
		if err := ws.accountManager.UpdateAccount(account); err != nil {
			return fmt.Errorf("failed to restore account %s: %v", addr, err)
		}
	}

	// Restore validators
	for addr, validator := range snapshot.Validators {
		ws.validators[addr] = validator
	}

	// Restore global state
	ws.height = snapshot.Height
	ws.stateRoot = snapshot.StateRoot
	ws.lastTimestamp = snapshot.Timestamp
	ws.totalSupply = snapshot.TotalSupply
	ws.totalStaked = snapshot.TotalStaked

	// Validate restored state
	if err := ws.ValidateStateConsistency(); err != nil {
		return fmt.Errorf("restored state failed consistency check: %v", err)
	}

	return nil
}

// StakingManager provides staking-related functionality
type StakingManager struct {
	worldState *WorldState
}

// NewStakingManager creates a new staking manager
func (ws *WorldState) GetStakingManager() *StakingManager {
	return &StakingManager{worldState: ws}
}

// Delegate stakes tokens to a validator
func (sm *StakingManager) Delegate(delegatorAddr, validatorAddr string, amount int64) error {
	ws := sm.worldState
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Validate addresses
	if err := account.ValidateAddress(delegatorAddr); err != nil {
		return fmt.Errorf("invalid delegator address: %v", err)
	}
	if err := account.ValidateAddress(validatorAddr); err != nil {
		return fmt.Errorf("invalid validator address: %v", err)
	}

	// Check minimum delegation amount
	if amount < ws.config.Staking.MinDelegation {
		return fmt.Errorf("delegation amount %d below minimum %d",
			amount, ws.config.Staking.MinDelegation)
	}

	// Get delegator account
	delegator, err := ws.accountManager.GetAccount(delegatorAddr)
	if err != nil {
		return fmt.Errorf("failed to get delegator account: %v", err)
	}

	// Check balance
	if delegator.Balance < amount {
		return fmt.Errorf("insufficient balance: have %d, need %d", delegator.Balance, amount)
	}

	// Get validator
	validator, exists := ws.validators[validatorAddr]
	if !exists {
		return fmt.Errorf("validator %s not found", validatorAddr)
	}

	if !validator.Active {
		return fmt.Errorf("validator %s is not active", validatorAddr)
	}

	// Update delegator account
	delegator.Balance -= amount
	delegator.StakedAmount += amount
	if delegator.DelegatedTo == nil {
		delegator.DelegatedTo = make(map[string]int64)
	}
	delegator.DelegatedTo[validatorAddr] += amount

	// Update validator
	validator.DelegatedStake += amount
	validator.Stake += amount
	if validator.Delegators == nil {
		validator.Delegators = make(map[string]int64)
	}
	validator.Delegators[delegatorAddr] += amount
	validator.UpdatedAt = time.Now().Unix()

	// Update accounts
	if err := ws.accountManager.UpdateAccount(delegator); err != nil {
		return fmt.Errorf("failed to update delegator account: %v", err)
	}

	// Update total staked
	ws.totalStaked += amount

	return nil
}

// Undelegate unstakes tokens from a validator
func (sm *StakingManager) Undelegate(delegatorAddr, validatorAddr string, amount int64) error {
	ws := sm.worldState
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Validate addresses
	if err := account.ValidateAddress(delegatorAddr); err != nil {
		return fmt.Errorf("invalid delegator address: %v", err)
	}
	if err := account.ValidateAddress(validatorAddr); err != nil {
		return fmt.Errorf("invalid validator address: %v", err)
	}

	// Get delegator account
	delegator, err := ws.accountManager.GetAccount(delegatorAddr)
	if err != nil {
		return fmt.Errorf("failed to get delegator account: %v", err)
	}

	// Check delegation exists
	if delegator.DelegatedTo == nil {
		return fmt.Errorf("no delegations found for account %s", delegatorAddr)
	}

	delegatedAmount, exists := delegator.DelegatedTo[validatorAddr]
	if !exists || delegatedAmount < amount {
		return fmt.Errorf("insufficient delegation: have %d, need %d", delegatedAmount, amount)
	}

	// Get validator
	validator, exists := ws.validators[validatorAddr]
	if !exists {
		return fmt.Errorf("validator %s not found", validatorAddr)
	}

	// Update delegator account (with unbonding period)
	delegator.StakedAmount -= amount
	delegator.DelegatedTo[validatorAddr] -= amount
	if delegator.DelegatedTo[validatorAddr] == 0 {
		delete(delegator.DelegatedTo, validatorAddr)
	}

	// Update validator
	validator.DelegatedStake -= amount
	validator.Stake -= amount
	if validator.Delegators != nil {
		validator.Delegators[delegatorAddr] -= amount
		if validator.Delegators[delegatorAddr] == 0 {
			delete(validator.Delegators, delegatorAddr)
		}
	}
	validator.UpdatedAt = time.Now().Unix()

	// Apply unbonding period (tokens will be available after period)
	// For now, immediately return tokens (in production, implement unbonding queue)
	delegator.Balance += amount

	// Update accounts
	if err := ws.accountManager.UpdateAccount(delegator); err != nil {
		return fmt.Errorf("failed to update delegator account: %v", err)
	}

	// Update total staked
	ws.totalStaked -= amount

	return nil
}

// DistributeRewards distributes staking rewards to validators and delegators
func (sm *StakingManager) DistributeRewards(totalRewards int64) error {
	ws := sm.worldState
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if totalRewards <= 0 {
		return fmt.Errorf("total rewards must be positive")
	}

	activeValidators := ws.GetActiveValidators()
	if len(activeValidators) == 0 {
		return fmt.Errorf("no active validators to distribute rewards")
	}

	// Calculate total voting power
	totalVotingPower := int64(0)
	for _, validator := range activeValidators {
		totalVotingPower += validator.Stake
	}

	if totalVotingPower == 0 {
		return fmt.Errorf("total voting power is zero")
	}

	// Distribute rewards proportionally
	for _, validator := range activeValidators {
		// Calculate validator's share
		validatorReward := (totalRewards * validator.Stake) / totalVotingPower

		// Calculate commission (handle float64 commission rate)
		commission := (validatorReward * int64(validator.Commission)) / 10000 // Commission in basis points
		delegatorReward := validatorReward - commission

		// Reward validator (commission)
		validatorAccount, err := ws.accountManager.GetAccount(validator.Address)
		if err != nil {
			continue // Skip if validator account not found
		}
		validatorAccount.Rewards += commission
		validatorAccount.Balance += commission
		ws.accountManager.UpdateAccount(validatorAccount)

		// Distribute remaining rewards to delegators proportionally
		if validator.DelegatedStake > 0 && len(validator.Delegators) > 0 {
			for delegatorAddr, delegatedAmount := range validator.Delegators {
				delegatorShare := (delegatorReward * delegatedAmount) / validator.DelegatedStake

				delegatorAccount, err := ws.accountManager.GetAccount(delegatorAddr)
				if err != nil {
					continue // Skip if delegator account not found
				}

				delegatorAccount.Rewards += delegatorShare
				delegatorAccount.Balance += delegatorShare
				ws.accountManager.UpdateAccount(delegatorAccount)
			}
		} else {
			// If no delegators, validator gets all rewards
			validatorAccount.Rewards += delegatorReward
			validatorAccount.Balance += delegatorReward
			ws.accountManager.UpdateAccount(validatorAccount)
		}

		// Update total supply
		ws.totalSupply += validatorReward
	}

	return nil
}

// GetDelegations returns all delegations for an account
func (sm *StakingManager) GetDelegations(delegatorAddr string) (map[string]int64, error) {
	ws := sm.worldState
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	account, err := ws.accountManager.GetAccount(delegatorAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %v", err)
	}

	if account.DelegatedTo == nil {
		return make(map[string]int64), nil
	}

	// Return copy to prevent external modification
	delegations := make(map[string]int64)
	for validator, amount := range account.DelegatedTo {
		delegations[validator] = amount
	}

	return delegations, nil
}

// Helper methods for transaction pool integration
func (ws *WorldState) GetTransactionPool() *transaction.Pool {
	return ws.txPool
}

func (ws *WorldState) GetTransactionValidator() *transaction.Validator {
	return ws.txValidator
}

func (ws *WorldState) GetTransactionExecutor() *transaction.Executor {
	return ws.txExecutor
}

func (ws *WorldState) GetAccountManager() *account.AccountManager {
	return ws.accountManager
}

// UpdateTotalStaked recalculates total staked amount (useful for consistency checks)
func (ws *WorldState) UpdateTotalStaked() {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	total := int64(0)
	for _, validator := range ws.validators {
		total += validator.Stake
	}
	ws.totalStaked = total
}

// GetValidatorCount returns the number of validators
func (ws *WorldState) GetValidatorCount() int {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return len(ws.validators)
}

// GetActiveValidatorCount returns the number of active validators
func (ws *WorldState) GetActiveValidatorCount() int {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	count := 0
	for _, validator := range ws.validators {
		if validator.Active && !ws.isValidatorJailed(validator) {
			count++
		}
	}
	return count
}

// GetAccountCount returns the number of accounts
func (ws *WorldState) GetAccountCount() int {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	accounts := ws.accountManager.GetAllAccounts()
	return len(accounts)
}

// Cleanup removes old completed transactions and performs maintenance
func (ws *WorldState) Cleanup() {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Clean up stale transactions (older than 1 hour)
	maxAge := time.Hour
	removedCount := ws.txPool.CleanupStaleTransactions(maxAge)

	// Log cleanup if any transactions were removed
	if removedCount > 0 {
		// Could add logging here if needed
		_ = removedCount // Acknowledge the return value
	}

	// Update state root after cleanup
	ws.updateStateRoot()
}

func (ws *WorldState) GetCurrentHeight() int64 {
	return ws.GetHeight()
}

// ExportAccounts returns all accounts for state sync
func (ws *WorldState) ExportAccounts() map[string]*core.Account {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	return ws.accountManager.GetAllAccounts()
}

// ExportValidators returns all validators for state sync
func (ws *WorldState) ExportValidators() map[string]*core.Validator {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	// Create a copy to prevent external modification
	validators := make(map[string]*core.Validator)
	for addr, validator := range ws.validators {
		// Deep copy validator
		delegators := make(map[string]int64)
		if validator.Delegators != nil {
			for k, v := range validator.Delegators {
				delegators[k] = v
			}
		}

		validators[addr] = &core.Validator{
			Address:        validator.Address,
			Pubkey:         append([]byte(nil), validator.Pubkey...),
			Stake:          validator.Stake,
			SelfStake:      validator.SelfStake,
			DelegatedStake: validator.DelegatedStake,
			Delegators:     delegators,
			Commission:     validator.Commission,
			Active:         validator.Active,
			BlocksProposed: validator.BlocksProposed,
			BlocksMissed:   validator.BlocksMissed,
			JailUntil:      validator.JailUntil,
			CreatedAt:      validator.CreatedAt,
			UpdatedAt:      validator.UpdatedAt,
		}
	}

	return validators
}

// ExportStakes returns staking information for state sync
func (ws *WorldState) ExportStakes() map[string]map[string]int64 {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	stakes := make(map[string]map[string]int64)

	// Export delegations from all accounts
	accounts := ws.accountManager.GetAllAccounts()
	for addr, account := range accounts {
		if account.DelegatedTo != nil && len(account.DelegatedTo) > 0 {
			stakes[addr] = make(map[string]int64)
			for validator, amount := range account.DelegatedTo {
				stakes[addr][validator] = amount
			}
		}
	}

	return stakes
}

// Clear clears the world state (for restoring from snapshot)
func (ws *WorldState) Clear() error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Reset account manager
	ws.accountManager = account.NewAccountManager(ws.shardID, ws.totalShards)

	// Clear validators
	ws.validators = make(map[string]*core.Validator)

	// Reset blocks
	ws.blocks = make([]*core.Block, 0)

	// Reset state
	ws.currentHash = ""
	ws.height = -1
	ws.stateRoot = ""
	ws.totalSupply = 0
	ws.totalStaked = 0
	ws.lastTimestamp = 0

	// Recreate transaction pool
	ws.txPool = transaction.NewPool(ws.shardID, ws.totalShards, ws.config.Consensus.MaxTxPerBlock, ws.config.Consensus.MinGasPrice)

	return nil
}

// SetAccount sets an account in the world state
func (ws *WorldState) SetAccount(address string, account *core.Account) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	return ws.accountManager.UpdateAccount(account)
}

// SetValidator sets a validator in the world state
func (ws *WorldState) SetValidator(address string, validator *core.Validator) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Validate validator
	if validator == nil {
		return fmt.Errorf("validator cannot be nil")
	}

	if validator.Address != address {
		return fmt.Errorf("validator address mismatch")
	}

	// Validate address format
	if err := account.ValidateAddress(address); err != nil {
		return fmt.Errorf("invalid validator address: %v", err)
	}

	ws.validators[address] = validator
	return nil
}

// SetStake sets a delegation in the world state
func (ws *WorldState) SetStake(delegatorAddr, validatorAddr string, amount int64) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Get or create delegator account
	delegator, err := ws.accountManager.GetAccount(delegatorAddr)
	if err != nil {
		// Create new account if it doesn't exist
		delegator = &core.Account{
			Address:      delegatorAddr,
			Balance:      0,
			Nonce:        0,
			StakedAmount: 0,
			DelegatedTo:  make(map[string]int64),
			Rewards:      0,
		}
	}

	// Initialize DelegatedTo map if nil
	if delegator.DelegatedTo == nil {
		delegator.DelegatedTo = make(map[string]int64)
	}

	// Set delegation
	if amount > 0 {
		delegator.DelegatedTo[validatorAddr] = amount
		delegator.StakedAmount += amount // Update total staked amount
	} else {
		// Remove delegation if amount is 0
		delete(delegator.DelegatedTo, validatorAddr)
	}

	// Update account
	return ws.accountManager.UpdateAccount(delegator)
}

// SetStateRoot sets the state root
func (ws *WorldState) SetStateRoot(stateRoot string) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	ws.stateRoot = stateRoot
	return nil
}

// SetCurrentHeight sets the current height
func (ws *WorldState) SetCurrentHeight(height int64) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	ws.height = height
	return nil
}

// PruneStatesBefore removes state data before a given height
func (ws *WorldState) PruneStatesBefore(height int64) (int, error) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if height <= 0 || height >= ws.height {
		return 0, nil // Nothing to prune
	}

	// Count blocks to be pruned
	pruned := 0
	newBlocks := make([]*core.Block, 0)

	for _, block := range ws.blocks {
		if block.Header.Index >= height {
			newBlocks = append(newBlocks, block)
		} else {
			pruned++
		}
	}

	// Update blocks array
	ws.blocks = newBlocks

	return pruned, nil
}

func (ws *WorldState) SaveState() error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Save current height
	if err := ws.state.SaveHeight(ws.height); err != nil {
		return fmt.Errorf("failed to save height: %v", err)
	}

	// Save state root
	if err := ws.state.SaveStateRoot(ws.stateRoot); err != nil {
		return fmt.Errorf("failed to save state root: %v", err)
	}

	// Save all accounts
	accounts := ws.accountManager.GetAllAccounts()
	for _, account := range accounts {
		if err := ws.state.SaveAccount(account); err != nil {
			return fmt.Errorf("failed to save account %s: %v", account.Address, err)
		}
	}

	// Save all validators
	for _, validator := range ws.validators {
		if err := ws.state.SaveValidator(validator); err != nil {
			return fmt.Errorf("failed to save validator %s: %v", validator.Address, err)
		}
	}

	// Save assets (ADD THIS)
	if err := ws.SaveAssetsToStorage(); err != nil {
		return fmt.Errorf("failed to save assets: %v", err)
	}

	return nil
}

// LoadState loads the world state from storage
func (ws *WorldState) LoadState() error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	fmt.Printf("🔍 LoadState: Attempting to load state from storage...\n")

	// Load height
	height, err := ws.state.GetHeight()
	if err != nil {
		fmt.Printf("🔍 LoadState: No height found (fresh database): %v\n", err)
		return fmt.Errorf("no existing state found (fresh database)")
	}

	fmt.Printf("🔍 LoadState: Found height: %d\n", height)
	if height >= 0 { // -1 means genesis state
		ws.height = height
	}

	// Load state root
	stateRoot, err := ws.state.GetStateRoot()
	if err != nil {
		fmt.Printf("🔍 LoadState: No state root found: %v\n", err)
		return fmt.Errorf("no state root found")
	}

	fmt.Printf("🔍 LoadState: Found state root: %s\n", stateRoot)
	ws.stateRoot = stateRoot

	// *** ADD THIS - Load total transactions ***
	totalTx, err := ws.state.GetTotalTransactions()
	if err != nil {
		fmt.Printf("🔍 LoadState: No transaction count found (will calculate from blocks): %v\n", err)
		ws.totalTransactions = 0 // Will be calculated later
	} else {
		ws.totalTransactions = totalTx
		fmt.Printf("🔍 LoadState: Found total transactions: %d\n", totalTx)
	}

	// Load all accounts
	accounts, err := ws.state.GetAllAccounts()
	if err != nil {
		fmt.Printf("🔍 LoadState: Error loading accounts: %v\n", err)
		return fmt.Errorf("failed to load accounts: %v", err)
	}

	fmt.Printf("🔍 LoadState: Found %d accounts\n", len(accounts))

	// Reset account manager and load accounts
	ws.accountManager = account.NewAccountManager(ws.shardID, ws.totalShards)
	totalSupply := int64(0)
	for _, account := range accounts {
		if err := ws.accountManager.UpdateAccount(account); err != nil {
			return fmt.Errorf("failed to restore account %s: %v", account.Address, err)
		}
		totalSupply += account.Balance + account.StakedAmount + account.Rewards
	}
	ws.totalSupply = totalSupply

	// Load all validators
	validators, err := ws.state.GetAllValidators()
	if err != nil {
		fmt.Printf("🔍 LoadState: Error loading validators: %v\n", err)
		return fmt.Errorf("failed to load validators: %v", err)
	}

	fmt.Printf("🔍 LoadState: Found %d validators\n", len(validators))

	// Clear and reload validators
	ws.validators = make(map[string]*core.Validator)
	totalStaked := int64(0)
	for address, validator := range validators {
		ws.validators[address] = validator
		totalStaked += validator.Stake
	}
	ws.totalStaked = totalStaked

	if err != nil {
		fmt.Printf("🔍 LoadState: Warning - could not load blocks: %v\n", err)
	} else {
		// If transaction count wasn't found or is 0, calculate it from blocks
		if ws.totalTransactions == 0 && len(ws.blocks) > 0 {
			fmt.Printf("🔍 LoadState: Calculating transaction count from %d blocks...\n", len(ws.blocks))
			ws.recalculateTotalTransactions()

			// Save the calculated value for future loads
			if err := ws.state.SaveTotalTransactions(ws.totalTransactions); err != nil {
				fmt.Printf("⚠️  LoadState: Failed to save calculated transaction count: %v\n", err)
			} else {
				fmt.Printf("✅ LoadState: Saved calculated transaction count: %d\n", ws.totalTransactions)
			}
		}
	}

	// Load assets
	if err := ws.LoadAssetsFromStorage(); err != nil {
		fmt.Printf("🔍 LoadState: Warning - could not load assets: %v\n", err)
		// Don't fail - assets might not exist yet
	}

	fmt.Printf("✅ LoadState: State loaded successfully - Height: %d, Accounts: %d, Validators: %d, Transactions: %d\n",
		ws.height, len(accounts), len(validators), ws.totalTransactions)

	return nil
}

// Close properly closes the storage
func (ws *WorldState) Close() error {
	// Save current state before closing
	if err := ws.SaveState(); err != nil {
		return fmt.Errorf("failed to save state during close: %v", err)
	}

	// Close high-level components
	if err := ws.db.Close(); err != nil {
		return fmt.Errorf("failed to close db: %v", err)
	}

	if err := ws.state.Close(); err != nil {
		return fmt.Errorf("failed to close state storage: %v", err)
	}

	// We need access to the underlying BadgerStorage
	// Option 1: Store a reference to BadgerStorage in WorldState
	// Option 2: Add a method to get the underlying storage
	// For now, return nil since the Node handles storage closing
	return nil
}

// GetBlockFromStorage retrieves a block from storage (not just memory)
func (ws *WorldState) GetBlockFromStorage(hash string) (*core.Block, error) {
	return ws.db.GetBlock(hash)
}

// GetTransactionFromStorage retrieves a transaction from storage
func (ws *WorldState) GetTransactionFromStorage(hash string) (*core.Transaction, error) {
	return ws.db.GetTransaction(hash)
}

// Modified account operations to persist to storage
func (ws *WorldState) UpdateAccountWithStorage(account *core.Account) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Update in memory
	if err := ws.accountManager.UpdateAccount(account); err != nil {
		return err
	}

	// Persist to storage
	return ws.state.SaveAccount(account)
}

// Modified validator operations to persist to storage
func (ws *WorldState) UpdateValidatorWithStorage(validator *core.Validator) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Update in memory
	ws.validators[validator.Address] = validator

	// Persist to storage
	return ws.state.SaveValidator(validator)
}

// Add to StakingManager in worldstate.go
func (sm *StakingManager) ClaimRewards(delegatorAddr string) error {
	ws := sm.worldState
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Validate address
	if err := account.ValidateAddress(delegatorAddr); err != nil {
		return fmt.Errorf("invalid delegator address: %v", err)
	}

	// Get delegator account
	delegator, err := ws.accountManager.GetAccount(delegatorAddr)
	if err != nil {
		return fmt.Errorf("failed to get delegator account: %v", err)
	}

	if delegator.Rewards <= 0 {
		return fmt.Errorf("no rewards available to claim")
	}

	// Move rewards to balance
	claimedAmount := delegator.Rewards
	delegator.Balance += claimedAmount
	delegator.Rewards = 0

	// Update account
	if err := ws.accountManager.UpdateAccount(delegator); err != nil {
		return fmt.Errorf("failed to update delegator account: %v", err)
	}

	return nil
}

// StoreAsset stores an asset token in the world state
func (ws *WorldState) StoreAsset(asset *AssetToken) error {
	if asset == nil {
		return fmt.Errorf("asset cannot be nil")
	}

	if asset.ID == "" {
		return fmt.Errorf("asset ID cannot be empty")
	}

	// Initialize maps if needed
	if ws.assets == nil {
		ws.assets = make(map[string]*AssetToken)
	}
	if ws.assetBalances == nil {
		ws.assetBalances = make(map[string]map[string]int64)
	}
	if ws.assetRegistry == nil {
		ws.assetRegistry = make(map[string]string)
	}

	// Check if asset already exists
	if _, exists := ws.assets[asset.ID]; exists {
		return fmt.Errorf("asset %s already exists", asset.ID)
	}

	// Store asset
	ws.assets[asset.ID] = asset
	ws.assetBalances[asset.ID] = make(map[string]int64)
	ws.assetRegistry[asset.ID] = asset.Creator

	// Persist to storage
	assetData, err := json.Marshal(asset)
	if err != nil {
		return fmt.Errorf("failed to marshal asset: %v", err)
	}

	// Save to state storage with key prefix
	key := fmt.Sprintf("asset:%s", asset.ID)
	if err := ws.state.SaveRawData(key, assetData); err != nil {
		return fmt.Errorf("failed to save asset to storage: %v", err)
	}

	// Save asset registry entry
	registryKey := fmt.Sprintf("asset_registry:%s", asset.ID)
	if err := ws.state.SaveRawData(registryKey, []byte(asset.Creator)); err != nil {
		return fmt.Errorf("failed to save asset registry: %v", err)
	}

	return nil
}

// GetAsset retrieves an asset by ID
func (ws *WorldState) GetAsset(assetID string) (*AssetToken, error) {
	if assetID == "" {
		return nil, fmt.Errorf("asset ID cannot be empty")
	}

	// Check memory first
	if ws.assets != nil {
		if asset, exists := ws.assets[assetID]; exists {
			return asset, nil
		}
	}

	// Load from storage
	key := fmt.Sprintf("asset:%s", assetID)
	assetData, err := ws.state.GetRawData(key)
	if err != nil {
		return nil, fmt.Errorf("asset %s not found: %v", assetID, err)
	}

	var asset AssetToken
	if err := json.Unmarshal(assetData, &asset); err != nil {
		return nil, fmt.Errorf("failed to unmarshal asset: %v", err)
	}

	// Cache in memory
	if ws.assets == nil {
		ws.assets = make(map[string]*AssetToken)
	}
	ws.assets[assetID] = &asset

	return &asset, nil
}

// DeleteAsset removes an asset from the world state
func (ws *WorldState) DeleteAsset(assetID string) error {
	if assetID == "" {
		return fmt.Errorf("asset ID cannot be empty")
	}

	// Remove from memory
	if ws.assets != nil {
		delete(ws.assets, assetID)
	}
	if ws.assetBalances != nil {
		delete(ws.assetBalances, assetID)
	}
	if ws.assetRegistry != nil {
		delete(ws.assetRegistry, assetID)
	}

	// Remove from storage
	key := fmt.Sprintf("asset:%s", assetID)
	if err := ws.state.DeleteRawData(key); err != nil {
		return fmt.Errorf("failed to delete asset from storage: %v", err)
	}

	// Remove registry entry
	registryKey := fmt.Sprintf("asset_registry:%s", assetID)
	if err := ws.state.DeleteRawData(registryKey); err != nil {
		return fmt.Errorf("failed to delete asset registry: %v", err)
	}

	return nil
}

// SetAssetBalance sets the balance of an asset for an address
func (ws *WorldState) SetAssetBalance(assetID, address string, balance int64) error {
	if assetID == "" {
		return fmt.Errorf("asset ID cannot be empty")
	}
	if address == "" {
		return fmt.Errorf("address cannot be empty")
	}
	if balance < 0 {
		return fmt.Errorf("balance cannot be negative")
	}

	// Validate address format
	if err := account.ValidateAddress(address); err != nil {
		return fmt.Errorf("invalid address: %v", err)
	}

	// Check asset exists
	if _, err := ws.GetAsset(assetID); err != nil {
		return fmt.Errorf("asset does not exist: %v", err)
	}

	// Initialize maps if needed
	if ws.assetBalances == nil {
		ws.assetBalances = make(map[string]map[string]int64)
	}
	if ws.assetBalances[assetID] == nil {
		ws.assetBalances[assetID] = make(map[string]int64)
	}

	// Set balance
	if balance == 0 {
		delete(ws.assetBalances[assetID], address)
	} else {
		ws.assetBalances[assetID][address] = balance
	}

	// Persist to storage
	balanceKey := fmt.Sprintf("asset_balance:%s:%s", assetID, address)
	if balance == 0 {
		// Remove balance entry
		if err := ws.state.DeleteRawData(balanceKey); err != nil {
			return fmt.Errorf("failed to delete asset balance: %v", err)
		}
	} else {
		balanceData := make([]byte, 8)
		binary.BigEndian.PutUint64(balanceData, uint64(balance))
		if err := ws.state.SaveRawData(balanceKey, balanceData); err != nil {
			return fmt.Errorf("failed to save asset balance: %v", err)
		}
	}

	return nil
}

// GetAssetBalance gets the balance of an asset for an address
func (ws *WorldState) GetAssetBalance(assetID, address string) (int64, error) {
	if assetID == "" {
		return 0, fmt.Errorf("asset ID cannot be empty")
	}
	if address == "" {
		return 0, fmt.Errorf("address cannot be empty")
	}

	// Validate address format
	if err := account.ValidateAddress(address); err != nil {
		return 0, fmt.Errorf("invalid address: %v", err)
	}

	// Check memory first
	if ws.assetBalances != nil {
		if balances, exists := ws.assetBalances[assetID]; exists {
			if balance, exists := balances[address]; exists {
				return balance, nil
			}
		}
	}

	// Load from storage
	balanceKey := fmt.Sprintf("asset_balance:%s:%s", assetID, address)
	balanceData, err := ws.state.GetRawData(balanceKey)
	if err != nil {
		// No balance found = 0 balance
		return 0, nil
	}

	if len(balanceData) != 8 {
		return 0, fmt.Errorf("invalid balance data length")
	}

	balance := int64(binary.BigEndian.Uint64(balanceData))

	// Cache in memory
	if ws.assetBalances == nil {
		ws.assetBalances = make(map[string]map[string]int64)
	}
	if ws.assetBalances[assetID] == nil {
		ws.assetBalances[assetID] = make(map[string]int64)
	}
	ws.assetBalances[assetID][address] = balance

	return balance, nil
}

// TransferAssetBalance transfers asset tokens between addresses
func (ws *WorldState) TransferAssetBalance(assetID, from, to string, amount int64) error {
	if amount <= 0 {
		return fmt.Errorf("transfer amount must be positive")
	}

	// Get current balances
	fromBalance, err := ws.GetAssetBalance(assetID, from)
	if err != nil {
		return fmt.Errorf("failed to get sender balance: %v", err)
	}

	if fromBalance < amount {
		return fmt.Errorf("insufficient balance: have %d, need %d", fromBalance, amount)
	}

	toBalance, err := ws.GetAssetBalance(assetID, to)
	if err != nil {
		return fmt.Errorf("failed to get recipient balance: %v", err)
	}

	// Check for overflow
	if toBalance > 0 && toBalance > (9223372036854775807-amount) {
		return fmt.Errorf("transfer would cause balance overflow")
	}

	// Perform transfer
	if err := ws.SetAssetBalance(assetID, from, fromBalance-amount); err != nil {
		return fmt.Errorf("failed to update sender balance: %v", err)
	}

	if err := ws.SetAssetBalance(assetID, to, toBalance+amount); err != nil {
		// Rollback sender balance on failure
		ws.SetAssetBalance(assetID, from, fromBalance)
		return fmt.Errorf("failed to update recipient balance: %v", err)
	}

	return nil
}

// AddAssetToRegistry adds an asset to the registry for tracking
func (ws *WorldState) AddAssetToRegistry(assetID, creator, assetType string) error {
	if assetID == "" {
		return fmt.Errorf("asset ID cannot be empty")
	}
	if creator == "" {
		return fmt.Errorf("creator cannot be empty")
	}

	// Validate creator address
	if err := account.ValidateAddress(creator); err != nil {
		return fmt.Errorf("invalid creator address: %v", err)
	}

	// Initialize registry if needed
	if ws.assetRegistry == nil {
		ws.assetRegistry = make(map[string]string)
	}

	// Add to registry
	ws.assetRegistry[assetID] = creator

	// Persist to storage
	registryKey := fmt.Sprintf("asset_registry:%s", assetID)
	registryData := fmt.Sprintf("%s:%s", creator, assetType)
	if err := ws.state.SaveRawData(registryKey, []byte(registryData)); err != nil {
		return fmt.Errorf("failed to save asset registry: %v", err)
	}

	return nil
}

// GetAssetsByCreator returns all assets created by an address
func (ws *WorldState) GetAssetsByCreator(creator string) ([]*AssetToken, error) {
	if creator == "" {
		return nil, fmt.Errorf("creator address cannot be empty")
	}

	// Validate address format
	if err := account.ValidateAddress(creator); err != nil {
		return nil, fmt.Errorf("invalid creator address: %v", err)
	}

	var assets []*AssetToken

	// Check registry for assets by this creator
	if ws.assetRegistry != nil {
		for assetID, assetCreator := range ws.assetRegistry {
			if assetCreator == creator {
				asset, err := ws.GetAsset(assetID)
				if err == nil {
					assets = append(assets, asset)
				}
			}
		}
	}

	// Also scan storage for assets (in case registry is incomplete)
	// This is a more expensive operation but ensures completeness
	allAssets, err := ws.GetAllAssets()
	if err != nil {
		return assets, nil // Return what we found from registry
	}

	assetMap := make(map[string]bool)
	for _, asset := range assets {
		assetMap[asset.ID] = true
	}

	for _, asset := range allAssets {
		if asset.Creator == creator && !assetMap[asset.ID] {
			assets = append(assets, asset)
		}
	}

	return assets, nil
}

// GetAllAssets returns all assets in the system
func (ws *WorldState) GetAllAssets() ([]*AssetToken, error) {
	var assets []*AssetToken

	// Get all assets from storage
	// This requires implementing a method to scan storage with prefix
	// For now, return from memory cache
	if ws.assets != nil {
		for _, asset := range ws.assets {
			assets = append(assets, asset)
		}
	}

	return assets, nil
}

// ValidateAssetOperation validates if an asset operation is allowed
func (ws *WorldState) ValidateAssetOperation(assetID, operation, address string) error {
	asset, err := ws.GetAsset(assetID)
	if err != nil {
		return fmt.Errorf("asset not found: %v", err)
	}

	// Check expiration
	if asset.ExpirationDate != nil && *asset.ExpirationDate <= time.Now().Unix() {
		return fmt.Errorf("asset %s has expired", assetID)
	}

	switch operation {
	case "transfer":
		if !asset.Transferable {
			return fmt.Errorf("asset %s is not transferable", assetID)
		}

	case "mint":
		// Only creator can mint (in this implementation)
		if address != asset.Creator {
			return fmt.Errorf("only asset creator can mint tokens")
		}

	case "burn":
		// Only token holder or creator can burn
		balance, err := ws.GetAssetBalance(assetID, address)
		if err != nil {
			return fmt.Errorf("failed to check balance: %v", err)
		}
		if balance == 0 && address != asset.Creator {
			return fmt.Errorf("no balance to burn")
		}

	default:
		return fmt.Errorf("unknown operation: %s", operation)
	}

	return nil
}

// GetAssetInfo returns comprehensive information about an asset
func (ws *WorldState) GetAssetInfo(assetID string) (map[string]interface{}, error) {
	asset, err := ws.GetAsset(assetID)
	if err != nil {
		return nil, err
	}

	// Calculate total supply in circulation
	circulatingSupply := int64(0)
	if ws.assetBalances != nil && ws.assetBalances[assetID] != nil {
		for _, balance := range ws.assetBalances[assetID] {
			circulatingSupply += balance
		}
	}

	// Count holders
	holderCount := 0
	if ws.assetBalances != nil && ws.assetBalances[assetID] != nil {
		for _, balance := range ws.assetBalances[assetID] {
			if balance > 0 {
				holderCount++
			}
		}
	}

	return map[string]interface{}{
		"id":                 asset.ID,
		"name":               asset.Name,
		"asset_type":         asset.AssetType,
		"real_world_ref":     asset.RealWorldRef,
		"max_decimals":       asset.MaxDecimals,
		"total_supply":       asset.TotalSupply,
		"circulating_supply": circulatingSupply,
		"creator":            asset.Creator,
		"transferable":       asset.Transferable,
		"requires_approval":  asset.RequiresApproval,
		"expiration_date":    asset.ExpirationDate,
		"regulatory_info":    asset.RegulatoryInfo,
		"created_at":         asset.CreatedAt,
		"holder_count":       holderCount,
		"expired":            asset.ExpirationDate != nil && *asset.ExpirationDate <= time.Now().Unix(),
	}, nil
}

// GetAssetHolders returns all holders of an asset
func (ws *WorldState) GetAssetHolders(assetID string) (map[string]int64, error) {
	// Check asset exists
	if _, err := ws.GetAsset(assetID); err != nil {
		return nil, err
	}

	holders := make(map[string]int64)

	// Get from memory first
	if ws.assetBalances != nil && ws.assetBalances[assetID] != nil {
		for address, balance := range ws.assetBalances[assetID] {
			if balance > 0 {
				holders[address] = balance
			}
		}
	}

	return holders, nil
}

// UpdateAsset updates an existing asset (limited fields)
func (ws *WorldState) UpdateAsset(asset *AssetToken) error {
	if asset == nil {
		return fmt.Errorf("asset cannot be nil")
	}

	// Check asset exists
	existing, err := ws.GetAsset(asset.ID)
	if err != nil {
		return fmt.Errorf("asset not found: %v", err)
	}

	// Only allow updating certain fields
	updated := *existing
	updated.RegulatoryInfo = asset.RegulatoryInfo // Allow regulatory updates
	// Don't allow changing: ID, Creator, TotalSupply, AssetType, etc.

	// Store updated asset
	if ws.assets == nil {
		ws.assets = make(map[string]*AssetToken)
	}
	ws.assets[asset.ID] = &updated

	// Persist to storage
	assetData, err := json.Marshal(&updated)
	if err != nil {
		return fmt.Errorf("failed to marshal asset: %v", err)
	}

	key := fmt.Sprintf("asset:%s", asset.ID)
	if err := ws.state.SaveRawData(key, assetData); err != nil {
		return fmt.Errorf("failed to save asset to storage: %v", err)
	}

	return nil
}

// InitializeAssetMaps initializes asset-related maps (call in NewWorldState)
func (ws *WorldState) InitializeAssetMaps() {
	if ws.assets == nil {
		ws.assets = make(map[string]*AssetToken)
	}
	if ws.assetBalances == nil {
		ws.assetBalances = make(map[string]map[string]int64)
	}
	if ws.assetRegistry == nil {
		ws.assetRegistry = make(map[string]string)
	}
}

// LoadAssetsFromStorage loads all assets from storage (call in LoadState)
func (ws *WorldState) LoadAssetsFromStorage() error {
	ws.InitializeAssetMaps()

	// Load all assets (this requires implementing storage prefix scanning)
	// For now, this is a placeholder that would need storage support

	// In a full implementation, you'd scan storage with prefix "asset:"
	// and load all asset data into memory maps

	return nil
}

// SaveAssetsToStorage saves all assets to storage (call in SaveState)
func (ws *WorldState) SaveAssetsToStorage() error {
	// Assets are saved individually when created/updated
	// This method could be used for batch operations or consistency checks

	if ws.assets == nil {
		return nil
	}

	for assetID, asset := range ws.assets {
		assetData, err := json.Marshal(asset)
		if err != nil {
			return fmt.Errorf("failed to marshal asset %s: %v", assetID, err)
		}

		key := fmt.Sprintf("asset:%s", assetID)
		if err := ws.state.SaveRawData(key, assetData); err != nil {
			return fmt.Errorf("failed to save asset %s: %v", assetID, err)
		}
	}

	return nil
}

// GetAssetStatistics returns system-wide asset statistics
func (ws *WorldState) GetAssetStatistics() map[string]interface{} {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	stats := map[string]interface{}{
		"total_assets":  0,
		"asset_types":   make(map[string]int),
		"total_holders": 0,
		"total_supply":  int64(0),
	}

	if ws.assets == nil {
		return stats
	}

	assetTypes := make(map[string]int)
	totalHolders := 0
	totalSupply := int64(0)

	for _, asset := range ws.assets {
		assetTypes[asset.AssetType]++
		totalSupply += asset.TotalSupply

		// Count holders for this asset
		if ws.assetBalances != nil && ws.assetBalances[asset.ID] != nil {
			for _, balance := range ws.assetBalances[asset.ID] {
				if balance > 0 {
					totalHolders++
				}
			}
		}
	}

	stats["total_assets"] = len(ws.assets)
	stats["asset_types"] = assetTypes
	stats["total_holders"] = totalHolders
	stats["total_supply"] = totalSupply

	return stats
}

// ValidateAssetConsistency validates asset state consistency
func (ws *WorldState) ValidateAssetConsistency() error {
	if ws.assets == nil {
		return nil // No assets to validate
	}

	for assetID, asset := range ws.assets {
		// Validate asset structure
		if asset.ID != assetID {
			return fmt.Errorf("asset ID mismatch: key=%s, asset.ID=%s", assetID, asset.ID)
		}

		if asset.TotalSupply < 0 {
			return fmt.Errorf("asset %s has negative total supply: %d", assetID, asset.TotalSupply)
		}

		// Validate asset balances don't exceed total supply
		if ws.assetBalances != nil && ws.assetBalances[assetID] != nil {
			circulatingSupply := int64(0)
			for address, balance := range ws.assetBalances[assetID] {
				if balance < 0 {
					return fmt.Errorf("asset %s has negative balance for address %s: %d",
						assetID, address, balance)
				}
				circulatingSupply += balance

				// Validate address format
				if err := account.ValidateAddress(address); err != nil {
					return fmt.Errorf("asset %s has invalid holder address %s: %v",
						assetID, address, err)
				}
			}

			if circulatingSupply > asset.TotalSupply {
				return fmt.Errorf("asset %s circulating supply (%d) exceeds total supply (%d)",
					assetID, circulatingSupply, asset.TotalSupply)
			}
		}

		// Validate creator address
		if err := account.ValidateAddress(asset.Creator); err != nil {
			return fmt.Errorf("asset %s has invalid creator address: %v", assetID, err)
		}

		// Validate expiration date
		if asset.ExpirationDate != nil && *asset.ExpirationDate <= asset.CreatedAt {
			return fmt.Errorf("asset %s has expiration date before creation date", assetID)
		}
	}

	return nil
}
