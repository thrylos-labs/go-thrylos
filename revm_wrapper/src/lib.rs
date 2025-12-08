// thrylos-revm/src/lib.rs
// Ultra-fast EVM implementation using revm (Rust)
// 5-10x faster than go-ethereum

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
pub struct CByteSlice { // RENAMED from CBytes
    data: *const u8,
    len: usize,
}

#[repr(C)]
pub struct CExecutionResult {
    success: bool,
    gas_used: u64,
    return_data: CByteSlice, // Updated
    error_message: *const c_char,
}

// ============================================================================
// State Database - Bridges to Thrylos state
// ============================================================================

pub struct ThrylosDB {
    // Callbacks to Go for state access
    get_balance_fn: extern "C" fn(CAddress) -> CU256,
    get_nonce_fn: extern "C" fn(CAddress) -> u64,
    get_code_fn: extern "C" fn(CAddress) -> CByteSlice, // Updated
    get_storage_fn: extern "C" fn(CAddress, CU256) -> CU256,
    
    // Cache for performance
    #[allow(dead_code)] // Suppress unused warning
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
// EVM Executor
// ============================================================================

pub struct EVMExecutor {
    db: ThrylosDB,
    chain_id: u64,
}

impl EVMExecutor {
    pub fn new(
        chain_id: u64,
        get_balance_fn: extern "C" fn(CAddress) -> CU256,
        get_nonce_fn: extern "C" fn(CAddress) -> u64,
        get_code_fn: extern "C" fn(CAddress) -> CByteSlice, // Updated
        get_storage_fn: extern "C" fn(CAddress, CU256) -> CU256,
    ) -> Self {
        let db = ThrylosDB {
            get_balance_fn,
            get_nonce_fn,
            get_code_fn,
            get_storage_fn,
            cache: CacheDB::new(EmptyDB),
        };

        Self { db, chain_id }
    }

    pub fn execute_call(
        &mut self,
        caller: Address,
        to: Address,
        data: Bytes,
        gas_limit: u64,
        value: U256,
    ) -> CExecutionResult {
        // Configure transaction
        let mut tx_env = TxEnv::default();
        tx_env.caller = caller;
        tx_env.transact_to = TransactTo::Call(to);
        tx_env.data = data;
        tx_env.gas_limit = gas_limit;
        tx_env.value = value;
        tx_env.chain_id = Some(self.chain_id);

        // Create EVM instance
        let mut evm = Evm::builder()
            .with_db(&mut self.db)
            .modify_tx_env(|tx| *tx = tx_env)
            .build();

        // Execute transaction
        match evm.transact() {
            Ok(result) => Self::convert_result(result.result),
            Err(e) => CExecutionResult {
                success: false,
                gas_used: 0,
                return_data: CByteSlice { // Updated
                    data: std::ptr::null(),
                    len: 0,
                },
                error_message: CString::new(format!("{:?}", e))
                    .unwrap()
                    .into_raw(),
            },
        }
    }

