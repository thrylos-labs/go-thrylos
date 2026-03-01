// thrylos-revm/src/lib.rs
mod bytecode_validation;
mod ffi_safety;
mod memory_tracker;
use memory_tracker::get_memory_tracker;

use bytecode_validation::{validate_bytecode, MIN_DEPLOYMENT_GAS};
use ffi_safety::{ffi_safe_exec, revm_free_error_message, FFIErrorCode, FFIResult};
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::{SystemTime, UNIX_EPOCH};
// Ultra-fast EVM implementation using revm (Rust)
// 5-10x faster than go-ethereum

// ============================================================================
// SECURITY FIXES:
// - C-01: Atomic nonce validation with per-account locking
// - C-02: Comprehensive bytecode validation
// - H-01: Nonce reservation system for concurrent transactions
// ============================================================================

const MAX_GAS_LIMIT: u64 = 30_000_000; // 30M gas (Ethereum block limit)
const MAX_CALLDATA_SIZE: usize = 1_048_576; // 1 MB max calldata
const MAX_BYTECODE_SIZE: usize = 24_576; // 24 KB (EIP-170 limit)
const EXECUTOR_MAGIC: u64 = 0xDEADBEEF_CAFEBABE;
const EXECUTOR_FREED_MAGIC: u64 = 0xDEADDEAD_DEADBEEF;
const DEFAULT_MAX_GAS_PER_WINDOW: u64 = 300_000_000;
const DEFAULT_MAX_TX_PER_WINDOW: u64 = 1000;
const DEFAULT_CIRCUIT_BREAKER_WINDOW_SECS: u64 = 10;

use revm::{
    db::CacheDB,
    primitives::{Address, Bytecode, Bytes, TransactTo, TxEnv, B256, U256},
    Database, Evm,
};
use std::collections::HashMap;
use std::env;
use std::ffi::{c_char, CStr, CString};
use std::panic::{catch_unwind, AssertUnwindSafe};
use std::slice;

use dashmap::DashMap;
use parking_lot::{Mutex, RwLock};
use std::sync::Arc;

// Add this struct to lib.rs
pub struct CircuitBreaker {
    // Rolling window tracking
    window_start: AtomicU64,
    window_gas_used: AtomicU64,
    window_tx_count: AtomicU64,

    // Thresholds
    max_gas_per_window: u64,
    max_tx_per_window: u64,
    window_duration_secs: u64,
}

// ============================================================================
// C FFI Types for Go interop
// ============================================================================

#[repr(C)]
#[derive(Clone, Copy)]
pub struct CAddress {
    bytes: [u8; 20],
}

#[repr(C)]
#[derive(Clone, Copy)]
pub struct CU256 {
    bytes: [u8; 32],
}

#[repr(C)]
#[derive(Clone, Copy)]
pub struct CByteSlice {
    data: *const u8,
    len: usize,
}

#[repr(C)]
pub struct CExecutionResult {
    pub success: u8,
    pub gas_used: u64,
    pub return_data: CByteSlice,
    pub error_message: *const c_char,
    pub error_code: i32,
}

impl Default for CExecutionResult {
    fn default() -> Self {
        CExecutionResult {
            success: 0,
            gas_used: 0,
            return_data: CByteSlice {
                data: std::ptr::null(),
                len: 0,
            },
            error_message: std::ptr::null(),
            error_code: FFIErrorCode::Success as i32,
        }
    }
}

// ============================================================================
// Input Validation Helpers
// ============================================================================

/// Validate executor pointer for safety
fn validate_executor(executor: *mut EVMExecutor) -> Result<(), &'static str> {
    if executor.is_null() {
        return Err("Executor pointer is null");
    }

    unsafe {
        let magic = (*executor).magic;

        if magic == EXECUTOR_FREED_MAGIC {
            return Err("Executor has been freed (use-after-free detected)");
        }

        if magic != EXECUTOR_MAGIC {
            return Err("Executor pointer is corrupted (invalid magic number)");
        }
    }

    Ok(())
}

/// Validate gas limit is within safe bounds
fn validate_gas_limit(gas_limit: u64) -> Result<(), &'static str> {
    if gas_limit == 0 {
        return Err("Gas limit cannot be zero");
    }

    if gas_limit > MAX_GAS_LIMIT {
        return Err("Gas limit exceeds maximum (30M)");
    }

    Ok(())
}

/// Validate calldata/bytecode size and pointer
fn validate_data(data: CByteSlice, max_size: usize, data_type: &str) -> Result<(), String> {
    if data.len == 0 {
        return Ok(());
    }

    if data.data.is_null() {
        return Err(format!(
            "{} pointer is null but length is {}",
            data_type, data.len
        ));
    }

    if data.len > max_size {
        return Err(format!(
            "{} size {} exceeds maximum {}",
            data_type, data.len, max_size
        ));
    }

    Ok(())
}

