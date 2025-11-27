package pos

import (
	"fmt"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/storage"
	"github.com/thrylos-labs/go-thrylos/types"
)

// WorldStateBalancer interface for balance operations needed by slashing
type WorldStateBalancer interface {
	GetBalance(address string) (int64, error)
	UpdateBalance(address string, newBalance int64) error
}

type DowntimePolicy struct {
	WarningThreshold   uint64 // Warning after X misses
	MinorSlashingStart uint64 // Start slashing at X misses
	MajorSlashingStart uint64 // Escalate penalty at X misses
	JailThreshold      uint64 // Jail validator at X misses
	EjectionThreshold  uint64 // Force unstake at X misses

	MinorPenalty int64         // Percentage penalty (e.g., 1)
	MajorPenalty int64         // Percentage penalty (e.g., 3)
	JailPenalty  int64         // Percentage penalty (e.g., 5)
	JailDuration time.Duration // How long to jail
}

// SlashingManager handles all slashing-related operations
type SlashingManager struct {
	config *storage.SlashingConfig

	// Track all attestations by validator for double voting detection
	attestationsByValidator map[string][]*storage.AttestationRecord

	// Track jailed validators
	jailedValidators map[string]*storage.JailedValidator // ✅

	// Track slashed validators and their records
	slashingRecords map[string][]*types.SlashingRecord // ✅

	// Track attestation history for downtime detection
	attestationHistory map[string]*storage.AttestationHistory

	// Track validator statuses
	validatorStatus map[string]storage.ValidatorStatus // ✅

	// Track processed evidence to prevent double slashing
	processedEvidence map[string]bool

	mu sync.RWMutex

	// Reference to world state for stake updates
	worldState WorldStateBalancer

	// Persistent storage (optional, can be nil for tests)
	storage *storage.SlashingStorage // ✅

	policy DowntimePolicy
}

// NewSlashingManager creates a new slashing manager
// storage parameter is optional (can be nil) - will be used for persistence
func NewSlashingManager(config *storage.SlashingConfig, worldState WorldStateBalancer, slashingStorage *storage.SlashingStorage) *SlashingManager {
	if config == nil {
		config = storage.DefaultSlashingConfig()
	}

	// ✅ NEW: Calculate progressive thresholds
	maxMisses := config.MaxMissedAttestations
	if maxMisses == 0 {
		maxMisses = 100
	}

	policy := DowntimePolicy{
		WarningThreshold:   maxMisses / 20, // 5% misses
		MinorSlashingStart: maxMisses / 10, // 10% misses
		MajorSlashingStart: maxMisses / 5,  // 20% misses
		JailThreshold:      maxMisses / 2,  // 50% misses
		EjectionThreshold:  maxMisses,      // 100% misses

		MinorPenalty: 1, // 1%
		MajorPenalty: 3, // 3%
		JailPenalty:  int64(config.SlashingDowntime),
		JailDuration: time.Duration(config.JailDurationHours) * time.Hour,
	}

	sm := &SlashingManager{
		config:                  config,
		policy:                  policy, // ✅ Set the policy
		attestationsByValidator: make(map[string][]*storage.AttestationRecord),
		jailedValidators:        make(map[string]*storage.JailedValidator),
		slashingRecords:         make(map[string][]*types.SlashingRecord),
		attestationHistory:      make(map[string]*storage.AttestationHistory),
		validatorStatus:         make(map[string]storage.ValidatorStatus),
		processedEvidence:       make(map[string]bool),
		worldState:              worldState,
		storage:                 slashingStorage,
	}

	// Load data from storage if available
	if slashingStorage != nil {
		if err := sm.loadFromStorage(); err != nil {
			// Log error but don't fail - start with empty state
			fmt.Printf("⚠️  Warning: Failed to load slashing data from storage: %v\n", err)
		} else {
			fmt.Printf("✅ Loaded slashing data from storage\n")
		}
	}

	return sm
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

	// Load jailed validators
	sm.jailedValidators = data.JailedValidators
	fmt.Printf("   📥 Loaded %d jailed validators\n", len(sm.jailedValidators))

	// Load processed evidence
	sm.processedEvidence = data.ProcessedEvidence
	fmt.Printf("   📥 Loaded %d processed evidence records\n", len(sm.processedEvidence))

	// Load validator statuses
	sm.validatorStatus = data.ValidatorStatuses
	fmt.Printf("   📥 Loaded %d validator statuses\n", len(sm.validatorStatus))

	return nil
}

