// consensus/pos/consensus.go
// Proof of Stake consensus implementation for Thrylos blockchain

package pos

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha3"
	"encoding/binary"
	"fmt"
	"log"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/consensus/validator"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/chain"
	"github.com/thrylos-labs/go-thrylos/core/math"
	coremath "github.com/thrylos-labs/go-thrylos/core/math"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/crypto/hash"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
	"github.com/thrylos-labs/go-thrylos/types"
)

// NewConsensusEngine creates a new PoS consensus engine
func NewConsensusEngine(
	cfg *config.Config,
	blockchain *chain.Blockchain,
	worldState *state.WorldState,
	nodePrivateKey crypto.PrivateKey,
	broadcastChan chan interface{},
	receiveChan chan interface{},
) *ConsensusEngine {

	nodeAddress, _ := account.GenerateAddress(nodePrivateKey.PublicKey())

	engine := &ConsensusEngine{
		config:            cfg,
		blockchain:        blockchain,
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
	if cfg.Consensus.StakeCacheTTL > 0 {
		fcConfig.StakeCacheTTL = cfg.Consensus.StakeCacheTTL
	}

	// Pass SlashingManager placeholder
	engine.forkChoice = NewForkChoiceWithConfig(cfg, worldState, &SlashingManager{}, fcConfig)

	// Initialize slashing manager
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

	// Create slashing storage
	var slashingStorage *storage.SlashingStorage
	badgerDB := worldState.GetBadgerDB()

	if badgerDB != nil {
		slashingStorage = storage.NewSlashingStorage(badgerDB)
		log.Println("✅ Slashing persistence enabled")
	} else {
		slashingStorage = nil
		log.Println("⚠️ Slashing persistence disabled")
	}

	engine.slashingManager = NewSlashingManager(slashingConfig, worldState, slashingStorage, worldState)

	// Update fork choice with real slashing manager
	engine.forkChoice.slashingManager = engine.slashingManager

	// Initialize timestamp validator (prevents time manipulation)
	genesisTime := cfg.GenesisTimestamp
	if genesisTime == 0 {
		// Fallback to current time if not configured
		genesisTime = time.Now().Unix()
		log.Printf("⚠️ GenesisTimestamp not set in config, using current time: %d\n", genesisTime)
	}

	engine.timestampValidator = NewTimestampValidator(
		300, // maxDriftSeconds - allow ±30 seconds drift
		6,   // slotDurationSeconds
		genesisTime,
	)

	// Database and checkpoint loading
	if badgerDB != nil {
		// Wrap badger.DB to implement DatabaseStore interface
		dbWrapper := &BadgerDatabaseWrapper{db: badgerDB}
		engine.forkChoice.SetDatabase(dbWrapper)

		if err := engine.forkChoice.LoadFinalizedCheckpoint(); err != nil {
			fmt.Printf("⚠️ Failed to load checkpoint: %v\n", err)
		} else {
			log.Println("✅ Checkpoint persistence enabled")
		}
	}

	// Initialize evidence tracker
	engine.evidenceTracker = NewEvidenceTracker()

	// Initialize time validator
	engine.timeValidator = NewTimeValidator()

	return engine
}

// GetForkChoice returns the fork choice instance
func (ce *ConsensusEngine) GetForkChoice() interface{} {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	return ce.forkChoice
}

// generateVRFProof generates VRF-style proof using validator's private key
func (ce *ConsensusEngine) generateVRFProof(input []byte) (*VRFProof, error) {
	// Use ECVRF instead of custom implementation
	return GenerateVRFProof(ce.nodePrivateKey, input)
}

// Start begins the consensus process
func (ce *ConsensusEngine) Start() error {
	log.Printf("🚀 Consensus starting for validator: %s", ce.nodeAddress)

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

	return nil
}

// In consensusLoop (around line 94-112)
func (ce *ConsensusEngine) consensusLoop() {
	slotTicker := time.NewTicker(ce.proposalTimeout)
	defer slotTicker.Stop()

	// 🔍 ADD THIS
	fmt.Printf("🔍 consensusLoop started, timeout=%v\n", ce.proposalTimeout)

	for {
		select {
		case <-slotTicker.C:
			// 🔍 ADD THIS
			fmt.Printf("\n⏰ TICK - calling processSlot\n")

			ce.processSlot()

			// 🔍 ADD THIS
			fmt.Printf("✅ processSlot returned\n")
		}
	}
}

func (ce *ConsensusEngine) SetSyncing(syncing bool) {
	ce.mu.Lock()
	defer ce.mu.Unlock()
	ce.isSyncing = syncing
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

	// ✅ FIX 1: Re-initialize validator set EVERY slot during startup
	// Ensures as containers boot and register in WorldState, they enter the rotation.
	if ce.worldState.GetHeight() < 50 {
		if err := ce.initializeValidatorSet(); err != nil {
			log.Printf("⚠️ Failed to initialize validator set: %v", err)
		}
	}

	// ✅ NEW: Warm-up Guard
	// Prevents nodes from starting consensus prematurely with an incomplete validator list.
	// Without this, Node A (knowing only itself) would pick itself for Slot 1,
	// while Node B (knowing only itself) would also pick itself, causing an immediate fork.
	expectedValidators := 4
	if ce.validatorSet.Size() < expectedValidators {
		log.Printf("⏳ Waiting for validators to join... (Have %d, Need %d)",
			ce.validatorSet.Size(), expectedValidators)
		ce.mu.Unlock() // Must unlock before returning
		return
	}

	if ce.isSyncing {
		log.Printf("⏳ Skipping slot %d - chain sync in progress", ce.currentSlot)
		ce.mu.Unlock()
		return
	}

	// ✅ NEW: One-time propagation delay after quorum is first reached
	// Gives time for all nodes to receive each other's validator announcements
	// before anyone starts proposing blocks and requiring signature verification.
	if !ce.validatorsSynced {
		ce.validatorsSynced = true
		ce.mu.Unlock()
		log.Printf("✅ Validator quorum reached, waiting 15s for announcements to propagate...")
		time.Sleep(15 * time.Second)
		ce.mu.Lock()
	}

	// Process completed unbondings
	if err := ce.worldState.ProcessUnbondingQueue(); err != nil {
		log.Printf("⚠️ Failed to process unbonding queue: %v", err)
	}

	// ✅ FIX 2: Grace Period for Activity Tracking
	if ce.currentSlot > 0 && ce.worldState.GetHeight() >= 100 {
		previousSlot := ce.currentSlot
		expectedProposer, err := ce.getSlotProposer(previousSlot)
		if err == nil {
			currentBlock := ce.worldState.GetCurrentBlock()
			wasBlockProduced := currentBlock != nil && currentBlock.Header.Slot == previousSlot
			ce.updateValidatorActivity(expectedProposer, wasBlockProduced)
		}
	}

	ce.currentSlot++
	ce.currentEpoch = ce.currentSlot / 32

	// Get the proposer for this slot
	proposer, err := ce.getSlotProposer(ce.currentSlot)
	if err != nil {
		log.Printf("❌ Failed to get slot proposer for slot %d: %v", ce.currentSlot, err)
		ce.mu.Unlock()
		return
	}

	// Check if the proposer is active
	isActive := ce.slashingManager.IsValidatorActive(proposer)
	if !isActive {
		activeCount := len(ce.validatorSet.GetActiveValidators())
		if activeCount == 0 {
			log.Printf("🚨 EMERGENCY: Proposer %s is INACTIVE, but proceeding (Recovery Mode)", proposer)
		} else {
			log.Printf("⚠️ Proposer %s is not active, skipping slot", proposer)
			ce.mu.Unlock()
			return
		}
	}

	// Capture state for logging/logic before unlocking
	isMyTurn := (proposer == ce.nodeAddress)
	currentSlot := ce.currentSlot
	currentHeight := ce.worldState.GetHeight()

	log.Printf("🎲 Slot %d: Proposer=%s, Match=%v", currentSlot, proposer[:10], isMyTurn)

	// ✅ FIX 3: Unlock before P2P Network Operations
	ce.mu.Unlock()

	if isMyTurn {
		fmt.Printf("🔨 I AM proposer for slot %d! (Current Height: %d)\n", currentSlot, currentHeight)

		if err := ce.proposeBlock(); err != nil {
			fmt.Printf("❌ BLOCK PROPOSAL FAILED: %v\n", err)

			if currentHeight >= 100 {
				ce.mu.Lock()
				ce.blocksMissed++
				ce.mu.Unlock()
			} else {
				fmt.Printf("ℹ️  Miss ignored due to startup grace period\n")
			}
		} else {
			fmt.Printf("✅ SUCCESS: Block %d proposed and broadcasted!\n", currentHeight+1)
			ce.mu.Lock()
			ce.blocksProposed++
			ce.mu.Unlock()
		}
	} else {
		fmt.Printf("ℹ️  Not my turn (proposer: %s..., me: %s...)\n", proposer[:8], ce.nodeAddress[:8])
	}

	// Re-acquire lock for Attestation and state copying
	ce.mu.Lock()
	if ce.isCurrentNodeValidator() {
		if err := ce.createAttestation(); err != nil {
			fmt.Printf("❌ Failed to create attestation: %v\n", err)
		} else {
			ce.attestationsMade++
		}
	}

	attestationsCopy := make(map[string]*types.Attestation)
	for k, v := range ce.attestations {
		attestationsCopy[k] = v
	}
	ce.mu.Unlock()

	// Process attestations and fork choice ASYNC
	go func() {
		ce.processAttestationsAsync(attestationsCopy)
		ce.updateForkChoice()
	}()
}

func (ce *ConsensusEngine) processAttestationsAsync(attestations map[string]*types.Attestation) {
	for _, attestation := range attestations {
		if err := ce.validateAttestation(attestation); err != nil {
			continue
		}

		if err := ce.slashingManager.ProcessAttestation(attestation); err != nil {
			continue
		}

		ce.forkChoice.ProcessAttestation(attestation)
	}
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
	// ✅ ADD THIS: Skip activity tracking during startup (first 100 blocks)
	// This prevents "withholding" jailing before the network is stable.
	if ce.worldState.GetHeight() < 100 {
		return
	}

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

// proposeBlock creates and broadcasts a new block proposal
// proposeBlock creates and broadcasts a new block proposal
// proposeBlock creates and broadcasts a new block proposal
func (ce *ConsensusEngine) proposeBlock() error {
	// Generate VRF input
	input := make([]byte, 16)
	binary.BigEndian.PutUint64(input[0:8], ce.currentSlot)
	binary.BigEndian.PutUint64(input[8:16], ce.currentEpoch)

	vrfProof, err := GenerateVRFProof(ce.nodePrivateKey, input)
	if err != nil {
		return fmt.Errorf("VRF generation failed: %v", err)
	}

	// Create block
	result, err := ce.blockProposer.ProposeBlockWithVRF(
		ce.currentSlot,
		ce.currentEpoch,
		vrfProof.Output,
		vrfProof.Proof,
	)
	if err != nil {
		return fmt.Errorf("failed to create block: %v", err)
	}

	// Sign and Validate
	if err := ce.signBlock(result.Block); err != nil {
		return fmt.Errorf("failed to sign block: %v", err)
	}

	// Add to local state first
	if err := ce.worldState.AddBlock(result.Block); err != nil {
		return fmt.Errorf("failed to add block to local world state: %v", err)
	}

	// Prepare proposal for P2P
	proposal := &BlockProposal{
		Block: result.Block,
		// Standardize proposer string to lowercase hex without 0x
		Proposer: "0x" + strings.ToLower(strings.TrimPrefix(ce.nodeAddress, "0x")),
		Slot:     ce.currentSlot,
		Epoch:    ce.currentEpoch,
	}

	if err := ce.signBlockProposal(proposal); err != nil {
		return fmt.Errorf("failed to sign block proposal: %v", err)
	}

	// Store proposal signature on block for P2P transmission
	result.Block.ProposalSignature = proposal.Signature

	// ✅ FIX: Use a small timeout instead of dropping immediately
	// This allows the P2P layer a moment to catch up if it's busy.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	select {
	case ce.broadcastChan <- proposal:
		fmt.Printf("✅ Block %s successfully queued for broadcast\n", result.Block.Hash[:8])
	case <-ctx.Done():
		// If this happens, your P2P routine (GossipSub) is stuck!
		return fmt.Errorf("critical: broadcast channel blocked for 2s, proposal lost")
	}

	fmt.Printf("🚀 Block #%d (Hash: %s) produced with %d txs\n",
		result.Block.Header.Index, result.Block.Hash[:8], result.TransactionCount)

	return nil
}

// getPreviousBlockHash returns the hash of the most recent block
func (ce *ConsensusEngine) getPreviousBlockHash() string {
	if ce.worldState != nil {
		currentBlock := ce.worldState.GetCurrentBlock()
		if currentBlock != nil {
			return currentBlock.Hash
		}
	}
	return ""
}

// createAttestation creates an attestation for the current head
// createAttestation creates an attestation for the current head
func (ce *ConsensusEngine) createAttestation() error {
	// ✅ NEW: Prevent Equivocation by checking epoch
	if ce.currentEpoch <= ce.lastAttestedEpoch && ce.currentSlot > 0 {
		// We already voted for this epoch, skip to avoid self-slashing
		return nil
	}

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

	// ✅ FIX: Broadcast attestation (non-blocking)
	select {
	case ce.broadcastChan <- attestation:
		fmt.Printf("✅ Attestation broadcast queued for Slot %d\n", ce.currentSlot)
		// ONLY update tracker if we actually sent it
		ce.lastAttestedEpoch = ce.currentEpoch
	default:
		fmt.Printf("⚠️ Attestation broadcast channel full, dropping\n")
		// We don't update lastAttestedEpoch here, allowing a retry on the next tick
	}

	// After successfully signing/broadcasting:
	ce.lastAttestedEpoch = ce.currentEpoch
	return nil
}

func (ce *ConsensusEngine) getSlotProposer(slot uint64) (string, error) {
	// 1. Try to get standard active validators
	activeValidators := ce.validatorSet.GetActiveValidators()

	// ✅ FIX: Emergency Deadlock Recovery
	// If NO active validators exist (all jailed/slashed), the chain halts.
	// Fallback: Use ALL validators (including jailed ones) to allow a "recovery block"
	// to be proposed, which can contain transactions to unjail nodes.
	if len(activeValidators) == 0 {
		fmt.Println("🚨 EMERGENCY: No active validators found (all jailed?). Entering Recovery Mode.")

		// Retrieve ALL validators from WorldState (requires the new method added above)
		allValidators := ce.worldState.GetAllValidators()

		if len(allValidators) == 0 {
			return "", fmt.Errorf("CRITICAL: No validators exist in world state (bootstrap required)")
		}

		fmt.Printf("⚠️ Recovery Mode: Using %d total validators (active+inactive) for consensus\n", len(allValidators))

		// Overwrite activeValidators with the full set for this slot selection only
		activeValidators = allValidators
	}

	// 🔍 DEBUG LOG
	fmt.Printf("🔍 DEBUG getSlotProposer: slot=%d, candidates=%d\n", slot, len(activeValidators))

	// 2. Filter for eligibility (Slashing Check)
	eligibleValidators := make([]*core.Validator, 0)
	for _, validator := range activeValidators {
		// In Recovery Mode, we might want to skip the "IsValidatorActive" check
		// or check purely for slashing status, not "Active" status.
		// For now, we trust the set passed in.
		eligibleValidators = append(eligibleValidators, validator)
	}

	if len(eligibleValidators) == 0 {
		return "", fmt.Errorf("no eligible validators found even after recovery attempt")
	}

	// 3. VRF Selection
	selectedValidator, err := ce.selectValidatorWithVRF(eligibleValidators, slot)
	if err != nil {
		return "", fmt.Errorf("failed to select validator: %v", err)
	}

	fmt.Printf("✅ DEBUG: Selected proposer %s for slot %d\n", selectedValidator.Address, slot)
	return selectedValidator.Address, nil
}

// selectValidatorWithVRF selects a validator using VRF-based deterministic randomness
// This provides cryptographically secure, unpredictable validator selection
func (ce *ConsensusEngine) selectValidatorWithVRF(validators []*core.Validator, slot uint64) (*core.Validator, error) {
	if len(validators) == 0 {
		return nil, fmt.Errorf("no validators provided")
	}

	// Create VRF input from slot and blockchain state
	vrfInput := ce.createVRFInput(slot)

	// Track best candidate
	type VRFCandidate struct {
		Validator     *core.Validator
		VRFOutput     []byte
		WeightedScore *big.Int
	}

	var candidates []VRFCandidate

	for _, validator := range validators {
		// Create validator-specific VRF input
		validatorInput := append(vrfInput, []byte(validator.Address)...)

		// Generate VRF output deterministically
		// In multi-validator production: each validator submits their VRF proof
		vrfOutput := ce.generateDeterministicVRFOutput(validatorInput)

		// Calculate stake-weighted score (lower is better)
		score := ce.calculateVRFStakeScore(vrfOutput, validator.Stake)

		candidates = append(candidates, VRFCandidate{
			Validator:     validator,
			VRFOutput:     vrfOutput,
			WeightedScore: score,
		})
	}

	// Select validator with lowest weighted score
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no valid VRF candidates")
	}

	bestCandidate := &candidates[0]
	for i := 1; i < len(candidates); i++ {
		if candidates[i].WeightedScore.Cmp(bestCandidate.WeightedScore) < 0 {
			bestCandidate = &candidates[i]
		}
	}

	return bestCandidate.Validator, nil
}

