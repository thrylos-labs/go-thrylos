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
	"bytes"
	"encoding/binary"
	"fmt"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/math"
	"github.com/thrylos-labs/go-thrylos/crypto/hash"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

const (
	recentProposerWindow = 16
	maxConsecutiveShift  = 8
)

// ProposerHistoryReader provides canonical block history for deterministic anti-concentration.
type ProposerHistoryReader interface {
	GetBlock(index int64) (*core.Block, error)
	GetHeight() int64
}

// StakeDomainReader exposes ownership-domain assignments for domain-aware scheduling.
type StakeDomainReader interface {
	GetValidatorStakeDomain(validatorAddr string) (string, error)
}

// Set represents a set of validators with selection capabilities
type Set struct {
	validators    map[string]*core.Validator
	activeList    []*core.Validator
	totalStake    string
	maxValidators int
	mu            sync.RWMutex
	historyReader ProposerHistoryReader

	// Selection history for analytics only.
	selectionHistory map[string]*SelectionStats

	// Performance adjustments
	performanceMultipliers map[string]float64

	scheduleCache *epochScheduleCache
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
	TotalStake    string            `json:"total_stake"`
	SelectionSeed []byte            `json:"selection_seed"`
	CreatedAt     int64             `json:"created_at"`
	Purpose       string            `json:"purpose"`
}

type quotaAllocation struct {
	address   string
	quota     int
	remainder *big.Int
}

type domainScheduleState struct {
	order []string
	index int
}

