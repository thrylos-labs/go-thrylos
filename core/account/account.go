// core/account/account.go

// Shard-aware account management using Blake2b hashing for consistent shard assignment
// Account validation and balance management with PoS-specific operations
// Cross-shard awareness - knows which accounts belong to which shards
// Genesis account creation for initial supply distribution
// Enhanced with staking, delegation, and reward management

package account

import (
	"encoding/binary"
	"fmt"
	"math/big" // ✅ Added for BigInt support

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/thrylos-labs/go-thrylos/storage"

	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/crypto/address"
	"github.com/thrylos-labs/go-thrylos/proto/core"
	"golang.org/x/crypto/blake2b"
)

// ShardID represents a shard identifier
type ShardID int

const (
	// BeaconShardID represents the beacon chain (shard -1)
	BeaconShardID ShardID = -1
)

// Constants as BigInt strings or handled during runtime for comparison
// keeping int64 here for reference, but will convert to BigInt in logic
const (
	MinimumStakeAmount    = int64(1000000000)  // 1 THRYLOS minimum stake
	MinimumBalance        = int64(1000000)     // 0.001 THRYLOS minimum balance
	MinimumDelegation     = int64(100000000)   // 0.1 THRYLOS minimum delegation
	MinimumTransfer       = int64(10000000)    // 0.01 THRYLOS minimum transfer
	MinimumValidatorStake = int64(32000000000) // 32 THRYLOS minimum validator stake
)

// AccountManager handles account operations with DB backing and LRU caching
type AccountManager struct {
	db          *storage.StateStorage
	cache       *lru.Cache[string, *core.Account] // Thread-safe LRU cache
	shardID     ShardID
	totalShards int
}

// NewAccountManager creates a manager with DB connection and 50k item cache
func NewAccountManager(db *storage.StateStorage, shardID ShardID, totalShards int) *AccountManager {
	// Initialize LRU cache (holds approx 10-20MB of hot accounts)
	cache, _ := lru.New[string, *core.Account](50000)

	return &AccountManager{
		db:          db,
		cache:       cache,
		shardID:     shardID,
		totalShards: totalShards,
	}
}

// CalculateShardID determines which shard an address belongs to
func CalculateShardID(addr string, totalShards int) ShardID {
	if totalShards <= 1 {
		return 0
	}

	// Use Blake2b to hash the address for consistent shard assignment
	hash := blake2b.Sum256([]byte(addr))

	// Use the first 8 bytes as uint64 for modulo operation
	shardIndex := binary.BigEndian.Uint64(hash[:8]) % uint64(totalShards)

	return ShardID(shardIndex)
}

// BelongsToShard checks if an address belongs to this shard
func (am *AccountManager) BelongsToShard(addr string) bool {
	if am.shardID == BeaconShardID {
		return true // Beacon shard can access all accounts
	}

	addressShard := CalculateShardID(addr, am.totalShards)
	return addressShard == am.shardID
}

// SetAccount is primarily used for Genesis/Testing. It bypasses validation.
func (am *AccountManager) SetAccount(addr string, account *core.Account) error {
	// Ensure the address key matches the account address
	if account.Address != addr {
		account.Address = addr
	}
	// Direct save to DB + Cache update
	return am.UpdateAccount(account)
}

// GetAccount retrieves an account from Cache -> DB -> New (if not found)
func (am *AccountManager) GetAccount(addr string) (*core.Account, error) {
	if err := address.Validate(addr); err != nil {
		return nil, fmt.Errorf("invalid address format: %v", err)
	}

	if !am.BelongsToShard(addr) {
		return nil, fmt.Errorf("address %s belongs to shard %d, not %d",
			addr, CalculateShardID(addr, am.totalShards), am.shardID)
	}

	// 1. Check Cache
	if acc, ok := am.cache.Get(addr); ok {
		return acc, nil
	}

	// 2. Check Database
	acc, err := am.db.GetAccount(addr)
	if err != nil {
		return nil, err
	}

	// 3. If not found in DB, return new empty account (standard blockchain behavior)
	if acc == nil {
		return &core.Account{
			Address:      addr,
			Balance:      "0", // ✅ Fix: String
			Nonce:        0,
			StakedAmount: "0",                     // ✅ Fix: String
			DelegatedTo:  make(map[string]string), // ✅ Fix: map[string]string
			Rewards:      "0",                     // ✅ Fix: String
		}, nil
	}

	// 4. Update Cache
	am.cache.Add(addr, acc)
	return acc, nil
}

