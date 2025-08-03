package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/libp2p/go-libp2p/p2p/discovery/routing"

	stdlog "log"

	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// Discovery implementation

// HandlePeerFound handles newly discovered peers via mDNS
func (m *Manager) HandlePeerFound(pi peer.AddrInfo) {
	stdlog.Printf("Discovered new peer via mDNS: %s", pi.ID.String())
	if pi.ID == m.Host.ID() {
		return
	}
	go func() {
		connectCtx, connectCancel := context.WithTimeout(m.Ctx, 10*time.Second)
		defer connectCancel()
		if err := m.Host.Connect(connectCtx, pi); err != nil {
			stdlog.Printf("Failed to connect to mDNS discovered peer %s: %v", pi.ID.String(), err)
		} else {
			stdlog.Printf("Successfully connected to mDNS discovered peer %s", pi.ID.String())
		}
	}()
}

// startMDNSDiscovery starts local network peer discovery
func (m *Manager) startMDNSDiscovery() {
	service := mdns.NewMdnsService(m.Host, "thrylos-blockchain", m)
	if err := service.Start(); err != nil {
		stdlog.Printf("Failed to start mDNS discovery: %v", err)
	} else {
		stdlog.Println("mDNS discovery started")
	}
}

// startDHTDiscovery starts DHT-based peer discovery
func (m *Manager) startDHTDiscovery() {
	routingDiscovery := routing.NewRoutingDiscovery(m.DHT)
	routingDiscovery.Advertise(m.Ctx, "thrylos-blockchain")

	go func() {
		for {
			select {
			case <-m.Ctx.Done():
				return
			case <-time.After(30 * time.Second):
				stdlog.Println("Searching for peers via DHT...")
				peerChan, err := routingDiscovery.FindPeers(m.Ctx, "thrylos-blockchain")
				if err != nil {
					stdlog.Printf("DHT peer discovery failed: %v", err)
					continue
				}
				for pi := range peerChan {
					if pi.ID == m.Host.ID() || len(pi.Addrs) == 0 {
						continue
					}
					stdlog.Printf("Discovered peer via DHT: %s", pi.ID.String())
					go func(pi peer.AddrInfo) {
						connectCtx, connectCancel := context.WithTimeout(m.Ctx, 10*time.Second)
						defer connectCancel()
						if err := m.Host.Connect(connectCtx, pi); err != nil {
							stdlog.Printf("Failed to connect to DHT discovered peer %s: %v", pi.ID.String(), err)
						} else {
							stdlog.Printf("Successfully connected to DHT discovered peer %s", pi.ID.String())
						}
					}(pi)
				}
			}
		}
	}()
	stdlog.Println("DHT discovery started")
}

// Protocol Handlers

// handleBlockSyncRequest handles incoming block synchronization requests
func (m *Manager) handleBlockSyncRequest(s network.Stream) {
	defer s.Close()
	stdlog.Printf("Received block sync request from %s", s.Conn().RemotePeer().String())

	reader := NewJSONStreamReader(s)
	writer := NewJSONStreamWriter(s)

	var reqData map[string]int64
	if err := reader.ReadJSON(&reqData); err != nil {
		stdlog.Printf("Error reading sync request from %s: %v", s.Conn().RemotePeer().String(), err)
		return
	}
	startHeight := reqData["startHeight"]
	stdlog.Printf("Peer %s requested blocks from height: %d", s.Conn().RemotePeer().String(), startHeight)

	// Send request to blockchain for blocks
	responseCh := make(chan Response)
	m.MessageBus <- Message{
		Type:       GetBlocksFromHeight,
		Data:       startHeight,
		ResponseCh: responseCh,
	}

	resp := <-responseCh
	if resp.Error != nil {
		stdlog.Printf("Error fetching blocks for sync with %s: %v", s.Conn().RemotePeer().String(), resp.Error)
		writer.WriteJSON(map[string]string{"error": resp.Error.Error()})
		return
	}

	blocks, ok := resp.Data.([]*core.Block)
	if !ok {
		stdlog.Printf("Invalid data type received for GetBlocksFromHeight: %T", resp.Data)
		writer.WriteJSON(map[string]string{"error": "internal server error"})
		return
	}

	for _, block := range blocks {
		if err := writer.WriteJSON(block); err != nil {
			stdlog.Printf("Error writing block %s to sync stream: %v", block.Hash, err)
			return
		}
	}
	writer.Write([]byte("EOF\n"))
	stdlog.Printf("Sent %d blocks to peer %s for sync", len(blocks), s.Conn().RemotePeer().String())
}

