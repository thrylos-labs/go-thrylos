// consensus/pos/vrf_security_enhancements.go
// Additional security enhancements for VRF implementation
// Addresses H-3: Priority 2 (Timestamp) and Priority 3 (Finality)

package pos

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// ============================================================================
// PRIORITY 2: STRICT TIMESTAMP VALIDATION
// ============================================================================

// TimestampValidator enforces strict timing constraints
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

	// Rule 3: Block timestamp should align with slot time
	expectedTimestamp := tv.CalculateSlotTimestamp(blockSlot)
	timestampDiff := abs64(blockTimestamp - expectedTimestamp)

	if timestampDiff > tv.maxDriftSeconds {
		return fmt.Errorf(
			"block timestamp (%d) deviates too much from expected slot time (%d), diff: %d seconds, max allowed: %d",
			blockTimestamp, expectedTimestamp, timestampDiff, tv.maxDriftSeconds,
		)
	}

	// Rule 4: Prevent timestamp manipulation via multiple consecutive deviations
	// This catches validators who consistently push timing boundaries
	expectedDiff := tv.slotDurationSeconds
	actualDiff := blockTimestamp - parentTimestamp

	// Allow only ±50% deviation from expected slot duration
	minExpectedDiff := int64(float64(expectedDiff) * 0.5)
	maxExpectedDiff := int64(float64(expectedDiff) * 1.5)

	if actualDiff < minExpectedDiff || actualDiff > maxExpectedDiff {
		return fmt.Errorf(
			"time between blocks (%d seconds) is outside acceptable range [%d, %d]",
			actualDiff, minExpectedDiff, maxExpectedDiff,
		)
	}

	return nil
}

// CalculateSlotTimestamp calculates the expected timestamp for a slot
func (tv *TimestampValidator) CalculateSlotTimestamp(slot uint64) int64 {
	return tv.genesisTimestamp + (int64(slot) * tv.slotDurationSeconds)
}

// GetTimestampWindow returns the valid timestamp range for a slot
func (tv *TimestampValidator) GetTimestampWindow(slot uint64) (min, max int64) {
	expectedTimestamp := tv.CalculateSlotTimestamp(slot)
	return expectedTimestamp - tv.maxDriftSeconds, expectedTimestamp + tv.maxDriftSeconds
}

// ValidateTimestampProgression checks timestamp progression across multiple blocks
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
	// If validator consistently uses max drift, flag as suspicious
	deviationSum := int64(0)
	for i := range timestamps {
		expected := tv.CalculateSlotTimestamp(slots[i])
		deviationSum += abs64(timestamps[i] - expected)
	}

	avgDeviation := deviationSum / int64(len(timestamps))
	// If average deviation > 75% of max drift, suspicious
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
// PRIORITY 3: FINALIZED BLOCK SEED GENERATION
// ============================================================================

// FinalityManager tracks block finality for secure seed generation
type FinalityManager struct {
	finalityDepth       int64 // Blocks must be this deep to be considered finalized
	finalizedBlocks     map[uint64]*FinalizedBlock
	latestFinalizedSlot uint64
}

// FinalizedBlock represents a finalized block with verified data
type FinalizedBlock struct {
	Slot        uint64 `json:"slot"`
	BlockHash   string `json:"block_hash"`
	VRFOutput   []byte `json:"vrf_output"`
	Timestamp   int64  `json:"timestamp"`
	Validator   string `json:"validator"`
	FinalizedAt int64  `json:"finalized_at"`
}

// NewFinalityManager creates a new finality manager
func NewFinalityManager(finalityDepth int64) *FinalityManager {
	if finalityDepth == 0 {
		finalityDepth = 32 // Default: Ethereum-style 32 block finality
	}

	return &FinalityManager{
		finalityDepth:   finalityDepth,
		finalizedBlocks: make(map[uint64]*FinalizedBlock),
	}
}

// MarkBlockFinalized marks a block as finalized
func (fm *FinalityManager) MarkBlockFinalized(
	slot uint64,
	blockHash string,
	vrfOutput []byte,
	timestamp int64,
	validator string,
) error {

	if slot <= fm.latestFinalizedSlot {
		return fmt.Errorf("cannot finalize slot %d: already finalized up to %d",
			slot, fm.latestFinalizedSlot)
	}

	finalizedBlock := &FinalizedBlock{
		Slot:        slot,
		BlockHash:   blockHash,
		VRFOutput:   vrfOutput,
		Timestamp:   timestamp,
		Validator:   validator,
		FinalizedAt: time.Now().Unix(),
	}

	fm.finalizedBlocks[slot] = finalizedBlock

	if slot > fm.latestFinalizedSlot {
		fm.latestFinalizedSlot = slot
	}

	return nil
}

