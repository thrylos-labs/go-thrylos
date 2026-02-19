package network

import (
	"encoding/json"
	"fmt"
	"time"

	stdlog "log"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/network/p2p"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// ConsensusEngineInterface for validator discovery
type ConsensusEngineInterface interface {
	RegisterDiscoveredValidator(validator *core.Validator) error
	GetAllValidators() []*core.Validator
}

// SetConsensusEngine sets the consensus engine
func (n *P2PNetwork) SetConsensusEngine(engine ConsensusEngineInterface) {
	n.consensusEngine = engine
}

// P2PNetwork represents the P2P networking layer for Thrylos
type P2PNetwork struct {
	manager *p2p.Manager
	config  *config.Config

	// Event channels for blockchain integration
	BlockChan       chan *core.Block
	TransactionChan chan *core.Transaction
	AttestationChan chan interface{}
	VoteChan        chan interface{}
	startTime       time.Time
	validator       *p2p.MessageValidator
	consensusEngine ConsensusEngineInterface // ADD THIS

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

	// Create a shared validator instance
	validator := p2p.NewMessageValidator(
		p2p.DefaultMaxMessageSize,
		p2p.DefaultMaxBlockRangeSize,
		p2p.DefaultStreamReadTimeout,
		p2p.DefaultStreamWriteTimeout,
	)

	p2pConfig := &p2p.Config{
		ListenPort:     cfg.P2P.ListenPort,
		BootstrapPeers: cfg.P2P.BootstrapPeers,
	}

	manager, err := p2p.NewManager(p2pConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create P2P manager: %w", err)
	}

	network := &P2PNetwork{
		startTime:       time.Now(),
		manager:         manager,
		config:          cfg,
		BlockChan:       make(chan *core.Block, 1000),
		TransactionChan: make(chan *core.Transaction, 1000),
		AttestationChan: make(chan interface{}, 1000),
		VoteChan:        make(chan interface{}, 1000),
		validator:       validator,
	}

	// Set up event handlers
	network.setupEventHandlers()

	return network, nil
}

// NewP2PNetworkWithConfig creates a new P2P network with explicit configuration
func NewP2PNetworkWithConfig(cfg *config.Config, p2pListenPort int, bootstrapPeers []string, enabled bool, identityKey libp2pcrypto.PrivKey) (*P2PNetwork, error) {
	if !enabled {
		return nil, fmt.Errorf("P2P networking is disabled")
	}

	validator := p2p.NewMessageValidator(
		p2p.DefaultMaxMessageSize,
		p2p.DefaultMaxBlockRangeSize,
		p2p.DefaultStreamReadTimeout,
		p2p.DefaultStreamWriteTimeout,
	)

	p2pConfig := &p2p.Config{
		ListenPort:     p2pListenPort,
		BootstrapPeers: bootstrapPeers,
		IdentityKey:    identityKey, // ← add this
	}

	manager, err := p2p.NewManager(p2pConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create P2P manager: %w", err)
	}

	network := &P2PNetwork{
		startTime:       time.Now(), // ✅ ALSO ADD THIS for consistency
		manager:         manager,
		config:          cfg,
		BlockChan:       make(chan *core.Block, 1000),
		TransactionChan: make(chan *core.Transaction, 1000),
		AttestationChan: make(chan interface{}, 1000),
		VoteChan:        make(chan interface{}, 1000),
		validator:       validator, // ✅ ADD THIS LINE
	}

	// Set up event handlers
	network.setupEventHandlers()

	return network, nil
}

// Start starts the P2P network
func (n *P2PNetwork) Start() error {
	stdlog.Println("Starting Thrylos P2P network...")
	n.startTime = time.Now()

	if err := n.manager.Start(); err != nil {
		return fmt.Errorf("failed to start P2P manager: %w", err)
	}

	go n.processMessages()
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
	// 1. Peer Check (Rate Limit & Ban Status)
	if err := n.validator.CheckPeerStatus(msg.FromPeerID); err != nil {
		stdlog.Printf("Dropped message from bad peer %s: %v", msg.FromPeerID, err)
		return
	}

	switch msg.Type {
	case p2p.ProcessBlock:
		if block, ok := msg.Data.(*core.Block); ok {
			// Basic Check: Is block valid? (Deep validation happens in Core, but surface check here)
			if block == nil {
				n.validator.AdjustReputation(msg.FromPeerID, p2p.ScoreSpam)
				return
			}

			select {
			case n.BlockChan <- block:
				// Reward good behavior (tentatively)
				n.validator.AdjustReputation(msg.FromPeerID, p2p.ScoreGoodBlock)
			default:
				stdlog.Println("Block channel full")
			}
		} else {
			// Received malformed data
			n.validator.AdjustReputation(msg.FromPeerID, p2p.ScoreInvalidBlock)
		}

	case p2p.ProcessTransaction:
		if tx, ok := msg.Data.(*core.Transaction); ok {
			if tx == nil {
				n.validator.AdjustReputation(msg.FromPeerID, p2p.ScoreSpam)
				return
			}
			select {
			case n.TransactionChan <- tx:
				// Transactions are neutral/slight positive
			default:
				stdlog.Println("Transaction channel full")
			}
		} else {
			n.validator.AdjustReputation(msg.FromPeerID, p2p.ScoreInvalidTx)
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

	// NEW: Handle validator announcements
	case p2p.ValidatorAnnouncement:
		if validator, ok := msg.Data.(*core.Validator); ok {
			if validator == nil {
				stdlog.Println("⚠️ Received nil validator announcement")
				return
			}

			// Register the validator
			if n.consensusEngine != nil {
				if err := n.consensusEngine.RegisterDiscoveredValidator(validator); err != nil {
					stdlog.Printf("❌ Failed to register validator from peer: %v", err)
					if n.validator != nil {
						n.validator.AdjustReputation(msg.FromPeerID, p2p.ScoreInvalidBlock)
					}
				} else {
					stdlog.Printf("✅ Registered validator %s from peer %s", validator.Address, msg.FromPeerID)
					if n.validator != nil {
						n.validator.AdjustReputation(msg.FromPeerID, p2p.ScoreGoodBlock)
					}
				}
			}
		} else {
			stdlog.Println("⚠️ Invalid validator announcement data type")
			if n.validator != nil {
				n.validator.AdjustReputation(msg.FromPeerID, p2p.ScoreSpam)
			}
		}

	// NEW: Handle validator sync requests
	case p2p.ValidatorSync:
		if n.consensusEngine != nil {
			// Get all validators and send them back
			validators := n.consensusEngine.GetAllValidators()
			stdlog.Printf("📤 Sending %d validators to peer %s", len(validators), msg.FromPeerID)

			// Send response if ResponseCh is available
			if msg.ResponseCh != nil {
				msg.ResponseCh <- p2p.Response{
					Success: true,
					Data:    validators,
				}
			}
		}

	default:
		stdlog.Printf("⚠️ Unknown message type: %v", msg.Type)
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
// IsHealthy reports whether the P2P layer is considered healthy.
func (n *P2PNetwork) IsHealthy() bool {
	if time.Since(n.startTime) < 5*time.Minute {
		// startup grace period
		return true
	}
	return n.IsConnected()
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

// DisconnectPeer disconnects from a specific peer
func (n *P2PNetwork) DisconnectPeer(peerID string) error {
	if n.manager == nil {
		return fmt.Errorf("P2P manager not available")
	}
	return n.manager.DisconnectPeer(peerID)
}

func (n *P2PNetwork) GetValidatorChannel() <-chan *core.Validator {
	return n.manager.ValidatorChan
}

// ============================================================================
// VALIDATOR DISCOVERY
// ============================================================================

// AnnounceValidator broadcasts this node's validator to the network
func (n *P2PNetwork) AnnounceValidator(validator *core.Validator) error {
	if validator == nil {
		return fmt.Errorf("validator cannot be nil")
	}

	// Broadcast via PubSub
	topic, err := n.manager.GetTopic("thrylos-validators")
	if err != nil {
		return fmt.Errorf("failed to get validators topic: %w", err)
	}

	// Serialize validator
	data, err := json.Marshal(validator)
	if err != nil {
		return fmt.Errorf("failed to marshal validator: %w", err)
	}

	if err := topic.Publish(n.manager.Ctx, data); err != nil {
		return fmt.Errorf("failed to publish validator announcement: %w", err)
	}

	stdlog.Printf("📢 Announced validator %s to network", validator.Address)
	return nil
}

type Message = p2p.Message
type Response = p2p.Response

// RequestValidatorSync requests full validator set from a peer
func (n *P2PNetwork) RequestValidatorSync() {
	// Send validator sync request via manager
	msg := p2p.Message{
		Type: p2p.ValidatorSync,
		Data: nil, // Request all validators
	}

	select {
	case n.manager.BlockchainProcessCh <- msg:
		stdlog.Println("📡 Requested validator sync from network")
	default:
		stdlog.Println("⚠️ Failed to request validator sync: channel full")
	}
}

// GetMessageBus returns the P2P message bus
func (n *P2PNetwork) GetMessageBus() chan Message {
	if n.manager == nil {
		return nil
	}
	return n.manager.GetMessageBus()
}
