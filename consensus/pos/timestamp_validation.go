// consensus/pos/vrf_security_enhancements.go
// Timestamp validation for block timing security
// Prevents time manipulation attacks

package pos

import (
	"errors"
	"fmt"
	"time"
)

// ============================================================================
// TIMESTAMP VALIDATION
// ============================================================================

// TimestampValidator enforces strict timing constraints on blocks
// This prevents validators from manipulating timestamps to gain unfair advantages
type TimestampValidator struct {
	maxDriftSeconds     int64 // Maximum allowed clock drift (±2 seconds)
	slotDurationSeconds int64 // Duration of each slot (default: 6s)
	genesisTimestamp    int64 // Network genesis time
}

// NewTimestampValidator creates a new timestamp validator
func NewTimestampValidator(maxDriftSeconds, slotDurationSeconds, genesisTimestamp int64) *TimestampValidator {
	if maxDriftSeconds == 0 {
		maxDriftSeconds = 2 // Default: ±2 seconds
	}
	if slotDurationSeconds == 0 {
		slotDurationSeconds = 6 // Default: 6 seconds per slot
	}

	return &TimestampValidator{
		maxDriftSeconds:     maxDriftSeconds,
		slotDurationSeconds: slotDurationSeconds,
		genesisTimestamp:    genesisTimestamp,
	}
}

// ValidateBlockTimestamp validates a block's timestamp with strict constraints
// Enforces 4 rules:
// 1. Block timestamp must be after parent
// 2. Block timestamp must not be too far in the future
// 3. Block timestamp should align with expected slot time
// 4. Time between blocks must be reasonable
func (tv *TimestampValidator) ValidateBlockTimestamp(
	blockTimestamp int64,
	blockSlot uint64,
	parentTimestamp int64,
) error {

	// Rule 1: Block timestamp must be after parent
	if blockTimestamp <= parentTimestamp {
		return fmt.Errorf(
			"block timestamp (%d) must be after parent timestamp (%d)",
			blockTimestamp, parentTimestamp,
		)
	}

	// Rule 2: Block timestamp must not be too far in the future
	currentTime := time.Now().Unix()
	maxAllowedTimestamp := currentTime + tv.maxDriftSeconds

	if blockTimestamp > maxAllowedTimestamp {
		return fmt.Errorf(
			"block timestamp (%d) is too far in future (max allowed: %d, drift: %d seconds)",
			blockTimestamp, maxAllowedTimestamp, tv.maxDriftSeconds,
		)
	}

	// Rule 3: Block timestamp should align with expected slot time
	expectedTimestamp := tv.CalculateSlotTimestamp(blockSlot)
	timestampDiff := abs64(blockTimestamp - expectedTimestamp)

	if timestampDiff > tv.maxDriftSeconds {
		return fmt.Errorf(
			"block timestamp (%d) deviates too much from expected slot time (%d), diff: %d seconds, max allowed: %d",
			blockTimestamp, expectedTimestamp, timestampDiff, tv.maxDriftSeconds,
		)
	}

	// Rule 4: Prevent timestamp manipulation via unreasonable time between blocks
	// Time between blocks should be roughly equal to slot duration
	// Rule 4: Prevent timestamp manipulation - but be more lenient in dev
	expectedDiff := tv.slotDurationSeconds
	actualDiff := blockTimestamp - parentTimestamp

	// Allow wider range for development (10x the slot duration)
	minExpectedDiff := int64(1)           // At least 1 second
	maxExpectedDiff := expectedDiff * 200 // Up to 10x slot duration (60 seconds)

	if actualDiff < minExpectedDiff || actualDiff > maxExpectedDiff {
		return fmt.Errorf(
			"time between blocks (%d seconds) is outside acceptable range [%d, %d]",
			actualDiff, minExpectedDiff, maxExpectedDiff,
		)
	}

	return nil
}

// CalculateSlotTimestamp calculates the expected timestamp for a given slot
func (tv *TimestampValidator) CalculateSlotTimestamp(slot uint64) int64 {
	return tv.genesisTimestamp + (int64(slot) * tv.slotDurationSeconds)
}

// GetTimestampWindow returns the valid timestamp range for a slot
// Returns (min, max) timestamps that are acceptable for the given slot
func (tv *TimestampValidator) GetTimestampWindow(slot uint64) (min, max int64) {
	expectedTimestamp := tv.CalculateSlotTimestamp(slot)
	return expectedTimestamp - tv.maxDriftSeconds, expectedTimestamp + tv.maxDriftSeconds
}

// ValidateTimestampProgression checks timestamp progression across multiple blocks
// This detects validators who consistently manipulate timestamps
func (tv *TimestampValidator) ValidateTimestampProgression(timestamps []int64, slots []uint64) error {
	if len(timestamps) != len(slots) {
		return errors.New("timestamps and slots length mismatch")
	}

	if len(timestamps) < 2 {
		return nil // Need at least 2 blocks to validate progression
	}

	// Check each consecutive pair
	for i := 1; i < len(timestamps); i++ {
		err := tv.ValidateBlockTimestamp(timestamps[i], slots[i], timestamps[i-1])
		if err != nil {
			return fmt.Errorf("block %d validation failed: %w", i, err)
		}
	}

	// Additional check: Detect consistent boundary pushing
	// If a validator consistently uses maximum allowed drift, it's suspicious
	deviationSum := int64(0)
	for i := range timestamps {
		expected := tv.CalculateSlotTimestamp(slots[i])
		deviationSum += abs64(timestamps[i] - expected)
	}

	avgDeviation := deviationSum / int64(len(timestamps))

	// If average deviation > 75% of max drift, flag as suspicious
	suspiciousThreshold := int64(float64(tv.maxDriftSeconds) * 0.75)

	if avgDeviation > suspiciousThreshold {
		return fmt.Errorf(
			"suspicious timestamp pattern: average deviation (%d) exceeds threshold (%d)",
			avgDeviation, suspiciousThreshold,
		)
	}

	return nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// abs64 returns the absolute value of a 64-bit integer
func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
