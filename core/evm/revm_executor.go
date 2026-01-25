package evm //

/*
#cgo LDFLAGS: -L${SRCDIR}/../../lib -lthrylos_revm
#include <stdlib.h>
#include <stdint.h>

// 1. Use #define for constants to ensure CGO exports them reliably
#define FFI_SUCCESS 0
#define FFI_PANIC_CAUGHT 1
#define FFI_INVALID_INPUT 2
#define FFI_EXECUTION_FAILED 3
#define FFI_OUT_OF_GAS 4
#define FFI_REVERT 5
#define FFI_MEMORY_ERROR 6

// Existing types
typedef struct { uint8_t bytes[20]; } CAddress;
typedef struct { uint8_t bytes[32]; } CU256;
typedef struct { const uint8_t* data; size_t len; } CByteSlice;

// 2. Explicitly name the struct tag to avoid anonymous struct issues
typedef struct CExecutionResult {
    uint8_t success;
    uint64_t gas_used;
    CByteSlice return_data;
    const char* error_message;
    int32_t error_code;  // Changed to int32_t to match Rust i32 exactly
} CExecutionResult;

// Callback Typedefs
typedef CU256 (*BalanceCallback)(CAddress);
typedef uint64_t (*NonceCallback)(CAddress);
typedef CByteSlice (*CodeCallback)(CAddress);
typedef CU256 (*StorageCallback)(CAddress, CU256);

// External Exports
extern CU256 getBalanceCallback(CAddress);
extern uint64_t getNonceCallback(CAddress);
extern CByteSlice getCodeCallback(CAddress);
extern CU256 getStorageCallback(CAddress, CU256);

// Static Helpers
static BalanceCallback get_balance_cb() { return &getBalanceCallback; }
static NonceCallback get_nonce_cb() { return &getNonceCallback; }
static CodeCallback get_code_cb() { return &getCodeCallback; }
static StorageCallback get_storage_cb() { return &getStorageCallback; }

// Rust Functions
void* revm_executor_new(uint64_t chain_id, BalanceCallback b, NonceCallback n, CodeCallback c, StorageCallback s);
void revm_executor_free(void* executor);
void revm_free_result(CExecutionResult result);
CExecutionResult revm_execute_call(void* executor, CAddress caller, CAddress to, CByteSlice data, uint64_t gas, CU256 value, uint64_t nonce);
CExecutionResult revm_deploy_contract(void* executor, CAddress deployer, CByteSlice code, uint64_t gas, CU256 value, uint64_t nonce);
CAddress revm_calculate_create_address(CAddress deployer, uint64_t nonce);
uint64_t revm_estimate_gas(void* executor, CAddress caller, CAddress to, CByteSlice data, CU256 value);
void revm_free_string(char* s);
void revm_free_bytes(uint8_t* data, size_t len);
extern void revm_free_error_message(char* ptr);
*/
import "C"
import (
	"fmt"
	"math/big"
	"strconv"
	"unsafe"

	"github.com/ethereum/go-ethereum/common"
	"github.com/thrylos-labs/go-thrylos/config"
)

// StateReader interface prevents import cycles
type StateReader interface {
	GetBalance(address string) (*big.Int, error)
	GetNonce(address string) (uint64, error)
	GetContractCode(address string) ([]byte, error)
	GetContractStorage(address, key string) ([]byte, error)
	AtomicIncrementNonce(address string, expectedNonce uint64) (success bool, currentNonce uint64, err error)
}

type RevmExecutor struct {
	executor   unsafe.Pointer
	worldState StateReader
	chainID    uint64
}

func (e *RevmExecutor) GetStorageAt(address common.Address, key common.Hash) common.Hash {
	val, _ := e.worldState.GetContractStorage(address.Hex(), key.Hex())
	return common.BytesToHash(val)
}

func (e *RevmExecutor) GetCode(address common.Address) []byte {
	code, _ := e.worldState.GetContractCode(address.Hex())
	return code
}

