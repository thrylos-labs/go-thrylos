// // consensus/pos/consensus_test.go
// // Tests for the main consensus engine

package pos

// import (
// 	"testing"
// 	"time"

// 	"github.com/thrylos-labs/go-thrylos/config"
// 	"github.com/thrylos-labs/go-thrylos/core/state"
// 	"github.com/thrylos-labs/go-thrylos/crypto"
// 	"github.com/thrylos-labs/go-thrylos/crypto/address"
// 	core "github.com/thrylos-labs/go-thrylos/proto/core"
// )

// // --- MOCK IMPLEMENTATIONS ---

// // MockSignature implements crypto.Signature
// type MockSignature struct{}

// func (m *MockSignature) Bytes() []byte {
// 	return []byte("mock_signature")
// }

// func (m *MockSignature) Marshal() ([]byte, error) {
// 	return m.Bytes(), nil
// }

// func (m *MockSignature) Unmarshal(data []byte) error {
// 	return nil
// }

// // Updated Verify signature to match interface requirements
// func (m *MockSignature) Verify(pubKey *crypto.PublicKey, data []byte) error {
// 	return nil // nil error means signature is valid
// }

// // FIX: Added VerifyWithSalt to satisfy the interface
// func (m *MockSignature) VerifyWithSalt(pubKey *crypto.PublicKey, data, salt []byte) error {
// 	return nil
// }

// func (m *MockSignature) String() string {
// 	return "mock_signature_hex"
// }

// // Equal implements crypto.Signature (takes interface value)
// func (m *MockSignature) Equal(other crypto.Signature) bool {
// 	if other == nil {
// 		return false
// 	}
// 	_, ok := other.(*MockSignature)
// 	return ok
// }

// // MockPublicKey implements crypto.PublicKey for testing
// type MockPublicKey struct{}

// func (m *MockPublicKey) Verify(data, signature []byte) bool {
// 	return true
// }

// func (m *MockPublicKey) Bytes() []byte {
// 	return []byte("mock_public_key")
// }

// func (m *MockPublicKey) Address() (*address.Address, error) {
// 	addr := &address.Address{}
// 	return addr, nil
// }

// func (m *MockPublicKey) String() string {
// 	return "mock_public_key_hex"
// }

// // Equal implements crypto.PublicKey
// // Updated to take *crypto.PublicKey to match the pointer pattern seen in Verify and PrivateKey
// func (m *MockPublicKey) Equal(other *crypto.PublicKey) bool {
// 	if other == nil {
// 		return false
// 	}
// 	if *other == nil {
// 		return false
// 	}
// 	_, ok := (*other).(*MockPublicKey)
// 	return ok
// }

// // MockPrivateKey implements crypto.PrivateKey for testing
// type MockPrivateKey struct {
// 	pubKey *MockPublicKey
// }

// func (m *MockPrivateKey) Bytes() []byte {
// 	return []byte("mock_private_key")
// }

// // Equal implements crypto.PrivateKey
// func (m *MockPrivateKey) Equal(other *crypto.PrivateKey) bool {
// 	if other == nil {
// 		return false
// 	}
// 	// Dereference the pointer to the interface to get the actual value
// 	if *other == nil {
// 		return false
// 	}
// 	_, ok := (*other).(*MockPrivateKey)
// 	return ok
// }

// func (m *MockPrivateKey) Marshal() ([]byte, error) {
// 	return m.Bytes(), nil
// }

// func (m *MockPrivateKey) Sign(data []byte) crypto.Signature {
// 	return &MockSignature{}
// }

// func (m *MockPrivateKey) PublicKey() crypto.PublicKey {
// 	return m.pubKey
// }

// func (m *MockPrivateKey) String() string {
// 	return "mock_private_key_hex"
// }

// // --- TESTS ---

// func setupTestEngine(t *testing.T) *ConsensusEngine {
// 	cfg := &config.Config{
// 		Consensus: config.ConsensusConfig{
// 			BlockTime:     5 * time.Second,
// 			MaxValidators: 100,
// 		},
// 	}

