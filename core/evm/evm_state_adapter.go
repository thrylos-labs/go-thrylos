// core/evm/state_adapter.go
// Bridges Thrylos state to Ethereum EVM state

package evm

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// StateAdapter implements vm.StateDB interface for Thrylos
// This allows go-ethereum's EVM to interact with Thrylos state
type StateAdapter struct {
	worldState *state.WorldState

	// Track state changes for commit/revert
	journal        *journal
	validRevisions []revision
	nextRevisionId int
}

// NewStateAdapter creates a new state adapter
func NewStateAdapter(worldState *state.WorldState) *StateAdapter {
	return &StateAdapter{
		worldState:     worldState,
		journal:        newJournal(),
		validRevisions: make([]revision, 0, 16),
		nextRevisionId: 0,
	}
}

// CreateAccount creates a new account
func (s *StateAdapter) CreateAccount(addr common.Address) {
	s.journal.append(createObjectChange{account: &addr})

	// Create account in Thrylos state
	account := &core.Account{
		Address: addr.Hex(),
		Balance: 0,
		Nonce:   0,
	}
	s.worldState.CreateAccount(account)
}

// SubBalance subtracts amount from the balance of addr
func (s *StateAdapter) SubBalance(addr common.Address, amount *big.Int) {
	if amount.Sign() == 0 {
		return
	}

	s.journal.append(balanceChange{
		account: &addr,
		prev:    new(big.Int).Set(s.GetBalance(addr)),
	})

	// Update balance in Thrylos
	currentBalance := s.GetBalance(addr)
	newBalance := new(big.Int).Sub(currentBalance, amount)
	s.worldState.UpdateBalance(addr.Hex(), newBalance.Int64())
}

// AddBalance adds amount to the balance of addr
func (s *StateAdapter) AddBalance(addr common.Address, amount *big.Int) {
	if amount.Sign() == 0 {
		return
	}

	s.journal.append(balanceChange{
		account: &addr,
		prev:    new(big.Int).Set(s.GetBalance(addr)),
	})

	// Update balance in Thrylos
	currentBalance := s.GetBalance(addr)
	newBalance := new(big.Int).Add(currentBalance, amount)
	s.worldState.UpdateBalance(addr.Hex(), newBalance.Int64())
}

// GetBalance returns the balance of addr
func (s *StateAdapter) GetBalance(addr common.Address) *big.Int {
	account, err := s.worldState.GetAccount(addr.Hex())
	if err != nil {
		return big.NewInt(0)
	}
	return big.NewInt(account.Balance)
}

// GetNonce returns the nonce of addr
func (s *StateAdapter) GetNonce(addr common.Address) uint64 {
	account, err := s.worldState.GetAccount(addr.Hex())
	if err != nil {
		return 0
	}
	return account.Nonce
}

// SetNonce sets the nonce of addr
func (s *StateAdapter) SetNonce(addr common.Address, nonce uint64) {
	s.journal.append(nonceChange{
		account: &addr,
		prev:    s.GetNonce(addr),
	})

	s.worldState.SetNonce(addr.Hex(), nonce)
}

// GetCodeHash returns the code hash of addr
func (s *StateAdapter) GetCodeHash(addr common.Address) common.Hash {
	code := s.GetCode(addr)
	if len(code) == 0 {
		return common.Hash{}
	}
	return crypto.Keccak256Hash(code)
}

// GetCode returns the code of addr
func (s *StateAdapter) GetCode(addr common.Address) []byte {
	account, err := s.worldState.GetAccount(addr.Hex())
	if err != nil {
		return nil
	}

	// Get contract code from storage
	code, _ := s.worldState.GetContractCode(addr.Hex())
	return code
}

// SetCode sets the code of addr
func (s *StateAdapter) SetCode(addr common.Address, code []byte) {
	s.journal.append(codeChange{
		account:  &addr,
		prevcode: s.GetCode(addr),
		prevhash: s.GetCodeHash(addr),
	})

	s.worldState.SetContractCode(addr.Hex(), code)
}

// GetCodeSize returns the size of the code of addr
func (s *StateAdapter) GetCodeSize(addr common.Address) int {
	return len(s.GetCode(addr))
}

