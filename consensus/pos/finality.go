// consensus/pos/finality.go
// Casper FFG finality implementation with justification and finalization

package pos

import (
	"fmt"
	"math/big"
	"time"

	coremath "github.com/thrylos-labs/go-thrylos/core/math" // Safe BigInt math
)

// checkJustification checks if a block should become justified based on attestations
// A block becomes justified when it receives 2/3+ of total stake in attestations
func (fc *ForkChoice) checkJustification(epoch uint64, blockHash string, attestingStake, totalStake string) {
	// Parse BigInts
	attestingBig := coremath.ParseBigInt(attestingStake)
	totalBig := coremath.ParseBigInt(totalStake)

	// Calculate Quorum Threshold: (Total * 2) / 3 + 1
	two := big.NewInt(2)
	three := big.NewInt(3)
	quorumThreshold := new(big.Int).Mul(totalBig, two)
	quorumThreshold.Div(quorumThreshold, three)
	quorumThreshold.Add(quorumThreshold, big.NewInt(1))

	// Must have at least 2/3 stake attesting
	if attestingBig.Cmp(quorumThreshold) < 0 {
		return
	}

	// Check if we already have a justified checkpoint for this epoch
	if fc.justifiedCheckpoint != nil && fc.justifiedCheckpoint.Epoch >= epoch {
		return
	}

	// Update justified checkpoint
	fc.justifiedCheckpoint = &Checkpoint{
		Epoch:          epoch,
		BlockHash:      blockHash,
		Timestamp:      time.Now().Unix(),
		AttestingStake: attestingStake,
		TotalStake:     totalStake,
	}

	// Safe truncation for logging
	blockHashShort := blockHash
	if len(blockHashShort) > 8 {
		blockHashShort = blockHashShort[:8]
	}

	// Calculate percentage for logging
	percentage := calculatePercentage(attestingBig, totalBig)

	fmt.Printf("🎯 Block %s JUSTIFIED at epoch %d with %s/%s stake (%.1f%%)\n",
		blockHashShort, epoch, attestingStake, totalStake, percentage)

	// Check for finalization
	fc.checkFinalization()
}

// checkFinalization checks if we can finalize based on Casper FFG rules
// A checkpoint is finalized when:
// 1. It is justified
// 2. The next epoch's checkpoint is also justified
// 3. Both have 2/3+ stake attestations
func (fc *ForkChoice) checkFinalization() {
	if fc.justifiedCheckpoint == nil {
		return
	}

	currentJustifiedEpoch := fc.justifiedCheckpoint.Epoch

	// Casper FFG: An epoch becomes finalized when the next epoch is justified
	// Check if we have a justified checkpoint from 2 epochs ago
	if currentJustifiedEpoch < 2 {
		return
	}

	// Finalize epoch that is 2 behind the current justified epoch
	epochToFinalize := currentJustifiedEpoch - 2

	// Only finalize if we don't already have a more recent finalized checkpoint
	if fc.finalizedCheckpoint != nil && fc.finalizedCheckpoint.Epoch >= epochToFinalize {
		return
	}

	// Look for the block that was justified at epochToFinalize
	// We need to find it in our epoch attestations
	if attestations, exists := fc.epochAttestations[epochToFinalize]; exists && len(attestations) > 0 {
		// Find the block with the most stake in that epoch
		var bestBlock string
		var bestStakeBig *big.Int

		for blockHash, stakeStr := range attestations {
			stakeBig := coremath.ParseBigInt(stakeStr)
			if bestStakeBig == nil || stakeBig.Cmp(bestStakeBig) > 0 {
				bestStakeBig = stakeBig
				bestBlock = blockHash
			}
		}

		if bestBlock != "" {
			totalStakeStr := fc.getTotalActiveStake()
			totalStakeBig := coremath.ParseBigInt(totalStakeStr)

			fc.finalizedCheckpoint = &Checkpoint{
				Epoch:          epochToFinalize,
				BlockHash:      bestBlock,
				Timestamp:      time.Now().Unix(),
				AttestingStake: bestStakeBig.String(),
				TotalStake:     totalStakeStr,
			}

			// Safe truncation for logging
			finalizedHashShort := bestBlock
			if len(finalizedHashShort) > 8 {
				finalizedHashShort = finalizedHashShort[:8]
			}

			percentage := calculatePercentage(bestStakeBig, totalStakeBig)

			fmt.Printf("🔒 Block %s FINALIZED at epoch %d with %s/%s stake (%.1f%%)\n",
				finalizedHashShort,
				epochToFinalize,
				bestStakeBig.String(),
				totalStakeStr,
				percentage)
		}
	}
}

