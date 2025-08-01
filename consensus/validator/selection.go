// consensus/validator/selection.go

// Validator selection algorithms for Proof of Stake consensus
// Features:
// - Stake-weighted random selection for block proposers
// - Committee selection for attestations and votes
// - Deterministic selection using verifiable random functions (VRF)
// - Anti-concentration mechanisms to prevent validator monopolization
// - Rotation algorithms for validator set updates
// - Performance-based selection adjustments

package validator

import (
	"encoding/binary"
	"fmt"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"golang.org/x/crypto/blake2b"
)

// Set represents a set of validators with selection capabilities
type Set struct {
	validators    map[string]*core.Validator
	activeList    []*core.Validator
	totalStake    int64
	maxValidators int
	mu            sync.RWMutex

	// Selection history for anti-concentration
	selectionHistory map[string]*SelectionStats

	// Performance adjustments
	performanceMultipliers map[string]float64
}

// SelectionStats tracks validator selection statistics
type SelectionStats struct {
	ValidatorAddress      string  `json:"validator_address"`
	TimesSelected         uint64  `json:"times_selected"`
	LastSelected          int64   `json:"last_selected"`
	ConsecutiveSelections int     `json:"consecutive_selections"`
	SelectionRate         float64 `json:"selection_rate"`
	ExpectedSelections    float64 `json:"expected_selections"`
	PerformanceScore      float64 `json:"performance_score"`
}

// SelectionResult represents the result of a validator selection
type SelectionResult struct {
	SelectedValidator *core.Validator `json:"selected_validator"`
	SelectionSeed     []byte          `json:"selection_seed"`
	TotalStake        int64           `json:"total_stake"`
	SelectionWeight   int64           `json:"selection_weight"`
	Timestamp         int64           `json:"timestamp"`
}

// Committee represents a selected committee of validators
type Committee struct {
	Members       []*core.Validator `json:"members"`
	TotalStake    int64             `json:"total_stake"`
	SelectionSeed []byte            `json:"selection_seed"`
	CreatedAt     int64             `json:"created_at"`
	Purpose       string            `json:"purpose"`
}

// NewSet creates a new validator set
func NewSet(maxValidators int) *Set {
	return &Set{
		validators:             make(map[string]*core.Validator),
		activeList:             make([]*core.Validator, 0),
		maxValidators:          maxValidators,
		selectionHistory:       make(map[string]*SelectionStats),
		performanceMultipliers: make(map[string]float64),
	}
}

// AddValidator adds a validator to the set
func (vs *Set) AddValidator(validator *core.Validator) error {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	if validator == nil {
		return fmt.Errorf("validator cannot be nil")
	}

	// Add to validator map
	vs.validators[validator.Address] = validator

	// Update active list and total stake
	vs.updateActiveListUnsafe()

	// Initialize selection stats
	if _, exists := vs.selectionHistory[validator.Address]; !exists {
		vs.selectionHistory[validator.Address] = &SelectionStats{
			ValidatorAddress: validator.Address,
			PerformanceScore: 1.0, // Default performance score
		}
	}

	// Initialize performance multiplier
	if _, exists := vs.performanceMultipliers[validator.Address]; !exists {
		vs.performanceMultipliers[validator.Address] = 1.0
	}

	return nil
}

// RemoveValidator removes a validator from the set
func (vs *Set) RemoveValidator(address string) error {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	if _, exists := vs.validators[address]; !exists {
		return fmt.Errorf("validator %s not found", address)
	}

	delete(vs.validators, address)
	vs.updateActiveListUnsafe()

	return nil
}

// UpdateValidator updates a validator in the set
func (vs *Set) UpdateValidator(validator *core.Validator) error {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	if validator == nil {
		return fmt.Errorf("validator cannot be nil")
	}

	vs.validators[validator.Address] = validator
	vs.updateActiveListUnsafe()

	return nil
}