// ProcessAttestation checks an attestation for slashable offenses
func (sm *SlashingManager) ProcessAttestation(att *types.Attestation) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	validatorAddress := att.ValidatorAddress

	// Check if validator is jailed
	if sm.isValidatorJailed(validatorAddress) {
		return fmt.Errorf("validator %s is jailed and cannot attest", validatorAddress)
	}

	// Create attestation record
	record := &storage.AttestationRecord{
		ValidatorAddress: att.ValidatorAddress,
		Epoch:            att.Epoch,
		BlockHash:        att.BlockHash,
		Signature:        att.Signature,
		Timestamp:        time.Now(),
	}

	// Get validator's previous attestations
	prevAttestations := sm.attestationsByValidator[validatorAddress]

	fmt.Printf("🔍 DEBUG: Checking %d previous attestations for validator %s\n", len(prevAttestations), validatorAddress)

	// Check for double voting
	for i, prev := range prevAttestations {
		fmt.Printf("🔍 DEBUG [%d]: Previous - Epoch=%d, Hash=%s\n", i, prev.Epoch, prev.BlockHash)
		fmt.Printf("🔍 DEBUG [%d]: Current  - Epoch=%d, Hash=%s\n", i, record.Epoch, record.BlockHash)

		conflicts := record.Conflicts(prev)
		fmt.Printf("🔍 DEBUG [%d]: Conflicts? %v (same epoch: %v, diff hash: %v)\n",
			i, conflicts, record.Epoch == prev.Epoch, record.BlockHash != prev.BlockHash)

		if conflicts {
			fmt.Println("🚨 DEBUG: CONFLICT DETECTED! Calling slashDoubleVoting...")
			return sm.slashDoubleVoting(att, record, prev)
		}
	}

	fmt.Println("✅ DEBUG: No conflicts found, recording attestation")

	// Record this attestation
	sm.attestationsByValidator[validatorAddress] = append(sm.attestationsByValidator[validatorAddress], record)

	// Update attestation history for downtime tracking
	sm.recordAttestationForDowntime(validatorAddress, att)

	// Clean up old attestations (keep only last 1000 per validator)
	if len(sm.attestationsByValidator[validatorAddress]) > 1000 {
		sm.attestationsByValidator[validatorAddress] = sm.attestationsByValidator[validatorAddress][len(sm.attestationsByValidator[validatorAddress])-1000:]
	}

	return nil
}

// ReportBlockWithholding penalizes a validator for consecutively failing to propose blocks
func (sm *SlashingManager) ReportBlockWithholding(validatorAddr string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// 1. Check if already jailed to avoid double jeopardy
	if sm.isValidatorJailed(validatorAddr) {
		return nil
	}

	fmt.Printf("🚨 REPORT: Validator %s has withheld blocks (excessive missed proposals)\n", validatorAddr)

	// 2. Get Validator Balance
	balance, err := sm.worldState.GetBalance(validatorAddr)
	if err != nil {
		return fmt.Errorf("failed to get validator balance: %v", err)
	}

	// 3. Define Penalty
	// ✅ FIX: Use JailPenalty (which comes from config.SlashingDowntime) instead of MinorPenalty
	penaltyPercent := sm.policy.JailPenalty
	penaltyAmount := balance * penaltyPercent / 100

	// 4. Create Evidence Structure
	evidence := types.SlashingEvidence{
		MissedSlots: []uint64{},
	}

	// 5. Create Slashing Record
	record := &types.SlashingRecord{
		ValidatorAddress: validatorAddr,
		Condition:        types.Downtime,
		Timestamp:        time.Now(),
		Evidence:         evidence,
		SlashedAmount:    penaltyAmount,
		Reason:           "Block Withholding: Exceeded consecutive missed proposal limit",
	}

	// 6. Apply Slashing
	if err := sm.applySlashing(record); err != nil {
		return err
	}

	// 7. Jail the validator
	sm.jailValidator(validatorAddr, types.Downtime)

	fmt.Printf("⚖️  Slashed and Jailed validator %s for block withholding\n", validatorAddr)

	return nil
}

