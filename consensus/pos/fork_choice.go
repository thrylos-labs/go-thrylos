// consensus/pos/fork_choice.go
// Fork choice rule with stake-weighted quorum checking and memory management

package pos

import (
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	coremath "github.com/thrylos-labs/go-thrylos/core/math" // Safe BigInt math
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
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
	MaxReorgDepth           int           `json:"max_reorg_depth"`     // Maximum reorg depth (default: 100)
	FinalizationEpochs      int           `json:"finalization_epochs"` // Epochs before finalization (default: 2)
	MinStakeForReorg        float64       `json:"min_stake_for_reorg"` // Minimum stake fraction (default: 0.66 = 66%)
	CheckpointInterval      int           `json:"checkpoint_interval"`
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
		MaxReorgDepth:           100,  // Can reorg up to 100 blocks
		FinalizationEpochs:      2,    // Finalize after 2 epochs
		MinStakeForReorg:        0.66, // Require 66% stake for reorg
		CheckpointInterval:      10,   // Checkpoint every 10 epochs
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
		epochBlockOrder:       make(map[uint64][]string),
		totalActiveStake:      "0",
		totalActiveStakeTime:  time.Time{},
		metrics:               &ForkChoiceMetrics{},
		latestMessages:        make(map[uint64]map[string]string),
		children:              make(map[string][]string),
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

// CORRECTED ProcessAttestation method for fork_choice.go
// Replace your current ProcessAttestation with this:

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
			if dvErr, ok := err.(*DoubleSigningError); ok && dvErr.ConflictingRecord != nil {
				prevAttestation := attestationFromRecord(dvErr.ConflictingRecord)
				if slashErr := fc.slashingManager.ApplyDoubleVoteSlashing(prevAttestation, attestation); slashErr != nil {
					fmt.Printf("⚠️ Failed to apply double-vote slashing for %s: %v\n", validatorAddr, slashErr)
				}
			}
			return
		}

		if !fc.slashingManager.IsValidatorActive(validatorAddr) {
			fmt.Printf("⚠️ Inactive/jailed validator %s attempted to attest\n", validatorAddr)
			return
		}
	}

	// Get validator info early
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

	// 2. Track latest messages and detect equivocations
	if fc.latestMessages[epoch] == nil {
		fc.latestMessages[epoch] = make(map[string]string)
	}

	// Check for equivocation (validator voting for different blocks in same epoch)
	if prevVote, exists := fc.latestMessages[epoch][validatorAddr]; exists {
		if prevVote != blockHash {
			fmt.Printf("⚠️ EQUIVOCATION DETECTED: validator %s voted for both %s and %s in epoch %d\n",
				validatorAddr, prevVote[:8], blockHash[:8], epoch)

			// Apply slashing immediately if we can reconstruct the prior conflicting attestation
			// from fork-choice history. This covers the case where the slashing manager's
			// startup grace path skipped recording/slashing on first observation.
			if fc.slashingManager != nil {
				if prevAttestation := fc.findValidatorAttestation(prevVote, validatorAddr, epoch); prevAttestation != nil {
					if err := fc.slashingManager.ApplyDoubleVoteSlashing(prevAttestation, attestation); err != nil {
						fmt.Printf("⚠️ Failed to slash validator %s for equivocation: %v\n", validatorAddr, err)
					} else {
						fmt.Printf("⚠️ Validator %s slashed for equivocation\n", validatorAddr)
					}
				} else {
					fmt.Printf("⚠️ Equivocation detected for %s but prior attestation was not retained\n", validatorAddr)
				}
			}
			return
		}
		// Same block, allow (idempotent)
	}

	// Update latest message for this validator in this epoch
	fc.latestMessages[epoch][validatorAddr] = blockHash

	// 3. Check if this validator has already attested to this block
	if fc.validatorAttestations[blockHash] == nil {
		fc.validatorAttestations[blockHash] = make(map[string]bool)
	}

	if fc.validatorAttestations[blockHash][validatorAddr] {
		return // Duplicate attestation for same block
	}

	// Mark validator as having attested to this block
	fc.validatorAttestations[blockHash][validatorAddr] = true

	// 4. Store attestation (with limit)
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

	isNewBlock := false
	if _, exists := fc.blockScores[blockHash]; !exists {
		isNewBlock = true
	}

	// 5. Update block score using BigInt math
	currentScore := fc.blockScores[blockHash]
	newScore := addBigIntStrings(currentScore, coremath.BigIntToString(coremath.ParseBigInt(validatorStake)))
	fc.blockScores[blockHash] = newScore

	// Track epoch mapping
	fc.blockEpochMap[blockHash] = epoch
	if isNewBlock {
		fc.epochBlockOrder[epoch] = append(fc.epochBlockOrder[epoch], blockHash)
		fc.pruneEpochBlocksLocked(epoch)
	}

	// 6. Update epoch attestations
	createdEpochWindow := false
	if fc.epochAttestations[epoch] == nil {
		fc.epochAttestations[epoch] = make(map[string]string)
		fc.metrics.TotalEpochs++
		createdEpochWindow = true
	}
	currentEpochScore := fc.epochAttestations[epoch][blockHash]
	newEpochScore := addBigIntStrings(currentEpochScore, coremath.BigIntToString(coremath.ParseBigInt(validatorStake)))
	fc.epochAttestations[epoch][blockHash] = newEpochScore

	// Bound epoch-indexed state immediately on epoch growth instead of waiting
	// for the background cleanup ticker.
	if createdEpochWindow {
		fc.cleanupOldEpochsLocked(time.Now())
	}

	// 7. Check Quorum (2/3 of total stake)
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

		// ✅ CREATE CHECKPOINT (NEW - ADD THIS!)
		fc.EnsurePeriodicCheckpoint(
			epoch,
			blockHash,
			attestingStakeBig.String(),
			totalStakeBig.String(),
		)

		// Check justification (passing strings)
		fc.checkJustification(epoch, blockHash, attestingStakeBig.String(), totalStakeBig.String())
	}
}

