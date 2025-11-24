// // consensus/pos/critical_fixes_test.go
// // Tests for signature verification and chain traversal fixes

package pos

// import (
// 	"fmt"
// 	"testing"
// 	"time"

// 	"github.com/thrylos-labs/go-thrylos/crypto"
// 	core "github.com/thrylos-labs/go-thrylos/proto/core"
// 	"golang.org/x/crypto/blake2b"
// )

// // ============================================================================
// // MOCK WORLD STATE FOR TESTING
// // ============================================================================

// // MockWorldState implements the minimal interface needed for testing
// type MockWorldState struct {
// 	validators map[string]*core.Validator
// 	blocks     map[string]*core.Block
// }

// func NewMockWorldState() *MockWorldState {
// 	return &MockWorldState{
// 		validators: make(map[string]*core.Validator),
// 		blocks:     make(map[string]*core.Block),
// 	}
// }

// func (m *MockWorldState) GetValidator(address string) (*core.Validator, error) {
// 	if v, ok := m.validators[address]; ok {
// 		return v, nil
// 	}
// 	return nil, fmt.Errorf("validator %s not found", address)
// }

// func (m *MockWorldState) GetBlockByHash(hash string) (*core.Block, error) {
// 	if b, ok := m.blocks[hash]; ok {
// 		return b, nil
// 	}
// 	return nil, fmt.Errorf("block %s not found", hash)
// }

// func (m *MockWorldState) AddValidator(address string, pubKey []byte) {
// 	m.validators[address] = &core.Validator{
// 		Address: address,
// 		Pubkey:  pubKey,
// 		Stake:   1000000,
// 		Active:  true,
// 	}
// }

// func (m *MockWorldState) AddBlock(block *core.Block) {
// 	m.blocks[block.Hash] = block
// }

// // ============================================================================
// // TEST HELPERS
// // ============================================================================

// // createTestConsensusEngine creates a minimal consensus engine for testing
// func createTestConsensusEngine(t *testing.T) (*ConsensusEngine, crypto.PrivateKey) {
// 	privateKey := crypto.GeneratePrivateKey()
// 	mockWS := NewMockWorldState()

// 	// Add validator with public key
// 	pubKey := privateKey.PublicKey()
// 	mockWS.AddValidator("test-validator", pubKey.Bytes())

// 	ce := &ConsensusEngine{
// 		nodePrivateKey: privateKey,
// 		nodeAddress:    "test-validator",
// 		worldState:     mockWS,
// 		chainCache:     NewChainCache(),
// 	}

// 	return ce, privateKey
// }

// // ============================================================================
// // SIGNATURE VERIFICATION TESTS
// // ============================================================================

// func TestAttestationSignatureValid(t *testing.T) {
// 	ce, privateKey := createTestConsensusEngine(t)

// 	// Create attestation
// 	attestation := &Attestation{
// 		ValidatorAddress: "test-validator",
// 		BlockHash:        "block-hash-123",
// 		BlockHeight:      100,
// 		Epoch:            3,
// 		Slot:             96,
// 		Timestamp:        time.Now().Unix(),
// 	}

// 	// Sign attestation
// 	data := fmt.Sprintf("%s%s%d%d%d%d",
// 		attestation.ValidatorAddress,
// 		attestation.BlockHash,
// 		attestation.BlockHeight,
// 		attestation.Epoch,
// 		attestation.Slot,
// 		attestation.Timestamp)
// 	hash := blake2b.Sum256([]byte(data))
// 	signature := privateKey.Sign(hash[:])
// 	attestation.Signature = signature.Bytes()

// 	// Verify signature
// 	err := ce.verifyAttestationSignature(attestation)
// 	if err != nil {
// 		t.Fatalf("Valid signature failed verification: %v", err)
// 	}

// 	t.Log("✅ Valid attestation signature verified successfully")
// }

// func TestAttestationSignatureInvalid(t *testing.T) {
// 	ce, _ := createTestConsensusEngine(t)

// 	// Create attestation with WRONG signature
// 	wrongKey := crypto.GeneratePrivateKey()
// 	attestation := &Attestation{
// 		ValidatorAddress: "test-validator",
// 		BlockHash:        "block-hash-123",
// 		BlockHeight:      100,
// 		Epoch:            3,
// 		Slot:             96,
// 		Timestamp:        time.Now().Unix(),
// 	}

