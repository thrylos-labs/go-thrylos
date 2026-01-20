// consensus/pos/fork_choice_security.go
// Security enhancements for existing fork choice - ADD THESE METHODS to your ForkChoice

package pos

import (
	"errors"
	"fmt"
	"math/big"

	coremath "github.com/thrylos-labs/go-thrylos/core/math"
)

// Security constants
const (
	REORG_DEPTH_LIMIT         = 50  // Maximum blocks that can be reorganized
	REORG_STAKE_MULTIPLIER    = 1.1 // New chain needs 10% more stake (1.1x)
	CHECKPOINT_EPOCH_INTERVAL = 10  // Create checkpoint every 10 epochs
)

var (
	ErrReorgTooDeep           = errors.New("reorg exceeds maximum depth")
	ErrReorgCrossesFinality   = errors.New("reorg crosses finalized checkpoint")
	ErrReorgInsufficientStake = errors.New("insufficient stake for reorg")
)

// =============================================================================
// ADD THESE 3 METHODS TO YOUR EXISTING ForkChoice STRUCT
// =============================================================================

// ValidateReorganization checks if a chain reorganization is safe
// Call this BEFORE accepting any reorg
func (fc *ForkChoice) ValidateReorganization(
	forkPointEpoch uint64, // Epoch where chains diverge
	reorgDepth int, // Number of blocks being replaced
	newChainStake string, // Total stake backing new chain (as string)
	oldChainStake string, // Total stake backing old chain (as string)
) error {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	// SECURITY CHECK 1: Depth limit
	if reorgDepth > REORG_DEPTH_LIMIT {
		return fmt.Errorf("%w: depth=%d, max=%d",
			ErrReorgTooDeep, reorgDepth, REORG_DEPTH_LIMIT)
	}

	// SECURITY CHECK 2: Don't cross finality checkpoint
	if fc.finalizedCheckpoint != nil {
		if forkPointEpoch <= fc.finalizedCheckpoint.Epoch {
			return fmt.Errorf("%w: fork_epoch=%d, finalized_epoch=%d",
				ErrReorgCrossesFinality, forkPointEpoch, fc.finalizedCheckpoint.Epoch)
		}
	}

	// SECURITY CHECK 3: Stake advantage required
	newStakeBig := coremath.ParseBigInt(newChainStake)
	oldStakeBig := coremath.ParseBigInt(oldChainStake)

	// Calculate required threshold: old_stake * 1.1
	threshold := new(big.Int).Mul(oldStakeBig, big.NewInt(11))
	threshold.Div(threshold, big.NewInt(10))

	if newStakeBig.Cmp(threshold) < 0 {
		return fmt.Errorf("%w: new=%s, old=%s, need=%s",
			ErrReorgInsufficientStake,
			newChainStake,
			oldChainStake,
			threshold.String())
	}

	return nil
}

// EnsurePeriodicCheckpoint creates checkpoints at regular epoch intervals
// Call this in ProcessAttestation when processing epoch boundaries
func (fc *ForkChoice) EnsurePeriodicCheckpoint(epoch uint64, blockHash string, attestingStake string, totalStake string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	// Only create checkpoints at the right intervals
	if epoch%CHECKPOINT_EPOCH_INTERVAL != 0 {
		return
	}

	// Update finalized checkpoint using your existing Checkpoint type
	fc.finalizedCheckpoint = &Checkpoint{
		Epoch:          epoch,
		BlockHash:      blockHash,
		Timestamp:      0, // Will be set by caller if needed
		AttestingStake: attestingStake,
		TotalStake:     totalStake,
	}

	fmt.Printf("✅ Finality checkpoint created at epoch %d (block %s)\n", epoch, blockHash[:8])
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
// HELPER FUNCTION - Use this to calculate chain stake
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