// GetAccountReadOnly retrieves an account without creating a new one
func (am *AccountManager) GetAccountReadOnly(addr string) (*core.Account, bool) {
	if !am.BelongsToShard(addr) {
		return nil, false
	}

	// Check Cache
	if acc, ok := am.cache.Get(addr); ok {
		return acc, true
	}

	// Check DB
	acc, err := am.db.GetAccount(addr)
	if err != nil || acc == nil {
		return nil, false
	}

	// Cache it since we found it
	am.cache.Add(addr, acc)
	return acc, true
}

// AccountExists checks if an account exists in DB/Cache
func (am *AccountManager) AccountExists(addr string) bool {
	_, exists := am.GetAccountReadOnly(addr)
	return exists
}

// UpdateAccount persists changes to DB and updates Cache
func (am *AccountManager) UpdateAccount(account *core.Account) error {
	if account == nil {
		return fmt.Errorf("account cannot be nil")
	}

	if err := am.ValidateAccount(account); err != nil {
		return fmt.Errorf("account validation failed: %v", err)
	}

	if !am.BelongsToShard(account.Address) {
		return fmt.Errorf("address %s belongs to shard %d, not %d",
			account.Address, CalculateShardID(account.Address, am.totalShards), am.shardID)
	}

	// Write to DB (Persist immediately)
	if err := am.db.SaveAccount(account); err != nil {
		return err
	}

	// Update Cache
	am.cache.Add(account.Address, account)
	return nil
}

// ValidateAccount performs comprehensive account validation
func (am *AccountManager) ValidateAccount(account *core.Account) error {
	if account == nil {
		return fmt.Errorf("account cannot be nil")
	}

	if err := address.Validate(account.Address); err != nil {
		return fmt.Errorf("invalid account address: %v", err)
	}

	// ✅ Fix: Use BigInt comparisons
	zero := big.NewInt(0)

	bal, _ := new(big.Int).SetString(account.Balance, 10)
	if bal != nil && bal.Cmp(zero) < 0 {
		return fmt.Errorf("account balance cannot be negative: %s", account.Balance)
	}

	staked, _ := new(big.Int).SetString(account.StakedAmount, 10)
	if staked != nil && staked.Cmp(zero) < 0 {
		return fmt.Errorf("staked amount cannot be negative: %s", account.StakedAmount)
	}

	rewards, _ := new(big.Int).SetString(account.Rewards, 10)
	if rewards != nil && rewards.Cmp(zero) < 0 {
		return fmt.Errorf("rewards cannot be negative: %s", account.Rewards)
	}

	totalDelegated := big.NewInt(0)
	for validator, amountStr := range account.DelegatedTo {
		if err := address.Validate(validator); err != nil {
			return fmt.Errorf("invalid validator address %s: %v", validator, err)
		}

		amount, _ := new(big.Int).SetString(amountStr, 10)
		if amount == nil || amount.Sign() <= 0 {
			return fmt.Errorf("delegation amount to %s must be positive: %s", validator, amountStr)
		}
		totalDelegated.Add(totalDelegated, amount)
	}

	// Check if totalDelegated > StakedAmount
	stakedVal, _ := new(big.Int).SetString(account.StakedAmount, 10)
	if stakedVal == nil {
		stakedVal = big.NewInt(0)
	}

	if totalDelegated.Cmp(stakedVal) > 0 {
		return fmt.Errorf("total delegated amount (%s) exceeds staked amount (%s)",
			totalDelegated.String(), account.StakedAmount)
	}

	return nil
}

