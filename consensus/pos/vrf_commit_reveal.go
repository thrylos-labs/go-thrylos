// consensus/pos/vrf_commit_reveal.go
// Implementation of commit-reveal scheme for VRF protection
// Addresses H-3: VRF Seed Predictability Window

package pos

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// ============================================================================
// COMMIT-REVEAL PROTOCOL FOR VRF PROTECTION
// ============================================================================

// CommitRevealManager manages the commit-reveal protocol for VRF
type CommitRevealManager struct {
	// Pending commitments by slot and validator
	commitments map[uint64]map[string]*VRFCommitment

	// Verified reveals
	reveals map[uint64]map[string]*VRFReveal

	// Configuration
	revealDeadlineSlots int64 // Must reveal within N slots
	maxPendingSlots     int   // Clean up old data after N slots

	mu                  sync.RWMutex
	minRevealDelaySlots int64 // Minimum slots between commit and reveal

	// NEW: Timeout enforcement
	timeoutMonitor  *time.Ticker
	stopMonitor     chan bool
	slashingManager *SlashingManager // Reference to slashing system

	// NEW: Network partition protection
	lastNetworkCheck   time.Time
	networkPartitioned bool
}

// VRFCommitment represents a commitment to a future VRF proof
type VRFCommitment struct {
	ValidatorAddress string `json:"validator_address"`
	Commitment       []byte `json:"commitment"` // H(VRF_output || VRF_proof || nonce)
	Nonce            []byte `json:"-"`          // Kept secret until reveal
	Slot             uint64 `json:"slot"`
	Epoch            uint64 `json:"epoch"`
	RevealDeadline   int64  `json:"reveal_deadline"` // Unix timestamp
	CommittedAt      int64  `json:"committed_at"`

	// Additional security
	BlockHash string `json:"block_hash"` // Block hash when committed
	Signature []byte `json:"signature"`  // Validator's signature
}

// VRFReveal represents the reveal phase of a VRF commitment
type VRFReveal struct {
	ValidatorAddress string `json:"validator_address"`
	VRFOutput        []byte `json:"vrf_output"`
	VRFProof         []byte `json:"vrf_proof"`
	Nonce            []byte `json:"nonce"`
	Slot             uint64 `json:"slot"`
	Epoch            uint64 `json:"epoch"`
	RevealedAt       int64  `json:"revealed_at"`

	// Link to commitment
	CommitmentHash []byte `json:"commitment_hash"`
}

// CommitRevealResult contains the result of commitment/reveal operations
type CommitRevealResult struct {
	Success          bool   `json:"success"`
	Error            string `json:"error,omitempty"`
	CommitmentHash   []byte `json:"commitment_hash,omitempty"`
	ValidatorAddress string `json:"validator_address"`
	Slot             uint64 `json:"slot"`
	Phase            string `json:"phase"` // "commit" or "reveal"
}

// NewCommitRevealManager creates a new commit-reveal manager
// NewCommitRevealManager creates a new commit-reveal manager with timeout enforcement
func NewCommitRevealManager(revealDeadlineSlots int64, slashingMgr *SlashingManager) *CommitRevealManager {
	crm := &CommitRevealManager{
		commitments:         make(map[uint64]map[string]*VRFCommitment),
		reveals:             make(map[uint64]map[string]*VRFReveal),
		revealDeadlineSlots: revealDeadlineSlots,
		minRevealDelaySlots: 2, // MUST wait at least 2 slots
		maxPendingSlots:     1000,

		// NEW: Initialize timeout monitoring
		stopMonitor:      make(chan bool),
		slashingManager:  slashingMgr,
		lastNetworkCheck: time.Now(),
	}

	// Start automatic timeout monitoring
	crm.startTimeoutMonitor()

	return crm
}

// ============================================================================
// TIMEOUT ENFORCEMENT & SLASHING
// ============================================================================

// startTimeoutMonitor runs a background goroutine to check for expired commitments
func (crm *CommitRevealManager) startTimeoutMonitor() {
	// Check every 30 seconds
	crm.timeoutMonitor = time.NewTicker(30 * time.Second)

	go func() {
		for {
			select {
			case <-crm.timeoutMonitor.C:
				crm.enforceTimeouts()
			case <-crm.stopMonitor:
				crm.timeoutMonitor.Stop()
				return
			}
		}
	}()
}