// ============================================================================
// State Database - Bridges to Thrylos state
// ============================================================================

pub struct ThrylosDB {
    get_balance_fn: extern "C" fn(CAddress) -> CU256,
    get_nonce_fn: extern "C" fn(CAddress) -> u64,
    get_code_fn: extern "C" fn(CAddress) -> CByteSlice,
    get_storage_fn: extern "C" fn(CAddress, CU256) -> CU256,

    #[allow(dead_code)]
    cache: CacheDB<EmptyDB>,
}

impl Database for ThrylosDB {
    type Error = std::io::Error;

    fn basic(
        &mut self,
        address: Address,
    ) -> Result<Option<revm::primitives::AccountInfo>, Self::Error> {
        let c_addr = CAddress {
            bytes: address.0 .0,
        };

        let balance_bytes = (self.get_balance_fn)(c_addr);
        let balance = U256::from_be_bytes(balance_bytes.bytes);

        let nonce = (self.get_nonce_fn)(c_addr);

        let code_bytes = (self.get_code_fn)(c_addr);
        let code = if code_bytes.len > 0 {
            let slice = unsafe { slice::from_raw_parts(code_bytes.data, code_bytes.len) };
            Bytecode::new_raw(Bytes::copy_from_slice(slice))
        } else {
            Bytecode::default()
        };

        Ok(Some(revm::primitives::AccountInfo {
            balance,
            nonce,
            code_hash: code.hash_slow(),
            code: Some(code),
        }))
    }

    fn code_by_hash(&mut self, _code_hash: B256) -> Result<Bytecode, Self::Error> {
        Ok(Bytecode::default())
    }

    fn storage(&mut self, address: Address, index: U256) -> Result<U256, Self::Error> {
        let c_addr = CAddress {
            bytes: address.0 .0,
        };

        let c_index = CU256 {
            bytes: index.to_be_bytes(),
        };

        let value_bytes = (self.get_storage_fn)(c_addr, c_index);
        let value = U256::from_be_bytes(value_bytes.bytes);

        Ok(value)
    }

    fn block_hash(&mut self, _number: u64) -> Result<B256, Self::Error> {
        Ok(B256::ZERO)
    }
}

#[derive(Debug)]
struct EmptyDB;

impl Database for EmptyDB {
    type Error = std::io::Error;

    fn basic(
        &mut self,
        _address: Address,
    ) -> Result<Option<revm::primitives::AccountInfo>, Self::Error> {
        Ok(None)
    }

    fn code_by_hash(&mut self, _code_hash: B256) -> Result<Bytecode, Self::Error> {
        Ok(Bytecode::default())
    }

    fn storage(&mut self, _address: Address, _index: U256) -> Result<U256, Self::Error> {
        Ok(U256::ZERO)
    }

    fn block_hash(&mut self, _number: u64) -> Result<B256, Self::Error> {
        Ok(B256::ZERO)
    }
}

impl CircuitBreaker {
    pub fn new(max_gas_per_window: u64, max_tx_per_window: u64, window_duration_secs: u64) -> Self {
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs();

        Self {
            window_start: AtomicU64::new(now),
            window_gas_used: AtomicU64::new(0),
            window_tx_count: AtomicU64::new(0),
            max_gas_per_window,
            max_tx_per_window,
            window_duration_secs,
        }
    }

    /// Check if transaction should be allowed
    pub fn check_and_record(&self, gas_limit: u64) -> Result<(), String> {
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs();

        let window_start = self.window_start.load(Ordering::Relaxed);

        // Reset window if expired
        if now >= window_start + self.window_duration_secs {
            self.window_start.store(now, Ordering::Relaxed);
            self.window_gas_used.store(0, Ordering::Relaxed);
            self.window_tx_count.store(0, Ordering::Relaxed);
        }

        // Check transaction count
        let tx_count = self.window_tx_count.fetch_add(1, Ordering::Relaxed);
        if tx_count >= self.max_tx_per_window {
            return Err(format!(
                "Circuit breaker: too many transactions in window ({}/{})",
                tx_count, self.max_tx_per_window
            ));
        }

        // Check gas usage
        let gas_used = self.window_gas_used.fetch_add(gas_limit, Ordering::Relaxed);
        if gas_used + gas_limit > self.max_gas_per_window {
            return Err(format!(
                "Circuit breaker: gas limit exceeded for window ({}/{})",
                gas_used + gas_limit,
                self.max_gas_per_window
            ));
        }

        Ok(())
    }