// Stake adds stake to an account
func (am *AccountManager) Stake(addr string, amount int64) error {
	if amount < MinimumStakeAmount {
		return fmt.Errorf("stake amount %d below minimum %d", amount, MinimumStakeAmount)
	}

	account, err := am.GetAccount(addr)
	if err != nil {
		return fmt.Errorf("failed to get account: %v", err)
	}

	amountBig := big.NewInt(amount)
	balanceBig, _ := new(big.Int).SetString(account.Balance, 10)
	if balanceBig == nil {
		balanceBig = big.NewInt(0)
	}

	if balanceBig.Cmp(amountBig) < 0 {
		return fmt.Errorf("insufficient balance: have %s, need %d", account.Balance, amount)
	}

	// Balance -= amount
	newBalance := new(big.Int).Sub(balanceBig, amountBig)
	account.Balance = newBalance.String()

	// StakedAmount += amount
	stakedBig, _ := new(big.Int).SetString(account.StakedAmount, 10)
	if stakedBig == nil {
		stakedBig = big.NewInt(0)
	}

	newStaked := new(big.Int).Add(stakedBig, amountBig)
	account.StakedAmount = newStaked.String()

	return am.UpdateAccount(account)
}

// Unstake removes stake from an account
func (am *AccountManager) Unstake(addr string, amount int64) error {
	if amount <= 0 {
		return fmt.Errorf("unstake amount must be positive")
	}

	account, err := am.GetAccount(addr)
	if err != nil {
		return fmt.Errorf("failed to get account: %v", err)
	}

	amountBig := big.NewInt(amount)
	stakedBig, _ := new(big.Int).SetString(account.StakedAmount, 10)
	if stakedBig == nil {
		stakedBig = big.NewInt(0)
	}

	if stakedBig.Cmp(amountBig) < 0 {
		return fmt.Errorf("insufficient staked amount: have %s, need %d", account.StakedAmount, amount)
	}

	totalDelegated := big.NewInt(0)
	for _, delegatedStr := range account.DelegatedTo {
		d, _ := new(big.Int).SetString(delegatedStr, 10)
		if d != nil {
			totalDelegated.Add(totalDelegated, d)
		}
	}

	// Remaining Stake check: (Staked - Amount) < TotalDelegated
	remainingStake := new(big.Int).Sub(stakedBig, amountBig)
	if remainingStake.Cmp(totalDelegated) < 0 {
		return fmt.Errorf("cannot unstake: would leave insufficient stake for delegations")
	}

	account.StakedAmount = remainingStake.String()

	// Balance += Amount
	balanceBig, _ := new(big.Int).SetString(account.Balance, 10)
	if balanceBig == nil {
		balanceBig = big.NewInt(0)
	}

	newBalance := new(big.Int).Add(balanceBig, amountBig)
	account.Balance = newBalance.String()

	return am.UpdateAccount(account)
}