type epochScheduleCache struct {
	epoch uint64
	key   []byte
	value []string
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

// SetHistoryReader configures a canonical block-history reader for deterministic proposer penalties.
func (vs *Set) SetHistoryReader(reader ProposerHistoryReader) {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	vs.historyReader = reader
	vs.clearScheduleCacheUnsafe()
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

	vs.clearScheduleCacheUnsafe()

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
	vs.clearScheduleCacheUnsafe()

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
	vs.clearScheduleCacheUnsafe()

	return nil
}

// SelectProposer selects a validator to propose a block using stake-weighted randomness
func (vs *Set) SelectProposer(seed []byte, slot uint64) (*SelectionResult, error) {
	// ✅ Use write lock since we modify selectionHistory
	vs.mu.Lock()
	defer vs.mu.Unlock()

	if len(vs.activeList) == 0 {
		return nil, fmt.Errorf("no active validators")
	}

	// Quick string check first for efficiency
	if vs.totalStake == "0" || vs.totalStake == "" {
		return nil, fmt.Errorf("total stake is zero")
	}

	// Parse to BigInt to be sure (and for later conversion)
	totalStakeBig, _ := new(big.Int).SetString(vs.totalStake, 10)
	if totalStakeBig == nil || totalStakeBig.Sign() == 0 {
		return nil, fmt.Errorf("total stake is zero or invalid")
	}

	// Create deterministic randomness from seed and slot
	slotBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(slotBytes, slot)

	combined := append(seed, slotBytes...)
	hashBytes := hash.Keccak256(combined)

	// Convert hash to big integer for modular arithmetic
	hashInt := new(big.Int).SetBytes(hashBytes)

	// Apply anti-concentration adjustment using canonical block history.
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
			// ✅ Now safe to update selection statistics with write lock
			vs.updateSelectionStatsUnsafe(validator.Address)

			return &SelectionResult{
				SelectedValidator: validator,
				SelectionSeed:     hashBytes, // ✅ Changed from hash[:]
				TotalStake:        totalStakeBig.Int64(),
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
		SelectionSeed:     hashBytes, // ✅ Changed from hash[:]
		TotalStake:        totalStakeBig.Int64(),
		SelectionWeight:   adjustedStakes[lastValidator.Address],
		Timestamp:         time.Now().Unix(),
	}, nil
}

// BuildEpochSchedule allocates stake-proportional proposer slots for an epoch and then
// deterministically shuffles them while respecting a best-effort cooldown window.
func (vs *Set) BuildEpochSchedule(
	candidates []*core.Validator,
	epoch uint64,
	slotsPerEpoch int,
	cooldownWindow int,
) ([]string, error) {
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no validator candidates")
	}
	if slotsPerEpoch <= 0 {
		return nil, fmt.Errorf("slots per epoch must be positive")
	}
	if cooldownWindow < 0 {
		cooldownWindow = 0
	}

	vs.mu.RLock()
	snapshot := make([]*core.Validator, 0, len(candidates))
	totalStakeBig := big.NewInt(0)
	for _, validator := range candidates {
		if validator == nil {
			continue
		}
		stakeBig := math.ParseBigInt(validator.Stake)
		if stakeBig.Sign() <= 0 {
			continue
		}
		snapshot = append(snapshot, validator)
		totalStakeBig.Add(totalStakeBig, stakeBig)
	}

	if len(snapshot) == 0 || totalStakeBig.Sign() <= 0 {
		vs.mu.RUnlock()
		return nil, fmt.Errorf("total stake is zero")
	}

	seed := vs.buildEpochSeed(epoch, slotsPerEpoch)
	domainGroups, err := vs.groupValidatorsByDomain(snapshot)
	if err != nil {
		vs.mu.RUnlock()
		return nil, err
	}
	cacheKey := buildScheduleCacheKey(epoch, slotsPerEpoch, cooldownWindow, seed, domainGroups)
	if cached := vs.copyCachedScheduleUnsafe(epoch, cacheKey); cached != nil {
		vs.mu.RUnlock()
		return cached, nil
	}

	domainAllocations := allocateStakeQuotas(domainGroups, slotsPerEpoch)
	domainSchedule := expandQuotaSchedule(domainAllocations, slotsPerEpoch)
	vs.shuffleSchedule(domainSchedule, seed)
	vs.applyCooldown(domainSchedule, cooldownWindow)

	domainStates := make(map[string]*domainScheduleState, len(domainAllocations))
	for _, allocation := range domainAllocations {
		if allocation.quota == 0 {
			continue
		}
		members := domainGroups[allocation.address]
		memberAllocations := allocateValidatorQuotas(members, allocation.quota)
		memberSchedule := expandQuotaSchedule(memberAllocations, allocation.quota)
		domainSeedInput := make([]byte, 0, len(seed)+len(allocation.address))
		domainSeedInput = append(domainSeedInput, seed...)
		domainSeedInput = append(domainSeedInput, allocation.address...)
		domainSeed := hash.Keccak256(domainSeedInput)
		vs.shuffleSchedule(memberSchedule, domainSeed)
		domainStates[allocation.address] = &domainScheduleState{order: memberSchedule}
	}

	schedule := make([]string, 0, slotsPerEpoch)
	for _, domainID := range domainSchedule {
		state := domainStates[domainID]
		if state == nil || len(state.order) == 0 {
			continue
		}
		if state.index >= len(state.order) {
			state.index = 0
		}
		schedule = append(schedule, state.order[state.index])
		state.index++
	}

	if len(schedule) != slotsPerEpoch {
		vs.mu.RUnlock()
		return nil, fmt.Errorf("failed to build complete epoch schedule")
	}
	vs.mu.RUnlock()

	vs.mu.Lock()
	vs.scheduleCache = &epochScheduleCache{
		epoch: epoch,
		key:   append([]byte(nil), cacheKey...),
		value: append([]string(nil), schedule...),
	}
	vs.mu.Unlock()

	return schedule, nil
}

