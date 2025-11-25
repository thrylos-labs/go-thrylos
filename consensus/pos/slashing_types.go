package pos

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// SlashingCondition represents different types of slashable offenses
type SlashingCondition int

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
)

// SlashingConfig defines the penalties for each slashing condition
type SlashingConfig struct {
	// Penalty percentages (0-100)
	DoubleVotingPenalty     uint8 // Default: 50%
	SurroundVotingPenalty   uint8 // Default: 30%
	InvalidProposalPenalty  uint8 // Default: 20%
	DowntimePenalty         uint8 // Default: 5%
	InvalidSignaturePenalty uint8 // Default: 10%

	// Downtime configuration
	MaxMissedAttestations uint64        // Default: 100
	AttestationWindow     time.Duration // Default: 24 hours

	// Slashing jail time (time before validator can rejoin)
	JailDuration time.Duration // Default: 7 days

	// Minimum stake required to be a validator
	MinimumStake int64 // Default: 1000 tokens
}

// DefaultSlashingConfig returns sensible default configuration
func DefaultSlashingConfig() *SlashingConfig {
	return &SlashingConfig{
		DoubleVotingPenalty:     50,
		SurroundVotingPenalty:   30,
		InvalidProposalPenalty:  20,
		DowntimePenalty:         5,
		InvalidSignaturePenalty: 10,
		MaxMissedAttestations:   100,
		AttestationWindow:       24 * time.Hour,
		JailDuration:            7 * 24 * time.Hour,
		MinimumStake:            1000,
	}
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

// SlashingEvidence contains the proof for a slashing event
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

// ValidatorStatus represents the current status of a validator
type ValidatorStatus int

const (
	ValidatorActive ValidatorStatus = iota
	ValidatorJailed
	ValidatorSlashed
	ValidatorExited
)

// JailedValidator tracks validators that are temporarily jailed
type JailedValidator struct {
	ValidatorAddress string
	JailTime         time.Time
	ReleaseTime      time.Time
	Reason           SlashingCondition
}

// AttestationHistory tracks validator attestations for downtime detection
type AttestationHistory struct {
	ValidatorAddress string
	TotalSlots       uint64
	MissedSlots      uint64
	LastAttestation  time.Time
	MissedSlotList   []uint64
	mu               sync.RWMutex
}

// RecordAttestation records that a validator attested at a slot
func (ah *AttestationHistory) RecordAttestation(slot uint64) {
	ah.mu.Lock()
	defer ah.mu.Unlock()

	ah.TotalSlots++
	ah.LastAttestation = time.Now()
}

// RecordMiss records that a validator missed a slot
func (ah *AttestationHistory) RecordMiss(slot uint64) {
	ah.mu.Lock()
	defer ah.mu.Unlock()

	ah.TotalSlots++
	ah.MissedSlots++
	ah.MissedSlotList = append(ah.MissedSlotList, slot)

	// Keep only last 1000 missed slots in memory
	if len(ah.MissedSlotList) > 1000 {
		ah.MissedSlotList = ah.MissedSlotList[len(ah.MissedSlotList)-1000:]
	}
}

// GetMissRate returns the percentage of missed attestations
func (ah *AttestationHistory) GetMissRate() float64 {
	ah.mu.RLock()
	defer ah.mu.RUnlock()

	if ah.TotalSlots == 0 {
		return 0
	}
	return float64(ah.MissedSlots) / float64(ah.TotalSlots) * 100
}

// AttestationRecord represents a single attestation for comparison
type AttestationRecord struct {
	ValidatorAddress string
	Epoch            uint64
	BlockHash        string
	Signature        []byte
	Timestamp        time.Time
}

// Conflicts checks if this attestation conflicts with another (double vote)
func (ar *AttestationRecord) Conflicts(other *AttestationRecord) bool {
	// Two attestations conflict if they have the same epoch
	// but different block hashes
	return ar.Epoch == other.Epoch && ar.BlockHash != other.BlockHash
}

// IsSurroundedBy checks if this attestation is surrounded by another
// Note: Since your Attestation only has Epoch (not source/target),
// surround voting detection is simplified or may need Vote struct instead
func (ar *AttestationRecord) IsSurroundedBy(other *AttestationRecord) bool {
	// For simplified surround detection with single epoch
	// This would need to be enhanced with Vote struct for full Casper FFG
	return false // Placeholder - use Vote struct for proper surround detection
}
