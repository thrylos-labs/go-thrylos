// consensus/pos/consensus.go
// Proof of Stake consensus implementation for Thrylos blockchain

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
	"github.com/thrylos-labs/go-thrylos/core/chain"
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
	blockchain *chain.Blockchain, // Added parameter
	worldState *state.WorldState,
	nodePrivateKey crypto.PrivateKey,
	broadcastChan chan interface{},
	receiveChan chan interface{},
) *ConsensusEngine {

	nodeAddress, _ := account.GenerateAddress(nodePrivateKey.PublicKey())

	engine := &ConsensusEngine{
		config:            cfg,
		blockchain:        blockchain, // Initialized field
		worldState:        worldState,
		nodePrivateKey:    nodePrivateKey,
		nodeAddress:       nodeAddress,
		broadcastChan:     broadcastChan,
		receiveChan:       receiveChan,
		proposalTimeout:   time.Duration(cfg.Consensus.BlockTime),
		attestationPhase:  time.Duration(cfg.Consensus.BlockTime) / 3,
		attestations:      make(map[string]*types.Attestation),
		votes:             make(map[string]*Vote),
		currentEpoch:      0,
		currentSlot:       0,
		chainCache:        NewChainCache(),
		validatorActivity: make(map[string]*ValidatorActivity),
	}

	// Initialize validator management
	engine.validatorManager = validator.NewManager(cfg, worldState)
	engine.validatorSet = validator.NewSet(cfg.Consensus.MaxValidators)

	// Initialize block production and validation
	engine.blockProposer = NewBlockProposer(cfg, worldState, nodeAddress)
	engine.blockValidator = NewBlockValidator(engine)

	// Configure and Initialize Fork Choice
	fcConfig := DefaultForkChoiceConfig()
	// Use config value if set, otherwise default is kept
	if cfg.Consensus.StakeCacheTTL > 0 {
		fcConfig.StakeCacheTTL = cfg.Consensus.StakeCacheTTL
	}

	// Pass SlashingManager placeholder, it will be overwritten shortly but needed for initialization order
	engine.forkChoice = NewForkChoiceWithConfig(cfg, worldState, &SlashingManager{}, fcConfig)

	// Initialize slashing manager with persistent storage
	slashingConfig := &storage.SlashingConfig{
		DoubleVotingPenalty:     uint8(cfg.Consensus.SlashingDoubleVote),
		SurroundVotingPenalty:   uint8(cfg.Consensus.SlashingSurroundVote),
		InvalidProposalPenalty:  uint8(cfg.Consensus.SlashingInvalidProposal),
		SlashingDowntime:        uint8(cfg.Consensus.SlashingDowntime),
		InvalidSignaturePenalty: uint8(cfg.Consensus.SlashingInvalidSig),
		MaxMissedAttestations:   cfg.Consensus.MaxMissedAttestations,
		AttestationWindow:       24 * time.Hour,
		JailDurationHours:       cfg.Consensus.JailDurationHours,
		MinimumStake:            cfg.Staking.MinValidatorStake,
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

	// Update fork choice with real slashing manager
	engine.forkChoice.slashingManager = engine.slashingManager

	// Initialize evidence tracker for slashing evidence broadcasting
	engine.evidenceTracker = NewEvidenceTracker()
	log.Println("✅ Slashing evidence tracker initialized")

	// Initialize time validator for timestamp validation and drift monitoring
	engine.timeValidator = NewTimeValidator()
	log.Println("✅ Time drift monitoring initialized")

	return engine
}

// ... [Include all other existing methods like Start, Stop, updateForkChoice, etc.] ...

// Start begins the consensus process
func (ce *ConsensusEngine) Start() error {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	// Initialize validator set from world state
	if err := ce.initializeValidatorSet(); err != nil {
		return fmt.Errorf("failed to initialize validator set: %v", err)
	}

	// Start time drift monitoring (critical for preventing time manipulation attacks)
	stopChan := make(chan struct{})
	go ce.timeValidator.StartDriftMonitoring(stopChan)
	log.Println("✅ Time drift monitoring started")

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

	// Check previous slot for withholding
	if ce.currentSlot > 0 {
		previousSlot := ce.currentSlot

		// 1. Get the expected proposer for the previous slot
		expectedProposer, err := ce.getSlotProposer(previousSlot)
		if err == nil {
			// 2. Check if a block was actually produced for that slot
			currentBlock := ce.worldState.GetCurrentBlock()
			wasBlockProduced := currentBlock != nil && currentBlock.Header.Slot == previousSlot

			// 3. Update activity (Testable Logic)
			ce.updateValidatorActivity(expectedProposer, wasBlockProduced)
		}
	}

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

// updateForkChoice updates the fork choice rule with safety checks and Reorg logic
func (ce *ConsensusEngine) updateForkChoice() {
	head := ce.forkChoice.GetHead()
	if head == "" {
		return
	}

	// 1. Safety Check: Is this new head viable? (Descends from finalized checkpoint)
	if !ce.forkChoice.IsViableChain(head) {
		fmt.Printf("⚠️ Fork choice rejected head %s: violates finality\n", head[:8])
		return
	}

	currentHead := ce.worldState.GetCurrentBlock()

	// 2. If we have no current head (genesis), accept it
	if currentHead == nil {
		fmt.Printf("📍 Setting initial head: %s\n", head[:8])
		return
	}

	// 3. If fork choice suggests a different head
	if head != currentHead.Hash {
		hasQuorum := ce.forkChoice.HasQuorum(head)
		quorumPercentage := ce.forkChoice.GetQuorumPercentage(head)

		if hasQuorum {
			fmt.Printf("🔀 Fork choice suggests new head: %s (HAS QUORUM). Triggering Reorg Logic.\n", head[:8])

			// --- REAL IMPLEMENTATION START ---

			// A. Find Common Ancestor between new head and current chain
			ancestorHash, err := ce.getCommonAncestor(head, currentHead.Hash)
			if err != nil {
				log.Printf("❌ Reorg failed: could not find common ancestor between %s and %s: %v", head[:8], currentHead.Hash[:8], err)
				return
			}

			// B. Get path of hashes from new head back to (but excluding) Common Ancestor
			// getChainPath returns [head, parent, ..., ancestor+1, ancestor]
			hashPath, err := ce.getChainPath(head, ancestorHash)
			if err != nil {
				log.Printf("❌ Reorg failed: could not calculate chain path: %v", err)
				return
			}

			// C. Convert Hashes to Blocks and Reverse Order
			// We need blocks ordered [Ancestor+1, ..., Head]
			var newBlocks []*core.Block

			// Iterate backwards from len-2 (skipping the ancestor at the end) down to 0
			for i := len(hashPath) - 2; i >= 0; i-- {
				blockHash := hashPath[i]

				// Try fetching from WorldState first
				block, err := ce.worldState.GetBlockByHash(blockHash)
				if err != nil || block == nil {
					log.Printf("❌ Reorg failed: block %s data not found", blockHash[:8])
					return
				}
				newBlocks = append(newBlocks, block)
			}

			// D. Execute Reorganization
			if len(newBlocks) > 0 {
				if ce.blockchain != nil {
					if err := ce.blockchain.ReorganizeChain(newBlocks); err != nil {
						// [FIX L-01] Critical Consensus Failure
						// If we fail to reorg to a valid quorum-backed chain, our state is now inconsistent
						// relative to the network. We must crash to trigger a restart/recovery.
						panic(fmt.Sprintf("❌ CRITICAL: Reorg failed during execution! Database may be corrupted or state inconsistent: %v", err))
					} else {
						log.Printf("✅ Successfully reorganized chain to new head %s", head[:8])
					}
				} else {
					log.Printf("⚠️ Blockchain reference not set, cannot execute reorg")
				}
			}
			// --- REAL IMPLEMENTATION END ---

		} else {
			fmt.Printf("⏳ Fork choice suggests %s but waiting for quorum (%.1f%% < 66.7%%)\n",
				head[:8], quorumPercentage)
		}
	}
}

func (ce *ConsensusEngine) updateValidatorActivity(validatorAddr string, wasBlockProduced bool) {
	if ce.validatorActivity == nil {
		ce.validatorActivity = make(map[string]*ValidatorActivity)
	}

	if ce.validatorActivity[validatorAddr] == nil {
		ce.validatorActivity[validatorAddr] = &ValidatorActivity{}
	}
	activity := ce.validatorActivity[validatorAddr]

	if wasBlockProduced {
		// ✅ SUCCESS: Reset missed count
		activity.MissedProposals = 0
		activity.LastProposal = time.Now()
	} else {
		// ❌ FAILURE: Increment missed count
		activity.MissedProposals++
		fmt.Printf("⚠️ Validator %s missed proposal (Consecutive: %d)\n",
			validatorAddr, activity.MissedProposals)

		// Trigger Slashing if Threshold Exceeded (10 misses)
		if activity.MissedProposals >= 10 {
			// Apply Penalty
			err := ce.slashingManager.ReportBlockWithholding(validatorAddr)
			if err != nil {
				fmt.Printf("Error reporting withholding: %v\n", err)
			} else {
				// Reset count after punishment to avoid looping penalties
				activity.MissedProposals = 0
			}
		}
	}
}

// proposeBlock creates and broadcast a new block proposal
func (ce *ConsensusEngine) proposeBlock() error {
	// Use the dedicated block proposer
	result, err := ce.blockProposer.ProposeBlock(ce.currentSlot, ce.currentEpoch)
	if err != nil {
		return fmt.Errorf("failed to create block: %v", err)
	}

	// 🔐 NEW: sign the block with this validator's key
	if err := ce.signBlock(result.Block); err != nil {
		return fmt.Errorf("failed to sign block: %v", err)
	}

	// Validate our own block (now includes signature checks, see below)
	if err := ce.blockValidator.ValidateBlock(result.Block); err != nil {
		return fmt.Errorf("block validation failed: %v", err)
	}

	// Add block to world state
	if err := ce.worldState.AddBlock(result.Block); err != nil {
		return fmt.Errorf("failed to add block to world state: %v", err)
	}

	// --- existing proposal-signing + broadcast logic stays as-is ---
	proposal := &BlockProposal{
		Block:     result.Block,
		Proposer:  ce.nodeAddress,
		Slot:      ce.currentSlot,
		Epoch:     ce.currentEpoch,
		Signature: nil,
	}

	if err := ce.signBlockProposal(proposal); err != nil {
		return fmt.Errorf("failed to sign block proposal: %v", err)
	}

	ce.broadcastChan <- proposal

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

// getRandomnessSeed generates deterministic randomness for validator selection.
// TESTNET IMPLEMENTATION: Uses block hash history.
// MAINNET TODO: Replace with VDF (Verifiable Delay Function) or RANDAO to prevent stake grinding.
func (ce *ConsensusEngine) getRandomnessSeed(slot uint64) []byte {
	// 1. Define Epoch Parameters
	const slotsPerEpoch = 32
	currentEpoch := slot / slotsPerEpoch

	var seedSource []byte

	// 2. Determine the Seed Source
	if currentEpoch == 0 {
		// Epoch 0: Use Genesis Hash
		genesis, err := ce.worldState.GetBlock(0)
		if err == nil && genesis != nil {
			seedSource = []byte(genesis.Hash)
		} else {
			// Fallback for initialization safety
			seedSource = make([]byte, 32)
		}
	} else {
		// Epoch N > 0: Use the hash of the last block of Epoch N-1.
		// This prevents manipulating the current epoch's blocks to influence the current epoch's seed.
		lookbackHeight := int64(currentEpoch*slotsPerEpoch) - 1

		refBlock, err := ce.worldState.GetBlock(lookbackHeight)
		if err != nil || refBlock == nil {
			// Fallback to Genesis if historical block is missing
			genesis, errGen := ce.worldState.GetBlock(0)
			if errGen == nil && genesis != nil {
				seedSource = []byte(genesis.Hash)
			} else {
				seedSource = make([]byte, 32)
			}
		} else {
			seedSource = []byte(refBlock.Hash)
		}
	}

	// 3. Cryptographic Mixing (Hardened for Testnet)
	// We use a Domain Separation Tag to prevent hash collisions with other protocol messages.
	domainTag := []byte("THRYLOS_RANDOMNESS_V1_EPOCH_SEED")

	slotBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(slotBytes, slot)

	// Combine: Tag + SeedSource (Previous Epoch Hash) + Slot ID
	combined := make([]byte, 0, len(domainTag)+len(seedSource)+len(slotBytes))
	combined = append(combined, domainTag...)
	combined = append(combined, seedSource...)
	combined = append(combined, slotBytes...)

	// 4. Generate final hash
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

// computeBlockSigningHash creates the hash that validators sign / verify for a block.
func (ce *ConsensusEngine) computeBlockSigningHash(block *core.Block) ([]byte, error) {
	if block == nil || block.Header == nil {
		return nil, fmt.Errorf("block or header cannot be nil")
	}
	if block.Hash == "" {
		return nil, fmt.Errorf("block hash must be set before signing")
	}

	// Domain separation + chain binding
	data := fmt.Sprintf(
		"%s|block-v1|%s|%d",
		ce.config.Network.ChainID, // e.g. "thrylos-testnet-1"
		block.Hash,
		block.Header.Index,
	)

	h := blake2b.Sum256([]byte(data))
	return h[:], nil
}

// signBlock signs the block with this node's validator key.
func (ce *ConsensusEngine) signBlock(block *core.Block) error {
	if block == nil || block.Header == nil {
		return fmt.Errorf("block or header cannot be nil")
	}

	// Safety: make sure hash is what we expect
	expectedHash := ce.blockProposer.calculateBlockHash(block)
	if block.Hash != expectedHash {
		return fmt.Errorf("cannot sign block: hash mismatch (got %s, expected %s)",
			block.Hash, expectedHash)
	}

	// Safety: this node must actually be the proposer in the header
	if block.Header.Validator != ce.nodeAddress {
		return fmt.Errorf("cannot sign block: header.Validator=%s, node=%s",
			block.Header.Validator, ce.nodeAddress)
	}

	msg, err := ce.computeBlockSigningHash(block)
	if err != nil {
		return err
	}

	sig := ce.nodePrivateKey.Sign(msg)
	if sig == nil {
		return fmt.Errorf("failed to sign block: signature is nil")
	}

	// Assumes core.Block has `Signature []byte`
	block.Signature = sig.Bytes()
	return nil
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

	// [SEC-FIX] Verify signature is now backed by robust ChainID logic
	if err := ce.verifyProposalSignature(proposal); err != nil {
		fmt.Printf("❌ Invalid proposal signature: %v\n", err)
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

	// [SEC-FIX] Verify signature with ChainID binding BEFORE processing
	if err := ce.verifyVoteSignature(vote); err != nil {
		fmt.Printf("❌ Invalid vote signature from %s: %v\n", vote.ValidatorAddress, err)
		// Do not process invalid votes
		return
	}

	// 1. Persistence Check (Prevent Double Voting)
	hasVoted, _ := ce.worldState.GetStateStorage().HasVoted(vote.TargetEpoch, vote.ValidatorAddress)
	if hasVoted {
		fmt.Printf("⚠️ Duplicate vote detected for validator %s at epoch %d\n", vote.ValidatorAddress, vote.TargetEpoch)
		return
	}

	// Validate vote
	if err := ce.validateVote(vote); err != nil {
		fmt.Printf("Invalid vote: %v\n", err)
		return
	}

	// 2. Persist Vote
	storageVote := &types.Vote{
		ValidatorAddress: vote.ValidatorAddress,
		SourceBlockHash:  vote.SourceBlockHash,
		TargetBlockHash:  vote.TargetBlockHash,
		SourceEpoch:      vote.SourceEpoch,
		TargetEpoch:      vote.TargetEpoch,
		Signature:        vote.Signature,
	}

	if err := ce.worldState.GetStateStorage().SaveConsensusVote(storageVote); err != nil {
		fmt.Printf("❌ Failed to persist vote: %v\n", err)
		return
	}

	// Store vote in memory
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

	// Add time synchronization status
	stats["time_sync"] = ce.timeValidator.GetTimeDriftStatus()
	stats["time_sync_healthy"] = ce.timeValidator.IsTimeSyncHealthy()

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

	// 🔐 NEW: validate block signature
	if err := bv.validateBlockSignature(block); err != nil {
		return fmt.Errorf("block signature validation failed: %v", err)
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

func (bv *BlockValidator) validateBlockSignature(block *core.Block) error {
	// Skip genesis – it’s unsigned and created by InitializeFromConfig
	if block.Header.Index == 0 {
		return nil
	}

	if len(block.Signature) == 0 {
		return fmt.Errorf("block signature cannot be empty")
	}

	valAddr := block.Header.Validator
	if valAddr == "" {
		return fmt.Errorf("block header has empty validator address")
	}

	// Look up validator in world state
	validator, err := bv.consensusEngine.worldState.GetValidator(valAddr)
	if err != nil {
		return fmt.Errorf("failed to load validator %s: %v", valAddr, err)
	}

	if !validator.Active {
		return fmt.Errorf("validator %s is not active", valAddr)
	}
	if validator.JailUntil > time.Now().Unix() {
		return fmt.Errorf("validator %s is jailed until %d", valAddr, validator.JailUntil)
	}

	if len(validator.Pubkey) == 0 {
		return fmt.Errorf("validator %s has empty pubkey", valAddr)
	}

	// Rebuild public key from bytes
	pubKey, err := crypto.NewPublicKeyFromBytes(validator.Pubkey)
	if err != nil {
		return fmt.Errorf("invalid validator pubkey for %s: %v", valAddr, err)
	}

	// Extra safety: pubkey → address must match header.Validator
	derivedAddr, err := account.GenerateAddress(pubKey)
	if err != nil {
		return fmt.Errorf("failed to derive address from validator pubkey: %v", err)
	}
	if derivedAddr != valAddr {
		return fmt.Errorf("validator address/pubkey mismatch: header=%s, derived=%s", valAddr, derivedAddr)
	}

	// Use the same domain-separated signing hash as the proposer
	msg, err := bv.consensusEngine.computeBlockSigningHash(block)
	if err != nil {
		return err
	}

	sig, err := crypto.SignatureFromBytes(block.Signature)
	if err != nil {
		return fmt.Errorf("failed to parse block signature: %v", err)
	}

	if err := pubKey.Verify(msg, &sig); err != nil {
		return fmt.Errorf("block signature verification failed: %v", err)
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

	// Enhanced timestamp validation with time drift protection
	var previousBlockTimestamp int64
	currentBlock := bv.consensusEngine.worldState.GetCurrentBlock()
	if currentBlock != nil {
		previousBlockTimestamp = currentBlock.Header.Timestamp
	}

	// Use TimeValidator for comprehensive timestamp validation
	err := bv.consensusEngine.timeValidator.ValidateBlockTimestamp(
		block.Header.Timestamp,
		previousBlockTimestamp,
		cfg.MaxFutureBlockTime,
		cfg.MaxPastBlockTime,
	)
	if err != nil {
		return fmt.Errorf("block timestamp validation failed: %v", err)
	}

	// Validate chain continuity (Previous Block Check)
	if currentBlock != nil {
		if block.Header.Index != currentBlock.Header.Index+1 {
			return fmt.Errorf("invalid block index: expected %d, got %d",
				currentBlock.Header.Index+1, block.Header.Index)
		}

		if block.Header.PrevHash != currentBlock.Hash {
			return fmt.Errorf("invalid previous hash")
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

// cleanupChainCache should be called periodically (e.g., every epoch)
func (ce *ConsensusEngine) cleanupChainCache() {
	ce.chainCache.Clear()
	fmt.Printf("🧹 Chain cache cleared\n")
}
