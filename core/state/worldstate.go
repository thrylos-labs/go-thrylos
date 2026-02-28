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
	"log"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v3"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/block"
	"github.com/thrylos-labs/go-thrylos/core/evm"
	"github.com/thrylos-labs/go-thrylos/core/math"
	"github.com/thrylos-labs/go-thrylos/core/transaction"
	"github.com/thrylos-labs/go-thrylos/crypto/hash"
	"github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
	"github.com/thrylos-labs/go-thrylos/types"
)

const (
	stateRootEncodingVersionLegacy    uint32 = 1
	stateRootEncodingVersionCanonical uint32 = 2
)

// WorldState manages the global state for a shard
type WorldState struct {
	// Configuration
	config *config.Config

	db    *storage.DB
	state *storage.StateStorage

	// Account management
	accountManager *account.AccountManager

	// Transaction pool and services
	txPool      *transaction.Pool
	txValidator *transaction.Validator
	txExecutor  *transaction.Executor

	// ✅ KEEP: int64 is fine for counts (max 9 quintillion is enough for tx counts)
	totalTransactions int64

	// Shard configuration
	shardID     account.ShardID
	totalShards int

	currentHash string

	// ✅ KEEP: int64 is fine for block height
	height int64

	// Blockchain validators (consensus participants)
	validators map[string]*core.Validator

	// Global statistics
	totalSupply string

	// 🔴 CHANGE: Aggregates validator stakes (which are 18-decimal BigInts)
	totalStaked string

	// ✅ KEEP: Unix timestamp fits in int64
	lastTimestamp int64

	// State root for Merkle tree
	stateRoot                string
	stateRootEncodingVersion uint32

	// Cross-shard manager
	crossShardManager *CrossShardManager

	chainMu     sync.RWMutex
	validatorMu sync.RWMutex
	accountMu   *ShardedMutex

	stateRootMu sync.RWMutex

	badgerStorage *storage.BadgerStorage

	unbondingQueue []types.UnbondingEntry
	unbondingMu    sync.RWMutex

	// genesisCommitted is set to true once block 0 has been saved to the DB,
	// either via InitializeGenesis (node-1) or AddBlockFromSync (nodes 2-4).
	// Used to distinguish in-memory defaults from committed state in ImportWorldState.
	genesisCommitted bool
}

func mustStateUint256Bytes(v *big.Int) []byte {
	encoded, _ := math.BigIntToUint256Bytes(v)
	return encoded
}

func (ws *WorldState) calculateBlockHash(b *core.Block) string {
	hash, err := block.CanonicalBlockHash(b)
	if err != nil {
		panic(fmt.Sprintf("worldstate.calculateBlockHash: %v", err))
	}
	return hash
}

// InitializeFromConfig initializes the world state with config-driven genesis data
func (ws *WorldState) InitializeFromConfig() error {
	return ws.initializeFromConfig(true)
}

// InitializeFromConfigQuiet bootstraps genesis state without writing to stdout.
// Used by performance tests to keep benchmark output stable and readable.
func (ws *WorldState) InitializeFromConfigQuiet() error {
	return ws.initializeFromConfig(false)
}