// SelectProposer selects a validator to propose a block using stake-weighted randomness
func (vs *Set) SelectProposer(seed []byte, slot uint64) (*SelectionResult, error) {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	if len(vs.activeList) == 0 {
		return nil, fmt.Errorf("no active validators")
	}

	if vs.totalStake == 0 {
		return nil, fmt.Errorf("total stake is zero")
	}

	// Create deterministic randomness from seed and slot
	slotBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(slotBytes, slot)

	combined := append(seed, slotBytes...)
	hash := blake2b.Sum256(combined)

	// Convert hash to big integer for modular arithmetic
	hashInt := new(big.Int).SetBytes(hash[:])

	// Apply anti-concentration adjustment
	adjustedStakes := vs.calculateAdjustedStakes()
	totalAdjustedStake := int64(0)
	for _, stake := range adjustedStakes {
		totalAdjustedStake += stake
	}

	if totalAdjustedStake == 0 {
		return nil, fmt.Errorf("total adjusted stake is zero")
	}

	// Select validator based on adjusted stake weight
	maxInt := big.NewInt(totalAdjustedStake)
	randomStake := new(big.Int).Mod(hashInt, maxInt).Int64()

	cumulativeStake := int64(0)
	for _, validator := range vs.activeList {
		adjustedStake := adjustedStakes[validator.Address]
		cumulativeStake += adjustedStake

		if randomStake < cumulativeStake {
			// Update selection statistics
			vs.updateSelectionStatsUnsafe(validator.Address)

			return &SelectionResult{
				SelectedValidator: validator,
				SelectionSeed:     hash[:],
				TotalStake:        vs.totalStake,
				SelectionWeight:   adjustedStake,
				Timestamp:         time.Now().Unix(),
			}, nil
		}
	}

	// Fallback to last validator (should not happen)
	lastValidator := vs.activeList[len(vs.activeList)-1]
	vs.updateSelectionStatsUnsafe(lastValidator.Address)

	return &SelectionResult{
		SelectedValidator: lastValidator,
		SelectionSeed:     hash[:],
		TotalStake:        vs.totalStake,
		SelectionWeight:   adjustedStakes[lastValidator.Address],
		Timestamp:         time.Now().Unix(),
	}, nil
}

// SelectCommittee selects a committee of validators for attestations or voting
func (vs *Set) SelectCommittee(seed []byte, committeeSize int, purpose string) (*Committee, error) {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	if len(vs.activeList) == 0 {
		return nil, fmt.Errorf("no active validators")
	}

	if committeeSize <= 0 {
		return nil, fmt.Errorf("committee size must be positive")
	}

	// Limit committee size to available validators
	if committeeSize > len(vs.activeList) {
		committeeSize = len(vs.activeList)
	}

	// Create deterministic shuffling based on seed
	shuffledValidators := vs.shuffleValidators(vs.activeList, seed)

	// Select top validators from shuffled list
	selectedValidators := make([]*core.Validator, committeeSize)
	totalCommitteeStake := int64(0)

	for i := 0; i < committeeSize; i++ {
		selectedValidators[i] = shuffledValidators[i]
		totalCommitteeStake += shuffledValidators[i].Stake
	}

	return &Committee{
		Members:       selectedValidators,
		TotalStake:    totalCommitteeStake,
		SelectionSeed: seed,
		CreatedAt:     time.Now().Unix(),
		Purpose:       purpose,
	}, nil
}

// shuffleValidators creates a deterministic shuffle of validators using Fisher-Yates
func (vs *Set) shuffleValidators(validators []*core.Validator, seed []byte) []*core.Validator {
	// Create a copy to avoid modifying the original
	shuffled := make([]*core.Validator, len(validators))
	copy(shuffled, validators)

	// Use seed to create deterministic randomness
	hash := blake2b.Sum256(seed)

	// Fisher-Yates shuffle with deterministic randomness
	for i := len(shuffled) - 1; i > 0; i-- {
		// Generate deterministic random number for this position
		positionSeed := append(hash[:], byte(i))
		positionHash := blake2b.Sum256(positionSeed)
		randomInt := new(big.Int).SetBytes(positionHash[:8])

		j := new(big.Int).Mod(randomInt, big.NewInt(int64(i+1))).Int64()

		// Swap elements
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}

	return shuffled
}

