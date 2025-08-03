package p2p

import (
	"context"
	"fmt"
	"sync"
	"time"

	go_log "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"
	"golang.org/x/time/rate"

	stdlog "log"

	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// Protocol IDs and PubSub topic names for Thrylos
const (
	ProtocolBlockSync   protocol.ID = "/thrylos/blocksync/1.0.0"
	ProtocolTransaction protocol.ID = "/thrylos/transaction/1.0.0"
	ProtocolAttestation protocol.ID = "/thrylos/attestation/1.0.0"
	ProtocolVote        protocol.ID = "/thrylos/vote/1.0.0"

	// ADD THESE MISSING ONES:
	ProtocolBlockRange    protocol.ID = "/thrylos/blockrange/1.0.0"
	ProtocolHeightRequest protocol.ID = "/thrylos/height/1.0.0"
	ProtocolStateSync     protocol.ID = "/thrylos/statesync/1.0.0"

	TopicBlocks       = "thrylos-blocks"
	TopicTransactions = "thrylos-transactions"
	TopicAttestations = "thrylos-attestations"
	TopicVotes        = "thrylos-votes"
)

// Message types for communication with blockchain
type MessageType int

const (
	ProcessBlock MessageType = iota
	ProcessTransaction
	ProcessAttestation
	ProcessVote
	GetBlockchainInfo
	GetBlocksFromHeight
	GetStateSnapshot
)

type Message struct {
	Type       MessageType
	Data       interface{}
	ResponseCh chan Response
}

type Response struct {
	Data  interface{}
	Error error
}

// NetworkMetrics tracks P2P network performance
type NetworkMetrics struct {
	MessagesReceived   int64
	MessagesSent       int64
	BlocksSynced       int64
	ConnectionAttempts int64
	FailedConnections  int64
	PeerCount          int64
	LastSyncTime       time.Time
	mu                 sync.RWMutex
}

// Add these structs to p2p/manager.go:
type BlockRangeRequest struct {
	StartHeight int64 `json:"start_height"`
	EndHeight   int64 `json:"end_height"`
}

type BlockRangeResponse struct {
	Blocks []*core.Block `json:"blocks"`
	Error  string        `json:"error,omitempty"`
}

type HeightRequest struct{}

type HeightResponse struct {
	Height int64  `json:"height"`
	Error  string `json:"error,omitempty"`
}

type StateSnapshotRequest struct {
	Height int64 `json:"height"`
}

type StateSnapshot struct {
	Height         int64                       `json:"height"`
	StateRoot      string                      `json:"state_root"`
	Timestamp      int64                       `json:"timestamp"`
	Accounts       map[string]*core.Account    `json:"accounts"`
	Validators     map[string]*core.Validator  `json:"validators"`
	Stakes         map[string]map[string]int64 `json:"stakes"`
	CrossShardData map[string]interface{}      `json:"cross_shard_data"`
	Metadata       map[string]string           `json:"metadata"`
	Checksum       string                      `json:"checksum"`
	CompressedData []byte                      `json:"compressed_data,omitempty"`
	Size           int64                       `json:"size"`
}

type StateSnapshotResponse struct {
	Snapshot *StateSnapshot `json:"snapshot"`
	Error    string         `json:"error,omitempty"`
}

func (nm *NetworkMetrics) IncrementMessagesReceived() {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.MessagesReceived++
}

func (nm *NetworkMetrics) IncrementMessagesSent() {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.MessagesSent++
}

func (nm *NetworkMetrics) IncrementConnectionAttempts() {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.ConnectionAttempts++
}

func (nm *NetworkMetrics) IncrementFailedConnections() {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.FailedConnections++
}

func (nm *NetworkMetrics) UpdatePeerCount(count int64) {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.PeerCount = count
}

func (nm *NetworkMetrics) GetSnapshot() map[string]interface{} {
	nm.mu.RLock()
	defer nm.mu.RUnlock()
	return map[string]interface{}{
		"messages_received":   nm.MessagesReceived,
		"messages_sent":       nm.MessagesSent,
		"blocks_synced":       nm.BlocksSynced,
		"connection_attempts": nm.ConnectionAttempts,
		"failed_connections":  nm.FailedConnections,
		"peer_count":          nm.PeerCount,
		"last_sync_time":      nm.LastSyncTime,
	}
}

// Manager manages the libp2p host and related services for Thrylos
type Manager struct {
	Host   host.Host
	Ctx    context.Context
	Cancel context.CancelFunc
	PubSub *pubsub.PubSub
	DHT    *dht.IpfsDHT

	// Communication channels
	BlockchainProcessCh chan Message
	MessageBus          chan Message

	// Configuration
	listenPort     int
	bootstrapPeers []multiaddr.Multiaddr

	// Event handlers
	OnBlockReceived       func(*core.Block)
	OnTransactionReceived func(*core.Transaction)
	OnAttestationReceived func(interface{}) // Attestation type from consensus
	OnVoteReceived        func(interface{}) // Vote type from consensus

	// Topic management
	joinedTopics map[string]*pubsub.Topic
	topicsMu     sync.RWMutex

	// Rate limiting
	rateLimiter *rate.Limiter

	// Connection management
	connectionStates map[peer.ID]*ConnectionState
	connectionsMu    sync.RWMutex

	// Metrics
	metrics *NetworkMetrics

	// Health monitoring
	healthTicker *time.Ticker

	// Synchronization
	mu sync.RWMutex
}

// ConnectionState tracks the state of peer connections
type ConnectionState struct {
	LastConnected time.Time
	Attempts      int
	IsHealthy     bool
	LastError     error
}

// Config represents P2P configuration
type Config struct {
	ListenPort     int
	BootstrapPeers []string
}

// NewManager initializes a new libp2p manager for Thrylos
func NewManager(config *Config) (*Manager, error) {
	go_log.SetLogLevel("libp2p", "info")
	ctx, cancel := context.WithCancel(context.Background())

	// Parse bootstrap peers
	var bootstrapPeers []multiaddr.Multiaddr
	for _, addr := range config.BootstrapPeers {
		maddr, err := multiaddr.NewMultiaddr(addr)
		if err != nil {
			stdlog.Printf("Invalid bootstrap peer address %s: %v", addr, err)
			continue
		}
		bootstrapPeers = append(bootstrapPeers, maddr)
	}

	opts := []libp2p.Option{
		libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", config.ListenPort)),
		libp2p.NATPortMap(),
		libp2p.EnableRelay(),
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create libp2p host: %w", err)
	}

	stdlog.Printf("Libp2p host created with Peer ID: %s, listening on: %s",
		h.ID().String(), h.Addrs())

	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		h.Close()
		cancel()
		return nil, fmt.Errorf("failed to create pubsub: %w", err)
	}

	kademliaDHT, err := dht.New(ctx, h, dht.Mode(dht.ModeServer))
	if err != nil {
		h.Close()
		cancel()
		return nil, fmt.Errorf("failed to create DHT: %w", err)
	}

	if err = kademliaDHT.Bootstrap(ctx); err != nil {
		h.Close()
		cancel()
		return nil, fmt.Errorf("failed to bootstrap DHT: %w", err)
	}

	manager := &Manager{
		Host:                h,
		Ctx:                 ctx,
		Cancel:              cancel,
		PubSub:              ps,
		DHT:                 kademliaDHT,
		listenPort:          config.ListenPort,
		bootstrapPeers:      bootstrapPeers,
		BlockchainProcessCh: make(chan Message, 1000),
		MessageBus:          make(chan Message, 1000),
		joinedTopics:        make(map[string]*pubsub.Topic),
		rateLimiter:         rate.NewLimiter(rate.Limit(100), 200), // 100 msgs/sec with burst of 200
		connectionStates:    make(map[peer.ID]*ConnectionState),
		metrics:             &NetworkMetrics{},
	}

	return manager, nil
}

