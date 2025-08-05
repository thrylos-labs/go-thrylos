// core/transaction/pool.go
// Manages pending transactions in memory:

// ✅ Transaction pool management with configurable limits
// ✅ Nonce-based ordering for proper transaction sequencing
// ✅ Gas price prioritization with eviction of low gas price transactions
// ✅ Address-based indexing for efficient transaction retrieval
// ✅ Duplicate detection by ID and hash
// ✅ Stale transaction cleanup with configurable max age
// ✅ Pool statistics for monitoring and debugging
// ✅ Nonce gap detection for identifying missing transactions

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
	pending   map[string]*core.Transaction   // txid -> tx
	byAddress map[string][]*core.Transaction // address -> txs (sorted by nonce)
	byHash    map[string]*core.Transaction   // hash -> tx for quick lookup

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
func NewPool(shardID account.ShardID, totalShards int, maxTxs int, minGasPrice int64) *Pool {
	return &Pool{
		pending:     make(map[string]*core.Transaction),
		byAddress:   make(map[string][]*core.Transaction),
		byHash:      make(map[string]*core.Transaction),
		shardID:     shardID,
		totalShards: totalShards,
		maxTxs:      maxTxs,
		minGasPrice: minGasPrice,
	}
}

// AddTransaction adds a transaction to the pool after validation
func (p *Pool) AddTransaction(tx *core.Transaction) error {
	if err := p.validateTransactionForPool(tx); err != nil {
		return fmt.Errorf("transaction validation failed: %v", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if pool is full
	if len(p.pending) >= p.maxTxs {
		// Try to remove lowest gas price transaction
		if !p.evictLowestGasPrice(tx.GasPrice) {
			return fmt.Errorf("transaction pool is full and cannot evict lower gas price transactions")
		}
	}

	// Check for duplicate by ID
	if _, exists := p.pending[tx.Id]; exists {
		return fmt.Errorf("transaction %s already exists in pool", tx.Id)
	}

	// Check for duplicate by hash
	if _, exists := p.byHash[tx.Hash]; exists {
		return fmt.Errorf("transaction with hash %s already exists in pool", tx.Hash)
	}

	// Check for nonce conflicts
	if err := p.checkNonceConflict(tx); err != nil {
		return fmt.Errorf("nonce conflict: %v", err)
	}

	// Add to all indices
	p.pending[tx.Id] = tx
	p.byHash[tx.Hash] = tx
	p.byAddress[tx.From] = append(p.byAddress[tx.From], tx)

	// Sort by nonce for this address
	p.sortTransactionsByNonce(tx.From)

	p.totalAdded++

	return nil
}

// RemoveTransaction removes a transaction from the pool
func (p *Pool) RemoveTransaction(txID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	tx, exists := p.pending[txID]
	if !exists {
		return fmt.Errorf("transaction %s not found in pool", txID)
	}

	// Remove from all indices
	delete(p.pending, txID)
	delete(p.byHash, tx.Hash)

	// Remove from address index
	addressTxs := p.byAddress[tx.From]
	for i, addrTx := range addressTxs {
		if addrTx.Id == txID {
			p.byAddress[tx.From] = append(addressTxs[:i], addressTxs[i+1:]...)
			break
		}
	}

	// Clean up empty address entries
	if len(p.byAddress[tx.From]) == 0 {
		delete(p.byAddress, tx.From)
	}

	p.totalRemoved++
	return nil
}

// RemoveTransactionByHash removes a transaction by its hash
func (p *Pool) RemoveTransactionByHash(txHash string) error {
	p.mu.RLock()
	tx, exists := p.byHash[txHash]
	p.mu.RUnlock()

	if !exists {
		return fmt.Errorf("transaction with hash %s not found in pool", txHash)
	}

	return p.RemoveTransaction(tx.Id)
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

// GetTransactionsForAddress returns all transactions for a specific address
func (p *Pool) GetTransactionsForAddress(address string) []*core.Transaction {
	p.mu.RLock()
	defer p.mu.RUnlock()

	addressTxs, exists := p.byAddress[address]
	if !exists {
		return []*core.Transaction{}
	}

	// Return a copy to prevent external modification
	result := make([]*core.Transaction, len(addressTxs))
	copy(result, addressTxs)
	return result
}

// GetExecutableTransactions returns transactions ready for execution
func (p *Pool) GetExecutableTransactions(maxCount int, accountManager *account.AccountManager) []*core.Transaction {
	p.mu.RLock()
	defer p.mu.RUnlock()

	fmt.Printf("🔍 Pool: GetExecutableTransactions called, max=%d, pending=%d\n", maxCount, len(p.pending))

	var executable []*core.Transaction
	processed := make(map[string]bool) // Track processed addresses

	// First pass: Get transactions with perfect nonce sequence
	for address, txs := range p.byAddress {
		if len(executable) >= maxCount {
			break
		}

		// Get current nonce for this address
		currentNonce, err := accountManager.GetNonce(address)
		if err != nil {
			fmt.Printf("🔍 Pool: Skipping address %s, can't get nonce: %v\n", address[:10]+"...", err)
			continue
		}

		account, err := accountManager.GetAccount(address)
		if err != nil {
			fmt.Printf("🔍 Pool: Skipping address %s, can't get account: %v\n", address[:10]+"...", err)
			continue
		}

		consecutiveCount := 0
		expectedNonce := currentNonce
		remainingBalance := account.Balance

		// Process transactions in nonce order
		for _, tx := range txs {
			if len(executable) >= maxCount {
				break
			}

			// Check if transaction has expected nonce (consecutive)
			if tx.Nonce == expectedNonce {
				// Check if account has sufficient balance for this transaction
				totalCost := tx.Amount + (tx.Gas * tx.GasPrice)
				if remainingBalance >= totalCost {
					executable = append(executable, tx)
					expectedNonce++
					consecutiveCount++
					remainingBalance -= totalCost

					fmt.Printf("🔍 Pool: Added tx from %s, nonce %d, remaining balance %d\n",
						address[:10]+"...", tx.Nonce, remainingBalance)
				} else {
					fmt.Printf("🔍 Pool: Insufficient balance for tx from %s, nonce %d (need %d, have %d)\n",
						address[:10]+"...", tx.Nonce, totalCost, remainingBalance)
					break // Insufficient balance, skip remaining
				}
			} else if tx.Nonce > expectedNonce {
				fmt.Printf("🔍 Pool: Nonce gap for address %s, expected %d, got %d\n",
					address[:10]+"...", expectedNonce, tx.Nonce)
				break // Gap in nonces, can't execute remaining
			}
			// Skip transactions with nonce < expectedNonce (already executed)
		}

		processed[address] = true
		fmt.Printf("🔍 Pool: Processed address %s, added %d consecutive transactions\n",
			address[:10]+"...", consecutiveCount)
	}

	// Second pass: If we still need more transactions, be more lenient
	if len(executable) < maxCount && len(executable) < len(p.pending)/2 {
		fmt.Printf("🔍 Pool: Second pass - looking for more transactions (%d/%d found)\n",
			len(executable), maxCount)

		// Get all remaining transactions and sort by gas price
		var remaining []*core.Transaction
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

		// Add high gas price transactions even if nonce isn't perfect
		for _, tx := range remaining {
			if len(executable) >= maxCount {
				break
			}

			// Basic validation - ensure transaction can be executed
			account, err := accountManager.GetAccount(tx.From)
			if err != nil {
				continue
			}

			currentNonce, err := accountManager.GetNonce(tx.From)
			if err != nil {
				continue
			}

			// Allow transactions that are close to the expected nonce (within 5)
			if tx.Nonce >= currentNonce && tx.Nonce <= currentNonce+5 {
				totalCost := tx.Amount + (tx.Gas * tx.GasPrice)
				if account.Balance >= totalCost {
					executable = append(executable, tx)
					fmt.Printf("🔍 Pool: Second pass added tx from %s, nonce %d (current %d)\n",
						tx.From[:10]+"...", tx.Nonce, currentNonce)
				}
			}
		}
	}

	fmt.Printf("🔍 Pool: Returning %d executable transactions\n", len(executable))
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
	p.byAddress = make(map[string][]*core.Transaction)
	p.byHash = make(map[string]*core.Transaction)
}

// CleanupStaleTransactions removes transactions older than the specified duration
func (p *Pool) CleanupStaleTransactions(maxAge time.Duration) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	currentTime := time.Now().Unix()
	removed := 0

	var staleTransactions []string

	for txID, tx := range p.pending {
		// If transaction is older than maxAge, mark for removal
		if currentTime-tx.Timestamp > int64(maxAge.Seconds()) {
			staleTransactions = append(staleTransactions, txID)
		}
	}

	// Remove stale transactions
	for _, txID := range staleTransactions {
		tx := p.pending[txID]

		// Remove from all indices
		delete(p.pending, txID)
		delete(p.byHash, tx.Hash)

		// Remove from address index
		addressTxs := p.byAddress[tx.From]
		for i, addrTx := range addressTxs {
			if addrTx.Id == txID {
				p.byAddress[tx.From] = append(addressTxs[:i], addressTxs[i+1:]...)
				break
			}
		}

		// Clean up empty address entries
		if len(p.byAddress[tx.From]) == 0 {
			delete(p.byAddress, tx.From)
		}

		removed++
		p.totalRemoved++
	}

	return removed
}

// validateTransactionForPool validates a transaction for pool inclusion
func (p *Pool) validateTransactionForPool(tx *core.Transaction) error {
	if tx == nil {
		return fmt.Errorf("transaction cannot be nil")
	}

	// Basic field validation
	if tx.Id == "" {
		return fmt.Errorf("transaction ID cannot be empty")
	}

	if tx.Hash == "" {
		return fmt.Errorf("transaction hash cannot be empty")
	}

	if tx.From == "" {
		return fmt.Errorf("sender address cannot be empty")
	}

	if tx.To == "" && tx.Type != core.TransactionType_STAKE && tx.Type != core.TransactionType_UNSTAKE && tx.Type != core.TransactionType_CLAIM_REWARDS {
		return fmt.Errorf("recipient address cannot be empty for this transaction type")
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

	// Validate signature exists
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

// checkNonceConflict checks if adding this transaction would create a nonce conflict
func (p *Pool) checkNonceConflict(newTx *core.Transaction) error {
	addressTxs, exists := p.byAddress[newTx.From]
	if !exists {
		return nil // No existing transactions for this address
	}

	// Check if nonce already exists
	for _, tx := range addressTxs {
		if tx.Nonce == newTx.Nonce {
			// Allow replacement if new transaction has higher gas price
			if newTx.GasPrice > tx.GasPrice {
				// Remove the old transaction
				delete(p.pending, tx.Id)
				delete(p.byHash, tx.Hash)

				// Remove from address array
				for i, addrTx := range addressTxs {
					if addrTx.Id == tx.Id {
						p.byAddress[newTx.From] = append(addressTxs[:i], addressTxs[i+1:]...)
						break
					}
				}

				p.totalRemoved++
				return nil
			} else {
				return fmt.Errorf("transaction with nonce %d already exists with higher gas price", newTx.Nonce)
			}
		}
	}

	return nil
}

// sortTransactionsByNonce sorts transactions for an address by nonce
func (p *Pool) sortTransactionsByNonce(address string) {
	if txs, exists := p.byAddress[address]; exists {
		sort.Slice(txs, func(i, j int) bool {
			return txs[i].Nonce < txs[j].Nonce
		})
	}
}

// evictLowestGasPrice tries to evict the transaction with lowest gas price
func (p *Pool) evictLowestGasPrice(newGasPrice int64) bool {
	var lowestGasPrice int64 = newGasPrice
	var evictTxID string

	// Find transaction with lowest gas price
	for txID, tx := range p.pending {
		if tx.GasPrice < lowestGasPrice {
			lowestGasPrice = tx.GasPrice
			evictTxID = txID
		}
	}

	// If we found a transaction with lower gas price, evict it
	if evictTxID != "" {
		tx := p.pending[evictTxID]

		// Remove from all indices
		delete(p.pending, evictTxID)
		delete(p.byHash, tx.Hash)

		// Remove from address index
		addressTxs := p.byAddress[tx.From]
		for i, addrTx := range addressTxs {
			if addrTx.Id == evictTxID {
				p.byAddress[tx.From] = append(addressTxs[:i], addressTxs[i+1:]...)
				break
			}
		}

		// Clean up empty address entries
		if len(p.byAddress[tx.From]) == 0 {
			delete(p.byAddress, tx.From)
		}

		p.totalRemoved++
		return true
	}

	return false
}

// GetNextNonce returns the next expected nonce for an address
func (p *Pool) GetNextNonce(address string, currentNonce uint64) uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	addressTxs, exists := p.byAddress[address]
	if !exists {
		return currentNonce
	}

	// Find the highest nonce for this address
	highestNonce := currentNonce - 1
	for _, tx := range addressTxs {
		if tx.Nonce > highestNonce {
			highestNonce = tx.Nonce
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

	// oldMinGasPrice := p.minGasPrice
	p.minGasPrice = newMinGasPrice

	// Remove transactions that no longer meet the minimum gas price
	var toRemove []string
	for txID, tx := range p.pending {
		if tx.GasPrice < newMinGasPrice {
			toRemove = append(toRemove, txID)
		}
	}

	// Remove transactions below new minimum
	for _, txID := range toRemove {
		tx := p.pending[txID]

		// Remove from all indices
		delete(p.pending, txID)
		delete(p.byHash, tx.Hash)

		// Remove from address index
		addressTxs := p.byAddress[tx.From]
		for i, addrTx := range addressTxs {
			if addrTx.Id == txID {
				p.byAddress[tx.From] = append(addressTxs[:i], addressTxs[i+1:]...)
				break
			}
		}

		// Clean up empty address entries
		if len(p.byAddress[tx.From]) == 0 {
			delete(p.byAddress, tx.From)
		}

		p.totalRemoved++
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

	// Sort by gas price
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

	addressTxs, exists := p.byAddress[address]
	if !exists {
		return []uint64{}
	}

	// Collect all nonces for this address
	nonces := make(map[uint64]bool)
	for _, tx := range addressTxs {
		nonces[tx.Nonce] = true
	}

	// Find gaps
	var gaps []uint64
	expectedNonce := currentNonce

	// Check for gaps up to the highest nonce
	var highestNonce uint64 = currentNonce - 1
	for nonce := range nonces {
		if nonce > highestNonce {
			highestNonce = nonce
		}
	}

	for nonce := expectedNonce; nonce <= highestNonce; nonce++ {
		if !nonces[nonce] {
			gaps = append(gaps, nonce)
		}
	}

	return gaps
}
