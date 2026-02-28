// api/ethereum_rpc.go
// Ethereum JSON-RPC API for MetaMask compatibility

package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"strings"
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
	var params []interface{}

	// Decode the JSON-RPC params array
	var rpcReq struct {
		Params []interface{} `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&rpcReq); err != nil {
		log.Printf("❌ Failed to decode request: %v", err)
		respondError(w, -32700, "Parse error")
		return
	}

	params = rpcReq.Params

	if len(params) < 1 {
		respondError(w, -32602, "Invalid params")
		return
	}

	// First param is the address
	addressStr, ok := params[0].(string)
	if !ok {
		respondError(w, -32602, "Invalid address parameter")
		return
	}

	address := common.HexToAddress(addressStr)
	addressHex := address.Hex()

	// 🔍 DEBUG: Log what we're querying
	log.Printf("🔍 eth_getBalance request: input='%s', normalized='%s'", addressStr, addressHex)

	// GetBalance returns (*big.Int, error)
	balance, err := h.blockchain.GetBalance(addressHex)

	// 🔍 DEBUG: Log the result
	if err != nil {
		log.Printf("❌ GetBalance error: %v", err)
		balance = big.NewInt(0)
	} else if balance == nil {
		log.Printf("⚠️ GetBalance returned nil, using 0")
		balance = big.NewInt(0)
	} else {
		log.Printf("✅ GetBalance result: %s wei", balance.String())
	}

	// Cast *big.Int directly to *hexutil.Big
	response := (*hexutil.Big)(balance)
	respondJSON(w, response)
}

func (h *EthereumRPCHandler) GetTransactionCount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address     string `json:"address"`
		BlockNumber string `json:"blockNumber"`
		Params      []json.RawMessage `json:"params"`
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, -32700, "Parse error")
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}
	// JSON-RPC standard shape: params = [address, blockTag]
	if len(req.Params) > 0 {
		var addr string
		if err := json.Unmarshal(req.Params[0], &addr); err == nil && addr != "" {
			req.Address = addr
		}
	}
	if len(req.Params) > 1 {
		var blockTag string
		if err := json.Unmarshal(req.Params[1], &blockTag); err == nil && blockTag != "" {
			req.BlockNumber = blockTag
		}
	}
	if req.Address == "" {
		respondError(w, -32602, "Invalid params")
		return
	}

	address := common.HexToAddress(req.Address)
	addressHex := address.Hex()
	normalizedAddress := strings.ToLower(addressHex)

	nonce, err := h.blockchain.GetNonce(addressHex)
	if err != nil {
		// Some stores may key addresses in normalized form.
		if fallbackNonce, fallbackErr := h.blockchain.GetNonce(normalizedAddress); fallbackErr == nil {
			nonce = fallbackNonce
		} else {
			nonce = 0
		}
	}

	// For "pending", include mempool transactions so wallets don't reuse stale nonce.
	if req.BlockNumber == "" || req.BlockNumber == "pending" || req.BlockNumber == "latest" {
		// Track "next nonce" as max(sourceNonce+1) across all known sources.
		nextNonce := nonce

		// Include replay-detector nonce progression (can advance before account nonce commits).
		if h.blockchain != nil && h.blockchain.GetWorldState() != nil {
			if tv := h.blockchain.GetWorldState().GetTransactionValidator(); tv != nil {
				if replayNonce, ok := tv.GetReplayNonce(addressHex); ok && replayNonce+1 > nextNonce {
					nextNonce = replayNonce + 1
				}
				if replayNonce, ok := tv.GetReplayNonce(normalizedAddress); ok && replayNonce+1 > nextNonce {
					nextNonce = replayNonce + 1
				}
			}
		}

		pendingTxs := h.blockchain.GetPendingTransactions()
		for _, tx := range pendingTxs {
			if tx == nil {
				continue
			}
			if strings.EqualFold(tx.From, addressHex) && tx.Nonce+1 > nextNonce {
				nextNonce = tx.Nonce + 1
			}
		}

		// Include confirmed tx history from storage as a final guard against under-reporting.
		if h.blockchain != nil && h.blockchain.GetWorldState() != nil {
			if confirmedTxs, txErr := h.blockchain.GetWorldState().GetTransactionsByAddress(addressHex, 1000); txErr == nil {
				for _, tx := range confirmedTxs {
					if tx == nil {
						continue
					}
					if strings.EqualFold(tx.From, addressHex) && tx.Nonce+1 > nextNonce {
						nextNonce = tx.Nonce + 1
					}
				}
			}
		}

		// Never return a value lower than the account nonce.
		if nonce > nextNonce {
			nextNonce = nonce
		}
		nonce = nextNonce
	}

	// For strict "earliest"/explicit historical tags, keep account-state nonce only.
	if req.BlockNumber == "earliest" {
		nonce = 0
	}

	// Normalize to the highest known monotonic nonce floor when replay detector has state.
	if h.blockchain != nil && h.blockchain.GetWorldState() != nil {
		if tv := h.blockchain.GetWorldState().GetTransactionValidator(); tv != nil {
			if replayNonce, ok := tv.GetReplayNonce(addressHex); ok && replayNonce+1 > nonce {
				nonce = replayNonce + 1
			}
			if replayNonce, ok := tv.GetReplayNonce(normalizedAddress); ok && replayNonce+1 > nonce {
				nonce = replayNonce + 1
			}
		}
	}

	response := hexutil.Uint64(nonce)
	respondJSON(w, response)
}

func (h *EthereumRPCHandler) GetCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address     string `json:"address"`
		BlockNumber string `json:"blockNumber"`
		Params      []json.RawMessage `json:"params"`
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, -32700, "Parse error")
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}
	// JSON-RPC standard shape: params = [address, blockTag]
	if len(req.Params) > 0 {
		var addr string
		if err := json.Unmarshal(req.Params[0], &addr); err == nil && addr != "" {
			req.Address = addr
		}
	}
	if req.Address == "" {
		respondError(w, -32602, "Invalid params")
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
		Data   string            `json:"data"`
		Params []json.RawMessage `json:"params"`
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, -32700, "Parse error")
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}
	// JSON-RPC standard shape: params = [rawTxHex]
	if len(req.Params) > 0 {
		var rawTx string
		if err := json.Unmarshal(req.Params[0], &rawTx); err == nil && rawTx != "" {
			req.Data = rawTx
		}
	}
	if req.Data == "" {
		respondError(w, -32602, "Invalid transaction data")
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
	const maxGasLimit = 30000000
	if thrylosTx.Gas < 21000 {
		respondError(w, -32602, "Intrinsic gas too low")
		return
	}
	if thrylosTx.Gas > maxGasLimit {
		respondError(w, -32602, fmt.Sprintf("Gas limit %d exceeds maximum %d", thrylosTx.Gas, maxGasLimit))
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
	response := toRPCTxHash(thrylosTx.Hash)
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
		Params   []json.RawMessage `json:"params"`
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, -32700, "Parse error")
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}
	// JSON-RPC standard shape: params = [callObject, blockTag?]
	if len(req.Params) > 0 {
		var callData CallArgs
		if err := json.Unmarshal(req.Params[0], &callData); err == nil {
			req.CallData = callData
		}
	}

	// SECURITY: Validate gas if provided
	const maxGasLimit = 30000000
	if req.CallData.Gas > 0 && uint64(req.CallData.Gas) > maxGasLimit {
		respondError(w, -32602, fmt.Sprintf("Gas limit %d exceeds maximum %d", req.CallData.Gas, maxGasLimit))
		return
	}

	// Fast path for plain value transfers (EOA -> EOA, no calldata):
	// return canonical intrinsic gas instead of invoking REVM estimation.
	if req.CallData.To != "" && len(req.CallData.Data) == 0 {
		response := hexutil.Uint64(21000)
		respondJSON(w, response)
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
		// Safety fallback: if REVM estimation panics/fails for empty calldata,
		// still provide intrinsic transfer gas to keep wallets functional.
		if req.CallData.To != "" && len(req.CallData.Data) == 0 {
			response := hexutil.Uint64(21000)
			respondJSON(w, response)
			return
		}
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

// FeeHistory returns EIP-1559-style fee history metadata.
// Thrylos currently uses a legacy gas model, so we provide deterministic
// synthetic base fee and reward values for wallet compatibility.
func (h *EthereumRPCHandler) FeeHistory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BlockCount        string            `json:"blockCount"`
		NewestBlock       string            `json:"newestBlock"`
		RewardPercentiles []float64         `json:"rewardPercentiles"`
		Params            []json.RawMessage `json:"params"`
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, -32700, "Parse error")
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}

	// JSON-RPC standard shape: params = [blockCount, newestBlock, rewardPercentiles]
	if len(req.Params) > 0 {
		var blockCount string
		if err := json.Unmarshal(req.Params[0], &blockCount); err == nil && blockCount != "" {
			req.BlockCount = blockCount
		}
	}
	if len(req.Params) > 1 {
		var newest string
		if err := json.Unmarshal(req.Params[1], &newest); err == nil && newest != "" {
			req.NewestBlock = newest
		}
	}
	if len(req.Params) > 2 {
		var p []float64
		if err := json.Unmarshal(req.Params[2], &p); err == nil {
			req.RewardPercentiles = p
		}
	}

	// Parse block count (hex quantity), cap to keep payload bounded.
	blockCount := uint64(10)
	if req.BlockCount != "" {
		if parsed, err := hexutil.DecodeUint64(req.BlockCount); err == nil {
			blockCount = parsed
		}
	}
	if blockCount == 0 {
		blockCount = 1
	}
	if blockCount > 1024 {
		blockCount = 1024
	}

	// Resolve newest block.
	latest := uint64(h.blockchain.GetHeight())
	newest := latest
	switch req.NewestBlock {
	case "", "latest", "pending":
		newest = latest
	case "earliest":
		newest = 0
	default:
		if parsed, err := hexutil.DecodeUint64(req.NewestBlock); err == nil {
			newest = parsed
		}
	}

	var oldest uint64
	if newest+1 > blockCount {
		oldest = newest + 1 - blockCount
	} else {
		oldest = 0
		blockCount = newest + 1
	}

	baseGasPrice := math.ParseBigInt(h.blockchain.GetConfig().Economics.BaseGasPrice)
	if baseGasPrice.Sign() <= 0 {
		baseGasPrice = big.NewInt(1)
	}
	priorityTip := big.NewInt(1)

	baseFee := make([]string, 0, blockCount+1)
	gasUsedRatio := make([]float64, 0, blockCount)
	reward := make([][]string, 0, blockCount)

	// Build history for [oldest..newest]
	for i := uint64(0); i < blockCount; i++ {
		height := int64(oldest + i)
		block, err := h.blockchain.GetBlockByIndex(height)

		// Legacy chain: keep base fee stable at base gas price.
		baseFee = append(baseFee, (*hexutil.Big)(baseGasPrice).String())

		if err != nil || block == nil || block.Header == nil || block.Header.GasLimit == 0 {
			gasUsedRatio = append(gasUsedRatio, 0)
		} else {
			ratio := float64(block.Header.GasUsed) / float64(block.Header.GasLimit)
			if ratio < 0 {
				ratio = 0
			}
			if ratio > 1 {
				ratio = 1
			}
			gasUsedRatio = append(gasUsedRatio, ratio)
		}

		if len(req.RewardPercentiles) > 0 {
			row := make([]string, len(req.RewardPercentiles))
			for j := range req.RewardPercentiles {
				row[j] = (*hexutil.Big)(priorityTip).String()
			}
			reward = append(reward, row)
		}
	}
	// Append "next block" base fee as required by eth_feeHistory.
	baseFee = append(baseFee, (*hexutil.Big)(baseGasPrice).String())

	resp := map[string]interface{}{
		"oldestBlock":  hexutil.Uint64(oldest).String(),
		"baseFeePerGas": baseFee,
		"gasUsedRatio": gasUsedRatio,
	}
	if len(req.RewardPercentiles) > 0 {
		resp["reward"] = reward
	}

	respondJSON(w, resp)
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
		Params      []json.RawMessage `json:"params"`
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, -32700, "Parse error")
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}
	if len(req.Params) > 0 {
		var blockTag string
		if err := json.Unmarshal(req.Params[0], &blockTag); err == nil {
			req.BlockNumber = blockTag
		}
	}
	if len(req.Params) > 1 {
		var fullTx bool
		if err := json.Unmarshal(req.Params[1], &fullTx); err == nil {
			req.FullTx = fullTx
		}
	}

	var blockNum uint64
	switch req.BlockNumber {
	case "", "latest", "pending":
		blockNum = uint64(h.blockchain.GetHeight())
	case "earliest":
		blockNum = 0
	default:
		parsed, err := parseBlockNumber(req.BlockNumber)
		if err != nil {
			respondJSON(w, nil)
			return
		}
		blockNum = parsed
	}
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
		Params    []json.RawMessage `json:"params"`
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, -32700, "Parse error")
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}
	if len(req.Params) > 0 {
		var blockHash string
		if err := json.Unmarshal(req.Params[0], &blockHash); err == nil {
			req.BlockHash = blockHash
		}
	}
	if len(req.Params) > 1 {
		var fullTx bool
		if err := json.Unmarshal(req.Params[1], &fullTx); err == nil {
			req.FullTx = fullTx
		}
	}

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
		TxHash string            `json:"txHash"` // non-standard fallback
		Params []json.RawMessage `json:"params"` // JSON-RPC standard: [hash]
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, -32700, "Parse error")
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}
	if len(req.Params) > 0 {
		var hash string
		if err := json.Unmarshal(req.Params[0], &hash); err == nil && hash != "" {
			req.TxHash = hash
		}
	}

	if req.TxHash == "" {
		// Try Mux vars if routed that way, though standard RPC is POST
		// Returning nil for now if not found
		respondJSON(w, nil)
		return
	}

	normalizedHash := normalizeTxHash(req.TxHash)
	if normalizedHash == "" {
		respondJSON(w, nil)
		return
	}

	// 1. Try mempool first (pending transaction)
	pendingTxs := h.blockchain.GetPendingTransactions()
	for _, pTx := range pendingTxs {
		if pTx == nil {
			continue
		}
		if normalizeTxHash(pTx.Id) == normalizedHash || normalizeTxHash(pTx.Hash) == normalizedHash {
			respondJSON(w, h.convertToEthPendingTx(pTx))
			return
		}
	}

	// 2. Try to find mined tx location by scanning canonical chain
	loc, err := h.findMinedTxLocation(normalizedHash)
	if err == nil && loc != nil && loc.Tx != nil {
		respondJSON(w, h.convertToEthMinedTx(loc.Tx, loc.Block, loc.TxIndex))
		return
	}

	// 3. Final fallback: direct storage lookup (if hash key matches legacy format)
	tx, err := h.blockchain.GetWorldState().GetTransactionFromStorage(req.TxHash)
	if err == nil && tx != nil {
		respondJSON(w, h.convertToEthPendingTx(tx))
		return
	}
	tx, err = h.blockchain.GetWorldState().GetTransactionFromStorage(normalizedHash)
	if err == nil && tx != nil {
		respondJSON(w, h.convertToEthPendingTx(tx))
		return
	}

	respondJSON(w, nil)
}

func (h *EthereumRPCHandler) GetTransactionReceipt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TxHash string            `json:"txHash"` // non-standard fallback
		Params []json.RawMessage `json:"params"` // JSON-RPC standard: [hash]
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, -32700, "Parse error")
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		respondError(w, -32700, "Parse error")
		return
	}

	txHash := req.TxHash
	if len(req.Params) > 0 {
		var hash string
		if err := json.Unmarshal(req.Params[0], &hash); err == nil && hash != "" {
			txHash = hash
		}
	}

	if txHash == "" {
		respondJSON(w, nil)
		return
	}
	normalizedHash := normalizeTxHash(txHash)

	// Pending txs do not have receipts yet.
	pendingTxs := h.blockchain.GetPendingTransactions()
	for _, pTx := range pendingTxs {
		if pTx == nil {
			continue
		}
		if normalizeTxHash(pTx.Id) == normalizedHash || normalizeTxHash(pTx.Hash) == normalizedHash {
			respondJSON(w, nil)
			return
		}
	}

	// Find mined tx with exact block + index.
	loc, err := h.findMinedTxLocation(normalizedHash)
	if err != nil || loc == nil || loc.Tx == nil || loc.Block == nil || loc.Block.Header == nil {
		respondJSON(w, nil)
		return
	}

	// Construct Receipt
	receipt := map[string]interface{}{
		"transactionHash":   toRPCTxHash(loc.Tx.Hash),
		"transactionIndex":  hexutil.Uint64(loc.TxIndex),
		"blockHash":         toRPCTxHash(loc.Block.Hash),
		"blockNumber":       hexutil.Uint64(loc.Block.Header.Index),
		"from":              loc.Tx.From,
		"to":                loc.Tx.To,
		"cumulativeGasUsed": hexutil.Uint64(loc.Tx.Gas),
		"gasUsed":           hexutil.Uint64(loc.Tx.Gas),
		"contractAddress":   nil, // Populate if Deploy
		"logs":              []interface{}{},
		"logsBloom":         "0x" + strings.Repeat("0", 512),
		"status":            "0x1", // Success (Thrylos only commits success)
	}

	if loc.Tx.Type == core.TransactionType_EVM_CONTRACT_DEPLOY {
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
	switch {
	case ethTx.To() == nil:
		txType = core.TransactionType_EVM_CONTRACT_DEPLOY
	case len(ethTx.Data()) == 0:
		// Plain value transfer from MetaMask should follow native transfer rules.
		txType = core.TransactionType_TRANSFER
	default:
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

	chainID := h.chainID.String()
	if h.blockchain != nil && h.blockchain.GetConfig() != nil {
		if cfgChainID := strings.TrimSpace(h.blockchain.GetConfig().Network.ChainID); cfgChainID != "" {
			chainID = cfgChainID
		}
	}

	thrylosTx := &core.Transaction{
		// Use the Ethereum transaction hash as stable transaction ID.
		// IMPORTANT: Id participates in Thrylos hash calculation, so it must be set
		// before calling CalculateTransactionHash.
		Id:        ethTx.Hash().Hex(),
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
		ChainId:   chainID,
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
			txs[i] = h.convertToEthMinedTx(tx, block, i)
		}
		result["transactions"] = txs
	} else {
		txHashes := make([]string, len(block.Transactions))
		for i, tx := range block.Transactions {
			txHashes[i] = toRPCTxHash(tx.Hash)
		}
		result["transactions"] = txHashes
	}

	return result
}

func (h *EthereumRPCHandler) convertToEthPendingTx(tx *core.Transaction) map[string]interface{} {
	return map[string]interface{}{
		"hash":             toRPCTxHash(tx.Hash),
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
		"transactionIndex": nil,
		"blockHash":        nil,
		"blockNumber":      nil,
	}
}

func (h *EthereumRPCHandler) convertToEthMinedTx(tx *core.Transaction, block *core.Block, txIndex int) map[string]interface{} {
	result := h.convertToEthPendingTx(tx)
	result["transactionIndex"] = hexutil.Uint64(txIndex)
	if block != nil && block.Header != nil {
		result["blockHash"] = toRPCTxHash(block.Hash)
		result["blockNumber"] = hexutil.Uint64(block.Header.Index)
	}
	return result
}

type txLocation struct {
	Tx      *core.Transaction
	Block   *core.Block
	TxIndex int
}

func (h *EthereumRPCHandler) findMinedTxLocation(targetHash string) (*txLocation, error) {
	if h == nil || h.blockchain == nil {
		return nil, fmt.Errorf("blockchain unavailable")
	}

	height := h.blockchain.GetHeight()
	for i := height; i >= 0; i-- {
		block, err := h.blockchain.GetBlockByIndex(i)
		if err != nil || block == nil {
			continue
		}
		for idx, tx := range block.Transactions {
			if tx == nil {
				continue
			}
			if normalizeTxHash(tx.Hash) == targetHash || normalizeTxHash(tx.Id) == targetHash {
				return &txLocation{
					Tx:      tx,
					Block:   block,
					TxIndex: idx,
				}, nil
			}
		}
	}

	return nil, nil
}

// Helpers
func parseBlockNumber(blockNumber string) (uint64, error) {
	switch blockNumber {
	case "latest", "pending":
		return 0, nil
	case "earliest":
		return 0, nil
	default:
		num, err := hexutil.DecodeUint64(blockNumber)
		if err != nil {
			return 0, err
		}
		return num, nil
	}
}

func normalizeTxHash(hash string) string {
	hash = strings.TrimSpace(strings.ToLower(hash))
	hash = strings.TrimPrefix(hash, "0x")
	return hash
}

func toRPCTxHash(hash string) string {
	norm := normalizeTxHash(hash)
	if norm == "" {
		return "0x"
	}
	return "0x" + norm
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
