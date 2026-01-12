# Ed25519 vs secp256k1: MetaMask Compatibility Analysis

## TL;DR - The Answer

**You MUST use secp256k1 for MetaMask compatibility.** Ed25519 will NOT work with MetaMask.

However, you have options to support BOTH cryptographic schemes in your blockchain.

---

## Why MetaMask Requires secp256k1

### 1. Ethereum Standard
MetaMask is built for Ethereum, which uses:
- **Signature Algorithm:** ECDSA with secp256k1 curve
- **Address Derivation:** Keccak256(public_key)[12:] (last 20 bytes)
- **Key Format:** 32-byte private keys, 64-byte uncompressed public keys

### 2. Hardcoded Dependencies
MetaMask's transaction signing is hardcoded to:
```javascript
// MetaMask internally does this:
const signature = secp256k1.sign(messageHash, privateKey)
const address = keccak256(publicKey).slice(-20)
```

There's **no way** to tell MetaMask to use Ed25519 instead.

### 3. Address Format
- **Ethereum/secp256k1:** 20-byte addresses (0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb)
- **Ed25519:** Typically 32-byte addresses or different encoding

MetaMask expects 20-byte Ethereum addresses derived from secp256k1 public keys.

---

## What Happens if You Use Ed25519?

### Scenario: Thrylos uses Ed25519, user tries MetaMask

```
1. User adds Thrylos to MetaMask ✅ (works - just network params)
2. User sends transaction via MetaMask ❌ (FAILS)

Why it fails:
- MetaMask signs with secp256k1
- Creates signature (r, s, v) using secp256k1
- Thrylos tries to verify with Ed25519 → signature verification FAILS
- Transaction rejected
```

### Technical Details

**MetaMask creates:**
```javascript
// Transaction signed by MetaMask
{
  from: "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb",  // secp256k1 address
  signature: {
    r: "0x1b5e...",  // 32 bytes from secp256k1 signature
    s: "0x7ac3...",  // 32 bytes from secp256k1 signature  
    v: 27            // Recovery ID (27 or 28)
  }
}
```

**Your blockchain would need:**
```go
// To verify this signature, you MUST use secp256k1
publicKey := crypto.Ecrecover(txHash, signature)  // secp256k1 recovery
address := crypto.PubkeyToAddress(publicKey)       // Keccak256 hash
```

**Ed25519 cannot:**
- Recover public keys from signatures (no recovery mechanism)
- Verify secp256k1 signatures (different curve math)
- Generate addresses that match Ethereum format

---

## Your Options

### Option 1: Full Migration to secp256k1 (Recommended for MetaMask)

**Pros:**
- ✅ Full MetaMask compatibility
- ✅ Compatible with all Ethereum tools (Ethers.js, Web3.js, Hardhat, etc.)
- ✅ Users can use same keys across Ethereum and Thrylos
- ✅ Simpler codebase (one signature scheme)

**Cons:**
- ❌ Need to migrate existing Ed25519 accounts
- ❌ Lose Ed25519's advantages (smaller signatures, faster verification)

**Implementation:**
```go
// Replace Ed25519 imports:
// import "crypto/ed25519"

// With secp256k1:
import "github.com/ethereum/go-ethereum/crypto"

// Address generation
func GenerateAddress(privateKey *ecdsa.PrivateKey) common.Address {
    publicKey := privateKey.Public().(*ecdsa.PublicKey)
    return crypto.PubkeyToAddress(*publicKey)
}

// Signature verification
func VerifySignature(txHash []byte, signature []byte, address common.Address) bool {
    publicKey, err := crypto.SigToPub(txHash, signature)
    if err != nil {
        return false
    }
    
    recoveredAddress := crypto.PubkeyToAddress(*publicKey)
    return recoveredAddress == address
}
```

---

### Option 2: Dual Signature Support (Best of Both Worlds)

Support BOTH Ed25519 and secp256k1 in your blockchain.

**Pros:**
- ✅ MetaMask compatibility (secp256k1 accounts)
- ✅ Keep Ed25519 for native wallets (better performance)
- ✅ Users choose their preferred scheme
- ✅ No migration needed for existing users

**Cons:**
- ❌ More complex implementation
- ❌ Two codepaths to maintain
- ❌ Slightly larger codebase

**Implementation:**

