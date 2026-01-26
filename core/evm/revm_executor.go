package evm

/*
#cgo LDFLAGS: -L${SRCDIR}/../../lib -lthrylos_revm
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>

// FFI Error Codes
#define FFI_SUCCESS 0
#define FFI_PANIC_CAUGHT 1
#define FFI_INVALID_INPUT 2
#define FFI_EXECUTION_FAILED 3
#define FFI_OUT_OF_GAS 4
#define FFI_REVERT 5
#define FFI_MEMORY_ERROR 6

// C Types
typedef struct { uint8_t bytes[20]; } CAddress;
typedef struct { uint8_t bytes[32]; } CU256;
typedef struct { const uint8_t* data; size_t len; } CByteSlice;

typedef struct CExecutionResult {
    uint8_t success;
    uint64_t gas_used;
    CByteSlice return_data;
    const char* error_message;
    int32_t error_code;
} CExecutionResult;

// Callback Typedefs
typedef CU256 (*BalanceCallback)(CAddress);
typedef uint64_t (*NonceCallback)(CAddress);
typedef CByteSlice (*CodeCallback)(CAddress);
typedef CU256 (*StorageCallback)(CAddress, CU256);

// External Exports for Callbacks
extern CU256 getBalanceCallback(CAddress);
extern uint64_t getNonceCallback(CAddress);
extern CByteSlice getCodeCallback(CAddress);
extern CU256 getStorageCallback(CAddress, CU256);

// Static Helpers
static BalanceCallback get_balance_cb() { return &getBalanceCallback; }
static NonceCallback get_nonce_cb() { return &getNonceCallback; }
static CodeCallback get_code_cb() { return &getCodeCallback; }
static StorageCallback get_storage_cb() { return &getStorageCallback; }

// Rust FFI Functions
void* revm_executor_new(uint64_t chain_id, BalanceCallback b, NonceCallback n, CodeCallback c, StorageCallback s);
void revm_executor_free(void* executor);
CExecutionResult revm_execute_call(void* executor, CAddress caller, CAddress to, CByteSlice data, uint64_t gas, CU256 value, uint64_t nonce);
CExecutionResult revm_deploy_contract(void* executor, CAddress deployer, CByteSlice code, uint64_t gas, CU256 value, uint64_t nonce);
uint64_t revm_estimate_gas(void* executor, CAddress caller, CAddress to, CByteSlice data, CU256 value);
CAddress revm_calculate_create_address(CAddress deployer, uint64_t nonce);
void revm_free_result(CExecutionResult result);
void revm_free_string(char* s);
void revm_free_bytes(uint8_t* data, size_t len);
void revm_free_error_message(char* ptr);

// Nonce Reservation Functions
uint64_t revm_reserve_nonce(void* executor, CAddress address);
void revm_release_nonce(void* executor, CAddress address, uint64_t nonce);
uint64_t revm_get_next_nonce(void* executor, CAddress address);

// Monitoring Functions
size_t revm_get_active_locks(void* executor);
bool revm_is_account_locked(void* executor, CAddress address);
size_t revm_get_reserved_nonces_count(void* executor);
*/
import "C"
import (
	"fmt"
	"log"
	"math/big"
	"strconv"
	"sync"
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

// ============================================================================
// Nonce Management Functions
// ============================================================================

// ReserveNonce reserves a nonce for an upcoming transaction
func (e *RevmExecutor) ReserveNonce(address common.Address) (uint64, error) {
	cAddr := addressToC(address)
	nonce := C.revm_reserve_nonce(e.executor, cAddr)

	goNonce := uint64(nonce)
	if goNonce == ^uint64(0) {
		return 0, fmt.Errorf("failed to reserve nonce for %s", address.Hex())
	}

	return goNonce, nil
}

// ReleaseNonce releases a reserved nonce (if transaction fails before execution)
func (e *RevmExecutor) ReleaseNonce(address common.Address, nonce uint64) {
	cAddr := addressToC(address)
	C.revm_release_nonce(e.executor, cAddr, C.uint64_t(nonce))
}

// GetNextNonce gets the next available nonce (considering reservations)
func (e *RevmExecutor) GetNextNonce(address common.Address) (uint64, error) {
	cAddr := addressToC(address)
	nonce := C.revm_get_next_nonce(e.executor, cAddr)

	goNonce := uint64(nonce)
	if goNonce == ^uint64(0) {
		return 0, fmt.Errorf("failed to get next nonce for %s", address.Hex())
	}

	return goNonce, nil
}

func (e *RevmExecutor) GetNonce(address common.Address) uint64 {
	n, _ := e.worldState.GetNonce(address.Hex())
	return n
}

// ============================================================================
// Transaction Execution
// ============================================================================

// ExecuteCall executes a contract call with atomic nonce validation
func (e *RevmExecutor) ExecuteCall(caller, contract common.Address, input []byte, gas uint64, value *big.Int, nonce uint64) ([]byte, uint64, error) {
	// SECURITY: Validate gas parameter
	const maxGasLimit = 30000000
	if gas > maxGasLimit {
		return nil, 0, fmt.Errorf("gas limit %d exceeds maximum %d", gas, maxGasLimit)
	}
	if gas == 0 {
		return nil, 0, fmt.Errorf("gas limit cannot be zero")
	}

	// CRITICAL: ATOMIC NONCE VALIDATION (handled by Go side)
	success, currentNonce, err := e.worldState.AtomicIncrementNonce(caller.Hex(), nonce)

	if err != nil {
		e.ReleaseNonce(caller, nonce)
		return nil, 0, fmt.Errorf("nonce validation failed: %w", err)
	}

	if !success {
		e.ReleaseNonce(caller, nonce)
		return nil, 0, fmt.Errorf("nonce mismatch: expected %d, but account nonce is %d", nonce, currentNonce)
	}

	// Convert Go types to C types
	cCaller := addressToC(caller)
	cContract := addressToC(contract)

	var cData C.CByteSlice
	if len(input) > 0 {
		cData.data = (*C.uint8_t)(unsafe.Pointer(&input[0]))
		cData.len = C.size_t(len(input))
	}

	cValue := bigIntToC(value)

	// Call Rust (nonce already incremented by Go)
	res := C.revm_execute_call(e.executor, cCaller, cContract, cData, C.uint64_t(gas), cValue, C.uint64_t(nonce))
	defer C.revm_free_result(res)

	// Process result
	return e.processResult(res)
}

// DeployContract deploys a new contract with atomic nonce validation
func (e *RevmExecutor) DeployContract(deployer common.Address, bytecode []byte, gas uint64, value *big.Int) (common.Address, uint64, error) {
	// SECURITY: Validate gas parameter
	const maxGasLimit = 30000000
	const minDeploymentGas = 100000

	if gas < minDeploymentGas {
		return common.Address{}, 0, fmt.Errorf("deployment requires minimum %d gas, got %d", minDeploymentGas, gas)
	}

	if gas > maxGasLimit {
		return common.Address{}, 0, fmt.Errorf("gas limit %d exceeds maximum %d", gas, maxGasLimit)
	}

	if len(bytecode) == 0 {
		return common.Address{}, 0, fmt.Errorf("cannot deploy empty bytecode")
	}

	// Get current nonce for address calculation
	nonce, err := e.worldState.GetNonce(deployer.Hex())
	if err != nil {
		return common.Address{}, 0, fmt.Errorf("failed to get nonce: %w", err)
	}

	// CRITICAL: ATOMIC NONCE VALIDATION (handled by Go side)
	success, currentNonce, err := e.worldState.AtomicIncrementNonce(deployer.Hex(), nonce)

	if err != nil {
		e.ReleaseNonce(deployer, nonce)
		return common.Address{}, 0, fmt.Errorf("nonce validation failed: %w", err)
	}

	if !success {
		e.ReleaseNonce(deployer, nonce)
		return common.Address{}, 0, fmt.Errorf("nonce mismatch: expected %d, but account nonce is %d", nonce, currentNonce)
	}

	// Calculate contract address
	cDeployer := addressToC(deployer)
	cAddr := C.revm_calculate_create_address(cDeployer, C.uint64_t(nonce))
	contractAddr := cToAddress(cAddr)

	var cCode C.CByteSlice
	cCode.data = (*C.uint8_t)(unsafe.Pointer(&bytecode[0]))
	cCode.len = C.size_t(len(bytecode))

	cValue := bigIntToC(value)

	res := C.revm_deploy_contract(e.executor, cDeployer, cCode, C.uint64_t(gas), cValue, C.uint64_t(nonce))
	defer C.revm_free_result(res)

	_, gasUsed, err := e.processResult(res)
	if err != nil {
		return common.Address{}, gasUsed, fmt.Errorf("deployment failed: %w", err)
	}

	return contractAddr, gasUsed, nil
}

// EstimateGas estimates gas for a transaction (read-only, does NOT increment nonce)
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

	// SECURITY CHECK 1: Check for Rust panic sentinel BEFORE conversion
	if gas == C.uint64_t(^uint64(0)) || gas == 0 {
		return 0, fmt.Errorf("gas estimation failed: execution error or revert")
	}

	estimatedGas := uint64(gas)

	// SECURITY CHECK 2: Validate before any arithmetic
	if estimatedGas > maxEstimateGas {
		return maxEstimateGas, nil
	}

	// SECURITY CHECK 3: Overflow-safe buffer calculation
	bufferAmount := estimatedGas / 10

	if estimatedGas > maxEstimateGas-bufferAmount {
		return maxEstimateGas, nil
	}

	gasWithBuffer := estimatedGas + bufferAmount
	return gasWithBuffer, nil
}

