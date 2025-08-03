package node

// transaction_testing.go - Transaction utilities for testing purposes only
// In production, transactions are created and submitted by wallets

import (
	"fmt"
	"time"

	"github.com/thrylos-labs/go-thrylos/crypto"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"golang.org/x/crypto/blake2b"
)

// TransactionTester provides utilities for creating test transactions
// This should only be used for development, testing, and debugging
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

// CreateTestTransaction creates a test transaction (for development/testing only)
func (tt *TransactionTester) CreateTestTransaction(from, to string, amount int64, gasPrice int64) (*core.Transaction, error) {
	nonce, err := tt.node.blockchain.GetNonce(from)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %v", err)
	}

	// Generate transaction ID
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
		Timestamp: time.Now().Unix(),
	}

	// Calculate hash
	hash, err := tt.calculateTransactionHash(tx)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate transaction hash: %v", err)
	}
	tx.Hash = hash

	// Sign the transaction
	signature, err := tt.signTransaction(tx)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %v", err)
	}
	tx.Signature = signature

	return tx, nil
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

// CreateTransactionSimple creates a minimal transaction for testing
func (tt *TransactionTester) CreateTransactionSimple(from, to string, amount int64, gasPrice int64) (*core.Transaction, error) {
	nonce, err := tt.node.blockchain.GetNonce(from)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %v", err)
	}

	// Create transaction and let blockchain handle the hash
	tx := &core.Transaction{
		From:      from,
		To:        to,
		Amount:    amount,
		Gas:       21000,
		GasPrice:  gasPrice,
		Nonce:     nonce,
		Timestamp: time.Now().Unix(),
	}

	return tx, nil
}

// CreateTransactionWithDummySignature creates a transaction with placeholder signature
func (tt *TransactionTester) CreateTransactionWithDummySignature(from, to string, amount int64, gasPrice int64) (*core.Transaction, error) {
	nonce, err := tt.node.blockchain.GetNonce(from)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %v", err)
	}

	timestamp := time.Now().UnixNano()
	hashData := fmt.Sprintf("%s_%s_%d_%d_%d", from, to, amount, nonce, timestamp)
	hash := blake2b.Sum256([]byte(hashData))
	hashString := fmt.Sprintf("%x", hash)

	tx := &core.Transaction{
		Id:        fmt.Sprintf("dummy_tx_%d_%d", timestamp, nonce),
		Hash:      hashString,
		From:      from,
		To:        to,
		Amount:    amount,
		Gas:       21000,
		GasPrice:  gasPrice,
		Nonce:     nonce,
		Timestamp: time.Now().Unix(),
		Signature: []byte("dummy_signature"), // Simple placeholder signature
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

		// Small delay to ensure unique timestamps
		time.Sleep(time.Millisecond)
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

// Helper methods for transaction creation

func (tt *TransactionTester) calculateTransactionHash(tx *core.Transaction) (string, error) {
	// Create hash data from transaction fields (excluding hash and signature)
	hashData := fmt.Sprintf("%s%s%s%d%d%d%d%d",
		tx.Id,
		tx.From,
		tx.To,
		tx.Amount,
		tx.Gas,
		tx.GasPrice,
		tx.Nonce,
		tx.Timestamp,
	)

	hash := blake2b.Sum256([]byte(hashData))
	return fmt.Sprintf("%x", hash), nil
}

func (tt *TransactionTester) signTransaction(tx *core.Transaction) ([]byte, error) {
	// Sign the transaction hash
	hashBytes, err := tt.calculateTransactionHash(tx)
	if err != nil {
		return nil, err
	}

	hash := blake2b.Sum256([]byte(hashBytes))
	signature := tt.privateKey.Sign(hash[:])
	if signature == nil {
		return nil, fmt.Errorf("failed to sign transaction: signature is nil")
	}

	return signature.Bytes(), nil
}

// Test scenarios

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
				_, err := tt.SubmitTestTransaction(from, to, 100, 1000)
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

	// In a real test, you would check if other nodes receive this transaction
	// For now, just log the submission
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
