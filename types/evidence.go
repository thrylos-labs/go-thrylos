package types

type EvidenceType int

const (
	EvidenceType_DOUBLE_SIGN EvidenceType = iota
	EvidenceType_DOWNTIME
	EvidenceType_FORK_CHOICE_VIOLATION
	EvidenceType_INVALID_ATTESTATION
	EvidenceType_MISSED_VRF_REVEAL // NEW
)

type Evidence struct {
	Type             EvidenceType
	ValidatorAddress string
	Slot             uint64
	Epoch            uint64
	Timestamp        int64
	Description      string
	Severity         int // 1-10
}
