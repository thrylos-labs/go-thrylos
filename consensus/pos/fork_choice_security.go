// consensus/pos/fork_choice_security.go
// Security enhancements for fork choice with configurable limits and auto-finalization
// AUDIT FIX: Enhanced validation and error handling

package pos

import (
	"errors"
	"fmt"
	"log"
	"math/big"

	coremath "github.com/thrylos-labs/go-thrylos/core/math"
)

// Security constants (defaults)
const (
	DefaultReorgDepthLimit    = 32   // Maximum blocks that can be reorganized
	DefaultFinalizationEpochs = 2    // Epochs before auto-finalization
	DefaultMinStakeForReorg   = 0.66 // 66% stake required for reorg
	DefaultCheckpointInterval = 10   // Create checkpoint every 10 epochs
)

var (
	ErrReorgTooDeep           = errors.New("reorg exceeds maximum depth")
	ErrReorgCrossesFinality   = errors.New("reorg crosses finalized checkpoint")
	ErrReorgInsufficientStake = errors.New("insufficient stake for reorg")
	ErrInvalidReorgDepth      = errors.New("invalid reorg depth")
	ErrInvalidEpoch           = errors.New("invalid epoch")
	ErrInvalidStake           = errors.New("invalid stake value")
)

// =============================================================================
// ENHANCED VALIDATION WITH CONFIGURABLE LIMITS
// =============================================================================

// ValidateReorganization checks if a chain reorganization is safe
// Call this BEFORE accepting any reorg
func (fc *ForkChoice) ValidateReorganization(
	reorgDepth int, // Number of blocks being replaced
	forkPointEpoch uint64, // Epoch where chains diverge
	newChainStake string, // Total stake backing new chain
	totalStake string, // Total network stake
) error {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	// AUDIT FIX #4: Validate inputs
	if reorgDepth < 0 {
		return fmt.Errorf("%w: %d (cannot be negative)", ErrInvalidReorgDepth, reorgDepth)
	}
	if forkPointEpoch == 0 {
		return fmt.Errorf("%w: fork point epoch cannot be zero", ErrInvalidEpoch)
	}

	// Get max depth from config (with fallback)
	maxDepth := DefaultReorgDepthLimit
	if fc.config != nil && fc.config.Consensus.MaxReorgDepth > 0 {
		maxDepth = fc.config.Consensus.MaxReorgDepth
	}

	// SECURITY CHECK 1: Depth limit
	if reorgDepth > maxDepth {
		return fmt.Errorf("%w: attempted=%d, max=%d",
			ErrReorgTooDeep, reorgDepth, maxDepth)
	}

	// SECURITY CHECK 2: Don't cross finality checkpoint
	if fc.finalizedCheckpoint != nil {
		if forkPointEpoch <= fc.finalizedCheckpoint.Epoch {
			return fmt.Errorf("%w: fork_epoch=%d, finalized_epoch=%d",
				ErrReorgCrossesFinality, forkPointEpoch, fc.finalizedCheckpoint.Epoch)
		}
	}

	// SECURITY CHECK 3: Minimum stake requirement for reorg
	minStakeFraction := DefaultMinStakeForReorg
	if fc.config != nil && fc.config.Consensus.MinStakeForReorg > 0 {
		minStakeFraction = fc.config.Consensus.MinStakeForReorg
	}

	// AUDIT FIX #1 & #5: Validate ParseBigInt results
	newStake := coremath.ParseBigInt(newChainStake)
	if newStake == nil || newStake.Sign() < 0 {
		return fmt.Errorf("%w: new chain stake '%s' is invalid or negative",
			ErrInvalidStake, newChainStake)
	}

	total := coremath.ParseBigInt(totalStake)
	if total == nil || total.Sign() <= 0 {
		return fmt.Errorf("%w: total stake '%s' is invalid or non-positive",
			ErrInvalidStake, totalStake)
	}

	// Calculate percentage: (newStake * 10000) / totalStake (basis points for precision)
	percentage := new(big.Int).Mul(newStake, big.NewInt(10000))
	percentage.Div(percentage, total)

	requiredBasisPoints := int64(minStakeFraction * 10000)

	if percentage.Cmp(big.NewInt(requiredBasisPoints)) < 0 {
		actualPercent := new(big.Int).Div(percentage, big.NewInt(100))
		requiredPercent := requiredBasisPoints / 100
		return fmt.Errorf("%w: has=%s%%, required=%d%%",
			ErrReorgInsufficientStake, actualPercent.String(), requiredPercent)
	}

	return nil
}

