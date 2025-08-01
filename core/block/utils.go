// core/block/utils.go
package block

import (
	"fmt"

	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/proto/core"
	"golang.org/x/crypto/blake2b"
)

// Package-level utility functions shared between creator and validator

// VerifyBlock verifies a block's integrity and signature (package-level function)
func VerifyBlock(block *core.Block, publicKey *crypto.PublicKey) error {
	if block == nil {
		return fmt.Errorf("block cannot be nil")
	}

	// Verify block hash
	if err := verifyBlockHash(block); err != nil {
		return fmt.Errorf("block hash verification failed: %v", err)
	}

	// Verify transaction root
	if err := verifyTransactionRoot(block); err != nil {
		return fmt.Errorf("transaction root verification failed: %v", err)
	}

	// Verify signature if present
	if len(block.Signature) > 0 && publicKey != nil {
		if err := verifyBlockSignature(block, publicKey); err != nil {
			return fmt.Errorf("block signature verification failed: %v", err)
		}
	}

	return nil
}

// verifyBlockHash verifies that a block's hash is correct
func verifyBlockHash(block *core.Block) error {
	// Temporarily clear the hash and signature to recalculate
	originalHash := block.Hash
	originalSig := block.Signature

	block.Hash = ""
	block.Signature = nil

	// Create a temporary creator to calculate hash
	tempCreator := &Creator{}
	calculatedHash, err := tempCreator.calculateBlockHash(block)

	// Restore original values
	block.Hash = originalHash
	block.Signature = originalSig

	if err != nil {
		return fmt.Errorf("failed to recalculate block hash: %v", err)
	}

	if calculatedHash != originalHash {
		return fmt.Errorf("block hash mismatch: expected %s, got %s", originalHash, calculatedHash)
	}

	return nil
}

// verifyTransactionRoot verifies the transaction Merkle root
func verifyTransactionRoot(block *core.Block) error {
	// Calculate expected transaction root
	var txHashes []string
	for _, tx := range block.Transactions {
		txHashes = append(txHashes, tx.Hash)
	}

	expectedRoot := calculateEmptyRoot()
	if len(txHashes) > 0 {
		expectedRoot = calculateMerkleRoot(txHashes)
	}

	if block.Header.TxRoot != expectedRoot {
		return fmt.Errorf("transaction root mismatch: expected %s, got %s",
			expectedRoot, block.Header.TxRoot)
	}

	return nil
}

// verifyBlockSignature verifies a block's signature
func verifyBlockSignature(block *core.Block, publicKey *crypto.PublicKey) error {
	if publicKey == nil {
		return fmt.Errorf("public key cannot be nil")
	}

	if len(block.Signature) == 0 {
		return fmt.Errorf("block signature is empty")
	}

	// Recreate the hash that was signed
	hashBytes, err := blake2b.New256(nil)
	if err != nil {
		return fmt.Errorf("failed to create Blake2b hasher: %v", err)
	}

	hashBytes.Write([]byte(block.Hash))
	hashToVerify := hashBytes.Sum(nil)

	// Create signature object from bytes
	signature, err := crypto.SignatureFromBytes(block.Signature)
	if err != nil {
		return fmt.Errorf("failed to parse signature: %v", err)
	}

	// Dereference the pointer to get the interface value, then call Verify
	err = (*publicKey).Verify(hashToVerify, &signature)
	if err != nil {
		return fmt.Errorf("signature verification failed: %v", err)
	}

	return nil
}

// calculateMerkleRoot calculates the Merkle root of a list of hashes
func calculateMerkleRoot(hashes []string) string {
	if len(hashes) == 0 {
		return calculateEmptyRoot()
	}

	if len(hashes) == 1 {
		return hashes[0]
	}

	// Convert string hashes to byte slices for hashing
	var level [][]byte
	for _, hash := range hashes {
		// Convert hex string to bytes
		hashBytes := make([]byte, len(hash)/2)
		for i := 0; i < len(hash); i += 2 {
			var b byte
			fmt.Sscanf(hash[i:i+2], "%02x", &b)
			hashBytes[i/2] = b
		}
		level = append(level, hashBytes)
	}

	// Build Merkle tree
	for len(level) > 1 {
		var nextLevel [][]byte

		for i := 0; i < len(level); i += 2 {
			var combined []byte
			combined = append(combined, level[i]...)

			if i+1 < len(level) {
				combined = append(combined, level[i+1]...)
			} else {
				// Odd number of nodes - duplicate the last one
				combined = append(combined, level[i]...)
			}

			// Hash the combined bytes
			hash := blake2b.Sum256(combined)
			nextLevel = append(nextLevel, hash[:])
		}

		level = nextLevel
	}

	return fmt.Sprintf("%x", level[0])
}