// AddRefund adds gas to the refund counter
func (s *StateAdapter) AddRefund(gas uint64) {
	s.journal.append(refundChange{prev: s.refund})
	s.refund += gas
}

// SubRefund removes gas from the refund counter
func (s *StateAdapter) SubRefund(gas uint64) {
	s.journal.append(refundChange{prev: s.refund})
	if gas > s.refund {
		panic("Refund counter below zero")
	}
	s.refund -= gas
}

// GetRefund returns the current value of the refund counter
func (s *StateAdapter) GetRefund() uint64 {
	return s.refund
}

// GetCommittedState returns the committed state of a storage slot
func (s *StateAdapter) GetCommittedState(addr common.Address, key common.Hash) common.Hash {
	// For simplicity, return same as GetState
	// In full implementation, track committed vs pending state separately
	return s.GetState(addr, key)
}

// GetState returns the current state of a storage slot
func (s *StateAdapter) GetState(addr common.Address, key common.Hash) common.Hash {
	value, _ := s.worldState.GetContractStorage(addr.Hex(), key.Hex())
	return common.BytesToHash(value)
}

// SetState sets the state of a storage slot
func (s *StateAdapter) SetState(addr common.Address, key, value common.Hash) {
	s.journal.append(storageChange{
		account:  &addr,
		key:      key,
		prevalue: s.GetState(addr, key),
	})

	s.worldState.SetContractStorage(addr.Hex(), key.Hex(), value.Bytes())
}

// Suicide marks the given account as suicided
func (s *StateAdapter) Suicide(addr common.Address) bool {
	s.journal.append(suicideChange{
		account:     &addr,
		prev:        s.hasSubicided(addr),
		prevbalance: new(big.Int).Set(s.GetBalance(addr)),
	})

	s.suicides[addr] = true
	return true
}

// HasSuicided returns if the contract was suicided in current transaction
func (s *StateAdapter) HasSuicided(addr common.Address) bool {
	return s.suicides[addr]
}

// Exist reports whether the given account exists in state
func (s *StateAdapter) Exist(addr common.Address) bool {
	_, err := s.worldState.GetAccount(addr.Hex())
	return err == nil
}

// Empty returns whether the given account is empty
func (s *StateAdapter) Empty(addr common.Address) bool {
	account, err := s.worldState.GetAccount(addr.Hex())
	if err != nil {
		return true
	}

	return account.Nonce == 0 &&
		account.Balance == 0 &&
		len(s.GetCode(addr)) == 0
}

// PrepareAccessList prepares the access list (EIP-2929, EIP-2930)
func (s *StateAdapter) PrepareAccessList(sender common.Address, dest *common.Address, precompiles []common.Address, txAccesses types.AccessList) {
	s.AddAddressToAccessList(sender)
	if dest != nil {
		s.AddAddressToAccessList(*dest)
	}
	for _, addr := range precompiles {
		s.AddAddressToAccessList(addr)
	}
	for _, el := range txAccesses {
		s.AddAddressToAccessList(el.Address)
		for _, key := range el.StorageKeys {
			s.AddSlotToAccessList(el.Address, key)
		}
	}
}

// AddressInAccessList checks if an address is in the access list
func (s *StateAdapter) AddressInAccessList(addr common.Address) bool {
	return s.accessList.ContainsAddress(addr)
}

// SlotInAccessList checks if a storage slot is in the access list
func (s *StateAdapter) SlotInAccessList(addr common.Address, slot common.Hash) (addressOk bool, slotOk bool) {
	return s.accessList.Contains(addr, slot)
}

// AddAddressToAccessList adds an address to the access list
func (s *StateAdapter) AddAddressToAccessList(addr common.Address) {
	if s.accessList.AddAddress(addr) {
		s.journal.append(accessListAddAccountChange{&addr})
	}
}

// AddSlotToAccessList adds a storage slot to the access list
func (s *StateAdapter) AddSlotToAccessList(addr common.Address, slot common.Hash) {
	addrMod, slotMod := s.accessList.AddSlot(addr, slot)
	if addrMod {
		s.journal.append(accessListAddAccountChange{&addr})
	}
	if slotMod {
		s.journal.append(accessListAddSlotChange{
			address: &addr,
			slot:    &slot,
		})
	}
}

