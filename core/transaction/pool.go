// core/transaction/pool.go
package transaction

import (
	"fmt"
	"log"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/config" // Import config
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/math"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// TransactionEntry wraps a transaction with metadata
type TransactionEntry struct {
	Transaction *core.Transaction
	ReceivedAt  time.Time
}

// Pool manages pending transactions for a shard
type Pool struct {
	// Transaction storage
	pending   map[string]*TransactionEntry            // txid -> entry
	byAddress map[string]map[uint64]*core.Transaction // address -> nonce -> tx
	byHash    map[string]*core.Transaction            // hash -> tx for quick lookup

	// Maps address -> {nonce -> true} for nonces in flight
	nonceReservations map[string]map[uint64]bool

	// Dependencies
	accountManager *account.AccountManager

	// Configuration
	shardID     account.ShardID
	totalShards int
	maxTxs      int
	minGasPrice *big.Int
	maxCount    int

	// Security Limits
	maxTxsPerAddress int // FIX: Per-address limit

	// Lifecycle management
	stopChan chan struct{}
	wg       sync.WaitGroup

	// Statistics
	totalAdded   int64
	totalRemoved int64

	// Synchronization
	mu sync.RWMutex
}

// PoolStats represents statistics about the transaction pool
type PoolStats struct {
	PendingCount int    `json:"pending_count"`
	AddressCount int    `json:"address_count"`
	TotalAdded   int64  `json:"total_added"`
	TotalRemoved int64  `json:"total_removed"`
	ShardID      int    `json:"shard_id"`
	MaxCapacity  int    `json:"max_capacity"`
	MinGasPrice  string `json:"min_gas_price"`
}

const (
	// MaxTransactionsPerSender limits how many pending txs a single account can have.
	// 16 is a standard standard for preventing slot-filling attacks while allowing batching.
	MaxTransactionsPerSender = 16

	// ReplacementPriceBumpPercent requires a 10% price bump to replace a tx
	ReplacementPriceBumpPercent = 10
)

// NewPool creates a new transaction pool for a shard
func NewPool(
	shardID account.ShardID,
	totalShards int,
	maxCount int,
	minGasPrice string,
	accountManager *account.AccountManager,
) *Pool {

	minGasPriceBig := math.ParseBigInt(minGasPrice)
	pool := &Pool{
		pending:           make(map[string]*TransactionEntry),
		byAddress:         make(map[string]map[uint64]*core.Transaction),
		byHash:            make(map[string]*core.Transaction),
		nonceReservations: make(map[string]map[uint64]bool),
		stopChan:          make(chan struct{}),
		accountManager:    accountManager,
		shardID:           shardID,
		totalShards:       totalShards,
		maxTxs:            maxCount,
		minGasPrice:       minGasPriceBig,
		maxTxsPerAddress:  MaxTransactionsPerSender, // Initialize security limit
	}

	// Start automatic cleanup
	pool.wg.Add(1)
	go pool.startAutoCleanup()

	return pool
}

// startAutoCleanup runs periodic cleanup of expired transactions
func (p *Pool) startAutoCleanup() {
	defer p.wg.Done()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.CleanupExpired()
		case <-p.stopChan:
			log.Println("Transaction pool cleanup stopped")
			return
		}
	}
}

// Stop gracefully stops the transaction pool
func (p *Pool) Stop() {
	close(p.stopChan)
	p.wg.Wait()
}

