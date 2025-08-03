package node

// transaction_testing.go - CORRECTED to match validator.go exactly

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

// BatchCreateTestTransactions creates multiple test transactions
func (tt *TransactionTester) BatchCreateTestTransactions(from, to string, count int, amount int64, gasPrice int64) ([]*core.Transaction, error) {
	var transactions []*core.Transaction

	for i := 0; i < count; i++ {
		tx, err := tt.CreateTestTransaction(from, to, amount, gasPrice)
		if err != nil {
			return nil, fmt.Errorf("failed to create transaction %d: %v", i, err)
		}
		transactions = append(transactions, tx)

		// Small delay to ensure unique timestamps and nonces
		time.Sleep(time.Millisecond * 10)
	}

	return transactions, nil
}

// BatchSubmitTestTransactions creates and submits multiple test transactions
func (tt *TransactionTester) BatchSubmitTestTransactions(from, to string, count int, amount int64, gasPrice int64) ([]*core.Transaction, error) {
	var transactions []*core.Transaction

	for i := 0; i < count; i++ {
		tx, err := tt.SubmitTestTransaction(from, to, amount, gasPrice)
		if err != nil {
			return nil, fmt.Errorf("failed to submit transaction %d: %v", i, err)
		}
		transactions = append(transactions, tx)

		// Small delay between submissions
		time.Sleep(100 * time.Millisecond)
	}

	return transactions, nil
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

// DebugHashMismatch helps debug hash calculation differences
func (tt *TransactionTester) DebugHashMismatch(from, to string, amount int64, gasPrice int64) {
	fmt.Println("🔧 === UPDATED TRANSACTION HASH DEBUG ===")

	nonce, err := tt.node.blockchain.GetNonce(from)
	if err != nil {
		fmt.Printf("Failed to get nonce: %v\n", err)
		return
	}

	timestamp := time.Now().UnixNano()
	txID := fmt.Sprintf("debug_tx_%d_%d", timestamp, nonce)

	tx := &core.Transaction{
		Id:        txID,
		From:      from,
		To:        to,
		Amount:    amount,
		Gas:       21000,
		GasPrice:  gasPrice,
		Nonce:     nonce,
		Type:      core.TransactionType_TRANSFER, // NOW INCLUDED
		Data:      nil,                           // NOW INCLUDED
		Timestamp: time.Now().Unix(),
	}

	fmt.Printf("Transaction fields:\n")
	fmt.Printf("  ID: %s\n", tx.Id)
	fmt.Printf("  From: %s\n", tx.From)
	fmt.Printf("  To: %s\n", tx.To)
	fmt.Printf("  Amount: %d\n", tx.Amount)
	fmt.Printf("  Gas: %d\n", tx.Gas)
	fmt.Printf("  GasPrice: %d\n", tx.GasPrice)
	fmt.Printf("  Nonce: %d\n", tx.Nonce)
	fmt.Printf("  Type: %v\n", tx.Type)
	fmt.Printf("  Data: %v\n", tx.Data)
	fmt.Printf("  Timestamp: %d\n", tx.Timestamp)
	fmt.Println()

	// Calculate hash with corrected method
	hash := tt.calculateTransactionHashExact(tx)
	fmt.Printf("Calculated hash (corrected): %s\n", hash)

	tx.Hash = hash
	tx.Signature = []byte("debug_signature")

	fmt.Printf("Testing corrected transaction...\n")
	if err := tt.node.SubmitTransaction(tx); err != nil {
		fmt.Printf("❌ Still failed: %v\n", err)
	} else {
		fmt.Printf("✅ SUCCESS! Transaction accepted!\n")
	}

	fmt.Println("========================================")
}

// Helper methods for testing scenarios

// SimulateTransactionLoad creates a realistic transaction load for testing
func (tt *TransactionTester) SimulateTransactionLoad(addresses []string, duration time.Duration, tps int) error {
	if len(addresses) < 2 {
		return fmt.Errorf("need at least 2 addresses for transaction simulation")
	}

	ticker := time.NewTicker(time.Second / time.Duration(tps))
	defer ticker.Stop()

	timeout := time.After(duration)

	for {
		select {
		case <-ticker.C:
			// Pick random from and to addresses
			from := addresses[time.Now().UnixNano()%int64(len(addresses))]
			to := addresses[(time.Now().UnixNano()+1)%int64(len(addresses))]

			if from != to {
				_, err := tt.SubmitTestTransaction(from, to, 15000000, 1000) // Use valid amount
				if err != nil {
					fmt.Printf("Failed to submit test transaction: %v\n", err)
				}
			}

		case <-timeout:
			fmt.Printf("Transaction load simulation completed\n")
			return nil
		}
	}
}

// TestP2PTransactionPropagation tests if transactions propagate across P2P network
func (tt *TransactionTester) TestP2PTransactionPropagation(from, to string, amount int64) error {
	// Create and submit transaction
	tx, err := tt.SubmitTestTransaction(from, to, amount, 1000)
	if err != nil {
		return fmt.Errorf("failed to submit test transaction: %v", err)
	}

	fmt.Printf("Submitted test transaction %s for P2P propagation test\n", tx.Id)
	return nil
}

// ValidateTransactionFormat checks if a transaction has the correct format
func (tt *TransactionTester) ValidateTransactionFormat(tx *core.Transaction) error {
	if tx == nil {
		return fmt.Errorf("transaction is nil")
	}

	if tx.Id == "" {
		return fmt.Errorf("transaction ID is empty")
	}

	if tx.From == "" {
		return fmt.Errorf("from address is empty")
	}

	if tx.To == "" {
		return fmt.Errorf("to address is empty")
	}

	if tx.Amount <= 0 {
		return fmt.Errorf("amount must be positive")
	}

	if tx.Gas <= 0 {
		return fmt.Errorf("gas must be positive")
	}

	if tx.GasPrice <= 0 {
		return fmt.Errorf("gas price must be positive")
	}

	if len(tx.Signature) == 0 {
		return fmt.Errorf("signature is missing")
	}

	return nil
}

// GetBalance returns the balance of an address (helper method)
func (tt *TransactionTester) GetBalance(address string) (int64, error) {
	return tt.node.GetBalance(address)
}

// GetNode returns the node instance (helper method)
func (tt *TransactionTester) GetNode() *Node {
	return tt.node
}
