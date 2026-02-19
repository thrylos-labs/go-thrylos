package p2p

// NetworkAttestation mirrors types.Attestation for P2P deserialization.
// JSON tags must match exactly.
type NetworkAttestation struct {
	ValidatorAddress string `json:"validator_address"`
	BlockHash        string `json:"block_hash"`
	BlockHeight      int64  `json:"block_height"`
	Epoch            uint64 `json:"epoch"`
	Slot             uint64 `json:"slot"`
	Signature        []byte `json:"signature"`
	Timestamp        int64  `json:"timestamp"`
}

// NetworkVote mirrors pos.Vote for P2P deserialization.
type NetworkVote struct {
	ValidatorAddress string `json:"validator_address"`
	SourceBlockHash  string `json:"source_block_hash"`
	TargetBlockHash  string `json:"target_block_hash"`
	SourceEpoch      uint64 `json:"source_epoch"`
	TargetEpoch      uint64 `json:"target_epoch"`
	Signature        []byte `json:"signature"`
}
