// consensus/pos/timesync.go
// Time synchronization utilities to prevent time drift attacks
// Validators must maintain accurate time synchronization to participate in consensus

package pos

import (
	"fmt"
	"math"
	"sync"
	"time"
)

const (
	// MaxAllowedTimeDrift is the maximum acceptable clock drift for validators
	// Tightened from 15s to 5s to reduce manipulation window (Recommendation 1)
	MaxAllowedTimeDrift = 5 * time.Second

	// TimeCheckInterval is how often we check system time drift
	TimeCheckInterval = 1 * time.Minute

	// MaxCumulativeTimeDrift is the maximum cumulative drift before warning
	MaxCumulativeTimeDrift = 10 * time.Second

	// MinBlockTimeInterval ensures blocks aren't produced too rapidly (Spam protection)
	MinBlockTimeInterval = 1 * time.Second
)

// TimeValidator monitors and validates system time synchronization
type TimeValidator struct {
	mu sync.RWMutex

	// Track time drift samples
	driftSamples    []time.Duration
	maxSamples      int
	lastNTPSync     time.Time
	cumulativeDrift time.Duration
	warningIssued   bool
	ntpSyncRequired bool

	// For detecting local clock manipulation (jumps)
	lastDriftCheck    time.Time
	lastMonotonic     time.Time // Uses Go's monotonic clock
	driftChecksFailed int
}

// NewTimeValidator creates a new time validator
func NewTimeValidator() *TimeValidator {
	return &TimeValidator{
		driftSamples:    make([]time.Duration, 0, 100),
		maxSamples:      100,
		lastNTPSync:     time.Now().UTC(),
		lastDriftCheck:  time.Now().UTC(),
		lastMonotonic:   time.Now(), // Captures monotonic clock
		ntpSyncRequired: true,
	}
}

// ValidateTimestamp checks if a timestamp is within acceptable bounds
// This is the PRIMARY defense against time manipulation attacks
func (tv *TimeValidator) ValidateTimestamp(timestamp int64, maxFutureDrift, maxPastDrift time.Duration) error {
	currentTime := time.Now().UTC().Unix()

	// Check future drift (Preventing "Time Warp" attacks where miners post from the future)
	futureDrift := timestamp - currentTime
	if futureDrift > int64(maxFutureDrift.Seconds()) {
		return fmt.Errorf("timestamp %d is %d seconds in future (max allowed: %d seconds)",
			timestamp, futureDrift, int64(maxFutureDrift.Seconds()))
	}

	// Check past drift (Preventing selfish mining or withholding)
	pastDrift := currentTime - timestamp
	if pastDrift > int64(maxPastDrift.Seconds()) {
		return fmt.Errorf("timestamp %d is %d seconds in past (max allowed: %d seconds)",
			timestamp, pastDrift, int64(maxPastDrift.Seconds()))
	}

	return nil
}

// CheckSystemTimeDrift checks if the system clock has significant drift
// Implements local anomaly detection by comparing Wall Clock vs Monotonic Clock
func (tv *TimeValidator) CheckSystemTimeDrift() (time.Duration, error) {
	tv.mu.Lock()
	defer tv.mu.Unlock()

	now := time.Now().UTC()
	monotonicNow := time.Now()

	// 1. Calculate expected elapsed time based on monotonic clock (immune to wall-clock changes)
	realElapsed := monotonicNow.Sub(tv.lastMonotonic)

	// 2. Calculate elapsed time based on system wall clock
	wallElapsed := now.Sub(tv.lastDriftCheck)

	// 3. The difference is the "Drift" caused by OS clock adjustments
	// If the OS clock was changed manually, wallElapsed will differ significantly from realElapsed
	drift := wallElapsed - realElapsed

	// Update baselines
	tv.lastDriftCheck = now
	tv.lastMonotonic = monotonicNow

	// Record the drift sample
	if len(tv.driftSamples) >= tv.maxSamples {
		tv.driftSamples = tv.driftSamples[1:]
	}
	tv.driftSamples = append(tv.driftSamples, drift)

	// Calculate cumulative drift (Absolute value to detect volatility)
	tv.cumulativeDrift = tv.calculateAverageDrift()

	// Check if drift is excessive (Outlier Detection)
	// We use Abs() because drift can be negative if clock is set backwards
	if math.Abs(drift.Seconds()) > 1.0 {
		tv.driftChecksFailed++
		return drift, fmt.Errorf("CRITICAL: System clock jumped by %s (potential manipulation detected)", drift)
	}

	// Check cumulative average drift
	if tv.cumulativeDrift > MaxCumulativeTimeDrift {
		if !tv.warningIssued {
			tv.warningIssued = true
			return drift, fmt.Errorf("WARNING: Average system time drift (%s) exceeds safe threshold (%s)",
				tv.cumulativeDrift, MaxCumulativeTimeDrift)
		}
	}

	return drift, nil
}