// enforceTimeouts checks for expired commitments and triggers slashing
func (crm *CommitRevealManager) enforceTimeouts() {
	currentTime := time.Now().Unix()
	slashableValidators := crm.GetSlashableValidators(currentTime)

	// Slash each validator that missed their reveal deadline
	for _, validatorAddr := range slashableValidators {
		crm.slashMissedReveal(validatorAddr, currentTime)
	}
}

// slashMissedReveal applies slashing penalty for missed reveal
func (crm *CommitRevealManager) slashMissedReveal(validatorAddr string, currentTime int64) {
	crm.mu.Lock()
	defer crm.mu.Unlock()

	var missedCommitment *VRFCommitment
	for _, slotCommitments := range crm.commitments {
		if commitment, exists := slotCommitments[validatorAddr]; exists {
			if currentTime > commitment.RevealDeadline {
				if !crm.hasRevealUnsafe(commitment.Slot, validatorAddr) {
					missedCommitment = commitment
					break
				}
			}
		}
	}

	if missedCommitment == nil {
		return
	}

	// ✅ Create proper slashing evidence
	if crm.slashingManager != nil {
		missedEvidence := &MissedVRFRevealEvidence{
			Slot:             missedCommitment.Slot,
			Epoch:            missedCommitment.Epoch,
			CommitmentHash:   missedCommitment.Commitment,
			CommittedAt:      missedCommitment.CommittedAt,
			RevealDeadline:   missedCommitment.RevealDeadline,
			CurrentTimestamp: currentTime,
		}

		evidence := &SlashingEvidence{
			Type:             EvidenceMissedVRFReveal,
			ValidatorAddress: validatorAddr,
			Evidence:         missedEvidence,
			ReporterAddress:  "system", // System-generated evidence
			Timestamp:        currentTime,
		}

		// Submit to slashing manager
		if err := crm.slashingManager.ProcessEvidence(evidence); err != nil {
			log.Printf("⚠️ Failed to process missed VRF reveal evidence: %v", err)
		}
	}

	// Clean up the missed commitment
	delete(crm.commitments[missedCommitment.Slot], validatorAddr)
}

// Stop stops the timeout monitor (call on shutdown)
func (crm *CommitRevealManager) Stop() {
	close(crm.stopMonitor)
}

// ============================================================================
// DETERMINISTIC FALLBACK RANDOMNESS
// ============================================================================

// GetRandomnessWithFallback returns VRF randomness with fallback to block hash
func (crm *CommitRevealManager) GetRandomnessWithFallback(
	slot uint64,
	blockHash string,
) ([]byte, error) {
	crm.mu.RLock()
	defer crm.mu.RUnlock()

	// First, try to get revealed VRF outputs
	slotReveals, hasReveals := crm.reveals[slot]

	if hasReveals && len(slotReveals) > 0 {
		// Combine all revealed VRF outputs with XOR
		// This prevents any single validator from controlling randomness
		combined := make([]byte, 32)
		count := 0

		for _, reveal := range slotReveals {
			if len(reveal.VRFOutput) == 32 {
				for i := 0; i < 32; i++ {
					combined[i] ^= reveal.VRFOutput[i]
				}
				count++
			}
		}

		if count > 0 {
			// Hash the combined output for additional mixing
			h := sha256.New()
			h.Write(combined)
			h.Write([]byte(fmt.Sprintf("slot:%d", slot)))
			return h.Sum(nil), nil
		}
	}

	// FALLBACK: Use deterministic randomness from block hash
	// This ensures the protocol never stalls
	if blockHash != "" {
		h := sha256.New()
		h.Write([]byte(blockHash))
		h.Write([]byte(fmt.Sprintf("slot:%d:fallback", slot)))
		fallbackRandomness := h.Sum(nil)

		log.Printf("⚠️ Using fallback randomness for slot %d (no VRF reveals available)", slot)
		return fallbackRandomness, nil
	}

	return nil, fmt.Errorf("no randomness available for slot %d", slot)
}

// GetRandomnessSources returns information about randomness sources used
func (crm *CommitRevealManager) GetRandomnessSources(slot uint64) map[string]interface{} {
	crm.mu.RLock()
	defer crm.mu.RUnlock()

	slotReveals, hasReveals := crm.reveals[slot]

	vrfCount := 0
	if hasReveals {
		vrfCount = len(slotReveals)
	}

	return map[string]interface{}{
		"slot":           slot,
		"vrf_reveals":    vrfCount,
		"using_fallback": vrfCount == 0,
		"source":         map[bool]string{true: "block_hash_fallback", false: "vrf_combined"}[vrfCount == 0],
	}
}

