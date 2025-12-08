// core/evm/revm_executor.go
package evm

/*
#cgo LDFLAGS: -L${SRCDIR}/../../lib -lthrylos_revm
#include <stdlib.h>
#include <stdint.h>

// 1. Types
typedef struct { uint8_t bytes[20]; } CAddress;
typedef struct { uint8_t bytes[32]; } CU256;
typedef struct { const uint8_t* data; size_t len; } CByteSlice;
typedef struct { uint8_t success; uint64_t gas_used; CByteSlice return_data; const char* error_message; } CExecutionResult;

// 2. Callback Typedefs
typedef CU256 (*BalanceCallback)(CAddress);
typedef uint64_t (*NonceCallback)(CAddress);
typedef CByteSlice (*CodeCallback)(CAddress);
typedef CU256 (*StorageCallback)(CAddress, CU256);

// 3. Go Exports
extern CU256 getBalanceCallback(CAddress);
extern uint64_t getNonceCallback(CAddress);
extern CByteSlice getCodeCallback(CAddress);
extern CU256 getStorageCallback(CAddress, CU256);

// 4. Static Helpers
static BalanceCallback get_balance_cb() { return &getBalanceCallback; }
static NonceCallback get_nonce_cb() { return &getNonceCallback; }
static CodeCallback get_code_cb() { return &getCodeCallback; }
static StorageCallback get_storage_cb() { return &getStorageCallback; }

// 5. Rust Functions
void* revm_executor_new(uint64_t chain_id, BalanceCallback b, NonceCallback n, CodeCallback c, StorageCallback s);
void revm_executor_free(void* executor);
CExecutionResult revm_execute_call(void* executor, CAddress caller, CAddress to, CByteSlice data, uint64_t gas, CU256 value);
CExecutionResult revm_deploy_contract(void* executor, CAddress deployer, CByteSlice code, uint64_t gas, CU256 value);
CAddress revm_calculate_create_address(CAddress deployer, uint64_t nonce);
uint64_t revm_estimate_gas(void* executor, CAddress caller, CAddress to, CByteSlice data, CU256 value);
void revm_free_string(char* s);
void revm_free_bytes(uint8_t* data, size_t len);
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
	GetBalance(address string) (int64, error)
	GetNonce(address string) (uint64, error)
	GetContractCode(address string) ([]byte, error)
	GetContractStorage(address, key string) ([]byte, error)
}

type RevmExecutor struct {
	executor   unsafe.Pointer
	worldState StateReader
	chainID    uint64
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

func (e *RevmExecutor) ExecuteCall(caller, contract common.Address, input []byte, gas uint64, value *big.Int) ([]byte, uint64, error) {
	cCaller := addressToC(caller)
	cContract := addressToC(contract)

	var cData C.CByteSlice
	if len(input) > 0 {
		cData.data = (*C.uint8_t)(unsafe.Pointer(&input[0]))
		cData.len = C.size_t(len(input))
	}

	cValue := bigIntToC(value)

	res := C.revm_execute_call(e.executor, cCaller, cContract, cData, C.uint64_t(gas), cValue)
	return e.processResult(res)
}

func (e *RevmExecutor) DeployContract(deployer common.Address, bytecode []byte, gas uint64, value *big.Int) (common.Address, uint64, error) {
	nonce, _ := e.worldState.GetNonce(deployer.Hex())
	cDeployer := addressToC(deployer)

	// Calculate contract address via Rust helper
	cAddr := C.revm_calculate_create_address(cDeployer, C.uint64_t(nonce))
	contractAddr := cToAddress(cAddr)

	var cCode C.CByteSlice
	if len(bytecode) > 0 {
		cCode.data = (*C.uint8_t)(unsafe.Pointer(&bytecode[0]))
		cCode.len = C.size_t(len(bytecode))
	}

	cValue := bigIntToC(value)

	res := C.revm_deploy_contract(e.executor, cDeployer, cCode, C.uint64_t(gas), cValue)

	_, gasUsed, err := e.processResult(res)
	return contractAddr, gasUsed, err
}

func (e *RevmExecutor) EstimateGas(from common.Address, to *common.Address, data []byte, value *big.Int) (uint64, error) {
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
	if gas == 0 {
		return 0, fmt.Errorf("gas estimation failed")
	}
	return uint64(gas), nil
}

// Helpers
func (e *RevmExecutor) processResult(res C.CExecutionResult) ([]byte, uint64, error) {
	gasUsed := uint64(res.gas_used)
	var data []byte
	if res.return_data.len > 0 {
		data = C.GoBytes(unsafe.Pointer(res.return_data.data), C.int(res.return_data.len))
		C.revm_free_bytes((*C.uint8_t)(res.return_data.data), res.return_data.len)
	}

	if res.success == 0 {
		var msg string
		if res.error_message != nil {
			msg = C.GoString(res.error_message)
			C.revm_free_string((*C.char)(res.error_message))
		} else {
			msg = "execution failed"
		}
		return data, gasUsed, fmt.Errorf("%s", msg)
	}
	return data, gasUsed, nil
}

// === FIX: Manual casting loops to satisfy CGO types ===

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

// Callbacks exported to C
//
//export getBalanceCallback
func getBalanceCallback(addr C.CAddress) C.CU256 {
	if globalExecutor == nil {
		return C.CU256{}
	}
	val, _ := globalExecutor.worldState.GetBalance(cToAddress(addr).Hex())
	return bigIntToC(big.NewInt(val))
}

//export getNonceCallback
func getNonceCallback(addr C.CAddress) C.uint64_t {
	if globalExecutor == nil {
		return 0
	}
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

	// Manual conversion for key hash
	var kBytes [32]byte
	for i, b := range key.bytes {
		kBytes[i] = byte(b)
	}
	kHash := common.BytesToHash(kBytes[:])

	val, _ := globalExecutor.worldState.GetContractStorage(cToAddress(addr).Hex(), kHash.Hex())
	var c C.CU256

	// Manual conversion for value bytes
	if len(val) > 0 {
		// Pad to 32 bytes if necessary
		start := 32 - len(val)
		if start >= 0 {
			for i, b := range val {
				c.bytes[start+i] = C.uint8_t(b)
			}
		}
	}
	return c
}