```go
// Account type prefix in address
const (
    AddressTypeSecp256k1 byte = 0x01  // MetaMask compatible
    AddressTypeEd25519   byte = 0x02  // Native Thrylos
)

type Account struct {
    Address       []byte
    AddressType   byte
    PublicKey     []byte
    // ... other fields
}

// Address format: [type:1 byte][payload:20 bytes for secp256k1, 32 for Ed25519]
func CreateAddress(publicKey []byte, addrType byte) []byte {
    switch addrType {
    case AddressTypeSecp256k1:
        // Ethereum-style: last 20 bytes of Keccak256(pubkey)
        hash := crypto.Keccak256(publicKey)
        return append([]byte{addrType}, hash[12:]...)  // 1 + 20 = 21 bytes
        
    case AddressTypeEd25519:
        // Ed25519-style: full public key hash
        hash := sha256.Sum256(publicKey)
        return append([]byte{addrType}, hash[:]...)     // 1 + 32 = 33 bytes
    }
}

// Signature verification
func (tx *Transaction) VerifySignature() error {
    account, err := GetAccount(tx.From)
    if err != nil {
        return err
    }
    
    txHash := tx.Hash()
    
    switch account.AddressType {
    case AddressTypeSecp256k1:
        return verifySecp256k1Signature(txHash, tx.Signature, account)
        
    case AddressTypeEd25519:
        return verifyEd25519Signature(txHash, tx.Signature, account)
        
    default:
        return errors.New("unknown address type")
    }
}

func verifySecp256k1Signature(txHash []byte, signature []byte, account *Account) error {
    // Ethereum-style signature verification
    publicKey, err := crypto.SigToPub(txHash, signature)
    if err != nil {
        return err
    }
    
    recoveredAddress := crypto.PubkeyToAddress(*publicKey)
    expectedAddress := common.BytesToAddress(account.Address[1:]) // Skip type byte
    
    if recoveredAddress != expectedAddress {
        return errors.New("signature verification failed")
    }
    return nil
}

func verifyEd25519Signature(txHash []byte, signature []byte, account *Account) error {
    // Ed25519 signature verification
    publicKey := ed25519.PublicKey(account.PublicKey)
    if !ed25519.Verify(publicKey, txHash, signature) {
        return errors.New("signature verification failed")
    }
    return nil
}
```

**MetaMask Integration with Dual Support:**

```go
// api/ethereum_rpc.go

func (s *Server) handleSendTransaction(params json.RawMessage) (interface{}, error) {
    var tx Transaction
    if err := json.Unmarshal(params, &tx); err != nil {
        return nil, err
    }
    
    // Check if address is secp256k1 type (MetaMask)
    if tx.From[0] == AddressTypeSecp256k1 {
        // Handle as Ethereum-style transaction
        return s.handleEthereumTransaction(tx)
    } else {
        // Handle as native Ed25519 transaction
        return s.handleNativeTransaction(tx)
    }
}
```

---

### Option 3: Proxy/Adapter Layer (Complex)

Keep Ed25519 internally, add conversion layer for MetaMask.

**Pros:**
- ✅ No changes to core blockchain
- ✅ Ed25519 everywhere internally

**Cons:**
- ❌ Very complex
- ❌ Key management nightmare
- ❌ Users need two sets of keys
- ❌ NOT RECOMMENDED

---

## Comparison Table

| Feature | Ed25519 | secp256k1 |
|---------|---------|-----------|
| **MetaMask Compatible** | ❌ No | ✅ Yes |
| **Signature Size** | 64 bytes | 65 bytes (r+s+v) |
| **Public Key Size** | 32 bytes | 64 bytes (uncompressed) |
| **Signing Speed** | Faster (~2x) | Fast |
| **Verification Speed** | Faster (~5x) | Fast |
| **Key Recovery** | ❌ Not possible | ✅ Yes (from signature) |
| **Security** | ✅ Very high | ✅ Very high |
| **Ethereum Ecosystem** | ❌ Incompatible | ✅ Full support |
| **Quantum Resistance** | More resistant | Less resistant |

---

## Real-World Examples

### Blockchains Using secp256k1 (for Ethereum compatibility):
- Ethereum
- Binance Smart Chain
- Polygon
- Avalanche C-Chain
- Arbitrum
- Optimism

### Blockchains Using Ed25519 (no MetaMask):
- Solana (uses Phantom wallet, not MetaMask)
- Cardano (uses Yoroi, not MetaMask)
- Algorand (native wallet)

### Blockchains Using Dual Support:
- Cosmos chains (some support both via IBC)
- Near Protocol (has Ethereum bridge with conversion)

---

## Migration Path (If You're Currently Ed25519)

