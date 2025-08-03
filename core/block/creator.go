// core/block/creator.go
package block

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/crypto/hash"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// Creator handles block creation for a specific shard
type Creator struct {
	shardID     account.ShardID
	totalShards int
	mu          sync.Mutex
}

// NewCreator creates a new block creator for a shard
func NewCreator(shardID account.ShardID, totalShards int) *Creator {
	return &Creator{
		shardID:     shardID,
		totalShards: totalShards,
	}
}

// CreateBlock creates a new block with the given transactions
func (bc *Creator) CreateBlock(
	prevBlock *core.Block,
	transactions []*core.Transaction,
	validator string,
	gasLimit int64,
) (*core.Block, error) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	// Validate inputs
	if validator == "" {
		return nil, fmt.Errorf("validator address cannot be empty")
	}

	if gasLimit <= 0 {
		return nil, fmt.Errorf("gas limit must be positive")
	}

	// Filter transactions for this shard
	shardTransactions := bc.filterTransactionsForShard(transactions)

	// Calculate transaction root
	txRoot, err := bc.calculateTransactionRoot(shardTransactions)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate transaction root: %v", err)
	}

	// Calculate gas used
	gasUsed := bc.calculateGasUsed(shardTransactions)

	// Validate gas usage doesn't exceed limit
	if gasUsed > gasLimit {
		return nil, fmt.Errorf("gas used (%d) exceeds gas limit (%d)", gasUsed, gasLimit)
	}

	// Determine block index and previous hash
	var blockIndex int64 = 0
	var prevHash string = ""
	if prevBlock != nil {
		blockIndex = prevBlock.Header.Index + 1
		prevHash = prevBlock.Hash
	}

	// Create block header
	header := &core.BlockHeader{
		Index:     blockIndex,
		PrevHash:  prevHash,
		Timestamp: time.Now().Unix(),
		Validator: validator,
		TxRoot:    txRoot,
		StateRoot: "", // Will be set by state manager
		GasUsed:   gasUsed,
		GasLimit:  gasLimit,
	}

	// Create block
	block := &core.Block{
		Header:       header,
		Transactions: shardTransactions,
		Hash:         "",  // Will be calculated
		Signature:    nil, // Will be set when signed
	}

	// Calculate block hash
	blockHash, err := bc.calculateBlockHash(block)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate block hash: %v", err)
	}
	block.Hash = blockHash

	return block, nil
}

// CreateGenesisBlock creates the genesis block for a shard
func (bc *Creator) CreateGenesisBlock(genesisValidator string, timestamp int64) (*core.Block, error) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if genesisValidator == "" {
		return nil, fmt.Errorf("genesis validator cannot be empty")
	}

	if timestamp <= 0 {
		timestamp = time.Now().Unix()
	}

	header := &core.BlockHeader{
		Index:     0,
		PrevHash:  "",
		Timestamp: timestamp,
		Validator: genesisValidator,
		TxRoot:    calculateEmptyRoot(),
		StateRoot: "",
		GasUsed:   0,
		GasLimit:  1000000, // Default genesis gas limit
	}

	block := &core.Block{
		Header:       header,
		Transactions: []*core.Transaction{},
		Hash:         "",
		Signature:    nil,
	}

	// Calculate genesis block hash
	blockHash, err := bc.calculateBlockHash(block)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate genesis block hash: %v", err)
	}
	block.Hash = blockHash

	return block, nil
}

// CreateBlockFromTemplate creates a block from a template with specific parameters
func (bc *Creator) CreateBlockFromTemplate(
	template *BlockTemplate,
	validator string,
) (*core.Block, error) {
	return bc.CreateBlock(
		template.PrevBlock,
		template.Transactions,
		validator,
		template.GasLimit,
	)
}

// BlockTemplate represents a template for creating blocks
type BlockTemplate struct {
	PrevBlock    *core.Block
	Transactions []*core.Transaction
	GasLimit     int64
	MaxTxs       int
}

// CreateBlockTemplate creates a template for block creation
func (bc *Creator) CreateBlockTemplate(
	prevBlock *core.Block,
	candidateTransactions []*core.Transaction,
	gasLimit int64,
	maxTxs int,
) *BlockTemplate {
	// Filter and limit transactions
	shardTxs := bc.filterTransactionsForShard(candidateTransactions)

	// Limit number of transactions
	if maxTxs > 0 && len(shardTxs) > maxTxs {
		shardTxs = shardTxs[:maxTxs]
	}

	// Filter by gas limit
	filteredTxs := bc.filterTransactionsByGas(shardTxs, gasLimit)

	return &BlockTemplate{
		PrevBlock:    prevBlock,
		Transactions: filteredTxs,
		GasLimit:     gasLimit,
		MaxTxs:       maxTxs,
	}
}