// calculateAdjustedStakes calculates stake weights with anti-concentration adjustments
func (vs *Set) calculateAdjustedStakes() map[string]int64 {
	adjustedStakes := make(map[string]int64)
	currentTime := time.Now().Unix()

	for _, validator := range vs.activeList {
		baseStake := validator.Stake

		// Apply performance multiplier
		performanceMultiplier := vs.performanceMultipliers[validator.Address]
		adjustedStake := int64(float64(baseStake) * performanceMultiplier)

		// Apply anti-concentration penalty for recently selected validators
		if stats, exists := vs.selectionHistory[validator.Address]; exists {
			// Reduce stake weight if selected recently
			timeSinceLastSelection := currentTime - stats.LastSelected

			if timeSinceLastSelection < 300 { // 5 minutes
				recentSelectionPenalty := 0.5 // 50% penalty
				adjustedStake = int64(float64(adjustedStake) * recentSelectionPenalty)
			}

			// Reduce stake weight for consecutive selections
			if stats.ConsecutiveSelections > 2 {
				consecutivePenalty := 1.0 / float64(stats.ConsecutiveSelections)
				adjustedStake = int64(float64(adjustedStake) * consecutivePenalty)
			}
		}

		// Ensure minimum stake weight
		if adjustedStake < 1 {
			adjustedStake = 1
		}

		adjustedStakes[validator.Address] = adjustedStake
	}

	return adjustedStakes
}

// updateSelectionStatsUnsafe updates selection statistics (caller must hold lock)
func (vs *Set) updateSelectionStatsUnsafe(validatorAddress string) {
	currentTime := time.Now().Unix()

	stats, exists := vs.selectionHistory[validatorAddress]
	if !exists {
		stats = &SelectionStats{
			ValidatorAddress: validatorAddress,
			PerformanceScore: 1.0,
		}
		vs.selectionHistory[validatorAddress] = stats
	}

	// Update selection count
	stats.TimesSelected++

	// Check for consecutive selections
	if currentTime-stats.LastSelected < 600 { // 10 minutes
		stats.ConsecutiveSelections++
	} else {
		stats.ConsecutiveSelections = 1
	}

	stats.LastSelected = currentTime

	// Update selection rate (selections per hour)
	if stats.TimesSelected > 1 {
		firstSelection := stats.LastSelected - int64(stats.TimesSelected-1)*600 // Rough estimate
		hoursSinceFirst := float64(currentTime-firstSelection) / 3600.0
		if hoursSinceFirst > 0 {
			stats.SelectionRate = float64(stats.TimesSelected) / hoursSinceFirst
		}
	}

	// Reset consecutive selections for other validators
	for addr, otherStats := range vs.selectionHistory {
		if addr != validatorAddress && currentTime-otherStats.LastSelected > 600 {
			otherStats.ConsecutiveSelections = 0
		}
	}
}

// UpdatePerformanceMultiplier updates the performance multiplier for a validator
func (vs *Set) UpdatePerformanceMultiplier(validatorAddress string, multiplier float64) error {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	if _, exists := vs.validators[validatorAddress]; !exists {
		return fmt.Errorf("validator %s not found", validatorAddress)
	}

	// Clamp multiplier to reasonable bounds
	if multiplier < 0.1 {
		multiplier = 0.1
	} else if multiplier > 2.0 {
		multiplier = 2.0
	}

	vs.performanceMultipliers[validatorAddress] = multiplier

	// Update performance score in selection stats
	if stats, exists := vs.selectionHistory[validatorAddress]; exists {
		stats.PerformanceScore = multiplier
	}

	return nil
}

// GetActiveValidators returns the list of active validators
func (vs *Set) GetActiveValidators() []*core.Validator {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	// Return a copy to prevent external modification
	activeList := make([]*core.Validator, len(vs.activeList))
	copy(activeList, vs.activeList)
	return activeList
}

