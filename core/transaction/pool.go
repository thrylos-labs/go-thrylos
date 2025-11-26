package transaction

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// Pool manages pending transactions for a shard
type Pool struct {
	// Transaction storage
	pending   map[string]*core.Transaction            // txid -> tx
	byAddress map[string]map[uint64]*core.Transaction // address -> nonce -> tx (Changed for O(1) lookup)
	byHash    map[string]*core.Transaction            // hash -> tx for quick lookup

	// Dependencies
	accountManager *account.AccountManager // Needed for balance checks

	// Configuration
	shardID     account.ShardID
	totalShards int
	maxTxs      int
	minGasPrice int64

	// Statistics
	totalAdded   int64
	totalRemoved int64

	// Synchronization
	mu sync.RWMutex
}

// PoolStats represents statistics about the transaction pool
type PoolStats struct {
	PendingCount int   `json:"pending_count"`
	AddressCount int   `json:"address_count"`
	TotalAdded   int64 `json:"total_added"`
	TotalRemoved int64 `json:"total_removed"`
	ShardID      int   `json:"shard_id"`
	MaxCapacity  int   `json:"max_capacity"`
	MinGasPrice  int64 `json:"min_gas_price"`
}

// NewPool creates a new transaction pool for a shard
// NOTE: Added accountManager to constructor to enable balance validation
func NewPool(shardID account.ShardID, totalShards int, maxTxs int, minGasPrice int64, am *account.AccountManager) *Pool {
	return &Pool{
		pending:        make(map[string]*core.Transaction),
		byAddress:      make(map[string]map[uint64]*core.Transaction),
		byHash:         make(map[string]*core.Transaction),
		accountManager: am,
		shardID:        shardID,
		totalShards:    totalShards,
		maxTxs:         maxTxs,
		minGasPrice:    minGasPrice,
	}
}

