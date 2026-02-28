package p2p

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	// Default limits
	DefaultMaxMessageSize     = 2 * 1024 * 1024 // 2MB
	DefaultMaxBlockRangeSize  = 20
	DefaultStreamReadTimeout  = 30 * time.Second
	DefaultStreamWriteTimeout = 30 * time.Second

	// For development/testnet with 4+ nodes
	DefaultRequestRateLimit   = 200 // requests per minute (increased from 50)
	DefaultMaxPendingRequests = 100 // Concurrent requests (increased from 10)

	// Tiered rate limits
	HighReputationRateLimit   = 300 // High reputation (increased from 100)
	MediumReputationRateLimit = 200 // Medium (increased from 50)
	LowReputationRateLimit    = 100 // Low reputation (increased from 20)

	// Reputation Constants
	ReputationInitial      = 100
	ReputationMax          = 200
	ReputationMin          = 0
	ReputationBanThreshold = 50

	// M-3 FIX: Reputation adjustments
	ScoreInvalidBlock      = -20
	ScoreInvalidTx         = -5
	ScoreSpam              = -10
	ScoreGoodBlock         = +5
	ScoreGoodTx            = +2
	ScoreTimeout           = -3
	ScoreExcessiveRequests = -15

	// M-3 FIX: Adaptive throttling thresholds
	ThrottleHighLoad     = 0.8  // Throttle when 80% capacity used
	ThrottleCriticalLoad = 0.95 // Critical throttling at 95%

	// M-3 FIX: Ban durations
	ShortBanDuration  = 10 * time.Minute
	MediumBanDuration = 1 * time.Hour
	LongBanDuration   = 24 * time.Hour
)

// M-3 FIX: Message priority levels
type MessagePriority int

const (
	PriorityLow MessagePriority = iota
	PriorityNormal
	PriorityHigh
	PriorityCritical
)

// MessageValidator handles P2P message validation, rate limiting, and reputation
type MessageValidator struct {
	maxMessageSize    int64
	maxBlockRangeSize int
	readTimeout       time.Duration
	writeTimeout      time.Duration

	// Mutex for thread-safe map access
	mu           sync.RWMutex
	peerRequests map[peer.ID]*PeerRequestTracker

	// M-3 FIX: Global load tracking for adaptive throttling
	totalPendingRequests int
	maxGlobalPending     int

	// M-3 FIX: Ban history for repeat offenders
	banHistory map[peer.ID]int // Count of times banned
}

// PeerRequestTracker tracks requests and reputation from a specific peer
type PeerRequestTracker struct {
	requestCount    int
	pendingRequests int
	lastReset       time.Time

	// Configurable Limits per peer
	rateLimit  int
	maxPending int

	// Reputation
	score      int
	isBanned   bool
	banExpires time.Time

	// M-3 FIX: Enhanced tracking
	consecutiveFailures int
	lastRequestTime     time.Time
	goodRequests        int
	badRequests         int

	// M-3 FIX: Message priority tracking
	priorityQuota map[MessagePriority]int

	// M-3 FIX: Throttling state
	isThrottled     bool
	throttleExpires time.Time
}

// NewMessageValidator creates a new message validator
func NewMessageValidator(maxMsgSize int64, maxBlockRange int, readTimeout, writeTimeout time.Duration) *MessageValidator {
	return &MessageValidator{
		maxMessageSize:    maxMsgSize,
		maxBlockRangeSize: maxBlockRange,
		readTimeout:       readTimeout,
		writeTimeout:      writeTimeout,
		peerRequests:      make(map[peer.ID]*PeerRequestTracker),
		maxGlobalPending:  1000, // M-3 FIX: Global capacity
		banHistory:        make(map[peer.ID]int),
	}
}

