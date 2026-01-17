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
	"github.com/thrylos-labs/go-thrylos/core/math"
	"github.com/thrylos-labs/go-thrylos/proto/core"
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

func (h *EthereumRPCHandler) ChainId(w http.ResponseWriter, r *http.Request) {
	response := hexutil.Uint64(h.chainID.Uint64())
	respondJSON(w, response)
}

func (h *EthereumRPCHandler) NetworkId(w http.ResponseWriter, r *http.Request) {
	response := fmt.Sprintf("%d", h.chainID.Uint64())
	respondJSON(w, response)
}

// ➕ NEW: Web3_clientVersion (Required for handshake)
func (h *EthereumRPCHandler) ClientVersion(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, "Thrylos/v1.0.0/go-1.21/linux-amd64")
}

// ➕ NEW: Eth_coinbase (Required to prevent polling errors)
func (h *EthereumRPCHandler) Coinbase(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, "0x0000000000000000000000000000000000000000") // Return zero address or miner address
}

// ➕ NEW: Eth_mining (Required to prevent polling errors)
func (h *EthereumRPCHandler) Mining(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, false)
}

// ➕ NEW: Eth_syncing (Required to prevent polling errors)
func (h *EthereumRPCHandler) Syncing(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, false)
}

// ===== Account Information =====

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

	// GetBalance returns (*big.Int, error)
	balance, err := h.blockchain.GetBalance(address.Hex())
	if err != nil || balance == nil {
		balance = big.NewInt(0)
	}

	// Cast *big.Int directly to *hexutil.Big
	response := (*hexutil.Big)(balance)
	respondJSON(w, response)
}

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

func (h *EthereumRPCHandler) SendRawTransaction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Data string `json:"data"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}

	txBytes, err := hexutil.Decode(req.Data)
	if err != nil {
		respondError(w, -32602, "Invalid transaction data")
		return
	}

	ethTx := new(types.Transaction)
	if err := rlp.DecodeBytes(txBytes, ethTx); err != nil {
		respondError(w, -32602, "Invalid transaction encoding")
		return
	}

	// Convert and sign correctly
	thrylosTx, err := h.convertEthTxToThrylosTx(ethTx)
	if err != nil {
		respondError(w, -32602, fmt.Sprintf("Transaction conversion failed: %v", err))
		return
	}

	// 🛡️ INPUT VALIDATION (Medium) - FIXED
	// Validate fields before submitting to mempool/blockchain
	if thrylosTx.Gas < 21000 {
		respondError(w, -32602, "Intrinsic gas too low")
		return
	}
	// Check Address Length (0x + 40 chars = 42)
	if len(thrylosTx.From) != 42 {
		respondError(w, -32602, "Invalid sender address")
		return
	}

	// Submit to blockchain
	if err := h.blockchain.AddTransaction(thrylosTx); err != nil {
		respondError(w, -32000, fmt.Sprintf("Transaction rejected: %v", err))
		return
	}

	// Return the Hash (MetaMask uses this to poll for receipt)
	response := thrylosTx.Hash
	respondJSON(w, response)
}

// ===== Contract Calls =====

func (h *EthereumRPCHandler) Call(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CallData    CallArgs `json:"callData"`
		BlockNumber string   `json:"blockNumber"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}

	// 🛡️ INPUT VALIDATION (Medium) - FIXED
	// 1. Gas Validator: Cap at 30M (Block Limit)
	gas := uint64(req.CallData.Gas)
	if gas == 0 || gas > 30_000_000 {
		gas = 30_000_000
	}

	// 2. Value Validator: Prevent negative values
	val := big.NewInt(0)
	if req.CallData.Value != nil {
		val = req.CallData.Value.ToInt()
		if val.Sign() < 0 {
			respondError(w, -32602, "Value cannot be negative")
			return
		}
	}

	// 3. Convert address
	fromAddr := common.HexToAddress(req.CallData.From)

	// 4. Fetch current nonce (Required to pass Rust security check)
	currentNonce := h.evmExecutor.GetNonce(fromAddr)

	// 5. Execute with Type Cast and Nonce
	result, _, err := h.evmExecutor.ExecuteCall(
		fromAddr,
		common.HexToAddress(req.CallData.To),
		[]byte(req.CallData.Data),
		gas,
		val,
		currentNonce,
	)

	if err != nil {
		respondError(w, -32000, fmt.Sprintf("Execution reverted: %v", err))
		return
	}

	response := hexutil.Bytes(result)
	respondJSON(w, response)
}

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

	val := big.NewInt(0)
	if req.CallData.Value != nil {
		val = req.CallData.Value.ToInt()
	}

	gas, err := h.evmExecutor.EstimateGas(
		from,
		to,
		req.CallData.Data,
		val,
	)

	if err != nil {
		respondError(w, -32000, fmt.Sprintf("Gas estimation failed: %v", err))
		return
	}

	response := hexutil.Uint64(gas)
	respondJSON(w, response)
}

// ===== Gas Price =====

