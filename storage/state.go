// StateStorage handles blockchain state persistence for PoS consensus
//
// This component manages the persistent storage of the blockchain's current state,
// which is critical for Proof-of-Stake (PoS) consensus operations. It handles:
//
// • Account State: User balances, nonces, and account metadata
// • Validator State: Active validators, their stakes, rewards, and slashing history
// • Consensus State: Current block height, state root hashes for merkle verification
// • State Synchronization: Bulk operations for fast sync and state snapshots
//
// StateStorage operates at a higher abstraction level than raw database operations,
// providing blockchain-specific data structures and validation. It works in conjunction
// with DB (for blocks/transactions) to maintain complete blockchain state.
//
// Key responsibilities:
// - Ensures atomic state updates during block processing
// - Maintains validator set consistency for PoS consensus
// - Provides efficient state queries for transaction validation
// - Supports state migration and rollback operations

package storage

import (
	"encoding/binary"
	"encoding/json"
	"fmt"

	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// StateStorage handles blockchain state persistence
type StateStorage struct {
	storage Storage // Use the interface from the same package
}

// NewStateStorage creates a new state storage handler
func NewStateStorage(storage Storage) *StateStorage {
	return &StateStorage{
		storage: storage,
	}
}

func (ss *StateStorage) Close() error {
	// StateStorage doesn't own the storage, so just return nil
	// The underlying BadgerStorage will be closed by WorldState
	return nil
}

// Account operations
func (ss *StateStorage) SaveAccount(account *core.Account) error {
	data, err := json.Marshal(account)
	if err != nil {
		return fmt.Errorf("failed to marshal account: %v", err)
	}

	return ss.storage.Set(AccountKey(account.Address), data)
}

func (ss *StateStorage) GetAccount(address string) (*core.Account, error) {
	data, err := ss.storage.Get(AccountKey(address))
	if err != nil {
		if err == ErrKeyNotFound {
			return nil, nil // Account doesn't exist
		}
		return nil, err
	}

	var account core.Account
	if err := json.Unmarshal(data, &account); err != nil {
		return nil, fmt.Errorf("failed to unmarshal account: %v", err)
	}

	return &account, nil
}

// Validator operations
func (ss *StateStorage) SaveValidator(validator *core.Validator) error {
	data, err := json.Marshal(validator)
	if err != nil {
		return fmt.Errorf("failed to marshal validator: %v", err)
	}

	return ss.storage.Set(ValidatorKey(validator.Address), data)
}

func (ss *StateStorage) GetValidator(address string) (*core.Validator, error) {
	data, err := ss.storage.Get(ValidatorKey(address))
	if err != nil {
		if err == ErrKeyNotFound {
			return nil, nil
		}
		return nil, err
	}

	var validator core.Validator
	if err := json.Unmarshal(data, &validator); err != nil {
		return nil, fmt.Errorf("failed to unmarshal validator: %v", err)
	}

	return &validator, nil
}

// Get all accounts (for state sync)
func (ss *StateStorage) GetAllAccounts() (map[string]*core.Account, error) {
	accounts := make(map[string]*core.Account)

	iter := ss.storage.Iterator([]byte(AccountPrefix))
	defer iter.Close()

	for iter.Next() {
		var account core.Account
		if err := json.Unmarshal(iter.Value(), &account); err != nil {
			continue // Skip invalid accounts
		}
		accounts[account.Address] = &account
	}

	return accounts, iter.Error()
}

// State root and height operations
func (ss *StateStorage) SaveHeight(height int64) error {
	return ss.storage.Set(HeightKey(), []byte(fmt.Sprintf("%d", height)))
}

func (ss *StateStorage) GetHeight() (int64, error) {
	data, err := ss.storage.Get(HeightKey())
	if err != nil {
		if err == ErrKeyNotFound {
			return -1, nil // Genesis state
		}
		return 0, err
	}

	var height int64
	if _, err := fmt.Sscanf(string(data), "%d", &height); err != nil {
		return 0, fmt.Errorf("failed to parse height: %v", err)
	}

	return height, nil
}

func (ss *StateStorage) SaveStateRoot(stateRoot string) error {
	return ss.storage.Set(StateRootKey(), []byte(stateRoot))
}

func (ss *StateStorage) GetStateRoot() (string, error) {
	data, err := ss.storage.Get(StateRootKey())
	if err != nil {
		if err == ErrKeyNotFound {
			return "", nil
		}
		return "", err
	}

	return string(data), nil
}

func (ss *StateStorage) GetAllValidators() (map[string]*core.Validator, error) {
	validators := make(map[string]*core.Validator)

	iter := ss.storage.Iterator([]byte(ValidatorPrefix))
	defer iter.Close()

	for iter.Next() {
		var validator core.Validator
		if err := json.Unmarshal(iter.Value(), &validator); err != nil {
			continue // Skip invalid validators
		}
		validators[validator.Address] = &validator
	}

	return validators, iter.Error()
}

// Add these implementations to your StateStorage struct in storage/state.go

// SaveRawData saves arbitrary data with a given key
func (ss *StateStorage) SaveRawData(key string, data []byte) error {
	if key == "" {
		return fmt.Errorf("key cannot be empty")
	}
	if data == nil {
		return fmt.Errorf("data cannot be nil")
	}

	// Use a prefix to avoid conflicts with other storage keys
	prefixedKey := "raw:" + key
	return ss.storage.Set([]byte(prefixedKey), data)
}

// GetRawData retrieves arbitrary data by key
func (ss *StateStorage) GetRawData(key string) ([]byte, error) {
	if key == "" {
		return nil, fmt.Errorf("key cannot be empty")
	}

	// Use the same prefix as SaveRawData
	prefixedKey := "raw:" + key
	data, err := ss.storage.Get([]byte(prefixedKey))
	if err != nil {
		if err == ErrKeyNotFound {
			return nil, fmt.Errorf("key not found: %s", key)
		}
		return nil, fmt.Errorf("failed to get raw data: %v", err)
	}

	return data, nil
}

// DeleteRawData removes data by key
func (ss *StateStorage) DeleteRawData(key string) error {
	if key == "" {
		return fmt.Errorf("key cannot be empty")
	}

	// Use the same prefix as SaveRawData
	prefixedKey := "raw:" + key
	err := ss.storage.Delete([]byte(prefixedKey))
	if err != nil {
		// Don't return error if key doesn't exist - deletion is idempotent
		if err == ErrKeyNotFound {
			return nil
		}
		return fmt.Errorf("failed to delete raw data: %v", err)
	}

	return nil
}

// Optional: GetRawDataWithPrefix gets all data with a given prefix (useful for assets)
func (ss *StateStorage) GetRawDataWithPrefix(prefix string) (map[string][]byte, error) {
	if prefix == "" {
		return nil, fmt.Errorf("prefix cannot be empty")
	}

	result := make(map[string][]byte)
	prefixedKey := "raw:" + prefix

	iter := ss.storage.Iterator([]byte(prefixedKey))
	defer iter.Close()

	for iter.Next() {
		// Remove the "raw:" prefix from the key to return the original key
		originalKey := string(iter.Key())
		if len(originalKey) > 4 && originalKey[:4] == "raw:" {
			originalKey = originalKey[4:] // Remove "raw:" prefix
		}

		result[originalKey] = append([]byte(nil), iter.Value()...) // Make a copy
	}

	return result, iter.Error()
}

// Optional: DeleteRawDataWithPrefix deletes all data with a given prefix
func (ss *StateStorage) DeleteRawDataWithPrefix(prefix string) (int, error) {
	if prefix == "" {
		return 0, fmt.Errorf("prefix cannot be empty")
	}

	deleted := 0
	prefixedKey := "raw:" + prefix

	iter := ss.storage.Iterator([]byte(prefixedKey))
	defer iter.Close()

	// Collect keys first to avoid modifying while iterating
	var keysToDelete [][]byte
	for iter.Next() {
		keysToDelete = append(keysToDelete, append([]byte(nil), iter.Key()...))
	}

	if err := iter.Error(); err != nil {
		return 0, fmt.Errorf("iterator error: %v", err)
	}

	// Delete collected keys
	for _, key := range keysToDelete {
		if err := ss.storage.Delete(key); err != nil {
			// Log error but continue with other deletions
			continue
		}
		deleted++
	}

	return deleted, nil
}

// SaveTotalTransactions saves the total transaction count
func (ss *StateStorage) SaveTotalTransactions(count int64) error {
	data := make([]byte, 8)
	binary.BigEndian.PutUint64(data, uint64(count))
	return ss.storage.Set([]byte("total_transactions"), data)
}

// GetTotalTransactions retrieves the total transaction count
func (ss *StateStorage) GetTotalTransactions() (int64, error) {
	data, err := ss.storage.Get([]byte("total_transactions"))
	if err != nil {
		if err == ErrKeyNotFound {
			return 0, fmt.Errorf("total transactions count not found")
		}
		return 0, fmt.Errorf("failed to get total transactions: %v", err)
	}

	if len(data) != 8 {
		return 0, fmt.Errorf("invalid total transactions data length: expected 8, got %d", len(data))
	}

	count := int64(binary.BigEndian.Uint64(data))
	return count, nil
}
