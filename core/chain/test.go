package chain

import (
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	coremath "github.com/thrylos-labs/go-thrylos/core/math"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/crypto"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
)

func u(v string) []byte {
	return coremath.ParseBigInt(v).Bytes()
}

func TestBlockProcessing_ProcessesUnbondingQueue(t *testing.T) {
	// 1. Setup blockchain with temporary storage
	tmpDir, err := os.MkdirTemp("", "thrylos-unbonding-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	require.NoError(t, err)
	defer badgerStore.Close()

	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			MaxBlockSize:       1000000,
			MaxTxPerBlock:      100,
			MinGasPrice:        "1",
			MaxFutureBlockTime: 15 * time.Second,
		},
		Economics: config.EconomicsConfig{
			BaseGasPrice: "1",
		},
		Staking: config.StakingConfig{
			MinValidatorStake: "1000000000000000000000", // 1000 THRYLOS
			MinDelegation:     "100000000000000000000",  // 100 THRYLOS
			UnbondingPeriod:   7 * 24 * time.Hour,       // 7 days
			MaxCommission:     0.15,
		},
	}

	ws, err := state.NewWorldState(tmpDir, 0, 1, cfg, badgerStore)
	require.NoError(t, err)

	bcConfig := &BlockchainConfig{
		Config:        cfg,
		WorldState:    ws,
		ShardID:       0,
		TotalShards:   1,
		MaxReorgDepth: 100,
	}
	blockchain, err := NewBlockchain(bcConfig)
	require.NoError(t, err)

	// 2. Create validator with sufficient stake
	validatorPrivKey, _ := crypto.NewPrivateKey()
	validatorAddr, _ := account.GenerateAddress(validatorPrivKey.PublicKey())

	validator := &core.Validator{
		Address:        validatorAddr,
		Pubkey:         validatorPrivKey.PublicKey().Bytes(),
		Name:           "Test Validator",
		Stake:          u("10000000000000000000000"), // 10,000 THRYLOS
		SelfStake:      u("3000000000000000000000"),  // 3,000 THRYLOS
		DelegatedStake: u("7000000000000000000000"),  // 7,000 THRYLOS
		Commission:     0.10,                      // 10%
		Active:         true,
		Delegators:     make(map[string][]byte),
		CreatedAt:      time.Now().Unix(),
		UpdatedAt:      time.Now().Unix(),
	}

	// Create validator account
	validatorAccount := &core.Account{
		Address: validatorAddr,
		Balance: nil,
		Nonce:   0,
	}
	ws.GetAccountManager().UpdateAccount(validatorAccount)
	ws.SetValidator(validatorAddr, validator)

	// 3. Create delegator with balance
	delegatorPrivKey, _ := crypto.NewPrivateKey()
	delegatorAddr, _ := account.GenerateAddress(delegatorPrivKey.PublicKey())

	// Give delegator initial balance
	delegatorAccount := &core.Account{
		Address:      delegatorAddr,
		Balance:      nil,                      // No liquid balance (it's all staked)
		StakedAmount: u("7000000000000000000000"), // 7,000 THRYLOS staked
		Nonce:        0,
		DelegatedTo:  make(map[string][]byte),
	}
	// Track delegation
	delegatorAccount.DelegatedTo[validatorAddr] = u("7000000000000000000000") // 7,000 THRYLOS

	ws.GetAccountManager().UpdateAccount(delegatorAccount)

	// Update validator's delegators map
	validator.Delegators[delegatorAddr] = u("7000000000000000000000")
	ws.SetValidator(validatorAddr, validator)

	// 4. Undelegate some tokens
	// ✅ FIX: Use proper big.Int creation to avoid overflow
	unstakeAmount := new(big.Int)
	unstakeAmount.SetString("1000000000000000000000", 10) // 1,000 THRYLOS

	stakingManager := ws.GetStakingManager()
	err = stakingManager.Undelegate(delegatorAddr, validatorAddr, unstakeAmount)
	require.NoError(t, err, "Undelegate should succeed")

	// 5. Verify unbonding entry was created
	entries := ws.GetUnbondingEntries(delegatorAddr)
	assert.Len(t, entries, 1, "Should have 1 unbonding entry")
	assert.Equal(t, delegatorAddr, entries[0].DelegatorAddr)
	assert.Equal(t, validatorAddr, entries[0].ValidatorAddr)
	assert.Equal(t, unstakeAmount.String(), entries[0].Amount)

	// 6. Verify funds NOT in balance yet
	updatedDelegator, err := ws.GetAccount(delegatorAddr)
	require.NoError(t, err)
	assert.Equal(t, "0", coremath.BigIntToString(coremath.ParseBigInt(updatedDelegator.Balance)), "Balance should still be 0 (funds in unbonding)")

	// 7. Manually set completion time to past (simulate 7 days passing)
	ws.UnbondingMu().Lock()
	ws.UnbondingQueue()[0].CompletionTime = time.Now().Add(-1 * time.Hour).Unix()
	ws.UnbondingMu().Unlock()

	// 8. Create and add a test block (this should trigger unbonding processing)
	block := createTestBlockForUnbonding(t, blockchain, validatorAddr)
	err = blockchain.AddBlock(block)
	require.NoError(t, err, "AddBlock should succeed")

	// 9. Verify funds were released to balance
	finalDelegator, err := ws.GetAccount(delegatorAddr)
	require.NoError(t, err)
	assert.Equal(t, unstakeAmount.String(), coremath.BigIntToString(coremath.ParseBigInt(finalDelegator.Balance)),
		"Funds should now be in balance after unbonding period")

	// 10. Verify unbonding queue is empty
	entriesAfter := ws.GetUnbondingEntries(delegatorAddr)
	assert.Len(t, entriesAfter, 0, "Unbonding queue should be empty")

	// 11. Verify staked amount decreased
	expectedStaked := "6000000000000000000000" // 7,000 - 1,000 = 6,000
	assert.Equal(t, expectedStaked, coremath.BigIntToString(coremath.ParseBigInt(finalDelegator.StakedAmount)),
		"Staked amount should have decreased")
}

