// consensus/pos/bridge_test.go
// Tests for the P2P <-> Consensus bridge

package pos

import (
	"testing"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/crypto/address"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
	"github.com/thrylos-labs/go-thrylos/types"
)

// MockPublicKey implements the PublicKey interface for testing
type MockPublicKey struct{}

func (m *MockPublicKey) String() string {
	return "mock_public_key"
}

func (m *MockPublicKey) Bytes() []byte {
	return []byte("mock_public_key_bytes")
}

func (m *MockPublicKey) Type() string {
	return "mock"
}

func (m *MockPublicKey) Equal(other *crypto.PublicKey) bool {
	if other == nil {
		return false
	}
	otherBytes := (*other).Bytes()
	return string(m.Bytes()) == string(otherBytes)
}

func (m *MockPublicKey) Verify(data []byte, signature *crypto.Signature) error {
	// Mock - always succeeds
	return nil
}

func (m *MockPublicKey) Address() (*address.Address, error) {
	// Create a simple mock address using FromBytes
	mockBytes := make([]byte, address.AddressByteLength)
	copy(mockBytes, []byte("mockadd"))
	return address.FromBytes(mockBytes)
}

func (m *MockPublicKey) Marshal() ([]byte, error) {
	return m.Bytes(), nil
}

func (m *MockPublicKey) Unmarshal(data []byte) error {
	return nil
}

// MockSignature implements a signature type for testing
type MockSignature struct {
	data []byte
}

func (m *MockSignature) Bytes() []byte {
	return m.data
}

func (m *MockSignature) Equal(other crypto.Signature) bool {
	if other == nil {
		return false
	}
	return string(m.data) == string(other.Bytes())
}

func (m *MockSignature) Verify(pubKey *crypto.PublicKey, data []byte) error {
	return nil
}

func (m *MockSignature) VerifyWithSalt(pubKey *crypto.PublicKey, data, salt []byte) error {
	return nil
}

func (m *MockSignature) String() string {
	return "mock_signature"
}

func (m *MockSignature) Marshal() ([]byte, error) {
	return m.data, nil
}

func (m *MockSignature) Unmarshal(data []byte) error {
	m.data = data
	return nil
}

// MockPrivateKey implements the PrivateKey interface for testing
type MockPrivateKey struct {
	pubKey *MockPublicKey
}

func (m *MockPrivateKey) PublicKey() crypto.PublicKey {
	return m.pubKey
}

func (m *MockPrivateKey) Sign(data []byte) crypto.Signature {
	return &MockSignature{data: []byte("mock_signature")}
}

func (m *MockPrivateKey) Bytes() []byte {
	return []byte("mock_private_key_bytes")
}

func (m *MockPrivateKey) String() string {
	return "mock_private_key"
}

func (m *MockPrivateKey) Equal(other *crypto.PrivateKey) bool {
	if other == nil {
		return false
	}
	otherBytes := (*other).Bytes()
	return string(m.Bytes()) == string(otherBytes)
}

func (m *MockPrivateKey) Marshal() ([]byte, error) {
	return m.Bytes(), nil
}

func (m *MockPrivateKey) Unmarshal(data []byte) error {
	// Mock implementation - just accept any data
	return nil
}

func (m *MockPrivateKey) Type() string {
	return "mock"
}

// MockP2PNetwork implements the P2PNetwork interface for testing
type MockP2PNetwork struct {
	blockChan       chan *core.Block
	attestationChan chan interface{}
	voteChan        chan interface{}

	broadcastedBlocks       []*core.Block
	broadcastedAttestations []interface{}
	broadcastedVotes        []interface{}

	connectedPeers int
}

func NewMockP2PNetwork() *MockP2PNetwork {
	return &MockP2PNetwork{
		blockChan:               make(chan *core.Block, 10),
		attestationChan:         make(chan interface{}, 10),
		voteChan:                make(chan interface{}, 10),
		broadcastedBlocks:       make([]*core.Block, 0),
		broadcastedAttestations: make([]interface{}, 0),
		broadcastedVotes:        make([]interface{}, 0),
		connectedPeers:          3, // Simulate 3 connected peers
	}
}

func (m *MockP2PNetwork) BroadcastBlock(block *core.Block) error {
	m.broadcastedBlocks = append(m.broadcastedBlocks, block)
	return nil
}

func (m *MockP2PNetwork) BroadcastAttestation(attestation interface{}) error {
	m.broadcastedAttestations = append(m.broadcastedAttestations, attestation)
	return nil
}

func (m *MockP2PNetwork) BroadcastVote(vote interface{}) error {
	m.broadcastedVotes = append(m.broadcastedVotes, vote)
	return nil
}

func (m *MockP2PNetwork) GetBlockChannel() <-chan *core.Block {
	return m.blockChan
}