// ============================================================================
// PHASE 1: COMMITMENT
// ============================================================================

// CreateCommitment creates a commitment for a VRF proof
// This should be called BEFORE generating the actual VRF proof
func (crm *CommitRevealManager) CreateCommitment(
	validatorAddress string,
	slot uint64,
	epoch uint64,
	blockHash string,
) (*VRFCommitment, []byte, error) {

	// Generate random nonce (32 bytes)
	nonce, err := generateSecureNonce()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Calculate reveal deadline (N slots in the future)
	currentTime := time.Now().Unix()
	revealDeadline := currentTime + (crm.revealDeadlineSlots * 6) // Assuming 6s slots

	// Create commitment (to be filled with actual VRF data later)
	commitment := &VRFCommitment{
		ValidatorAddress: validatorAddress,
		Nonce:            nonce,
		Slot:             slot,
		Epoch:            epoch,
		RevealDeadline:   revealDeadline,
		CommittedAt:      currentTime,
		BlockHash:        blockHash,
	}

	return commitment, nonce, nil
}

// CommitVRF stores a VRF commitment on-chain
func (crm *CommitRevealManager) CommitVRF(
	vrfProof *VRFProof,
	nonce []byte,
	validatorAddress string,
	slot uint64,
	epoch uint64,
	blockHash string,
) (*VRFCommitment, error) {

	if vrfProof == nil || len(vrfProof.Output) == 0 {
		return nil, errors.New("VRF proof is empty")
	}

	if len(nonce) != 32 {
		return nil, errors.New("nonce must be 32 bytes")
	}

	// Compute commitment hash: H(VRF_output || VRF_proof || nonce)
	commitmentHash := computeCommitmentHash(vrfProof, nonce)

	currentTime := time.Now().Unix()
	revealDeadline := currentTime + (crm.revealDeadlineSlots * 6)

	commitment := &VRFCommitment{
		ValidatorAddress: validatorAddress,
		Commitment:       commitmentHash,
		Nonce:            nonce, // Keep secret locally, not sent on-chain
		Slot:             slot,
		Epoch:            epoch,
		RevealDeadline:   revealDeadline,
		CommittedAt:      currentTime,
		BlockHash:        blockHash,
	}

	// Store commitment
	crm.mu.Lock()
	defer crm.mu.Unlock()

	if crm.commitments[slot] == nil {
		crm.commitments[slot] = make(map[string]*VRFCommitment)
	}

	// Check for duplicate commitment
	if existing, exists := crm.commitments[slot][validatorAddress]; exists {
		return nil, fmt.Errorf("validator %s already committed for slot %d at %d",
			validatorAddress, slot, existing.CommittedAt)
	}

	crm.commitments[slot][validatorAddress] = commitment

	return commitment, nil
}

// ============================================================================
// PHASE 2: REVEAL
// ============================================================================

// RevealVRF reveals a previously committed VRF proof
func (crm *CommitRevealManager) RevealVRF(
	vrfProof *VRFProof,
	nonce []byte,
	validatorAddress string,
	slot uint64,
	epoch uint64,
) (*VRFReveal, error) {
	crm.mu.Lock()
	defer crm.mu.Unlock()

	commitment, exists := crm.commitments[slot][validatorAddress]
	if !exists {
		return nil, fmt.Errorf("no commitment found for validator %s at slot %d",
			validatorAddress, slot)
	}

	currentTime := time.Now().Unix()

	// NEW: Check minimum delay requirement
	minRevealTime := commitment.CommittedAt + (crm.minRevealDelaySlots * 6)
	if currentTime < minRevealTime {
		return nil, fmt.Errorf("reveal too early: must wait %d more seconds",
			minRevealTime-currentTime)
	}

	// Check reveal deadline hasn't passed
	if currentTime > commitment.RevealDeadline {
		return nil, fmt.Errorf("reveal deadline passed")
	}

	// 3. Verify commitment matches reveal
	expectedCommitment := computeCommitmentHash(vrfProof, nonce)
	if !bytesEqual(commitment.Commitment, expectedCommitment) {
		return nil, errors.New("VRF reveal does not match commitment")
	}

	// 4. Create reveal record
	reveal := &VRFReveal{
		ValidatorAddress: validatorAddress,
		VRFOutput:        vrfProof.Output,
		VRFProof:         vrfProof.Proof,
		Nonce:            nonce,
		Slot:             slot,
		Epoch:            epoch,
		RevealedAt:       currentTime,
		CommitmentHash:   commitment.Commitment,
	}

	// 5. Store reveal
	if crm.reveals[slot] == nil {
		crm.reveals[slot] = make(map[string]*VRFReveal)
	}
	crm.reveals[slot][validatorAddress] = reveal

	return reveal, nil
}