// handleTransactionRequest handles incoming transaction requests
func (m *Manager) handleTransactionRequest(s network.Stream) {
	defer s.Close()
	stdlog.Printf("Received transaction from %s", s.Conn().RemotePeer().String())

	reader := NewJSONStreamReader(s)
	var tx core.Transaction
	if err := reader.ReadJSON(&tx); err != nil {
		stdlog.Printf("Error unmarshaling transaction: %v", err)
		return
	}

	// Forward to blockchain for processing
	if m.OnTransactionReceived != nil {
		m.OnTransactionReceived(&tx)
	}
}

// handleAttestationRequest handles incoming attestation requests
func (m *Manager) handleAttestationRequest(s network.Stream) {
	defer s.Close()
	stdlog.Printf("Received attestation from %s", s.Conn().RemotePeer().String())

	reader := NewJSONStreamReader(s)
	var attestation map[string]interface{} // Generic attestation format
	if err := reader.ReadJSON(&attestation); err != nil {
		stdlog.Printf("Error unmarshaling attestation: %v", err)
		return
	}

	// Forward to consensus for processing
	if m.OnAttestationReceived != nil {
		m.OnAttestationReceived(attestation)
	}
}

// handleVoteRequest handles incoming vote requests
func (m *Manager) handleVoteRequest(s network.Stream) {
	defer s.Close()
	stdlog.Printf("Received vote from %s", s.Conn().RemotePeer().String())

	reader := NewJSONStreamReader(s)
	var vote map[string]interface{} // Generic vote format
	if err := reader.ReadJSON(&vote); err != nil {
		stdlog.Printf("Error unmarshaling vote: %v", err)
		return
	}

	// Forward to consensus for processing
	if m.OnVoteReceived != nil {
		m.OnVoteReceived(vote)
	}
}

// PubSub Logic

// subscribeToPubSubTopics subscribes to all Thrylos PubSub topics
func (m *Manager) subscribeToPubSubTopics() {
	topics := []string{TopicBlocks, TopicTransactions, TopicAttestations, TopicVotes}
	for _, topicName := range topics {
		// Use the caching system instead of direct PubSub.Join()
		topic, err := m.getOrJoinTopic(topicName)
		if err != nil {
			stdlog.Fatalf("Failed to join PubSub topic %s: %v", topicName, err)
		}

		// Subscribe to the topic
		sub, err := topic.Subscribe()
		if err != nil {
			stdlog.Fatalf("Failed to subscribe to PubSub topic %s: %v", topicName, err)
		}

		// Start reading messages from this topic
		go m.readPubSubMessages(topicName, sub)
		stdlog.Printf("Subscribed to PubSub topic: %s", topicName)
	}
}