// Delegate stakes tokens to a validator
func (am *AccountManager) Delegate(delegatorAddr, validatorAddr string, amount int64) error {
	if amount < MinimumDelegation {
		return fmt.Errorf("delegation amount %d below minimum %d", amount, MinimumDelegation)
	}

	if delegatorAddr == validatorAddr {
		return fmt.Errorf("cannot delegate to self")
	}

	if err := address.Validate(validatorAddr); err != nil {
		return fmt.Errorf("invalid validator address: %v", err)
	}

	delegator, err := am.GetAccount(delegatorAddr)
	if err != nil {
		return fmt.Errorf("failed to get delegator account: %v", err)
	}

	amountBig := big.NewInt(amount)

	// Balance Check
	balBig, _ := new(big.Int).SetString(delegator.Balance, 10)
	if balBig == nil {
		balBig = big.NewInt(0)
	}

	if balBig.Cmp(amountBig) < 0 {
		return fmt.Errorf("insufficient balance for delegation")
	}

	// Math updates
	newBalance := new(big.Int).Sub(balBig, amountBig)

	stakedBig, _ := new(big.Int).SetString(delegator.StakedAmount, 10)
	if stakedBig == nil {
		stakedBig = big.NewInt(0)
	}
	newStaked := new(big.Int).Add(stakedBig, amountBig)

	if delegator.DelegatedTo == nil {
		delegator.DelegatedTo = make(map[string]string)
	}

	// Update delegation map
	currentDelegationStr := delegator.DelegatedTo[validatorAddr]
	currentDelegationBig, _ := new(big.Int).SetString(currentDelegationStr, 10)
	if currentDelegationBig == nil {
		currentDelegationBig = big.NewInt(0)
	}

	newDelegation := new(big.Int).Add(currentDelegationBig, amountBig)

	// Commit strings
	delegator.Balance = newBalance.String()
	delegator.StakedAmount = newStaked.String()
	delegator.DelegatedTo[validatorAddr] = newDelegation.String()

	return am.UpdateAccount(delegator)
}

// Undelegate removes delegation from a validator
func (am *AccountManager) Undelegate(delegatorAddr, validatorAddr string, amount int64) error {
	if amount <= 0 {
		return fmt.Errorf("undelegation amount must be positive")
	}

	if err := address.Validate(validatorAddr); err != nil {
		return fmt.Errorf("invalid validator address: %v", err)
	}

	delegator, err := am.GetAccount(delegatorAddr)
	if err != nil {
		return fmt.Errorf("failed to get delegator account: %v", err)
	}

	amountBig := big.NewInt(amount)

	// Check Map
	currentDelegationStr, exists := delegator.DelegatedTo[validatorAddr]
	if !exists {
		return fmt.Errorf("no delegation found for validator %s", validatorAddr)
	}

	currentDelegationBig, _ := new(big.Int).SetString(currentDelegationStr, 10)
	if currentDelegationBig == nil {
		currentDelegationBig = big.NewInt(0)
	}

	if currentDelegationBig.Cmp(amountBig) < 0 {
		return fmt.Errorf("insufficient delegation to %s: have %s, need %d",
			validatorAddr, currentDelegationStr, amount)
	}

	// Update Map
	newDelegation := new(big.Int).Sub(currentDelegationBig, amountBig)
	if newDelegation.Sign() == 0 {
		delete(delegator.DelegatedTo, validatorAddr)
	} else {
		delegator.DelegatedTo[validatorAddr] = newDelegation.String()
	}

	// Update Staked Amount
	stakedBig, _ := new(big.Int).SetString(delegator.StakedAmount, 10)
	if stakedBig == nil {
		stakedBig = big.NewInt(0)
	}
	delegator.StakedAmount = new(big.Int).Sub(stakedBig, amountBig).String()

	// Update Balance (Refund)
	balBig, _ := new(big.Int).SetString(delegator.Balance, 10)
	if balBig == nil {
		balBig = big.NewInt(0)
	}
	delegator.Balance = new(big.Int).Add(balBig, amountBig).String()

	return am.UpdateAccount(delegator)
}

// AddRewards adds rewards to an account
func (am *AccountManager) AddRewards(addr string, rewards int64) error {
	if rewards <= 0 {
		return fmt.Errorf("rewards must be positive: %d", rewards)
	}

	account, err := am.GetAccount(addr)
	if err != nil {
		return fmt.Errorf("failed to get account: %v", err)
	}

	rewardsBig := big.NewInt(rewards)
	currentRewards, _ := new(big.Int).SetString(account.Rewards, 10)
	if currentRewards == nil {
		currentRewards = big.NewInt(0)
	}

	account.Rewards = new(big.Int).Add(currentRewards, rewardsBig).String()
	return am.UpdateAccount(account)
}

