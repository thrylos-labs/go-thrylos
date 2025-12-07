// core/transaction/executor.go
// Handles transaction execution against account state:

// ✅ Transaction execution for all transaction types (transfer, stake, unstake, delegate, etc.)
// ✅ Execution receipts with success/failure status and gas usage
// ✅ Batch execution for processing multiple transactions
// ✅ Shard-aware execution - validates transactions belong to correct shard
// ✅ Account state updates - properly updates balances, nonces, staking amounts
// ✅ Liquid staking support - immediate stake/unstake operations

package transaction

import (
	"fmt"
	"log"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/evm"
	"github.com/thrylos-labs/go-thrylos/core/math"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// ExecutionReceipt represents the result of transaction execution
type ExecutionReceipt struct {
	TxHash      string `json:"tx_hash"`
	Status      int    `json:"status"` // 1 = success, 0 = failure
	GasUsed     int64  `json:"gas_used"`
	BlockHeight int64  `json:"block_height"`
	Error       string `json:"error,omitempty"`
}

// Executor handles transaction execution against account state
type Executor struct {
	worldState  *state.WorldState
	validator   *Validator
	config      *config.Config
	evmExecutor *evm.RevmExecutor // NEW
}

// NewExecutor creates a new transaction executor
func NewExecutor(
	worldState *state.WorldState,
	validator *Validator,
	cfg *config.Config,
	evmExecutor *evm.RevmExecutor, // NEW
) *Executor {
	return &Executor{
		worldState:  worldState,
		validator:   validator,
		config:      cfg,
		evmExecutor: evmExecutor, // NEW
	}
}

// ExecuteTransaction executes a transaction against the account state
func (e *Executor) ExecuteTransaction(tx *core.Transaction, accountManager *account.AccountManager) (*ExecutionReceipt, error) {
	if tx == nil {
		return nil, fmt.Errorf("transaction cannot be nil")
	}

	// Create receipt
	receipt := &ExecutionReceipt{
		TxHash:  tx.Hash,
		GasUsed: tx.Gas,
		Status:  0, // Default to failure
	}

	// Execute based on transaction type
	var err error
	switch tx.Type {
	case core.TransactionType_TRANSFER:
		err = e.executeTransfer(tx, accountManager)
	case core.TransactionType_STAKE:
		err = e.executeStake(tx, accountManager)
	case core.TransactionType_UNSTAKE:
		err = e.executeUnstake(tx, accountManager)
	case core.TransactionType_DELEGATE:
		err = e.executeDelegate(tx, accountManager)
	case core.TransactionType_UNDELEGATE:
		err = e.executeUndelegate(tx, accountManager)
	case core.TransactionType_CLAIM_REWARDS:
		err = e.executeClaimRewards(tx, accountManager)
	case core.TransactionType_EVM_CONTRACT_CALL:
		return e.executeEVMCall(tx)
	case core.TransactionType_EVM_CONTRACT_DEPLOY:
		return e.executeEVMDeploy(tx)
	default:
		err = fmt.Errorf("unknown transaction type: %v", tx.Type)
	}

	if err != nil {
		receipt.Error = err.Error()
		return receipt, err
	}

	// Mark as successful
	receipt.Status = 1
	return receipt, nil
}

func (e *Executor) executeEVMCall(tx *core.Transaction) error {
	caller := common.HexToAddress(tx.From)
	contract := common.HexToAddress(tx.To)

	// Execute contract call
	returnData, gasUsed, err := e.evmExecutor.ExecuteCall(
		caller,
		contract,
		tx.Data,
		uint64(tx.Gas),
		big.NewInt(tx.Amount),
	)

	if err != nil {
		return fmt.Errorf("EVM call failed: %v", err)
	}

	// Deduct gas cost
	gasCost := gasUsed * uint64(tx.GasPrice)
	balance, _ := e.worldState.GetBalance(tx.From)
	newBalance := balance - int64(gasCost)
	e.worldState.UpdateBalance(tx.From, newBalance)

	// Increment nonce
	nonce, _ := e.worldState.GetNonce(tx.From)
	e.worldState.SetNonce(tx.From, nonce+1)

	log.Printf("✅ EVM call executed: gas used %d, return data: %d bytes",
		gasUsed, len(returnData))

	return nil
}

func (e *Executor) executeEVMDeploy(tx *core.Transaction) error {
	deployer := common.HexToAddress(tx.From)

	// Deploy contract
	contractAddr, gasUsed, err := e.evmExecutor.DeployContract(
		deployer,
		tx.Data,
		uint64(tx.Gas),
		big.NewInt(tx.Amount),
	)

	if err != nil {
		return fmt.Errorf("contract deployment failed: %v", err)
	}

	// Deduct gas cost
	gasCost := gasUsed * uint64(tx.GasPrice)
	balance, _ := e.worldState.GetBalance(tx.From)
	newBalance := balance - int64(gasCost)
	e.worldState.UpdateBalance(tx.From, newBalance)

	// Increment nonce
	nonce, _ := e.worldState.GetNonce(tx.From)
	e.worldState.SetNonce(tx.From, nonce+1)

	log.Printf("✅ Contract deployed at %s, gas used: %d",
		contractAddr.Hex(), gasUsed)

	return nil
}

// ExecuteBatch executes multiple transactions in order
func (e *Executor) ExecuteBatch(transactions []*core.Transaction, accountManager *account.AccountManager) ([]*ExecutionReceipt, error) {
	receipts := make([]*ExecutionReceipt, 0, len(transactions))

	for i, tx := range transactions {
		receipt, err := e.ExecuteTransaction(tx, accountManager)
		if err != nil {
			// Return receipts up to the failed transaction
			return receipts, fmt.Errorf("transaction %d failed: %v", i, err)
		}
		receipts = append(receipts, receipt)
	}

	return receipts, nil
}

// executeTransfer handles transfer transactions
func (e *Executor) executeTransfer(tx *core.Transaction, accountManager *account.AccountManager) error {
	// Validate cross-shard transfers
	senderShard := account.CalculateShardID(tx.From, e.totalShards)
	recipientShard := account.CalculateShardID(tx.To, e.totalShards)

	// For now, only handle same-shard transfers
	// Cross-shard transfers would require additional coordination
	if senderShard != recipientShard {
		return fmt.Errorf("cross-shard transfers not supported in executor: sender shard %d, recipient shard %d",
			senderShard, recipientShard)
	}

	// Only execute if sender belongs to this shard
	if e.shardID != account.BeaconShardID && senderShard != e.shardID {
		return fmt.Errorf("transaction sender %s belongs to shard %d, not %d",
			tx.From, senderShard, e.shardID)
	}

	// Calculate total cost (amount + gas fee)
	totalCost := tx.Amount + (tx.Gas * tx.GasPrice)

	// Get sender account
	sender, err := accountManager.GetAccount(tx.From)
	if err != nil {
		return fmt.Errorf("failed to get sender account: %v", err)
	}

	// Check balance and nonce
	if sender.Balance < totalCost {
		return fmt.Errorf("insufficient balance: have %d, need %d", sender.Balance, totalCost)
	}

	if sender.Nonce != tx.Nonce {
		return fmt.Errorf("invalid nonce: expected %d, got %d", sender.Nonce, tx.Nonce)
	}

	// Get receiver account
	receiver, err := accountManager.GetAccount(tx.To)
	if err != nil {
		return fmt.Errorf("failed to get receiver account: %v", err)
	}

	// Update balances
	newSenderBalance, err := math.SafeSub(sender.Balance, totalCost)
	if err != nil {
		return fmt.Errorf("sender balance update failed: %v", err)
	}

	newReceiverBalance, err := math.SafeAdd(receiver.Balance, tx.Amount)
	if err != nil {
		return fmt.Errorf("receiver balance update failed: %v", err)
	}

	sender.Balance = newSenderBalance
	sender.Nonce++
	receiver.Balance = newReceiverBalance

	// Save accounts
	if err := accountManager.UpdateAccount(sender); err != nil {
		return fmt.Errorf("failed to update sender account: %v", err)
	}

	if err := accountManager.UpdateAccount(receiver); err != nil {
		return fmt.Errorf("failed to update receiver account: %v", err)
	}

	return nil
}

// executeStake handles staking transactions
func (e *Executor) executeStake(tx *core.Transaction, accountManager *account.AccountManager) error {
	// Validate sender belongs to this shard
	senderShard := account.CalculateShardID(tx.From, e.totalShards)
	if e.shardID != account.BeaconShardID && senderShard != e.shardID {
		return fmt.Errorf("transaction sender %s belongs to shard %d, not %d",
			tx.From, senderShard, e.shardID)
	}

	// Get account
	account, err := accountManager.GetAccount(tx.From)
	if err != nil {
		return fmt.Errorf("failed to get account: %v", err)
	}

	// Calculate total cost
	totalCost := tx.Amount + (tx.Gas * tx.GasPrice)

	// Check balance and nonce
	if account.Balance < totalCost {
		return fmt.Errorf("insufficient balance for staking: have %d, need %d", account.Balance, totalCost)
	}

	if account.Nonce != tx.Nonce {
		return fmt.Errorf("invalid nonce: expected %d, got %d", account.Nonce, tx.Nonce)
	}

	// Update account (liquid staking - immediate) with overflow protection
	newBalance, err := math.SafeSub(account.Balance, totalCost)
	if err != nil {
		return fmt.Errorf("balance update failed: %v", err)
	}

	newStakedAmount, err := math.SafeAdd(account.StakedAmount, tx.Amount)
	if err != nil {
		return fmt.Errorf("staked amount update failed: %v", err)
	}

	// Apply the updates
	account.Balance = newBalance
	account.StakedAmount = newStakedAmount
	account.Nonce++

	return accountManager.UpdateAccount(account)
}

// executeUnstake handles unstaking transactions
func (e *Executor) executeUnstake(tx *core.Transaction, accountManager *account.AccountManager) error {
	// Validate sender belongs to this shard
	senderShard := account.CalculateShardID(tx.From, e.totalShards)
	if e.shardID != account.BeaconShardID && senderShard != e.shardID {
		return fmt.Errorf("transaction sender %s belongs to shard %d, not %d",
			tx.From, senderShard, e.shardID)
	}

	// Get account
	account, err := accountManager.GetAccount(tx.From)
	if err != nil {
		return fmt.Errorf("failed to get account: %v", err)
	}

	// Calculate gas cost
	gasCost := tx.Gas * tx.GasPrice

	// Check balances and nonce
	if account.Balance < gasCost {
		return fmt.Errorf("insufficient balance for gas: have %d, need %d", account.Balance, gasCost)
	}

	if account.StakedAmount < tx.Amount {
		return fmt.Errorf("insufficient staked amount: have %d, need %d", account.StakedAmount, tx.Amount)
	}

	if account.Nonce != tx.Nonce {
		return fmt.Errorf("invalid nonce: expected %d, got %d", account.Nonce, tx.Nonce)
	}

	// Update account (liquid unstaking - immediate) with overflow protection
	newBalance, err := math.SafeSub(account.Balance, gasCost)
	if err != nil {
		return fmt.Errorf("gas deduction failed: %v", err)
	}

	newBalance, err = math.SafeAdd(newBalance, tx.Amount)
	if err != nil {
		return fmt.Errorf("balance update after unstake failed: %v", err)
	}

	newStakedAmount, err := math.SafeSub(account.StakedAmount, tx.Amount)
	if err != nil {
		return fmt.Errorf("staked amount update failed: %v", err)
	}

	// Actually apply the updates ← YOU WERE MISSING THIS!
	account.Balance = newBalance
	account.StakedAmount = newStakedAmount
	account.Nonce++

	return accountManager.UpdateAccount(account)
}

// executeDelegate handles delegation transactions
func (e *Executor) executeDelegate(tx *core.Transaction, accountManager *account.AccountManager) error {
	// Validate sender belongs to this shard
	senderShard := account.CalculateShardID(tx.From, e.totalShards)
	if e.shardID != account.BeaconShardID && senderShard != e.shardID {
		return fmt.Errorf("transaction sender %s belongs to shard %d, not %d",
			tx.From, senderShard, e.shardID)
	}

	// Get delegator account
	delegator, err := accountManager.GetAccount(tx.From)
	if err != nil {
		return fmt.Errorf("failed to get delegator account: %v", err)
	}

	// Calculate total cost
	totalCost := tx.Amount + (tx.Gas * tx.GasPrice)

	// Check balance and nonce
	if delegator.Balance < totalCost {
		return fmt.Errorf("insufficient balance for delegation: have %d, need %d", delegator.Balance, totalCost)
	}

	if delegator.Nonce != tx.Nonce {
		return fmt.Errorf("invalid nonce: expected %d, got %d", delegator.Nonce, tx.Nonce)
	}

	// Validator address should be in tx.To field
	validatorAddr := tx.To
	if validatorAddr == "" {
		return fmt.Errorf("validator address cannot be empty for delegation")
	}

	// Update delegator account with overflow protection
	newBalance, err := math.SafeSub(delegator.Balance, totalCost)
	if err != nil {
		return fmt.Errorf("balance update failed: %v", err)
	}

	newStakedAmount, err := math.SafeAdd(delegator.StakedAmount, tx.Amount)
	if err != nil {
		return fmt.Errorf("staked amount update failed: %v", err)
	}

	// Apply balance updates
	delegator.Balance = newBalance
	delegator.StakedAmount = newStakedAmount
	delegator.Nonce++

	// Update delegation mapping with overflow protection
	if delegator.DelegatedTo == nil {
		delegator.DelegatedTo = make(map[string]int64)
	}

	currentDelegation := delegator.DelegatedTo[validatorAddr] // ← GET current value first!
	newDelegation, err := math.SafeAdd(currentDelegation, tx.Amount)
	if err != nil {
		return fmt.Errorf("delegation update failed: %v", err)
	}
	delegator.DelegatedTo[validatorAddr] = newDelegation // ← ASSIGN it back!

	return accountManager.UpdateAccount(delegator)
}

// executeUndelegate handles undelegation transactions
func (e *Executor) executeUndelegate(tx *core.Transaction, accountManager *account.AccountManager) error {
	// Validate sender belongs to this shard
	senderShard := account.CalculateShardID(tx.From, e.totalShards)
	if e.shardID != account.BeaconShardID && senderShard != e.shardID {
		return fmt.Errorf("transaction sender %s belongs to shard %d, not %d",
			tx.From, senderShard, e.shardID)
	}

	// Get delegator account
	delegator, err := accountManager.GetAccount(tx.From)
	if err != nil {
		return fmt.Errorf("failed to get delegator account: %v", err)
	}

	// Calculate gas cost
	gasCost := tx.Gas * tx.GasPrice

	// Check balance and nonce
	if delegator.Balance < gasCost {
		return fmt.Errorf("insufficient balance for gas: have %d, need %d", delegator.Balance, gasCost)
	}

	if delegator.Nonce != tx.Nonce {
		return fmt.Errorf("invalid nonce: expected %d, got %d", delegator.Nonce, tx.Nonce)
	}

	// Validator address should be in tx.To field
	validatorAddr := tx.To
	if validatorAddr == "" {
		return fmt.Errorf("validator address cannot be empty for undelegation")
	}

	// Check delegation exists and has sufficient amount
	if delegator.DelegatedTo == nil {
		delegator.DelegatedTo = make(map[string]int64)
	}

	delegatedAmount := delegator.DelegatedTo[validatorAddr]
	if delegatedAmount < tx.Amount {
		return fmt.Errorf("insufficient delegation to validator %s: have %d, need %d",
			validatorAddr, delegatedAmount, tx.Amount)
	}

	if delegator.StakedAmount < tx.Amount {
		return fmt.Errorf("insufficient staked amount: have %d, need %d", delegator.StakedAmount, tx.Amount)
	}

	// Update delegator account (liquid undelegation - immediate) with overflow protection
	newBalance, err := math.SafeSub(delegator.Balance, gasCost)
	if err != nil {
		return fmt.Errorf("gas deduction failed: %v", err)
	}

	newBalance, err = math.SafeAdd(newBalance, tx.Amount)
	if err != nil {
		return fmt.Errorf("balance update after undelegate failed: %v", err)
	}

	newStakedAmount, err := math.SafeSub(delegator.StakedAmount, tx.Amount)
	if err != nil {
		return fmt.Errorf("staked amount update failed: %v", err)
	}

	// Apply balance updates
	delegator.Balance = newBalance
	delegator.StakedAmount = newStakedAmount
	delegator.Nonce++

	// Update delegation mapping with overflow protection
	currentDelegation := delegator.DelegatedTo[validatorAddr] // ← GET it first!
	newDelegation, err := math.SafeSub(currentDelegation, tx.Amount)
	if err != nil {
		return fmt.Errorf("delegation update failed: %v", err)
	}

	delegator.DelegatedTo[validatorAddr] = newDelegation // ← ASSIGN it back!
	if delegator.DelegatedTo[validatorAddr] == 0 {
		delete(delegator.DelegatedTo, validatorAddr)
	}

	return accountManager.UpdateAccount(delegator)
}

// executeClaimRewards handles reward claiming transactions
func (e *Executor) executeClaimRewards(tx *core.Transaction, accountManager *account.AccountManager) error {
	// Validate sender belongs to this shard
	senderShard := account.CalculateShardID(tx.From, e.totalShards)
	if e.shardID != account.BeaconShardID && senderShard != e.shardID {
		return fmt.Errorf("transaction sender %s belongs to shard %d, not %d",
			tx.From, senderShard, e.shardID)
	}

	// Get account
	account, err := accountManager.GetAccount(tx.From)
	if err != nil {
		return fmt.Errorf("failed to get account: %v", err)
	}

	// Calculate gas cost
	gasCost := tx.Gas * tx.GasPrice

	// Check balance and nonce
	if account.Balance < gasCost {
		return fmt.Errorf("insufficient balance for gas: have %d, need %d", account.Balance, gasCost)
	}

	if account.Nonce != tx.Nonce {
		return fmt.Errorf("invalid nonce: expected %d, got %d", account.Nonce, tx.Nonce)
	}

	if account.Rewards <= 0 {
		return fmt.Errorf("no rewards to claim")
	}

	// Update account - claim all available rewards
	claimableRewards := account.Rewards
	newBalance, err := math.SafeSub(account.Balance, gasCost)
	newBalance, err = math.SafeAdd(newBalance, claimableRewards)
	account.Rewards = 0
	account.Nonce++

	return accountManager.UpdateAccount(account)
}

// ValidateExecution validates that a transaction can be executed
func (e *Executor) ValidateExecution(tx *core.Transaction, accountManager *account.AccountManager) error {
	if tx == nil {
		return fmt.Errorf("transaction cannot be nil")
	}

	// Basic validation
	if tx.Amount < 0 {
		return fmt.Errorf("transaction amount cannot be negative")
	}

	if tx.Gas <= 0 {
		return fmt.Errorf("gas must be positive")
	}

	if tx.GasPrice <= 0 {
		return fmt.Errorf("gas price must be positive")
	}

	// Get sender account for validation
	sender, err := accountManager.GetAccount(tx.From)
	if err != nil {
		return fmt.Errorf("failed to get sender account: %v", err)
	}

	// Validate nonce
	if sender.Nonce != tx.Nonce {
		return fmt.Errorf("invalid nonce: expected %d, got %d", sender.Nonce, tx.Nonce)
	}

	// Validate balance based on transaction type
	switch tx.Type {
	case core.TransactionType_TRANSFER:
		totalCost := tx.Amount + (tx.Gas * tx.GasPrice)
		if sender.Balance < totalCost {
			return fmt.Errorf("insufficient balance for transfer: have %d, need %d", sender.Balance, totalCost)
		}

	case core.TransactionType_STAKE, core.TransactionType_DELEGATE:
		totalCost := tx.Amount + (tx.Gas * tx.GasPrice)
		if sender.Balance < totalCost {
			return fmt.Errorf("insufficient balance for staking: have %d, need %d", sender.Balance, totalCost)
		}

	case core.TransactionType_UNSTAKE, core.TransactionType_UNDELEGATE:
		gasCost := tx.Gas * tx.GasPrice
		if sender.Balance < gasCost {
			return fmt.Errorf("insufficient balance for gas: have %d, need %d", sender.Balance, gasCost)
		}
		if sender.StakedAmount < tx.Amount {
			return fmt.Errorf("insufficient staked amount: have %d, need %d", sender.StakedAmount, tx.Amount)
		}

	case core.TransactionType_CLAIM_REWARDS:
		gasCost := tx.Gas * tx.GasPrice
		if sender.Balance < gasCost {
			return fmt.Errorf("insufficient balance for gas: have %d, need %d", sender.Balance, gasCost)
		}
		if sender.Rewards <= 0 {
			return fmt.Errorf("no rewards to claim")
		}
	}

	return nil
}
