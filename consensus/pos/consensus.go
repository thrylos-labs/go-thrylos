// Proof of Stake consensus implementation for Thrylos blockchain
// Features:
// - Validator selection based on stake weight and randomness
// - Block proposal and validation with economic incentives
// - Slashing conditions for double signing and downtime
// - Dynamic validator set management with rotation
// - Cross-shard consensus coordination via beacon chain
// - Fork choice rule based on validator attestations
// - Economic finality with stake-based voting

package pos

import (
	"encoding/binary"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/consensus/validator"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/crypto"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"golang.org/x/crypto/blake2b"
)

// ConsensusEngine implements Proof of Stake consensus
type ConsensusEngine struct {
	// Configuration
	config *config.Config

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
	attestations map[string]*Attestation
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
}

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

// Vote represents a validator's vote in fork choice
type Vote struct {
	ValidatorAddress string `json:"validator_address"`
	SourceBlockHash  string `json:"source_block_hash"`
	TargetBlockHash  string `json:"target_block_hash"`
	SourceEpoch      uint64 `json:"source_epoch"`
	TargetEpoch      uint64 `json:"target_epoch"`
	Signature        []byte `json:"signature"`
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

// NewConsensusEngine creates a new PoS consensus engine
func NewConsensusEngine(
	cfg *config.Config,
	worldState *state.WorldState,
	nodePrivateKey crypto.PrivateKey,
	broadcastChan chan interface{},
	receiveChan chan interface{},
) *ConsensusEngine {

	nodeAddress, _ := account.GenerateAddress(nodePrivateKey.PublicKey())

	engine := &ConsensusEngine{
		config:           cfg,
		worldState:       worldState,
		nodePrivateKey:   nodePrivateKey,
		nodeAddress:      nodeAddress,
		broadcastChan:    broadcastChan,
		receiveChan:      receiveChan,
		proposalTimeout:  time.Duration(cfg.Consensus.BlockTime),
		attestationPhase: time.Duration(cfg.Consensus.BlockTime) / 3,
		attestations:     make(map[string]*Attestation),
		votes:            make(map[string]*Vote),
		currentEpoch:     0,
		currentSlot:      0,
	}

	// Initialize validator management
	engine.validatorManager = validator.NewManager(cfg, worldState)
	engine.validatorSet = validator.NewSet(cfg.Consensus.MaxValidators)

	// Initialize block production and validation
	engine.blockProposer = NewBlockProposer(cfg, worldState, nodeAddress)
	engine.blockValidator = NewBlockValidator(engine)

	// Initialize fork choice
	engine.forkChoice = NewForkChoice(cfg)

	return engine
}

// Start begins the consensus process
func (ce *ConsensusEngine) Start() error {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	// Initialize validator set from world state
	if err := ce.initializeValidatorSet(); err != nil {
		return fmt.Errorf("failed to initialize validator set: %v", err)
	}

	// Start consensus loop
	go ce.consensusLoop()

	// Start message processing
	go ce.messageHandler()

	return nil
}

// Stop halts the consensus process
func (ce *ConsensusEngine) Stop() error {
	// Implementation would gracefully stop all goroutines
	return nil
}

// consensusLoop runs the main consensus algorithm
func (ce *ConsensusEngine) consensusLoop() {
	slotTicker := time.NewTicker(ce.proposalTimeout)
	defer slotTicker.Stop()

	for {
		select {
		case <-slotTicker.C:
			ce.processSlot()
		}
	}
}

// processSlot handles consensus for a single slot
func (ce *ConsensusEngine) processSlot() {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	ce.currentSlot++

	// Calculate epoch (32 slots per epoch)
	ce.currentEpoch = ce.currentSlot / 32

	// Get the proposer for this slot
	proposer, err := ce.getSlotProposer(ce.currentSlot)
	if err != nil {
		fmt.Printf("Failed to get slot proposer: %v\n", err)
		return
	}

	// If we are the proposer, create and broadcast block
	if proposer == ce.nodeAddress {
		if err := ce.proposeBlock(); err != nil {
			fmt.Printf("Failed to propose block: %v\n", err)
			ce.blocksMissed++
		} else {
			ce.blocksProposed++
		}
	}

	// Always create attestation if we're a validator
	if ce.isValidator() {
		if err := ce.createAttestation(); err != nil {
			fmt.Printf("Failed to create attestation: %v\n", err)
		} else {
			ce.attestationsMade++
		}
	}

	// Process any received attestations
	ce.processAttestations()

	// Update fork choice
	ce.updateForkChoice()
}

// proposeBlock creates and broadcasts a new block proposal
func (ce *ConsensusEngine) proposeBlock() error {
	// Use the dedicated block proposer
	result, err := ce.blockProposer.ProposeBlock(ce.currentSlot, ce.currentEpoch)
	if err != nil {
		return fmt.Errorf("failed to create block: %v", err)
	}

	// Validate our own block
	if err := ce.blockValidator.ValidateBlock(result.Block); err != nil {
		return fmt.Errorf("block validation failed: %v", err)
	}

	// Add block to world state
	if err := ce.worldState.AddBlock(result.Block); err != nil {
		return fmt.Errorf("failed to add block to world state: %v", err)
	}

	// Broadcast block
	ce.broadcastChan <- &BlockProposal{
		Block:     result.Block,
		Proposer:  ce.nodeAddress,
		Slot:      ce.currentSlot,
		Epoch:     ce.currentEpoch,
		Signature: nil, // Would be signed in production
	}

	// Log block construction metrics
	fmt.Printf("Proposed block %s by validator %s with %d txs, gas: %d, fees: %d, construction time: %v, score: %.2f\n",
		result.Block.Hash,
		result.Block.Header.Validator,
		result.TransactionCount,
		result.TotalGasUsed,
		result.TotalFees,
		result.ConstructionTime,
		result.OptimizationScore)

	return nil
}

// createAttestation creates an attestation for the current head
func (ce *ConsensusEngine) createAttestation() error {
	currentHead := ce.worldState.GetCurrentBlock()
	if currentHead == nil {
		return fmt.Errorf("no current head block")
	}

	attestation := &Attestation{
		ValidatorAddress: ce.nodeAddress,
		BlockHash:        currentHead.Hash,
		BlockHeight:      currentHead.Header.Index,
		Epoch:            ce.currentEpoch,
		Slot:             ce.currentSlot,
		Timestamp:        time.Now().Unix(),
	}

	// Sign attestation
	signature, err := ce.signAttestation(attestation)
	if err != nil {
		return fmt.Errorf("failed to sign attestation: %v", err)
	}
	attestation.Signature = signature

	// Store attestation
	attestationKey := fmt.Sprintf("%s-%d", ce.nodeAddress, ce.currentSlot)
	ce.attestations[attestationKey] = attestation

	// Broadcast attestation
	ce.broadcastChan <- attestation

	return nil
}

// getSlotProposer determines which validator should propose for a given slot
func (ce *ConsensusEngine) getSlotProposer(slot uint64) (string, error) {
	activeValidators := ce.validatorSet.GetActiveValidators()
	if len(activeValidators) == 0 {
		return "", fmt.Errorf("no active validators")
	}

	// Use deterministic randomness based on slot and previous block hash
	seed := ce.getRandomnessSeed(slot)

	// Select validator based on stake-weighted randomness
	selectedValidator, err := ce.selectValidatorByStake(activeValidators, seed)
	if err != nil {
		return "", fmt.Errorf("failed to select validator: %v", err)
	}

	return selectedValidator.Address, nil
}

// selectValidatorByStake selects a validator based on stake weight and randomness
func (ce *ConsensusEngine) selectValidatorByStake(validators []*core.Validator, seed []byte) (*core.Validator, error) {
	if len(validators) == 0 {
		return nil, fmt.Errorf("no validators provided")
	}

	// Calculate total stake
	totalStake := int64(0)
	for _, v := range validators {
		totalStake += v.Stake
	}

	if totalStake == 0 {
		return nil, fmt.Errorf("total stake is zero")
	}

	// Generate random number from seed
	seedInt := new(big.Int).SetBytes(seed)
	maxInt := big.NewInt(totalStake)
	randomStake := new(big.Int).Mod(seedInt, maxInt).Int64()

	// Select validator based on cumulative stake
	cumulativeStake := int64(0)
	for _, validator := range validators {
		cumulativeStake += validator.Stake
		if randomStake < cumulativeStake {
			return validator, nil
		}
	}

	// Fallback to last validator (should not happen)
	return validators[len(validators)-1], nil
}

// getRandomnessSeed generates deterministic randomness for validator selection
func (ce *ConsensusEngine) getRandomnessSeed(slot uint64) []byte {
	currentBlock := ce.worldState.GetCurrentBlock()

	var prevHash []byte
	if currentBlock != nil {
		prevHash = []byte(currentBlock.Hash)
	}

	// Combine slot number and previous block hash
	slotBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(slotBytes, slot)

	combined := append(slotBytes, prevHash...)
	hash := blake2b.Sum256(combined)

	return hash[:]
}

// isValidator checks if this node is an active validator
func (ce *ConsensusEngine) isValidator() bool {
	validator, err := ce.worldState.GetValidator(ce.nodeAddress)
	if err != nil {
		return false
	}
	return validator.Active
}

// processAttestations processes received attestations
func (ce *ConsensusEngine) processAttestations() {
	// Process attestations for fork choice and finality
	for _, attestation := range ce.attestations {
		if err := ce.validateAttestation(attestation); err != nil {
			continue
		}

		// Add to fork choice
		ce.forkChoice.ProcessAttestation(attestation)
	}
}

// validateAttestation validates an attestation
func (ce *ConsensusEngine) validateAttestation(attestation *Attestation) error {
	// Check validator exists and is active
	validator, err := ce.worldState.GetValidator(attestation.ValidatorAddress)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	if !validator.Active {
		return fmt.Errorf("validator not active")
	}

	// Verify signature
	if err := ce.verifyAttestationSignature(attestation); err != nil {
		return fmt.Errorf("invalid signature: %v", err)
	}

	// Check timing constraints
	currentTime := time.Now().Unix()
	if currentTime-attestation.Timestamp > int64(ce.config.Consensus.MaxTimestampAge.Seconds()) {
		return fmt.Errorf("attestation too old")
	}

	return nil
}

// updateForkChoice updates the fork choice rule
func (ce *ConsensusEngine) updateForkChoice() {
	head := ce.forkChoice.GetHead()
	currentHead := ce.worldState.GetCurrentBlock()

	// If fork choice suggests a different head, reorganize
	if currentHead == nil || head != currentHead.Hash {
		// Implementation would handle chain reorganization
		fmt.Printf("Fork choice suggests new head: %s\n", head)
	}
}

// signAttestation signs an attestation with the node's private key
func (ce *ConsensusEngine) signAttestation(attestation *Attestation) ([]byte, error) {
	// Create attestation hash
	data := fmt.Sprintf("%s%s%d%d%d%d",
		attestation.ValidatorAddress,
		attestation.BlockHash,
		attestation.BlockHeight,
		attestation.Epoch,
		attestation.Slot,
		attestation.Timestamp)

	hash := blake2b.Sum256([]byte(data))

	// Sign with private key - your Sign method returns only Signature, not (Signature, error)
	signature := ce.nodePrivateKey.Sign(hash[:])
	if signature == nil {
		return nil, fmt.Errorf("failed to sign attestation: signature is nil")
	}

	return signature.Bytes(), nil
}

// verifyAttestationSignature verifies an attestation signature
func (ce *ConsensusEngine) verifyAttestationSignature(attestation *Attestation) error {
	// Get validator's public key
	validator, err := ce.worldState.GetValidator(attestation.ValidatorAddress)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	// Recreate the signed data
	data := fmt.Sprintf("%s%s%d%d%d%d",
		attestation.ValidatorAddress,
		attestation.BlockHash,
		attestation.BlockHeight,
		attestation.Epoch,
		attestation.Slot,
		attestation.Timestamp)

	hash := blake2b.Sum256([]byte(data))

	// Verify signature (implementation would use actual crypto verification)
	_ = validator.Pubkey
	_ = hash
	_ = attestation.Signature

	return nil // Placeholder - would implement actual verification
}

// initializeValidatorSet initializes the validator set from world state
func (ce *ConsensusEngine) initializeValidatorSet() error {
	activeValidators := ce.worldState.GetActiveValidators()

	for _, validator := range activeValidators {
		if err := ce.validatorSet.AddValidator(validator); err != nil {
			return fmt.Errorf("failed to add validator %s: %v", validator.Address, err)
		}
	}

	return nil
}

// messageHandler processes incoming consensus messages
func (ce *ConsensusEngine) messageHandler() {
	for msg := range ce.receiveChan {
		switch m := msg.(type) {
		case *BlockProposal:
			ce.handleBlockProposal(m)
		case *Attestation:
			ce.handleAttestation(m)
		case *Vote:
			ce.handleVote(m)
		}
	}
}

// handleBlockProposal processes a received block proposal
func (ce *ConsensusEngine) handleBlockProposal(proposal *BlockProposal) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	// Validate the block
	if err := ce.blockValidator.ValidateBlock(proposal.Block); err != nil {
		fmt.Printf("Invalid block proposal: %v\n", err)
		return
	}

	// Check if proposer is correct for this slot
	expectedProposer, err := ce.getSlotProposer(proposal.Slot)
	if err != nil || expectedProposer != proposal.Proposer {
		fmt.Printf("Invalid proposer for slot %d\n", proposal.Slot)
		return
	}

	// Add block to world state
	if err := ce.worldState.AddBlock(proposal.Block); err != nil {
		fmt.Printf("Failed to add block: %v\n", err)
		return
	}

	fmt.Printf("Accepted block %s from validator %s\n", proposal.Block.Hash, proposal.Proposer)
}