// ProcessBlockProposal checks a block proposal for slashable offenses
func (sm *SlashingManager) ProcessBlockProposal(proposal *BlockProposal) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	validatorAddress := proposal.Proposer

	// Check if validator is jailed
	if sm.isValidatorJailed(validatorAddress) {
		return fmt.Errorf("validator %s is jailed and cannot propose blocks", validatorAddress)
	}

	// Additional validation can be added here
	// For now, invalid proposals are detected elsewhere and reported via ReportInvalidProposal

	return nil
}

// ReportMissedAttestation records that a validator missed their attestation slot
func (sm *SlashingManager) ReportMissedAttestation(validatorKey string, slot uint64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Get or create attestation history
	history, exists := sm.attestationHistory[validatorKey]
	if !exists {
		history = &storage.AttestationHistory{
			ValidatorAddress: validatorKey,
		}
		sm.attestationHistory[validatorKey] = history
	}

	history.RecordMiss(slot)
	missedCount := history.MissedSlots

	fmt.Printf("⚠️ Validator %s missed attestation (Total: %d)\n", validatorKey, missedCount)

	// ✅ NEW: Progressive penalties logic
	if missedCount >= sm.policy.EjectionThreshold {
		// 1. Ejection: Force Unstake
		fmt.Printf("⛔ EJECTING validator %s for excessive downtime\n", validatorKey)
		sm.forceUnstake(validatorKey)
		sm.slashValidator(validatorKey, sm.policy.JailPenalty, "extended_downtime_ejection", history)

	} else if missedCount >= sm.policy.JailThreshold {
		// 2. Jail: Suspend validator + High Penalty
		if !sm.isValidatorJailed(validatorKey) {
			fmt.Printf("🔒 JAILING validator %s for downtime\n", validatorKey)
			sm.jailValidator(validatorKey, types.Downtime)
			sm.slashValidator(validatorKey, sm.policy.JailPenalty, "extended_downtime_jail", history)
		}

	} else if missedCount >= sm.policy.MajorSlashingStart {
		// 3. Major Slashing
		sm.slashValidator(validatorKey, sm.policy.MajorPenalty, "downtime_major", history)

	} else if missedCount >= sm.policy.MinorSlashingStart {
		// 4. Minor Slashing
		sm.slashValidator(validatorKey, sm.policy.MinorPenalty, "downtime_minor", history)

	} else if missedCount >= sm.policy.WarningThreshold {
		// 5. Warning
		sm.recordWarning(validatorKey, "approaching_downtime_threshold")
	}
}

// slashValidator applies a generic slashing penalty based on percentage
func (sm *SlashingManager) slashValidator(validatorKey string, percent int64, reason string, history *storage.AttestationHistory) {
	// Construct evidence
	evidence := types.SlashingEvidence{
		MissedSlots: history.MissedSlotList,
	}

	// Deduplicate logic
	evidenceHash := fmt.Sprintf("%s-%s-%d", validatorKey, reason, history.MissedSlots)
	if sm.processedEvidence[evidenceHash] {
		return
	}

	balance, _ := sm.worldState.GetBalance(validatorKey)
	penaltyAmount := balance * percent / 100

	record := &types.SlashingRecord{
		ValidatorAddress: validatorKey,
		Condition:        types.Downtime,
		Timestamp:        time.Now(),
		Evidence:         evidence,
		SlashedAmount:    penaltyAmount,
		Reason:           fmt.Sprintf("%s (Missed: %d)", reason, history.MissedSlots),
	}

	if err := sm.applySlashing(record); err == nil {
		sm.processedEvidence[evidenceHash] = true
	}
}

// forceUnstake permanently removes a validator from the active set due to excessive downtime
// Assumes lock is held by caller
func (sm *SlashingManager) forceUnstake(validatorKey string) {
	// Mark as slashed (permanently removed from active set)
	sm.validatorStatus[validatorKey] = storage.ValidatorSlashed

	// Persist to storage if available
	if sm.storage != nil {
		if err := sm.storage.SaveValidatorStatus(validatorKey, storage.ValidatorSlashed); err != nil {
			fmt.Printf("⚠️ Failed to persist forced unstake status for %s: %v\n", validatorKey, err)
		}
	}
}

