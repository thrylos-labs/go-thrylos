// Package crypto provides Ethereum-compatible cryptographic operations
// This file contains MetaMask-specific helper functions
package crypto

import (
	"fmt"
	"math/big"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/thrylos-labs/go-thrylos/crypto/address"
)

// ============================================================================
// Address Recovery (Essential for MetaMask)
// ============================================================================

// RecoverAddress recovers the signer's address from a signature and message
// This is essential for MetaMask transaction verification
func RecoverAddress(message []byte, signature []byte) (*address.Address, error) {
	// Hash the message
	hash := ethcrypto.Keccak256(message)

	// Recover public key
	pubKey, err := ethcrypto.SigToPub(hash, signature)
	if err != nil {
		return nil, fmt.Errorf("failed to recover public key: %w", err)
	}

	// Get Ethereum address
	ethAddr := ethcrypto.PubkeyToAddress(*pubKey)

	// Convert to our Address type
	return address.FromEthereumAddress(ethAddr), nil
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

	pubKey, err := ethcrypto.SigToPub(hash, signature)
	if err != nil {
		return nil, fmt.Errorf("failed to recover public key: %w", err)
	}

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
	// Create Ethereum personal message with standard prefix
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(message))
	prefixedMessage := append([]byte(prefix), message...)

	// Hash and sign
	hash := ethcrypto.Keccak256(prefixedMessage)
	return privateKey.SignHash(hash)
}

// VerifyPersonalSign verifies a personal_sign signature
// Returns true if the signature was created by the given address using personal_sign
func VerifyPersonalSign(addr *address.Address, message []byte, signature []byte) (bool, error) {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(message))
	prefixedMessage := append([]byte(prefix), message...)

	hash := ethcrypto.Keccak256(prefixedMessage)
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

// ============================================================================
// EIP-712 Typed Data Signing (MetaMask eth_signTypedData_v4)
// ============================================================================

// SignTypedData signs EIP-712 typed data
// This is what MetaMask's eth_signTypedData_v4 method uses
func SignTypedData(privateKey PrivateKey, typedData apitypes.TypedData) (Signature, error) {
	// Validate typed data structure
	if err := ValidateTypedData(&typedData); err != nil {
		return nil, fmt.Errorf("invalid typed data: %w", err)
	}

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
	rawData := []byte{0x19, 0x01}
	rawData = append(rawData, domainSeparator...)
	rawData = append(rawData, messageHash...)
	finalHash := ethcrypto.Keccak256(rawData)

	// Sign the hash
	return privateKey.SignHash(finalHash)
}

// VerifyTypedData verifies an EIP-712 typed data signature
func VerifyTypedData(addr *address.Address, typedData apitypes.TypedData, signature []byte) (bool, error) {
	// Validate typed data
	if err := ValidateTypedData(&typedData); err != nil {
		return false, fmt.Errorf("invalid typed data: %w", err)
	}

	// Create domain separator
	domainSeparator, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		return false, fmt.Errorf("failed to hash domain: %w", err)
	}

	// Hash the message
	messageHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		return false, fmt.Errorf("failed to hash message: %w", err)
	}

	// Create final hash
	rawData := []byte{0x19, 0x01}
	rawData = append(rawData, domainSeparator...)
	rawData = append(rawData, messageHash...)
	finalHash := ethcrypto.Keccak256(rawData)

	// Recover address from signature
	recoveredAddr, err := RecoverAddressFromHash(finalHash, signature)
	if err != nil {
		return false, err
	}

	return addr.Equal(recoveredAddr), nil
}