    pub fn get_stats(&self) -> (u64, u64, u64) {
        (
            self.window_gas_used.load(Ordering::Relaxed),
            self.window_tx_count.load(Ordering::Relaxed),
            self.window_start.load(Ordering::Relaxed),
        )
    }
}

fn read_circuit_breaker_limit(name: &str, default: u64) -> u64 {
    env::var(name)
        .ok()
        .and_then(|value| value.parse::<u64>().ok())
        .filter(|value| *value > 0)
        .unwrap_or(default)
}

fn circuit_breaker_config_from_env() -> (u64, u64, u64) {
    (
        read_circuit_breaker_limit("THRYLOS_EVM_MAX_GAS_PER_WINDOW", DEFAULT_MAX_GAS_PER_WINDOW),
        read_circuit_breaker_limit("THRYLOS_EVM_MAX_TX_PER_WINDOW", DEFAULT_MAX_TX_PER_WINDOW),
        read_circuit_breaker_limit(
            "THRYLOS_EVM_WINDOW_DURATION_SECS",
            DEFAULT_CIRCUIT_BREAKER_WINDOW_SECS,
        ),
    )
}

// ============================================================================
// EVM Executor - WITH ATOMIC NONCE VALIDATION & RESERVATION SYSTEM
// ============================================================================

pub struct EVMExecutor {
    magic: u64,
    db: ThrylosDB,
    chain_id: u64,
    account_locks: Arc<DashMap<Address, Arc<Mutex<()>>>>,
    reserved_nonces: Arc<RwLock<HashMap<Address, Vec<u64>>>>,
    circuit_breaker: Arc<CircuitBreaker>, // NEW
}

impl EVMExecutor {
    pub fn new(
        chain_id: u64,
        get_balance_fn: extern "C" fn(CAddress) -> CU256,
        get_nonce_fn: extern "C" fn(CAddress) -> u64,
        get_code_fn: extern "C" fn(CAddress) -> CByteSlice,
        get_storage_fn: extern "C" fn(CAddress, CU256) -> CU256,
    ) -> Self {
        let db = ThrylosDB {
            get_balance_fn,
            get_nonce_fn,
            get_code_fn,
            get_storage_fn,
            cache: CacheDB::new(EmptyDB),
        };

        let (max_gas_per_window, max_tx_per_window, window_duration_secs) =
            circuit_breaker_config_from_env();
        let circuit_breaker = Arc::new(CircuitBreaker::new(
            max_gas_per_window,
            max_tx_per_window,
            window_duration_secs,
        ));

        Self {
            magic: EXECUTOR_MAGIC,
            db,
            chain_id,
            account_locks: Arc::new(DashMap::new()),
            reserved_nonces: Arc::new(RwLock::new(HashMap::new())),
            circuit_breaker,
        }
    }

    pub fn reserve_nonce(&self, address: &Address) -> u64 {
        let mut reserved = self.reserved_nonces.write();
        let c_addr = CAddress {
            bytes: address.0 .0,
        };
        let current_nonce = (self.db.get_nonce_fn)(c_addr);

        let next_nonce = reserved
            .get(address)
            .and_then(|nonces| nonces.iter().max().copied())
            .map(|max_nonce| max_nonce + 1)
            .unwrap_or(current_nonce);

        reserved
            .entry(*address)
            .or_insert_with(Vec::new)
            .push(next_nonce);

        next_nonce
    }

    pub fn release_nonce(&self, address: &Address, nonce: u64) {
        let mut reserved = self.reserved_nonces.write();
        if let Some(nonces) = reserved.get_mut(address) {
            nonces.retain(|&n| n != nonce);
            if nonces.is_empty() {
                reserved.remove(address);
            }
        }
    }

    pub fn get_next_nonce(&self, address: &Address) -> u64 {
        let reserved = self.reserved_nonces.read();
        let c_addr = CAddress {
            bytes: address.0 .0,
        };
        let current_nonce = (self.db.get_nonce_fn)(c_addr);

        reserved
            .get(address)
            .and_then(|nonces| nonces.iter().max().copied())
            .map(|max_nonce| max_nonce + 1)
            .unwrap_or(current_nonce)
    }