// recordWarning logs a warning for a validator nearing the slashing threshold
func (sm *SlashingManager) recordWarning(validatorKey string, reason string) {
	// Log to console (in production this would likely emit a metric or event)
	fmt.Printf("⚠️  [SlashingManager] WARNING: Validator %s %s\n", validatorKey, reason)
}

// ReportInvalidProposal reports that a validator proposed an invalid block
func (sm *SlashingManager) ReportInvalidProposal(proposal *types.BlockProposal, reason string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	validatorAddress := proposal.Proposer

	evidence := types.SlashingEvidence{
		InvalidBlock: proposal, // Now this matches!
	}

	// Check if already processed
	evidenceHash := evidence.Hash()
	if sm.processedEvidence[evidenceHash] {
		return nil // Already slashed for this
	}

	// Get validator's stake
	balance, err := sm.worldState.GetBalance(validatorAddress)
	if err != nil {
		return fmt.Errorf("failed to get validator balance: %w", err)
	}

	// Calculate penalty
	penaltyAmount := balance * int64(sm.config.InvalidProposalPenalty) / 100

	// Create slashing record
	record := &types.SlashingRecord{
		ValidatorAddress: validatorAddress,
		// CHANGE THIS: storage.InvalidProposal -> types.InvalidProposal
		Condition:     types.InvalidProposal,
		Epoch:         proposal.Epoch,
		Timestamp:     time.Now(),
		Evidence:      evidence,
		SlashedAmount: penaltyAmount,
		Reason:        fmt.Sprintf("Invalid proposal: %s", reason),
	}

	// Apply slashing
	return sm.applySlashing(record)
}

// slashDoubleVoting handles double voting offense
func (sm *SlashingManager) slashDoubleVoting(att *types.Attestation, first, second *storage.AttestationRecord) error {
	validatorAddress := att.ValidatorAddress

	fmt.Printf("🚨 slashDoubleVoting called for %s\n", validatorAddress)

	evidence := types.SlashingEvidence{
		FirstAttestation: att,
		SecondAttestation: &types.Attestation{
			ValidatorAddress: second.ValidatorAddress,
			BlockHash:        second.BlockHash,
			BlockHeight:      int64(second.Epoch * 32), // Approximate start of epoch
			Epoch:            second.Epoch,
			Slot:             second.Epoch * 32, // Approximate
			Signature:        second.Signature,
			Timestamp:        second.Timestamp.Unix(),
		},
	}

	// Check if already processed
	evidenceHash := evidence.Hash()
	fmt.Printf("🔍 Evidence hash: %s\n", evidenceHash)
	if sm.processedEvidence[evidenceHash] {
		fmt.Println("⚠️  Already processed this evidence")
		return fmt.Errorf("already slashed for this offense")
	}

	// Get validator's stake
	balance, err := sm.worldState.GetBalance(validatorAddress)
	if err != nil {
		fmt.Printf("❌ Failed to get balance: %v\n", err)
		return fmt.Errorf("failed to get validator balance: %w", err)
	}
	fmt.Printf("💰 Current balance: %d\n", balance)

	// Calculate penalty (most severe)
	penaltyAmount := balance * int64(sm.config.DoubleVotingPenalty) / 100
	fmt.Printf("⚖️  Penalty amount: %d (%d%%)\n", penaltyAmount, sm.config.DoubleVotingPenalty)

	// Create slashing record
	record := &types.SlashingRecord{
		ValidatorAddress: validatorAddress,
		Condition:        types.DoubleVoting,
		Epoch:            att.Epoch,
		Timestamp:        time.Now(),
		Evidence:         evidence,
		SlashedAmount:    penaltyAmount,
		Reason:           fmt.Sprintf("Double voting at epoch %d", att.Epoch),
	}

	// Apply slashing
	fmt.Println("📝 Calling applySlashing...")
	err = sm.applySlashing(record)
	if err != nil {
		fmt.Printf("❌ applySlashing failed: %v\n", err)
	} else {
		fmt.Println("✅ applySlashing succeeded")
	}
	return err
}