// calculateAverageDrift calculates the average drift from recent samples
func (tv *TimeValidator) calculateAverageDrift() time.Duration {
	if len(tv.driftSamples) == 0 {
		return 0
	}

	var total float64
	for _, drift := range tv.driftSamples {
		// usage of Abs ensures we track volatility, not just offset
		total += math.Abs(drift.Seconds())
	}

	avgSeconds := total / float64(len(tv.driftSamples))
	return time.Duration(avgSeconds * float64(time.Second))
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

	if tv.cumulativeDrift > MaxCumulativeTimeDrift {
		return false
	}
	// Strict: Fail immediately if checks are failing
	if tv.driftChecksFailed > 0 {
		return false
	}
	if time.Since(tv.lastDriftCheck) > 5*time.Minute {
		return false
	}

	return true
}

// StartDriftMonitoring starts background monitoring of time drift
func (tv *TimeValidator) StartDriftMonitoring(stopChan <-chan struct{}) {
	ticker := time.NewTicker(TimeCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			drift, err := tv.CheckSystemTimeDrift()
			if err != nil {
				fmt.Printf("⚠️  Time drift warning: %v (drift: %s)\n", err, drift)
				// We do NOT reset driftChecksFailed here; manual intervention or successful checks required
			} else {
				tv.mu.Lock()
				// Only decay failure count on success to prevent flapping
				if tv.driftChecksFailed > 0 {
					tv.driftChecksFailed--
				}
				tv.warningIssued = false
				tv.mu.Unlock()
			}

		case <-stopChan:
			return
		}
	}
}

// ValidateBlockTimestamp validates a block timestamp with enhanced checks
func (tv *TimeValidator) ValidateBlockTimestamp(
	blockTimestamp int64,
	previousBlockTimestamp int64,
	maxFutureDrift time.Duration,
	maxPastDrift time.Duration,
) error {

	// 1. Check against current system time (always enforced)
	if err := tv.ValidateTimestamp(blockTimestamp, maxFutureDrift, maxPastDrift); err != nil {
		return fmt.Errorf("timestamp validation failed: %v", err)
	}

	if previousBlockTimestamp == 0 {
		if !tv.IsTimeSyncHealthy() {
			fmt.Printf("⚠️  WARNING: Validating first block without previous timestamp and unhealthy time sync status\n")
		}
		return nil
	}

	// 2. Check monotonic increase
	if blockTimestamp <= previousBlockTimestamp {
		return fmt.Errorf("block timestamp %d must be strictly greater than previous %d",
			blockTimestamp, previousBlockTimestamp)
	}

	// 3. [NEW] Enforce Minimum Block Interval (Spam Protection)
	// Prevents validators from creating blocks with 1ms difference
	timeDiff := time.Duration(blockTimestamp-previousBlockTimestamp) * time.Second
	if timeDiff < MinBlockTimeInterval {
		return fmt.Errorf("block timestamp increment %s too small (min: %s)", timeDiff, MinBlockTimeInterval)
	}

	// 4. Check reasonable increment
	maxReasonableIncrement := int64(3600) // 1 hour
	increment := blockTimestamp - previousBlockTimestamp
	if increment > maxReasonableIncrement {
		return fmt.Errorf(
			"block timestamp increment %d seconds too large (max: %d seconds)",
			increment, maxReasonableIncrement,
		)
	}

	// 5. Warn if system time drift is unhealthy
	if !tv.IsTimeSyncHealthy() {
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