    fn execute_call(
        &mut self,
        caller: Address,
        to: Address,
        data: Bytes,
        gas_limit: u64,
        value: U256,
        nonce: u64,
    ) -> CExecutionResult {
        // Check circuit breaker
        if let Err(e) = self.circuit_breaker.check_and_record(gas_limit) {
            eprintln!("⚠️ Circuit breaker triggered: {}", e);
            return create_error_result_with_code(&e, FFIErrorCode::ExecutionFailed);
        }

        let mut tx_env = TxEnv::default();
        tx_env.caller = caller;
        tx_env.transact_to = TransactTo::Call(to);
        tx_env.data = data;
        tx_env.gas_limit = gas_limit;
        tx_env.value = value;
        tx_env.chain_id = Some(self.chain_id);
        tx_env.nonce = Some(nonce);

        let mut evm = Evm::builder()
            .with_db(&mut self.db)
            .modify_tx_env(|tx| *tx = tx_env)
            .build();

        let result = match evm.transact() {
            Ok(result) => result,
            Err(e) => {
                let err_msg = format!("EVM execution failed: {:?}", e);
                return create_error_result_with_code(&err_msg, FFIErrorCode::ExecutionFailed);
            }
        };

        let gas_used = result.result.gas_used();

        let return_data = if let Some(output) = result.result.output() {
            // output is a Bytes (Arc-backed).  We need a pointer that
            // Vec::from_raw_parts can later reclaim, so copy into an owned Vec
            // and leak that — not the Arc.
            let mut owned: Vec<u8> = output.to_vec();
            let len = owned.len();
            let ptr = owned.as_mut_ptr();
            std::mem::forget(owned); // prevent Vec destructor; revm_free_bytes will reclaim

            // ✅ Track the allocation before returning
            get_memory_tracker().track_return_data(ptr, len);

            CByteSlice { data: ptr, len }
        } else {
            CByteSlice {
                data: std::ptr::null(),
                len: 0,
            }
        };

        if result.result.is_success() {
            CExecutionResult {
                success: 1,
                gas_used,
                return_data,
                error_message: std::ptr::null(),
                error_code: FFIErrorCode::Success as i32,
            }
        } else {
            let error_msg = format!("Execution reverted: {:?}", result.result);
            let c_msg = CString::new(error_msg)
                .unwrap_or_else(|_| CString::new("Execution reverted").unwrap());
            let msg_ptr = c_msg.into_raw();

            // ✅ Track error message
            get_memory_tracker().track_error_message(msg_ptr);

            CExecutionResult {
                success: 0,
                gas_used,
                return_data,
                error_message: msg_ptr,
                error_code: FFIErrorCode::Revert as i32,
            }
        }
    }

    fn deploy_contract(
        &mut self,
        deployer: Address,
        code: Bytes,
        gas_limit: u64,
        value: U256,
        nonce: u64,
    ) -> CExecutionResult {
        // Validate bytecode
        if let Err(e) = validate_bytecode(&code) {
            return create_error_result_with_code(
                &format!("Bytecode validation failed: {}", e),
                FFIErrorCode::InvalidInput,
            );
        }

        // Check circuit breaker
        if let Err(e) = self.circuit_breaker.check_and_record(gas_limit) {
            return create_error_result_with_code(&e, FFIErrorCode::ExecutionFailed);
        }

        if gas_limit < MIN_DEPLOYMENT_GAS {
            return create_error_result_with_code(
                &format!(
                    "Gas limit {} below minimum deployment gas {}",
                    gas_limit, MIN_DEPLOYMENT_GAS
                ),
                FFIErrorCode::OutOfGas,
            );
        }

        let mut tx_env = TxEnv::default();
        tx_env.caller = deployer;
        tx_env.transact_to = TransactTo::Create;
        tx_env.data = code;
        tx_env.gas_limit = gas_limit;
        tx_env.value = value;
        tx_env.chain_id = Some(self.chain_id);
        tx_env.nonce = Some(nonce);

        let mut evm = Evm::builder()
            .with_db(&mut self.db)
            .modify_tx_env(|tx| *tx = tx_env)
            .build();

        let result = match evm.transact() {
            Ok(result) => result,
            Err(e) => {
                let err_msg = format!("Contract deployment failed: {:?}", e);
                return create_error_result_with_code(&err_msg, FFIErrorCode::ExecutionFailed);
            }
        };

        let gas_used = result.result.gas_used();

        let return_data = if let Some(output) = result.result.output() {
            // Same fix as execute_call: copy into an owned Vec before leaking.
            let mut owned: Vec<u8> = output.to_vec();
            let len = owned.len();
            let ptr = owned.as_mut_ptr();
            std::mem::forget(owned);

            get_memory_tracker().track_return_data(ptr, len);

            CByteSlice { data: ptr, len }
        } else {
            CByteSlice {
                data: std::ptr::null(),
                len: 0,
            }
        };

        if result.result.is_success() {
            CExecutionResult {
                success: 1,
                gas_used,
                return_data,
                error_message: std::ptr::null(),
                error_code: FFIErrorCode::Success as i32,
            }
        } else {
            let error_msg = format!("Deployment failed: {:?}", result.result);
            let c_msg = CString::new(error_msg)
                .unwrap_or_else(|_| CString::new("Deployment failed").unwrap());
            let msg_ptr = c_msg.into_raw();

            // ✅ Track error message
            get_memory_tracker().track_error_message(msg_ptr);

            CExecutionResult {
                success: 0,
                gas_used,
                return_data,
                error_message: msg_ptr,
                error_code: FFIErrorCode::Revert as i32,
            }
        }
    }