// Helper function to create a test block
func createTestBlockForUnbonding(t *testing.T, bc *Blockchain, validatorAddr string) *core.Block {
	currentBlock := bc.worldState.GetCurrentBlock()
	var parentHash string
	var index int64

	if currentBlock != nil {
		parentHash = currentBlock.Hash
		index = currentBlock.Header.Index + 1
	} else {
		parentHash = "0x0000000000000000000000000000000000000000000000000000000000000000"
		index = 1
	}

	block := &core.Block{
		Header: &core.BlockHeader{
			Index:     index,
			Timestamp: time.Now().Unix(),
			PrevHash:  parentHash, // ✅ Fixed: Use PrevHash
			Validator: validatorAddr,
			StateRoot: "",
			GasLimit:  1000000,
		},
		Hash:         calculateSimpleHash(index),
		Transactions: []*core.Transaction{}, // Empty block
	}

	return block
}

// Simple hash calculation for testing
func calculateSimpleHash(index int64) string {
	return "0x" + time.Now().Format("20060102150405") + string(rune(index))
}

// Test multiple unbonding entries
func TestUnbondingQueue_MultipleEntries(t *testing.T) {
	// Setup (similar to above)
	tmpDir, err := os.MkdirTemp("", "thrylos-unbonding-multi-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	require.NoError(t, err)
	defer badgerStore.Close()

	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			MaxBlockSize:       1000000,
			MaxTxPerBlock:      100,
			MinGasPrice:        "1",
			MaxFutureBlockTime: 15 * time.Second,
		},
		Staking: config.StakingConfig{
			MinValidatorStake: "1000000000000000000000",
			MinDelegation:     "100000000000000000000",
			UnbondingPeriod:   7 * 24 * time.Hour,
			MaxCommission:     0.15,
		},
	}

	ws, err := state.NewWorldState(tmpDir, 0, 1, cfg, badgerStore)
	require.NoError(t, err)

	// Create validator and delegator (simplified)
	validatorPrivKey, _ := crypto.NewPrivateKey()
	validatorAddr, _ := account.GenerateAddress(validatorPrivKey.PublicKey())

	delegatorPrivKey, _ := crypto.NewPrivateKey()
	delegatorAddr, _ := account.GenerateAddress(delegatorPrivKey.PublicKey())

	validator := &core.Validator{
		Address:        validatorAddr,
		Stake:          u("100000000000000000000000"), // 100,000 THRYLOS
		DelegatedStake: u("50000000000000000000000"),  // 50,000 THRYLOS
		Active:         true,
		Delegators:     map[string][]byte{delegatorAddr: u("50000000000000000000000")},
	}
	ws.SetValidator(validatorAddr, validator)

	delegator := &core.Account{
		Address:      delegatorAddr,
		Balance:      nil,
		StakedAmount: u("50000000000000000000000"),
		DelegatedTo:  map[string][]byte{validatorAddr: u("50000000000000000000000")},
	}
	ws.GetAccountManager().UpdateAccount(delegator)

	// Undelegate in 3 batches
	stakingManager := ws.GetStakingManager()

	amount1 := new(big.Int).SetUint64(1000)
	amount1.Mul(amount1, new(big.Int).SetUint64(1e18)) // 1,000 THRYLOS

	amount2 := new(big.Int).SetUint64(2000)
	amount2.Mul(amount2, new(big.Int).SetUint64(1e18)) // 2,000 THRYLOS

	amount3 := new(big.Int).SetUint64(3000)
	amount3.Mul(amount3, new(big.Int).SetUint64(1e18)) // 3,000 THRYLOS

	err = stakingManager.Undelegate(delegatorAddr, validatorAddr, amount1)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	err = stakingManager.Undelegate(delegatorAddr, validatorAddr, amount2)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	err = stakingManager.Undelegate(delegatorAddr, validatorAddr, amount3)
	require.NoError(t, err)

	// Verify 3 entries exist
	entries := ws.GetUnbondingEntries(delegatorAddr)
	assert.Len(t, entries, 3, "Should have 3 unbonding entries")

	// Verify total unbonding amount
	totalUnbonding := ws.GetTotalUnbonding(delegatorAddr)
	expected := new(big.Int).SetUint64(6000)
	expected.Mul(expected, new(big.Int).SetUint64(1e18)) // 6,000 THRYLOS
	assert.Equal(t, expected.String(), totalUnbonding.String(),
		"Total unbonding should be 6,000 THRYLOS")
}