func (m *MockP2PNetwork) GetAttestationChannel() <-chan interface{} {
	return m.attestationChan
}

func (m *MockP2PNetwork) GetVoteChannel() <-chan interface{} {
	return m.voteChan
}

func (m *MockP2PNetwork) GetConnectedPeers() int {
	return m.connectedPeers
}

func (m *MockP2PNetwork) IsConnected() bool {
	return m.connectedPeers > 0
}

func setupTestEngine(t *testing.T) *ConsensusEngine {
	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			BlockTime:     5 * time.Second,
			MaxValidators: 100,
		},
	}

	// Create BadgerStorage for testing
	tmpDir := t.TempDir()
	badgerStorage, err := storage.NewBadgerStorage(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create BadgerStorage: %v", err)
	}

	worldState, err := state.NewWorldState(
		tmpDir, // Test temp directory
		0,      // shard ID
		1,      // total shards
		cfg,
		badgerStorage, // badger storage
	)
	if err != nil {
		t.Fatalf("Failed to create world state: %v", err)
	}

	mockPrivKey := &MockPrivateKey{
		pubKey: &MockPublicKey{},
	}

	broadcastChan := make(chan interface{}, 100)
	receiveChan := make(chan interface{}, 100)

	return NewConsensusEngine(cfg, worldState, mockPrivKey, broadcastChan, receiveChan)
}

// setupTestEngineForBenchmark creates a test engine for benchmarking (uses a temp dir without testing.T)
func setupTestEngineForBenchmark(b *testing.B) *ConsensusEngine {
	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			BlockTime:     5 * time.Second,
			MaxValidators: 100,
		},
	}

	// Create BadgerStorage for testing
	tmpDir := b.TempDir()
	badgerStorage, err := storage.NewBadgerStorage(tmpDir)
	if err != nil {
		b.Fatalf("Failed to create BadgerStorage: %v", err)
	}

	worldState, err := state.NewWorldState(
		tmpDir, // Benchmark temp directory
		0,      // shard ID
		1,      // total shards
		cfg,
		badgerStorage, // badger storage
	)
	if err != nil {
		b.Fatalf("Failed to create world state: %v", err)
	}

	mockPrivKey := &MockPrivateKey{
		pubKey: &MockPublicKey{},
	}

	broadcastChan := make(chan interface{}, 100)
	receiveChan := make(chan interface{}, 100)

	return NewConsensusEngine(cfg, worldState, mockPrivKey, broadcastChan, receiveChan)
}

// TestBridgeCreation tests bridge initialization
func TestBridgeCreation(t *testing.T) {
	engine := setupTestEngine(t)
	mockNetwork := NewMockP2PNetwork()

	bridge := NewConsensusBridge(engine, mockNetwork)

	if bridge == nil {
		t.Fatal("Expected bridge to be created")
	}

	if bridge.consensus != engine {
		t.Error("Consensus engine not set correctly")
	}

	if bridge.network != mockNetwork {
		t.Error("Network not set correctly")
	}

	t.Log("✅ Bridge created successfully")
}

// TestBridgeForwardsConsensusToNetwork tests consensus → network forwarding
func TestBridgeForwardsConsensusToNetwork(t *testing.T) {
	engine := setupTestEngine(t)
	mockNetwork := NewMockP2PNetwork()

	bridge := NewConsensusBridge(engine, mockNetwork)
	if err := bridge.Start(); err != nil {
		t.Fatalf("Failed to start bridge: %v", err)
	}
	defer bridge.Stop()

	t.Run("Forward block proposal", func(t *testing.T) {
		block := &core.Block{
			Hash: "test_block_hash",
			Header: &core.BlockHeader{
				Index:     1,
				Timestamp: time.Now().Unix(),
				Validator: "validator1",
			},
		}

		proposal := &BlockProposal{
			Block:    block,
			Proposer: "validator1",
			Slot:     1,
			Epoch:    0,
		}

		// Send to consensus broadcast channel
		engine.broadcastChan <- proposal

		// Wait for bridge to forward
		time.Sleep(100 * time.Millisecond)

		// Check network received it
		if len(mockNetwork.broadcastedBlocks) != 1 {
			t.Errorf("Expected 1 block broadcast, got %d", len(mockNetwork.broadcastedBlocks))
		}

		if mockNetwork.broadcastedBlocks[0].Hash != "test_block_hash" {
			t.Error("Block not forwarded correctly")
		}

		t.Log("✅ Block proposal forwarded to network")
	})

	t.Run("Forward attestation", func(t *testing.T) {
		attestation := &types.Attestation{
			ValidatorAddress: "validator1",
			BlockHash:        "block_hash",
			Epoch:            1,
			Slot:             32,
		}

		// Send to consensus broadcast channel
		engine.broadcastChan <- attestation

		// Wait for bridge to forward
		time.Sleep(100 * time.Millisecond)

		// Check network received it
		if len(mockNetwork.broadcastedAttestations) != 1 {
			t.Errorf("Expected 1 attestation broadcast, got %d", len(mockNetwork.broadcastedAttestations))
		}

		t.Log("✅ Attestation forwarded to network")
	})
}