// 	// Sign with wrong key
// 	data := fmt.Sprintf("%s%s%d%d%d%d",
// 		attestation.ValidatorAddress,
// 		attestation.BlockHash,
// 		attestation.BlockHeight,
// 		attestation.Epoch,
// 		attestation.Slot,
// 		attestation.Timestamp)
// 	hash := blake2b.Sum256([]byte(data))
// 	wrongSignature := wrongKey.Sign(hash[:])
// 	attestation.Signature = wrongSignature.Bytes()

// 	// Verification should fail
// 	err := ce.verifyAttestationSignature(attestation)
// 	if err == nil {
// 		t.Fatal("Invalid signature should have failed verification")
// 	}

// 	t.Logf("✅ Invalid signature correctly rejected: %v", err)
// }

// func TestAttestationSignatureMissing(t *testing.T) {
// 	ce, _ := createTestConsensusEngine(t)

// 	// Attestation with no signature
// 	attestation := &Attestation{
// 		ValidatorAddress: "test-validator",
// 		BlockHash:        "block-hash-123",
// 		BlockHeight:      100,
// 		Epoch:            3,
// 		Slot:             96,
// 		Timestamp:        time.Now().Unix(),
// 		Signature:        nil,
// 	}

// 	// Should fail
// 	err := ce.verifyAttestationSignature(attestation)
// 	if err == nil {
// 		t.Fatal("Missing signature should have failed verification")
// 	}

// 	t.Logf("✅ Missing signature correctly rejected: %v", err)
// }

// func TestAttestationSignatureUnknownValidator(t *testing.T) {
// 	ce, _ := createTestConsensusEngine(t)

// 	// Attestation from unknown validator
// 	attestation := &Attestation{
// 		ValidatorAddress: "unknown-validator",
// 		BlockHash:        "block-hash-123",
// 		BlockHeight:      100,
// 		Epoch:            3,
// 		Slot:             96,
// 		Timestamp:        time.Now().Unix(),
// 		Signature:        make([]byte, 64), // Random signature
// 	}

// 	// Should fail - validator not found
// 	err := ce.verifyAttestationSignature(attestation)
// 	if err == nil {
// 		t.Fatal("Unknown validator should have failed verification")
// 	}

// 	t.Logf("✅ Unknown validator correctly rejected: %v", err)
// }

// func TestProposalSignatureValid(t *testing.T) {
// 	ce, privateKey := createTestConsensusEngine(t)

// 	// Create block
// 	block := &core.Block{
// 		Hash: "block-hash-456",
// 		Header: &core.BlockHeader{
// 			Index:     100,
// 			PrevHash:  "prev-hash",
// 			Timestamp: time.Now().Unix(),
// 		},
// 	}

// 	// Create proposal
// 	proposal := &BlockProposal{
// 		Block:    block,
// 		Proposer: "test-validator",
// 		Slot:     96,
// 		Epoch:    3,
// 	}

// 	// Sign proposal
// 	proposalData := fmt.Sprintf("%s%s%d%d",
// 		proposal.Block.Hash,
// 		proposal.Proposer,
// 		proposal.Slot,
// 		proposal.Epoch)
// 	proposalHash := blake2b.Sum256([]byte(proposalData))
// 	signature := privateKey.Sign(proposalHash[:])
// 	proposal.Signature = signature.Bytes()

// 	// Verify signature
// 	err := ce.verifyProposalSignature(proposal)
// 	if err != nil {
// 		t.Fatalf("Valid proposal signature failed verification: %v", err)
// 	}

// 	t.Log("✅ Valid proposal signature verified successfully")
// }

// func TestProposalSignatureInvalid(t *testing.T) {
// 	ce, _ := createTestConsensusEngine(t)
// 	wrongKey := crypto.GeneratePrivateKey()

// 	block := &core.Block{
// 		Hash: "block-hash-456",
// 		Header: &core.BlockHeader{
// 			Index:     100,
// 			PrevHash:  "prev-hash",
// 			Timestamp: time.Now().Unix(),
// 		},
// 	}

// 	proposal := &BlockProposal{
// 		Block:    block,
// 		Proposer: "test-validator",
// 		Slot:     96,
// 		Epoch:    3,
// 	}

