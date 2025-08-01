// core/account/account.go

// Shard-aware account management using Blake2b hashing for consistent shard assignment
// Account validation and balance management with PoS-specific operations
// Cross-shard awareness - knows which accounts belong to which shards
// Genesis account creation for initial supply distribution
// Thrylos address generation from MLDSA44 public keys (0x format)
// Enhanced with staking, delegation, and reward management

package account

import (
	"encoding/binary"
	"fmt"
	"strings"
	"sync"

	"github.com/thrylos-labs/go-thrylos/crypto"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"golang.org/x/crypto/blake2b"
)

// ShardID represents a shard identifier
type ShardID int

const (
	// BeaconShardID represents the beacon chain (shard -1)
	BeaconShardID ShardID = -1

	// Minimum balance thresholds (from config - these should be imported from config)
	MinimumStakeAmount    = int64(1000000000)  // 1 THRYLOS minimum stake
	MinimumBalance        = int64(1000000)     // 0.001 THRYLOS minimum balance
	MinimumDelegation     = int64(100000000)   // 0.1 THRYLOS minimum delegation
	MinimumTransfer       = int64(10000000)    // 0.01 THRYLOS minimum transfer
	MinimumValidatorStake = int64(32000000000) // 32 THRYLOS minimum validator stake
)

// AccountManager handles account operations with shard awareness
type AccountManager struct {
	accounts    map[string]*core.Account
	shardID     ShardID
	totalShards int
	mu          sync.RWMutex
}

// NewAccountManager creates a new account manager for a specific shard
func NewAccountManager(shardID ShardID, totalShards int) *AccountManager {
	return &AccountManager{
		accounts:    make(map[string]*core.Account),
		shardID:     shardID,
		totalShards: totalShards,
	}
}

// CalculateShardID determines which shard an address belongs to
func CalculateShardID(address string, totalShards int) ShardID {
	if totalShards <= 1 {
		return 0
	}

	// Use Blake2b to hash the address for consistent shard assignment
	hash := blake2b.Sum256([]byte(address))

	// Use the first 8 bytes as uint64 for modulo operation
	shardIndex := binary.BigEndian.Uint64(hash[:8]) % uint64(totalShards)

	return ShardID(shardIndex)
}

// BelongsToShard checks if an address belongs to this shard
func (am *AccountManager) BelongsToShard(address string) bool {
	if am.shardID == BeaconShardID {
		return true // Beacon shard can access all accounts
	}

	addressShard := CalculateShardID(address, am.totalShards)
	return addressShard == am.shardID
}

// GetAccount retrieves an account, creating a new one if it doesn't exist
func (am *AccountManager) GetAccount(address string) (*core.Account, error) {
	// Validate address format first
	if err := ValidateAddress(address); err != nil {
		return nil, fmt.Errorf("invalid address format: %v", err)
	}

	if !am.BelongsToShard(address) {
		return nil, fmt.Errorf("address %s belongs to shard %d, not %d",
			address, CalculateShardID(address, am.totalShards), am.shardID)
	}

	am.mu.RLock()
	if account, exists := am.accounts[address]; exists {
		am.mu.RUnlock()
		return account, nil
	}
	am.mu.RUnlock()

	// Create new account
	am.mu.Lock()
	defer am.mu.Unlock()

	// Double-check after acquiring write lock
	if account, exists := am.accounts[address]; exists {
		return account, nil
	}

	newAccount := &core.Account{
		Address:      address,
		Balance:      0,
		Nonce:        0,
		StakedAmount: 0,
		DelegatedTo:  make(map[string]int64),
		Rewards:      0,
		CodeHash:     nil, // For future smart contract support
		StorageRoot:  nil, // For future contract storage
	}

	am.accounts[address] = newAccount
	return newAccount, nil
}

// GetAccountReadOnly retrieves an account without creating a new one
func (am *AccountManager) GetAccountReadOnly(address string) (*core.Account, bool) {
	if !am.BelongsToShard(address) {
		return nil, false
	}

	am.mu.RLock()
	defer am.mu.RUnlock()

	account, exists := am.accounts[address]
	return account, exists
}