// shuffleValidators creates a deterministic shuffle of validators using Fisher-Yates
func (vs *Set) shuffleValidators(validators []*core.Validator, seed []byte) []*core.Validator {
	// Create a copy to avoid modifying the original
	shuffled := make([]*core.Validator, len(validators))
	copy(shuffled, validators)

	// Use seed to create deterministic randomness with Keccak256
	hashBytes := hash.Keccak256(seed)

	// Fisher-Yates shuffle with deterministic randomness
	for i := len(shuffled) - 1; i > 0; i-- {
		// Generate deterministic random number for this position
		positionSeed := append(hashBytes, byte(i))
		positionHash := hash.Keccak256(positionSeed)
		randomInt := new(big.Int).SetBytes(positionHash[:8])

		j := new(big.Int).Mod(randomInt, big.NewInt(int64(i+1))).Int64()

		// Swap elements
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}

	return shuffled
}

func (vs *Set) buildEpochSeed(epoch uint64, slotsPerEpoch int) []byte {
	if vs.historyReader == nil {
		return GenerateSeedFromInputs(nil, nil, epoch)
	}

	height := vs.historyReader.GetHeight()
	if height < 0 {
		return GenerateSeedFromInputs(nil, nil, epoch)
	}

	if epoch == 0 {
		return GenerateSeedFromInputs(nil, nil, epoch)
	}

	blockHashes := make([][]byte, 0, slotsPerEpoch)
	vrfOutputs := make([][]byte, 0, slotsPerEpoch)
	targetEpoch := epoch - 1

	for i := height; i >= 0; i-- {
		block, err := vs.historyReader.GetBlock(i)
		if err != nil || block == nil {
			continue
		}
		if block.Header == nil {
			continue
		}
		if block.Header.Epoch > targetEpoch {
			continue
		}
		if block.Header.Epoch < targetEpoch {
			break
		}
		if block.Hash != "" {
			blockHashes = append(blockHashes, []byte(block.Hash))
		}
		if len(block.Header.VrfOutput) > 0 {
			vrfOutputs = append(vrfOutputs, append([]byte(nil), block.Header.VrfOutput...))
		}
		if len(blockHashes) >= slotsPerEpoch {
			break
		}
	}

	return GenerateSeedFromInputs(blockHashes, vrfOutputs, epoch)
}

func (vs *Set) shuffleSchedule(schedule []string, seed []byte) {
	if len(schedule) < 2 {
		return
	}

	baseSeed := hash.Keccak256(seed)
	for i := len(schedule) - 1; i > 0; i-- {
		indexBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(indexBytes, uint64(i))
		positionHash := hash.Keccak256(append(baseSeed, indexBytes...))
		randomInt := new(big.Int).SetBytes(positionHash[:8])
		j := new(big.Int).Mod(randomInt, big.NewInt(int64(i+1))).Int64()
		schedule[i], schedule[j] = schedule[j], schedule[i]
	}
}

func (vs *Set) applyCooldown(schedule []string, cooldownWindow int) {
	if cooldownWindow <= 0 || len(schedule) < 2 {
		return
	}

	for i := 1; i < len(schedule); i++ {
		if !violatesCooldown(schedule, i, cooldownWindow) {
			continue
		}

		for j := i + 1; j < len(schedule); j++ {
			candidate := schedule[j]
			if hasRecentMatch(schedule, i, cooldownWindow, candidate) {
				continue
			}

			original := schedule[i]
			schedule[i] = candidate
			schedule[j] = original
			if !violatesCooldown(schedule, i, cooldownWindow) {
				break
			}
			schedule[j] = candidate
			schedule[i] = original
		}
	}
}

func violatesCooldown(schedule []string, index, cooldownWindow int) bool {
	return hasRecentMatch(schedule, index, cooldownWindow, schedule[index])
}

func hasRecentMatch(schedule []string, index, cooldownWindow int, candidate string) bool {
	start := index - cooldownWindow
	if start < 0 {
		start = 0
	}
	for i := start; i < index; i++ {
		if schedule[i] == candidate {
			return true
		}
	}
	return false
}

