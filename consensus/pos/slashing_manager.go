package pos

import (
	"fmt"
	"sync"
	"time"
)

// WorldStateBalancer interface for balance operations needed by slashing
type WorldStateBalancer interface {
	GetBalance(address string) (int64, error)
	UpdateBalance(address string, newBalance int64) error
}

// SlashingManager handles all slashing-related operations
type SlashingManager struct {
	config *SlashingConfig

	// Track all attestations by validator for double voting detection
	attestationsByValidator map[string][]*AttestationRecord

	// Track jailed validators
	jailedValidators map[string]*JailedValidator

	// Track slashed validators and their records
	slashingRecords map[string][]*SlashingRecord

	// Track attestation history for downtime detection
	attestationHistory map[string]*AttestationHistory

	// Track validator statuses
	validatorStatus map[string]ValidatorStatus

	// Track processed evidence to prevent double slashing
	processedEvidence map[string]bool

	mu sync.RWMutex

	// Reference to world state for stake updates
	worldState WorldStateBalancer
}

// NewSlashingManager creates a new slashing manager
// storage parameter is optional (can be nil) - will be used for persistence in future
func NewSlashingManager(config *SlashingConfig, worldState WorldStateBalancer, storage interface{}) *SlashingManager {
	if config == nil {
		config = DefaultSlashingConfig()
	}

	return &SlashingManager{
		config:                  config,
		attestationsByValidator: make(map[string][]*AttestationRecord),
		jailedValidators:        make(map[string]*JailedValidator),
		slashingRecords:         make(map[string][]*SlashingRecord),
		attestationHistory:      make(map[string]*AttestationHistory),
		validatorStatus:         make(map[string]ValidatorStatus),
		processedEvidence:       make(map[string]bool),
		worldState:              worldState,
		// storage will be added in future update
	}
}

// ProcessAttestation checks an attestation for slashable offenses
func (sm *SlashingManager) ProcessAttestation(att *Attestation) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	validatorAddress := att.ValidatorAddress

	// Check if validator is jailed
	if sm.isValidatorJailed(validatorAddress) {
		return fmt.Errorf("validator %s is jailed and cannot attest", validatorAddress)
	}

	// Create attestation record
	record := &AttestationRecord{
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
		history = &AttestationHistory{
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
func (sm *SlashingManager) ReportInvalidProposal(proposal *BlockProposal, reason string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	validatorAddress := proposal.Proposer

	evidence := SlashingEvidence{
		InvalidBlock: proposal,
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
	record := &SlashingRecord{
		ValidatorAddress: validatorAddress,
		Condition:        InvalidProposal,
		Epoch:            proposal.Epoch,
		Timestamp:        time.Now(),
		Evidence:         evidence,
		SlashedAmount:    penaltyAmount,
		Reason:           fmt.Sprintf("Invalid proposal: %s", reason),
	}

	// Apply slashing
	return sm.applySlashing(record)
}

// slashDoubleVoting handles double voting offense
func (sm *SlashingManager) slashDoubleVoting(att *Attestation, first, second *AttestationRecord) error {
	validatorAddress := att.ValidatorAddress

	fmt.Printf("🚨 slashDoubleVoting called for %s\n", validatorAddress)

	evidence := SlashingEvidence{
		FirstAttestation: att,
		SecondAttestation: &Attestation{
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
	record := &SlashingRecord{
		ValidatorAddress: validatorAddress,
		Condition:        DoubleVoting,
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
func (sm *SlashingManager) slashDowntime(validatorAddress string, history *AttestationHistory) error {
	evidence := SlashingEvidence{
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
	record := &SlashingRecord{
		ValidatorAddress: validatorAddress,
		Condition:        Downtime,
		Timestamp:        time.Now(),
		Evidence:         evidence,
		SlashedAmount:    penaltyAmount,
		Reason:           fmt.Sprintf("Missed %d attestations out of %d", history.MissedSlots, history.TotalSlots),
	}

	// Apply slashing
	return sm.applySlashing(record)
}

// applySlashing executes the slashing penalty
func (sm *SlashingManager) applySlashing(record *SlashingRecord) error {
	validatorAddress := record.ValidatorAddress

	// Mark evidence as processed
	sm.processedEvidence[record.Evidence.Hash()] = true

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
	if record.Condition == DoubleVoting || record.Condition == SurroundVoting {
		sm.jailValidator(validatorAddress, record.Condition)
	}

	// Update validator status
	if newBalance < sm.config.MinimumStake {
		sm.validatorStatus[validatorAddress] = ValidatorSlashed
	}

	// Record the slashing
	sm.slashingRecords[validatorAddress] = append(sm.slashingRecords[validatorAddress], record)

	return nil
}

// jailValidator temporarily jails a validator
func (sm *SlashingManager) jailValidator(validatorAddress string, reason SlashingCondition) {
	jailTime := time.Now()
	releaseTime := jailTime.Add(sm.config.JailDuration)

	sm.jailedValidators[validatorAddress] = &JailedValidator{
		ValidatorAddress: validatorAddress,
		JailTime:         jailTime,
		ReleaseTime:      releaseTime,
		Reason:           reason,
	}

	sm.validatorStatus[validatorAddress] = ValidatorJailed
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
		sm.validatorStatus[validatorAddress] = ValidatorActive
		return false
	}

	return true
}

// recordAttestationForDowntime updates attestation history
func (sm *SlashingManager) recordAttestationForDowntime(validatorKey string, att *Attestation) {
	history, exists := sm.attestationHistory[validatorKey]
	if !exists {
		history = &AttestationHistory{
			ValidatorAddress: validatorKey,
		}
		sm.attestationHistory[validatorKey] = history
	}

	// Use slot from attestation
	history.RecordAttestation(att.Slot)
}

// GetSlashingRecords returns all slashing records for a validator
func (sm *SlashingManager) GetSlashingRecords(validatorKey string) []*SlashingRecord {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return sm.slashingRecords[validatorKey]
}

// GetValidatorStatus returns the current status of a validator
func (sm *SlashingManager) GetValidatorStatus(validatorKey string) ValidatorStatus {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	status, exists := sm.validatorStatus[validatorKey]
	if !exists {
		return ValidatorActive
	}

	return status
}

// GetJailedValidators returns all currently jailed validators
func (sm *SlashingManager) GetJailedValidators() []*JailedValidator {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	validators := make([]*JailedValidator, 0, len(sm.jailedValidators))
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
	if exists && status != ValidatorActive {
		return false
	}

	// Check if has minimum stake
	balance, err := sm.worldState.GetBalance(validatorKey)
	if err != nil || balance < sm.config.MinimumStake {
		return false
	}

	return true
}