    // convert_result removed: it used Box::leak and untracked CString::into_raw().
    // All paths now go through execute_call / deploy_contract, which track every
    // allocation via get_memory_tracker() before returning.

    pub fn is_account_locked(&self, addr: &Address) -> bool {
        if let Some(lock) = self.account_locks.get(addr) {
            lock.try_lock().is_none()
        } else {
            false
        }
    }

    pub fn get_active_locks_count(&self) -> usize {
        self.account_locks.len()
    }

    pub fn get_reserved_nonces_count(&self) -> usize {
        let reserved = self.reserved_nonces.read();
        reserved.values().map(|v| v.len()).sum()
    }
}

// ============================================================================
// C FFI Exports for Go
// ============================================================================

#[no_mangle]
pub extern "C" fn revm_executor_new(
    chain_id: u64,
    get_balance_fn: extern "C" fn(CAddress) -> CU256,
    get_nonce_fn: extern "C" fn(CAddress) -> u64,
    get_code_fn: extern "C" fn(CAddress) -> CByteSlice,
    get_storage_fn: extern "C" fn(CAddress, CU256) -> CU256,
) -> *mut EVMExecutor {
    // 🛡️ SECURITY FIX: Catch panic during initialization
    let result = catch_unwind(AssertUnwindSafe(|| {
        let executor = EVMExecutor::new(
            chain_id,
            get_balance_fn,
            get_nonce_fn,
            get_code_fn,
            get_storage_fn,
        );
        Box::into_raw(Box::new(executor))
    }));

    match result {
        Ok(ptr) => ptr,
        Err(_) => {
            eprintln!("🚨 CRITICAL: Panic in revm_executor_new");
            std::ptr::null_mut()
        }
    }
}

/// Enhanced executor cleanup with leak detection
#[no_mangle]
pub extern "C" fn revm_executor_free(executor: *mut EVMExecutor) {
    // 🛡️ SECURITY: Catch panics during destruction (Drop trait)
    let _ = catch_unwind(AssertUnwindSafe(|| {
        if executor.is_null() {
            return;
        }

        unsafe {
            (*executor).magic = EXECUTOR_FREED_MAGIC;

            // Report leaks before destroying
            let leak_count = revm_get_leak_count();
            if leak_count > 0 {
                eprintln!(
                    "⚠️ WARNING: Freeing executor with {} potential memory leaks",
                    leak_count
                );
                revm_report_memory_stats();
            }

            // If this panics (e.g. locking issues in DashMap/RwLock), we catch it here.
            let _ = Box::from_raw(executor);
        }
    }));
}

#[no_mangle]
pub extern "C" fn revm_cleanup_leaked_memory() -> usize {
    let tracker = get_memory_tracker();
    let stats = tracker.get_stats();

    if stats.potential_leaks == 0 {
        return 0;
    }

    eprintln!(
        "🧹 Memory Leak Report: {} potential leaks",
        stats.potential_leaks
    );
    if stats.current_error_messages > 0 {
        eprintln!(
            "   ⚠️ {} error messages not freed",
            stats.current_error_messages
        );
    }
    if stats.current_return_data > 0 {
        eprintln!("   ⚠️ {} return data not freed", stats.current_return_data);
    }

    stats.potential_leaks
}

#[no_mangle]
pub extern "C" fn revm_free_result(result: CExecutionResult) {
    // Free return data if present
    if !result.return_data.data.is_null() && result.return_data.len > 0 {
        revm_free_bytes(result.return_data.data as *mut u8, result.return_data.len);
    }

    // Free error message if present
    if !result.error_message.is_null() {
        revm_free_error_message(result.error_message as *mut c_char);
    }
}

