// core/block/validator.go
package block

import (
	"fmt"

	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// Validator represents block validation logic
type Validator struct {
	shardID     account.ShardID
	totalShards int
}

// NewValidator creates a new block validator
func NewValidator(shardID account.ShardID, totalShards int) *Validator {
	return &Validator{
		shardID:     shardID,
		totalShards: totalShards,
	}
}

// ValidateBlock performs comprehensive block validation
func (v *Validator) ValidateBlock(block *core.Block, prevBlock *core.Block, publicKey *crypto.PublicKey) error {
	// Basic block structure validation
	if err := v.validateBlockStructure(block); err != nil {
		return fmt.Errorf("block structure validation failed: %v", err)
	}

	// Chain continuity validation
	if err := v.validateChainContinuity(block, prevBlock); err != nil {
		return fmt.Errorf("chain continuity validation failed: %v", err)
	}

	// Shard-specific validation
	if err := v.validateShardTransactions(block); err != nil {
		return fmt.Errorf("shard transaction validation failed: %v", err)
	}

	// Cryptographic validation
	if err := v.validateCryptographic(block, publicKey); err != nil {
		return fmt.Errorf("cryptographic validation failed: %v", err)
	}

	return nil
}

// ValidateBlockForChain validates a block can be added to a specific chain position
func (v *Validator) ValidateBlockForChain(block *core.Block, prevBlock *core.Block, expectedHeight int64) error {
	if block == nil {
		return fmt.Errorf("block cannot be nil")
	}

	// Check height matches expected
	if block.Header.Index != expectedHeight {
		return fmt.Errorf("block height mismatch: expected %d, got %d", expectedHeight, block.Header.Index)
	}

	// Validate against previous block
	return v.ValidateBlock(block, prevBlock, nil) // Skip signature validation here
}

// ValidateBlockBatch validates multiple blocks in sequence
func (v *Validator) ValidateBlockBatch(blocks []*core.Block, startingPrevBlock *core.Block) error {
	if len(blocks) == 0 {
		return nil
	}

	prevBlock := startingPrevBlock
	for i, block := range blocks {
		if err := v.ValidateBlock(block, prevBlock, nil); err != nil {
			return fmt.Errorf("block %d validation failed: %v", i, err)
		}
		prevBlock = block
	}

	return nil
}

// validateBlockStructure validates basic block structure
func (v *Validator) validateBlockStructure(block *core.Block) error {
	if block == nil {
		return fmt.Errorf("block cannot be nil")
	}

	if block.Header == nil {
		return fmt.Errorf("block header cannot be nil")
	}

	if block.Hash == "" {
		return fmt.Errorf("block hash cannot be empty")
	}

	if block.Header.Validator == "" {
		return fmt.Errorf("block validator cannot be empty")
	}

	if block.Header.Index < 0 {
		return fmt.Errorf("block index cannot be negative: %d", block.Header.Index)
	}

	if block.Header.Timestamp <= 0 {
		return fmt.Errorf("block timestamp must be positive: %d", block.Header.Timestamp)
	}

	if block.Header.GasUsed < 0 {
		return fmt.Errorf("gas used cannot be negative: %d", block.Header.GasUsed)
	}

	if block.Header.GasLimit <= 0 {
		return fmt.Errorf("gas limit must be positive: %d", block.Header.GasLimit)
	}

	if block.Header.GasUsed > block.Header.GasLimit {
		return fmt.Errorf("gas used (%d) exceeds gas limit (%d)",
			block.Header.GasUsed, block.Header.GasLimit)
	}

	if block.Header.TxRoot == "" {
		return fmt.Errorf("transaction root cannot be empty")
	}

	return nil
}

// validateChainContinuity validates block fits in the chain
func (v *Validator) validateChainContinuity(block *core.Block, prevBlock *core.Block) error {
	if prevBlock == nil {
		// Genesis block validation
		if block.Header.Index != 0 {
			return fmt.Errorf("genesis block must have index 0, got %d", block.Header.Index)
		}
		if block.Header.PrevHash != "" {
			return fmt.Errorf("genesis block must have empty previous hash")
		}
		return nil
	}

	// Regular block validation
	if block.Header.Index != prevBlock.Header.Index+1 {
		return fmt.Errorf("invalid block index: expected %d, got %d",
			prevBlock.Header.Index+1, block.Header.Index)
	}

	if block.Header.PrevHash != prevBlock.Hash {
		return fmt.Errorf("invalid previous hash: expected %s, got %s",
			prevBlock.Hash, block.Header.PrevHash)
	}

	if block.Header.Timestamp <= prevBlock.Header.Timestamp {
		return fmt.Errorf("block timestamp (%d) must be greater than previous block timestamp (%d)",
			block.Header.Timestamp, prevBlock.Header.Timestamp)
	}

	return nil
}

// validateShardTransactions validates transactions belong to this shard
func (v *Validator) validateShardTransactions(block *core.Block) error {
	if v.shardID == account.BeaconShardID {
		// Beacon shard can process any transactions
		return nil
	}

	for i, tx := range block.Transactions {
		if tx == nil {
			return fmt.Errorf("transaction %d is nil", i)
		}

		if tx.From == "" {
			return fmt.Errorf("transaction %d has empty sender address", i)
		}

		senderShard := account.CalculateShardID(tx.From, v.totalShards)
		if senderShard != v.shardID {
			return fmt.Errorf("transaction %s sender %s belongs to shard %d, not %d",
				tx.Id, tx.From, senderShard, v.shardID)
		}

		// Validate transaction has required fields
		if tx.Hash == "" {
			return fmt.Errorf("transaction %s has empty hash", tx.Id)
		}

		if tx.Id == "" {
			return fmt.Errorf("transaction at index %d has empty ID", i)
		}
	}

	return nil
}

// validateCryptographic validates block hash, transaction root, and signature
func (v *Validator) validateCryptographic(block *core.Block, publicKey *crypto.PublicKey) error {
	// Verify block hash
	if err := v.verifyBlockHash(block); err != nil {
		return fmt.Errorf("block hash verification failed: %v", err)
	}

	// Verify transaction root
	if err := v.verifyTransactionRoot(block); err != nil {
		return fmt.Errorf("transaction root verification failed: %v", err)
	}

	// Verify signature if present and public key provided
	if len(block.Signature) > 0 && publicKey != nil {
		if err := v.verifyBlockSignature(block, publicKey); err != nil {
			return fmt.Errorf("block signature verification failed: %v", err)
		}
	}

	return nil
}

// verifyBlockHash verifies that a block's hash is correct
func (v *Validator) verifyBlockHash(block *core.Block) error {
	// Create a temporary creator to calculate hash
	tempCreator := NewCreator(v.shardID, v.totalShards)

	// Temporarily clear the hash to recalculate
	originalHash := block.Hash
	block.Hash = ""

	calculatedHash, err := tempCreator.calculateBlockHash(block)

	// Restore original hash
	block.Hash = originalHash

	if err != nil {
		return fmt.Errorf("failed to recalculate block hash: %v", err)
	}

	if calculatedHash != originalHash {
		return fmt.Errorf("block hash mismatch: expected %s, got %s", originalHash, calculatedHash)
	}

	return nil
}

// verifyTransactionRoot verifies the transaction Merkle root
func (v *Validator) verifyTransactionRoot(block *core.Block) error {
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
func (v *Validator) verifyBlockSignature(block *core.Block, publicKey *crypto.PublicKey) error {
	if publicKey == nil {
		return fmt.Errorf("public key cannot be nil")
	}

	if len(block.Signature) == 0 {
		return fmt.Errorf("block signature is empty")
	}

	// Use the package-level verification function
	return verifyBlockSignature(block, publicKey)
}

// ValidateGasUsage validates that gas usage calculations are correct
func (v *Validator) ValidateGasUsage(block *core.Block) error {
	calculatedGas := int64(0)
	for _, tx := range block.Transactions {
		calculatedGas += tx.Gas
	}

	if block.Header.GasUsed != calculatedGas {
		return fmt.Errorf("gas used mismatch: header shows %d, calculated %d",
			block.Header.GasUsed, calculatedGas)
	}

	return nil
}

// ValidateTimestamp validates block timestamp is reasonable
func (v *Validator) ValidateTimestamp(block *core.Block, maxFutureTime, maxPastTime int64) error {
	if block.Header.Timestamp > maxFutureTime {
		return fmt.Errorf("block timestamp %d is too far in the future (max: %d)",
			block.Header.Timestamp, maxFutureTime)
	}

	if block.Header.Timestamp < maxPastTime {
		return fmt.Errorf("block timestamp %d is too far in the past (min: %d)",
			block.Header.Timestamp, maxPastTime)
	}

	return nil
}
