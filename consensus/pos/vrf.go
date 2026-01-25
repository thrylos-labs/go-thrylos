// consensus/pos/vrf.go
package pos

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/crypto/secp256k1"
	"github.com/thrylos-labs/go-thrylos/crypto"
)

// VRFProof contains the VRF proof and output
type VRFProof struct {
	Output []byte // VRF output (32 bytes)
	Proof  []byte // VRF proof (81 bytes for secp256k1)
}

// ECVRF implements IETF draft-irtf-cfrg-vrf-15 (secp256k1-SHA256-TAI variant)
// https://datatracker.ietf.org/doc/html/draft-irtf-cfrg-vrf-15

const (
	// Suite identifier for secp256k1-SHA256-TAI
	suiteString = "ECVRF_secp256k1_SHA256_TAI"

	// Point to octet string conversion
	ptLen = 33 // Compressed point length

	// Challenge length
	cLen = 32

	// Proof length (Gamma + c + s)
	proofLen = 33 + 32 + 32 // 97 bytes
)

// GenerateVRFProof generates a VRF proof using ECVRF
// Input: alpha (message to be hashed)
// Output: beta (VRF output), pi (proof)
func GenerateVRFProof(privateKey crypto.PrivateKey, alpha []byte) (*VRFProof, error) {
	if privateKey == nil {
		return nil, fmt.Errorf("private key cannot be nil")
	}

	// Get ECDSA private key
	ecdsaKey := privateKey.ToECDSA()
	if ecdsaKey == nil {
		return nil, fmt.Errorf("failed to convert to ECDSA key")
	}

	// 1. Hash to curve: H = ECVRF_hash_to_curve(Y, alpha)
	publicKey := privateKey.PublicKey()
	H, err := hashToCurve(publicKey, alpha)
	if err != nil {
		return nil, fmt.Errorf("hash to curve failed: %w", err)
	}

	// 2. Gamma = x*H (where x is the private key scalar)
	gamma := scalarMultPoint(H, ecdsaKey.D)

	// 3. Choose nonce k deterministically
	k, err := generateNonce(ecdsaKey, H)
	if err != nil {
		return nil, fmt.Errorf("nonce generation failed: %w", err)
	}

	// 4. Compute k*B and k*H
	kB := scalarMultBase(k)
	kH := scalarMultPoint(H, k)

	// 5. Challenge c = ECVRF_hash_points(H, Gamma, k*B, k*H)
	c := hashPoints(H, gamma, kB, kH, publicKey, alpha)

	// 6. s = k - c*x (mod order)
	s := computeS(k, c, ecdsaKey.D)

	// 7. Proof pi = point_to_string(Gamma) || int_to_string(c, cLen) || int_to_string(s, qLen)
	proof := encodeProof(gamma, c, s)

	// 8. Output beta = ECVRF_proof_to_hash(pi)
	beta := proofToHash(gamma)

	return &VRFProof{
		Output: beta,
		Proof:  proof,
	}, nil
}

// VerifyVRFProof verifies a VRF proof
// Returns true if proof is valid, and the VRF output
func VerifyVRFProof(publicKey crypto.PublicKey, alpha []byte, proof *VRFProof) (bool, []byte, error) {
	if publicKey == nil {
		return false, nil, fmt.Errorf("public key cannot be nil")
	}

	if proof == nil || len(proof.Proof) != proofLen {
		return false, nil, fmt.Errorf("invalid proof length")
	}

	// 1. Decode proof: (Gamma, c, s) = ECVRF_decode_proof(pi)
	gamma, c, s, err := decodeProof(proof.Proof)
	if err != nil {
		return false, nil, fmt.Errorf("proof decode failed: %w", err)
	}

	// 2. Hash to curve: H = ECVRF_hash_to_curve(Y, alpha)
	H, err := hashToCurve(publicKey, alpha)
	if err != nil {
		return false, nil, fmt.Errorf("hash to curve failed: %w", err)
	}

	// 3. U = s*B + c*Y (where Y is the public key)
	sB := scalarMultBase(s)
	ecdsaPubKey := publicKey.ToECDSA()
	cY := scalarMultPubKey(ecdsaPubKey, c)
	U := addPoints(sB, cY)

	// 4. V = s*H + c*Gamma
	sH := scalarMultPoint(H, s)
	cGamma := scalarMultPoint(gamma, c)
	V := addPoints(sH, cGamma)

	// 5. c' = ECVRF_hash_points(H, Gamma, U, V)
	cPrime := hashPoints(H, gamma, U, V, publicKey, alpha)

	// 6. Check if c == c'
	if c.Cmp(cPrime) != 0 {
		return false, nil, fmt.Errorf("challenge verification failed")
	}

	// 7. Compute beta = ECVRF_proof_to_hash(Gamma)
	beta := proofToHash(gamma)

	// 8. Verify beta matches the claimed output
	if len(proof.Output) != len(beta) {
		return false, nil, fmt.Errorf("output length mismatch")
	}

	for i := range beta {
		if beta[i] != proof.Output[i] {
			return false, nil, fmt.Errorf("output verification failed")
		}
	}

	return true, beta, nil
}