// NewRevmExecutor creates a new revm-based EVM executor
func NewRevmExecutor(cfg *config.Config, worldState StateReader) (*RevmExecutor, error) {
	chainID, _ := strconv.ParseUint(cfg.Network.ChainID, 10, 64)
	if chainID == 0 {
		chainID = 1
	}

	executor := &RevmExecutor{
		worldState: worldState,
		chainID:    chainID,
	}

	globalExecutor = executor

	executor.executor = C.revm_executor_new(
		C.uint64_t(executor.chainID),
		C.get_balance_cb(),
		C.get_nonce_cb(),
		C.get_code_cb(),
		C.get_storage_cb(),
	)

	if executor.executor == nil {
		return nil, fmt.Errorf("failed to create revm executor")
	}

	return executor, nil
}

func (e *RevmExecutor) Close() {
	if e.executor != nil {
		C.revm_executor_free(e.executor)
		e.executor = nil
	}
}

// ============================================================================
// ExecuteCall - UPDATED with atomic nonce validation
// ============================================================================
func (e *RevmExecutor) ExecuteCall(caller, contract common.Address, input []byte, gas uint64, value *big.Int, nonce uint64) ([]byte, uint64, error) {
	// SECURITY: Validate gas parameter
	const maxGasLimit = 30000000
	if gas > maxGasLimit {
		return nil, 0, fmt.Errorf("gas limit %d exceeds maximum %d", gas, maxGasLimit)
	}
	if gas == 0 {
		return nil, 0, fmt.Errorf("gas limit cannot be zero")
	}

	// ✅ CRITICAL: ATOMIC NONCE VALIDATION
	// This prevents race conditions and double-spending at the EVM layer
	success, currentNonce, err := e.worldState.AtomicIncrementNonce(caller.Hex(), nonce)

	if err != nil {
		return nil, 0, fmt.Errorf("nonce validation failed: %w", err)
	}

	if !success {
		return nil, 0, fmt.Errorf("nonce mismatch: expected %d, but account nonce is %d", nonce, currentNonce)
	}

	// ✅ Nonce is now atomically incremented - safe to execute
	// Even if EVM execution fails below, we DO NOT roll back the nonce

	// 1. Convert Go types to C types
	cCaller := addressToC(caller)
	cContract := addressToC(contract)

	var cData C.CByteSlice
	if len(input) > 0 {
		cData.data = (*C.uint8_t)(unsafe.Pointer(&input[0]))
		cData.len = C.size_t(len(input))
	}

	cValue := bigIntToC(value)

	// 2. Call Rust (Now with Nonce - but validation already done above)
	res := C.revm_execute_call(e.executor, cCaller, cContract, cData, C.uint64_t(gas), cValue, C.uint64_t(nonce))

	// 3. 🛡️ SECURITY FIX: Clean up Rust memory immediately after execution
	defer C.revm_free_result(res)

	// 4. Process result
	return e.processResult(res)
}

func (e *RevmExecutor) GetNonce(address common.Address) uint64 {
	n, _ := e.worldState.GetNonce(address.Hex())
	return n
}

