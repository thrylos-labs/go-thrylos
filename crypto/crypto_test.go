// crypto/crypto_test.go
package crypto

import (
	"crypto/rand"
	"testing"

	"github.com/thrylos-labs/go-thrylos/crypto/hash"
)

func TestKeyGeneration(t *testing.T) {
	privKey, err := NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate keys: %v", err)
	}
	if privKey == nil {
		t.Fatalf("NewPrivateKey returned nil")
	}
	t.Logf("Key generation successful. Type: %T, String: %s", privKey, privKey.String())

	// Verify key is valid
	keyBytes := privKey.Bytes()
	if len(keyBytes) != 32 {
		t.Errorf("Private key should be 32 bytes, got %d", len(keyBytes))
	}
}

func TestPublicKey(t *testing.T) {
	privKey, err := NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate keys: %v", err)
	}

	pubKey1 := privKey.PublicKey()
	if pubKey1 == nil {
		t.Fatalf("privKey.PublicKey() returned nil")
	}
	t.Logf("PublicKey1 generated. String: %s", pubKey1.String())

	// Test compressed format (33 bytes)
	compressedBytes := pubKey1.Bytes()
	if len(compressedBytes) != 33 {
		t.Errorf("Compressed public key should be 33 bytes, got %d", len(compressedBytes))
	}

	// Test uncompressed format (65 bytes)
	uncompressedBytes := pubKey1.BytesUncompressed()
	if len(uncompressedBytes) != 65 {
		t.Errorf("Uncompressed public key should be 65 bytes, got %d", len(uncompressedBytes))
	}

	// Marshal and unmarshal (uses compressed format)
	marshaledPubKey, err := pubKey1.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal public key: %v", err)
	}

	pubKey2, err := NewPublicKeyFromBytes(marshaledPubKey)
	if err != nil {
		t.Fatalf("NewPublicKeyFromBytes failed: %v", err)
	}
	if pubKey2 == nil {
		t.Fatalf("NewPublicKeyFromBytes returned nil key")
	}
	t.Logf("PublicKey2 unmarshaled. String: %s", pubKey2.String())

	// Compare using the Equal method (takes PublicKey interface, not pointer)
	if !pubKey1.Equal(pubKey2) {
		t.Errorf("Public keys should be equal after marshal/unmarshal")
		t.Logf("PubKey1 bytes: %x", pubKey1.Bytes())
		t.Logf("PubKey2 bytes: %x", pubKey2.Bytes())
	} else {
		t.Log("Public key equality after marshal/unmarshal verified.")
	}
}

func TestPublicKeyFormats(t *testing.T) {
	privKey, _ := NewPrivateKey()
	pubKey := privKey.PublicKey()

	// Test compressed format
	compressed := pubKey.Bytes()
	if len(compressed) != 33 {
		t.Errorf("Compressed key should be 33 bytes, got %d", len(compressed))
	}
	if compressed[0] != 0x02 && compressed[0] != 0x03 {
		t.Errorf("Compressed key should start with 0x02 or 0x03, got 0x%02x", compressed[0])
	}

	// Test uncompressed format
	uncompressed := pubKey.BytesUncompressed()
	if len(uncompressed) != 65 {
		t.Errorf("Uncompressed key should be 65 bytes, got %d", len(uncompressed))
	}
	if uncompressed[0] != 0x04 {
		t.Errorf("Uncompressed key should start with 0x04, got 0x%02x", uncompressed[0])
	}

	// Test parsing both formats
	pubKey1, err := NewPublicKeyFromBytes(compressed)
	if err != nil {
		t.Fatalf("Failed to parse compressed key: %v", err)
	}

	pubKey2, err := NewPublicKeyFromBytes(uncompressed)
	if err != nil {
		t.Fatalf("Failed to parse uncompressed key: %v", err)
	}

	// Both should produce the same public key
	if !pubKey1.Equal(pubKey2) {
		t.Error("Compressed and uncompressed formats should produce equal keys")
	}
}