// handleAttestation processes a received attestation
func (ce *ConsensusEngine) handleAttestation(attestation *Attestation) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	if err := ce.validateAttestation(attestation); err != nil {
		fmt.Printf("Invalid attestation: %v\n", err)
		return
	}

	// Store attestation
	key := fmt.Sprintf("%s-%d", attestation.ValidatorAddress, attestation.Slot)
	ce.attestations[key] = attestation

	// Process in fork choice
	ce.forkChoice.ProcessAttestation(attestation)
}

// handleVote processes a received vote
func (ce *ConsensusEngine) handleVote(vote *Vote) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	// Validate vote
	if err := ce.validateVote(vote); err != nil {
		fmt.Printf("Invalid vote: %v\n", err)
		return
	}

	// Store vote
	key := fmt.Sprintf("%s-%d", vote.ValidatorAddress, vote.TargetEpoch)
	ce.votes[key] = vote
}

// validateVote validates a vote
func (ce *ConsensusEngine) validateVote(vote *Vote) error {
	// Check validator exists and is active
	validator, err := ce.worldState.GetValidator(vote.ValidatorAddress)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	if !validator.Active {
		return fmt.Errorf("validator not active")
	}

	// Check epoch ordering
	if vote.TargetEpoch <= vote.SourceEpoch {
		return fmt.Errorf("invalid epoch ordering")
	}

	return nil
}

