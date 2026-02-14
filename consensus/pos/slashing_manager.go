package pos

import (
	"fmt"
	"log"
	"math/big"
	"sync"
	"time"

	coremath "github.com/thrylos-labs/go-thrylos/core/math" // Use the safe math package
	"github.com/thrylos-labs/go-thrylos/core/security"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
	"github.com/thrylos-labs/go-thrylos/types"
)

// WorldStateBalancer interface for balance operations needed by slashing
type WorldStateBalancer interface {
	// Returns *big.Int
	GetBalance(address string) (*big.Int, error)

	// Accepts *big.Int
	UpdateBalance(address string, amount *big.Int) error

	GetHeight() int64

	// Existing methods
	GetValidator(address string) (*core.Validator, error)
	UpdateValidator(validator *core.Validator) error
}

type DowntimePolicy struct {
	WarningThreshold   uint64
	MinorSlashingStart uint64
	MajorSlashingStart uint64
	JailThreshold      uint64
	EjectionThreshold  uint64

	MinorPenalty int64 // Percentage
	MajorPenalty int64 // Percentage
	JailPenalty  int64 // Percentage
	JailDuration time.Duration
}

// ValidatorRegistry interface for getting validator public keys
type ValidatorRegistry interface {
	GetValidator(address string) (*core.Validator, error)
}

// DoubleSigningError carries the existing attestation that caused the conflict.
type DoubleSigningError struct {
	ConflictingRecord *storage.AttestationRecord
}

func (e *DoubleSigningError) Error() string {
	return fmt.Sprintf("double signing detected against block %s", e.ConflictingRecord.BlockHash)
}

// SlashingManager handles all slashing-related operations
type SlashingManager struct {
	config     *storage.SlashingConfig
	forkChoice *ForkChoice

	// Track all attestations by validator for double voting detection
	attestationsByValidator map[string][]*storage.AttestationRecord

	// Track jailed validators
	jailedValidators map[string]*storage.JailedValidator

	// Track slashed validators and their records
	slashingRecords map[string][]*types.SlashingRecord

	// Track attestation history for downtime detection
	attestationHistory map[string]*storage.AttestationHistory

	// Track validator statuses
	validatorStatus map[string]storage.ValidatorStatus

	// Track processed evidence to prevent double slashing
	processedEvidence map[string]bool

	validatorRegistry ValidatorRegistry

	rateLimiter   *EvidenceRateLimiter
	confirmations *SlashingConfirmation
	cooldowns     *SlashingCooldown
	metrics       *SlashingMetrics

	mu sync.RWMutex

	// Reference to world state for stake updates
	worldState WorldStateBalancer

	// Persistent storage
	storage *storage.SlashingStorage

	policy DowntimePolicy
}

// NewSlashingManager creates a new slashing manager
func NewSlashingManager(
	config *storage.SlashingConfig,
	worldState WorldStateBalancer,
	slashingStorage *storage.SlashingStorage,
	validatorRegistry ValidatorRegistry,
) *SlashingManager {
	if config == nil {
		config = storage.DefaultSlashingConfig()
	}

	maxMisses := config.MaxMissedAttestations
	if maxMisses == 0 {
		maxMisses = 100
	}

	policy := DowntimePolicy{
		WarningThreshold:   maxMisses / 20,
		MinorSlashingStart: maxMisses / 10,
		MajorSlashingStart: maxMisses / 5,
		JailThreshold:      maxMisses / 2,
		EjectionThreshold:  maxMisses,

		MinorPenalty: 1,
		MajorPenalty: 3,
		JailPenalty:  int64(config.SlashingDowntime),
		JailDuration: time.Duration(config.JailDurationHours) * time.Hour,
	}

	sm := &SlashingManager{
		config:                  config,
		policy:                  policy,
		attestationsByValidator: make(map[string][]*storage.AttestationRecord),
		jailedValidators:        make(map[string]*storage.JailedValidator),
		slashingRecords:         make(map[string][]*types.SlashingRecord),
		attestationHistory:      make(map[string]*storage.AttestationHistory),
		validatorStatus:         make(map[string]storage.ValidatorStatus),
		processedEvidence:       make(map[string]bool),
		validatorRegistry:       validatorRegistry,
		worldState:              worldState,
		storage:                 slashingStorage,
		rateLimiter:             NewEvidenceRateLimiter(),
		confirmations:           NewSlashingConfirmation(3, 10*time.Minute), // 3 confirmations, 10min window
		cooldowns:               NewSlashingCooldown(),
		metrics:                 NewSlashingMetrics(),
	}

	if sm.storage != nil {
		if err := sm.loadFromStorage(); err != nil {
			log.Printf("⚠️ Failed to load slashing data from storage: %v", err)
		}
	}

	// Start cleanup goroutine
	go sm.cleanupPeriodic()

	// M-2 FIX: Start evidence pruning goroutine
	if sm.storage != nil && sm.config.EnableAutoPruning {
		go sm.pruneEvidencePeriodic()
	}

	return sm
}