// Add new function to identify validators who should be slashed:
func (crm *CommitRevealManager) GetSlashableValidators(currentTime int64) []string {
	crm.mu.RLock()
	defer crm.mu.RUnlock()

	slashable := make([]string, 0)

	for _, slotCommitments := range crm.commitments {
		for validatorAddr, commitment := range slotCommitments {
			// If deadline passed and no reveal exists
			if currentTime > commitment.RevealDeadline {
				if !crm.hasRevealUnsafe(commitment.Slot, validatorAddr) {
					slashable = append(slashable, validatorAddr)
				}
			}
		}
	}

	return slashable
}

// ============================================================================
// VERIFICATION
// ============================================================================

// VerifyCommitment verifies a commitment is valid
func (crm *CommitRevealManager) VerifyCommitment(commitment *VRFCommitment) error {
	if commitment == nil {
		return errors.New("commitment is nil")
	}

	if len(commitment.Commitment) != 32 {
		return errors.New("commitment hash must be 32 bytes")
	}

	if commitment.ValidatorAddress == "" {
		return errors.New("validator address is empty")
	}

	if commitment.Slot == 0 {
		return errors.New("slot cannot be zero")
	}

	currentTime := time.Now().Unix()
	if commitment.CommittedAt > currentTime {
		return errors.New("commitment time is in the future")
	}

	if commitment.RevealDeadline <= commitment.CommittedAt {
		return errors.New("reveal deadline must be after commitment time")
	}

	return nil
}

// VerifyReveal verifies a reveal matches its commitment
func (crm *CommitRevealManager) VerifyReveal(
	commitment *VRFCommitment,
	reveal *VRFReveal,
) error {

	if commitment == nil || reveal == nil {
		return errors.New("commitment or reveal is nil")
	}

	// 1. Check addresses match
	if commitment.ValidatorAddress != reveal.ValidatorAddress {
		return errors.New("validator addresses do not match")
	}

	// 2. Check slots match
	if commitment.Slot != reveal.Slot {
		return errors.New("slot numbers do not match")
	}

	// 3. Check reveal is within deadline
	if reveal.RevealedAt > commitment.RevealDeadline {
		return fmt.Errorf("reveal came after deadline (revealed: %d, deadline: %d)",
			reveal.RevealedAt, commitment.RevealDeadline)
	}

	// 4. Verify commitment hash
	vrfProof := &VRFProof{
		Output: reveal.VRFOutput,
		Proof:  reveal.VRFProof,
	}
	expectedCommitment := computeCommitmentHash(vrfProof, reveal.Nonce)

	if !bytesEqual(commitment.Commitment, expectedCommitment) {
		return errors.New("reveal does not match commitment hash")
	}

	return nil
}

// ============================================================================
// QUERIES
// ============================================================================

// GetCommitment retrieves a commitment for a validator at a slot
func (crm *CommitRevealManager) GetCommitment(slot uint64, validatorAddress string) (*VRFCommitment, error) {
	crm.mu.RLock()
	defer crm.mu.RUnlock()

	slotCommitments, exists := crm.commitments[slot]
	if !exists {
		return nil, fmt.Errorf("no commitments for slot %d", slot)
	}

	commitment, exists := slotCommitments[validatorAddress]
	if !exists {
		return nil, fmt.Errorf("no commitment for validator %s at slot %d",
			validatorAddress, slot)
	}

	return commitment, nil
}

