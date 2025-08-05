package node

// transaction_testing.go - Complete with all missing methods

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/crypto/hash"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// TransactionTester provides utilities for creating test transactions
type TransactionTester struct {
	node       *Node
	privateKey crypto.PrivateKey
	address    string
}

// ⚠️ CRITICAL: You need to add nonce tracking to your TPSTransactionTester struct
// Make sure your TPSTransactionTester looks like this:
/*
type TPSTransactionTester struct {
	node        *Node
	privateKey  crypto.PrivateKey
	address     string
	testResults []TPSTestResult
	mu          sync.RWMutex

	// Add these fields for nonce tracking
	currentNonce uint64
	nonceMu      sync.Mutex
}
*/

// NewTransactionTester creates a new transaction testing utility
func NewTransactionTester(node *Node, privateKey crypto.PrivateKey, address string) *TransactionTester {
	return &TransactionTester{
		node:       node,
		privateKey: privateKey,
		address:    address,
	}
}

// CreateTestTransaction creates a test transaction matching validator's exact hash calculation
func (tt *TransactionTester) CreateTestTransaction(from, to string, amount int64, gasPrice int64) (*core.Transaction, error) {
	nonce, err := tt.node.blockchain.GetNonce(from)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %v", err)
	}

	// Generate transaction ID (simple version for testing)
	timestamp := time.Now().UnixNano()
	txID := fmt.Sprintf("test_tx_%d_%d", timestamp, nonce)

	tx := &core.Transaction{
		Id:        txID,
		From:      from,
		To:        to,
		Amount:    amount,
		Gas:       21000,
		GasPrice:  gasPrice,
		Nonce:     nonce,
		Type:      core.TransactionType_TRANSFER, // This was missing!
		Data:      nil,                           // This was missing!
		Timestamp: time.Now().Unix(),
	}

	// Calculate hash using EXACT same method as validator
	tx.Hash = tt.calculateTransactionHashExact(tx)

	// Sign the transaction
	signature, err := tt.signTransaction(tx)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %v", err)
	}
	tx.Signature = signature

	return tx, nil
}

// calculateTransactionHashExact - EXACT copy of validator's CalculateTransactionHash method
func (tt *TransactionTester) calculateTransactionHashExact(tx *core.Transaction) string {
	var buf bytes.Buffer

	// Serialize transaction fields for hashing (excluding signature and hash)
	// THIS MATCHES validator.go EXACTLY:

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
	hashBytes := hash.HashData(buf.Bytes())
	return fmt.Sprintf("%x", hashBytes)
}

// SubmitTestTransaction creates and submits a test transaction
func (tt *TransactionTester) SubmitTestTransaction(from, to string, amount int64, gasPrice int64) (*core.Transaction, error) {
	tx, err := tt.CreateTestTransaction(from, to, amount, gasPrice)
	if err != nil {
		return nil, fmt.Errorf("failed to create test transaction: %v", err)
	}

	if err := tt.node.SubmitTransaction(tx); err != nil {
		return nil, fmt.Errorf("failed to submit test transaction: %v", err)
	}

	return tx, nil
}

// signTransaction signs the transaction using the private key (matches validator approach)
func (tt *TransactionTester) signTransaction(tx *core.Transaction) ([]byte, error) {
	// Create hash to sign (same approach as validator)
	hashToSign := hash.HashData([]byte(tx.Hash))

	// Sign with private key
	signature := tt.privateKey.Sign(hashToSign)
	if signature == nil {
		return nil, fmt.Errorf("failed to sign transaction: signature is nil")
	}

	return signature.Bytes(), nil
}

// CreateTransferTransaction creates a proper transfer transaction (like validator)
func (tt *TransactionTester) CreateTransferTransaction(from, to string, amount, gas, gasPrice int64, nonce uint64) (*core.Transaction, error) {
	timestamp := time.Now().UnixNano()
	txID := fmt.Sprintf("test_tx_%d_%d", timestamp, nonce)

	tx := &core.Transaction{
		Id:        txID,
		From:      from,
		To:        to,
		Amount:    amount,
		Gas:       gas,
		GasPrice:  gasPrice,
		Nonce:     nonce,
		Type:      core.TransactionType_TRANSFER,
		Data:      nil,
		Timestamp: time.Now().Unix(),
	}

	// Calculate hash using exact method
	tx.Hash = tt.calculateTransactionHashExact(tx)

	// Sign the transaction
	signature, err := tt.signTransaction(tx)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %v", err)
	}
	tx.Signature = signature

	return tx, nil
}

// GetBalance returns the balance of an address (helper method)
func (tt *TransactionTester) GetBalance(address string) (int64, error) {
	return tt.node.GetBalance(address)
}

// GetNode returns the node instance (helper method)
func (tt *TransactionTester) GetNode() *Node {
	return tt.node
}