// Add cleanup function
func (sm *SlashingManager) cleanupPeriodic() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		cleaned := sm.confirmations.CleanupExpired()
		if cleaned > 0 {
			log.Printf("🧹 Cleaned up %d expired pending confirmations", cleaned)
		}
	}
}

// ValidateEvidence validates slashing evidence with cryptographic verification
func (sm *SlashingManager) ValidateEvidence(evidence *SlashingEvidence) error {
	// Use the evidence's built-in validation with our registry
	return evidence.Validate(sm.validatorRegistry)
}

// ProcessEvidence processes validated slashing evidence
// ProcessEvidence processes validated slashing evidence
func (sm *SlashingManager) ProcessEvidence(evidence *SlashingEvidence) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// If the chain is just starting (Height < 100), ignore slashing evidence.
	// This prevents "boot-up storms" in Docker from jailing nodes.
	if sm.worldState != nil && sm.worldState.GetHeight() < 100 {
		log.Printf("ℹ️  [Slashing] Evidence ignored due to startup grace period (Height < 100)")
		return nil
	}

	sm.metrics.RecordSubmission()

	// LAYER 1: Rate Limiting (Spam Protection)
	if err := sm.rateLimiter.CheckReporter(evidence.ReporterAddress); err != nil {
		sm.metrics.RecordSpam()
		return fmt.Errorf("rate limit exceeded: %w", err)
	}

	// LAYER 2: Validate Evidence (Existing - includes signature verification)
	if err := evidence.Validate(sm.validatorRegistry); err != nil {
		sm.rateLimiter.RecordRejection(evidence.ReporterAddress)
		sm.metrics.RecordInvalid()
		return fmt.Errorf("invalid evidence: %v", err)
	}

	// LAYER 3: Check Deduplication (Existing)
	evidenceHash := evidence.Hash()
	if sm.processedEvidence[evidenceHash] {
		return fmt.Errorf("evidence already processed")
	}

	// LAYER 4: Check Cooldown Period
	if err := sm.cooldowns.CheckCooldown(evidence.ValidatorAddress, evidence.Type); err != nil {
		sm.metrics.RecordCooldown()
		return err
	}

	// LAYER 5: Require Multiple Confirmations
	ready, err := sm.confirmations.AddConfirmation(evidence, evidence.ReporterAddress)
	if err != nil {
		return err
	}
	if !ready {
		sm.metrics.RecordPending()
		return nil // Waiting for more confirmations
	}

	// LAYER 6: Mark as Processed
	sm.processedEvidence[evidenceHash] = true
	if sm.storage != nil {
		evidence.MarkProcessed()
		if err := sm.storage.SaveProcessedEvidenceWithMetadata(
			evidenceHash,
			evidence.Type.String(),
			evidence.ValidatorAddress,
		); err != nil {
			log.Printf("⚠️ Failed to persist processed evidence: %v", err)
		}
	}

	// LAYER 7: Record Valid Submission
	sm.rateLimiter.RecordAcceptance(evidence.ReporterAddress)
	sm.metrics.RecordValid()

	// LAYER 8: Process Evidence by Type
	var processErr error
	switch evidence.Type {
	case EvidenceDoubleVoting:
		dvEvidence, ok := evidence.Evidence.(*DoubleVoteEvidence)
		if ok && dvEvidence.Attestation1 != nil {
			security.LogDoubleSign(evidence.ValidatorAddress, dvEvidence.Attestation1.Slot)
		}
		processErr = sm.processDoubleVoteEvidence(evidence)

	case EvidenceSurroundVoting:
		processErr = sm.processSurroundVoteEvidence(evidence)

	case EvidenceInvalidProposal:
		dvEvidence, ok := evidence.Evidence.(*InvalidProposalEvidence)
		if !ok {
			return fmt.Errorf("invalid evidence format for invalid proposal")
		}
		processErr = sm.ReportInvalidProposal(&types.BlockProposal{
			Proposer: evidence.ValidatorAddress,
			Epoch:    dvEvidence.Proposal.Epoch,
		}, "evidence submitted")

		// Get current stake
		validator, err := sm.worldState.GetValidator(evidence.ValidatorAddress)
		if err != nil {
			return fmt.Errorf("failed to get validator stake: %w", err)
		}

		// Calculate 5% penalty
		currentStake := coremath.ParseBigInt(validator.Stake)
		penalty := new(big.Int).Set(currentStake)
		penalty.Mul(penalty, big.NewInt(5))
		penalty.Div(penalty, big.NewInt(100))

		// Create slashing record for invalid proposal
		slashingRecord := &types.SlashingRecord{
			ValidatorAddress: evidence.ValidatorAddress,
			SlashedAmount:    penalty.Int64(),
			Condition:        types.InvalidProposal,
			Timestamp:        time.Now(),
		}

		// Apply slashing
		processErr = sm.applySlashing(slashingRecord)
		if processErr == nil {
			log.Printf("⚠️ Slashed validator %s for invalid proposal (penalty: %s)",
				evidence.ValidatorAddress,
				penalty.String(),
			)
		}

	default:
		processErr = fmt.Errorf("unsupported evidence type: %v", evidence.Type)
	}

	if processErr != nil {
		return processErr
	}

	// LAYER 9: Record Cooldown
	sm.cooldowns.RecordSlashing(evidence.ValidatorAddress, evidence.Type)
	sm.metrics.RecordExecution()

	return nil
}

