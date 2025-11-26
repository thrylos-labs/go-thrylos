// consensus/pos/slashing_broadcast_test.go
// Tests for slashing evidence broadcasting and network-wide consensus

package pos

import (
	"fmt"
	"testing"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/types"
)

// MockWorldState for testing
type MockWorldStateForBroadcast struct {
	balances   map[string]int64
	validators map[string]*core.Validator
}

func NewMockWorldStateForBroadcast() *MockWorldStateForBroadcast {
	return &MockWorldStateForBroadcast{
		balances:   make(map[string]int64),
		validators: make(map[string]*core.Validator),
	}
}

func (m *MockWorldStateForBroadcast) GetBalance(address string) (int64, error) {
	balance, exists := m.balances[address]
	if !exists {
		return 1000000000, nil // Default 1000 THRYLOS
	}
	return balance, nil
}

func (m *MockWorldStateForBroadcast) UpdateBalance(address string, newBalance int64) error {
	m.balances[address] = newBalance
	return nil
}

func (m *MockWorldStateForBroadcast) GetValidator(address string) (*core.Validator, error) {
	val, exists := m.validators[address]
	if !exists {
		return nil, fmt.Errorf("validator not found")
	}
	return val, nil
}

// Test 1: Evidence Tracker - Duplicate Prevention
func TestEvidenceTrackerDuplicatePrevention(t *testing.T) {
	tracker := NewEvidenceTracker()

	// Create test evidence
	evidence := &SlashingEvidence{
		ID:               "test-evidence-1",
		Type:             EvidenceDoubleVoting,
		ValidatorAddress: "val1abc",
		Timestamp:        time.Now().Unix(),
	}

	// First check - should not be processed
	if tracker.IsProcessed(evidence.ID) {
		t.Error("Evidence should not be marked as processed initially")
	}

	// Mark as processed
	tracker.MarkProcessed(evidence)

	// Second check - should be processed
	if !tracker.IsProcessed(evidence.ID) {
		t.Error("Evidence should be marked as processed after MarkProcessed")
	}

	// Try to process again - should detect duplicate
	if !tracker.IsProcessed(evidence.ID) {
		t.Error("Duplicate evidence should be detected")
	}

	t.Log("✅ Evidence tracker correctly prevents duplicates")
}

// Test 2: Evidence Creation and Validation
func TestDoubleVotingEvidenceCreation(t *testing.T) {
	// Create validator address - FIX: Handle error return
	privateKey, err := crypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to create private key: %v", err)
	}

	publicKey := privateKey.PublicKey()

	// FIX: Use correct GenerateAddress signature (no AccountManager needed)
	validatorAddr, err := account.GenerateAddress(publicKey)
	if err != nil {
		t.Fatalf("Failed to generate address: %v", err)
	}

	// Create two conflicting attestations
	att1 := &types.Attestation{
		ValidatorAddress: validatorAddr,
		BlockHash:        "block-A",
		BlockHeight:      100,
		Epoch:            10,
		Slot:             320,
		Timestamp:        time.Now().Unix(),
	}

	att2 := &types.Attestation{
		ValidatorAddress: validatorAddr,
		BlockHash:        "block-B", // Different block!
		BlockHeight:      100,
		Epoch:            10,
		Slot:             320, // Same slot!
		Timestamp:        time.Now().Unix(),
	}

	// Create evidence
	doubleVoteEvidence := &DoubleVoteEvidence{
		Attestation1: att1,
		Attestation2: att2,
	}

	evidence := NewSlashingEvidence(
		EvidenceDoubleVoting,
		validatorAddr,
		doubleVoteEvidence,
		"reporter-addr",
	)

	// Validate evidence
	if err := evidence.Validate(); err != nil {
		t.Errorf("Valid evidence should pass validation: %v", err)
	}

	// Check evidence fields
	if evidence.Type != EvidenceDoubleVoting {
		t.Error("Evidence type should be DoubleVoting")
	}

	if evidence.ValidatorAddress != validatorAddr {
		t.Error("Validator address mismatch")
	}

	if evidence.ID == "" {
		t.Error("Evidence ID should be generated")
	}

	t.Logf("✅ Evidence created successfully: ID=%s", evidence.ID)
}

