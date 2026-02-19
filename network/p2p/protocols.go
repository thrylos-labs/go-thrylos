package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	peerID := s.Conn().RemotePeer()

	// [SEC-FIX H-04] Rate Limiting
	if err := m.validator.CheckRateLimit(peerID); err != nil {
		stdlog.Printf("⚠️ Rate limit exceeded for %s (BlockSync): %v", peerID, err)
		return
	}
	defer m.validator.ReleaseRequest(peerID)

	// [SEC-FIX H-04] Stream Validation (Size limits & Timeouts)
	limitedReader, err := m.validator.ValidateStream(s)
	if err != nil {
		stdlog.Printf("⚠️ Stream validation failed for %s: %v", peerID, err)
		return
	}

	stdlog.Printf("Received block sync request from %s", peerID)

	reader := NewJSONStreamReader(limitedReader)
	writer := NewJSONStreamWriter(s)

	var reqData map[string]int64
	if err := reader.ReadJSON(&reqData); err != nil {
		stdlog.Printf("Error reading sync request from %s: %v", peerID, err)
		return
	}
	startHeight := reqData["startHeight"]
	stdlog.Printf("Peer %s requested blocks from height: %d", peerID, startHeight)

	// Send request to blockchain for blocks
	responseCh := make(chan Response)
	m.MessageBus <- Message{
		Type:       GetBlocksFromHeight,
		Data:       startHeight,
		ResponseCh: responseCh,
	}

	resp := <-responseCh
	if resp.Error != nil {
		stdlog.Printf("Error fetching blocks for sync with %s: %v", peerID, resp.Error)
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
	stdlog.Printf("Sent %d blocks to peer %s for sync", len(blocks), peerID)
}

// handleTransactionRequest handles incoming transaction requests
func (m *Manager) handleTransactionRequest(s network.Stream) {
	defer s.Close()
	peerID := s.Conn().RemotePeer()

	// [SEC-FIX H-04] Rate & Stream Validation
	if err := m.validator.CheckRateLimit(peerID); err != nil {
		return
	}
	defer m.validator.ReleaseRequest(peerID)

	limitedReader, err := m.validator.ValidateStream(s)
	if err != nil {
		return
	}

	stdlog.Printf("Received transaction from %s", peerID)

	reader := NewJSONStreamReader(limitedReader)
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
	peerID := s.Conn().RemotePeer()

	// [SEC-FIX H-04] Rate & Stream Validation
	if err := m.validator.CheckRateLimit(peerID); err != nil {
		return
	}
	defer m.validator.ReleaseRequest(peerID)

	limitedReader, err := m.validator.ValidateStream(s)
	if err != nil {
		return
	}

	stdlog.Printf("Received attestation from %s", peerID)

	reader := NewJSONStreamReader(limitedReader)
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
	peerID := s.Conn().RemotePeer()

	// [SEC-FIX H-04] Rate & Stream Validation
	if err := m.validator.CheckRateLimit(peerID); err != nil {
		return
	}
	defer m.validator.ReleaseRequest(peerID)

	limitedReader, err := m.validator.ValidateStream(s)
	if err != nil {
		return
	}

	stdlog.Printf("Received vote from %s", peerID)

	reader := NewJSONStreamReader(limitedReader)
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
	// Include the new validators topic
	topics := []string{
		TopicBlocks,
		TopicTransactions,
		TopicAttestations,
		TopicVotes,
		TopicValidators, // NEW: Add validator topic
	}

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
				stdlog.Printf("Context canceled for topic %s", topicName)
				return
			}
			stdlog.Printf("Error reading from topic %s: %v", topicName, err)
			continue
		}

		// Skip messages from self
		if msg.ReceivedFrom == m.Host.ID() {
			continue
		}

		stdlog.Printf("Received PubSub message from %s on topic %s", msg.ReceivedFrom, topicName)

		// Route based on topic
		switch topicName {
		case TopicBlocks:
			var block core.Block
			if err := json.Unmarshal(msg.Data, &block); err != nil {
				stdlog.Printf("Failed to unmarshal block: %v", err)
				continue
			}
			m.BlockchainProcessCh <- Message{
				Type:       ProcessBlock,
				Data:       &block,
				FromPeerID: msg.ReceivedFrom,
			}

		case TopicTransactions:
			var tx core.Transaction
			if err := json.Unmarshal(msg.Data, &tx); err != nil {
				stdlog.Printf("Failed to unmarshal transaction: %v", err)
				continue
			}
			m.BlockchainProcessCh <- Message{
				Type:       ProcessTransaction,
				Data:       &tx,
				FromPeerID: msg.ReceivedFrom,
			}

		case TopicAttestations:
			var attestation NetworkAttestation
			if err := json.Unmarshal(msg.Data, &attestation); err != nil {
				stdlog.Printf("Failed to unmarshal attestation: %v", err)
				continue
			}
			m.BlockchainProcessCh <- Message{
				Type:       ProcessAttestation,
				Data:       &attestation,
				FromPeerID: msg.ReceivedFrom,
			}

		case TopicVotes:
			var vote NetworkVote
			if err := json.Unmarshal(msg.Data, &vote); err != nil {
				stdlog.Printf("Failed to unmarshal vote: %v", err)
				continue
			}
			m.BlockchainProcessCh <- Message{
				Type:       ProcessVote,
				Data:       &vote,
				FromPeerID: msg.ReceivedFrom,
			}

		// NEW: Handle validator announcements
		case TopicValidators:
			stdlog.Printf("🔍 DEBUG: Raw validator message received, len=%d", len(msg.Data))
			var validator core.Validator
			if err := json.Unmarshal(msg.Data, &validator); err != nil {
				stdlog.Printf("Failed to unmarshal validator: %v", err)
				continue
			}
			stdlog.Printf("📢 Received validator announcement: %s from peer %s",
				validator.Address, msg.ReceivedFrom)
			select {
			case m.ValidatorChan <- &validator:
				stdlog.Printf("✅ Queued validator %s to ValidatorChan", validator.Address)
			default:
				stdlog.Printf("⚠️ ValidatorChan full, dropping announcement from %s", validator.Address)
			}

		default:
			stdlog.Printf("Unknown topic: %s", topicName)
		}
	}
}

// Broadcasting Functions

// BroadcastBlock broadcasts a block to all peers via PubSub
func (m *Manager) BroadcastBlock(block *core.Block) error {
	blockData, err := json.Marshal(block)
	if err != nil {
		return fmt.Errorf("failed to serialize block for PubSub: %w", err)
	}

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