// Test that unbonding doesn't complete early
func TestUnbondingQueue_DoesNotCompleteEarly(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "thrylos-unbonding-early-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	require.NoError(t, err)
	defer badgerStore.Close()

	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			MaxBlockSize:       1000000,
			MaxTxPerBlock:      100,
			MinGasPrice:        "1",
			MaxFutureBlockTime: 15 * time.Second,
		},
		Staking: config.StakingConfig{
			MinValidatorStake: "1000000000000000000000",
			MinDelegation:     "100000000000000000000",
			UnbondingPeriod:   7 * 24 * time.Hour, // 7 days
		},
	}

	ws, err := state.NewWorldState(tmpDir, 0, 1, cfg, badgerStore)
	require.NoError(t, err)

	bcConfig := &BlockchainConfig{
		Config:      cfg,
		WorldState:  ws,
		ShardID:     0,
		TotalShards: 1,
	}
	blockchain, err := NewBlockchain(bcConfig)
	require.NoError(t, err)

	// Setup validator and delegator
	validatorPrivKey, _ := crypto.NewPrivateKey()
	validatorAddr, _ := account.GenerateAddress(validatorPrivKey.PublicKey())

	delegatorPrivKey, _ := crypto.NewPrivateKey()
	delegatorAddr, _ := account.GenerateAddress(delegatorPrivKey.PublicKey())

	validator := &core.Validator{
		Address:        validatorAddr,
		Stake:          u("10000000000000000000000"),
		DelegatedStake: u("5000000000000000000000"),
		Active:         true,
		Delegators:     map[string][]byte{delegatorAddr: u("5000000000000000000000")},
	}
	ws.SetValidator(validatorAddr, validator)

	delegator := &core.Account{
		Address:      delegatorAddr,
		Balance:      nil,
		StakedAmount: u("5000000000000000000000"),
		DelegatedTo:  map[string][]byte{validatorAddr: u("5000000000000000000000")},
	}
	ws.GetAccountManager().UpdateAccount(delegator)

	// Undelegate
	amount := new(big.Int).SetUint64(1000)
	amount.Mul(amount, new(big.Int).SetUint64(1e18))

	stakingManager := ws.GetStakingManager()
	err = stakingManager.Undelegate(delegatorAddr, validatorAddr, amount)
	require.NoError(t, err)

	// Process a block immediately (unbonding period NOT elapsed)
	block := createTestBlockForUnbonding(t, blockchain, validatorAddr)
	err = blockchain.AddBlock(block)
	require.NoError(t, err)

	// Verify funds still NOT in balance
	updatedDelegator, _ := ws.GetAccount(delegatorAddr)
	assert.Equal(t, "0", updatedDelegator.Balance,
		"Funds should still be 0 (unbonding not complete)")

	// Verify entry still in queue
	entries := ws.GetUnbondingEntries(delegatorAddr)
	assert.Len(t, entries, 1, "Entry should still be in queue")
}