func (vs *Set) groupValidatorsByDomain(candidates []*core.Validator) (map[string][]*core.Validator, error) {
	domainReader, _ := vs.historyReader.(StakeDomainReader)
	domainGroups := make(map[string][]*core.Validator)

	for _, validator := range candidates {
		domainID := validator.Address
		if domainReader != nil {
			assignedDomain, err := domainReader.GetValidatorStakeDomain(validator.Address)
			if err != nil {
				return nil, fmt.Errorf("failed to load stake domain for %s: %w", validator.Address, err)
			}
			if assignedDomain != "" {
				domainID = assignedDomain
			}
		}
		domainGroups[domainID] = append(domainGroups[domainID], validator)
	}

	return domainGroups, nil
}

func allocateStakeQuotas(domainGroups map[string][]*core.Validator, slots int) []quotaAllocation {
	weights := make(map[string]*big.Int, len(domainGroups))
	for domainID, validators := range domainGroups {
		total := big.NewInt(0)
		for _, validator := range validators {
			total.Add(total, math.ParseBigInt(validator.Stake))
		}
		weights[domainID] = total
	}

	return allocateQuotas(weights, slots)
}

func allocateValidatorQuotas(validators []*core.Validator, slots int) []quotaAllocation {
	weights := make(map[string]*big.Int, len(validators))
	for _, validator := range validators {
		weights[validator.Address] = math.ParseBigInt(validator.Stake)
	}
	return allocateQuotas(weights, slots)
}

func allocateQuotas(weights map[string]*big.Int, slots int) []quotaAllocation {
	totalWeight := big.NewInt(0)
	for _, weight := range weights {
		if weight != nil && weight.Sign() > 0 {
			totalWeight.Add(totalWeight, weight)
		}
	}

	allocations := make([]quotaAllocation, 0, len(weights))
	if slots <= 0 || totalWeight.Sign() <= 0 {
		return allocations
	}

	allocatedSlots := 0
	for address, weight := range weights {
		safeWeight := weight
		if safeWeight == nil || safeWeight.Sign() < 0 {
			safeWeight = big.NewInt(0)
		}

		numerator := new(big.Int).Mul(safeWeight, big.NewInt(int64(slots)))
		quotaBig := new(big.Int).Div(numerator, totalWeight)
		remainderBig := new(big.Int).Mod(numerator, totalWeight)
		quota := int(quotaBig.Int64())
		allocatedSlots += quota

		allocations = append(allocations, quotaAllocation{
			address:   address,
			quota:     quota,
			remainder: remainderBig,
		})
	}

	remaining := slots - allocatedSlots
	sort.Slice(allocations, func(i, j int) bool {
		if cmp := allocations[i].remainder.Cmp(allocations[j].remainder); cmp != 0 {
			return cmp > 0
		}
		return allocations[i].address < allocations[j].address
	})
	for i := 0; i < remaining && i < len(allocations); i++ {
		allocations[i].quota++
	}
	sort.Slice(allocations, func(i, j int) bool {
		return allocations[i].address < allocations[j].address
	})

	return allocations
}

func expandQuotaSchedule(allocations []quotaAllocation, expectedLength int) []string {
	schedule := make([]string, 0, expectedLength)
	for _, allocation := range allocations {
		for i := 0; i < allocation.quota; i++ {
			schedule = append(schedule, allocation.address)
		}
	}
	return schedule
}

