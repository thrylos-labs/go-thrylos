// consensus/pos/slashing_evidence.go
// Slashing evidence types and broadcasting system
// M-2 FIX: Added evidence expiration and pruning support

package pos

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/types"
)

// SlashingEvidenceType represents the type of slashing violation
type SlashingEvidenceType uint8

const (
	EvidenceDoubleVoting SlashingEvidenceType = iota
	EvidenceSurroundVoting
	EvidenceInvalidProposal
	EvidenceDowntime
	EvidenceInvalidSignature
	EvidenceMissedVRFReveal
)

type MissedVRFRevealEvidence struct {
	Slot             uint64 `json:"slot"`
	Epoch            uint64 `json:"epoch"`
	CommitmentHash   []byte `json:"commitment_hash"`
	CommittedAt      int64  `json:"committed_at"`
	RevealDeadline   int64  `json:"reveal_deadline"`
	CurrentTimestamp int64  `json:"current_timestamp"`
}

// Validate implements the Evidence interface
func (e *MissedVRFRevealEvidence) Validate() error {
	if e.Slot == 0 {
		return errors.New("slot cannot be zero")
	}
	if len(e.CommitmentHash) != 32 {
		return errors.New("commitment hash must be 32 bytes")
	}
	if e.CurrentTimestamp <= e.RevealDeadline {
		return errors.New("deadline has not passed yet")
	}
	return nil
}

// String returns string representation of evidence type
func (t SlashingEvidenceType) String() string {
	switch t {
	case EvidenceDoubleVoting:
		return "double_voting"
	case EvidenceSurroundVoting:
		return "surround_voting"
	case EvidenceInvalidProposal:
		return "invalid_proposal"
	case EvidenceDowntime:
		return "downtime"
	case EvidenceInvalidSignature:
		return "invalid_signature"
	default:
		return "unknown"
	}
}

// M-2 FIX: Evidence retention periods by type
const (
	// How long to keep evidence before it can be pruned
	EvidenceRetentionCritical = 90 * 24 * time.Hour // 90 days for critical offenses
	EvidenceRetentionStandard = 30 * 24 * time.Hour // 30 days for standard offenses
	EvidenceRetentionMinor    = 7 * 24 * time.Hour  // 7 days for minor offenses

	// Maximum age for evidence to be considered valid for slashing
	MaxEvidenceAge = 7 * 24 * time.Hour // 7 days
)

// SlashingEvidence represents evidence of a slashable offense
type SlashingEvidence struct {
	// Unique identifier for this evidence
	ID string `json:"id"`

	// Type of slashing violation
	Type SlashingEvidenceType `json:"type"`

	// Validator being accused
	ValidatorAddress string `json:"validator_address"`

	// Evidence details (varies by type)
	Evidence interface{} `json:"evidence"`

	// When evidence was created
	Timestamp int64 `json:"timestamp"`

	// Reporter's address (node that detected it)
	ReporterAddress string `json:"reporter_address"`

	// Reporter's signature on the evidence
	ReporterSignature []byte `json:"reporter_signature"`

	// M-2 FIX: Track when evidence was processed
	ProcessedAt int64 `json:"processed_at,omitempty"`

	// M-2 FIX: Track if evidence resulted in slashing
	SlashingApplied bool `json:"slashing_applied"`
}

// DoubleVoteEvidence contains proof of double voting
type DoubleVoteEvidence struct {
	// First attestation
	Attestation1 *types.Attestation `json:"attestation_1"`

	// Second conflicting attestation
	Attestation2 *types.Attestation `json:"attestation_2"`

	// Both attestations must be:
	// - From same validator
	// - For same slot/epoch
	// - For different blocks
}

// SurroundVoteEvidence contains proof of surround voting
type SurroundVoteEvidence struct {
	// Inner vote (being surrounded)
	InnerAttestation *types.Attestation `json:"inner_attestation"`

	// Outer vote (surrounding)
	OuterAttestation *types.Attestation `json:"outer_attestation"`

	// Outer must surround inner:
	// outer.source < inner.source < inner.target < outer.target
}

// InvalidProposalEvidence contains proof of invalid block proposal
type InvalidProposalEvidence struct {
	// The invalid block proposal
	Proposal *BlockProposal `json:"proposal"`

	// Validation errors
	ValidationErrors []string `json:"validation_errors"`

	// Block hash for reference
	BlockHash string `json:"block_hash"`
}