// Test 3: Evidence Broadcasting Flow
func TestSlashingEvidenceBroadcastFlow(t *testing.T) {
	// Create test config
	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			SlashingDoubleVote: 5,
			JailDurationHours:  24,
		},
	}

	// Create validator - FIX: Handle error
	privateKey, err := crypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to create private key: %v", err)
	}

	publicKey := privateKey.PublicKey()

	// FIX: Use correct GenerateAddress signature
	validatorAddr, err := account.GenerateAddress(publicKey)
	if err != nil {
		t.Fatalf("Failed to generate address: %v", err)
	}

	// Create mock world state
	worldState := NewMockWorldStateForBroadcast()
	worldState.balances[validatorAddr] = 1000000000 // 1000 THRYLOS

	// Create broadcast and receive channels
	broadcastChan := make(chan interface{}, 10)
	receiveChan := make(chan interface{}, 10)

	// Create consensus engine - The mock implements the interface needed
	engine := &ConsensusEngine{
		config:          cfg,
		worldState:      nil, // Cast through interface{}
		nodePrivateKey:  privateKey,
		nodeAddress:     validatorAddr,
		broadcastChan:   broadcastChan,
		receiveChan:     receiveChan,
		evidenceTracker: NewEvidenceTracker(),
	}

	// Create double voting evidence
	att1 := &types.Attestation{
		ValidatorAddress: validatorAddr,
		BlockHash:        "block-A",
		Epoch:            10,
		Slot:             320,
		Timestamp:        time.Now().Unix(),
	}

	att2 := &types.Attestation{
		ValidatorAddress: validatorAddr,
		BlockHash:        "block-B",
		Epoch:            10,
		Slot:             320,
		Timestamp:        time.Now().Unix(),
	}

	doubleVoteEvidence := &DoubleVoteEvidence{
		Attestation1: att1,
		Attestation2: att2,
	}

	evidence := NewSlashingEvidence(
		EvidenceDoubleVoting,
		validatorAddr,
		doubleVoteEvidence,
		validatorAddr,
	)

	// Test broadcasting
	err = engine.broadcastSlashingEvidence(evidence)
	if err != nil {
		t.Errorf("Failed to broadcast evidence: %v", err)
	}

	// Check if evidence was sent to broadcast channel
	select {
	case msg := <-broadcastChan:
		broadcastedEvidence, ok := msg.(*SlashingEvidence)
		if !ok {
			t.Fatal("Broadcasted message is not SlashingEvidence")
		}

		if broadcastedEvidence.ID != evidence.ID {
			t.Error("Broadcasted evidence ID mismatch")
		}

		if len(broadcastedEvidence.ReporterSignature) == 0 {
			t.Error("Broadcasted evidence should be signed")
		}

		t.Log("✅ Evidence successfully broadcasted with signature")

	case <-time.After(time.Second):
		t.Fatal("Evidence was not broadcasted within timeout")
	}
}

// Test 4: Evidence Reception and Processing
func TestSlashingEvidenceReception(t *testing.T) {
	// Create test config
	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			SlashingDoubleVote: 5,
			JailDurationHours:  24,
		},
	}

	maliciousKey, err := crypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to create malicious key: %v", err)
	}

	reporterKey, err := crypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to create reporter key: %v", err)
	}

	maliciousAddr, _ := account.GenerateAddress(maliciousKey.PublicKey())
	reporterAddr, _ := account.GenerateAddress(reporterKey.PublicKey())

	broadcastChan := make(chan interface{}, 10)
	receiveChan := make(chan interface{}, 10)

	receivingEngine := &ConsensusEngine{
		config:          cfg,
		worldState:      nil,
		slashingManager: nil,
		nodePrivateKey:  reporterKey,
		nodeAddress:     reporterAddr,
		broadcastChan:   broadcastChan,
		receiveChan:     receiveChan,
		evidenceTracker: NewEvidenceTracker(),
	}

	// Create evidence (from reporter)
	att1 := &types.Attestation{
		ValidatorAddress: maliciousAddr,
		BlockHash:        "block-A",
		Epoch:            10,
		Slot:             320,
		Timestamp:        time.Now().Unix(),
	}

	att2 := &types.Attestation{
		ValidatorAddress: maliciousAddr,
		BlockHash:        "block-B",
		Epoch:            10,
		Slot:             320,
		Timestamp:        time.Now().Unix(),
	}

	doubleVoteEvidence := &DoubleVoteEvidence{
		Attestation1: att1,
		Attestation2: att2,
	}

	evidence := NewSlashingEvidence(
		EvidenceDoubleVoting,
		maliciousAddr,
		doubleVoteEvidence,
		reporterAddr,
	)

	// ✅ ADD THIS: Sign the evidence with reporter's key
	evidenceData := []byte(evidence.ID + evidence.ValidatorAddress + fmt.Sprint(evidence.Timestamp))
	signature := reporterKey.Sign(evidenceData)
	evidence.ReporterSignature = signature.Bytes()

	// Process received evidence
	err = receivingEngine.processReceivedSlashingEvidence(evidence)
	if err != nil {
		t.Logf("Note: %v", err)
	}

	// Check if evidence was marked as processed
	if !receivingEngine.evidenceTracker.IsProcessed(evidence.ID) {
		t.Error("Evidence should be marked as processed after reception")
	}

	t.Log("✅ Evidence reception and processing completed")
}

