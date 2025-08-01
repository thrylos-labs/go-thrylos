// core/state/worldstate.go

// Complete state management for a shard
// Cross-shard transfer handling for inter-shard communication
// State root calculation using Blake2b for Merkle state trees
// Snapshot functionality for backups and fast sync
// Consistency validation to ensure state integrity

package state

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/transaction"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"golang.org/x/crypto/blake2b"
)

// WorldState manages the global state for a shard
type WorldState struct {
	// Account management
	accountManager *account.AccountManager

	// Transaction pool and services
	txPool      *transaction.Pool
	txValidator *transaction.Validator
	txExecutor  *transaction.Executor // Add this

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

	// Synchronization
	mu sync.RWMutex
}

// NewWorldState creates a new world state for a shard
func NewWorldState(shardID account.ShardID, totalShards int, maxTxs int, cfg *config.Config, minGasPrice int64) *WorldState {
	return &WorldState{
		accountManager: account.NewAccountManager(shardID, totalShards),
		txPool:         transaction.NewPool(shardID, totalShards, maxTxs, minGasPrice),
		txValidator:    transaction.NewValidator(shardID, totalShards, cfg),
		txExecutor:     transaction.NewExecutor(shardID, totalShards),
		shardID:        shardID,
		totalShards:    totalShards,
		blocks:         make([]*core.Block, 0),
		validators:     make(map[string]*core.Validator),
		totalSupply:    0,
		totalStaked:    0,
		lastTimestamp:  time.Now().Unix(),
	}
}

// InitializeGenesis initializes the world state with genesis data
func (ws *WorldState) InitializeGenesis(genesisAccount string, initialSupply int64, genesisValidators []*core.Validator) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

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

// AddBlock adds a new block to the chain and updates state
func (ws *WorldState) AddBlock(block *core.Block) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Validate block can be added
	if err := ws.validateBlockForAddition(block); err != nil {
		return fmt.Errorf("block validation failed: %v", err)
	}

	// Execute all transactions in the block
	for _, tx := range block.Transactions {
		// Use the executor instance, not a package-level function
		receipt, err := ws.txExecutor.ExecuteTransaction(tx, ws.accountManager)
		if err != nil {
			return fmt.Errorf("failed to execute transaction %s: %v", tx.Id, err)
		}

		// Log receipt if needed (optional)
		if receipt.Status == 0 {
			return fmt.Errorf("transaction %s failed: %s", tx.Id, receipt.Error)
		}

		// Remove transaction from pool if it exists
		ws.txPool.RemoveTransaction(tx.Id)
	}

	// Add block to chain
	ws.blocks = append(ws.blocks, block)
	ws.currentHash = block.Hash
	ws.height = block.Header.Index
	ws.lastTimestamp = block.Header.Timestamp

	// Update state root
	if err := ws.updateStateRoot(); err != nil {
		return fmt.Errorf("failed to update state root: %v", err)
	}

	// Update block's state root
	block.Header.StateRoot = ws.stateRoot

	return nil
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

// GetNonce returns the nonce of an account
func (ws *WorldState) GetNonce(address string) (uint64, error) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	return ws.accountManager.GetNonce(address)
}

// AddTransaction adds a transaction to the pool
func (ws *WorldState) AddTransaction(tx *core.Transaction) error {
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
		if validator.Active {
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

// GetStatus returns a status summary of the world state
func (ws *WorldState) GetStatus() map[string]interface{} {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	return map[string]interface{}{
		"shard_id":        ws.shardID,
		"height":          ws.height,
		"current_hash":    ws.currentHash,
		"state_root":      ws.stateRoot,
		"total_supply":    ws.totalSupply,
		"total_staked":    ws.totalStaked,
		"block_count":     len(ws.blocks),
		"pending_txs":     len(ws.txPool.GetPendingTransactions()),
		"validator_count": len(ws.validators),
		"last_timestamp":  ws.lastTimestamp,
	}
}

// validateBlockForAddition validates that a block can be added to the chain
func (ws *WorldState) validateBlockForAddition(block *core.Block) error {
	if block == nil {
		return fmt.Errorf("block cannot be nil")
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

	if len(validator.Pubkey) == 0 {
		return fmt.Errorf("validator public key cannot be empty")
	}

	if validator.Stake < 0 {
		return fmt.Errorf("validator stake cannot be negative")
	}

	// Check if validator already exists
	if _, exists := ws.validators[validator.Address]; exists {
		return fmt.Errorf("validator %s already exists", validator.Address)
	}

	// Initialize validator fields if needed
	if validator.Delegators == nil {
		validator.Delegators = make(map[string]int64)
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

	// Create transfer record
	transfer := &CrossShardTransfer{
		FromShard: fromShard,
		ToShard:   toShard,
		From:      from,
		To:        to,
		Amount:    amount,
		Nonce:     nonce,
		Timestamp: time.Now().Unix(),
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

	hash := blake2b.Sum256(buf)
	transfer.Hash = fmt.Sprintf("%x", hash)

	// Debit sender account
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

	// Update sender
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

	// Credit recipient account
	recipientAccount, err := csm.worldState.GetAccount(transfer.To)
	if err != nil {
		return fmt.Errorf("failed to get recipient account: %v", err)
	}

	recipientAccount.Balance += transfer.Amount

	if err := csm.worldState.accountManager.UpdateAccount(recipientAccount); err != nil {
		return fmt.Errorf("failed to update recipient account: %v", err)
	}

	// Remove from pending transfers
	delete(csm.pendingTransfers, transferHash)

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

// StateSnapshot represents a point-in-time snapshot of the world state
type StateSnapshot struct {
	Height      int64
	StateRoot   string
	Timestamp   int64
	TotalSupply int64
	TotalStaked int64
	Accounts    map[string]*core.Account
	Validators  map[string]*core.Validator
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
		for k, v := range account.DelegatedTo {
			delegatedTo[k] = v
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
		for k, v := range validator.Delegators {
			delegators[k] = v
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
	}
}

// RestoreFromSnapshot restores the world state from a snapshot
func (ws *WorldState) RestoreFromSnapshot(snapshot *StateSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("snapshot cannot be nil")
	}

	ws.mu.Lock()
	defer ws.mu.Unlock()

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

	return nil
}

// ValidateStateConsistency validates the consistency of the world state
func (ws *WorldState) ValidateStateConsistency() error {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	// Validate account balances are non-negative
	accounts := ws.accountManager.GetAllAccounts()
	for addr, account := range accounts {
		if account.Balance < 0 {
			return fmt.Errorf("account %s has negative balance: %d", addr, account.Balance)
		}
		if account.StakedAmount < 0 {
			return fmt.Errorf("account %s has negative staked amount: %d", addr, account.StakedAmount)
		}
		if account.Rewards < 0 {
			return fmt.Errorf("account %s has negative rewards: %d", addr, account.Rewards)
		}
	}

	// Validate validator stakes
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
	}

	// Validate state root can be recalculated
	originalRoot := ws.stateRoot
	if err := ws.updateStateRoot(); err != nil {
		return fmt.Errorf("failed to recalculate state root: %v", err)
	}

	if ws.stateRoot != originalRoot {
		return fmt.Errorf("state root mismatch: stored=%s, calculated=%s", originalRoot, ws.stateRoot)
	}

	return nil
}
