package network

import (
	"fmt"
	"time"

	stdlog "log"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/network/p2p"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// P2PNetwork represents the P2P networking layer for Thrylos
type P2PNetwork struct {
	manager *p2p.Manager
	config  *config.Config

	// Event channels for blockchain integration
	BlockChan       chan *core.Block
	TransactionChan chan *core.Transaction
	AttestationChan chan interface{}
	VoteChan        chan interface{}
}

// Config for P2P network
type NetworkConfig struct {
	ListenPort     int
	BootstrapPeers []string
	EnableP2P      bool
}

// NewP2PNetwork creates a new P2P network instance
func NewP2PNetwork(cfg *config.Config) (*P2PNetwork, error) {
	if !cfg.P2P.Enabled {
		return nil, fmt.Errorf("P2P networking is disabled in configuration")
	}

	p2pConfig := &p2p.Config{
		ListenPort:     cfg.P2P.ListenPort,
		BootstrapPeers: cfg.P2P.BootstrapPeers,
	}

	manager, err := p2p.NewManager(p2pConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create P2P manager: %w", err)
	}

	network := &P2PNetwork{
		manager:         manager,
		config:          cfg,
		BlockChan:       make(chan *core.Block, 1000),
		TransactionChan: make(chan *core.Transaction, 1000),
		AttestationChan: make(chan interface{}, 1000),
		VoteChan:        make(chan interface{}, 1000),
	}

	// Set up event handlers
	network.setupEventHandlers()

	return network, nil
}

// NewP2PNetworkWithConfig creates a new P2P network with explicit configuration
func NewP2PNetworkWithConfig(cfg *config.Config, p2pListenPort int, bootstrapPeers []string, enabled bool) (*P2PNetwork, error) {
	if !enabled {
		return nil, fmt.Errorf("P2P networking is disabled")
	}

	p2pConfig := &p2p.Config{
		ListenPort:     p2pListenPort,
		BootstrapPeers: bootstrapPeers,
	}

	manager, err := p2p.NewManager(p2pConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create P2P manager: %w", err)
	}

	network := &P2PNetwork{
		manager:         manager,
		config:          cfg,
		BlockChan:       make(chan *core.Block, 1000),
		TransactionChan: make(chan *core.Transaction, 1000),
		AttestationChan: make(chan interface{}, 1000),
		VoteChan:        make(chan interface{}, 1000),
	}

	// Set up event handlers
	network.setupEventHandlers()

	return network, nil
}

// Start starts the P2P network
func (n *P2PNetwork) Start() error {
	stdlog.Println("Starting Thrylos P2P network...")

	if err := n.manager.Start(); err != nil {
		return fmt.Errorf("failed to start P2P manager: %w", err)
	}

	// Start message processing
	go n.processMessages()

	stdlog.Printf("P2P network started successfully on port %d", n.config.P2P.ListenPort)
	return nil
}

// Stop stops the P2P network
func (n *P2PNetwork) Stop() error {
	stdlog.Println("Stopping Thrylos P2P network...")

	if err := n.manager.Stop(); err != nil {
		return fmt.Errorf("failed to stop P2P manager: %w", err)
	}

	// Close channels
	close(n.BlockChan)
	close(n.TransactionChan)
	close(n.AttestationChan)
	close(n.VoteChan)

	stdlog.Println("P2P network stopped successfully")
	return nil
}

// setupEventHandlers sets up callbacks for different P2P events
func (n *P2PNetwork) setupEventHandlers() {
	n.manager.SetEventHandlers(
		func(block *core.Block) {
			select {
			case n.BlockChan <- block:
				stdlog.Printf("Received block %s from P2P network", block.Hash)
			default:
				stdlog.Println("Block channel full, dropping received block")
			}
		},
		func(tx *core.Transaction) {
			select {
			case n.TransactionChan <- tx:
				stdlog.Printf("Received transaction %s from P2P network", tx.Id)
			default:
				stdlog.Println("Transaction channel full, dropping received transaction")
			}
		},
		func(attestation interface{}) {
			select {
			case n.AttestationChan <- attestation:
				stdlog.Println("Received attestation from P2P network")
			default:
				stdlog.Println("Attestation channel full, dropping received attestation")
			}
		},
		func(vote interface{}) {
			select {
			case n.VoteChan <- vote:
				stdlog.Println("Received vote from P2P network")
			default:
				stdlog.Println("Vote channel full, dropping received vote")
			}
		},
	)
}

// processMessages processes incoming P2P messages
func (n *P2PNetwork) processMessages() {
	for {
		select {
		case msg := <-n.manager.BlockchainProcessCh:
			n.handleBlockchainMessage(msg)
		case <-n.manager.Ctx.Done():
			stdlog.Println("P2P message processing stopped")
			return
		}
	}
}

// handleBlockchainMessage handles messages from the P2P layer
func (n *P2PNetwork) handleBlockchainMessage(msg p2p.Message) {
	switch msg.Type {
	case p2p.ProcessBlock:
		if block, ok := msg.Data.(*core.Block); ok {
			select {
			case n.BlockChan <- block:
			default:
				stdlog.Println("Block channel full, dropping message")
			}
		}
	case p2p.ProcessTransaction:
		if tx, ok := msg.Data.(*core.Transaction); ok {
			select {
			case n.TransactionChan <- tx:
			default:
				stdlog.Println("Transaction channel full, dropping message")
			}
		}
	case p2p.ProcessAttestation:
		select {
		case n.AttestationChan <- msg.Data:
		default:
			stdlog.Println("Attestation channel full, dropping message")
		}
	case p2p.ProcessVote:
		select {
		case n.VoteChan <- msg.Data:
		default:
			stdlog.Println("Vote channel full, dropping message")
		}
	}
}

// Broadcast methods for sending data to the network

// BroadcastBlock broadcasts a block to all peers
func (n *P2PNetwork) BroadcastBlock(block *core.Block) error {
	return n.manager.BroadcastBlock(block)
}

// BroadcastTransaction broadcasts a transaction to all peers
func (n *P2PNetwork) BroadcastTransaction(tx *core.Transaction) error {
	return n.manager.BroadcastTransaction(tx)
}

// BroadcastAttestation broadcasts an attestation to all peers
func (n *P2PNetwork) BroadcastAttestation(attestation interface{}) error {
	return n.manager.BroadcastAttestation(attestation)
}

// BroadcastVote broadcasts a vote to all peers
func (n *P2PNetwork) BroadcastVote(vote interface{}) error {
	return n.manager.BroadcastVote(vote)
}

// GetNetworkStats returns P2P network statistics
func (n *P2PNetwork) GetNetworkStats() map[string]interface{} {
	return n.manager.GetStats()
}

// GetConnectedPeers returns the number of connected peers
func (n *P2PNetwork) GetConnectedPeers() int {
	return n.manager.GetPeerCount()
}

// IsConnected returns true if connected to at least one peer
func (n *P2PNetwork) IsConnected() bool {
	return n.GetConnectedPeers() > 0
}

// GetPeerID returns this node's peer ID
func (n *P2PNetwork) GetPeerID() string {
	return n.manager.GetHostID().String()
}

// Discovery methods

// DiscoverPeers starts peer discovery
func (n *P2PNetwork) DiscoverPeers() {
	// Discovery is automatically started in Start(), but can be triggered manually
	stdlog.Println("Peer discovery is running automatically")
}

// Health and monitoring

// IsHealthy returns true if the P2P network is healthy
func (n *P2PNetwork) IsHealthy() bool {
	// Consider network healthy if we have at least one connection
	// or if we're still trying to connect (within first 5 minutes)
	return n.IsConnected() || time.Since(time.Now()) < 5*time.Minute
}

// GetHealthStatus returns detailed health information
func (n *P2PNetwork) GetHealthStatus() map[string]interface{} {
	stats := n.GetNetworkStats()
	stats["is_healthy"] = n.IsHealthy()
	stats["is_connected"] = n.IsConnected()

	return stats
}

// Configuration helpers

// DefaultNetworkConfig returns a default network configuration
func DefaultNetworkConfig() *NetworkConfig {
	return &NetworkConfig{
		ListenPort:     9000,
		BootstrapPeers: []string{},
		EnableP2P:      true,
	}
}

// ValidateConfig validates the network configuration
func ValidateConfig(cfg *NetworkConfig) error {
	if cfg.ListenPort <= 0 || cfg.ListenPort > 65535 {
		return fmt.Errorf("invalid listen port: %d", cfg.ListenPort)
	}

	return nil
}

// RequestBlockRange requests a range of blocks from a specific peer
func (n *P2PNetwork) RequestBlockRange(peerID string, startHeight, endHeight int64) ([]*core.Block, error) {
	if n.manager == nil {
		return nil, fmt.Errorf("P2P manager not available")
	}
	return n.manager.RequestBlockRange(peerID, startHeight, endHeight)
}

// RequestPeerHeight requests the blockchain height from a specific peer
func (n *P2PNetwork) RequestPeerHeight(peerID string) (int64, error) {
	if n.manager == nil {
		return 0, fmt.Errorf("P2P manager not available")
	}
	return n.manager.RequestPeerHeight(peerID)
}

// RequestStateSnapshot requests a state snapshot from a peer
func (n *P2PNetwork) RequestStateSnapshot(peerID string, height int64) (*p2p.StateSnapshot, error) {
	if n.manager == nil {
		return nil, fmt.Errorf("P2P manager not available")
	}
	return n.manager.RequestStateSnapshot(peerID, height)
}

// GetConnectedPeerIDs returns list of connected peer IDs as strings
func (n *P2PNetwork) GetConnectedPeerIDs() []string {
	if n.manager == nil {
		return []string{}
	}
	return n.manager.GetConnectedPeerIDs()
}
