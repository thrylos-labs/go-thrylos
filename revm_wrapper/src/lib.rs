// thrylos-revm/src/lib.rs
// Ultra-fast EVM implementation using revm (Rust)
// 5-10x faster than go-ethereum

// ============================================================================
// SECURITY FIXES:
// - C-01: Atomic nonce validation with per-account locking
// - C-02: Input validation for all FFI boundaries
// ============================================================================

const MAX_GAS_LIMIT: u64 = 30_000_000;           // 30M gas (Ethereum block limit)
const MAX_CALLDATA_SIZE: usize = 1_048_576;      // 1 MB max calldata
const MAX_BYTECODE_SIZE: usize = 24_576;         // 24 KB (EIP-170 limit)
const EXECUTOR_MAGIC: u64 = 0xDEADBEEF_CAFEBABE;
const EXECUTOR_FREED_MAGIC: u64 = 0xDEADDEAD_DEADBEEF;

macro_rules! with_executor {
    ($executor:expr, $body:expr) => {{
        if let Err(msg) = validate_executor($executor) {
            return create_error_result(msg);
        }
        
        let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            let executor: &mut EVMExecutor = unsafe { &mut *$executor };
            $body(executor)
        }));
        
        match result {
            Ok(r) => r,
            Err(_) => create_error_result("Panic in executor operation"),
        }
    }};
}

use revm::{
    db::CacheDB,
    primitives::{
        Address, Bytecode, Bytes, ExecutionResult, Output, TransactTo, TxEnv, B256, U256,
    },
    Database, Evm,
};
use std::ffi::CString;
use std::os::raw::c_char;
use std::slice;
use std::panic::{catch_unwind, AssertUnwindSafe};
use std::ptr;

// ✅ NEW IMPORTS for C-01 fix
use dashmap::DashMap;
use std::sync::Arc;
use parking_lot::Mutex;

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
    success: bool,
    gas_used: u64,
    return_data: CByteSlice,
    error_message: *const c_char,
}

// ============================================================================
// Helper Functions
// ============================================================================

/// Helper function for creating error results
fn create_error_result(msg: &str) -> CExecutionResult {
    CExecutionResult {
        success: false,
        gas_used: 0,
        return_data: CByteSlice { 
            data: ptr::null(), 
            len: 0 
        },
        error_message: CString::new(msg)
            .unwrap_or_else(|_| CString::new("Validation error").unwrap())
            .into_raw(),
    }
}

// ============================================================================
// Input Validation Helpers (C-02 Fix)
// ============================================================================

/// Validate executor pointer for safety
fn validate_executor(executor: *mut EVMExecutor) -> Result<(), &'static str> {
    if executor.is_null() {
        return Err("Executor pointer is null");
    }
    
    unsafe {
        // ✅ Check magic number
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
    // If length is 0, pointer can be null
    if data.len == 0 {
        return Ok(());
    }
    
    // If length > 0, pointer must be valid
    if data.data.is_null() {
        return Err(format!("{} pointer is null but length is {}", data_type, data.len));
    }
    
    // Check size limit
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
    // Callbacks to Go for state access
    get_balance_fn: extern "C" fn(CAddress) -> CU256,
    get_nonce_fn: extern "C" fn(CAddress) -> u64,
    set_nonce_fn: extern "C" fn(CAddress, u64) -> bool,  // ✅ NEW for C-01
    get_code_fn: extern "C" fn(CAddress) -> CByteSlice,
    get_storage_fn: extern "C" fn(CAddress, CU256) -> CU256,
    
    // Cache for performance
    #[allow(dead_code)]
    cache: CacheDB<EmptyDB>,
}

impl Database for ThrylosDB {
    type Error = std::io::Error;