func (sm *SlashingManager) processDoubleVoteEvidence(evidence *SlashingEvidence) error {
	dvEvidence, ok := evidence.Evidence.(*DoubleVoteEvidence)
	if !ok {
		return fmt.Errorf("invalid evidence format for double voting")
	}

	// Evidence is already validated (signatures verified)
	return sm.ApplyDoubleVoteSlashing(dvEvidence.Attestation1, dvEvidence.Attestation2)
}

func (sm *SlashingManager) processSurroundVoteEvidence(evidence *SlashingEvidence) error {
	svEvidence, ok := evidence.Evidence.(*SurroundVoteEvidence)
	if !ok {
		return fmt.Errorf("invalid evidence format for surround voting")
	}

	// Evidence is already validated (signatures verified)
	// Apply slashing for surround voting
	return sm.ApplyDoubleVoteSlashing(svEvidence.InnerAttestation, svEvidence.OuterAttestation)
}

func (sm *SlashingManager) processInvalidProposalEvidence(evidence *SlashingEvidence) error {
	ipEvidence, ok := evidence.Evidence.(*InvalidProposalEvidence)
	if !ok {
		return fmt.Errorf("invalid evidence format")
	}
	proposal := &types.BlockProposal{Proposer: evidence.ValidatorAddress}
	return sm.ReportInvalidProposal(proposal, fmt.Sprintf("%v", ipEvidence.ValidationErrors))
}

// loadFromStorage loads all slashing data from persistent storage into memory
func (sm *SlashingManager) loadFromStorage() error {
	if sm.storage == nil {
		return nil
	}

	data, err := sm.storage.LoadAllSlashingData()
	if err != nil {
		return fmt.Errorf("failed to load slashing data: %w", err)
	}

	sm.jailedValidators = data.JailedValidators
	sm.processedEvidence = data.ProcessedEvidence
	sm.validatorStatus = data.ValidatorStatuses

	return nil
}