func TestSigningAndVerification(t *testing.T) {
	privKey, err := NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}
	pubKey := privKey.PublicKey()
	if pubKey == nil {
		t.Fatalf("Failed to get public key from private key")
	}

	msg := []byte("test message for signing and verification")

	// Sign message - Sign now returns (Signature, error)
	sig, err := privKey.Sign(msg)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}
	if sig == nil {
		t.Fatalf("Sign returned nil signature")
	}
	t.Logf("Signing successful. Signature: %s", sig.String())

	// Verify signature is 65 bytes
	sigBytes := sig.Bytes()
	if len(sigBytes) != 65 {
		t.Errorf("Signature should be 65 bytes, got %d", len(sigBytes))
	}

	// Verify using PublicKey.Verify (takes Signature, returns error)
	err = pubKey.Verify(msg, sig)
	if err != nil {
		t.Errorf("Verification failed using pubKey.Verify: %v", err)
	} else {
		t.Log("Verification successful using pubKey.Verify.")
	}

	// Verify using Signature.Verify (takes PublicKey, returns error)
	err = sig.Verify(pubKey, msg)
	if err != nil {
		t.Errorf("Verification failed using sig.Verify: %v", err)
	} else {
		t.Log("Verification successful using sig.Verify.")
	}

	// Test verification failure with wrong message
	wrongMsg := []byte("this is not the correct message")
	err = pubKey.Verify(wrongMsg, sig)
	if err == nil {
		t.Errorf("Verification SUCCEEDED with wrong message, expected failure")
	} else {
		t.Logf("Verification correctly failed with wrong message: %v", err)
	}

	// Test verification failure with wrong key
	privKeyWrong, _ := NewPrivateKey()
	pubKeyWrong := privKeyWrong.PublicKey()
	if pubKeyWrong == nil {
		t.Fatalf("Failed to get wrong public key")
	}
	err = pubKeyWrong.Verify(msg, sig)
	if err == nil {
		t.Errorf("Verification SUCCEEDED with wrong public key, expected failure")
	} else {
		t.Logf("Verification correctly failed with wrong public key: %v", err)
	}
}

func TestSignHash(t *testing.T) {
	privKey, err := NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}

	msg := []byte("test message")
	hash := hash.Keccak256(msg)

	// Sign the hash directly
	sig, err := privKey.SignHash(hash)
	if err != nil {
		t.Fatalf("SignHash failed: %v", err)
	}

	// Verify using VerifyHash
	pubKey := privKey.PublicKey()
	err = pubKey.VerifyHash(hash, sig)
	if err != nil {
		t.Errorf("VerifyHash failed: %v", err)
	}

	// Verify that Sign(msg) and SignHash(Keccak256(msg)) produce same result
	sig2, err := privKey.Sign(msg)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	if !sig.Equal(sig2) {
		t.Error("Sign(msg) and SignHash(Keccak256(msg)) should produce equal signatures")
	}
}

func TestPublicKeyRecovery(t *testing.T) {
	privKey, _ := NewPrivateKey()
	pubKey := privKey.PublicKey()

	msg := []byte("test message for recovery")
	hash := hash.Keccak256(msg)

	sig, err := privKey.SignHash(hash)
	if err != nil {
		t.Fatalf("SignHash failed: %v", err)
	}

	// Recover public key from signature
	recoveredPubKey, err := RecoverPublicKey(hash, sig)
	if err != nil {
		t.Fatalf("RecoverPublicKey failed: %v", err)
	}

	// Recovered public key should match original
	if !pubKey.Equal(recoveredPubKey) {
		t.Error("Recovered public key doesn't match original")
		t.Logf("Original: %s", pubKey.String())
		t.Logf("Recovered: %s", recoveredPubKey.String())
	}

	// Addresses should also match
	originalAddr := pubKey.Address()
	recoveredAddr := recoveredPubKey.Address()

	if !originalAddr.Equal(recoveredAddr) {
		t.Error("Recovered address doesn't match original")
		t.Logf("Original: %s", originalAddr.String())
		t.Logf("Recovered: %s", recoveredAddr.String())
	}
}

func TestAddressDerivation(t *testing.T) {
	privKey, _ := NewPrivateKey()
	pubKey := privKey.PublicKey()

	// Get address from public key
	addr := pubKey.Address()
	if addr == nil {
		t.Fatal("Address is nil")
	}

	// Verify address is 20 bytes
	addrBytes := addr.Bytes()
	if len(addrBytes) != 20 {
		t.Errorf("Address should be 20 bytes, got %d", len(addrBytes))
	}

	// Verify address string format
	addrStr := addr.String()
	if len(addrStr) != 42 { // "0x" + 40 hex chars
		t.Errorf("Address string should be 42 chars, got %d", len(addrStr))
	}
	if addrStr[:2] != "0x" {
		t.Errorf("Address should start with 0x, got %s", addrStr[:2])
	}

	// Get address from private key directly
	addr2 := privKey.Address()
	if !addr.Equal(addr2) {
		t.Error("Addresses from pubKey and privKey should match")
	}
}

