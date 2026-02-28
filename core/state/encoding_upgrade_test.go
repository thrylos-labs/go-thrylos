package state

import (
	"testing"

	"github.com/thrylos-labs/go-thrylos/config"
)

func TestDesiredStateRootEncodingVersionForHeight(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Consensus.StateEncodingUpgradeHeight = 100

	ws := &WorldState{
		config:                   cfg,
		stateRootEncodingVersion: stateRootEncodingVersionLegacy,
	}

	if got := ws.desiredStateRootEncodingVersionForHeight(99); got != stateRootEncodingVersionLegacy {
		t.Fatalf("expected legacy encoding before upgrade height, got %d", got)
	}

	if got := ws.desiredStateRootEncodingVersionForHeight(100); got != stateRootEncodingVersionCanonical {
		t.Fatalf("expected canonical encoding at upgrade height, got %d", got)
	}
}

func TestDesiredStateRootEncodingVersionForFreshChainWithoutUpgrade(t *testing.T) {
	ws := &WorldState{
		config: config.DefaultConfig(),
	}

	if got := ws.desiredStateRootEncodingVersionForHeight(0); got != stateRootEncodingVersionCanonical {
		t.Fatalf("expected fresh chains without an upgrade gate to use canonical encoding, got %d", got)
	}
}