// ProcessAttestation checks an attestation for slashable offenses
func (sm *SlashingManager) ProcessAttestation(att *types.Attestation) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// 1. Basic Window Validation
	if time.Since(time.Unix(att.Timestamp, 0)) > sm.config.AttestationWindow {
		return nil
	}

	// ✅ NEW: Startup Grace Period
	// We do NOT want to check for conflicts during the first 100 blocks
	if sm.worldState != nil && sm.worldState.GetHeight() < 100 {
		// We still record it for history, but we skip the "Double Signing" check below
		// or just return early if you don't care about history yet.
		return nil
	}

	// 2. Initialize storage if nil
	if sm.attestationsByValidator == nil {
		sm.attestationsByValidator = make(map[string][]*storage.AttestationRecord)
	}
	if sm.attestationHistory == nil {
		sm.attestationHistory = make(map[string]*storage.AttestationHistory)
	}

	validatorAddress := att.ValidatorAddress

	// 3. Jailing Check
	if sm.isValidatorJailed(validatorAddress) {
		return fmt.Errorf("validator %s is jailed and cannot attest", validatorAddress)
	}

	// 4. SECURITY INTEGRATION: Fork Choice & Reorg Validation
	// This addresses the MEDIUM severity security finding regarding reorg depth.
	if sm.forkChoice != nil {
		// Retrieve current head to compare against the block in the attestation
		currentHead := sm.forkChoice.GetHead() //

		// If the attestation is for a block not on the current head's path, it's a potential reorg
		if currentHead != "" && currentHead != att.BlockHash {
			// We assume a helper or logic to determine depth; for validation,
			// we use the security parameters defined in fork_choice_security.go.

			// Note: Total stake and validator stake should be fetched from WorldState
			totalStake := sm.forkChoice.GetTotalActiveStake()              //
			validator, err := sm.worldState.GetValidator(validatorAddress) //

			if err == nil && validator != nil {
				// Perform the reorg security check before recording the attestation
				// Depth is calculated as the difference between current height and fork point
				approxDepth := int(sm.worldState.GetHeight() - int64(att.Slot))

				err := sm.forkChoice.ValidateReorganization(
					approxDepth,
					att.Epoch,
					validator.Stake,
					totalStake,
				)
				if err != nil {
					// Reject the attestation if it exceeds reorg depth or crosses finality
					return fmt.Errorf("attestation rejected for security: %w", err)
				}
			}
		}
	}

	// 5. Create Attestation Record
	record := &storage.AttestationRecord{
		ValidatorAddress: att.ValidatorAddress,
		Epoch:            att.Epoch,
		Slot:             att.Slot,
		BlockHash:        att.BlockHash,
		Signature:        att.Signature,
		Timestamp:        time.Now(),
	}

	// This is a primary function of the SlashingManager.
	// 6. Double-Signing / Equivocation Check
	prevAttestations := sm.attestationsByValidator[validatorAddress]
	for _, prev := range prevAttestations {
		conflicts := record.Conflicts(prev)
		if conflicts {
			// ✅ Log it but don't error out if we want to be super lenient (optional)
			// But usually, the "return nil" at the top handles it.
			return &DoubleSigningError{
				ConflictingRecord: prev,
			}
		}
	}

	// 7. Update History and Metrics
	sm.attestationsByValidator[validatorAddress] =
		append(sm.attestationsByValidator[validatorAddress], record)

	sm.recordAttestationForDowntime(validatorAddress, att)

	// 8. Memory Management
	// Keep a rolling window of attestations to prevent memory exhaustion
	if len(sm.attestationsByValidator[validatorAddress]) > 1000 {
		attestations := sm.attestationsByValidator[validatorAddress]
		sm.attestationsByValidator[validatorAddress] = attestations[len(attestations)-1000:]
	}

	return nil
}

// ApplyDoubleVoteSlashing is called by ConsensusEngine AFTER evidence verification.
func (sm *SlashingManager) ApplyDoubleVoteSlashing(att1, att2 *types.Attestation) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if att1 == nil || att2 == nil {
		return fmt.Errorf("ApplyDoubleVoteSlashing: nil attestation")
	}

	if att1.ValidatorAddress != att2.ValidatorAddress {
		return fmt.Errorf("ApplyDoubleVoteSlashing: attestations from different validators")
	}

	rec1 := &storage.AttestationRecord{
		ValidatorAddress: att1.ValidatorAddress,
		Epoch:            att1.Epoch,
		BlockHash:        att1.BlockHash,
		Signature:        att1.Signature,
		Timestamp:        time.Unix(att1.Timestamp, 0),
	}

	rec2 := &storage.AttestationRecord{
		ValidatorAddress: att2.ValidatorAddress,
		Epoch:            att2.Epoch,
		BlockHash:        att2.BlockHash,
		Signature:        att2.Signature,
		Timestamp:        time.Unix(att2.Timestamp, 0),
	}

	return sm.slashDoubleVoting(att1, rec1, rec2)
}

// ReportBlockWithholding penalizes a validator for consecutively failing to propose blocks
func (sm *SlashingManager) ReportBlockWithholding(validatorAddr string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// ✅ NEW: Grace Period
	if sm.worldState != nil && sm.worldState.GetHeight() < 100 {
		return nil
	}

	if sm.isValidatorJailed(validatorAddr) {
		return nil
	}

	recentRecords := sm.slashingRecords[validatorAddr]
	cutoffTime := time.Now().Add(-24 * time.Hour)

	for _, record := range recentRecords {
		if record.Condition == types.Downtime && record.Timestamp.After(cutoffTime) {
			fmt.Printf("⏭️  Skipping: Validator %s already slashed for withholding in last 24h\n", validatorAddr)
			return nil
		}
	}

	// 1. Get Balance (BigInt)
	balance, err := sm.worldState.GetBalance(validatorAddr)
	if err != nil {
		return fmt.Errorf("failed to get validator balance: %v", err)
	}

	// 2. Calculate Penalty using coremath (BigInt)
	// JailPenalty is int64 percentage (e.g., 10)
	penaltyPercent := int64(sm.policy.JailPenalty)
	penaltyAmountBig, err := coremath.SafePercentageBig(balance, penaltyPercent)
	if err != nil {
		return fmt.Errorf("failed to calculate penalty: %v", err)
	}

	evidence := types.SlashingEvidence{
		MissedSlots: []uint64{},
	}

	evidenceHash := evidence.Hash()
	if sm.processedEvidence[evidenceHash] {
		return nil
	}

	record := &types.SlashingRecord{
		ValidatorAddress: validatorAddr,
		Condition:        types.Downtime,
		Timestamp:        time.Now(),
		Evidence:         evidence,
		// ✅ FIX: Convert BigInt to int64 for the struct
		SlashedAmount: penaltyAmountBig.Int64(),
		Reason:        "Block Withholding: Exceeded consecutive missed proposal limit",
	}

	if err := sm.applySlashing(record); err != nil {
		return err
	}

	sm.jailValidator(validatorAddr, types.Downtime)
	return nil
}