// DowntimeEvidence contains proof of validator downtime
type DowntimeEvidence struct {
	// Number of missed attestations
	MissedAttestations int `json:"missed_attestations"`

	// Slots where attestations were missed
	MissedSlots []uint64 `json:"missed_slots"`

	// Time period
	StartTime int64 `json:"start_time"`
	EndTime   int64 `json:"end_time"`
}

// InvalidSignatureEvidence contains proof of invalid signature
type InvalidSignatureEvidence struct {
	// The message that was signed
	Message []byte `json:"message"`

	// The invalid signature
	Signature []byte `json:"signature"`

	// Public key used for verification
	PublicKey []byte `json:"public_key"`

	// Context (attestation, proposal, etc.)
	Context string `json:"context"`
}

// NewSlashingEvidence creates new slashing evidence
func NewSlashingEvidence(
	evidenceType SlashingEvidenceType,
	validatorAddress string,
	evidence interface{},
	reporterAddress string,
) *SlashingEvidence {
	se := &SlashingEvidence{
		Type:             evidenceType,
		ValidatorAddress: validatorAddress,
		Evidence:         evidence,
		Timestamp:        time.Now().Unix(),
		ReporterAddress:  reporterAddress,
		SlashingApplied:  false, // M-2 FIX: Initialize as not applied
	}

	// Generate unique ID from content
	se.ID = se.generateID()

	return se
}

// hashAttestation creates a deterministic hash of an attestation for signing
func (se *SlashingEvidence) hashAttestation(att *types.Attestation) []byte {
	// Create message to sign: validator|slot|blockhash
	message := fmt.Sprintf("%s:%d:%s",
		att.ValidatorAddress,
		att.Slot,
		att.BlockHash,
	)

	hash := sha256.Sum256([]byte(message))
	return hash[:]
}

// verifySignature verifies an Secp256k1 signature
func (se *SlashingEvidence) verifySignature(publicKeyBytes []byte, message []byte, signature []byte) bool {
	// Parse public key (accepts both 33-byte compressed and 65-byte uncompressed)
	publicKey, err := crypto.NewPublicKeyFromBytes(publicKeyBytes)
	if err != nil {
		return false
	}

	// Parse signature (65 bytes for Secp256k1: R || S || V)
	sig, err := crypto.SignatureFromBytes(signature)
	if err != nil {
		return false
	}

	// Verify signature - returns error on failure
	err = publicKey.Verify(message, sig)
	return err == nil
}

// generateID creates a unique ID for the evidence
// [FIX M-02] Deterministic ID generation based on (Validator, Height, Type)
func (se *SlashingEvidence) generateID() string {
	// 1. Determine height based on evidence type
	var height int64

	switch se.Type {
	case EvidenceDoubleVoting:
		if e, ok := se.Evidence.(*DoubleVoteEvidence); ok && e.Attestation1 != nil {
			height = e.Attestation1.BlockHeight
		}
	case EvidenceSurroundVoting:
		if e, ok := se.Evidence.(*SurroundVoteEvidence); ok && e.InnerAttestation != nil {
			height = e.InnerAttestation.BlockHeight
		}
	case EvidenceInvalidProposal:
		if e, ok := se.Evidence.(*InvalidProposalEvidence); ok && e.Proposal != nil {
			// Estimate height from Epoch if absolute height isn't available
			height = int64(e.Proposal.Epoch * 32)
		}
	case EvidenceDowntime:
		// For downtime, use the end timestamp/slot as a proxy for "height" uniqueness
		if e, ok := se.Evidence.(*DowntimeEvidence); ok {
			height = e.EndTime
		}
	default:
		height = se.Timestamp
	}

	// 2. Create deterministic string: Type:Validator:Height
	// This ensures we can only slash a validator ONCE per offense type at a specific height
	rawID := fmt.Sprintf("%s:%s:%d", se.Type.String(), se.ValidatorAddress, height)

	// 3. Hash it
	hash := sha256.Sum256([]byte(rawID))
	return fmt.Sprintf("%x", hash[:16]) // Use first 16 bytes
}

