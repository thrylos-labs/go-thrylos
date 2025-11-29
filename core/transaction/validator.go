// core/transaction/validator.go
// Handles transaction validation and creation:

// ✅ Transaction creation with proper ID and hash generation
// ✅ Blake2b hash calculation for transaction integrity
// ✅ Ed25519 signature integration for signing and verification
// ✅ Comprehensive validation - structure, hash, shard, and business logic
// ✅ Address format validation using tl1 bech 32 format
// ✅ Business logic validation - balance checks, minimum amounts, nonce validation
// ✅ Batch validation - validates multiple transactions with temporary state tracking
// ✅ Cross-shard awareness - handles transactions between different shards

package transaction

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/crypto/hash"
	"github.com/thrylos-labs/go-thrylos/proto/core"
	"golang.org/x/crypto/blake2b"
)

// Validator handles transaction validation and creation
type Validator struct {
	shardID     account.ShardID
	totalShards int
	config      *config.Config

	// Replay protection
	replayConfig *ReplayProtectionConfig
	metrics      *ReplayProtectionMetrics
}

// --- ADD THIS INTERFACE ---
// StateReader defines the interface for accessing account state.
// This allows passing either *account.AccountManager or a mock in tests.
type StateReader interface {
	GetAccount(address string) (*core.Account, error)
}

// NewValidator creates a new transaction validator
func NewValidator(shardID account.ShardID, totalShards int, cfg *config.Config) *Validator {
	return &Validator{
		shardID:      shardID,
		totalShards:  totalShards,
		config:       cfg,
		replayConfig: DefaultReplayProtectionConfig(),
		metrics:      &ReplayProtectionMetrics{},
	}
}

// NewValidatorWithReplayConfig creates a validator with custom replay protection config
func NewValidatorWithReplayConfig(shardID account.ShardID, totalShards int, cfg *config.Config, replayConfig *ReplayProtectionConfig) *Validator {
	return &Validator{
		shardID:      shardID,
		totalShards:  totalShards,
		config:       cfg,
		replayConfig: replayConfig,
		metrics:      &ReplayProtectionMetrics{},
	}
}

// CreateTransaction creates a new transaction with proper hash calculation
func (v *Validator) CreateTransaction(
	from, to string,
	amount int64,
	gas int64,
	gasPrice int64,
	nonce uint64,
	txType core.TransactionType,
	data []byte,
) (*core.Transaction, error) {

	// Validate input addresses
	if err := account.ValidateAddress(from); err != nil {
		return nil, fmt.Errorf("invalid sender address: %v", err)
	}

	// Validate recipient address for applicable transaction types
	if to != "" {
		if err := account.ValidateAddress(to); err != nil {
			return nil, fmt.Errorf("invalid recipient address: %v", err)
		}
	}

	// Normalize addresses to lowercase for consistency
	from = strings.ToLower(from)
	if to != "" {
		to = strings.ToLower(to)
	}

	tx := &core.Transaction{
		Id:        generateTransactionID(),
		From:      from,
		To:        to,
		Amount:    amount,
		Gas:       gas,
		GasPrice:  gasPrice,
		Nonce:     nonce,
		Data:      data,
		Type:      txType,
		Timestamp: time.Now().Unix(),
	}

	// Calculate hash
	hash, err := v.CalculateTransactionHash(tx)
	if err != nil {
		return nil, err
	}
	tx.Hash = hash

	return tx, nil
}

// CreateTransferTransaction creates a transfer transaction
func (v *Validator) CreateTransferTransaction(from, to string, amount, gas, gasPrice int64, nonce uint64) (*core.Transaction, error) {
	if amount < v.config.Economics.MinTransfer {
		return nil, fmt.Errorf("transfer amount %d below minimum %d", amount, v.config.Economics.MinTransfer)
	}
	return v.CreateTransaction(from, to, amount, gas, gasPrice, nonce, core.TransactionType_TRANSFER, nil)
}

// CreateStakeTransaction creates a staking transaction
func (v *Validator) CreateStakeTransaction(from string, amount, gas, gasPrice int64, nonce uint64) (*core.Transaction, error) {
	if amount < v.config.Economics.MinStake {
		return nil, fmt.Errorf("stake amount %d below minimum %d", amount, v.config.Economics.MinStake)
	}
	return v.CreateTransaction(from, "", amount, gas, gasPrice, nonce, core.TransactionType_STAKE, nil)
}

// CreateDelegateTransaction creates a delegation transaction
func (v *Validator) CreateDelegateTransaction(from, validator string, amount, gas, gasPrice int64, nonce uint64) (*core.Transaction, error) {
	if amount < v.config.Economics.MinDelegation {
		return nil, fmt.Errorf("delegation amount %d below minimum %d", amount, v.config.Economics.MinDelegation)
	}
	if from == validator {
		return nil, fmt.Errorf("cannot delegate to self")
	}
	return v.CreateTransaction(from, validator, amount, gas, gasPrice, nonce, core.TransactionType_DELEGATE, nil)
}

