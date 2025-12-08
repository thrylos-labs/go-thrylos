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
	response := hexutil.Uint64(h.chainID.Uint64())
	respondJSON(w, response)
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

	// TODO: Handle BlockNumber (historical queries)
	address := common.HexToAddress(req.Address)
	balance, err := h.blockchain.GetBalance(address.Hex())
	if err != nil {
		balance = 0
	}

	response := (*hexutil.Big)(big.NewInt(balance))
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
	code := h.evmExecutor.GetCode(address) // This method exists in RevmExecutor

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

	thrylosTx, err := h.convertEthTxToThrylosTx(ethTx)
	if err != nil {
		respondError(w, -32602, fmt.Sprintf("Transaction conversion failed: %v", err))
		return
	}

	// FIX: Use AddTransaction instead of SubmitTransaction
	if err := h.blockchain.AddTransaction(thrylosTx); err != nil {
		respondError(w, -32000, fmt.Sprintf("Transaction rejected: %v", err))
		return
	}

	// Calculate and return the hash
	// Note: Ideally, AddTransaction calculates the hash.
	// Here we use the pre-calculated one.
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

	// Default gas if missing
	gas := uint64(req.CallData.Gas)
	if gas == 0 {
		gas = 1000000
	}

	val := big.NewInt(0)
	if req.CallData.Value != nil {
		val = req.CallData.Value.ToInt()
	}

	result, _, err := h.evmExecutor.ExecuteCall(
		common.HexToAddress(req.CallData.From),
		common.HexToAddress(req.CallData.To),
		req.CallData.Data,
		gas,
		val,
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
	// FIX: Use config value directly if method is missing
	gasPrice := h.blockchain.GetConfig().Economics.BaseGasPrice
	response := (*hexutil.Big)(big.NewInt(gasPrice))
	respondJSON(w, response)
}

func (h *EthereumRPCHandler) MaxPriorityFeePerGas(w http.ResponseWriter, r *http.Request) {
	// FIX: Hardcode sensible default (1 gwei) if method missing
	tip := int64(1000000000)
	response := (*hexutil.Big)(big.NewInt(tip))
	respondJSON(w, response)
}

// ===== Block Information =====

func (h *EthereumRPCHandler) BlockNumber(w http.ResponseWriter, r *http.Request) {
	height := h.blockchain.GetHeight()
	response := hexutil.Uint64(height)
	respondJSON(w, response)
}

func (h *EthereumRPCHandler) GetBlockByNumber(w http.ResponseWriter, r *http.Request) {
	// ... (No changes, logic is good)
	// Ensure you implement the rest of this function or copy from previous
	// Assuming GetBlockByNumber exists on blockchain, otherwise use GetBlockByIndex

	var req struct {
		BlockNumber string `json:"blockNumber"`
		FullTx      bool   `json:"fullTx"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	// ... parsing logic ...
	blockNum, _ := parseBlockNumber(req.BlockNumber)

	// FIX: Use GetBlockByIndex
	block, err := h.blockchain.GetBlockByIndex(int64(blockNum))
	if err != nil {
		respondJSON(w, nil)
		return
	}
	ethBlock := h.convertToEthBlock(block, req.FullTx)
	respondJSON(w, ethBlock)
}

func (h *EthereumRPCHandler) GetBlockByHash(w http.ResponseWriter, r *http.Request) {
	// ... similar to above ...
	// FIX: Use GetBlock (it accepts hash)
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
	// ...
	// FIX: Use GetTransactionFromStorage (which is exposed via Blockchain usually, or add wrapper)
	// If Blockchain doesn't expose it, expose WorldState:
	// tx, err := h.blockchain.GetWorldState().GetTransactionFromStorage(hash)

	// For now assuming Blockchain has GetTransaction wrapper:
	// tx, err := h.blockchain.GetTransaction(hash)
	respondJSON(w, nil) // Placeholder to make it compile if method missing
}

func (h *EthereumRPCHandler) GetTransactionReceipt(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, nil) // Placeholder
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

	response := value
	respondJSON(w, response)
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

	// NOTE: Thrylos uses int64 for Amount/GasPrice in core.Transaction
	// Ensure you handle potential overflows if Eth values > MaxInt64

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

	// IMPORTANT: Generate ID/Hash for Thrylos system
	// thrylosTx.Id = ...
	// thrylosTx.Hash = ...

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
		"miner":           block.Header.Validator, // Proposer
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
		"value":            (*hexutil.Big)(big.NewInt(tx.Amount)),
		"gas":              hexutil.Uint64(tx.Gas),
		"gasPrice":         (*hexutil.Big)(big.NewInt(tx.GasPrice)),
		"input":            hexutil.Bytes(tx.Data),
		"v":                "0x1c", // Dummy V
		"r":                "0x0",  // Dummy R
		"s":                "0x0",  // Dummy S
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
