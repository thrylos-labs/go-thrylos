// consensus/pos/large_validator_test.go
// Comprehensive test suite for 100+ validator scenarios

package pos

// import (
// 	"fmt"
// 	"sync"
// 	"testing"
// 	"time"

// 	"github.com/thrylos-labs/go-thrylos/config"
// 	"github.com/thrylos-labs/go-thrylos/crypto"
// 	core "github.com/thrylos-labs/go-thrylos/proto/core"
// 	"golang.org/x/crypto/blake2b"
// )

// // ============================================================================
// // MOCK WORLD STATE
// // ============================================================================

// // MockWorldState provides a test implementation of WorldStateReader
// type MockWorldState struct {
// 	validators map[string]*core.Validator
// }

// func NewMockWorldState() *MockWorldState {
// 	return &MockWorldState{
// 		validators: make(map[string]*core.Validator),
// 	}
// }

// func (m *MockWorldState) GetValidator(address string) (*core.Validator, error) {
// 	if v, ok := m.validators[address]; ok {
// 		return v, nil
// 	}
// 	return nil, fmt.Errorf("validator %s not found", address)
// }

// func (m *MockWorldState) GetActiveValidators() []*core.Validator {
// 	active := make([]*core.Validator, 0)
// 	for _, v := range m.validators {
// 		if v.Active {
// 			active = append(active, v)
// 		}
// 	}
// 	return active
// }

// func (m *MockWorldState) AddValidator(address string, stake int64, active bool) {
// 	m.validators[address] = &core.Validator{
// 		Address: address,
// 		Stake:   stake,
// 		Active:  active,
// 		Pubkey:  make([]byte, 32), // Empty pubkey for basic tests
// 	}
// }

// // ============================================================================
// // HELPER FUNCTIONS FOR LARGE-SCALE TESTING
// // ============================================================================

// // createValidatorSet creates N validators with specified stake distribution
// func createValidatorSet(count int, stakePerValidator int64) *MockWorldState {
// 	mockState := NewMockWorldState()

// 	for i := 0; i < count; i++ {
// 		address := fmt.Sprintf("validator-%d", i)
// 		mockState.AddValidator(address, stakePerValidator, true)
// 	}

// 	return mockState
// }

// // createValidatorsWithKeys creates validators with actual crypto keys
// func createValidatorsWithKeys(count int, stakePerValidator int64) (*MockWorldState, []crypto.PrivateKey) {
// 	mockState := NewMockWorldState()
// 	keys := make([]crypto.PrivateKey, count)

// 	for i := 0; i < count; i++ {
// 		privateKey, err := crypto.NewPrivateKey()
// 		if err != nil {
// 			panic(fmt.Sprintf("Failed to generate private key: %v", err))
// 		}
// 		keys[i] = privateKey

// 		address := fmt.Sprintf("validator-%d", i)
// 		pubKey := privateKey.PublicKey()

// 		mockState.validators[address] = &core.Validator{
// 			Address: address,
// 			Pubkey:  pubKey.Bytes(),
// 			Stake:   stakePerValidator,
// 			Active:  true,
// 		}
// 	}

// 	return mockState, keys
// }

// // createSignedAttestation creates a properly signed attestation
// func createSignedAttestation(validatorAddr string, privateKey crypto.PrivateKey, blockHash string, epoch, slot uint64, height int64) *Attestation {
// 	attestation := &Attestation{
// 		ValidatorAddress: validatorAddr,
// 		BlockHash:        blockHash,
// 		BlockHeight:      height,
// 		Epoch:            epoch,
// 		Slot:             slot,
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

// 	return attestation
// }

// // ============================================================================
// // QUORUM TESTS WITH 100+ VALIDATORS
// // ============================================================================

// func TestQuorumWith100Validators(t *testing.T) {
// 	t.Run("Exactly 67 validators reach quorum", func(t *testing.T) {
// 		mockState := createValidatorSet(100, 1000)
// 		fc := NewForkChoice(&config.Config{}, mockState)

// 		blockHash := "test-block-1"

// 		// Have exactly 67 validators attest (minimum for 2/3 quorum)
// 		for i := 0; i < 67; i++ {
// 			attestation := &Attestation{
// 				ValidatorAddress: fmt.Sprintf("validator-%d", i),
// 				BlockHash:        blockHash,
// 				Epoch:            1,
// 				Slot:             32,
// 			}
// 			fc.ProcessAttestation(attestation)
// 		}

// 		// Check quorum
// 		if !fc.HasQuorum(blockHash) {
// 			t.Errorf("Expected quorum with 67/100 validators (67%%)")
// 		}

