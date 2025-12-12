// consensus/pos/fork_choice.go
// Fork choice rule with stake-weighted quorum checking and memory management

package pos

import (
	"fmt"
	"math/big"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	coremath "github.com/thrylos-labs/go-thrylos/core/math" // Safe BigInt math
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/types"
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

// WorldStateReader interface for dependency injection
type WorldStateReader interface {
	GetValidator(address string) (*core.Validator, error)
	GetActiveValidators() []*core.Validator
	GetBlockByHash(hash string) (*core.Block, error)
}

// NewForkChoice creates a new fork choice instance with memory management
func NewForkChoice(config *config.Config, worldState WorldStateReader, slashingManager *SlashingManager) *ForkChoice {
	return NewForkChoiceWithConfig(config, worldState, slashingManager, DefaultForkChoiceConfig())
}

// NewForkChoiceWithConfig creates a fork choice with custom configuration
func NewForkChoiceWithConfig(config *config.Config, worldState WorldStateReader, slashingManager *SlashingManager, fcConfig *ForkChoiceConfig) *ForkChoice {
	fc := &ForkChoice{
		config:          config,
		fcConfig:        fcConfig,
		worldState:      worldState,
		slashingManager: slashingManager,
		// Initialize maps with string values for BigInts
		blockScores:           make(map[string]string),
		attestationsByBlock:   make(map[string][]*types.Attestation),
		validatorAttestations: make(map[string]map[string]bool),
		epochAttestations:     make(map[uint64]map[string]string),
		blockEpochMap:         make(map[string]uint64),
		totalActiveStake:      "0",
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
func (fc *ForkChoice) ProcessAttestation(attestation *types.Attestation) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	blockHash := attestation.BlockHash
	validatorAddr := attestation.ValidatorAddress
	epoch := attestation.Epoch

	blockHashShort := blockHash
	if len(blockHashShort) > 8 {
		blockHashShort = blockHashShort[:8]
	}

	// 1. Check for slashing violations
	if fc.slashingManager != nil {
		if err := fc.slashingManager.ProcessAttestation(attestation); err != nil {
			fmt.Printf("⚠️ Slashing violation detected for validator %s: %v\n", validatorAddr, err)
			return
		}

		if !fc.slashingManager.IsValidatorActive(validatorAddr) {
			fmt.Printf("⚠️ Inactive/jailed validator %s attempted to attest\n", validatorAddr)
			return
		}
	}

	// Check if this validator has already attested to this block
	if fc.validatorAttestations[blockHash] == nil {
		fc.validatorAttestations[blockHash] = make(map[string]bool)
	}

	if fc.validatorAttestations[blockHash][validatorAddr] {
		return // Duplicate
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

	validatorStake := validator.Stake // String (BigInt)

	// Mark validator as having attested
	fc.validatorAttestations[blockHash][validatorAddr] = true

	// Check limit
	if fc.attestationsByBlock[blockHash] == nil {
		fc.attestationsByBlock[blockHash] = make([]*types.Attestation, 0, fc.fcConfig.MaxAttestationsPerBlock)
	}

	if len(fc.attestationsByBlock[blockHash]) < fc.fcConfig.MaxAttestationsPerBlock {
		fc.attestationsByBlock[blockHash] = append(fc.attestationsByBlock[blockHash], attestation)
		fc.metrics.TotalAttestations++
	} else {
		fmt.Printf("⚠️ Block %s reached max attestations (%d), counting stake but not storing\n",
			blockHashShort, fc.fcConfig.MaxAttestationsPerBlock)
	}

	// Update block score using BigInt math
	currentScore := fc.blockScores[blockHash]
	newScore := addBigIntStrings(currentScore, validatorStake)
	fc.blockScores[blockHash] = newScore

	// Track epoch mapping
	fc.blockEpochMap[blockHash] = epoch

	// Update epoch attestations
	if fc.epochAttestations[epoch] == nil {
		fc.epochAttestations[epoch] = make(map[string]string)
		fc.metrics.TotalEpochs++
	}
	currentEpochScore := fc.epochAttestations[epoch][blockHash]
	newEpochScore := addBigIntStrings(currentEpochScore, validatorStake)
	fc.epochAttestations[epoch][blockHash] = newEpochScore

	// Check Quorum (2/3 of total stake)
	totalStakeStr := fc.getTotalActiveStake()
	totalStakeBig := coremath.ParseBigInt(totalStakeStr)

	attestingStakeBig := coremath.ParseBigInt(newScore)

	// Threshold = (Total * 2) / 3 + 1
	two := big.NewInt(2)
	three := big.NewInt(3)
	thresholdBig := new(big.Int).Mul(totalStakeBig, two)
	thresholdBig.Div(thresholdBig, three)
	thresholdBig.Add(thresholdBig, big.NewInt(1))

	if attestingStakeBig.Cmp(thresholdBig) >= 0 {
		// Log percentage
		percentage := calculatePercentage(attestingStakeBig, totalStakeBig)

		fmt.Printf("✅ Block %s reached 2/3 quorum: %s/%s stake (%.1f%%)\n",
			blockHashShort, attestingStakeBig.String(), totalStakeBig.String(), percentage)

		// Check justification (passing strings)
		fc.checkJustification(epoch, blockHash, attestingStakeBig.String(), totalStakeBig.String())
	}
}

// getTotalActiveStake calculates the total stake of all active validators
func (fc *ForkChoice) getTotalActiveStake() string {
	// Check cache
	if time.Since(fc.totalActiveStakeTime) < fc.fcConfig.StakeCacheTTL && fc.totalActiveStake != "0" {
		return fc.totalActiveStake
	}

	activeValidators := fc.worldState.GetActiveValidators()
	totalStake := big.NewInt(0)

	for _, validator := range activeValidators {
		if validator.Active {
			valStakeBig := coremath.ParseBigInt(validator.Stake)
			totalStake = coremath.Add(totalStake, valStakeBig)
		}
	}

	// Update cache
	fc.totalActiveStake = totalStake.String()
	fc.totalActiveStakeTime = time.Now()

	return fc.totalActiveStake
}

// updateMemoryEstimate calculates rough memory usage
func (fc *ForkChoice) updateMemoryEstimate() {
	estimate := int64(0)
	// Block scores: ~128 bytes per entry
	estimate += int64(len(fc.blockScores)) * 128
	for _, attestations := range fc.attestationsByBlock {
		estimate += int64(len(attestations)) * 200
	}
	for _, validators := range fc.validatorAttestations {
		estimate += int64(len(validators)) * 100
	}
	for _, blocks := range fc.epochAttestations {
		estimate += int64(len(blocks)) * 128
	}
	fc.metrics.MemoryEstimateBytes = estimate
}

// getCurrentEpoch returns the current epoch
func (fc *ForkChoice) getCurrentEpoch() uint64 {
	maxEpoch := uint64(0)
	for epoch := range fc.epochAttestations {
		if epoch > maxEpoch {
			maxEpoch = epoch
		}
	}
	return maxEpoch
}

// GetHead returns the current head block according to fork choice
func (fc *ForkChoice) GetHead() string {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	if len(fc.blockScores) == 0 {
		return ""
	}

	totalStakeStr := fc.getTotalActiveStake()
	totalStakeBig := coremath.ParseBigInt(totalStakeStr)

	// Threshold = (Total * 2) / 3 + 1
	two := big.NewInt(2)
	three := big.NewInt(3)
	quorumThreshold := new(big.Int).Mul(totalStakeBig, two)
	quorumThreshold.Div(quorumThreshold, three)
	quorumThreshold.Add(quorumThreshold, big.NewInt(1))

	var bestBlockWithQuorum string
	var bestScoreWithQuorum *big.Int

	// First pass: find blocks with quorum
	for blockHash, scoreStr := range fc.blockScores {
		scoreBig := coremath.ParseBigInt(scoreStr)
		if scoreBig.Cmp(quorumThreshold) >= 0 {
			if bestScoreWithQuorum == nil || scoreBig.Cmp(bestScoreWithQuorum) > 0 {
				bestScoreWithQuorum = scoreBig
				bestBlockWithQuorum = blockHash
			}
		}
	}

	if bestBlockWithQuorum != "" {
		return bestBlockWithQuorum
	}

	// Fallback: highest stake
	var bestBlock string
	var bestScore *big.Int

	for blockHash, scoreStr := range fc.blockScores {
		scoreBig := coremath.ParseBigInt(scoreStr)
		if bestScore == nil || scoreBig.Cmp(bestScore) > 0 {
			bestScore = scoreBig
			bestBlock = blockHash
		}
	}

	return bestBlock
}

// HasQuorum checks if a specific block has achieved 2/3 quorum
func (fc *ForkChoice) HasQuorum(blockHash string) bool {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	totalStakeStr := fc.getTotalActiveStake()
	totalStakeBig := coremath.ParseBigInt(totalStakeStr)

	attestingStakeStr := fc.blockScores[blockHash]
	attestingStakeBig := coremath.ParseBigInt(attestingStakeStr)

	two := big.NewInt(2)
	three := big.NewInt(3)
	threshold := new(big.Int).Mul(totalStakeBig, two)
	threshold.Div(threshold, three)
	threshold.Add(threshold, big.NewInt(1))

	return attestingStakeBig.Cmp(threshold) >= 0
}

// GetAttestingStake returns the total stake attesting to a block (as string)
func (fc *ForkChoice) GetAttestingStake(blockHash string) string {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	s, exists := fc.blockScores[blockHash]
	if !exists {
		return "0"
	}
	return s
}

// GetQuorumPercentage returns the percentage of stake attesting to a block
func (fc *ForkChoice) GetQuorumPercentage(blockHash string) float64 {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	totalStakeStr := fc.getTotalActiveStake()
	totalStakeBig := coremath.ParseBigInt(totalStakeStr)

	attestingStakeStr := fc.blockScores[blockHash]
	attestingStakeBig := coremath.ParseBigInt(attestingStakeStr)

	return calculatePercentage(attestingStakeBig, totalStakeBig)
}

// IsBlockSafeToAccept checks if a block has sufficient attestations
func (fc *ForkChoice) IsBlockSafeToAccept(blockHash string) bool {
	return fc.HasQuorum(blockHash)
}

// GetBlockScore returns the score for a block (as string)
func (fc *ForkChoice) GetBlockScore(blockHash string) string {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	s, exists := fc.blockScores[blockHash]
	if !exists {
		return "0"
	}
	return s
}

// GetAttestationsForBlock returns attestations for a specific block
func (fc *ForkChoice) GetAttestationsForBlock(blockHash string) []*types.Attestation {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	attestations := fc.attestationsByBlock[blockHash]
	if attestations == nil {
		return nil
	}
	result := make([]*types.Attestation, len(attestations))
	copy(result, attestations)
	return result
}

// GetMetrics returns current fork choice metrics
func (fc *ForkChoice) GetMetrics() *ForkChoiceMetrics {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	metrics := *fc.metrics
	metrics.TotalBlocks = int64(len(fc.blockScores))
	metrics.TotalEpochs = int64(len(fc.epochAttestations))
	attestationCount := int64(0)
	for _, attestations := range fc.attestationsByBlock {
		attestationCount += int64(len(attestations))
	}
	metrics.TotalAttestations = attestationCount
	return &metrics
}

// IsViableChain checks if a block is a valid candidate for the head of the chain.
func (fc *ForkChoice) IsViableChain(blockHash string) bool {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	if fc.finalizedCheckpoint == nil {
		return true
	}
	blockEpoch, exists := fc.blockEpochMap[blockHash]
	if !exists {
		return false
	}
	if blockEpoch < fc.finalizedCheckpoint.Epoch {
		return false
	}
	return fc.isDescendant(blockHash, fc.finalizedCheckpoint.BlockHash)
}

func (fc *ForkChoice) isDescendant(childHash, ancestorHash string) bool {
	if childHash == ancestorHash {
		return true
	}
	currentHash := childHash
	maxDepth := 1000
	for i := 0; i < maxDepth; i++ {
		block, err := fc.worldState.GetBlockByHash(currentHash)
		if err != nil || block == nil {
			return false
		}
		if block.Header.PrevHash == ancestorHash {
			return true
		}
		if block.Header.PrevHash == "" {
			return false
		}
		currentHash = block.Header.PrevHash
	}
	return false
}

// Helper: Add two numeric strings
func addBigIntStrings(a, b string) string {
	biA := coremath.ParseBigInt(a)
	biB := coremath.ParseBigInt(b)
	return new(big.Int).Add(biA, biB).String()
}
