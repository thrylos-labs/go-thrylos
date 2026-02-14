// consensus/pos/consensus_signature.go
package pos

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/thrylos-labs/go-thrylos/crypto/hash"
	core "github.com/thrylos-labs/go-thrylos/proto/core"

	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/types"
)

// Domain Separation Tags prevent signature reuse across different message types
const (
	DomainAttestation = "THRYLOS_ATTESTATION_V1"
	DomainProposal    = "THRYLOS_PROPOSAL_V1"
	DomainVote        = "THRYLOS_VOTE_V1"
)

// =============================================================================
// ATTESTATION SIGNATURES
// =============================================================================

// computeAttestationHash creates a secure hash bound to the ChainID
func (ce *ConsensusEngine) computeAttestationHash(attestation *types.Attestation) ([]byte, error) {
	var buf bytes.Buffer

	// 1. Domain Separation
	buf.WriteString(DomainAttestation)
	// 2. Chain Binding (Prevents Cross-Chain Replay)
	buf.WriteString(ce.config.Network.ChainID)

	// 3. Data Fields
	buf.WriteString(attestation.ValidatorAddress)
	buf.WriteString(attestation.BlockHash)

	// Use binary encoding for deterministic numeric serialization
	if err := binary.Write(&buf, binary.BigEndian, attestation.BlockHeight); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, attestation.Epoch); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, attestation.Slot); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, attestation.Timestamp); err != nil {
		return nil, err
	}

	// Use your crypto/hash package - Keccak256 wrapper
	return hash.Keccak256(buf.Bytes()), nil
}

// verifyAttestationSignature verifies an attestation signature
func (ce *ConsensusEngine) verifyAttestationSignature(attestation *types.Attestation) error {
	validator, err := ce.worldState.GetValidator(attestation.ValidatorAddress)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}
	if len(validator.Pubkey) == 0 {
		return fmt.Errorf("validator %s has no public key", attestation.ValidatorAddress)
	}

	pubKey, err := crypto.NewPublicKeyFromBytes(validator.Pubkey)
	if err != nil {
		return fmt.Errorf("invalid public key: %v", err)
	}

	// Recompute hash with ChainID context
	hash, err := ce.computeAttestationHash(attestation)
	if err != nil {
		return fmt.Errorf("failed to compute hash: %v", err)
	}

	if len(attestation.Signature) == 0 {
		return fmt.Errorf("missing signature")
	}

	sig, err := crypto.SignatureFromBytes(attestation.Signature)
	if err != nil {
		return fmt.Errorf("invalid signature format: %v", err)
	}

	// [FIX L-02] Use VerifyHash to avoid double-hashing (Keccak(Blake2b))
	if err := pubKey.VerifyHash(hash, sig); err != nil {
		return fmt.Errorf("invalid signature from %s: %v", attestation.ValidatorAddress, err)
	}

	return nil
}

// signAttestation signs an attestation with ChainID binding
func (ce *ConsensusEngine) signAttestation(attestation *types.Attestation) ([]byte, error) {
	hash, err := ce.computeAttestationHash(attestation)
	if err != nil {
		return nil, err
	}

	// [FIX L-02] Use SignHash to sign the Blake2b digest directly
	signature, err := ce.nodePrivateKey.SignHash(hash)
	if err != nil {
		return nil, fmt.Errorf("signing failed: %v", err)
	}

	return signature.Bytes(), nil
}

// =============================================================================
// BLOCK PROPOSAL SIGNATURES
// =============================================================================

func (ce *ConsensusEngine) computeProposalHash(proposal *BlockProposal) ([]byte, error) {
	var buf bytes.Buffer

	buf.WriteString(DomainProposal)
	buf.WriteString(ce.config.Network.ChainID)

	buf.WriteString(proposal.Block.Hash)
	buf.WriteString(proposal.Proposer)

	if err := binary.Write(&buf, binary.BigEndian, proposal.Slot); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, proposal.Epoch); err != nil {
		return nil, err
	}

	return hash.Keccak256(buf.Bytes()), nil

}