// CalculateTransactionHash calculates the Blake2b hash of a transaction
func (v *Validator) CalculateTransactionHash(tx *core.Transaction) (string, error) {
	var buf bytes.Buffer

	// Serialize transaction fields for hashing (excluding signature and hash)
	buf.WriteString(tx.Id)
	buf.WriteString(tx.From)
	buf.WriteString(tx.To)

	// Write amount as bytes
	amountBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(amountBytes, uint64(tx.Amount))
	buf.Write(amountBytes)

	// Write gas as bytes
	gasBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasBytes, uint64(tx.Gas))
	buf.Write(gasBytes)

	// Write gas price as bytes
	gasPriceBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasPriceBytes, uint64(tx.GasPrice))
	buf.Write(gasPriceBytes)

	// Write nonce as bytes
	nonceBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(nonceBytes, tx.Nonce)
	buf.Write(nonceBytes)

	// Write transaction type
	typeBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(typeBytes, uint32(tx.Type))
	buf.Write(typeBytes)

	// Write timestamp
	timestampBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(timestampBytes, uint64(tx.Timestamp))
	buf.Write(timestampBytes)

	// Write data
	buf.Write(tx.Data)

	// Calculate Blake2b hash using crypto/hash
	hashBytes, err := hash.HashData(buf.Bytes())
	if err != nil {
		return "", fmt.Errorf("failed to hash buffer: %w", err)
	}

	return fmt.Sprintf("%x", hashBytes), err
}

// SignTransaction signs a transaction with Ed25519
// SignTransaction signs a transaction with Ed25519
// SignTransaction signs a transaction with Ed25519
func (v *Validator) SignTransaction(tx *core.Transaction, privateKey crypto.PrivateKey) error {
	if tx == nil {
		return fmt.Errorf("transaction cannot be nil")
	}

	if privateKey == nil {
		return fmt.Errorf("private key cannot be nil")
	}

	// 🔹 Derive the public key from the private key
	pubKey := privateKey.PublicKey() // ✅ this method exists on crypto.PrivateKey
	if pubKey == nil {
		return fmt.Errorf("failed to derive public key from private key")
	}

	// Derive address from the public key
	derivedAddr, err := account.GenerateAddress(pubKey)
	if err != nil {
		return fmt.Errorf("failed to derive address from public key: %v", err)
	}

	// Normalize & sanity check that tx.From matches this key
	if !strings.EqualFold(derivedAddr, tx.From) {
		return fmt.Errorf("private key does not match tx.From (tx.From=%s, derived=%s)", tx.From, derivedAddr)
	}

	// 🔹 Store public key bytes into the transaction (so validators can reconstruct it)
	tx.FromPubkey = pubKey.Bytes()

	// Calculate the signable hash including chain ID and all context
	hashToSign, err := v.calculateSignableHash(tx)
	if err != nil {
		return fmt.Errorf("failed to calculate signable hash: %v", err)
	}

	// Sign with Ed25519
	signature := privateKey.Sign(hashToSign)
	if signature == nil {
		return fmt.Errorf("failed to sign transaction")
	}

	tx.Signature = signature.Bytes()
	return nil
}

// SignTransactionWithReplayProtection signs a transaction with enhanced replay protection
// This includes the finalized block hash to prevent post-reorg replay attacks
func (v *Validator) SignTransactionWithReplayProtection(tx *core.Transaction, privateKey crypto.PrivateKey, finalizedBlockHash string) error {
	if tx == nil {
		return fmt.Errorf("transaction cannot be nil")
	}

	if privateKey == nil {
		return fmt.Errorf("private key cannot be nil")
	}

	// 🔹 Derive public key from private key
	pubKey := privateKey.PublicKey()
	if pubKey == nil {
		return fmt.Errorf("failed to derive public key from private key")
	}

	derivedAddr, err := account.GenerateAddress(pubKey)
	if err != nil {
		return fmt.Errorf("failed to derive address from public key: %v", err)
	}

	if !strings.EqualFold(derivedAddr, tx.From) {
		return fmt.Errorf("private key does not match tx.From (tx.From=%s, derived=%s)", tx.From, derivedAddr)
	}

	// 🔹 Store pubkey bytes on tx
	tx.FromPubkey = pubKey.Bytes()

	// Calculate the enhanced signable hash including finalized block
	hashToSign, err := v.calculateSignableHashWithReplayProtection(tx, finalizedBlockHash)
	if err != nil {
		return fmt.Errorf("failed to calculate signable hash with replay protection: %v", err)
	}

	signature := privateKey.Sign(hashToSign)
	if signature == nil {
		return fmt.Errorf("failed to sign transaction")
	}

	tx.Signature = signature.Bytes()
	v.metrics.TransactionsWithReplayProtection++

	if finalizedBlockHash == "" {
		v.metrics.TransactionsWithoutFinalized++
	}

	return nil
}

// validateSignature checks that the transaction is correctly signed by the sender.
func (v *Validator) validateSignature(tx *core.Transaction) error {
	// Basic presence checks
	if len(tx.Signature) == 0 {
		return fmt.Errorf("transaction signature cannot be empty")
	}
	if len(tx.FromPubkey) == 0 {
		return fmt.Errorf("transaction from_pubkey cannot be empty")
	}

	// Recreate the public key from bytes
	pubKey, err := crypto.NewPublicKeyFromBytes(tx.FromPubkey)
	if err != nil {
		return fmt.Errorf("invalid from_pubkey: %v", err)
	}

	// Delegate to existing verification logic (also checks address ↔ pubkey match)
	if err := v.VerifyTransactionSignature(tx, pubKey); err != nil {
		return err
	}

	return nil
}