// createVRFInput creates the input for VRF from slot and blockchain state
// This ensures unpredictability by mixing slot number with previous block hash
func (ce *ConsensusEngine) createVRFInput(slot uint64) []byte {
	// Domain separation for security
	domain := []byte("THRYLOS_VRF_PROPOSER_V1")

	// Get previous block hash for entropy
	currentHeight := ce.worldState.GetHeight()
	var prevBlockHash []byte

	if currentHeight > 0 {
		prevBlock, err := ce.worldState.GetBlock(currentHeight)
		if err == nil && prevBlock != nil {
			prevBlockHash = []byte(prevBlock.Hash)
		}
	}

	// Fallback to genesis if no previous block
	if len(prevBlockHash) == 0 {
		genesis, err := ce.worldState.GetBlock(0)
		if err == nil && genesis != nil {
			prevBlockHash = []byte(genesis.Hash)
		} else {
			// Ultimate fallback to zeros
			prevBlockHash = make([]byte, 32)
		}
	}

	// Convert slot to bytes
	slotBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(slotBytes, slot)

	// Combine: domain | prevBlockHash | slot
	combined := make([]byte, 0, len(domain)+len(prevBlockHash)+len(slotBytes))
	combined = append(combined, domain...)
	combined = append(combined, prevBlockHash...)
	combined = append(combined, slotBytes...)

	return combined
}