// GetValidator returns a specific validator
func (vs *Set) GetValidator(address string) (*core.Validator, error) {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	validator, exists := vs.validators[address]
	if !exists {
		return nil, fmt.Errorf("validator %s not found", address)
	}

	return validator, nil
}

// GetSelectionStats returns selection statistics for a validator
func (vs *Set) GetSelectionStats(validatorAddress string) (*SelectionStats, error) {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	stats, exists := vs.selectionHistory[validatorAddress]
	if !exists {
		return nil, fmt.Errorf("selection stats not found for validator %s", validatorAddress)
	}

	// Return a copy
	statsCopy := *stats
	return &statsCopy, nil
}

// GetAllSelectionStats returns selection statistics for all validators
func (vs *Set) GetAllSelectionStats() map[string]*SelectionStats {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	stats := make(map[string]*SelectionStats)
	for addr, stat := range vs.selectionHistory {
		statsCopy := *stat
		stats[addr] = &statsCopy
	}
	return stats
}

// Size returns the number of validators in the set
func (vs *Set) Size() int {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return len(vs.validators)
}

// ActiveSize returns the number of active validators
func (vs *Set) ActiveSize() int {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return len(vs.activeList)
}

// GetTotalStake returns the total stake of all active validators
func (vs *Set) GetTotalStake() int64 {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.totalStake
}

// updateActiveListUnsafe updates the active validator list (caller must hold lock)
func (vs *Set) updateActiveListUnsafe() {
	vs.activeList = vs.activeList[:0] // Reset slice
	vs.totalStake = 0

	// Collect active validators
	for _, validator := range vs.validators {
		if validator.Active && !vs.isJailed(validator) {
			vs.activeList = append(vs.activeList, validator)
			vs.totalStake += validator.Stake
		}
	}

	// Sort by stake (descending) for consistent ordering
	sort.Slice(vs.activeList, func(i, j int) bool {
		return vs.activeList[i].Stake > vs.activeList[j].Stake
	})

	// Limit to max validators if necessary
	if len(vs.activeList) > vs.maxValidators {
		// Keep top validators by stake
		vs.activeList = vs.activeList[:vs.maxValidators]

		// Recalculate total stake
		vs.totalStake = 0
		for _, validator := range vs.activeList {
			vs.totalStake += validator.Stake
		}
	}
}

// isJailed checks if a validator is currently jailed
func (vs *Set) isJailed(validator *core.Validator) bool {
	return validator.JailUntil > time.Now().Unix()
}

// RotateValidatorSet performs validator set rotation based on performance and stake
func (vs *Set) RotateValidatorSet(config *config.Config) ([]*core.Validator, []*core.Validator, error) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	if len(vs.activeList) == 0 {
		return nil, nil, fmt.Errorf("no active validators")
	}

	var toRemove []*core.Validator
	var toAdd []*core.Validator

	// Find validators to remove (poor performance or below threshold)
	for _, validator := range vs.activeList {
		stats := vs.selectionHistory[validator.Address]
		performanceMultiplier := vs.performanceMultipliers[validator.Address]

		// Remove if performance is too low
		if performanceMultiplier < 0.5 {
			toRemove = append(toRemove, validator)
			continue
		}

		// Remove if stake has fallen below minimum
		if validator.Stake < config.Staking.MinValidatorStake {
			toRemove = append(toRemove, validator)
			continue
		}

		// Remove if selection rate is anomalously high (possible attack)
		if stats != nil && stats.SelectionRate > 10.0 { // More than 10 selections per hour
			toRemove = append(toRemove, validator)
		}
	}

	// Find validators to add from inactive set
	for _, validator := range vs.validators {
		if !validator.Active && !vs.isJailed(validator) {
			// Check if meets requirements
			if validator.Stake >= config.Staking.MinValidatorStake {
				performanceMultiplier := vs.performanceMultipliers[validator.Address]
				if performanceMultiplier >= 0.8 { // Good performance threshold
					toAdd = append(toAdd, validator)
				}
			}
		}
	}

	// Sort candidates to add by stake (descending)
	sort.Slice(toAdd, func(i, j int) bool {
		return toAdd[i].Stake > toAdd[j].Stake
	})

	// Limit additions to maintain max validator count
	maxToAdd := vs.maxValidators - (len(vs.activeList) - len(toRemove))
	if maxToAdd > 0 && len(toAdd) > maxToAdd {
		toAdd = toAdd[:maxToAdd]
	} else if maxToAdd <= 0 {
		toAdd = nil
	}

	return toRemove, toAdd, nil
}