// ClaimRewards moves rewards to balance
// ✅ Fix: Return string instead of int64
func (am *AccountManager) ClaimRewards(addr string) (string, error) {
	account, err := am.GetAccount(addr)
	if err != nil {
		return "0", fmt.Errorf("failed to get account: %v", err)
	}

	rewardsBig, _ := new(big.Int).SetString(account.Rewards, 10)
	if rewardsBig == nil || rewardsBig.Sign() <= 0 {
		return "0", fmt.Errorf("no rewards to claim")
	}

	claimedRewards := rewardsBig.String()

	// Reset Rewards
	account.Rewards = "0"

	// Add to Balance
	balanceBig, _ := new(big.Int).SetString(account.Balance, 10)
	if balanceBig == nil {
		balanceBig = big.NewInt(0)
	}

	account.Balance = new(big.Int).Add(balanceBig, rewardsBig).String()

	return claimedRewards, am.UpdateAccount(account)
}

// Transfer performs a balance transfer between accounts
func (am *AccountManager) Transfer(fromAddr, toAddr string, amount int64) error {
	if amount < MinimumTransfer {
		return fmt.Errorf("transfer amount %d below minimum %d", amount, MinimumTransfer)
	}

	if fromAddr == toAddr {
		return fmt.Errorf("cannot transfer to self")
	}

	if err := address.Validate(fromAddr); err != nil {
		return fmt.Errorf("invalid sender address: %v", err)
	}
	if err := address.Validate(toAddr); err != nil {
		return fmt.Errorf("invalid recipient address: %v", err)
	}

	fromAccount, err := am.GetAccount(fromAddr)
	if err != nil {
		return fmt.Errorf("failed to get sender account: %v", err)
	}

	amountBig := big.NewInt(amount)
	fromBalBig, _ := new(big.Int).SetString(fromAccount.Balance, 10)
	if fromBalBig == nil {
		fromBalBig = big.NewInt(0)
	}

	if fromBalBig.Cmp(amountBig) < 0 {
		return fmt.Errorf("insufficient balance: have %s, need %d", fromAccount.Balance, amount)
	}

	// We only handle same-shard transfers here for simplicity logic
	// Cross-shard logic is usually in Executor, but if called directly, we validate destination
	var toAccount *core.Account
	if am.BelongsToShard(toAddr) {
		toAccount, err = am.GetAccount(toAddr)
		if err != nil {
			return fmt.Errorf("failed to get receiver account: %v", err)
		}
	} else {
		return fmt.Errorf("cross-shard transfer to %s not implemented at account level", toAddr)
	}

	// Update Balances
	fromAccount.Balance = new(big.Int).Sub(fromBalBig, amountBig).String()
	fromAccount.Nonce++

	toBalBig, _ := new(big.Int).SetString(toAccount.Balance, 10)
	if toBalBig == nil {
		toBalBig = big.NewInt(0)
	}
	toAccount.Balance = new(big.Int).Add(toBalBig, amountBig).String()

	if err := am.UpdateAccount(fromAccount); err != nil {
		return fmt.Errorf("failed to update sender account: %v", err)
	}

	if err := am.UpdateAccount(toAccount); err != nil {
		return fmt.Errorf("failed to update receiver account: %v", err)
	}

	return nil
}

// GetBalance returns the balance of an account
// ✅ Fix: Return string
func (am *AccountManager) GetBalance(addr string) (string, error) {
	account, err := am.GetAccount(addr)
	if err != nil {
		return "0", err
	}
	return account.Balance, nil
}

// GetNonce returns the nonce of an account
func (am *AccountManager) GetNonce(addr string) (uint64, error) {
	account, err := am.GetAccount(addr)
	if err != nil {
		return 0, err
	}
	return account.Nonce, nil
}

