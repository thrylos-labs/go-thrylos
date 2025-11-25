// consensus/pos/bridge.go
// Bridge between P2P network and consensus engine

package pos

import (
	"fmt"
	"log"

	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/types"
)

// P2PNetwork interface for the network layer
// This matches your network.P2PNetwork structure
type P2PNetwork interface {
	// Outgoing: Broadcast to network
	BroadcastBlock(block *core.Block) error
	BroadcastAttestation(attestation interface{}) error
	BroadcastVote(vote interface{}) error

	// Incoming: Receive from network (channels)
	GetBlockChannel() <-chan *core.Block
	GetAttestationChannel() <-chan interface{}
	GetVoteChannel() <-chan interface{}

	// Network info
	GetConnectedPeers() int
	IsConnected() bool
}

// ConsensusBridge bridges P2P network and consensus engine
type ConsensusBridge struct {
	consensus *ConsensusEngine
	network   P2PNetwork
	stopCh    chan struct{}
}

// NewConsensusBridge creates a new bridge between P2P and consensus
func NewConsensusBridge(consensus *ConsensusEngine, network P2PNetwork) *ConsensusBridge {
	return &ConsensusBridge{
		consensus: consensus,
		network:   network,
		stopCh:    make(chan struct{}),
	}
}

// Start starts the bridge, forwarding messages in both directions
func (cb *ConsensusBridge) Start() error {
	log.Println("🌉 Starting consensus <-> P2P bridge...")

	// Start forwarding consensus messages to P2P network
	go cb.forwardConsensusToNetwork()

	// Start forwarding P2P messages to consensus engine
	go cb.forwardNetworkToConsensus()

	log.Println("✅ Bridge started successfully")
	return nil
}

// Stop stops the bridge
func (cb *ConsensusBridge) Stop() error {
	log.Println("🛑 Stopping consensus bridge...")
	close(cb.stopCh)
	return nil
}

// forwardConsensusToNetwork forwards consensus broadcasts to P2P network
func (cb *ConsensusBridge) forwardConsensusToNetwork() {
	log.Println("📤 Starting consensus → network forwarder")

	for {
		select {
		case msg := <-cb.consensus.broadcastChan:
			if err := cb.handleConsensusMessage(msg); err != nil {
				log.Printf("❌ Failed to broadcast consensus message: %v", err)
			}

		case <-cb.stopCh:
			log.Println("🛑 Stopping consensus → network forwarder")
			return
		}
	}
}

// forwardNetworkToConsensus forwards P2P messages to consensus engine
func (cb *ConsensusBridge) forwardNetworkToConsensus() {
	log.Println("📥 Starting network → consensus forwarder")

	for {
		select {
		// Forward blocks
		case block := <-cb.network.GetBlockChannel():
			blockProposal := &BlockProposal{
				Block:     block,
				Proposer:  block.Header.Validator,
				Slot:      uint64(block.Header.Index), // Approximation
				Epoch:     uint64(block.Header.Index) / 32,
				Signature: nil,
			}
			cb.consensus.receiveChan <- blockProposal
			log.Printf("📦 Forwarded block %s to consensus", block.Hash[:min(8, len(block.Hash))])

		// Forward attestations
		case attestation := <-cb.network.GetAttestationChannel():
			if att, ok := attestation.(*types.Attestation); ok {
				cb.consensus.receiveChan <- att
				log.Printf("✅ Forwarded attestation from %s to consensus", att.ValidatorAddress[:min(8, len(att.ValidatorAddress))])
			} else {
				log.Printf("⚠️ Received non-Attestation type from network: %T", attestation)
			}

		// Forward votes
		case vote := <-cb.network.GetVoteChannel():
			if v, ok := vote.(*Vote); ok {
				cb.consensus.receiveChan <- v
				log.Printf("🗳️  Forwarded vote from %s to consensus", v.ValidatorAddress[:min(8, len(v.ValidatorAddress))])
			} else {
				log.Printf("⚠️ Received non-Vote type from network: %T", vote)
			}

		case <-cb.stopCh:
			log.Println("🛑 Stopping network → consensus forwarder")
			return
		}
	}
}

// handleConsensusMessage broadcasts a consensus message to the P2P network
func (cb *ConsensusBridge) handleConsensusMessage(msg interface{}) error {
	switch m := msg.(type) {
	case *BlockProposal:
		if m.Block == nil {
			return fmt.Errorf("block proposal has nil block")
		}
		if err := cb.network.BroadcastBlock(m.Block); err != nil {
			return fmt.Errorf("failed to broadcast block: %w", err)
		}
		log.Printf("📤 Broadcasted block %s to network", m.Block.Hash[:min(8, len(m.Block.Hash))])
		return nil

	case *types.Attestation:
		if err := cb.network.BroadcastAttestation(m); err != nil {
			return fmt.Errorf("failed to broadcast attestation: %w", err)
		}
		log.Printf("📤 Broadcasted attestation from %s to network", m.ValidatorAddress[:min(8, len(m.ValidatorAddress))])
		return nil

	case *Vote:
		if err := cb.network.BroadcastVote(m); err != nil {
			return fmt.Errorf("failed to broadcast vote: %w", err)
		}
		log.Printf("📤 Broadcasted vote from %s to network", m.ValidatorAddress[:min(8, len(m.ValidatorAddress))])
		return nil

	default:
		return fmt.Errorf("unknown message type: %T", msg)
	}
}

// GetStats returns bridge statistics
func (cb *ConsensusBridge) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"connected_peers": cb.network.GetConnectedPeers(),
		"is_connected":    cb.network.IsConnected(),
		"consensus_stats": cb.consensus.GetStats(),
	}
}

// Helper function for safe string slicing
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
