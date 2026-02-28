// crypto/ethereum.go
// Package crypto provides Ethereum-compatible cryptographic operations
package crypto

import (
	"fmt"
	"math/big"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"

	"github.com/ethereum/go-ethereum/crypto/secp256k1"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/thrylos-labs/go-thrylos/crypto/address"
	"github.com/thrylos-labs/go-thrylos/crypto/hash"
)

// ============================================================================
// Address Recovery (Essential for MetaMask)
// ============================================================================

// RecoverAddress recovers the signer's address from a signature and message
// This is essential for MetaMask transaction verification
func RecoverAddress(message []byte, signature []byte) (*address.Address, error) {
	// Validate signature
	if len(signature) != 65 {
		return nil, fmt.Errorf("signature must be 65 bytes, got %d", len(signature))
	}

	// Hash the message with Keccak256
	hash := hash.Keccak256(message)

	// Recover address from hash
	return RecoverAddressFromHash(hash, signature)
}

// RecoverAddressFromHash recovers address from a pre-hashed message
// Use this when you already have the Keccak256 hash
func RecoverAddressFromHash(hash []byte, signature []byte) (*address.Address, error) {
	if len(hash) != 32 {
		return nil, fmt.Errorf("hash must be 32 bytes, got %d", len(hash))
	}

	if len(signature) != 65 {
		return nil, fmt.Errorf("signature must be 65 bytes, got %d", len(signature))
	}

	// Recover public key using Secp256k1
	pubKey, err := ethcrypto.SigToPub(hash, signature)
	if err != nil {
		return nil, fmt.Errorf("failed to recover public key: %w", err)
	}

	// Derive Ethereum address from public key
	ethAddr := ethcrypto.PubkeyToAddress(*pubKey)
	return address.FromEthereumAddress(ethAddr), nil
}

// VerifyEthereumSignature verifies an Ethereum signature
// Returns true if the signature was created by the given address
func VerifyEthereumSignature(addr *address.Address, message []byte, signature []byte) (bool, error) {
	recoveredAddr, err := RecoverAddress(message, signature)
	if err != nil {
		return false, err
	}

	return addr.Equal(recoveredAddr), nil
}

// ============================================================================
// Personal Sign (MetaMask personal_sign method)
// ============================================================================

// PersonalSign signs a message with Ethereum's personal_sign format
// Adds "\x19Ethereum Signed Message:\n{len}" prefix
// This is what MetaMask's personal_sign method produces
func PersonalSign(privateKey PrivateKey, message []byte) (Signature, error) {
	if privateKey == nil {
		return nil, fmt.Errorf("private key cannot be nil")
	}

	// Create Ethereum personal message with standard prefix
	prefixedMessage := createPersonalMessagePrefix(message)

	// Hash with Keccak256 and sign
	hash := hash.Keccak256(prefixedMessage)
	return privateKey.SignHash(hash)
}

// VerifyPersonalSign verifies a personal_sign signature
// Returns true if the signature was created by the given address using personal_sign
func VerifyPersonalSign(addr *address.Address, message []byte, signature []byte) (bool, error) {
	if addr == nil {
		return false, fmt.Errorf("address cannot be nil")
	}

	// Create the same prefixed message
	prefixedMessage := createPersonalMessagePrefix(message)

	// Hash with Keccak256
	hash := hash.Keccak256(prefixedMessage)

	// Recover address
	recoveredAddr, err := RecoverAddressFromHash(hash, signature)
	if err != nil {
		return false, err
	}

	return addr.Equal(recoveredAddr), nil
}

// PersonalSignWithString is a convenience function for signing string messages
func PersonalSignWithString(privateKey PrivateKey, message string) (Signature, error) {
	return PersonalSign(privateKey, []byte(message))
}

// VerifyPersonalSignWithString verifies a personal_sign signature for a string message
func VerifyPersonalSignWithString(addr *address.Address, message string, signature []byte) (bool, error) {
	return VerifyPersonalSign(addr, []byte(message), signature)
}

// createPersonalMessagePrefix creates the Ethereum personal message prefix
// Format: "\x19Ethereum Signed Message:\n{length}{message}"
func createPersonalMessagePrefix(message []byte) []byte {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(message))
	return append([]byte(prefix), message...)
}

// ============================================================================
// EIP-712 Typed Data Signing (MetaMask eth_signTypedData_v4)
// ============================================================================

// SignTypedData signs EIP-712 typed data
// This is what MetaMask's eth_signTypedData_v4 method uses
func SignTypedData(privateKey PrivateKey, typedData apitypes.TypedData) (Signature, error) {
	if privateKey == nil {
		return nil, fmt.Errorf("private key cannot be nil")
	}

	// Validate typed data structure
	if err := ValidateTypedData(&typedData); err != nil {
		return nil, fmt.Errorf("invalid typed data: %w", err)
	}

	// Calculate the EIP-712 hash
	hash, err := calculateEIP712Hash(&typedData)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate EIP-712 hash: %w", err)
	}

	// Sign the hash using Secp256k1
	return privateKey.SignHash(hash)
}