// Start starts all P2P services
func (m *Manager) Start() error {
	// Connect to bootstrap peers with improved logic
	m.connectToBootstrapPeersWithRetry()

	// Start discovery services
	m.startMDNSDiscovery()
	m.startDHTDiscovery()

	// Set up protocol handlers
	m.Host.SetStreamHandler(ProtocolBlockSync, m.handleBlockSyncRequest)
	m.Host.SetStreamHandler(ProtocolTransaction, m.handleTransactionRequest)
	m.Host.SetStreamHandler(ProtocolAttestation, m.handleAttestationRequest)
	m.Host.SetStreamHandler(ProtocolVote, m.handleVoteRequest)

	// Add these new handlers:
	m.setupSyncProtocolHandlers()
	// Subscribe to PubSub topics
	m.subscribeToPubSubTopics()

	// Start health monitoring
	m.startConnectionHealthMonitor()

	stdlog.Println("Thrylos P2P services started successfully")
	return nil
}

func (m *Manager) setupSyncProtocolHandlers() {
	m.Host.SetStreamHandler(ProtocolBlockRange, m.handleBlockRangeRequest)
	m.Host.SetStreamHandler(ProtocolHeightRequest, m.handleHeightRequest)
	m.Host.SetStreamHandler(ProtocolStateSync, m.handleStateSnapshotRequest)
}