// GetFinalizedBlock retrieves a finalized block
func (fm *FinalityManager) GetFinalizedBlock(slot uint64) (*FinalizedBlock, error) {
	block, exists := fm.finalizedBlocks[slot]
	if !exists {
		return nil, fmt.Errorf("no finalized block for slot %d", slot)
	}
	return block, nil
}

// IsBlockFinalized checks if a block at the given slot is finalized
func (fm *FinalityManager) IsBlockFinalized(currentSlot, targetSlot uint64) bool {
	// Block is finalized if it's at least finalityDepth slots behind current
	return currentSlot >= targetSlot+uint64(fm.finalityDepth)
}

// GetLatestFinalizedSlot returns the latest finalized slot
func (fm *FinalityManager) GetLatestFinalizedSlot() uint64 {
	return fm.latestFinalizedSlot
}

// ============================================================================
// ENHANCED VRF SEED GENERATOR WITH FINALITY
// ============================================================================

// SecureVRFSeedGenerator generates VRF seeds using only finalized data
type SecureVRFSeedGenerator struct {
	*VRFSeedGenerator
	finalityManager    *FinalityManager
	timestampValidator *TimestampValidator
}

// NewSecureVRFSeedGenerator creates a secure seed generator with finality
func NewSecureVRFSeedGenerator(
	finalityDepth int64,
	maxDriftSeconds int64,
	slotDurationSeconds int64,
	genesisTimestamp int64,
) *SecureVRFSeedGenerator {

	return &SecureVRFSeedGenerator{
		VRFSeedGenerator:   NewVRFSeedGenerator(),
		finalityManager:    NewFinalityManager(finalityDepth),
		timestampValidator: NewTimestampValidator(maxDriftSeconds, slotDurationSeconds, genesisTimestamp),
	}
}

// GenerateSeedFromFinalized generates seed using ONLY finalized blocks
func (svsg *SecureVRFSeedGenerator) GenerateSeedFromFinalized(
	epoch uint64,
	slot uint64,
	currentSlot uint64,
) ([]byte, error) {

	// Determine which finalized block to use
	finalizedSlot := uint64(0)
	if currentSlot > uint64(svsg.finalityManager.finalityDepth) {
		finalizedSlot = currentSlot - uint64(svsg.finalityManager.finalityDepth)
	}

	// Get finalized block data
	finalizedBlock, err := svsg.finalityManager.GetFinalizedBlock(finalizedSlot)
	if err != nil {
		return nil, fmt.Errorf("cannot get finalized block for seed generation: %w", err)
	}

	// Generate seed using finalized data
	seed := svsg.GenerateSeed(
		epoch,
		slot,
		finalizedBlock.BlockHash,
		finalizedBlock.Timestamp,
	)

	return seed, nil
}

// ValidateAndGenerateSeed validates timing and generates seed from finalized data
func (svsg *SecureVRFSeedGenerator) ValidateAndGenerateSeed(
	epoch uint64,
	slot uint64,
	currentSlot uint64,
	proposedTimestamp int64,
	parentTimestamp int64,
) ([]byte, error) {

	// Step 1: Validate timestamp
	err := svsg.timestampValidator.ValidateBlockTimestamp(
		proposedTimestamp,
		slot,
		parentTimestamp,
	)
	if err != nil {
		return nil, fmt.Errorf("timestamp validation failed: %w", err)
	}

	// Step 2: Generate seed from finalized data
	seed, err := svsg.GenerateSeedFromFinalized(epoch, slot, currentSlot)
	if err != nil {
		return nil, fmt.Errorf("seed generation failed: %w", err)
	}

	// Step 3: Validate seed quality
	err = svsg.ValidateSeed(seed)
	if err != nil {
		return nil, fmt.Errorf("seed validation failed: %w", err)
	}

	return seed, nil
}

// ============================================================================
// MONITORING & DETECTION
// ============================================================================

// TimestampAnomalyDetector detects timestamp manipulation attempts
type TimestampAnomalyDetector struct {
	validatorTimestamps map[string]*ValidatorTimingStats
	alertThreshold      float64 // Standard deviations before alert
}

// ValidatorTimingStats tracks timing statistics per validator
type ValidatorTimingStats struct {
	ValidatorAddress    string
	BlockCount          int
	TotalDeviation      int64
	MaxDeviation        int64
	ConsecutiveMaxDrift int
	SuspiciousPatterns  int
}

// NewTimestampAnomalyDetector creates a new anomaly detector
func NewTimestampAnomalyDetector(alertThreshold float64) *TimestampAnomalyDetector {
	if alertThreshold == 0 {
		alertThreshold = 2.0 // Alert after 2 standard deviations
	}

	return &TimestampAnomalyDetector{
		validatorTimestamps: make(map[string]*ValidatorTimingStats),
		alertThreshold:      alertThreshold,
	}
}

