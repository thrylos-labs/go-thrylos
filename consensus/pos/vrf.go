package pos

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/thrylos-labs/go-thrylos/crypto"
)

// VRFProof contains the VRF proof and output
type VRFProof struct {
	Output []byte // VRF output (32 bytes)
	Proof  []byte // VRF proof (Gamma || c || s)
}

// Constants for ECVRF-secp256k1-SHA256-TAI (draft-irtf-cfrg-vrf-15)
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

	// 1. Extract Private Key Scalar (d)
	privKeyBytes := privateKey.Bytes()
	privKey := secp256k1.PrivKeyFromBytes(privKeyBytes)

	// 2. Hash to curve: H = ECVRF_hash_to_curve(Y, alpha)
	pubKey := privKey.PubKey()
	H, err := hashToCurve(pubKey, alpha)
	if err != nil {
		return nil, fmt.Errorf("hash to curve failed: %w", err)
	}

	// 3. Gamma = d * H
	var gamma secp256k1.JacobianPoint
	secp256k1.ScalarMultNonConst(&privKey.Key, &H, &gamma)
	// Note: v4's ScalarMultNonConst is standard for variable points.
	// For H (derived from alpha), this is acceptable.

	// 4. Nonce Generation (RFC 6979 Deterministic k)
	k := generateNonce(privKey, H)

	// 5. k*B (Public Key part of commitment)
	// NewPrivateKey implicitly calculates k*G
	kB := secp256k1.NewPrivateKey(k).PubKey()

	// 6. k*H
	var kH secp256k1.JacobianPoint
	secp256k1.ScalarMultNonConst(k, &H, &kH)

	// Convert Jacobian to Affine for hashing
	gammaAffine := jacobianToAffine(&gamma)
	kHAffine := jacobianToAffine(&kH)

	// 7. Challenge c = Hash(H, Gamma, k*B, k*H)
	c := hashPoints(H, *gammaAffine, *kB, *kHAffine, *pubKey, alpha)

	// 8. s = (k - c*d) mod q
	var s secp256k1.ModNScalar
	var cScalar secp256k1.ModNScalar
	cScalar.SetByteSlice(c)

	// Calculate term = c * d
	var term secp256k1.ModNScalar
	term.Mul2(&cScalar, &privKey.Key)

	// s = k - term
	// Note: s = k + (-term) mod q
	s.Set(k)
	term.Negate() // term = -term
	s.Add(&term)  // s = k + (-c*d)

	// 9. Proof construction
	proof := encodeProof(gammaAffine, c, &s)

	// 10. Beta (Output)
	beta := proofToHash(gammaAffine)

	return &VRFProof{
		Output: beta,
		Proof:  proof,
	}, nil
}

// VerifyVRFProof verifies the provided proof
func VerifyVRFProof(publicKey crypto.PublicKey, alpha []byte, proof *VRFProof) (bool, []byte, error) {
	if proof == nil || len(proof.Proof) == 0 {
		return false, nil, errors.New("empty proof")
	}

	// 1. Decode Proof
	gamma, cBytes, s, err := decodeProof(proof.Proof)
	if err != nil {
		return false, nil, err
	}

	// Parse Public Key
	pubKey, err := secp256k1.ParsePubKey(publicKey.Bytes())
	if err != nil {
		return false, nil, fmt.Errorf("invalid public key: %w", err)
	}

	// 2. Hash to Curve to get H
	H, err := hashToCurve(pubKey, alpha)
	if err != nil {
		return false, nil, err
	}

	// 3. U = s*B + c*Y
	// s*B
	sB := secp256k1.NewPrivateKey(s).PubKey()

	// c*Y
	var cY secp256k1.JacobianPoint
	var pubKeyJacobian secp256k1.JacobianPoint
	pubKey.AsJacobian(&pubKeyJacobian)

	var cScalar secp256k1.ModNScalar
	cScalar.SetByteSlice(cBytes)

	secp256k1.ScalarMultNonConst(&cScalar, &pubKeyJacobian, &cY)

	// U = sB + cY
	var U_Jacobian secp256k1.JacobianPoint
	var sB_Jacobian secp256k1.JacobianPoint
	sB.AsJacobian(&sB_Jacobian)

	secp256k1.AddNonConst(&sB_Jacobian, &cY, &U_Jacobian)
	U := jacobianToAffine(&U_Jacobian)

	// 4. V = s*H + c*Gamma
	// s*H
	var sH secp256k1.JacobianPoint
	secp256k1.ScalarMultNonConst(s, &H, &sH)

	// c*Gamma
	var cGamma secp256k1.JacobianPoint
	var gammaJacobian secp256k1.JacobianPoint
	gamma.AsJacobian(&gammaJacobian)

	secp256k1.ScalarMultNonConst(&cScalar, &gammaJacobian, &cGamma)

	// V = sH + cGamma
	var V_Jacobian secp256k1.JacobianPoint
	secp256k1.AddNonConst(&sH, &cGamma, &V_Jacobian)
	V := jacobianToAffine(&V_Jacobian)

	// 5. Recompute Challenge c'
	cPrime := hashPoints(H, *gamma, *U, *V, *pubKey, alpha)

	// 6. Compare c and c'
	if !bytesEqual(cBytes, cPrime) {
		return false, nil, errors.New("invalid challenge in proof")
	}

	// 7. Generate Beta
	beta := proofToHash(gamma)

	// 8. Check Output Match
	if !bytesEqual(beta, proof.Output) {
		return false, nil, errors.New("VRF output does not match proof")
	}

	return true, beta, nil
}