// Stop gracefully shuts down the P2P manager
func (m *Manager) Stop() error {
	stdlog.Println("Shutting down Thrylos P2P services...")

	// Stop health monitoring
	if m.healthTicker != nil {
		m.healthTicker.Stop()
	}

	// Cancel context to stop all goroutines
	m.Cancel()

	// Close topics
	m.topicsMu.Lock()
	for _, topic := range m.joinedTopics {
		if err := topic.Close(); err != nil {
			stdlog.Printf("Error closing topic: %v", err)
		}
	}
	m.topicsMu.Unlock()

	// Close DHT
	if m.DHT != nil {
		if err := m.DHT.Close(); err != nil {
			stdlog.Printf("Error closing DHT: %v", err)
		}
	}

	// Close host
	if err := m.Host.Close(); err != nil {
		return fmt.Errorf("error closing libp2p host: %w", err)
	}

	// Close channels
	close(m.BlockchainProcessCh)
	close(m.MessageBus)

	stdlog.Println("Thrylos P2P services shut down successfully")
	return nil
}

// connectToBootstrapPeersWithRetry connects to bootstrap peers with retry logic
func (m *Manager) connectToBootstrapPeersWithRetry() {
	var wg sync.WaitGroup

	for _, addr := range m.bootstrapPeers {
		pi, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			stdlog.Printf("Invalid bootstrap peer address %s: %v", addr, err)
			continue
		}
		if pi.ID == m.Host.ID() {
			continue // Don't connect to self
		}

		wg.Add(1)
		go func(pi peer.AddrInfo) {
			defer wg.Done()
			m.connectWithRetry(pi, 3) // Retry up to 3 times
		}(*pi)
	}

	// Wait for all connection attempts to complete (with timeout)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		stdlog.Println("Bootstrap peer connection attempts completed")
	case <-time.After(30 * time.Second):
		stdlog.Println("Bootstrap peer connection attempts timed out")
	}
}

// connectWithRetry attempts to connect to a peer with retry logic
func (m *Manager) connectWithRetry(pi peer.AddrInfo, maxRetries int) {
	for attempt := 1; attempt <= maxRetries; attempt++ {
		m.metrics.IncrementConnectionAttempts()

		connectCtx, connectCancel := context.WithTimeout(m.Ctx, 10*time.Second)
		err := m.Host.Connect(connectCtx, pi)
		connectCancel()

		if err == nil {
			stdlog.Printf("Connected to peer: %s (attempt %d)", pi.ID.String(), attempt)
			m.updateConnectionState(pi.ID, true, nil)
			return
		}

		m.metrics.IncrementFailedConnections()
		m.updateConnectionState(pi.ID, false, err)
		stdlog.Printf("Failed to connect to peer %s (attempt %d/%d): %v",
			pi.ID.String(), attempt, maxRetries, err)

		if attempt < maxRetries {
			// Exponential backoff
			backoff := time.Duration(attempt*attempt) * time.Second
			select {
			case <-time.After(backoff):
			case <-m.Ctx.Done():
				return
			}
		}
	}

	stdlog.Printf("Failed to connect to peer %s after %d attempts", pi.ID.String(), maxRetries)
}