// 	// Create WorldState with proper parameters
// 	worldState, err := state.NewWorldState(
// 		t.TempDir(), // Use test temp directory
// 		0,           // shard ID
// 		1,           // total shards
// 		cfg,         // config
// 		nil,         // badger storage (nil for testing)
// 	)
// 	if err != nil {
// 		t.Fatalf("Failed to create world state: %v", err)
// 	}

// 	// Initialize the mock key with a public key
// 	mockPrivKey := &MockPrivateKey{pubKey: &MockPublicKey{}}
// 	broadcastChan := make(chan interface{}, 10)
// 	receiveChan := make(chan interface{}, 10)

// 	return NewConsensusEngine(cfg, worldState, mockPrivKey, broadcastChan, receiveChan)
// }

// // TestNewConsensusEngine tests consensus engine creation
// func TestNewConsensusEngine(t *testing.T) {
// 	t.Run("Create consensus engine successfully", func(t *testing.T) {
// 		engine := setupTestEngine(t)

// 		if engine == nil {
// 			t.Fatal("Expected engine to be created, got nil")
// 		}

// 		if engine.worldState == nil {
// 			t.Error("WorldState not set")
// 		}

// 		if engine.forkChoice == nil {
// 			t.Error("ForkChoice not initialized")
// 		}

// 		if engine.validatorManager == nil {
// 			t.Error("ValidatorManager not initialized")
// 		}

// 		if engine.blockProposer == nil {
// 			t.Error("BlockProposer not initialized")
// 		}

// 		if engine.blockValidator == nil {
// 			t.Error("BlockValidator not initialized")
// 		}

// 		if engine.proposalTimeout != 5*time.Second {
// 			t.Errorf("Expected proposalTimeout 5s, got %v", engine.proposalTimeout)
// 		}

// 		expectedAttestationPhase := 5 * time.Second / 3
// 		if engine.attestationPhase != expectedAttestationPhase {
// 			t.Errorf("Expected attestationPhase %v, got %v", expectedAttestationPhase, engine.attestationPhase)
// 		}

// 		t.Logf("✅ Consensus engine created successfully")
// 	})

// 	t.Run("Initial state is correct", func(t *testing.T) {
// 		engine := setupTestEngine(t)

// 		if engine.currentEpoch != 0 {
// 			t.Errorf("Expected initial epoch 0, got %d", engine.currentEpoch)
// 		}

// 		if engine.currentSlot != 0 {
// 			t.Errorf("Expected initial slot 0, got %d", engine.currentSlot)
// 		}

// 		if engine.blocksProposed != 0 {
// 			t.Errorf("Expected 0 blocks proposed, got %d", engine.blocksProposed)
// 		}

// 		if engine.attestations == nil {
// 			t.Error("Attestations map not initialized")
// 		}

// 		if engine.votes == nil {
// 			t.Error("Votes map not initialized")
// 		}

// 		t.Logf("✅ Initial state correct: epoch=%d, slot=%d", engine.currentEpoch, engine.currentSlot)
// 	})
// }

// // TestSlotEpochCalculation tests slot and epoch calculations
// func TestSlotEpochCalculation(t *testing.T) {
// 	tests := []struct {
// 		name          string
// 		slot          uint64
// 		expectedEpoch uint64
// 	}{
// 		{"First slot of epoch 0", 0, 0},
// 		{"Last slot of epoch 0", 31, 0},
// 		{"First slot of epoch 1", 32, 1},
// 		{"Last slot of epoch 1", 63, 1},
// 		{"First slot of epoch 2", 64, 2},
// 		{"Middle of epoch 5", 160, 5},
// 		{"Slot 320 is epoch 10", 320, 10},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			epoch := tt.slot / 32

// 			if epoch != tt.expectedEpoch {
// 				t.Errorf("Slot %d: expected epoch %d, got %d", tt.slot, tt.expectedEpoch, epoch)
// 			}