func (h *EthereumRPCHandler) GasPrice(w http.ResponseWriter, r *http.Request) {
	gasPriceStr := h.blockchain.GetConfig().Economics.BaseGasPrice
	gasPriceBig := math.ParseBigInt(gasPriceStr)
	response := (*hexutil.Big)(gasPriceBig)
	respondJSON(w, response)
}

func (h *EthereumRPCHandler) MaxPriorityFeePerGas(w http.ResponseWriter, r *http.Request) {
	tip := big.NewInt(1000000000) // 1 Gwei
	response := (*hexutil.Big)(tip)
	respondJSON(w, response)
}

// ===== Block Information =====

func (h *EthereumRPCHandler) BlockNumber(w http.ResponseWriter, r *http.Request) {
	height := h.blockchain.GetHeight()
	response := hexutil.Uint64(height)
	respondJSON(w, response)
}

func (h *EthereumRPCHandler) GetBlockByNumber(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BlockNumber string `json:"blockNumber"`
		FullTx      bool   `json:"fullTx"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	blockNum, _ := parseBlockNumber(req.BlockNumber)
	block, err := h.blockchain.GetBlockByIndex(int64(blockNum))
	if err != nil {
		respondJSON(w, nil)
		return
	}
	ethBlock := h.convertToEthBlock(block, req.FullTx)
	respondJSON(w, ethBlock)
}

func (h *EthereumRPCHandler) GetBlockByHash(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BlockHash string `json:"blockHash"`
		FullTx    bool   `json:"fullTx"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	block, err := h.blockchain.GetBlock(req.BlockHash)
	if err != nil {
		respondJSON(w, nil)
		return
	}
	ethBlock := h.convertToEthBlock(block, req.FullTx)
	respondJSON(w, ethBlock)
}

// ===== Transaction Information =====

func (h *EthereumRPCHandler) GetTransactionByHash(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TxHash string `json:"txHash"` // Some clients send it as parameter
	}
	// MetaMask might send params as array, this handler assumes body or manual parsing
	// Standard JSON-RPC 2.0 params are [hash]
	var params []interface{}
	if err := json.NewDecoder(r.Body).Decode(&params); err == nil && len(params) > 0 {
		if hashStr, ok := params[0].(string); ok {
			req.TxHash = hashStr
		}
	} else if req.TxHash == "" {
		// Fallback for body parsing
		json.NewDecoder(r.Body).Decode(&req)
	}

	if req.TxHash == "" {
		// Try Mux vars if routed that way, though standard RPC is POST
		// Returning nil for now if not found
		respondJSON(w, nil)
		return
	}

	// 1. Try to find in storage (Mined)
	tx, err := h.blockchain.GetWorldState().GetTransactionFromStorage(req.TxHash)
	if err == nil && tx != nil {
		respondJSON(w, h.convertToEthTx(tx))
		return
	}

	// 2. Try to find in mempool (Pending)
	pendingTxs := h.blockchain.GetPendingTransactions()
	for _, pTx := range pendingTxs {
		if pTx.Id == req.TxHash || pTx.Hash == req.TxHash {
			respondJSON(w, h.convertToEthTx(pTx))
			return
		}
	}

	respondJSON(w, nil)
}

func (h *EthereumRPCHandler) GetTransactionReceipt(w http.ResponseWriter, r *http.Request) {
	var params []interface{}
	var txHash string

	if err := json.NewDecoder(r.Body).Decode(&params); err == nil && len(params) > 0 {
		if hashStr, ok := params[0].(string); ok {
			txHash = hashStr
		}
	}

	if txHash == "" {
		respondJSON(w, nil)
		return
	}

	// Check if transaction exists and is confirmed
	tx, err := h.blockchain.GetWorldState().GetTransactionFromStorage(txHash)
	if err != nil || tx == nil {
		respondJSON(w, nil)
		return
	}

	// NOTE: Since Thrylos DB currently doesn't map TxHash -> BlockHash directly in a fast index,
	// and we don't have block info in the Tx struct, we fake the block info for the Testnet.
	// For Mainnet, you MUST add BlockNumber/Hash to the stored Transaction struct or a separate index.

	// Construct Receipt
	receipt := map[string]interface{}{
		"transactionHash":   tx.Hash,
		"transactionIndex":  hexutil.Uint64(0),
		"blockHash":         "0x0000000000000000000000000000000000000000000000000000000000000000", // Unknown without index
		"blockNumber":       hexutil.Uint64(h.blockchain.GetHeight()),                             // Approx
		"from":              tx.From,
		"to":                tx.To,
		"cumulativeGasUsed": hexutil.Uint64(tx.Gas),
		"gasUsed":           hexutil.Uint64(tx.Gas),
		"contractAddress":   nil, // Populate if Deploy
		"logs":              []interface{}{},
		"logsBloom":         "0x0000000000000000000000000000000000000000",
		"status":            "0x1", // Success (Thrylos only commits success)
	}

	if tx.Type == core.TransactionType_EVM_CONTRACT_DEPLOY {
		// Calculate contract address if it was a deployment
		// (Optional enhancement: Store this in Tx metadata)
	}

	respondJSON(w, receipt)
}

