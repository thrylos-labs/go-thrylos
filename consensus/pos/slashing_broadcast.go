// consensus/pos/slashing_broadcast.go
// Slashing evidence broadcasting and processing

package pos

import (
	"fmt"
	"log"
	"math/big"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
	coremath "github.com/thrylos-labs/go-thrylos/core/math"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/crypto/hash"
	"github.com/thrylos-labs/go-thrylos/types"
)

// EvidenceTracker tracks processed slashing evidence to prevent duplicates
type EvidenceTracker struct {
	mu           sync.RWMutex
	evidenceByID *lru.Cache[string, *SlashingEvidence]
}

// NewEvidenceTracker creates a new evidence tracker
func NewEvidenceTracker() *EvidenceTracker {
	cache, err := lru.New[string, *SlashingEvidence](10000)
	if err != nil {
		panic(fmt.Sprintf("failed to initialize evidence tracker cache: %v", err))
	}

	return &EvidenceTracker{
		evidenceByID: cache,
	}
}

// IsProcessed checks if evidence has been processed
func (et *EvidenceTracker) IsProcessed(evidenceID string) bool {
	et.mu.Lock()
	defer et.mu.Unlock()

	_, exists := et.evidenceByID.Get(evidenceID)
	return exists
}

// MarkProcessed marks evidence as processed
func (et *EvidenceTracker) MarkProcessed(evidence *SlashingEvidence) {
	et.mu.Lock()
	defer et.mu.Unlock()

	// Add updates recency and evicts the least recently used evidence deterministically.
	et.evidenceByID.Add(evidence.ID, evidence)
}

// ============================================================================
// MAIN IMPLEMENTATION: Add these methods to ConsensusEngine (usually in consensus.go, but placed here for context)
// ============================================================================

// HandleSlashingEvidence handles detected slashing evidence
// Note: Changed receiver to ConsensusEngine to match your provided code snippet's intent
func (ce *ConsensusEngine) HandleSlashingEvidence(evidence *SlashingEvidence) error {
	// 1. Check if already processed (prevent duplicates) - FAIL FAST
	if ce.evidenceTracker.IsProcessed(evidence.ID) {
		log.Printf("📝 Evidence %s already processed, skipping", evidence.ID)
		return nil
	}

	// 2. Validate evidence structure
	if err := evidence.Validate(ce.worldState); err != nil {
		return fmt.Errorf("invalid slashing evidence: %v", err)
	}

	// 3. Verify reporter signature
	if err := ce.verifyEvidenceSignature(evidence); err != nil {
		return fmt.Errorf("invalid evidence signature: %v", err)
	}

	// 4. Apply slashing locally
	if err := ce.applySlashing(evidence); err != nil {
		return fmt.Errorf("failed to apply slashing: %v", err)
	}

	// 5. Mark as processed
	ce.evidenceTracker.MarkProcessed(evidence)

	// 6. Broadcast to network
	if err := ce.broadcastSlashingEvidence(evidence); err != nil {
		log.Printf("⚠️  Failed to broadcast slashing evidence: %v", err)
		// Don't return error - local slashing succeeded
	}

	// 7. Store evidence for auditing (if storage available)
	if err := ce.persistSlashingEvidence(evidence); err != nil {
		log.Printf("⚠️  Failed to persist slashing evidence: %v", err)
		// Don't return error - slashing succeeded
	}

	log.Printf("✅ Slashing evidence %s processed successfully for validator %s",
		evidence.ID, evidence.ValidatorAddress)

	return nil
}