// GetStakedAmount returns the total staked amount for an account
// ✅ Fix: Return string
func (am *AccountManager) GetStakedAmount(addr string) (string, error) {
	account, err := am.GetAccount(addr)
	if err != nil {
		return "0", err
	}
	return account.StakedAmount, nil
}

// GetRewards returns the rewards for an account
// ✅ Fix: Return string
func (am *AccountManager) GetRewards(addr string) (string, error) {
	account, err := am.GetAccount(addr)
	if err != nil {
		return "0", err
	}
	return account.Rewards, nil
}

// GetDelegations returns all delegations for an account
// ✅ Fix: Return map[string]string
func (am *AccountManager) GetDelegations(addr string) (map[string]string, error) {
	account, err := am.GetAccount(addr)
	if err != nil {
		return nil, err
	}

	delegations := make(map[string]string)
	for validator, amount := range account.DelegatedTo {
		delegations[validator] = amount
	}

	return delegations, nil
}

// GetDelegationToValidator returns delegation amount to a specific validator
// ✅ Fix: Return string
func (am *AccountManager) GetDelegationToValidator(delegatorAddr, validatorAddr string) (string, error) {
	account, err := am.GetAccount(delegatorAddr)
	if err != nil {
		return "0", err
	}

	if val, ok := account.DelegatedTo[validatorAddr]; ok {
		return val, nil
	}
	return "0", nil
}

// GetTotalStakedInShard returns total staked amount using DB iterator
// ✅ Fix: Return string (BigInt)
func (am *AccountManager) GetTotalStakedInShard() string {
	accounts, err := am.db.GetAllAccounts()
	if err != nil {
		return "0"
	}

	total := big.NewInt(0)
	for _, account := range accounts {
		staked, _ := new(big.Int).SetString(account.StakedAmount, 10)
		if staked != nil {
			total.Add(total, staked)
		}
	}
	return total.String()
}

// GetTotalBalanceInShard returns total balance using DB iterator
func (am *AccountManager) GetTotalBalanceInShard() string {
	accounts, err := am.db.GetAllAccounts()
	if err != nil {
		return "0"
	}

	total := big.NewInt(0)
	for _, account := range accounts {
		bal, _ := new(big.Int).SetString(account.Balance, 10)
		if bal != nil {
			total.Add(total, bal)
		}
	}
	return total.String()
}

// GetTotalRewardsInShard returns total unclaimed rewards using DB iterator
func (am *AccountManager) GetTotalRewardsInShard() string {
	accounts, err := am.db.GetAllAccounts()
	if err != nil {
		return "0"
	}

	total := big.NewInt(0)
	for _, account := range accounts {
		rew, _ := new(big.Int).SetString(account.Rewards, 10)
		if rew != nil {
			total.Add(total, rew)
		}
	}
	return total.String()
}

// GetAccountStats returns statistics about accounts in this shard
func (am *AccountManager) GetAccountStats() map[string]interface{} {
	accounts, err := am.db.GetAllAccounts()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}

	totalAccounts := len(accounts)
	totalBalance := big.NewInt(0)
	totalStaked := big.NewInt(0)
	totalRewards := big.NewInt(0)
	accountsWithStake := 0
	accountsWithDelegations := 0

	for _, account := range accounts {
		// Sum Balance
		if b, _ := new(big.Int).SetString(account.Balance, 10); b != nil {
			totalBalance.Add(totalBalance, b)
		}

		// Sum Stake
		s, _ := new(big.Int).SetString(account.StakedAmount, 10)
		if s != nil {
			totalStaked.Add(totalStaked, s)
			if s.Sign() > 0 {
				accountsWithStake++
			}
		}

		// Sum Rewards
		if r, _ := new(big.Int).SetString(account.Rewards, 10); r != nil {
			totalRewards.Add(totalRewards, r)
		}

		if len(account.DelegatedTo) > 0 {
			accountsWithDelegations++
		}
	}

	var stakingParticipation float64
	var delegationParticipation float64

	if totalAccounts > 0 {
		stakingParticipation = float64(accountsWithStake) / float64(totalAccounts)
		delegationParticipation = float64(accountsWithDelegations) / float64(totalAccounts)
	}

	// Calculate Average Balance
	avgBalance := big.NewInt(0)
	if totalAccounts > 0 {
		avgBalance.Div(totalBalance, big.NewInt(int64(totalAccounts)))
	}

	return map[string]interface{}{
		"shard_id":                  am.shardID,
		"total_accounts":            totalAccounts,
		"accounts_with_stake":       accountsWithStake,
		"accounts_with_delegations": accountsWithDelegations,
		"total_balance":             totalBalance.String(),
		"total_staked":              totalStaked.String(),
		"total_rewards":             totalRewards.String(),
		"average_balance":           avgBalance.String(),
		"staking_participation":     stakingParticipation,
		"delegation_participation":  delegationParticipation,
	}
}