// ===== Storage =====

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
	respondJSON(w, value)
}

// ===== Helper Types & Functions =====

type CallArgs struct {
	From     string         `json:"from"`
	To       string         `json:"to"`
	Gas      hexutil.Uint64 `json:"gas"`
	GasPrice *hexutil.Big   `json:"gasPrice"`
	Value    *hexutil.Big   `json:"value"`
	Data     hexutil.Bytes  `json:"data"`
}

func (h *EthereumRPCHandler) convertEthTxToThrylosTx(ethTx *types.Transaction) (*core.Transaction, error) {
	signer := types.LatestSignerForChainID(h.chainID)
	sender, err := types.Sender(signer, ethTx)
	if err != nil {
		return nil, fmt.Errorf("failed to recover sender: %v", err)
	}

	var txType core.TransactionType
	if ethTx.To() == nil {
		txType = core.TransactionType_EVM_CONTRACT_DEPLOY
	} else {
		txType = core.TransactionType_EVM_CONTRACT_CALL
	}

	// 1. Extract Raw Signature Values (V, R, S)
	v, r, s := ethTx.RawSignatureValues()

	// 2. Construct Standard [R || S || V] Signature (65 bytes)
	sigBytes := make([]byte, 65)

	// Pad R and S to 32 bytes
	rBytes := r.Bytes()
	sBytes := s.Bytes()

	// Copy R into [0:32]
	copy(sigBytes[32-len(rBytes):32], rBytes)
	// Copy S into [32:64]
	copy(sigBytes[64-len(sBytes):64], sBytes)

	// Normalize V to 0 or 1 for standard recovery (EIP-155 support)
	// EIP-155: v = 2 * chainId + 35 + yParity
	// Legacy: v = 27 + yParity
	var vByte byte
	if v.Cmp(big.NewInt(35)) >= 0 {
		// EIP-155
		// yParity = v - (2 * chainId + 35)
		subVal := new(big.Int).Mul(h.chainID, big.NewInt(2))
		subVal.Add(subVal, big.NewInt(35))
		vByte = byte(new(big.Int).Sub(v, subVal).Uint64())
	} else if v.Cmp(big.NewInt(27)) >= 0 {
		// Legacy (27/28)
		vByte = byte(v.Uint64() - 27)
	} else {
		// Already normalized (0/1)
		vByte = byte(v.Uint64())
	}
	sigBytes[64] = vByte

	thrylosTx := &core.Transaction{
		From:      sender.Hex(),
		To:        "", // Set below
		Amount:    ethTx.Value().String(),
		Gas:       int64(ethTx.Gas()),
		GasPrice:  ethTx.GasPrice().String(),
		Nonce:     ethTx.Nonce(),
		Data:      ethTx.Data(),
		Type:      txType,
		Timestamp: time.Now().Unix(),
		Signature: sigBytes, // ✅ Crucial: Pass the signature!
		ChainId:   h.chainID.String(),
	}

	if ethTx.To() != nil {
		thrylosTx.To = ethTx.To().Hex()
	}

	// ✅ CRITICAL: Calculate the Hash using Thrylos logic so it matches Validation
	// This requires access to the TransactionValidator logic.
	// Since we are in the API package, we call the helper via WorldState if available,
	// or recalculate it manually here using the exact same logic as `core/transaction/validator.go`.

	// Getting the validator from world state is cleaner:
	if h.blockchain != nil && h.blockchain.GetWorldState() != nil {
		tv := h.blockchain.GetWorldState().GetTransactionValidator()
		if tv != nil {
			hash, err := tv.CalculateTransactionHash(thrylosTx)
			if err == nil {
				thrylosTx.Hash = hash
				// Also set ID to hash for consistency
				thrylosTx.Id = hash
			} else {
				return nil, fmt.Errorf("failed to calculate tx hash: %v", err)
			}
		}
	}

	// Fallback if validator isn't reachable (shouldn't happen in prod)
	if thrylosTx.Hash == "" {
		return nil, fmt.Errorf("transaction validator unavailable")
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
		"miner":           block.Header.Validator,
		"difficulty":      "0x0",
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
		"value":            (*hexutil.Big)(math.ParseBigInt(tx.Amount)),
		"gas":              hexutil.Uint64(tx.Gas),
		"gasPrice":         (*hexutil.Big)(math.ParseBigInt(tx.GasPrice)),
		"input":            hexutil.Bytes(tx.Data),
		"v":                "0x1c", // Placeholder
		"r":                "0x0",  // Placeholder
		"s":                "0x0",  // Placeholder
		"transactionIndex": hexutil.Uint64(0),
		"blockHash":        "",
		"blockNumber":      hexutil.Uint64(0),
	}
}

// Helpers
func parseBlockNumber(blockNumber string) (uint64, error) {
	switch blockNumber {
	case "latest", "pending":
		return 0, nil
	case "earliest":
		return 1, nil
	default:
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