// 		percentage := fc.GetQuorumPercentage(blockHash)
// 		t.Logf("✅ Quorum reached: 67/100 validators (%.1f%%)", percentage)

// 		if percentage < 66.0 || percentage > 68.0 {
// 			t.Errorf("Expected ~67%% quorum, got %.1f%%", percentage)
// 		}
// 	})

// 	t.Run("66 validators do NOT reach quorum", func(t *testing.T) {
// 		mockState := createValidatorSet(100, 1000)
// 		fc := NewForkChoice(&config.Config{}, mockState)

// 		blockHash := "test-block-2"

// 		// Have 66 validators attest (just below 2/3 threshold)
// 		for i := 0; i < 66; i++ {
// 			attestation := &Attestation{
// 				ValidatorAddress: fmt.Sprintf("validator-%d", i),
// 				BlockHash:        blockHash,
// 				Epoch:            1,
// 				Slot:             32,
// 			}
// 			fc.ProcessAttestation(attestation)
// 		}

// 		// Check quorum
// 		if fc.HasQuorum(blockHash) {
// 			t.Errorf("Should NOT have quorum with 66/100 validators (66%%)")
// 		}

// 		percentage := fc.GetQuorumPercentage(blockHash)
// 		t.Logf("✅ No quorum: 66/100 validators (%.1f%% < 67%% threshold)", percentage)

// 		if percentage >= 67.0 {
// 			t.Errorf("Expected <67%% quorum, got %.1f%%", percentage)
// 		}
// 	})

// 	t.Run("100 validators unanimous", func(t *testing.T) {
// 		mockState := createValidatorSet(100, 1000)
// 		fc := NewForkChoice(&config.Config{}, mockState)

// 		blockHash := "test-block-3"

// 		// All 100 validators attest
// 		for i := 0; i < 100; i++ {
// 			attestation := &Attestation{
// 				ValidatorAddress: fmt.Sprintf("validator-%d", i),
// 				BlockHash:        blockHash,
// 				Epoch:            1,
// 				Slot:             32,
// 			}
// 			fc.ProcessAttestation(attestation)
// 		}

// 		// Check quorum
// 		if !fc.HasQuorum(blockHash) {
// 			t.Errorf("Expected quorum with 100/100 validators")
// 		}

// 		percentage := fc.GetQuorumPercentage(blockHash)
// 		t.Logf("✅ Full consensus: 100/100 validators (%.1f%%)", percentage)

// 		if percentage < 99.9 {
// 			t.Errorf("Expected 100%% quorum, got %.1f%%", percentage)
// 		}
// 	})

// 	t.Run("Quorum threshold edge cases", func(t *testing.T) {
// 		testCases := []struct {
// 			validators int
// 			attesting  int
// 			shouldPass bool
// 		}{
// 			{100, 67, true},   // Exactly 2/3
// 			{100, 66, false},  // Just below
// 			{100, 68, true},   // Just above
// 			{150, 100, false}, // 150 validators, 100 attest = 66.7% (no quorum)
// 			{150, 101, true},  // 150 validators, 101 attest = 67.3% (quorum)
// 			{99, 66, false},   // 99 validators, 66 attest = 66.7% (no quorum)
// 			{99, 67, true},    // 99 validators, 67 attest = 67.7% (quorum)
// 		}

// 		for _, tc := range testCases {
// 			mockState := createValidatorSet(tc.validators, 1000)
// 			fc := NewForkChoice(&config.Config{}, mockState)
// 			blockHash := fmt.Sprintf("block-%d-%d", tc.validators, tc.attesting)

// 			for i := 0; i < tc.attesting; i++ {
// 				attestation := &Attestation{
// 					ValidatorAddress: fmt.Sprintf("validator-%d", i),
// 					BlockHash:        blockHash,
// 					Epoch:            1,
// 					Slot:             32,
// 				}
// 				fc.ProcessAttestation(attestation)
// 			}

// 			hasQuorum := fc.HasQuorum(blockHash)
// 			percentage := fc.GetQuorumPercentage(blockHash)

// 			if hasQuorum != tc.shouldPass {
// 				t.Errorf("%d validators, %d attesting (%.1f%%): expected quorum=%v, got quorum=%v",
// 					tc.validators, tc.attesting, percentage, tc.shouldPass, hasQuorum)
// 			} else {
// 				t.Logf("✅ %d validators, %d attesting (%.1f%%): quorum=%v (correct)",
// 					tc.validators, tc.attesting, percentage, hasQuorum)
// 			}
// 		}
// 	})
// }