func (fc *ForkChoice) pruneEpochBlocksLocked(epoch uint64) {
	if fc.fcConfig == nil || fc.fcConfig.MaxBlocksPerEpoch <= 0 {
		return
	}

	order := fc.epochBlockOrder[epoch]
	for len(order) > fc.fcConfig.MaxBlocksPerEpoch {
		oldest := order[0]
		order = order[1:]
		fc.removeTrackedBlockLocked(oldest)
	}

	if len(order) == 0 {
		delete(fc.epochBlockOrder, epoch)
		return
	}
	fc.epochBlockOrder[epoch] = order
}

func (fc *ForkChoice) removeTrackedBlockLocked(blockHash string) {
	epoch, hasEpoch := fc.blockEpochMap[blockHash]
	if hasEpoch {
		if order := fc.epochBlockOrder[epoch]; len(order) > 0 {
			filtered := order[:0]
			for _, candidate := range order {
				if candidate != blockHash {
					filtered = append(filtered, candidate)
				}
			}
			if len(filtered) == 0 {
				delete(fc.epochBlockOrder, epoch)
			} else {
				fc.epochBlockOrder[epoch] = filtered
			}
		}

		if blocks := fc.epochAttestations[epoch]; blocks != nil {
			delete(blocks, blockHash)
			if len(blocks) == 0 {
				delete(fc.epochAttestations, epoch)
			}
		}
	}

	delete(fc.blockScores, blockHash)
	delete(fc.attestationsByBlock, blockHash)
	delete(fc.validatorAttestations, blockHash)
	delete(fc.blockEpochMap, blockHash)
}

func (fc *ForkChoice) findValidatorAttestation(blockHash, validatorAddr string, epoch uint64) *types.Attestation {
	attestations := fc.attestationsByBlock[blockHash]
	for _, att := range attestations {
		if att != nil && att.ValidatorAddress == validatorAddr && att.Epoch == epoch {
			return att
		}
	}
	return nil
}