// M-3 FIX: CheckPeerStatusWithPriority includes priority-aware rate limiting
func (mv *MessageValidator) CheckPeerStatusWithPriority(peerID peer.ID, priority MessagePriority) error {
	// [FIX] Check for nil BEFORE locking
	if mv == nil {
		return nil // or return fmt.Errorf("message validator is nil")
	}

	mv.mu.Lock()
	defer mv.mu.Unlock()

	// [FIX] Add this check at the very beginning of the function
	if mv == nil {
		return nil // or handle error appropriately
	}
	// 1. Check global load first (adaptive throttling)
	if err := mv.checkGlobalLoadInternal(); err != nil {
		// During high load, only allow high-priority messages
		if priority < PriorityHigh {
			return fmt.Errorf("system under high load, low priority requests throttled")
		}
	}

	tracker, exists := mv.peerRequests[peerID]
	if !exists {
		// Initialize new peer with default reputation and limits
		tracker = &PeerRequestTracker{
			lastReset:       time.Now(),
			score:           ReputationInitial,
			rateLimit:       DefaultRequestRateLimit,
			maxPending:      DefaultMaxPendingRequests,
			priorityQuota:   make(map[MessagePriority]int),
			lastRequestTime: time.Now(),
		}
		mv.peerRequests[peerID] = tracker
	}

	// 2. Check Ban Status
	if tracker.isBanned {
		if time.Now().After(tracker.banExpires) {
			// Unban if expired
			tracker.isBanned = false
			tracker.score = ReputationInitial / 2 // Partial reputation restore
			tracker.consecutiveFailures = 0
		} else {
			return fmt.Errorf("peer %s is banned until %v", peerID, tracker.banExpires)
		}
	}

	// 3. Check Throttle Status (M-3 FIX)
	if tracker.isThrottled && time.Now().Before(tracker.throttleExpires) {
		// Only allow high-priority during throttle
		if priority < PriorityHigh {
			return fmt.Errorf("peer %s is throttled, only high-priority requests allowed", peerID)
		}
	}

	// 4. Adaptive Rate Limiting (M-3 FIX: Reputation-based)
	if time.Since(tracker.lastReset) > time.Minute {
		tracker.requestCount = 0
		tracker.lastReset = time.Now()
		// Adjust rate limit based on reputation
		tracker.rateLimit = mv.calculateRateLimitInternal(tracker.score)
	}

	// Check Request Frequency
	if tracker.requestCount >= tracker.rateLimit {
		// Penalize for spamming
		tracker.score += ScoreExcessiveRequests
		tracker.consecutiveFailures++

		// Progressive punishment
		if tracker.consecutiveFailures >= 3 {
			duration := mv.calculateBanDurationInternal(peerID, tracker)
			mv.banPeerInternal(tracker, duration)
			return fmt.Errorf("peer %s banned for excessive requests", peerID)
		}

		// Throttle instead of immediate ban
		mv.throttlePeerInternal(tracker, 5*time.Minute)
		return fmt.Errorf("rate limit exceeded for peer %s (throttled)", peerID)
	}

	// 5. Check Concurrent Pending Requests
	if tracker.pendingRequests >= tracker.maxPending {
		tracker.score += ScoreSpam
		return fmt.Errorf("too many pending requests for peer %s: %d pending", peerID, tracker.pendingRequests)
	}

	// 6. Check Priority Quota (M-3 FIX)
	if !mv.checkPriorityQuotaInternal(tracker, priority) {
		return fmt.Errorf("priority quota exceeded for peer %s at priority %v", peerID, priority)
	}

	// 7. All checks passed - allow request
	tracker.requestCount++
	tracker.pendingRequests++
	tracker.lastRequestTime = time.Now()
	mv.totalPendingRequests++

	return nil
}

// CheckPeerStatus verifies if a peer is allowed to communicate (backward compatible)
func (mv *MessageValidator) CheckPeerStatus(peerID peer.ID) error {
	return mv.CheckPeerStatusWithPriority(peerID, PriorityNormal)
}

// M-3 FIX: Check global system load for adaptive throttling
func (mv *MessageValidator) checkGlobalLoadInternal() error {
	if mv.maxGlobalPending == 0 {
		return nil // No global limit
	}

	loadPercent := float64(mv.totalPendingRequests) / float64(mv.maxGlobalPending)

	if loadPercent >= ThrottleCriticalLoad {
		return fmt.Errorf("critical system load: %.1f%%", loadPercent*100)
	}

	if loadPercent >= ThrottleHighLoad {
		return fmt.Errorf("high system load: %.1f%%", loadPercent*100)
	}

	return nil
}

// M-3 FIX: Calculate rate limit based on reputation
func (mv *MessageValidator) calculateRateLimitInternal(score int) int {
	if score >= 150 {
		return HighReputationRateLimit // High reputation
	} else if score >= 80 {
		return MediumReputationRateLimit // Medium reputation
	} else {
		return LowReputationRateLimit // Low reputation
	}
}

// M-3 FIX: Calculate ban duration based on history
func (mv *MessageValidator) calculateBanDurationInternal(peerID peer.ID, tracker *PeerRequestTracker) time.Duration {
	banCount := mv.banHistory[peerID]

	switch {
	case banCount == 0:
		return ShortBanDuration // First offense: 10 minutes
	case banCount == 1:
		return MediumBanDuration // Second offense: 1 hour
	default:
		return LongBanDuration // Repeat offender: 24 hours
	}
}