// ProcessBlockProposal checks a block proposal for slashable offenses
func (sm *SlashingManager) ProcessBlockProposal(proposal *BlockProposal) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	validatorAddress := proposal.Proposer
	if sm.isValidatorJailed(validatorAddress) {
		return fmt.Errorf("validator %s is jailed and cannot propose blocks", validatorAddress)
	}

	return nil
}

// ReportMissedAttestation records that a validator missed their attestation slot
func (sm *SlashingManager) ReportMissedAttestation(validatorKey string, slot uint64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	history, exists := sm.attestationHistory[validatorKey]
	if !exists {
		history = &storage.AttestationHistory{
			ValidatorAddress: validatorKey,
		}
		sm.attestationHistory[validatorKey] = history
	}

	history.RecordMiss(slot)
	missedCount := history.MissedSlots

	if missedCount >= sm.policy.EjectionThreshold {
		sm.forceUnstake(validatorKey)
		sm.slashValidator(validatorKey, sm.policy.JailPenalty, "extended_downtime_ejection", history)
	} else if missedCount >= sm.policy.JailThreshold {
		if !sm.isValidatorJailed(validatorKey) {
			sm.jailValidator(validatorKey, types.Downtime)
			sm.slashValidator(validatorKey, sm.policy.JailPenalty, "extended_downtime_jail", history)
		}
	} else if missedCount >= sm.policy.MajorSlashingStart {
		sm.slashValidator(validatorKey, sm.policy.MajorPenalty, "downtime_major", history)
	} else if missedCount >= sm.policy.MinorSlashingStart {
		sm.slashValidator(validatorKey, sm.policy.MinorPenalty, "downtime_minor", history)
	} else if missedCount >= sm.policy.WarningThreshold {
		sm.recordWarning(validatorKey, "approaching_downtime_threshold")
	}
}

// slashValidator applies a generic slashing penalty based on percentage
func (sm *SlashingManager) slashValidator(validatorKey string, percent int64, reason string, history *storage.AttestationHistory) {
	evidence := types.SlashingEvidence{
		MissedSlots: history.MissedSlotList,
	}

	evidenceHash := fmt.Sprintf("%s-%s-%d", validatorKey, reason, history.MissedSlots)
	if sm.processedEvidence[evidenceHash] {
		return
	}

	// 1. Get Balance (BigInt)
	balance, _ := sm.worldState.GetBalance(validatorKey)

	// 2. Calculate Penalty (BigInt)
	penaltyAmountBig, err := coremath.SafePercentageBig(balance, percent)
	if err != nil {
		log.Printf("Error calculating slash percentage: %v", err)
		return
	}

	record := &types.SlashingRecord{
		ValidatorAddress: validatorKey,
		Condition:        types.Downtime,
		Timestamp:        time.Now(),
		Evidence:         evidence,
		// ✅ FIX: Convert BigInt to int64 for the struct
		SlashedAmount: penaltyAmountBig.Int64(),
		Reason:        fmt.Sprintf("%s (Missed: %d)", reason, history.MissedSlots),
	}

	if err := sm.applySlashing(record); err == nil {
		sm.processedEvidence[evidenceHash] = true
	}

}

// forceUnstake permanently removes a validator from the active set
func (sm *SlashingManager) forceUnstake(validatorKey string) {
	sm.validatorStatus[validatorKey] = storage.ValidatorSlashed
	if sm.storage != nil {
		if err := sm.storage.SaveValidatorStatus(validatorKey, storage.ValidatorSlashed); err != nil {
			fmt.Printf("⚠️ Failed to persist forced unstake status for %s: %v\n", validatorKey, err)
		}
	}
}

func (sm *SlashingManager) recordWarning(validatorKey string, reason string) {
	fmt.Printf("⚠️  [SlashingManager] WARNING: Validator %s %s\n", validatorKey, reason)
}

