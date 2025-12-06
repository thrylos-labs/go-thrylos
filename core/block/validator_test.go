package block

import (
	"testing"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

func TestValidateTimestamp(t *testing.T) {
	// Setup config
	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			MaxFutureBlockTime: 15 * time.Second,
			MaxPastBlockTime:   2 * time.Hour,
		},
	}

	// [FIX] Pass config to NewValidator
	v := NewValidator(account.ShardID(0), 1, cfg)

	// Define limits (matching your config)
	maxFuture := 15 * time.Second
	maxPast := 2 * time.Hour

	// Current time reference
	now := time.Now().Unix()

	tests := []struct {
		name        string
		blockTime   int64
		prevTime    int64 // 0 means no previous block
		expectError bool
	}{
		{
			name:        "✅ Valid Timestamp (Now)",
			blockTime:   now,
			prevTime:    now - 10,
			expectError: false,
		},
		{
			name:        "✅ Valid Timestamp (Slightly Future but within limit)",
			blockTime:   now + 10, // 10s future < 15s limit
			prevTime:    now - 5,
			expectError: false,
		},
		{
			name:        "❌ Timestamp Too Far Future",
			blockTime:   now + 20, // 20s future > 15s limit
			prevTime:    now,
			expectError: true,
		},
		{
			name:        "❌ Timestamp Too Old",
			blockTime:   now - int64((3 * time.Hour).Seconds()), // 3h past > 2h limit
			prevTime:    now - int64((4 * time.Hour).Seconds()),
			expectError: true,
		},
		{
			name:        "❌ Non-Monotonic (Before Previous Block)",
			blockTime:   now - 100,
			prevTime:    now - 50, // Prev is newer than current
			expectError: true,
		},
		{
			name:        "❌ Duplicate Timestamp (Same as Previous)",
			blockTime:   now,
			prevTime:    now,  // Timestamps must strictly increase
			expectError: true, // Depending on your logic (<= vs <), usually we want strict increase
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create dummy blocks
			block := &core.Block{
				Header: &core.BlockHeader{
					Timestamp: tt.blockTime,
				},
			}

			var prevBlock *core.Block
			if tt.prevTime != 0 {
				prevBlock = &core.Block{
					Header: &core.BlockHeader{
						Timestamp: tt.prevTime,
					},
				}
			}

			err := v.ValidateTimestamp(block, prevBlock, maxFuture, maxPast)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected success but got error: %v", err)
			}
		})
	}
}