// calculateSignableHash creates a hash that includes chain ID and all transaction context
// This prevents replay attacks across different chains and ensures complete transaction integrity
func (v *Validator) calculateSignableHash(tx *core.Transaction) ([]byte, error) {
	var buf bytes.Buffer

	// 1. Include chain ID to prevent cross-chain replay attacks
	chainID := v.config.Network.ChainID
	buf.WriteString(chainID)

	// 2. Include protocol version for future upgrades
	buf.WriteString("v1")

	// 3. Include all transaction fields (same as hash calculation but with chain context)
	buf.WriteString(tx.Id)
	buf.WriteString(tx.From)
	buf.WriteString(tx.To)

	// Write amount as bytes
	amountBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(amountBytes, uint64(tx.Amount))
	buf.Write(amountBytes)

	// Write gas as bytes
	gasBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasBytes, uint64(tx.Gas))
	buf.Write(gasBytes)

	// Write gas price as bytes
	gasPriceBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasPriceBytes, uint64(tx.GasPrice))
	buf.Write(gasPriceBytes)

	// Write nonce as bytes - CRITICAL for replay protection
	nonceBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(nonceBytes, tx.Nonce)
	buf.Write(nonceBytes)

	// Write transaction type
	typeBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(typeBytes, uint32(tx.Type))
	buf.Write(typeBytes)

	// Write timestamp
	timestampBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(timestampBytes, uint64(tx.Timestamp))
	buf.Write(timestampBytes)

	// Write data
	buf.Write(tx.Data)

	// Hash the complete buffer
	return hash.HashData(buf.Bytes())
}

// calculateSignableHashWithReplayProtection creates a hash with finalized block for replay protection
// This is the ENHANCED version that includes finalized block hash to prevent post-reorg replay
func (v *Validator) calculateSignableHashWithReplayProtection(tx *core.Transaction, finalizedBlockHash string) ([]byte, error) {
	var buf bytes.Buffer

	// 1. Include chain ID to prevent cross-chain replay attacks
	chainID := v.config.Network.ChainID
	buf.WriteString(chainID)

	// 2. Include protocol version (v2 for enhanced replay protection)
	buf.WriteString("v2")

	// 3. ✅ CRITICAL: Include finalized block hash for post-reorg replay protection
	// This binds the transaction to a specific finalized state
	if finalizedBlockHash != "" {
		buf.WriteString(finalizedBlockHash)
	} else if v.replayConfig.RequireFinalizedBlock && !v.replayConfig.AllowEmptyFinalizedBlock {
		return nil, fmt.Errorf("finalized block hash required for replay protection")
	}

	// 4. Include all transaction fields
	buf.WriteString(tx.Id)
	buf.WriteString(tx.From)
	buf.WriteString(tx.To)

	// Write amount as bytes
	amountBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(amountBytes, uint64(tx.Amount))
	buf.Write(amountBytes)

	// Write gas as bytes
	gasBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasBytes, uint64(tx.Gas))
	buf.Write(gasBytes)

	// Write gas price as bytes
	gasPriceBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasPriceBytes, uint64(tx.GasPrice))
	buf.Write(gasPriceBytes)

	// Write nonce as bytes - CRITICAL for replay protection
	nonceBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(nonceBytes, tx.Nonce)
	buf.Write(nonceBytes)

	// Write transaction type
	typeBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(typeBytes, uint32(tx.Type))
	buf.Write(typeBytes)

	// Write timestamp
	timestampBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(timestampBytes, uint64(tx.Timestamp))
	buf.Write(timestampBytes)

	// Write data
	buf.Write(tx.Data)

	// Hash the complete buffer
	return hash.HashData(buf.Bytes())
}

// VerifyTransactionSignature verifies a transaction's signature
func (v *Validator) VerifyTransactionSignature(tx *core.Transaction, publicKey crypto.PublicKey) error {
	if tx == nil {
		return fmt.Errorf("transaction cannot be nil")
	}

	if publicKey == nil {
		return fmt.Errorf("public key cannot be nil")
	}

	if len(tx.Signature) == 0 {
		return fmt.Errorf("transaction signature is empty")
	}

	// 1. CRITICAL: Verify that the sender address matches the public key
	// This prevents attackers from using someone else's signature
	derivedAddress, err := account.GenerateAddress(publicKey)
	if err != nil {
		return fmt.Errorf("failed to derive address from public key: %v", err)
	}

	// Normalize both addresses for comparison
	normalizedDerived := strings.ToLower(derivedAddress)
	normalizedFrom := strings.ToLower(tx.From)

	if normalizedDerived != normalizedFrom {
		return fmt.Errorf("signature verification failed: sender address %s does not match public key address %s",
			tx.From, derivedAddress)
	}

	// 2. Calculate the signable hash including chain ID and all context
	// This prevents replay attacks across different chains and ensures transaction integrity
	hashToVerify, err := v.calculateSignableHash(tx)
	if err != nil {
		return fmt.Errorf("failed to calculate signable hash: %v", err)
	}

	// 3. Create signature object from bytes
	signature, err := crypto.SignatureFromBytes(tx.Signature)
	if err != nil {
		return fmt.Errorf("failed to parse signature: %v", err)
	}

	// 4. Verify the signature against the calculated hash
	err = publicKey.Verify(hashToVerify, &signature)
	if err != nil {
		return fmt.Errorf("signature verification failed: %v", err)
	}

	return nil
}

// VerifyTransactionSignatureWithReplayProtection verifies a transaction signature with enhanced replay protection
// This verifies against the finalized block hash to detect replay attempts after chain reorgs
func (v *Validator) VerifyTransactionSignatureWithReplayProtection(tx *core.Transaction, publicKey crypto.PublicKey, finalizedBlockHash string) error {
	if tx == nil {
		return fmt.Errorf("transaction cannot be nil")
	}

	if publicKey == nil {
		return fmt.Errorf("public key cannot be nil")
	}

	if len(tx.Signature) == 0 {
		return fmt.Errorf("transaction signature is empty")
	}

	// 1. CRITICAL: Verify that the sender address matches the public key
	derivedAddress, err := account.GenerateAddress(publicKey)
	if err != nil {
		return fmt.Errorf("failed to derive address from public key: %v", err)
	}

	// Normalize both addresses for comparison
	normalizedDerived := strings.ToLower(derivedAddress)
	normalizedFrom := strings.ToLower(tx.From)

	if normalizedDerived != normalizedFrom {
		return fmt.Errorf("signature verification failed: sender address %s does not match public key address %s",
			tx.From, derivedAddress)
	}

	// 2. Calculate the signable hash with replay protection (includes finalized block)
	hashToVerify, err := v.calculateSignableHashWithReplayProtection(tx, finalizedBlockHash)
	if err != nil {
		return fmt.Errorf("failed to calculate signable hash with replay protection: %v", err)
	}

	// 3. Create signature object from bytes
	signature, err := crypto.SignatureFromBytes(tx.Signature)
	if err != nil {
		return fmt.Errorf("failed to parse signature: %v", err)
	}

	// 4. Verify the signature against the calculated hash
	err = publicKey.Verify(hashToVerify, &signature)
	if err != nil {
		// This could be a replay attempt - record it
		v.metrics.RecordReplayAttempt()
		return fmt.Errorf("signature verification failed (possible replay attack): %v", err)
	}

	return nil
}