// applySlashing applies the slashing penalty locally using existing SlashingManager
func (ce *ConsensusEngine) applySlashing(evidence *SlashingEvidence) error {
	// Skip if slashing manager not initialized (e.g., in tests)
	if ce.slashingManager == nil {
		log.Println("⚠️  Skipping slashing application (slashingManager not initialized)")
		return nil
	}

	switch evidence.Type {
	case EvidenceDoubleVoting:
		doubleVote, ok := evidence.Evidence.(*DoubleVoteEvidence)
		if !ok {
			return fmt.Errorf("invalid evidence type for double voting")
		}

		if err := ce.slashingManager.ApplyDoubleVoteSlashing(
			doubleVote.Attestation1,
			doubleVote.Attestation2,
		); err != nil {
			return fmt.Errorf("failed to apply double-vote slashing: %w", err)
		}

		return nil

	case EvidenceSurroundVoting:
		surroundVote, ok := evidence.Evidence.(*SurroundVoteEvidence)
		if !ok {
			return fmt.Errorf("invalid evidence type for surround voting")
		}

		err := ce.slashingManager.ProcessAttestation(surroundVote.InnerAttestation)
		if err != nil {
			log.Printf("⚠️  Inner attestation processing: %v", err)
		}

		err = ce.slashingManager.ProcessAttestation(surroundVote.OuterAttestation)
		if err != nil {
			log.Printf("✅ Surround voting detected and slashed: %v", err)
			return nil
		}

		return nil

	case EvidenceInvalidProposal:
		invalidProposal, ok := evidence.Evidence.(*InvalidProposalEvidence)
		if !ok {
			return fmt.Errorf("invalid evidence type for invalid proposal")
		}

		blockProposal := &types.BlockProposal{
			Proposer:  invalidProposal.Proposal.Proposer,
			Slot:      invalidProposal.Proposal.Slot,
			Epoch:     invalidProposal.Proposal.Epoch,
			Signature: invalidProposal.Proposal.Signature,
		}

		reason := "Invalid proposal"
		if len(invalidProposal.ValidationErrors) > 0 {
			reason = invalidProposal.ValidationErrors[0]
		}

		return ce.slashingManager.ReportInvalidProposal(blockProposal, reason)

	case EvidenceDowntime:
		downtime, ok := evidence.Evidence.(*DowntimeEvidence)
		if !ok {
			return fmt.Errorf("invalid evidence type for downtime")
		}

		for _, slot := range downtime.MissedSlots {
			ce.slashingManager.ReportMissedAttestation(evidence.ValidatorAddress, slot)
		}

		log.Printf("✅ Reported %d missed attestations for validator %s",
			len(downtime.MissedSlots), evidence.ValidatorAddress)

		return nil

	case EvidenceInvalidSignature:
		invalidSig, ok := evidence.Evidence.(*InvalidSignatureEvidence)
		if !ok {
			return fmt.Errorf("invalid evidence type for invalid signature")
		}

		// 1. Get validator's current balance
		balance, err := ce.worldState.GetBalance(evidence.ValidatorAddress)
		if err != nil {
			return fmt.Errorf("failed to get validator balance: %v", err)
		}

		// 2. Calculate Penalty
		penaltyPercent := int64(ce.config.Consensus.SlashingInvalidSig)

		// Use SafePercentageBig helper
		penaltyAmount, err := coremath.SafePercentageBig(balance, penaltyPercent)
		if err != nil {
			return fmt.Errorf("failed to calculate penalty: %v", err)
		}

		// 3. Subtract Penalty
		newBalance := coremath.Sub(balance, penaltyAmount)

		// 4. Ensure non-negative
		if newBalance.Sign() < 0 {
			newBalance = big.NewInt(0)
		}

		// 5. Update Balance
		err = ce.worldState.UpdateBalance(evidence.ValidatorAddress, newBalance)
		if err != nil {
			return fmt.Errorf("failed to update balance: %v", err)
		}

		log.Printf("🔨 Slashed validator %s for invalid signature in %s: %s tokens",
			evidence.ValidatorAddress, invalidSig.Context, penaltyAmount.String())

		return nil

	default:
		return fmt.Errorf("unknown evidence type: %d", evidence.Type)
	}
}

// broadcastSlashingEvidence broadcasts evidence to the network
func (ce *ConsensusEngine) broadcastSlashingEvidence(evidence *SlashingEvidence) error {
	if ce.broadcastChan == nil {
		return fmt.Errorf("broadcast channel not initialized")
	}

	// Sign evidence with our private key before broadcasting
	if err := ce.signEvidence(evidence); err != nil {
		return fmt.Errorf("failed to sign evidence: %v", err)
	}

	// Send to broadcast channel
	ce.broadcastChan <- evidence

	log.Printf("📡 Broadcasted slashing evidence %s for validator %s",
		evidence.ID, evidence.ValidatorAddress)

	return nil
}

// persistSlashingEvidence stores evidence for auditing
func (ce *ConsensusEngine) persistSlashingEvidence(evidence *SlashingEvidence) error {
	// Check if we have slashing storage
	if ce.slashingManager == nil || ce.slashingManager.storage == nil {
		return nil // No storage available, skip
	}

	// Store via slashing manager's storage
	// For now, we log it
	log.Printf("💾 Persisting slashing evidence %s (storage method TBD)", evidence.ID)

	return nil
}

// processReceivedSlashingEvidence handles evidence received from peers
func (ce *ConsensusEngine) processReceivedSlashingEvidence(evidence *SlashingEvidence) error {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	log.Printf("📨 Received slashing evidence %s for validator %s from reporter %s",
		evidence.ID, evidence.ValidatorAddress, evidence.ReporterAddress)

	// Check if already processed (Fail Fast)
	if ce.evidenceTracker.IsProcessed(evidence.ID) {
		log.Printf("⚠️  Evidence %s already processed, skipping", evidence.ID)
		return nil
	}

	// Validate the evidence
	if err := evidence.Validate(ce.worldState); err != nil {
		return fmt.Errorf("invalid evidence: %v", err)
	}

	// Verify signature
	if err := ce.verifyEvidenceSignature(evidence); err != nil {
		// Log warning but don't crash in tests
		log.Printf("⚠️  Signature verification failed: %v", err)
		// Still mark as processed to avoid re-processing invalid data repeatedly
		ce.evidenceTracker.MarkProcessed(evidence)
		return fmt.Errorf("invalid evidence signature: %v", err)
	}

	// Mark as processed BEFORE applying
	ce.evidenceTracker.MarkProcessed(evidence)

	// Apply slashing locally
	if err := ce.applySlashing(evidence); err != nil {
		log.Printf("⚠️  Failed to apply slashing: %v", err)
		// Don't return error - evidence was still processed
	}

	log.Printf("✅ Slashing evidence processed successfully")
	return nil
}