// ReportInvalidProposal reports that a validator proposed an invalid block
func (sm *SlashingManager) ReportInvalidProposal(proposal *types.BlockProposal, reason string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	validatorAddress := proposal.Proposer
	evidence := types.SlashingEvidence{
		InvalidBlock: proposal,
	}

	evidenceHash := evidence.Hash()
	if sm.processedEvidence[evidenceHash] {
		return nil
	}

	// 1. Get Balance (BigInt)
	balance, err := sm.worldState.GetBalance(validatorAddress)
	if err != nil {
		return fmt.Errorf("failed to get validator balance: %w", err)
	}

	// 2. Calculate Penalty (BigInt)
	penaltyPercent := int64(sm.config.InvalidProposalPenalty)
	penaltyAmountBig, err := coremath.SafePercentageBig(balance, penaltyPercent)
	if err != nil {
		return fmt.Errorf("failed to calculate penalty: %v", err)
	}

	record := &types.SlashingRecord{
		ValidatorAddress: validatorAddress,
		Condition:        types.InvalidProposal,
		Epoch:            proposal.Epoch,
		Timestamp:        time.Now(),
		Evidence:         evidence,
		// ✅ FIX: Convert BigInt to int64 for the struct
		SlashedAmount: penaltyAmountBig.Int64(),
		Reason:        fmt.Sprintf("Invalid proposal: %s", reason),
	}

	return sm.applySlashing(record)
}

// slashDoubleVoting handles double voting offense
func (sm *SlashingManager) slashDoubleVoting(att *types.Attestation, first, second *storage.AttestationRecord) error {
	validatorAddress := att.ValidatorAddress

	evidence := types.SlashingEvidence{
		FirstAttestation: att,
		SecondAttestation: &types.Attestation{
			ValidatorAddress: second.ValidatorAddress,
			BlockHash:        second.BlockHash,
			BlockHeight:      int64(second.Epoch * 32),
			Epoch:            second.Epoch,
			Slot:             second.Epoch * 32,
			Signature:        second.Signature,
			Timestamp:        second.Timestamp.Unix(),
		},
	}

	evidenceHash := evidence.Hash()
	if sm.processedEvidence[evidenceHash] {
		return fmt.Errorf("already slashed for this offense")
	}

	// 1. Get Balance (BigInt)
	balance, err := sm.worldState.GetBalance(validatorAddress)
	if err != nil {
		return fmt.Errorf("failed to get validator balance: %w", err)
	}

	// 2. Calculate Penalty (BigInt)
	penaltyPercent := int64(sm.config.DoubleVotingPenalty)
	penaltyAmountBig, err := coremath.SafePercentageBig(balance, penaltyPercent)
	if err != nil {
		return fmt.Errorf("failed to calculate penalty: %v", err)
	}

	record := &types.SlashingRecord{
		ValidatorAddress: validatorAddress,
		Condition:        types.DoubleVoting,
		Epoch:            att.Epoch,
		Timestamp:        time.Now(),
		Evidence:         evidence,
		// ✅ FIX: Convert BigInt to int64 for the struct
		SlashedAmount: penaltyAmountBig.Int64(),
		Reason:        fmt.Sprintf("Double voting at epoch %d", att.Epoch),
	}

	return sm.applySlashing(record)
}

// slashDowntime handles downtime offense
func (sm *SlashingManager) slashDowntime(validatorAddress string, history *storage.AttestationHistory) error {
	evidence := types.SlashingEvidence{
		MissedSlots: history.MissedSlotList,
	}

	evidenceHash := evidence.Hash()
	if sm.processedEvidence[evidenceHash] {
		return nil
	}

	// 1. Get Balance (BigInt)
	balance, err := sm.worldState.GetBalance(validatorAddress)
	if err != nil {
		return fmt.Errorf("failed to get validator balance: %w", err)
	}

	// 2. Calculate Penalty (BigInt)
	penaltyPercent := int64(sm.config.SlashingDowntime)
	penaltyAmountBig, err := coremath.SafePercentageBig(balance, penaltyPercent)
	if err != nil {
		return fmt.Errorf("failed to calculate penalty: %v", err)
	}

	record := &types.SlashingRecord{
		ValidatorAddress: validatorAddress,
		Condition:        types.Downtime,
		Timestamp:        time.Now(),
		Evidence:         evidence,
		// ✅ FIX: Convert BigInt to int64 for the struct
		SlashedAmount: penaltyAmountBig.Int64(),
		Reason:        fmt.Sprintf("Missed %d attestations out of %d", history.MissedSlots, history.TotalSlots),
	}

	return sm.applySlashing(record)
}