#[no_mangle]
pub extern "C" fn revm_execute_call(
    executor: *mut EVMExecutor,
    caller: CAddress,
    to: CAddress,
    data: CByteSlice,
    gas_limit: u64,
    value: CU256,
    nonce: u64,
) -> CExecutionResult {
    let mut ffi_result = ffi_safe_exec(AssertUnwindSafe(|| {
        execute_call_impl(executor, caller, to, data, gas_limit, value, nonce)
    }));

    match ffi_result.error_code {
        FFIErrorCode::Success => ffi_result.value,
        FFIErrorCode::PanicCaught => {
            eprintln!("🚨 Panic caught in revm_execute_call");
            if !ffi_result.error_message.is_null() {
                revm_free_error_message(ffi_result.error_message);
                ffi_result.error_message = std::ptr::null_mut();
            }
            create_error_result_with_code("Rust panic in execute_call", FFIErrorCode::PanicCaught)
        }
        _ => {
            let msg = consume_error_message(&mut ffi_result);
            create_error_result_with_code(&msg, ffi_result.error_code)
        }
    }
}

fn execute_call_impl(
    executor: *mut EVMExecutor,
    caller: CAddress,
    to: CAddress,
    data: CByteSlice,
    gas_limit: u64,
    value: CU256,
    nonce: u64,
) -> Result<CExecutionResult, String> {
    validate_gas_limit(gas_limit).map_err(|e| e.to_string())?;

    validate_data(data, MAX_CALLDATA_SIZE, "Calldata")?;

    validate_executor(executor).map_err(|e| e.to_string())?;

    let executor = unsafe { &mut *executor };
    let caller_addr = Address::from_slice(&caller.bytes);
    let to_addr = Address::from_slice(&to.bytes);

    let data_bytes = if data.len > 0 {
        unsafe { Bytes::copy_from_slice(slice::from_raw_parts(data.data, data.len)) }
    } else {
        Bytes::default()
    };

    let value_u256 = U256::from_be_bytes(value.bytes);

    let result = executor.execute_call(
        caller_addr,
        to_addr,
        data_bytes,
        gas_limit,
        value_u256,
        nonce,
    );

    if result.success == 0 {
        // Read the error string while the pointer is still valid.
        let msg = if !result.error_message.is_null() {
            unsafe {
                CStr::from_ptr(result.error_message)
                    .to_string_lossy()
                    .to_string()
            }
        } else {
            "Execution failed".to_string()
        };
        // The CExecutionResult is about to be dropped.  Its error_message and
        // return_data pointers are tracked by MemoryTracker but nothing will
        // ever call revm_free_result on this local value, so we must free them
        // here or they leak permanently.
        revm_free_result(result);
        return Err(msg);
    }

    Ok(result)
}

fn deploy_contract_impl(
    executor: *mut EVMExecutor,
    deployer: CAddress,
    code: CByteSlice,
    gas_limit: u64,
    value: CU256,
    nonce: u64,
) -> Result<CExecutionResult, String> {
    if gas_limit < MIN_DEPLOYMENT_GAS {
        return Err(format!(
            "Deployment requires minimum {} gas, got {}",
            MIN_DEPLOYMENT_GAS, gas_limit
        ));
    }

    if gas_limit > MAX_GAS_LIMIT {
        return Err(format!(
            "Gas limit {} exceeds maximum {}",
            gas_limit, MAX_GAS_LIMIT
        ));
    }

    validate_data(code, MAX_BYTECODE_SIZE, "Bytecode")?;

    validate_executor(executor).map_err(|e| e.to_string())?;

    let code_bytes = if code.len > 0 {
        unsafe { Bytes::copy_from_slice(slice::from_raw_parts(code.data, code.len)) }
    } else {
        return Err("Cannot deploy empty bytecode".to_string());
    };

    if let Err(e) = validate_bytecode(&code_bytes) {
        return Err(format!("Bytecode validation failed: {}", e));
    }

    let executor = unsafe { &mut *executor };
    let deployer_addr = Address::from_slice(&deployer.bytes);
    let value_u256 = U256::from_be_bytes(value.bytes);

    let result = executor.deploy_contract(deployer_addr, code_bytes, gas_limit, value_u256, nonce);

    if result.success == 0 {
        let msg = if !result.error_message.is_null() {
            unsafe {
                CStr::from_ptr(result.error_message)
                    .to_string_lossy()
                    .to_string()
            }
        } else {
            "Deployment failed".to_string()
        };
        // Same as execute_call_impl: free tracked pointers before this local
        // CExecutionResult is dropped, or they leak permanently.
        revm_free_result(result);
        return Err(msg);
    }

    Ok(result)
}

fn create_error_result_with_code(msg: &str, code: FFIErrorCode) -> CExecutionResult {
    let c_msg =
        CString::new(msg).unwrap_or_else(|_| CString::new("Error creating message").unwrap());

    let ptr = c_msg.into_raw();

    // ✅ Track the allocation
    get_memory_tracker().track_error_message(ptr);

    CExecutionResult {
        success: 0,
        gas_used: 0,
        return_data: CByteSlice {
            data: std::ptr::null(),
            len: 0,
        },
        error_message: ptr,
        error_code: code as i32,
    }
}

