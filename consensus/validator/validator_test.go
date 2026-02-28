package validator

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	accountpkg "github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/state"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
)

func newTestValidatorManager(t *testing.T, cfg *config.Config) (*Manager, *state.WorldState) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "thrylos-validator-test-*")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.RemoveAll(tmpDir)
	})

	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = badgerStore.Close()
	})

	ws, err := state.NewWorldState(tmpDir, accountpkg.ShardID(0), 1, cfg, badgerStore)
	require.NoError(t, err)

	return NewManager(cfg, ws), ws
}

func TestBeginUnbonding_UsesConfiguredBlockTime(t *testing.T) {
	const (
		validatorAddr = "0x2222222222222222222222222222222222222222"
		delegatorAddr = "0x1111111111111111111111111111111111111111"
	)

	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			BlockTime: 3 * time.Second,
		},
		Staking: config.StakingConfig{
			UnbondingPeriod: 12 * time.Second,
		},
	}

	vm, ws := newTestValidatorManager(t, cfg)
	err := ws.SetValidator(validatorAddr, &core.Validator{
		Address:        validatorAddr,
		Active:         true,
		Stake:          "1000",
		DelegatedStake: "500",
		Delegators: map[string]string{
			delegatorAddr: "500",
		},
	})
	require.NoError(t, err)

	err = vm.BeginUnbonding(validatorAddr, delegatorAddr, "100")
	require.NoError(t, err)

	entries := vm.unbondingQueue[delegatorAddr]
	require.Len(t, entries, 1)
	require.Equal(t, int64(4), entries[0].CompletionBlock)
}

func TestProcessUnbondings_ReturnsPrincipalToBalance(t *testing.T) {
	const (
		validatorAddr = "0x4444444444444444444444444444444444444444"
		delegatorAddr = "0x3333333333333333333333333333333333333333"
	)

	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			BlockTime: 3 * time.Second,
		},
		Staking: config.StakingConfig{
			MinValidatorStake: "1",
		},
	}

	vm, ws := newTestValidatorManager(t, cfg)
	const amount = "10000000000000000000"

	err := ws.SetValidator(validatorAddr, &core.Validator{
		Address:        validatorAddr,
		Active:         true,
		Stake:          amount,
		DelegatedStake: amount,
		Delegators: map[string]string{
			delegatorAddr: amount,
		},
	})
	require.NoError(t, err)

	err = ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: delegatorAddr,
		Balance: "7",
		Rewards: "0",
	})
	require.NoError(t, err)

	vm.unbondingQueue[delegatorAddr] = []*UnbondingEntry{
		{
			ValidatorAddress: validatorAddr,
			DelegatorAddress: delegatorAddr,
			Amount:           amount,
			CompletionBlock:  0,
			CreatedAt:        time.Now().Unix(),
		},
	}

	err = vm.ProcessUnbondings()
	require.NoError(t, err)

	delegator, err := ws.GetAccount(delegatorAddr)
	require.NoError(t, err)
	require.Equal(t, "10000000000000000007", delegator.Balance)
	require.Equal(t, "0", delegator.Rewards)

	validator, err := ws.GetValidator(validatorAddr)
	require.NoError(t, err)
	require.Equal(t, "0", validator.Stake)
	require.Equal(t, "0", validator.DelegatedStake)
	require.Empty(t, validator.Delegators)
	require.NotContains(t, vm.unbondingQueue, delegatorAddr)
}

func TestSlashValidator_ApportionsLossAcrossDelegationAndPendingUnbonding(t *testing.T) {
	const (
		validatorAddr = "0x6666666666666666666666666666666666666666"
		delegatorAddr = "0x5555555555555555555555555555555555555555"
	)

	cfg := &config.Config{
		Staking: config.StakingConfig{
			MinValidatorStake:       "1",
			SlashFractionDowntime:   0.50,
			MaxSlashingEvents:       10,
			MinStakeRetention:       0.0,
			AutoRemoveOnDoubleSign:  true,
			CommissionChangeMax:     0.01,
			MaxCommission:           0.20,
			SlashFractionDoubleSign: 0.05,
		},
	}

	vm, ws := newTestValidatorManager(t, cfg)

	err := ws.SetValidator(validatorAddr, &core.Validator{
		Address:        validatorAddr,
		Active:         true,
		Stake:          "1000",
		SelfStake:      "400",
		DelegatedStake: "600",
		Delegators: map[string]string{
			delegatorAddr: "600",
		},
		CreatedAt: time.Now().Add(-48 * time.Hour).Unix(),
		UpdatedAt: time.Now().Add(-48 * time.Hour).Unix(),
	})
	require.NoError(t, err)

	err = ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: delegatorAddr,
		Balance: "0",
		Rewards: "0",
	})
	require.NoError(t, err)

	vm.unbondingQueue[delegatorAddr] = []*UnbondingEntry{
		{
			ValidatorAddress: validatorAddr,
			DelegatorAddress: delegatorAddr,
			Amount:           "200",
			CompletionBlock:  0,
			CreatedAt:        time.Now().Unix(),
		},
	}

	err = vm.SlashValidator(validatorAddr, SlashingDowntime, nil)
	require.NoError(t, err)

	validator, err := ws.GetValidator(validatorAddr)
	require.NoError(t, err)
	require.Equal(t, "500", validator.Stake)
	require.Equal(t, "200", validator.SelfStake)
	require.Equal(t, "300", validator.DelegatedStake)
	require.Equal(t, "300", validator.Delegators[delegatorAddr])
	require.Equal(t, "100", vm.unbondingQueue[delegatorAddr][0].Amount)

	err = vm.ProcessUnbondings()
	require.NoError(t, err)

	delegator, err := ws.GetAccount(delegatorAddr)
	require.NoError(t, err)
	require.Equal(t, "100", delegator.Balance)

	validator, err = ws.GetValidator(validatorAddr)
	require.NoError(t, err)
	require.Equal(t, "200", validator.Delegators[delegatorAddr])
	require.Equal(t, "200", validator.DelegatedStake)
}