### Phase 1: Add secp256k1 Support
1. Add address type prefix to all accounts
2. Mark existing accounts as Ed25519 type
3. Add secp256k1 signature verification
4. Test both paths independently

### Phase 2: Enable MetaMask
1. Deploy wallet methods with secp256k1 support
2. Allow users to create secp256k1 accounts
3. Provide migration tool for Ed25519 → secp256k1

### Phase 3: (Optional) Deprecate Ed25519
1. Set sunset date for Ed25519
2. Force migration of remaining accounts
3. Remove Ed25519 code

---

## Code Examples

### Creating secp256k1-Compatible Accounts

```go
package account

import (
    "crypto/ecdsa"
    "github.com/ethereum/go-ethereum/crypto"
    "github.com/ethereum/go-ethereum/common"
)

type Account struct {
    PrivateKey *ecdsa.PrivateKey
    PublicKey  *ecdsa.PublicKey
    Address    common.Address
}

func NewAccount() (*Account, error) {
    // Generate secp256k1 private key
    privateKey, err := crypto.GenerateKey()
    if err != nil {
        return nil, err
    }
    
    publicKey := privateKey.Public().(*ecdsa.PublicKey)
    address := crypto.PubkeyToAddress(*publicKey)
    
    return &Account{
        PrivateKey: privateKey,
        PublicKey:  publicKey,
        Address:    address,
    }, nil
}

func (a *Account) SignTransaction(txHash []byte) ([]byte, error) {
    // Sign with secp256k1 (Ethereum-compatible)
    signature, err := crypto.Sign(txHash, a.PrivateKey)
    if err != nil {
        return nil, err
    }
    return signature, nil
}
```

### Transaction Verification

```go
package transaction

import (
    "github.com/ethereum/go-ethereum/crypto"
    "github.com/ethereum/go-ethereum/common"
)

func (tx *Transaction) Verify() error {
    // Get transaction hash
    txHash := tx.Hash()
    
    // Recover public key from signature (secp256k1 feature)
    publicKey, err := crypto.SigToPub(txHash.Bytes(), tx.Signature)
    if err != nil {
        return fmt.Errorf("signature recovery failed: %w", err)
    }
    
    // Derive address from public key
    recoveredAddress := crypto.PubkeyToAddress(*publicKey)
    
    // Verify it matches the sender
    if recoveredAddress != tx.From {
        return fmt.Errorf("signature verification failed: address mismatch")
    }
    
    return nil
}
```

---

## Recommendation for Thrylos

Based on your audit goal (MetaMask compatibility), I recommend:

### Short-term: Dual Support (Option 2)
- ✅ Maintain existing Ed25519 users
- ✅ Add secp256k1 for MetaMask users
- ✅ No breaking changes
- ✅ Maximum flexibility

### Long-term: Consider Full Migration (Option 1)
- ✅ Simpler codebase
- ✅ Better ecosystem compatibility
- ✅ Standard Ethereum tooling works out of box

### Implementation Priority:
1. Add secp256k1 address type (1-2 days)
2. Implement dual signature verification (2-3 days)
3. Update wallet methods to handle both (1 day)
4. Test with MetaMask (1 day)
5. Document for users (1 day)

**Total: ~1 week to add secp256k1 support while keeping Ed25519**

---

## Testing MetaMask Compatibility

```javascript
// Test if your blockchain works with MetaMask

// 1. Connect MetaMask
const accounts = await ethereum.request({ 
  method: 'eth_requestAccounts' 
});

// 2. Sign a message
const message = "Test message";
const signature = await ethereum.request({
  method: 'personal_sign',
  params: [message, accounts[0]]
});

// 3. Send transaction
const txHash = await ethereum.request({
  method: 'eth_sendTransaction',
  params: [{
    from: accounts[0],
    to: '0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb',
    value: '0x1000000000000000', // 0.001 ETH
    gas: '0x5208'
  }]
});

// If all three work, you have secp256k1 compatibility! ✅
```

---

## Final Answer

**You MUST use secp256k1 for MetaMask compatibility.**

Ed25519 will not work with MetaMask because:
1. MetaMask only signs with secp256k1
2. Ethereum addresses are derived from secp256k1 public keys
3. There's no way to configure MetaMask to use different cryptography

**Recommended Approach:**
Implement **Option 2 (Dual Support)** to:
- Keep your existing Ed25519 users happy
- Add MetaMask compatibility with secp256k1
- Provide maximum flexibility
- Avoid breaking changes

This gives you the best of both worlds! 🎯