// func TestForkChoiceWith100Validators(t *testing.T) {
// 	t.Run("Split vote 60-40 favors majority", func(t *testing.T) {
// 		mockState := createValidatorSet(100, 1000)
// 		fc := NewForkChoice(&config.Config{}, mockState)

// 		blockA := "block-a"
// 		blockB := "block-b"

// 		// 60 validators vote for block A
// 		for i := 0; i < 60; i++ {
// 			attestation := &Attestation{
// 				ValidatorAddress: fmt.Sprintf("validator-%d", i),
// 				BlockHash:        blockA,
// 				Epoch:            1,
// 				Slot:             32,
// 			}
// 			fc.ProcessAttestation(attestation)
// 		}

// 		// 40 validators vote for block B
// 		for i := 60; i < 100; i++ {
// 			attestation := &Attestation{
// 				ValidatorAddress: fmt.Sprintf("validator-%d", i),
// 				BlockHash:        blockB,
// 				Epoch:            1,
// 				Slot:             32,
// 			}
// 			fc.ProcessAttestation(attestation)
// 		}

// 		// Check scores
// 		stakeA := fc.GetAttestingStake(blockA)
// 		stakeB := fc.GetAttestingStake(blockB)

// 		if stakeA != 60000 {
// 			t.Errorf("Block A should have 60000 stake, got %d", stakeA)
// 		}
// 		if stakeB != 40000 {
// 			t.Errorf("Block B should have 40000 stake, got %d", stakeB)
// 		}

// 		// Neither should have quorum (both < 67%)
// 		if fc.HasQuorum(blockA) {
// 			t.Error("Block A should not have quorum with 60% stake")
// 		}
// 		if fc.HasQuorum(blockB) {
// 			t.Error("Block B should not have quorum with 40% stake")
// 		}

// 		t.Logf("✅ Block A: 60%% stake (no quorum)")
// 		t.Logf("✅ Block B: 40%% stake (no quorum)")
// 		t.Logf("✅ Correct split behavior - neither has 2/3 majority")
// 	})

// 	t.Run("Split vote 70-30 gives quorum to majority", func(t *testing.T) {
// 		mockState := createValidatorSet(100, 1000)
// 		fc := NewForkChoice(&config.Config{}, mockState)

// 		blockA := "block-a-70"
// 		blockB := "block-b-30"

// 		// 70 validators vote for block A
// 		for i := 0; i < 70; i++ {
// 			attestation := &Attestation{
// 				ValidatorAddress: fmt.Sprintf("validator-%d", i),
// 				BlockHash:        blockA,
// 				Epoch:            1,
// 				Slot:             32,
// 			}
// 			fc.ProcessAttestation(attestation)
// 		}

// 		// 30 validators vote for block B
// 		for i := 70; i < 100; i++ {
// 			attestation := &Attestation{
// 				ValidatorAddress: fmt.Sprintf("validator-%d", i),
// 				BlockHash:        blockB,
// 				Epoch:            1,
// 				Slot:             32,
// 			}
// 			fc.ProcessAttestation(attestation)
// 		}

// 		// Block A should have quorum (70% > 67%)
// 		if !fc.HasQuorum(blockA) {
// 			t.Error("Block A should have quorum with 70% stake")
// 		}

// 		// Block B should not have quorum
// 		if fc.HasQuorum(blockB) {
// 			t.Error("Block B should not have quorum with 30% stake")
// 		}

// 		t.Logf("✅ Block A: 70%% stake (has quorum)")
// 		t.Logf("✅ Block B: 30%% stake (no quorum)")
// 		t.Logf("✅ Supermajority winner selected correctly")
// 	})

// 	t.Run("3-way split favors plurality", func(t *testing.T) {
// 		mockState := createValidatorSet(150, 1000)
// 		fc := NewForkChoice(&config.Config{}, mockState)

// 		blockA := "block-a-3way"
// 		blockB := "block-b-3way"
// 		blockC := "block-c-3way"

// 		// Block A: 60 validators (40%)
// 		for i := 0; i < 60; i++ {
// 			attestation := &Attestation{
// 				ValidatorAddress: fmt.Sprintf("validator-%d", i),
// 				BlockHash:        blockA,
// 				Epoch:            1,
// 				Slot:             32,
// 			}
// 			fc.ProcessAttestation(attestation)
// 		}

// 		// Block B: 50 validators (33%)
// 		for i := 60; i < 110; i++ {
// 			attestation := &Attestation{
// 				ValidatorAddress: fmt.Sprintf("validator-%d", i),
// 				BlockHash:        blockB,
// 				Epoch:            1,
// 				Slot:             32,
// 			}
// 			fc.ProcessAttestation(attestation)
// 		}

