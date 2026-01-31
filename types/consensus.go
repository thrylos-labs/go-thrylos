package types

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

const (
	// DoubleVoting: Validator votes for two different blocks at the same height
	DoubleVoting SlashingCondition = iota
	// SurroundVoting: Validator's attestation surrounds another attestation
	SurroundVoting
	// InvalidProposal: Validator proposes an invalid block
	InvalidProposal
	// Downtime: Validator is offline for extended period
	Downtime
	// InvalidSignature: Validator signs with incorrect key or malformed signature
	InvalidSignature

	MissedVRFReveal
)

// Attestation represents a validator's vote on a block
type Attestation struct {
	ValidatorAddress string `json:"validator_address"`
	BlockHash        string `json:"block_hash"`
	BlockHeight      int64  `json:"block_height"`
	Epoch            uint64 `json:"epoch"`
	Slot             uint64 `json:"slot"`
	Signature        []byte `json:"signature"`
	Timestamp        int64  `json:"timestamp"`
}

// BlockProposal represents a block proposal message
type BlockProposal struct {
	Block     *core.Block `json:"block"`
	Proposer  string      `json:"proposer"`
	Slot      uint64      `json:"slot"`
	Epoch     uint64      `json:"epoch"`
	Signature []byte      `json:"signature"`
}

// Vote represents a validator's vote in fork choice
type Vote struct {
	ValidatorAddress string `json:"validator_address"`
	SourceBlockHash  string `json:"source_block_hash"`
	TargetBlockHash  string `json:"target_block_hash"`
	SourceEpoch      uint64 `json:"source_epoch"`
	TargetEpoch      uint64 `json:"target_epoch"`
	Signature        []byte `json:"signature"`
}

// SlashingCondition represents different types of slashable offenses
type SlashingCondition int

type SlashingEvidence struct {
	// For double voting
	FirstAttestation  *Attestation
	SecondAttestation *Attestation

	// For surround voting
	SurroundingAttestation *Attestation
	SurroundedAttestation  *Attestation

	// For invalid proposals
	InvalidBlock *BlockProposal

	// For downtime
	MissedSlots []uint64

	// For invalid signatures
	ExpectedKey []byte
	ActualKey   []byte
	Signature   []byte
}

// SlashingRecord represents a single slashing event
type SlashingRecord struct {
	ValidatorAddress string
	Condition        SlashingCondition
	Epoch            uint64
	Timestamp        time.Time
	Evidence         SlashingEvidence
	SlashedAmount    int64
	Reason           string
}

// Hash returns a unique hash of the slashing evidence
func (e *SlashingEvidence) Hash() string {
	h := sha256.New()

	if e.FirstAttestation != nil {
		h.Write([]byte(fmt.Sprintf("%v", e.FirstAttestation)))
	}
	if e.SecondAttestation != nil {
		h.Write([]byte(fmt.Sprintf("%v", e.SecondAttestation)))
	}
	if e.SurroundingAttestation != nil {
		h.Write([]byte(fmt.Sprintf("%v", e.SurroundingAttestation)))
	}
	if e.SurroundedAttestation != nil {
		h.Write([]byte(fmt.Sprintf("%v", e.SurroundedAttestation)))
	}
	if e.InvalidBlock != nil {
		h.Write([]byte(fmt.Sprintf("%v", e.InvalidBlock)))
	}

	return hex.EncodeToString(h.Sum(nil))
}

// In package types (e.g., types/slashing.go)

// String implements the fmt.Stringer interface for readable logging
func (sc SlashingCondition) String() string {
	switch sc {
	case DoubleVoting:
		return "DoubleVoting"
	case SurroundVoting:
		return "SurroundVoting"
	case InvalidProposal:
		return "InvalidProposal"
	case Downtime:
		return "Downtime"
	case InvalidSignature:
		return "InvalidSignature"
	case MissedVRFReveal:
		return "MissedVRFReveal" // ✅ NEW: Add this
	default:
		return fmt.Sprintf("UnknownCondition(%d)", int(sc))
	}
}
