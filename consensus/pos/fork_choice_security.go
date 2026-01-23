// consensus/pos/fork_choice_security.go
// Security enhancements for fork choice with configurable limits and auto-finalization

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
	DefaultReorgDepthLimit    = 100  // Maximum blocks that can be reorganized (increased from 50)
	DefaultFinalizationEpochs = 2    // Epochs before auto-finalization
	DefaultMinStakeForReorg   = 0.66 // 66% stake required for reorg
	DefaultCheckpointInterval = 10   // Create checkpoint every 10 epochs
)

var (
	ErrReorgTooDeep           = errors.New("reorg exceeds maximum depth")
	ErrReorgCrossesFinality   = errors.New("reorg crosses finalized checkpoint")
	ErrReorgInsufficientStake = errors.New("insufficient stake for reorg")
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

	// SECURITY CHECK 3: ✅ NEW - Minimum stake requirement for reorg
	minStakeFraction := DefaultMinStakeForReorg
	if fc.config != nil && fc.config.Consensus.MinStakeForReorg > 0 {
		minStakeFraction = fc.config.Consensus.MinStakeForReorg
	}

	newStake := coremath.ParseBigInt(newChainStake)
	total := coremath.ParseBigInt(totalStake)

	if total.Sign() > 0 {
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
	}

	return nil
}

// =============================================================================
// ✅ NEW: AUTOMATIC FINALIZATION
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

	// ✅ NEW: Special case - 0 means no auto-finalization
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
				safeHashPrefix(fc.finalizedCheckpoint.BlockHash), // ✅ Safe slicing
				epochsSinceJustified)
		}
	}
}

// =============================================================================
// PERIODIC CHECKPOINT CREATION (Keep existing)
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
		epoch, safeHashPrefix(blockHash)) // ✅ Safe slicing
}

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
func (fc *ForkChoice) CalculateChainStakeFromHashes(blockHashes []string) string {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	totalStake := big.NewInt(0)
	seenValidators := make(map[string]bool)

	for _, blockHash := range blockHashes {
		// Get attestations for this block
		attestations := fc.attestationsByBlock[blockHash]
		for _, attestation := range attestations {
			validator := attestation.ValidatorAddress

			// Only count each validator once
			if !seenValidators[validator] {
				validatorInfo, err := fc.worldState.GetValidator(validator)
				if err == nil && validatorInfo != nil && validatorInfo.Active {
					stake := coremath.ParseBigInt(validatorInfo.Stake)
					totalStake = coremath.Add(totalStake, stake)
					seenValidators[validator] = true
				}
			}
		}
	}

	return totalStake.String()
}

// GetSecurityMetrics returns current security status for monitoring
func (fc *ForkChoice) GetSecurityMetrics() map[string]interface{} {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	metrics := make(map[string]interface{})

	// Finalization status
	if fc.finalizedCheckpoint != nil {
		metrics["finalized_epoch"] = fc.finalizedCheckpoint.Epoch
		metrics["finalized_block"] = fc.finalizedCheckpoint.BlockHash[:8]
	} else {
		metrics["finalized_epoch"] = "none"
	}

	// Justification status
	if fc.justifiedCheckpoint != nil {
		metrics["justified_epoch"] = fc.justifiedCheckpoint.Epoch
		metrics["justified_block"] = fc.justifiedCheckpoint.BlockHash[:8]
	} else {
		metrics["justified_epoch"] = "none"
	}

	// Security config
	maxDepth := DefaultReorgDepthLimit
	if fc.config != nil && fc.config.Consensus.MaxReorgDepth > 0 {
		maxDepth = fc.config.Consensus.MaxReorgDepth
	}
	metrics["max_reorg_depth"] = maxDepth

	minStake := DefaultMinStakeForReorg
	if fc.config != nil && fc.config.Consensus.MinStakeForReorg > 0 {
		minStake = fc.config.Consensus.MinStakeForReorg
	}
	metrics["min_stake_for_reorg"] = fmt.Sprintf("%.1f%%", minStake*100)

	return metrics
}