// UpdateJustifiedCheckpoint updates the justified checkpoint
func (fc *ForkChoice) UpdateJustifiedCheckpoint(epoch uint64, blockHash string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	attestingStake := fc.blockScores[blockHash]
	totalStake := fc.getTotalActiveStake()

	fc.justifiedCheckpoint = &Checkpoint{
		Epoch:          epoch,
		BlockHash:      blockHash,
		Timestamp:      time.Now().Unix(),
		AttestingStake: attestingStake,
		TotalStake:     totalStake,
	}

	// Safe truncation for logging
	blockHashShort := blockHash
	if len(blockHashShort) > 8 {
		blockHashShort = blockHashShort[:8]
	}

	attestingBig := coremath.ParseBigInt(attestingStake)
	totalBig := coremath.ParseBigInt(totalStake)
	percentage := calculatePercentage(attestingBig, totalBig)

	fmt.Printf("🎯 Justified checkpoint updated: epoch %d, block %s, stake %s/%s (%.1f%%)\n",
		epoch, blockHashShort, attestingStake, totalStake, percentage)
}

// UpdateFinalizedCheckpoint updates the finalized checkpoint
func (fc *ForkChoice) UpdateFinalizedCheckpoint(epoch uint64, blockHash string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	attestingStake := fc.blockScores[blockHash]
	totalStake := fc.getTotalActiveStake()

	fc.finalizedCheckpoint = &Checkpoint{
		Epoch:          epoch,
		BlockHash:      blockHash,
		Timestamp:      time.Now().Unix(),
		AttestingStake: attestingStake,
		TotalStake:     totalStake,
	}

	// Safe truncation for logging
	blockHashShort := blockHash
	if len(blockHashShort) > 8 {
		blockHashShort = blockHashShort[:8]
	}

	attestingBig := coremath.ParseBigInt(attestingStake)
	totalBig := coremath.ParseBigInt(totalStake)
	percentage := calculatePercentage(attestingBig, totalBig)

	fmt.Printf("🔒 Finalized checkpoint updated: epoch %d, block %s, stake %s/%s (%.1f%%)\n",
		epoch, blockHashShort, attestingStake, totalStake, percentage)
}

// GetJustifiedCheckpoint returns the current justified checkpoint
func (fc *ForkChoice) GetJustifiedCheckpoint() *Checkpoint {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	if fc.justifiedCheckpoint == nil {
		return nil
	}

	// Return copy
	checkpoint := *fc.justifiedCheckpoint
	return &checkpoint
}

// GetFinalizedCheckpoint returns the current finalized checkpoint
func (fc *ForkChoice) GetFinalizedCheckpoint() *Checkpoint {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	if fc.finalizedCheckpoint == nil {
		return nil
	}

	// Return copy
	checkpoint := *fc.finalizedCheckpoint
	return &checkpoint
}

// IsBlockFinalized checks if a block is finalized
func (fc *ForkChoice) IsBlockFinalized(blockHash string) bool {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	if fc.finalizedCheckpoint == nil {
		return false
	}

	return fc.finalizedCheckpoint.BlockHash == blockHash
}

// GetFinalityStatus returns detailed finality status information
func (fc *ForkChoice) GetFinalityStatus() map[string]interface{} {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	status := make(map[string]interface{})

	if fc.justifiedCheckpoint != nil {
		status["justified_epoch"] = fc.justifiedCheckpoint.Epoch
		status["justified_block"] = fc.justifiedCheckpoint.BlockHash
	}

	if fc.finalizedCheckpoint != nil {
		status["finalized_epoch"] = fc.finalizedCheckpoint.Epoch
		status["finalized_block"] = fc.finalizedCheckpoint.BlockHash
	}

	if fc.justifiedCheckpoint != nil && fc.finalizedCheckpoint != nil {
		status["epochs_since_finalized"] = fc.justifiedCheckpoint.Epoch - fc.finalizedCheckpoint.Epoch
	}

	return status
}