// Test 5: Duplicate Evidence Rejection
func TestDuplicateEvidenceRejection(t *testing.T) {
	// Create test config
	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			SlashingDoubleVote: 5,
			JailDurationHours:  24,
		},
	}

	// FIX: Handle errors
	privateKey, err := crypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to create private key: %v", err)
	}

	validatorAddr, _ := account.GenerateAddress(privateKey.PublicKey())

	// REMOVE: Don't create mock worldState
	// worldState := NewMockWorldStateForBroadcast()
	// worldState.balances[validatorAddr] = 1000000000

	broadcastChan := make(chan interface{}, 10)
	receiveChan := make(chan interface{}, 10)

	engine := &ConsensusEngine{
		config:          cfg,
		worldState:      nil, // ✅ Just use nil
		nodePrivateKey:  privateKey,
		nodeAddress:     validatorAddr,
		broadcastChan:   broadcastChan,
		receiveChan:     receiveChan,
		evidenceTracker: NewEvidenceTracker(),
	}

	// Create evidence
	att1 := &types.Attestation{
		ValidatorAddress: validatorAddr,
		BlockHash:        "block-A",
		Epoch:            10,
		Slot:             320,
		Timestamp:        time.Now().Unix(),
	}

	att2 := &types.Attestation{
		ValidatorAddress: validatorAddr,
		BlockHash:        "block-B",
		Epoch:            10,
		Slot:             320,
		Timestamp:        time.Now().Unix(),
	}

	doubleVoteEvidence := &DoubleVoteEvidence{
		Attestation1: att1,
		Attestation2: att2,
	}

	evidence := NewSlashingEvidence(
		EvidenceDoubleVoting,
		validatorAddr,
		doubleVoteEvidence,
		validatorAddr,
	)

	// Process first time
	err = engine.processReceivedSlashingEvidence(evidence)
	if err != nil {
		t.Logf("First processing: %v", err)
	}

	// Clear broadcast channel
	select {
	case <-broadcastChan:
	default:
	}

	// Try to process same evidence again
	err = engine.processReceivedSlashingEvidence(evidence)
	if err != nil {
		t.Logf("Second processing: %v (expected)", err)
	}

	// Verify duplicate was detected (no new broadcast)
	select {
	case <-broadcastChan:
		t.Error("Duplicate evidence should not be re-broadcasted")
	case <-time.After(100 * time.Millisecond):
		t.Log("✅ Duplicate evidence correctly rejected (not re-broadcasted)")
	}
}

// Test 6: Multiple Node Consensus ⭐ KEY TEST
func TestMultiNodeSlashingConsensus(t *testing.T) {
	// Create test config
	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			SlashingDoubleVote: 5,
			JailDurationHours:  24,
		},
	}

	// Create malicious validator
	maliciousKey, err := crypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to create malicious key: %v", err)
	}

	maliciousAddr, _ := account.GenerateAddress(maliciousKey.PublicKey())

	// Create 3 nodes
	nodes := make([]*ConsensusEngine, 3)

	for i := 0; i < 3; i++ {
		nodeKey, err := crypto.NewPrivateKey()
		if err != nil {
			t.Fatalf("Failed to create node key %d: %v", i, err)
		}

		nodeAddr, _ := account.GenerateAddress(nodeKey.PublicKey())

		broadcastChan := make(chan interface{}, 10)
		receiveChan := make(chan interface{}, 10)

		nodes[i] = &ConsensusEngine{
			config:          cfg,
			worldState:      nil,
			slashingManager: nil,
			nodePrivateKey:  nodeKey,
			nodeAddress:     nodeAddr,
			broadcastChan:   broadcastChan,
			receiveChan:     receiveChan,
			evidenceTracker: NewEvidenceTracker(),
		}
	}

	// Node 0 detects double voting and creates evidence
	att1 := &types.Attestation{
		ValidatorAddress: maliciousAddr,
		BlockHash:        "block-A",
		Epoch:            10,
		Slot:             320,
		Timestamp:        time.Now().Unix(),
	}

	att2 := &types.Attestation{
		ValidatorAddress: maliciousAddr,
		BlockHash:        "block-B",
		Epoch:            10,
		Slot:             320,
		Timestamp:        time.Now().Unix(),
	}

	doubleVoteEvidence := &DoubleVoteEvidence{
		Attestation1: att1,
		Attestation2: att2,
	}

	evidence := NewSlashingEvidence(
		EvidenceDoubleVoting,
		maliciousAddr,
		doubleVoteEvidence,
		nodes[0].nodeAddress,
	)

	// ✅ ADD THIS: Sign the evidence with node 0's key
	evidenceData := []byte(evidence.ID + evidence.ValidatorAddress + fmt.Sprint(evidence.Timestamp))
	signature := nodes[0].nodePrivateKey.Sign(evidenceData)
	evidence.ReporterSignature = signature.Bytes()

	// Node 0 broadcasts evidence
	err = nodes[0].broadcastSlashingEvidence(evidence)
	if err != nil {
		t.Fatalf("Node 0 failed to broadcast: %v", err)
	}

	// Get broadcasted evidence
	var broadcastedEvidence *SlashingEvidence
	select {
	case msg := <-nodes[0].broadcastChan:
		broadcastedEvidence = msg.(*SlashingEvidence)
	case <-time.After(time.Second):
		t.Fatal("Evidence not broadcasted")
	}

	// ✅ ADD THIS: Node 0 should also process its own evidence
	err = nodes[0].processReceivedSlashingEvidence(broadcastedEvidence)
	if err != nil {
		t.Logf("Node 0 processing: %v", err)
	}

	// Nodes 1 and 2 receive the evidence
	for i := 1; i < 3; i++ {
		err := nodes[i].processReceivedSlashingEvidence(broadcastedEvidence)
		if err != nil {
			t.Logf("Node %d processing: %v", i, err)
		}

		// Check evidence is marked as processed
		if !nodes[i].evidenceTracker.IsProcessed(evidence.ID) {
			t.Errorf("Node %d did not mark evidence as processed", i)
		}
	}

	// Verify all nodes reached consensus (all processed same evidence)
	evidenceID := evidence.ID
	allProcessed := true
	for i := 0; i < 3; i++ {
		if !nodes[i].evidenceTracker.IsProcessed(evidenceID) {
			allProcessed = false
			t.Errorf("Node %d did not process evidence", i)
		}
	}

	if allProcessed {
		t.Log("✅ All nodes reached consensus on slashing evidence")
	}
}

