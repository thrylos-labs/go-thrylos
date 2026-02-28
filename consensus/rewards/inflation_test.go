package rewards

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
)

func TestNewInflationManager_UsesConfiguredEconomics(t *testing.T) {
	cfg := &config.Config{
		Economics: config.EconomicsConfig{
			InflationRate:       0.05,
			InflationMin:        0.02,
			InflationMax:        0.07,
			GoalBonded:          0.55,
			CommunityTax:        0.03,
			ValidatorRewardRate: 0.12,
			DelegatorRewardRate: 0.08,
			ValidatorRewardPool: "1000",
		},
	}

	im := NewInflationManager(cfg, nil)

	require.Equal(t, 0.05, im.targetInflationRate)
	require.Equal(t, 0.05, im.currentInflationRate)
	require.Equal(t, 0.02, im.minInflationRate)
	require.Equal(t, 0.07, im.maxInflationRate)
	require.Equal(t, 0.55, im.targetStakingRatio)
	require.InDelta(t, 0.60, im.validatorRewardShare(), 0.000001)
	require.InDelta(t, 0.03, im.communityTaxRate(), 0.000001)
}

func TestInflationManager_DistributeRewards_UsesConfiguredSplits(t *testing.T) {
	cfg := &config.Config{
		Economics: config.EconomicsConfig{
			CommunityTax:        0.03,
			ValidatorRewardRate: 0.12,
			DelegatorRewardRate: 0.08,
		},
	}

	im := NewInflationManager(cfg, nil)
	validatorShare, delegatorShare, communityShare := im.distributeRewards(big.NewInt(1000))

	require.Equal(t, "29", communityShare)
	require.Equal(t, "582", validatorShare)
	require.Equal(t, "389", delegatorShare)
}
