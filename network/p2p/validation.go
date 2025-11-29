package p2p

import (
	"fmt"
	"io"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	// Default limits (can be overridden by config)
	DefaultMaxMessageSize     = 10 * 1024 * 1024 // 10MB
	DefaultMaxBlockRangeSize  = 100              // blocks
	DefaultStreamReadTimeout  = 30 * time.Second
	DefaultStreamWriteTimeout = 30 * time.Second
	DefaultRequestRateLimit   = 60  // requests per minute
	DefaultMaxPendingRequests = 100 // per peer
)

// MessageValidator handles P2P message validation and rate limiting
type MessageValidator struct {
	maxMessageSize    int64
	maxBlockRangeSize int
	readTimeout       time.Duration
	writeTimeout      time.Duration

	// Rate limiting per peer
	peerRequests map[peer.ID]*PeerRequestTracker
}

// PeerRequestTracker tracks requests from a specific peer
type PeerRequestTracker struct {
	requestCount    int
	pendingRequests int
	lastReset       time.Time
	rateLimit       int
	maxPending      int
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

// ValidateStream sets timeouts and size limits on a stream
func (mv *MessageValidator) ValidateStream(s network.Stream) (io.Reader, error) {
	// Set read deadline
	if err := s.SetReadDeadline(time.Now().Add(mv.readTimeout)); err != nil {
		return nil, fmt.Errorf("failed to set read deadline: %v", err)
	}

	// Set write deadline
	if err := s.SetWriteDeadline(time.Now().Add(mv.writeTimeout)); err != nil {
		return nil, fmt.Errorf("failed to set write deadline: %v", err)
	}

	// Limit message size
	limitedReader := io.LimitReader(s, mv.maxMessageSize)

	return limitedReader, nil
}

// CheckRateLimit checks if peer has exceeded rate limit
func (mv *MessageValidator) CheckRateLimit(peerID peer.ID) error {
	tracker, exists := mv.peerRequests[peerID]
	if !exists {
		mv.peerRequests[peerID] = &PeerRequestTracker{
			requestCount:    1,
			pendingRequests: 1,
			lastReset:       time.Now(),
			rateLimit:       DefaultRequestRateLimit,
			maxPending:      DefaultMaxPendingRequests,
		}
		return nil
	}

	// Reset counter every minute
	if time.Since(tracker.lastReset) > time.Minute {
		tracker.requestCount = 0
		tracker.lastReset = time.Now()
	}

	// Check rate limit
	if tracker.requestCount >= tracker.rateLimit {
		return fmt.Errorf("rate limit exceeded for peer %s: %d requests/min", peerID, tracker.rateLimit)
	}

	// Check pending requests
	if tracker.pendingRequests >= tracker.maxPending {
		return fmt.Errorf("too many pending requests for peer %s: %d pending", peerID, tracker.pendingRequests)
	}

	tracker.requestCount++
	tracker.pendingRequests++

	return nil
}

// ReleaseRequest decrements pending request counter
func (mv *MessageValidator) ReleaseRequest(peerID peer.ID) {
	if tracker, exists := mv.peerRequests[peerID]; exists {
		if tracker.pendingRequests > 0 {
			tracker.pendingRequests--
		}
	}
}

// ValidateBlockRangeRequest validates block range request parameters
func (mv *MessageValidator) ValidateBlockRangeRequest(startHeight, endHeight int64) error {
	if startHeight < 0 {
		return fmt.Errorf("invalid start height: %d (must be >= 0)", startHeight)
	}

	if endHeight < startHeight {
		return fmt.Errorf("invalid range: end height %d < start height %d", endHeight, startHeight)
	}

	rangeSize := endHeight - startHeight
	if rangeSize > int64(mv.maxBlockRangeSize) {
		return fmt.Errorf("range too large: requested %d blocks, max allowed %d", rangeSize, mv.maxBlockRangeSize)
	}

	return nil
}

// GetMetrics returns current validation metrics
func (mv *MessageValidator) GetMetrics() map[string]interface{} {
	totalPeers := len(mv.peerRequests)
	totalRequests := 0
	totalPending := 0
	rateLimitedPeers := 0

	for _, tracker := range mv.peerRequests {
		totalRequests += tracker.requestCount
		totalPending += tracker.pendingRequests
		if tracker.requestCount >= tracker.rateLimit {
			rateLimitedPeers++
		}
	}

	return map[string]interface{}{
		"total_peers":        totalPeers,
		"total_requests":     totalRequests,
		"pending_requests":   totalPending,
		"rate_limited_peers": rateLimitedPeers,
		"max_message_size":   mv.maxMessageSize,
		"max_block_range":    mv.maxBlockRangeSize,
		"read_timeout_sec":   mv.readTimeout.Seconds(),
		"write_timeout_sec":  mv.writeTimeout.Seconds(),
	}
}
