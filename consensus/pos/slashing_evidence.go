// consensus/pos/slashing_evidence.go
// Slashing evidence types and broadcasting system

package pos

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

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
)

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
	}

	// Generate unique ID from content
	se.ID = se.generateID()

	return se
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
func (se *SlashingEvidence) Validate() error {
	if se.ID == "" {
		// Auto-generate if missing
		se.ID = se.generateID()
	}

	// Verify ID consistency
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

	// Check timestamp is not too old or in future
	now := time.Now().Unix()
	if se.Timestamp > now+300 { // 5 minutes future tolerance
		return fmt.Errorf("evidence timestamp is in the future")
	}

	// [FIX M-02] Enforce strict staleness check (e.g., 7 days)
	// This prevents processing evidence from ancient history or different chains
	const MaxEvidenceAge = 86400 * 7 // 7 days
	if se.Timestamp < now-MaxEvidenceAge {
		return fmt.Errorf("evidence is too old (stale): %d seconds", now-se.Timestamp)
	}

	// Validate type-specific evidence
	return se.validateTypeSpecific()
}

// validateTypeSpecific validates evidence based on its type
func (se *SlashingEvidence) validateTypeSpecific() error {
	switch se.Type {
	case EvidenceDoubleVoting:
		return se.validateDoubleVoteEvidence()
	case EvidenceSurroundVoting:
		return se.validateSurroundVoteEvidence()
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

func (se *SlashingEvidence) validateDoubleVoteEvidence() error {
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

	// Must be for same slot
	if evidence.Attestation1.Slot != evidence.Attestation2.Slot {
		return fmt.Errorf("attestations for different slots")
	}

	// Must be for different blocks
	if evidence.Attestation1.BlockHash == evidence.Attestation2.BlockHash {
		return fmt.Errorf("attestations for same block (not double voting)")
	}

	return nil
}

func (se *SlashingEvidence) validateSurroundVoteEvidence() error {
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