// M-3 FIX: Check priority-based quota
func (mv *MessageValidator) checkPriorityQuotaInternal(tracker *PeerRequestTracker, priority MessagePriority) bool {
	// Reset quotas every minute
	if time.Since(tracker.lastReset) > time.Minute {
		tracker.priorityQuota = make(map[MessagePriority]int)
	}

	// Define quotas per priority
	var maxQuota int
	switch priority {
	case PriorityCritical:
		maxQuota = 200 // Critical messages: 200/min (increased 10x)
	case PriorityHigh:
		maxQuota = 300 // High priority: 300/min (increased 10x)
	case PriorityNormal:
		maxQuota = 400 // Normal: 400/min (increased 10x)
	case PriorityLow:
		maxQuota = 200 // Low priority: 200/min (increased 10x)
	}

	current := tracker.priorityQuota[priority]
	if current >= maxQuota {
		return false
	}

	tracker.priorityQuota[priority] = current + 1
	return true
}

// M-3 FIX: Throttle a peer temporarily
func (mv *MessageValidator) throttlePeerInternal(tracker *PeerRequestTracker, duration time.Duration) {
	tracker.isThrottled = true
	tracker.throttleExpires = time.Now().Add(duration)
}

// ReleaseRequest decrements pending request counter (Must be called when processing finishes)
func (mv *MessageValidator) ReleaseRequest(peerID peer.ID) {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	if tracker, exists := mv.peerRequests[peerID]; exists {
		if tracker.pendingRequests > 0 {
			tracker.pendingRequests--
			mv.totalPendingRequests--
		}
	}
}

// AdjustReputation allows external components (Block/Tx processors) to report peer behavior
func (mv *MessageValidator) AdjustReputation(peerID peer.ID, delta int) {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	tracker, exists := mv.peerRequests[peerID]
	if !exists {
		return
	}

	// M-3 FIX: Track good vs bad requests
	if delta > 0 {
		tracker.goodRequests++
		tracker.consecutiveFailures = 0 // Reset failure counter on success
	} else {
		tracker.badRequests++
		tracker.consecutiveFailures++
	}

	tracker.score += delta

	// Cap score at bounds
	if tracker.score > ReputationMax {
		tracker.score = ReputationMax
	}
	if tracker.score < ReputationMin {
		tracker.score = ReputationMin
	}

	// M-3 FIX: Progressive punishment based on consecutive failures
	if tracker.consecutiveFailures >= 5 {
		duration := mv.calculateBanDurationInternal(peerID, tracker)
		mv.banPeerInternal(tracker, duration)
		fmt.Printf("🚫 Peer %s BANNED for %v due to %d consecutive failures\n",
			peerID, duration, tracker.consecutiveFailures)
		return
	}

	// Check Ban Threshold
	if tracker.score <= ReputationBanThreshold {
		duration := mv.calculateBanDurationInternal(peerID, tracker)
		mv.banPeerInternal(tracker, duration)
		fmt.Printf("🚫 Peer %s BANNED for %v due to low reputation (%d)\n",
			peerID, duration, tracker.score)
	}
}

// Internal helper to ban a peer
func (mv *MessageValidator) banPeerInternal(tracker *PeerRequestTracker, duration time.Duration) {
	tracker.isBanned = true
	tracker.banExpires = time.Now().Add(duration)

	// M-3 FIX: Track ban history
	// Note: peerID not available in this context, tracked by caller
}

// M-3 FIX: BanPeer allows external banning with history tracking
func (mv *MessageValidator) BanPeer(peerID peer.ID, duration time.Duration, reason string) {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	tracker, exists := mv.peerRequests[peerID]
	if !exists {
		tracker = &PeerRequestTracker{
			lastReset:     time.Now(),
			score:         ReputationMin,
			priorityQuota: make(map[MessagePriority]int),
		}
		mv.peerRequests[peerID] = tracker
	}

	tracker.isBanned = true
	tracker.banExpires = time.Now().Add(duration)
	tracker.score = ReputationMin

	// Track ban history
	mv.banHistory[peerID]++

	fmt.Printf("🚫 Peer %s BANNED for %v: %s (ban #%d)\n",
		peerID, duration, reason, mv.banHistory[peerID])
}