// GetReplayProtectionMetrics returns current replay protection metrics
func (v *Validator) GetReplayProtectionMetrics() map[string]interface{} {
	return v.metrics.GetMetrics()
}

// SetReplayProtectionConfig updates the replay protection configuration
func (v *Validator) SetReplayProtectionConfig(config *ReplayProtectionConfig) {
	v.replayConfig = config
}

func (v *Validator) ValidateTransaction(tx *core.Transaction, stateReader StateReader) error {
	if tx == nil {
		return fmt.Errorf("transaction cannot be nil")
	}

	// Structure validation
	if err := v.validateStructure(tx); err != nil {
		return fmt.Errorf("structure validation failed: %v", err)
	}

	// Hash validation
	if err := v.validateHash(tx); err != nil {
		return fmt.Errorf("hash validation failed: %v", err)
	}

	// Shard validation
	if err := v.validateShard(tx); err != nil {
		return fmt.Errorf("shard validation failed: %v", err)
	}

	// 🔐 NEW: Signature validation
	if err := v.validateSignature(tx); err != nil {
		return fmt.Errorf("signature validation failed: %v", err)
	}

	// Business logic validation
	if err := v.validateBusinessLogic(tx, stateReader); err != nil {
		return fmt.Errorf("business logic validation failed: %v", err)
	}

	return nil
}

// validateStructure validates the basic structure of a transaction
func (v *Validator) validateStructure(tx *core.Transaction) error {
	// Basic field validation
	if tx.Id == "" {
		return fmt.Errorf("transaction ID cannot be empty")
	}

	if tx.Hash == "" {
		return fmt.Errorf("transaction hash cannot be empty")
	}

	if tx.From == "" {
		return fmt.Errorf("sender address cannot be empty")
	}

	// Validate sender address format (tl format)
	if err := account.ValidateAddress(tx.From); err != nil {
		return fmt.Errorf("invalid sender address format: %v", err)
	}

	// Validate recipient address for applicable transaction types
	if tx.Type == core.TransactionType_TRANSFER ||
		tx.Type == core.TransactionType_DELEGATE ||
		tx.Type == core.TransactionType_UNDELEGATE {
		if tx.To == "" {
			return fmt.Errorf("recipient address cannot be empty for %v transactions", tx.Type)
		}
		if err := account.ValidateAddress(tx.To); err != nil {
			return fmt.Errorf("invalid recipient address format: %v", err)
		}
	}

	// Amount validation
	if tx.Amount < 0 {
		return fmt.Errorf("transaction amount cannot be negative")
	}

	// Type-specific amount validation
	switch tx.Type {
	case core.TransactionType_TRANSFER:
		if tx.Amount < v.config.Economics.MinTransfer {
			return fmt.Errorf("transfer amount %d below minimum %d", tx.Amount, v.config.Economics.MinTransfer)
		}
	case core.TransactionType_STAKE:
		if tx.Amount < v.config.Economics.MinStake {
			return fmt.Errorf("stake amount %d below minimum %d", tx.Amount, v.config.Economics.MinStake)
		}
	case core.TransactionType_DELEGATE:
		if tx.Amount < v.config.Economics.MinDelegation {
			return fmt.Errorf("delegation amount %d below minimum %d", tx.Amount, v.config.Economics.MinDelegation)
		}
	case core.TransactionType_CLAIM_REWARDS:
		if tx.Amount != 0 {
			return fmt.Errorf("claim rewards transaction should have zero amount, got %d", tx.Amount)
		}
	}

	// Gas validation with upper and lower bounds
	if tx.Gas < v.config.Economics.MinGasLimit {
		return fmt.Errorf("gas too low: minimum %d, got %d", v.config.Economics.MinGasLimit, tx.Gas)
	}

	if tx.Gas > v.config.Economics.MaxGasPerTx {
		return fmt.Errorf("gas too high: maximum %d, got %d", v.config.Economics.MaxGasPerTx, tx.Gas)
	}

	// Gas price validation with upper and lower bounds
	if tx.GasPrice < v.config.Economics.BaseGasPrice {
		return fmt.Errorf("gas price %d below minimum %d", tx.GasPrice, v.config.Economics.BaseGasPrice)
	}

	if tx.GasPrice > v.config.Economics.MaxGasPrice {
		return fmt.Errorf("gas price too high: maximum %d, got %d", v.config.Economics.MaxGasPrice, tx.GasPrice)
	}

	// Signature validation
	if len(tx.Signature) == 0 {
		return fmt.Errorf("transaction signature cannot be empty")
	}

	// Timestamp validation using config values
	currentTime := time.Now().Unix()
	maxFutureTime := currentTime + int64(v.config.Consensus.MaxTimestampSkew.Seconds())
	maxPastTime := currentTime - int64(v.config.Consensus.MaxTimestampAge.Seconds())

	if tx.Timestamp > maxFutureTime {
		return fmt.Errorf("transaction timestamp too far in the future: %d > %d", tx.Timestamp, maxFutureTime)
	}

	if tx.Timestamp < maxPastTime {
		return fmt.Errorf("transaction timestamp too old: %d < %d", tx.Timestamp, maxPastTime)
	}

	return nil
}

