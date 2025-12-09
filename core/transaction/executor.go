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
	"github.com/thrylos-labs/go-thrylos/core/math"
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
	shardID     account.ShardID
	totalShards int
	worldState  StateInterface
	validator   *Validator
	config      *config.Config
	evmExecutor EVMExecutorInterface
}

// NewExecutor creates a new transaction executor
func NewExecutor(
	shardID account.ShardID,
	totalShards int,
	worldState StateInterface,
	validator *Validator,
	cfg *config.Config,
	evmExecutor EVMExecutorInterface,
) *Executor {
	return &Executor{
		shardID:     shardID,
		totalShards: totalShards,
		worldState:  worldState,
		validator:   validator,
		config:      cfg,
		evmExecutor: evmExecutor,
	}
}

// ExecuteTransaction executes a transaction against the account state
func (e *Executor) ExecuteTransaction(tx *core.Transaction, accountManager *account.AccountManager) (*ExecutionReceipt, error) {
	if tx == nil {
		return nil, fmt.Errorf("transaction cannot be nil")
	}

	// Create initial receipt
	receipt := &ExecutionReceipt{
		TxHash:  tx.Hash,
		GasUsed: tx.Gas,
		Status:  0, // Default to failure
	}

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

	// EVM CASES
	case core.TransactionType_EVM_CONTRACT_CALL:
		// executeEVMCall returns (receipt, error), so we return immediately
		return e.executeEVMCall(tx)

	case core.TransactionType_EVM_CONTRACT_DEPLOY:
		// executeEVMDeploy returns ONLY error. We must assign it to err.
		err = e.executeEVMDeploy(tx)

	default:
		err = fmt.Errorf("unknown transaction type: %v", tx.Type)
	}

	// Handle standard errors (Transfer, Stake, Deploy, etc.)
	if err != nil {
		receipt.Error = err.Error()
		return receipt, err
	}

	// Mark as successful if no error
	receipt.Status = 1
	return receipt, nil
}

func (e *Executor) executeEVMCall(tx *core.Transaction) (*ExecutionReceipt, error) {
	caller := common.HexToAddress(tx.From)
	contract := common.HexToAddress(tx.To)

	// Convert Amount string to BigInt
	value := math.ParseBigInt(tx.Amount)

	// Execute contract call
	returnData, gasUsed, err := e.evmExecutor.ExecuteCall(
		caller,
		contract,
		tx.Data,
		uint64(tx.Gas),
		value,
	)

	if err != nil {
		return nil, fmt.Errorf("EVM call failed: %v", err)
	}

	// Deduct gas cost
	// GasCost = GasUsed * GasPrice
	gasUsedBig := new(big.Int).SetUint64(gasUsed)
	gasPriceBig := math.ParseBigInt(tx.GasPrice)
	gasCostBig := new(big.Int).Mul(gasUsedBig, gasPriceBig)

	// Fetch Balance
	balanceBig, err := e.worldState.GetBalance(tx.From)
	if err != nil {
		return nil, fmt.Errorf("failed to get balance for gas deduction: %v", err)
	}

	// Check if balance is sufficient
	if balanceBig.Cmp(gasCostBig) < 0 {
		return nil, fmt.Errorf("insufficient balance for EVM gas")
	}

	// Update Balance
	balanceBig.Sub(balanceBig, gasCostBig)
	e.worldState.UpdateBalance(tx.From, balanceBig)

	// Increment nonce
	nonce, _ := e.worldState.GetNonce(tx.From)
	e.worldState.SetNonce(tx.From, nonce+1)

	log.Printf("✅ EVM call executed: gas used %d, return data: %d bytes",
		gasUsed, len(returnData))

	return &ExecutionReceipt{
		TxHash:  tx.Hash,
		Status:  1,
		GasUsed: int64(gasUsed),
	}, nil
}

