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
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
)

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

	// Create two validators: one with stake, one with zero stake
	validatorWithStake := &core.Validator{
		Address:        "validator_with_stake",
		Active:         true,
		Stake:          "10000000000000000000000", // 10,000 THRYLOS
		DelegatedStake: "0",
		Commission:     0.10,
		Delegators:     make(map[string]string),
	}

	validatorZeroStake := &core.Validator{
		Address:        "validator_zero_stake",
		Active:         true,
		Stake:          "0", // ❌ Zero stake
		DelegatedStake: "0",
		Commission:     0.10,
		Delegators:     make(map[string]string),
	}

	ws.SetValidator(validatorWithStake.Address, validatorWithStake)
	ws.SetValidator(validatorZeroStake.Address, validatorZeroStake)

	// Create accounts for validators
	ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: validatorWithStake.Address,
		Balance: "0",
		Rewards: "0",
	})
	ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: validatorZeroStake.Address,
		Balance: "0",
		Rewards: "0",
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
	assert.Contains(t, logOutput, "validator_zero_stake", "Should log warning about zero stake")
	assert.Contains(t, logOutput, "zero stake", "Should mention zero stake in warning")

	// Verify validator with stake got rewards
	validatorWithStakeAcc, _ := ws.GetAccount(validatorWithStake.Address)
	assert.NotEqual(t, "0", validatorWithStakeAcc.Rewards, "Validator with stake should have rewards")

	// Verify zero-stake validator got nothing
	validatorZeroStakeAcc, _ := ws.GetAccount(validatorZeroStake.Address)
	assert.Equal(t, "0", validatorZeroStakeAcc.Rewards, "Zero-stake validator should have no rewards")
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

	// Create two validators: one with large stake, one with tiny stake
	largeStakeValidator := &core.Validator{
		Address:        "large_validator",
		Active:         true,
		Stake:          "99999999999999999999999999", // Massive stake
		DelegatedStake: "0",
		Commission:     0.10,
		Delegators:     make(map[string]string),
	}

	tinyStakeValidator := &core.Validator{
		Address:        "tiny_validator",
		Active:         true,
		Stake:          "1", // 1 wei - will round to zero reward
		DelegatedStake: "0",
		Commission:     0.10,
		Delegators:     make(map[string]string),
	}

	ws.SetValidator(largeStakeValidator.Address, largeStakeValidator)
	ws.SetValidator(tinyStakeValidator.Address, tinyStakeValidator)

	// Create accounts
	ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: largeStakeValidator.Address,
		Balance: "0",
		Rewards: "0",
	})
	ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: tinyStakeValidator.Address,
		Balance: "0",
		Rewards: "0",
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
	assert.Contains(t, logOutput, "tiny_validator", "Should log warning about tiny validator")
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

	// Create 3 validators with stakes that don't divide evenly
	val1 := &core.Validator{
		Address:        "validator1",
		Active:         true,
		Stake:          "3333333333333333333333333", // Doesn't divide evenly
		DelegatedStake: "0",
		Commission:     0.10,
		Delegators:     make(map[string]string),
	}

	val2 := &core.Validator{
		Address:        "validator2",
		Active:         true,
		Stake:          "3333333333333333333333333",
		DelegatedStake: "0",
		Commission:     0.10,
		Delegators:     make(map[string]string),
	}

	val3 := &core.Validator{
		Address:        "validator3",
		Active:         true,
		Stake:          "3333333333333333333333334",
		DelegatedStake: "0",
		Commission:     0.10,
		Delegators:     make(map[string]string),
	}

	ws.SetValidator(val1.Address, val1)
	ws.SetValidator(val2.Address, val2)
	ws.SetValidator(val3.Address, val3)

	// Create accounts
	for _, val := range []*core.Validator{val1, val2, val3} {
		ws.GetAccountManager().UpdateAccount(&core.Account{
			Address: val.Address,
			Balance: "0",
			Rewards: "0",
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

	// Create 2 validators
	val1 := &core.Validator{
		Address:        "validator1",
		Active:         true,
		Stake:          "5000000000000000000000",
		DelegatedStake: "0",
		Commission:     0.10,
		Delegators:     make(map[string]string),
	}

	val2 := &core.Validator{
		Address:        "validator2",
		Active:         true,
		Stake:          "5000000000000000000000",
		DelegatedStake: "0",
		Commission:     0.10,
		Delegators:     make(map[string]string),
	}

	ws.SetValidator(val1.Address, val1)
	ws.SetValidator(val2.Address, val2)

	// Create accounts
	ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: val1.Address,
		Balance: "0",
		Rewards: "0",
	})
	ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: val2.Address,
		Balance: "0",
		Rewards: "0",
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

	// Create validator with invalid stake string
	validatorInvalid := &core.Validator{
		Address:        "validator_invalid",
		Active:         true,
		Stake:          "not-a-number", // Invalid stake
		DelegatedStake: "0",
		Commission:     0.10,
		Delegators:     make(map[string]string),
	}

	validatorValid := &core.Validator{
		Address:        "validator_valid",
		Active:         true,
		Stake:          "10000000000000000000000",
		DelegatedStake: "0",
		Commission:     0.10,
		Delegators:     make(map[string]string),
	}

	ws.SetValidator(validatorInvalid.Address, validatorInvalid)
	ws.SetValidator(validatorValid.Address, validatorValid)

	// Create accounts
	ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: validatorValid.Address,
		Balance: "0",
		Rewards: "0",
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
	assert.Contains(t, logOutput, "validator_invalid", "Should log about invalid validator")
	assert.Contains(t, logOutput, "zero stake", "Should mention zero/invalid stake")

	// Valid validator should still get rewards
	validAcc, _ := ws.GetAccount(validatorValid.Address)
	assert.NotEqual(t, "0", validAcc.Rewards, "Valid validator should have rewards")
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

	validator := &core.Validator{
		Address:        "validator_claim_flow",
		Active:         true,
		Stake:          "10000000000000000000000",
		DelegatedStake: "0",
		Commission:     0.10,
		Delegators:     make(map[string]string),
	}

	err = ws.SetValidator(validator.Address, validator)
	require.NoError(t, err)

	err = ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: validator.Address,
		Balance: "500",
		Rewards: "0",
	})
	require.NoError(t, err)

	err = ws.GetStakingManager().DistributeRewards("1000")
	require.NoError(t, err)

	acc, err := ws.GetAccount(validator.Address)
	require.NoError(t, err)
	assert.Equal(t, "500", acc.Balance, "Rewards should not become spendable before claim")
	assert.Equal(t, "1000", acc.Rewards, "Rewards should accrue in the rewards bucket")

	claimed, err := ws.GetAccountManager().ClaimRewards(validator.Address)
	require.NoError(t, err)
	assert.Equal(t, "1000", claimed)

	acc, err = ws.GetAccount(validator.Address)
	require.NoError(t, err)
	assert.Equal(t, "1500", acc.Balance, "Claiming should move rewards into spendable balance exactly once")
	assert.Equal(t, "0", acc.Rewards, "Rewards bucket should be cleared after claim")
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
			Address:        "validator" + string(rune(i)),
			Active:         true,
			Stake:          stake,
			DelegatedStake: "0",
			Commission:     0.10,
			Delegators:     make(map[string]string),
		}
		ws.SetValidator(validator.Address, validator)

		if stake != "0" {
			ws.GetAccountManager().UpdateAccount(&core.Account{
				Address: validator.Address,
				Balance: "0",
				Rewards: "0",
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
