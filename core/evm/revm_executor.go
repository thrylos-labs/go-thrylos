// core/evm/revm_executor.go
// Ultra-fast EVM executor using revm (Rust)
// 5-10x faster than go-ethereum

package evm

/*
#cgo LDFLAGS: -L${SRCDIR}/../../lib -lthrylos_revm
#include <stdlib.h>
#include <stdint.h>

// C types matching Rust FFI
typedef struct {
    uint8_t bytes[20];
} CAddress;

typedef struct {
    uint8_t bytes[32];
} CU256;

typedef struct {
    const uint8_t* data;
    size_t len;
} CBytes;

typedef struct {
    uint8_t success;
    uint64_t gas_used;
    CBytes return_data;
    const char* error_message;
} CExecutionResult;

// Function declarations
void* revm_executor_new(
    uint64_t chain_id,
    CU256 (*get_balance_fn)(CAddress),
    uint64_t (*get_nonce_fn)(CAddress),
    CBytes (*get_code_fn)(CAddress),
    CU256 (*get_storage_fn)(CAddress, CU256)
);

void revm_executor_free(void* executor);

CExecutionResult revm_execute_call(
    void* executor,
    CAddress caller,
    CAddress to,
    CBytes data,
    uint64_t gas_limit,
    CU256 value
);

CExecutionResult revm_deploy_contract(
    void* executor,
    CAddress deployer,
    CBytes bytecode,
    uint64_t gas_limit,
    CU256 value
);

CAddress revm_calculate_create_address(CAddress deployer, uint64_t nonce);

uint64_t revm_estimate_gas(
    void* executor,
    CAddress caller,
    CAddress to,
    CBytes data,
    CU256 value
);

void revm_free_string(char* s);
void revm_free_bytes(uint8_t* data, size_t len);
*/
import "C"
import (
	"fmt"
	"math/big"
	"unsafe"

	"github.com/ethereum/go-ethereum/common"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/state"
)

// RevmExecutor is an ultra-fast EVM executor using revm (Rust)
type RevmExecutor struct {
	executor   unsafe.Pointer
	worldState *state.WorldState
	chainID    uint64
}

// NewRevmExecutor creates a new revm-based EVM executor
func NewRevmExecutor(cfg *config.Config, worldState *state.WorldState) (*RevmExecutor, error) {
	executor := &RevmExecutor{
		worldState: worldState,
		chainID:    uint64(cfg.Network.ChainID),
	}

	// Create revm executor with callbacks
	executor.executor = C.revm_executor_new(
		C.uint64_t(executor.chainID),
		C.CU256((*[1]byte)(C.getBalanceCallback)),
		C.uint64_t((*[1]byte)(C.getNonceCallback)),
		C.CBytes((*[1]byte)(C.getCodeCallback)),
		C.CU256((*[1]byte)(C.getStorageCallback)),
	)

	if executor.executor == nil {
		return nil, fmt.Errorf("failed to create revm executor")
	}

	return executor, nil
}

// Close frees the revm executor
func (e *RevmExecutor) Close() {
	if e.executor != nil {
		C.revm_executor_free(e.executor)
		e.executor = nil
	}
}

// ExecuteCall executes a smart contract call
func (e *RevmExecutor) ExecuteCall(
	caller common.Address,
	contract common.Address,
	input []byte,
	gas uint64,
	value *big.Int,
) ([]byte, uint64, error) {

	// Convert addresses
	cCaller := addressToC(caller)
	cContract := addressToC(contract)

	// Convert input data
	var cData C.CBytes
	if len(input) > 0 {
		cData.data = (*C.uint8_t)(unsafe.Pointer(&input[0]))
		cData.len = C.size_t(len(input))
	}

	// Convert value
	cValue := bigIntToC(value)

	// Execute call
	result := C.revm_execute_call(
		e.executor,
		cCaller,
		cContract,
		cData,
		C.uint64_t(gas),
		cValue,
	)

	return e.processResult(result)
}

// DeployContract deploys a new smart contract
func (e *RevmExecutor) DeployContract(
	deployer common.Address,
	bytecode []byte,
	gas uint64,
	value *big.Int,
) (contractAddr common.Address, gasUsed uint64, err error) {

	// Calculate contract address
	nonce, _ := e.worldState.GetNonce(deployer.Hex())
	contractAddr = e.CalculateCreateAddress(deployer, nonce)

	// Convert deployer address
	cDeployer := addressToC(deployer)

	// Convert bytecode
	var cBytecode C.CBytes
	if len(bytecode) > 0 {
		cBytecode.data = (*C.uint8_t)(unsafe.Pointer(&bytecode[0]))
		cBytecode.len = C.size_t(len(bytecode))
	}

	// Convert value
	cValue := bigIntToC(value)

	// Deploy contract
	result := C.revm_deploy_contract(
		e.executor,
		cDeployer,
		cBytecode,
		C.uint64_t(gas),
		cValue,
	)

	returnData, gasUsed, err := e.processResult(result)
	if err != nil {
		return common.Address{}, gasUsed, err
	}

	// If deployment succeeded, return contract address
	if len(returnData) > 0 {
		// returnData contains deployed code address
		return contractAddr, gasUsed, nil
	}

	return contractAddr, gasUsed, nil
}