// hashToCurve implements ECVRF_hash_to_curve using try-and-increment
func hashToCurve(publicKey crypto.PublicKey, alpha []byte) (*secp256k1Point, error) {
	curve := secp256k1.S256()

	// Encode public key
	pubKeyBytes := publicKey.Bytes()

	// Domain separation
	suite := []byte(suiteString)

	// Try-and-increment to find valid point
	for ctr := 0; ctr < 256; ctr++ {
		// hash_input = suite_string || 0x01 || public_key || alpha || ctr
		hashInput := make([]byte, 0, len(suite)+1+len(pubKeyBytes)+len(alpha)+1)
		hashInput = append(hashInput, suite...)
		hashInput = append(hashInput, 0x01)
		hashInput = append(hashInput, pubKeyBytes...)
		hashInput = append(hashInput, alpha...)
		hashInput = append(hashInput, byte(ctr))

		// Hash
		hash := sha256.Sum256(hashInput)

		// Try to decompress as a point
		// Use 0x02 prefix (even y)
		candidateBytes := make([]byte, 33)
		candidateBytes[0] = 0x02
		copy(candidateBytes[1:], hash[:32])

		x := new(big.Int).SetBytes(hash[:32])

		// Check if x is valid (less than field prime)
		if x.Cmp(curve.Params().P) >= 0 {
			continue
		}

		// Try to get y from x
		y := getY(x, curve)
		if y != nil {
			return &secp256k1Point{X: x, Y: y}, nil
		}

		// Try with 0x03 prefix (odd y)
		candidateBytes[0] = 0x03
		y = getY(x, curve)
		if y != nil {
			return &secp256k1Point{X: x, Y: y}, nil
		}
	}

	return nil, fmt.Errorf("hash to curve failed after 256 iterations")
}

// hashPoints implements ECVRF_hash_points for challenge generation
func hashPoints(H, gamma, U, V *secp256k1Point, publicKey crypto.PublicKey, alpha []byte) *big.Int {
	// Encode all points
	suite := []byte(suiteString)

	hashInput := make([]byte, 0, 1024)
	hashInput = append(hashInput, suite...)
	hashInput = append(hashInput, 0x02) // Challenge generation tag
	hashInput = append(hashInput, publicKey.Bytes()...)
	hashInput = append(hashInput, pointToBytes(H)...)
	hashInput = append(hashInput, pointToBytes(gamma)...)
	hashInput = append(hashInput, pointToBytes(U)...)
	hashInput = append(hashInput, pointToBytes(V)...)
	hashInput = append(hashInput, alpha...)

	hash := sha256.Sum256(hashInput)

	// Truncate to cLen and convert to integer
	c := new(big.Int).SetBytes(hash[:cLen])

	// Reduce modulo curve order
	n := secp256k1.S256().Params().N
	c.Mod(c, n)

	return c
}

// proofToHash implements ECVRF_proof_to_hash
func proofToHash(gamma *secp256k1Point) []byte {
	suite := []byte(suiteString)

	hashInput := make([]byte, 0, len(suite)+1+ptLen)
	hashInput = append(hashInput, suite...)
	hashInput = append(hashInput, 0x03) // Proof to hash tag
	hashInput = append(hashInput, pointToBytes(gamma)...)

	hash := sha256.Sum256(hashInput)
	return hash[:]
}

// generateNonce generates deterministic nonce using RFC 6979
func generateNonce(privKey *ecdsa.PrivateKey, H *secp256k1Point) (*big.Int, error) {
	// Use deterministic k generation (RFC 6979)
	// hash_input = private_key || H
	hashInput := make([]byte, 32+ptLen)
	copy(hashInput[0:32], privKey.D.Bytes())
	copy(hashInput[32:], pointToBytes(H))

	hash := sha256.Sum256(hashInput)
	k := new(big.Int).SetBytes(hash[:])

	// Reduce modulo order
	n := secp256k1.S256().Params().N
	k.Mod(k, n)

	// Ensure k is non-zero
	if k.Sign() == 0 {
		k.SetInt64(1)
	}

	return k, nil
}

