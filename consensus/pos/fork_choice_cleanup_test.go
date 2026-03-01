package pos

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	coremath "github.com/thrylos-labs/go-thrylos/core/math"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/types"
)

func TestForkChoiceProcessAttestation_CleansOldEpochStateOnEpochGrowth(t *testing.T) {
	cfg := &config.Config{}
	ws := NewMockWorldStateForFork()
	ws.validators["0x1"] = &core.Validator{Address: "0x1", Active: true, Stake: coremath.ParseBigInt("1").Bytes()}
	ws.validators["0x2"] = &core.Validator{Address: "0x2", Active: true, Stake: coremath.ParseBigInt("1").Bytes()}

	fcCfg := DefaultForkChoiceConfig()
	fcCfg.CleanupInterval = 0
	fcCfg.MaxEpochsToKeep = 2

	fc := NewForkChoiceWithConfig(cfg, ws, nil, fcCfg)

	for epoch := uint64(1); epoch <= 4; epoch++ {
		fc.ProcessAttestation(&types.Attestation{
			ValidatorAddress: "0x1",
			BlockHash:        fmt.Sprintf("block-%d", epoch),
			Epoch:            epoch,
		})
	}

	_, hasOldEpochMessages := fc.latestMessages[1]
	require.False(t, hasOldEpochMessages)

	_, hasOldEpochAttestations := fc.epochAttestations[1]
	require.False(t, hasOldEpochAttestations)

	_, hasOldBlock := fc.attestationsByBlock["block-1"]
	require.False(t, hasOldBlock)

	for _, epoch := range fc.blockEpochMap {
		require.GreaterOrEqual(t, epoch, uint64(2))
	}
}

func TestForkChoiceProcessAttestation_PrunesBlocksWithinEpoch(t *testing.T) {
	cfg := &config.Config{}
	ws := NewMockWorldStateForFork()
	ws.validators["0x1"] = &core.Validator{Address: "0x1", Active: true, Stake: coremath.ParseBigInt("1").Bytes()}
	ws.validators["0x2"] = &core.Validator{Address: "0x2", Active: true, Stake: coremath.ParseBigInt("1").Bytes()}
	ws.validators["0x3"] = &core.Validator{Address: "0x3", Active: true, Stake: coremath.ParseBigInt("1").Bytes()}

	fcCfg := DefaultForkChoiceConfig()
	fcCfg.CleanupInterval = 0
	fcCfg.MaxBlocksPerEpoch = 2

	fc := NewForkChoiceWithConfig(cfg, ws, nil, fcCfg)

	for idx, validator := range []string{"0x1", "0x2", "0x3"} {
		fc.ProcessAttestation(&types.Attestation{
			ValidatorAddress: validator,
			BlockHash:        fmt.Sprintf("epoch-1-block-%d", idx+1),
			Epoch:            1,
		})
	}

	require.Len(t, fc.blockScores, 2)
	require.NotContains(t, fc.blockScores, "epoch-1-block-1")
	require.Contains(t, fc.blockScores, "epoch-1-block-2")
	require.Contains(t, fc.blockScores, "epoch-1-block-3")
	require.Len(t, fc.epochBlockOrder[1], 2)
}