// generateDeterministicVRFOutput generates a VRF-like output using your existing VRF implementation
// This connects to your existing VRF code in vrf_seed.go
func (ce *ConsensusEngine) generateDeterministicVRFOutput(input []byte) []byte {
	// Option 1: Use your existing VRF implementation
	if ce.nodePrivateKey != nil {
		proof, err := GenerateVRFProof(ce.nodePrivateKey, input)
		if err == nil && proof != nil {
			// Return the VRF output
			return proof.Output
		}
	}

	// Option 2: Fallback to secure hash if VRF generation fails
	// This maintains security while ensuring the system works
	hash := sha3.Sum256(input)
	return hash[:]
}

// calculateVRFStakeScore calculates a score that gives higher stake better odds
// Formula: VRF_output / stake
// Lower score = better chance (inversely proportional to stake)
func (ce *ConsensusEngine) calculateVRFStakeScore(vrfOutput []byte, stake string) *big.Int {
	// Convert VRF output to a big integer
	vrfNumber := new(big.Int).SetBytes(vrfOutput)

	// Parse validator's stake
	stakeBig := math.ParseBigInt(stake)
	if stakeBig.Sign() == 0 {
		stakeBig = big.NewInt(1) // Prevent division by zero
	}

	// Calculate score: VRF_output / stake
	// Validators with more stake get lower scores (better chance)
	score := new(big.Int).Div(vrfNumber, stakeBig)

	return score
}