func (ws *WorldState) initializeFromConfig(verbose bool) error {
	logf := func(format string, args ...interface{}) {
		if verbose {
			fmt.Printf(format, args...)
		}
	}

	logf("🔍 InitializeFromConfig: Setting up genesis state from config...\n")

	// Check if we already have accounts (existing state)
	accounts := ws.accountManager.GetAllAccounts()
	if len(accounts) > 0 {
		logf("✅ InitializeFromConfig: Existing state found (%d accounts), skipping genesis\n", len(accounts))
		return nil
	}

	// Initialize genesis accounts from config
	// ✅ FIX: Use BigInt accumulator
	totalGenesisBalanceBig := big.NewInt(0)

	// Pre-calculate BaseUnit for print statements (assuming config.BaseUnit is *big.Int)
	// If config.BaseUnit isn't available, use 10^18
	baseUnit := config.BaseUnit

	for _, genesisAccount := range ws.config.Genesis.Accounts {
		// Parse balance string to BigInt
		balanceBig := math.ParseBigInt(genesisAccount.Balance)

		// Calculate readable balance for printing (Balance / BaseUnit)
		readableBalance := new(big.Int).Div(balanceBig, baseUnit)

		logf("🏦 Creating genesis account: %s with %s tokens (%s)\n",
			genesisAccount.Address, readableBalance.String(), genesisAccount.Purpose)

		account := &core.Account{
			Address: genesisAccount.Address,
			Balance: balanceBig.Bytes(),
			Nonce:   0,
			StakedAmount: nil,
			DelegatedTo:  make(map[string][]byte),
			Rewards:      nil,
		}

		// Create account using account manager
		if err := ws.accountManager.UpdateAccount(account); err != nil {
			return fmt.Errorf("failed to create genesis account %s: %v", genesisAccount.Address, err)
		}

		// Save to persistent storage
		if err := ws.state.SaveAccount(account); err != nil {
			return fmt.Errorf("failed to save genesis account %s: %v", genesisAccount.Address, err)
		}

		// ✅ FIX: Accumulate using BigInt.Add
		totalGenesisBalanceBig.Add(totalGenesisBalanceBig, balanceBig)
	}

	// Set total supply (Convert BigInt back to string)
	ws.totalSupply = totalGenesisBalanceBig.String()

	// Genesis block uses fixed timestamp for deterministic hash
	// Align genesis to current slot boundary (6-second slots)
	currentTime := time.Now().Unix()
	genesisTimestamp := (currentTime / 6) * 6 // Round down to nearest 6-second boundary

	// Initialize genesis block (block 0)
	genesisBlock := &core.Block{
		Header: &core.BlockHeader{
			Index:                0,
			PrevHash:             "",
			Timestamp:            genesisTimestamp,
			Validator:            "",
			GasLimit:             ws.config.Consensus.MaxBlockSize,
			GasUsed:              0,
			StateRoot:            "",
			StateEncodingVersion: ws.GetStateRootEncodingVersion(),
		},
		Transactions: []*core.Transaction{},
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
	genesisBlock.Header.StateEncodingVersion = ws.GetStateRootEncodingVersion()

	// Add genesis block
	ws.currentHash = genesisBlock.Hash
	ws.height = 0
	ws.lastTimestamp = genesisBlock.Header.Timestamp

	// Save genesis state
	if err := ws.SaveState(); err != nil {
		return fmt.Errorf("failed to save genesis state: %v", err)
	}

	// Save genesis block
	// Save genesis block
	if err := ws.db.SaveBlock(genesisBlock); err != nil {
		return fmt.Errorf("failed to save genesis block: %v", err)
	}
	ws.genesisCommitted = true

	// Calculate readable total supply for print
	readableTotal := new(big.Int).Div(totalGenesisBalanceBig, baseUnit)

	logf("✅ InitializeFromConfig: Genesis state created successfully\n")
	logf("   - Total accounts: %d\n", len(ws.config.Genesis.Accounts))
	logf("   - Total supply: %s THRYLOS\n", readableTotal.String())
	logf("   - Genesis block: %s\n", genesisBlock.Hash)
	logf("   - State root: %s\n", ws.stateRoot)

	return nil
}

// NewWorldState creates a new world state for a shard with config-driven initialization
func NewWorldState(dataDir string, shardID account.ShardID, totalShards int, cfg *config.Config, badgerStorage *storage.BadgerStorage) (*WorldState, error) {
	// 1. Initialize Storage & Managers
	db := storage.NewDB(badgerStorage)
	stateStorage := storage.NewStateStorage(badgerStorage)
	acctMgr := account.NewAccountManager(stateStorage, shardID, totalShards)

	// 2. Initialize Transaction Pool & Validator
	// ✅ NOTE: Ensure transaction.NewPool signature is updated to accept (string) for minGasPrice
	txPool := transaction.NewPool(
		shardID,
		totalShards,
		cfg.Consensus.MaxTxPerBlock,
		cfg.Consensus.MinGasPrice, // Pass String
		acctMgr,
	)

	txValidator := transaction.NewValidator(shardID, totalShards, cfg)

	initialStateEncodingVersion := stateRootEncodingVersionCanonical
	if cfg != nil && cfg.Consensus.StateEncodingUpgradeHeight > 0 {
		initialStateEncodingVersion = stateRootEncodingVersionLegacy
	}

	// 3. PHASE ONE: Create WorldState struct WITHOUT Executor
	ws := &WorldState{
		config:         cfg,
		db:             db,
		state:          stateStorage,
		accountManager: acctMgr,
		txPool:         txPool,
		txValidator:    txValidator,
		// txExecutor:     nil, // Set in Phase Two
		shardID:     shardID,
		totalShards: totalShards,
		validators:  make(map[string]*core.Validator),
		totalSupply: cfg.Economics.GenesisSupply,

		// ✅ FIX: Initialize as string "0"
		totalStaked: "0",

		lastTimestamp:            time.Now().Unix(),
		stateRootEncodingVersion: initialStateEncodingVersion,

		totalTransactions: 0,
		badgerStorage:     badgerStorage,
		accountMu:         NewShardedMutex(),
	}

	// 4. PHASE TWO: Initialize REVM & Executor

	// Create REVM executor
	revmExec, err := evm.NewRevmExecutor(cfg, ws)
	if err != nil {
		return nil, fmt.Errorf("failed to create revm executor: %v", err)
	}

	// Create Transaction Executor
	ws.txExecutor = transaction.NewExecutor(
		shardID,
		totalShards,
		ws.state, // ← ADD THIS LINE (your existing state field!)
		ws,       // worldState
		txValidator,
		cfg,
		revmExec,
	)

	// 5. Initialize Cross-Shard Manager
	ws.crossShardManager = NewCrossShardManager(ws)

	return ws, nil
}

func (ws *WorldState) SetMetadata(key, value string) error {
	return ws.state.SetMetadata(key, value)
}

func (ws *WorldState) GetMetadata(key string) (string, error) {
	return ws.state.GetMetadata(key)
}

func (ws *WorldState) AtomicIncrementNonce(address string, expectedNonce uint64) (success bool, currentNonce uint64, err error) {
	// Delegate to the state storage's atomic nonce method
	return ws.state.AtomicIncrementNonce(address, expectedNonce)
}

// GetStateStorage returns the state storage handler (needed for consensus persistence)
func (ws *WorldState) GetStateStorage() *storage.StateStorage {
	return ws.state
}

// Public accessors for testing
func (ws *WorldState) UnbondingMu() *sync.RWMutex {
	return &ws.unbondingMu
}

func (ws *WorldState) UnbondingQueue() []types.UnbondingEntry {
	return ws.unbondingQueue
}

func (ws *WorldState) SetUnbondingQueue(queue []types.UnbondingEntry) {
	ws.unbondingQueue = queue
}

func (ws *WorldState) GetBadgerDB() *badger.DB {
	if ws.badgerStorage == nil {
		return nil
	}
	return ws.badgerStorage.GetDB()
}

// InitializeGenesis initializes the world state with genesis data
// ✅ UPDATE: initialSupply changed from int64 -> string
// InitializeGenesis initializes the world state with genesis data
func (ws *WorldState) InitializeGenesis(genesisAccount string, initialSupply string, genesisValidators []*core.Validator) error {
	// 1. Acquire Global Locks
	ws.chainMu.Lock()
	defer ws.chainMu.Unlock()
	ws.validatorMu.Lock()
	defer ws.validatorMu.Unlock()
	ws.stateRootMu.Lock()
	defer ws.stateRootMu.Unlock()

	// Validate genesis account address format
	if err := account.ValidateAddress(genesisAccount); err != nil {
		return fmt.Errorf("invalid genesis account address: %v", err)
	}

	// Use config supply if not specified
	if initialSupply == "" || initialSupply == "0" {
		initialSupply = ws.config.Economics.GenesisSupply
	}

	// ✅ NEW: Load ALL genesis accounts from config instead of just one
	if len(ws.config.Genesis.Accounts) > 0 {
		fmt.Printf("💰 Loading %d genesis accounts from config...\n", len(ws.config.Genesis.Accounts))

		for _, genesisAcct := range ws.config.Genesis.Accounts {
			ws.accountMu.Lock(genesisAcct.Address)
			err := ws.accountManager.CreateGenesisAccount(genesisAcct.Address, genesisAcct.Balance)
			ws.accountMu.Unlock(genesisAcct.Address)

			if err != nil {
				fmt.Printf("⚠️  Failed to create genesis account %s: %v\n", genesisAcct.Address, err)
				continue
			}

			// Display balance in THRYLOS
			balance := math.ParseBigInt(genesisAcct.Balance)
			if balance != nil {
				thrylosBalance := new(big.Float).Quo(
					new(big.Float).SetInt(balance),
					new(big.Float).SetInt(config.BaseUnit),
				)
				fmt.Printf("✅ Created genesis account %s with %s THRYLOS\n",
					genesisAcct.Address, thrylosBalance.Text('f', 2))
			}
		}
	} else {
		// Fallback: Create single genesis account (backward compatibility)
		fmt.Printf("💰 Creating single genesis account (no accounts in genesis.json)\n")
		ws.accountMu.Lock(genesisAccount)
		err := ws.accountManager.CreateGenesisAccount(genesisAccount, initialSupply)
		ws.accountMu.Unlock(genesisAccount)

		if err != nil {
			return fmt.Errorf("failed to create genesis account: %v", err)
		}
	}

	// Initialize validators
	for _, validator := range genesisValidators {
		if err := ws.addValidator(validator); err != nil {
			return fmt.Errorf("failed to add genesis validator %s: %v", validator.Address, err)
		}
	}

	// ✅ ADD THIS: Fund all validator addresses
	fmt.Printf("💰 Funding %d validators...\n", len(genesisValidators))
	for _, validator := range genesisValidators {
		balance, ok := new(big.Int).SetString("10000000000000000000000", 10) // 10M tokens
		if !ok {
			fmt.Printf("⚠️  Failed to parse balance for validator %s\n", validator.Address)
			continue
		}

		ws.accountMu.Lock(validator.Address)
		err := ws.accountManager.CreateGenesisAccount(validator.Address, balance.String())
		ws.accountMu.Unlock(validator.Address)

		if err != nil {
			fmt.Printf("⚠️  Failed to fund validator %s: %v\n", validator.Address, err)
			continue
		}

		// Display balance
		thrylosBalance := new(big.Float).Quo(
			new(big.Float).SetInt(balance),
			new(big.Float).SetInt(config.BaseUnit),
		)
		fmt.Printf("✅ Funded validator %s with %s THRYLOS\n",
			validator.Address, thrylosBalance.Text('f', 2))
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

func (ws *WorldState) AddBlockFromSync(block *core.Block) error {
	ws.chainMu.Lock()
	defer ws.chainMu.Unlock()

	if err := ws.validateBlockForAddition(block); err != nil {
		return fmt.Errorf("block validation failed: %v", err)
	}

	if block != nil && block.Header != nil && block.Header.StateEncodingVersion != 0 {
		ws.stateRootMu.Lock()
		ws.stateRootEncodingVersion = block.Header.StateEncodingVersion
		ws.stateRootMu.Unlock()
	}

	for _, tx := range block.Transactions {
		receipt, err := ws.ExecuteTransaction(tx)
		if err != nil {
			return fmt.Errorf("failed to execute transaction %s: %v", tx.Id, err)
		}
		if receipt.Status == 0 {
			return fmt.Errorf("transaction %s failed: %s", tx.Id, receipt.Error)
		}
		if err := ws.db.SaveTransactionWithIndex(tx); err != nil {
			return fmt.Errorf("failed to save transaction %s with indexing: %v", tx.Id, err)
		}
		ws.txPool.RemoveTransaction(tx.Id)
	}

	ws.currentHash = block.Hash
	ws.height = block.Header.Index
	ws.lastTimestamp = block.Header.Timestamp
	ws.totalTransactions += int64(len(block.Transactions))

	if block.Header.Index == 0 {
		ws.stateRootMu.Lock()
		ws.stateRoot = block.Header.StateRoot
		if block.Header.StateEncodingVersion != 0 {
			ws.stateRootEncodingVersion = block.Header.StateEncodingVersion
		} else {
			ws.stateRootEncodingVersion = stateRootEncodingVersionLegacy
		}
		ws.stateRootMu.Unlock()
		log.Printf("✅ AddBlockFromSync: accepted genesis stateRoot %s", block.Header.StateRoot)
	} else {
		if err := ws.updateStateRoot(); err != nil {
			return fmt.Errorf("failed to update state root during sync: %v", err)
		}
		if block.Header.StateRoot != ws.stateRoot {
			return fmt.Errorf("state root mismatch during sync: block=%s computed=%s",
				block.Header.StateRoot, ws.stateRoot)
		}
		block.Header.StateRoot = ws.stateRoot
		block.Header.StateEncodingVersion = ws.GetStateRootEncodingVersion()
		log.Printf("✅ AddBlockFromSync: verified stateRoot %s for block %d", ws.stateRoot, block.Header.Index)
	}

	if err := ws.db.SaveBlock(block); err != nil {
		return fmt.Errorf("failed to save block: %v", err)
	}
	if block.Header.Index == 0 {
		ws.genesisCommitted = true
	}
	if err := ws.db.SaveBlockByHeight(block); err != nil {
		return fmt.Errorf("failed to save block by height: %v", err)
	}

	accounts := ws.accountManager.GetAllAccounts()
	var updatedAccounts []*core.Account
	for _, account := range accounts {
		updatedAccounts = append(updatedAccounts, account)
	}

	var updatedValidators []*core.Validator
	for _, validator := range ws.validators {
		updatedValidators = append(updatedValidators, validator)
	}

	if err := ws.db.CommitBlock(block, updatedAccounts, updatedValidators, ws.totalTransactions); err != nil {
		return fmt.Errorf("failed to commit block to storage: %v", err)
	}

	return nil
}

func (ws *WorldState) AddBlock(block *core.Block) error {
	ws.chainMu.Lock()
	defer ws.chainMu.Unlock()

	if err := ws.validateBlockForAddition(block); err != nil {
		return fmt.Errorf("block validation failed: %v", err)
	}

	// ✅ FIX: Keep chainMu locked - no unlock/relock
	for _, tx := range block.Transactions {
		receipt, err := ws.ExecuteTransaction(tx)
		if err != nil {
			return fmt.Errorf("failed to execute transaction %s: %v", tx.Id, err)
		}
		if receipt.Status == 0 {
			return fmt.Errorf("transaction %s failed: %s", tx.Id, receipt.Error)
		}

		if err := ws.db.SaveTransactionWithIndex(tx); err != nil {
			return fmt.Errorf("failed to save transaction %s with indexing: %v", tx.Id, err)
		}

		ws.txPool.RemoveTransaction(tx.Id)
	}

	ws.currentHash = block.Hash
	ws.height = block.Header.Index
	ws.lastTimestamp = block.Header.Timestamp
	ws.totalTransactions += int64(len(block.Transactions))

	if err := ws.updateStateRoot(); err != nil {
		return fmt.Errorf("failed to update state root: %v", err)
	}

	block.Header.StateRoot = ws.stateRoot
	block.Header.StateEncodingVersion = ws.GetStateRootEncodingVersion()

	if err := ws.db.SaveBlock(block); err != nil {
		return fmt.Errorf("failed to save block: %v", err)
	}
	if err := ws.db.SaveBlockByHeight(block); err != nil {
		return fmt.Errorf("failed to save block by height: %v", err)
	}

	accounts := ws.accountManager.GetAllAccounts()
	var updatedAccounts []*core.Account
	for _, account := range accounts {
		updatedAccounts = append(updatedAccounts, account)
	}

	var updatedValidators []*core.Validator
	for _, validator := range ws.validators {
		updatedValidators = append(updatedValidators, validator)
	}

	if err := ws.db.CommitBlock(block, updatedAccounts, updatedValidators, ws.totalTransactions); err != nil {
		return fmt.Errorf("failed to commit block to storage: %v", err)
	}

	return nil
}

func (ws *WorldState) GetTransactionsByAddress(address string, limit int) ([]*core.Transaction, error) {
	ws.chainMu.RLock()
	defer ws.chainMu.RUnlock()

	return ws.db.GetTransactionsByAddress(address, limit)
}

// ValidateTransaction validates a transaction using the transaction validator
func (ws *WorldState) ValidateTransaction(tx *core.Transaction) error {
	// Get current height
	currentHeight := ws.GetHeight()

	// Pass transaction, height, and state reader
	return ws.txValidator.ValidateTransaction(tx, currentHeight, ws)
}

// ExecuteTransaction executes a single transaction (helper method)
// worldstate.go - Updated ExecuteTransaction
func (ws *WorldState) ExecuteTransaction(tx *core.Transaction) (*transaction.ExecutionReceipt, error) {
	// ✅ REMOVED MANUAL LOCKING
	// The executor's AtomicTransfer handles all locking internally
	// to prevent deadlock and ensure proper lock ordering

	return ws.txExecutor.ExecuteTransaction(tx, ws.accountManager)
}

// ExecuteBatchTransactions executes multiple transactions
func (ws *WorldState) ExecuteBatchTransactions(transactions []*core.Transaction) ([]*transaction.ExecutionReceipt, error) {
	// We do not hold a global lock here to allow parallelism.
	// We rely on ExecuteTransaction to lock individual accounts as needed.

	receipts := make([]*transaction.ExecutionReceipt, 0, len(transactions))

	for i, tx := range transactions {
		// Delegate to the safe execution method defined above
		receipt, err := ws.ExecuteTransaction(tx)
		if err != nil {
			// Stop batch on first error (sequential dependency)
			return receipts, fmt.Errorf("transaction %d failed: %v", i, err)
		}
		receipts = append(receipts, receipt)
	}

	return receipts, nil
}

// ImportWorldState applies accounts and validators received from a peer during genesis sync.
// Called on non-genesis nodes after syncing the genesis block, before joining consensus.
// Guards against double-application: returns an error if accounts already exist locally.
func (ws *WorldState) ImportWorldState(
	accounts map[string]*core.Account,
	validators map[string]*core.Validator,
) error {
	if len(accounts) == 0 && len(validators) == 0 {
		return fmt.Errorf("ImportWorldState: received empty snapshot, refusing to apply")
	}

	// Guard: only refuse to overwrite if genesis has been committed to the DB.
	// In-memory defaults from internal config (when genesis.json is missing) are
	// not committed state and should be replaced by a peer's full snapshot.
	existing := ws.accountManager.GetAllAccounts()
	if len(existing) > 0 && ws.genesisCommitted {
		return fmt.Errorf(
			"ImportWorldState: world state already has %d accounts, refusing to overwrite",
			len(existing),
		)
	}

	// Apply accounts via UpdateAccount, which handles both LRU cache and DB persistence.
	for _, acc := range accounts {
		if err := ws.accountManager.UpdateAccount(acc); err != nil {
			return fmt.Errorf("ImportWorldState: failed to import account %s: %w", acc.Address, err)
		}
	}

	// Apply validators directly to the in-memory map.
	// We cannot use UpdateValidator here because it rejects validators that don't already exist.
	// The in-memory state is sufficient for consensus; validators will be durably persisted
	// on the next CommitBlock call.
	ws.validatorMu.Lock()
	for addr, v := range validators {
		ws.validators[addr] = v
	}
	ws.validatorMu.Unlock()

	// Recalculate totalStaked from imported validators.
	totalStaked := new(big.Int)
	ws.validatorMu.RLock()
	for _, v := range ws.validators {
		if len(v.Stake) == 0 {
			continue
		}
		totalStaked.Add(totalStaked, math.ParseBigInt(v.Stake))
	}
	ws.validatorMu.RUnlock()

	ws.chainMu.Lock()
	ws.totalStaked = totalStaked.String()
	ws.chainMu.Unlock()

	log.Printf("✅ ImportWorldState: applied %d accounts, %d validators, totalStaked=%s",
		len(accounts), len(validators), totalStaked.String())
	return nil
}

// ValidateTransactionExecution validates that a transaction can be executed
func (ws *WorldState) ValidateTransactionExecution(tx *core.Transaction) error {
	if tx.From != "" {
		ws.accountMu.RLock(tx.From)
		defer ws.accountMu.RUnlock(tx.From)
	}

	return ws.txExecutor.ValidateExecution(tx, ws.accountManager)
}

// GetAccount retrieves an account by address
// NOTE: This function acquires accountMu[address] read lock.
// AccountManager.GetAccount() MUST NOT acquire any locks internally.
func (ws *WorldState) GetAccount(address string) (*core.Account, error) {
	ws.accountMu.RLock(address)
	defer ws.accountMu.RUnlock(address)
	return ws.accountManager.GetAccount(address)
}

// GetBalance returns the balance of an account
func (ws *WorldState) GetBalance(address string) (*big.Int, error) {
	acc, err := ws.GetAccount(address)
	if err != nil {
		return big.NewInt(0), err
	}
	return math.ParseUint256Bytes(acc.Balance)
}

// UpdateBalance updates the balance for a given address (needed for slashing)
// UpdateBalance sets the balance of an account to a specific amount
// ✅ FIX: Renamed argument 'amount' to 'newBalance' for clarity
func (ws *WorldState) UpdateBalance(address string, newBalance *big.Int) error {
	ws.accountMu.Lock(address)
	defer ws.accountMu.Unlock(address)

	// Check if nil
	if newBalance == nil {
		return fmt.Errorf("new balance cannot be nil")
	}

	// ✅ FIX: Use .Sign() to check for negative BigInt
	if newBalance.Sign() < 0 {
		return fmt.Errorf("cannot set negative balance")
	}

	// Get the account
	account, err := ws.accountManager.GetAccount(address)
	if err != nil {
		return fmt.Errorf("failed to get account %s: %w", address, err)
	}

	// ✅ FIX: Convert BigInt to string before assigning
	account.Balance = mustStateUint256Bytes(newBalance)

	// Save back using UpdateAccount
	err = ws.accountManager.UpdateAccount(account)
	if err != nil {
		return fmt.Errorf("failed to update account %s: %w", address, err)
	}

	return nil
}

// GetNonce returns the nonce of an account
func (ws *WorldState) GetNonce(address string) (uint64, error) {
	ws.accountMu.RLock(address)
	defer ws.accountMu.RUnlock(address)

	return ws.accountManager.GetNonce(address)
}

// 1. ValidateTransaction handles its own granular account locking
// 2. txPool.AddTransaction handles its own internal locking
func (ws *WorldState) AddTransaction(tx *core.Transaction) error {
	// First validate the transaction (uses sharded locks internally)
	if err := ws.ValidateTransaction(tx); err != nil {
		return fmt.Errorf("transaction validation failed: %v", err)
	}

	// Add to pool (uses pool's own mutex)
	return ws.txPool.AddTransaction(tx)
}

// GetPendingTransactions returns all pending transactions
// No WorldState lock needed (delegates to thread-safe pool)
func (ws *WorldState) GetPendingTransactions() []*core.Transaction {
	return ws.txPool.GetPendingTransactions()
}

// GetExecutableTransactions returns transactions ready for execution
// No WorldState lock needed (delegates to thread-safe pool)
func (ws *WorldState) GetExecutableTransactions(maxCount int) []*core.Transaction {
	return ws.txPool.GetExecutableTransactions(maxCount, ws.accountManager)
}

// [Helper Method: Place this near GetCurrentBlock]
// getCurrentBlockUnsafe retrieves the current block without locking.
// Caller MUST hold ws.chainMu.
func (ws *WorldState) getCurrentBlockUnsafe() *core.Block {
	// Efficiency: If we have the current hash, fetch that directly
	if ws.currentHash != "" {
		block, err := ws.db.GetBlock(ws.currentHash)
		if err == nil {
			return block
		}
	}

	// Fallback: Try fetching by height
	if ws.height >= 0 {
		block, err := ws.db.GetBlockByHeight(ws.height)
		if err == nil {
			return block
		}
	}

	return nil
}

// GetCurrentBlock returns the current (latest) block
func (ws *WorldState) GetCurrentBlock() *core.Block {
	ws.chainMu.RLock()
	defer ws.chainMu.RUnlock()

	return ws.getCurrentBlockUnsafe()
}

// [Find GetBlock around line 1008]
func (ws *WorldState) GetBlock(index int64) (*core.Block, error) {
	if index < 0 {
		return nil, fmt.Errorf("block index %d out of range", index)
	}
	// Strictly read from DB
	return ws.db.GetBlockByHeight(index)
}

// [Find GetBlockByHash around line 1009]
func (ws *WorldState) GetBlockByHash(hash string) (*core.Block, error) {
	// Strictly read from DB
	return ws.db.GetBlock(hash)
}

// GetHeight returns the current blockchain height
func (ws *WorldState) GetHeight() int64 {
	ws.chainMu.RLock()
	defer ws.chainMu.RUnlock()
	return ws.height
}

// GetStateRoot returns the current state root
func (ws *WorldState) GetStateRoot() string {
	ws.stateRootMu.RLock()
	defer ws.stateRootMu.RUnlock()

	return ws.stateRoot
}

func (ws *WorldState) GetStateRootEncodingVersion() uint32 {
	ws.stateRootMu.RLock()
	defer ws.stateRootMu.RUnlock()

	if ws.stateRootEncodingVersion == 0 {
		return stateRootEncodingVersionLegacy
	}

	return ws.stateRootEncodingVersion
}

func (ws *WorldState) GetStateRootEncodingVersionForHeight(height int64) uint32 {
	return ws.desiredStateRootEncodingVersionForHeight(height)
}

func (ws *WorldState) desiredStateRootEncodingVersionForHeight(height int64) uint32 {
	if ws == nil {
		return stateRootEncodingVersionLegacy
	}

	upgradeHeight := int64(0)
	if ws.config != nil {
		upgradeHeight = ws.config.Consensus.StateEncodingUpgradeHeight
	}

	if upgradeHeight > 0 && height >= upgradeHeight {
		return stateRootEncodingVersionCanonical
	}

	if ws.stateRootEncodingVersion != 0 {
		return ws.stateRootEncodingVersion
	}

	if upgradeHeight > 0 {
		return stateRootEncodingVersionLegacy
	}

	return stateRootEncodingVersionCanonical
}

func (ws *WorldState) applyStateRootEncodingVersionForHeight(height int64) uint32 {
	ws.stateRootMu.Lock()
	defer ws.stateRootMu.Unlock()

	version := ws.desiredStateRootEncodingVersionForHeight(height)
	ws.stateRootEncodingVersion = version

	return version
}

// AddValidator adds a validator to the state
func (ws *WorldState) AddValidator(validator *core.Validator) error {
	ws.validatorMu.Lock()
	defer ws.validatorMu.Unlock()

	// delegates to internal method which assumes lock is held
	return ws.addValidator(validator)
}

// GetValidator returns a validator by address
func (ws *WorldState) GetValidator(address string) (*core.Validator, error) {
	ws.validatorMu.RLock()
	defer ws.validatorMu.RUnlock()

	validator, exists := ws.validators[address]
	if !exists {
		return nil, fmt.Errorf("validator %s not found", address)
	}

	return validator, nil
}

func (ws *WorldState) GetActiveValidators() []*core.Validator {
	ws.validatorMu.RLock()
	defer ws.validatorMu.RUnlock()

	validators := make([]*core.Validator, 0, len(ws.validators))
	for _, v := range ws.validators {
		if v.Active {
			validators = append(validators, v)
		}
	}

	// 🔴 CRITICAL FIX: Sort validators to ensure deterministic order across all nodes
	sort.Slice(validators, func(i, j int) bool {
		return validators[i].Address < validators[j].Address
	})

	return validators
}

// UpdateValidator updates an existing validator
func (ws *WorldState) UpdateValidator(validator *core.Validator) error {
	ws.validatorMu.Lock()
	defer ws.validatorMu.Unlock()

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

// ✅ UPDATE: Returns *big.Int instead of int64
func (ws *WorldState) GetTotalSupply() *big.Int {
	ws.chainMu.RLock()
	defer ws.chainMu.RUnlock()

	// Parse string -> BigInt safely
	return math.ParseBigInt(ws.totalSupply)
}

// GetTotalStaked returns the total amount of staked tokens
// ✅ UPDATE: Returns *big.Int instead of int64
func (ws *WorldState) GetTotalStaked() *big.Int {
	ws.chainMu.RLock()
	defer ws.chainMu.RUnlock()

	// Parse string -> BigInt safely
	return math.ParseBigInt(ws.totalStaked)
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
// Updated to use Granular Locking (Chain, Validator, StateRoot)
func (ws *WorldState) GetStatus() map[string]interface{} {
	// 1. Capture Chain Data (Blocks, Supply, Height)
	ws.chainMu.RLock()
	height := ws.height
	currentHash := ws.currentHash
	totalSupply := ws.totalSupply
	totalStaked := ws.totalStaked
	totalTx := ws.totalTransactions
	blockCount := height + 1
	lastTime := ws.lastTimestamp
	ws.chainMu.RUnlock()

	// 2. Capture Validator Data
	ws.validatorMu.RLock()
	valCount := len(ws.validators)
	ws.validatorMu.RUnlock()

	// 3. Capture State Root
	ws.stateRootMu.RLock()
	root := ws.stateRoot
	ws.stateRootMu.RUnlock()

	// 4. Get Sub-component stats (These handle their own internal locking)
	poolStats := ws.txPool.GetStats()
	accountStats := ws.accountManager.GetAccountStats()

	return map[string]interface{}{
		"shard_id":           ws.shardID, // Immutable
		"height":             height,
		"current_hash":       currentHash,
		"state_root":         root,
		"total_supply":       totalSupply,
		"total_staked":       totalStaked,
		"total_transactions": totalTx,
		"block_count":        blockCount,
		"pending_txs":        poolStats.PendingCount,
		"validator_count":    valCount,
		"last_timestamp":     lastTime,
		"pool_stats":         poolStats,
		"account_stats":      accountStats,
	}
}

func (ws *WorldState) recalculateTotalTransactions() {
	ws.totalTransactions = 0

	// FIX: Iterate through blockchain height using DB instead of memory slice
	// Start from 0 up to current height
	for i := int64(0); i <= ws.height; i++ {
		block, err := ws.db.GetBlockByHeight(i)
		if err != nil {
			// If a block is missing, we stop counting to avoid panic
			fmt.Printf("⚠️ recalculateTotalTransactions: Missing block at height %d\n", i)
			break
		}
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

	// FIX: Use unsafe version to avoid Deadlock (AddBlock already holds Lock)
	currentBlock := ws.getCurrentBlockUnsafe()

	// Case 1: Genesis Block (Chain is empty or has no current block)
	if currentBlock == nil {
		if block.Header.Index != 0 {
			return fmt.Errorf("first block must be genesis (index 0), got %d", block.Header.Index)
		}
		if block.Header.PrevHash != "" {
			return fmt.Errorf("genesis block must have empty previous hash")
		}
		return nil
	}

	// Case 2: Subsequent Blocks
	// Validate chain continuity
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

	// ✅ FIX: Compare BigInts, not strings!
	stakeBig := math.ParseBigInt(validator.Stake)
	minStakeBig := math.ParseBigInt(ws.config.Staking.MinValidatorStake)

	// Compare: if stake < minStake, reject
	if stakeBig.Cmp(minStakeBig) < 0 {
		return fmt.Errorf("validator stake %s below minimum %s",
			validator.Stake, ws.config.Staking.MinValidatorStake)
	}

	// Check if validator already exists
	if _, exists := ws.validators[validator.Address]; exists {
		return fmt.Errorf("validator %s already exists", validator.Address)
	}

	// Initialize validator fields if needed
	if validator.Delegators == nil {
		validator.Delegators = make(map[string][]byte)
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
	stateEncodingVersion := ws.applyStateRootEncodingVersionForHeight(ws.height)

	// Get all accounts
	accounts := ws.accountManager.GetAllAccounts()
	addresses := make([]string, 0, len(accounts))
	for addr := range accounts {
		addresses = append(addresses, addr)
	}

	// Sort for deterministic ordering
	sort.Strings(addresses)

	// Serialize account data
	for _, addr := range addresses {
		account := accounts[addr]

		// Serialize account data
		stateData = append(stateData, []byte(account.Address)...)

		appendStateUint256(&stateData, stateEncodingVersion, account.Balance, nil)

		// Nonce is still uint64, so this remains correct
		nonceBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(nonceBytes, account.Nonce)
		stateData = append(stateData, nonceBytes...)

		appendStateUint256(&stateData, stateEncodingVersion, account.StakedAmount, nil)
		appendStateUint256(&stateData, stateEncodingVersion, account.Rewards, nil)

		// Sort delegation keys for deterministic state
		if len(account.DelegatedTo) > 0 {
			valAddrs := make([]string, 0, len(account.DelegatedTo))
			for valAddr := range account.DelegatedTo {
				valAddrs = append(valAddrs, valAddr)
			}
			sort.Strings(valAddrs)

			for _, valAddr := range valAddrs {
				amountStr := account.DelegatedTo[valAddr]

				stateData = append(stateData, []byte(valAddr)...)
				appendStateUint256(&stateData, stateEncodingVersion, amountStr, nil)
			}
		}
	}

	// Add validator data
	validatorAddresses := make([]string, 0, len(ws.validators))
	for addr := range ws.validators {
		validatorAddresses = append(validatorAddresses, addr)
	}

	sort.Strings(validatorAddresses)

	for _, addr := range validatorAddresses {
		validator := ws.validators[addr]

		stateData = append(stateData, []byte(validator.Address)...)
		stateData = append(stateData, validator.Pubkey...)

		appendStateUint256(&stateData, stateEncodingVersion, validator.Stake, nil)

		// Add active status
		if validator.Active {
			stateData = append(stateData, 1)
		} else {
			stateData = append(stateData, 0)
		}
	}

	// Calculate Blake2b hash
	hashBytes := hash.Keccak256(stateData)
	ws.stateRoot = fmt.Sprintf("%x", hashBytes)

	return nil
}

func appendStateUint256(dst *[]byte, version uint32, raw []byte, _ []byte) {
	if version >= stateRootEncodingVersionCanonical {
		value, err := math.ParseUint256Bytes(raw)
		if err == nil {
			canonical, err := math.BigIntToUint256Bytes(value)
			if err == nil {
				lengthBytes := make([]byte, 2)
				binary.BigEndian.PutUint16(lengthBytes, uint16(len(canonical)))
				*dst = append(*dst, lengthBytes...)
				*dst = append(*dst, canonical...)
				return
			}
		}
	}

	*dst = append(*dst, raw...)
}

// ValidateStateConsistency validates the consistency of the world state
func (ws *WorldState) ValidateStateConsistency() error {
	ws.chainMu.RLock()
	defer ws.chainMu.RUnlock()

	ws.validatorMu.RLock()
	defer ws.validatorMu.RUnlock()

	ws.stateRootMu.RLock()
	defer ws.stateRootMu.RUnlock()

	// Validate account balances are non-negative
	accounts := ws.accountManager.GetAllAccounts()
	for addr, acc := range accounts {
		// ✅ FIX: Parse Strings to BigInt
		balanceBig := math.ParseBigInt(acc.Balance)
		stakedBig := math.ParseBigInt(acc.StakedAmount)
		rewardsBig := math.ParseBigInt(acc.Rewards)

		// ✅ FIX: Check for negative values using .Sign() < 0
		if balanceBig.Sign() < 0 {
			return fmt.Errorf("account %s has negative balance: %s", addr, acc.Balance)
		}
		if stakedBig.Sign() < 0 {
			return fmt.Errorf("account %s has negative staked amount: %s", addr, acc.StakedAmount)
		}
		if rewardsBig.Sign() < 0 {
			return fmt.Errorf("account %s has negative rewards: %s", addr, acc.Rewards)
		}

		// Validate address format
		if err := account.ValidateAddress(addr); err != nil {
			return fmt.Errorf("account %s has invalid address format: %v", addr, err)
		}
	}

	// Validate validator stakes and addresses
	for addr, validator := range ws.validators {
		// ✅ FIX: Parse Validator Strings to BigInt
		stakeBig := math.ParseBigInt(validator.Stake)
		selfStakeBig := math.ParseBigInt(validator.SelfStake)
		delegatedStakeBig := math.ParseBigInt(validator.DelegatedStake)

		if stakeBig.Sign() < 0 {
			return fmt.Errorf("validator %s has negative stake: %s", addr, validator.Stake)
		}
		if selfStakeBig.Sign() < 0 {
			return fmt.Errorf("validator %s has negative self stake: %s", addr, validator.SelfStake)
		}
		if delegatedStakeBig.Sign() < 0 {
			return fmt.Errorf("validator %s has negative delegated stake: %s", addr, validator.DelegatedStake)
		}

		// Validate address format
		if err := account.ValidateAddress(addr); err != nil {
			return fmt.Errorf("validator %s has invalid address format: %v", addr, err)
		}

		// ✅ FIX: Parse MinValidatorStake to BigInt for comparison
		minStakeBig := math.ParseBigInt(ws.config.Staking.MinValidatorStake)

		// ✅ FIX: Compare using .Cmp()
		// Returns -1 if stakeBig < minStakeBig
		if stakeBig.Cmp(minStakeBig) < 0 {
			return fmt.Errorf("validator %s stake %s below minimum %s",
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

	return nil
}

// CrossShardTransfer represents a transfer between shards
type CrossShardTransfer struct {
	FromShard account.ShardID
	ToShard   account.ShardID
	From      string
	To        string
	Amount    string
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
// ✅ UPDATE: amount parameter changed from int64 to string
// Update InitiateTransfer to use atomic operations:
func (csm *CrossShardManager) InitiateTransfer(from, to string, amount string, nonce uint64) (*CrossShardTransfer, error) {
	csm.mu.Lock()
	defer csm.mu.Unlock()

	fromShard := account.CalculateShardID(from, csm.worldState.totalShards)
	toShard := account.CalculateShardID(to, csm.worldState.totalShards)

	if fromShard == toShard {
		return nil, fmt.Errorf("not a cross-shard transfer: both addresses in shard %d", fromShard)
	}

	if fromShard != csm.worldState.shardID {
		return nil, fmt.Errorf("can only initiate transfers from local shard %d, got %d",
			csm.worldState.shardID, fromShard)
	}

	// ✅ Use atomic transaction wrapper
	var transfer *CrossShardTransfer
	err := csm.worldState.ExecuteInTransaction([]string{from}, func() error {
		// Validate sender account
		senderAccount, err := csm.worldState.accountManager.GetAccount(from)
		if err != nil {
			return fmt.Errorf("failed to get sender account: %v", err)
		}

		balanceBig := math.ParseBigInt(senderAccount.Balance)
		amountBig := math.ParseBigInt(amount)

		if balanceBig.Cmp(amountBig) < 0 {
			return fmt.Errorf("insufficient balance: have %s, need %s", senderAccount.Balance, amount)
		}

		if senderAccount.Nonce != nonce {
			return fmt.Errorf("invalid nonce: expected %d, got %d", senderAccount.Nonce, nonce)
		}

		// Create transfer record
		transfer = &CrossShardTransfer{
			FromShard: fromShard,
			ToShard:   toShard,
			From:      from,
			To:        to,
			Amount:    amount,
			Nonce:     nonce,
			Timestamp: time.Now().Unix(),
			Status:    "pending",
		}

		// Calculate hash
		var buf []byte
		buf = append(buf, []byte(from)...)
		buf = append(buf, []byte(to)...)
		buf = append(buf, []byte(amount)...)

		nonceBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(nonceBytes, nonce)
		buf = append(buf, nonceBytes...)

		timestampBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(timestampBytes, uint64(transfer.Timestamp))
		buf = append(buf, timestampBytes...)

		hashBytes := hash.Keccak256(buf)
		transfer.Hash = fmt.Sprintf("%x", hashBytes)

		// Debit sender (atomic)
		newBalanceBig := new(big.Int).Sub(balanceBig, amountBig)
		senderAccount.Balance = mustStateUint256Bytes(newBalanceBig)

		if err := csm.worldState.accountManager.UpdateAccount(senderAccount); err != nil {
			return fmt.Errorf("failed to update sender account: %v", err)
		}

		if err := csm.worldState.state.SaveAccount(senderAccount); err != nil {
			return fmt.Errorf("failed to save sender account: %v", err)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Store pending transfer
	csm.pendingTransfers[transfer.Hash] = transfer
	return transfer, nil
}

// ExecuteAtomicAccountUpdate performs atomic updates on multiple accounts
func (ws *WorldState) ExecuteAtomicAccountUpdate(addresses []string, updateFn func(accounts map[string]*core.Account) error) error {
	if len(addresses) == 0 {
		return nil
	}

	// Use the atomic batch system
	batch := ws.accountMu.BeginBatch(addresses)
	batch.Lock()
	defer batch.Rollback()

	// Validate versions
	if !batch.ValidateVersions() {
		return fmt.Errorf("state conflict: accounts modified during update")
	}

	// Fetch all accounts
	accounts := make(map[string]*core.Account)
	for _, addr := range addresses {
		acc, err := ws.accountManager.GetAccount(addr)
		if err != nil {
			return fmt.Errorf("failed to get account %s: %w", addr, err)
		}
		accounts[addr] = acc
	}

	// Execute update function
	if err := updateFn(accounts); err != nil {
		return err
	}

	// Save all accounts
	for _, acc := range accounts {
		if err := ws.accountManager.UpdateAccount(acc); err != nil {
			return fmt.Errorf("failed to update account %s: %w", acc.Address, err)
		}
		if err := ws.state.SaveAccount(acc); err != nil {
			return fmt.Errorf("failed to save account %s: %w", acc.Address, err)
		}
	}

	batch.Commit()
	return nil
}

// CompleteTransfer completes a cross-shard transfer
// CompleteTransfer completes a cross-shard transfer
func (csm *CrossShardManager) CompleteTransfer(transferHash string) error {
	csm.mu.Lock()
	defer csm.mu.Unlock()

	transfer, exists := csm.pendingTransfers[transferHash]
	if !exists {
		return fmt.Errorf("transfer %s not found", transferHash)
	}

	if transfer.ToShard != csm.worldState.shardID {
		return fmt.Errorf("can only complete transfers to local shard %d, got %d",
			csm.worldState.shardID, transfer.ToShard)
	}

	recipientAccount, err := csm.worldState.GetAccount(transfer.To)
	if err != nil {
		recipientAccount = &core.Account{
			Address:      transfer.To,
			Balance:      nil,
			Nonce:        0,
			StakedAmount: nil,
			DelegatedTo:  make(map[string][]byte),
			Rewards:      nil,
		}
	}

	currentBal := math.ParseBigInt(recipientAccount.Balance)

	amountBig, _ := new(big.Int).SetString(transfer.Amount, 10)
	if amountBig == nil {
		return fmt.Errorf("invalid transfer amount format")
	}

	// Balance += Amount
	newBalance := new(big.Int).Add(currentBal, amountBig)
	recipientAccount.Balance = mustStateUint256Bytes(newBalance)

	if err := csm.worldState.accountManager.UpdateAccount(recipientAccount); err != nil {
		return fmt.Errorf("failed to update recipient account: %v", err)
	}

	transfer.Status = "completed"

	go func() {
		time.Sleep(time.Hour)
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
	TotalSupply string
	TotalStaked string
	Accounts    map[string]*core.Account
	Validators  map[string]*core.Validator
	Config      *config.Config
}

// CreateSnapshot creates a snapshot of the current world state
func (ws *WorldState) CreateSnapshot() *StateSnapshot {
	ws.chainMu.RLock()
	defer ws.chainMu.RUnlock()

	ws.validatorMu.RLock()
	defer ws.validatorMu.RUnlock()

	ws.stateRootMu.RLock()
	defer ws.stateRootMu.RUnlock()

	// Copy accounts
	accounts := make(map[string]*core.Account)
	for addr, account := range ws.accountManager.GetAllAccounts() {
		// Deep copy account
		delegatedTo := make(map[string][]byte)
		if account.DelegatedTo != nil {
			for k, v := range account.DelegatedTo {
				delegatedTo[k] = append([]byte(nil), v...)
			}
		}

		accounts[addr] = &core.Account{
			Address:      account.Address,
			Balance:      account.Balance,
			Nonce:        account.Nonce,
			StakedAmount: account.StakedAmount,
			DelegatedTo:  delegatedTo, // Type matches struct field
			Rewards:      account.Rewards,
			CodeHash:     append([]byte(nil), account.CodeHash...),
			StorageRoot:  append([]byte(nil), account.StorageRoot...),
		}
	}

	// Copy validators
	validators := make(map[string]*core.Validator)
	for addr, validator := range ws.validators {
		// Deep copy validator
		delegators := make(map[string][]byte)
		if validator.Delegators != nil {
			for k, v := range validator.Delegators {
				delegators[k] = append([]byte(nil), v...)
			}
		}

		validators[addr] = &core.Validator{
			Address:        validator.Address,
			Pubkey:         append([]byte(nil), validator.Pubkey...),
			Stake:          validator.Stake,
			SelfStake:      validator.SelfStake,
			DelegatedStake: validator.DelegatedStake,
			Delegators:     delegators, // Type matches struct field
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

	ws.chainMu.Lock()
	defer ws.chainMu.Unlock()

	ws.validatorMu.Lock()
	defer ws.validatorMu.Unlock()

	ws.stateRootMu.Lock()
	defer ws.stateRootMu.Unlock()

	// Validate snapshot compatibility
	if snapshot.Config != nil {
		if ws.config.Economics.GenesisSupply != snapshot.Config.Economics.GenesisSupply {
			return fmt.Errorf("incompatible genesis supply: current=%s, snapshot=%s",
				ws.config.Economics.GenesisSupply, snapshot.Config.Economics.GenesisSupply)
		}
	}

	// Clear current state
	// Pass existing state storage to account manager
	ws.accountManager = account.NewAccountManager(ws.state, ws.shardID, ws.totalShards)
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

// Around line 1450 in worldstate.go
func (sm *StakingManager) Delegate(delegatorAddr, validatorAddr string, amount *big.Int) error {
	// ✅ ADD THIS DEBUG LINE
	log.Printf("🔍 DEBUG: UnbondingPeriod = %v (should be 168h0m0s for 7 days)", sm.worldState.config.Staking.UnbondingPeriod)

	ws := sm.worldState

	// Lock order
	ws.chainMu.Lock()
	defer ws.chainMu.Unlock()

	ws.validatorMu.Lock()
	defer ws.validatorMu.Unlock()

	ws.accountMu.Lock(delegatorAddr)
	defer ws.accountMu.Unlock(delegatorAddr)

	// --- VALIDATION PHASE ---

	// ✅ Check minimum delegation
	minDelegationBig, _ := new(big.Int).SetString(ws.config.Staking.MinDelegation, 10)
	if minDelegationBig == nil {
		minDelegationBig = big.NewInt(0)
	}

	if amount.Cmp(minDelegationBig) < 0 {
		return fmt.Errorf("delegation amount %s below minimum %s",
			amount.String(), ws.config.Staking.MinDelegation)
	}

	// ✅ Get delegator account
	delegator, err := ws.accountManager.GetAccount(delegatorAddr)
	if err != nil {
		return fmt.Errorf("failed to get delegator account: %v", err)
	}

	// ✅ Get validator
	validator, exists := ws.validators[validatorAddr]
	if !exists {
		return fmt.Errorf("validator %s not found", validatorAddr)
	}

	// ✅ Check validator is active
	if !validator.Active {
		return fmt.Errorf("validator %s is not active", validatorAddr)
	}

	// ✅ Check balance
	delBalance := math.ParseBigInt(delegator.Balance)

	if delBalance.Cmp(amount) < 0 {
		return fmt.Errorf("insufficient balance: have %s, need %s",
			delegator.Balance, amount.String())
	}

	// ✅ NEW CHECK 1: Maximum delegations per validator
	if len(validator.Delegators) >= ws.config.Staking.MaxDelegationsPerValidator {
		return fmt.Errorf("validator has reached maximum delegations (%d)",
			ws.config.Staking.MaxDelegationsPerValidator)
	}

	// ✅ NEW CHECK 2: Maximum stake per validator (absolute limit)
	currentStakeBig := math.ParseBigInt(validator.Stake)
	newStakeBig := new(big.Int).Add(currentStakeBig, amount)

	maxStakeBig, _ := new(big.Int).SetString(ws.config.Staking.MaxValidatorStake, 10)
	if maxStakeBig == nil {
		// Fallback to a very large number if not configured
		maxStakeBig = new(big.Int).Mul(big.NewInt(10000000), big.NewInt(1e18)) // 10M tokens
	}

	if newStakeBig.Cmp(maxStakeBig) > 0 {
		return fmt.Errorf("delegation would exceed validator maximum stake of %s (current: %s, trying to add: %s, would be: %s)",
			ws.config.Staking.MaxValidatorStake,
			currentStakeBig.String(),
			amount.String(),
			newStakeBig.String())
	}

	// ✅ NEW CHECK 3: Maximum stake percentage (concentration limit)
	totalNetworkStakeBig, _ := new(big.Int).SetString(ws.totalStaked, 10)
	if totalNetworkStakeBig != nil && totalNetworkStakeBig.Sign() > 0 {
		// Calculate: (newValidatorStake / totalNetworkStake)
		newStakeFloat := new(big.Float).SetInt(newStakeBig)
		totalStakeFloat := new(big.Float).SetInt(totalNetworkStakeBig)
		percentageFloat := new(big.Float).Quo(newStakeFloat, totalStakeFloat)
		percentage, _ := percentageFloat.Float64()

		maxPercentage := ws.config.Staking.MaxStakePercentage
		if maxPercentage == 0 {
			maxPercentage = 0.15 // Default to 15% if not configured
		}

		if percentage > maxPercentage {
			return fmt.Errorf("delegation would exceed network stake concentration limit: validator would have %.2f%% of total stake (max: %.2f%%)",
				percentage*100, maxPercentage*100)
		}
	}

	// --- CALCULATION PHASE ---

	// Calculate new delegator balances
	newBalance := new(big.Int).Sub(delBalance, amount)
	currentStaked := math.ParseBigInt(delegator.StakedAmount)
	newStakedAmount := new(big.Int).Add(currentStaked, amount)

	// Update delegator delegation map
	if delegator.DelegatedTo == nil {
		delegator.DelegatedTo = make(map[string][]byte)
	}

	currentDelegationStr := delegator.DelegatedTo[validatorAddr]
	currentDelegationBig := math.ParseBigInt(currentDelegationStr)
	newDelegationBig := new(big.Int).Add(currentDelegationBig, amount)

	// Calculate new validator stakes
	valDelegated := math.ParseBigInt(validator.DelegatedStake)
	newValDelegated := new(big.Int).Add(valDelegated, amount)

	// Calculate new validator delegator amount
	if validator.Delegators == nil {
		validator.Delegators = make(map[string][]byte)
	}

	currentValDelStr := validator.Delegators[delegatorAddr]
	currentValDelBig := math.ParseBigInt(currentValDelStr)
	newValDelBig := new(big.Int).Add(currentValDelBig, amount)

	// Calculate new total staked
	totalStaked, _ := new(big.Int).SetString(ws.totalStaked, 10)
	if totalStaked == nil {
		totalStaked = big.NewInt(0)
	}
	newTotalStaked := new(big.Int).Add(totalStaked, amount)

	// --- UPDATE PHASE ---

	// Update delegator
	delegator.Balance = mustStateUint256Bytes(newBalance)
	delegator.StakedAmount = mustStateUint256Bytes(newStakedAmount)
	delegator.DelegatedTo[validatorAddr] = mustStateUint256Bytes(newDelegationBig)

	if err := ws.accountManager.UpdateAccount(delegator); err != nil {
		return fmt.Errorf("failed to update delegator account: %v", err)
	}

	if err := ws.state.SaveAccount(delegator); err != nil {
		return fmt.Errorf("failed to save delegator account: %v", err)
	}

	// Update validator
	validator.Delegators[delegatorAddr] = mustStateUint256Bytes(newValDelBig)
	validator.DelegatedStake = mustStateUint256Bytes(newValDelegated)
	validator.Stake = mustStateUint256Bytes(newStakeBig) // Use the validated newStakeBig
	validator.UpdatedAt = time.Now().Unix()

	// Update global state
	ws.totalStaked = newTotalStaked.String()

	return nil
}

// Undelegate unstakes tokens from a validator
// Around line 1550
func (sm *StakingManager) Undelegate(delegatorAddr, validatorAddr string, amount *big.Int) error {
	ws := sm.worldState

	// Lock order
	ws.chainMu.Lock()
	defer ws.chainMu.Unlock()

	ws.validatorMu.Lock()
	defer ws.validatorMu.Unlock()

	ws.accountMu.Lock(delegatorAddr)
	defer ws.accountMu.Unlock(delegatorAddr)

	// --- 1. Validation ---
	if amount.Sign() <= 0 {
		return fmt.Errorf("undelegation amount must be positive")
	}

	delegator, err := ws.accountManager.GetAccount(delegatorAddr)
	if err != nil {
		return fmt.Errorf("failed to get delegator account: %v", err)
	}

	if delegator.DelegatedTo == nil {
		return fmt.Errorf("no delegations found")
	}

	delegatedAmountStr, exists := delegator.DelegatedTo[validatorAddr]
	if !exists {
		return fmt.Errorf("delegation not found for validator %s", validatorAddr)
	}

	delegatedAmountBig := math.ParseBigInt(delegatedAmountStr)

	if delegatedAmountBig.Cmp(amount) < 0 {
		return fmt.Errorf("insufficient delegation: have %s, want %s",
			delegatedAmountStr, amount.String())
	}

	validator, exists := ws.validators[validatorAddr]
	if !exists {
		return fmt.Errorf("validator not found")
	}

	// --- 2. Math Operations ---
	newDelegatedToValBig := new(big.Int).Sub(delegatedAmountBig, amount)

	stakedAmountBig := math.ParseBigInt(delegator.StakedAmount)
	newStakedAmount := new(big.Int).Sub(stakedAmountBig, amount)

	valDelegatedStakeBig := math.ParseBigInt(validator.DelegatedStake)
	newValDelegatedStake := new(big.Int).Sub(valDelegatedStakeBig, amount)

	valStakeBig := math.ParseBigInt(validator.Stake)
	newValStake := new(big.Int).Sub(valStakeBig, amount)

	currentValDelegationStr := validator.Delegators[delegatorAddr]
	currentValDelegationBig := math.ParseBigInt(currentValDelegationStr)
	newValDelegationBig := new(big.Int).Sub(currentValDelegationBig, amount)

	totalStakedBig, _ := new(big.Int).SetString(ws.totalStaked, 10)
	if totalStakedBig == nil {
		totalStakedBig = big.NewInt(0)
	}
	newTotalStaked := new(big.Int).Sub(totalStakedBig, amount)

	// ✅ FIX: Do NOT add to balance immediately!
	// Funds will be added after unbonding period completes

	// --- 3. Update State (No balance change yet) ---
	delegator.StakedAmount = mustStateUint256Bytes(newStakedAmount)
	// ✅ Balance stays the same (funds in unbonding)

	if newDelegatedToValBig.Sign() == 0 {
		delete(delegator.DelegatedTo, validatorAddr)
	} else {
		delegator.DelegatedTo[validatorAddr] = mustStateUint256Bytes(newDelegatedToValBig)
	}

	validator.DelegatedStake = mustStateUint256Bytes(newValDelegatedStake)
	validator.Stake = mustStateUint256Bytes(newValStake)

	if newValDelegationBig.Sign() == 0 {
		delete(validator.Delegators, delegatorAddr)
	} else {
		validator.Delegators[delegatorAddr] = mustStateUint256Bytes(newValDelegationBig)
	}
	validator.UpdatedAt = time.Now().Unix()

	ws.totalStaked = newTotalStaked.String()

	if err := ws.accountManager.UpdateAccount(delegator); err != nil {
		return err
	}

	// ✅ FIX: Create unbonding entry instead of immediate return
	creationTime := time.Now()
	completionTime := creationTime.Add(ws.config.Staking.UnbondingPeriod)

	unbondingEntry := types.UnbondingEntry{
		DelegatorAddr:  delegatorAddr,
		ValidatorAddr:  validatorAddr,
		Amount:         amount.String(),
		CreationTime:   creationTime.Unix(),
		CompletionTime: completionTime.Unix(),
	}

	// Add to unbonding queue
	ws.unbondingMu.Lock()
	ws.unbondingQueue = append(ws.unbondingQueue, unbondingEntry)
	ws.unbondingMu.Unlock()

	log.Printf("🔓 Unbonding started: %s withdrawing %s from %s (complete at %s)",
		delegatorAddr, amount.String(), validatorAddr, completionTime.Format("2006-01-02 15:04:05"))

	return nil
}

// Add to worldstate.go

// ProcessUnbondingQueue checks for completed unbonding entries and releases funds
// Call this in your block processing logic (e.g., at the end of each block)
func (ws *WorldState) ProcessUnbondingQueue() error {
	ws.unbondingMu.Lock()
	defer ws.unbondingMu.Unlock()

	currentTime := time.Now().Unix()
	remaining := []types.UnbondingEntry{}
	processedCount := 0

	for _, entry := range ws.unbondingQueue {
		if entry.CompletionTime <= currentTime {
			// Unbonding period complete - return funds
			if err := ws.completeUnbonding(entry); err != nil {
				log.Printf("❌ ERROR: Failed to complete unbonding for %s: %v",
					entry.DelegatorAddr, err)
				// Keep in queue to retry later
				remaining = append(remaining, entry)
			} else {
				processedCount++
				log.Printf("✅ Unbonding complete: %s received %s from %s",
					entry.DelegatorAddr, entry.Amount, entry.ValidatorAddr)
			}
		} else {
			// Not ready yet, keep in queue
			remaining = append(remaining, entry)
		}
	}

	ws.unbondingQueue = remaining

	if processedCount > 0 {
		log.Printf("🔓 Processed %d unbonding completions", processedCount)
	}

	return nil
}

// completeUnbonding returns funds to the delegator after unbonding period
func (ws *WorldState) completeUnbonding(entry types.UnbondingEntry) error {
	// Lock for account update
	ws.accountMu.Lock(entry.DelegatorAddr)
	defer ws.accountMu.Unlock(entry.DelegatorAddr)

	// Get delegator account
	delegator, err := ws.accountManager.GetAccount(entry.DelegatorAddr)
	if err != nil {
		return fmt.Errorf("failed to get delegator account: %w", err)
	}

	// Parse amount
	amountBig, success := new(big.Int).SetString(entry.Amount, 10)
	if !success {
		return fmt.Errorf("invalid amount format: %s", entry.Amount)
	}

	// Add to balance
	balanceBig := math.ParseBigInt(delegator.Balance)
	newBalance := new(big.Int).Add(balanceBig, amountBig)
	delegator.Balance = mustStateUint256Bytes(newBalance)

	// Update account
	if err := ws.accountManager.UpdateAccount(delegator); err != nil {
		return fmt.Errorf("failed to update account: %w", err)
	}

	return nil
}

// GetUnbondingEntries returns all unbonding entries for a delegator
func (ws *WorldState) GetUnbondingEntries(delegatorAddr string) []types.UnbondingEntry {
	ws.unbondingMu.RLock()
	defer ws.unbondingMu.RUnlock()

	var entries []types.UnbondingEntry
	for _, entry := range ws.unbondingQueue {
		if entry.DelegatorAddr == delegatorAddr {
			entries = append(entries, entry)
		}
	}

	return entries
}

// GetTotalUnbonding returns total amount currently unbonding for a delegator
func (ws *WorldState) GetTotalUnbonding(delegatorAddr string) *big.Int {
	ws.unbondingMu.RLock()
	defer ws.unbondingMu.RUnlock()

	total := big.NewInt(0)
	for _, entry := range ws.unbondingQueue {
		if entry.DelegatorAddr == delegatorAddr {
			amount, success := new(big.Int).SetString(entry.Amount, 10)
			if success {
				total.Add(total, amount)
			}
		}
	}

	return total
}

// DistributeRewards distributes staking rewards with edge case handling
func (sm *StakingManager) DistributeRewards(totalRewardsStr string) error {
	ws := sm.worldState

	ws.chainMu.Lock()
	ws.validatorMu.RLock()
	defer ws.chainMu.Unlock()
	defer ws.validatorMu.RUnlock()

	// Parse total rewards
	totalRewardsBig, _ := new(big.Int).SetString(totalRewardsStr, 10)
	if totalRewardsBig == nil || totalRewardsBig.Sign() <= 0 {
		return fmt.Errorf("rewards must be positive")
	}

	activeValidators := ws.GetActiveValidators()
	if len(activeValidators) == 0 {
		return fmt.Errorf("no active validators")
	}

	// Calculate Total Voting Power
	totalVotingPower := big.NewInt(0)
	for _, v := range activeValidators {
		vStake := math.ParseBigInt(v.Stake)
		if vStake != nil {
			totalVotingPower.Add(totalVotingPower, vStake)
		}
	}

	if totalVotingPower.Sign() == 0 {
		return fmt.Errorf("total voting power is zero")
	}

	// ✅ Track distribution metrics
	successCount := 0
	failureCount := 0
	totalDistributed := big.NewInt(0)

	// Distribute rewards to each validator
	for _, validator := range activeValidators {
		valStake := math.ParseBigInt(validator.Stake)

		// ✅ ISSUE #8 FIX 1: Skip validators with zero or invalid stake
		if valStake == nil || valStake.Sign() == 0 {
			log.Printf("⚠️ Warning: Validator %s has zero stake, skipping reward distribution",
				validator.Address)
			continue
		}

		// Calculate validator's share
		numerator := new(big.Int).Mul(totalRewardsBig, valStake)
		validatorReward := new(big.Int).Div(numerator, totalVotingPower)

		// ✅ ISSUE #8 FIX 2: Skip if reward rounds to zero
		if validatorReward.Sign() == 0 {
			log.Printf("⚠️ Warning: Validator %s reward rounded to zero, skipping",
				validator.Address)
			continue
		}

		// Calculate commission (using big.Float for precision)
		rewardFloat := new(big.Float).SetInt(validatorReward)
		commissionRate := big.NewFloat(validator.Commission)
		commAmtFloat := new(big.Float).Mul(rewardFloat, commissionRate)
		commAmt, _ := commAmtFloat.Int(nil)
		if commAmt == nil {
			commAmt = big.NewInt(0)
		}

		delegatorReward := new(big.Int).Sub(validatorReward, commAmt)

		// Handle validator account retrieval errors
		valAcc, err := ws.accountManager.GetAccount(validator.Address)
		if err != nil {
			log.Printf("❌ ERROR: Failed to get validator account %s: %v (reward %s LOST)",
				validator.Address, err, validatorReward.String())
			failureCount++
			continue
		}

		// Update validator account with commission
		currRew := math.ParseBigInt(valAcc.Rewards)
		valAcc.Rewards = mustStateUint256Bytes(new(big.Int).Add(currRew, commAmt))

		// If no delegators, validator gets everything
		if len(validator.Delegators) == 0 {
			currentRewards := math.ParseBigInt(valAcc.Rewards)
			valAcc.Rewards = mustStateUint256Bytes(new(big.Int).Add(currentRewards, delegatorReward))
		}

		// Handle update errors
		if err := ws.accountManager.UpdateAccount(valAcc); err != nil {
			log.Printf("❌ ERROR: Failed to update validator account %s: %v (reward %s LOST)",
				validator.Address, err, validatorReward.String())
			failureCount++
			continue
		}

		// ✅ ISSUE #8 FIX 3: Track successfully distributed amount
		successCount++
		totalDistributed.Add(totalDistributed, commAmt)

		// Distribute to Delegators
		if len(validator.Delegators) > 0 {
			valDelegatedStake := math.ParseBigInt(validator.DelegatedStake)
			if valDelegatedStake != nil && valDelegatedStake.Sign() > 0 {

				for delAddr, delAmountStr := range validator.Delegators {
					delAmount := math.ParseBigInt(delAmountStr)
					if delAmount == nil {
						log.Printf("⚠️ Warning: Delegator %s has invalid stake amount", delAddr)
						continue
					}

					// Calculate delegator's share using big.Float for precision
					delStakeFloat := new(big.Float).SetInt(delAmount)
					totalDelegatedFloat := new(big.Float).SetInt(valDelegatedStake)
					shareRatio := new(big.Float).Quo(delStakeFloat, totalDelegatedFloat)

					rewardFloat := new(big.Float).SetInt(delegatorReward)
					shareFloat := new(big.Float).Mul(shareRatio, rewardFloat)
					share, _ := shareFloat.Int(nil)

					if share == nil || share.Sign() <= 0 {
						continue
					}

					// Handle delegator account errors
					delAcc, err := ws.accountManager.GetAccount(delAddr)
					if err != nil {
						log.Printf("❌ ERROR: Failed to get delegator account %s: %v (reward %s LOST)",
							delAddr, err, share.String())
						failureCount++
						continue
					}

					// Update delegator rewards
					r := math.ParseBigInt(delAcc.Rewards)
					delAcc.Rewards = mustStateUint256Bytes(new(big.Int).Add(r, share))

					// Handle delegator update errors
					if err := ws.accountManager.UpdateAccount(delAcc); err != nil {
						log.Printf("❌ ERROR: Failed to update delegator account %s: %v (reward %s LOST)",
							delAddr, err, share.String())
						failureCount++
						continue
					}

					// Track successful distribution
					successCount++
					totalDistributed.Add(totalDistributed, share)
				}
			}
		}
	}

	// Log distribution summary
	log.Printf("💰 Reward Distribution Complete:")
	log.Printf("   Total Rewards: %s", totalRewardsBig.String())
	log.Printf("   Successfully Distributed: %s", totalDistributed.String())
	log.Printf("   Success Count: %d", successCount)
	log.Printf("   Failure Count: %d", failureCount)

	// ✅ ISSUE #8 FIX 4: Calculate and log dust (rounding errors)
	dust := new(big.Int).Sub(totalRewardsBig, totalDistributed)
	if dust.Sign() > 0 {
		// Convert to human-readable THRYLOS amount
		dustFloat := new(big.Float).SetInt(dust)
		divisor := new(big.Float).SetInt64(1e18)
		thrylosAmount := new(big.Float).Quo(dustFloat, divisor)
		thrylosStr := thrylosAmount.Text('f', 6)

		log.Printf("   💰 Dust (rounding errors): %s wei (%s THRYLOS)",
			dust.String(), thrylosStr)
		log.Printf("   ℹ️  Dust will accumulate for next distribution round")
	}

	// Return error if too many failures (>10%)
	if successCount+failureCount > 0 {
		failureRate := float64(failureCount) / float64(successCount+failureCount)
		if failureRate > 0.1 {
			return fmt.Errorf("reward distribution had %d failures (%.1f%% failure rate)",
				failureCount, failureRate*100)
		}
	}

	return nil
}

// GetDelegations returns all delegations for an account
// ✅ CHANGED: Return type is now map[string]string
func (sm *StakingManager) GetDelegations(delegatorAddr string) (map[string]string, error) {
	ws := sm.worldState

	// Acquire read lock for this specific account
	ws.accountMu.RLock(delegatorAddr)
	defer ws.accountMu.RUnlock(delegatorAddr)

	account, err := ws.accountManager.GetAccount(delegatorAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %v", err)
	}

	if account.DelegatedTo == nil {
		return make(map[string]string), nil
	}

	// Return a copy to prevent external modification after unlock
	// ✅ CHANGED: Map type matches account.DelegatedTo
	delegations := make(map[string]string)
	for validator, amount := range account.DelegatedTo {
		delegations[validator] = math.BigIntToString(math.ParseBigInt(amount))
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
	ws.validatorMu.RLock()
	defer ws.validatorMu.RUnlock()

	total := big.NewInt(0)
	for _, validator := range ws.validators {
		s := math.ParseBigInt(validator.Stake)
		if s != nil {
			total.Add(total, s)
		}
	}

	ws.chainMu.Lock()
	ws.totalStaked = total.String()
	ws.chainMu.Unlock()
}

// GetValidatorCount returns the number of validators
func (ws *WorldState) GetValidatorCount() int {
	ws.validatorMu.RLock()
	defer ws.validatorMu.RUnlock()
	return len(ws.validators)
}

// GetActiveValidatorCount returns the number of active validators
func (ws *WorldState) GetActiveValidatorCount() int {
	ws.validatorMu.RLock()
	defer ws.validatorMu.RUnlock()

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
	accounts := ws.accountManager.GetAllAccounts()
	return len(accounts)
}

// Cleanup removes old completed transactions and performs maintenance
func (ws *WorldState) Cleanup() {
	// txPool handles its own locking
	maxAge := time.Hour
	ws.txPool.CleanupStaleTransactions(maxAge)

	// Update state root
	// This method acquires necessary locks
	ws.updateStateRoot()
}

func (ws *WorldState) GetCurrentHeight() int64 {
	return ws.GetHeight()
}

// ExportAccounts returns all accounts for state sync
func (ws *WorldState) ExportAccounts() map[string]*core.Account {
	// Iterates DB, safe
	return ws.accountManager.GetAllAccounts()
}

// ExportValidators returns all validators for state sync
func (ws *WorldState) ExportValidators() map[string]*core.Validator {
	ws.validatorMu.RLock()
	defer ws.validatorMu.RUnlock()

	validators := make(map[string]*core.Validator)
	for addr, validator := range ws.validators {
		// Deep copy
		// ✅ CHANGED: Map type is now map[string]string
		delegators := make(map[string][]byte)
		if validator.Delegators != nil {
			for k, v := range validator.Delegators {
				delegators[k] = append([]byte(nil), v...)
			}
		}

		validators[addr] = &core.Validator{
			Address:        validator.Address,
			Pubkey:         append([]byte(nil), validator.Pubkey...),
			Stake:          validator.Stake,
			SelfStake:      validator.SelfStake,
			DelegatedStake: validator.DelegatedStake,
			Delegators:     delegators, // Now matches the struct field type
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

// StakeExport represents a single delegation record.
// We use a flat structure or nested structure, but it MUST be in a slice.
type StakeExport struct {
	DelegatorAddr string `json:"delegator_addr"`
	ValidatorAddr string `json:"validator_addr"`
	Amount        string `json:"amount"`
}

// ExportStakes returns a deterministically sorted list of all stakes.
// CHANGED: Return type is now []*StakeExport instead of map.
func (ws *WorldState) ExportStakes() ([]*StakeExport, error) {
	// 1. Get all accounts
	accounts := ws.accountManager.GetAllAccounts()

	// 2. Sort Account Keys (Determinism Step A)
	// Go map iteration is random, so we must collect keys and sort them.
	var accountKeys []string
	for addr := range accounts {
		accountKeys = append(accountKeys, addr)
	}
	sort.Strings(accountKeys)

	// Initialize the slice
	var exportData []*StakeExport

	// 3. Iterate Sorted Accounts
	for _, delegatorAddr := range accountKeys {
		account := accounts[delegatorAddr]

		// Skip if no delegations
		if account.DelegatedTo == nil || len(account.DelegatedTo) == 0 {
			continue
		}

		// 4. Sort Delegation Keys (Determinism Step B)
		// We must also sort the inner map (Validators)
		var validatorKeys []string
		for valAddr := range account.DelegatedTo {
			validatorKeys = append(validatorKeys, valAddr)
		}
		sort.Strings(validatorKeys)

		// 5. Build Ordered List
		for _, valAddr := range validatorKeys {
			amount := account.DelegatedTo[valAddr]

			exportData = append(exportData, &StakeExport{
				DelegatorAddr: delegatorAddr,
				ValidatorAddr: valAddr,
				Amount:        math.BigIntToString(math.ParseBigInt(amount)),
			})
		}
	}

	return exportData, nil
}

// Clear clears the world state (for restoring from snapshot)
func (ws *WorldState) Clear() error {
	ws.chainMu.Lock()
	defer ws.chainMu.Unlock()

	ws.validatorMu.Lock()
	defer ws.validatorMu.Unlock()

	ws.stateRootMu.Lock()
	defer ws.stateRootMu.Unlock()

	// Reset account manager
	ws.accountManager = account.NewAccountManager(ws.state, ws.shardID, ws.totalShards)

	// Clear validators
	ws.validators = make(map[string]*core.Validator)

	// Reset state
	ws.currentHash = ""
	ws.height = -1
	ws.stateRoot = ""

	// ✅ FIX: Assign string "0" instead of int 0
	ws.totalSupply = "0"
	ws.totalStaked = "0"

	ws.lastTimestamp = 0

	// Recreate transaction pool
	ws.txPool = transaction.NewPool(
		ws.shardID,
		ws.totalShards,
		ws.config.Consensus.MaxTxPerBlock,
		ws.config.Consensus.MinGasPrice,
		ws.accountManager,
	)

	return nil
}

// SetAccount sets an account in the world state
func (ws *WorldState) SetAccount(address string, account *core.Account) error {
	ws.accountMu.Lock(address)
	defer ws.accountMu.Unlock(address)

	return ws.accountManager.UpdateAccount(account)
}

// SetValidator sets a validator in the world state
func (ws *WorldState) SetValidator(address string, validator *core.Validator) error {
	ws.validatorMu.Lock()
	defer ws.validatorMu.Unlock()

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
// ✅ UPDATE: amount changed from int64 to string
func (ws *WorldState) SetStake(delegatorAddr, validatorAddr string, amount string) error {
	// 1. Lock delegator account
	ws.accountMu.Lock(delegatorAddr)
	defer ws.accountMu.Unlock(delegatorAddr)

	// Get or create delegator account
	delegator, err := ws.accountManager.GetAccount(delegatorAddr)
	if err != nil {
		// Create new account if it doesn't exist
		delegator = &core.Account{
			Address:      delegatorAddr,
			Balance:      nil,
			Nonce:        0,   // Nonce remains integer (uint64)
			StakedAmount: nil,
			DelegatedTo:  make(map[string][]byte),
			Rewards:      nil,
		}
	}

	// Initialize DelegatedTo map if nil
	if delegator.DelegatedTo == nil {
		delegator.DelegatedTo = make(map[string][]byte)
	}

	// Parse amount to BigInt for checking
	amountBig := math.ParseBigInt(amount)

	// Check if amount > 0
	if amountBig.Sign() > 0 {
		delegator.DelegatedTo[validatorAddr] = append([]byte(nil), amount...)

		// Update Total Staked Amount: StakedAmount + amount
		currentStakedBig := math.ParseBigInt(delegator.StakedAmount)
		currentStakedBig.Add(currentStakedBig, amountBig)

		delegator.StakedAmount = mustStateUint256Bytes(currentStakedBig)
	} else {
		// Remove delegation if amount is 0
		delete(delegator.DelegatedTo, validatorAddr)
	}

	// Update account
	return ws.accountManager.UpdateAccount(delegator)
}

// SetStateRoot sets the state root
func (ws *WorldState) SetStateRoot(stateRoot string) error {
	return ws.SetStateRootWithVersion(stateRoot, ws.GetStateRootEncodingVersion())
}

func (ws *WorldState) SetStateRootWithVersion(stateRoot string, version uint32) error {
	ws.stateRootMu.Lock()
	defer ws.stateRootMu.Unlock()

	ws.stateRoot = stateRoot
	if version != 0 {
		ws.stateRootEncodingVersion = version
	}
	return nil
}

// SetCurrentHeight sets the current height
func (ws *WorldState) SetCurrentHeight(height int64) error {
	ws.chainMu.Lock()
	defer ws.chainMu.Unlock()

	ws.height = height
	return nil
}

// [Find PruneStatesBefore around line 1079]
func (ws *WorldState) PruneStatesBefore(height int64) (int, error) {
	// The in-memory block slice has been removed to prevent memory leaks.
	// This function is now a no-op for memory, but serves as a hook
	// for future DB pruning logic.
	return 0, nil
}

func (ws *WorldState) SaveState() error {
	// 1. Acquire Chain Lock (Height, Root)
	ws.chainMu.RLock()
	height := ws.height
	// stateRoot := ws.stateRoot // Use GetStateRoot or lock stateRootMu if separate
	ws.chainMu.RUnlock()

	ws.stateRootMu.RLock()
	stateRoot := ws.stateRoot
	ws.stateRootMu.RUnlock()

	// Save current height
	if err := ws.state.SaveHeight(height); err != nil {
		return fmt.Errorf("failed to save height: %v", err)
	}

	// Save state root
	if err := ws.state.SaveStateRoot(stateRoot); err != nil {
		return fmt.Errorf("failed to save state root: %v", err)
	}
	if err := ws.state.SaveStateRootEncodingVersion(ws.GetStateRootEncodingVersion()); err != nil {
		return fmt.Errorf("failed to save state root encoding version: %v", err)
	}

	// Save all accounts
	// Note: GetAllAccounts iterates DB, so it doesn't use the cache lock directly
	// but it's safe as long as DB is consistent
	accounts := ws.accountManager.GetAllAccounts()
	for _, account := range accounts {
		if err := ws.state.SaveAccount(account); err != nil {
			return fmt.Errorf("failed to save account %s: %v", account.Address, err)
		}
	}

	// Save all validators
	ws.validatorMu.RLock()
	defer ws.validatorMu.RUnlock()

	for _, validator := range ws.validators {
		if err := ws.state.SaveValidator(validator); err != nil {
			return fmt.Errorf("failed to save validator %s: %v", validator.Address, err)
		}
	}

	return nil
}

// LoadState loads the world state from storage
func (ws *WorldState) LoadState() error {
	// Acquire locks in standard order: Chain -> Validators -> Accounts
	ws.chainMu.Lock()
	ws.validatorMu.Lock()

	defer ws.validatorMu.Unlock()
	defer ws.chainMu.Unlock()

	fmt.Printf("🔍 LoadState: Attempting to load state from storage...\n")

	// Load height
	height, err := ws.state.GetHeight()
	if err != nil {
		fmt.Printf("🔍 LoadState: No height found (fresh database): %v\n", err)
		return fmt.Errorf("no existing state found (fresh database)")
	}

	fmt.Printf("🔍 LoadState: Found height: %d\n", height)
	if height >= 0 {
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

	stateRootEncodingVersion, err := ws.state.GetStateRootEncodingVersion()
	if err != nil {
		return fmt.Errorf("failed to load state root encoding version: %v", err)
	}
	if stateRootEncodingVersion == 0 {
		stateRootEncodingVersion = stateRootEncodingVersionLegacy
	}
	ws.stateRootEncodingVersion = stateRootEncodingVersion

	// Load total transactions
	totalTx, err := ws.state.GetTotalTransactions()
	if err != nil {
		fmt.Printf("🔍 LoadState: No transaction count found (will calculate from blocks): %v\n", err)
		ws.totalTransactions = 0
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

	// Reset account manager
	ws.accountManager = account.NewAccountManager(ws.state, ws.shardID, ws.totalShards)

	// ✅ FIX: Initialize Total Supply as BigInt
	totalSupplyBig := big.NewInt(0)

	for _, acc := range accounts {
		if err := ws.accountManager.UpdateAccount(acc); err != nil {
			return fmt.Errorf("failed to restore account %s: %v", acc.Address, err)
		}

		// ✅ FIX: Parse String Fields to BigInt
		balanceBig := math.ParseBigInt(acc.Balance)
		stakedBig := math.ParseBigInt(acc.StakedAmount)
		rewardsBig := math.ParseBigInt(acc.Rewards)

		// Calculate Account Total (Balance + Staked + Rewards)
		// Note: Using a temporary sum variable to avoid mutating the original pointers accidentally
		accountTotal := new(big.Int).Add(balanceBig, stakedBig)
		accountTotal.Add(accountTotal, rewardsBig)

		// Add to Total Supply
		totalSupplyBig.Add(totalSupplyBig, accountTotal)
	}

	// ✅ FIX: Assign BigInt String to WorldState
	ws.totalSupply = totalSupplyBig.String()

	// Load all validators
	validators, err := ws.state.GetAllValidators()
	if err != nil {
		fmt.Printf("🔍 LoadState: Error loading validators: %v\n", err)
		return fmt.Errorf("failed to load validators: %v", err)
	}

	fmt.Printf("🔍 LoadState: Found %d validators\n", len(validators))

	// Clear and reload validators
	ws.validators = make(map[string]*core.Validator)

	// ✅ FIX: Initialize Total Staked as BigInt
	totalStakedBig := big.NewInt(0)

	for address, validator := range validators {
		ws.validators[address] = validator

		// ✅ FIX: Parse Validator Stake String
		stakeBig := math.ParseBigInt(validator.Stake)
		totalStakedBig.Add(totalStakedBig, stakeBig)
	}

	// ✅ FIX: Assign BigInt String to WorldState
	ws.totalStaked = totalStakedBig.String()

	// Check height instead of ws.blocks length
	if ws.totalTransactions == 0 && ws.height >= 0 {
		fmt.Printf("🔍 LoadState: Calculating transaction count from height %d...\n", ws.height)
		ws.recalculateTotalTransactions()

		if err := ws.state.SaveTotalTransactions(ws.totalTransactions); err != nil {
			fmt.Printf("⚠️  LoadState: Failed to save calculated transaction count: %v\n", err)
		} else {
			fmt.Printf("✅ LoadState: Saved calculated transaction count: %d\n", ws.totalTransactions)
		}
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
	// Acquire lock for specific account
	ws.accountMu.Lock(account.Address)
	defer ws.accountMu.Unlock(account.Address)

	// Update in memory
	if err := ws.accountManager.UpdateAccount(account); err != nil {
		return err
	}

	// Persist to storage
	return ws.state.SaveAccount(account)
}

// Modified validator operations to persist to storage
func (ws *WorldState) UpdateValidatorWithStorage(validator *core.Validator) error {
	ws.validatorMu.Lock()
	defer ws.validatorMu.Unlock()

	// Update in memory
	ws.validators[validator.Address] = validator

	// Persist to storage
	return ws.state.SaveValidator(validator)
}

// Add to StakingManager in worldstate.go
func (sm *StakingManager) ClaimRewards(delegatorAddr string) error {
	ws := sm.worldState

	// Acquire account lock first
	ws.accountMu.Lock(delegatorAddr)
	defer ws.accountMu.Unlock(delegatorAddr)

	// Validate address
	if err := account.ValidateAddress(delegatorAddr); err != nil {
		return fmt.Errorf("invalid delegator address: %v", err)
	}

	// Get delegator account
	delegator, err := ws.accountManager.GetAccount(delegatorAddr)
	if err != nil {
		return fmt.Errorf("failed to get delegator account: %v", err)
	}

	// 1. Parse Rewards (String -> BigInt)
	rewardsBig := math.ParseBigInt(delegator.Rewards)

	// Check if rewards <= 0
	if rewardsBig.Sign() <= 0 {
		return fmt.Errorf("no rewards available to claim")
	}

	// 2. Parse Balance
	balanceBig := math.ParseBigInt(delegator.Balance)

	// 3. Add: Balance + Rewards
	balanceBig.Add(balanceBig, rewardsBig)

	delegator.Balance = mustStateUint256Bytes(balanceBig)
	delegator.Rewards = nil

	if err := ws.accountManager.UpdateAccount(delegator); err != nil {
		return fmt.Errorf("failed to update delegator account: %v", err)
	}

	return nil
}

// GetContractCode returns the bytecode of a contract
func (ws *WorldState) GetContractCode(address string) ([]byte, error) {
	key := []byte("code:" + address)
	code, err := ws.db.Get(key)
	if err != nil {
		return nil, err
	}
	return code, nil
}

// SetContractCode stores contract bytecode
func (ws *WorldState) SetContractCode(address string, code []byte) error {
	key := []byte("code:" + address)
	return ws.db.Put(key, code)
}

// GetContractStorage returns a storage value
func (ws *WorldState) GetContractStorage(address, key string) ([]byte, error) {
	storageKey := []byte("storage:" + address + ":" + key)
	value, err := ws.db.Get(storageKey)
	if err != nil {
		return make([]byte, 32), nil // Return zero if not found
	}
	return value, nil
}

// SetContractStorage sets a storage value
func (ws *WorldState) SetContractStorage(address, key string, value []byte) error {
	storageKey := []byte("storage:" + address + ":" + key)
	return ws.db.Put(storageKey, value)
}

// SetNonce sets account nonce
func (ws *WorldState) SetNonce(address string, nonce uint64) error {
	account, err := ws.GetAccount(address)
	if err != nil {
		return err
	}
	account.Nonce = nonce

	// FIX: Call the accountManager explicitly
	return ws.accountManager.UpdateAccount(account)
}

// GetAccountMutex returns the sharded mutex for account locking
func (ws *WorldState) GetAccountMutex() *ShardedMutex {
	return ws.accountMu
}

// AtomicTransfer performs an atomic transfer between two accounts
func (ws *WorldState) AtomicTransfer(fromAddr, toAddr string, updateFunc func(sender, receiver *core.Account) error) error {
	// ✅ FIX: Deduplicate addresses for self-transfers
	addresses := []string{fromAddr}
	if toAddr != fromAddr {
		addresses = append(addresses, toAddr)
	}

	batch := ws.accountMu.BeginBatch(addresses)
	batch.Lock()
	defer batch.Rollback()

	sender, err := ws.accountManager.GetAccount(fromAddr)
	if err != nil {
		return fmt.Errorf("failed to get sender account: %w", err)
	}

	// ✅ FIX: Handle self-transfer
	var receiver *core.Account
	if toAddr == fromAddr {
		receiver = sender // Same account
	} else {
		receiver, err = ws.accountManager.GetAccount(toAddr)
		if err != nil {
			return fmt.Errorf("failed to get receiver account: %w", err)
		}
	}

	if !batch.ValidateVersions() {
		return fmt.Errorf("state conflict: accounts modified during transfer")
	}

	if err := updateFunc(sender, receiver); err != nil {
		return err
	}

	// Update sender
	if err := ws.accountManager.UpdateAccount(sender); err != nil {
		return fmt.Errorf("failed to update sender: %w", err)
	}
	if err := ws.state.SaveAccount(sender); err != nil {
		return fmt.Errorf("failed to save sender: %w", err)
	}

	// ✅ FIX: Only update receiver if it's a different account
	if toAddr != fromAddr {
		if err := ws.accountManager.UpdateAccount(receiver); err != nil {
			return fmt.Errorf("failed to update receiver: %w", err)
		}
		if err := ws.state.SaveAccount(receiver); err != nil {
			return fmt.Errorf("failed to save receiver: %w", err)
		}
	}

	batch.Commit()
	return nil
}

// ExecuteInTransaction executes a function with transaction-like semantics
func (ws *WorldState) ExecuteInTransaction(addresses []string, fn func() error) error {
	batch := ws.accountMu.BeginBatch(addresses)
	batch.Lock()
	defer batch.Rollback()

	if !batch.ValidateVersions() {
		return fmt.Errorf("transaction conflict: state modified by concurrent transaction")
	}

	if err := fn(); err != nil {
		return err
	}

	batch.Commit()
	return nil
}

// GetAccountsSnapshot returns a consistent snapshot of multiple accounts
func (ws *WorldState) GetAccountsSnapshot(addresses []string) ([]*core.Account, error) {
	if len(addresses) == 0 {
		return nil, nil
	}

	ws.accountMu.RLockMultiple(addresses)
	defer ws.accountMu.RUnlockMultiple(addresses)

	accounts := make([]*core.Account, 0, len(addresses))
	for _, addr := range addresses {
		acc, err := ws.accountManager.GetAccount(addr)
		if err != nil {
			return nil, fmt.Errorf("failed to get account %s: %w", addr, err)
		}
		accounts = append(accounts, acc)
	}

	return accounts, nil
}

// GetAllValidators retrieves all validators (active, inactive, and jailed) from the world state.
// This is critical for emergency recovery when no active validators exist.
func (ws *WorldState) GetAllValidators() []*core.Validator {
	ws.validatorMu.RLock()
	defer ws.validatorMu.RUnlock()

	allValidators := make([]*core.Validator, 0, len(ws.validators))
	for _, v := range ws.validators {
		allValidators = append(allValidators, v)
	}

	return allValidators
}

// SimulateStateRoot executes the block's transactions against an in-memory overlay
// and returns the state root that results, without committing any changes.
// This is used by ValidateBlock to verify that a proposer's claimed state root is correct.
func (ws *WorldState) SimulateStateRoot(block *core.Block) (string, error) {
	// Take a read lock on chain state for the duration of simulation.
	// This prevents the real state from changing under us while we read accounts.
	ws.chainMu.RLock()
	defer ws.chainMu.RUnlock()

	log.Printf("🔬 SimulateStateRoot: block=%d, txs=%d", block.Header.Index, len(block.Transactions))

	store := newSimulationStore(ws)
	executor := &simulationExecutor{store: store}

	for _, tx := range block.Transactions {
		if err := executor.applyTransaction(tx); err != nil {
			// A simulation failure means the block contains an invalid transaction.
			// Return the error so ValidateBlock can reject the block.
			return "", fmt.Errorf("SimulateStateRoot: tx %s failed: %w", tx.Id, err)
		}
	}

	return ws.calculateStateRootFromOverlay(store)
}

// calculateStateRootFromOverlay runs the same hashing logic as updateStateRoot,
// but merges the simulation overlay with the real account set before hashing.
func (ws *WorldState) calculateStateRootFromOverlay(store *simulationStore) (string, error) {
	// Build the merged account map: real accounts + overlay (overlay wins on conflict)
	realAccounts := ws.accountManager.GetAllAccounts()

	merged := make(map[string]*core.Account, len(realAccounts)+len(store.overlay))
	for addr, acc := range realAccounts {
		merged[addr] = acc
	}
	// Overlay accounts overwrite real ones — these are the post-execution versions
	for addr, acc := range store.overlay {
		merged[addr] = acc
	}

	// Sort for deterministic ordering — mirrors updateStateRoot exactly
	addresses := make([]string, 0, len(merged))
	for addr := range merged {
		addresses = append(addresses, addr)
	}
	sort.Strings(addresses)

	var stateData []byte
	stateEncodingVersion := ws.desiredStateRootEncodingVersionForHeight(ws.height)

	for _, addr := range addresses {
		acc := merged[addr]
		stateData = append(stateData, []byte(acc.Address)...)
		appendStateUint256(&stateData, stateEncodingVersion, acc.Balance, nil)

		nonceBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(nonceBytes, acc.Nonce)
		stateData = append(stateData, nonceBytes...)

		appendStateUint256(&stateData, stateEncodingVersion, acc.StakedAmount, nil)
		appendStateUint256(&stateData, stateEncodingVersion, acc.Rewards, nil)

		if len(acc.DelegatedTo) > 0 {
			valAddrs := make([]string, 0, len(acc.DelegatedTo))
			for valAddr := range acc.DelegatedTo {
				valAddrs = append(valAddrs, valAddr)
			}
			sort.Strings(valAddrs)
			for _, valAddr := range valAddrs {
				stateData = append(stateData, []byte(valAddr)...)
				appendStateUint256(&stateData, stateEncodingVersion, acc.DelegatedTo[valAddr], nil)
			}
		}
	}

	// Add validator data — same as updateStateRoot
	ws.validatorMu.RLock()
	validatorAddresses := make([]string, 0, len(ws.validators))
	for addr := range ws.validators {
		validatorAddresses = append(validatorAddresses, addr)
	}

	sort.Strings(validatorAddresses)

	for _, addr := range validatorAddresses {
		v := ws.validators[addr]
		stateData = append(stateData, []byte(v.Address)...)
		stateData = append(stateData, v.Pubkey...)
		appendStateUint256(&stateData, stateEncodingVersion, v.Stake, nil)
		if v.Active {
			stateData = append(stateData, 1)
		} else {
			stateData = append(stateData, 0)
		}
	}
	ws.validatorMu.RUnlock()

	hashBytes := hash.Keccak256(stateData)
	return fmt.Sprintf("%x", hashBytes), nil
}