// AddTransaction adds a transaction to the pool after validation
func (p *Pool) AddTransaction(tx *core.Transaction) error {
	if err := p.validateTransactionForPool(tx); err != nil {
		return fmt.Errorf("transaction validation failed: %v", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// 1. Check if transaction already exists (Id or Hash)
	if _, exists := p.pending[tx.Id]; exists {
		return fmt.Errorf("transaction %s already exists in pool", tx.Id)
	}
	if _, exists := p.byHash[tx.Hash]; exists {
		return fmt.Errorf("transaction with hash %s already exists in pool", tx.Hash)
	}

	// 2. Initialize sender map if needed
	senderTxs, exists := p.byAddress[tx.From]
	if !exists {
		senderTxs = make(map[uint64]*core.Transaction)
		p.byAddress[tx.From] = senderTxs
	}

	// 3. Check for Duplicate Nonce (Replace-by-Fee logic)
	if existingTx, conflict := senderTxs[tx.Nonce]; conflict {
		// Only allow replacement if new tx has higher gas price
		if tx.GasPrice <= existingTx.GasPrice {
			return fmt.Errorf("nonce %d already exists; replacement requires higher gas price (existing: %d, new: %d)",
				tx.Nonce, existingTx.GasPrice, tx.GasPrice)
		}

		// Log replacement
		fmt.Printf("♻️ Pool: Replacing tx %s with %s (nonce %d, higher gas price)\n",
			existingTx.Id, tx.Id, tx.Nonce)

		// Remove old transaction internally
		p.removeInternal(existingTx)
	}

	// 4. Validate Total Pending Balance
	// Ensure user has enough balance for THIS + ALL other pending transactions
	if err := p.validateTotalPendingBalance(tx.From, tx); err != nil {
		return fmt.Errorf("insufficient balance for pending transactions: %v", err)
	}

	// 5. Check Capacity
	if len(p.pending) >= p.maxTxs {
		if !p.evictLowestGasPrice(tx.GasPrice) {
			return fmt.Errorf("transaction pool is full and cannot evict lower gas price transactions")
		}
	}

	// 6. Add to all indices
	p.pending[tx.Id] = tx
	p.byHash[tx.Hash] = tx

	// Re-fetch senderTxs in case it was modified/deleted during eviction/removal
	if p.byAddress[tx.From] == nil {
		p.byAddress[tx.From] = make(map[uint64]*core.Transaction)
	}
	p.byAddress[tx.From][tx.Nonce] = tx

	p.totalAdded++
	return nil
}

// validateTotalPendingBalance calculates the total cost of all pending transactions plus the new one
// and checks if the account balance is sufficient.
func (p *Pool) validateTotalPendingBalance(address string, newTx *core.Transaction) error {
	// If we don't have an account manager, we skip this check (or fail safe)
	if p.accountManager == nil {
		return nil
	}

	account, err := p.accountManager.GetAccount(address)
	if err != nil {
		return fmt.Errorf("could not retrieve account for balance check: %v", err)
	}

	totalRequired := int64(0)

	// Sum existing pending transactions
	if senderTxs, exists := p.byAddress[address]; exists {
		for _, tx := range senderTxs {
			// Skip the one we might be replacing (though logic in Add handles removal first usually,
			// this is safe if called before removal)
			if tx.Nonce == newTx.Nonce {
				continue
			}
			totalRequired += tx.Amount + (tx.Gas * tx.GasPrice)
		}
	}

	// Add new transaction cost
	totalRequired += newTx.Amount + (newTx.Gas * newTx.GasPrice)

	if account.Balance < totalRequired {
		return fmt.Errorf("have %d, need %d", account.Balance, totalRequired)
	}

	return nil
}

// removeInternal performs the deletion logic without locking (caller must hold lock)
func (p *Pool) removeInternal(tx *core.Transaction) {
	delete(p.pending, tx.Id)
	delete(p.byHash, tx.Hash)

	if senderTxs, exists := p.byAddress[tx.From]; exists {
		delete(senderTxs, tx.Nonce)
		if len(senderTxs) == 0 {
			delete(p.byAddress, tx.From)
		}
	}
	p.totalRemoved++
}

// RemoveTransaction removes a transaction from the pool
func (p *Pool) RemoveTransaction(txID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	tx, exists := p.pending[txID]
	if !exists {
		return fmt.Errorf("transaction %s not found in pool", txID)
	}

	p.removeInternal(tx)
	return nil
}

// RemoveTransactionByHash removes a transaction by its hash
func (p *Pool) RemoveTransactionByHash(txHash string) error {
	p.mu.Lock() // Changed to Lock because removeInternal writes
	defer p.mu.Unlock()

	tx, exists := p.byHash[txHash]
	if !exists {
		return fmt.Errorf("transaction with hash %s not found in pool", txHash)
	}

	p.removeInternal(tx)
	return nil
}

// GetTransaction retrieves a transaction by ID
func (p *Pool) GetTransaction(txID string) (*core.Transaction, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	tx, exists := p.pending[txID]
	if !exists {
		return nil, fmt.Errorf("transaction %s not found in pool", txID)
	}

	return tx, nil
}

// GetTransactionByHash retrieves a transaction by hash
func (p *Pool) GetTransactionByHash(txHash string) (*core.Transaction, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	tx, exists := p.byHash[txHash]
	if !exists {
		return nil, fmt.Errorf("transaction with hash %s not found in pool", txHash)
	}

	return tx, nil
}

// GetPendingTransactions returns all pending transactions
func (p *Pool) GetPendingTransactions() []*core.Transaction {
	p.mu.RLock()
	defer p.mu.RUnlock()

	txs := make([]*core.Transaction, 0, len(p.pending))
	for _, tx := range p.pending {
		txs = append(txs, tx)
	}

	return txs
}

// getSortedTransactions is a helper to get transactions for an address sorted by nonce
// The caller must hold the lock.
func (p *Pool) getSortedTransactions(address string) []*core.Transaction {
	senderMap, exists := p.byAddress[address]
	if !exists {
		return []*core.Transaction{}
	}

	result := make([]*core.Transaction, 0, len(senderMap))
	for _, tx := range senderMap {
		result = append(result, tx)
	}

	// Sort by nonce
	sort.Slice(result, func(i, j int) bool {
		return result[i].Nonce < result[j].Nonce
	})

	return result
}

// GetTransactionsForAddress returns all transactions for a specific address
func (p *Pool) GetTransactionsForAddress(address string) []*core.Transaction {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.getSortedTransactions(address)
}

// GetExecutableTransactions returns transactions ready for execution
func (p *Pool) GetExecutableTransactions(maxCount int, accountManager *account.AccountManager) []*core.Transaction {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Use internal accountManager if passed one is nil, or prefer passed one?
	// Usually strict dependency injection is better, but falling back to struct field is safe.
	am := accountManager
	if am == nil {
		am = p.accountManager
	}

	fmt.Printf("🔍 Pool: GetExecutableTransactions called, max=%d, pending=%d\n", maxCount, len(p.pending))

	var executable []*core.Transaction
	processed := make(map[string]bool) // Track processed addresses

	// First pass: Get transactions with perfect nonce sequence
	for address := range p.byAddress {
		if len(executable) >= maxCount {
			break
		}

		// Get sorted transactions for this address (since map is unsorted)
		txs := p.getSortedTransactions(address)
		if len(txs) == 0 {
			continue
		}

		// Get current nonce for this address
		currentNonce, err := am.GetNonce(address)
		if err != nil {
			continue
		}

		account, err := am.GetAccount(address)
		if err != nil {
			continue
		}

		expectedNonce := currentNonce
		remainingBalance := account.Balance

		// Process transactions in nonce order
		for _, tx := range txs {
			if len(executable) >= maxCount {
				break
			}

			// Check if transaction has expected nonce (consecutive)
			if tx.Nonce == expectedNonce {
				// Check if account has sufficient balance
				totalCost := tx.Amount + (tx.Gas * tx.GasPrice)
				if remainingBalance >= totalCost {
					executable = append(executable, tx)
					expectedNonce++
					remainingBalance -= totalCost
				} else {
					break // Insufficient balance, skip remaining for this address
				}
			} else if tx.Nonce > expectedNonce {
				break // Gap in nonces
			}
			// Skip transactions with nonce < expectedNonce (already executed)
		}
		processed[address] = true
	}

	// Second pass: If we still need more transactions, be more lenient (High Gas Price filler)
	if len(executable) < maxCount && len(executable) < len(p.pending)/2 {
		var remaining []*core.Transaction

		// Collect candidates
		for _, tx := range p.pending {
			// Skip if already included
			isIncluded := false
			for _, execTx := range executable {
				if execTx.Id == tx.Id {
					isIncluded = true
					break
				}
			}
			if !isIncluded {
				remaining = append(remaining, tx)
			}
		}

		// Sort by gas price (highest first)
		sort.Slice(remaining, func(i, j int) bool {
			return remaining[i].GasPrice > remaining[j].GasPrice
		})

		for _, tx := range remaining {
			if len(executable) >= maxCount {
				break
			}

			// Basic validations
			currentNonce, err := am.GetNonce(tx.From)
			if err != nil {
				continue
			}
			account, err := am.GetAccount(tx.From)
			if err != nil {
				continue
			}

			// Allow transactions that are close to the expected nonce (within 5)
			if tx.Nonce >= currentNonce && tx.Nonce <= currentNonce+5 {
				totalCost := tx.Amount + (tx.Gas * tx.GasPrice)
				if account.Balance >= totalCost {
					executable = append(executable, tx)
				}
			}
		}
	}

	return executable
}

// GetHighestGasPriceTransactions returns transactions with highest gas prices
func (p *Pool) GetHighestGasPriceTransactions(maxCount int) []*core.Transaction {
	p.mu.RLock()
	defer p.mu.RUnlock()

	txs := make([]*core.Transaction, 0, len(p.pending))
	for _, tx := range p.pending {
		txs = append(txs, tx)
	}

	// Sort by gas price (descending)
	sort.Slice(txs, func(i, j int) bool {
		return txs[i].GasPrice > txs[j].GasPrice
	})

	if len(txs) > maxCount {
		txs = txs[:maxCount]
	}

	return txs
}

// GetStats returns statistics about the transaction pool
func (p *Pool) GetStats() *PoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return &PoolStats{
		PendingCount: len(p.pending),
		AddressCount: len(p.byAddress),
		TotalAdded:   p.totalAdded,
		TotalRemoved: p.totalRemoved,
		ShardID:      int(p.shardID),
		MaxCapacity:  p.maxTxs,
		MinGasPrice:  p.minGasPrice,
	}
}

// Clear removes all transactions from the pool
func (p *Pool) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.totalRemoved += int64(len(p.pending))

	p.pending = make(map[string]*core.Transaction)
	p.byAddress = make(map[string]map[uint64]*core.Transaction)
	p.byHash = make(map[string]*core.Transaction)
}

