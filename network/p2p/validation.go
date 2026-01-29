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

	// Rate Limits
	DefaultRequestRateLimit   = 50 // requests per minute
	DefaultMaxPendingRequests = 10 // Concurrent requests

	// Reputation Constants
	ReputationInitial      = 100
	ReputationMax          = 200
	ReputationBanThreshold = 50
	ScoreInvalidBlock      = -20
	ScoreInvalidTx         = -5
	ScoreSpam              = -10
	ScoreGoodBlock         = +5
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
}

// NewMessageValidator creates a new message validator
func NewMessageValidator(maxMsgSize int64, maxBlockRange int, readTimeout, writeTimeout time.Duration) *MessageValidator {
	return &MessageValidator{
		maxMessageSize:    maxMsgSize,
		maxBlockRangeSize: maxBlockRange,
		readTimeout:       readTimeout,
		writeTimeout:      writeTimeout,
		peerRequests:      make(map[peer.ID]*PeerRequestTracker),
	}
}

// CheckPeerStatus verifies if a peer is allowed to communicate (Rate Limit + Reputation Check)
func (mv *MessageValidator) CheckPeerStatus(peerID peer.ID) error {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	tracker, exists := mv.peerRequests[peerID]
	if !exists {
		// Initialize new peer with default reputation and limits
		tracker = &PeerRequestTracker{
			lastReset:  time.Now(),
			score:      ReputationInitial,
			rateLimit:  DefaultRequestRateLimit,
			maxPending: DefaultMaxPendingRequests,
		}
		mv.peerRequests[peerID] = tracker
	}

	// 1. Check Ban Status
	if tracker.isBanned {
		if time.Now().After(tracker.banExpires) {
			// Unban if expired
			tracker.isBanned = false
			tracker.score = ReputationInitial // Reset score
		} else {
			return fmt.Errorf("peer %s is banned until %v", peerID, tracker.banExpires)
		}
	}

	// 2. Rate Limiting Logic (Reset counter every minute)
	if time.Since(tracker.lastReset) > time.Minute {
		tracker.requestCount = 0
		tracker.lastReset = time.Now()
	}

	// Check Request Frequency
	if tracker.requestCount >= tracker.rateLimit {
		// Penalize for spamming
		tracker.score += ScoreSpam

		// If score drops too low from spamming, ban them
		if tracker.score <= ReputationBanThreshold {
			mv.banPeerInternal(tracker, 10*time.Minute)
			return fmt.Errorf("peer %s banned for excessive rate limit spam", peerID)
		}
		return fmt.Errorf("rate limit exceeded for peer %s", peerID)
	}

	// Check Concurrent Pending Requests
	if tracker.pendingRequests >= tracker.maxPending {
		return fmt.Errorf("too many pending requests for peer %s: %d pending", peerID, tracker.pendingRequests)
	}

	tracker.requestCount++
	tracker.pendingRequests++
	return nil
}

// ReleaseRequest decrements pending request counter (Must be called when processing finishes)
func (mv *MessageValidator) ReleaseRequest(peerID peer.ID) {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	if tracker, exists := mv.peerRequests[peerID]; exists {
		if tracker.pendingRequests > 0 {
			tracker.pendingRequests--
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

	tracker.score += delta

	// Cap score at Max
	if tracker.score > ReputationMax {
		tracker.score = ReputationMax
	}

	// Check Ban Threshold
	if tracker.score <= ReputationBanThreshold {
		mv.banPeerInternal(tracker, 1*time.Hour) // 1 Hour Ban for bad data
		fmt.Printf("🚫 Peer %s BANNED due to low reputation (%d)\n", peerID, tracker.score)
	}
}

// Internal helper to ban a peer
func (mv *MessageValidator) banPeerInternal(tracker *PeerRequestTracker, duration time.Duration) {
	tracker.isBanned = true
	tracker.banExpires = time.Now().Add(duration)
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

// GetMetrics returns current validation metrics for monitoring
func (mv *MessageValidator) GetMetrics() map[string]interface{} {
	mv.mu.RLock()
	defer mv.mu.RUnlock()

	totalPeers := len(mv.peerRequests)
	totalRequests := 0
	bannedPeers := 0
	totalPending := 0

	for _, tracker := range mv.peerRequests {
		totalRequests += tracker.requestCount
		totalPending += tracker.pendingRequests
		if tracker.isBanned {
			bannedPeers++
		}
	}

	return map[string]interface{}{
		"total_peers":      totalPeers,
		"total_requests":   totalRequests,
		"pending_requests": totalPending,
		"banned_peers":     bannedPeers,
		"max_message_size": mv.maxMessageSize,
	}
}

// It performs the exact same checks (Ban + Rate Limit).
func (mv *MessageValidator) CheckRateLimit(peerID peer.ID) error {
	return mv.CheckPeerStatus(peerID)
}