// updateConnectionState updates the connection state for a peer
func (m *Manager) updateConnectionState(peerID peer.ID, isHealthy bool, err error) {
	m.connectionsMu.Lock()
	defer m.connectionsMu.Unlock()

	if m.connectionStates[peerID] == nil {
		m.connectionStates[peerID] = &ConnectionState{}
	}

	state := m.connectionStates[peerID]
	if isHealthy {
		state.LastConnected = time.Now()
		state.Attempts = 0
	} else {
		state.Attempts++
	}
	state.IsHealthy = isHealthy
	state.LastError = err
}

// getOrJoinTopic returns an existing topic or joins a new one
func (m *Manager) getOrJoinTopic(topicName string) (*pubsub.Topic, error) {
	// Check if already joined (read lock)
	m.topicsMu.RLock()
	if topic, exists := m.joinedTopics[topicName]; exists {
		m.topicsMu.RUnlock()
		return topic, nil
	}
	m.topicsMu.RUnlock()

	// Join topic (write lock)
	m.topicsMu.Lock()
	defer m.topicsMu.Unlock()

	// Double-check in case another goroutine joined while we waited for lock
	if topic, exists := m.joinedTopics[topicName]; exists {
		return topic, nil
	}

	// Join the topic
	topic, err := m.PubSub.Join(topicName)
	if err != nil {
		return nil, fmt.Errorf("failed to join topic %s: %w", topicName, err)
	}

	// Cache the topic
	m.joinedTopics[topicName] = topic
	stdlog.Printf("Successfully joined and cached PubSub topic: %s", topicName)
	return topic, nil
}

// rateLimitedBroadcast broadcasts a message with rate limiting
func (m *Manager) rateLimitedBroadcast(topicName string, data []byte) error {
	// Check rate limit
	if !m.rateLimiter.Allow() {
		return fmt.Errorf("rate limit exceeded for topic %s", topicName)
	}

	// Get cached topic or join if needed
	topic, err := m.getOrJoinTopic(topicName)
	if err != nil {
		return fmt.Errorf("failed to get topic %s: %w", topicName, err)
	}

	// Publish to topic
	if err := topic.Publish(m.Ctx, data); err != nil {
		return fmt.Errorf("failed to publish to topic %s: %w", topicName, err)
	}

	// Update metrics
	m.metrics.IncrementMessagesSent()
	return nil
}

// startConnectionHealthMonitor starts monitoring connection health
func (m *Manager) startConnectionHealthMonitor() {
	m.healthTicker = time.NewTicker(30 * time.Second)

	go func() {
		defer m.healthTicker.Stop()
		for {
			select {
			case <-m.healthTicker.C:
				m.checkConnectionHealth()
			case <-m.Ctx.Done():
				return
			}
		}
	}()
}

// checkConnectionHealth checks the health of peer connections
func (m *Manager) checkConnectionHealth() {
	peers := m.Host.Network().Peers()
	healthyPeers := 0

	for _, peerID := range peers {
		if m.isPeerHealthy(peerID) {
			healthyPeers++
		}
	}

	m.metrics.UpdatePeerCount(int64(len(peers)))

	// If we have fewer than 3 healthy peers, try to reconnect to bootstrap peers
	if healthyPeers < 3 && len(m.bootstrapPeers) > 0 {
		stdlog.Printf("Only %d healthy peers, attempting to reconnect to bootstrap peers", healthyPeers)
		go m.tryReconnectToBootstrapPeers()
	}
}

