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
