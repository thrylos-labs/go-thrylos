package state

import (
	"bytes"
	"io"
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	coremath "github.com/thrylos-labs/go-thrylos/core/math"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
)

func u(v string) []byte {
	return coremath.ParseBigInt(v).Bytes()
}

func testAddress(seed byte) string {
	raw := make([]byte, account.GetAddressByteLength())
	for i := range raw {
		raw[i] = seed
	}

	addr, err := account.FormatAddress(raw)
	if err != nil {
		panic(err)
	}

	return addr
}

// Test Issue #8 Fix 1: Skip validators with zero stake
func TestDistributeRewards_SkipsZeroStakeValidator(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "thrylos-test-zero-stake-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	require.NoError(t, err)
	defer badgerStore.Close()

	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			MaxBlockSize:  1000000,
			MaxTxPerBlock: 100,
		},
		Staking: config.StakingConfig{
			MinValidatorStake: "1000000000000000000000",
			MinDelegation:     "100000000000000000000",
		},
	}

	ws, err := NewWorldState(tmpDir, 0, 1, cfg, badgerStore)
	require.NoError(t, err)

	validatorWithStakeAddr := testAddress(1)
	validatorZeroStakeAddr := testAddress(2)

	// Create two validators: one with stake, one with zero stake
	validatorWithStake := &core.Validator{
		Address:        validatorWithStakeAddr,
		Active:         true,
		Stake:          u("10000000000000000000000"), // 10,000 THRYLOS
		DelegatedStake: nil,
		Commission:     0.10,
		Delegators:     make(map[string][]byte),
	}

	validatorZeroStake := &core.Validator{
		Address:        validatorZeroStakeAddr,
		Active:         true,
		Stake:          nil, // ❌ Zero stake
		DelegatedStake: nil,
		Commission:     0.10,
		Delegators:     make(map[string][]byte),
	}

	ws.SetValidator(validatorWithStake.Address, validatorWithStake)
	ws.SetValidator(validatorZeroStake.Address, validatorZeroStake)

	// Create accounts for validators
	ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: validatorWithStake.Address,
		Balance: nil,
		Rewards: nil,
	})
	ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: validatorZeroStake.Address,
		Balance: nil,
		Rewards: nil,
	})

	// Capture logs
	var logBuffer bytes.Buffer
	log.SetOutput(&logBuffer)
	defer log.SetOutput(os.Stderr)

	// Distribute rewards
	stakingManager := ws.GetStakingManager()
	totalRewards := "1000000000000000000000" // 1,000 THRYLOS
	err = stakingManager.DistributeRewards(totalRewards)

	// Should not error, just skip the zero-stake validator
	assert.NoError(t, err, "Distribution should succeed even with zero-stake validator")

	// Check logs for warning
	logOutput := logBuffer.String()
	assert.Contains(t, logOutput, validatorZeroStakeAddr, "Should log warning about zero stake")
	assert.Contains(t, logOutput, "zero stake", "Should mention zero stake in warning")

	// Verify validator with stake got rewards
	validatorWithStakeAcc, _ := ws.GetAccount(validatorWithStake.Address)
	assert.NotZero(t, coremath.ParseBigInt(validatorWithStakeAcc.Rewards).Sign(), "Validator with stake should have rewards")

	// Verify zero-stake validator got nothing
	validatorZeroStakeAcc, _ := ws.GetAccount(validatorZeroStake.Address)
	assert.Zero(t, coremath.ParseBigInt(validatorZeroStakeAcc.Rewards).Sign(), "Zero-stake validator should have no rewards")
}

