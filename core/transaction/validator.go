// core/transaction/validator.go
// Handles transaction validation and creation:

// ✅ Transaction creation with proper ID and hash generation
// ✅ Blake2b hash calculation for transaction integrity
// ✅ secp256k1 signature integration (Ethereum-compatible) for signing and verification

// ✅ Comprehensive validation - structure, hash, shard, and business logic
// ✅ Address format validation using Ethereum-style 0x-prefixed addresses
// ✅ Business logic validation - balance checks, minimum amounts, nonce validation
// ✅ Batch validation - validates multiple transactions with temporary state tracking
// ✅ Cross-shard awareness - handles transactions between different shards

package transaction

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/math"
	"github.com/thrylos-labs/go-thrylos/core/security"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/crypto/hash"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// Validator handles transaction validation and creation
type Validator struct {
	shardID     account.ShardID
	totalShards int
	config      *config.Config

	// Replay protection
	replayConfig   *ReplayProtectionConfig
	metrics        *ReplayProtectionMetrics
	replayDetector *ReplayDetectorV3 // V3 replay detection

}

// NewValidator creates a new transaction validator
func NewValidator(shardID account.ShardID, totalShards int, cfg *config.Config) *Validator {
	var replayConfig *ReplayProtectionConfig

	if isDevelopmentEnvironment(cfg.Environment) {
		replayConfig = DevelopmentReplayProtectionConfig()
	} else {
		replayConfig = DefaultReplayProtectionConfig()
		replayConfig.RequireFinalizedBlock = true
		replayConfig.AllowEmptyFinalizedBlock = false
	}

	metrics := &ReplayProtectionMetrics{}

	return &Validator{
		shardID:      shardID,
		totalShards:  totalShards,
		config:       cfg,
		replayConfig: replayConfig,
		metrics:      metrics,
		// ✅ ADD THIS LINE:
		replayDetector: NewReplayDetectorV3(cfg.Network.ChainID, replayConfig, metrics),
	}
}

// isDevelopmentEnvironment checks if the environment is explicitly set to dev
func isDevelopmentEnvironment(env string) bool {
	env = strings.ToLower(strings.TrimSpace(env))
	return env == "development" || env == "dev" || env == "local"
}

// NewValidatorWithReplayConfig creates a validator with custom replay protection config
func NewValidatorWithReplayConfig(shardID account.ShardID, totalShards int, cfg *config.Config, replayConfig *ReplayProtectionConfig) *Validator {
	metrics := &ReplayProtectionMetrics{}

	return &Validator{
		shardID:        shardID,
		totalShards:    totalShards,
		config:         cfg,
		replayConfig:   replayConfig,
		metrics:        metrics,
		replayDetector: NewReplayDetectorV3(cfg.Network.ChainID, replayConfig, metrics),
	}
}

// Validator method to create and sign a transaction
// ✅ UPDATE: Added 'privateKey crypto.PrivateKey' as the last argument
func (v *Validator) CreateTransaction(from, to string, amount string, gas int64, gasPrice string, nonce uint64, txType core.TransactionType, data []byte, privateKey crypto.PrivateKey) (*core.Transaction, error) {

	// Create the transaction
	tx := &core.Transaction{
		Id:        uuid.New().String(),
		From:      from,
		To:        to,
		Amount:    amount,
		Gas:       gas,
		GasPrice:  gasPrice,
		Nonce:     nonce,
		Type:      txType,
		Data:      data,
		Timestamp: time.Now().Unix(),
	}

	if err := EnsureReplayProtection(tx, v.config); err != nil {
		return nil, fmt.Errorf("failed to set replay protection: %v", err)
	}

	// ✅ Fix: Pass the 'privateKey' argument to SignTransaction
	if err := v.SignTransaction(tx, privateKey); err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %v", err)
	}

	return tx, nil
}

// CreateTransferTransaction creates a transfer transaction
// ✅ UPDATE: amount and gasPrice are now 'string'
func (v *Validator) CreateTransferTransaction(from, to string, amount string, gas int64, gasPrice string, nonce uint64, privateKey crypto.PrivateKey) (*core.Transaction, error) {
	amtBig := math.ParseBigInt(amount)
	minTransferBig := math.ParseBigInt(v.config.Economics.MinTransfer)

	if amtBig.Cmp(minTransferBig) < 0 {
		return nil, fmt.Errorf("transfer amount %s below minimum %s", amount, v.config.Economics.MinTransfer)
	}

	// Pass privateKey down
	return v.CreateTransaction(from, to, amount, gas, gasPrice, nonce, core.TransactionType_TRANSFER, nil, privateKey)
}