func (p *Pool) AddTransaction(tx *core.Transaction) error {
	// 1. Basic Validation (Stateless)
	if err := p.validateTransactionForPool(tx); err != nil {
		return fmt.Errorf("transaction validation failed: %v", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// 2. Check Global Duplicates
	if _, exists := p.pending[tx.Id]; exists {
		return fmt.Errorf("transaction %s already exists in pool", tx.Id)
	}
	if _, exists := p.byHash[tx.Hash]; exists {
		return fmt.Errorf("transaction with hash %s already exists in pool", tx.Hash)
	}

	// Initialize maps if needed
	if _, exists := p.nonceReservations[tx.From]; !exists {
		p.nonceReservations[tx.From] = make(map[uint64]bool)
	}
	senderTxs, exists := p.byAddress[tx.From]
	if !exists {
		senderTxs = make(map[uint64]*core.Transaction)
		p.byAddress[tx.From] = senderTxs
	}

	// 3. Per-Address Limits & Replacement Logic (Stateful Security)
	isReplacement := false
	if existingTx, conflict := senderTxs[tx.Nonce]; conflict {
		// This is a replacement (RBF - Replace By Fee)
		isReplacement = true

		// FIX: Enforce 10% Price Bump to prevent spamming replacements
		// NewPrice >= OldPrice * 1.10
		oldPrice := math.ParseBigInt(existingTx.GasPrice)
		newPrice := math.ParseBigInt(tx.GasPrice)
		minRequired := new(big.Int).Mul(oldPrice, big.NewInt(100+ReplacementPriceBumpPercent))
		minRequired.Div(minRequired, big.NewInt(100))

		if newPrice.Cmp(minRequired) < 0 {
			return fmt.Errorf("replacement transaction underpriced: needs gas price >= %s (current: %s)",
				minRequired.String(), tx.GasPrice)
		}
	} else {
		// Not a replacement - check strict per-address limit
		if len(senderTxs) >= p.maxTxsPerAddress {
			return fmt.Errorf("sender %s has reached maximum pending transactions (%d)",
				tx.From, p.maxTxsPerAddress)
		}
	}

	// Check reserved nonces
	if p.nonceReservations[tx.From][tx.Nonce] {
		return fmt.Errorf("nonce %d is reserved for address %s (concurrent request in progress)", tx.Nonce, tx.From)
	}

	// 4. Handle Replacement
	if isReplacement {
		existingTx := senderTxs[tx.Nonce]
		p.removeInternal(existingTx)
	}

	// 5. Validate Balance (including all pending txs)
	if err := p.validateTotalPendingBalance(tx.From, tx); err != nil {
		return fmt.Errorf("insufficient balance for pending transactions: %v", err)
	}

	// 6. Global Pool Capacity & Dynamic Gas Price
	if len(p.pending) >= p.maxTxs {
		// Pool is full. Try to evict a cheap transaction to make room for this one.
		evicted := p.evictLowestGasPrice(tx.GasPrice)
		if !evicted {
			// If we couldn't evict anyone (meaning this tx is cheaper than everyone else), reject it.
			return fmt.Errorf("transaction pool full and gas price too low to replace existing transactions")
		}
	}

	// 7. Commit to Pool
	// Reserve nonce first
	p.nonceReservations[tx.From][tx.Nonce] = true
	defer func() {
		// Cleanup reservation if we somehow fail strictly after this (though we are at the end)
		if len(p.pending) == 0 || p.pending[tx.Id] == nil {
			delete(p.nonceReservations[tx.From], tx.Nonce)
		}
	}()

	p.pending[tx.Id] = &TransactionEntry{
		Transaction: tx,
		ReceivedAt:  time.Now(),
	}
	p.byHash[tx.Hash] = tx
	p.byAddress[tx.From][tx.Nonce] = tx

	p.totalAdded++
	return nil
}

// CleanupExpired removes transactions that have been in the pool longer than the TTL
func (p *Pool) CleanupExpired() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	expiredCount := 0
	orphanedCount := 0

	var entriesToRemove []*core.Transaction
	for _, entry := range p.pending {
		if now.Sub(entry.ReceivedAt) > config.TransactionPoolTTL {
			entriesToRemove = append(entriesToRemove, entry.Transaction)
		}
	}

	// Remove expired transactions
	for _, tx := range entriesToRemove {
		p.removeInternal(tx)
		expiredCount++
		log.Printf("Removed expired transaction: %s (Age: %v)",
			tx.Id, now.Sub(entryReceivedTime(tx.Timestamp)))
	}

	// Clean up orphaned reservations
	for address := range p.nonceReservations {
		for nonce := range p.nonceReservations[address] {
			senderTxs, exists := p.byAddress[address]
			txExists := exists && senderTxs[nonce] != nil

			if !txExists {
				delete(p.nonceReservations[address], nonce)
				orphanedCount++
			}
		}
		if len(p.nonceReservations[address]) == 0 {
			delete(p.nonceReservations, address)
		}
	}

	if expiredCount > 0 {
		log.Printf("CleanupExpired: Removed %d stale transactions, %d orphaned reservations", expiredCount, orphanedCount)
	}
}

// helper for logging timestamp conversion
func entryReceivedTime(ts int64) time.Time {
	return time.Unix(ts, 0)
}

// validateTotalPendingBalance calculates the total cost of pending transactions
func (p *Pool) validateTotalPendingBalance(address string, newTx *core.Transaction) error {
	if p.accountManager == nil {
		return nil
	}

	account, err := p.accountManager.GetAccount(address)
	if err != nil {
		return fmt.Errorf("could not retrieve account: %v", err)
	}

	totalRequired := big.NewInt(0)

	addCost := func(tx *core.Transaction) error {
		amountBig := math.ParseBigInt(tx.Amount)
		gasPriceBig := math.ParseBigInt(tx.GasPrice)
		gasLimitBig := big.NewInt(tx.Gas)

		gasCost := math.MulBig(gasLimitBig, gasPriceBig)
		txTotal := math.AddBig(amountBig, gasCost)

		totalRequired = math.AddBig(totalRequired, txTotal)
		return nil
	}

	if senderTxs, exists := p.byAddress[address]; exists {
		for _, tx := range senderTxs {
			if tx.Nonce == newTx.Nonce {
				continue // Skip replaced tx
			}
			if err := addCost(tx); err != nil {
				return err
			}
		}
	}

	if err := addCost(newTx); err != nil {
		return err
	}

	balanceBig := math.ParseBigInt(account.Balance)
	if balanceBig.Cmp(totalRequired) < 0 {
		return fmt.Errorf("insufficient funds for pending pool: have %s, need %s",
			account.Balance, totalRequired.String())
	}

	return nil
}

// removeInternal performs the deletion logic without locking
func (p *Pool) removeInternal(tx *core.Transaction) {
	delete(p.pending, tx.Id)
	delete(p.byHash, tx.Hash)

	if senderTxs, exists := p.byAddress[tx.From]; exists {
		delete(senderTxs, tx.Nonce)
		if len(senderTxs) == 0 {
			delete(p.byAddress, tx.From)
		}
	}

	if reservations, exists := p.nonceReservations[tx.From]; exists {
		delete(reservations, tx.Nonce)
		if len(reservations) == 0 {
			delete(p.nonceReservations, tx.From)
		}
	}

	p.totalRemoved++
}

// RemoveTransaction removes a transaction from the pool
func (p *Pool) RemoveTransaction(txID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry, exists := p.pending[txID]
	if !exists {
		return fmt.Errorf("transaction %s not found in pool", txID)
	}

	p.removeInternal(entry.Transaction)
	return nil
}

// RemoveTransactionByHash removes a transaction by its hash
func (p *Pool) RemoveTransactionByHash(txHash string) error {
	p.mu.Lock()
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

	entry, exists := p.pending[txID]
	if !exists {
		return nil, fmt.Errorf("transaction %s not found in pool", txID)
	}

	return entry.Transaction, nil
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
	for _, entry := range p.pending {
		txs = append(txs, entry.Transaction)
	}

	return txs
}

// getSortedTransactions is a helper to get transactions for an address sorted by nonce
func (p *Pool) getSortedTransactions(address string) []*core.Transaction {
	senderMap, exists := p.byAddress[address]
	if !exists {
		return []*core.Transaction{}
	}

	result := make([]*core.Transaction, 0, len(senderMap))
	for _, tx := range senderMap {
		result = append(result, tx)
	}

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

	am := accountManager
	if am == nil {
		am = p.accountManager
	}

	var executable []*core.Transaction
	processed := make(map[string]bool)

	// First pass: Perfect nonce sequence
	for address := range p.byAddress {
		if len(executable) >= maxCount {
			break
		}

		txs := p.getSortedTransactions(address)
		if len(txs) == 0 {
			continue
		}

		currentNonce, err := am.GetNonce(address)
		if err != nil {
			continue
		}

		account, err := am.GetAccount(address)
		if err != nil {
			continue
		}

		expectedNonce := currentNonce
		remainingBalanceBig := math.ParseBigInt(account.Balance)

		for _, tx := range txs {
			if len(executable) >= maxCount {
				break
			}

			if tx.Nonce == expectedNonce {
				amountBig := math.ParseBigInt(tx.Amount)
				gasPriceBig := math.ParseBigInt(tx.GasPrice)
				gasLimitBig := big.NewInt(tx.Gas)

				gasCostBig := new(big.Int).Mul(gasLimitBig, gasPriceBig)
				totalCostBig := new(big.Int).Add(amountBig, gasCostBig)

				if remainingBalanceBig.Cmp(totalCostBig) >= 0 {
					executable = append(executable, tx)
					expectedNonce++
					remainingBalanceBig.Sub(remainingBalanceBig, totalCostBig)
				} else {
					break
				}
			} else if tx.Nonce > expectedNonce {
				break
			}
		}
		processed[address] = true
	}

	// Second pass: Fill with high gas price txs if room (Optional mining strategy)
	// This helps miners pick profitable txs even if they are slightly out of order (for future blocks)
	if len(executable) < maxCount && len(executable) < len(p.pending)/2 {
		var remaining []*core.Transaction

		// Filter out already included transactions
		for _, entry := range p.pending {
			isIncluded := false
			for _, execTx := range executable {
				if execTx.Id == entry.Transaction.Id {
					isIncluded = true
					break
				}
			}
			if !isIncluded {
				remaining = append(remaining, entry.Transaction)
			}
		}

		sort.Slice(remaining, func(i, j int) bool {
			priceI := math.ParseBigInt(remaining[i].GasPrice)
			priceJ := math.ParseBigInt(remaining[j].GasPrice)
			return priceI.Cmp(priceJ) > 0
		})

		for _, tx := range remaining {
			if len(executable) >= maxCount {
				break
			}
			// Only include if nonce is reasonably close (prevent hoarding far-future txs)
			currentNonce, _ := am.GetNonce(tx.From)
			if tx.Nonce >= currentNonce && tx.Nonce <= currentNonce+5 {
				executable = append(executable, tx)
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
	for _, entry := range p.pending {
		txs = append(txs, entry.Transaction)
	}

	sort.Slice(txs, func(i, j int) bool {
		// Use BigInt comparison for safety
		pI := math.ParseBigInt(txs[i].GasPrice)
		pJ := math.ParseBigInt(txs[j].GasPrice)
		return pI.Cmp(pJ) > 0
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

	minGasStr := "0"
	if p.minGasPrice != nil {
		minGasStr = p.minGasPrice.String()
	}

	return &PoolStats{
		PendingCount: len(p.pending),
		AddressCount: len(p.byAddress),
		TotalAdded:   p.totalAdded,
		TotalRemoved: p.totalRemoved,
		ShardID:      int(p.shardID),
		MaxCapacity:  p.maxTxs, // Fixed field name
		MinGasPrice:  minGasStr,
	}
}

// Clear removes all transactions from the pool
func (p *Pool) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.totalRemoved += int64(len(p.pending))

	p.pending = make(map[string]*TransactionEntry)
	p.byAddress = make(map[string]map[uint64]*core.Transaction)
	p.byHash = make(map[string]*core.Transaction)
	// Clear reservations too
	p.nonceReservations = make(map[string]map[uint64]bool)
}

// CleanupStaleTransactions removes transactions older than the specified duration
func (p *Pool) CleanupStaleTransactions(maxAge time.Duration) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	currentTime := time.Now().Unix()
	removed := 0

	var staleTransactions []*core.Transaction

	for _, entry := range p.pending {
		if currentTime-entry.Transaction.Timestamp > int64(maxAge.Seconds()) {
			staleTransactions = append(staleTransactions, entry.Transaction)
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

	amountBig := math.ParseBigInt(tx.Amount)
	if amountBig.Sign() < 0 {
		return fmt.Errorf("transaction amount cannot be negative")
	}

	if tx.Gas <= 0 {
		return fmt.Errorf("gas must be positive")
	}

	gasPriceBig := math.ParseBigInt(tx.GasPrice)
	if gasPriceBig.Cmp(p.minGasPrice) < 0 {
		return fmt.Errorf("gas price %s below minimum %s", tx.GasPrice, p.minGasPrice.String())
	}

	if len(tx.Signature) == 0 {
		return fmt.Errorf("transaction signature cannot be empty")
	}

	// Shard validation
	if p.shardID != account.BeaconShardID {
		senderShard := account.CalculateShardID(tx.From, p.totalShards)
		if senderShard != p.shardID {
			return fmt.Errorf("transaction sender %s belongs to shard %d, not %d",
				tx.From, senderShard, p.shardID)
		}
	}

	return nil
}

// evictLowestGasPrice removes the transaction with the lowest gas price
// Returns true if eviction happened, false if new tx is too cheap to evict anyone
func (p *Pool) evictLowestGasPrice(newGasPrice []byte) bool {
	newPriceBig := math.ParseBigInt(newGasPrice)
	var evictTx *core.Transaction
	var lowestPriceBig *big.Int

	// Find the absolute cheapest transaction in the pool
	for _, entry := range p.pending {
		currentPriceBig := math.ParseBigInt(entry.Transaction.GasPrice)

		if lowestPriceBig == nil || currentPriceBig.Cmp(lowestPriceBig) < 0 {
			lowestPriceBig = currentPriceBig
			evictTx = entry.Transaction
		}
	}

	// Only evict if the new transaction pays MORE than the cheapest one
	// Strict > check (cannot be equal) to discourage spam
	if evictTx != nil && newPriceBig.Cmp(lowestPriceBig) > 0 {
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
		reservations := p.nonceReservations[address]
		for nonce := range reservations {
			if nonce == currentNonce {
				return currentNonce + 1
			}
		}
		return currentNonce
	}

	highestNonce := currentNonce - 1

	for nonce := range senderTxs {
		if nonce > highestNonce {
			highestNonce = nonce
		}
	}

	reservations := p.nonceReservations[address]
	for nonce := range reservations {
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

// UpdateGasPrice updates the minimum gas price and evicts transactions below it
func (p *Pool) UpdateGasPrice(newMinGasPrice string) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.minGasPrice = math.ParseBigInt(newMinGasPrice)

	var toRemove []*core.Transaction
	for _, entry := range p.pending {
		txPrice := math.ParseBigInt(entry.Transaction.GasPrice)
		if txPrice.Cmp(p.minGasPrice) < 0 {
			toRemove = append(toRemove, entry.Transaction)
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
	for _, entry := range p.pending {
		txs = append(txs, entry.Transaction)
	}

	sort.Slice(txs, func(i, j int) bool {
		pI := math.ParseBigInt(txs[i].GasPrice)
		pJ := math.ParseBigInt(txs[j].GasPrice)
		if ascending {
			return pI.Cmp(pJ) < 0
		}
		return pI.Cmp(pJ) > 0
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

	var highestNonce uint64 = currentNonce - 1
	for nonce := range senderTxs {
		if nonce > highestNonce {
			highestNonce = nonce
		}
	}

	var gaps []uint64
	for nonce := currentNonce; nonce <= highestNonce; nonce++ {
		if _, ok := senderTxs[nonce]; !ok {
			gaps = append(gaps, nonce)
		}
	}

	return gaps
}