// Test Issue #8 Fix 2: Skip validators with zero reward (after rounding)
func TestDistributeRewards_SkipsZeroRewardValidator(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "thrylos-test-zero-reward-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	require.NoError(t, err)
	defer badgerStore.Close()

	cfg := &config.Config{
		Staking: config.StakingConfig{
			MinValidatorStake: "1000000000000000000000",
		},
	}

	ws, err := NewWorldState(tmpDir, 0, 1, cfg, badgerStore)
	require.NoError(t, err)

	largeStakeValidatorAddr := testAddress(3)
	tinyStakeValidatorAddr := testAddress(4)

	// Create two validators: one with large stake, one with tiny stake
	largeStakeValidator := &core.Validator{
		Address:        largeStakeValidatorAddr,
		Active:         true,
		Stake:          u("99999999999999999999999999"), // Massive stake
		DelegatedStake: nil,
		Commission:     0.10,
		Delegators:     make(map[string][]byte),
	}

	tinyStakeValidator := &core.Validator{
		Address:        tinyStakeValidatorAddr,
		Active:         true,
		Stake:          u("1"), // 1 wei - will round to zero reward
		DelegatedStake: nil,
		Commission:     0.10,
		Delegators:     make(map[string][]byte),
	}

	ws.SetValidator(largeStakeValidator.Address, largeStakeValidator)
	ws.SetValidator(tinyStakeValidator.Address, tinyStakeValidator)

	// Create accounts
	ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: largeStakeValidator.Address,
		Balance: nil,
		Rewards: nil,
	})
	ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: tinyStakeValidator.Address,
		Balance: nil,
		Rewards: nil,
	})

	// Capture logs
	var logBuffer bytes.Buffer
	log.SetOutput(&logBuffer)
	defer log.SetOutput(os.Stderr)

	// Distribute small rewards
	stakingManager := ws.GetStakingManager()
	totalRewards := "1000" // Only 1000 wei total
	err = stakingManager.DistributeRewards(totalRewards)

	assert.NoError(t, err)

	// Check logs
	logOutput := logBuffer.String()
	assert.Contains(t, logOutput, tinyStakeValidatorAddr, "Should log warning about tiny validator")
	assert.Contains(t, logOutput, "rounded to zero", "Should mention zero reward")
}

// Test Issue #8 Fix 3 & 4: Dust tracking and logging
func TestDistributeRewards_TracksAndLogsDust(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "thrylos-test-dust-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	require.NoError(t, err)
	defer badgerStore.Close()

	cfg := &config.Config{
		Staking: config.StakingConfig{
			MinValidatorStake: "1000000000000000000000",
		},
	}

	ws, err := NewWorldState(tmpDir, 0, 1, cfg, badgerStore)
	require.NoError(t, err)

	val1Addr := testAddress(5)
	val2Addr := testAddress(6)
	val3Addr := testAddress(7)

	// Create 3 validators with stakes that don't divide evenly
	val1 := &core.Validator{
		Address:        val1Addr,
		Active:         true,
		Stake:          u("3333333333333333333333333"), // Doesn't divide evenly
		DelegatedStake: nil,
		Commission:     0.10,
		Delegators:     make(map[string][]byte),
	}

	val2 := &core.Validator{
		Address:        val2Addr,
		Active:         true,
		Stake:          u("3333333333333333333333333"),
		DelegatedStake: nil,
		Commission:     0.10,
		Delegators:     make(map[string][]byte),
	}

	val3 := &core.Validator{
		Address:        val3Addr,
		Active:         true,
		Stake:          u("3333333333333333333333334"),
		DelegatedStake: nil,
		Commission:     0.10,
		Delegators:     make(map[string][]byte),
	}

	ws.SetValidator(val1.Address, val1)
	ws.SetValidator(val2.Address, val2)
	ws.SetValidator(val3.Address, val3)

	// Create accounts
	for _, val := range []*core.Validator{val1, val2, val3} {
		ws.GetAccountManager().UpdateAccount(&core.Account{
			Address: val.Address,
			Balance: nil,
			Rewards: nil,
		})
	}

	// Capture logs
	var logBuffer bytes.Buffer
	log.SetOutput(&logBuffer)
	defer log.SetOutput(os.Stderr)

	// Distribute rewards that won't divide evenly
	stakingManager := ws.GetStakingManager()
	totalRewards := "1000000000000000000001" // 1000.000...001 THRYLOS (odd number)
	err = stakingManager.DistributeRewards(totalRewards)

	assert.NoError(t, err)

	// Check that dust was logged
	logOutput := logBuffer.String()
	assert.Contains(t, logOutput, "Dust", "Should log dust")
	assert.Contains(t, logOutput, "rounding", "Should mention rounding")
	assert.Contains(t, logOutput, "accumulate", "Should mention accumulation")
}

