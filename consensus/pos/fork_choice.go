// consensus/pos/fork_choice.go
// Fork choice rule with stake-weighted quorum checking and memory management

package pos

import (
	"fmt"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// ForkChoiceConfig contains configuration for fork choice memory management
type ForkChoiceConfig struct {
	MaxEpochsToKeep         uint64        // Keep data for this many epochs (default: 2)
	MaxBlocksPerEpoch       int           // Maximum blocks to track per epoch (default: 100)
	CleanupInterval         time.Duration // How often to run cleanup (default: 5 minutes)
	StakeCacheTTL           time.Duration // How long to cache total stake (default: 30s)
	EnableMetrics           bool          // Enable detailed metrics tracking
	MaxAttestationsPerBlock int           // Max attestations to store per block (default: 1000)
}

// DefaultForkChoiceConfig returns sensible defaults
func DefaultForkChoiceConfig() *ForkChoiceConfig {
	return &ForkChoiceConfig{
		MaxEpochsToKeep:         2,
		MaxBlocksPerEpoch:       100,
		CleanupInterval:         5 * time.Minute,
		StakeCacheTTL:           30 * time.Second,
		EnableMetrics:           true,
		MaxAttestationsPerBlock: 1000,
	}
}

// ForkChoiceMetrics tracks memory usage and performance
type ForkChoiceMetrics struct {
	TotalAttestations     int64
	TotalBlocks           int64
	TotalEpochs           int64
	LastCleanupTime       time.Time
	AttestationsRemoved   int64
	BlocksRemoved         int64
	EpochsRemoved         int64
	MemoryEstimateBytes   int64
	LastCleanupDurationMs int64
}

// This allows us to swap the real WorldState for a MockWorldState during testing.
type WorldStateReader interface {
	GetValidator(address string) (*core.Validator, error)
	GetActiveValidators() []*core.Validator
}

// NewForkChoice creates a new fork choice instance with memory management
func NewForkChoice(config *config.Config, worldState WorldStateReader) *ForkChoice {
	return NewForkChoiceWithConfig(config, worldState, DefaultForkChoiceConfig())
}

// NewForkChoiceWithConfig creates a fork choice with custom configuration
func NewForkChoiceWithConfig(config *config.Config, worldState WorldStateReader, fcConfig *ForkChoiceConfig) *ForkChoice {
	fc := &ForkChoice{
		config:                config,
		fcConfig:              fcConfig,
		worldState:            worldState,
		blockScores:           make(map[string]int64),
		attestationsByBlock:   make(map[string][]*Attestation),
		validatorAttestations: make(map[string]map[string]bool),
		epochAttestations:     make(map[uint64]map[string]int64),
		blockEpochMap:         make(map[string]uint64),
		totalActiveStake:      0,
		totalActiveStakeTime:  time.Time{},
		metrics:               &ForkChoiceMetrics{},
	}

	// Start background cleanup if configured
	if fcConfig.CleanupInterval > 0 {
		go fc.backgroundCleanup()
	}

	return fc
}

// backgroundCleanup runs periodic cleanup in the background
func (fc *ForkChoice) backgroundCleanup() {
	ticker := time.NewTicker(fc.fcConfig.CleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		fc.CleanupOldEpochs()
	}
}

// ProcessAttestation processes an attestation for fork choice with stake-weighted voting
func (fc *ForkChoice) ProcessAttestation(attestation *Attestation) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	blockHash := attestation.BlockHash
	validatorAddr := attestation.ValidatorAddress
	epoch := attestation.Epoch

	blockHashShort := blockHash
	if len(blockHashShort) > 8 {
		blockHashShort = blockHashShort[:8]
	}

	// Check if this validator has already attested to this block
	if fc.validatorAttestations[blockHash] == nil {
		fc.validatorAttestations[blockHash] = make(map[string]bool)
	}

	if fc.validatorAttestations[blockHash][validatorAddr] {
		// Validator already attested to this block, ignore duplicate
		return
	}

	// Get validator info
	validator, err := fc.worldState.GetValidator(validatorAddr)
	if err != nil || validator == nil {
		fmt.Printf("⚠️ Failed to get validator %s for attestation: %v\n", validatorAddr, err)
		return
	}

	if !validator.Active {
		fmt.Printf("⚠️ Inactive validator %s attempted to attest\n", validatorAddr)
		return
	}

	validatorStake := validator.Stake

	// Mark validator as having attested to this block
	fc.validatorAttestations[blockHash][validatorAddr] = true

	// Check if we've hit the attestation limit for this block
	if fc.attestationsByBlock[blockHash] == nil {
		fc.attestationsByBlock[blockHash] = make([]*Attestation, 0, fc.fcConfig.MaxAttestationsPerBlock)
	}

	// Only store attestation if under limit (still count stake even if we don't store)
	if len(fc.attestationsByBlock[blockHash]) < fc.fcConfig.MaxAttestationsPerBlock {
		fc.attestationsByBlock[blockHash] = append(fc.attestationsByBlock[blockHash], attestation)
		fc.metrics.TotalAttestations++
	} else {
		fmt.Printf("⚠️ Block %s reached max attestations (%d), counting stake but not storing\n",
			blockHashShort, fc.fcConfig.MaxAttestationsPerBlock)
	}

	// Update block score with stake weight (always count, even if not storing attestation)
	fc.blockScores[blockHash] += validatorStake

	// Track epoch and block mapping for cleanup
	fc.blockEpochMap[blockHash] = epoch

	// Track epoch attestations for finality
	if fc.epochAttestations[epoch] == nil {
		fc.epochAttestations[epoch] = make(map[string]int64)
		fc.metrics.TotalEpochs++
	}
	fc.epochAttestations[epoch][blockHash] += validatorStake

	// Check if this block has reached quorum (2/3 of total stake)
	totalStake := fc.getTotalActiveStake()
	attestingStake := fc.blockScores[blockHash]
	quorumThreshold := (totalStake*2)/3 + 1

	if attestingStake >= quorumThreshold {
		fmt.Printf("✅ Block %s reached 2/3 quorum: %d/%d stake (%.1f%%)\n",
			blockHashShort, attestingStake, totalStake,
			float64(attestingStake)/float64(totalStake)*100)

		// Check if this should become justified
		fc.checkJustification(epoch, blockHash, attestingStake, totalStake)
	}
}

