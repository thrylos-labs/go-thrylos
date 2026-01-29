package pos

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/thrylos-labs/go-thrylos/crypto"
)

// VRFProof contains the VRF proof and output
type VRFProof struct {
	Output []byte // VRF output (32 bytes)
	Proof  []byte // VRF proof (Gamma || c || s)
}

// Constants for ECVRF-secp256k1-SHA256-TAI
const (
	suiteString = "ECVRF_secp256k1_SHA256_TAI"
	ptLen       = 33 // Compressed point length
	cLen        = 16 // Challenge length
	qLen        = 32 // Scalar length
)

// GenerateVRFProof generates a VRF proof using constant-time arithmetic.
func GenerateVRFProof(privateKey crypto.PrivateKey, alpha []byte) (*VRFProof, error) {
	if privateKey == nil {
		return nil, errors.New("private key cannot be nil")
	}

	// 1. Extract Private Key
	privKeyBytes := privateKey.Bytes()
	privKey, _ := btcec.PrivKeyFromBytes(privKeyBytes)

	// 2. Hash to curve: H = ECVRF_hash_to_curve(Y, alpha)
	pubKey := privKey.PubKey()
	H, err := hashToCurve(pubKey, alpha)
	if err != nil {
		return nil, fmt.Errorf("hash to curve failed: %w", err)
	}

	// 3. Gamma = d * H
	// Use ScalarMult which returns *FieldVal in v2
	dBytes := privKey.Key.Bytes()
	gammaX, gammaY := btcec.S256().ScalarMult(H.X(), H.Y(), dBytes[:])

	// Construct Gamma via uncompressed bytes to avoid accessing FieldVal internals
	gamma := fieldValsToPubKey(gammaX, gammaY)

	// 4. Nonce Generation
	k := generateNonce(privKey, H)
	kBytes := k.Bytes()

	// 5. k*B
	kBX, kBY := btcec.S256().ScalarBaseMult(kBytes[:])
	kB := fieldValsToPubKey(kBX, kBY)

	// 6. k*H
	kHX, kHY := btcec.S256().ScalarMult(H.X(), H.Y(), kBytes[:])
	kH := fieldValsToPubKey(kHX, kHY)

	// 7. Challenge c
	c := hashPoints(H, gamma, kB, kH, pubKey, alpha)

	// 8. s = (k - c*d) mod q
	var s btcec.ModNScalar
	var kScalar btcec.ModNScalar
	var cScalar btcec.ModNScalar
	var dScalar btcec.ModNScalar

	kScalar.SetByteSlice(kBytes[:])
	cScalar.SetByteSlice(c)
	dScalar.SetByteSlice(dBytes[:])

	var term btcec.ModNScalar
	term.Mul2(&cScalar, &dScalar)
	term.Negate()
	s.Add2(&kScalar, &term)

	// 9. Proof construction
	proof := encodeProof(gamma, c, &s)

	// 10. Beta (Output)
	beta := proofToHash(gamma)

	return &VRFProof{
		Output: beta,
		Proof:  proof,
	}, nil
}

// VerifyVRFProof verifies the provided proof
func VerifyVRFProof(publicKey []byte, alpha []byte, proof *VRFProof) (bool, []byte, error) {
	if proof == nil || len(proof.Proof) == 0 {
		return false, nil, errors.New("empty proof")
	}

	// 1. Decode Proof
	gamma, cBytes, s, err := decodeProof(proof.Proof)
	if err != nil {
		return false, nil, err
	}

	pubKey, err := btcec.ParsePubKey(publicKey)
	if err != nil {
		return false, nil, fmt.Errorf("invalid public key: %w", err)
	}

	// 2. Hash to Curve to get H
	H, err := hashToCurve(pubKey, alpha)
	if err != nil {
		return false, nil, err
	}

	// 3. U = s*B + c*Y
	sBytes := s.Bytes()

	// s*B
	sBX, sBY := btcec.S256().ScalarBaseMult(sBytes[:])

	// c*Y
	cYX, cYY := btcec.S256().ScalarMult(pubKey.X(), pubKey.Y(), cBytes)

	// U = sB + cY
	uX, uY := btcec.S256().Add(sBX, sBY, cYX, cYY)
	U := fieldValsToPubKey(uX, uY)

	// 4. V = s*H + c*Gamma
	// s*H
	sHX, sHY := btcec.S256().ScalarMult(H.X(), H.Y(), sBytes[:])

	// c*Gamma
	cGammaX, cGammaY := btcec.S256().ScalarMult(gamma.X(), gamma.Y(), cBytes)

	// V = sH + cGamma
	vX, vY := btcec.S256().Add(sHX, sHY, cGammaX, cGammaY)
	V := fieldValsToPubKey(vX, vY)

	// 5. Recompute Challenge c'
	cPrime := hashPoints(H, gamma, U, V, pubKey, alpha)

	// 6. Compare
	if !bytes.Equal(cBytes, cPrime) {
		return false, nil, errors.New("invalid challenge in proof")
	}

	// 7. Generate Beta
	beta := proofToHash(gamma)

	// 8. Check Output Match
	if !bytes.Equal(beta, proof.Output) {
		return false, nil, errors.New("VRF output does not match proof")
	}

	return true, beta, nil
}