// calculateAdjustedStakes calculates stake weights with anti-concentration adjustments
func (vs *Set) calculateAdjustedStakes() map[string]int64 {
	adjustedStakes := make(map[string]int64)
	recentSelections, consecutiveSelections := vs.getRecentProposerPenalties()
	expectedRecentShare := 0.0
	if len(vs.activeList) > 0 {
		expectedRecentShare = float64(recentProposerWindow) / float64(len(vs.activeList))
	}

	for _, validator := range vs.activeList {
		baseStakeBig := math.ParseBigInt(validator.Stake)

		// Convert to Float for multiplier math
		adjustedStakeFloat := new(big.Float).SetInt(baseStakeBig)

		// Apply performance multiplier
		performanceMultiplier := vs.performanceMultipliers[validator.Address]
		adjustedStakeFloat.Mul(adjustedStakeFloat, big.NewFloat(performanceMultiplier))

		if expectedRecentShare > 0 {
			if recentCount := recentSelections[validator.Address]; float64(recentCount) > expectedRecentShare {
				overSelectionPenalty := expectedRecentShare / float64(recentCount)
				adjustedStakeFloat.Mul(adjustedStakeFloat, big.NewFloat(overSelectionPenalty))
			}
		}

		if consecutiveCount := consecutiveSelections[validator.Address]; consecutiveCount > 0 {
			shift := consecutiveCount
			if shift > maxConsecutiveShift {
				shift = maxConsecutiveShift
			}
			consecutivePenalty := 1.0 / float64(uint64(1)<<shift)
			adjustedStakeFloat.Mul(adjustedStakeFloat, big.NewFloat(consecutivePenalty))
		}

		// Convert back to int64 for the weight map
		// Note: This caps at int64 max. If stakes are huge, this logic might need
		// to change to return map[string]string (BigInt) instead.
		adjustedStake, _ := adjustedStakeFloat.Int64()

		// Ensure minimum stake weight
		if adjustedStake < 1 {
			adjustedStake = 1
		}

		adjustedStakes[validator.Address] = adjustedStake
	}

	return adjustedStakes
}