// SelectRandomSubset selects a random subset of validators for various purposes
func (vs *Set) SelectRandomSubset(seed []byte, subsetSize int) ([]*core.Validator, error) {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	if len(vs.activeList) == 0 {
		return nil, fmt.Errorf("no active validators")
	}

	if subsetSize <= 0 {
		return nil, fmt.Errorf("subset size must be positive")
	}

	// Limit subset size to available validators
	if subsetSize > len(vs.activeList) {
		subsetSize = len(vs.activeList)
	}

	// Create deterministic shuffle
	shuffled := vs.shuffleValidators(vs.activeList, seed)

	// Return the first 'subsetSize' validators
	subset := make([]*core.Validator, subsetSize)
	copy(subset, shuffled[:subsetSize])

	return subset, nil
}

// ValidateSelectionFairness analyzes selection fairness and returns metrics
func (vs *Set) ValidateSelectionFairness() map[string]interface{} {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	fairnessMetrics := make(map[string]interface{})

	if len(vs.activeList) == 0 {
		return fairnessMetrics
	}

	// Calculate expected vs actual selection rates
	totalSelections := uint64(0)
	for _, stats := range vs.selectionHistory {
		totalSelections += stats.TimesSelected
	}

	if totalSelections == 0 {
		fairnessMetrics["total_selections"] = 0
		return fairnessMetrics
	}

	// Calculate variance in selection rates
	selectionRates := make([]float64, 0, len(vs.activeList))
	stakeWeights := make([]float64, 0, len(vs.activeList))

	for _, validator := range vs.activeList {
		stats := vs.selectionHistory[validator.Address]
		if stats != nil {
			actualRate := float64(stats.TimesSelected) / float64(totalSelections)
			expectedRate := float64(validator.Stake) / float64(vs.totalStake)

			selectionRates = append(selectionRates, actualRate)
			stakeWeights = append(stakeWeights, expectedRate)

			stats.ExpectedSelections = expectedRate * float64(totalSelections)
		}
	}

	// Calculate fairness coefficient (lower is more fair)
	fairnessCoeff := calculateGiniCoefficient(selectionRates)
	expectedFairnessCoeff := calculateGiniCoefficient(stakeWeights)

	fairnessMetrics["total_selections"] = totalSelections
	fairnessMetrics["active_validators"] = len(vs.activeList)
	fairnessMetrics["selection_gini_coefficient"] = fairnessCoeff
	fairnessMetrics["expected_gini_coefficient"] = expectedFairnessCoeff
	fairnessMetrics["fairness_deviation"] = fairnessCoeff - expectedFairnessCoeff

	// Find validators with anomalous selection rates
	anomalousValidators := make([]string, 0)
	for _, validator := range vs.activeList {
		stats := vs.selectionHistory[validator.Address]
		if stats != nil && stats.ExpectedSelections > 0 {
			actualVsExpected := float64(stats.TimesSelected) / stats.ExpectedSelections
			if actualVsExpected > 2.0 || actualVsExpected < 0.5 {
				anomalousValidators = append(anomalousValidators, validator.Address)
			}
		}
	}

	fairnessMetrics["anomalous_validators"] = anomalousValidators
	fairnessMetrics["anomalous_count"] = len(anomalousValidators)

	return fairnessMetrics
}