func (e *Executor) executeEVMDeploy(tx *core.Transaction) error {
	deployer := common.HexToAddress(tx.From)
	value := math.ParseBigInt(tx.Amount)

	// Deploy contract
	contractAddr, gasUsed, err := e.evmExecutor.DeployContract(
		deployer,
		tx.Data,
		uint64(tx.Gas),
		value,
	)

	if err != nil {
		return fmt.Errorf("contract deployment failed: %v", err)
	}

	// Deduct gas cost
	gasUsedBig := new(big.Int).SetUint64(gasUsed)
	gasPriceBig := math.ParseBigInt(tx.GasPrice)
	gasCostBig := new(big.Int).Mul(gasUsedBig, gasPriceBig)

	// Fetch Balance
	balanceBig, err := e.worldState.GetBalance(tx.From)
	if err != nil {
		return fmt.Errorf("failed to get balance for gas deduction: %v", err)
	}

	if balanceBig.Cmp(gasCostBig) < 0 {
		return fmt.Errorf("insufficient balance for deployment gas")
	}

	// Update Balance
	balanceBig.Sub(balanceBig, gasCostBig)
	e.worldState.UpdateBalance(tx.From, balanceBig)

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

	if senderShard != recipientShard {
		return fmt.Errorf("cross-shard transfers not supported in executor: sender shard %d, recipient shard %d",
			senderShard, recipientShard)
	}

	if e.shardID != account.BeaconShardID && senderShard != e.shardID {
		return fmt.Errorf("transaction sender %s belongs to shard %d, not %d",
			tx.From, senderShard, e.shardID)
	}

	// 1. Calculate Total Cost (Amount + Gas*GasPrice)
	amountBig := math.ParseBigInt(tx.Amount)
	gasLimitBig := big.NewInt(tx.Gas)
	gasPriceBig := math.ParseBigInt(tx.GasPrice)
	gasCostBig := new(big.Int).Mul(gasLimitBig, gasPriceBig)
	totalCostBig := new(big.Int).Add(amountBig, gasCostBig)

	// Get sender account
	sender, err := accountManager.GetAccount(tx.From)
	if err != nil {
		return fmt.Errorf("failed to get sender account: %v", err)
	}

	senderBalanceBig := math.ParseBigInt(sender.Balance)

	// Check balance
	if senderBalanceBig.Cmp(totalCostBig) < 0 {
		return fmt.Errorf("insufficient balance: have %s, need %s", sender.Balance, totalCostBig.String())
	}

	if sender.Nonce != tx.Nonce {
		return fmt.Errorf("invalid nonce: expected %d, got %d", sender.Nonce, tx.Nonce)
	}

	// Get receiver account
	receiver, err := accountManager.GetAccount(tx.To)
	if err != nil {
		return fmt.Errorf("failed to get receiver account: %v", err)
	}
	receiverBalanceBig := math.ParseBigInt(receiver.Balance)

	// Update balances
	senderBalanceBig.Sub(senderBalanceBig, totalCostBig)
	receiverBalanceBig.Add(receiverBalanceBig, amountBig)

	sender.Balance = senderBalanceBig.String()
	sender.Nonce++
	receiver.Balance = receiverBalanceBig.String()

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
	senderShard := account.CalculateShardID(tx.From, e.totalShards)
	if e.shardID != account.BeaconShardID && senderShard != e.shardID {
		return fmt.Errorf("transaction sender %s belongs to shard %d, not %d",
			tx.From, senderShard, e.shardID)
	}

	account, err := accountManager.GetAccount(tx.From)
	if err != nil {
		return fmt.Errorf("failed to get account: %v", err)
	}

	// 1. Calculate Total Cost
	amountBig := math.ParseBigInt(tx.Amount)
	gasLimitBig := big.NewInt(tx.Gas)
	gasPriceBig := math.ParseBigInt(tx.GasPrice)
	gasCostBig := new(big.Int).Mul(gasLimitBig, gasPriceBig)
	totalCostBig := new(big.Int).Add(amountBig, gasCostBig)

	senderBalanceBig := math.ParseBigInt(account.Balance)

	// Check balance
	if senderBalanceBig.Cmp(totalCostBig) < 0 {
		return fmt.Errorf("insufficient balance for staking: have %s, need %s", account.Balance, totalCostBig.String())
	}

	if account.Nonce != tx.Nonce {
		return fmt.Errorf("invalid nonce: expected %d, got %d", account.Nonce, tx.Nonce)
	}

	// Update account
	senderBalanceBig.Sub(senderBalanceBig, totalCostBig)

	stakedAmountBig := math.ParseBigInt(account.StakedAmount)
	stakedAmountBig.Add(stakedAmountBig, amountBig)

	account.Balance = senderBalanceBig.String()
	account.StakedAmount = stakedAmountBig.String()
	account.Nonce++

	return accountManager.UpdateAccount(account)
}