// AccountExists checks if an account exists without creating it
func (am *AccountManager) AccountExists(address string) bool {
	_, exists := am.GetAccountReadOnly(address)
	return exists
}

// UpdateAccount updates an existing account
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

	am.mu.Lock()
	defer am.mu.Unlock()

	am.accounts[account.Address] = account
	return nil
}

// ValidateAccount performs comprehensive account validation
func (am *AccountManager) ValidateAccount(account *core.Account) error {
	if account == nil {
		return fmt.Errorf("account cannot be nil")
	}

	if err := ValidateAddress(account.Address); err != nil {
		return fmt.Errorf("invalid account address: %v", err)
	}

	if account.Balance < 0 {
		return fmt.Errorf("account balance cannot be negative: %d", account.Balance)
	}

	if account.StakedAmount < 0 {
		return fmt.Errorf("staked amount cannot be negative: %d", account.StakedAmount)
	}

	if account.Rewards < 0 {
		return fmt.Errorf("rewards cannot be negative: %d", account.Rewards)
	}

	// Validate delegations
	totalDelegated := int64(0)
	for validator, amount := range account.DelegatedTo {
		if err := ValidateAddress(validator); err != nil {
			return fmt.Errorf("invalid validator address %s: %v", validator, err)
		}
		if amount <= 0 {
			return fmt.Errorf("delegation amount to %s must be positive: %d", validator, amount)
		}
		totalDelegated += amount
	}

	// Ensure delegated amounts don't exceed staked amount
	if totalDelegated > account.StakedAmount {
		return fmt.Errorf("total delegated amount (%d) exceeds staked amount (%d)",
			totalDelegated, account.StakedAmount)
	}

	return nil
}

// Stake adds stake to an account
func (am *AccountManager) Stake(address string, amount int64) error {
	if amount < MinimumStakeAmount {
		return fmt.Errorf("stake amount %d below minimum %d", amount, MinimumStakeAmount)
	}

	account, err := am.GetAccount(address)
	if err != nil {
		return fmt.Errorf("failed to get account: %v", err)
	}

	if account.Balance < amount {
		return fmt.Errorf("insufficient balance: have %d, need %d", account.Balance, amount)
	}

	account.Balance -= amount
	account.StakedAmount += amount

	return am.UpdateAccount(account)
}

// Unstake removes stake from an account
func (am *AccountManager) Unstake(address string, amount int64) error {
	if amount <= 0 {
		return fmt.Errorf("unstake amount must be positive")
	}

	account, err := am.GetAccount(address)
	if err != nil {
		return fmt.Errorf("failed to get account: %v", err)
	}

	if account.StakedAmount < amount {
		return fmt.Errorf("insufficient staked amount: have %d, need %d", account.StakedAmount, amount)
	}

	// Check that unstaking doesn't violate delegations
	totalDelegated := int64(0)
	for _, delegated := range account.DelegatedTo {
		totalDelegated += delegated
	}

	if account.StakedAmount-amount < totalDelegated {
		return fmt.Errorf("cannot unstake: would leave insufficient stake for delegations")
	}

	account.StakedAmount -= amount
	account.Balance += amount

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

	if err := ValidateAddress(validatorAddr); err != nil {
		return fmt.Errorf("invalid validator address: %v", err)
	}

	delegator, err := am.GetAccount(delegatorAddr)
	if err != nil {
		return fmt.Errorf("failed to get delegator account: %v", err)
	}

	if delegator.Balance < amount {
		return fmt.Errorf("insufficient balance for delegation: have %d, need %d", delegator.Balance, amount)
	}

	// Move balance to staked amount and add delegation
	delegator.Balance -= amount
	delegator.StakedAmount += amount

	if delegator.DelegatedTo == nil {
		delegator.DelegatedTo = make(map[string]int64)
	}
	delegator.DelegatedTo[validatorAddr] += amount

	return am.UpdateAccount(delegator)
}

