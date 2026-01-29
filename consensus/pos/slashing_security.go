// consensus/pos/slashing_security.go
// Security components for slashing protection
// CertiK Audit Finding #3: Slashing Logic Vulnerabilities

package pos

import (
	"fmt"
	"sync"
	"time"
)

// ============================================================================
// Evidence Rate Limiter - Prevents Spam Attacks
// ============================================================================

// EvidenceRateLimiter prevents spam attacks where malicious actors flood
// the system with fake evidence
type EvidenceRateLimiter struct {
	mu sync.RWMutex

	// Track submissions per reporter
	reporters map[string]*ReporterStats

	// Configuration
	maxPerHour     int
	maxPerDay      int
	suspicionLevel int
	banDuration    time.Duration
}

type ReporterStats struct {
	HourlyCount   int
	DailyCount    int
	RejectedCount int
	HourReset     time.Time
	DayReset      time.Time
	BannedUntil   time.Time
}

func NewEvidenceRateLimiter() *EvidenceRateLimiter {
	return &EvidenceRateLimiter{
		reporters:      make(map[string]*ReporterStats),
		maxPerHour:     10,
		maxPerDay:      50,
		suspicionLevel: 5,
		banDuration:    24 * time.Hour,
	}
}

// CheckReporter validates if reporter can submit evidence
func (erl *EvidenceRateLimiter) CheckReporter(reporterAddr string) error {
	erl.mu.Lock()
	defer erl.mu.Unlock()

	now := time.Now()
	stats, exists := erl.reporters[reporterAddr]

	if !exists {
		stats = &ReporterStats{
			HourReset: now,
			DayReset:  now,
		}
		erl.reporters[reporterAddr] = stats
	}

	// Check if banned
	if now.Before(stats.BannedUntil) {
		return fmt.Errorf("reporter banned until %v (reason: suspicious activity)",
			stats.BannedUntil.Format(time.RFC3339))
	}

	// Reset counters if time windows expired
	if now.Sub(stats.HourReset) >= time.Hour {
		stats.HourlyCount = 0
		stats.HourReset = now
	}
	if now.Sub(stats.DayReset) >= 24*time.Hour {
		stats.DailyCount = 0
		stats.DayReset = now
	}

	// Check limits
	if stats.HourlyCount >= erl.maxPerHour {
		return fmt.Errorf("hourly limit exceeded (%d/%d)", stats.HourlyCount, erl.maxPerHour)
	}
	if stats.DailyCount >= erl.maxPerDay {
		return fmt.Errorf("daily limit exceeded (%d/%d)", stats.DailyCount, erl.maxPerDay)
	}

	// Increment counters
	stats.HourlyCount++
	stats.DailyCount++

	return nil
}

// RecordRejection tracks rejected evidence and bans malicious reporters
func (erl *EvidenceRateLimiter) RecordRejection(reporterAddr string) {
	erl.mu.Lock()
	defer erl.mu.Unlock()

	if stats, exists := erl.reporters[reporterAddr]; exists {
		stats.RejectedCount++

		// Ban if too many rejections
		if stats.RejectedCount >= erl.suspicionLevel {
			stats.BannedUntil = time.Now().Add(erl.banDuration)
		}
	}
}

// RecordAcceptance resets rejection counter on valid evidence
func (erl *EvidenceRateLimiter) RecordAcceptance(reporterAddr string) {
	erl.mu.Lock()
	defer erl.mu.Unlock()

	if stats, exists := erl.reporters[reporterAddr]; exists {
		// Decay rejection count on valid submission
		if stats.RejectedCount > 0 {
			stats.RejectedCount--
		}
	}
}

// ============================================================================
// Slashing Confirmation - Requires Multiple Validators
// ============================================================================

// SlashingConfirmation requires multiple independent validators to confirm
// evidence before slashing is applied
type SlashingConfirmation struct {
	mu sync.RWMutex

	// Pending evidence awaiting confirmations
	pending map[string]*PendingEvidence

	requiredConfirmations int
	windowDuration        time.Duration
}

type PendingEvidence struct {
	Evidence    *SlashingEvidence
	Confirmers  map[string]time.Time
	SubmittedAt time.Time
	ExpiresAt   time.Time
}

func NewSlashingConfirmation(required int, window time.Duration) *SlashingConfirmation {
	return &SlashingConfirmation{
		pending:               make(map[string]*PendingEvidence),
		requiredConfirmations: required,
		windowDuration:        window,
	}
}

// AddConfirmation adds a confirmation from a validator
// Returns true if enough confirmations collected
func (sc *SlashingConfirmation) AddConfirmation(
	evidence *SlashingEvidence,
	confirmerAddr string,
) (ready bool, err error) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	evidenceID := evidence.ID
	now := time.Now()

	// Get or create pending entry
	pending, exists := sc.pending[evidenceID]
	if !exists {
		pending = &PendingEvidence{
			Evidence:    evidence,
			Confirmers:  make(map[string]time.Time),
			SubmittedAt: now,
			ExpiresAt:   now.Add(sc.windowDuration),
		}
		sc.pending[evidenceID] = pending
	}

	// Check expiration
	if now.After(pending.ExpiresAt) {
		delete(sc.pending, evidenceID)
		return false, fmt.Errorf("confirmation window expired")
	}

	// Prevent validator from confirming their own slashing
	if confirmerAddr == evidence.ValidatorAddress {
		return false, fmt.Errorf("validator cannot confirm their own slashing")
	}

	// Add confirmation
	pending.Confirmers[confirmerAddr] = now

	// Check if ready
	if len(pending.Confirmers) >= sc.requiredConfirmations {
		delete(sc.pending, evidenceID)
		return true, nil
	}

	return false, nil
}

