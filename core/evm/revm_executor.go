//go:build !test
// +build !test

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

// Memory Tracking Functions
void revm_report_memory_stats();
size_t revm_get_leak_count();
size_t revm_get_tracked_error_messages();
size_t revm_get_tracked_return_data();
size_t revm_cleanup_leaked_memory();
*/
import "C"
import (
	"fmt"
	"log"
	"math/big"
	"os"
	"strconv"
	"sync"
	"time"
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

	// Gas tracking
	blockGasUsed  uint64
	blockGasLimit uint64
	gasTrackingMu sync.Mutex
}

func (e *RevmExecutor) GetStorageAt(address common.Address, key common.Hash) common.Hash {
	val, _ := e.worldState.GetContractStorage(address.Hex(), key.Hex())
	return common.BytesToHash(val)
}

func (e *RevmExecutor) GetCode(address common.Address) []byte {
	code, _ := e.worldState.GetContractCode(address.Hex())
	return code
}

func (e *RevmExecutor) ResetBlockGas(blockGasLimit uint64) {
	e.gasTrackingMu.Lock()
	defer e.gasTrackingMu.Unlock()

	e.blockGasUsed = 0
	e.blockGasLimit = blockGasLimit
}

func (e *RevmExecutor) GetBlockGasUsed() uint64 {
	e.gasTrackingMu.Lock()
	defer e.gasTrackingMu.Unlock()

	return e.blockGasUsed
}

func (e *RevmExecutor) CheckAndReserveGas(gasLimit uint64) error {
	e.gasTrackingMu.Lock()
	defer e.gasTrackingMu.Unlock()

	if e.blockGasUsed+gasLimit > e.blockGasLimit {
		return fmt.Errorf("block gas limit exceeded: used %d + requested %d > limit %d",
			e.blockGasUsed, gasLimit, e.blockGasLimit)
	}

	e.blockGasUsed += gasLimit
	return nil
}

func (e *RevmExecutor) RefundGas(gasRefund uint64) {
	e.gasTrackingMu.Lock()
	defer e.gasTrackingMu.Unlock()

	if gasRefund > e.blockGasUsed {
		e.blockGasUsed = 0
	} else {
		e.blockGasUsed -= gasRefund
	}
}

const FFISentinelError = ^uint64(0)

// ============================================================================
// Nonce Management Functions
// ============================================================================

// ReserveNonce reserves a nonce for an upcoming transaction
func (e *RevmExecutor) ReserveNonce(address common.Address) (uint64, error) {
	cAddr := addressToC(address)
	nonce := C.revm_reserve_nonce(e.executor, cAddr)

	// [FIX H-01] Strict validation against sentinel
	if uint64(nonce) == FFISentinelError {
		return 0, fmt.Errorf("CRITICAL: Rust FFI panic during ReserveNonce for %s", address.Hex())
	}

	return uint64(nonce), nil
}

// ReleaseNonce releases a reserved nonce
func (e *RevmExecutor) ReleaseNonce(address common.Address, nonce uint64) {
	cAddr := addressToC(address)
	C.revm_release_nonce(e.executor, cAddr, C.uint64_t(nonce))
}

// GetNextNonce gets the next available nonce (considering reservations)
func (e *RevmExecutor) GetNextNonce(address common.Address) (uint64, error) {
	cAddr := addressToC(address)
	nonce := C.revm_get_next_nonce(e.executor, cAddr)

	// [FIX H-01] Strict validation against sentinel
	if uint64(nonce) == FFISentinelError {
		return 0, fmt.Errorf("CRITICAL: Rust FFI panic during GetNextNonce for %s", address.Hex())
	}

	return uint64(nonce), nil
}

func (e *RevmExecutor) GetNonce(address common.Address) uint64 {
	n, _ := e.worldState.GetNonce(address.Hex())
	return n
}

// ============================================================================
// Transaction Execution
// ============================================================================