// Undelegate removes delegation from a validator
func (am *AccountManager) Undelegate(delegatorAddr, validatorAddr string, amount int64) error {
	if amount <= 0 {
		return fmt.Errorf("undelegation amount must be positive")
	}

	if err := ValidateAddress(validatorAddr); err != nil {
		return fmt.Errorf("invalid validator address: %v", err)
	}

	delegator, err := am.GetAccount(delegatorAddr)
	if err != nil {
		return fmt.Errorf("failed to get delegator account: %v", err)
	}

	currentDelegation := delegator.DelegatedTo[validatorAddr]
	if currentDelegation < amount {
		return fmt.Errorf("insufficient delegation to %s: have %d, need %d",
			validatorAddr, currentDelegation, amount)
	}

	// Remove delegation and return to balance
	delegator.DelegatedTo[validatorAddr] -= amount
	if delegator.DelegatedTo[validatorAddr] == 0 {
		delete(delegator.DelegatedTo, validatorAddr)
	}

	delegator.StakedAmount -= amount
	delegator.Balance += amount

	return am.UpdateAccount(delegator)
}

// AddRewards adds rewards to an account
func (am *AccountManager) AddRewards(address string, rewards int64) error {
	if rewards <= 0 {
		return fmt.Errorf("rewards must be positive: %d", rewards)
	}

	account, err := am.GetAccount(address)
	if err != nil {
		return fmt.Errorf("failed to get account: %v", err)
	}

	account.Rewards += rewards
	return am.UpdateAccount(account)
}

// ClaimRewards moves rewards to balance
func (am *AccountManager) ClaimRewards(address string) (int64, error) {
	account, err := am.GetAccount(address)
	if err != nil {
		return 0, fmt.Errorf("failed to get account: %v", err)
	}

	if account.Rewards <= 0 {
		return 0, fmt.Errorf("no rewards to claim")
	}

	claimedRewards := account.Rewards
	account.Rewards = 0
	account.Balance += claimedRewards

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

	// Validate addresses
	if err := ValidateAddress(fromAddr); err != nil {
		return fmt.Errorf("invalid sender address: %v", err)
	}
	if err := ValidateAddress(toAddr); err != nil {
		return fmt.Errorf("invalid recipient address: %v", err)
	}

	// Get sender account
	fromAccount, err := am.GetAccount(fromAddr)
	if err != nil {
		return fmt.Errorf("failed to get sender account: %v", err)
	}

	// Check balance
	if fromAccount.Balance < amount {
		return fmt.Errorf("insufficient balance: have %d, need %d", fromAccount.Balance, amount)
	}

	// Get receiver account (may need cross-shard handling)
	var toAccount *core.Account
	if am.BelongsToShard(toAddr) {
		toAccount, err = am.GetAccount(toAddr)
		if err != nil {
			return fmt.Errorf("failed to get receiver account: %v", err)
		}
	} else {
		// Cross-shard transfer - this would typically be handled by the consensus layer
		return fmt.Errorf("cross-shard transfer to %s not implemented at account level", toAddr)
	}

	// Perform transfer
	fromAccount.Balance -= amount
	fromAccount.Nonce++
	toAccount.Balance += amount

	// Update accounts
	if err := am.UpdateAccount(fromAccount); err != nil {
		return fmt.Errorf("failed to update sender account: %v", err)
	}

	if err := am.UpdateAccount(toAccount); err != nil {
		return fmt.Errorf("failed to update receiver account: %v", err)
	}

	return nil
}

// GetBalance returns the balance of an account
func (am *AccountManager) GetBalance(address string) (int64, error) {
	account, err := am.GetAccount(address)
	if err != nil {
		return 0, err
	}
	return account.Balance, nil
}

// GetNonce returns the nonce of an account
func (am *AccountManager) GetNonce(address string) (uint64, error) {
	account, err := am.GetAccount(address)
	if err != nil {
		return 0, err
	}
	return account.Nonce, nil
}

// GetStakedAmount returns the total staked amount for an account
func (am *AccountManager) GetStakedAmount(address string) (int64, error) {
	account, err := am.GetAccount(address)
	if err != nil {
		return 0, err
	}
	return account.StakedAmount, nil
}

// GetRewards returns the rewards for an account
func (am *AccountManager) GetRewards(address string) (int64, error) {
	account, err := am.GetAccount(address)
	if err != nil {
		return 0, err
	}
	return account.Rewards, nil
}