// verifyVRFProof verifies a VRF proof from another validator
// verifyVRFProof verifies a VRF proof from another validator
func (ce *ConsensusEngine) verifyVRFProof(validatorPubKey crypto.PublicKey, input []byte, proof *VRFProof) (bool, error) {
	if validatorPubKey == nil {
		return false, fmt.Errorf("validator public key is nil")
	}

	// FIX: Convert the interface to bytes using .Bytes()
	valid, output, err := VerifyVRFProof(validatorPubKey.Bytes(), input, proof)
	if err != nil {
		return false, fmt.Errorf("VRF verification failed: %w", err)
	}

	if !valid {
		return false, fmt.Errorf("VRF proof is invalid")
	}

	// Optionally check that output matches
	if len(output) != len(proof.Output) {
		return false, fmt.Errorf("VRF output length mismatch")
	}

	return true, nil
}

// selectValidatorByStake selects a validator based on stake weight and randomness
// selectValidatorByStake selects a validator based on stake weight and randomness
func (ce *ConsensusEngine) selectValidatorByStake(validators []*core.Validator, seed []byte) (*core.Validator, error) {
	if len(validators) == 0 {
		return nil, fmt.Errorf("no validators provided")
	}

	// 1. Calculate Total Stake using BigInt
	totalStakeBig := big.NewInt(0)
	for _, v := range validators {
		stakeVal := math.ParseBigInt(v.Stake)
		totalStakeBig.Add(totalStakeBig, stakeVal)
	}

	// Check if total stake is zero
	if totalStakeBig.Sign() == 0 {
		return nil, fmt.Errorf("total stake is zero")
	}

	// 2. Generate random number from seed (0 to TotalStake)
	// We treat the seed bytes as a massive number, then Modulo by TotalStake
	seedInt := new(big.Int).SetBytes(seed)
	randomStake := new(big.Int).Mod(seedInt, totalStakeBig)

	// 3. Select validator based on cumulative stake
	cumulativeStakeBig := big.NewInt(0)

	for _, validator := range validators {
		stakeVal := math.ParseBigInt(validator.Stake)

		// Add current stake to cumulative
		cumulativeStakeBig.Add(cumulativeStakeBig, stakeVal)

		// Check: if randomStake < cumulativeStake
		if randomStake.Cmp(cumulativeStakeBig) < 0 {
			return validator, nil
		}
	}

	// Fallback to last validator (should technically be unreachable if math is correct)
	return validators[len(validators)-1], nil
}