// 	// Sign with wrong key
// 	proposalData := fmt.Sprintf("%s%s%d%d",
// 		proposal.Block.Hash,
// 		proposal.Proposer,
// 		proposal.Slot,
// 		proposal.Epoch)
// 	proposalHash := blake2b.Sum256([]byte(proposalData))
// 	wrongSignature := wrongKey.Sign(proposalHash[:])
// 	proposal.Signature = wrongSignature.Bytes()

// 	// Should fail
// 	err := ce.verifyProposalSignature(proposal)
// 	if err == nil {
// 		t.Fatal("Invalid proposal signature should have failed verification")
// 	}

// 	t.Logf("✅ Invalid proposal signature correctly rejected: %v", err)
// }

// func TestSignBlockProposal(t *testing.T) {
// 	ce, _ := createTestConsensusEngine(t)

// 	block := &core.Block{
// 		Hash: "block-hash-789",
// 		Header: &core.BlockHeader{
// 			Index:     100,
// 			PrevHash:  "prev-hash",
// 			Timestamp: time.Now().Unix(),
// 		},
// 	}

// 	proposal := &BlockProposal{
// 		Block:     block,
// 		Proposer:  "test-validator",
// 		Slot:      96,
// 		Epoch:     3,
// 		Signature: nil,
// 	}

// 	// Sign the proposal
// 	err := ce.signBlockProposal(proposal)
// 	if err != nil {
// 		t.Fatalf("Failed to sign proposal: %v", err)
// 	}

// 	// Signature should now be set
// 	if proposal.Signature == nil {
// 		t.Fatal("Signature should be set after signing")
// 	}

// 	// Verify the signature we just created
// 	err = ce.verifyProposalSignature(proposal)
// 	if err != nil {
// 		t.Fatalf("Self-signed proposal failed verification: %v", err)
// 	}

// 	t.Log("✅ Block proposal signed and verified successfully")
// }

// // ============================================================================
// // CHAIN TRAVERSAL TESTS
// // ============================================================================

// func TestIsDescendantSelf(t *testing.T) {
// 	ce, _ := createTestConsensusEngine(t)

// 	// Same block is trivially a descendant
// 	if !ce.isDescendant("block-1", "block-1") {
// 		t.Error("Block should be descendant of itself")
// 	}

// 	t.Log("✅ Self-descendant check passed")
// }

// func TestIsDescendantDirect(t *testing.T) {
// 	ce, _ := createTestConsensusEngine(t)
// 	mockWS := ce.worldState.(*MockWorldState)

// 	// Create chain: genesis <- block-1
// 	genesis := &core.Block{
// 		Hash: "genesis",
// 		Header: &core.BlockHeader{
// 			Index:    0,
// 			PrevHash: "",
// 		},
// 	}
// 	block1 := &core.Block{
// 		Hash: "block-1",
// 		Header: &core.BlockHeader{
// 			Index:    1,
// 			PrevHash: "genesis",
// 		},
// 	}

// 	mockWS.AddBlock(genesis)
// 	mockWS.AddBlock(block1)

// 	// block-1 should be descendant of genesis
// 	if !ce.isDescendant("block-1", "genesis") {
// 		t.Error("block-1 should be descendant of genesis")
// 	}

// 	// genesis should NOT be descendant of block-1
// 	if ce.isDescendant("genesis", "block-1") {
// 		t.Error("genesis should NOT be descendant of block-1")
// 	}

// 	t.Log("✅ Direct descendant check passed")
// }

// func TestIsDescendantMultiLevel(t *testing.T) {
// 	ce, _ := createTestConsensusEngine(t)
// 	mockWS := ce.worldState.(*MockWorldState)

// 	// Create chain: genesis <- block-1 <- block-2 <- block-3
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "genesis",
// 		Header: &core.BlockHeader{Index: 0, PrevHash: ""},
// 	})
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "block-1",
// 		Header: &core.BlockHeader{Index: 1, PrevHash: "genesis"},
// 	})
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "block-2",
// 		Header: &core.BlockHeader{Index: 2, PrevHash: "block-1"},
// 	})
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "block-3",
// 		Header: &core.BlockHeader{Index: 3, PrevHash: "block-2"},
// 	})