// GetDelegations returns all delegations for an account
func (am *AccountManager) GetDelegations(address string) (map[string]int64, error) {
	account, err := am.GetAccount(address)
	if err != nil {
		return nil, err
	}

	// Return a copy to prevent external modification
	delegations := make(map[string]int64)
	for validator, amount := range account.DelegatedTo {
		delegations[validator] = amount
	}

	return delegations, nil
}

// GetDelegationToValidator returns delegation amount to a specific validator
func (am *AccountManager) GetDelegationToValidator(delegatorAddr, validatorAddr string) (int64, error) {
	account, err := am.GetAccount(delegatorAddr)
	if err != nil {
		return 0, err
	}

	return account.DelegatedTo[validatorAddr], nil
}

// GetTotalStakedInShard returns total staked amount across all accounts in this shard
func (am *AccountManager) GetTotalStakedInShard() int64 {
	am.mu.RLock()
	defer am.mu.RUnlock()

	total := int64(0)
	for _, account := range am.accounts {
		total += account.StakedAmount
	}
	return total
}

// GetTotalBalanceInShard returns total balance across all accounts in this shard
func (am *AccountManager) GetTotalBalanceInShard() int64 {
	am.mu.RLock()
	defer am.mu.RUnlock()

	total := int64(0)
	for _, account := range am.accounts {
		total += account.Balance
	}
	return total
}

// GetTotalRewardsInShard returns total unclaimed rewards across all accounts in this shard
func (am *AccountManager) GetTotalRewardsInShard() int64 {
	am.mu.RLock()
	defer am.mu.RUnlock()

	total := int64(0)
	for _, account := range am.accounts {
		total += account.Rewards
	}
	return total
}

// GetAccountStats returns statistics about accounts in this shard
func (am *AccountManager) GetAccountStats() map[string]interface{} {
	am.mu.RLock()
	defer am.mu.RUnlock()

	totalAccounts := len(am.accounts)
	totalBalance := int64(0)
	totalStaked := int64(0)
	totalRewards := int64(0)
	accountsWithStake := 0
	accountsWithDelegations := 0

	for _, account := range am.accounts {
		totalBalance += account.Balance
		totalStaked += account.StakedAmount
		totalRewards += account.Rewards

		if account.StakedAmount > 0 {
			accountsWithStake++
		}

		if len(account.DelegatedTo) > 0 {
			accountsWithDelegations++
		}
	}

	// Calculate participation rates safely
	var stakingParticipation float64
	var delegationParticipation float64

	if totalAccounts > 0 {
		stakingParticipation = float64(accountsWithStake) / float64(totalAccounts)
		delegationParticipation = float64(accountsWithDelegations) / float64(totalAccounts)
	}

	return map[string]interface{}{
		"shard_id":                  am.shardID,
		"total_accounts":            totalAccounts,
		"accounts_with_stake":       accountsWithStake,
		"accounts_with_delegations": accountsWithDelegations,
		"total_balance":             totalBalance,
		"total_staked":              totalStaked,
		"total_rewards":             totalRewards,
		"average_balance":           totalBalance / max(int64(totalAccounts), 1),
		"staking_participation":     stakingParticipation,
		"delegation_participation":  delegationParticipation,
	}
}

// GetAllAccounts returns all accounts in this shard (for debugging/admin)
func (am *AccountManager) GetAllAccounts() map[string]*core.Account {
	am.mu.RLock()
	defer am.mu.RUnlock()

	// Return a copy to prevent external modification
	accounts := make(map[string]*core.Account)
	for addr, account := range am.accounts {
		accounts[addr] = account
	}

	return accounts
}

// GetAccountCount returns the number of accounts in this shard
func (am *AccountManager) GetAccountCount() int {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return len(am.accounts)
}

// Address format constants
const (
	AddressPrefix     = "0x"
	AddressLength     = 42 // "0x" + 40 hex characters
	AddressByteLength = 20 // 20 bytes = 40 hex characters
)