// executeUnstake handles unstaking transactions
func (e *Executor) executeUnstake(tx *core.Transaction, accountManager *account.AccountManager) error {
	senderShard := account.CalculateShardID(tx.From, e.totalShards)
	if e.shardID != account.BeaconShardID && senderShard != e.shardID {
		return fmt.Errorf("transaction sender %s belongs to shard %d, not %d",
			tx.From, senderShard, e.shardID)
	}

	account, err := accountManager.GetAccount(tx.From)
	if err != nil {
		return fmt.Errorf("failed to get account: %v", err)
	}

	// 1. Calculate Gas Cost
	gasLimitBig := big.NewInt(tx.Gas)
	gasPriceBig := math.ParseBigInt(tx.GasPrice)
	gasCostBig := new(big.Int).Mul(gasLimitBig, gasPriceBig)

	senderBalanceBig := math.ParseBigInt(account.Balance)
	stakedAmountBig := math.ParseBigInt(account.StakedAmount)
	amountBig := math.ParseBigInt(tx.Amount)

	// Check balances
	if senderBalanceBig.Cmp(gasCostBig) < 0 {
		return fmt.Errorf("insufficient balance for gas: have %s, need %s", account.Balance, gasCostBig.String())
	}

	if stakedAmountBig.Cmp(amountBig) < 0 {
		return fmt.Errorf("insufficient staked amount: have %s, need %s", account.StakedAmount, tx.Amount)
	}

	if account.Nonce != tx.Nonce {
		return fmt.Errorf("invalid nonce: expected %d, got %d", account.Nonce, tx.Nonce)
	}

	// Update Logic:
	// 1. Deduct Gas from Balance
	senderBalanceBig.Sub(senderBalanceBig, gasCostBig)
	// 2. Add Unstaked Amount to Balance
	senderBalanceBig.Add(senderBalanceBig, amountBig)
	// 3. Deduct Amount from Staked
	stakedAmountBig.Sub(stakedAmountBig, amountBig)

	account.Balance = senderBalanceBig.String()
	account.StakedAmount = stakedAmountBig.String()
	account.Nonce++

	return accountManager.UpdateAccount(account)
}

// executeDelegate handles delegation transactions
func (e *Executor) executeDelegate(tx *core.Transaction, accountManager *account.AccountManager) error {
	senderShard := account.CalculateShardID(tx.From, e.totalShards)
	if e.shardID != account.BeaconShardID && senderShard != e.shardID {
		return fmt.Errorf("transaction sender %s belongs to shard %d, not %d",
			tx.From, senderShard, e.shardID)
	}

	delegator, err := accountManager.GetAccount(tx.From)
	if err != nil {
		return fmt.Errorf("failed to get delegator account: %v", err)
	}

	// 1. Calculate Total Cost
	amountBig := math.ParseBigInt(tx.Amount)
	gasLimitBig := big.NewInt(tx.Gas)
	gasPriceBig := math.ParseBigInt(tx.GasPrice)
	gasCostBig := new(big.Int).Mul(gasLimitBig, gasPriceBig)
	totalCostBig := new(big.Int).Add(amountBig, gasCostBig)

	delegatorBalanceBig := math.ParseBigInt(delegator.Balance)

	// Check balance
	if delegatorBalanceBig.Cmp(totalCostBig) < 0 {
		return fmt.Errorf("insufficient balance for delegation: have %s, need %s", delegator.Balance, totalCostBig.String())
	}

	if delegator.Nonce != tx.Nonce {
		return fmt.Errorf("invalid nonce: expected %d, got %d", delegator.Nonce, tx.Nonce)
	}

	validatorAddr := tx.To
	if validatorAddr == "" {
		return fmt.Errorf("validator address cannot be empty for delegation")
	}

	// Update Delegator
	delegatorBalanceBig.Sub(delegatorBalanceBig, totalCostBig)

	stakedAmountBig := math.ParseBigInt(delegator.StakedAmount)
	stakedAmountBig.Add(stakedAmountBig, amountBig)

	delegator.Balance = delegatorBalanceBig.String()
	delegator.StakedAmount = stakedAmountBig.String()
	delegator.Nonce++

	// Update Delegation Map
	if delegator.DelegatedTo == nil {
		delegator.DelegatedTo = make(map[string]string)
	}

	currentDelegationStr := "0"
	if val, exists := delegator.DelegatedTo[validatorAddr]; exists {
		currentDelegationStr = val
	}
	currentDelegationBig := math.ParseBigInt(currentDelegationStr)
	currentDelegationBig.Add(currentDelegationBig, amountBig)

	delegator.DelegatedTo[validatorAddr] = currentDelegationBig.String()

	return accountManager.UpdateAccount(delegator)
}