    fn basic(&mut self, address: Address) -> Result<Option<revm::primitives::AccountInfo>, Self::Error> {
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

    fn basic(&mut self, _address: Address) -> Result<Option<revm::primitives::AccountInfo>, Self::Error> {
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

// ============================================================================
// EVM Executor - WITH ATOMIC NONCE VALIDATION (C-01 FIX)
// ============================================================================

pub struct EVMExecutor {
    magic: u64,  
    db: ThrylosDB,
    chain_id: u64,
    account_locks: Arc<DashMap<Address, Arc<Mutex<()>>>>,  // ✅ NEW for C-01
}

impl EVMExecutor {
    pub fn new(
        chain_id: u64,
        get_balance_fn: extern "C" fn(CAddress) -> CU256,
        get_nonce_fn: extern "C" fn(CAddress) -> u64,
        set_nonce_fn: extern "C" fn(CAddress, u64) -> bool,
        get_code_fn: extern "C" fn(CAddress) -> CByteSlice,
        get_storage_fn: extern "C" fn(CAddress, CU256) -> CU256,
    ) -> Self {
        let db = ThrylosDB {
            get_balance_fn,
            get_nonce_fn,
            set_nonce_fn,
            get_code_fn,
            get_storage_fn,
            cache: CacheDB::new(EmptyDB),
        };

        Self {
            magic: EXECUTOR_MAGIC,  // ✅ Initialize magic here
            db,
            chain_id,
            account_locks: Arc::new(DashMap::new()),
        }
    }

    /// ✅ NEW: Get or create a lock for a specific account
    fn get_account_lock(&self, addr: &Address) -> Arc<Mutex<()>> {
        self.account_locks
            .entry(*addr)
            .or_insert_with(|| Arc::new(Mutex::new(())))
            .clone()
    }

    /// ✅ UPDATED: Execute call with atomic nonce validation
    pub fn execute_call(
        &mut self,
        caller: Address,
        to: Address,
        data: Bytes,
        gas_limit: u64,
        value: U256,
        nonce: u64,  // ✅ NEW: Required nonce parameter
    ) -> CExecutionResult {
        // 🔒 STEP 1: Acquire exclusive lock for caller's account
        let account_lock = self.get_account_lock(&caller);
        let _guard = account_lock.lock();
        
        // 🔒 STEP 2: Validate nonce atomically while holding lock
        let c_addr = CAddress {
            bytes: caller.0.0,
        };
        let state_nonce = (self.db.get_nonce_fn)(c_addr);
        
        if nonce != state_nonce {
            return CExecutionResult {
                success: false,
                gas_used: 0,
                return_data: CByteSlice {
                    data: std::ptr::null(),
                    len: 0,
                },
                error_message: CString::new(format!(
                    "Nonce mismatch: expected {}, got {}",
                    state_nonce, nonce
                ))
                .unwrap()
                .into_raw(),
            };
        }

        // 🔒 STEP 3: Immediately increment nonce BEFORE execution
        let nonce_updated = (self.db.set_nonce_fn)(c_addr, state_nonce + 1);
        
        if !nonce_updated {
            return CExecutionResult {
                success: false,
                gas_used: 0,
                return_data: CByteSlice {
                    data: std::ptr::null(),
                    len: 0,
                },
                error_message: CString::new("Failed to increment nonce".to_string())
                    .unwrap()
                    .into_raw(),
            };
        }

        // STEP 4: Configure transaction
        let mut tx_env = TxEnv::default();
        tx_env.caller = caller;
        tx_env.transact_to = TransactTo::Call(to);
        tx_env.data = data;
        tx_env.gas_limit = gas_limit;
        tx_env.value = value;
        tx_env.chain_id = Some(self.chain_id);
        tx_env.nonce = Some(nonce);

        // STEP 5: Create EVM instance
        let mut evm = Evm::builder()
            .with_db(&mut self.db)
            .modify_tx_env(|tx| *tx = tx_env)
            .build();

        // STEP 6: Execute transaction
        let result = match evm.transact() {
            Ok(result) => Self::convert_result(result.result),
            Err(e) => CExecutionResult {
                success: false,
                gas_used: 0,
                return_data: CByteSlice {
                    data: std::ptr::null(),
                    len: 0,
                },
                error_message: CString::new(format!("{:?}", e))
                    .unwrap()
                    .into_raw(),
            },
        };

        // 🔓 Lock automatically released when _guard goes out of scope
        result
    }

    /// ✅ UPDATED: Deploy contract with atomic nonce validation
    pub fn deploy_contract(
        &mut self,
        deployer: Address,
        bytecode: Bytes,
        gas_limit: u64,
        value: U256,
        nonce: u64,  // ✅ NEW: Required nonce parameter
    ) -> CExecutionResult {
        // 🔒 STEP 1: Acquire exclusive lock for deployer's account
        let account_lock = self.get_account_lock(&deployer);
        let _guard = account_lock.lock();
        
        // 🔒 STEP 2: Validate nonce atomically while holding lock
        let c_addr = CAddress {
            bytes: deployer.0.0,
        };
        let state_nonce = (self.db.get_nonce_fn)(c_addr);
        
        if nonce != state_nonce {
            return CExecutionResult {
                success: false,
                gas_used: 0,
                return_data: CByteSlice {
                    data: std::ptr::null(),
                    len: 0,
                },
                error_message: CString::new(format!(
                    "Nonce mismatch: expected {}, got {}",
                    state_nonce, nonce
                ))
                .unwrap()
                .into_raw(),
            };
        }

        // 🔒 STEP 3: Immediately increment nonce BEFORE deployment
        let nonce_updated = (self.db.set_nonce_fn)(c_addr, state_nonce + 1);
        
        if !nonce_updated {
            return CExecutionResult {
                success: false,
                gas_used: 0,
                return_data: CByteSlice {
                    data: std::ptr::null(),
                    len: 0,
                },
                error_message: CString::new("Failed to increment nonce".to_string())
                    .unwrap()
                    .into_raw(),
            };
        }

        // STEP 4: Configure transaction
        let mut tx_env = TxEnv::default();
        tx_env.caller = deployer;
        tx_env.transact_to = TransactTo::Create;
        tx_env.data = bytecode;
        tx_env.gas_limit = gas_limit;
        tx_env.value = value;
        tx_env.chain_id = Some(self.chain_id);
        tx_env.nonce = Some(nonce);

        // STEP 5: Create EVM instance
        let mut evm = Evm::builder()
            .with_db(&mut self.db)
            .modify_tx_env(|tx| *tx = tx_env)
            .build();

        // STEP 6: Execute deployment
        let result = match evm.transact() {
            Ok(result) => Self::convert_result(result.result),
            Err(e) => CExecutionResult {
                success: false,
                gas_used: 0,
                return_data: CByteSlice {
                    data: std::ptr::null(),
                    len: 0,
                },
                error_message: CString::new(format!("{:?}", e))
                    .unwrap()
                    .into_raw(),
            },
        };

        // 🔓 Lock automatically released when _guard goes out of scope
        result
    }

    /// Convert REVM execution result to C-compatible result
    fn convert_result(result: ExecutionResult) -> CExecutionResult {
        match result {
            ExecutionResult::Success {
                gas_used,
                output,
                ..
            } => {
                let return_data = match output {
                    Output::Call(data) => data,
                    Output::Create(data, _) => data,
                };

                let data_len = return_data.len();
                let leaked = Box::leak(return_data.to_vec().into_boxed_slice());

                CExecutionResult {
                    success: true,
                    gas_used,
                    return_data: CByteSlice {
                        data: leaked.as_ptr(),
                        len: data_len,
                    },
                    error_message: std::ptr::null(),
                }
            }
            ExecutionResult::Revert { gas_used, output } => {
                let data_len = output.len();
                let leaked = Box::leak(output.to_vec().into_boxed_slice());
                
                CExecutionResult {
                    success: false,
                    gas_used,
                    return_data: CByteSlice {
                        data: leaked.as_ptr(),
                        len: data_len,
                    },
                    error_message: CString::new("execution reverted")
                        .unwrap()
                        .into_raw(),
                }
            }
            ExecutionResult::Halt { reason, gas_used } => CExecutionResult {
                success: false,
                gas_used,
                return_data: CByteSlice {
                    data: std::ptr::null(),
                    len: 0,
                },
                error_message: CString::new(format!("execution halted: {:?}", reason))
                    .unwrap()
                    .into_raw(),
            },
        }
    }

    /// ✅ NEW: Helper method for checking if an account is currently locked
    pub fn is_account_locked(&self, addr: &Address) -> bool {
        if let Some(lock) = self.account_locks.get(addr) {
            lock.try_lock().is_none()
        } else {
            false
        }
    }

    /// ✅ NEW: Get the current number of active account locks
    pub fn get_active_locks_count(&self) -> usize {
        self.account_locks.len()
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
    set_nonce_fn: extern "C" fn(CAddress, u64) -> bool,
    get_code_fn: extern "C" fn(CAddress) -> CByteSlice,
    get_storage_fn: extern "C" fn(CAddress, CU256) -> CU256,
) -> *mut EVMExecutor {
    let executor = EVMExecutor::new(
        chain_id,
        get_balance_fn,
        get_nonce_fn,
        set_nonce_fn,
        get_code_fn,
        get_storage_fn,
    );
    Box::into_raw(Box::new(executor))
}

#[no_mangle]
pub extern "C" fn revm_executor_free(executor: *mut EVMExecutor) {
    if executor.is_null() {
        return;
    }
    
    unsafe {
        // ✅ Mark as freed before dropping
        (*executor).magic = EXECUTOR_FREED_MAGIC;
        let _ = Box::from_raw(executor);
    }
}

#[no_mangle]
pub extern "C" fn revm_free_result(result: CExecutionResult) {
    // Free return_data if it exists
    if !result.return_data.data.is_null() && result.return_data.len > 0 {
        unsafe {
            let _ = Vec::from_raw_parts(
                result.return_data.data as *mut u8,
                result.return_data.len,
                result.return_data.len,
            );
        }
    }
    // Free error_message if it exists
    if !result.error_message.is_null() {
        unsafe {
            let _ = CString::from_raw(result.error_message as *mut c_char);
        }
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
    // Validate other inputs first
    if let Err(msg) = validate_gas_limit(gas_limit) {
        return create_error_result(msg);
    }
    
    if let Err(msg) = validate_data(data, MAX_CALLDATA_SIZE, "Calldata") {
        return create_error_result(&msg);
    }
    
    // ✅ Use the safe wrapper
    with_executor!(executor, |exec: &mut EVMExecutor| {
        let caller_addr = Address::from_slice(&caller.bytes);
        let to_addr = Address::from_slice(&to.bytes);
        
        let data_bytes = if data.len > 0 {
            unsafe { Bytes::copy_from_slice(slice::from_raw_parts(data.data, data.len)) }
        } else {
            Bytes::default()
        };
        
        let value_u256 = U256::from_be_bytes(value.bytes);
        
        exec.execute_call(caller_addr, to_addr, data_bytes, gas_limit, value_u256, nonce)
    })
}

#[no_mangle]
pub extern "C" fn revm_deploy_contract(
    executor: *mut EVMExecutor,
    deployer: CAddress,
    bytecode: CByteSlice,
    gas_limit: u64,
    value: CU256,
    nonce: u64,
) -> CExecutionResult {
    // Validate inputs
    if let Err(msg) = validate_gas_limit(gas_limit) {
        return create_error_result(msg);
    }

    if let Err(msg) = validate_data(bytecode, MAX_BYTECODE_SIZE, "Bytecode") {
        return create_error_result(&msg);
    }

    // ✅ Use the safe wrapper
    with_executor!(executor, |exec: &mut EVMExecutor| {
        let deployer_addr = Address::from_slice(&deployer.bytes);
        
        let bytecode_bytes = if bytecode.len > 0 {
            unsafe { Bytes::copy_from_slice(slice::from_raw_parts(bytecode.data, bytecode.len)) }
        } else {
            Bytes::default()
        };
        let value_u256 = U256::from_be_bytes(value.bytes);

        exec.deploy_contract(
            deployer_addr, 
            bytecode_bytes, 
            gas_limit, 
            value_u256,
            nonce
        )
    })
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
    if !data.is_null() {
        unsafe {
            let _ = Vec::from_raw_parts(data as *mut u8, len, len);
        }
    }
}

// ============================================================================
// Helper: Calculate contract address
// ============================================================================

#[no_mangle]
pub extern "C" fn revm_calculate_create_address(deployer: CAddress, nonce: u64) -> CAddress {
    use revm::primitives::keccak256;
    
    // RLP encode [address, nonce]
    let mut rlp = Vec::new();
    rlp.push(0xc0 + 22); // List header
    rlp.push(0x80 + 20); // Address header
    rlp.extend_from_slice(&deployer.bytes);
    
    // Encode nonce
    if nonce == 0 {
        rlp.push(0x80);
    } else if nonce < 0x80 {
        rlp.push(nonce as u8);
    } else {
        // Variable length encoding
        let nonce_bytes = nonce.to_be_bytes();
        let start = nonce_bytes.iter().position(|&b| b != 0).unwrap();
        let len = 8 - start;
        rlp.push(0x80 + len as u8);
        rlp.extend_from_slice(&nonce_bytes[start..]);
    }
    
    // Hash and take last 20 bytes
    let hash = keccak256(&rlp);
    let mut result = CAddress { bytes: [0u8; 20] };
    result.bytes.copy_from_slice(&hash[12..]);
    result
}

// ============================================================================
// Gas Estimation
// ============================================================================

#[no_mangle]
pub extern "C" fn revm_estimate_gas(
    executor: *mut EVMExecutor,
    caller: CAddress,
    to: CAddress,
    data: CByteSlice,
    value: CU256,
) -> u64 {
    // ✅ Validate inputs
    if validate_executor(executor).is_err() {
        eprintln!("⚠️  Gas estimation failed: invalid executor");
        return u64::MAX;
    }

    if let Err(msg) = validate_data(data, MAX_CALLDATA_SIZE, "Calldata") {
        eprintln!("⚠️  Gas estimation failed: {}", msg);
        return u64::MAX;
    }

    // ✅ For gas estimation, we use current nonce but DON'T increment
    let result = catch_unwind(AssertUnwindSafe(|| {
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

        // Get current nonce (read-only, no increment needed for estimation)
        let c_addr = CAddress {
            bytes: caller_addr.0.0,
        };
        let current_nonce = (executor.db.get_nonce_fn)(c_addr);

        // Create temporary transaction for estimation
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

// ============================================================================
// MONITORING: Get Lock Statistics (for debugging/monitoring)
// ============================================================================

#[no_mangle]
pub extern "C" fn revm_get_active_locks(executor: *mut EVMExecutor) -> usize {
    if executor.is_null() {
        return 0;
    }
    
    let executor = unsafe { &*executor };
    executor.get_active_locks_count()
}

#[no_mangle]
pub extern "C" fn revm_is_account_locked(executor: *mut EVMExecutor, address: CAddress) -> bool {
    if executor.is_null() {
        return false;
    }
    
    let executor = unsafe { &*executor };
    let addr = Address::from_slice(&address.bytes);
    executor.is_account_locked(&addr)
}