// filterTransactionsForShard filters transactions that belong to this shard
func (bc *Creator) filterTransactionsForShard(transactions []*core.Transaction) []*core.Transaction {
	if bc.shardID == account.BeaconShardID {
		// Beacon shard can include all transactions
		return transactions
	}

	var shardTxs []*core.Transaction
	for _, tx := range transactions {
		if tx == nil || tx.From == "" {
			continue // Skip invalid transactions
		}

		senderShard := account.CalculateShardID(tx.From, bc.totalShards)

		// Include transaction if sender belongs to this shard
		if senderShard == bc.shardID {
			shardTxs = append(shardTxs, tx)
		}
	}

	return shardTxs
}

// filterTransactionsByGas filters transactions to fit within gas limit
func (bc *Creator) filterTransactionsByGas(transactions []*core.Transaction, gasLimit int64) []*core.Transaction {
	var filteredTxs []*core.Transaction
	var totalGas int64

	for _, tx := range transactions {
		if tx == nil {
			continue
		}

		if totalGas+tx.Gas <= gasLimit {
			filteredTxs = append(filteredTxs, tx)
			totalGas += tx.Gas
		}
		// Stop when we hit the gas limit
		if totalGas >= gasLimit {
			break
		}
	}

	return filteredTxs
}

// calculateTransactionRoot calculates the Merkle root of transactions
func (bc *Creator) calculateTransactionRoot(transactions []*core.Transaction) (string, error) {
	if len(transactions) == 0 {
		return calculateEmptyRoot(), nil
	}

	// Create list of transaction hashes
	var txHashes []string
	for _, tx := range transactions {
		if tx.Hash == "" {
			return "", fmt.Errorf("transaction %s has empty hash", tx.Id)
		}
		txHashes = append(txHashes, tx.Hash)
	}

	// Calculate Merkle root
	return calculateMerkleRoot(txHashes), nil
}

// calculateGasUsed calculates total gas used by transactions
func (bc *Creator) calculateGasUsed(transactions []*core.Transaction) int64 {
	var totalGas int64
	for _, tx := range transactions {
		if tx != nil {
			totalGas += tx.Gas
		}
	}
	return totalGas
}

// calculateBlockHash calculates the Blake2b hash of a block
func (bc *Creator) calculateBlockHash(block *core.Block) (string, error) {
	var buf bytes.Buffer

	// Serialize block header fields
	header := block.Header

	// Index
	indexBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(indexBytes, uint64(header.Index))
	buf.Write(indexBytes)

	// Previous hash
	buf.WriteString(header.PrevHash)

	// Timestamp
	timestampBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(timestampBytes, uint64(header.Timestamp))
	buf.Write(timestampBytes)

	// Validator
	buf.WriteString(header.Validator)

	// Transaction root
	buf.WriteString(header.TxRoot)

	// State root
	buf.WriteString(header.StateRoot)

	// Gas used
	gasUsedBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasUsedBytes, uint64(header.GasUsed))
	buf.Write(gasUsedBytes)

	// Gas limit
	gasLimitBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasLimitBytes, uint64(header.GasLimit))
	buf.Write(gasLimitBytes)

	// Calculate Blake2b hash using crypto/hash
	hashBytes := hash.HashData(buf.Bytes())
	return fmt.Sprintf("%x", hashBytes), nil
}

// SignBlock signs a block with the validator's private key
func (bc *Creator) SignBlock(block *core.Block, privateKey crypto.PrivateKey) error {
	if block == nil {
		return fmt.Errorf("block cannot be nil")
	}
	if privateKey == nil {
		return fmt.Errorf("private key cannot be nil")
	}

	// Create hash to sign using crypto/hash
	hashToSign := hash.HashData([]byte(block.Hash))

	// Sign with MLDSA44
	signature := privateKey.Sign(hashToSign)
	if signature == nil {
		return fmt.Errorf("failed to sign block")
	}

	block.Signature = signature.Bytes()
	return nil
}

// EstimateBlockSize estimates the size of a block in bytes
func (bc *Creator) EstimateBlockSize(transactions []*core.Transaction) int64 {
	// Rough estimate based on transaction count and average transaction size
	// This is a simplified estimation
	baseSize := int64(200)  // Block header overhead
	avgTxSize := int64(300) // Average transaction size

	return baseSize + (int64(len(transactions)) * avgTxSize)
}

// GetShardID returns the shard ID this creator is responsible for
func (bc *Creator) GetShardID() account.ShardID {
	return bc.shardID
}

// SetStateRoot sets the state root in a block header
func (bc *Creator) SetStateRoot(block *core.Block, stateRoot string) {
	if block != nil && block.Header != nil {
		block.Header.StateRoot = stateRoot

		// Recalculate block hash since state root changed
		newHash, err := bc.calculateBlockHash(block)
		if err == nil {
			block.Hash = newHash
		}
	}
}