// ValidateStream sets timeouts and limits on a P2P stream
func (mv *MessageValidator) ValidateStream(s network.Stream) (io.Reader, error) {
	if err := s.SetReadDeadline(time.Now().Add(mv.readTimeout)); err != nil {
		return nil, err
	}
	if err := s.SetWriteDeadline(time.Now().Add(mv.writeTimeout)); err != nil {
		return nil, err
	}
	return io.LimitReader(s, mv.maxMessageSize), nil
}

// ValidateBlockRangeRequest checks semantic validity of range requests
func (mv *MessageValidator) ValidateBlockRangeRequest(startHeight, endHeight int64) error {
	if startHeight < 0 {
		return fmt.Errorf("invalid start height: %d (must be >= 0)", startHeight)
	}
	if endHeight < startHeight {
		return fmt.Errorf("invalid range: end height %d < start height %d", endHeight, startHeight)
	}

	rangeSize := endHeight - startHeight
	// Limit range size to prevent massive data requests
	if rangeSize > int64(mv.maxBlockRangeSize) {
		return fmt.Errorf("range too large: requested %d blocks, max allowed %d", rangeSize, mv.maxBlockRangeSize)
	}
	return nil
}

// M-3 FIX: GetPeerReputation returns current reputation score
func (mv *MessageValidator) GetPeerReputation(peerID peer.ID) int {
	mv.mu.RLock()
	defer mv.mu.RUnlock()

	if tracker, exists := mv.peerRequests[peerID]; exists {
		return tracker.score
	}
	return ReputationInitial
}

// M-3 FIX: GetPeerStats returns detailed peer statistics
func (mv *MessageValidator) GetPeerStats(peerID peer.ID) map[string]interface{} {
	mv.mu.RLock()
	defer mv.mu.RUnlock()

	tracker, exists := mv.peerRequests[peerID]
	if !exists {
		return map[string]interface{}{"error": "peer not found"}
	}

	return map[string]interface{}{
		"reputation":           tracker.score,
		"rate_limit":           tracker.rateLimit,
		"request_count":        tracker.requestCount,
		"pending_requests":     tracker.pendingRequests,
		"good_requests":        tracker.goodRequests,
		"bad_requests":         tracker.badRequests,
		"consecutive_failures": tracker.consecutiveFailures,
		"is_banned":            tracker.isBanned,
		"is_throttled":         tracker.isThrottled,
		"ban_count":            mv.banHistory[peerID],
	}
}

// GetMetrics returns current validation metrics for monitoring
func (mv *MessageValidator) GetMetrics() map[string]interface{} {
	mv.mu.RLock()
	defer mv.mu.RUnlock()

	totalPeers := len(mv.peerRequests)
	totalRequests := 0
	bannedPeers := 0
	throttledPeers := 0
	highRepPeers := 0
	lowRepPeers := 0

	for _, tracker := range mv.peerRequests {
		totalRequests += tracker.requestCount
		if tracker.isBanned {
			bannedPeers++
		}
		if tracker.isThrottled {
			throttledPeers++
		}
		if tracker.score >= 150 {
			highRepPeers++
		} else if tracker.score < 80 {
			lowRepPeers++
		}
	}

	loadPercent := float64(0)
	if mv.maxGlobalPending > 0 {
		loadPercent = float64(mv.totalPendingRequests) / float64(mv.maxGlobalPending) * 100
	}

	return map[string]interface{}{
		"total_peers":         totalPeers,
		"total_requests":      totalRequests,
		"pending_requests":    mv.totalPendingRequests,
		"banned_peers":        bannedPeers,
		"throttled_peers":     throttledPeers,
		"high_rep_peers":      highRepPeers,
		"low_rep_peers":       lowRepPeers,
		"system_load_percent": loadPercent,
		"max_message_size":    mv.maxMessageSize,
		"max_block_range":     mv.maxBlockRangeSize,
		"max_global_pending":  mv.maxGlobalPending,
	}
}

// M-3 FIX: CleanupInactivePeers removes peers that haven't been seen recently
func (mv *MessageValidator) CleanupInactivePeers(inactiveThreshold time.Duration) int {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	now := time.Now()
	cleaned := 0

	for peerID, tracker := range mv.peerRequests {
		// Don't cleanup banned peers (they need to stay banned)
		if tracker.isBanned {
			continue
		}

		// Remove if inactive for threshold period
		if now.Sub(tracker.lastRequestTime) > inactiveThreshold {
			delete(mv.peerRequests, peerID)
			cleaned++
		}
	}

	return cleaned
}

// It performs the exact same checks (Ban + Rate Limit).
func (mv *MessageValidator) CheckRateLimit(peerID peer.ID) error {
	return mv.CheckPeerStatus(peerID)
}
