// consensus/pos/consensus_signature_fix.go
// FIX #1: Proper signature verification for attestations
// Replace the placeholder verifyAttestationSignature in consensus.go

package pos

import (
	"fmt"

	"github.com/thrylos-labs/go-thrylos/crypto"
	"golang.org/x/crypto/blake2b"
)

// verifyAttestationSignature verifies an attestation signature
// This is the FIXED version that actually performs cryptographic verification
func (ce *ConsensusEngine) verifyAttestationSignature(attestation *Attestation) error {
	// Get validator's public key from world state
	validator, err := ce.worldState.GetValidator(attestation.ValidatorAddress)
	if err != nil {
		return fmt.Errorf("validator not found: %v", err)
	}

	// Validate that validator has a public key
	if validator.Pubkey == nil || len(validator.Pubkey) == 0 {
		return fmt.Errorf("validator %s has no public key", attestation.ValidatorAddress)
	}

	// Parse validator's public key
	pubKey, err := crypto.NewPublicKeyFromBytes(validator.Pubkey)
	if err != nil {
		return fmt.Errorf("failed to parse validator public key: %v", err)
	}

	// Recreate the exact data that was signed
	data := fmt.Sprintf("%s%s%d%d%d%d",
		attestation.ValidatorAddress,
		attestation.BlockHash,
		attestation.BlockHeight,
		attestation.Epoch,
		attestation.Slot,
		attestation.Timestamp)

	// Hash the data using Blake2b
	hash := blake2b.Sum256([]byte(data))

	// Validate signature exists
	if attestation.Signature == nil || len(attestation.Signature) == 0 {
		return fmt.Errorf("attestation has no signature")
	}

	// Parse the signature
	sig, err := crypto.SignatureFromBytes(attestation.Signature)
	if err != nil {
		return fmt.Errorf("failed to parse attestation signature: %v", err)
	}

	// Verify the signature using Ed25519
	// Note: Your Verify method returns error (not bool), nil = success
	if err := pubKey.Verify(hash[:], &sig); err != nil {
		return fmt.Errorf("invalid attestation signature from validator %s: %v",
			attestation.ValidatorAddress, err)
	}

	return nil
}

// verifyProposalSignature verifies a block proposal signature
// This should be added to your consensus engine for block proposal verification
func (ce *ConsensusEngine) verifyProposalSignature(proposal *BlockProposal) error {
	// Get proposer's public key from world state
	validator, err := ce.worldState.GetValidator(proposal.Proposer)
	if err != nil {
		return fmt.Errorf("proposer not found: %v", err)
	}

	// Validate that validator has a public key
	if validator.Pubkey == nil || len(validator.Pubkey) == 0 {
		return fmt.Errorf("proposer %s has no public key", proposal.Proposer)
	}

	// Parse proposer's public key
	pubKey, err := crypto.NewPublicKeyFromBytes(validator.Pubkey)
	if err != nil {
		return fmt.Errorf("failed to parse proposer public key: %v", err)
	}

	// Recreate the exact data that was signed
	proposalData := fmt.Sprintf("%s%s%d%d",
		proposal.Block.Hash,
		proposal.Proposer,
		proposal.Slot,
		proposal.Epoch)

	// Hash the data using Blake2b
	proposalHash := blake2b.Sum256([]byte(proposalData))

	// Validate signature exists
	if proposal.Signature == nil || len(proposal.Signature) == 0 {
		return fmt.Errorf("proposal has no signature")
	}

	// Parse the signature
	sig, err := crypto.SignatureFromBytes(proposal.Signature)
	if err != nil {
		return fmt.Errorf("failed to parse proposal signature: %v", err)
	}

	// Verify the signature using Ed25519
	if err := pubKey.Verify(proposalHash[:], &sig); err != nil {
		return fmt.Errorf("invalid proposal signature from proposer %s: %v",
			proposal.Proposer, err)
	}

	return nil
}

// signBlockProposal creates a signature for a block proposal
// This should be called when creating a block proposal
func (ce *ConsensusEngine) signBlockProposal(proposal *BlockProposal) error {
	// Create the data to sign
	proposalData := fmt.Sprintf("%s%s%d%d",
		proposal.Block.Hash,
		proposal.Proposer,
		proposal.Slot,
		proposal.Epoch)

	// Hash the data
	proposalHash := blake2b.Sum256([]byte(proposalData))

	// Sign with private key
	signature := ce.nodePrivateKey.Sign(proposalHash[:])
	if signature == nil {
		return fmt.Errorf("failed to sign block proposal: signature is nil")
	}

	// Set the signature on the proposal
	proposal.Signature = signature.Bytes()

	return nil
}