// ============================================================================
// DeployContract - UPDATED with atomic nonce validation
// ============================================================================
func (e *RevmExecutor) DeployContract(deployer common.Address, bytecode []byte, gas uint64, value *big.Int) (common.Address, uint64, error) {
	// SECURITY: Validate gas parameter
	const maxGasLimit = 30000000
	if gas > maxGasLimit {
		return common.Address{}, 0, fmt.Errorf("gas limit %d exceeds maximum %d", gas, maxGasLimit)
	}
	if gas == 0 {
		return common.Address{}, 0, fmt.Errorf("gas limit cannot be zero")
	}

	// Get current nonce for address calculation
	nonce, err := e.worldState.GetNonce(deployer.Hex())
	if err != nil {
		return common.Address{}, 0, fmt.Errorf("failed to get nonce: %w", err)
	}

	// ✅ CRITICAL: ATOMIC NONCE VALIDATION for deployment
	success, currentNonce, err := e.worldState.AtomicIncrementNonce(deployer.Hex(), nonce)

	if err != nil {
		return common.Address{}, 0, fmt.Errorf("nonce validation failed: %w", err)
	}

	if !success {
		return common.Address{}, 0, fmt.Errorf("nonce mismatch: expected %d, but account nonce is %d", nonce, currentNonce)
	}

	// ✅ Nonce is now atomically incremented - safe to deploy

	cDeployer := addressToC(deployer)

	// Calculate contract address via Rust helper (using the nonce BEFORE increment)
	cAddr := C.revm_calculate_create_address(cDeployer, C.uint64_t(nonce))
	contractAddr := cToAddress(cAddr)

	var cCode C.CByteSlice
	if len(bytecode) > 0 {
		cCode.data = (*C.uint8_t)(unsafe.Pointer(&bytecode[0]))
		cCode.len = C.size_t(len(bytecode))
	}

	cValue := bigIntToC(value)

	res := C.revm_deploy_contract(e.executor, cDeployer, cCode, C.uint64_t(gas), cValue, C.uint64_t(nonce))

	// 🛡️ SECURITY FIX: Clean up Rust memory after Go copies it
	defer C.revm_free_result(res)

	_, gasUsed, err := e.processResult(res)
	return contractAddr, gasUsed, err
}

// ============================================================================
// EstimateGas - Read-only, does NOT increment nonce
// ============================================================================
func (e *RevmExecutor) EstimateGas(from common.Address, to *common.Address, data []byte, value *big.Int) (uint64, error) {
	const maxGasLimit = 30000000    // 30M - Ethereum block limit
	const maxEstimateGas = 15000000 // 15M - Half of block limit for safety

	cFrom := addressToC(from)
	var cTo C.CAddress
	if to != nil {
		cTo = addressToC(*to)
	}

	var cData C.CByteSlice
	if len(data) > 0 {
		cData.data = (*C.uint8_t)(unsafe.Pointer(&data[0]))
		cData.len = C.size_t(len(data))
	}

	cValue := bigIntToC(value)

	// Call the Rust function
	gas := C.revm_estimate_gas(e.executor, cFrom, cTo, cData, cValue)

	// ✅ SECURITY CHECK 1: Check for Rust panic sentinel
	if uint64(gas) == ^uint64(0) {
		return 0, fmt.Errorf("gas estimation failed: internal error")
	}

	// ✅ SECURITY CHECK 2: Check for zero (execution failed)
	if gas == 0 {
		return 0, fmt.Errorf("gas estimation failed: execution reverted")
	}

	estimatedGas := uint64(gas)

	// ✅ SECURITY CHECK 3: Cap at reasonable maximum (NEW!)
	if estimatedGas > maxEstimateGas {
		return 0, fmt.Errorf("gas estimation %d exceeds maximum safe limit %d", estimatedGas, maxEstimateGas)
	}

	// ✅ SECURITY CHECK 4: Add 10% buffer for safety, but cap at max
	gasWithBuffer := estimatedGas + (estimatedGas / 10)
	if gasWithBuffer > maxEstimateGas {
		gasWithBuffer = maxEstimateGas
	}

	return gasWithBuffer, nil
}