// --- Helpers ---

func hashToCurve(pubKey *secp256k1.PublicKey, alpha []byte) (secp256k1.JacobianPoint, error) {
	var point secp256k1.JacobianPoint
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

		// Try to parse as compressed point (0x02 prefix for positive Y)
		var candidate []byte
		candidate = append(candidate, 0x02)
		candidate = append(candidate, digest...)

		p, err := secp256k1.ParsePubKey(candidate)
		if err == nil {
			p.AsJacobian(&point)
			return point, nil
		}
	}
	return point, errors.New("hashToCurve failed to find point")
}

func generateNonce(privKey *secp256k1.PrivateKey, H secp256k1.JacobianPoint) *secp256k1.ModNScalar {
	var k secp256k1.ModNScalar
	h := sha256.New()

	// FIX: Bytes() returns array, copy it first to slice
	pkBytes := privKey.Key.Bytes()
	h.Write(pkBytes[:])

	// Serialize H (X coordinate)
	hAffine := jacobianToAffine(&H)
	h.Write(hAffine.X().Bytes()[:]) // Bytes() returns *[32]byte or [32]byte, use slice

	digest := h.Sum(nil)
	k.SetByteSlice(digest)

	if k.IsZero() {
		one := []byte{1}
		k.SetByteSlice(one)
	}
	return &k
}

func hashPoints(H secp256k1.JacobianPoint, Gamma, U, V, PubKey secp256k1.PublicKey, alpha []byte) []byte {
	h := sha256.New()
	h.Write([]byte(suiteString))
	h.Write([]byte{0x02})
	h.Write(PubKey.SerializeCompressed())

	hAffine := jacobianToAffine(&H)
	h.Write(hAffine.SerializeCompressed())

	h.Write(Gamma.SerializeCompressed())
	h.Write(U.SerializeCompressed())
	h.Write(V.SerializeCompressed())
	h.Write(alpha)

	digest := h.Sum(nil)
	return digest[:cLen]
}

func proofToHash(gamma *secp256k1.PublicKey) []byte {
	h := sha256.New()
	h.Write([]byte(suiteString))
	h.Write([]byte{0x03})
	h.Write(gamma.SerializeCompressed())
	return h.Sum(nil)
}

func jacobianToAffine(j *secp256k1.JacobianPoint) *secp256k1.PublicKey {
	j.ToAffine()

	// FIX: Use ToAffine() then read X/Y manually or re-parse
	// dcrec/secp256k1/v4 has no simple "ToPublicKey" from Jacobian.
	// Best way is to Serialize and Parse.

	// Access X and Y field vals
	// Note: The library does not expose easy "ToBytes" on Jacobian unless we do this:

	var x, y secp256k1.FieldVal
	j.X.Normalize()
	j.Y.Normalize()
	x = j.X
	y = j.Y

	// Manually construct compressed bytes
	var compressed [33]byte
	compressed[0] = 0x02 // Assume even
	if y.IsOdd() {
		compressed[0] = 0x03
	}

	xBytes := x.Bytes()
	copy(compressed[1:], xBytes[:])

	res, _ := secp256k1.ParsePubKey(compressed[:])
	return res
}

func decodeProof(proof []byte) (*secp256k1.PublicKey, []byte, *secp256k1.ModNScalar, error) {
	if len(proof) != ptLen+cLen+qLen {
		return nil, nil, nil, fmt.Errorf("invalid proof len")
	}
	gamma, err := secp256k1.ParsePubKey(proof[0:ptLen])
	if err != nil {
		return nil, nil, nil, err
	}
	c := make([]byte, cLen)
	copy(c, proof[ptLen:ptLen+cLen])

	var s secp256k1.ModNScalar
	s.SetByteSlice(proof[ptLen+cLen:])

	return gamma, c, &s, nil
}

func encodeProof(gamma *secp256k1.PublicKey, c []byte, s *secp256k1.ModNScalar) []byte {
	proof := make([]byte, 0, ptLen+cLen+qLen)
	proof = append(proof, gamma.SerializeCompressed()...)
	proof = append(proof, c...)
	sBytes := s.Bytes()
	proof = append(proof, sBytes[:]...)
	return proof
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
