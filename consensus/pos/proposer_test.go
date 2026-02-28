package pos

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
)

func TestSetSelectionStrategy_RestrictsUnsafeModesOutsideDevelopment(t *testing.T) {
	productionProposer := NewBlockProposer(&config.Config{}, nil, "")
	productionProposer.SetSelectionStrategy(StrategyHighestGasPrice)
	require.Equal(t, StrategyBalanced, productionProposer.selectionStrategy)

	developmentProposer := NewBlockProposer(&config.Config{Environment: "development"}, nil, "")
	developmentProposer.SetSelectionStrategy(StrategyHighestGasPrice)
	require.Equal(t, StrategyHighestGasPrice, developmentProposer.selectionStrategy)
}

func TestEnqueueAttestation_OnlyAdvancesEpochOnSuccessfulQueue(t *testing.T) {
	engine := &ConsensusEngine{
		broadcastChan:     make(chan interface{}, 1),
		currentSlot:       9,
		currentEpoch:      3,
		lastAttestedEpoch: 1,
	}

	engine.broadcastChan <- struct{}{}
	require.False(t, engine.enqueueAttestation(nil))
	require.Equal(t, uint64(1), engine.lastAttestedEpoch)

	<-engine.broadcastChan
	require.True(t, engine.enqueueAttestation(nil))
	require.Equal(t, uint64(3), engine.lastAttestedEpoch)
}