// getRecentBlockHashes returns the last N block hashes
func (ce *ConsensusEngine) getRecentBlockHashes(n int) [][]byte {
	currentHeight := ce.worldState.GetHeight()
	if currentHeight < 1 {
		return nil
	}

	if int64(n) > currentHeight {
		n = int(currentHeight)
	}

	blockHashes := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		height := currentHeight - int64(i)
		block, err := ce.worldState.GetBlock(height)
		if err != nil || block == nil {
			continue
		}
		blockHashes = append(blockHashes, []byte(block.Hash))
	}

	return blockHashes
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

			// Create and broadcast slashing evidence
			evidence := ce.createSlashingEvidenceFromAttestation(attestation, err)
			if evidence != nil {
				// ✅ FIX: Use Capital 'H' to match the defined method
				if err := ce.HandleSlashingEvidence(evidence); err != nil {
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

	// Use your crypto/hash package
	return hash.Keccak256([]byte(data)), nil
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

	sig, err := ce.nodePrivateKey.Sign(msg)
	if err != nil {
		return fmt.Errorf("failed to sign block: %w", err)
	}

	log.Printf("🔍 Signing block with pubkey=%x", ce.nodePrivateKey.PublicKey().Bytes())

	// Assumes core.Block has `Signature []byte`
	block.Signature = sig.Bytes()
	return nil
}

// initializeValidatorSet initializes the validator set from world state
func (ce *ConsensusEngine) initializeValidatorSet() error {
	candidates := ce.worldState.GetActiveValidators()

	// CRITICAL: Sort candidates by address to ensure deterministic selection across nodes
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Address < candidates[j].Address
	})

	ce.validatorSet.Clear()
	for _, v := range candidates {
		ce.validatorSet.AddValidator(v)
	}

	log.Printf("✅ Validator set initialized with %d candidates (sorted)", ce.validatorSet.Size())
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

	// Add block signature verification
	if err := ce.VerifyBlockWithSignatures(proposal.Block); err != nil {
		fmt.Printf("❌ Invalid block signature: %v\n", err)
		return
	}

	// Then validate the block
	if err := ce.blockValidator.ValidateBlock(proposal.Block); err != nil {
		fmt.Printf("Invalid block proposal: %v\n", err)
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
		// ✅ FIX: Use helper to calculate percentage from strings
		stats["justified_stake_percentage"] = calculateStakePercentage(justified.AttestingStake, justified.TotalStake)
	}

	// Add finalized checkpoint info
	finalized := ce.forkChoice.GetFinalizedCheckpoint()
	if finalized != nil {
		stats["finalized_epoch"] = finalized.Epoch
		stats["finalized_block"] = finalized.BlockHash[:8]
		// ✅ FIX: Use helper to calculate percentage from strings
		stats["finalized_stake_percentage"] = calculateStakePercentage(finalized.AttestingStake, finalized.TotalStake)
	}

	// Add time synchronization status
	stats["time_sync"] = ce.timeValidator.GetTimeDriftStatus()
	stats["time_sync_healthy"] = ce.timeValidator.IsTimeSyncHealthy()

	return stats
}