func (ce *ConsensusEngine) verifyProposalSignature(proposal *BlockProposal) error {
	validator, err := ce.worldState.GetValidator(proposal.Proposer)
	if err != nil {
		return fmt.Errorf("proposer not found: %v", err)
	}

	pubKey, err := crypto.NewPublicKeyFromBytes(validator.Pubkey)
	if err != nil {
		return fmt.Errorf("invalid public key bytes (len=%d): %v", len(validator.Pubkey), err)
	}

	hash, err := ce.computeProposalHash(proposal)
	if err != nil {
		return err
	}

	if len(proposal.Signature) == 0 {
		return fmt.Errorf("missing signature")
	}

	sig, err := crypto.SignatureFromBytes(proposal.Signature)
	if err != nil {
		return fmt.Errorf("invalid signature format: %v", err)
	}

	// ✅ DEBUGGING: Print details on failure
	if err := pubKey.VerifyHash(hash, sig); err != nil {
		// Log the ChainID used during verification
		fmt.Printf("❌ SIG FAIL | Proposer: %s | ChainID: %s | PubKeyLen: %d\n",
			proposal.Proposer, ce.config.Network.ChainID, len(validator.Pubkey))
		return fmt.Errorf("proposal signature verification failed: %v", err)
	}

	return nil
}

func (ce *ConsensusEngine) signBlockProposal(proposal *BlockProposal) error {
	hash, err := ce.computeProposalHash(proposal)
	if err != nil {
		return err
	}

	// [FIX L-02] Use SignHash
	signature, err := ce.nodePrivateKey.SignHash(hash)
	if err != nil {
		return fmt.Errorf("signing failed: %v", err)
	}
	proposal.Signature = signature.Bytes()
	return nil
}

// =============================================================================
// VOTE SIGNATURES
// =============================================================================

func (ce *ConsensusEngine) computeVoteHash(vote *Vote) ([]byte, error) {
	var buf bytes.Buffer

	buf.WriteString(DomainVote)
	buf.WriteString(ce.config.Network.ChainID)

	buf.WriteString(vote.ValidatorAddress)
	buf.WriteString(vote.SourceBlockHash)
	buf.WriteString(vote.TargetBlockHash)

	if err := binary.Write(&buf, binary.BigEndian, vote.SourceEpoch); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, vote.TargetEpoch); err != nil {
		return nil, err
	}

	return hash.Keccak256(buf.Bytes()), nil

}

func (ce *ConsensusEngine) verifyVoteSignature(vote *Vote) error {
	validator, err := ce.worldState.GetValidator(vote.ValidatorAddress)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	pubKey, err := crypto.NewPublicKeyFromBytes(validator.Pubkey)
	if err != nil {
		return fmt.Errorf("invalid public key: %v", err)
	}

	hash, err := ce.computeVoteHash(vote)
	if err != nil {
		return err
	}

	if len(vote.Signature) == 0 {
		return fmt.Errorf("missing signature")
	}

	sig, err := crypto.SignatureFromBytes(vote.Signature)
	if err != nil {
		return fmt.Errorf("invalid signature format: %v", err)
	}

	// [FIX L-02] Use VerifyHash
	if err := pubKey.VerifyHash(hash, sig); err != nil {
		return fmt.Errorf("vote signature verification failed: %v", err)
	}

	return nil
}

func (ce *ConsensusEngine) VerifyBlockWithSignatures(block *core.Block) error {
	// Skip genesis block
	if block.Header.Index == 0 {
		return nil
	}

	// 1. Verify block has a signature
	if len(block.Signature) == 0 {
		return fmt.Errorf("CRITICAL: block %s has no signature", block.Hash)
	}

	// 2. Verify proposer exists and is active
	validator, err := ce.worldState.GetValidator(block.Header.Validator)
	if err != nil {
		return fmt.Errorf("CRITICAL: proposer not found: %v", err)
	}

	if !validator.Active || validator.JailUntil > time.Now().Unix() {
		return fmt.Errorf("CRITICAL: proposer is inactive or jailed")
	}

	// 3. Verify public key and signature
	pubKey, err := crypto.NewPublicKeyFromBytes(validator.Pubkey)
	if err != nil {
		return fmt.Errorf("CRITICAL: invalid public key: %v", err)
	}

	msg, err := ce.computeBlockSigningHash(block)
	if err != nil {
		return fmt.Errorf("CRITICAL: failed to compute signing hash: %v", err)
	}

	sig, err := crypto.SignatureFromBytes(block.Signature)
	if err != nil {
		return fmt.Errorf("CRITICAL: invalid signature format: %v", err)
	}

	if err := pubKey.Verify(msg, sig); err != nil {
		return fmt.Errorf("CRITICAL: signature verification failed: %v", err)
	}

	return nil
}
