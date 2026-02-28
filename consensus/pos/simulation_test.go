package pos

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func baseSimulationValidators() []SimulationValidator {
	return []SimulationValidator{
		{Address: "A1", Stake: "400", DomainID: "operator-a", Active: true},
		{Address: "A2", Stake: "200", DomainID: "operator-a", Active: true},
		{Address: "B1", Stake: "200", DomainID: "operator-b", Active: true},
		{Address: "C1", Stake: "200", DomainID: "operator-c", Active: true},
	}
}

func TestRunAdversarialSimulation_PartitionStallsFinality(t *testing.T) {
	result, err := RunAdversarialSimulation(SimulationConfig{
		Validators:       baseSimulationValidators(),
		Slots:            6,
		SlotsPerEpoch:    6,
		CooldownWindow:   1,
		QuorumThreshold:  0.67,
		PartitionWindows: []SimulationPartitionWindow{{StartSlot: 1, EndSlot: 6, Groups: [][]string{{"A1", "A2"}, {"B1", "C1"}}}},
	})
	require.NoError(t, err)

	require.Equal(t, uint64(6), result.ProducedBlocks)
	require.Equal(t, uint64(6), result.PartitionedSlots)
	require.Equal(t, uint64(0), result.FinalizedBlocks)
	require.Equal(t, uint64(6), result.UnfinalizedBlocks)
}

func TestRunAdversarialSimulation_DelayedAttestationsIncreaseFinalityDelay(t *testing.T) {
	result, err := RunAdversarialSimulation(SimulationConfig{
		Validators:      baseSimulationValidators(),
		Slots:           6,
		SlotsPerEpoch:   6,
		CooldownWindow:  1,
		QuorumThreshold: 0.67,
		DelayedAttestations: []SimulationDelayedAttestation{
			{Validator: "B1", StartSlot: 1, EndSlot: 6, DelaySlots: 1},
			{Validator: "C1", StartSlot: 1, EndSlot: 6, DelaySlots: 1},
		},
	})
	require.NoError(t, err)

	require.Equal(t, uint64(6), result.ProducedBlocks)
	require.Greater(t, result.FinalizedBlocks, uint64(0))
	require.Greater(t, result.UnfinalizedBlocks, uint64(0))
	require.Greater(t, result.AverageFinalityDelay, 0.0)
	require.GreaterOrEqual(t, result.MaxFinalityDelay, uint64(1))
}

func TestRunAdversarialSimulation_ProposerWithholdingCreatesMissedSlots(t *testing.T) {
	result, err := RunAdversarialSimulation(SimulationConfig{
		Validators:      baseSimulationValidators(),
		Slots:           6,
		SlotsPerEpoch:   6,
		CooldownWindow:  1,
		QuorumThreshold: 0.67,
		WithheldSlots: map[uint64]bool{
			2: true,
			5: true,
		},
	})
	require.NoError(t, err)

	require.Equal(t, uint64(2), result.WithheldSlots)
	require.Equal(t, uint64(2), result.MissedSlots)
	require.Equal(t, uint64(4), result.ProducedBlocks)
	require.Equal(t, int64(4), result.HeadHeight)
}

func TestRunAdversarialSimulation_PartitionHealReplaysBufferedAttestations(t *testing.T) {
	result, err := RunAdversarialSimulation(SimulationConfig{
		Validators:      baseSimulationValidators(),
		Slots:           6,
		SlotsPerEpoch:   6,
		CooldownWindow:  1,
		QuorumThreshold: 0.67,
		PartitionWindows: []SimulationPartitionWindow{{
			StartSlot:                  1,
			EndSlot:                    3,
			Groups:                     [][]string{{"A1", "A2"}, {"B1", "C1"}},
			ReplayBufferedAttestations: true,
		}},
	})
	require.NoError(t, err)

	require.Equal(t, uint64(6), result.ProducedBlocks)
	require.Equal(t, uint64(3), result.PartitionedSlots)
	require.Greater(t, result.DeliveredAttestations, uint64(0))
	require.Greater(t, result.FinalizedBlocks, uint64(0))
	require.Less(t, result.UnfinalizedBlocks, result.ProducedBlocks)
	require.Greater(t, result.MaxFinalityDelay, uint64(0))
}
