// consensus/pos/types.go
// Common types and structures for Proof of Stake consensus

package pos

import (
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/consensus/validator"
	"github.com/thrylos-labs/go-thrylos/core/chain" // Import chain package
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/crypto"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/types"
)

// Vote represents a validator's vote in fork choice
type Vote struct {
	ValidatorAddress string `json:"validator_address"`
	SourceBlockHash  string `json:"source_block_hash"`
	TargetBlockHash  string `json:"target_block_hash"`
	SourceEpoch      uint64 `json:"source_epoch"`
	TargetEpoch      uint64 `json:"target_epoch"`
	Signature        []byte `json:"signature"`
}

// ValidatorActivity tracks a validator's proposal performance
type ValidatorActivity struct {
	LastProposal    time.Time
	MissedProposals uint64 // Consecutive missed proposals
}

// Checkpoint represents a justified or finalized checkpoint
type Checkpoint struct {
	Epoch          uint64 `json:"epoch"`
	BlockHash      string `json:"block_hash"`
	Timestamp      int64  `json:"timestamp"`
	AttestingStake int64  `json:"attesting_stake"` // Total stake that attested to this checkpoint
	TotalStake     int64  `json:"total_stake"`     // Total active stake at time of checkpoint
}

// ProposalSlot represents a slot where a validator should propose
type ProposalSlot struct {
	Slot             uint64 `json:"slot"`
	ValidatorAddress string `json:"validator_address"`
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

// ConsensusEngine implements Proof of Stake consensus
type ConsensusEngine struct {
	// Configuration
	config *config.Config

	// Chain reference for Reorgs
	blockchain *chain.Blockchain

	// Validator management
	validatorManager *validator.Manager
	validatorSet     *validator.Set

	// State management
	worldState *state.WorldState

	// Consensus state
	currentEpoch     uint64
	currentSlot      uint64
	proposalTimeout  time.Duration
	attestationPhase time.Duration

	// Block production
	blockProposer  *BlockProposer
	blockValidator *BlockValidator

	// Attestations and votes
	attestations map[string]*types.Attestation
	votes        map[string]*Vote

	// Fork choice
	forkChoice *ForkChoice

	// Network communication
	broadcastChan chan interface{}
	receiveChan   chan interface{}

	// Synchronization
	mu sync.RWMutex

	// Node identity
	nodePrivateKey crypto.PrivateKey
	nodeAddress    string

	// Metrics
	blocksProposed   uint64
	blocksMissed     uint64
	attestationsMade uint64

	chainCache *ChainCache

	slashingManager *SlashingManager

	evidenceTracker *EvidenceTracker

	validatorActivity map[string]*ValidatorActivity

	// Time synchronization and drift monitoring
	timeValidator *TimeValidator
}

// ForkChoice implements fork choice with memory management
type ForkChoice struct {
	config          *config.Config
	fcConfig        *ForkChoiceConfig
	worldState      WorldStateReader
	slashingManager *SlashingManager

	// Core consensus data
	blockScores           map[string]int64 // blockHash -> total attesting stake
	attestationsByBlock   map[string][]*types.Attestation
	validatorAttestations map[string]map[string]bool  // blockHash -> validatorAddress -> hasAttested
	epochAttestations     map[uint64]map[string]int64 // epoch -> blockHash -> totalStake
	blockEpochMap         map[string]uint64           // blockHash -> epoch (for cleanup)

	// Checkpoints for finality
	justifiedCheckpoint *Checkpoint
	finalizedCheckpoint *Checkpoint

	// Performance optimizations
	totalActiveStake     int64
	totalActiveStakeTime time.Time

	// Metrics
	metrics *ForkChoiceMetrics

	mu sync.RWMutex
}