func TestUpdateValidatorCommission_EnforcesDailyCooldown(t *testing.T) {
	const validatorAddr = "0x7777777777777777777777777777777777777777"

	cfg := &config.Config{
		Staking: config.StakingConfig{
			MaxCommission:       0.20,
			CommissionChangeMax: 0.01,
		},
	}

	vm, ws := newTestValidatorManager(t, cfg)
	err := ws.SetValidator(validatorAddr, &core.Validator{
		Address:    validatorAddr,
		Commission: 0.10,
		CreatedAt:  time.Now().Add(-48 * time.Hour).Unix(),
		UpdatedAt:  time.Now().Add(-48 * time.Hour).Unix(),
	})
	require.NoError(t, err)

	err = vm.UpdateValidatorCommission(validatorAddr, 0.11)
	require.NoError(t, err)

	err = vm.UpdateValidatorCommission(validatorAddr, 0.12)
	require.Error(t, err)
	require.Contains(t, err.Error(), "once every")

	vm.commissionUpdateTimes[validatorAddr] = time.Now().Add(-25 * time.Hour).Unix()
	err = vm.UpdateValidatorCommission(validatorAddr, 0.12)
	require.NoError(t, err)

	validator, err := ws.GetValidator(validatorAddr)
	require.NoError(t, err)
	require.Equal(t, 0.12, validator.Commission)
}

func TestRegisterValidator_EnforcesRegistrationStakeLimits(t *testing.T) {
	cfg := &config.Config{
		Staking: config.StakingConfig{
			MinValidatorStake:   "100",
			MaxValidatorStake:   "1000",
			MaxStakePercentage:  0.50,
			MaxCommission:       0.20,
			CommissionChangeMax: 0.01,
		},
	}

	vm, ws := newTestValidatorManager(t, cfg)

	err := vm.RegisterValidator(
		"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		[]byte{1, 2, 3},
		"1001",
		0.10,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")

	err = ws.SetValidator("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", &core.Validator{
		Address: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Active:  true,
		Stake:   "900",
	})
	require.NoError(t, err)
	ws.UpdateTotalStaked()

	err = vm.RegisterValidator(
		"0xcccccccccccccccccccccccccccccccccccccccc",
		[]byte{4, 5, 6},
		"1000",
		0.10,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "concentration limit")
}

func TestRegisterValidatorWithDomain_EnforcesAggregateDomainConcentration(t *testing.T) {
	const (
		validatorOne = "0x8888888888888888888888888888888888888888"
		validatorTwo = "0x9999999999999999999999999999999999999999"
		domainID     = "operator-a"
	)

	cfg := config.DefaultConfig()
	cfg.Staking.MinValidatorStake = "1"
	cfg.Staking.MaxValidatorStake = "1000"
	cfg.Staking.MaxStakePercentage = 0.60
	cfg.Governance.OwnershipDomainsEnabled = true

	vm, ws := newTestValidatorManager(t, cfg)

	err := vm.RegisterValidatorWithDomain(validatorOne, []byte{1}, "60", 0.05, domainID)
	require.NoError(t, err)

	ws.UpdateTotalStaked()

	err = vm.RegisterValidatorWithDomain(validatorTwo, []byte{2}, "20", 0.05, domainID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "stake domain would exceed concentration limit")

	registeredDomain, err := ws.GetValidatorStakeDomain(validatorOne)
	require.NoError(t, err)
	require.Equal(t, domainID, registeredDomain)
}