// readPubSubMessages reads messages from a PubSub topic
func (m *Manager) readPubSubMessages(topicName string, sub *pubsub.Subscription) {
	for {
		msg, err := sub.Next(m.Ctx)
		if err != nil {
			if err == context.Canceled {
				stdlog.Printf("PubSub subscription for %s canceled", topicName)
			} else {
				stdlog.Printf("Error reading from PubSub subscription %s: %v", topicName, err)
			}
			return
		}

		if msg.ReceivedFrom == m.Host.ID() {
			continue // Ignore messages from self
		}

		stdlog.Printf("Received PubSub message from %s on topic %s", msg.ReceivedFrom.String(), topicName)

		switch topicName {
		case TopicBlocks:
			var block core.Block
			if err := json.Unmarshal(msg.Data, &block); err != nil {
				stdlog.Printf("Failed to unmarshal block from PubSub: %v", err)
				continue
			}
			m.BlockchainProcessCh <- Message{Type: ProcessBlock, Data: &block}

		case TopicTransactions:
			var tx core.Transaction
			if err := json.Unmarshal(msg.Data, &tx); err != nil {
				stdlog.Printf("Failed to unmarshal transaction from PubSub: %v", err)
				continue
			}
			m.BlockchainProcessCh <- Message{Type: ProcessTransaction, Data: &tx}

		case TopicAttestations:
			var attestation map[string]interface{}
			if err := json.Unmarshal(msg.Data, &attestation); err != nil {
				stdlog.Printf("Failed to unmarshal attestation from PubSub: %v", err)
				continue
			}
			m.BlockchainProcessCh <- Message{Type: ProcessAttestation, Data: attestation}

		case TopicVotes:
			var vote map[string]interface{}
			if err := json.Unmarshal(msg.Data, &vote); err != nil {
				stdlog.Printf("Failed to unmarshal vote from PubSub: %v", err)
				continue
			}
			m.BlockchainProcessCh <- Message{Type: ProcessVote, Data: vote}
		}
	}
}

// Broadcasting Functions

// BroadcastBlock broadcasts a block to all peers via PubSub
// In manager.go - Fix the broadcast methods
func (m *Manager) BroadcastBlock(block *core.Block) error {
	blockData, err := json.Marshal(block)
	if err != nil {
		return fmt.Errorf("failed to serialize block for PubSub: %w", err)
	}

	// Use rate-limited broadcast instead of direct topic join
	stdlog.Printf("Broadcasting block %s via PubSub to topic %s", block.Hash, TopicBlocks)
	return m.rateLimitedBroadcast(TopicBlocks, blockData)
}

// BroadcastTransaction broadcasts a transaction to all peers via PubSub
func (m *Manager) BroadcastTransaction(tx *core.Transaction) error {
	txData, err := json.Marshal(tx)
	if err != nil {
		return fmt.Errorf("failed to serialize transaction for PubSub: %w", err)
	}

	stdlog.Printf("Broadcasting transaction %s via PubSub to topic %s", tx.Id, TopicTransactions)
	return m.rateLimitedBroadcast(TopicTransactions, txData)
}

func (m *Manager) BroadcastAttestation(attestation interface{}) error {
	attestationData, err := json.Marshal(attestation)
	if err != nil {
		return fmt.Errorf("failed to serialize attestation for PubSub: %w", err)
	}

	stdlog.Printf("Broadcasting attestation via PubSub to topic %s", TopicAttestations)
	return m.rateLimitedBroadcast(TopicAttestations, attestationData)
}

// BroadcastVote broadcasts a vote to all peers via PubSub
func (m *Manager) BroadcastVote(vote interface{}) error {
	voteData, err := json.Marshal(vote)
	if err != nil {
		return fmt.Errorf("failed to serialize vote for PubSub: %w", err)
	}

	stdlog.Printf("Broadcasting vote via PubSub to topic %s", TopicVotes)
	return m.rateLimitedBroadcast(TopicVotes, voteData)
}

// Blockchain Synchronization