// getTotalActiveStake calculates the total stake of all active validators
// Results are cached based on StakeCacheTTL to avoid expensive recalculation
func (fc *ForkChoice) getTotalActiveStake() int64 {
	// Check cache
	if time.Since(fc.totalActiveStakeTime) < fc.fcConfig.StakeCacheTTL && fc.totalActiveStake > 0 {
		return fc.totalActiveStake
	}

	// Recalculate total active stake
	activeValidators := fc.worldState.GetActiveValidators()
	totalStake := int64(0)

	for _, validator := range activeValidators {
		if validator.Active {
			totalStake += validator.Stake
		}
	}

	// Update cache
	fc.totalActiveStake = totalStake
	fc.totalActiveStakeTime = time.Now()

	return totalStake
}

// updateMemoryEstimate calculates rough memory usage
func (fc *ForkChoice) updateMemoryEstimate() {
	estimate := int64(0)

	// Block scores: ~100 bytes per entry (hash + int64)
	estimate += int64(len(fc.blockScores)) * 100

	// Attestations: ~200 bytes per attestation
	for _, attestations := range fc.attestationsByBlock {
		estimate += int64(len(attestations)) * 200
	}

	// Validator attestations: ~100 bytes per validator per block
	for _, validators := range fc.validatorAttestations {
		estimate += int64(len(validators)) * 100
	}

	// Epoch attestations: ~100 bytes per block per epoch
	for _, blocks := range fc.epochAttestations {
		estimate += int64(len(blocks)) * 100
	}

	fc.metrics.MemoryEstimateBytes = estimate
}