func TestSignatureComparison(t *testing.T) {
	privKey, err := NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}
	msg := []byte("test message for signature comparison")

	sig1, err := privKey.Sign(msg)
	if err != nil {
		t.Fatalf("First Sign failed: %v", err)
	}

	sig2, err := privKey.Sign(msg)
	if err != nil {
		t.Fatalf("Second Sign failed: %v", err)
	}

	// Test self-equality
	if !sig1.Equal(sig1) {
		t.Errorf("Signature should be equal to itself")
	} else {
		t.Log("Signature self-equality verified.")
	}

	// Signatures should be equal (deterministic signing)
	if !sig1.Equal(sig2) {
		t.Logf("Note: Signatures are different (this is OK if using randomized nonces)")
		t.Logf("Sig1 bytes: %x", sig1.Bytes())
		t.Logf("Sig2 bytes: %x", sig2.Bytes())
	} else {
		t.Log("Deterministic signatures verified.")
	}

	// Test inequality with signature of different message
	msg2 := []byte("a different message")
	sig3, err := privKey.Sign(msg2)
	if err != nil {
		t.Fatalf("Third Sign failed: %v", err)
	}

	if sig1.Equal(sig3) {
		t.Errorf("Signatures of different messages should be different")
	} else {
		t.Log("Inequality of signatures from different messages verified.")
	}
}

func TestSignatureNormalization(t *testing.T) {
	privKey, _ := NewPrivateKey()
	msg := []byte("test normalization")

	sig, err := privKey.Sign(msg)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	// go-ethereum produces normalized signatures by default
	if !sig.IsNormalized() {
		t.Error("Signature should be normalized (low-s)")
	}

	// Test normalization function
	normalized := sig.Normalize()
	if !normalized.Equal(sig) {
		t.Error("Already normalized signature should equal its normalized version")
	}

	// Test that normalized signature is valid
	if !normalized.IsValid() {
		t.Error("Normalized signature should be valid")
	}
}

func TestSignatureValidation(t *testing.T) {
	privKey, _ := NewPrivateKey()
	msg := []byte("test validation")

	sig, err := privKey.Sign(msg)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	// Valid signature should pass validation
	if !sig.IsValid() {
		t.Error("Valid signature failed IsValid()")
	}

	// Test signature components
	r := sig.R()
	s := sig.S()
	v := sig.V()

	if r.Sign() <= 0 {
		t.Error("R should be positive")
	}
	if s.Sign() <= 0 {
		t.Error("S should be positive")
	}
	if v != 0 && v != 1 && v != 27 && v != 28 {
		t.Errorf("V should be 0, 1, 27, or 28, got %d", v)
	}

	// Test recovery ID
	recoveryID := sig.RecoveryID()
	if recoveryID > 1 {
		t.Errorf("Recovery ID should be 0 or 1, got %d", recoveryID)
	}
}

func TestEIP155ChainID(t *testing.T) {
	privKey, _ := NewPrivateKey()
	msg := []byte("test EIP-155")
	hash := hash.Keccak256(msg)

	sig, err := privKey.SignHash(hash)
	if err != nil {
		t.Fatalf("SignHash failed: %v", err)
	}

	// Apply chain ID
	chainID := uint64(1) // Ethereum mainnet
	sigWithChainID := sig.WithChainID(chainID)

	// Extract chain ID
	extractedChainID, hasChainID := sigWithChainID.ExtractChainID()
	if !hasChainID {
		t.Error("Signature should have chain ID")
	}
	if extractedChainID != chainID {
		t.Errorf("Expected chain ID %d, got %d", chainID, extractedChainID)
	}

	// Original signature should not have chain ID
	_, originalHasChainID := sig.ExtractChainID()
	if originalHasChainID {
		t.Log("Note: Original signature has chain ID (go-ethereum might add it)")
	}
}