// SyncBlockchainWithPeer synchronizes blockchain with a specific peer
func (m *Manager) SyncBlockchainWithPeer(peerID peer.ID) error {
	stdlog.Printf("Initiating blockchain sync with peer: %s", peerID.String())
	s, err := m.Host.NewStream(m.Ctx, peerID, ProtocolBlockSync)
	if err != nil {
		return fmt.Errorf("failed to open block sync stream with %s: %w", peerID.String(), err)
	}
	defer s.Close()

	writer := NewJSONStreamWriter(s)
	reader := NewJSONStreamReader(s)

	// Get our current blockchain height
	heightCh := make(chan Response)
	m.MessageBus <- Message{
		Type:       GetBlockchainInfo,
		Data:       "height",
		ResponseCh: heightCh,
	}
	heightResp := <-heightCh

	var currentHeight int64
	if heightResp.Error == nil {
		if height, ok := heightResp.Data.(int64); ok {
			currentHeight = height
		}
	} else {
		stdlog.Printf("Warning: Could not get current blockchain height for sync: %v", heightResp.Error)
		currentHeight = 0
	}

	// Send request to peer for blocks from startHeight
	reqData := map[string]int64{"startHeight": currentHeight + 1}
	if err := writer.WriteJSON(reqData); err != nil {
		return fmt.Errorf("failed to write sync request to stream: %w", err)
	}

	// Read blocks from the peer's stream
	var totalSyncedBlocks int
	for {
		var block core.Block
		err := reader.ReadJSON(&block)
		if err != nil {
			if err == io.EOF {
				stdlog.Printf("Sync stream from %s closed by peer", peerID.String())
				break
			}
			// Check for EOF marker
			if err.Error() == "received EOF marker" {
				stdlog.Printf("Received end-of-sync marker from %s", peerID.String())
				break
			}
			return fmt.Errorf("error reading block data from stream: %w", err)
		}

		// Send block to blockchain core for processing
		m.BlockchainProcessCh <- Message{Type: ProcessBlock, Data: &block}
		totalSyncedBlocks++
	}
	stdlog.Printf("Successfully synced %d blocks from peer %s", totalSyncedBlocks, peerID.String())
	return nil
}

// SyncBlockchain synchronizes blockchain with all available peers
func (m *Manager) SyncBlockchain() error {
	stdlog.Println("Starting blockchain synchronization with libp2p peers...")

	peers := m.Host.Network().Peers()
	if len(peers) == 0 {
		stdlog.Println("No libp2p peers available for blockchain synchronization")
		return fmt.Errorf("no peers available for sync")
	}

	var wg sync.WaitGroup
	for _, peerID := range peers {
		wg.Add(1)
		go func(pID peer.ID) {
			defer wg.Done()
			if err := m.SyncBlockchainWithPeer(pID); err != nil {
				stdlog.Printf("Failed to sync with peer %s: %v", pID.String(), err)
			}
		}(peerID)
	}

	wg.Wait()
	stdlog.Println("Blockchain synchronization with libp2p peers completed")
	return nil
}

// JSON Stream Reader/Writer for protocol communication

type JSONStreamReader struct {
	decoder *json.Decoder
	reader  io.Reader
}

func NewJSONStreamReader(r io.Reader) *JSONStreamReader {
	return &JSONStreamReader{
		decoder: json.NewDecoder(r),
		reader:  r,
	}
}

// ReadJSON reads a JSON object from the stream
func (jsr *JSONStreamReader) ReadJSON(v interface{}) error {
	if err := jsr.decoder.Decode(v); err != nil {
		// Check for EOF marker
		var raw json.RawMessage
		if err := jsr.decoder.Decode(&raw); err == io.EOF && string(raw) == "\"EOF\"" {
			return fmt.Errorf("received EOF marker")
		}
		return err
	}
	return nil
}

type JSONStreamWriter struct {
	encoder *json.Encoder
	writer  io.Writer
}

func NewJSONStreamWriter(w io.Writer) *JSONStreamWriter {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return &JSONStreamWriter{
		encoder: encoder,
		writer:  w,
	}
}

// WriteJSON writes a JSON object to the stream
func (jsw *JSONStreamWriter) WriteJSON(v interface{}) error {
	return jsw.encoder.Encode(v)
}

// Write writes raw bytes to the stream
func (jsw *JSONStreamWriter) Write(data []byte) (int, error) {
	return jsw.writer.Write(data)
}