// calculateGiniCoefficient calculates the Gini coefficient for a set of values
func calculateGiniCoefficient(values []float64) float64 {
	if len(values) == 0 {
		return 0.0
	}

	// Sort values
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	// Calculate Gini coefficient
	n := float64(len(sorted))
	sum := 0.0
	for i, val := range sorted {
		sum += val * (2*float64(i+1) - n - 1)
	}

	// Calculate mean
	mean := 0.0
	for _, val := range sorted {
		mean += val
	}
	mean /= n

	if mean == 0 {
		return 0.0
	}

	return sum / (n * n * mean)
}

// ResetSelectionHistory resets selection history (useful for testing or new epochs)
func (vs *Set) ResetSelectionHistory() {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	for addr := range vs.selectionHistory {
		vs.selectionHistory[addr] = &SelectionStats{
			ValidatorAddress: addr,
			PerformanceScore: vs.performanceMultipliers[addr],
		}
	}
}

// GetTopValidatorsByStake returns the top N validators by stake
func (vs *Set) GetTopValidatorsByStake(n int) []*core.Validator {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	if n <= 0 || len(vs.activeList) == 0 {
		return []*core.Validator{}
	}

	// activeList is already sorted by stake (descending)
	if n > len(vs.activeList) {
		n = len(vs.activeList)
	}

	result := make([]*core.Validator, n)
	copy(result, vs.activeList[:n])
	return result
}

// GetValidatorRank returns the rank of a validator by stake (1-based)
func (vs *Set) GetValidatorRank(validatorAddress string) (int, error) {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	for i, validator := range vs.activeList {
		if validator.Address == validatorAddress {
			return i + 1, nil // 1-based rank
		}
	}

	return 0, fmt.Errorf("validator %s not found in active set", validatorAddress)
}

// GetSetStatistics returns overall statistics about the validator set
func (vs *Set) GetSetStatistics() map[string]interface{} {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	stats := make(map[string]interface{})

	stats["total_validators"] = len(vs.validators)
	stats["active_validators"] = len(vs.activeList)
	stats["total_stake"] = vs.totalStake
	stats["max_validators"] = vs.maxValidators

	if len(vs.activeList) > 0 {
		// Calculate stake distribution
		minStake := vs.activeList[len(vs.activeList)-1].Stake
		maxStake := vs.activeList[0].Stake
		avgStake := vs.totalStake / int64(len(vs.activeList))

		stats["min_stake"] = minStake
		stats["max_stake"] = maxStake
		stats["avg_stake"] = avgStake
		stats["stake_concentration"] = float64(maxStake) / float64(vs.totalStake)

		// Calculate validator ages
		currentTime := time.Now().Unix()
		totalAge := int64(0)
		oldestAge := int64(0)

		for _, validator := range vs.activeList {
			age := currentTime - validator.CreatedAt
			totalAge += age
			if age > oldestAge {
				oldestAge = age
			}
		}

		stats["avg_validator_age"] = totalAge / int64(len(vs.activeList))
		stats["oldest_validator_age"] = oldestAge
	}

	// Selection statistics
	totalSelections := uint64(0)
	activeWithHistory := 0
	for _, validator := range vs.activeList {
		if stats, exists := vs.selectionHistory[validator.Address]; exists {
			totalSelections += stats.TimesSelected
			activeWithHistory++
		}
	}

	stats["total_selections"] = totalSelections
	stats["validators_with_history"] = activeWithHistory

	if activeWithHistory > 0 {
		stats["avg_selections_per_validator"] = float64(totalSelections) / float64(activeWithHistory)
	}

	return stats
}

// CleanupInactiveValidators removes validators that have been inactive for too long
func (vs *Set) CleanupInactiveValidators(maxInactiveTime time.Duration) []string {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	cutoff := time.Now().Add(-maxInactiveTime).Unix()
	var removed []string

	for addr, validator := range vs.validators {
		// Remove if inactive for too long and has no stake
		if !validator.Active && validator.Stake == 0 && validator.UpdatedAt < cutoff {
			delete(vs.validators, addr)
			delete(vs.selectionHistory, addr)
			delete(vs.performanceMultipliers, addr)
			removed = append(removed, addr)
		}
	}

	// Update active list after cleanup
	vs.updateActiveListUnsafe()

	return removed
}
