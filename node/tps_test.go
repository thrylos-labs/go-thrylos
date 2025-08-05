package node_test

// import (
// 	"context"
// 	"fmt"
// 	"sync"
// 	"sync/atomic"
// 	"testing"
// 	"time"

// 	"github.com/thrylos-labs/go-thrylos/core/account"
// 	"github.com/thrylos-labs/go-thrylos/crypto"
// 	core "github.com/thrylos-labs/go-thrylos/proto/core"
// 	// Import your main node package
// )

// // The following code simulates a complete, self-contained TPS test environment.

// // MockNode simulates a simplified blockchain node for testing purposes.
// type MockNode struct {
// 	txPool           chan *core.Transaction
// 	worldState       map[string]*core.Account
// 	blockProduction  time.Duration
// 	mu               sync.Mutex
// 	validatorAddress string
// }

// func NewMockNode(validatorAddress string, blockTime time.Duration) *MockNode {
// 	return &MockNode{
// 		txPool:           make(chan *core.Transaction, 10000), // High capacity mempool
// 		worldState:       make(map[string]*core.Account),
// 		blockProduction:  blockTime,
// 		validatorAddress: validatorAddress,
// 	}
// }

// // Start runs the mock node's block production loop.
// func (n *MockNode) Start(ctx context.Context) {
// 	ticker := time.NewTicker(n.blockProduction)
// 	defer ticker.Stop()

// 	for {
// 		select {
// 		case <-ctx.Done():
// 			return
// 		case <-ticker.C:
// 			n.produceBlock()
// 		}
// 	}
// }

// // produceBlock processes transactions from the pool and updates the world state.
// func (n *MockNode) produceBlock() {
// 	n.mu.Lock()
// 	defer n.mu.Unlock()

// 	// Simulate processing transactions from the pool
// 	processedTxs := 0
// 	for len(n.txPool) > 0 && processedTxs < 100 { // Max 100 txs per block
// 		tx := <-n.txPool
// 		n.processTransaction(tx)
// 		processedTxs++
// 	}

// 	if processedTxs > 0 {
// 		fmt.Printf("📦 Mock block produced with %d transactions.\n", processedTxs)
// 	}
// }

// // processTransaction validates and applies a single transaction.
// func (n *MockNode) processTransaction(tx *core.Transaction) {
// 	sender, ok := n.worldState[tx.From]
// 	if !ok {
// 		return // Sender account doesn't exist
// 	}

// 	// Basic nonce check
// 	if tx.Nonce != sender.Nonce {
// 		return // Invalid nonce
// 	}

// 	// Update state
// 	sender.Nonce++
// 	sender.Balance -= tx.Amount

// 	// Simulate updating a recipient's balance (if it exists)
// 	recipient, ok := n.worldState[tx.To]
// 	if ok {
// 		recipient.Balance += tx.Amount
// 	} else {
// 		// Create new account for recipient if it doesn't exist
// 		n.worldState[tx.To] = &core.Account{
// 			Address: tx.To,
// 			Balance: tx.Amount,
// 			Nonce:   0,
// 		}
// 	}
// }

// // SubmitTransaction sends a transaction to the mock node's mempool.
// func (n *MockNode) SubmitTransaction(tx *core.Transaction) error {
// 	select {
// 	case n.txPool <- tx:
// 		return nil
// 	default:
// 		return fmt.Errorf("transaction pool is full")
// 	}
// }

// // GetNonce returns the current nonce of an account.
// func (n *MockNode) GetNonce(address string) (uint64, error) {
// 	n.mu.Lock()
// 	defer n.mu.Unlock()
// 	acc, ok := n.worldState[address]
// 	if !ok {
// 		return 0, fmt.Errorf("account not found")
// 	}
// 	return acc.Nonce, nil
// }

// // GetBalance returns the balance of an account.
// func (n *MockNode) GetBalance(address string) (int64, error) {
// 	n.mu.Lock()
// 	defer n.mu.Unlock()
// 	acc, ok := n.worldState[address]
// 	if !ok {
// 		return 0, fmt.Errorf("account not found")
// 	}
// 	return acc.Balance, nil
// }

// // userWallet represents a single test user with its own private key and nonce.
// type userWallet struct {
// 	privateKey   crypto.PrivateKey
// 	address      string
// 	currentNonce uint64
// 	node         *MockNode // Each wallet interacts with the mock node
// }

// // newTestUser creates and funds a new test user account.
// func newTestUser(m *MockNode, initialFunds int64) (*userWallet, error) {
// 	pk, err := crypto.NewPrivateKey()
// 	if err != nil {
// 		return nil, err
// 	}
// 	addr, err := account.GenerateAddress(pk.PublicKey())
// 	if err != nil {
// 		return nil, err
// 	}