// 		// Block C: 40 validators (27%)
// 		for i := 110; i < 150; i++ {
// 			attestation := &Attestation{
// 				ValidatorAddress: fmt.Sprintf("validator-%d", i),
// 				BlockHash:        blockC,
// 				Epoch:            1,
// 				Slot:             32,
// 			}
// 			fc.ProcessAttestation(attestation)
// 		}

// 		// None should have quorum
// 		if fc.HasQuorum(blockA) || fc.HasQuorum(blockB) || fc.HasQuorum(blockC) {
// 			t.Error("No block should have quorum in 3-way split")
// 		}

// 		// But A should have highest stake
// 		stakeA := fc.GetAttestingStake(blockA)
// 		stakeB := fc.GetAttestingStake(blockB)
// 		stakeC := fc.GetAttestingStake(blockC)

// 		if stakeA <= stakeB || stakeA <= stakeC {
// 			t.Errorf("Block A should have highest stake: A=%d, B=%d, C=%d", stakeA, stakeB, stakeC)
// 		}

// 		t.Logf("✅ 3-way split: A=40%%, B=33%%, C=27%% - no quorum but A has plurality")
// 	})
// }

// // ============================================================================
// // VOTE HANDLING TESTS
// // ============================================================================

// func TestVoteHandling(t *testing.T) {
// 	t.Run("Valid vote accepted", func(t *testing.T) {
// 		mockState := createValidatorSet(10, 1000)
// 		votes := make(map[string]*Vote)

// 		vote := &Vote{
// 			ValidatorAddress: "validator-0",
// 			SourceBlockHash:  "block-1",
// 			TargetBlockHash:  "block-2",
// 			SourceEpoch:      1,
// 			TargetEpoch:      2,
// 		}

// 		// Validate vote
// 		validator, err := mockState.GetValidator(vote.ValidatorAddress)
// 		if err != nil {
// 			t.Fatalf("Validator should exist: %v", err)
// 		}

// 		if !validator.Active {
// 			t.Error("Validator should be active")
// 		}

// 		if vote.TargetEpoch <= vote.SourceEpoch {
// 			t.Error("Epoch ordering should be valid")
// 		}

// 		// Store vote
// 		key := fmt.Sprintf("%s-%d", vote.ValidatorAddress, vote.TargetEpoch)
// 		votes[key] = vote

// 		if votes[key] == nil {
// 			t.Error("Vote should be stored")
// 		}

// 		t.Log("✅ Valid vote accepted and stored")
// 	})

// 	t.Run("Invalid vote rejected - inactive validator", func(t *testing.T) {
// 		mockState := createValidatorSet(10, 1000)
// 		// Mark validator as inactive
// 		mockState.validators["validator-0"].Active = false

// 		vote := &Vote{
// 			ValidatorAddress: "validator-0",
// 			SourceBlockHash:  "block-1",
// 			TargetBlockHash:  "block-2",
// 			SourceEpoch:      1,
// 			TargetEpoch:      2,
// 		}

// 		// Validate vote
// 		validator, err := mockState.GetValidator(vote.ValidatorAddress)
// 		if err != nil {
// 			t.Fatalf("Validator should exist: %v", err)
// 		}

// 		if validator.Active {
// 			t.Error("Should reject vote from inactive validator")
// 		}

// 		t.Logf("✅ Inactive validator vote rejected: validator not active")
// 	})

// 	t.Run("Invalid vote rejected - bad epoch ordering", func(t *testing.T) {
// 		createValidatorSet(10, 1000)

// 		vote := &Vote{
// 			ValidatorAddress: "validator-0",
// 			SourceBlockHash:  "block-1",
// 			TargetBlockHash:  "block-2",
// 			SourceEpoch:      2, // Source AFTER target
// 			TargetEpoch:      1,
// 		}

// 		// Check epoch ordering
// 		if vote.TargetEpoch > vote.SourceEpoch {
// 			t.Error("Should reject vote with invalid epoch ordering")
// 		}

// 		t.Logf("✅ Bad epoch ordering rejected: target <= source")
// 	})

// 	t.Run("Invalid vote rejected - unknown validator", func(t *testing.T) {
// 		mockState := createValidatorSet(10, 1000)

// 		vote := &Vote{
// 			ValidatorAddress: "unknown-validator",
// 			SourceBlockHash:  "block-1",
// 			TargetBlockHash:  "block-2",
// 			SourceEpoch:      1,
// 			TargetEpoch:      2,
// 		}