// isPeerHealthy checks if a peer connection is healthy
func (m *Manager) isPeerHealthy(peerID peer.ID) bool {
	// Check if peer is currently connected
	connectedness := m.Host.Network().Connectedness(peerID)
	if connectedness != 1 { // 1 = Connected
		return false
	}

	// Check connection state
	m.connectionsMu.RLock()
	defer m.connectionsMu.RUnlock()

	if state, exists := m.connectionStates[peerID]; exists {
		return state.IsHealthy && time.Since(state.LastConnected) < 5*time.Minute
	}

	return true // Assume healthy if no state recorded
}

// tryReconnectToBootstrapPeers attempts to reconnect to bootstrap peers
func (m *Manager) tryReconnectToBootstrapPeers() {
	for _, addr := range m.bootstrapPeers {
		pi, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil || pi.ID == m.Host.ID() {
			continue
		}

		// Only reconnect if not currently connected
		if m.Host.Network().Connectedness(pi.ID) != 1 {
			go m.connectWithRetry(*pi, 2) // Fewer retries for reconnection
		}
	}
}

// isValidBlockStructure performs basic validation on block structure
func (m *Manager) isValidBlockStructure(block *core.Block) bool {
	if block == nil {
		return false
	}

	// Basic validation checks
	if block.Hash == "" {
		return false
	}

	if block.Header == nil {
		return false
	}

	if block.Header.Index < 0 {
		return false
	}

	// Additional validation can be added here
	return true
}

// RequestBlockRange requests a range of blocks from a specific peer
func (m *Manager) RequestBlockRange(peerID string, startHeight, endHeight int64) ([]*core.Block, error) {
	// Convert string to peer.ID
	pid, err := peer.Decode(peerID)
	if err != nil {
		return nil, fmt.Errorf("invalid peer ID: %v", err)
	}

	// Check if peer is connected
	if m.Host.Network().Connectedness(pid) != network.Connected {
		return nil, fmt.Errorf("peer %s not connected", peerID)
	}

	// Open stream to peer
	stream, err := m.Host.NewStream(m.Ctx, pid, ProtocolBlockRange)
	if err != nil {
		return nil, fmt.Errorf("failed to open stream: %v", err)
	}
	defer stream.Close()

	// Create and send request
	request := &BlockRangeRequest{
		StartHeight: startHeight,
		EndHeight:   endHeight,
	}

	writer := NewJSONStreamWriter(stream)
	if err := writer.WriteJSON(request); err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}

	// Read response
	reader := NewJSONStreamReader(stream)
	response := &BlockRangeResponse{}
	if err := reader.ReadJSON(response); err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	if response.Error != "" {
		return nil, fmt.Errorf("peer error: %s", response.Error)
	}

	stdlog.Printf("Received %d blocks from peer %s", len(response.Blocks), peerID)
	return response.Blocks, nil
}

// RequestPeerHeight requests blockchain height from a peer
func (m *Manager) RequestPeerHeight(peerID string) (int64, error) {
	pid, err := peer.Decode(peerID)
	if err != nil {
		return 0, fmt.Errorf("invalid peer ID: %v", err)
	}

	if m.Host.Network().Connectedness(pid) != network.Connected {
		return 0, fmt.Errorf("peer %s not connected", peerID)
	}

	stream, err := m.Host.NewStream(m.Ctx, pid, ProtocolHeightRequest)
	if err != nil {
		return 0, fmt.Errorf("failed to open stream: %v", err)
	}
	defer stream.Close()

	// Send height request
	writer := NewJSONStreamWriter(stream)
	request := &HeightRequest{}
	if err := writer.WriteJSON(request); err != nil {
		return 0, fmt.Errorf("failed to send request: %v", err)
	}

	// Read response
	reader := NewJSONStreamReader(stream)
	response := &HeightResponse{}
	if err := reader.ReadJSON(response); err != nil {
		return 0, fmt.Errorf("failed to read response: %v", err)
	}

	if response.Error != "" {
		return 0, fmt.Errorf("peer error: %s", response.Error)
	}

	return response.Height, nil
}