// Test that distribution summary is logged correctly
func TestDistributeRewards_LogsSummary(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "thrylos-test-summary-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	require.NoError(t, err)
	defer badgerStore.Close()

	cfg := &config.Config{
		Staking: config.StakingConfig{
			MinValidatorStake: "1000000000000000000000",
		},
	}

	ws, err := NewWorldState(tmpDir, 0, 1, cfg, badgerStore)
	require.NoError(t, err)

	val1Addr := testAddress(8)
	val2Addr := testAddress(9)

	// Create 2 validators
	val1 := &core.Validator{
		Address:        val1Addr,
		Active:         true,
		Stake:          u("5000000000000000000000"),
		DelegatedStake: nil,
		Commission:     0.10,
		Delegators:     make(map[string][]byte),
	}

	val2 := &core.Validator{
		Address:        val2Addr,
		Active:         true,
		Stake:          u("5000000000000000000000"),
		DelegatedStake: nil,
		Commission:     0.10,
		Delegators:     make(map[string][]byte),
	}

	ws.SetValidator(val1.Address, val1)
	ws.SetValidator(val2.Address, val2)

	// Create accounts
	ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: val1.Address,
		Balance: nil,
		Rewards: nil,
	})
	ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: val2.Address,
		Balance: nil,
		Rewards: nil,
	})

	// Capture logs
	var logBuffer bytes.Buffer
	log.SetOutput(&logBuffer)
	defer log.SetOutput(os.Stderr)

	// Distribute rewards
	stakingManager := ws.GetStakingManager()
	totalRewards := "10000000000000000000000" // 10,000 THRYLOS
	err = stakingManager.DistributeRewards(totalRewards)

	assert.NoError(t, err)

	// Verify summary was logged
	logOutput := logBuffer.String()
	assert.Contains(t, logOutput, "Reward Distribution Complete", "Should log completion")
	assert.Contains(t, logOutput, "Total Rewards:", "Should log total rewards")
	assert.Contains(t, logOutput, "Successfully Distributed:", "Should log distributed amount")
	assert.Contains(t, logOutput, "Success Count:", "Should log success count")
	assert.Contains(t, logOutput, "Failure Count:", "Should log failure count")
}

// Test that null/invalid stake values are handled
func TestDistributeRewards_HandlesInvalidStake(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "thrylos-test-invalid-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	require.NoError(t, err)
	defer badgerStore.Close()

	cfg := &config.Config{
		Staking: config.StakingConfig{
			MinValidatorStake: "1000000000000000000000",
		},
	}

	ws, err := NewWorldState(tmpDir, 0, 1, cfg, badgerStore)
	require.NoError(t, err)

	validatorInvalidAddr := testAddress(10)
	validatorValidAddr := testAddress(11)

	// Create validator with invalid stake string
	validatorInvalid := &core.Validator{
		Address:        validatorInvalidAddr,
		Active:         true,
		Stake:          []byte{0x00, 0x01}, // Invalid canonical encoding
		DelegatedStake: nil,
		Commission:     0.10,
		Delegators:     make(map[string][]byte),
	}

	validatorValid := &core.Validator{
		Address:        validatorValidAddr,
		Active:         true,
		Stake:          u("10000000000000000000000"),
		DelegatedStake: nil,
		Commission:     0.10,
		Delegators:     make(map[string][]byte),
	}

	ws.SetValidator(validatorInvalid.Address, validatorInvalid)
	ws.SetValidator(validatorValid.Address, validatorValid)

	// Create accounts
	ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: validatorValid.Address,
		Balance: nil,
		Rewards: nil,
	})

	// Capture logs
	var logBuffer bytes.Buffer
	log.SetOutput(&logBuffer)
	defer log.SetOutput(os.Stderr)

	// Distribute rewards
	stakingManager := ws.GetStakingManager()
	totalRewards := "1000000000000000000000"
	err = stakingManager.DistributeRewards(totalRewards)

	// Should still succeed (skips invalid validator)
	assert.NoError(t, err)

	// Check logs
	logOutput := logBuffer.String()
	assert.Contains(t, logOutput, validatorInvalidAddr, "Should log about invalid validator")
	assert.Contains(t, logOutput, "zero stake", "Should mention zero/invalid stake")

	// Valid validator should still get rewards
	validAcc, _ := ws.GetAccount(validatorValid.Address)
	assert.NotZero(t, coremath.ParseBigInt(validAcc.Rewards).Sign(), "Valid validator should have rewards")
}