// CleanupOldEpochs removes old epoch data to prevent unbounded memory growth
// This is critical for long-running nodes with many validators
func (fc *ForkChoice) CleanupOldEpochs() {
	startTime := time.Now()

	fc.mu.Lock()
	defer fc.mu.Unlock()

	currentEpoch := fc.getCurrentEpoch()

	// 1. Define Safety Cap (Hard Limit)
	// Even if finality stalls, do not keep more than 50 epochs (~25 minutes @ 3s blocks) in memory.
	// This prevents OOM crashes during network partitions.
	const MaxSafetyRetention = 50
	var minSafeCutoff uint64 = 0
	if currentEpoch > MaxSafetyRetention {
		minSafeCutoff = currentEpoch - MaxSafetyRetention
	}

	// 2. Determine Cutoff Epoch
	var cutoffEpoch uint64

	if fc.finalizedCheckpoint != nil {
		finalizedEpoch := fc.finalizedCheckpoint.Epoch
		// Ideally keep 2 epochs before finalized
		if finalizedEpoch > 2 {
			cutoffEpoch = finalizedEpoch - 2
		} else {
			cutoffEpoch = 0
		}
	} else {
		// No finalization yet, use Config default
		if currentEpoch > fc.fcConfig.MaxEpochsToKeep {
			cutoffEpoch = currentEpoch - fc.fcConfig.MaxEpochsToKeep
		} else {
			cutoffEpoch = 0
		}
	}

	// 3. Apply Safety Cap Override
	// If the calculated cutoff (based on finality) is too old (stalled chain),
	// force the cutoff to the safety limit.
	if cutoffEpoch < minSafeCutoff {
		fmt.Printf("⚠️ Finality stalled or lagging; forcing cleanup at safety cutoff %d (current: %d)\n",
			minSafeCutoff, currentEpoch)
		cutoffEpoch = minSafeCutoff
	}

	epochsRemoved := 0
	blocksRemoved := 0
	attestationsRemoved := 0

	// 4. Cleanup old epoch attestations
	for epoch := range fc.epochAttestations {
		if epoch < cutoffEpoch {
			delete(fc.epochAttestations, epoch)
			epochsRemoved++
		}
	}

	// 5. Cleanup old blocks
	for blockHash := range fc.blockScores {
		blockEpoch, exists := fc.blockEpochMap[blockHash]

		shouldRemove := false
		// Remove if epoch is too old OR if we have a "zombie" block with no mapped epoch
		if !exists || blockEpoch < cutoffEpoch {
			shouldRemove = true
		}

		if shouldRemove {
			// Count attestations being removed
			if attestations, ok := fc.attestationsByBlock[blockHash]; ok {
				attestationsRemoved += len(attestations)
			}

			// Remove all data for this block
			delete(fc.blockScores, blockHash)
			delete(fc.attestationsByBlock, blockHash)
			delete(fc.validatorAttestations, blockHash)
			delete(fc.blockEpochMap, blockHash)
			blocksRemoved++
		}
	}

	// Update metrics
	fc.metrics.LastCleanupTime = time.Now()
	fc.metrics.EpochsRemoved += int64(epochsRemoved)
	fc.metrics.BlocksRemoved += int64(blocksRemoved)
	fc.metrics.AttestationsRemoved += int64(attestationsRemoved)
	fc.metrics.LastCleanupDurationMs = time.Since(startTime).Milliseconds()
	fc.metrics.TotalBlocks = int64(len(fc.blockScores))
	fc.metrics.TotalEpochs = int64(len(fc.epochAttestations))

	// Estimate memory usage
	fc.updateMemoryEstimate()

	if epochsRemoved > 0 || blocksRemoved > 0 {
		fmt.Printf("🧹 Cleanup completed in %dms: removed %d epochs, %d blocks, %d attestations (current: %d blocks, %d epochs, ~%d MB)\n",
			fc.metrics.LastCleanupDurationMs,
			epochsRemoved,
			blocksRemoved,
			attestationsRemoved,
			fc.metrics.TotalBlocks,
			fc.metrics.TotalEpochs,
			fc.metrics.MemoryEstimateBytes/(1024*1024))
	}
}

// --------------------------------------------------------------------------
// Helper: calculatePercentage (Internal)
// --------------------------------------------------------------------------

func calculatePercentage(numerator, denominator *big.Int) float64 {
	if denominator == nil || denominator.Sign() == 0 {
		return 0.0
	}
	numF := new(big.Float).SetInt(numerator)
	denF := new(big.Float).SetInt(denominator)
	res := new(big.Float).Quo(numF, denF)
	res.Mul(res, big.NewFloat(100))
	f, _ := res.Float64()
	return f
}
