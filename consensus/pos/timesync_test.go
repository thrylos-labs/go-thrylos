// consensus/pos/timesync_test.go
package pos

import (
	"testing"
	"time"
)

func TestValidateTimestamp_Drift(t *testing.T) {
	tv := NewTimeValidator()

	// Define strict limits for testing (match your constants)
	maxDrift := 5 * time.Second
	now := time.Now().UTC().Unix()

	tests := []struct {
		name      string
		timestamp int64
		wantErr   bool
	}{
		{"Valid Timestamp (Now)", now, false},
		{"Valid Future (4s)", now + 4, false},
		{"Valid Past (4s)", now - 4, false},
		{"Invalid Future (6s)", now + 6, true},
		{"Invalid Past (6s)", now - 6, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tv.ValidateTimestamp(tt.timestamp, maxDrift, maxDrift)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTimestamp() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateBlockTimestamp_Intervals(t *testing.T) {
	tv := NewTimeValidator()
	maxDrift := 5 * time.Second

	// FIX: Set previous block to just 2 seconds ago (instead of 10).
	// This allows us to add +2s for the next block without hitting the "5s past drift" limit.
	prevTime := time.Now().UTC().Unix() - 2

	tests := []struct {
		name      string
		blockTime int64
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "Valid Interval (2s)",
			blockTime: prevTime + 2, // Results in time.Now() -> Valid
			wantErr:   false,
		},
		{
			name:      "Too Fast (0s - Duplicate Time)",
			blockTime: prevTime,
			wantErr:   true,
			errMsg:    "must be strictly greater",
		},
		{
			name:      "Too Fast (0.5s - Spam)",
			blockTime: prevTime, // Integer math handles this same as 0s usually
			wantErr:   true,
		},
		{
			name:      "Backwards Time",
			blockTime: prevTime - 1,
			wantErr:   true,
			errMsg:    "must be strictly greater",
		},
		{
			name:      "Huge Gap (2 Hours)",
			blockTime: prevTime + 7200,
			wantErr:   true,
			errMsg:    "too large",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tv.ValidateBlockTimestamp(tt.blockTime, prevTime, maxDrift, maxDrift)
			// Helper to check if error matches expectation
			if (err != nil) != tt.wantErr {
				t.Errorf("Test '%s' failed: ValidateBlockTimestamp() error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
		})
	}
}

func TestSystemTimeDrift_Detection(t *testing.T) {
	tv := NewTimeValidator()

	// 1. Initial State: Healthy
	drift, err := tv.CheckSystemTimeDrift()
	if err != nil {
		t.Errorf("Initial check should be healthy, got error: %v", err)
	}

	// FIX: Allow for tiny execution delays.
	// We expect drift to be less than 1ms (negligible), but not necessarily exactly 0.
	if drift > time.Millisecond || drift < -time.Millisecond {
		t.Errorf("Initial drift should be negligible (<1ms), got %v", drift)
	}

	// 2. Simulate a "Jump"
	tv.mu.Lock()
	// Fake that the last check was 1 hour ago in Wall Clock time...
	tv.lastDriftCheck = time.Now().Add(-1 * time.Hour)
	// ...but Monotonic time remains "now" (real elapsed is small)
	tv.mu.Unlock()

	// 3. Run Check - Should detect the discrepancy
	drift, err = tv.CheckSystemTimeDrift()

	// We expect a critical error because WallClock elapsed (1h) != Monotonic elapsed (~0s)
	if err == nil {
		t.Error("Expected error for massive time jump, got nil")
	} else {
		t.Logf("Successfully detected time jump: %v", err)
	}

	// 4. Verify Failure Count Incremented
	stats := tv.GetTimeDriftStatus()
	if stats["drift_checks_failed"].(int) < 1 {
		t.Error("Drift checks failed count did not increment")
	}
}

func TestTimeSyncHealthy(t *testing.T) {
	tv := NewTimeValidator()

	// Should be healthy initially
	if !tv.IsTimeSyncHealthy() {
		t.Error("New validator should be healthy")
	}

	// Force unhealthy state
	tv.mu.Lock()
	tv.driftChecksFailed = 5
	tv.mu.Unlock()

	if tv.IsTimeSyncHealthy() {
		t.Error("Validator should be unhealthy after failed checks")
	}
}