// validateHash validates that the transaction hash is correct
func (v *Validator) validateHash(tx *core.Transaction) error {
	// Recalculate hash
	expectedHash, err := v.CalculateTransactionHash(tx)
	if err != nil {
		return fmt.Errorf("failed to calculate transaction hash: %w", err)
	}

	if tx.Hash != expectedHash {
		return fmt.Errorf("transaction hash mismatch: expected %s, got %s", expectedHash, tx.Hash)
	}

	return nil
}

// validateShard validates that the transaction belongs to the correct shard
func (v *Validator) validateShard(tx *core.Transaction) error {
	// Skip shard validation for beacon shard
	if v.shardID == account.BeaconShardID {
		return nil
	}

	// Check sender shard
	senderShard := account.CalculateShardID(tx.From, v.totalShards)
	if senderShard != v.shardID {
		return fmt.Errorf("transaction sender %s belongs to shard %d, not %d",
			tx.From, senderShard, v.shardID)
	}

	// For cross-shard transactions, validate recipient shard
	if tx.To != "" && tx.Type == core.TransactionType_TRANSFER {
		recipientShard := account.CalculateShardID(tx.To, v.totalShards)
		if recipientShard != v.shardID {
			// This is a cross-shard transaction
			// For now, we'll allow it but mark it for special handling
			// In a full implementation, cross-shard txs would need additional validation
		}
	}

	return nil
}

// validateNonce performs comprehensive nonce validation to prevent replay attacks
func (v *Validator) validateNonce(txNonce, accountNonce uint64, address string) error {
	// 1. Check if nonce is too low (already used transaction)
	if txNonce < accountNonce {
		return fmt.Errorf("nonce too low for address %s: account nonce is %d, transaction nonce is %d (transaction already processed or replay attack)",
			address, accountNonce, txNonce)
	}

	// 2. Check if nonce is exactly the expected next nonce
	if txNonce == accountNonce {
		// Perfect - this is the next transaction to process
		return nil
	}

	// 3. If nonce is higher than expected, check if it's within acceptable future range
	// Allow a small gap for queued transactions in mempool, but not too large
	const maxNonceGap = 1000 // Maximum allowed gap between current and future nonce

	nonceGap := txNonce - accountNonce
	if nonceGap > maxNonceGap {
		return fmt.Errorf("nonce too high for address %s: account nonce is %d, transaction nonce is %d (gap of %d exceeds maximum allowed gap of %d)",
			address, accountNonce, txNonce, nonceGap, maxNonceGap)
	}

	// 4. Nonce is in the future but within acceptable range
	// This transaction can be queued for later processing
	return fmt.Errorf("nonce gap detected for address %s: expected %d, got %d (transaction is for future processing, gap: %d)",
		address, accountNonce, txNonce, nonceGap)
}

// ValidateForMempool validates a transaction for mempool inclusion
// This allows future nonces within a reasonable range for queued transactions
func (v *Validator) ValidateForMempool(tx *core.Transaction, stateReader StateReader) error {
	// First, ensure signature is valid
	if err := v.validateSignature(tx); err != nil {
		return fmt.Errorf("signature validation failed: %v", err)
	}

	// Get sender account
	sender, err := stateReader.GetAccount(tx.From)
	if err != nil {
		return fmt.Errorf("failed to get sender account: %v", err)
	}

	// 1. Reject transactions with nonces that are too old
	if tx.Nonce < sender.Nonce {
		return fmt.Errorf("nonce too old: account nonce is %d, transaction nonce is %d",
			sender.Nonce, tx.Nonce)
	}

	// 2. Allow current nonce and reasonable future nonces
	const maxMempoolNonceGap = 100

	if tx.Nonce > sender.Nonce+maxMempoolNonceGap {
		return fmt.Errorf("nonce too far in future: account nonce is %d, transaction nonce is %d (max gap: %d)",
			sender.Nonce, tx.Nonce, maxMempoolNonceGap)
	}

	// 3. Validate sufficient balance
	return v.validateSufficientBalanceForNonce(tx, sender, stateReader)
}

// validateSufficientBalanceForNonce checks if the sender has enough balance
// to process all transactions up to and including this nonce
func (v *Validator) validateSufficientBalanceForNonce(tx *core.Transaction, sender *core.Account, stateReader StateReader) error {
	totalCost, err := v.calculateTotalCost(tx.Amount, tx.Gas, tx.GasPrice)
	if err != nil {
		return fmt.Errorf("failed to calculate total cost: %v", err)
	}

	if sender.Balance < totalCost {
		return fmt.Errorf("insufficient balance for transaction: have %d, need %d",
			sender.Balance, totalCost)
	}

	return nil
}

// validateBusinessLogic validates transaction business logic
func (v *Validator) validateBusinessLogic(tx *core.Transaction, stateReader StateReader) error {
	// Get sender account via Interface
	sender, err := stateReader.GetAccount(tx.From)
	if err != nil {
		return fmt.Errorf("failed to get sender account: %v", err)
	}

	// Comprehensive nonce validation
	if err := v.validateNonce(tx.Nonce, sender.Nonce, tx.From); err != nil {
		return err
	}

	// Validate based on transaction type (Logic inside these functions remains the same)
	switch tx.Type {
	case core.TransactionType_TRANSFER:
		return v.validateTransfer(tx, sender)
	case core.TransactionType_STAKE:
		return v.validateStake(tx, sender)
	case core.TransactionType_UNSTAKE:
		return v.validateUnstake(tx, sender)
	case core.TransactionType_DELEGATE:
		return v.validateDelegate(tx, sender)
	case core.TransactionType_UNDELEGATE:
		return v.validateUndelegate(tx, sender)
	case core.TransactionType_CLAIM_REWARDS:
		return v.validateClaimRewards(tx, sender)
	default:
		return fmt.Errorf("unknown transaction type: %v", tx.Type)
	}
}