// GetStats returns consensus engine statistics
func (ce *ConsensusEngine) GetStats() map[string]interface{} {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	stats := map[string]interface{}{
		"current_epoch":     ce.currentEpoch,
		"current_slot":      ce.currentSlot,
		"blocks_proposed":   ce.blocksProposed,
		"blocks_missed":     ce.blocksMissed,
		"attestations_made": ce.attestationsMade,
		"is_validator":      ce.isValidator(),
		"validator_count":   ce.validatorSet.Size(),
		"active_validators": len(ce.worldState.GetActiveValidators()),
		"attestation_count": len(ce.attestations),
		"vote_count":        len(ce.votes),
	}

	// Add block proposer stats
	proposerStats := ce.blockProposer.GetProposerStats()
	stats["proposer_stats"] = proposerStats

	return stats
}

// GetCurrentEpoch returns the current epoch
func (ce *ConsensusEngine) GetCurrentEpoch() uint64 {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	return ce.currentEpoch
}

// GetCurrentSlot returns the current slot
func (ce *ConsensusEngine) GetCurrentSlot() uint64 {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	return ce.currentSlot
}

// GetValidatorSet returns the current validator set
func (ce *ConsensusEngine) GetValidatorSet() *validator.Set {
	return ce.validatorSet
}

// GetForkChoice returns the fork choice instance
func (ce *ConsensusEngine) GetForkChoice() *ForkChoice {
	return ce.forkChoice
}

