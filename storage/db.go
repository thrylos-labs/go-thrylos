// DB provides high-level database operations for blockchain data structures
// This component handles the persistent storage of the blockchain's immutable data,
// focusing on the historical record and transaction processing. It manages:
//
// • Block Storage: Complete blocks with headers, transactions, and metadata
// • Transaction Storage: Individual transaction records with full details
// • Blockchain Index: Block height mapping and hash-based lookups
// • Batch Operations: Atomic commits of multiple blocks and related data
//
// DB operates as the primary interface for blockchain data persistence, handling
// the immutable aspects of the blockchain (blocks, transactions) while StateStorage
// manages the mutable state (accounts, validators). Together they provide complete
// blockchain data management.
//
// Key responsibilities:
// - Ensures block and transaction immutability and integrity
// - Provides efficient block and transaction retrieval by hash or height
// - Supports atomic batch operations for consistent blockchain updates
// - Maintains blockchain indexing for fast lookups and synchronization
// - Handles blockchain reorganizations and fork management data

package storage

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// DB provides high-level database operations for blockchain data
type DB struct {
	storage Storage // Use the interface from the same package
}

// NewDB creates a new database operations handler
func NewDB(storage Storage) *DB {
	return &DB{
		storage: storage,
	}
}

func (db *DB) Close() error {
	// DB doesn't own the storage, so just return nil
	// The underlying BadgerStorage will be closed by WorldState
	return nil
}

// Block operations
func (db *DB) SaveBlock(block *core.Block) error {
	data, err := json.Marshal(block)
	if err != nil {
		return fmt.Errorf("failed to marshal block: %v", err)
	}

	return db.storage.Set(BlockKey(block.Hash), data)
}

func (db *DB) GetBlock(hash string) (*core.Block, error) {
	data, err := db.storage.Get(BlockKey(hash))
	if err != nil {
		return nil, err
	}

	var block core.Block
	if err := json.Unmarshal(data, &block); err != nil {
		return nil, fmt.Errorf("failed to unmarshal block: %v", err)
	}

	return &block, nil
}

// Transaction operations
func (db *DB) SaveTransaction(tx *core.Transaction) error {
	data, err := json.Marshal(tx)
	if err != nil {
		return fmt.Errorf("failed to marshal transaction: %v", err)
	}

	return db.storage.Set(TransactionKey(tx.Id), data)
}

func (db *DB) GetTransaction(hash string) (*core.Transaction, error) {
	data, err := db.storage.Get(TransactionKey(hash))
	if err != nil {
		return nil, err
	}

	var tx core.Transaction
	if err := json.Unmarshal(data, &tx); err != nil {
		return nil, fmt.Errorf("failed to unmarshal transaction: %v", err)
	}

	return &tx, nil
}

// Batch operations for block commits
// Update the method signature to include totalTransactions
func (db *DB) CommitBlock(block *core.Block, accounts []*core.Account, validators []*core.Validator, totalTransactions int64) error {
	return db.storage.Update(func(txn Transaction) error {
		// Save block
		blockData, _ := json.Marshal(block)
		if err := txn.Set(BlockKey(block.Hash), blockData); err != nil {
			return err
		}

		// Update height
		if err := txn.Set(HeightKey(), []byte(fmt.Sprintf("%d", block.Header.Index))); err != nil {
			return err
		}

		// *** ADD THIS - Save total transactions count ***
		if err := txn.Set([]byte("total_transactions"), []byte(fmt.Sprintf("%d", totalTransactions))); err != nil {
			return err
		}

		// Save accounts
		for _, account := range accounts {
			accountData, _ := json.Marshal(account)
			if err := txn.Set(AccountKey(account.Address), accountData); err != nil {
				return err
			}
		}

		// Save validators
		for _, validator := range validators {
			validatorData, _ := json.Marshal(validator)
			if err := txn.Set(ValidatorKey(validator.Address), validatorData); err != nil {
				return err
			}
		}

		return nil
	})
}

// Add these methods to your DB struct in storage/db.go