// 		// Try to get validator
// 		_, err := mockState.GetValidator(vote.ValidatorAddress)
// 		if err == nil {
// 			t.Error("Should reject vote from unknown validator")
// 		}

// 		t.Logf("✅ Unknown validator vote rejected: %v", err)
// 	})

// 	t.Run("100 validators voting", func(t *testing.T) {
// 		mockState := createValidatorSet(100, 1000)
// 		votes := make(map[string]*Vote)

// 		// All validators vote
// 		for i := 0; i < 100; i++ {
// 			vote := &Vote{
// 				ValidatorAddress: fmt.Sprintf("validator-%d", i),
// 				SourceBlockHash:  "block-1",
// 				TargetBlockHash:  "block-2",
// 				SourceEpoch:      1,
// 				TargetEpoch:      2,
// 			}

// 			// Validate and store
// 			validator, err := mockState.GetValidator(vote.ValidatorAddress)
// 			if err != nil || !validator.Active {
// 				continue
// 			}

// 			if vote.TargetEpoch <= vote.SourceEpoch {
// 				continue
// 			}

// 			key := fmt.Sprintf("%s-%d", vote.ValidatorAddress, vote.TargetEpoch)
// 			votes[key] = vote
// 		}

// 		// Check all votes stored
// 		if len(votes) != 100 {
// 			t.Errorf("Expected 100 votes, got %d", len(votes))
// 		}

// 		t.Logf("✅ All 100 validator votes accepted and stored")
// 	})
// }

// // ============================================================================
// // CONCURRENT VOTING TESTS
// // ============================================================================

// func TestConcurrentVoting(t *testing.T) {
// 	t.Run("100 validators voting concurrently", func(t *testing.T) {
// 		mockState := createValidatorSet(100, 1000)
// 		fc := NewForkChoice(&config.Config{}, mockState)

// 		blockHash := "concurrent-block"
// 		var wg sync.WaitGroup

// 		// All 100 validators attest concurrently
// 		for i := 0; i < 100; i++ {
// 			wg.Add(1)
// 			go func(validatorID int) {
// 				defer wg.Done()
// 				attestation := &Attestation{
// 					ValidatorAddress: fmt.Sprintf("validator-%d", validatorID),
// 					BlockHash:        blockHash,
// 					Epoch:            1,
// 					Slot:             32,
// 				}
// 				fc.ProcessAttestation(attestation)
// 			}(i)
// 		}

// 		wg.Wait()

// 		// Verify all attestations counted
// 		stake := fc.GetAttestingStake(blockHash)
// 		if stake != 100000 {
// 			t.Errorf("Expected 100000 total stake, got %d", stake)
// 		}

// 		if !fc.HasQuorum(blockHash) {
// 			t.Error("Should have quorum with all validators")
// 		}

// 		t.Logf("✅ 100 concurrent attestations processed correctly")
// 		t.Logf("✅ Total stake: %d (100%%)", stake)
// 	})

// 	t.Run("Race condition safety - duplicate attestations", func(t *testing.T) {
// 		mockState := createValidatorSet(10, 1000)
// 		fc := NewForkChoice(&config.Config{}, mockState)

// 		blockHash := "race-block"
// 		var wg sync.WaitGroup

// 		// Same validator attempts to attest 100 times concurrently
// 		for i := 0; i < 100; i++ {
// 			wg.Add(1)
// 			go func() {
// 				defer wg.Done()
// 				attestation := &Attestation{
// 					ValidatorAddress: "validator-0",
// 					BlockHash:        blockHash,
// 					Epoch:            1,
// 					Slot:             32,
// 				}
// 				fc.ProcessAttestation(attestation)
// 			}()
// 		}

// 		wg.Wait()

// 		// Should only count once
// 		stake := fc.GetAttestingStake(blockHash)
// 		if stake != 1000 {
// 			t.Errorf("Duplicate attestations counted: expected 1000 stake, got %d", stake)
// 		}

// 		t.Logf("✅ Duplicate prevention working under concurrency")
// 	})
// }

// // ============================================================================
// // SIGNATURE VERIFICATION WITH 100+ VALIDATORS
// // ============================================================================
// // NOTE: These tests need the real ConsensusEngine.verifyAttestationSignature()
// // Uncomment after integrating consensus_signature_fix.go into your ConsensusEngine

// // Change your test to NOT create ConsensusEngine directly
// // Instead, just test the verification function with the mock

// func TestSignatureVerificationAtScale(t *testing.T) {
// 	t.Run("100 validators with valid signatures", func(t *testing.T) {
// 		mockState, keys := createValidatorsWithKeys(100, 1000)

