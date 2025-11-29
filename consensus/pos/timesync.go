// consensus/pos/timesync.go
// Time synchronization utilities to prevent time drift attacks
// Validators must maintain accurate time synchronization to participate in consensus

package pos

import (
	"fmt"
	"sync"
	"time"
)

const (
	// MaxAllowedTimeDrift is the maximum acceptable clock drift for validators
	// This must match or be stricter than MaxFutureBlockTime in config
	MaxAllowedTimeDrift = 15 * time.Second

	// TimeCheckInterval is how often we check system time drift
	TimeCheckInterval = 1 * time.Minute

	// MaxCumulativeTimeDrift is the maximum cumulative drift before warning
	MaxCumulativeTimeDrift = 30 * time.Second
)

// TimeValidator monitors and validates system time synchronization
type TimeValidator struct {
	mu sync.RWMutex

	// Track time drift samples
	driftSamples      []time.Duration
	maxSamples        int
	lastNTPSync       time.Time
	cumulativeDrift   time.Duration
	warningIssued     bool
	ntpSyncRequired   bool
	lastDriftCheck    time.Time
	driftChecksFailed int
}

// NewTimeValidator creates a new time validator
func NewTimeValidator() *TimeValidator {
	return &TimeValidator{
		driftSamples:    make([]time.Duration, 0, 100),
		maxSamples:      100,
		lastNTPSync:     time.Now(),
		lastDriftCheck:  time.Now(),
		ntpSyncRequired: true, // Require NTP by default
	}
}

// ValidateTimestamp checks if a timestamp is within acceptable bounds
// This is the PRIMARY defense against time manipulation attacks
func (tv *TimeValidator) ValidateTimestamp(timestamp int64, maxFutureDrift, maxPastDrift time.Duration) error {
	currentTime := time.Now().Unix()

	// Check future drift
	futureDrift := timestamp - currentTime
	if futureDrift > int64(maxFutureDrift.Seconds()) {
		return fmt.Errorf("timestamp %d is %d seconds in future (max allowed: %d seconds)",
			timestamp, futureDrift, int64(maxFutureDrift.Seconds()))
	}

	// Check past drift
	pastDrift := currentTime - timestamp
	if pastDrift > int64(maxPastDrift.Seconds()) {
		return fmt.Errorf("timestamp %d is %d seconds in past (max allowed: %d seconds)",
			timestamp, pastDrift, int64(maxPastDrift.Seconds()))
	}

	return nil
}

// CheckSystemTimeDrift checks if the system clock has significant drift
// This should be called periodically by validators
func (tv *TimeValidator) CheckSystemTimeDrift() (time.Duration, error) {
	tv.mu.Lock()
	defer tv.mu.Unlock()

	// In a production system, this would:
	// 1. Query NTP servers
	// 2. Calculate offset from network time
	// 3. Track drift over time
	// 4. Alert if drift exceeds thresholds

	// For now, we'll implement a basic check
	// TODO: Implement actual NTP synchronization check

	drift := time.Duration(0)

	// Record the drift sample
	if len(tv.driftSamples) >= tv.maxSamples {
		tv.driftSamples = tv.driftSamples[1:]
	}
	tv.driftSamples = append(tv.driftSamples, drift)

	// Calculate cumulative drift
	tv.cumulativeDrift = tv.calculateAverageDrift()

	// Check if drift is excessive
	if tv.cumulativeDrift > MaxCumulativeTimeDrift {
		if !tv.warningIssued {
			tv.warningIssued = true
			return drift, fmt.Errorf("WARNING: System time drift (%s) exceeds safe threshold (%s) - NTP sync required",
				tv.cumulativeDrift, MaxCumulativeTimeDrift)
		}
	}

	tv.lastDriftCheck = time.Now()
	return drift, nil
}

// calculateAverageDrift calculates the average drift from recent samples
func (tv *TimeValidator) calculateAverageDrift() time.Duration {
	if len(tv.driftSamples) == 0 {
		return 0
	}

	var total time.Duration
	for _, drift := range tv.driftSamples {
		total += drift
	}

	return total / time.Duration(len(tv.driftSamples))
}