// 	// Test multi-level descent
// 	tests := []struct {
// 		descendant string
// 		ancestor   string
// 		expected   bool
// 	}{
// 		{"block-3", "genesis", true},
// 		{"block-3", "block-1", true},
// 		{"block-2", "genesis", true},
// 		{"block-1", "block-2", false},
// 		{"genesis", "block-3", false},
// 	}

// 	for _, tt := range tests {
// 		result := ce.isDescendant(tt.descendant, tt.ancestor)
// 		if result != tt.expected {
// 			t.Errorf("isDescendant(%s, %s) = %v, want %v",
// 				tt.descendant, tt.ancestor, result, tt.expected)
// 		}
// 	}

// 	t.Log("✅ Multi-level descendant check passed")
// }

// func TestIsDescendantFork(t *testing.T) {
// 	ce, _ := createTestConsensusEngine(t)
// 	mockWS := ce.worldState.(*MockWorldState)

// 	// Create fork:
// 	// genesis <- block-1 <- block-2a
// 	//                   \<- block-2b
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "genesis",
// 		Header: &core.BlockHeader{Index: 0, PrevHash: ""},
// 	})
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "block-1",
// 		Header: &core.BlockHeader{Index: 1, PrevHash: "genesis"},
// 	})
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "block-2a",
// 		Header: &core.BlockHeader{Index: 2, PrevHash: "block-1"},
// 	})
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "block-2b",
// 		Header: &core.BlockHeader{Index: 2, PrevHash: "block-1"},
// 	})

// 	// Both 2a and 2b are descendants of block-1
// 	if !ce.isDescendant("block-2a", "block-1") {
// 		t.Error("block-2a should be descendant of block-1")
// 	}
// 	if !ce.isDescendant("block-2b", "block-1") {
// 		t.Error("block-2b should be descendant of block-1")
// 	}

// 	// But they're not descendants of each other
// 	if ce.isDescendant("block-2a", "block-2b") {
// 		t.Error("block-2a should NOT be descendant of block-2b")
// 	}
// 	if ce.isDescendant("block-2b", "block-2a") {
// 		t.Error("block-2b should NOT be descendant of block-2a")
// 	}

// 	t.Log("✅ Fork descendant check passed")
// }

// func TestIsDescendantMissingBlock(t *testing.T) {
// 	ce, _ := createTestConsensusEngine(t)
// 	mockWS := ce.worldState.(*MockWorldState)

// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "genesis",
// 		Header: &core.BlockHeader{Index: 0, PrevHash: ""},
// 	})

// 	// Query for non-existent block
// 	if ce.isDescendant("non-existent", "genesis") {
// 		t.Error("Non-existent block should not be descendant")
// 	}

// 	t.Log("✅ Missing block check passed")
// }

// func TestChainCache(t *testing.T) {
// 	ce, _ := createTestConsensusEngine(t)
// 	mockWS := ce.worldState.(*MockWorldState)

// 	// Create simple chain
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "genesis",
// 		Header: &core.BlockHeader{Index: 0, PrevHash: ""},
// 	})
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "block-1",
// 		Header: &core.BlockHeader{Index: 1, PrevHash: "genesis"},
// 	})

// 	// First call - not cached
// 	result1 := ce.isDescendant("block-1", "genesis")
// 	if !result1 {
// 		t.Fatal("Expected true")
// 	}

// 	// Second call - should use cache
// 	result2 := ce.isDescendant("block-1", "genesis")
// 	if !result2 {
// 		t.Fatal("Expected true from cache")
// 	}

// 	// Verify cache has the entry
// 	cached, found := ce.chainCache.Get("genesis", "block-1")
// 	if !found {
// 		t.Error("Result should be cached")
// 	}
// 	if !cached {
// 		t.Error("Cached result should be true")
// 	}

// 	t.Log("✅ Chain cache working correctly")
// }

// func TestIsForkDetected(t *testing.T) {
// 	ce, _ := createTestConsensusEngine(t)
// 	mockWS := ce.worldState.(*MockWorldState)

// 	// Create blocks at same height
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "block-2a",
// 		Header: &core.BlockHeader{Index: 2, PrevHash: "block-1"},
// 	})
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "block-2b",
// 		Header: &core.BlockHeader{Index: 2, PrevHash: "block-1"},
// 	})

