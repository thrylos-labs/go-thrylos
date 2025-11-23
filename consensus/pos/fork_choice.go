// consensus/pos/fork_choice.go
// Fork choice rule with stake-weighted quorum checking

package pos

import (
	"fmt"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// This allows us to swap the real WorldState for a MockWorldState during testing.
type WorldStateReader interface {
	GetValidator(address string) (*core.Validator, error)
	GetActiveValidators() []*core.Validator
}

// NewForkChoice creates a new fork choice instance
// Update the argument type here ----------------vvvvvvvvvvvvvvvv
func NewForkChoice(config *config.Config, worldState WorldStateReader) *ForkChoice {
	return &ForkChoice{
		config:                config,
		worldState:            worldState,
		blockScores:           make(map[string]int64),
		attestationsByBlock:   make(map[string][]*Attestation),
		validatorAttestations: make(map[string]map[string]bool),
		epochAttestations:     make(map[uint64]map[string]int64),
		totalActiveStake:      0,
		totalActiveStakeTime:  time.Time{},
	}
}

// ProcessAttestation processes an attestation for fork choice with stake-weighted voting
func (fc *ForkChoice) ProcessAttestation(attestation *Attestation) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	blockHash := attestation.BlockHash
	validatorAddr := attestation.ValidatorAddress // ✅ Use FULL address for lookups

	// Get validator info using FULL address
	validator, err := fc.worldState.GetValidator(validatorAddr)
	if err != nil || validator == nil {
		fmt.Printf("⚠️ Failed to get validator %s for attestation: %v\n", validatorAddr, err)
		return
	}

	blockHashShort := blockHash
	if len(blockHashShort) > 8 {
		blockHashShort = blockHashShort[:8]
	}

	epoch := attestation.Epoch

	// Check if this validator has already attested to this block
	if fc.validatorAttestations[blockHash] == nil {
		fc.validatorAttestations[blockHash] = make(map[string]bool)
	}

	if fc.validatorAttestations[blockHash][validatorAddr] {
		// Validator already attested to this block, ignore duplicate
		return
	}

	if !validator.Active {
		fmt.Printf("⚠️ Inactive validator %s attempted to attest\n", validatorAddr)
		return
	}

	validatorStake := validator.Stake

	// Mark validator as having attested to this block
	fc.validatorAttestations[blockHash][validatorAddr] = true

	// Add attestation to block
	if fc.attestationsByBlock[blockHash] == nil {
		fc.attestationsByBlock[blockHash] = make([]*Attestation, 0)
	}
	fc.attestationsByBlock[blockHash] = append(fc.attestationsByBlock[blockHash], attestation)

	// Update block score with stake weight
	fc.blockScores[blockHash] += validatorStake

	// Track epoch attestations for finality
	if fc.epochAttestations[epoch] == nil {
		fc.epochAttestations[epoch] = make(map[string]int64)
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
// Results are cached for 30 seconds to avoid expensive recalculation
func (fc *ForkChoice) getTotalActiveStake() int64 {
	// Check cache (30 second TTL)
	if time.Since(fc.totalActiveStakeTime) < 30*time.Second && fc.totalActiveStake > 0 {
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
// A block is safe if it has 2/3+ of total stake attesting to it
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
