package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
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

// EstimateGasResponse represents the response from the gas estimation endpoint
type EstimateGasResponse struct {
	GasEstimate int64   `json:"gas_estimate"`
	GasPrice    int64   `json:"gas_price"`
	TotalFee    int64   `json:"total_fee"`
	FeeThrylos  float64 `json:"fee_thrylos"`
}

// EstimateGas estimates the gas required for a transaction
func (c *Client) EstimateGas(from, to string, amount int64, data []byte) (*EstimateGasResponse, error) {
	url := fmt.Sprintf("%s/api/v1/estimate-gas", c.baseURL)

	reqBody := map[string]interface{}{
		"from":   from,
		"to":     to,
		"amount": amount,
		"data":   string(data),
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	resp, err := c.httpClient.Post(url, "application/json", bytes.NewBuffer(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var result EstimateGasResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %v", err)
	}

	return &result, nil
}

// GetGasPrice fetches the current gas price from the node
func (c *Client) GetGasPrice() (string, error) {
	// Use the JSON-RPC endpoint
	url := fmt.Sprintf("%s/eth_gasPrice", c.baseURL)

	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "eth_gasPrice",
		"params":  []interface{}{},
		"id":      1,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "0", fmt.Errorf("failed to marshal request: %v", err)
	}

	resp, err := c.httpClient.Post(url, "application/json", bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "0", fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	var rpcResp struct {
		Result string `json:"result"`
		Error  interface{} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return "0", fmt.Errorf("failed to decode response: %v", err)
	}

	if rpcResp.Error != nil {
		return "0", fmt.Errorf("RPC error: %v", rpcResp.Error)
	}

	// Result is in hex (e.g., "0xa"), convert to decimal string
	cleanHex := strings.TrimPrefix(rpcResp.Result, "0x")
	if cleanHex == "" {
		return "0", nil
	}
	
	val := new(big.Int)
	val.SetString(cleanHex, 16)
	return val.String(), nil
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

// SetAggressiveMode temporarily increases polling for all addresses
func (sp *SmartPoller) SetAggressiveMode(duration time.Duration) {
	for _, poller := range sp.pollers {
		poller.SetAggressivePolling(duration)
	}
}