// GenerateAddress generates a new Ethereum-compatible address from a public key
func GenerateAddress(pubKey crypto.PublicKey) (string, error) {
	if pubKey == nil {
		return "", fmt.Errorf("public key cannot be nil")
	}

	// Get the raw bytes of the public key
	pubKeyBytes := pubKey.Bytes()
	if len(pubKeyBytes) == 0 {
		return "", fmt.Errorf("public key bytes cannot be empty")
	}

	// Hash the public key with Blake2b
	hash := blake2b.Sum256(pubKeyBytes)

	// Take the last 20 bytes of the hash (Ethereum standard)
	addressBytes := hash[len(hash)-20:]

	// Format as 0x + hex (lowercase for consistency)
	address := fmt.Sprintf("%s%x", AddressPrefix, addressBytes)

	return address, nil
}

// ValidateAddress checks if an address has the correct 0x format
func ValidateAddress(address string) error {
	if len(address) != AddressLength {
		return fmt.Errorf("address must be exactly %d characters long, got %d", AddressLength, len(address))
	}

	if address[:2] != AddressPrefix {
		return fmt.Errorf("address must start with '%s', got '%s'", AddressPrefix, address[:2])
	}

	// Validate hex characters
	hexPart := address[2:]
	for i, char := range hexPart {
		if !isHexChar(char) {
			return fmt.Errorf("address contains invalid hex character '%c' at position %d", char, i+2)
		}
	}

	return nil
}

// isHexChar checks if a character is a valid hex digit
func isHexChar(char rune) bool {
	return (char >= '0' && char <= '9') ||
		(char >= 'a' && char <= 'f') ||
		(char >= 'A' && char <= 'F')
}

// NormalizeAddress converts address to lowercase (for internal storage consistency)
func NormalizeAddress(address string) (string, error) {
	if err := ValidateAddress(address); err != nil {
		return "", err
	}
	return strings.ToLower(address), nil
}

// IsValidAddress is a convenience function for address validation
func IsValidAddress(address string) bool {
	return ValidateAddress(address) == nil
}

// FormatAddress formats raw bytes as an address
func FormatAddress(addressBytes []byte) (string, error) {
	if len(addressBytes) != AddressByteLength {
		return "", fmt.Errorf("address bytes must be exactly %d bytes, got %d", AddressByteLength, len(addressBytes))
	}

	return fmt.Sprintf("%s%x", AddressPrefix, addressBytes), nil
}

// AddressToBytes converts a string address to bytes
func AddressToBytes(address string) ([]byte, error) {
	if err := ValidateAddress(address); err != nil {
		return nil, err
	}

	hexPart := address[2:] // Remove "0x" prefix
	addressBytes := make([]byte, AddressByteLength)

	for i := 0; i < len(hexPart); i += 2 {
		var b byte
		n, err := fmt.Sscanf(hexPart[i:i+2], "%02x", &b)
		if err != nil || n != 1 {
			return nil, fmt.Errorf("invalid hex at position %d-%d", i, i+1)
		}
		addressBytes[i/2] = b
	}

	return addressBytes, nil
}

// CreateGenesisAccount creates the genesis account for a shard
func (am *AccountManager) CreateGenesisAccount(genesisAddr string, initialSupply int64) error {
	if err := ValidateAddress(genesisAddr); err != nil {
		return fmt.Errorf("invalid genesis address: %v", err)
	}

	if initialSupply <= 0 {
		return fmt.Errorf("initial supply must be positive: %d", initialSupply)
	}

	if !am.BelongsToShard(genesisAddr) {
		return fmt.Errorf("genesis address %s does not belong to shard %d", genesisAddr, am.shardID)
	}

	// Check if genesis account already exists
	if am.AccountExists(genesisAddr) {
		return fmt.Errorf("genesis account %s already exists", genesisAddr)
	}

	genesisAccount := &core.Account{
		Address:      genesisAddr,
		Balance:      initialSupply,
		Nonce:        0,
		StakedAmount: 0,
		DelegatedTo:  make(map[string]int64),
		Rewards:      0,
		CodeHash:     nil,
		StorageRoot:  nil,
	}

	am.mu.Lock()
	defer am.mu.Unlock()

	am.accounts[genesisAddr] = genesisAccount
	return nil
}

// Utility function for max
func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