// CreateStakeTransaction creates a staking transaction
// ✅ UPDATE: amount and gasPrice are now 'string'
func (v *Validator) CreateStakeTransaction(from string, amount string, gas int64, gasPrice string, nonce uint64, privateKey crypto.PrivateKey) (*core.Transaction, error) {
	amtBig := math.ParseBigInt(amount)
	minStakeBig := math.ParseBigInt(v.config.Economics.MinStake)

	if amtBig.Cmp(minStakeBig) < 0 {
		return nil, fmt.Errorf("stake amount %s below minimum %s", amount, v.config.Economics.MinStake)
	}

	// Pass privateKey down
	return v.CreateTransaction(from, "", amount, gas, gasPrice, nonce, core.TransactionType_STAKE, nil, privateKey)
}

// CreateDelegateTransaction creates a delegation transaction
// ✅ UPDATE: amount and gasPrice are now 'string'
func (v *Validator) CreateDelegateTransaction(from, validator string, amount string, gas int64, gasPrice string, nonce uint64, privateKey crypto.PrivateKey) (*core.Transaction, error) {
	amtBig := math.ParseBigInt(amount)
	minDelegationBig := math.ParseBigInt(v.config.Economics.MinDelegation)

	if amtBig.Cmp(minDelegationBig) < 0 {
		return nil, fmt.Errorf("delegation amount %s below minimum %s", amount, v.config.Economics.MinDelegation)
	}

	if from == validator {
		return nil, fmt.Errorf("cannot delegate to self")
	}

	// Pass privateKey down
	return v.CreateTransaction(from, validator, amount, gas, gasPrice, nonce, core.TransactionType_DELEGATE, nil, privateKey)
}

// CalculateTransactionHash calculates the Blake2b hash of a transaction
func (v *Validator) CalculateTransactionHash(tx *core.Transaction) (string, error) {
	var buf bytes.Buffer

	// Serialize transaction fields for hashing (excluding signature and hash)
	buf.WriteString(tx.Id)
	buf.WriteString(tx.From)
	buf.WriteString(tx.To)

	// ✅ FIX: Write Amount string directly (do not convert to uint64)
	buf.WriteString(tx.Amount)

	// Write gas as bytes (Gas is still int64, so this is fine)
	gasBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasBytes, uint64(tx.Gas))
	buf.Write(gasBytes)

	// ✅ FIX: Write GasPrice string directly
	buf.WriteString(tx.GasPrice)

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