// GetReveal retrieves a reveal for a validator at a slot
func (crm *CommitRevealManager) GetReveal(slot uint64, validatorAddress string) (*VRFReveal, error) {
	crm.mu.RLock()
	defer crm.mu.RUnlock()

	slotReveals, exists := crm.reveals[slot]
	if !exists {
		return nil, fmt.Errorf("no reveals for slot %d", slot)
	}

	reveal, exists := slotReveals[validatorAddress]
	if !exists {
		return nil, fmt.Errorf("no reveal for validator %s at slot %d",
			validatorAddress, slot)
	}

	return reveal, nil
}

// HasCommitment checks if a commitment exists
func (crm *CommitRevealManager) HasCommitment(slot uint64, validatorAddress string) bool {
	crm.mu.RLock()
	defer crm.mu.RUnlock()

	if slotCommitments, exists := crm.commitments[slot]; exists {
		_, exists := slotCommitments[validatorAddress]
		return exists
	}
	return false
}

// HasReveal checks if a reveal exists
func (crm *CommitRevealManager) HasReveal(slot uint64, validatorAddress string) bool {
	crm.mu.RLock()
	defer crm.mu.RUnlock()

	if slotReveals, exists := crm.reveals[slot]; exists {
		_, exists := slotReveals[validatorAddress]
		return exists
	}
	return false
}

// ============================================================================
// CLEANUP & MAINTENANCE
// ============================================================================

// CleanupOldData removes commitments and reveals older than maxPendingSlots
func (crm *CommitRevealManager) CleanupOldData(currentSlot uint64) int {
	crm.mu.Lock()
	defer crm.mu.Unlock()

	cutoffSlot := uint64(0)
	if currentSlot > uint64(crm.maxPendingSlots) {
		cutoffSlot = currentSlot - uint64(crm.maxPendingSlots)
	}

	removed := 0

	// Clean commitments
	for slot := range crm.commitments {
		if slot < cutoffSlot {
			delete(crm.commitments, slot)
			removed++
		}
	}

	// Clean reveals
	for slot := range crm.reveals {
		if slot < cutoffSlot {
			delete(crm.reveals, slot)
			removed++
		}
	}

	return removed
}

// GetExpiredCommitments returns commitments that missed their reveal deadline
func (crm *CommitRevealManager) GetExpiredCommitments(currentTime int64) []*VRFCommitment {
	crm.mu.RLock()
	defer crm.mu.RUnlock()

	expired := make([]*VRFCommitment, 0)

	for _, slotCommitments := range crm.commitments {
		for _, commitment := range slotCommitments {
			// Check if deadline passed and no reveal exists
			if currentTime > commitment.RevealDeadline {
				if !crm.hasRevealUnsafe(commitment.Slot, commitment.ValidatorAddress) {
					expired = append(expired, commitment)
				}
			}
		}
	}

	return expired
}

// hasRevealUnsafe checks for reveal without locking (internal use)
func (crm *CommitRevealManager) hasRevealUnsafe(slot uint64, validatorAddress string) bool {
	if slotReveals, exists := crm.reveals[slot]; exists {
		_, exists := slotReveals[validatorAddress]
		return exists
	}
	return false
}

// ============================================================================
// STATISTICS
// ============================================================================

// GetStats returns statistics about commit-reveal operations
func (crm *CommitRevealManager) GetStats() map[string]interface{} {
	crm.mu.RLock()
	defer crm.mu.RUnlock()

	totalCommitments := 0
	totalReveals := 0

	for _, slotCommitments := range crm.commitments {
		totalCommitments += len(slotCommitments)
	}

	for _, slotReveals := range crm.reveals {
		totalReveals += len(slotReveals)
	}

	revealRate := 0.0
	if totalCommitments > 0 {
		revealRate = float64(totalReveals) / float64(totalCommitments)
	}

	return map[string]interface{}{
		"total_commitments":     totalCommitments,
		"total_reveals":         totalReveals,
		"reveal_rate":           revealRate,
		"pending_commitments":   totalCommitments - totalReveals,
		"reveal_deadline_slots": crm.revealDeadlineSlots,
		"max_pending_slots":     crm.maxPendingSlots,
	}
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// computeCommitmentHash computes H(VRF_output || VRF_proof || nonce)
func computeCommitmentHash(vrfProof *VRFProof, nonce []byte) []byte {
	h := sha256.New()
	h.Write(vrfProof.Output)
	h.Write(vrfProof.Proof)
	h.Write(nonce)
	return h.Sum(nil)
}

// generateSecureNonce generates a cryptographically secure 32-byte nonce
func generateSecureNonce() ([]byte, error) {
	nonce := make([]byte, 32)

	// Use cryptographically secure random generator
	_, err := rand.Read(nonce)
	if err != nil {
		return nil, fmt.Errorf("failed to generate secure random nonce: %w", err)
	}

	return nonce, nil
}

// bytesEqual performs constant-time comparison of two byte slices
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}

	result := byte(0)
	for i := 0; i < len(a); i++ {
		result |= a[i] ^ b[i]
	}

	return result == 0
}