// executeUndelegate handles undelegation transactions
func (e *Executor) executeUndelegate(tx *core.Transaction, accountManager *account.AccountManager) error {
	senderShard := account.CalculateShardID(tx.From, e.totalShards)
	if e.shardID != account.BeaconShardID && senderShard != e.shardID {
		return fmt.Errorf("transaction sender %s belongs to shard %d, not %d",
			tx.From, senderShard, e.shardID)
	}

	delegator, err := accountManager.GetAccount(tx.From)
	if err != nil {
		return fmt.Errorf("failed to get delegator account: %v", err)
	}

	// 1. Calculate Gas Cost
	gasLimitBig := big.NewInt(tx.Gas)
	gasPriceBig := math.ParseBigInt(tx.GasPrice)
	gasCostBig := new(big.Int).Mul(gasLimitBig, gasPriceBig)

	delegatorBalanceBig := math.ParseBigInt(delegator.Balance)
	stakedAmountBig := math.ParseBigInt(delegator.StakedAmount)
	amountBig := math.ParseBigInt(tx.Amount)

	// Check balances
	if delegatorBalanceBig.Cmp(gasCostBig) < 0 {
		return fmt.Errorf("insufficient balance for gas: have %s, need %s", delegator.Balance, gasCostBig.String())
	}

	if delegator.Nonce != tx.Nonce {
		return fmt.Errorf("invalid nonce: expected %d, got %d", delegator.Nonce, tx.Nonce)
	}

	validatorAddr := tx.To
	if validatorAddr == "" {
		return fmt.Errorf("validator address cannot be empty for undelegation")
	}

	// Check Map Existence
	if delegator.DelegatedTo == nil {
		delegator.DelegatedTo = make(map[string]string)
	}

	currentDelegationStr := "0"
	if val, exists := delegator.DelegatedTo[validatorAddr]; exists {
		currentDelegationStr = val
	}
	currentDelegationBig := math.ParseBigInt(currentDelegationStr)

	// Check if sufficient delegation
	if currentDelegationBig.Cmp(amountBig) < 0 {
		return fmt.Errorf("insufficient delegation to validator %s: have %s, need %s",
			validatorAddr, currentDelegationStr, tx.Amount)
	}

	if stakedAmountBig.Cmp(amountBig) < 0 {
		return fmt.Errorf("insufficient staked amount: have %s, need %s", delegator.StakedAmount, tx.Amount)
	}

	// Update Logic
	// 1. Deduct Gas from Balance
	delegatorBalanceBig.Sub(delegatorBalanceBig, gasCostBig)
	// 2. Add Undelegated Amount to Balance
	delegatorBalanceBig.Add(delegatorBalanceBig, amountBig)
	// 3. Deduct from Staked Amount
	stakedAmountBig.Sub(stakedAmountBig, amountBig)
	// 4. Deduct from Specific Delegation
	currentDelegationBig.Sub(currentDelegationBig, amountBig)

	delegator.Balance = delegatorBalanceBig.String()
	delegator.StakedAmount = stakedAmountBig.String()
	delegator.Nonce++

	if currentDelegationBig.Sign() == 0 {
		delete(delegator.DelegatedTo, validatorAddr)
	} else {
		delegator.DelegatedTo[validatorAddr] = currentDelegationBig.String()
	}

	return accountManager.UpdateAccount(delegator)
}