func attestationFromRecord(record *storage.AttestationRecord) *types.Attestation {
	if record == nil {
		return nil
	}
	return &types.Attestation{
		ValidatorAddress: record.ValidatorAddress,
		BlockHash:        record.BlockHash,
		BlockHeight:      int64(record.Epoch * 32),
		Epoch:            record.Epoch,
		Slot:             record.Slot,
		Signature:        append([]byte(nil), record.Signature...),
		Timestamp:        record.Timestamp.Unix(),
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
// GetHead returns the current head block according to the fork choice rule.
func (fc *ForkChoice) GetHead() string {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	// 1. Anchor Point: Start at the latest secure checkpoint
	// This simplifies complexity by ignoring all history before finalization.
	currentBlockHash := ""
	if fc.finalizedCheckpoint != nil {
		currentBlockHash = fc.finalizedCheckpoint.BlockHash
	} else if fc.justifiedCheckpoint != nil {
		currentBlockHash = fc.justifiedCheckpoint.BlockHash
	} else {
		// Fallback to Genesis if no checkpoints exist
		genesis, _ := fc.worldState.GetBlockByHash("") // or height 0
		if genesis != nil {
			currentBlockHash = genesis.Hash
		}
	}

	// 2. LMD GHOST Traversal
	// Loop until we reach a tip (a block with no children)
	for {
		children := fc.children[currentBlockHash]
		if len(children) == 0 {
			return currentBlockHash // Found the head
		}

		bestChild := ""
		maxWeight := big.NewInt(0)

		for _, childHash := range children {
			// Calculate weight (votes for this child + all its descendants)
			weight := fc.getSubtreeStake(childHash)

			// 3. Selection Rule: Heaviest Weight wins
			cmp := weight.Cmp(maxWeight)
			if cmp > 0 {
				maxWeight = weight
				bestChild = childHash
			} else if cmp == 0 {
				// 4. Tie-Breaker: Deterministic (Lower Hash wins)
				// This solves the partition edge case where nodes split 50/50.
				if bestChild == "" || childHash < bestChild {
					bestChild = childHash
				}
			}
		}

		// Move down the tree
		currentBlockHash = bestChild
	}
}

// getHeadByHighestStake is the fallback when no justified checkpoint exists
func (fc *ForkChoice) getHeadByHighestStake() string {
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

// getSubtreeStake calculates stake supporting a subtree using latest messages
func (fc *ForkChoice) getSubtreeStake(rootHash string) *big.Int {
	totalWeight := big.NewInt(0)
	currentEpoch := fc.getCurrentEpoch()

	// Iterate over the Latest Message of every validator
	// (This is the "LMD" in LMD GHOST)
	for epoch, votes := range fc.latestMessages {
		// Optimization: Only look at recent epochs to reduce complexity
		if epoch < currentEpoch-2 {
			continue
		}

		for validatorAddr, votedBlockHash := range votes {
			// Check if the validator's vote supports this branch
			if votedBlockHash == rootHash || fc.isDescendant(votedBlockHash, rootHash) {

				// Get Validator Stake
				validator, err := fc.worldState.GetValidator(validatorAddr)
				if err == nil && validator != nil && validator.Active {
					stake := coremath.ParseBigInt(validator.Stake)

					// Apply Weight Decay (Audit Recommendation)
					// Reduces the weight of old votes to prevent long-range attacks
					decayedStake := fc.ApplyWeightDecay(stake, epoch, currentEpoch)

					totalWeight.Add(totalWeight, decayedStake)
				}
			}
		}
	}

	return totalWeight
}

// OnBlockAdded tracks block relationships when adding blocks to the chain
func (fc *ForkChoice) OnBlockAdded(block *core.Block) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	parentHash := block.Header.PrevHash
	blockHash := block.Hash // Hash is on Block, not Header

	if fc.children[parentHash] == nil {
		fc.children[parentHash] = make([]string, 0)
	}
	fc.children[parentHash] = append(fc.children[parentHash], blockHash)
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

	curr := childHash
	// Safety limit to prevent infinite loops in cyclic graphs (attack vector)
	for i := 0; i < 100; i++ {
		block, _ := fc.worldState.GetBlockByHash(curr)
		if block == nil {
			return false
		}
		parent := block.Header.PrevHash

		if parent == ancestorHash {
			return true
		}
		if parent == "" {
			return false
		}
		curr = parent
	}
	return false
}

// Helper: Add two numeric strings
func addBigIntStrings(a, b string) string {
	biA := coremath.ParseBigInt(a)
	biB := coremath.ParseBigInt(b)
	return new(big.Int).Add(biA, biB).String()
}

// SetDatabase attaches a database for checkpoint persistence
func (fc *ForkChoice) SetDatabase(db DatabaseStore) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.database = db
}

// LoadFinalizedCheckpoint loads checkpoint from disk on startup
func (fc *ForkChoice) LoadFinalizedCheckpoint() error {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	if fc.database == nil {
		return nil // No database attached
	}

	data, err := fc.database.Get([]byte("finalized_checkpoint"))
	if err != nil {
		return nil // Not found is OK (first run)
	}

	var checkpoint Checkpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return fmt.Errorf("failed to load checkpoint: %w", err)
	}

	fc.finalizedCheckpoint = &checkpoint
	fmt.Printf("📂 Loaded finalized checkpoint: epoch %d, block %s\n",
		checkpoint.Epoch, checkpoint.BlockHash[:8])

	return nil
}

// GetTotalActiveStake returns total active stake (public wrapper)
func (fc *ForkChoice) GetTotalActiveStake() string {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	return fc.getTotalActiveStake()
}

// This implements the "Exponential attestation weight decay" recommendation.
func (fc *ForkChoice) ApplyWeightDecay(originalStake *big.Int, attestationEpoch uint64, currentEpoch uint64) *big.Int {
	if currentEpoch <= attestationEpoch {
		return originalStake
	}

	age := currentEpoch - attestationEpoch

	// Decay calculation: Stake * (0.9 ^ age)
	// For simplicity in integer math, we use basis points: (Stake * (9000^age)) / (10000^age)
	// Or even simpler: reduce by 10% for every epoch of age.
	decayedStake := new(big.Int).Set(originalStake)

	// We limit the decay loop to prevent CPU exhaustion on very old blocks
	maxDecayEpochs := 32
	if age > uint64(maxDecayEpochs) {
		age = uint64(maxDecayEpochs)
	}

	for i := uint64(0); i < age; i++ {
		// Reduce by 10% each epoch: decayedStake = (decayedStake * 9) / 10
		decayedStake.Mul(decayedStake, big.NewInt(9))
		decayedStake.Div(decayedStake, big.NewInt(10))
	}

	return decayedStake
}