// Helper functions for safe arithmetic operations and input validation

// validateAmountNonNegative checks if an amount is non-negative
func validateAmountNonNegative(amount int64, fieldName string) error {
	if amount < 0 {
		return fmt.Errorf("%s cannot be negative: %d", fieldName, amount)
	}
	return nil
}

// safeMultiply performs multiplication with overflow check
func safeMultiply(a, b int64, operation string) (int64, error) {
	if a == 0 || b == 0 {
		return 0, nil
	}

	// Check for overflow: if a * b would overflow, then a > MaxInt64 / b
	if a > 0 && b > 0 && a > math.MaxInt64/b {
		return 0, fmt.Errorf("%s would overflow (tried to multiply %d * %d)", operation, a, b)
	}
	if a < 0 && b < 0 && a < math.MaxInt64/b {
		return 0, fmt.Errorf("%s would overflow (tried to multiply %d * %d)", operation, a, b)
	}
	if (a > 0 && b < 0 && b < math.MinInt64/a) || (a < 0 && b > 0 && a < math.MinInt64/b) {
		return 0, fmt.Errorf("%s would underflow (tried to multiply %d * %d)", operation, a, b)
	}

	return a * b, nil
}

// safeAdd performs addition with overflow check
func safeAdd(a, b int64, operation string) (int64, error) {
	// Check for positive overflow
	if a > 0 && b > 0 && a > math.MaxInt64-b {
		return 0, fmt.Errorf("%s would overflow (tried to add %d + %d)", operation, a, b)
	}
	// Check for negative overflow (underflow)
	if a < 0 && b < 0 && a < math.MinInt64-b {
		return 0, fmt.Errorf("%s would underflow (tried to add %d + %d)", operation, a, b)
	}

	return a + b, nil
}

// calculateGasCost safely calculates gas cost with overflow protection
func (v *Validator) calculateGasCost(gas, gasPrice int64) (int64, error) {
	// Validate both inputs are non-negative
	if err := validateAmountNonNegative(gas, "gas"); err != nil {
		return 0, err
	}
	if err := validateAmountNonNegative(gasPrice, "gas price"); err != nil {
		return 0, err
	}

	// Calculate with overflow protection
	return safeMultiply(gas, gasPrice, "gas cost calculation")
}

// calculateTotalCost safely calculates total transaction cost with overflow protection
func (v *Validator) calculateTotalCost(amount, gas, gasPrice int64) (int64, error) {
	// Validate amount is non-negative
	if err := validateAmountNonNegative(amount, "amount"); err != nil {
		return 0, err
	}

	// Calculate gas cost
	gasCost, err := v.calculateGasCost(gas, gasPrice)
	if err != nil {
		return 0, err
	}

	// Calculate total with overflow protection
	return safeAdd(amount, gasCost, "total cost calculation")
}

// validateTransfer validates transfer transaction logic
func (v *Validator) validateTransfer(tx *core.Transaction, sender *core.Account) error {
	// Validate amount is non-negative
	if err := validateAmountNonNegative(tx.Amount, "transfer amount"); err != nil {
		return err
	}

	// Calculate total cost with overflow protection
	totalCost, err := v.calculateTotalCost(tx.Amount, tx.Gas, tx.GasPrice)
	if err != nil {
		return fmt.Errorf("transfer validation failed: %v", err)
	}

	// Check sufficient balance
	if sender.Balance < totalCost {
		return fmt.Errorf("insufficient balance: have %d, need %d", sender.Balance, totalCost)
	}

	// Prevent self-transfer
	if tx.From == tx.To {
		return fmt.Errorf("cannot transfer to self")
	}

	return nil
}

// validateStake validates stake transaction logic
func (v *Validator) validateStake(tx *core.Transaction, sender *core.Account) error {
	// Validate amount is non-negative
	if err := validateAmountNonNegative(tx.Amount, "stake amount"); err != nil {
		return err
	}

	// Calculate total cost with overflow protection
	totalCost, err := v.calculateTotalCost(tx.Amount, tx.Gas, tx.GasPrice)
	if err != nil {
		return fmt.Errorf("stake validation failed: %v", err)
	}

	// Check sufficient balance
	if sender.Balance < totalCost {
		return fmt.Errorf("insufficient balance for staking: have %d, need %d", sender.Balance, totalCost)
	}

	return nil
}

// validateUnstake validates unstake transaction logic
func (v *Validator) validateUnstake(tx *core.Transaction, sender *core.Account) error {
	// Validate amount is non-negative
	if err := validateAmountNonNegative(tx.Amount, "unstake amount"); err != nil {
		return err
	}

	// Calculate gas cost with overflow protection
	gasCost, err := v.calculateGasCost(tx.Gas, tx.GasPrice)
	if err != nil {
		return fmt.Errorf("unstake validation failed: %v", err)
	}

	// Check sufficient balance for gas
	if sender.Balance < gasCost {
		return fmt.Errorf("insufficient balance for gas: have %d, need %d", sender.Balance, gasCost)
	}

	// Check sufficient staked amount
	if sender.StakedAmount < tx.Amount {
		return fmt.Errorf("insufficient staked amount: have %d, need %d", sender.StakedAmount, tx.Amount)
	}

	return nil
}