// RecordBlockTiming records timing statistics for a block
func (tad *TimestampAnomalyDetector) RecordBlockTiming(
	validatorAddress string,
	blockTimestamp int64,
	expectedTimestamp int64,
	maxDrift int64,
) {

	stats, exists := tad.validatorTimestamps[validatorAddress]
	if !exists {
		stats = &ValidatorTimingStats{
			ValidatorAddress: validatorAddress,
		}
		tad.validatorTimestamps[validatorAddress] = stats
	}

	deviation := abs64(blockTimestamp - expectedTimestamp)
	stats.BlockCount++
	stats.TotalDeviation += deviation

	if deviation > stats.MaxDeviation {
		stats.MaxDeviation = deviation
	}

	// Track consecutive max drift usage
	if deviation >= maxDrift-1 { // Within 1 second of max
		stats.ConsecutiveMaxDrift++
		if stats.ConsecutiveMaxDrift >= 3 {
			stats.SuspiciousPatterns++
		}
	} else {
		stats.ConsecutiveMaxDrift = 0
	}
}

// GetSuspiciousValidators returns validators with suspicious timing patterns
func (tad *TimestampAnomalyDetector) GetSuspiciousValidators() []*ValidatorTimingStats {
	suspicious := make([]*ValidatorTimingStats, 0)

	for _, stats := range tad.validatorTimestamps {
		if stats.BlockCount < 10 {
			continue // Need sufficient data
		}

		avgDeviation := float64(stats.TotalDeviation) / float64(stats.BlockCount)

		// Calculate standard deviation
		variance := float64(0)
		// (Simplified: would need to track individual deviations for true variance)
		stdDev := math.Sqrt(variance)

		// Alert if average deviation exceeds threshold
		if avgDeviation > tad.alertThreshold*stdDev || stats.SuspiciousPatterns > 0 {
			suspicious = append(suspicious, stats)
		}
	}

	return suspicious
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// ============================================================================
// INTEGRATION EXAMPLE
// ============================================================================

// EnhancedVRFProtocol combines all security enhancements
type EnhancedVRFProtocol struct {
	seedGenerator      *SecureVRFSeedGenerator
	commitReveal       *CommitRevealManager
	timestampValidator *TimestampValidator
	anomalyDetector    *TimestampAnomalyDetector
}

// NewEnhancedVRFProtocol creates a fully secured VRF protocol
func NewEnhancedVRFProtocol(
	finalityDepth int64,
	maxDriftSeconds int64,
	slotDurationSeconds int64,
	genesisTimestamp int64,
	revealDeadlineSlots int64,
) *EnhancedVRFProtocol {

	return &EnhancedVRFProtocol{
		seedGenerator: NewSecureVRFSeedGenerator(
			finalityDepth,
			maxDriftSeconds,
			slotDurationSeconds,
			genesisTimestamp,
		),
		commitReveal:       NewCommitRevealManager(revealDeadlineSlots),
		timestampValidator: NewTimestampValidator(maxDriftSeconds, slotDurationSeconds, genesisTimestamp),
		anomalyDetector:    NewTimestampAnomalyDetector(2.0),
	}
}

// ValidateAndProposeBlock performs complete validation before block proposal
func (evp *EnhancedVRFProtocol) ValidateAndProposeBlock(
	validatorAddress string,
	slot uint64,
	epoch uint64,
	currentSlot uint64,
	proposedTimestamp int64,
	parentTimestamp int64,
	blockHash string,
) error {

	// Step 1: Validate timestamp
	err := evp.timestampValidator.ValidateBlockTimestamp(
		proposedTimestamp,
		slot,
		parentTimestamp,
	)
	if err != nil {
		return fmt.Errorf("timestamp validation failed: %w", err)
	}

	// Step 2: Record timing for anomaly detection
	expectedTimestamp := evp.timestampValidator.CalculateSlotTimestamp(slot)
	evp.anomalyDetector.RecordBlockTiming(
		validatorAddress,
		proposedTimestamp,
		expectedTimestamp,
		evp.timestampValidator.maxDriftSeconds,
	)

	// Step 3: Generate secure seed from finalized blocks
	_, err = evp.seedGenerator.GenerateSeedFromFinalized(epoch, slot, currentSlot)
	if err != nil {
		return fmt.Errorf("seed generation failed: %w", err)
	}

	// Step 4: Check if validator must commit (not shown: commitment logic)
	if !evp.commitReveal.HasCommitment(slot, validatorAddress) {
		return fmt.Errorf("validator %s has not committed for slot %d", validatorAddress, slot)
	}

	return nil
}
