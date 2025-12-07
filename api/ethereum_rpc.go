// api/ethereum_rpc.go
// Ethereum JSON-RPC API for MetaMask compatibility

package api

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/thrylos-labs/go-thrylos/core/chain"
	"github.com/thrylos-labs/go-thrylos/core/evm"
)

// EthereumRPCHandler handles Ethereum-compatible JSON-RPC requests
type EthereumRPCHandler struct {
	blockchain  *chain.Blockchain
	evmExecutor *evm.RevmExecutor
	chainID     *big.Int
}

// NewEthereumRPCHandler creates a new Ethereum RPC handler
func NewEthereumRPCHandler(
	blockchain *chain.Blockchain,
	// UPDATE THIS TYPE:
	executor *evm.RevmExecutor,
	chainID int64,
) *EthereumRPCHandler {
	return &EthereumRPCHandler{
		blockchain:  blockchain,
		evmExecutor: executor,
		chainID:     big.NewInt(chainID),
	}
}

// ===== Network Information =====

// ChainId returns the chain ID used for signing replay-protected transactions
func (h *EthereumRPCHandler) ChainId(w http.ResponseWriter, r *http.Request) {
	response := hexutil.Uint64(h.chainID.Uint64())
	respondJSON(w, response)
}

// NetworkId returns the network ID (same as chain ID for most chains)
func (h *EthereumRPCHandler) NetworkId(w http.ResponseWriter, r *http.Request) {
	response := hexutil.Uint64(h.chainID.Uint64())
	respondJSON(w, response)
}

// ===== Account Information =====

// GetBalance returns the balance of the account at the given address
func (h *EthereumRPCHandler) GetBalance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address     string `json:"address"`
		BlockNumber string `json:"blockNumber"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}

	address := common.HexToAddress(req.Address)
	balance, err := h.blockchain.GetBalance(address.Hex())
	if err != nil {
		balance = 0
	}

	response := (*hexutil.Big)(big.NewInt(balance))
	respondJSON(w, response)
}

// GetTransactionCount returns the number of transactions sent from an address
func (h *EthereumRPCHandler) GetTransactionCount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address     string `json:"address"`
		BlockNumber string `json:"blockNumber"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}

	address := common.HexToAddress(req.Address)
	nonce, err := h.blockchain.GetNonce(address.Hex())
	if err != nil {
		nonce = 0
	}

	response := hexutil.Uint64(nonce)
	respondJSON(w, response)
}

// GetCode returns the code at a given address
func (h *EthereumRPCHandler) GetCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address     string `json:"address"`
		BlockNumber string `json:"blockNumber"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}

	address := common.HexToAddress(req.Address)
	code := h.evmExecutor.GetCode(address)

	response := hexutil.Bytes(code)
	respondJSON(w, response)
}

// ===== Transaction Submission =====

// SendRawTransaction submits a signed transaction
func (h *EthereumRPCHandler) SendRawTransaction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Data string `json:"data"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}

	// Decode hex transaction
	txBytes, err := hexutil.Decode(req.Data)
	if err != nil {
		respondError(w, -32602, "Invalid transaction data")
		return
	}

	// Parse Ethereum transaction
	ethTx := new(types.Transaction)
	if err := rlp.DecodeBytes(txBytes, ethTx); err != nil {
		respondError(w, -32602, "Invalid transaction encoding")
		return
	}

	// Convert Ethereum tx to Thrylos tx and submit
	thrylosTx, err := h.convertEthTxToThrylosTx(ethTx)
	if err != nil {
		respondError(w, -32602, fmt.Sprintf("Transaction conversion failed: %v", err))
		return
	}

	// Submit to blockchain
	txHash, err := h.blockchain.SubmitTransaction(thrylosTx)
	if err != nil {
		respondError(w, -32000, fmt.Sprintf("Transaction rejected: %v", err))
		return
	}

	response := txHash
	respondJSON(w, response)
}

// ===== Contract Calls =====

// Call executes a new message call immediately without creating a transaction
func (h *EthereumRPCHandler) Call(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CallData    CallArgs `json:"callData"`
		BlockNumber string   `json:"blockNumber"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}

	// Execute static call
	result, _, err := h.evmExecutor.StaticCall(
		common.HexToAddress(req.CallData.From),
		common.HexToAddress(req.CallData.To),
		req.CallData.Data,
		uint64(req.CallData.Gas),
		big.NewInt(0), // Block number - use latest
	)

	if err != nil {
		respondError(w, -32000, fmt.Sprintf("Execution reverted: %v", err))
		return
	}

	response := hexutil.Bytes(result)
	respondJSON(w, response)
}

// EstimateGas generates and returns an estimate of how much gas is necessary
func (h *EthereumRPCHandler) EstimateGas(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CallData CallArgs `json:"callData"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}

	from := common.HexToAddress(req.CallData.From)
	var to *common.Address
	if req.CallData.To != "" {
		addr := common.HexToAddress(req.CallData.To)
		to = &addr
	}

	gas, err := h.evmExecutor.EstimateGas(
		from,
		to,
		req.CallData.Data,
		req.CallData.Value.ToInt(),
	)

	if err != nil {
		respondError(w, -32000, fmt.Sprintf("Gas estimation failed: %v", err))
		return
	}

	response := hexutil.Uint64(gas)
	respondJSON(w, response)
}