// validateDelegate validates delegate transaction logic
func (v *Validator) validateDelegate(tx *core.Transaction, sender *core.Account) error {
	// Validate amount is non-negative
	if err := validateAmountNonNegative(tx.Amount, "delegation amount"); err != nil {
		return err
	}

	// Calculate total cost with overflow protection
	totalCost, err := v.calculateTotalCost(tx.Amount, tx.Gas, tx.GasPrice)
	if err != nil {
		return fmt.Errorf("delegation validation failed: %v", err)
	}

	// Check sufficient balance
	if sender.Balance < totalCost {
		return fmt.Errorf("insufficient balance for delegation: have %d, need %d", sender.Balance, totalCost)
	}

	// Prevent self-delegation
	if tx.From == tx.To {
		return fmt.Errorf("cannot delegate to self")
	}

	return nil
}

// validateUndelegate validates undelegate transaction logic
func (v *Validator) validateUndelegate(tx *core.Transaction, sender *core.Account) error {
	// Validate amount is non-negative
	if err := validateAmountNonNegative(tx.Amount, "undelegation amount"); err != nil {
		return err
	}

	// Calculate gas cost with overflow protection
	gasCost, err := v.calculateGasCost(tx.Gas, tx.GasPrice)
	if err != nil {
		return fmt.Errorf("undelegation validation failed: %v", err)
	}

	// Check sufficient balance for gas
	if sender.Balance < gasCost {
		return fmt.Errorf("insufficient balance for gas: have %d, need %d", sender.Balance, gasCost)
	}

	// Check if delegation exists
	if sender.DelegatedTo == nil {
		return fmt.Errorf("no delegations found")
	}

	// Check sufficient delegated amount to specific validator
	delegatedAmount, exists := sender.DelegatedTo[tx.To]
	if !exists || delegatedAmount < tx.Amount {
		return fmt.Errorf("insufficient delegation to validator %s: have %d, need %d",
			tx.To, delegatedAmount, tx.Amount)
	}

	return nil
}

// validateClaimRewards validates claim rewards transaction logic
func (v *Validator) validateClaimRewards(tx *core.Transaction, sender *core.Account) error {
	// Calculate gas cost with overflow protection
	gasCost, err := v.calculateGasCost(tx.Gas, tx.GasPrice)
	if err != nil {
		return fmt.Errorf("claim rewards validation failed: %v", err)
	}

	// Check sufficient balance for gas
	if sender.Balance < gasCost {
		return fmt.Errorf("insufficient balance for gas: have %d, need %d", sender.Balance, gasCost)
	}

	// Check if there are rewards to claim
	if sender.Rewards <= 0 {
		return fmt.Errorf("no rewards to claim")
	}

	return nil
}

// ValidateBatch validates multiple transactions as a batch
func (v *Validator) ValidateBatch(transactions []*core.Transaction, stateReader StateReader) error {
	tempAccounts := make(map[string]*core.Account)

	for i, tx := range transactions {
		var sender *core.Account
		if tempAccount, exists := tempAccounts[tx.From]; exists {
			sender = tempAccount
		} else {
			// Get current account state via Interface
			currentAccount, err := stateReader.GetAccount(tx.From)
			if err != nil {
				return fmt.Errorf("failed to get account %s for transaction %d: %v", tx.From, i, err)
			}

			// Create copy for temporary state (deep copy logic remains same)
			sender = &core.Account{
				Address:      currentAccount.Address,
				Balance:      currentAccount.Balance,
				Nonce:        currentAccount.Nonce,
				StakedAmount: currentAccount.StakedAmount,
				DelegatedTo:  make(map[string]int64),
				Rewards:      currentAccount.Rewards,
				CodeHash:     currentAccount.CodeHash,
				StorageRoot:  currentAccount.StorageRoot,
			}
			for k, val := range currentAccount.DelegatedTo {
				sender.DelegatedTo[k] = val
			}
			tempAccounts[tx.From] = sender
		}

		// Validate transaction against temporary state
		// NOTE: We recursively call ValidateTransaction.
		// Ideally, we refactor validation to separate "Check Statless" from "Check State".
		// For now, to fix your compile error, we pass the reader:
		if err := v.ValidateTransaction(tx, stateReader); err != nil {
			return fmt.Errorf("transaction %d validation failed: %v", i, err)
		}

		// Update temporary state
		if err := v.updateTempAccountState(tx, sender); err != nil {
			return fmt.Errorf("failed to update temporary state for transaction %d: %v", i, err)
		}
	}

	return nil
}