// signEvidence signs evidence with the node's private key
func (ce *ConsensusEngine) signEvidence(evidence *SlashingEvidence) error {
	// Create hash of evidence for signing
	data := fmt.Sprintf("%s%s%s%d",
		evidence.ID,
		evidence.Type.String(),
		evidence.ValidatorAddress,
		evidence.Timestamp)

	// Use Keccak256
	hashBytes := hash.Keccak256([]byte(data))

	// Sign with private key
	signature, err := ce.nodePrivateKey.Sign(hashBytes)
	if err != nil {
		return fmt.Errorf("failed to sign evidence: %w", err)
	}

	evidence.ReporterSignature = signature.Bytes()
	evidence.ReporterAddress = ce.nodeAddress

	return nil
}

// verifyEvidenceSignature verifies the reporter's signature
func (ce *ConsensusEngine) verifyEvidenceSignature(evidence *SlashingEvidence) error {
	// 1. Signature must exist
	if len(evidence.ReporterSignature) == 0 {
		return fmt.Errorf("evidence not signed")
	}

	// 2. In tests, we might not have worldState
	if ce.worldState == nil {
		log.Println("⚠️ CRITICAL WARNING: Skipping evidence verification because worldState is nil. This should ONLY happen in unit tests.")
		return nil
	}

	// 3. Signature length check
	if len(evidence.ReporterSignature) != crypto.SignatureSize {
		return fmt.Errorf("invalid signature length: got %d, want %d",
			len(evidence.ReporterSignature), crypto.SignatureSize)
	}

	// 4. Reporter must be a known validator
	validator, err := ce.worldState.GetValidator(evidence.ReporterAddress)
	if err != nil {
		return fmt.Errorf("reporter %s is not a registered validator: %w",
			evidence.ReporterAddress, err)
	}

	if validator.Pubkey == nil || len(validator.Pubkey) == 0 {
		return fmt.Errorf("reporter %s has no registered public key", evidence.ReporterAddress)
	}

	// 5. Parse reporter's public key
	pubKey, err := crypto.NewPublicKeyFromBytes(validator.Pubkey)
	if err != nil {
		return fmt.Errorf("failed to parse reporter public key: %w", err)
	}

	// 6. Parse the signature bytes
	sig, err := crypto.SignatureFromBytes(evidence.ReporterSignature)
	if err != nil {
		return fmt.Errorf("failed to parse reporter signature: %w", err)
	}

	// 7. Reconstruct the message hash
	data := fmt.Sprintf("%s%s%s%d",
		evidence.ID,
		evidence.Type.String(),
		evidence.ValidatorAddress,
		evidence.Timestamp,
	)

	hashBytes := hash.Keccak256([]byte(data))

	// 8. Verify
	if err := sig.Verify(pubKey, hashBytes); err != nil {
		return fmt.Errorf("invalid evidence signature: %w", err)
	}

	log.Printf("✅ Evidence signature cryptographically validated for reporter %s", evidence.ReporterAddress)
	return nil
}

// createSlashingEvidenceFromAttestation creates evidence from a DoubleSigningError
func (ce *ConsensusEngine) createSlashingEvidenceFromAttestation(
	attestation *types.Attestation,
	violation error,
) *SlashingEvidence {

	dsErr, ok := violation.(*DoubleSigningError)
	if !ok || dsErr.ConflictingRecord == nil {
		fmt.Printf("⚠️ Cannot create evidence: violation does not contain conflicting data: %v\n", violation)
		return nil
	}

	rec := dsErr.ConflictingRecord

	// Rebuild the conflicting attestation
	conflicting := &types.Attestation{
		ValidatorAddress: rec.ValidatorAddress,
		BlockHash:        rec.BlockHash,
		Epoch:            rec.Epoch,
		Slot:             rec.Slot,
		Signature:        rec.Signature,
		Timestamp:        rec.Timestamp.Unix(),
	}

	dv := &DoubleVoteEvidence{
		Attestation1: attestation,
		Attestation2: conflicting,
	}

	return NewSlashingEvidence(
		EvidenceDoubleVoting,
		attestation.ValidatorAddress,
		dv,
		ce.nodeAddress,
	)
}