// applySlashing executes the slashing penalty
// applySlashing executes the slashing penalty
func (sm *SlashingManager) applySlashing(record *types.SlashingRecord) error {
	validatorAddress := record.ValidatorAddress

	evidenceHash := record.Evidence.Hash()
	if sm.processedEvidence[evidenceHash] {
		return fmt.Errorf("evidence %s already processed", evidenceHash)
	}
	sm.processedEvidence[evidenceHash] = true

	if sm.storage != nil {
		// M-2 FIX: Save with metadata for pruning
		// FIX APPLIED: Changed string(record.Condition) to record.Condition.String()
		if err := sm.storage.SaveProcessedEvidenceWithMetadata(
			evidenceHash,
			record.Condition.String(), // ✅ Fixed type conversion error
			validatorAddress,
		); err != nil {
			fmt.Printf("⚠️  Failed to persist processed evidence: %v\n", err)
		}
	}

	// 1. Get Current Balance (BigInt)
	currentBalance, err := sm.worldState.GetBalance(validatorAddress)
	if err != nil {
		return fmt.Errorf("failed to get validator balance: %w", err)
	}

	// 2. Parse SlashedAmount (Convert int64 back to BigInt)
	slashedAmountBig := big.NewInt(record.SlashedAmount)

	// 3. Subtract (BigInt)
	newBalance := coremath.Sub(currentBalance, slashedAmountBig)

	// 4. Ensure non-negative
	if newBalance.Sign() < 0 {
		newBalance = big.NewInt(0)
	}

	// 5. Update Balance (Pass BigInt)
	err = sm.worldState.UpdateBalance(validatorAddress, newBalance)
	if err != nil {
		return fmt.Errorf("failed to update validator balance: %w", err)
	}

	if record.Condition == types.DoubleVoting || record.Condition == types.SurroundVoting {
		sm.jailValidator(validatorAddress, record.Condition)
	}

	// 6. Check Minimum Stake (BigInt Comparison)
	minStakeBig := coremath.ParseBigInt(sm.config.MinimumStake)

	// If newBalance < minStakeBig
	if newBalance.Cmp(minStakeBig) < 0 {
		sm.validatorStatus[validatorAddress] = storage.ValidatorSlashed
		if sm.storage != nil {
			if err := sm.storage.SaveValidatorStatus(validatorAddress, storage.ValidatorSlashed); err != nil {
				fmt.Printf("⚠️  Failed to persist validator status: %v\n", err)
			}
		}
	}

	sm.slashingRecords[validatorAddress] = append(sm.slashingRecords[validatorAddress], record)

	if sm.storage != nil {
		if err := sm.storage.SaveSlashingRecord(validatorAddress, record); err != nil {
			fmt.Printf("⚠️  Failed to persist slashing record: %v\n", err)
		}
	}

	return nil
}

// jailValidator temporarily jails a validator
func (sm *SlashingManager) jailValidator(validatorAddress string, reason types.SlashingCondition) {
	jailTime := time.Now()
	duration := time.Duration(sm.config.JailDurationHours) * time.Hour
	releaseTime := jailTime.Add(duration)

	jail := &storage.JailedValidator{
		ValidatorAddress: validatorAddress,
		JailTime:         jailTime,
		ReleaseTime:      releaseTime,
		Reason:           reason,
	}

	sm.jailedValidators[validatorAddress] = jail
	sm.validatorStatus[validatorAddress] = storage.ValidatorJailed

	if sm.storage != nil {
		if err := sm.storage.SaveJailedValidator(validatorAddress, jail); err != nil {
			fmt.Printf("⚠️  Failed to persist jailed validator %s: %v\n", validatorAddress, err)
		}
		if err := sm.storage.SaveValidatorStatus(validatorAddress, storage.ValidatorJailed); err != nil {
			fmt.Printf("⚠️  Failed to persist validator status %s: %v\n", validatorAddress, err)
		}
	}
}

// isValidatorJailed checks if a validator is currently jailed
func (sm *SlashingManager) isValidatorJailed(validatorAddress string) bool {
	jailed, exists := sm.jailedValidators[validatorAddress]
	if !exists {
		return false
	}

	if time.Now().After(jailed.ReleaseTime) {
		delete(sm.jailedValidators, validatorAddress)
		sm.validatorStatus[validatorAddress] = storage.ValidatorActive

		if sm.storage != nil {
			if err := sm.storage.DeleteJailedValidator(validatorAddress); err != nil {
				fmt.Printf("⚠️  Failed to delete jailed validator %s from storage: %v\n", validatorAddress, err)
			}
			if err := sm.storage.SaveValidatorStatus(validatorAddress, storage.ValidatorActive); err != nil {
				fmt.Printf("⚠️  Failed to persist validator status %s: %v\n", validatorAddress, err)
			}
		}

		return false
	}

	return true
}

// recordAttestationForDowntime updates attestation history
func (sm *SlashingManager) recordAttestationForDowntime(validatorKey string, att *types.Attestation) {
	history, exists := sm.attestationHistory[validatorKey]
	if !exists {
		history = &storage.AttestationHistory{
			ValidatorAddress: validatorKey,
		}
		sm.attestationHistory[validatorKey] = history
	}
	history.RecordAttestation(att.Slot)
}