// getCurrentEpoch returns the current epoch
func (fc *ForkChoice) getCurrentEpoch() uint64 {
	// Find the highest epoch in our data
	maxEpoch := uint64(0)
	for epoch := range fc.epochAttestations {
		if epoch > maxEpoch {
			maxEpoch = epoch
		}
	}
	return maxEpoch
}

// GetHead returns the current head block according to fork choice
// Prioritizes blocks with 2/3 quorum, then uses stake-weighted scores
func (fc *ForkChoice) GetHead() string {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	if len(fc.blockScores) == 0 {
		return ""
	}

	totalStake := fc.getTotalActiveStake()
	quorumThreshold := (totalStake*2)/3 + 1

	// First pass: find blocks with quorum
	var bestBlockWithQuorum string
	var bestScoreWithQuorum int64

	for blockHash, score := range fc.blockScores {
		if score >= quorumThreshold && score > bestScoreWithQuorum {
			bestScoreWithQuorum = score
			bestBlockWithQuorum = blockHash
		}
	}

	// If we have a block with quorum, return it
	if bestBlockWithQuorum != "" {
		return bestBlockWithQuorum
	}

	// Fallback: no blocks have quorum yet, return highest stake
	var bestBlock string
	var bestScore int64

	for blockHash, score := range fc.blockScores {
		if score > bestScore {
			bestScore = score
			bestBlock = blockHash
		}
	}

	return bestBlock
}

// HasQuorum checks if a specific block has achieved 2/3 quorum
func (fc *ForkChoice) HasQuorum(blockHash string) bool {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	totalStake := fc.getTotalActiveStake()
	attestingStake := fc.blockScores[blockHash]
	quorumThreshold := (totalStake*2)/3 + 1

	return attestingStake >= quorumThreshold
}

// GetAttestingStake returns the total stake attesting to a block
func (fc *ForkChoice) GetAttestingStake(blockHash string) int64 {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	return fc.blockScores[blockHash]
}

// GetQuorumPercentage returns the percentage of stake attesting to a block
func (fc *ForkChoice) GetQuorumPercentage(blockHash string) float64 {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	totalStake := fc.getTotalActiveStake()
	if totalStake == 0 {
		return 0
	}

	attestingStake := fc.blockScores[blockHash]
	return float64(attestingStake) / float64(totalStake) * 100
}

// IsBlockSafeToAccept checks if a block has sufficient attestations to be safely accepted
func (fc *ForkChoice) IsBlockSafeToAccept(blockHash string) bool {
	return fc.HasQuorum(blockHash)
}

// GetBlockScore returns the score for a block
func (fc *ForkChoice) GetBlockScore(blockHash string) int64 {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	return fc.blockScores[blockHash]
}

// GetAttestationsForBlock returns attestations for a specific block
func (fc *ForkChoice) GetAttestationsForBlock(blockHash string) []*Attestation {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	attestations := fc.attestationsByBlock[blockHash]
	if attestations == nil {
		return []*Attestation{}
	}

	// Return copy
	result := make([]*Attestation, len(attestations))
	copy(result, attestations)
	return result
}

// GetMetrics returns current fork choice metrics
func (fc *ForkChoice) GetMetrics() *ForkChoiceMetrics {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	// Return a copy to avoid race conditions
	metrics := *fc.metrics

	// Update real-time counts
	metrics.TotalBlocks = int64(len(fc.blockScores))
	metrics.TotalEpochs = int64(len(fc.epochAttestations))

	attestationCount := int64(0)
	for _, attestations := range fc.attestationsByBlock {
		attestationCount += int64(len(attestations))
	}
	metrics.TotalAttestations = attestationCount

	return &metrics
}

// IsBlockFinalized checks if a specific block is finalized
// Note: GetJustifiedCheckpoint, GetFinalizedCheckpoint, and IsBlockFinalized
// are implemented in finality.go