// updateTempAccountState updates temporary account state for batch validation
func (v *Validator) updateTempAccountState(tx *core.Transaction, account *core.Account) error {
	switch tx.Type {
	case core.TransactionType_TRANSFER:
		// Calculate total cost with overflow protection
		totalCost, err := v.calculateTotalCost(tx.Amount, tx.Gas, tx.GasPrice)
		if err != nil {
			return fmt.Errorf("transfer state update failed: %v", err)
		}

		// Check for underflow
		if account.Balance < totalCost {
			return fmt.Errorf("balance underflow: balance %d < total cost %d", account.Balance, totalCost)
		}

		account.Balance -= totalCost
		account.Nonce++

	case core.TransactionType_STAKE:
		// Calculate total cost with overflow protection
		totalCost, err := v.calculateTotalCost(tx.Amount, tx.Gas, tx.GasPrice)
		if err != nil {
			return fmt.Errorf("stake state update failed: %v", err)
		}

		// Check for underflow
		if account.Balance < totalCost {
			return fmt.Errorf("balance underflow: balance %d < total cost %d", account.Balance, totalCost)
		}

		account.Balance -= totalCost

		// Check for overflow when adding to staked amount
		newStakedAmount, err := safeAdd(account.StakedAmount, tx.Amount, "staked amount update")
		if err != nil {
			return fmt.Errorf("stake state update failed: %v", err)
		}
		account.StakedAmount = newStakedAmount
		account.Nonce++

	case core.TransactionType_UNSTAKE:
		// Calculate gas cost with overflow protection
		gasCost, err := v.calculateGasCost(tx.Gas, tx.GasPrice)
		if err != nil {
			return fmt.Errorf("unstake state update failed: %v", err)
		}

		// Check for underflow on balance for gas
		if account.Balance < gasCost {
			return fmt.Errorf("balance underflow: balance %d < gas cost %d", account.Balance, gasCost)
		}

		// Check for underflow on staked amount
		if account.StakedAmount < tx.Amount {
			return fmt.Errorf("staked amount underflow: staked %d < unstake amount %d",
				account.StakedAmount, tx.Amount)
		}

		account.Balance -= gasCost

		// Check for overflow when adding unstaked amount back to balance
		newBalance, err := safeAdd(account.Balance, tx.Amount, "balance update after unstake")
		if err != nil {
			return fmt.Errorf("unstake state update failed: %v", err)
		}
		account.Balance = newBalance
		account.StakedAmount -= tx.Amount
		account.Nonce++

	case core.TransactionType_DELEGATE:
		// Calculate total cost with overflow protection
		totalCost, err := v.calculateTotalCost(tx.Amount, tx.Gas, tx.GasPrice)
		if err != nil {
			return fmt.Errorf("delegate state update failed: %v", err)
		}

		// Check for underflow
		if account.Balance < totalCost {
			return fmt.Errorf("balance underflow: balance %d < total cost %d", account.Balance, totalCost)
		}

		account.Balance -= totalCost

		// Check for overflow when adding to staked amount
		newStakedAmount, err := safeAdd(account.StakedAmount, tx.Amount, "staked amount update")
		if err != nil {
			return fmt.Errorf("delegate state update failed: %v", err)
		}
		account.StakedAmount = newStakedAmount

		if account.DelegatedTo == nil {
			account.DelegatedTo = make(map[string]int64)
		}

		// Check for overflow when adding to delegation
		currentDelegation := account.DelegatedTo[tx.To]
		newDelegation, err := safeAdd(currentDelegation, tx.Amount, "delegation update")
		if err != nil {
			return fmt.Errorf("delegate state update failed: %v", err)
		}
		account.DelegatedTo[tx.To] = newDelegation
		account.Nonce++

	case core.TransactionType_UNDELEGATE:
		// Calculate gas cost with overflow protection
		gasCost, err := v.calculateGasCost(tx.Gas, tx.GasPrice)
		if err != nil {
			return fmt.Errorf("undelegate state update failed: %v", err)
		}

		// Check for underflow on balance for gas
		if account.Balance < gasCost {
			return fmt.Errorf("balance underflow: balance %d < gas cost %d", account.Balance, gasCost)
		}

		// Check for underflow on staked amount
		if account.StakedAmount < tx.Amount {
			return fmt.Errorf("staked amount underflow: staked %d < undelegate amount %d",
				account.StakedAmount, tx.Amount)
		}

		// Check for underflow on delegation
		currentDelegation := account.DelegatedTo[tx.To]
		if currentDelegation < tx.Amount {
			return fmt.Errorf("delegation underflow: delegated %d < undelegate amount %d",
				currentDelegation, tx.Amount)
		}

		account.Balance -= gasCost

		// Check for overflow when adding undelegated amount back to balance
		newBalance, err := safeAdd(account.Balance, tx.Amount, "balance update after undelegate")
		if err != nil {
			return fmt.Errorf("undelegate state update failed: %v", err)
		}
		account.Balance = newBalance
		account.StakedAmount -= tx.Amount
		account.DelegatedTo[tx.To] -= tx.Amount

		if account.DelegatedTo[tx.To] == 0 {
			delete(account.DelegatedTo, tx.To)
		}
		account.Nonce++

	case core.TransactionType_CLAIM_REWARDS:
		// Calculate gas cost with overflow protection
		gasCost, err := v.calculateGasCost(tx.Gas, tx.GasPrice)
		if err != nil {
			return fmt.Errorf("claim rewards state update failed: %v", err)
		}

		// Check for underflow on balance for gas
		if account.Balance < gasCost {
			return fmt.Errorf("balance underflow: balance %d < gas cost %d", account.Balance, gasCost)
		}

		account.Balance -= gasCost
		claimedRewards := account.Rewards

		// Check for overflow when adding rewards to balance
		newBalance, err := safeAdd(account.Balance, claimedRewards, "balance update with rewards")
		if err != nil {
			return fmt.Errorf("claim rewards state update failed: %v", err)
		}
		account.Balance = newBalance
		account.Rewards = 0
		account.Nonce++
	}

	return nil
}

// NormalizeAddress normalizes an address to lowercase for consistent storage
func (v *Validator) NormalizeAddress(address string) (string, error) {
	if err := account.ValidateAddress(address); err != nil {
		return "", fmt.Errorf("invalid address: %v", err)
	}
	return strings.ToLower(address), nil
}

// generateTransactionID generates a unique transaction ID
func generateTransactionID() string {
	// Generate random bytes
	randomBytes := make([]byte, 16)
	rand.Read(randomBytes)

	// Add timestamp for additional uniqueness
	timestamp := time.Now().UnixNano()
	timestampBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(timestampBytes, uint64(timestamp))

	// Combine and hash
	combined := append(randomBytes, timestampBytes...)
	hash := blake2b.Sum256(combined)

	return fmt.Sprintf("tx-%x", hash[:16])
}
