package state

import (
	"fmt"
	"math/big"

	coremath "github.com/thrylos-labs/go-thrylos/core/math"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// simulationStore is a copy-on-write account overlay used during state root simulation.
// Reads fall through to the real WorldState on first access, then operate on copies.
// Nothing here touches the DB, cache, or real account state.
type simulationStore struct {
	overlay   map[string]*core.Account
	realState *WorldState
}

func newSimulationStore(ws *WorldState) *simulationStore {
	return &simulationStore{
		overlay:   make(map[string]*core.Account),
		realState: ws,
	}
}

func (s *simulationStore) getAccount(addr string) (*core.Account, error) {
	if acc, ok := s.overlay[addr]; ok {
		return acc, nil
	}
	real, err := s.realState.accountManager.GetAccount(addr)
	if err != nil {
		return nil, err
	}
	copied := s.deepCopyAccount(real)
	s.overlay[addr] = copied
	return copied, nil
}

func (s *simulationStore) setAccount(acc *core.Account) {
	s.overlay[acc.Address] = acc
}

func (s *simulationStore) deepCopyAccount(acc *core.Account) *core.Account {
	delegatedTo := make(map[string][]byte, len(acc.DelegatedTo))
	for k, v := range acc.DelegatedTo {
		delegatedTo[k] = append([]byte(nil), v...)
	}
	return &core.Account{
		Address:      acc.Address,
		Balance:      append([]byte(nil), acc.Balance...),
		Nonce:        acc.Nonce,
		StakedAmount: append([]byte(nil), acc.StakedAmount...),
		DelegatedTo:  delegatedTo,
		Rewards:      append([]byte(nil), acc.Rewards...),
		CodeHash:     append([]byte(nil), acc.CodeHash...),
		StorageRoot:  append([]byte(nil), acc.StorageRoot...),
	}
}

// simulationExecutor applies transaction balance effects to a simulationStore.
// Mirrors the logic in Executor.execute* methods but never touches DB or real state.
type simulationExecutor struct {
	store *simulationStore
}

func mustSimUint256Bytes(v *big.Int) []byte {
	encoded, _ := coremath.BigIntToUint256Bytes(v)
	return encoded
}

func (se *simulationExecutor) applyTransaction(tx *core.Transaction) error {
	switch tx.Type {
	case core.TransactionType_TRANSFER:
		return se.applyTransfer(tx)
	case core.TransactionType_STAKE:
		return se.applyStake(tx)
	case core.TransactionType_UNSTAKE:
		return se.applyUnstake(tx)
	case core.TransactionType_DELEGATE:
		return se.applyDelegate(tx)
	case core.TransactionType_UNDELEGATE:
		return se.applyUndelegate(tx)
	case core.TransactionType_CLAIM_REWARDS:
		return se.applyClaimRewards(tx)
	case core.TransactionType_EVM_CONTRACT_CALL,
		core.TransactionType_EVM_CONTRACT_DEPLOY:
		return se.applyEVMGasCost(tx)
	default:
		return fmt.Errorf("unknown transaction type for simulation: %v", tx.Type)
	}
}

func (se *simulationExecutor) applyTransfer(tx *core.Transaction) error {
	sender, err := se.store.getAccount(tx.From)
	if err != nil {
		return fmt.Errorf("simulation: failed to get sender %s: %w", tx.From, err)
	}
	receiver, err := se.store.getAccount(tx.To)
	if err != nil {
		return fmt.Errorf("simulation: failed to get receiver %s: %w", tx.To, err)
	}

	amountBig := parseBigIntSim(tx.Amount)
	gasCost := new(big.Int).Mul(big.NewInt(tx.Gas), parseBigIntSim(tx.GasPrice))
	totalCost := new(big.Int).Add(amountBig, gasCost)

	senderBal := parseBigIntSim(sender.Balance)
	if senderBal.Cmp(totalCost) < 0 {
		return fmt.Errorf("simulation: insufficient balance in transfer")
	}

	receiverBal := parseBigIntSim(receiver.Balance)
	senderBal.Sub(senderBal, totalCost)
	receiverBal.Add(receiverBal, amountBig)

	sender.Balance = mustSimUint256Bytes(senderBal)
	sender.Nonce++
	receiver.Balance = mustSimUint256Bytes(receiverBal)

	se.store.setAccount(sender)
	se.store.setAccount(receiver)
	return nil
}

func (se *simulationExecutor) applyStake(tx *core.Transaction) error {
	acc, err := se.store.getAccount(tx.From)
	if err != nil {
		return fmt.Errorf("simulation: failed to get account %s: %w", tx.From, err)
	}

	amountBig := parseBigIntSim(tx.Amount)
	gasCost := new(big.Int).Mul(big.NewInt(tx.Gas), parseBigIntSim(tx.GasPrice))
	totalCost := new(big.Int).Add(amountBig, gasCost)

	bal := parseBigIntSim(acc.Balance)
	if bal.Cmp(totalCost) < 0 {
		return fmt.Errorf("simulation: insufficient balance for stake")
	}

	staked := parseBigIntSim(acc.StakedAmount)
	bal.Sub(bal, totalCost)
	staked.Add(staked, amountBig)

	acc.Balance = mustSimUint256Bytes(bal)
	acc.StakedAmount = mustSimUint256Bytes(staked)
	acc.Nonce++

	se.store.setAccount(acc)
	return nil
}

func (se *simulationExecutor) applyUnstake(tx *core.Transaction) error {
	acc, err := se.store.getAccount(tx.From)
	if err != nil {
		return fmt.Errorf("simulation: failed to get account %s: %w", tx.From, err)
	}

	amountBig := parseBigIntSim(tx.Amount)
	gasCost := new(big.Int).Mul(big.NewInt(tx.Gas), parseBigIntSim(tx.GasPrice))

	bal := parseBigIntSim(acc.Balance)
	staked := parseBigIntSim(acc.StakedAmount)

	if bal.Cmp(gasCost) < 0 {
		return fmt.Errorf("simulation: insufficient balance for gas in unstake")
	}
	if staked.Cmp(amountBig) < 0 {
		return fmt.Errorf("simulation: insufficient staked amount")
	}

	bal.Sub(bal, gasCost)
	bal.Add(bal, amountBig)
	staked.Sub(staked, amountBig)

	acc.Balance = mustSimUint256Bytes(bal)
	acc.StakedAmount = mustSimUint256Bytes(staked)
	acc.Nonce++

	se.store.setAccount(acc)
	return nil
}

func (se *simulationExecutor) applyDelegate(tx *core.Transaction) error {
	acc, err := se.store.getAccount(tx.From)
	if err != nil {
		return fmt.Errorf("simulation: failed to get account %s: %w", tx.From, err)
	}

	amountBig := parseBigIntSim(tx.Amount)
	gasCost := new(big.Int).Mul(big.NewInt(tx.Gas), parseBigIntSim(tx.GasPrice))
	totalCost := new(big.Int).Add(amountBig, gasCost)

	bal := parseBigIntSim(acc.Balance)
	if bal.Cmp(totalCost) < 0 {
		return fmt.Errorf("simulation: insufficient balance for delegate")
	}

	bal.Sub(bal, totalCost)
	acc.Balance = mustSimUint256Bytes(bal)
	acc.Nonce++

	if acc.DelegatedTo == nil {
		acc.DelegatedTo = make(map[string][]byte)
	}
	existing := parseBigIntSim(acc.DelegatedTo[tx.To])
	existing.Add(existing, amountBig)
	acc.DelegatedTo[tx.To] = mustSimUint256Bytes(existing)

	se.store.setAccount(acc)
	return nil
}

func (se *simulationExecutor) applyUndelegate(tx *core.Transaction) error {
	acc, err := se.store.getAccount(tx.From)
	if err != nil {
		return fmt.Errorf("simulation: failed to get account %s: %w", tx.From, err)
	}

	amountBig := parseBigIntSim(tx.Amount)
	gasCost := new(big.Int).Mul(big.NewInt(tx.Gas), parseBigIntSim(tx.GasPrice))

	bal := parseBigIntSim(acc.Balance)
	delegated := parseBigIntSim(acc.DelegatedTo[tx.To])

	if bal.Cmp(gasCost) < 0 {
		return fmt.Errorf("simulation: insufficient balance for gas in undelegate")
	}
	if delegated.Cmp(amountBig) < 0 {
		return fmt.Errorf("simulation: insufficient delegated amount")
	}

	bal.Sub(bal, gasCost)
	bal.Add(bal, amountBig)
	delegated.Sub(delegated, amountBig)

	acc.Balance = mustSimUint256Bytes(bal)
	acc.Nonce++

	if acc.DelegatedTo == nil {
		acc.DelegatedTo = make(map[string][]byte)
	}
	if delegated.Sign() == 0 {
		delete(acc.DelegatedTo, tx.To)
	} else {
		acc.DelegatedTo[tx.To] = mustSimUint256Bytes(delegated)
	}

	se.store.setAccount(acc)
	return nil
}

func (se *simulationExecutor) applyClaimRewards(tx *core.Transaction) error {
	acc, err := se.store.getAccount(tx.From)
	if err != nil {
		return fmt.Errorf("simulation: failed to get account %s: %w", tx.From, err)
	}

	gasCost := new(big.Int).Mul(big.NewInt(tx.Gas), parseBigIntSim(tx.GasPrice))
	bal := parseBigIntSim(acc.Balance)

	if bal.Cmp(gasCost) < 0 {
		return fmt.Errorf("simulation: insufficient balance for gas in claim rewards")
	}

	rewards := parseBigIntSim(acc.Rewards)
	bal.Sub(bal, gasCost)
	bal.Add(bal, rewards)

	acc.Balance = mustSimUint256Bytes(bal)
	acc.Rewards = nil
	acc.Nonce++

	se.store.setAccount(acc)
	return nil
}

func (se *simulationExecutor) applyEVMGasCost(tx *core.Transaction) error {
	acc, err := se.store.getAccount(tx.From)
	if err != nil {
		return fmt.Errorf("simulation: failed to get account %s: %w", tx.From, err)
	}

	gasCost := new(big.Int).Mul(big.NewInt(tx.Gas), parseBigIntSim(tx.GasPrice))
	bal := parseBigIntSim(acc.Balance)

	if bal.Cmp(gasCost) < 0 {
		return fmt.Errorf("simulation: insufficient balance for EVM gas")
	}

	bal.Sub(bal, gasCost)
	acc.Balance = mustSimUint256Bytes(bal)
	acc.Nonce++

	se.store.setAccount(acc)
	return nil
}

// parseBigIntSim safely parses a canonical uint256 byte slice into a big.Int.
// Returns zero on empty or invalid input rather than panicking.
func parseBigIntSim(raw []byte) *big.Int {
	return coremath.ParseBigInt(raw)
}