// Validate validates the evidence structure and content
func (se *SlashingEvidence) Validate(registry ValidatorRegistry) error {
	if se.ID == "" {
		se.ID = se.generateID()
	}

	expectedID := se.generateID()
	if se.ID != expectedID {
		return fmt.Errorf("invalid evidence ID: matches content mismatch")
	}

	if se.ValidatorAddress == "" {
		return fmt.Errorf("validator address cannot be empty")
	}

	if se.Evidence == nil {
		return fmt.Errorf("evidence data cannot be nil")
	}

	if se.Timestamp == 0 {
		return fmt.Errorf("timestamp cannot be zero")
	}

	now := time.Now().Unix()
	if se.Timestamp > now+300 {
		return fmt.Errorf("evidence timestamp is in the future")
	}

	// M-2 FIX: Use MaxEvidenceAge constant
	if se.Timestamp < now-int64(MaxEvidenceAge.Seconds()) {
		return fmt.Errorf("evidence is too old (stale): %d seconds", now-se.Timestamp)
	}

	return se.validateTypeSpecific(registry)
}

// validateTypeSpecific validates evidence based on its type
func (se *SlashingEvidence) validateTypeSpecific(registry ValidatorRegistry) error {
	switch se.Type {
	case EvidenceDoubleVoting:
		return se.validateDoubleVoteEvidence(registry)
	case EvidenceSurroundVoting:
		return se.validateSurroundVoteEvidence(registry)
	case EvidenceInvalidProposal:
		return se.validateInvalidProposalEvidence()
	case EvidenceDowntime:
		return se.validateDowntimeEvidence()
	case EvidenceInvalidSignature:
		return se.validateInvalidSignatureEvidence()
	default:
		return fmt.Errorf("unknown evidence type: %d", se.Type)
	}
}

func (se *SlashingEvidence) validateDoubleVoteEvidence(registry ValidatorRegistry) error {
	evidence, ok := se.Evidence.(*DoubleVoteEvidence)
	if !ok {
		return fmt.Errorf("invalid evidence type for double voting")
	}

	if evidence.Attestation1 == nil || evidence.Attestation2 == nil {
		return fmt.Errorf("both attestations must be provided")
	}

	// Must be from same validator
	if evidence.Attestation1.ValidatorAddress != evidence.Attestation2.ValidatorAddress {
		return fmt.Errorf("attestations from different validators")
	}

	validatorAddr := evidence.Attestation1.ValidatorAddress

	// Must be for same slot
	if evidence.Attestation1.Slot != evidence.Attestation2.Slot {
		return fmt.Errorf("attestations for different slots")
	}

	// Must be for different blocks
	if evidence.Attestation1.BlockHash == evidence.Attestation2.BlockHash {
		return fmt.Errorf("attestations for same block (not double voting)")
	}

	// H-01 FIX: Cryptographic verification
	if registry == nil {
		return fmt.Errorf("validator registry required for signature verification")
	}

	validator, err := registry.GetValidator(validatorAddr)
	if err != nil {
		return fmt.Errorf("failed to get validator %s: %v", validatorAddr, err)
	}

	if validator == nil {
		return fmt.Errorf("validator %s not found", validatorAddr)
	}

	// Verify signature on attestation 1
	att1Hash := se.hashAttestation(evidence.Attestation1)
	if !se.verifySignature(validator.Pubkey, att1Hash, evidence.Attestation1.Signature) {
		return fmt.Errorf("invalid signature on attestation 1")
	}

	// Verify signature on attestation 2
	att2Hash := se.hashAttestation(evidence.Attestation2)
	if !se.verifySignature(validator.Pubkey, att2Hash, evidence.Attestation2.Signature) {
		return fmt.Errorf("invalid signature on attestation 2")
	}

	return nil
}

func (se *SlashingEvidence) validateSurroundVoteEvidence(registry ValidatorRegistry) error {
	evidence, ok := se.Evidence.(*SurroundVoteEvidence)
	if !ok {
		return fmt.Errorf("invalid evidence type for surround voting")
	}

	if evidence.InnerAttestation == nil || evidence.OuterAttestation == nil {
		return fmt.Errorf("both attestations must be provided")
	}

	// Must be from same validator
	if evidence.InnerAttestation.ValidatorAddress != evidence.OuterAttestation.ValidatorAddress {
		return fmt.Errorf("attestations from different validators")
	}

	validatorAddr := evidence.InnerAttestation.ValidatorAddress

	// H-01 FIX: Cryptographic verification
	if registry == nil {
		return fmt.Errorf("validator registry required for signature verification")
	}

	validator, err := registry.GetValidator(validatorAddr)
	if err != nil {
		return fmt.Errorf("failed to get validator %s: %v", validatorAddr, err)
	}

	if validator == nil {
		return fmt.Errorf("validator %s not found", validatorAddr)
	}

	// Verify signature on inner attestation
	innerHash := se.hashAttestation(evidence.InnerAttestation)
	if !se.verifySignature(validator.Pubkey, innerHash, evidence.InnerAttestation.Signature) {
		return fmt.Errorf("invalid signature on inner attestation")
	}

	// Verify signature on outer attestation
	outerHash := se.hashAttestation(evidence.OuterAttestation)
	if !se.verifySignature(validator.Pubkey, outerHash, evidence.OuterAttestation.Signature) {
		return fmt.Errorf("invalid signature on outer attestation")
	}

	return nil
}

