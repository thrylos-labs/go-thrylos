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
	"log"
	"math/big"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/consensus/validator"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/crypto"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
	"github.com/thrylos-labs/go-thrylos/types"
	"golang.org/x/crypto/blake2b"
)

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
		attestations:     make(map[string]*types.Attestation),
		votes:            make(map[string]*Vote),
		currentEpoch:     0,
		currentSlot:      0,
		chainCache:       NewChainCache(),
	}

	// Initialize validator management
	engine.validatorManager = validator.NewManager(cfg, worldState)
	engine.validatorSet = validator.NewSet(cfg.Consensus.MaxValidators)

	// Initialize block production and validation
	engine.blockProposer = NewBlockProposer(cfg, worldState, nodeAddress)
	engine.blockValidator = NewBlockValidator(engine)

	// Initialize fork choice
	engine.forkChoice = NewForkChoice(cfg, worldState, &SlashingManager{})

	// Initialize slashing manager with persistent storage
	slashingConfig := &storage.SlashingConfig{
		DoubleVotingPenalty:    uint8(cfg.Consensus.SlashingDoubleVote),
		SurroundVotingPenalty:  uint8(cfg.Consensus.SlashingSurroundVote),
		InvalidProposalPenalty: uint8(cfg.Consensus.SlashingInvalidProposal),

		// ✅ FIX 1: Use SlashingDowntime
		SlashingDowntime: uint8(cfg.Consensus.SlashingDowntime),

		InvalidSignaturePenalty: uint8(cfg.Consensus.SlashingInvalidSig),
		MaxMissedAttestations:   cfg.Consensus.MaxMissedAttestations,
		AttestationWindow:       24 * time.Hour,

		// ✅ FIX 2: Use JailDurationHours (and pass the int directly, no math needed here)
		JailDurationHours: cfg.Consensus.JailDurationHours,

		MinimumStake: cfg.Staking.MinValidatorStake,
	}

	// Create slashing storage if we have access to BadgerDB
	var slashingStorage *storage.SlashingStorage
	badgerDB := worldState.GetBadgerDB()

	if badgerDB != nil {
		slashingStorage = storage.NewSlashingStorage(badgerDB)
		log.Println("✅ Slashing persistence enabled")
	} else {
		slashingStorage = nil
		log.Println("⚠️ Slashing persistence disabled")
	}

	engine.slashingManager = NewSlashingManager(slashingConfig, worldState, slashingStorage)

	// ============================================================================
	// ADD THIS: Initialize evidence tracker for slashing evidence broadcasting
	// ============================================================================
	engine.evidenceTracker = NewEvidenceTracker()
	log.Println("✅ Slashing evidence tracker initialized")
	// ============================================================================

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

// In consensusLoop (around line 94-112)
func (ce *ConsensusEngine) consensusLoop() {
	slotTicker := time.NewTicker(ce.proposalTimeout)
	defer slotTicker.Stop()

	cleanupTicker := time.NewTicker(ce.proposalTimeout * 32)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-slotTicker.C:
			ce.processSlot()

		case <-cleanupTicker.C:
			// Cleanup old epoch data to prevent memory leaks
			ce.forkChoice.CleanupOldEpochs()

			// ADD THIS LINE:
			ce.cleanupChainCache()
		}
	}
}