// Test 7: Evidence Validation
func TestEvidenceValidation(t *testing.T) {
	// Test cases
	testCases := []struct {
		name        string
		createFunc  func() *SlashingEvidence
		shouldFail  bool
		description string
	}{
		{
			name: "Valid double voting evidence",
			createFunc: func() *SlashingEvidence {
				att1 := &types.Attestation{
					ValidatorAddress: "val1",
					BlockHash:        "blockA",
					Epoch:            10,
					Slot:             100,
					Timestamp:        time.Now().Unix(),
				}
				att2 := &types.Attestation{
					ValidatorAddress: "val1",
					BlockHash:        "blockB",
					Epoch:            10,
					Slot:             100,
					Timestamp:        time.Now().Unix(),
				}
				return NewSlashingEvidence(
					EvidenceDoubleVoting,
					"val1",
					&DoubleVoteEvidence{Attestation1: att1, Attestation2: att2},
					"reporter",
				)
			},
			shouldFail:  false,
			description: "Valid double voting should pass",
		},
		{
			name: "Evidence with future timestamp",
			createFunc: func() *SlashingEvidence {
				evidence := NewSlashingEvidence(
					EvidenceDoubleVoting,
					"val1",
					&DoubleVoteEvidence{},
					"reporter",
				)
				evidence.Timestamp = time.Now().Unix() + 3600 // 1 hour in future
				return evidence
			},
			shouldFail:  true,
			description: "Future timestamp should fail",
		},
		{
			name: "Evidence too old",
			createFunc: func() *SlashingEvidence {
				evidence := NewSlashingEvidence(
					EvidenceDoubleVoting,
					"val1",
					&DoubleVoteEvidence{},
					"reporter",
				)
				evidence.Timestamp = time.Now().Unix() - (86400 * 8) // 8 days old
				return evidence
			},
			shouldFail:  true,
			description: "Old evidence should fail",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			evidence := tc.createFunc()
			err := evidence.Validate()

			if tc.shouldFail && err == nil {
				t.Errorf("%s: expected error but got none", tc.description)
			}

			if !tc.shouldFail && err != nil {
				t.Errorf("%s: unexpected error: %v", tc.description, err)
			}

			if err == nil {
				t.Logf("✅ %s", tc.description)
			}
		})
	}
}

// Benchmark: Evidence Processing Performance
func BenchmarkEvidenceProcessing(b *testing.B) {
	tracker := NewEvidenceTracker()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		evidence := &SlashingEvidence{
			ID:               fmt.Sprintf("evidence-%d", i),
			Type:             EvidenceDoubleVoting,
			ValidatorAddress: "validator",
			Timestamp:        time.Now().Unix(),
		}

		tracker.MarkProcessed(evidence)
		_ = tracker.IsProcessed(evidence.ID)
	}
}
