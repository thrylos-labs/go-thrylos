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

	"github.com/dgraph-io/badger/v3"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/types"
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

// SaveConsensusVote persists a vote to prevent double-voting after restart
func (ss *StateStorage) SaveConsensusVote(vote *types.Vote) error {
	key := []byte(fmt.Sprintf("consensus:vote:%d:%s", vote.TargetEpoch, vote.ValidatorAddress))
	data, _ := json.Marshal(vote) // Import encoding/json
	return ss.storage.Set(key, data)
}

// HasVoted checks if we already voted for a specific epoch
func (ss *StateStorage) HasVoted(epoch uint64, validatorAddr string) (bool, error) {
	key := []byte(fmt.Sprintf("consensus:vote:%d:%s", epoch, validatorAddr))
	return ss.storage.Has(key)
}

// Returns true if successful, false if nonce mismatch (someone else incremented it)
func (ss *StateStorage) AtomicIncrementNonce(address string, expectedNonce uint64) (success bool, currentNonce uint64, err error) {
	// Build the key for this address's nonce
	key := []byte("nonce_" + address)

	// Use AtomicUpdate which provides a badger transaction
	err = ss.storage.AtomicUpdate(func(txn *badger.Txn) error {
		// Read current nonce inside the transaction
		item, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			// Key doesn't exist, current nonce is 0
			currentNonce = 0
		} else if err != nil {
			return err
		} else {
			// Parse the existing nonce value
			err = item.Value(func(val []byte) error {
				currentNonce = binary.BigEndian.Uint64(val)
				return nil
			})
			if err != nil {
				return err
			}
		}

		// Check if nonce matches expected value
		if currentNonce != expectedNonce {
			success = false
			return nil // Not an error, just a mismatch
		}

		// Increment and write back atomically within the same transaction
		newNonce := currentNonce + 1
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, newNonce)

		err = txn.Set(key, buf)
		if err != nil {
			return err
		}

		success = true
		return nil
	})

	return success, currentNonce, err
}

// GetNonce reads the current nonce (read-only, no increment)
func (ss *StateStorage) GetNonce(address string) (uint64, error) {
	key := []byte("nonce_" + address)

	// Simple read operation
	val, err := ss.storage.Get(key)
	if err != nil {
		// If key doesn't exist, return nonce 0 (this is expected for new accounts)
		if err == badger.ErrKeyNotFound {
			return 0, nil
		}
		return 0, err
	}

	// Parse and return the nonce
	nonce := binary.BigEndian.Uint64(val)
	return nonce, nil
}

func (ss *StateStorage) SetMetadata(key, value string) error {
	return ss.storage.Set([]byte("meta:"+key), []byte(value))
}

func (ss *StateStorage) GetMetadata(key string) (string, error) {
	data, err := ss.storage.Get([]byte("meta:" + key))
	if err != nil {
		if err == ErrKeyNotFound {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}