// computeS computes s = k - c*x (mod order)
func computeS(k, c, x *big.Int) *big.Int {
	n := secp256k1.S256().Params().N

	s := new(big.Int).Mul(c, x)
	s.Sub(k, s)
	s.Mod(s, n)

	return s
}

// encodeProof encodes the VRF proof
func encodeProof(gamma *secp256k1Point, c, s *big.Int) []byte {
	proof := make([]byte, proofLen)

	// Gamma (33 bytes compressed)
	gammaBytes := pointToBytes(gamma)
	copy(proof[0:33], gammaBytes)

	// c (32 bytes)
	cBytes := c.Bytes()
	copy(proof[33+32-len(cBytes):33+32], cBytes)

	// s (32 bytes)
	sBytes := s.Bytes()
	copy(proof[65+32-len(sBytes):65+32], sBytes)

	return proof
}

// decodeProof decodes a VRF proof
func decodeProof(proof []byte) (*secp256k1Point, *big.Int, *big.Int, error) {
	if len(proof) != proofLen {
		return nil, nil, nil, fmt.Errorf("invalid proof length: %d", len(proof))
	}

	// Decode Gamma
	gamma, err := bytesToPoint(proof[0:33])
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to decode gamma: %w", err)
	}

	// Decode c
	c := new(big.Int).SetBytes(proof[33:65])

	// Decode s
	s := new(big.Int).SetBytes(proof[65:97])

	return gamma, c, s, nil
}

// secp256k1Point represents a point on the secp256k1 curve
type secp256k1Point struct {
	X, Y *big.Int
}

// Point operations (simplified - you may want to use a library)
func scalarMultBase(k *big.Int) *secp256k1Point {
	curve := secp256k1.S256()
	x, y := curve.ScalarBaseMult(k.Bytes())
	return &secp256k1Point{X: x, Y: y}
}

func scalarMultPoint(p *secp256k1Point, k *big.Int) *secp256k1Point {
	curve := secp256k1.S256()
	x, y := curve.ScalarMult(p.X, p.Y, k.Bytes())
	return &secp256k1Point{X: x, Y: y}
}

func scalarMultPubKey(pubKey *ecdsa.PublicKey, k *big.Int) *secp256k1Point {
	curve := secp256k1.S256()
	x, y := curve.ScalarMult(pubKey.X, pubKey.Y, k.Bytes())
	return &secp256k1Point{X: x, Y: y}
}

func addPoints(p1, p2 *secp256k1Point) *secp256k1Point {
	curve := secp256k1.S256()
	x, y := curve.Add(p1.X, p1.Y, p2.X, p2.Y)
	return &secp256k1Point{X: x, Y: y}
}

func pointToBytes(p *secp256k1Point) []byte {
	// Compressed point encoding
	bytes := make([]byte, 33)

	// Determine prefix based on y coordinate parity
	if p.Y.Bit(0) == 0 {
		bytes[0] = 0x02
	} else {
		bytes[0] = 0x03
	}

	// X coordinate
	xBytes := p.X.Bytes()
	copy(bytes[33-len(xBytes):], xBytes)

	return bytes
}

func bytesToPoint(data []byte) (*secp256k1Point, error) {
	if len(data) != 33 {
		return nil, fmt.Errorf("invalid compressed point length")
	}

	curve := secp256k1.S256()
	x := new(big.Int).SetBytes(data[1:33])

	// Get y from x
	y := getY(x, curve)
	if y == nil {
		return nil, fmt.Errorf("invalid point")
	}

	// Check parity matches prefix
	prefix := data[0]
	if (prefix == 0x02 && y.Bit(0) == 1) || (prefix == 0x03 && y.Bit(0) == 0) {
		// Flip y
		y.Sub(curve.Params().P, y)
	}

	return &secp256k1Point{X: x, Y: y}, nil
}

// getY computes y from x on secp256k1: y^2 = x^3 + 7
func getY(x *big.Int, curve *secp256k1.BitCurve) *big.Int {
	// y^2 = x^3 + 7 (mod p)
	x3 := new(big.Int).Mul(x, x)
	x3.Mul(x3, x)

	y2 := new(big.Int).Add(x3, big.NewInt(7))
	y2.Mod(y2, curve.Params().P)

	// Compute square root using Tonelli-Shanks
	y := new(big.Int).ModSqrt(y2, curve.Params().P)
	if y == nil {
		return nil
	}

	// Verify
	ySquared := new(big.Int).Mul(y, y)
	ySquared.Mod(ySquared, curve.Params().P)

	if ySquared.Cmp(y2) != 0 {
		return nil
	}

	return y
}
