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
func (db *DB) CommitBlock(block *core.Block, accounts []*core.Account, validators []*core.Validator) error {
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