func TestDistributeRewards_DoesNotCreditBalanceUntilClaim(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "thrylos-test-claim-flow-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	require.NoError(t, err)
	defer badgerStore.Close()

	cfg := &config.Config{
		Staking: config.StakingConfig{
			MinValidatorStake: "1000000000000000000000",
		},
	}

	ws, err := NewWorldState(tmpDir, 0, 1, cfg, badgerStore)
	require.NoError(t, err)

	validatorAddr := testAddress(12)

	validator := &core.Validator{
		Address:        validatorAddr,
		Active:         true,
		Stake:          u("10000000000000000000000"),
		DelegatedStake: nil,
		Commission:     0.10,
		Delegators:     make(map[string][]byte),
	}

	err = ws.SetValidator(validator.Address, validator)
	require.NoError(t, err)

	err = ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: validator.Address,
		Balance: u("500"),
		Rewards: nil,
	})
	require.NoError(t, err)

	err = ws.GetStakingManager().DistributeRewards("1000")
	require.NoError(t, err)

	acc, err := ws.GetAccount(validator.Address)
	require.NoError(t, err)
	assert.Equal(t, "500", coremath.BigIntToString(coremath.ParseBigInt(acc.Balance)), "Rewards should not become spendable before claim")
	assert.Equal(t, "1000", coremath.BigIntToString(coremath.ParseBigInt(acc.Rewards)), "Rewards should accrue in the rewards bucket")

	claimed, err := ws.GetAccountManager().ClaimRewards(validator.Address)
	require.NoError(t, err)
	assert.Equal(t, "1000", claimed)

	acc, err = ws.GetAccount(validator.Address)
	require.NoError(t, err)
	assert.Equal(t, "1500", coremath.BigIntToString(coremath.ParseBigInt(acc.Balance)), "Claiming should move rewards into spendable balance exactly once")
	assert.Equal(t, "0", coremath.BigIntToString(coremath.ParseBigInt(acc.Rewards)), "Rewards bucket should be cleared after claim")
}

// Benchmark test to ensure edge case handling doesn't slow things down
func BenchmarkDistributeRewards_WithEdgeCases(b *testing.B) {
	tmpDir, _ := os.MkdirTemp("", "thrylos-bench-*")
	defer os.RemoveAll(tmpDir)

	badgerStore, _ := storage.NewBadgerStorage(tmpDir)
	defer badgerStore.Close()

	cfg := &config.Config{
		Staking: config.StakingConfig{
			MinValidatorStake: "1000000000000000000000",
		},
	}

	ws, _ := NewWorldState(tmpDir, 0, 1, cfg, badgerStore)

	// Create 10 validators with mixed stakes
	for i := 0; i < 10; i++ {
		stake := "10000000000000000000000"
		if i%3 == 0 {
			stake = "0" // Every 3rd validator has zero stake
		}

		validator := &core.Validator{
			Address:        testAddress(byte(i + 13)),
			Active:         true,
			Stake:          u(stake),
			DelegatedStake: nil,
			Commission:     0.10,
			Delegators:     make(map[string][]byte),
		}
		ws.SetValidator(validator.Address, validator)

		if stake != "0" {
			ws.GetAccountManager().UpdateAccount(&core.Account{
				Address: validator.Address,
				Balance: nil,
				Rewards: nil,
			})
		}
	}

	stakingManager := ws.GetStakingManager()
	totalRewards := "10000000000000000000000"

	// Suppress logs during benchmark
	log.SetOutput(io.Discard)
	defer log.SetOutput(os.Stderr)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stakingManager.DistributeRewards(totalRewards)
	}
}