// GetSlashingRecords returns all slashing records for a validator
func (sm *SlashingManager) GetSlashingRecords(validatorKey string) []*types.SlashingRecord {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.slashingRecords[validatorKey]
}

// GetValidatorStatus returns the current status of a validator
func (sm *SlashingManager) GetValidatorStatus(validatorKey string) storage.ValidatorStatus {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	status, exists := sm.validatorStatus[validatorKey]
	if !exists {
		return storage.ValidatorActive
	}
	return status
}

// GetJailedValidators returns all currently jailed validators
func (sm *SlashingManager) GetJailedValidators() []*storage.JailedValidator {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	validators := make([]*storage.JailedValidator, 0, len(sm.jailedValidators))
	for _, v := range sm.jailedValidators {
		validators = append(validators, v)
	}

	return validators
}

// IsValidatorActive checks if a validator can participate in consensus
func (sm *SlashingManager) IsValidatorActive(validatorKey string) bool {
	if sm.isValidatorJailed(validatorKey) {
		log.Printf("[Slashing] Validator %s is jailed (inactive)", validatorKey)
		return false
	}

	status, ok := sm.validatorStatus[validatorKey]
	if !ok {
		status = storage.ValidatorActive
	}

	if status != storage.ValidatorActive {
		log.Printf("[Slashing] Validator %s status is %v (inactive)", validatorKey, status)
		return false
	}

	// Do NOT hard-fail validators just because their balance is below MinimumStake here.
	if sm.worldState != nil {
		minStakeBig := coremath.ParseBigInt(sm.config.MinimumStake)
		if minStakeBig.Sign() > 0 {
			balance, err := sm.worldState.GetBalance(validatorKey)
			if err != nil {
				log.Printf("[Slashing] GetBalance failed for %s: %v (ignoring for activity check)", validatorKey, err)
			} else {
				// if balance < minStakeBig
				if balance.Cmp(minStakeBig) < 0 {
					log.Printf("[Slashing] Validator %s balance %s < minimum %s (NOT disabling via slashing, enforcement handled by staking logic)",
						validatorKey, balance.String(), minStakeBig.String())
				}
			}
		}
	}

	return true
}

// M-2 FIX: pruneEvidencePeriodic runs periodic evidence pruning
func (sm *SlashingManager) pruneEvidencePeriodic() {
	if sm.storage == nil {
		return
	}

	interval := time.Duration(sm.config.PruneIntervalHours) * time.Hour
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		sm.pruneOldEvidence()
	}
}

// M-2 FIX: pruneOldEvidence performs the actual pruning
func (sm *SlashingManager) pruneOldEvidence() {
	// Calculate cutoff times
	archiveAge := time.Now().AddDate(0, 0, -sm.config.EvidenceRetentionDays)
	pruneAge := time.Now().AddDate(0, 0, -sm.config.ArchiveRetentionDays)

	// Run pruning with archival
	archived, pruned, err := sm.storage.PruneAndArchive(archiveAge, pruneAge)
	if err != nil {
		log.Printf("⚠️ Evidence pruning failed: %v", err)
		return
	}

	if archived > 0 || pruned > 0 {
		log.Printf("📦 Evidence pruning completed: archived=%d, pruned=%d", archived, pruned)
	}

	// Optional: Log warning if evidence count is growing too fast
	count, err := sm.storage.GetEvidenceCount()
	if err == nil && count > 100000 {
		log.Printf("⚠️ Evidence count high: %d entries", count)
	}
}

// M-2 FIX: GetPruningStats returns current pruning statistics
func (sm *SlashingManager) GetPruningStats() map[string]interface{} {
	if sm.storage == nil {
		return nil
	}

	stats := sm.storage.GetPruningStats()
	count, _ := sm.storage.GetEvidenceCount()

	return map[string]interface{}{
		"active_evidence_count": count,
		"total_pruned":          stats.TotalPruned,
		"total_archived":        stats.TotalArchived,
		"last_prune_time":       stats.LastPruneTime,
		"last_prune_count":      stats.LastPruneCount,
		"last_archive_count":    stats.LastArchiveCount,
	}
}

// ClearJailStatus removes jail status for a validator (dev mode only)
func (sm *SlashingManager) ClearJailStatus(address string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Remove from jailed validators map
	delete(sm.jailedValidators, address)

	// Clear any slashing records
	delete(sm.slashingRecords, address)

	// Clear attestation history for this validator
	delete(sm.attestationHistory, address)

	// Clear attestations by validator
	delete(sm.attestationsByValidator, address)

	log.Printf("🔓 Cleared jail status for %s", address)
}
