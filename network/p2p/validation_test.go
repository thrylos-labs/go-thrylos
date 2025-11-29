package p2p

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestMessageValidator_ValidateBlockRangeRequest(t *testing.T) {
	validator := NewMessageValidator(
		10*1024*1024, // 10MB
		100,          // 100 blocks max
		30*time.Second,
		30*time.Second,
	)

	tests := []struct {
		name        string
		startHeight int64
		endHeight   int64
		wantErr     bool
	}{
		{
			name:        "valid range",
			startHeight: 100,
			endHeight:   150,
			wantErr:     false,
		},
		{
			name:        "range too large",
			startHeight: 100,
			endHeight:   300, // 200 blocks > 100 max
			wantErr:     true,
		},
		{
			name:        "invalid range - end before start",
			startHeight: 200,
			endHeight:   100,
			wantErr:     true,
		},
		{
			name:        "negative start height",
			startHeight: -1,
			endHeight:   100,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateBlockRangeRequest(tt.startHeight, tt.endHeight)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateBlockRangeRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestMessageValidator_RateLimit(t *testing.T) {
	validator := NewMessageValidator(
		10*1024*1024,
		100,
		30*time.Second,
		30*time.Second,
	)

	// Create a fake peer ID
	peerID, _ := peer.Decode("12D3KooWBxJKLLvQJCbLN6dW2wqZXFqZP9jXAZcXxKqYXKxPnW2r")

	// First request should succeed
	err := validator.CheckRateLimit(peerID)
	if err != nil {
		t.Errorf("First request should not be rate limited: %v", err)
	}

	// Simulate many requests quickly
	for i := 0; i < DefaultRequestRateLimit; i++ {
		validator.CheckRateLimit(peerID)
	}

	// Next request should be rate limited
	err = validator.CheckRateLimit(peerID)
	if err == nil {
		t.Error("Expected rate limit error after exceeding limit")
	}

	// Release requests
	for i := 0; i < DefaultRequestRateLimit; i++ {
		validator.ReleaseRequest(peerID)
	}
}

func TestMessageValidator_GetMetrics(t *testing.T) {
	validator := NewMessageValidator(
		10*1024*1024,
		100,
		30*time.Second,
		30*time.Second,
	)

	peerID, _ := peer.Decode("12D3KooWBxJKLLvQJCbLN6dW2wqZXFqZP9jXAZcXxKqYXKxPnW2r")

	// Generate some activity
	validator.CheckRateLimit(peerID)
	validator.CheckRateLimit(peerID)

	metrics := validator.GetMetrics()

	if metrics["total_peers"].(int) != 1 {
		t.Errorf("Expected 1 peer, got %d", metrics["total_peers"])
	}

	if metrics["max_message_size"].(int64) != 10*1024*1024 {
		t.Errorf("Expected max message size 10MB, got %d", metrics["max_message_size"])
	}

	if metrics["max_block_range"].(int) != 100 {
		t.Errorf("Expected max block range 100, got %d", metrics["max_block_range"])
	}
}