// executeClaimRewards handles reward claiming transactions
func (e *Executor) executeClaimRewards(tx *core.Transaction, accountManager *account.AccountManager) error {
	senderShard := account.CalculateShardID(tx.From, e.totalShards)
	if e.shardID != account.BeaconShardID && senderShard != e.shardID {
		return fmt.Errorf("transaction sender %s belongs to shard %d, not %d",
			tx.From, senderShard, e.shardID)
	}

	account, err := accountManager.GetAccount(tx.From)
	if err != nil {
		return fmt.Errorf("failed to get account: %v", err)
	}

	// 1. Calculate Gas Cost
	gasLimitBig := big.NewInt(tx.Gas)
	gasPriceBig := math.ParseBigInt(tx.GasPrice)
	gasCostBig := new(big.Int).Mul(gasLimitBig, gasPriceBig)

	senderBalanceBig := math.ParseBigInt(account.Balance)
	rewardsBig := math.ParseBigInt(account.Rewards)

	// Check balance
	if senderBalanceBig.Cmp(gasCostBig) < 0 {
		return fmt.Errorf("insufficient balance for gas: have %s, need %s", account.Balance, gasCostBig.String())
	}

	if account.Nonce != tx.Nonce {
		return fmt.Errorf("invalid nonce: expected %d, got %d", account.Nonce, tx.Nonce)
	}

	if rewardsBig.Sign() <= 0 {
		return fmt.Errorf("no rewards to claim")
	}

	// Update Account
	// 1. Deduct Gas
	senderBalanceBig.Sub(senderBalanceBig, gasCostBig)
	// 2. Add Rewards
	senderBalanceBig.Add(senderBalanceBig, rewardsBig)

	account.Balance = senderBalanceBig.String()
	account.Rewards = "0"
	account.Nonce++

	return accountManager.UpdateAccount(account)
}

// ValidateExecution validates that a transaction can be executed
func (e *Executor) ValidateExecution(tx *core.Transaction, accountManager *account.AccountManager) error {
	if tx == nil {
		return fmt.Errorf("transaction cannot be nil")
	}

	amountBig := math.ParseBigInt(tx.Amount)
	if amountBig.Sign() < 0 {
		return fmt.Errorf("transaction amount cannot be negative")
	}

	if tx.Gas <= 0 {
		return fmt.Errorf("gas must be positive")
	}

	gasPriceBig := math.ParseBigInt(tx.GasPrice)
	if gasPriceBig.Sign() <= 0 {
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

	// Calculate common costs
	gasLimitBig := big.NewInt(tx.Gas)
	gasCostBig := new(big.Int).Mul(gasLimitBig, gasPriceBig)
	totalCostBig := new(big.Int).Add(amountBig, gasCostBig)

	senderBalanceBig := math.ParseBigInt(sender.Balance)
	stakedAmountBig := math.ParseBigInt(sender.StakedAmount)
	rewardsBig := math.ParseBigInt(sender.Rewards)

	// Validate balance based on transaction type
	switch tx.Type {
	case core.TransactionType_TRANSFER:
		if senderBalanceBig.Cmp(totalCostBig) < 0 {
			return fmt.Errorf("insufficient balance for transfer: have %s, need %s", sender.Balance, totalCostBig.String())
		}

	case core.TransactionType_STAKE, core.TransactionType_DELEGATE:
		if senderBalanceBig.Cmp(totalCostBig) < 0 {
			return fmt.Errorf("insufficient balance for staking: have %s, need %s", sender.Balance, totalCostBig.String())
		}

	case core.TransactionType_UNSTAKE, core.TransactionType_UNDELEGATE:
		if senderBalanceBig.Cmp(gasCostBig) < 0 {
			return fmt.Errorf("insufficient balance for gas: have %s, need %s", sender.Balance, gasCostBig.String())
		}
		if stakedAmountBig.Cmp(amountBig) < 0 {
			return fmt.Errorf("insufficient staked amount: have %s, need %s", sender.StakedAmount, tx.Amount)
		}

	case core.TransactionType_CLAIM_REWARDS:
		if senderBalanceBig.Cmp(gasCostBig) < 0 {
			return fmt.Errorf("insufficient balance for gas: have %s, need %s", sender.Balance, gasCostBig.String())
		}
		if rewardsBig.Sign() <= 0 {
			return fmt.Errorf("no rewards to claim")
		}
	}

	return nil
}