// Helpers
func (e *RevmExecutor) processResult(res C.CExecutionResult) ([]byte, uint64, error) {
	// ✅ FIX: Cast to standard int for comparison
	if int(res.error_code) == int(C.FFI_PANIC_CAUGHT) {
		msg := "Rust panic detected"
		if res.error_message != nil {
			msg = C.GoString(res.error_message)
		}
		return nil, 0, fmt.Errorf("CRITICAL: REVM panic - %s", msg)
	}

	if int(res.error_code) != int(C.FFI_SUCCESS) {
		msg := "Unknown FFI error"
		if res.error_message != nil {
			msg = C.GoString(res.error_message)
		}
		return nil, 0, fmt.Errorf("REVM FFI error (code %d): %s", int(res.error_code), msg)
	}

	gasUsed := uint64(res.gas_used)

	// SECURITY: Validate gas used is reasonable
	const maxReasonableGas = 50000000
	if gasUsed > maxReasonableGas {
		return nil, 0, fmt.Errorf("suspicious gas value: %d exceeds maximum %d", gasUsed, maxReasonableGas)
	}

	var data []byte

	// Copy return data to Go-managed memory
	if res.return_data.len > 0 {
		data = C.GoBytes(unsafe.Pointer(res.return_data.data), C.int(res.return_data.len))
	}

	// Check execution success (EVM-level failure, not FFI failure)
	if res.success == 0 {
		var msg string
		if res.error_message != nil {
			msg = C.GoString(res.error_message)
		} else {
			msg = "execution failed"
		}
		return data, gasUsed, fmt.Errorf("%s", msg)
	}

	return data, gasUsed, nil
}

// === CGO Type Conversion Helpers ===

func addressToC(a common.Address) C.CAddress {
	var c C.CAddress
	bs := a.Bytes()
	for i, b := range bs {
		c.bytes[i] = C.uint8_t(b)
	}
	return c
}

func cToAddress(c C.CAddress) common.Address {
	var bs [20]byte
	for i, b := range c.bytes {
		bs[i] = byte(b)
	}
	return common.BytesToAddress(bs[:])
}

func bigIntToC(v *big.Int) C.CU256 {
	var c C.CU256
	if v != nil {
		b := v.Bytes()
		start := 32 - len(b)
		if start >= 0 {
			for i, byteVal := range b {
				c.bytes[start+i] = C.uint8_t(byteVal)
			}
		}
	}
	return c
}

func cToBigInt(c C.CU256) *big.Int {
	var bs [32]byte
	for i, b := range c.bytes {
		bs[i] = byte(b)
	}
	return new(big.Int).SetBytes(bs[:])
}

// Global instance for C callbacks
var globalExecutor *RevmExecutor

//export getBalanceCallback
func getBalanceCallback(addr C.CAddress) C.CU256 {
	if globalExecutor == nil {
		return C.CU256{}
	}
	val, err := globalExecutor.worldState.GetBalance(cToAddress(addr).Hex())
	if err != nil || val == nil {
		return bigIntToC(big.NewInt(0))
	}
	return bigIntToC(val)
}

//export getNonceCallback
func getNonceCallback(addr C.CAddress) C.uint64_t {
	if globalExecutor == nil {
		return 0
	}
	// ✅ This is read-only - used for gas estimation and queries
	// Does NOT increment nonce
	val, _ := globalExecutor.worldState.GetNonce(cToAddress(addr).Hex())
	return C.uint64_t(val)
}

//export getCodeCallback
func getCodeCallback(addr C.CAddress) C.CByteSlice {
	if globalExecutor == nil {
		return C.CByteSlice{}
	}
	code, _ := globalExecutor.worldState.GetContractCode(cToAddress(addr).Hex())
	var c C.CByteSlice
	if len(code) > 0 {
		c.data = (*C.uint8_t)(unsafe.Pointer(&code[0]))
		c.len = C.size_t(len(code))
	}
	return c
}

//export getStorageCallback
func getStorageCallback(addr C.CAddress, key C.CU256) C.CU256 {
	if globalExecutor == nil {
		return C.CU256{}
	}

	var kBytes [32]byte
	for i, b := range key.bytes {
		kBytes[i] = byte(b)
	}
	kHash := common.BytesToHash(kBytes[:])

	val, _ := globalExecutor.worldState.GetContractStorage(cToAddress(addr).Hex(), kHash.Hex())
	var c C.CU256

	if len(val) > 0 {
		start := 32 - len(val)
		if start >= 0 {
			for i, b := range val {
				c.bytes[start+i] = C.uint8_t(b)
			}
		}
	}
	return c
}
