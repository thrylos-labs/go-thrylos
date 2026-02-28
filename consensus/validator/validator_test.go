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