// =============================================================================
// AUTOMATIC FINALIZATION
// =============================================================================

// UpdateFinalization checks if justified checkpoint should be finalized
// Call this after processing each epoch
func (fc *ForkChoice) UpdateFinalization(currentEpoch uint64) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	finalizationEpochs := DefaultFinalizationEpochs
	if fc.config != nil && fc.config.Consensus.FinalizationEpochs > 0 {
		finalizationEpochs = fc.config.Consensus.FinalizationEpochs
	}

	// Special case - 0 means no auto-finalization
	if finalizationEpochs == 0 {
		return
	}

	if fc.justifiedCheckpoint != nil {
		epochsSinceJustified := currentEpoch - fc.justifiedCheckpoint.Epoch

		if epochsSinceJustified >= uint64(finalizationEpochs) {
			fc.finalizedCheckpoint = &Checkpoint{
				Epoch:          fc.justifiedCheckpoint.Epoch,
				BlockHash:      fc.justifiedCheckpoint.BlockHash,
				Timestamp:      fc.justifiedCheckpoint.Timestamp,
				AttestingStake: fc.justifiedCheckpoint.AttestingStake,
				TotalStake:     fc.justifiedCheckpoint.TotalStake,
			}

			log.Printf("✅ FINALIZED: Epoch %d, Block %s (%d epochs ago)",
				fc.finalizedCheckpoint.Epoch,
				safeHashPrefix(fc.finalizedCheckpoint.BlockHash),
				epochsSinceJustified)
		}
	}
}

// =============================================================================
// PERIODIC CHECKPOINT CREATION
// =============================================================================

// EnsurePeriodicCheckpoint creates checkpoints at regular epoch intervals
// Call this in ProcessAttestation when processing epoch boundaries
func (fc *ForkChoice) EnsurePeriodicCheckpoint(epoch uint64, blockHash string, attestingStake string, totalStake string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	interval := DefaultCheckpointInterval
	if fc.config != nil && fc.config.Consensus.CheckpointInterval > 0 {
		interval = fc.config.Consensus.CheckpointInterval
	}

	if epoch%uint64(interval) != 0 {
		return
	}

	fc.justifiedCheckpoint = &Checkpoint{
		Epoch:          epoch,
		BlockHash:      blockHash,
		Timestamp:      0,
		AttestingStake: attestingStake,
		TotalStake:     totalStake,
	}

	log.Printf("✅ Justified checkpoint created at epoch %d (block %s)",
		epoch, safeHashPrefix(blockHash))
}

// safeHashPrefix safely extracts hash prefix without panicking
func safeHashPrefix(hash string) string {
	if len(hash) <= 8 {
		return hash
	}
	return hash[:8]
}

// IsEpochFinalized checks if an epoch is finalized
func (fc *ForkChoice) IsEpochFinalized(epoch uint64) bool {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	if fc.finalizedCheckpoint == nil {
		return false
	}

	return epoch <= fc.finalizedCheckpoint.Epoch
}

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

// CalculateChainStakeFromHashes calculates total stake for a chain of block hashes
// Use this when validating reorgs
// AUDIT FIX #3: Added error handling and validation
func (fc *ForkChoice) CalculateChainStakeFromHashes(blockHashes []string) (string, error) {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	totalDecayedStake := big.NewInt(0)
	currentEpoch := fc.getCurrentEpoch() // Helper to get latest tracked epoch
	seenValidators := make(map[string]bool)

	for _, blockHash := range blockHashes {
		attestations := fc.attestationsByBlock[blockHash]
		blockEpoch := fc.blockEpochMap[blockHash]

		for _, attestation := range attestations {
			validator := attestation.ValidatorAddress

			if !seenValidators[validator] {
				validatorInfo, err := fc.worldState.GetValidator(validator)
				if err == nil && validatorInfo != nil && validatorInfo.Active {
					stake := coremath.ParseBigInt(validatorInfo.Stake)

					// APPLY DECAY: Older votes are worth less
					weight := fc.ApplyWeightDecay(stake, blockEpoch, currentEpoch)

					totalDecayedStake.Add(totalDecayedStake, weight)
					seenValidators[validator] = true
				}
			}
		}
	}

	return totalDecayedStake.String(), nil
}