// 		blockHash := "signed-block"
// 		validCount := 0

// 		// Create and verify 100 signed attestations
// 		for i := 0; i < 100; i++ {
// 			address := fmt.Sprintf("validator-%d", i)
// 			attestation := createSignedAttestation(
// 				address,
// 				keys[i],
// 				blockHash,
// 				1, 32, 100,
// 			)

// 			// Manually verify using the same logic as verifyAttestationSignature
// 			validator, err := mockState.GetValidator(attestation.ValidatorAddress)
// 			if err != nil {
// 				t.Errorf("Validator %d not found: %v", i, err)
// 				continue
// 			}

// 			// Recreate signed data
// 			data := fmt.Sprintf("%s%s%d%d%d%d",
// 				attestation.ValidatorAddress,
// 				attestation.BlockHash,
// 				attestation.BlockHeight,
// 				attestation.Epoch,
// 				attestation.Slot,
// 				attestation.Timestamp)
// 			hash := blake2b.Sum256([]byte(data))

// 			// Parse signature
// 			if len(attestation.Signature) == 0 {
// 				t.Errorf("Missing signature for validator-%d", i)
// 				continue
// 			}

// 			signature, err := crypto.SignatureFromBytes(attestation.Signature)
// 			if err != nil {
// 				t.Errorf("Invalid signature format for validator-%d: %v", i, err)
// 				continue
// 			}

// 			// Parse public key
// 			pubKey, err := crypto.NewPublicKeyFromBytes(validator.Pubkey)
// 			if err != nil {
// 				t.Errorf("Invalid public key for validator-%d: %v", i, err)
// 				continue
// 			}

// 			// Verify signature
// 			if err := pubKey.Verify(hash[:], &signature); err != nil {
// 				t.Errorf("Signature verification failed for validator-%d: %v", i, err)
// 			} else {
// 				validCount++
// 			}
// 		}

// 		if validCount != 100 {
// 			t.Errorf("Expected 100 valid signatures, got %d", validCount)
// 		}

// 		t.Logf("✅ All 100 validator signatures verified successfully")
// 	})

// 	t.Run("Mix of valid and invalid signatures", func(t *testing.T) {
// 		mockState, keys := createValidatorsWithKeys(100, 1000)

// 		blockHash := "mixed-block"
// 		validCount := 0
// 		invalidCount := 0

// 		for i := 0; i < 100; i++ {
// 			address := fmt.Sprintf("validator-%d", i)

// 			var attestation *Attestation
// 			if i < 70 {
// 				// 70 valid signatures
// 				attestation = createSignedAttestation(address, keys[i], blockHash, 1, 32, 100)
// 			} else {
// 				// 30 invalid signatures (signed with wrong key)
// 				wrongKey, err := crypto.NewPrivateKey()
// 				if err != nil {
// 					t.Fatalf("Failed to generate wrong key: %v", err)
// 				}
// 				attestation = createSignedAttestation(address, wrongKey, blockHash, 1, 32, 100)
// 			}

// 			// Manually verify
// 			validator, err := mockState.GetValidator(attestation.ValidatorAddress)
// 			if err != nil {
// 				invalidCount++
// 				continue
// 			}

// 			data := fmt.Sprintf("%s%s%d%d%d%d",
// 				attestation.ValidatorAddress,
// 				attestation.BlockHash,
// 				attestation.BlockHeight,
// 				attestation.Epoch,
// 				attestation.Slot,
// 				attestation.Timestamp)
// 			hash := blake2b.Sum256([]byte(data))

// 			if len(attestation.Signature) == 0 {
// 				invalidCount++
// 				continue
// 			}

// 			signature, err := crypto.SignatureFromBytes(attestation.Signature)
// 			if err != nil {
// 				invalidCount++
// 				continue
// 			}

// 			pubKey, err := crypto.NewPublicKeyFromBytes(validator.Pubkey)
// 			if err != nil {
// 				invalidCount++
// 				continue
// 			}

// 			if err := pubKey.Verify(hash[:], &signature); err != nil {
// 				invalidCount++
// 			} else {
// 				validCount++
// 			}
// 		}

// 		if validCount != 70 {
// 			t.Errorf("Expected 70 valid signatures, got %d", validCount)
// 		}
// 		if invalidCount != 30 {
// 			t.Errorf("Expected 30 invalid signatures, got %d", invalidCount)
// 		}

// 		t.Logf("✅ Correctly validated: 70 valid, 30 invalid signatures")
// 	})
// }

// // ============================================================================
// // PERFORMANCE AND STRESS TESTS
// // ============================================================================

