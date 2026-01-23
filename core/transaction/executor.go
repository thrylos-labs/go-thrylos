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
	"github.com/thrylos-labs/go-thrylos/storage"
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
	shardID      account.ShardID
	totalShards  int
	stateStorage *storage.StateStorage
	worldState   StateInterface
	validator    *Validator
	config       *config.Config
	evmExecutor  EVMExecutorInterface
}

// NewExecutor creates a new transaction executor
func NewExecutor(
	shardID account.ShardID,
	totalShards int,
	stateStorage *storage.StateStorage, // ← ADD THIS LINE
	worldState StateInterface,
	validator *Validator,
	cfg *config.Config,
	evmExecutor EVMExecutorInterface,
) *Executor {
	return &Executor{
		shardID:      shardID,
		totalShards:  totalShards,
		worldState:   worldState,
		stateStorage: stateStorage,
		validator:    validator,
		config:       cfg,
		evmExecutor:  evmExecutor,
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

	// ============================================================================
	// CRITICAL: ATOMIC NONCE VALIDATION
	// This prevents race conditions and double-spending
	// ============================================================================

	// Get the sender address from the transaction
	senderAddress := tx.From // Assuming tx.From contains the sender's address as string

	// Attempt to atomically increment the nonce if it matches the expected value
	success, currentNonce, err := e.stateStorage.AtomicIncrementNonce(senderAddress, tx.Nonce)

	if err != nil {
		receipt.Error = fmt.Sprintf("nonce validation failed: %v", err)
		return receipt, fmt.Errorf("nonce validation failed: %w", err)
	}

	if !success {
		// Nonce mismatch - this transaction is either:
		// 1. Too old (already processed)
		// 2. Too new (gaps in nonce sequence)
		// 3. Another transaction with same nonce was processed first (race condition prevented!)
		receipt.Error = fmt.Sprintf("nonce mismatch: expected %d, but account nonce is %d", tx.Nonce, currentNonce)
		return receipt, fmt.Errorf("nonce mismatch: expected %d, but account nonce is %d", tx.Nonce, currentNonce)
	}

	// ✅ At this point, the nonce has been atomically incremented
	// Even if execution fails below, we DO NOT roll back the nonce increment
	// This is intentional and matches Ethereum behavior to prevent replay attacks

	// ============================================================================
	// EXECUTE TRANSACTION LOGIC
	// ============================================================================

	var execErr error

	switch tx.Type {
	case core.TransactionType_TRANSFER:
		execErr = e.executeTransfer(tx, accountManager)
	case core.TransactionType_STAKE:
		execErr = e.executeStake(tx, accountManager)
	case core.TransactionType_UNSTAKE:
		execErr = e.executeUnstake(tx, accountManager)
	case core.TransactionType_DELEGATE:
		execErr = e.executeDelegate(tx, accountManager)
	case core.TransactionType_UNDELEGATE:
		execErr = e.executeUndelegate(tx, accountManager)
	case core.TransactionType_CLAIM_REWARDS:
		execErr = e.executeClaimRewards(tx, accountManager)

	// EVM CASES
	case core.TransactionType_EVM_CONTRACT_CALL:
		// executeEVMCall returns (receipt, error), so we return immediately
		// Note: Nonce is already incremented above, so we're safe
		return e.executeEVMCall(tx)

	case core.TransactionType_EVM_CONTRACT_DEPLOY:
		// executeEVMDeploy returns ONLY error. We must assign it to execErr.
		execErr = e.executeEVMDeploy(tx)

	default:
		execErr = fmt.Errorf("unknown transaction type: %v", tx.Type)
	}

	// Handle execution errors
	// IMPORTANT: Even if execution fails, we already incremented the nonce
	// This prevents replay attacks where someone tries to re-submit a failed transaction
	if execErr != nil {
		receipt.Error = execErr.Error()
		return receipt, execErr
	}

	// Mark as successful if no error
	receipt.Status = 1
	return receipt, nil
}

// ✅ FIXED: Reentrancy-protected executeEVMCall function
func (e *Executor) executeEVMCall(tx *core.Transaction) (*ExecutionReceipt, error) {
	// 1. 🛡️ SECURITY FIX (H-05): Check Chain ID
	if tx.ChainId != e.config.Network.ChainID {
		return nil, fmt.Errorf("CRITICAL: Replay protection failed. Tx ChainID '%s' != Node ChainID '%s'", tx.ChainId, e.config.Network.ChainID)
	}

	// 2. 🛡️ SECURITY FIX (H-01): Gas Math & Overflow Protection
	if tx.Gas < 0 {
		return nil, fmt.Errorf("invalid gas limit: cannot be negative")
	}
	if tx.Gas < 21000 {
		return nil, fmt.Errorf("intrinsic gas too low: %d < 21000", tx.Gas)
	}
	const MaxBlockGas = 30_000_000
	if tx.Gas > MaxBlockGas {
		return nil, fmt.Errorf("gas limit exceeds block maximum: %d > %d", tx.Gas, MaxBlockGas)
	}

	caller := common.HexToAddress(tx.From)
	contract := common.HexToAddress(tx.To)

	// 3. 🛡️ INPUT VALIDATION (Medium): Strict Amount Parsing
	value := new(big.Int)
	if _, ok := value.SetString(tx.Amount, 10); !ok {
		return nil, fmt.Errorf("invalid transaction amount: %s", tx.Amount)
	}
	if value.Sign() < 0 {
		return nil, fmt.Errorf("transaction amount cannot be negative")
	}

	gasPrice := new(big.Int)
	if _, ok := gasPrice.SetString(tx.GasPrice, 10); !ok {
		return nil, fmt.Errorf("invalid gas price: %s", tx.GasPrice)
	}
	if gasPrice.Sign() < 0 {
		return nil, fmt.Errorf("gas price cannot be negative")
	}

	// 4. 🛡️ SECURITY FIX (H-02): Fetch Nonce EARLY
	nonce, err := e.worldState.GetNonce(tx.From)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce for execution: %v", err)
	}
	// Enforce strict nonce ordering
	if tx.Nonce != nonce {
		return nil, fmt.Errorf("nonce mismatch: expected %d, got %d", nonce, tx.Nonce)
	}

	// 5. Pre-Check Balance
	maxGasCost := new(big.Int).Mul(new(big.Int).SetUint64(uint64(tx.Gas)), gasPrice)
	totalReq := new(big.Int).Add(maxGasCost, value)

	balance, err := e.worldState.GetBalance(tx.From)
	if err != nil {
		return nil, fmt.Errorf("failed to get balance: %v", err)
	}
	if balance.Cmp(totalReq) < 0 {
		return nil, fmt.Errorf("insufficient funds for gas + value")
	}

	// ✅ SECURITY FIX (C-03): UPDATE STATE BEFORE EXTERNAL CALL
	// This prevents reentrancy attacks by ensuring:
	// 1. Nonce is incremented immediately (blocks duplicate transactions)
	// 2. Max gas + value is deducted upfront (prevents double-spending)

	// Deduct max gas + value immediately
	newBalance := new(big.Int).Sub(balance, totalReq)
	e.worldState.UpdateBalance(tx.From, newBalance)

	// 6. NOW Execute EVM Call (safe - state already updated)
	returnData, gasUsed, err := e.evmExecutor.ExecuteCall(
		caller,
		contract,
		tx.Data,
		uint64(tx.Gas),
		value,
		nonce,
	)

	// 7. Calculate actual gas cost
	gasUsedBig := new(big.Int).SetUint64(gasUsed)
	actualGasCost := new(big.Int).Mul(gasUsedBig, gasPrice)

	// 8. Refund unused gas
	// We deducted maxGasCost, but only used actualGasCost
	// So refund the difference
	gasRefund := new(big.Int).Sub(maxGasCost, actualGasCost)

	if gasRefund.Sign() > 0 {
		// Get current balance and add refund
		currentBalance, err := e.worldState.GetBalance(tx.From)
		if err != nil {
			log.Printf("⚠️ Warning: failed to get balance for gas refund: %v", err)
		} else {
			refundedBalance := new(big.Int).Add(currentBalance, gasRefund)
			e.worldState.UpdateBalance(tx.From, refundedBalance)
		}
	}

	// 9. Handle execution failure
	if err != nil {
		// On failure, refund the VALUE portion (but gas was consumed)
		// Note: Nonce stays incremented (failed txs still consume nonce)
		currentBalance, balErr := e.worldState.GetBalance(tx.From)
		if balErr != nil {
			log.Printf("⚠️ Warning: failed to get balance for value refund: %v", balErr)
		} else {
			refundedBalance := new(big.Int).Add(currentBalance, value)
			e.worldState.UpdateBalance(tx.From, refundedBalance)
		}

		return &ExecutionReceipt{
			TxHash:  tx.Hash,
			Status:  0, // Failed
			GasUsed: int64(gasUsed),
			Error:   err.Error(),
		}, fmt.Errorf("EVM execution failed: %v", err)
	}

	// ✅ Success
	log.Printf("✅ EVM call executed: gas used %d, return data len: %d", gasUsed, len(returnData))

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

	// Validate gas limit
	const maxGasLimit = 30000000
	if tx.Gas > maxGasLimit {
		return fmt.Errorf("gas limit %d exceeds maximum %d", tx.Gas, maxGasLimit)
	}
	if tx.Gas <= 0 {
		return fmt.Errorf("invalid gas limit: %d", tx.Gas)
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

	// Validate gas limit
	const maxGasLimit = 30000000
	if tx.Gas > maxGasLimit {
		return fmt.Errorf("gas limit %d exceeds maximum %d", tx.Gas, maxGasLimit)
	}
	if tx.Gas <= 0 {
		return fmt.Errorf("invalid gas limit: %d", tx.Gas)
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

	const maxGasLimit = 30000000
	if tx.Gas > maxGasLimit {
		return fmt.Errorf("gas limit %d exceeds maximum %d", tx.Gas, maxGasLimit)
	}
	if tx.Gas <= 0 {
		return fmt.Errorf("invalid gas limit: %d", tx.Gas)
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

	const maxGasLimit = 30000000
	if tx.Gas > maxGasLimit {
		return fmt.Errorf("gas limit %d exceeds maximum %d", tx.Gas, maxGasLimit)
	}
	if tx.Gas <= 0 {
		return fmt.Errorf("invalid gas limit: %d", tx.Gas)
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

	// Validate gas limit
	const maxGasLimit = 30000000
	if tx.Gas > maxGasLimit {
		return fmt.Errorf("gas limit %d exceeds maximum %d", tx.Gas, maxGasLimit)
	}
	if tx.Gas <= 0 {
		return fmt.Errorf("invalid gas limit: %d", tx.Gas)
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

	const maxGasLimit = 30000000
	if tx.Gas > maxGasLimit {
		return fmt.Errorf("gas limit %d exceeds maximum %d", tx.Gas, maxGasLimit)
	}
	if tx.Gas <= 0 {
		return fmt.Errorf("invalid gas limit: %d", tx.Gas)
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

	const maxGasLimit = 30000000
	if tx.Gas > maxGasLimit {
		return fmt.Errorf("gas limit %d exceeds maximum %d", tx.Gas, maxGasLimit)
	}
	if tx.Gas <= 0 {
		return fmt.Errorf("invalid gas limit: %d", tx.Gas)
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