// VerifyTypedData verifies an EIP-712 typed data signature
func VerifyTypedData(addr *address.Address, typedData apitypes.TypedData, signature []byte) (bool, error) {
	if addr == nil {
		return false, fmt.Errorf("address cannot be nil")
	}

	// Validate typed data
	if err := ValidateTypedData(&typedData); err != nil {
		return false, fmt.Errorf("invalid typed data: %w", err)
	}

	// Calculate the EIP-712 hash
	hash, err := calculateEIP712Hash(&typedData)
	if err != nil {
		return false, fmt.Errorf("failed to calculate EIP-712 hash: %w", err)
	}

	// Recover address from signature
	recoveredAddr, err := RecoverAddressFromHash(hash, signature)
	if err != nil {
		return false, err
	}

	return addr.Equal(recoveredAddr), nil
}

// calculateEIP712Hash calculates the EIP-712 hash
// Hash = keccak256("\x19\x01" || domainSeparator || messageHash)
func calculateEIP712Hash(typedData *apitypes.TypedData) ([]byte, error) {
	// Create domain separator
	domainSeparator, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		return nil, fmt.Errorf("failed to hash domain: %w", err)
	}

	// Hash the message
	messageHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		return nil, fmt.Errorf("failed to hash message: %w", err)
	}

	// Create final hash: keccak256("\x19\x01" || domainSeparator || messageHash)
	rawData := make([]byte, 2+len(domainSeparator)+len(messageHash))
	rawData[0] = 0x19
	rawData[1] = 0x01
	copy(rawData[2:], domainSeparator)
	copy(rawData[2+len(domainSeparator):], messageHash)

	return hash.Keccak256(rawData), nil
}

// ValidateTypedData validates EIP-712 typed data structure
func ValidateTypedData(typedData *apitypes.TypedData) error {
	if typedData == nil {
		return fmt.Errorf("typed data cannot be nil")
	}

	if typedData.Types == nil {
		return fmt.Errorf("types must be defined")
	}

	if typedData.PrimaryType == "" {
		return fmt.Errorf("primaryType must be defined")
	}

	if _, exists := typedData.Types[typedData.PrimaryType]; !exists {
		return fmt.Errorf("primaryType %s not found in types", typedData.PrimaryType)
	}

	if typedData.Domain.ChainId == nil {
		return fmt.Errorf("domain chainId must be defined")
	}

	return nil
}

// ============================================================================
// Transaction Signing Helpers (EIP-155)
// ============================================================================

// SignTransaction signs a transaction hash.
// Replay protection must already be encoded into txHash by the caller.
func SignTransaction(privateKey PrivateKey, txHash []byte, chainID uint64) (Signature, error) {
	if privateKey == nil {
		return nil, fmt.Errorf("private key cannot be nil")
	}

	if len(txHash) != 32 {
		return nil, fmt.Errorf("transaction hash must be 32 bytes, got %d", len(txHash))
	}

	if err := ValidateChainID(chainID); err != nil {
		return nil, err
	}

	// Sign the hash using Secp256k1
	sig, err := privateKey.SignHash(txHash)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	return sig.Normalize(), nil
}

// RecoverTransactionSender recovers the sender address from a signed transaction
func RecoverTransactionSender(txHash []byte, signature []byte, chainID uint64) (*address.Address, error) {
	if len(txHash) != 32 {
		return nil, fmt.Errorf("transaction hash must be 32 bytes, got %d", len(txHash))
	}

	if len(signature) != 65 {
		return nil, fmt.Errorf("signature must be 65 bytes, got %d", len(signature))
	}

	if err := ValidateChainID(chainID); err != nil {
		return nil, err
	}

	// Parse signature
	sig, err := NewSignatureFromBytes(signature)
	if err != nil {
		return nil, fmt.Errorf("invalid signature: %w", err)
	}

	// Recover public key
	pubKey, err := sig.Recover(txHash)
	if err != nil {
		return nil, fmt.Errorf("failed to recover public key: %w", err)
	}

	// Derive address (no error returned from Address())
	addr := pubKey.Address()
	if addr == nil || addr.IsZero() {
		return nil, fmt.Errorf("recovered address is invalid")
	}

	return addr, nil
}

// VerifyTransactionSignature verifies a transaction signature
func VerifyTransactionSignature(expectedAddr *address.Address, txHash []byte, signature []byte, chainID uint64) (bool, error) {
	recoveredAddr, err := RecoverTransactionSender(txHash, signature, chainID)
	if err != nil {
		return false, err
	}

	return expectedAddr.Equal(recoveredAddr), nil
}

// ============================================================================
// EIP-155 Utilities
// ============================================================================

