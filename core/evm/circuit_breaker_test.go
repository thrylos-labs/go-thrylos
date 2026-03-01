package evm

import (
	"testing"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
)

func TestCircuitBreakerUsesConfigLimits(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Economics.EVMMaxGasPerWindow = 100
	cfg.Economics.EVMMaxTxPerWindow = 2
	cfg.Economics.EVMWindowDurationSeconds = 10

	executor := &RevmExecutor{config: cfg}

	if err := executor.CheckAndReserveWindowGas(40); err != nil {
		t.Fatalf("unexpected error on first reservation: %v", err)
	}
	if err := executor.CheckAndReserveWindowGas(40); err != nil {
		t.Fatalf("unexpected error on second reservation: %v", err)
	}
	if err := executor.CheckAndReserveWindowGas(10); err == nil {
		t.Fatalf("expected tx-count limit to reject third reservation")
	}
}

func TestCircuitBreakerWindowResetsAndTracksGovernanceUpdatedConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Economics.EVMMaxGasPerWindow = 100
	cfg.Economics.EVMMaxTxPerWindow = 10
	cfg.Economics.EVMWindowDurationSeconds = 1

	executor := &RevmExecutor{config: cfg}

	if err := executor.CheckAndReserveWindowGas(90); err != nil {
		t.Fatalf("unexpected initial reservation error: %v", err)
	}
	if err := executor.CheckAndReserveWindowGas(20); err == nil {
		t.Fatalf("expected gas window limit to reject reservation")
	}

	cfg.Economics.EVMMaxGasPerWindow = 200
	executor.windowStart = time.Now().Add(-2 * time.Second)

	if err := executor.CheckAndReserveWindowGas(150); err != nil {
		t.Fatalf("expected updated config to allow larger reservation after window reset: %v", err)
	}
}