func fieldValsToPubKey(x, y *big.Int) *btcec.PublicKey {
	// Standard uncompressed serialization: 0x04 || X || Y
	b := make([]byte, 65)
	b[0] = 0x04
	xBytes := x.Bytes()
	yBytes := y.Bytes()

	// Copy X into [1..33], right-aligned
	copy(b[33-len(xBytes):33], xBytes)

	// Copy Y into [33..65], right-aligned
	copy(b[65-len(yBytes):], yBytes)

	key, _ := btcec.ParsePubKey(b)
	return key
}

func hashToCurve(pubKey *btcec.PublicKey, alpha []byte) (*btcec.PublicKey, error) {
	var header = []byte(suiteString)
	var pubKeyBytes = pubKey.SerializeCompressed()

	for ctr := 0; ctr < 256; ctr++ {
		h := sha256.New()
		h.Write(header)
		h.Write([]byte{0x01})
		h.Write(pubKeyBytes)
		h.Write(alpha)
		h.Write([]byte{byte(ctr)})
		digest := h.Sum(nil)

		var candidate []byte
		candidate = append(candidate, 0x02)
		candidate = append(candidate, digest...)

		p, err := btcec.ParsePubKey(candidate)
		if err == nil {
			return p, nil
		}
	}
	return nil, errors.New("hashToCurve failed to find point")
}

func generateNonce(privKey *btcec.PrivateKey, H *btcec.PublicKey) *btcec.ModNScalar {
	var k btcec.ModNScalar
	h := sha256.New()

	pkBytes := privKey.Key.Bytes()
	h.Write(pkBytes[:])

	h.Write(H.SerializeCompressed())

	digest := h.Sum(nil)
	k.SetByteSlice(digest)

	if k.IsZero() {
		one := []byte{1}
		k.SetByteSlice(one)
	}
	return &k
}

func hashPoints(H, Gamma, U, V, PubKey *btcec.PublicKey, alpha []byte) []byte {
	h := sha256.New()
	h.Write([]byte(suiteString))
	h.Write([]byte{0x02})
	h.Write(PubKey.SerializeCompressed())
	h.Write(H.SerializeCompressed())
	h.Write(Gamma.SerializeCompressed())
	h.Write(U.SerializeCompressed())
	h.Write(V.SerializeCompressed())
	h.Write(alpha)

	digest := h.Sum(nil)
	return digest[:cLen]
}

func proofToHash(gamma *btcec.PublicKey) []byte {
	h := sha256.New()
	h.Write([]byte(suiteString))
	h.Write([]byte{0x03})
	h.Write(gamma.SerializeCompressed())
	return h.Sum(nil)
}

func decodeProof(proof []byte) (*btcec.PublicKey, []byte, *btcec.ModNScalar, error) {
	if len(proof) != ptLen+cLen+qLen {
		return nil, nil, nil, fmt.Errorf("invalid proof len")
	}
	gamma, err := btcec.ParsePubKey(proof[0:ptLen])
	if err != nil {
		return nil, nil, nil, err
	}
	c := make([]byte, cLen)
	copy(c, proof[ptLen:ptLen+cLen])

	var s btcec.ModNScalar
	s.SetByteSlice(proof[ptLen+cLen:])

	return gamma, c, &s, nil
}

func encodeProof(gamma *btcec.PublicKey, c []byte, s *btcec.ModNScalar) []byte {
	proof := make([]byte, 0, ptLen+cLen+qLen)
	proof = append(proof, gamma.SerializeCompressed()...)
	proof = append(proof, c...)
	sBytes := s.Bytes()
	proof = append(proof, sBytes[:]...)
	return proof
}