// BlockValidator handles block validation
type BlockValidator struct {
	consensusEngine *ConsensusEngine
}

// NewBlockValidator creates a new block validator
func NewBlockValidator(engine *ConsensusEngine) *BlockValidator {
	return &BlockValidator{
		consensusEngine: engine,
	}
}

// ValidateBlock validates a block proposal
func (bv *BlockValidator) ValidateBlock(block *core.Block) error {
	if block == nil {
		return fmt.Errorf("block cannot be nil")
	}

	if block.Header == nil {
		return fmt.Errorf("block header cannot be nil")
	}

	// Validate basic structure
	if err := bv.validateBlockStructure(block); err != nil {
		return fmt.Errorf("block structure validation failed: %v", err)
	}

	// Validate block hash
	if err := bv.validateBlockHash(block); err != nil {
		return fmt.Errorf("block hash validation failed: %v", err)
	}

	// Validate transactions
	if err := bv.validateBlockTransactions(block); err != nil {
		return fmt.Errorf("block transactions validation failed: %v", err)
	}

	// Validate gas usage
	if err := bv.validateGasUsage(block); err != nil {
		return fmt.Errorf("gas usage validation failed: %v", err)
	}

	// Validate proposer
	if err := bv.validateProposer(block); err != nil {
		return fmt.Errorf("proposer validation failed: %v", err)
	}

	return nil
}