// ============================================================================
// INTEGRATION HOOKS
// ============================================================================

// ValidatorCommitRevealFlow demonstrates the complete flow
type ValidatorCommitRevealFlow struct {
	crm *CommitRevealManager
}

// Step1_Commit creates and stores a commitment
func (vcrf *ValidatorCommitRevealFlow) Step1_Commit(
	validatorAddress string,
	slot uint64,
	epoch uint64,
	blockHash string,
	vrfSeed []byte,
	privateKey interface{}, // Use actual private key type
) (*VRFCommitment, []byte, error) {

	// 1. Generate VRF proof
	// vrfProof, err := GenerateVRFProof(privateKey, vrfSeed)
	// For now, placeholder:
	vrfProof := &VRFProof{
		Output: make([]byte, 32),
		Proof:  make([]byte, 81),
	}

	// 2. Generate nonce
	nonce, err := generateSecureNonce()
	if err != nil {
		return nil, nil, err
	}

	// 3. Create and store commitment
	commitment, err := vcrf.crm.CommitVRF(
		vrfProof,
		nonce,
		validatorAddress,
		slot,
		epoch,
		blockHash,
	)

	return commitment, nonce, err
}

// Step2_Reveal reveals the VRF proof
func (vcrf *ValidatorCommitRevealFlow) Step2_Reveal(
	validatorAddress string,
	slot uint64,
	epoch uint64,
	vrfProof *VRFProof,
	nonce []byte,
) (*VRFReveal, error) {

	return vcrf.crm.RevealVRF(vrfProof, nonce, validatorAddress, slot, epoch)
}

// ============================================================================
// NETWORK PARTITION PROTECTION
// ============================================================================

// checkNetworkPartition detects if network is partitioned
func (crm *CommitRevealManager) checkNetworkPartition() bool {
	crm.mu.RLock()
	defer crm.mu.RUnlock()

	// Check if we've seen recent activity from multiple validators
	// Simple heuristic: if we have reveals from fewer than 3 validators
	// in the last 10 slots, we might be partitioned

	recentReveals := 0
	recentValidators := make(map[string]bool)

	// Count unique validators with reveals in recent slots
	for _, slotReveals := range crm.reveals {
		for validatorAddr := range slotReveals {
			recentValidators[validatorAddr] = true
		}
	}

	recentReveals = len(recentValidators)

	// If fewer than 3 validators are revealing, possible partition
	isPartitioned := recentReveals < 3

	crm.networkPartitioned = isPartitioned
	crm.lastNetworkCheck = time.Now()

	return isPartitioned
}

// RevealVRFWithPartitionCheck wraps RevealVRF with partition detection
func (crm *CommitRevealManager) RevealVRFWithPartitionCheck(
	vrfProof *VRFProof,
	nonce []byte,
	validatorAddress string,
	slot uint64,
	epoch uint64,
) (*VRFReveal, error) {

	// Check for network partition every minute
	if time.Since(crm.lastNetworkCheck) > time.Minute {
		if crm.checkNetworkPartition() {
			log.Printf("⚠️ Network partition detected - limiting VRF reveals")
		}
	}

	// If partitioned, be more strict about timing
	if crm.networkPartitioned {
		crm.mu.RLock()
		commitment, exists := crm.commitments[slot][validatorAddress]
		crm.mu.RUnlock()

		if exists {
			// Require reveals to happen within a tighter window during partition
			currentTime := time.Now().Unix()
			maxRevealTime := commitment.CommittedAt + (crm.revealDeadlineSlots * 3) // 50% of normal window

			if currentTime > maxRevealTime {
				return nil, fmt.Errorf("reveal rejected: network partition detected and reveal window expired")
			}
		}
	}

	// Proceed with normal reveal
	return crm.RevealVRF(vrfProof, nonce, validatorAddress, slot, epoch)
}