// calculateEmptyRoot returns the hash for an empty transaction set
func calculateEmptyRoot() string {
	empty := blake2b.Sum256([]byte{})
	return fmt.Sprintf("%x", empty)
}

// SignBlock signs a block with the validator's private key (package-level function)
func SignBlock(block *core.Block, privateKey crypto.PrivateKey) error {
	if block == nil {
		return fmt.Errorf("block cannot be nil")
	}
	if privateKey == nil {
		return fmt.Errorf("private key cannot be nil")
	}

	// Create hash to sign
	hashBytes, err := blake2b.New256(nil)
	if err != nil {
		return fmt.Errorf("failed to create Blake2b hasher: %v", err)
	}

	hashBytes.Write([]byte(block.Hash))
	hashToSign := hashBytes.Sum(nil)

	// Sign with MLDSA44
	signature := privateKey.Sign(hashToSign)
	if signature == nil {
		return fmt.Errorf("failed to sign block")
	}

	block.Signature = signature.Bytes()
	return nil
}

// GetBlockSize returns the approximate size of a block in bytes
func GetBlockSize(block *core.Block) int64 {
	if block == nil {
		return 0
	}

	// Rough calculation of block size
	baseSize := int64(200)                         // Header overhead
	txSize := int64(len(block.Transactions)) * 300 // Approximate tx size

	return baseSize + txSize
}

// CompareBlocks compares two blocks for equality (excluding signatures)
func CompareBlocks(a, b *core.Block) bool {
	if a == nil || b == nil {
		return a == b
	}

	if a.Hash != b.Hash {
		return false
	}

	if a.Header == nil || b.Header == nil {
		return a.Header == b.Header
	}

	// Compare headers
	if a.Header.Index != b.Header.Index ||
		a.Header.PrevHash != b.Header.PrevHash ||
		a.Header.Timestamp != b.Header.Timestamp ||
		a.Header.Validator != b.Header.Validator ||
		a.Header.TxRoot != b.Header.TxRoot ||
		a.Header.StateRoot != b.Header.StateRoot ||
		a.Header.GasUsed != b.Header.GasUsed ||
		a.Header.GasLimit != b.Header.GasLimit {
		return false
	}

	// Compare transaction count
	if len(a.Transactions) != len(b.Transactions) {
		return false
	}

	// Compare transaction hashes
	for i := range a.Transactions {
		if a.Transactions[i].Hash != b.Transactions[i].Hash {
			return false
		}
	}

	return true
}

// ValidateBlockHash validates that a block hash matches its content
func ValidateBlockHash(block *core.Block) error {
	return verifyBlockHash(block)
}

// ValidateTransactionRoot validates that a transaction root matches the transactions
func ValidateTransactionRoot(block *core.Block) error {
	return verifyTransactionRoot(block)
}

// IsGenesisBlock returns true if the block is a genesis block
func IsGenesisBlock(block *core.Block) bool {
	return block != nil &&
		block.Header != nil &&
		block.Header.Index == 0 &&
		block.Header.PrevHash == ""
}

// GetBlockInfo returns human-readable information about a block
func GetBlockInfo(block *core.Block) map[string]interface{} {
	if block == nil {
		return map[string]interface{}{"error": "block is nil"}
	}

	info := map[string]interface{}{
		"hash":          block.Hash,
		"has_signature": len(block.Signature) > 0,
		"tx_count":      len(block.Transactions),
		"size_bytes":    GetBlockSize(block),
		"is_genesis":    IsGenesisBlock(block),
	}

	if block.Header != nil {
		info["index"] = block.Header.Index
		info["prev_hash"] = block.Header.PrevHash
		info["timestamp"] = block.Header.Timestamp
		info["validator"] = block.Header.Validator
		info["tx_root"] = block.Header.TxRoot
		info["state_root"] = block.Header.StateRoot
		info["gas_used"] = block.Header.GasUsed
		info["gas_limit"] = block.Header.GasLimit
	}

	return info
}