func (vs *Set) getRecentProposerPenalties() (map[string]int, map[string]int) {
	recentSelections := make(map[string]int)
	consecutiveSelections := make(map[string]int)

	if vs.historyReader == nil {
		return recentSelections, consecutiveSelections
	}

	height := vs.historyReader.GetHeight()
	if height < 0 {
		return recentSelections, consecutiveSelections
	}

	window := recentProposerWindow
	if int(height+1) < window {
		window = int(height + 1)
	}

	lastProposer := ""
	for i := 0; i < window; i++ {
		block, err := vs.historyReader.GetBlock(height - int64(i))
		if err != nil || block == nil || block.Header == nil || block.Header.Validator == "" {
			continue
		}

		proposer := block.Header.Validator
		recentSelections[proposer]++

		if i == 0 {
			lastProposer = proposer
		}
		if proposer == lastProposer {
			consecutiveSelections[proposer]++
		} else if lastProposer != "" {
			break
		}
	}

	return recentSelections, consecutiveSelections
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
// ✅ Fix: Return string
func (vs *Set) GetTotalStake() string {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.totalStake
}

// updateActiveListUnsafe updates the active validator list (caller must hold lock)
func (vs *Set) updateActiveListUnsafe() {
	vs.activeList = vs.activeList[:0] // Reset slice

	// ✅ Fix: totalStake calculation using BigInt
	totalStakeBig := big.NewInt(0)

	// Collect active validators
	for _, validator := range vs.validators {
		if validator.Active && !vs.isJailed(validator) {
			vs.activeList = append(vs.activeList, validator)

			s := math.ParseBigInt(validator.Stake)
			if s != nil {
				totalStakeBig.Add(totalStakeBig, s)
			}
		}
	}
	vs.totalStake = totalStakeBig.String()

	// Sort by stake (descending) for consistent ordering
	// ✅ Fix: Compare BigInts
	sort.Slice(vs.activeList, func(i, j int) bool {
		s1 := math.ParseBigInt(vs.activeList[i].Stake)
		s2 := math.ParseBigInt(vs.activeList[j].Stake)
		if s1 == nil {
			s1 = big.NewInt(0)
		}
		if s2 == nil {
			s2 = big.NewInt(0)
		}
		return s1.Cmp(s2) > 0
	})

	// Limit to max validators if necessary
	if len(vs.activeList) > vs.maxValidators {
		// Keep top validators by stake
		vs.activeList = vs.activeList[:vs.maxValidators]

		// Recalculate total stake for the limited list
		totalStakeBig = big.NewInt(0)
		for _, validator := range vs.activeList {
			s := math.ParseBigInt(validator.Stake)
			if s != nil {
				totalStakeBig.Add(totalStakeBig, s)
			}
		}
		vs.totalStake = totalStakeBig.String()
	}
}

// isJailed checks if a validator is currently jailed
func (vs *Set) isJailed(validator *core.Validator) bool {
	return validator.JailUntil > time.Now().Unix()
}

// RotateValidatorSet performs validator set rotation based on performance and stake
// RotateValidatorSet performs validator set rotation based on performance and stake
func (vs *Set) RotateValidatorSet(config *config.Config) ([]*core.Validator, []*core.Validator, error) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	if len(vs.activeList) == 0 {
		return nil, nil, fmt.Errorf("no active validators")
	}

	var toRemove []*core.Validator
	var toAdd []*core.Validator

	// ✅ FIX: Parse config.Staking.MinValidatorStake from string
	minValStakeBig, _ := new(big.Int).SetString(config.Staking.MinValidatorStake, 10)
	if minValStakeBig == nil {
		minValStakeBig = big.NewInt(0) // Default to 0 if parsing fails
	}

	// Find validators to remove
	for _, validator := range vs.activeList {
		stats := vs.selectionHistory[validator.Address]
		performanceMultiplier := vs.performanceMultipliers[validator.Address]

		// Remove if performance is too low
		if performanceMultiplier < 0.5 {
			toRemove = append(toRemove, validator)
			continue
		}

		// Remove if stake has fallen below minimum
		valStakeBig := math.ParseBigInt(validator.Stake)

		// ✅ Now both are BigInts, so Cmp works correctly
		if valStakeBig.Cmp(minValStakeBig) < 0 {
			toRemove = append(toRemove, validator)
			continue
		}

		// Remove if selection rate is anomalously high
		if stats != nil && stats.SelectionRate > 10.0 {
			toRemove = append(toRemove, validator)
		}
	}

	// Find validators to add from inactive set
	for _, validator := range vs.validators {
		if !validator.Active && !vs.isJailed(validator) {
			valStakeBig := math.ParseBigInt(validator.Stake)

			// Check if meets requirements
			if valStakeBig.Cmp(minValStakeBig) >= 0 {
				performanceMultiplier := vs.performanceMultipliers[validator.Address]
				if performanceMultiplier >= 0.8 { // Good performance threshold
					toAdd = append(toAdd, validator)
				}
			}
		}
	}

	// Sort candidates to add by stake (descending)
	sort.Slice(toAdd, func(i, j int) bool {
		s1 := math.ParseBigInt(toAdd[i].Stake)
		s2 := math.ParseBigInt(toAdd[j].Stake)
		return s1.Cmp(s2) > 0
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

	totalStakeBig, _ := new(big.Int).SetString(vs.totalStake, 10)
	// Convert total stake to float for ratio calculation (precision loss acceptable for metrics)
	totalStakeFloat, _ := new(big.Float).SetInt(totalStakeBig).Float64()

	for _, validator := range vs.activeList {
		stats := vs.selectionHistory[validator.Address]
		if stats != nil {
			actualRate := float64(stats.TimesSelected) / float64(totalSelections)

			// ✅ Fix: Convert Stake to float for metric calculation
			valStakeBig := math.ParseBigInt(validator.Stake)
			valStakeFloat, _ := new(big.Float).SetInt(valStakeBig).Float64()

			expectedRate := 0.0
			if totalStakeFloat > 0 {
				expectedRate = valStakeFloat / totalStakeFloat
			}

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

		totalStakeBig, _ := new(big.Int).SetString(vs.totalStake, 10)
		if totalStakeBig == nil {
			totalStakeBig = big.NewInt(1)
		} // avoid div by zero

		avgStakeBig := new(big.Int).Div(totalStakeBig, big.NewInt(int64(len(vs.activeList))))

		stats["min_stake"] = minStake
		stats["max_stake"] = maxStake
		stats["avg_stake"] = avgStakeBig.String()

		// Stake concentration (float for display)
		maxStakeBig := math.ParseBigInt(maxStake)
		maxF, _ := new(big.Float).SetInt(maxStakeBig).Float64()
		totalF, _ := new(big.Float).SetInt(totalStakeBig).Float64()

		concentration := 0.0
		if totalF > 0 {
			concentration = maxF / totalF
		}
		stats["stake_concentration"] = concentration

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
		if !validator.Active && math.ParseBigInt(validator.Stake).Sign() == 0 && validator.UpdatedAt < cutoff {
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

// GenerateSeedFromBlocks creates randomness seed from recent block hashes
// At the end of the file, add:

// GenerateSeedFromBlocks creates randomness seed from recent block hashes
func GenerateSeedFromInputs(blockHashes [][]byte, vrfOutputs [][]byte, slot uint64) []byte {
	if len(blockHashes) == 0 && len(vrfOutputs) == 0 {
		slotBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(slotBytes, slot)
		return hash.Keccak256(slotBytes)
	}

	combined := make([]byte, 0)
	for _, hashBytes := range blockHashes {
		combined = append(combined, hashBytes...)
	}
	for _, vrfOutput := range vrfOutputs {
		combined = append(combined, vrfOutput...)
	}

	slotBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(slotBytes, slot)
	combined = append(combined, slotBytes...)

	return hash.Keccak256(combined)
}

func buildScheduleCacheKey(
	epoch uint64,
	slotsPerEpoch int,
	cooldownWindow int,
	seed []byte,
	domainGroups map[string][]*core.Validator,
) []byte {
	combined := make([]byte, 0)
	combined = append(combined, seed...)

	epochBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(epochBytes, epoch)
	combined = append(combined, epochBytes...)

	paramBytes := make([]byte, 8)
	binary.BigEndian.PutUint32(paramBytes[:4], uint32(slotsPerEpoch))
	binary.BigEndian.PutUint32(paramBytes[4:], uint32(cooldownWindow))
	combined = append(combined, paramBytes...)

	domainIDs := make([]string, 0, len(domainGroups))
	for domainID := range domainGroups {
		domainIDs = append(domainIDs, domainID)
	}
	sort.Strings(domainIDs)

	for _, domainID := range domainIDs {
		combined = append(combined, []byte(domainID)...)
		members := append([]*core.Validator(nil), domainGroups[domainID]...)
		sort.Slice(members, func(i, j int) bool {
			return members[i].Address < members[j].Address
		})
		for _, member := range members {
			combined = append(combined, []byte(member.Address)...)
			combined = append(combined, member.Stake...)
		}
	}

	return hash.Keccak256(combined)
}

func (vs *Set) copyCachedScheduleUnsafe(epoch uint64, cacheKey []byte) []string {
	if vs.scheduleCache == nil {
		return nil
	}
	if vs.scheduleCache.epoch != epoch {
		return nil
	}
	if !bytes.Equal(vs.scheduleCache.key, cacheKey) {
		return nil
	}
	return append([]string(nil), vs.scheduleCache.value...)
}

func (vs *Set) clearScheduleCacheUnsafe() {
	vs.scheduleCache = nil
}

// Clear removes all validators and resets the set state
func (vs *Set) Clear() {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	// Initialize as a map, not a slice
	vs.validators = make(map[string]*core.Validator)

	// Reset the active list slice
	vs.activeList = make([]*core.Validator, 0)

	// Reset total stake string
	vs.totalStake = "0"

	// Optional: If you want a truly fresh start, reset history and multipliers too
	vs.selectionHistory = make(map[string]*SelectionStats)
	vs.performanceMultipliers = make(map[string]float64)
	vs.clearScheduleCacheUnsafe()
}