// SaveTransactionWithIndex saves a transaction and creates address indexes
func (db *DB) SaveTransactionWithIndex(tx *core.Transaction) error {
	return db.storage.Update(func(txn Transaction) error {
		// Save the transaction itself
		txData, err := json.Marshal(tx)
		if err != nil {
			return fmt.Errorf("failed to marshal transaction: %v", err)
		}

		if err := txn.Set(TransactionKey(tx.Id), txData); err != nil {
			return fmt.Errorf("failed to save transaction: %v", err)
		}

		// Create index entries for both sender and receiver
		// Store just the transaction hash in the index (lightweight)
		txHashBytes := []byte(tx.Id)

		// Index for sender (from address)
		if tx.From != "" {
			fromKey := AddressTransactionKey(tx.From, tx.Id)
			if err := txn.Set(fromKey, txHashBytes); err != nil {
				return fmt.Errorf("failed to create from-address index: %v", err)
			}
		}

		// Index for receiver (to address)
		if tx.To != "" {
			toKey := AddressTransactionKey(tx.To, tx.Id)
			if err := txn.Set(toKey, txHashBytes); err != nil {
				return fmt.Errorf("failed to create to-address index: %v", err)
			}
		}

		return nil
	})
}

// GetTransactionsByAddress efficiently retrieves transactions for an address using indexes
func (db *DB) GetTransactionsByAddress(address string, limit int) ([]*core.Transaction, error) {
	var transactions []*core.Transaction
	seen := make(map[string]bool) // Prevent duplicates if address sends to itself

	// Use the address index to find transaction hashes
	iter := db.storage.Iterator(AddressTransactionPrefix(address))
	defer iter.Close()

	var txHashes []string
	for iter.Next() {
		// Extract transaction hash from the key
		// Key format: "addr_tx:address:txhash"
		key := string(iter.Key())
		parts := strings.Split(key, ":")
		if len(parts) >= 3 {
			txHash := strings.Join(parts[2:], ":") // Handle hashes with colons
			if !seen[txHash] {
				txHashes = append(txHashes, txHash)
				seen[txHash] = true

				if len(txHashes) >= limit {
					break
				}
			}
		}
	}

	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("iterator error: %v", err)
	}

	// Now fetch the actual transactions
	for _, txHash := range txHashes {
		tx, err := db.GetTransaction(txHash)
		if err != nil {
			// Log error but continue with other transactions
			log.Printf("Warning: could not retrieve transaction %s: %v", txHash, err)
			continue
		}
		transactions = append(transactions, tx)
	}

	// Sort by timestamp (newest first)
	sort.Slice(transactions, func(i, j int) bool {
		return transactions[i].Timestamp > transactions[j].Timestamp
	})

	return transactions, nil
}

// GetTransactionCount returns the total number of transactions for an address
func (db *DB) GetTransactionCount(address string) (int, error) {
	count := 0
	seen := make(map[string]bool)

	iter := db.storage.Iterator(AddressTransactionPrefix(address))
	defer iter.Close()

	for iter.Next() {
		// Extract transaction hash from the key
		key := string(iter.Key())
		parts := strings.Split(key, ":")
		if len(parts) >= 3 {
			txHash := strings.Join(parts[2:], ":")
			if !seen[txHash] {
				seen[txHash] = true
				count++
			}
		}
	}

	return count, iter.Error()
}

func TransactionKey(txHash string) []byte {
	return []byte(fmt.Sprintf("tx:%s", txHash))
}

// TransactionPrefix returns the prefix for all transactions
func TransactionPrefix() []byte {
	return []byte("tx:")
}

// AddressTransactionKey returns the key for indexing transactions by address
func AddressTransactionKey(address, txHash string) []byte {
	return []byte(fmt.Sprintf("addr_tx:%s:%s", address, txHash))
}

// AddressTransactionPrefix returns the prefix for address-based transaction index
func AddressTransactionPrefix(address string) []byte {
	return []byte(fmt.Sprintf("addr_tx:%s:", address))
}

func (db *DB) SaveBlockByHeight(block *core.Block) error {
	if block == nil || block.Header == nil {
		return fmt.Errorf("block or header is nil")
	}

	data, err := json.Marshal(block) // or proto.Marshal if you're using protobuf
	if err != nil {
		return fmt.Errorf("failed to marshal block: %w", err)
	}

	return db.storage.Set(BlockHeightKey(block.Header.Index), data)
}

func (db *DB) GetBlockByHeight(height int64) (*core.Block, error) {
	data, err := db.storage.Get(BlockHeightKey(height))
	if err != nil {
		return nil, fmt.Errorf("failed to get block by height %d: %w", height, err)
	}

	var block core.Block
	if err := json.Unmarshal(data, &block); err != nil {
		return nil, fmt.Errorf("failed to unmarshal block by height %d: %w", height, err)
	}

	return &block, nil
}
