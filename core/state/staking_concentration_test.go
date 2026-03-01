package state

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	coremath "github.com/thrylos-labs/go-thrylos/core/math"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
)

func stakeTestAddress(seed byte) string {
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

func stakeBytes(v string) []byte {
	return coremath.ParseBigInt(v).Bytes()
}

func TestDelegate_UsesGenesisStakeInConcentrationChecks(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "thrylos-test-stake-concentration-*")
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
		Economics: config.EconomicsConfig{
			GenesisSupply: "1000000000000000000000000",
		},
		Staking: config.StakingConfig{
			MinValidatorStake:          "100000000000000000000",
			MinDelegation:              "1000000000000000000",
			MaxValidatorStake:          "10000000000000000000000000",
			MaxStakePercentage:         0.60,
			MaxDelegationsPerValidator: 100,
		},
	}

	ws, err := NewWorldState(tmpDir, 0, 1, cfg, badgerStore)
	require.NoError(t, err)

	genesisAccount := stakeTestAddress(90)
	validators := []*core.Validator{
		{Address: stakeTestAddress(1), Pubkey: []byte{1}, Active: true, Stake: stakeBytes("500000000000000000000")},
		{Address: stakeTestAddress(2), Pubkey: []byte{2}, Active: true, Stake: stakeBytes("500000000000000000000")},
		{Address: stakeTestAddress(3), Pubkey: []byte{3}, Active: true, Stake: stakeBytes("500000000000000000000")},
		{Address: stakeTestAddress(4), Pubkey: []byte{4}, Active: true, Stake: stakeBytes("500000000000000000000")},
	}

	err = ws.InitializeGenesis(genesisAccount, cfg.Economics.GenesisSupply, validators)
	require.NoError(t, err)

	stakingManager := ws.GetStakingManager()
	validatorAddr := validators[0].Address
	oneToken := coremath.ParseBigInt("1000000000000000000")

	err = stakingManager.Delegate(genesisAccount, validatorAddr, oneToken)
	require.NoError(t, err)

	err = stakingManager.Delegate(genesisAccount, validatorAddr, oneToken)
	require.NoError(t, err, "repeat delegation should use genesis stake in the denominator")

	totalStaked := ws.GetTotalStaked()
	require.Equal(t, "2002000000000000000000", totalStaked.String())
}