// ===== Gas Price =====

// GasPrice returns the current gas price in wei
func (h *EthereumRPCHandler) GasPrice(w http.ResponseWriter, r *http.Request) {
	// Get current gas price from your config or dynamic pricing
	gasPrice := h.blockchain.GetGasPrice()

	response := (*hexutil.Big)(big.NewInt(gasPrice))
	respondJSON(w, response)
}

// MaxPriorityFeePerGas returns the maximum priority fee per gas
func (h *EthereumRPCHandler) MaxPriorityFeePerGas(w http.ResponseWriter, r *http.Request) {
	// EIP-1559 support - return suggested tip
	tip := h.blockchain.GetMaxPriorityFee()

	response := (*hexutil.Big)(big.NewInt(tip))
	respondJSON(w, response)
}

// ===== Block Information =====

// BlockNumber returns the number of most recent block
func (h *EthereumRPCHandler) BlockNumber(w http.ResponseWriter, r *http.Request) {
	height := h.blockchain.GetHeight()

	response := hexutil.Uint64(height)
	respondJSON(w, response)
}

// GetBlockByNumber returns information about a block by block number
func (h *EthereumRPCHandler) GetBlockByNumber(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BlockNumber string `json:"blockNumber"`
		FullTx      bool   `json:"fullTx"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}

	// Parse block number
	blockNum, err := parseBlockNumber(req.BlockNumber)
	if err != nil {
		respondError(w, -32602, "Invalid block number")
		return
	}

	// Get block
	block, err := h.blockchain.GetBlockByNumber(blockNum)
	if err != nil {
		respondJSON(w, nil) // Block not found
		return
	}

	// Convert to Ethereum format
	ethBlock := h.convertToEthBlock(block, req.FullTx)
	respondJSON(w, ethBlock)
}

// GetBlockByHash returns information about a block by hash
func (h *EthereumRPCHandler) GetBlockByHash(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BlockHash string `json:"blockHash"`
		FullTx    bool   `json:"fullTx"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}

	// Get block
	block, err := h.blockchain.GetBlockByHash(req.BlockHash)
	if err != nil {
		respondJSON(w, nil) // Block not found
		return
	}

	// Convert to Ethereum format
	ethBlock := h.convertToEthBlock(block, req.FullTx)
	respondJSON(w, ethBlock)
}

// ===== Transaction Information =====

// GetTransactionByHash returns the information about a transaction by hash
func (h *EthereumRPCHandler) GetTransactionByHash(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TxHash string `json:"txHash"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}

	// Get transaction
	tx, err := h.blockchain.GetTransaction(req.TxHash)
	if err != nil {
		respondJSON(w, nil) // Transaction not found
		return
	}

	// Convert to Ethereum format
	ethTx := h.convertToEthTx(tx)
	respondJSON(w, ethTx)
}

// GetTransactionReceipt returns the receipt of a transaction by hash
func (h *EthereumRPCHandler) GetTransactionReceipt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TxHash string `json:"txHash"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}

	// Get receipt
	receipt, err := h.blockchain.GetTransactionReceipt(req.TxHash)
	if err != nil {
		respondJSON(w, nil) // Receipt not found
		return
	}

	// Convert to Ethereum format
	ethReceipt := h.convertToEthReceipt(receipt)
	respondJSON(w, ethReceipt)
}

// ===== Storage =====