// 	// Should detect fork
// 	if !ce.isForkDetected("block-2a", "block-2b") {
// 		t.Error("Should detect fork at same height")
// 	}

// 	// Same block not a fork
// 	if ce.isForkDetected("block-2a", "block-2a") {
// 		t.Error("Same block should not be detected as fork")
// 	}

// 	t.Log("✅ Fork detection working correctly")
// }

// func TestGetCommonAncestor(t *testing.T) {
// 	ce, _ := createTestConsensusEngine(t)
// 	mockWS := ce.worldState.(*MockWorldState)

// 	// Create fork:
// 	// genesis <- block-1 <- block-2a <- block-3a
// 	//                   \<- block-2b <- block-3b
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "genesis",
// 		Header: &core.BlockHeader{Index: 0, PrevHash: ""},
// 	})
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "block-1",
// 		Header: &core.BlockHeader{Index: 1, PrevHash: "genesis"},
// 	})
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "block-2a",
// 		Header: &core.BlockHeader{Index: 2, PrevHash: "block-1"},
// 	})
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "block-2b",
// 		Header: &core.BlockHeader{Index: 2, PrevHash: "block-1"},
// 	})
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "block-3a",
// 		Header: &core.BlockHeader{Index: 3, PrevHash: "block-2a"},
// 	})
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "block-3b",
// 		Header: &core.BlockHeader{Index: 3, PrevHash: "block-2b"},
// 	})

// 	// Common ancestor of 3a and 3b should be block-1
// 	common, err := ce.getCommonAncestor("block-3a", "block-3b")
// 	if err != nil {
// 		t.Fatalf("Failed to find common ancestor: %v", err)
// 	}
// 	if common != "block-1" {
// 		t.Errorf("Expected block-1, got %s", common)
// 	}

// 	t.Log("✅ Common ancestor detection working correctly")
// }

// // ============================================================================
// // PERFORMANCE BENCHMARKS
// // ============================================================================

// func BenchmarkSignatureVerification(b *testing.B) {
// 	ce, privateKey := createTestConsensusEngine(&testing.T{})

// 	attestation := &Attestation{
// 		ValidatorAddress: "test-validator",
// 		BlockHash:        "block-hash",
// 		BlockHeight:      100,
// 		Epoch:            3,
// 		Slot:             96,
// 		Timestamp:        time.Now().Unix(),
// 	}

// 	data := fmt.Sprintf("%s%s%d%d%d%d",
// 		attestation.ValidatorAddress,
// 		attestation.BlockHash,
// 		attestation.BlockHeight,
// 		attestation.Epoch,
// 		attestation.Slot,
// 		attestation.Timestamp)
// 	hash := blake2b.Sum256([]byte(data))
// 	signature := privateKey.Sign(hash[:])
// 	attestation.Signature = signature.Bytes()

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		_ = ce.verifyAttestationSignature(attestation)
// 	}
// }

// func BenchmarkChainTraversal(b *testing.B) {
// 	ce, _ := createTestConsensusEngine(&testing.T{})
// 	mockWS := ce.worldState.(*MockWorldState)

// 	// Create chain of 100 blocks
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "block-0",
// 		Header: &core.BlockHeader{Index: 0, PrevHash: ""},
// 	})

// 	for i := 1; i <= 100; i++ {
// 		mockWS.AddBlock(&core.Block{
// 			Hash: fmt.Sprintf("block-%d", i),
// 			Header: &core.BlockHeader{
// 				Index:    int64(i),
// 				PrevHash: fmt.Sprintf("block-%d", i-1),
// 			},
// 		})
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		_ = ce.isDescendant("block-100", "block-0")
// 	}
// }

// func BenchmarkChainTraversalCached(b *testing.B) {
// 	ce, _ := createTestConsensusEngine(&testing.T{})
// 	mockWS := ce.worldState.(*MockWorldState)

// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "genesis",
// 		Header: &core.BlockHeader{Index: 0, PrevHash: ""},
// 	})
// 	mockWS.AddBlock(&core.Block{
// 		Hash:   "block-1",
// 		Header: &core.BlockHeader{Index: 1, PrevHash: "genesis"},
// 	})

// 	// Prime the cache
// 	_ = ce.isDescendant("block-1", "genesis")

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		_ = ce.isDescendant("block-1", "genesis")
// 	}
// }
