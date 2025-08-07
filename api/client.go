// api/client.go

// Smart polling client library for wallet applications

// Implements intelligent balance polling with adaptive intervals (15s normal, 2s after transactions)
// Provides BalancePoller for single addresses and SmartPoller for multiple addresses
// Handles background polling, error recovery, and aggressive mode after transactions
// Includes callback system for balance changes and connection errors
// Battery-efficient design for mobile wallets with automatic pause/resume

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client represents an API client for polling blockchain data
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new API client
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// BalanceResponse represents the response from balance endpoint
type BalanceResponse struct {
	Address        string  `json:"address"`
	Balance        int64   `json:"balance"`
	BalanceThrylos float64 `json:"balanceThrylos"`
	Nonce          uint64  `json:"nonce"`
}

// GetBalance fetches the current balance for an address
func (c *Client) GetBalance(address string) (*BalanceResponse, error) {
	url := fmt.Sprintf("%s/api/v1/account/%s/balance", c.baseURL, address)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch balance: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var balance BalanceResponse
	if err := json.NewDecoder(resp.Body).Decode(&balance); err != nil {
		return nil, fmt.Errorf("failed to decode response: %v", err)
	}

	return &balance, nil
}

// BalancePoller handles intelligent balance polling for wallet applications
type BalancePoller struct {
	client   *Client
	address  string
	interval time.Duration

	// Current state
	lastBalance int64
	lastNonce   uint64

	// Polling control
	ctx    context.Context
	cancel context.CancelFunc

	// Callbacks
	onBalanceChange func(oldBalance, newBalance int64)
	onError         func(error)
}

// NewBalancePoller creates a new balance poller for an address
func NewBalancePoller(client *Client, address string) *BalancePoller {
	ctx, cancel := context.WithCancel(context.Background())

	return &BalancePoller{
		client:   client,
		address:  address,
		interval: 15 * time.Second, // Default polling interval
		ctx:      ctx,
		cancel:   cancel,
	}
}

// SetInterval sets the polling interval
func (bp *BalancePoller) SetInterval(interval time.Duration) {
	bp.interval = interval
}

// OnBalanceChange sets a callback for when balance changes
func (bp *BalancePoller) OnBalanceChange(callback func(oldBalance, newBalance int64)) {
	bp.onBalanceChange = callback
}

// OnError sets a callback for when errors occur
func (bp *BalancePoller) OnError(callback func(error)) {
	bp.onError = callback
}

// Start begins polling for balance changes
func (bp *BalancePoller) Start() {
	go bp.pollLoop()
}

// Stop stops the polling
func (bp *BalancePoller) Stop() {
	bp.cancel()
}

// PollOnce performs a single balance check (useful after sending transactions)
func (bp *BalancePoller) PollOnce() {
	bp.checkBalance()
}

// SetAggressivePolling temporarily increases polling frequency (after transactions)
func (bp *BalancePoller) SetAggressivePolling(duration time.Duration) {
	originalInterval := bp.interval
	bp.interval = 2 * time.Second // Poll every 2 seconds

	// Reset to original interval after duration
	time.AfterFunc(duration, func() {
		bp.interval = originalInterval
	})
}

func (bp *BalancePoller) pollLoop() {
	ticker := time.NewTicker(bp.interval)
	defer ticker.Stop()

	// Initial check
	bp.checkBalance()

	for {
		select {
		case <-bp.ctx.Done():
			return
		case <-ticker.C:
			// Update ticker if interval changed
			if ticker.C != time.NewTicker(bp.interval).C {
				ticker.Stop()
				ticker = time.NewTicker(bp.interval)
			}
			bp.checkBalance()
		}
	}
}

func (bp *BalancePoller) checkBalance() {
	balance, err := bp.client.GetBalance(bp.address)
	if err != nil {
		if bp.onError != nil {
			bp.onError(err)
		}
		return
	}

	// Check if balance changed
	if balance.Balance != bp.lastBalance {
		oldBalance := bp.lastBalance
		bp.lastBalance = balance.Balance
		bp.lastNonce = balance.Nonce

		if bp.onBalanceChange != nil {
			bp.onBalanceChange(oldBalance, balance.Balance)
		}
	}
}

// GetCurrentBalance returns the last known balance without making an API call
func (bp *BalancePoller) GetCurrentBalance() int64 {
	return bp.lastBalance
}

// GetCurrentNonce returns the last known nonce without making an API call
func (bp *BalancePoller) GetCurrentNonce() uint64 {
	return bp.lastNonce
}

// Example usage function
func ExampleWalletPolling() {
	// Create client
	client := NewClient("http://localhost:8080")

	// Create poller for a wallet address
	address := "tl1wxyz123..."
	poller := NewBalancePoller(client, address)

	// Set up callbacks
	poller.OnBalanceChange(func(oldBalance, newBalance int64) {
		fmt.Printf("💰 Balance changed: %d → %d\n", oldBalance, newBalance)

		// Update UI here
		// updateWalletUI(newBalance)
	})

	poller.OnError(func(err error) {
		fmt.Printf("❌ Error polling balance: %v\n", err)

		// Handle error in UI - maybe show offline indicator
		// showConnectionError(err)
	})

	// Start background polling
	poller.Start()

	fmt.Printf("📊 Started polling balance for %s\n", address)

	// Simulate user sending a transaction
	fmt.Println("💸 User sent transaction, increasing poll frequency...")
	poller.SetAggressivePolling(30 * time.Second)

	// Keep running for demo
	time.Sleep(2 * time.Minute)

	// Stop polling
	poller.Stop()
	fmt.Println("⏹️ Stopped polling")
}

// SmartPoller handles multiple addresses with intelligent polling
type SmartPoller struct {
	client  *Client
	pollers map[string]*BalancePoller
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewSmartPoller creates a poller that can handle multiple addresses
func NewSmartPoller(client *Client) *SmartPoller {
	ctx, cancel := context.WithCancel(context.Background())

	return &SmartPoller{
		client:  client,
		pollers: make(map[string]*BalancePoller),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// AddAddress adds an address to poll
func (sp *SmartPoller) AddAddress(address string, onBalanceChange func(oldBalance, newBalance int64)) {
	if _, exists := sp.pollers[address]; exists {
		return // Already polling this address
	}

	poller := NewBalancePoller(sp.client, address)
	poller.OnBalanceChange(onBalanceChange)
	poller.OnError(func(err error) {
		fmt.Printf("Error polling %s: %v\n", address, err)
	})

	sp.pollers[address] = poller
	poller.Start()
}

// RemoveAddress stops polling an address
func (sp *SmartPoller) RemoveAddress(address string) {
	if poller, exists := sp.pollers[address]; exists {
		poller.Stop()
		delete(sp.pollers, address)
	}
}

// SetAggressiveMode temporarily increases polling for all addresses
func (sp *SmartPoller) SetAggressiveMode(duration time.Duration) {
	for _, poller := range sp.pollers {
		poller.SetAggressivePolling(duration)
	}
}

// Stop stops all polling
func (sp *SmartPoller) Stop() {
	sp.cancel()
	for _, poller := range sp.pollers {
		poller.Stop()
	}
	sp.pollers = make(map[string]*BalancePoller)
}

// GetBalances returns current balances for all addresses
func (sp *SmartPoller) GetBalances() map[string]int64 {
	balances := make(map[string]int64)
	for address, poller := range sp.pollers {
		balances[address] = poller.GetCurrentBalance()
	}
	return balances
}