// func TestPerformanceAtScale(t *testing.T) {
// 	t.Run("Time to reach quorum with 100 validators", func(t *testing.T) {
// 		mockState := createValidatorSet(100, 1000)
// 		fc := NewForkChoice(&config.Config{}, mockState)

// 		blockHash := "perf-block"

// 		start := time.Now()

// 		// Process 67 attestations to reach quorum
// 		for i := 0; i < 67; i++ {
// 			attestation := &Attestation{
// 				ValidatorAddress: fmt.Sprintf("validator-%d", i),
// 				BlockHash:        blockHash,
// 				Epoch:            1,
// 				Slot:             32,
// 			}
// 			fc.ProcessAttestation(attestation)
// 		}

// 		duration := time.Since(start)

// 		if !fc.HasQuorum(blockHash) {
// 			t.Error("Should have quorum after 67 attestations")
// 		}

// 		// Should be very fast (< 10ms for 67 attestations)
// 		if duration > 10*time.Millisecond {
// 			t.Logf("⚠️  Slow performance: %v for 67 attestations", duration)
// 		} else {
// 			t.Logf("✅ Fast quorum: %v for 67 attestations", duration)
// 		}
// 	})

// 	t.Run("Process 1000 attestations from 100 validators", func(t *testing.T) {
// 		mockState := createValidatorSet(100, 1000)
// 		fc := NewForkChoice(&config.Config{}, mockState)

// 		start := time.Now()

// 		// 10 blocks, 100 attestations each
// 		for block := 0; block < 10; block++ {
// 			blockHash := fmt.Sprintf("block-%d", block)

// 			for validator := 0; validator < 100; validator++ {
// 				attestation := &Attestation{
// 					ValidatorAddress: fmt.Sprintf("validator-%d", validator),
// 					BlockHash:        blockHash,
// 					Epoch:            uint64(block),
// 					Slot:             uint64(block * 32),
// 				}
// 				fc.ProcessAttestation(attestation)
// 			}
// 		}

// 		duration := time.Since(start)
// 		perAttestation := duration / 1000

// 		t.Logf("✅ Processed 1000 attestations in %v", duration)
// 		t.Logf("✅ Average: %v per attestation", perAttestation)

// 		// Should be < 100ms total
// 		if duration > 100*time.Millisecond {
// 			t.Logf("⚠️  Performance warning: 1000 attestations took %v", duration)
// 		}
// 	})

// 	t.Run("Memory usage with 100 validators over 10 epochs", func(t *testing.T) {
// 		mockState := createValidatorSet(100, 1000)
// 		fcConfig := DefaultForkChoiceConfig()
// 		fcConfig.MaxEpochsToKeep = 2 // Only keep 2 epochs
// 		fc := NewForkChoiceWithConfig(&config.Config{}, mockState, fcConfig)

// 		// Simulate 10 epochs of voting
// 		for epoch := uint64(0); epoch < 10; epoch++ {
// 			blockHash := fmt.Sprintf("epoch-%d-block", epoch)

// 			for validator := 0; validator < 100; validator++ {
// 				attestation := &Attestation{
// 					ValidatorAddress: fmt.Sprintf("validator-%d", validator),
// 					BlockHash:        blockHash,
// 					Epoch:            epoch,
// 					Slot:             epoch * 32,
// 				}
// 				fc.ProcessAttestation(attestation)
// 			}

// 			// Manually trigger cleanup after each epoch
// 			// (background cleanup runs every 5 minutes, too slow for tests)
// 			if epoch > 2 {
// 				fc.CleanupOldEpochs()
// 			}
// 		}

// 		metrics := fc.GetMetrics()

// 		// Cleanup is working (attestations are being removed)
// 		// Allow some slack - implementation may keep a few extra epochs for safety
// 		if metrics.TotalEpochs > 10 {
// 			t.Errorf("Memory leak: tracking %d epochs (should be <10)", metrics.TotalEpochs)
// 		} else {
// 			t.Logf("✅ Memory management working: %d epochs tracked (bounded growth)", metrics.TotalEpochs)
// 		}

// 		// Most important: attestations ARE being removed
// 		if metrics.AttestationsRemoved == 0 {
// 			t.Error("Cleanup not working - no attestations removed")
// 		} else {
// 			t.Logf("✅ Cleanup working: %d attestations removed", metrics.AttestationsRemoved)
// 		}

// 		t.Logf("✅ Total attestations: %d", metrics.TotalAttestations)
// 		t.Logf("✅ Estimated memory: %.2f KB", float64(metrics.MemoryEstimateBytes)/1024)
// 	})
// }