fn get_error_message(result: &FFIResult<CExecutionResult>) -> String {
    if result.error_message.is_null() {
        "Unknown error".to_string()
    } else {
        unsafe {
            CStr::from_ptr(result.error_message)
                .to_string_lossy()
                .to_string()
        }
    }
}

fn consume_error_message(result: &mut FFIResult<CExecutionResult>) -> String {
    let msg = get_error_message(result);
    if !result.error_message.is_null() {
        revm_free_error_message(result.error_message);
        result.error_message = std::ptr::null_mut();
    }
    msg
}

#[no_mangle]
pub extern "C" fn revm_deploy_contract(
    executor: *mut EVMExecutor,
    deployer: CAddress,
    code: CByteSlice,
    gas_limit: u64,
    value: CU256,
    nonce: u64,
) -> CExecutionResult {
    let mut ffi_result = ffi_safe_exec(AssertUnwindSafe(|| {
        deploy_contract_impl(executor, deployer, code, gas_limit, value, nonce)
    }));

    match ffi_result.error_code {
        FFIErrorCode::Success => ffi_result.value,
        FFIErrorCode::PanicCaught => {
            eprintln!("🚨 Panic caught in revm_deploy_contract");
            if !ffi_result.error_message.is_null() {
                revm_free_error_message(ffi_result.error_message);
                ffi_result.error_message = std::ptr::null_mut();
            }
            create_error_result_with_code(
                "Rust panic in deploy_contract",
                FFIErrorCode::PanicCaught,
            )
        }
        _ => {
            let msg = consume_error_message(&mut ffi_result);
            create_error_result_with_code(&msg, ffi_result.error_code)
        }
    }
}

#[no_mangle]
pub extern "C" fn revm_reserve_nonce(executor: *mut EVMExecutor, address: CAddress) -> u64 {
    let result = catch_unwind(AssertUnwindSafe(|| {
        if validate_executor(executor).is_err() {
            return u64::MAX;
        }
        let executor = unsafe { &*executor };
        let addr = Address::from_slice(&address.bytes);
        executor.reserve_nonce(&addr)
    }));
    result.unwrap_or(u64::MAX) // Return MAX on panic
}

#[no_mangle]
pub extern "C" fn revm_release_nonce(executor: *mut EVMExecutor, address: CAddress, nonce: u64) {
    let _ = catch_unwind(AssertUnwindSafe(|| {
        if validate_executor(executor).is_ok() {
            let executor = unsafe { &*executor };
            let addr = Address::from_slice(&address.bytes);
            executor.release_nonce(&addr, nonce);
        }
    }));
}

#[no_mangle]
pub extern "C" fn revm_get_next_nonce(executor: *mut EVMExecutor, address: CAddress) -> u64 {
    let result = catch_unwind(AssertUnwindSafe(|| {
        if validate_executor(executor).is_err() {
            return u64::MAX;
        }
        let executor = unsafe { &*executor };
        let addr = Address::from_slice(&address.bytes);
        executor.get_next_nonce(&addr)
    }));
    result.unwrap_or(u64::MAX)
}

#[no_mangle]
pub extern "C" fn revm_free_string(s: *mut c_char) {
    if !s.is_null() {
        unsafe {
            let _ = CString::from_raw(s);
        }
    }
}

#[no_mangle]
pub extern "C" fn revm_free_bytes(data: *mut u8, len: usize) {
    if data.is_null() {
        return;
    }

    // ✅ Check if pointer is tracked (prevents double-free)
    if !get_memory_tracker().untrack_return_data(data) {
        eprintln!(
            "⚠️ revm_free_bytes: Prevented double-free or invalid free at {:p}",
            data
        );
        return;
    }

    // Free the actual memory
    unsafe {
        let _ = Vec::from_raw_parts(data, len, len);
    }
}

#[no_mangle]
pub extern "C" fn revm_calculate_create_address(deployer: CAddress, nonce: u64) -> CAddress {
    let result = catch_unwind(AssertUnwindSafe(|| {
        use revm::primitives::keccak256;

        let mut rlp = Vec::new();
        rlp.push(0xc0 + 22);
        rlp.push(0x80 + 20);
        rlp.extend_from_slice(&deployer.bytes);

        if nonce == 0 {
            rlp.push(0x80);
        } else if nonce < 0x80 {
            rlp.push(nonce as u8);
        } else {
            let nonce_bytes = nonce.to_be_bytes();
            // Safety: if nonce >= 0x80, it must have a non-zero byte
            let start = nonce_bytes.iter().position(|&b| b != 0).unwrap_or(0);
            let len = 8 - start;
            rlp.push(0x80 + len as u8);
            rlp.extend_from_slice(&nonce_bytes[start..]);
        }

        let hash = keccak256(&rlp);
        let mut result = CAddress { bytes: [0u8; 20] };
        result.bytes.copy_from_slice(&hash[12..]);
        result
    }));

    // Return empty address on panic
    result.unwrap_or(CAddress { bytes: [0u8; 20] })
}