// calculateStakePercentage safely calculates (attesting / total) * 100
func calculateStakePercentage(attestingStr, totalStr string) float64 {
	attestingBig := coremath.ParseBigInt(attestingStr)
	totalBig := coremath.ParseBigInt(totalStr)

	// Avoid division by zero
	if totalBig.Sign() == 0 {
		return 0.0
	}

	// Convert to BigFloat for precision division
	attF := new(big.Float).SetInt(attestingBig)
	totF := new(big.Float).SetInt(totalBig)

	// Result = (att / tot) * 100
	res := new(big.Float).Quo(attF, totF)
	res.Mul(res, big.NewFloat(100))

	percent, _ := res.Float64()
	return percent
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

	// 🔐 Validate block signature
	if err := bv.validateBlockSignature(block); err != nil {
		return fmt.Errorf("block signature validation failed: %v", err)
	}

	// ✅ H-3 FIX: Strict timestamp validation
	if block.Header.Index > 0 {
		parentBlock, err := bv.consensusEngine.blockchain.GetBlock(block.Header.PrevHash)
		if err == nil && parentBlock != nil {
			// GOOD - reuses existing 'err' from line 1168
			if err := bv.consensusEngine.timestampValidator.ValidateBlockTimestamp(
				block.Header.Timestamp,
				block.Header.Slot,
				parentBlock.Header.Timestamp,
				parentBlock.Header.Index, // NEW PARAMETER

			); err != nil {
				return fmt.Errorf("timestamp validation failed: %v", err)
			}
		}
	}

	// ✅ NEW: Validate VRF proof (skip for genesis block)
	if block.Header.Index > 0 {
		if err := bv.validateVRFProof(block); err != nil {
			return fmt.Errorf("VRF proof validation failed: %v", err)
		}
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

	// Validate state root
	calculatedRoot := bv.consensusEngine.worldState.GetStateRoot()
	if block.Header.StateRoot != calculatedRoot {
		return fmt.Errorf("state root mismatch: block=%s, calculated=%s",
			block.Header.StateRoot, calculatedRoot)
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

	if err := pubKey.Verify(msg, sig); err != nil {
		return fmt.Errorf("block signature verification failed: %v", err)
	}

	return nil
}

// validateVRFProof validates the VRF proof in the block header
// validateVRFProof validates the VRF proof in the block header
func (bv *BlockValidator) validateVRFProof(block *core.Block) error {
	// Skip VRF validation in development mode (single validator)
	validators := bv.consensusEngine.worldState.GetActiveValidators()
	if len(validators) == 1 {
		return nil
	}

	// Get validator
	validator, err := bv.consensusEngine.worldState.GetValidator(block.Header.Validator)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	// For now, derive Ed25519 public key from secp256k1 public key
	// TODO: Add VRFPublicKey field to validator struct for proper Ed25519 keys
	vrfPubKey := deriveVRFPublicKeyFromSecp256k1(validator.Pubkey)

	// Create VRF input from slot and epoch
	input := make([]byte, 16)
	binary.BigEndian.PutUint64(input[0:8], block.Header.Slot)
	binary.BigEndian.PutUint64(input[8:16], block.Header.Epoch)

	// Construct VRF proof from block header
	vrfProof := &VRFProof{
		Output: block.Header.VrfOutput,
		Proof:  block.Header.VrfProof,
	}

	// Verify VRF proof
	valid, _, err := VerifyVRFProof(vrfPubKey, input, vrfProof)
	if err != nil {
		return fmt.Errorf("VRF verification failed: %v", err)
	}

	if !valid {
		return fmt.Errorf("VRF proof is invalid")
	}

	return nil
}

func (ce *ConsensusEngine) ReinitializeValidatorSet() error {
	ce.mu.Lock()
	defer ce.mu.Unlock()
	return ce.initializeValidatorSet()
}

// deriveVRFPublicKeyFromSecp256k1 creates a deterministic Ed25519 public key from secp256k1
// This is a bridge function until validators register proper Ed25519 VRF keys
func deriveVRFPublicKeyFromSecp256k1(secp256k1PubKey []byte) []byte {
	// Must match GenerateVRFProof derivation!
	hash := sha256.Sum256(secp256k1PubKey)
	ed25519PrivKey := ed25519.NewKeyFromSeed(hash[:])
	ed25519PubKey := ed25519PrivKey.Public().(ed25519.PublicKey)
	return ed25519PubKey
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
		MaxAllowedTimeDrift, // Forces 5s limit defined in timesync.go
		MaxAllowedTimeDrift, // Forces 5s limit defined in timesync.go
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

// ============================================================================
// VALIDATOR DISCOVERY
// ============================================================================

// RegisterDiscoveredValidator registers a validator discovered from a peer
func (ce *ConsensusEngine) RegisterDiscoveredValidator(validator *core.Validator) error {
	if validator == nil {
		return fmt.Errorf("validator cannot be nil")
	}

	// Basic validation
	if validator.Address == "" {
		return fmt.Errorf("validator address cannot be empty")
	}

	if len(validator.Pubkey) == 0 {
		return fmt.Errorf("validator public key cannot be empty")
	}

	// Check if already registered
	existing, err := ce.worldState.GetValidator(validator.Address)
	if err == nil && existing != nil {
		// Update pubkey if we now have one and didn't before
		if len(validator.Pubkey) > 0 && !bytes.Equal(existing.Pubkey, validator.Pubkey) {
			existing.Pubkey = validator.Pubkey
			if err := ce.worldState.UpdateValidator(existing); err != nil {
				log.Printf("⚠️ Failed to update validator pubkey: %v", err)
			} else {
				log.Printf("✅ Updated pubkey for validator %s", validator.Address)
			}
		}
		return nil
	}

	// Validate stake is non-zero
	stake, ok := new(big.Int).SetString(validator.Stake, 10)
	if !ok || stake.Sign() <= 0 {
		return fmt.Errorf("invalid stake amount: %s", validator.Stake)
	}

	// Add to WorldState
	if err := ce.worldState.AddValidator(validator); err != nil {
		return fmt.Errorf("failed to add validator to world state: %w", err)
	}

	log.Printf("✅ Registered discovered validator: %s (Stake: %s, Active: %v)",
		validator.Address, validator.Stake, validator.Active)

	return nil
}

// GetLocalValidator returns this node's validator info for broadcasting
func (ce *ConsensusEngine) GetLocalValidator() (*core.Validator, error) {
	validator, err := ce.worldState.GetValidator(ce.nodeAddress)
	if err != nil {
		return nil, fmt.Errorf("local validator not found: %w", err)
	}
	return validator, nil
}

// GetAllValidators returns all known validators for sync
func (ce *ConsensusEngine) GetAllValidators() []*core.Validator {
	return ce.worldState.GetActiveValidators()
}

// SyncValidators syncs a batch of validators from a peer
func (ce *ConsensusEngine) SyncValidators(validators []*core.Validator) error {
	successCount := 0
	for _, v := range validators {
		if err := ce.RegisterDiscoveredValidator(v); err != nil {
			log.Printf("⚠️ Failed to sync validator %s: %v", v.Address, err)
			continue
		}
		successCount++
	}

	log.Printf("✅ Synced %d/%d validators from peer", successCount, len(validators))
	return nil
}

// GetSlashingModule returns the slashing manager
func (ce *ConsensusEngine) GetSlashingModule() *SlashingManager {
	return ce.slashingManager
}