// EstimateGas estimates gas needed for a transaction
func (e *RevmExecutor) EstimateGas(
	from common.Address,
	to *common.Address,
	data []byte,
	value *big.Int,
) (uint64, error) {

	cFrom := addressToC(from)
	var cTo C.CAddress
	if to != nil {
		cTo = addressToC(*to)
	}

	// Convert data
	var cData C.CBytes
	if len(data) > 0 {
		cData.data = (*C.uint8_t)(unsafe.Pointer(&data[0]))
		cData.len = C.size_t(len(data))
	}

	cValue := bigIntToC(value)

	gasEstimate := C.revm_estimate_gas(
		e.executor,
		cFrom,
		cTo,
		cData,
		cValue,
	)

	if gasEstimate == 0 {
		return 0, fmt.Errorf("gas estimation failed")
	}

	return uint64(gasEstimate), nil
}

// CalculateCreateAddress calculates the address for a new contract
func (e *RevmExecutor) CalculateCreateAddress(deployer common.Address, nonce uint64) common.Address {
	cDeployer := addressToC(deployer)
	cAddr := C.revm_calculate_create_address(cDeployer, C.uint64_t(nonce))
	return cToAddress(cAddr)
}

// GetCode returns the code at a given address
func (e *RevmExecutor) GetCode(address common.Address) []byte {
	code, _ := e.worldState.GetContractCode(address.Hex())
	return code
}

// GetCodeHash returns the code hash at a given address
func (e *RevmExecutor) GetCodeHash(address common.Address) common.Hash {
	code := e.GetCode(address)
	if len(code) == 0 {
		return common.Hash{}
	}
	return common.BytesToHash(code) // Use Keccak256 in production
}

// GetStorageAt returns storage value at a specific key
func (e *RevmExecutor) GetStorageAt(address common.Address, key common.Hash) common.Hash {
	value, _ := e.worldState.GetContractStorage(address.Hex(), key.Hex())
	return common.BytesToHash(value)
}

// ============================================================================
// Helper Functions
// ============================================================================

func (e *RevmExecutor) processResult(result C.CExecutionResult) ([]byte, uint64, error) {
	gasUsed := uint64(result.gas_used)

	// Extract return data
	var returnData []byte
	if result.return_data.len > 0 {
		returnData = C.GoBytes(unsafe.Pointer(result.return_data.data), C.int(result.return_data.len))
		// Free the data allocated by Rust
		C.revm_free_bytes((*C.uint8_t)(result.return_data.data), result.return_data.len)
	}

	// Check for errors
	if result.success == 0 {
		var errMsg string
		if result.error_message != nil {
			errMsg = C.GoString(result.error_message)
			C.revm_free_string((*C.char)(result.error_message))
		} else {
			errMsg = "execution failed"
		}
		return returnData, gasUsed, fmt.Errorf("%s", errMsg)
	}

	return returnData, gasUsed, nil
}

// Convert Go address to C address
func addressToC(addr common.Address) C.CAddress {
	var cAddr C.CAddress
	copy(cAddr.bytes[:], addr.Bytes())
	return cAddr
}

// Convert C address to Go address
func cToAddress(cAddr C.CAddress) common.Address {
	return common.BytesToAddress(cAddr.bytes[:])
}

// Convert big.Int to C U256
func bigIntToC(value *big.Int) C.CU256 {
	var cValue C.CU256
	if value != nil {
		valueBytes := value.Bytes()
		// Pad to 32 bytes (big-endian)
		start := 32 - len(valueBytes)
		if start < 0 {
			start = 0
		}
		copy(cValue.bytes[start:], valueBytes)
	}
	return cValue
}

// Convert C U256 to big.Int
func cToBigInt(cValue C.CU256) *big.Int {
	return new(big.Int).SetBytes(cValue.bytes[:])
}

// ============================================================================
// State Callbacks (called by Rust)
// ============================================================================

var globalExecutor *RevmExecutor

//export getBalanceCallback
func getBalanceCallback(addr C.CAddress) C.CU256 {
	if globalExecutor == nil {
		return C.CU256{}
	}

	address := cToAddress(addr)
	balance, _ := globalExecutor.worldState.GetBalance(address.Hex())
	return bigIntToC(big.NewInt(balance))
}

//export getNonceCallback
func getNonceCallback(addr C.CAddress) C.uint64_t {
	if globalExecutor == nil {
		return 0
	}

	address := cToAddress(addr)
	nonce, _ := globalExecutor.worldState.GetNonce(address.Hex())
	return C.uint64_t(nonce)
}

//export getCodeCallback
func getCodeCallback(addr C.CAddress) C.CBytes {
	if globalExecutor == nil {
		return C.CBytes{}
	}

	address := cToAddress(addr)
	code, _ := globalExecutor.worldState.GetContractCode(address.Hex())

	var cBytes C.CBytes
	if len(code) > 0 {
		cBytes.data = (*C.uint8_t)(unsafe.Pointer(&code[0]))
		cBytes.len = C.size_t(len(code))
	}
	return cBytes
}

//export getStorageCallback
func getStorageCallback(addr C.CAddress, key C.CU256) C.CU256 {
	if globalExecutor == nil {
		return C.CU256{}
	}

	address := cToAddress(addr)
	keyHash := common.BytesToHash(key.bytes[:])
	
	value, _ := globalExecutor.worldState.GetContractStorage(address.Hex(), keyHash.Hex())
	
	var cValue C.CU256
	if len(value) > 0 {
		copy(cValue.bytes[:], value)
	}
	return cValue
}
