// Package storage provides the complete data persistence layer for the Thrylos blockchain
//
// This package implements a three-tier storage architecture optimized for Proof-of-Stake
// blockchain operations:
//
// Architecture Overview:
// ┌─────────────────┐    ┌──────────────────┐
// │   WorldState    │───▶│   StateStorage   │  (Accounts, Validators, State)
// │                 │    │       &          │
// │                 │───▶│       DB         │  (Blocks, Transactions)
// └─────────────────┘    └─────────┬────────┘
//                                  │
//                        ┌─────────▼────────┐
//                        │  BadgerStorage   │  (Low-level KV store)
//                        └──────────────────┘
//
// Components:
// • BadgerStorage: High-performance key-value store using BadgerDB v3 with blockchain-optimized
//   configuration including ZSTD compression, write-optimized settings, and transaction support
//
// • StateStorage: Manages mutable blockchain state (accounts, validators, consensus data)
//   with atomic operations and efficient queries for PoS consensus
//
// • DB: Manages immutable blockchain data (blocks, transactions, indexes) with batch
//   operations and integrity guarantees
//
// Key Features:
// - Thread-safe operations with read/write locks
// - Atomic batch operations for consistent updates
// - Optimized for high write throughput (blockchain consensus)
// - Memory-efficient with configurable caching
// - Support for iterators and prefix-based queries
// - Proper resource management and graceful shutdown

package storage

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/dgraph-io/badger/v3"
)

// BadgerStorage implements blockchain storage using BadgerDB v3
type BadgerStorage struct {
	db      *badger.DB
	dataDir string // Track which directory this instance uses
	mu      sync.RWMutex
}

// NewBadgerStorage creates a new BadgerDB v3 storage instance

func NewBadgerStorage(dataDir string) (*BadgerStorage, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create data directory %s: %w", dataDir, err)
	}

	opts := badger.DefaultOptions(dataDir)
	// You can keep or tweak logging as you like:
	opts = opts.WithLogger(nil)

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger db at %s: %w", dataDir, err)
	}

	return &BadgerStorage{
		db:      db,
		dataDir: dataDir, // 🔴 was `directory: dataDir`
	}, nil
}

// Close shuts down the underlying Badger DB.
func (bs *BadgerStorage) Close() error {
	return bs.db.Close()
}

// Get retrieves a value by key
func (bs *BadgerStorage) Get(key []byte) ([]byte, error) {
	bs.mu.RLock()
	defer bs.mu.RUnlock()

	var value []byte
	err := bs.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			return err
		}

		// Use ValueCopy to get a copy that's safe to use outside the transaction
		value, err = item.ValueCopy(nil)
		return err
	})

	if err == badger.ErrKeyNotFound {
		return nil, ErrKeyNotFound
	}

	return value, err
}

// Set stores a key-value pair
func (bs *BadgerStorage) Set(key, value []byte) error {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	return bs.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, value)
	})
}

// Delete removes a key
func (bs *BadgerStorage) Delete(key []byte) error {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	return bs.db.Update(func(txn *badger.Txn) error {
		return txn.Delete(key)
	})
}

// Has checks if a key exists
func (bs *BadgerStorage) Has(key []byte) (bool, error) {
	bs.mu.RLock()
	defer bs.mu.RUnlock()

	err := bs.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get(key)
		return err
	})

	if err == badger.ErrKeyNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Batch executes multiple operations atomically