// ValidateTypedData validates EIP-712 typed data structure
func ValidateTypedData(typedData *apitypes.TypedData) error {
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
// Transaction Signing Helpers
// ============================================================================

// SignTransaction signs a transaction with EIP-155 replay protection
// This ensures the transaction can only be valid on one chain
func SignTransaction(privateKey PrivateKey, txHash []byte, chainID uint64) (Signature, error) {
	if len(txHash) != 32 {
		return nil, fmt.Errorf("transaction hash must be 32 bytes, got %d", len(txHash))
	}

	// Sign the hash
	sig, err := privateKey.SignHash(txHash)
	if err != nil {
		return nil, err
	}

	// Apply EIP-155 v value: v = CHAIN_ID * 2 + 35 + {0, 1}
	sigBytes := sig.Bytes()
	if len(sigBytes) != 65 {
		return nil, fmt.Errorf("invalid signature length: got %d, expected 65", len(sigBytes))
	}

	// Adjust v value for EIP-155
	v := sigBytes[64]
	if v < 27 {
		v += 27
	}

	// Apply chain ID
	newV := chainID*2 + 35 + uint64(v-27)
	if newV > 255 {
		// For large chain IDs, v value needs special handling
		// This is handled by the transaction encoding layer
		return sig, nil
	}

	sigBytes[64] = byte(newV)
	return SignatureFromBytes(sigBytes)
}

// RecoverTransactionSender recovers the sender address from a signed transaction
func RecoverTransactionSender(txHash []byte, signature []byte, chainID uint64) (*address.Address, error) {
	if len(signature) != 65 {
		return nil, fmt.Errorf("signature must be 65 bytes, got %d", len(signature))
	}

	// Make a copy to avoid modifying original
	sigCopy := make([]byte, 65)
	copy(sigCopy, signature)

	// Extract v value
	v := sigCopy[64]

	// Check for EIP-155 signature
	if v >= 35 {
		// EIP-155: v = CHAIN_ID * 2 + 35 + {0, 1}
		extractedChainID := (uint64(v) - 35) / 2
		if extractedChainID != chainID {
			return nil, fmt.Errorf("chain ID mismatch: expected %d, got %d", chainID, extractedChainID)
		}

		// Convert back to standard v (0 or 1, then add 27)
		sigCopy[64] = byte((uint64(v) - 35) % 2)
		if sigCopy[64] < 27 {
			sigCopy[64] += 27
		}
	}

	return RecoverAddressFromHash(txHash, sigCopy)
}

// ============================================================================
// Utility Functions
// ============================================================================

// CreateEIP155Hash creates a transaction hash with EIP-155 replay protection
// Includes chain ID in the hash to prevent replay attacks across chains
func CreateEIP155Hash(rlpEncodedTx []byte, chainID uint64) []byte {
	// Append chain ID, 0, 0 for EIP-155
	chainIDBig := big.NewInt(int64(chainID))
	combined := append(rlpEncodedTx, chainIDBig.Bytes()...)
	combined = append(combined, 0, 0)

	return ethcrypto.Keccak256(combined)
}

// IsValidSignature checks if a signature is valid (correct length and format)
func IsValidSignature(signature []byte) bool {
	if len(signature) != 65 {
		return false
	}

	// Check v value is valid (27, 28, or EIP-155 format)
	v := signature[64]
	if v < 27 && v > 1 {
		return false
	}

	return true
}

// NormalizeSignatureV normalizes the v value to standard format (27 or 28)
func NormalizeSignatureV(signature []byte) []byte {
	if len(signature) != 65 {
		return signature
	}

	normalized := make([]byte, 65)
	copy(normalized, signature)

	v := normalized[64]
	if v < 27 {
		normalized[64] = v + 27
	} else if v >= 35 {
		// EIP-155 format, extract base v
		normalized[64] = byte((uint64(v)-35)%2) + 27
	}

	return normalized
}

// ExtractChainIDFromSignature extracts the chain ID from an EIP-155 signature
// Returns 0 if not an EIP-155 signature
func ExtractChainIDFromSignature(signature []byte) uint64 {
	if len(signature) != 65 {
		return 0
	}

	v := signature[64]
	if v < 35 {
		// Not an EIP-155 signature
		return 0
	}

	return (uint64(v) - 35) / 2
}

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

// ValidateChainID checks if a chain ID is valid
// Chain IDs must be positive and within safe JavaScript integer range
func ValidateChainID(chainID uint64) error {
	if chainID == 0 {
		return fmt.Errorf("chain ID cannot be zero")
	}

	// JavaScript safe integer limit: 2^53 - 1
	maxSafeInt := uint64(9007199254740991)
	if chainID > maxSafeInt {
		return fmt.Errorf("chain ID %d exceeds JavaScript safe integer limit", chainID)
	}

	return nil
}