// validateBlockStructure validates the basic structure of a block
func (bv *BlockValidator) validateBlockStructure(block *core.Block) error {
	config := bv.consensusEngine.config

	// Check transaction count
	if len(block.Transactions) > config.Consensus.MaxTxPerBlock {
		return fmt.Errorf("block contains %d transactions, maximum allowed is %d",
			len(block.Transactions), config.Consensus.MaxTxPerBlock)
	}

	// Check timestamp
	currentTime := time.Now().Unix()
	if block.Header.Timestamp > currentTime+int64(config.Consensus.MaxTimestampSkew.Seconds()) {
		return fmt.Errorf("block timestamp too far in future")
	}

	if block.Header.Timestamp < currentTime-int64(config.Consensus.MaxTimestampAge.Seconds()) {
		return fmt.Errorf("block timestamp too old")
	}

	// Validate chain continuity
	currentBlock := bv.consensusEngine.worldState.GetCurrentBlock()
	if currentBlock != nil {
		if block.Header.Index != currentBlock.Header.Index+1 {
			return fmt.Errorf("invalid block index: expected %d, got %d",
				currentBlock.Header.Index+1, block.Header.Index)
		}

		if block.Header.PrevHash != currentBlock.Hash {
			return fmt.Errorf("invalid previous hash")
		}

		if block.Header.Timestamp <= currentBlock.Header.Timestamp {
			return fmt.Errorf("block timestamp must be greater than previous block")
		}
	}

	return nil
}

// validateBlockHash validates the block hash
func (bv *BlockValidator) validateBlockHash(block *core.Block) error {
	// Recalculate hash and compare using the proposer's method
	expectedHash := bv.consensusEngine.blockProposer.calculateBlockHash(block)

	if block.Hash != expectedHash {
		return fmt.Errorf("invalid block hash: expected %s, got %s", expectedHash, block.Hash)
	}

	return nil
}

// validateBlockTransactions validates all transactions in the block
func (bv *BlockValidator) validateBlockTransactions(block *core.Block) error {
	for i, tx := range block.Transactions {
		if err := bv.consensusEngine.worldState.ValidateTransaction(tx); err != nil {
			return fmt.Errorf("transaction %d validation failed: %v", i, err)
		}
	}

	return nil
}

