package types

// UnbondingEntry represents tokens that are being unstaked
type UnbondingEntry struct {
	DelegatorAddr  string `json:"delegator_addr"`
	ValidatorAddr  string `json:"validator_addr"`
	Amount         string `json:"amount"`
	CreationTime   int64  `json:"creation_time"`   // Unix timestamp
	CompletionTime int64  `json:"completion_time"` // When funds will be released
}