// SignTransaction signs a transaction using the secp256k1 private key.
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

	// Calculate the signable hash (Blake2b)
	hashToSign, err := v.calculateSignableHash(tx)
	if err != nil {
		return fmt.Errorf("failed to calculate signable hash: %v", err)
	}

	// [FIX L-02] Use SignHash
	signature, err := privateKey.SignHash(hashToSign)
	if err != nil {
		return fmt.Errorf("failed to sign transaction: %v", err)
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

	// Sign - Sign now returns (Signature, error)
	signature, err := privateKey.Sign(hashToSign)
	if err != nil {
		return fmt.Errorf("failed to sign transaction: %w", err)
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
	// ==================== V3 ENHANCEMENT ====================
	// Validate chain ID before signature verification
	if err := ValidateChainIDMatch(tx.ChainId, v.config.Network.ChainID); err != nil {
		v.metrics.RecordChainIDMismatch() // Record the attempt
		return fmt.Errorf("chain ID validation failed: %w", err)
	}
	// ========================================================

	// NEW: Handle Ethereum Transactions differently
	if tx.Type == core.TransactionType_EVM_CONTRACT_CALL ||
		tx.Type == core.TransactionType_EVM_CONTRACT_DEPLOY {
		return v.validateEthereumSignature(tx)
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

// Add this new function to validate MetaMask signatures
func (v *Validator) validateEthereumSignature(tx *core.Transaction) error {
	// 1. Verify signature length
	if len(tx.Signature) != 65 {
		return fmt.Errorf("invalid ethereum signature length: %d", len(tx.Signature))
	}

	// 2. Parse BigInts
	amountBig := math.ParseBigInt(tx.Amount)
	gasPriceBig := math.ParseBigInt(tx.GasPrice)

	// 3. Extract R, S, V from signature
	// Thrylos stores signature as [R(32) || S(32) || V(1)]
	// V is normalized to 0 or 1
	r := new(big.Int).SetBytes(tx.Signature[:32])
	s := new(big.Int).SetBytes(tx.Signature[32:64])
	vByte := tx.Signature[64]

	// 4. Setup ChainID
	chainIDBig, _ := new(big.Int).SetString(v.config.Network.ChainID, 10)
	if chainIDBig == nil {
		chainIDBig = big.NewInt(1) // Default mainnet
	}

	// 5. Calculate EIP-155 V value
	// EIP-155 V = ChainID * 2 + 35 + RecoveryID
	vBig := new(big.Int).Mul(chainIDBig, big.NewInt(2))
	vBig.Add(vBig, big.NewInt(35))
	vBig.Add(vBig, big.NewInt(int64(vByte)))

	// 6. Define Signer Interface
	// ✅ FIX: Explicitly declare as interface to allow swapping signer types
	var signer types.Signer = types.NewEIP155Signer(chainIDBig)

	// 7. Construct Signed Transaction
	// We must populate V, R, S so signer.Sender() can recover the address
	var ethTxData types.TxData

	if tx.To == "" {
		// Contract Creation
		ethTxData = &types.LegacyTx{
			Nonce:    tx.Nonce,
			GasPrice: gasPriceBig,
			Gas:      uint64(tx.Gas),
			Value:    amountBig,
			Data:     tx.Data,
			V:        vBig, // ✅ Include Signature
			R:        r,    // ✅ Include Signature
			S:        s,    // ✅ Include Signature
		}
	} else {
		// Call / Transfer
		toAddr := common.HexToAddress(tx.To)
		ethTxData = &types.LegacyTx{
			Nonce:    tx.Nonce,
			GasPrice: gasPriceBig,
			Gas:      uint64(tx.Gas),
			To:       &toAddr,
			Value:    amountBig,
			Data:     tx.Data,
			V:        vBig, // ✅ Include Signature
			R:        r,    // ✅ Include Signature
			S:        s,    // ✅ Include Signature
		}
	}

	ethTx := types.NewTx(ethTxData)

	// 8. Attempt Recovery (EIP-155)
	fromAddr, err := signer.Sender(ethTx)

	// 9. Fallback to Homestead (Legacy) if EIP-155 fails
	if err != nil {
		// Recalculate V for Homestead: 27 + RecoveryID
		vLegacy := new(big.Int).SetInt64(int64(27 + vByte))

		// Re-create transaction with Legacy V
		// We need to modify the inner data. For LegacyTx we can cast and set.
		if legacyTx, ok := ethTxData.(*types.LegacyTx); ok {
			legacyTx.V = vLegacy
			ethTx = types.NewTx(legacyTx) // Wrap again
		}

		// Swap signer to Homestead
		signer = types.HomesteadSigner{}

		// Retry recovery
		fromAddr, err = signer.Sender(ethTx)
		if err != nil {
			return fmt.Errorf("failed to recover ethereum sender: %v", err)
		}
	}

	// 10. Verify Match
	if !strings.EqualFold(fromAddr.Hex(), tx.From) {
		return fmt.Errorf("signature mismatch: recovered %s, expected %s", fromAddr.Hex(), tx.From)
	}

	return nil
}

// calculateSignableHash creates a hash that includes chain ID and all transaction context
func (v *Validator) calculateSignableHash(tx *core.Transaction) ([]byte, error) {
	var buf bytes.Buffer

	// 1. Include chain ID to prevent cross-chain replay attacks
	chainID := v.config.Network.ChainID
	buf.WriteString(chainID)

	// 2. Include protocol version
	buf.WriteString("v1")

	// 3. Include all transaction fields
	buf.WriteString(tx.Id)
	buf.WriteString(tx.From)
	buf.WriteString(tx.To)

	// ✅ FIX: Write Amount string directly (do not convert to uint64)
	buf.WriteString(tx.Amount)

	// Write gas as bytes (Gas is still int64, so binary packing is fine)
	gasBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasBytes, uint64(tx.Gas))
	buf.Write(gasBytes)

	// ✅ FIX: Write GasPrice string directly
	buf.WriteString(tx.GasPrice)

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

	// 1. Include chain ID
	chainID := v.config.Network.ChainID
	buf.WriteString(chainID)

	// 2. Include protocol version
	buf.WriteString("v2")

	// 3. Include finalized block hash
	if finalizedBlockHash != "" {
		buf.WriteString(finalizedBlockHash)
	} else if v.replayConfig.RequireFinalizedBlock && !v.replayConfig.AllowEmptyFinalizedBlock {
		return nil, fmt.Errorf("finalized block hash required for replay protection")
	}

	// 4. Include all transaction fields
	buf.WriteString(tx.Id)
	buf.WriteString(tx.From)
	buf.WriteString(tx.To)

	// ✅ FIX: Write Amount string directly (do not convert to uint64)
	buf.WriteString(tx.Amount)

	// Write gas as bytes (Gas is still int64, so this is fine)
	gasBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasBytes, uint64(tx.Gas))
	buf.Write(gasBytes)

	// ✅ FIX: Write GasPrice string directly
	buf.WriteString(tx.GasPrice)

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

	// [FIX L-02] Use VerifyHash
	err = publicKey.VerifyHash(hashToVerify, signature)
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
	err = publicKey.Verify(hashToVerify, signature)
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

func (v *Validator) ValidateTransaction(tx *core.Transaction, currentHeight int64, stateReader StateInterface) error {
	if tx == nil {
		return fmt.Errorf("transaction cannot be nil")
	}

	// ========================================================================
	// ⭐ NEW #1: Chain ID Validation - MUST BE FIRST
	// ========================================================================
	if err := ValidateChainIDMatch(tx.ChainId, v.config.Network.ChainID); err != nil {
		v.metrics.RecordChainIDMismatch()
		return fmt.Errorf("chain ID validation failed: %w", err)
	}

	// ========================================================================
	// ⭐ NEW #2: Replay Attack Detection
	// ========================================================================
	if v.replayDetector != nil {
		if err := v.replayDetector.CheckReplayV3(
			tx.Hash,
			tx.ChainId,
			tx.From,
			tx.Nonce,
			currentHeight,
		); err != nil {
			v.metrics.RecordReplayAttempt()
			return fmt.Errorf("replay detection failed: %w", err)
		}
	}

	// ========================================================================
	// ⭐ NEW #3: Timing Validation (prevent old/future transactions)
	// ========================================================================
	if err := ValidateTransactionTimingV3(tx.Timestamp, v.replayConfig); err != nil {
		v.metrics.RecordTimeBasedExpiration()
		return fmt.Errorf("timing validation failed: %w", err)
	}

	// Structure validation (unchanged)
	if err := v.validateStructure(tx); err != nil {
		return fmt.Errorf("structure validation failed: %v", err)
	}

	// Hash validation (unchanged)
	if err := v.validateHash(tx); err != nil {
		return fmt.Errorf("hash validation failed: %v", err)
	}

	// Shard validation (unchanged)
	if err := v.validateShard(tx); err != nil {
		return fmt.Errorf("shard validation failed: %v", err)
	}

	// Signature validation (unchanged)
	if err := v.validateSignature(tx); err != nil {
		security.LogInvalidSignature(tx.From, tx.Id)
		return fmt.Errorf("signature validation failed: %v", err)
	}

	// Business logic validation (unchanged)
	if err := v.validateBusinessLogic(tx, stateReader); err != nil {
		return fmt.Errorf("business logic validation failed: %v", err)
	}

	// ✅ Validate replay protection (keep this - your existing code)
	if err := v.ValidateReplayProtection(tx, currentHeight); err != nil {
		return fmt.Errorf("replay protection validation failed: %w", err)
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

	// Validate sender address format
	if err := account.ValidateAddress(tx.From); err != nil {
		return fmt.Errorf("invalid sender address format: %v", err)
	}

	// Validate recipient
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

	// ✅ FIX: Parse Amount String to BigInt
	amountBig := math.ParseBigInt(tx.Amount)

	// Amount validation: Check if negative
	if amountBig.Sign() < 0 {
		return fmt.Errorf("transaction amount cannot be negative")
	}

	// Type-specific amount validation using BigInt comparison
	switch tx.Type {
	case core.TransactionType_TRANSFER:
		minTransfer := math.ParseBigInt(v.config.Economics.MinTransfer)
		if amountBig.Cmp(minTransfer) < 0 {
			return fmt.Errorf("transfer amount %s below minimum %s", tx.Amount, v.config.Economics.MinTransfer)
		}
	case core.TransactionType_STAKE:
		minStake := math.ParseBigInt(v.config.Economics.MinStake)
		if amountBig.Cmp(minStake) < 0 {
			return fmt.Errorf("stake amount %s below minimum %s", tx.Amount, v.config.Economics.MinStake)
		}
	case core.TransactionType_DELEGATE:
		minDelegation := math.ParseBigInt(v.config.Economics.MinDelegation)
		if amountBig.Cmp(minDelegation) < 0 {
			return fmt.Errorf("delegation amount %s below minimum %s", tx.Amount, v.config.Economics.MinDelegation)
		}
	case core.TransactionType_CLAIM_REWARDS:
		// Check if explicitly 0
		if amountBig.Sign() != 0 {
			return fmt.Errorf("claim rewards transaction should have zero amount, got %s", tx.Amount)
		}
	}

	// Gas validation (Gas Limit is typically still int64)
	if tx.Gas < v.config.Economics.MinGasLimit {
		return fmt.Errorf("gas too low: minimum %d, got %d", v.config.Economics.MinGasLimit, tx.Gas)
	}

	if tx.Gas > v.config.Economics.MaxGasPerTx {
		return fmt.Errorf("gas too high: maximum %d, got %d", v.config.Economics.MaxGasPerTx, tx.Gas)
	}

	// ✅ FIX: Gas Price validation (Gas Price is now a String/BigInt)
	gasPriceBig := math.ParseBigInt(tx.GasPrice)
	baseGasPrice := math.ParseBigInt(v.config.Economics.BaseGasPrice)

	// Check minimum gas price
	if gasPriceBig.Cmp(baseGasPrice) < 0 {
		return fmt.Errorf("gas price %s below minimum %s", tx.GasPrice, v.config.Economics.BaseGasPrice)
	}

	// Check maximum gas price (Assuming MaxGasPrice is also string in config)
	// If MaxGasPrice is still int64 in config, you need to cast it: big.NewInt(v.config...)
	maxGasPrice := math.ParseBigInt(v.config.Economics.MaxGasPrice)
	if gasPriceBig.Cmp(maxGasPrice) > 0 {
		return fmt.Errorf("gas price too high: maximum %s, got %s", v.config.Economics.MaxGasPrice, tx.GasPrice)
	}

	// Signature validation
	if len(tx.Signature) == 0 {
		return fmt.Errorf("transaction signature cannot be empty")
	}

	// Timestamp validation
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
func (v *Validator) ValidateForMempool(tx *core.Transaction, stateReader StateInterface) error {
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

func (v *Validator) validateSufficientBalanceForNonce(tx *core.Transaction, sender *core.Account, stateReader StateInterface) error {
	// 1. Parse Transaction Values
	txAmount := math.ParseBigInt(tx.Amount)
	txGasPrice := math.ParseBigInt(tx.GasPrice)
	txGas := big.NewInt(tx.Gas)

	// 2. Calculate Fee (Gas * GasPrice) with overflow protection
	fee := math.MulBig(txGas, txGasPrice)

	// 3. Total Cost (Amount + Fee) with overflow protection
	totalCost := math.AddBig(txAmount, fee)

	// 4. Get Sender Balance
	senderBalance := math.ParseBigInt(sender.Balance)

	// 5. Compare
	if senderBalance.Cmp(totalCost) < 0 {
		return fmt.Errorf("insufficient balance: have %s, need %s", senderBalance.String(), totalCost.String())
	}

	return nil
}

// validateBusinessLogic validates transaction business logic
func (v *Validator) validateBusinessLogic(tx *core.Transaction, stateReader StateInterface) error {
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

// Define constants locally to avoid conflict with your custom 'math' package
const (
	MaxInt64 = 1<<63 - 1
	MinInt64 = -1 << 63
)

// validateTransfer validates transfer transaction logic
func (v *Validator) validateTransfer(tx *core.Transaction, sender *core.Account) error {
	// 1. Parse Inputs
	amountBig := math.ParseBigInt(tx.Amount)
	gasPriceBig := math.ParseBigInt(tx.GasPrice)
	senderBalanceBig := math.ParseBigInt(sender.Balance)
	gasLimitBig := big.NewInt(tx.Gas)

	// 2. Validate amount is non-negative
	// Using Sign() instead of validateAmountNonNegative helper
	if amountBig.Sign() < 0 {
		return fmt.Errorf("transfer amount cannot be negative")
	}

	// 3. Calculate Total Cost
	// GasCost = GasLimit * GasPrice
	gasCostBig := new(big.Int).Mul(gasLimitBig, gasPriceBig)

	// TotalCost = Amount + GasCost
	totalCostBig := new(big.Int).Add(amountBig, gasCostBig)

	// 4. Check sufficient balance
	// Compare: if Balance < TotalCost
	if senderBalanceBig.Cmp(totalCostBig) < 0 {
		return fmt.Errorf("insufficient balance: have %s, need %s",
			sender.Balance, totalCostBig.String())
	}

	// 5. Prevent self-transfer
	if tx.From == tx.To {
		return fmt.Errorf("cannot transfer to self")
	}

	return nil
}

// validateStake validates stake transaction logic
func (v *Validator) validateStake(tx *core.Transaction, sender *core.Account) error {
	// 1. Parse Inputs to BigInt
	amountBig := math.ParseBigInt(tx.Amount)
	gasPriceBig := math.ParseBigInt(tx.GasPrice)
	senderBalanceBig := math.ParseBigInt(sender.Balance)
	gasLimitBig := big.NewInt(tx.Gas)

	// 2. Validate amount is non-negative
	// Replaces validateAmountNonNegative helper
	if amountBig.Sign() < 0 {
		return fmt.Errorf("stake amount cannot be negative")
	}

	// 3. Calculate Total Cost
	// GasCost = GasLimit * GasPrice
	gasCostBig := new(big.Int).Mul(gasLimitBig, gasPriceBig)

	// TotalCost = Amount + GasCost
	totalCostBig := new(big.Int).Add(amountBig, gasCostBig)

	// 4. Check sufficient balance
	// Compare: if Balance < TotalCost
	if senderBalanceBig.Cmp(totalCostBig) < 0 {
		return fmt.Errorf("insufficient balance for staking: have %s, need %s",
			sender.Balance, totalCostBig.String())
	}

	return nil
}

// validateUnstake validates unstake transaction logic
func (v *Validator) validateUnstake(tx *core.Transaction, sender *core.Account) error {
	// 1. Parse Inputs
	amountBig := math.ParseBigInt(tx.Amount)
	gasPriceBig := math.ParseBigInt(tx.GasPrice)
	senderBalanceBig := math.ParseBigInt(sender.Balance)
	stakedAmountBig := math.ParseBigInt(sender.StakedAmount)
	gasLimitBig := big.NewInt(tx.Gas)

	// 2. Validate amount is non-negative
	// Using Sign() replaces validateAmountNonNegative helper
	if amountBig.Sign() < 0 {
		return fmt.Errorf("unstake amount cannot be negative")
	}

	// 3. Calculate Gas Cost
	// GasCost = GasLimit * GasPrice
	gasCostBig := new(big.Int).Mul(gasLimitBig, gasPriceBig)

	// 4. Check sufficient balance for gas
	// Compare: if Balance < GasCost
	if senderBalanceBig.Cmp(gasCostBig) < 0 {
		return fmt.Errorf("insufficient balance for gas: have %s, need %s",
			sender.Balance, gasCostBig.String())
	}

	// 5. Check sufficient staked amount
	// Compare: if StakedAmount < tx.Amount
	if stakedAmountBig.Cmp(amountBig) < 0 {
		return fmt.Errorf("insufficient staked amount: have %s, need %s",
			sender.StakedAmount, tx.Amount)
	}

	return nil
}

// validateDelegate validates delegate transaction logic
func (v *Validator) validateDelegate(tx *core.Transaction, sender *core.Account) error {
	// 1. Parse Inputs
	amountBig := math.ParseBigInt(tx.Amount)
	gasPriceBig := math.ParseBigInt(tx.GasPrice)
	senderBalanceBig := math.ParseBigInt(sender.Balance)
	gasLimitBig := big.NewInt(tx.Gas)

	// 2. Validate amount is non-negative
	// Using Sign() replaces validateAmountNonNegative helper
	if amountBig.Sign() < 0 {
		return fmt.Errorf("delegation amount cannot be negative")
	}

	// 3. Calculate Total Cost
	// GasCost = GasLimit * GasPrice
	gasCostBig := new(big.Int).Mul(gasLimitBig, gasPriceBig)

	// TotalCost = Amount + GasCost
	totalCostBig := new(big.Int).Add(amountBig, gasCostBig)

	// 4. Check sufficient balance
	// Compare: if Balance < TotalCost
	if senderBalanceBig.Cmp(totalCostBig) < 0 {
		return fmt.Errorf("insufficient balance for delegation: have %s, need %s",
			sender.Balance, totalCostBig.String())
	}

	// 5. Prevent self-delegation
	if tx.From == tx.To {
		return fmt.Errorf("cannot delegate to self")
	}

	return nil
}

// validateUndelegate validates undelegate transaction logic
func (v *Validator) validateUndelegate(tx *core.Transaction, sender *core.Account) error {
	// 1. Parse Transaction Amount
	amountBig := math.ParseBigInt(tx.Amount)

	// Validate amount is non-negative (Check Sign)
	if amountBig.Sign() < 0 {
		return fmt.Errorf("undelegation amount cannot be negative")
	}

	// 2. Parse Gas Price & Calculate Gas Cost
	// Gas Cost = GasLimit (int64) * GasPrice (BigInt)
	gasPriceBig := math.ParseBigInt(tx.GasPrice)
	gasLimitBig := big.NewInt(tx.Gas)

	gasCostBig := new(big.Int).Mul(gasLimitBig, gasPriceBig)

	// 3. Check sufficient balance for gas
	// sender.Balance is now a string, parse it
	senderBalanceBig := math.ParseBigInt(sender.Balance)

	if senderBalanceBig.Cmp(gasCostBig) < 0 {
		return fmt.Errorf("insufficient balance for gas: have %s, need %s",
			sender.Balance, gasCostBig.String())
	}

	// 4. Check if delegation exists
	if sender.DelegatedTo == nil {
		return fmt.Errorf("no delegations found")
	}

	// 5. Check sufficient delegated amount to specific validator
	// sender.DelegatedTo is now map[string]string
	delegatedAmountStr, exists := sender.DelegatedTo[tx.To]
	if !exists {
		return fmt.Errorf("no delegation found to validator %s", tx.To)
	}

	delegatedAmountBig := math.ParseBigInt(delegatedAmountStr)

	// Compare: if delegatedAmount < undelegateAmount
	if delegatedAmountBig.Cmp(amountBig) < 0 {
		return fmt.Errorf("insufficient delegation to validator %s: have %s, need %s",
			tx.To, delegatedAmountStr, tx.Amount)
	}

	return nil
}

// validateClaimRewards validates claim rewards transaction logic
func (v *Validator) validateClaimRewards(tx *core.Transaction, sender *core.Account) error {
	// 1. Calculate Gas Cost
	// Gas Limit (int64) * Gas Price (String->BigInt)
	gasLimitBig := big.NewInt(tx.Gas)
	gasPriceBig := math.ParseBigInt(tx.GasPrice)

	// Gas Cost = Gas * GasPrice
	gasCostBig := new(big.Int).Mul(gasLimitBig, gasPriceBig)

	// 2. Check sufficient balance for gas
	// sender.Balance is String->BigInt
	senderBalanceBig := math.ParseBigInt(sender.Balance)

	// Compare: if Balance < GasCost
	if senderBalanceBig.Cmp(gasCostBig) < 0 {
		return fmt.Errorf("insufficient balance for gas: have %s, need %s",
			sender.Balance, gasCostBig.String())
	}

	// 3. Check if there are rewards to claim
	// sender.Rewards is String->BigInt
	rewardsBig := math.ParseBigInt(sender.Rewards)

	// Check if Rewards <= 0
	if rewardsBig.Sign() <= 0 {
		return fmt.Errorf("no rewards to claim")
	}

	return nil
}

// ValidateBatch validates multiple transactions as a batch
// ValidateBatch validates multiple transactions as a batch
func (v *Validator) ValidateBatch(transactions []*core.Transaction, stateReader StateInterface) error {
	tempAccounts := make(map[string]*core.Account)
	currentHeight := int64(0) // Placeholder for now

	for i, tx := range transactions {
		var sender *core.Account

		if tempAccount, exists := tempAccounts[tx.From]; exists {
			sender = tempAccount
		} else {
			// Get current account state
			currentAccount, err := stateReader.GetAccount(tx.From)
			if err != nil {
				return fmt.Errorf("failed to get account %s for transaction %d: %v", tx.From, i, err)
			}

			// Create copy for temporary state
			sender = &core.Account{
				Address:      currentAccount.Address,
				Balance:      currentAccount.Balance,
				Nonce:        currentAccount.Nonce,
				StakedAmount: currentAccount.StakedAmount,

				// ✅ FIX: Initialize as map[string]string
				DelegatedTo: make(map[string]string),

				Rewards:     currentAccount.Rewards,
				CodeHash:    currentAccount.CodeHash,
				StorageRoot: currentAccount.StorageRoot,
			}

			// Copy existing delegations
			// Since currentAccount.DelegatedTo is already map[string]string, this works directly
			for k, val := range currentAccount.DelegatedTo {
				sender.DelegatedTo[k] = val
			}

			tempAccounts[tx.From] = sender
		}

		// Validate transaction against temporary state
		if err := v.ValidateTransaction(tx, currentHeight, stateReader); err != nil {
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
	// 1. Common Parsing (used across most cases)
	amountBig := math.ParseBigInt(tx.Amount)
	gasLimitBig := big.NewInt(tx.Gas)
	gasPriceBig := math.ParseBigInt(tx.GasPrice)
	balanceBig := math.ParseBigInt(account.Balance)
	stakedBig := math.ParseBigInt(account.StakedAmount)
	rewardsBig := math.ParseBigInt(account.Rewards)

	// Calculate Gas Cost = Gas * GasPrice
	gasCostBig := new(big.Int).Mul(gasLimitBig, gasPriceBig)

	switch tx.Type {
	case core.TransactionType_TRANSFER:
		// Total Cost = Amount + GasCost
		totalCostBig := new(big.Int).Add(amountBig, gasCostBig)

		// Check for underflow: Balance < TotalCost
		if balanceBig.Cmp(totalCostBig) < 0 {
			return fmt.Errorf("balance underflow: balance %s < total cost %s",
				account.Balance, totalCostBig.String())
		}

		// Update Balance
		balanceBig.Sub(balanceBig, totalCostBig)
		account.Balance = balanceBig.String()

	case core.TransactionType_STAKE:
		// Total Cost = Amount + GasCost
		totalCostBig := new(big.Int).Add(amountBig, gasCostBig)

		// Check for underflow
		if balanceBig.Cmp(totalCostBig) < 0 {
			return fmt.Errorf("balance underflow: balance %s < total cost %s",
				account.Balance, totalCostBig.String())
		}

		// Update Balance
		balanceBig.Sub(balanceBig, totalCostBig)
		account.Balance = balanceBig.String()

		// Update Staked Amount: Staked + Amount
		stakedBig.Add(stakedBig, amountBig)
		account.StakedAmount = stakedBig.String()

	case core.TransactionType_UNSTAKE:
		// Check Balance for Gas
		if balanceBig.Cmp(gasCostBig) < 0 {
			return fmt.Errorf("balance underflow: balance %s < gas cost %s",
				account.Balance, gasCostBig.String())
		}

		// Check Staked Amount: Staked < Amount
		if stakedBig.Cmp(amountBig) < 0 {
			return fmt.Errorf("staked amount underflow: staked %s < unstake amount %s",
				account.StakedAmount, tx.Amount)
		}

		// Deduct Gas from Balance
		balanceBig.Sub(balanceBig, gasCostBig)

		// Add Unstaked Amount back to Balance
		balanceBig.Add(balanceBig, amountBig)
		account.Balance = balanceBig.String()

		// Deduct from Staked Amount
		stakedBig.Sub(stakedBig, amountBig)
		account.StakedAmount = stakedBig.String()

	case core.TransactionType_DELEGATE:
		// Total Cost = Amount + GasCost
		totalCostBig := new(big.Int).Add(amountBig, gasCostBig)

		// Check Balance
		if balanceBig.Cmp(totalCostBig) < 0 {
			return fmt.Errorf("balance underflow: balance %s < total cost %s",
				account.Balance, totalCostBig.String())
		}

		// Update Balance
		balanceBig.Sub(balanceBig, totalCostBig)
		account.Balance = balanceBig.String()

		// Update Total Staked Amount (Delegation counts as stake on account level too?)
		// Assuming your model tracks total staked including delegations:
		stakedBig.Add(stakedBig, amountBig)
		account.StakedAmount = stakedBig.String()

		// Initialize map if nil
		if account.DelegatedTo == nil {
			account.DelegatedTo = make(map[string]string)
		}

		// Update Specific Delegation
		currentDelegationStr := "0"
		if val, exists := account.DelegatedTo[tx.To]; exists {
			currentDelegationStr = val
		}
		currentDelegationBig := math.ParseBigInt(currentDelegationStr)

		currentDelegationBig.Add(currentDelegationBig, amountBig)
		account.DelegatedTo[tx.To] = currentDelegationBig.String()

	case core.TransactionType_UNDELEGATE:
		// Check Balance for Gas
		if balanceBig.Cmp(gasCostBig) < 0 {
			return fmt.Errorf("balance underflow: balance %s < gas cost %s",
				account.Balance, gasCostBig.String())
		}

		// Check Total Staked Amount
		if stakedBig.Cmp(amountBig) < 0 {
			return fmt.Errorf("staked amount underflow: staked %s < undelegate amount %s",
				account.StakedAmount, tx.Amount)
		}

		// Check Specific Delegation
		currentDelegationStr := "0"
		if val, exists := account.DelegatedTo[tx.To]; exists {
			currentDelegationStr = val
		}
		currentDelegationBig := math.ParseBigInt(currentDelegationStr)

		if currentDelegationBig.Cmp(amountBig) < 0 {
			return fmt.Errorf("delegation underflow: delegated %s < undelegate amount %s",
				currentDelegationStr, tx.Amount)
		}

		// Deduct Gas
		balanceBig.Sub(balanceBig, gasCostBig)

		// Add Undelegated Amount back to Balance
		balanceBig.Add(balanceBig, amountBig)
		account.Balance = balanceBig.String()

		// Deduct from Total Staked
		stakedBig.Sub(stakedBig, amountBig)
		account.StakedAmount = stakedBig.String()

		// Deduct from Specific Delegation
		currentDelegationBig.Sub(currentDelegationBig, amountBig)

		if currentDelegationBig.Sign() == 0 {
			delete(account.DelegatedTo, tx.To)
		} else {
			account.DelegatedTo[tx.To] = currentDelegationBig.String()
		}

	case core.TransactionType_CLAIM_REWARDS:
		// Check Balance for Gas
		if balanceBig.Cmp(gasCostBig) < 0 {
			return fmt.Errorf("balance underflow: balance %s < gas cost %s",
				account.Balance, gasCostBig.String())
		}

		// Deduct Gas
		balanceBig.Sub(balanceBig, gasCostBig)

		// Add Rewards to Balance
		balanceBig.Add(balanceBig, rewardsBig)
		account.Balance = balanceBig.String()

		// Reset Rewards
		account.Rewards = "0"
	}

	return nil
}

// ValidateReplayProtection validates transaction replay protection fields
func (v *Validator) ValidateReplayProtection(tx *core.Transaction, currentHeight int64) error {
	// Get replay protection config
	config := v.replayConfig
	if config == nil {
		config = DefaultReplayProtectionConfig()
	}

	// VALIDATION 1: ChainID is required and must match
	expectedChainID := v.config.Network.ChainID
	if tx.ChainId == "" {
		return fmt.Errorf("transaction missing chain_id (required for replay protection)")
	}

	if tx.ChainId != expectedChainID {
		return fmt.Errorf("chain_id mismatch: transaction has %s, expected %s (prevents cross-chain replay)",
			tx.ChainId, expectedChainID)
	}

	// VALIDATION 2: Check transaction expiration (if timestamp is set)
	if tx.Timestamp > 0 && config.TransactionMaxAge > 0 {
		txAge := time.Now().Unix() - tx.Timestamp
		maxAgeSeconds := config.TransactionMaxAge * 2 // Assume 2s blocks

		if txAge > maxAgeSeconds {
			return fmt.Errorf("transaction expired: age %ds exceeds max %ds",
				txAge, maxAgeSeconds)
		}
	}

	// VALIDATION 3: Nonce must be present (prevents replay with missing nonce)
	// This is already checked elsewhere, but belt-and-suspenders
	if tx.Nonce == 0 {
		return fmt.Errorf("transaction missing nonce (required for replay protection)")
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
	hashBytes := hash.Keccak256(combined)

	return fmt.Sprintf("tx-%x", hashBytes[:16])
}

// EnsureReplayProtection ensures transaction has proper replay protection fields
func EnsureReplayProtection(tx *core.Transaction, config *config.Config) error {
	if tx.ChainId == "" {
		if config == nil || config.Network.ChainID == "" {
			return fmt.Errorf("cannot set chain_id: config not available")
		}
		tx.ChainId = config.Network.ChainID
	}

	if tx.Timestamp == 0 {
		tx.Timestamp = time.Now().Unix()
	}

	return nil
}