// ExecuteCall executes a contract call with atomic nonce validation
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

	// NEW: Check block gas limit
	if err := e.CheckAndReserveGas(gas); err != nil {
		return nil, 0, fmt.Errorf("block gas limit check failed: %w", err)
	}

	// CRITICAL: ATOMIC NONCE VALIDATION
	success, currentNonce, err := e.worldState.AtomicIncrementNonce(caller.Hex(), nonce)
	if err != nil {
		e.RefundGas(gas) // Refund on validation failure
		e.ReleaseNonce(caller, nonce)
		return nil, 0, fmt.Errorf("nonce validation failed: %w", err)
	}

	if !success {
		e.RefundGas(gas)
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

	// Call Rust
	res := C.revm_execute_call(e.executor, cCaller, cContract, cData, C.uint64_t(gas), cValue, C.uint64_t(nonce))

	// ✅ CRITICAL: This defer ensures ALL memory is freed properly
	defer C.revm_free_result(res)

	// Process result (does NOT free memory, that's done by defer above)
	data, gasUsed, err := e.processResult(res)

	// Refund unused gas
	if gasUsed < gas {
		e.RefundGas(gas - gasUsed)
	}

	if err != nil {

		return data, gasUsed, err
	}

	return data, gasUsed, nil
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

	// ✅ FIX: Add block gas limit check
	if err := e.CheckAndReserveGas(gas); err != nil {
		return common.Address{}, 0, fmt.Errorf("block gas limit check failed: %w", err)
	}

	// Get current nonce for address calculation
	nonce, err := e.worldState.GetNonce(deployer.Hex())
	if err != nil {
		e.RefundGas(gas) // ✅ FIX: Refund on early error
		return common.Address{}, 0, fmt.Errorf("failed to get nonce: %w", err)
	}

	// CRITICAL: ATOMIC NONCE VALIDATION (handled by Go side)
	success, currentNonce, err := e.worldState.AtomicIncrementNonce(deployer.Hex(), nonce)

	if err != nil {
		e.RefundGas(gas) // ✅ FIX: Refund on validation failure
		e.ReleaseNonce(deployer, nonce)
		return common.Address{}, 0, fmt.Errorf("nonce validation failed: %w", err)
	}

	if !success {
		e.RefundGas(gas) // ✅ FIX: Refund on nonce mismatch
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

	// ✅ CRITICAL: This defer ensures ALL memory is freed properly
	defer C.revm_free_result(res)

	_, gasUsed, err := e.processResult(res)

	// Refund unused gas
	if gasUsed < gas {
		e.RefundGas(gas - gasUsed)
	}

	if err != nil {
		return common.Address{}, gasUsed, fmt.Errorf("deployment failed: %w", err)
	}

	return contractAddr, gasUsed, nil
}

// EstimateGas estimates gas for a transaction (read-only, does NOT increment nonce)
func (e *RevmExecutor) EstimateGas(from common.Address, to *common.Address, data []byte, value *big.Int) (uint64, error) {
	const maxGasLimit = 30000000
	const maxEstimateGas = 15000000

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

	gas := C.revm_estimate_gas(e.executor, cFrom, cTo, cData, cValue)

	// [FIX H-01] Check for Rust panic sentinel
	if uint64(gas) == FFISentinelError {
		return 0, fmt.Errorf("CRITICAL: Rust FFI panic during EstimateGas")
	}

	if uint64(gas) == 0 {
		return 0, fmt.Errorf("gas estimation failed: execution reverted")
	}

	estimatedGas := uint64(gas)

	if estimatedGas > maxEstimateGas {
		return maxEstimateGas, nil
	}

	// Add buffer safely
	bufferAmount := estimatedGas / 10
	if estimatedGas > maxEstimateGas-bufferAmount {
		return maxEstimateGas, nil
	}

	return estimatedGas + bufferAmount, nil
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
	// IMPORTANT: Do NOT free res.error_message or res.return_data here
	// They will be freed by the deferred revm_free_result call in the caller

	// Check for panic
	if int(res.error_code) == int(C.FFI_PANIC_CAUGHT) {
		msg := "Rust panic detected"
		if res.error_message != nil {
			msg = C.GoString(res.error_message)
		}
		return nil, 0, fmt.Errorf("CRITICAL: REVM panic - %s", msg)
	}

	// Check for other errors
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

	// Copy return data (C.GoBytes makes a copy, original will be freed by defer)
	var data []byte
	if res.return_data.len > 0 {
		data = C.GoBytes(unsafe.Pointer(res.return_data.data), C.int(res.return_data.len))
	}

	// Check execution success
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
		// Report memory stats before closing (in debug mode)
		if os.Getenv("REVM_DEBUG") != "" {
			log.Println("📊 Memory stats before executor close:")
			e.ReportMemoryStats()
		}

		// Check for leaks and attempt cleanup
		if leaks := e.GetLeakCount(); leaks > 0 {
			log.Printf("⚠️ WARNING: %d potential memory leaks detected before closing executor", leaks)

			// ✅ C-02 FIX: Attempt to clean up leaked memory
			cleaned := e.CleanupLeakedMemory()
			if cleaned > 0 {
				log.Printf("🧹 Attempted cleanup of %d leaked allocations", cleaned)
			}
		}

		C.revm_executor_free(e.executor)
		e.executor = nil
	}

	executorMutex.Lock()
	if globalExecutor == e {
		globalExecutor = nil
	}
	executorMutex.Unlock()
}

// StartMemoryHealthCheck starts a goroutine that periodically checks memory health
// Returns a channel that can be used to stop the health check
func (e *RevmExecutor) StartMemoryHealthCheck(interval time.Duration) chan struct{} {
	done := make(chan struct{})

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := e.CheckMemoryHealth(); err != nil {
					log.Printf("⚠️ Memory health check failed: %v", err)
					e.ReportMemoryStats()
				}
			case <-done:
				return
			}
		}
	}()

	return done
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

// ============================================================================
// NEW: Memory tracking functions for Go
// ============================================================================

// ReportMemoryStats prints a detailed memory report from the Rust side
func (e *RevmExecutor) ReportMemoryStats() {
	C.revm_report_memory_stats()
}

// GetLeakCount returns the number of potential memory leaks detected
func (e *RevmExecutor) GetLeakCount() int {
	count := C.revm_get_leak_count()
	return int(count)
}

// GetTrackedErrorMessages returns the number of currently tracked error messages
func (e *RevmExecutor) GetTrackedErrorMessages() int {
	count := C.revm_get_tracked_error_messages()
	return int(count)
}

// GetTrackedReturnData returns the number of currently tracked return data allocations
func (e *RevmExecutor) GetTrackedReturnData() int {
	count := C.revm_get_tracked_return_data()
	return int(count)
}

// CheckMemoryHealth performs a health check on memory management
// Returns an error if potential leaks are detected
func (e *RevmExecutor) CheckMemoryHealth() error {
	leaks := e.GetLeakCount()
	if leaks > 0 {
		return fmt.Errorf("memory leak detected: %d potential leaks", leaks)
	}
	return nil
}

// CleanupLeakedMemory attempts to clean up any leaked memory
// Returns the number of potential leaks that were detected
// ✅ C-02 FIX: Emergency cleanup function
func (e *RevmExecutor) CleanupLeakedMemory() int {
	cleaned := C.revm_cleanup_leaked_memory()
	if cleaned > 0 {
		log.Printf("🧹 Cleaned up %d potential memory leaks", cleaned)
	}
	return int(cleaned)
}