    pub fn deploy_contract(
        &mut self,
        deployer: Address,
        bytecode: Bytes,
        gas_limit: u64,
        value: U256,
    ) -> CExecutionResult {
        // Configure transaction
        let mut tx_env = TxEnv::default();
        tx_env.caller = deployer;
        tx_env.transact_to = TransactTo::Create;
        tx_env.data = bytecode;
        tx_env.gas_limit = gas_limit;
        tx_env.value = value;
        tx_env.chain_id = Some(self.chain_id);

        // Create EVM instance
        let mut evm = Evm::builder()
            .with_db(&mut self.db)
            .modify_tx_env(|tx| *tx = tx_env)
            .build();

        // Execute deployment
        match evm.transact() {
            Ok(result) => Self::convert_result(result.result),
            Err(e) => CExecutionResult {
                success: false,
                gas_used: 0,
                return_data: CByteSlice { // Updated
                    data: std::ptr::null(),
                    len: 0,
                },
                error_message: CString::new(format!("{:?}", e))
                    .unwrap()
                    .into_raw(),
            },
        }
    }

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
                    return_data: CByteSlice { // Updated
                        data: leaked.as_ptr(),
                        len: data_len,
                    },
                    error_message: std::ptr::null(),
                }
            }
            ExecutionResult::Revert { gas_used, output } => CExecutionResult {
                success: false,
                gas_used,
                return_data: CByteSlice { // Updated
                    data: output.as_ptr(),
                    len: output.len(),
                },
                error_message: CString::new("execution reverted")
                    .unwrap()
                    .into_raw(),
            },
            ExecutionResult::Halt { reason, gas_used } => CExecutionResult {
                success: false,
                gas_used,
                return_data: CByteSlice { // Updated
                    data: std::ptr::null(),
                    len: 0,
                },
                error_message: CString::new(format!("execution halted: {:?}", reason))
                    .unwrap()
                    .into_raw(),
            },
        }
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
    get_code_fn: extern "C" fn(CAddress) -> CByteSlice, // Updated
    get_storage_fn: extern "C" fn(CAddress, CU256) -> CU256,
) -> *mut EVMExecutor {
    let executor = EVMExecutor::new(
        chain_id,
        get_balance_fn,
        get_nonce_fn,
        get_code_fn,
        get_storage_fn,
    );
    Box::into_raw(Box::new(executor))
}

#[no_mangle]
pub extern "C" fn revm_executor_free(executor: *mut EVMExecutor) {
    if !executor.is_null() {
        unsafe {
            let _ = Box::from_raw(executor);
        }
    }
}

#[no_mangle]
pub extern "C" fn revm_execute_call(
    executor: *mut EVMExecutor,
    caller: CAddress,
    to: CAddress,
    data: CByteSlice, // Updated
    gas_limit: u64,
    value: CU256,
) -> CExecutionResult {
    let executor = unsafe { &mut *executor };

    let caller_addr = Address::from_slice(&caller.bytes);
    let to_addr = Address::from_slice(&to.bytes);
    let data_bytes = if data.len > 0 {
        unsafe { Bytes::copy_from_slice(slice::from_raw_parts(data.data, data.len)) }
    } else {
        Bytes::default()
    };
    let value_u256 = U256::from_be_bytes(value.bytes);

    executor.execute_call(caller_addr, to_addr, data_bytes, gas_limit, value_u256)
}

#[no_mangle]
pub extern "C" fn revm_deploy_contract(
    executor: *mut EVMExecutor,
    deployer: CAddress,
    bytecode: CByteSlice, // Updated
    gas_limit: u64,
    value: CU256,
) -> CExecutionResult {
    let executor = unsafe { &mut *executor };

    let deployer_addr = Address::from_slice(&deployer.bytes);
    let bytecode_bytes = if bytecode.len > 0 {
        unsafe { Bytes::copy_from_slice(slice::from_raw_parts(bytecode.data, bytecode.len)) }
    } else {
        Bytes::default()
    };
    let value_u256 = U256::from_be_bytes(value.bytes);

    executor.deploy_contract(deployer_addr, bytecode_bytes, gas_limit, value_u256)
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
    use revm_primitives::keccak256;
    
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
    data: CByteSlice, // Updated
    value: CU256,
) -> u64 {
    let executor = unsafe { &mut *executor };

    let caller_addr = Address::from_slice(&caller.bytes);
    let to_addr = Address::from_slice(&to.bytes);
    let data_bytes = if data.len > 0 {
        unsafe { Bytes::copy_from_slice(slice::from_raw_parts(data.data, data.len)) }
    } else {
        Bytes::default()
    };
    let value_u256 = U256::from_be_bytes(value.bytes);

    // Try with a high gas limit
    let high_gas = 30_000_000u64;
    let result = executor.execute_call(
        caller_addr,
        to_addr,
        data_bytes,
        high_gas,
        value_u256,
    );

    if result.success {
        // Add 10% buffer and round up
        let estimated = result.gas_used + (result.gas_used / 10);
        estimated
    } else {
        // Execution failed, return 0
        0
    }
}