// slashDowntime handles downtime offense
// slashDowntime handles downtime offense
// slashDowntime handles downtime offense
func (sm *SlashingManager) slashDowntime(validatorAddress string, history *storage.AttestationHistory) error {
	evidence := types.SlashingEvidence{
		MissedSlots: history.MissedSlotList,
	}

	// Check if already processed
	evidenceHash := evidence.Hash()
	if sm.processedEvidence[evidenceHash] {
		return nil // Already slashed
	}

	// Get validator's stake
	balance, err := sm.worldState.GetBalance(validatorAddress)
	if err != nil {
		return fmt.Errorf("failed to get validator balance: %w", err)
	}

	// ✅ FIX: Use SlashingDowntime instead of DowntimePenalty
	penaltyAmount := balance * int64(sm.config.SlashingDowntime) / 100

	// Create slashing record
	record := &types.SlashingRecord{
		ValidatorAddress: validatorAddress,
		Condition:        types.Downtime,
		Timestamp:        time.Now(),
		Evidence:         evidence,
		SlashedAmount:    penaltyAmount,
		Reason:           fmt.Sprintf("Missed %d attestations out of %d", history.MissedSlots, history.TotalSlots),
	}

	// Apply slashing
	return sm.applySlashing(record)
}

// applySlashing executes the slashing penalty
func (sm *SlashingManager) applySlashing(record *types.SlashingRecord) error {
	validatorAddress := record.ValidatorAddress

	// Mark evidence as processed
	evidenceHash := record.Evidence.Hash()
	sm.processedEvidence[evidenceHash] = true

	// Persist evidence
	if sm.storage != nil {
		if err := sm.storage.SaveProcessedEvidence(evidenceHash); err != nil {
			fmt.Printf("⚠️  Failed to persist processed evidence: %v\n", err)
		}
	}

	// Reduce validator's stake
	currentBalance, err := sm.worldState.GetBalance(validatorAddress)
	if err != nil {
		return fmt.Errorf("failed to get validator balance: %w", err)
	}

	newBalance := currentBalance - record.SlashedAmount
	if newBalance < 0 {
		newBalance = 0
	}

	err = sm.worldState.UpdateBalance(validatorAddress, newBalance)
	if err != nil {
		return fmt.Errorf("failed to update validator balance: %w", err)
	}

	// Jail validator for severe offenses
	if record.Condition == types.DoubleVoting || record.Condition == types.SurroundVoting {
		sm.jailValidator(validatorAddress, record.Condition)
	}

	// Update validator status
	if newBalance < sm.config.MinimumStake {
		sm.validatorStatus[validatorAddress] = storage.ValidatorSlashed

		// Persist status
		if sm.storage != nil {
			if err := sm.storage.SaveValidatorStatus(validatorAddress, storage.ValidatorSlashed); err != nil {
				fmt.Printf("⚠️  Failed to persist validator status: %v\n", err)
			}
		}
	}

	// Record the slashing
	sm.slashingRecords[validatorAddress] = append(sm.slashingRecords[validatorAddress], record)

	// Persist slashing record
	if sm.storage != nil {
		if err := sm.storage.SaveSlashingRecord(validatorAddress, record); err != nil {
			fmt.Printf("⚠️  Failed to persist slashing record: %v\n", err)
		}
	}

	return nil
}

// jailValidator temporarily jails a validator
// jailValidator temporarily jails a validator
func (sm *SlashingManager) jailValidator(validatorAddress string, reason types.SlashingCondition) {
	jailTime := time.Now()

	// ✅ FIX: Convert int hours to time.Duration
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

	// Persist to storage
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

	// Check if jail time has expired
	if time.Now().After(jailed.ReleaseTime) {
		delete(sm.jailedValidators, validatorAddress)
		sm.validatorStatus[validatorAddress] = storage.ValidatorActive

		// Persist release from jail
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

	// Use slot from attestation
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
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// Check if jailed
	if sm.isValidatorJailed(validatorKey) {
		return false
	}

	// Check if slashed below minimum
	status, exists := sm.validatorStatus[validatorKey]
	if exists && status != storage.ValidatorActive {
		return false
	}

	// Check if has minimum stake
	balance, err := sm.worldState.GetBalance(validatorKey)
	if err != nil || balance < sm.config.MinimumStake {
		return false
	}

	return true
}