func (se *SlashingEvidence) validateInvalidProposalEvidence() error {
	evidence, ok := se.Evidence.(*InvalidProposalEvidence)
	if !ok {
		return fmt.Errorf("invalid evidence type for invalid proposal")
	}

	if evidence.Proposal == nil {
		return fmt.Errorf("proposal must be provided")
	}

	if len(evidence.ValidationErrors) == 0 {
		return fmt.Errorf("validation errors must be provided")
	}

	return nil
}

func (se *SlashingEvidence) validateDowntimeEvidence() error {
	evidence, ok := se.Evidence.(*DowntimeEvidence)
	if !ok {
		return fmt.Errorf("invalid evidence type for downtime")
	}

	if evidence.MissedAttestations == 0 {
		return fmt.Errorf("missed attestations must be greater than 0")
	}

	if len(evidence.MissedSlots) == 0 {
		return fmt.Errorf("missed slots must be provided")
	}

	return nil
}

func (se *SlashingEvidence) validateInvalidSignatureEvidence() error {
	evidence, ok := se.Evidence.(*InvalidSignatureEvidence)
	if !ok {
		return fmt.Errorf("invalid evidence type for invalid signature")
	}

	if len(evidence.Message) == 0 {
		return fmt.Errorf("message must be provided")
	}

	if len(evidence.Signature) == 0 {
		return fmt.Errorf("signature must be provided")
	}

	if len(evidence.PublicKey) == 0 {
		return fmt.Errorf("public key must be provided")
	}

	return nil
}

// Hash returns the hash of the evidence for deduplication
func (se *SlashingEvidence) Hash() string {
	data, _ := json.Marshal(se)
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash)
}

// M-2 FIX: Evidence lifecycle management

// MarkProcessed marks evidence as processed
func (se *SlashingEvidence) MarkProcessed() {
	se.ProcessedAt = time.Now().Unix()
}

// MarkSlashed marks that slashing was applied for this evidence
func (se *SlashingEvidence) MarkSlashed() {
	se.SlashingApplied = true
	if se.ProcessedAt == 0 {
		se.ProcessedAt = time.Now().Unix()
	}
}

// GetRetentionPeriod returns the retention period for this evidence type
func (se *SlashingEvidence) GetRetentionPeriod() time.Duration {
	switch se.Type {
	case EvidenceDoubleVoting, EvidenceSurroundVoting:
		// Critical offenses - keep longer
		return EvidenceRetentionCritical
	case EvidenceInvalidProposal, EvidenceInvalidSignature:
		// Standard offenses
		return EvidenceRetentionStandard
	case EvidenceDowntime:
		// Minor offenses - shorter retention
		return EvidenceRetentionMinor
	default:
		return EvidenceRetentionStandard
	}
}

// CanBePruned checks if evidence is old enough to be pruned
func (se *SlashingEvidence) CanBePruned() bool {
	// Evidence can only be pruned if:
	// 1. It has been processed
	// 2. It's past the retention period

	if se.ProcessedAt == 0 {
		// Not yet processed - use creation timestamp
		return false
	}

	retentionPeriod := se.GetRetentionPeriod()
	expiryTime := se.ProcessedAt + int64(retentionPeriod.Seconds())

	return time.Now().Unix() > expiryTime
}

// Age returns how old the evidence is
func (se *SlashingEvidence) Age() time.Duration {
	return time.Since(time.Unix(se.Timestamp, 0))
}

// IsExpired checks if evidence has exceeded MaxEvidenceAge
func (se *SlashingEvidence) IsExpired() bool {
	return se.Age() > MaxEvidenceAge
}