// RequestStateSnapshot requests a state snapshot from a peer
func (m *Manager) RequestStateSnapshot(peerID string, height int64) (*StateSnapshot, error) {
	pid, err := peer.Decode(peerID)
	if err != nil {
		return nil, fmt.Errorf("invalid peer ID: %v", err)
	}

	if m.Host.Network().Connectedness(pid) != network.Connected {
		return nil, fmt.Errorf("peer %s not connected", peerID)
	}

	stream, err := m.Host.NewStream(m.Ctx, pid, ProtocolStateSync)
	if err != nil {
		return nil, fmt.Errorf("failed to open stream: %v", err)
	}
	defer stream.Close()

	// Send snapshot request
	writer := NewJSONStreamWriter(stream)
	request := &StateSnapshotRequest{
		Height: height,
	}
	if err := writer.WriteJSON(request); err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}

	// Read response
	reader := NewJSONStreamReader(stream)
	response := &StateSnapshotResponse{}
	if err := reader.ReadJSON(response); err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	if response.Error != "" {
		return nil, fmt.Errorf("peer error: %s", response.Error)
	}

	stdlog.Printf("Received state snapshot at height %d from peer %s",
		response.Snapshot.Height, peerID)
	return response.Snapshot, nil
}

// GetConnectedPeerIDs - returns peer IDs as strings
func (m *Manager) GetConnectedPeerIDs() []string {
	peers := m.Host.Network().Peers()
	peerIDs := make([]string, len(peers))
	for i, peer := range peers {
		peerIDs[i] = peer.String()
	}
	return peerIDs
}

// GetConnectedPeers returns list of connected peers
func (m *Manager) GetConnectedPeers() []peer.ID {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Host.Network().Peers()
}

// GetPeerCount returns the number of connected peers
func (m *Manager) GetPeerCount() int {
	return len(m.GetConnectedPeers())
}

// GetHostID returns the host's peer ID
func (m *Manager) GetHostID() peer.ID {
	return m.Host.ID()
}

// GetListenAddresses returns the addresses the host is listening on
func (m *Manager) GetListenAddresses() []multiaddr.Multiaddr {
	return m.Host.Addrs()
}

// GetStats returns P2P statistics including metrics
func (m *Manager) GetStats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := map[string]interface{}{
		"peer_id":         m.Host.ID().String(),
		"listen_port":     m.listenPort,
		"connected_peers": len(m.Host.Network().Peers()),
		"listen_addrs":    m.Host.Addrs(),
		"bootstrap_peers": len(m.bootstrapPeers),
		"joined_topics":   len(m.joinedTopics),
	}

	// Add metrics
	metricsSnapshot := m.metrics.GetSnapshot()
	for k, v := range metricsSnapshot {
		stats[k] = v
	}

	return stats
}

// GetMetrics returns current network metrics
func (m *Manager) GetMetrics() *NetworkMetrics {
	return m.metrics
}

// SetEventHandlers sets callback functions for different events
func (m *Manager) SetEventHandlers(
	onBlock func(*core.Block),
	onTx func(*core.Transaction),
	onAttestation func(interface{}),
	onVote func(interface{}),
) {
	m.OnBlockReceived = onBlock
	m.OnTransactionReceived = onTx
	m.OnAttestationReceived = onAttestation
	m.OnVoteReceived = onVote
}

// handleBlockRangeRequest handles incoming block range requests
func (m *Manager) handleBlockRangeRequest(s network.Stream) {
	defer s.Close()
	stdlog.Printf("Received block range request from %s", s.Conn().RemotePeer().String())

	reader := NewJSONStreamReader(s)
	writer := NewJSONStreamWriter(s)

	// Read request
	var request BlockRangeRequest
	if err := reader.ReadJSON(&request); err != nil {
		stdlog.Printf("Error reading block range request: %v", err)
		writer.WriteJSON(&BlockRangeResponse{Error: "invalid request"})
		return
	}

	// Validate request
	if request.StartHeight > request.EndHeight {
		writer.WriteJSON(&BlockRangeResponse{Error: "invalid height range"})
		return
	}

	if request.EndHeight-request.StartHeight > 100 { // Limit to 100 blocks
		writer.WriteJSON(&BlockRangeResponse{Error: "range too large"})
		return
	}

	// Get blocks from blockchain
	responseCh := make(chan Response)
	m.MessageBus <- Message{
		Type:       GetBlocksFromHeight,
		Data:       map[string]int64{"start": request.StartHeight, "end": request.EndHeight},
		ResponseCh: responseCh,
	}

	resp := <-responseCh
	if resp.Error != nil {
		stdlog.Printf("Error fetching blocks: %v", resp.Error)
		writer.WriteJSON(&BlockRangeResponse{Error: resp.Error.Error()})
		return
	}

	blocks, ok := resp.Data.([]*core.Block)
	if !ok {
		writer.WriteJSON(&BlockRangeResponse{Error: "internal error"})
		return
	}

	// Send response
	response := &BlockRangeResponse{Blocks: blocks}
	if err := writer.WriteJSON(response); err != nil {
		stdlog.Printf("Error writing block range response: %v", err)
		return
	}

	stdlog.Printf("Sent %d blocks to peer %s", len(blocks), s.Conn().RemotePeer().String())
}

