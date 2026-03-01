package pos

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