func TestKeyMarshaling(t *testing.T) {
	privKey1, err := NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}
	pubKey1 := privKey1.PublicKey()
	if pubKey1 == nil {
		t.Fatalf("PublicKey was nil")
	}

	// Marshal/Unmarshal Private Key
	marshaledPriv, err := privKey1.Marshal()
	if err != nil {
		t.Fatalf("privKey1.Marshal() failed: %v", err)
	}

	privKey2, err := NewPrivateKeyFromBytes(marshaledPriv)
	if err != nil {
		t.Fatalf("NewPrivateKeyFromBytes failed: %v", err)
	}
	if privKey2 == nil {
		t.Fatalf("NewPrivateKeyFromBytes returned nil")
	}

	// Compare Private Keys (Equal takes PrivateKey interface, not pointer)
	if !privKey1.Equal(privKey2) {
		t.Errorf("Private keys should be equal after marshal/unmarshal")
		t.Logf("PrivKey1 bytes: %x", privKey1.Bytes())
		t.Logf("PrivKey2 bytes: %x", privKey2.Bytes())
	} else {
		t.Log("Private key marshal/unmarshal successful.")
	}

	// Marshal/Unmarshal Public Key
	marshaledPub, err := pubKey1.Marshal()
	if err != nil {
		t.Fatalf("pubKey1.Marshal() failed: %v", err)
	}

	pubKey2, err := NewPublicKeyFromBytes(marshaledPub)
	if err != nil {
		t.Fatalf("NewPublicKeyFromBytes failed: %v", err)
	}
	if pubKey2 == nil {
		t.Fatalf("NewPublicKeyFromBytes returned nil")
	}

	// Compare Public Keys (Equal takes PublicKey interface, not pointer)
	if !pubKey1.Equal(pubKey2) {
		t.Errorf("Public keys should be equal after marshal/unmarshal")
		t.Logf("PubKey1 bytes: %x", pubKey1.Bytes())
		t.Logf("PubKey2 bytes: %x", pubKey2.Bytes())
	} else {
		t.Log("Public key marshal/unmarshal successful.")
	}
}

func TestSignatureMarshaling(t *testing.T) {
	privKey, _ := NewPrivateKey()
	msg := []byte("message for signature marshal test")

	sig1, err := privKey.Sign(msg)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	marshaledSig, err := sig1.Marshal()
	if err != nil {
		t.Fatalf("sig1.Marshal() failed: %v", err)
	}

	// Unmarshal into a new signature
	sig2, err := NewSignatureFromBytes(marshaledSig)
	if err != nil {
		t.Fatalf("NewSignatureFromBytes failed: %v", err)
	}

	// Compare signatures (Equal takes Signature interface)
	if !sig1.Equal(sig2) {
		t.Errorf("Signature objects mismatch after marshal/unmarshal")
		t.Logf("Sig1: %s, Bytes: %x", sig1.String(), sig1.Bytes())
		t.Logf("Sig2: %s, Bytes: %x", sig2.String(), sig2.Bytes())
	} else {
		t.Log("Signature objects equal after marshal/unmarshal.")
	}
}

func TestSignatureClone(t *testing.T) {
	privKey, _ := NewPrivateKey()
	msg := []byte("test clone")

	sig1, err := privKey.Sign(msg)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	sig2 := sig1.Clone()

	// Should be equal
	if !sig1.Equal(sig2) {
		t.Error("Cloned signature should equal original")
	}

	// Should be independent copies
	sig1Bytes := sig1.Bytes()
	sig2Bytes := sig2.Bytes()

	// Modify one shouldn't affect the other (they're copies)
	if &sig1Bytes[0] == &sig2Bytes[0] {
		t.Error("Clone should create independent copy")
	}
}

// Benchmarks
func BenchmarkKeyGeneration(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = NewPrivateKey()
	}
}

func BenchmarkSigning(b *testing.B) {
	privKey, _ := NewPrivateKey()
	msg := []byte("benchmark message")
	hash := hash.Keccak256(msg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = privKey.SignHash(hash)
	}
}

func BenchmarkVerification(b *testing.B) {
	privKey, _ := NewPrivateKey()
	pubKey := privKey.PublicKey()
	msg := []byte("benchmark message")
	hash := hash.Keccak256(msg)
	sig, _ := privKey.SignHash(hash)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pubKey.VerifyHash(hash, sig)
	}
}

func BenchmarkPublicKeyRecovery(b *testing.B) {
	privKey, _ := NewPrivateKey()
	msg := []byte("benchmark message")
	hash := hash.Keccak256(msg)
	sig, _ := privKey.SignHash(hash)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = RecoverPublicKey(hash, sig)
	}
}

func BenchmarkAddressDerivation(b *testing.B) {
	privKey, _ := NewPrivateKey()
	pubKey := privKey.PublicKey()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pubKey.Address()
	}
}

func BenchmarkKeccak256(b *testing.B) {
	data := make([]byte, 1024)
	rand.Read(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = hash.Keccak256(data)
	}
}