// GetTimeDriftStatus returns the current time drift status
func (tv *TimeValidator) GetTimeDriftStatus() map[string]interface{} {
	tv.mu.RLock()
	defer tv.mu.RUnlock()

	return map[string]interface{}{
		"cumulative_drift_seconds":  tv.cumulativeDrift.Seconds(),
		"max_allowed_drift_seconds": MaxAllowedTimeDrift.Seconds(),
		"drift_samples_count":       len(tv.driftSamples),
		"last_ntp_sync":             tv.lastNTPSync,
		"warning_issued":            tv.warningIssued,
		"ntp_sync_required":         tv.ntpSyncRequired,
		"last_drift_check":          tv.lastDriftCheck,
		"drift_checks_failed":       tv.driftChecksFailed,
	}
}

// IsTimeSyncHealthy returns true if time synchronization is healthy
func (tv *TimeValidator) IsTimeSyncHealthy() bool {
	tv.mu.RLock()
	defer tv.mu.RUnlock()

	// System is healthy if:
	// 1. Cumulative drift is within limits
	// 2. Recent drift check was successful
	// 3. No excessive warnings

	if tv.cumulativeDrift > MaxCumulativeTimeDrift {
		return false
	}

	if tv.driftChecksFailed > 3 {
		return false
	}

	// Check if last drift check was recent
	if time.Since(tv.lastDriftCheck) > 5*time.Minute {
		return false
	}

	return true
}

// StartDriftMonitoring starts background monitoring of time drift
// Should be called when consensus engine starts
func (tv *TimeValidator) StartDriftMonitoring(stopChan <-chan struct{}) {
	ticker := time.NewTicker(TimeCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			drift, err := tv.CheckSystemTimeDrift()
			if err != nil {
				fmt.Printf("⚠️  Time drift warning: %v (drift: %s)\n", err, drift)
				tv.mu.Lock()
				tv.driftChecksFailed++
				tv.mu.Unlock()
			} else {
				// Reset failure count on success
				tv.mu.Lock()
				tv.driftChecksFailed = 0
				tv.warningIssued = false
				tv.mu.Unlock()
			}

		case <-stopChan:
			return
		}
	}
}

// ValidateBlockTimestamp validates a block timestamp with enhanced checks
// This combines multiple validation strategies for defense in depth
func (tv *TimeValidator) ValidateBlockTimestamp(
	blockTimestamp int64,
	previousBlockTimestamp int64,
	maxFutureDrift time.Duration,
	maxPastDrift time.Duration,
) error {
	// 1. Check against current system time
	if err := tv.ValidateTimestamp(blockTimestamp, maxFutureDrift, maxPastDrift); err != nil {
		return fmt.Errorf("timestamp validation failed: %v", err)
	}

	// 2. Check monotonic increase from previous block
	if blockTimestamp <= previousBlockTimestamp {
		return fmt.Errorf("block timestamp %d must be greater than previous block timestamp %d",
			blockTimestamp, previousBlockTimestamp)
	}

	// 3. Check reasonable increment (not too far ahead of previous)
	// A block shouldn't jump more than 1 hour ahead of previous block
	maxReasonableIncrement := int64(3600) // 1 hour
	increment := blockTimestamp - previousBlockTimestamp
	if increment > maxReasonableIncrement {
		return fmt.Errorf("block timestamp increment %d seconds too large (max: %d seconds)",
			increment, maxReasonableIncrement)
	}

	// 4. Warn if system time drift is unhealthy
	if !tv.IsTimeSyncHealthy() {
		// Don't reject, but log warning
		fmt.Printf("⚠️  WARNING: Validating block with unhealthy time sync status\n")
	}

	return nil
}

// RequireNTPSync marks that NTP synchronization is required
func (tv *TimeValidator) RequireNTPSync(required bool) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	tv.ntpSyncRequired = required
}

// IsNTPSyncRequired returns whether NTP sync is required
func (tv *TimeValidator) IsNTPSyncRequired() bool {
	tv.mu.RLock()
	defer tv.mu.RUnlock()
	return tv.ntpSyncRequired
}