// GetStorageAt returns the value from a storage position at a given address
func (h *EthereumRPCHandler) GetStorageAt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address     string `json:"address"`
		Position    string `json:"position"`
		BlockNumber string `json:"blockNumber"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}

	address := common.HexToAddress(req.Address)
	key := common.HexToHash(req.Position)

	value := h.evmExecutor.GetStorageAt(address, key)

	response := value
	respondJSON(w, response)
}

// ===== Helper Types =====

type CallArgs struct {
	From     string         `json:"from"`
	To       string         `json:"to"`
	Gas      hexutil.Uint64 `json:"gas"`
	GasPrice *hexutil.Big   `json:"gasPrice"`
	Value    *hexutil.Big   `json:"value"`
	Data     hexutil.Bytes  `json:"data"`
}

// ===== Helper Functions =====

func (h *EthereumRPCHandler) convertEthTxToThrylosTx(ethTx *types.Transaction) (*core.Transaction, error) {
	// Extract sender from signature
	signer := types.LatestSignerForChainID(h.chainID)
	sender, err := types.Sender(signer, ethTx)
	if err != nil {
		return nil, fmt.Errorf("failed to recover sender: %v", err)
	}

	// Determine transaction type
	var txType core.TransactionType
	if ethTx.To() == nil {
		txType = core.TransactionType_EVM_CONTRACT_DEPLOY
	} else {
		txType = core.TransactionType_EVM_CONTRACT_CALL
	}

	// Create Thrylos transaction
	thrylosTx := &core.Transaction{
		From:      sender.Hex(),
		To:        "",
		Amount:    ethTx.Value().Int64(),
		Gas:       int64(ethTx.Gas()),
		GasPrice:  ethTx.GasPrice().Int64(),
		Nonce:     ethTx.Nonce(),
		Data:      ethTx.Data(),
		Type:      txType,
		Timestamp: time.Now().Unix(),
	}

	if ethTx.To() != nil {
		thrylosTx.To = ethTx.To().Hex()
	}

	return thrylosTx, nil
}

func (h *EthereumRPCHandler) convertToEthBlock(block *core.Block, fullTx bool) map[string]interface{} {
	result := map[string]interface{}{
		"number":          hexutil.Uint64(block.Header.Index),
		"hash":            block.Hash,
		"parentHash":      block.Header.PrevHash,
		"timestamp":       hexutil.Uint64(block.Header.Timestamp),
		"gasLimit":        hexutil.Uint64(block.Header.GasLimit),
		"gasUsed":         hexutil.Uint64(block.Header.GasUsed),
		"miner":           block.Header.Proposer,
		"difficulty":      "0x0", // PoS = 0 difficulty
		"totalDifficulty": "0x0",
		"size":            hexutil.Uint64(len(block.Transactions)),
		"transactions":    []interface{}{},
		"uncles":          []string{},
	}

	if fullTx {
		txs := make([]interface{}, len(block.Transactions))
		for i, tx := range block.Transactions {
			txs[i] = h.convertToEthTx(tx)
		}
		result["transactions"] = txs
	} else {
		txHashes := make([]string, len(block.Transactions))
		for i, tx := range block.Transactions {
			txHashes[i] = tx.Hash
		}
		result["transactions"] = txHashes
	}

	return result
}

func (h *EthereumRPCHandler) convertToEthTx(tx *core.Transaction) map[string]interface{} {
	return map[string]interface{}{
		"hash":             tx.Hash,
		"nonce":            hexutil.Uint64(tx.Nonce),
		"from":             tx.From,
		"to":               tx.To,
		"value":            (*hexutil.Big)(big.NewInt(tx.Amount)),
		"gas":              hexutil.Uint64(tx.Gas),
		"gasPrice":         (*hexutil.Big)(big.NewInt(tx.GasPrice)),
		"input":            hexutil.Bytes(tx.Data),
		"v":                "0x0", // Signature components
		"r":                "0x0",
		"s":                "0x0",
		"transactionIndex": hexutil.Uint64(0), // TODO: Get from block
		"blockHash":        "",                // TODO: Get from block
		"blockNumber":      hexutil.Uint64(0), // TODO: Get from block
	}
}

func (h *EthereumRPCHandler) convertToEthReceipt(receipt *core.TransactionReceipt) map[string]interface{} {
	return map[string]interface{}{
		"transactionHash":   receipt.TxHash,
		"transactionIndex":  hexutil.Uint64(receipt.Index),
		"blockHash":         receipt.BlockHash,
		"blockNumber":       hexutil.Uint64(receipt.BlockNumber),
		"from":              receipt.From,
		"to":                receipt.To,
		"cumulativeGasUsed": hexutil.Uint64(receipt.CumulativeGasUsed),
		"gasUsed":           hexutil.Uint64(receipt.GasUsed),
		"contractAddress":   receipt.ContractAddress,
		"logs":              receipt.Logs,
		"logsBloom":         "0x" + string(make([]byte, 512)), // 256 bytes hex
		"status":            hexutil.Uint64(receipt.Status),
	}
}

func parseBlockNumber(blockNumber string) (uint64, error) {
	switch blockNumber {
	case "latest", "pending":
		return 0, nil // Return latest
	case "earliest":
		return 1, nil
	default:
		// Parse hex number
		num, err := hexutil.DecodeUint64(blockNumber)
		if err != nil {
			return 0, err
		}
		return num, nil
	}
}

func respondJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"result":  data,
	})
}

func respondError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
	})
}