func (ce *ConsensusEngine) ValidateBlock(block *core.Block) error {
	if ce.blockValidator == nil {
		return fmt.Errorf("block validator not initialized")
	}

	return ce.blockValidator.ValidateBlock(block)
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

	// ✅ SAFETY CHECK: Verify proposer is still active (not jailed since selection)
	if !ce.slashingManager.IsValidatorActive(proposer) {
		fmt.Printf("⚠️  Selected proposer %s is no longer active (jailed/slashed), skipping slot\n", proposer)
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
	if ce.isCurrentNodeValidator() {
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

	// ============================================================================
	// CHANGED SECTION - Sign the proposal before broadcasting
	// ============================================================================

	// Create the block proposal
	proposal := &BlockProposal{
		Block:     result.Block,
		Proposer:  ce.nodeAddress,
		Slot:      ce.currentSlot,
		Epoch:     ce.currentEpoch,
		Signature: nil, // Will be set by signBlockProposal
	}

	// Sign the proposal
	if err := ce.signBlockProposal(proposal); err != nil {
		return fmt.Errorf("failed to sign block proposal: %v", err)
	}

	// Broadcast block with signature
	ce.broadcastChan <- proposal

	// ============================================================================
	// END CHANGED SECTION
	// ============================================================================

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

	attestation := &types.Attestation{
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

	// ✅ CRITICAL FIX #3: Filter out jailed and slashed validators
	eligibleValidators := make([]*core.Validator, 0)
	for _, validator := range activeValidators {
		if ce.slashingManager.IsValidatorActive(validator.Address) {
			eligibleValidators = append(eligibleValidators, validator)
		}
	}

	// Check if we have any eligible validators
	if len(eligibleValidators) == 0 {
		return "", fmt.Errorf("no eligible validators (all are jailed or slashed)")
	}

	// Use deterministic randomness based on slot and previous block hash
	seed := ce.getRandomnessSeed(slot)

	// Select validator based on stake-weighted randomness from ELIGIBLE validators only
	selectedValidator, err := ce.selectValidatorByStake(eligibleValidators, seed)
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

// IsValidator implements the chain.ConsensusEngine interface
func (ce *ConsensusEngine) IsValidator(address string) bool {
	validator, err := ce.worldState.GetValidator(address)
	if err != nil {
		return false
	}
	return validator.Active
}

// Helper method for internal use
func (ce *ConsensusEngine) isCurrentNodeValidator() bool {
	return ce.IsValidator(ce.nodeAddress)
}

// processAttestations processes received attestations
// OLD CODE (remove the TODO comment and add broadcasting):
func (ce *ConsensusEngine) processAttestations() {
	for _, attestation := range ce.attestations {
		if err := ce.validateAttestation(attestation); err != nil {
			continue
		}

		// Check for slashable offenses
		if err := ce.slashingManager.ProcessAttestation(attestation); err != nil {
			// Slashing violation detected!
			fmt.Printf("🚨 SLASHING VIOLATION: Validator %s - %v\n",
				attestation.ValidatorAddress, err)

			// TODO: Broadcast slashing evidence to network  ← REMOVE THIS TODO

			// ✅ NEW: Create and broadcast slashing evidence
			evidence := ce.createSlashingEvidenceFromAttestation(attestation, err)
			if evidence != nil {
				if err := ce.handleSlashingEvidence(evidence); err != nil {
					log.Printf("❌ Failed to handle slashing evidence: %v", err)
				}
			}

			// Skip this attestation
			continue
		}

		// Add to fork choice (only if no slashing violation)
		ce.forkChoice.ProcessAttestation(attestation)
	}
}

// validateAttestation validates an attestation
func (ce *ConsensusEngine) validateAttestation(attestation *types.Attestation) error {
	// Check validator exists and is active
	validator, err := ce.worldState.GetValidator(attestation.ValidatorAddress)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	if !validator.Active {
		return fmt.Errorf("validator not active")
	}

	// ✅ CRITICAL FIX #1: Check if validator is jailed or slashed
	if !ce.slashingManager.IsValidatorActive(attestation.ValidatorAddress) {
		return fmt.Errorf("validator %s is jailed or slashed, cannot attest", attestation.ValidatorAddress)
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

// updateForkChoice updates the fork choice rule with safety checks
func (ce *ConsensusEngine) updateForkChoice() {
	head := ce.forkChoice.GetHead()
	if head == "" {
		return
	}

	currentHead := ce.worldState.GetCurrentBlock()

	// Check if the new head has achieved quorum
	hasQuorum := ce.forkChoice.HasQuorum(head)
	quorumPercentage := ce.forkChoice.GetQuorumPercentage(head)

	// Only switch to new head if:
	// 1. It has quorum (2/3+ stake), OR
	// 2. We have no current head
	if currentHead == nil {
		fmt.Printf("📍 Setting initial head: %s (%.1f%% stake)\n", head[:8], quorumPercentage)
		return
	}

	// Check if current head is finalized - never reorg past finalized blocks
	if ce.forkChoice.IsBlockFinalized(currentHead.Hash) {
		if head != currentHead.Hash && !ce.isDescendant(head, currentHead.Hash) {
			fmt.Printf("⚠️ Ignoring fork choice - current head is finalized\n")
			return
		}
	}

	// If fork choice suggests a different head
	if head != currentHead.Hash {
		if hasQuorum {
			fmt.Printf("🔀 Fork choice suggests new head: %s (%.1f%% stake, HAS QUORUM)\n",
				head[:8], quorumPercentage)
			// In production, would trigger chain reorganization here
		} else {
			fmt.Printf("⏳ Fork choice suggests %s but waiting for quorum (%.1f%% < 66.7%%)\n",
				head[:8], quorumPercentage)
		}
	}
}

// signAttestation signs an attestation with the node's private key
func (ce *ConsensusEngine) signAttestation(attestation *types.Attestation) ([]byte, error) {
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
		case *types.Attestation:
			ce.handleAttestation(m)
		case *Vote:
			ce.handleVote(m)
		// ADD THIS:
		case *SlashingEvidence:
			ce.processReceivedSlashingEvidence(m)
		}
	}
}

// handleBlockProposal processes a received block proposal
func (ce *ConsensusEngine) handleBlockProposal(proposal *BlockProposal) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	// ADD THIS: Verify signature
	if err := ce.verifyProposalSignature(proposal); err != nil {
		fmt.Printf("❌ Invalid signature: %v\n", err)
		return
	}

	// Validate the block
	// FIX: Remove .(*core.Block)
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
	// FIX: Remove .(*core.Block)
	if err := ce.worldState.AddBlock(proposal.Block); err != nil {
		fmt.Printf("Failed to add block: %v\n", err)
		return
	}

	// FIX: Remove .(*core.Block)
	fmt.Printf("Accepted block %s from validator %s\n", proposal.Block.Hash, proposal.Proposer)
}

// handleAttestation processes a received attestation
func (ce *ConsensusEngine) handleAttestation(attestation *types.Attestation) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	// ADD THIS: Verify signature
	if err := ce.verifyAttestationSignature(attestation); err != nil {
		fmt.Printf("❌ Invalid signature: %v\n", err)
		return
	}

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
		"is_validator":      ce.isCurrentNodeValidator(),
		"validator_count":   ce.validatorSet.Size(),
		"active_validators": len(ce.worldState.GetActiveValidators()),
		"attestation_count": len(ce.attestations),
		"vote_count":        len(ce.votes),
	}

	// Add block proposer stats
	proposerStats := ce.blockProposer.GetProposerStats()
	stats["proposer_stats"] = proposerStats

	// Add quorum and finality information
	currentBlock := ce.worldState.GetCurrentBlock()
	if currentBlock != nil {
		blockHash := currentBlock.Hash
		stats["current_block"] = blockHash[:8]
		stats["current_block_has_quorum"] = ce.forkChoice.HasQuorum(blockHash)
		stats["current_block_stake_percentage"] = ce.forkChoice.GetQuorumPercentage(blockHash)
		stats["current_block_attesting_stake"] = ce.forkChoice.GetAttestingStake(blockHash)
	}

	// Add justified checkpoint info
	justified := ce.forkChoice.GetJustifiedCheckpoint()
	if justified != nil {
		stats["justified_epoch"] = justified.Epoch
		stats["justified_block"] = justified.BlockHash[:8]
		stats["justified_stake_percentage"] = float64(justified.AttestingStake) / float64(justified.TotalStake) * 100
	}

	// Add finalized checkpoint info
	finalized := ce.forkChoice.GetFinalizedCheckpoint()
	if finalized != nil {
		stats["finalized_epoch"] = finalized.Epoch
		stats["finalized_block"] = finalized.BlockHash[:8]
		stats["finalized_stake_percentage"] = float64(finalized.AttestingStake) / float64(finalized.TotalStake) * 100
	}

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
	// Get config from the engine
	cfg := bv.consensusEngine.config.Consensus

	// Check transaction count
	if len(block.Transactions) > cfg.MaxTxPerBlock {
		return fmt.Errorf("block contains %d transactions, maximum allowed is %d",
			len(block.Transactions), cfg.MaxTxPerBlock)
	}

	// CHANGED: Use Config values for Timestamp Validation
	currentTime := time.Now().Unix()

	// 1. Future Check
	if block.Header.Timestamp > currentTime+int64(cfg.MaxFutureBlockTime.Seconds()) {
		return fmt.Errorf("block timestamp %d too far in future (max drift: %s)",
			block.Header.Timestamp, cfg.MaxFutureBlockTime)
	}

	// 2. Past Check
	if block.Header.Timestamp < currentTime-int64(cfg.MaxPastBlockTime.Seconds()) {
		return fmt.Errorf("block timestamp %d too old (max age: %s)",
			block.Header.Timestamp, cfg.MaxPastBlockTime)
	}

	// Validate chain continuity (Previous Block Check)
	currentBlock := bv.consensusEngine.worldState.GetCurrentBlock()
	if currentBlock != nil {
		if block.Header.Index != currentBlock.Header.Index+1 {
			return fmt.Errorf("invalid block index: expected %d, got %d",
				currentBlock.Header.Index+1, block.Header.Index)
		}

		if block.Header.PrevHash != currentBlock.Hash {
			return fmt.Errorf("invalid previous hash")
		}

		// 3. Monotonic Time Check
		if block.Header.Timestamp <= currentBlock.Header.Timestamp {
			return fmt.Errorf("block timestamp must be strictly greater than previous block")
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