// CleanupStaleTransactions removes transactions older than the specified duration
func (p *Pool) CleanupStaleTransactions(maxAge time.Duration) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	currentTime := time.Now().Unix()
	removed := 0

	var staleTransactions []*core.Transaction

	for _, tx := range p.pending {
		if currentTime-tx.Timestamp > int64(maxAge.Seconds()) {
			staleTransactions = append(staleTransactions, tx)
		}
	}

	for _, tx := range staleTransactions {
		p.removeInternal(tx)
		removed++
	}

	return removed
}

// validateTransactionForPool validates a transaction for pool inclusion
func (p *Pool) validateTransactionForPool(tx *core.Transaction) error {
	if tx == nil {
		return fmt.Errorf("transaction cannot be nil")
	}
	if tx.Id == "" {
		return fmt.Errorf("transaction ID cannot be empty")
	}
	if tx.Hash == "" {
		return fmt.Errorf("transaction hash cannot be empty")
	}
	if tx.From == "" {
		return fmt.Errorf("sender address cannot be empty")
	}
	if tx.Amount < 0 {
		return fmt.Errorf("transaction amount cannot be negative")
	}
	if tx.Gas <= 0 {
		return fmt.Errorf("gas must be positive")
	}
	if tx.GasPrice < p.minGasPrice {
		return fmt.Errorf("gas price %d below minimum %d", tx.GasPrice, p.minGasPrice)
	}
	if len(tx.Signature) == 0 {
		return fmt.Errorf("transaction signature cannot be empty")
	}

	// Check if sender belongs to this shard (unless it's beacon shard)
	if p.shardID != account.BeaconShardID {
		senderShard := account.CalculateShardID(tx.From, p.totalShards)
		if senderShard != p.shardID {
			return fmt.Errorf("transaction sender %s belongs to shard %d, not %d",
				tx.From, senderShard, p.shardID)
		}
	}

	return nil
}