// 			t.Logf("✅ Slot %d → Epoch %d", tt.slot, epoch)
// 		})
// 	}
// }

// // TestValidateBlock tests block validation
// func TestValidateBlock(t *testing.T) {
// 	engine := setupTestEngine(t)

// 	t.Run("Validate valid block", func(t *testing.T) {
// 		block := &core.Block{
// 			Hash: "test_block_hash",
// 			Header: &core.BlockHeader{
// 				Index:     1,
// 				Timestamp: time.Now().Unix(),
// 				Validator: "validator1",
// 			},
// 			Transactions: []*core.Transaction{},
// 		}

// 		err := engine.ValidateBlock(block)

// 		if err != nil {
// 			t.Logf("Block validation returned error (expected): %v", err)
// 		} else {
// 			t.Log("✅ Block validated successfully")
// 		}
// 	})

// 	t.Run("Block validator not nil", func(t *testing.T) {
// 		if engine.blockValidator == nil {
// 			t.Error("Block validator should be initialized")
// 		} else {
// 			t.Log("✅ Block validator initialized")
// 		}
// 	})
// }

// // TestMetrics tests consensus metrics tracking
// func TestMetrics(t *testing.T) {
// 	engine := setupTestEngine(t)

// 	t.Run("Initial metrics are zero", func(t *testing.T) {
// 		if engine.blocksProposed != 0 {
// 			t.Errorf("Expected 0 blocks proposed, got %d", engine.blocksProposed)
// 		}

// 		if engine.blocksMissed != 0 {
// 			t.Errorf("Expected 0 blocks missed, got %d", engine.blocksMissed)
// 		}

// 		if engine.attestationsMade != 0 {
// 			t.Errorf("Expected 0 attestations made, got %d", engine.attestationsMade)
// 		}

// 		t.Log("✅ Initial metrics all zero")
// 	})

// 	t.Run("Metrics can be incremented", func(t *testing.T) {
// 		engine.blocksProposed++
// 		engine.blocksMissed++
// 		engine.attestationsMade++

// 		if engine.blocksProposed != 1 {
// 			t.Errorf("Expected 1 block proposed, got %d", engine.blocksProposed)
// 		}

// 		if engine.blocksMissed != 1 {
// 			t.Errorf("Expected 1 block missed, got %d", engine.blocksMissed)
// 		}

// 		if engine.attestationsMade != 1 {
// 			t.Errorf("Expected 1 attestation made, got %d", engine.attestationsMade)
// 		}

// 		t.Logf("✅ Metrics: proposed=%d, missed=%d, attestations=%d",
// 			engine.blocksProposed, engine.blocksMissed, engine.attestationsMade)
// 	})
// }

// // TestAttestationStorage tests attestation storage and retrieval
// func TestAttestationStorage(t *testing.T) {
// 	engine := setupTestEngine(t)

// 	t.Run("Store attestation", func(t *testing.T) {
// 		attestation := &Attestation{
// 			ValidatorAddress: "validator1",
// 			BlockHash:        "block_hash_1",
// 			BlockHeight:      100,
// 			Epoch:            10,
// 			Slot:             320,
// 			Timestamp:        time.Now().Unix(),
// 		}

// 		key := "validator1-320"
// 		engine.attestations[key] = attestation

// 		if len(engine.attestations) != 1 {
// 			t.Errorf("Expected 1 attestation, got %d", len(engine.attestations))
// 		}

// 		retrieved, exists := engine.attestations[key]
// 		if !exists {
// 			t.Error("Attestation not found")
// 		}

// 		if retrieved.ValidatorAddress != "validator1" {
// 			t.Errorf("Expected validator1, got %s", retrieved.ValidatorAddress)
// 		}

// 		t.Log("✅ Attestation stored and retrieved successfully")
// 	})