// 	// Fund the new account from the mock validator
// 	m.mu.Lock()
// 	m.worldState[addr] = &core.Account{
// 		Address: addr,
// 		Balance: initialFunds,
// 		Nonce:   0,
// 	}
// 	m.mu.Unlock()

// 	return &userWallet{
// 		privateKey:   pk,
// 		address:      addr,
// 		currentNonce: 0,
// 		node:         m,
// 	}, nil
// }

// // getNextNonce gets the next nonce for this user, ensuring it is correct relative to what has been sent.
// func (uw *userWallet) getNextNonce() uint64 {
// 	return atomic.AddUint64(&uw.currentNonce, 1) - 1
// }

// // TPS Test main entry point
// func TestConcurrentTPS(t *testing.T) {
// 	// Test configuration
// 	testDuration := 30 * time.Second
// 	blockTime := 1 * time.Second // 1-second block time
// 	numUsers := 100
// 	targetTPS := 50
// 	txAmount := int64(100)

// 	// Setup Mock Node
// 	validatorPK, _ := crypto.NewPrivateKey()
// 	validatorAddr, _ := account.GenerateAddress(validatorPK.PublicKey())
// 	mockNode := NewMockNode(validatorAddr, blockTime)

// 	// Fund the validator address (genesis)
// 	mockNode.mu.Lock()
// 	mockNode.worldState[validatorAddr] = &core.Account{
// 		Address: validatorAddr,
// 		Balance: 1_000_000_000,
// 		Nonce:   0,
// 	}
// 	mockNode.mu.Unlock()

// 	// Start block production
// 	ctx, cancel := context.WithCancel(context.Background())
// 	defer cancel()
// 	go mockNode.Start(ctx)

// 	// Create and fund user accounts
// 	var users []*userWallet
// 	for i := 0; i < numUsers; i++ {
// 		user, err := newTestUser(mockNode, 10_000)
// 		if err != nil {
// 			t.Fatalf("Failed to create test user: %v", err)
// 		}
// 		users = append(users, user)
// 	}

// 	fmt.Printf("Running concurrent TPS test with %d users, targeting %d TPS.\n", numUsers, targetTPS)

// 	var wg sync.WaitGroup
// 	var successfulTxs, totalTxs, failedTxs atomic.Int64

// 	// Transaction generation loop
// 	rateLimiter := time.NewTicker(time.Second / time.Duration(targetTPS))
// 	defer rateLimiter.Stop()

// 	testCtx, testCancel := context.WithTimeout(ctx, testDuration)
// 	defer testCancel()

// 	for i := 0; ; i++ {
// 		select {
// 		case <-testCtx.Done():
// 			goto endTest
// 		case <-rateLimiter.C:
// 			userIndex := i % numUsers
// 			user := users[userIndex]
// 			recipient := users[(userIndex+1)%numUsers]

// 			wg.Add(1)
// 			go func(sender, receiver *userWallet) {
// 				defer wg.Done()

// 				totalTxs.Add(1)

// 				// Create and submit a transaction
// 				tx := &core.Transaction{
// 					From:   sender.address,
// 					To:     receiver.address,
// 					Amount: txAmount,
// 					Nonce:  sender.getNextNonce(),
// 				}

// 				err := mockNode.SubmitTransaction(tx)
// 				if err != nil {
// 					failedTxs.Add(1)
// 				} else {
// 					successfulTxs.Add(1)
// 				}
// 			}(user, recipient)
// 		}
// 	}

// endTest:
// 	wg.Wait() // Wait for all goroutines to finish

// 	fmt.Println("Test complete. Calculating results...")

// 	// Final Results Calculation
// 	finalTotalTxs := totalTxs.Load()
// 	finalSuccessfulTxs := successfulTxs.Load()
// 	finalFailedTxs := failedTxs.Load()

// 	actualTPS := float64(finalSuccessfulTxs) / float64(testDuration.Seconds())
// 	successRate := float64(finalSuccessfulTxs) / float64(finalTotalTxs) * 100

// 	fmt.Printf("\n--- TEST SUMMARY ---\n")
// 	fmt.Printf("Total transactions generated: %d\n", finalTotalTxs)
// 	fmt.Printf("Successful submissions: %d\n", finalSuccessfulTxs)
// 	fmt.Printf("Failed submissions: %d\n", finalFailedTxs)
// 	fmt.Printf("Actual TPS: %.2f\n", actualTPS)
// 	fmt.Printf("Success Rate: %.2f%%\n", successRate)

// 	if successRate < 95 {
// 		t.Errorf("Test failed: Success rate was too low (%.2f%%)", successRate)
// 	}
// }