// evictLowestGasPrice tries to evict the transaction with lowest gas price
func (p *Pool) evictLowestGasPrice(newGasPrice int64) bool {
	var lowestGasPrice int64 = newGasPrice
	var evictTx *core.Transaction

	// Find transaction with lowest gas price
	for _, tx := range p.pending {
		if tx.GasPrice < lowestGasPrice {
			lowestGasPrice = tx.GasPrice
			evictTx = tx
		}
	}

	if evictTx != nil {
		p.removeInternal(evictTx)
		return true
	}

	return false
}

// GetNextNonce returns the next expected nonce for an address
func (p *Pool) GetNextNonce(address string, currentNonce uint64) uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	senderTxs, exists := p.byAddress[address]
	if !exists {
		return currentNonce
	}

	highestNonce := currentNonce - 1
	for nonce := range senderTxs {
		if nonce > highestNonce {
			highestNonce = nonce
		}
	}

	return highestNonce + 1
}

// HasTransaction checks if a transaction exists in the pool
func (p *Pool) HasTransaction(txID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, exists := p.pending[txID]
	return exists
}

// HasTransactionHash checks if a transaction with the given hash exists
func (p *Pool) HasTransactionHash(txHash string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, exists := p.byHash[txHash]
	return exists
}

// GetPoolCapacity returns current capacity information
func (p *Pool) GetPoolCapacity() (current int, max int, available int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	current = len(p.pending)
	max = p.maxTxs
	available = max - current
	if available < 0 {
		available = 0
	}
	return current, max, available
}

// UpdateGasPrice updates the minimum gas price for the pool
func (p *Pool) UpdateGasPrice(newMinGasPrice int64) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.minGasPrice = newMinGasPrice

	var toRemove []*core.Transaction
	for _, tx := range p.pending {
		if tx.GasPrice < newMinGasPrice {
			toRemove = append(toRemove, tx)
		}
	}

	for _, tx := range toRemove {
		p.removeInternal(tx)
	}

	return len(toRemove)
}

// GetTransactionsByGasPrice returns transactions sorted by gas price
func (p *Pool) GetTransactionsByGasPrice(ascending bool) []*core.Transaction {
	p.mu.RLock()
	defer p.mu.RUnlock()

	txs := make([]*core.Transaction, 0, len(p.pending))
	for _, tx := range p.pending {
		txs = append(txs, tx)
	}

	sort.Slice(txs, func(i, j int) bool {
		if ascending {
			return txs[i].GasPrice < txs[j].GasPrice
		}
		return txs[i].GasPrice > txs[j].GasPrice
	})

	return txs
}

// GetAddressNonceGap returns the nonce gap for an address
func (p *Pool) GetAddressNonceGap(address string, currentNonce uint64) []uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	senderTxs, exists := p.byAddress[address]
	if !exists {
		return []uint64{}
	}

	// Find highest nonce
	var highestNonce uint64 = currentNonce - 1
	for nonce := range senderTxs {
		if nonce > highestNonce {
			highestNonce = nonce
		}
	}

	// Check gaps
	var gaps []uint64
	for nonce := currentNonce; nonce <= highestNonce; nonce++ {
		if _, ok := senderTxs[nonce]; !ok {
			gaps = append(gaps, nonce)
		}
	}

	return gaps
}