// CleanupExpired removes expired pending evidence
func (sc *SlashingConfirmation) CleanupExpired() int {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	now := time.Now()
	count := 0

	for id, pending := range sc.pending {
		if now.After(pending.ExpiresAt) {
			delete(sc.pending, id)
			count++
		}
	}

	return count
}

// GetPendingCount returns number of pending confirmations
func (sc *SlashingConfirmation) GetPendingCount() int {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return len(sc.pending)
}

// ============================================================================
// Slashing Cooldown - Prevents Repeated Slashing
// ============================================================================

// SlashingCooldown prevents the same validator from being slashed multiple
// times in rapid succession for the same type of offense
type SlashingCooldown struct {
	mu sync.RWMutex

	// Track last slashing per validator per type
	lastSlashed map[string]map[SlashingEvidenceType]time.Time

	// Cooldown durations per evidence type
	cooldowns map[SlashingEvidenceType]time.Duration
}

func NewSlashingCooldown() *SlashingCooldown {
	return &SlashingCooldown{
		lastSlashed: make(map[string]map[SlashingEvidenceType]time.Time),
		cooldowns: map[SlashingEvidenceType]time.Duration{
			EvidenceDoubleVoting:     2 * time.Hour,
			EvidenceSurroundVoting:   2 * time.Hour,
			EvidenceInvalidProposal:  1 * time.Hour,
			EvidenceDowntime:         6 * time.Hour,
			EvidenceInvalidSignature: 1 * time.Hour,
		},
	}
}

// CheckCooldown verifies slashing is allowed (not on cooldown)
func (sc *SlashingCooldown) CheckCooldown(
	validatorAddr string,
	evidenceType SlashingEvidenceType,
) error {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	validatorHistory, exists := sc.lastSlashed[validatorAddr]
	if !exists {
		return nil
	}

	lastTime, exists := validatorHistory[evidenceType]
	if !exists {
		return nil
	}

	cooldown := sc.cooldowns[evidenceType]
	elapsed := time.Since(lastTime)

	if elapsed < cooldown {
		remaining := cooldown - elapsed
		return fmt.Errorf("cooldown active: %v remaining (type: %v)",
			remaining.Round(time.Minute), evidenceType)
	}

	return nil
}

// RecordSlashing records when a slashing occurred
func (sc *SlashingCooldown) RecordSlashing(
	validatorAddr string,
	evidenceType SlashingEvidenceType,
) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if _, exists := sc.lastSlashed[validatorAddr]; !exists {
		sc.lastSlashed[validatorAddr] = make(map[SlashingEvidenceType]time.Time)
	}

	sc.lastSlashed[validatorAddr][evidenceType] = time.Now()
}

// ============================================================================
// Slashing Metrics - Monitoring & Observability
// ============================================================================

// SlashingMetrics tracks security metrics
type SlashingMetrics struct {
	mu sync.RWMutex

	TotalSubmissions     int64
	ValidSubmissions     int64
	RejectedSpam         int64
	RejectedInvalid      int64
	CooldownBlocked      int64
	PendingConfirmations int64
	SlashingsExecuted    int64
}

func NewSlashingMetrics() *SlashingMetrics {
	return &SlashingMetrics{}
}

func (m *SlashingMetrics) RecordSubmission() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TotalSubmissions++
}

func (m *SlashingMetrics) RecordValid() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ValidSubmissions++
}

func (m *SlashingMetrics) RecordSpam() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RejectedSpam++
}

func (m *SlashingMetrics) RecordInvalid() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RejectedInvalid++
}

func (m *SlashingMetrics) RecordCooldown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CooldownBlocked++
}

func (m *SlashingMetrics) RecordPending() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PendingConfirmations++
}

func (m *SlashingMetrics) RecordExecution() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SlashingsExecuted++
}

func (m *SlashingMetrics) GetStats() map[string]int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return map[string]int64{
		"total_submissions":     m.TotalSubmissions,
		"valid_submissions":     m.ValidSubmissions,
		"rejected_spam":         m.RejectedSpam,
		"rejected_invalid":      m.RejectedInvalid,
		"cooldown_blocked":      m.CooldownBlocked,
		"pending_confirmations": m.PendingConfirmations,
		"slashings_executed":    m.SlashingsExecuted,
	}
}

// GetSuccessRate returns the percentage of valid submissions
func (m *SlashingMetrics) GetSuccessRate() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.TotalSubmissions == 0 {
		return 0
	}

	return float64(m.ValidSubmissions) / float64(m.TotalSubmissions) * 100
}

// GetSpamRate returns the percentage of spam submissions
func (m *SlashingMetrics) GetSpamRate() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.TotalSubmissions == 0 {
		return 0
	}

	return float64(m.RejectedSpam) / float64(m.TotalSubmissions) * 100
}