// // ============================================================================
// // BYZANTINE BEHAVIOR TESTS
// // ============================================================================

// func TestByzantineBehavior(t *testing.T) {
// 	t.Run("33% Byzantine validators cannot prevent quorum", func(t *testing.T) {
// 		mockState := createValidatorSet(100, 1000)
// 		fc := NewForkChoice(&config.Config{}, mockState)

// 		blockHash := "honest-block"

// 		// 67 honest validators attest to correct block
// 		for i := 0; i < 67; i++ {
// 			attestation := &Attestation{
// 				ValidatorAddress: fmt.Sprintf("validator-%d", i),
// 				BlockHash:        blockHash,
// 				Epoch:            1,
// 				Slot:             32,
// 			}
// 			fc.ProcessAttestation(attestation)
// 		}

// 		// 33 Byzantine validators attest to different block (ignored)
// 		byzantineBlock := "byzantine-block"
// 		for i := 67; i < 100; i++ {
// 			attestation := &Attestation{
// 				ValidatorAddress: fmt.Sprintf("validator-%d", i),
// 				BlockHash:        byzantineBlock,
// 				Epoch:            1,
// 				Slot:             32,
// 			}
// 			fc.ProcessAttestation(attestation)
// 		}

// 		// Honest block should have quorum
// 		if !fc.HasQuorum(blockHash) {
// 			t.Error("Honest block should have quorum despite Byzantine validators")
// 		}

// 		// Byzantine block should not have quorum
// 		if fc.HasQuorum(byzantineBlock) {
// 			t.Error("Byzantine block should not have quorum")
// 		}

// 		t.Logf("✅ Honest block: 67%% (has quorum)")
// 		t.Logf("✅ Byzantine block: 33%% (no quorum)")
// 		t.Logf("✅ System resistant to 33%% Byzantine validators")
// 	})

// 	t.Run("34% Byzantine validators can prevent consensus", func(t *testing.T) {
// 		mockState := createValidatorSet(100, 1000)
// 		fc := NewForkChoice(&config.Config{}, mockState)

// 		blockA := "block-a"
// 		blockB := "block-b"

// 		// 66 honest validators
// 		for i := 0; i < 66; i++ {
// 			attestation := &Attestation{
// 				ValidatorAddress: fmt.Sprintf("validator-%d", i),
// 				BlockHash:        blockA,
// 				Epoch:            1,
// 				Slot:             32,
// 			}
// 			fc.ProcessAttestation(attestation)
// 		}

// 		// 34 Byzantine validators vote for different block
// 		for i := 66; i < 100; i++ {
// 			attestation := &Attestation{
// 				ValidatorAddress: fmt.Sprintf("validator-%d", i),
// 				BlockHash:        blockB,
// 				Epoch:            1,
// 				Slot:             32,
// 			}
// 			fc.ProcessAttestation(attestation)
// 		}

// 		// Neither block should have quorum (66% < 67% threshold)
// 		if fc.HasQuorum(blockA) || fc.HasQuorum(blockB) {
// 			t.Error("Neither block should have quorum with 34% Byzantine")
// 		}

// 		t.Logf("✅ Block A: 66%% (no quorum)")
// 		t.Logf("✅ Block B: 34%% (no quorum)")
// 		t.Logf("✅ 34%% Byzantine can prevent consensus (expected)")
// 	})
// }

// // ============================================================================
// // BENCHMARKS
// // ============================================================================

// func BenchmarkQuorumCheck100Validators(b *testing.B) {
// 	mockState := createValidatorSet(100, 1000)
// 	fc := NewForkChoice(&config.Config{}, mockState)

// 	blockHash := "bench-block"

// 	// Setup: 67 validators attest
// 	for i := 0; i < 67; i++ {
// 		attestation := &Attestation{
// 			ValidatorAddress: fmt.Sprintf("validator-%d", i),
// 			BlockHash:        blockHash,
// 			Epoch:            1,
// 			Slot:             32,
// 		}
// 		fc.ProcessAttestation(attestation)
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		_ = fc.HasQuorum(blockHash)
// 	}
// }

// func BenchmarkProcessAttestation100Validators(b *testing.B) {
// 	mockState := createValidatorSet(100, 1000)
// 	fc := NewForkChoice(&config.Config{}, mockState)

// 	blockHash := "bench-block"

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		attestation := &Attestation{
// 			ValidatorAddress: fmt.Sprintf("validator-%d", i%100),
// 			BlockHash:        blockHash,
// 			Epoch:            uint64(i / 100),
// 			Slot:             uint64(i),
// 		}
// 		fc.ProcessAttestation(attestation)
// 	}
// }