// ============================================================================
// Monitoring Functions
// ============================================================================

// GetActiveLocks returns the number of active account locks
func (e *RevmExecutor) GetActiveLocks() int {
	count := C.revm_get_active_locks(e.executor)
	return int(count)
}

// IsAccountLocked checks if an account is currently locked
func (e *RevmExecutor) IsAccountLocked(address common.Address) bool {
	cAddr := addressToC(address)
	locked := C.revm_is_account_locked(e.executor, cAddr)
	return bool(locked)
}

// GetReservedNoncesCount returns the total number of reserved nonces
func (e *RevmExecutor) GetReservedNoncesCount() int {
	count := C.revm_get_reserved_nonces_count(e.executor)
	return int(count)
}

// ============================================================================
// Helper Functions
// ============================================================================

func (e *RevmExecutor) processResult(res C.CExecutionResult) ([]byte, uint64, error) {
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
	if res.return_data.len > 0 {
		data = C.GoBytes(unsafe.Pointer(res.return_data.data), C.int(res.return_data.len))
	}

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

// Type Conversion Helpers

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
	if v == nil {
		return c
	}

	b := v.Bytes()

	if len(b) > 32 {
		log.Printf("⚠️ bigIntToC: value exceeds U256 maximum (%d bytes), truncating to rightmost 32 bytes", len(b))
		b = b[len(b)-32:]
	}

	start := 32 - len(b)
	for i, byteVal := range b {
		c.bytes[start+i] = C.uint8_t(byteVal)
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

// ============================================================================
// Global Executor Management (Thread-Safe)
// ============================================================================

var (
	globalExecutor *RevmExecutor
	executorMutex  sync.RWMutex
)

func setGlobalExecutor(e *RevmExecutor) {
	executorMutex.Lock()
	defer executorMutex.Unlock()
	globalExecutor = e
}

func getGlobalExecutor() *RevmExecutor {
	executorMutex.RLock()
	defer executorMutex.RUnlock()
	return globalExecutor
}

// ============================================================================
// Executor Lifecycle
// ============================================================================

// NewRevmExecutor creates a new REVM executor instance
func NewRevmExecutor(cfg *config.Config, worldState StateReader) (*RevmExecutor, error) {
	chainID, _ := strconv.ParseUint(cfg.Network.ChainID, 10, 64)
	if chainID == 0 {
		chainID = 1
	}

	executor := &RevmExecutor{
		worldState: worldState,
		chainID:    chainID,
	}

	setGlobalExecutor(executor)

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

// Close frees the executor resources
func (e *RevmExecutor) Close() {
	if e.executor != nil {
		C.revm_executor_free(e.executor)
		e.executor = nil
	}

	executorMutex.Lock()
	if globalExecutor == e {
		globalExecutor = nil
	}
	executorMutex.Unlock()
}

// ============================================================================
// Callbacks (C → Go)
// ============================================================================

//export getBalanceCallback
func getBalanceCallback(addr C.CAddress) C.CU256 {
	executor := getGlobalExecutor()
	if executor == nil {
		log.Printf("⚠️ getBalanceCallback: globalExecutor is nil for address %s", cToAddress(addr).Hex())
		return C.CU256{}
	}

	val, err := executor.worldState.GetBalance(cToAddress(addr).Hex())
	if err != nil {
		log.Printf("⚠️ getBalanceCallback: GetBalance failed for %s: %v", cToAddress(addr).Hex(), err)
		return bigIntToC(big.NewInt(0))
	}
	if val == nil {
		return bigIntToC(big.NewInt(0))
	}
	return bigIntToC(val)
}

//export getNonceCallback
func getNonceCallback(addr C.CAddress) C.uint64_t {
	executor := getGlobalExecutor()
	if executor == nil {
		log.Printf("⚠️ getNonceCallback: globalExecutor is nil for address %s", cToAddress(addr).Hex())
		return 0
	}

	val, err := executor.worldState.GetNonce(cToAddress(addr).Hex())
	if err != nil {
		log.Printf("⚠️ getNonceCallback: GetNonce failed for %s: %v", cToAddress(addr).Hex(), err)
		return 0
	}
	return C.uint64_t(val)
}

//export getCodeCallback
func getCodeCallback(addr C.CAddress) C.CByteSlice {
	executor := getGlobalExecutor()
	if executor == nil {
		log.Printf("⚠️ getCodeCallback: globalExecutor is nil for address %s", cToAddress(addr).Hex())
		return C.CByteSlice{}
	}

	code, err := executor.worldState.GetContractCode(cToAddress(addr).Hex())
	if err != nil {
		log.Printf("⚠️ getCodeCallback: GetContractCode failed for %s: %v", cToAddress(addr).Hex(), err)
		return C.CByteSlice{}
	}

	var c C.CByteSlice
	if len(code) > 0 {
		c.data = (*C.uint8_t)(unsafe.Pointer(&code[0]))
		c.len = C.size_t(len(code))
	}
	return c
}

//export getStorageCallback
func getStorageCallback(addr C.CAddress, key C.CU256) C.CU256 {
	executor := getGlobalExecutor()
	if executor == nil {
		log.Printf("⚠️ getStorageCallback: globalExecutor is nil for address %s", cToAddress(addr).Hex())
		return C.CU256{}
	}

	var kBytes [32]byte
	for i, b := range key.bytes {
		kBytes[i] = byte(b)
	}
	kHash := common.BytesToHash(kBytes[:])

	val, err := executor.worldState.GetContractStorage(cToAddress(addr).Hex(), kHash.Hex())
	if err != nil {
		log.Printf("⚠️ getStorageCallback: GetContractStorage failed for %s: %v", cToAddress(addr).Hex(), err)
		return C.CU256{}
	}

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