// validateGasUsage validates the gas usage in the block
func (bv *BlockValidator) validateGasUsage(block *core.Block) error {
	config := bv.consensusEngine.config

	// Calculate total gas used
	totalGasUsed := int64(0)
	for _, tx := range block.Transactions {
		totalGasUsed += tx.Gas
	}

	// Check against header
	if block.Header.GasUsed != totalGasUsed {
		return fmt.Errorf("gas used mismatch: header says %d, calculated %d",
			block.Header.GasUsed, totalGasUsed)
	}

	// Check against limit
	if totalGasUsed > config.Consensus.MaxBlockSize {
		return fmt.Errorf("block gas usage %d exceeds limit %d",
			totalGasUsed, config.Consensus.MaxBlockSize)
	}

	return nil
}

// validateProposer validates that the proposer is authorized
func (bv *BlockValidator) validateProposer(block *core.Block) error {
	// Check if proposer is an active validator
	validator, err := bv.consensusEngine.worldState.GetValidator(block.Header.Validator)
	if err != nil {
		return fmt.Errorf("proposer %s is not a validator: %v", block.Header.Validator, err)
	}

	if !validator.Active {
		return fmt.Errorf("proposer %s is not active", block.Header.Validator)
	}

	// Check if proposer is jailed
	if validator.JailUntil > time.Now().Unix() {
		return fmt.Errorf("proposer %s is jailed until %d", block.Header.Validator, validator.JailUntil)
	}

	return nil
}

// ForkChoice implements the fork choice rule for consensus
type ForkChoice struct {
	config *config.Config

	// Block scores for fork choice
	blockScores map[string]int64

	// Attestation tracking
	attestationsByBlock map[string][]*Attestation

	// Current justified and finalized checkpoints
	justifiedCheckpoint *Checkpoint
	finalizedCheckpoint *Checkpoint

	mu sync.RWMutex
}

// Checkpoint represents a justified or finalized checkpoint
type Checkpoint struct {
	Epoch     uint64 `json:"epoch"`
	BlockHash string `json:"block_hash"`
	Timestamp int64  `json:"timestamp"`
}

// NewForkChoice creates a new fork choice instance
func NewForkChoice(config *config.Config) *ForkChoice {
	return &ForkChoice{
		config:              config,
		blockScores:         make(map[string]int64),
		attestationsByBlock: make(map[string][]*Attestation),
	}
}

// ProcessAttestation processes an attestation for fork choice
func (fc *ForkChoice) ProcessAttestation(attestation *Attestation) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	blockHash := attestation.BlockHash

	// Add attestation to block
	if fc.attestationsByBlock[blockHash] == nil {
		fc.attestationsByBlock[blockHash] = make([]*Attestation, 0)
	}
	fc.attestationsByBlock[blockHash] = append(fc.attestationsByBlock[blockHash], attestation)

	// Update block score (simplified - would use validator stake in production)
	fc.blockScores[blockHash]++
}

// GetHead returns the current head block according to fork choice
func (fc *ForkChoice) GetHead() string {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	if len(fc.blockScores) == 0 {
		return ""
	}

	// Find block with highest score
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

// UpdateJustifiedCheckpoint updates the justified checkpoint
func (fc *ForkChoice) UpdateJustifiedCheckpoint(epoch uint64, blockHash string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	fc.justifiedCheckpoint = &Checkpoint{
		Epoch:     epoch,
		BlockHash: blockHash,
		Timestamp: time.Now().Unix(),
	}
}

// UpdateFinalizedCheckpoint updates the finalized checkpoint
func (fc *ForkChoice) UpdateFinalizedCheckpoint(epoch uint64, blockHash string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	fc.finalizedCheckpoint = &Checkpoint{
		Epoch:     epoch,
		BlockHash: blockHash,
		Timestamp: time.Now().Unix(),
	}
}

// GetJustifiedCheckpoint returns the current justified checkpoint
func (fc *ForkChoice) GetJustifiedCheckpoint() *Checkpoint {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	if fc.justifiedCheckpoint == nil {
		return nil
	}

	// Return copy
	checkpoint := *fc.justifiedCheckpoint
	return &checkpoint
}

// GetFinalizedCheckpoint returns the current finalized checkpoint
func (fc *ForkChoice) GetFinalizedCheckpoint() *Checkpoint {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	if fc.finalizedCheckpoint == nil {
		return nil
	}

	// Return copy
	checkpoint := *fc.finalizedCheckpoint
	return &checkpoint
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
