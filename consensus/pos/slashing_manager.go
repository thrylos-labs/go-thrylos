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

}

// NewSlashingManager creates a new slashing manager
// storage parameter is optional (can be nil) - will be used for persistence
func NewSlashingManager(config *storage.SlashingConfig, worldState WorldStateBalancer, slashingStorage *storage.SlashingStorage) *SlashingManager {
	if config == nil {
		config = storage.DefaultSlashingConfig()
	}

	sm := &SlashingManager{
		config:                  config,
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

	// Check if validator exceeded max missed attestations
	if history.MissedSlots >= sm.config.MaxMissedAttestations {
		sm.slashDowntime(validatorKey, history)
	}
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
			BlockHeight:      int64(second.Epoch * 100), // Approximate
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

	// Calculate penalty (less severe)
	penaltyAmount := balance * int64(sm.config.DowntimePenalty) / 100

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
func (sm *SlashingManager) jailValidator(validatorAddress string, reason types.SlashingCondition) {
	jailTime := time.Now()
	releaseTime := jailTime.Add(sm.config.JailDuration)

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
