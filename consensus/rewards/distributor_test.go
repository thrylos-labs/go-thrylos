package rewards

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	accountpkg "github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/state"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
)

func newTestDistributor(t *testing.T, cfg *config.Config) (*Distributor, *state.WorldState) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "thrylos-distributor-test-*")
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

	return NewDistributor(cfg, ws), ws
}

func TestNewDistributor_UsesConfiguredEconomics(t *testing.T) {
	cfg := &config.Config{
		Economics: config.EconomicsConfig{
			InflationRate:       0.05,
			InflationMin:        0.02,
			InflationMax:        0.07,
			GoalBonded:          0.60,
			ValidatorRewardRate: 0.12,
			DelegatorRewardRate: 0.08,
			CommunityTax:        0.03,
		},
	}

	rd := NewDistributor(cfg, nil)

	require.Equal(t, 0.05, rd.currentInflationRate)
	require.Equal(t, 0.05, rd.inflationRate)
	require.Equal(t, 0.05, rd.inflationController.targetInflationRate)
	require.Equal(t, 0.60, rd.inflationController.targetStakingRatio)
	require.Equal(t, 0.02, rd.inflationController.minInflationRate)
	require.Equal(t, 0.07, rd.inflationController.maxInflationRate)
	require.InDelta(t, 0.60, rd.validatorRewardShare(), 0.000001)
}

func TestDistributor_UsesConfiguredRewardShareAndCommission(t *testing.T) {
	cfg := &config.Config{
		Economics: config.EconomicsConfig{
			ValidatorRewardRate: 0.12,
			DelegatorRewardRate: 0.08,
		},
	}

	rd, ws := newTestDistributor(t, cfg)
	err := ws.SetValidator("0x8888888888888888888888888888888888888888", &core.Validator{
		Address:    "0x8888888888888888888888888888888888888888",
		Active:     true,
		Commission: 0.10,
		Stake:      "1000",
	})
	require.NoError(t, err)

	rd.currentInflationRate = 0.06
	rd.currentStakingRatio = 0.50

	require.InDelta(t, 0.60, rd.validatorRewardShare(), 0.000001)
	require.InDelta(t, 0.10, rd.averageCommissionRate(), 0.000001)
	require.InDelta(t, 7.68, rd.calculateValidatorAPY_Global(), 0.000001)
	require.InDelta(t, 4.32, rd.calculateDelegatorAPY(), 0.000001)
}

func TestWithdrawFromCommunityPool_UsesBigIntRewards(t *testing.T) {
	const recipient = "0x9999999999999999999999999999999999999999"

	cfg := &config.Config{}
	rd, ws := newTestDistributor(t, cfg)

	err := ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: recipient,
		Balance: "0",
		Rewards: "0",
	})
	require.NoError(t, err)

	rd.communityPool = "100000000000000000000"

	err = rd.WithdrawFromCommunityPool("10000000000000000000", recipient)
	require.NoError(t, err)

	account, err := ws.GetAccount(recipient)
	require.NoError(t, err)
	require.Equal(t, "10000000000000000000", account.Rewards)
	require.Equal(t, "90000000000000000000", rd.communityPool)
}
