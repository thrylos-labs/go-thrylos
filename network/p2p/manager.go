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
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"

	stdlog "log"

	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// Protocol IDs and PubSub topic names for Thrylos
const (
	ProtocolBlockSync   protocol.ID = "/thrylos/blocksync/1.0.0"
	ProtocolTransaction protocol.ID = "/thrylos/transaction/1.0.0"
	ProtocolAttestation protocol.ID = "/thrylos/attestation/1.0.0"
	ProtocolVote        protocol.ID = "/thrylos/vote/1.0.0"
	TopicBlocks                     = "thrylos-blocks"
	TopicTransactions               = "thrylos-transactions"
	TopicAttestations               = "thrylos-attestations"
	TopicVotes                      = "thrylos-votes"
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

	// Synchronization
	mu sync.RWMutex
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
	}

	return manager, nil
}

// Start starts all P2P services
func (m *Manager) Start() error {
	// Connect to bootstrap peers
	m.connectToBootstrapPeers()

	// Start discovery services
	m.startMDNSDiscovery()
	m.startDHTDiscovery()

	// Set up protocol handlers
	m.Host.SetStreamHandler(ProtocolBlockSync, m.handleBlockSyncRequest)
	m.Host.SetStreamHandler(ProtocolTransaction, m.handleTransactionRequest)
	m.Host.SetStreamHandler(ProtocolAttestation, m.handleAttestationRequest)
	m.Host.SetStreamHandler(ProtocolVote, m.handleVoteRequest)

	// Subscribe to PubSub topics
	m.subscribeToPubSubTopics()

	stdlog.Println("Thrylos P2P services started successfully")
	return nil
}

// Stop gracefully shuts down the P2P manager
func (m *Manager) Stop() error {
	stdlog.Println("Shutting down Thrylos P2P services...")
	m.Cancel()

	if m.DHT != nil {
		if err := m.DHT.Close(); err != nil {
			stdlog.Printf("Error closing DHT: %v", err)
		}
	}

	if err := m.Host.Close(); err != nil {
		return fmt.Errorf("error closing libp2p host: %w", err)
	}

	close(m.BlockchainProcessCh)
	close(m.MessageBus)

	stdlog.Println("Thrylos P2P services shut down successfully")
	return nil
}

// connectToBootstrapPeers connects to configured bootstrap peers
func (m *Manager) connectToBootstrapPeers() {
	for _, addr := range m.bootstrapPeers {
		pi, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			stdlog.Printf("Invalid bootstrap peer address %s: %v", addr, err)
			continue
		}
		if pi.ID == m.Host.ID() {
			continue // Don't connect to self
		}

		stdlog.Printf("Connecting to bootstrap peer: %s", pi.ID.String())
		go func(pi peer.AddrInfo) {
			connectCtx, connectCancel := context.WithTimeout(m.Ctx, 10*time.Second)
			defer connectCancel()
			if err := m.Host.Connect(connectCtx, pi); err != nil {
				stdlog.Printf("Failed to connect to bootstrap peer %s: %v", pi.ID.String(), err)
			} else {
				stdlog.Printf("Connected to bootstrap peer: %s", pi.ID.String())
			}
		}(*pi)
	}
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

// GetStats returns P2P statistics
func (m *Manager) GetStats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return map[string]interface{}{
		"peer_id":         m.Host.ID().String(),
		"listen_port":     m.listenPort,
		"connected_peers": len(m.Host.Network().Peers()),
		"listen_addrs":    m.Host.Addrs(),
		"bootstrap_peers": len(m.bootstrapPeers),
	}
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
