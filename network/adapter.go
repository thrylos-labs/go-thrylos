// network/adapter.go
// Adapter to make P2PNetwork compatible with consensus bridge

package network

import (
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// Ensure P2PNetwork implements the required interface for consensus bridge

// GetBlockChannel returns the block channel for the consensus bridge
func (n *P2PNetwork) GetBlockChannel() <-chan *core.Block {
	return n.BlockChan
}

// GetAttestationChannel returns the attestation channel for the consensus bridge
func (n *P2PNetwork) GetAttestationChannel() <-chan interface{} {
	return n.AttestationChan
}

// GetVoteChannel returns the vote channel for the consensus bridge
func (n *P2PNetwork) GetVoteChannel() <-chan interface{} {
	return n.VoteChan
}

// Note: BroadcastBlock, BroadcastAttestation, BroadcastVote,
// GetConnectedPeers, and IsConnected are already implemented in p2p.go