func (bs *BadgerStorage) Batch(operations []BatchOperation) error {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	return bs.db.Update(func(txn *badger.Txn) error {
		for _, op := range operations {
			switch op.Type {
			case BatchSet:
				if err := txn.Set(op.Key, op.Value); err != nil {
					return err
				}
			case BatchDelete:
				if err := txn.Delete(op.Key); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// Update executes a function within a write transaction
func (bs *BadgerStorage) Update(fn func(txn Transaction) error) error {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	return bs.db.Update(func(txn *badger.Txn) error {
		return fn(&BadgerTransaction{txn: txn})
	})
}

// View executes a function within a read transaction
func (bs *BadgerStorage) View(fn func(txn Transaction) error) error {
	bs.mu.RLock()
	defer bs.mu.RUnlock()

	return bs.db.View(func(txn *badger.Txn) error {
		return fn(&BadgerTransaction{txn: txn})
	})
}

// Iterator returns an iterator for the database
func (bs *BadgerStorage) Iterator(prefix []byte) Iterator {
	return &BadgerIterator{
		db:     bs.db,
		prefix: prefix,
	}
}

// RunGC runs garbage collection
func (bs *BadgerStorage) RunGC(discardRatio float64) error {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	// BadgerDB v3 uses RunValueLogGC
	return bs.db.RunValueLogGC(discardRatio)
}

// Size returns the size of the database
func (bs *BadgerStorage) Size() (int64, error) {
	lsm, vlog := bs.db.Size()
	return lsm + vlog, nil
}

// GetDB returns the underlying badger.DB instance
// This is useful for components that need direct access to the database
// such as the slashing manager for persistence
func (bs *BadgerStorage) GetDB() *badger.DB {
	return bs.db
}

// Flatten runs compaction to optimize storage
func (bs *BadgerStorage) Flatten(workers int) error {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	return bs.db.Flatten(workers)
}

// Backup creates a backup of the database to a writer
// [FIX L-03] Implemented online backup functionality
func (bs *BadgerStorage) Backup(w interface{}, since uint64) (uint64, error) {
	bs.mu.RLock()
	defer bs.mu.RUnlock()

	// Ensure the interface passed is actually a writer
	writer, ok := w.(io.Writer)
	if !ok {
		return 0, fmt.Errorf("invalid writer: must implement io.Writer")
	}

	// Delegate to native BadgerDB backup
	// This is a non-blocking online backup (safe to run while node is active)
	return bs.db.Backup(writer, since)
}

// Transaction interface for atomic operations
type Transaction interface {
	Get(key []byte) ([]byte, error)
	Set(key, value []byte) error
	Delete(key []byte) error
	Has(key []byte) (bool, error)
}

// BadgerTransaction wraps badger.Txn to implement Transaction interface
type BadgerTransaction struct {
	txn *badger.Txn
}

func (bt *BadgerTransaction) Get(key []byte) ([]byte, error) {
	item, err := bt.txn.Get(key)
	if err != nil {
		if err == badger.ErrKeyNotFound {
			return nil, ErrKeyNotFound
		}
		return nil, err
	}
	return item.ValueCopy(nil)
}

func (bt *BadgerTransaction) Set(key, value []byte) error {
	return bt.txn.Set(key, value)
}

func (bt *BadgerTransaction) Delete(key []byte) error {
	return bt.txn.Delete(key)
}

func (bt *BadgerTransaction) Has(key []byte) (bool, error) {
	_, err := bt.txn.Get(key)
	if err == badger.ErrKeyNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// BatchOperation represents a batch operation
type BatchOperation struct {
	Type  BatchOperationType
	Key   []byte
	Value []byte
}

type BatchOperationType int

const (
	BatchSet BatchOperationType = iota
	BatchDelete
)

// Custom errors
var (
	ErrKeyNotFound = fmt.Errorf("key not found")
)

// Iterator interface for database iteration
type Iterator interface {
	Next() bool
	Key() []byte
	Value() []byte
	Error() error
	Close()
}

// BadgerIterator implements Iterator for BadgerDB v3
type BadgerIterator struct {
	db     *badger.DB
	prefix []byte
	txn    *badger.Txn
	iter   *badger.Iterator
	closed bool
}

func (bi *BadgerIterator) Next() bool {
	if bi.closed {
		return false
	}

	if bi.txn == nil {
		bi.txn = bi.db.NewTransaction(false)
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 100
		opts.PrefetchValues = false // Only prefetch keys for better performance
		bi.iter = bi.txn.NewIterator(opts)
		bi.iter.Seek(bi.prefix)
	} else {
		bi.iter.Next()
	}

	return bi.iter.ValidForPrefix(bi.prefix)
}

func (bi *BadgerIterator) Key() []byte {
	if bi.iter != nil {
		return bi.iter.Item().KeyCopy(nil)
	}
	return nil
}

func (bi *BadgerIterator) Value() []byte {
	if bi.iter != nil {
		val, _ := bi.iter.Item().ValueCopy(nil)
		return val
	}
	return nil
}

func (bi *BadgerIterator) Error() error {
	// BadgerDB iterator doesn't return errors during iteration
	return nil
}

func (bi *BadgerIterator) Close() {
	if !bi.closed {
		if bi.iter != nil {
			bi.iter.Close()
		}
		if bi.txn != nil {
			bi.txn.Discard()
		}
		bi.closed = true
	}
}

// Storage interface that BadgerStorage implements
type Storage interface {
	Get(key []byte) ([]byte, error)
	Set(key, value []byte) error
	Delete(key []byte) error
	Has(key []byte) (bool, error)
	Batch(operations []BatchOperation) error
	Update(fn func(txn Transaction) error) error
	View(fn func(txn Transaction) error) error
	Iterator(prefix []byte) Iterator
	Close() error
	RunGC(discardRatio float64) error
	Size() (int64, error)
	Flatten(workers int) error
}

// Key prefixes for different data types
const (
	BlockPrefix     = "blk:"
	AccountPrefix   = "acc:"
	ValidatorPrefix = "val:"
	HeightPrefix    = "hgt:"
	StateRootPrefix = "srt:"
	SnapshotPrefix  = "snp:"
	IndexPrefix     = "idx:"
)

// Helper functions for key construction
func BlockKey(hash string) []byte {
	return []byte(BlockPrefix + hash)
}

func BlockHeightKey(height int64) []byte {
	return []byte(fmt.Sprintf("%s%d", IndexPrefix+"height:", height))
}

func AccountKey(address string) []byte {
	return []byte(AccountPrefix + address)
}

func ValidatorKey(address string) []byte {
	return []byte(ValidatorPrefix + address)
}

func HeightKey() []byte {
	return []byte(HeightPrefix + "current")
}

func StateRootKey() []byte {
	return []byte(StateRootPrefix + "current")
}

func SnapshotKey(height int64) []byte {
	return []byte(fmt.Sprintf("%s%d", SnapshotPrefix, height))
}