// RevertToSnapshot reverts the state to a given snapshot
func (s *StateAdapter) RevertToSnapshot(revid int) {
	// Find the snapshot in the stack of valid snapshots
	idx := sort.Search(len(s.validRevisions), func(i int) bool {
		return s.validRevisions[i].id >= revid
	})
	if idx == len(s.validRevisions) || s.validRevisions[idx].id != revid {
		panic(fmt.Sprintf("revision id %v cannot be reverted", revid))
	}
	snapshot := s.validRevisions[idx].journalIndex

	// Replay the journal to undo changes
	s.journal.revert(s, snapshot)
	s.validRevisions = s.validRevisions[:idx]
}

// Snapshot returns an identifier for the current revision of the state
func (s *StateAdapter) Snapshot() int {
	id := s.nextRevisionId
	s.nextRevisionId++
	s.validRevisions = append(s.validRevisions, revision{id, s.journal.length()})
	return id
}

// AddLog adds a log
func (s *StateAdapter) AddLog(log *types.Log) {
	s.journal.append(addLogChange{txhash: s.thash})

	log.TxHash = s.thash
	log.TxIndex = uint(s.txIndex)
	log.Index = s.logSize
	s.logs[s.thash] = append(s.logs[s.thash], log)
	s.logSize++
}

// AddPreimage records a SHA3 preimage seen by the VM
func (s *StateAdapter) AddPreimage(hash common.Hash, preimage []byte) {
	if _, ok := s.preimages[hash]; !ok {
		s.journal.append(addPreimageChange{hash: hash})
		pi := make([]byte, len(preimage))
		copy(pi, preimage)
		s.preimages[hash] = pi
	}
}

// ForEachStorage iterates over storage (currently not implemented)
func (s *StateAdapter) ForEachStorage(addr common.Address, cb func(key, value common.Hash) bool) error {
	// TODO: Implement if needed for your use case
	return nil
}

// ===== Helper types =====

type revision struct {
	id           int
	journalIndex int
}

// Journal tracks state changes for revert capability
type journal struct {
	entries []journalEntry
	dirties map[common.Address]int
}

func newJournal() *journal {
	return &journal{
		dirties: make(map[common.Address]int),
	}
}

func (j *journal) append(entry journalEntry) {
	j.entries = append(j.entries, entry)
	if addr := entry.dirtied(); addr != nil {
		j.dirties[*addr]++
	}
}

func (j *journal) revert(statedb *StateAdapter, snapshot int) {
	for i := len(j.entries) - 1; i >= snapshot; i-- {
		j.entries[i].revert(statedb)

		// Remove from dirty set
		if addr := j.entries[i].dirtied(); addr != nil {
			if j.dirties[*addr]--; j.dirties[*addr] == 0 {
				delete(j.dirties, *addr)
			}
		}
	}
	j.entries = j.entries[:snapshot]
}

func (j *journal) length() int {
	return len(j.entries)
}

// Journal entry types for revertability
type journalEntry interface {
	revert(*StateAdapter)
	dirtied() *common.Address
}

type (
	createObjectChange struct {
		account *common.Address
	}

	balanceChange struct {
		account *common.Address
		prev    *big.Int
	}

	nonceChange struct {
		account *common.Address
		prev    uint64
	}

	storageChange struct {
		account  *common.Address
		key      common.Hash
		prevalue common.Hash
	}

	codeChange struct {
		account  *common.Address
		prevcode []byte
		prevhash common.Hash
	}

	refundChange struct {
		prev uint64
	}

	suicideChange struct {
		account     *common.Address
		prev        bool
		prevbalance *big.Int
	}

	accessListAddAccountChange struct {
		address *common.Address
	}

	accessListAddSlotChange struct {
		address *common.Address
		slot    *common.Hash
	}

	addLogChange struct {
		txhash common.Hash
	}

	addPreimageChange struct {
		hash common.Hash
	}
)

// Implement revert and dirtied for each change type
// (implementations omitted for brevity - follow Ethereum's pattern)

func (ch createObjectChange) revert(s *StateAdapter) {
	// Delete the account
	s.worldState.DeleteAccount(ch.account.Hex())
}

func (ch createObjectChange) dirtied() *common.Address {
	return ch.account
}

// ... implement for other types ...