// handleHeightRequest handles incoming height requests
func (m *Manager) handleHeightRequest(s network.Stream) {
	defer s.Close()
	stdlog.Printf("Received height request from %s", s.Conn().RemotePeer().String())

	reader := NewJSONStreamReader(s)
	writer := NewJSONStreamWriter(s)

	// Read request (empty request)
	var request HeightRequest
	if err := reader.ReadJSON(&request); err != nil {
		stdlog.Printf("Error reading height request: %v", err)
		writer.WriteJSON(&HeightResponse{Error: "invalid request"})
		return
	}

	// Get current height from blockchain
	responseCh := make(chan Response)
	m.MessageBus <- Message{
		Type:       GetBlockchainInfo,
		Data:       "height",
		ResponseCh: responseCh,
	}

	resp := <-responseCh
	if resp.Error != nil {
		stdlog.Printf("Error getting blockchain height: %v", resp.Error)
		writer.WriteJSON(&HeightResponse{Error: resp.Error.Error()})
		return
	}

	height, ok := resp.Data.(int64)
	if !ok {
		writer.WriteJSON(&HeightResponse{Error: "internal error"})
		return
	}

	// Send response
	response := &HeightResponse{Height: height}
	if err := writer.WriteJSON(response); err != nil {
		stdlog.Printf("Error writing height response: %v", err)
		return
	}

	stdlog.Printf("Sent height %d to peer %s", height, s.Conn().RemotePeer().String())
}

// handleStateSnapshotRequest handles incoming state snapshot requests
func (m *Manager) handleStateSnapshotRequest(s network.Stream) {
	defer s.Close()
	stdlog.Printf("Received state snapshot request from %s", s.Conn().RemotePeer().String())

	reader := NewJSONStreamReader(s)
	writer := NewJSONStreamWriter(s)

	// Read request
	var request StateSnapshotRequest
	if err := reader.ReadJSON(&request); err != nil {
		stdlog.Printf("Error reading state snapshot request: %v", err)
		writer.WriteJSON(&StateSnapshotResponse{Error: "invalid request"})
		return
	}

	// Get state snapshot from blockchain
	responseCh := make(chan Response)
	m.MessageBus <- Message{
		Type:       GetStateSnapshot,
		Data:       request.Height,
		ResponseCh: responseCh,
	}

	resp := <-responseCh
	if resp.Error != nil {
		stdlog.Printf("Error creating state snapshot: %v", resp.Error)
		writer.WriteJSON(&StateSnapshotResponse{Error: resp.Error.Error()})
		return
	}

	snapshot, ok := resp.Data.(*StateSnapshot)
	if !ok {
		writer.WriteJSON(&StateSnapshotResponse{Error: "internal error"})
		return
	}

	// Send response
	response := &StateSnapshotResponse{Snapshot: snapshot}
	if err := writer.WriteJSON(response); err != nil {
		stdlog.Printf("Error writing state snapshot response: %v", err)
		return
	}

	stdlog.Printf("Sent state snapshot at height %d to peer %s",
		snapshot.Height, s.Conn().RemotePeer().String())
}