// TestBridgeForwardsNetworkToConsensus tests network → consensus forwarding
func TestBridgeForwardsNetworkToConsensus(t *testing.T) {
	engine := setupTestEngine(t)
	mockNetwork := NewMockP2PNetwork()

	bridge := NewConsensusBridge(engine, mockNetwork)
	if err := bridge.Start(); err != nil {
		t.Fatalf("Failed to start bridge: %v", err)
	}
	defer bridge.Stop()

	t.Run("Forward block from network", func(t *testing.T) {
		block := &core.Block{
			Hash: "network_block",
			Header: &core.BlockHeader{
				Index:     10,
				Timestamp: time.Now().Unix(),
				Validator: "validator2",
			},
		}

		// Simulate network receiving block
		mockNetwork.blockChan <- block

		// Wait for bridge to forward
		time.Sleep(100 * time.Millisecond)

		// Check consensus received it (via receiveChan)
		select {
		case msg := <-engine.receiveChan:
			if proposal, ok := msg.(*BlockProposal); ok {
				if proposal.Block.Hash != "network_block" {
					t.Error("Wrong block forwarded")
				}
				t.Log("✅ Block from network forwarded to consensus")
			} else {
				t.Error("Expected BlockProposal type")
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("Block not forwarded to consensus")
		}
	})

	t.Run("Forward attestation from network", func(t *testing.T) {
		attestation := &types.Attestation{
			ValidatorAddress: "validator3",
			BlockHash:        "network_block_hash",
			Epoch:            5,
			Slot:             160,
		}

		// Simulate network receiving attestation
		mockNetwork.attestationChan <- attestation

		// Wait for bridge to forward
		time.Sleep(100 * time.Millisecond)

		// Check consensus received it
		select {
		case msg := <-engine.receiveChan:
			if att, ok := msg.(*types.Attestation); ok {
				if att.ValidatorAddress != "validator3" {
					t.Error("Wrong attestation forwarded")
				}
				t.Log("✅ Attestation from network forwarded to consensus")
			} else {
				t.Error("Expected Attestation type")
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("Attestation not forwarded to consensus")
		}
	})
}

// TestBridgeStats tests bridge statistics
func TestBridgeStats(t *testing.T) {
	engine := setupTestEngine(t)
	mockNetwork := NewMockP2PNetwork()

	bridge := NewConsensusBridge(engine, mockNetwork)

	stats := bridge.GetStats()

	if stats == nil {
		t.Fatal("Expected stats to be returned")
	}

	connectedPeers, ok := stats["connected_peers"].(int)
	if !ok || connectedPeers != 3 {
		t.Errorf("Expected 3 connected peers, got %v", stats["connected_peers"])
	}

	isConnected, ok := stats["is_connected"].(bool)
	if !ok || !isConnected {
		t.Error("Expected network to be connected")
	}

	t.Logf("✅ Bridge stats: %d peers connected", connectedPeers)
}

// TestBridgeStopGracefully tests graceful shutdown
func TestBridgeStopGracefully(t *testing.T) {
	engine := setupTestEngine(t)
	mockNetwork := NewMockP2PNetwork()

	bridge := NewConsensusBridge(engine, mockNetwork)

	if err := bridge.Start(); err != nil {
		t.Fatalf("Failed to start bridge: %v", err)
	}

	// Send some messages
	engine.broadcastChan <- &types.Attestation{
		ValidatorAddress: "test",
		BlockHash:        "test",
	}

	time.Sleep(50 * time.Millisecond)

	// Stop bridge
	if err := bridge.Stop(); err != nil {
		t.Errorf("Failed to stop bridge: %v", err)
	}

	t.Log("✅ Bridge stopped gracefully")
}

// BenchmarkBridgeForwarding benchmarks message forwarding
func BenchmarkBridgeForwarding(b *testing.B) {
	engine := setupTestEngineForBenchmark(b)
	mockNetwork := NewMockP2PNetwork()

	bridge := NewConsensusBridge(engine, mockNetwork)
	bridge.Start()
	defer bridge.Stop()

	attestation := &types.Attestation{
		ValidatorAddress: "benchmark_validator",
		BlockHash:        "benchmark_block",
		Epoch:            1,
		Slot:             32,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.broadcastChan <- attestation
	}
}