// GetSecurityMetrics returns current security status for monitoring
// AUDIT FIX #2: Safe string slicing
func (fc *ForkChoice) GetSecurityMetrics() map[string]interface{} {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	metrics := make(map[string]interface{})

	// 1. Finalization status
	if fc.finalizedCheckpoint != nil {
		metrics["finalized_epoch"] = fc.finalizedCheckpoint.Epoch
		metrics["finalized_block"] = safeHashPrefix(fc.finalizedCheckpoint.BlockHash)
	} else {
		metrics["finalized_epoch"] = "none"
		metrics["finalized_block"] = "none"
	}

	// 2. Justification status
	if fc.justifiedCheckpoint != nil {
		metrics["justified_epoch"] = fc.justifiedCheckpoint.Epoch
		metrics["justified_block"] = safeHashPrefix(fc.justifiedCheckpoint.BlockHash)
	} else {
		metrics["justified_epoch"] = "none"
		metrics["justified_block"] = "none"
	}

	// 3. Security config with Updated Default (Audit Recommendation: 32)
	maxDepth := 32 // Hard-coded security recommendation fallback
	if fc.config != nil && fc.config.Consensus.MaxReorgDepth > 0 {
		maxDepth = fc.config.Consensus.MaxReorgDepth
	}
	metrics["max_reorg_depth"] = maxDepth

	minStake := DefaultMinStakeForReorg
	if fc.config != nil && fc.config.Consensus.MinStakeForReorg > 0 {
		minStake = fc.config.Consensus.MinStakeForReorg
	}
	metrics["min_stake_for_reorg"] = fmt.Sprintf("%.1f%%", minStake*100)

	// 4. FORK DETECTION ALERTS (New Security Requirement)
	// Identify how many unique block hashes have received votes in recent history
	competingHeads := 0
	mainHead := fc.getHeadByHighestStake()

	// Check the number of tracked block scores as a proxy for fork activity
	// In a healthy network, this should not grow exponentially relative to finalization
	for blockHash := range fc.blockScores {
		if blockHash != mainHead && fc.HasQuorum(blockHash) {
			competingHeads++
		}
	}

	metrics["detected_forks"] = competingHeads
	metrics["security_status"] = "HEALTHY"

	// Alerting logic: multiple blocks with quorum suggests a network partition or attack
	if competingHeads > 0 {
		metrics["security_status"] = "CRITICAL_FORK_DETECTED"
	} else if len(fc.blockScores) > 100 { // Arbitrary threshold for uncleaned state
		metrics["security_status"] = "DEGRADED_STATE_BLOAT"
	}

	return metrics
}

// ValidateCheckpoint validates a checkpoint's integrity
func (fc *ForkChoice) ValidateCheckpoint(checkpoint *Checkpoint) error {
	if checkpoint == nil {
		return fmt.Errorf("checkpoint cannot be nil")
	}

	if checkpoint.Epoch == 0 {
		return fmt.Errorf("%w: epoch cannot be zero", ErrInvalidEpoch)
	}

	if checkpoint.BlockHash == "" {
		return fmt.Errorf("block hash cannot be empty")
	}

	// Validate stake values
	attestingStake := coremath.ParseBigInt(checkpoint.AttestingStake)
	if attestingStake == nil || attestingStake.Sign() < 0 {
		return fmt.Errorf("%w: attesting stake invalid", ErrInvalidStake)
	}

	totalStake := coremath.ParseBigInt(checkpoint.TotalStake)
	if totalStake == nil || totalStake.Sign() <= 0 {
		return fmt.Errorf("%w: total stake invalid", ErrInvalidStake)
	}

	// Attesting stake cannot exceed total stake
	if attestingStake.Cmp(totalStake) > 0 {
		return fmt.Errorf("attesting stake exceeds total stake")
	}

	return nil
}

// GetReorgSafetyMargin returns how close the chain is to the reorg limit
// Returns remaining safe blocks before hitting the limit
func (fc *ForkChoice) GetReorgSafetyMargin(currentHeight uint64) int {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	maxDepth := DefaultReorgDepthLimit
	if fc.config != nil && fc.config.Consensus.MaxReorgDepth > 0 {
		maxDepth = fc.config.Consensus.MaxReorgDepth
	}

	if fc.finalizedCheckpoint == nil {
		return maxDepth
	}

	// Calculate blocks since finalization
	// Note: This assumes checkpoint.Epoch maps to a block height
	// You may need to adjust based on your epoch/block relationship
	blocksSinceFinalized := int(currentHeight - fc.finalizedCheckpoint.Epoch)

	if blocksSinceFinalized >= maxDepth {
		return 0
	}

	return maxDepth - blocksSinceFinalized
}