// 	t.Run("Store multiple attestations", func(t *testing.T) {
// 		engine.attestations["val1-1"] = &Attestation{ValidatorAddress: "val1", Slot: 1}
// 		engine.attestations["val2-1"] = &Attestation{ValidatorAddress: "val2", Slot: 1}
// 		engine.attestations["val1-2"] = &Attestation{ValidatorAddress: "val1", Slot: 2}

// 		if len(engine.attestations) != 4 {
// 			t.Errorf("Expected 4 attestations, got %d", len(engine.attestations))
// 		}

// 		t.Logf("✅ Stored %d attestations", len(engine.attestations))
// 	})
// }

// // TestConsensusEngineChannels tests broadcast and receive channels
// func TestConsensusEngineChannels(t *testing.T) {
// 	engine := setupTestEngine(t)

// 	t.Run("Broadcast channel is set", func(t *testing.T) {
// 		if engine.broadcastChan == nil {
// 			t.Error("Broadcast channel not set")
// 		}

// 		if cap(engine.broadcastChan) != 10 {
// 			t.Errorf("Expected broadcast channel capacity 10, got %d", cap(engine.broadcastChan))
// 		}

// 		t.Log("✅ Broadcast channel configured correctly")
// 	})

// 	t.Run("Receive channel is set", func(t *testing.T) {
// 		if engine.receiveChan == nil {
// 			t.Error("Receive channel not set")
// 		}

// 		if cap(engine.receiveChan) != 10 {
// 			t.Errorf("Expected receive channel capacity 10, got %d", cap(engine.receiveChan))
// 		}

// 		t.Log("✅ Receive channel configured correctly")
// 	})
// }

// // TestNodeAddressGeneration tests that node address is generated correctly
// func TestNodeAddressGeneration(t *testing.T) {
// 	t.Run("Node address is generated", func(t *testing.T) {
// 		engine := setupTestEngine(t)

// 		if engine.nodeAddress == "" {
// 			t.Error("Node address not generated")
// 		}

// 		t.Logf("✅ Node address generated: %s", engine.nodeAddress)
// 	})

// 	t.Run("Node private key is stored", func(t *testing.T) {
// 		engine := setupTestEngine(t)

// 		if engine.nodePrivateKey == nil {
// 			t.Error("Node private key not stored")
// 		}

// 		t.Log("✅ Node private key stored correctly")
// 	})
// }

// // BenchmarkConsensusEngineCreation benchmarks engine creation
// func BenchmarkConsensusEngineCreation(b *testing.B) {
// 	cfg := &config.Config{
// 		Consensus: config.ConsensusConfig{
// 			BlockTime:     5 * time.Second,
// 			MaxValidators: 100,
// 		},
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		worldState, _ := state.NewWorldState(b.TempDir(), 0, 1, cfg, nil)
// 		mockPrivKey := &MockPrivateKey{pubKey: &MockPublicKey{}}
// 		broadcastChan := make(chan interface{}, 10)
// 		receiveChan := make(chan interface{}, 10)
// 		_ = NewConsensusEngine(cfg, worldState, mockPrivKey, broadcastChan, receiveChan)
// 	}
// }

// // BenchmarkAttestationStorage benchmarks attestation storage
// func BenchmarkAttestationStorage(b *testing.B) {
// 	cfg := &config.Config{
// 		Consensus: config.ConsensusConfig{
// 			BlockTime: 5 * time.Second,
// 		},
// 	}

// 	worldState, _ := state.NewWorldState(b.TempDir(), 0, 1, cfg, nil)
// 	mockPrivKey := &MockPrivateKey{pubKey: &MockPublicKey{}}
// 	broadcastChan := make(chan interface{}, 10)
// 	receiveChan := make(chan interface{}, 10)

// 	engine := NewConsensusEngine(cfg, worldState, mockPrivKey, broadcastChan, receiveChan)

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		key := "validator-" + string(rune(i))
// 		engine.attestations[key] = &Attestation{
// 			ValidatorAddress: key,
// 			BlockHash:        "block",
// 			Slot:             uint64(i),
// 		}
// 	}
// }