// GetAllAccounts returns all accounts via DB iterator
func (am *AccountManager) GetAllAccounts() map[string]*core.Account {
	accounts, err := am.db.GetAllAccounts()
	if err != nil {
		// In a read-only context where we can't fail, return empty
		return make(map[string]*core.Account)
	}
	return accounts
}

// GetAccountCount returns the number of accounts
func (am *AccountManager) GetAccountCount() int {
	accounts, _ := am.db.GetAllAccounts()
	return len(accounts)
}

// CreateGenesisAccount creates the genesis account for a shard
func (am *AccountManager) CreateGenesisAccount(genesisAddr string, initialSupply string) error {
	if err := address.Validate(genesisAddr); err != nil {
		return fmt.Errorf("invalid genesis address: %v", err)
	}

	// Check initial supply > 0 (parsing string)
	supplyBig, _ := new(big.Int).SetString(initialSupply, 10)
	if supplyBig == nil || supplyBig.Sign() <= 0 {
		return fmt.Errorf("initial supply must be positive: %s", initialSupply)
	}

	if !am.BelongsToShard(genesisAddr) {
		return fmt.Errorf("genesis address %s does not belong to shard %d", genesisAddr, am.shardID)
	}

	// Check if genesis account already exists (checking DB)
	if am.AccountExists(genesisAddr) {
		return fmt.Errorf("genesis account %s already exists", genesisAddr)
	}

	genesisAccount := &core.Account{
		Address:      genesisAddr,
		Balance:      initialSupply, // Assumed to be string already
		Nonce:        0,
		StakedAmount: "0",                     // ✅ Fix: String
		DelegatedTo:  make(map[string]string), // ✅ Fix: map[string]string
		Rewards:      "0",                     // ✅ Fix: String
		CodeHash:     nil,
		StorageRoot:  nil,
	}

	// Directly persist
	return am.UpdateAccount(genesisAccount)
}

// Utility function for max
func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// Wrappers for crypto address functions
func GenerateAddress(pubKey crypto.PublicKey) (string, error) {
	addr, err := pubKey.Address()
	if err != nil {
		return "", fmt.Errorf("failed to generate address: %v", err)
	}
	return addr.String(), nil
}

func ValidateAddress(addr string) error {
	return address.Validate(addr)
}

func IsValidAddress(addr string) bool {
	return address.IsValid(addr)
}

func NormalizeAddress(addr string) (string, error) {
	return address.NormalizeAddress(addr)
}

func FormatAddress(addressBytes []byte) (string, error) {
	return address.FormatAddress(addressBytes)
}

func AddressToBytes(addr string) ([]byte, error) {
	return address.AddressToBytes(addr)
}

func GetAddressPrefix() string {
	return address.GetAddressPrefix()
}

func GetAddressByteLength() int {
	return address.GetAddressByteLength()
}

func EstimateAddressLength() int {
	return address.EstimateAddressLength()
}

func AddressMetrics() map[string]interface{} {
	return address.AddressMetrics()
}