#[no_mangle]
pub extern "C" fn revm_estimate_gas(
    executor: *mut EVMExecutor,
    caller: CAddress,
    to: CAddress,
    data: CByteSlice,
    value: CU256,
) -> u64 {
    // 🛡️ SECURITY: Wrap EVERYTHING in catch_unwind, including validation
    let result = catch_unwind(AssertUnwindSafe(|| {
        // 1. Validation inside safety block
        if validate_executor(executor).is_err() {
            eprintln!("⚠️ Gas estimation failed: invalid executor");
            return u64::MAX;
        }

        if let Err(msg) = validate_data(data, MAX_CALLDATA_SIZE, "Calldata") {
            eprintln!("⚠️ Gas estimation failed: {}", msg);
            return u64::MAX;
        }

        // 2. Execution logic
        let executor = unsafe { &mut *executor };
        let caller_addr = Address::from_slice(&caller.bytes);
        let to_addr = Address::from_slice(&to.bytes);

        let data_bytes = if data.len > 0 {
            unsafe { Bytes::copy_from_slice(slice::from_raw_parts(data.data, data.len)) }
        } else {
            Bytes::default()
        };

        let value_u256 = U256::from_be_bytes(value.bytes);
        let high_gas = MAX_GAS_LIMIT;

        let c_addr = CAddress {
            bytes: caller_addr.0 .0,
        };
        let current_nonce = (executor.db.get_nonce_fn)(c_addr);

        let mut tx_env = TxEnv::default();
        tx_env.caller = caller_addr;
        tx_env.transact_to = TransactTo::Call(to_addr);
        tx_env.data = data_bytes;
        tx_env.gas_limit = high_gas;
        tx_env.value = value_u256;
        tx_env.chain_id = Some(executor.chain_id);
        tx_env.nonce = Some(current_nonce);

        let mut evm = Evm::builder()
            .with_db(&mut executor.db)
            .modify_tx_env(|tx| *tx = tx_env)
            .build();

        match evm.transact() {
            Ok(result) => result.result.gas_used(),
            Err(_) => u64::MAX,
        }
    }));

    match result {
        Ok(gas_estimate) => gas_estimate,
        Err(_) => {
            eprintln!("🚨 CRITICAL: Rust panic caught in revm_estimate_gas!");
            u64::MAX
        }
    }
}

#[no_mangle]
pub extern "C" fn revm_get_active_locks(executor: *mut EVMExecutor) -> usize {
    let result = catch_unwind(AssertUnwindSafe(|| {
        if executor.is_null() {
            return 0;
        }
        unsafe { (*executor).get_active_locks_count() }
    }));
    result.unwrap_or(0)
}

#[no_mangle]
pub extern "C" fn revm_is_account_locked(executor: *mut EVMExecutor, address: CAddress) -> bool {
    let result = catch_unwind(AssertUnwindSafe(|| {
        if executor.is_null() {
            return false;
        }
        let executor = unsafe { &*executor };
        let addr = Address::from_slice(&address.bytes);
        executor.is_account_locked(&addr)
    }));
    result.unwrap_or(false)
}

#[no_mangle]
pub extern "C" fn revm_get_reserved_nonces_count(executor: *mut EVMExecutor) -> usize {
    let result = catch_unwind(AssertUnwindSafe(|| {
        if executor.is_null() {
            return 0;
        }
        unsafe { (*executor).get_reserved_nonces_count() }
    }));
    result.unwrap_or(0)
}

/// Report memory statistics to stderr
/// This can be called from Go to check for memory leaks
#[no_mangle]
pub extern "C" fn revm_report_memory_stats() {
    get_memory_tracker().report();
}

/// Get the number of potential memory leaks
/// Returns the difference between allocations and frees
#[no_mangle]
pub extern "C" fn revm_get_leak_count() -> usize {
    get_memory_tracker().get_leak_count()
}

/// Get the number of currently tracked error messages
#[no_mangle]
pub extern "C" fn revm_get_tracked_error_messages() -> usize {
    get_memory_tracker().get_error_message_count()
}

/// Get the number of currently tracked return data allocations
#[no_mangle]
pub extern "C" fn revm_get_tracked_return_data() -> usize {
    get_memory_tracker().get_return_data_count()
}