// CreateEIP155Hash hashes an already-encoded EIP-155 signing payload.
// Callers must supply the correctly serialized transaction fields.
func CreateEIP155Hash(rlpEncodedTx []byte, chainID uint64) []byte {
	if err := ValidateChainID(chainID); err != nil {
		return hash.Keccak256(rlpEncodedTx)
	}
	return hash.Keccak256(rlpEncodedTx)
}

// ValidateChainID checks if a chain ID is valid
// Chain IDs must be positive and within safe JavaScript integer range
func ValidateChainID(chainID uint64) error {
	if chainID == 0 {
		return fmt.Errorf("chain ID cannot be zero")
	}

	// JavaScript safe integer limit: 2^53 - 1
	const maxSafeInt = uint64(9007199254740991)
	if chainID > maxSafeInt {
		return fmt.Errorf("chain ID %d exceeds JavaScript safe integer limit", chainID)
	}

	return nil
}

// ExtractChainIDFromSignature extracts the chain ID from an EIP-155 signature
// Returns (chainID, hasChainID)
func ExtractChainIDFromSignature(signature []byte) (uint64, bool) {
	if len(signature) != 65 {
		return 0, false
	}

	sig, err := NewSignatureFromBytes(signature)
	if err != nil {
		return 0, false
	}

	return sig.ExtractChainID()
}

// ============================================================================
// Signature Utilities
// ============================================================================

// IsValidSignature checks if a signature is valid (correct length and format)
func IsValidSignature(signature []byte) bool {
	if len(signature) != 65 {
		return false
	}

	sig, err := NewSignatureFromBytes(signature)
	if err != nil {
		return false
	}

	return sig.IsValid()
}

// NormalizeSignature ensures the signature is in normalized (low-s) format
// This prevents signature malleability attacks
func NormalizeSignature(signature []byte) ([]byte, error) {
	if len(signature) != 65 {
		return nil, fmt.Errorf("signature must be 65 bytes, got %d", len(signature))
	}

	sig, err := NewSignatureFromBytes(signature)
	if err != nil {
		return nil, fmt.Errorf("invalid signature: %w", err)
	}

	// Normalize to prevent malleability
	normalized := sig.Normalize()
	return normalized.Bytes(), nil
}

// NormalizeSignatureV normalizes the v value to standard format
// Converts recovery ID to standard format (27 or 28)
func NormalizeSignatureV(signature []byte) []byte {
	if len(signature) != 65 {
		return signature
	}

	normalized := make([]byte, 65)
	copy(normalized, signature)

	v := normalized[64]
	if v < 27 {
		// Convert 0/1 to 27/28
		normalized[64] = v + 27
	} else if v >= 35 {
		// EIP-155 format, extract base recovery ID and convert to 27/28
		recoveryID := byte((uint64(v) - 35) % 2)
		normalized[64] = recoveryID + 27
	}

	return normalized
}

// ============================================================================
// Address Comparison Utilities
// ============================================================================

// CompareAddresses compares two addresses for equality
// Handles nil addresses safely
func CompareAddresses(a, b *address.Address) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(b)
}

// ============================================================================
// Cryptographic Validation
// ============================================================================

// ValidatePublicKey validates a public key is on the secp256k1 curve
func ValidatePublicKey(pubKeyBytes []byte) error {
	pubKey, err := NewPublicKeyFromBytes(pubKeyBytes)
	if err != nil {
		return fmt.Errorf("invalid public key format: %w", err)
	}

	if !pubKey.IsOnCurve() {
		return fmt.Errorf("public key is not on secp256k1 curve")
	}

	return nil
}

// ValidatePrivateKey validates a private key is in the valid range
func ValidatePrivateKey(privKeyBytes []byte) error {
	if len(privKeyBytes) != 32 {
		return fmt.Errorf("private key must be 32 bytes, got %d", len(privKeyBytes))
	}

	// Check if key is in valid range [1, n-1]
	d := new(big.Int).SetBytes(privKeyBytes)
	n := secp256k1.S256().Params().N

	if d.Cmp(big.NewInt(0)) <= 0 {
		return fmt.Errorf("private key must be greater than zero")
	}

	if d.Cmp(n) >= 0 {
		return fmt.Errorf("private key must be less than curve order")
	}

	return nil
}

// ============================================================================
// Hash Utilities (All using Keccak256)
// ============================================================================

// Keccak256Hash is a convenience wrapper for Keccak256
func Keccak256Hash(data ...[]byte) []byte {
	if len(data) == 0 {
		return hash.Keccak256([]byte{})
	}
	if len(data) == 1 {
		return hash.Keccak256(data[0])
	}

	// Concatenate all data
	var combined []byte
	for _, d := range data {
		combined = append(combined, d...)
	}
	return hash.Keccak256(combined)
}

// ============================================================================
// REMOVED: All Ed25519 functions
// REMOVED: All Blake2b functions
// REMOVED: Dual-crypto compatibility code
// SIMPLIFIED: All functions now use Secp256k1 + Keccak256 exclusively
// ADDED: Better validation and error handling
// ADDED: Signature normalization helpers
// ADDED: Public/private key validation
// ============================================================================
