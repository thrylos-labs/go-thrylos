// revm_wrapper/tests/gas_validation_test.rs

use revm::primitives::{Address, Bytes, U256};
use std::sync::Arc;

// Mock callback functions for testing
extern "C" fn mock_balance(_addr: thrylos_revm::CAddress) -> thrylos_revm::CU256 {
    thrylos_revm::CU256 { bytes: [0u8; 32] }
}

extern "C" fn mock_nonce(_addr: thrylos_revm::CAddress) -> u64 {
    0
}

extern "C" fn mock_code(_addr: thrylos_revm::CAddress) -> thrylos_revm::CByteSlice {
    thrylos_revm::CByteSlice {
        data: std::ptr::null(),
        len: 0,
    }
}

extern "C" fn mock_storage(_addr: thrylos_revm::CAddress, _key: thrylos_revm::CU256) -> thrylos_revm::CU256 {
    thrylos_revm::CU256 { bytes: [0u8; 32] }
}

fn create_test_executor() -> thrylos_revm::EVMExecutor {
    thrylos_revm::EVMExecutor::new(1, mock_balance, mock_nonce, mock_code, mock_storage)
}

#[test]
fn test_gas_limit_enforcement() {
    let mut executor = create_test_executor();
    
    let caller = Address::ZERO;
    let to = Address::from([1u8; 20]);
    let data = Bytes::default();
    let gas_limit = 300_000; // 300k gas per call
    let value = U256::ZERO;
    
    // Try to exceed window limit (300M gas / 300k = 1000 tx max)
    for i in 0..1100 {
        let result = executor.execute_call(
            caller,
            to,
            data.clone(),
            gas_limit,
            value,
            i, // nonce
        );
        
        if i >= 1000 {
            // Should fail after 1000 transactions
            assert_eq!(result.success, 0, "Transaction {} should have failed", i);
            
            if !result.error_message.is_null() {
                let error_msg = unsafe {
                    std::ffi::CStr::from_ptr(result.error_message)
                        .to_string_lossy()
                        .to_string()
                };
                assert!(
                    error_msg.contains("Circuit breaker") || error_msg.contains("window"),
                    "Expected circuit breaker error, got: {}",
                    error_msg
                );
            }
        }
    }
    
    println!("✅ Circuit breaker successfully prevented DoS after 1000 transactions");
}

#[test]
fn test_bytecode_complexity_rejection() {
    let mut executor = create_test_executor();
    
    // Create bytecode with 200 SSTORE operations (way too many)
    let mut bytecode = Vec::new();
    for _ in 0..200 {
        bytecode.extend_from_slice(&[
            0x60, 0x01, // PUSH1 1
            0x60, 0x00, // PUSH1 0
            0x55,       // SSTORE
        ]);
    }
    
    let deployer = Address::ZERO;
    let gas_limit = 30_000_000;
    let value = U256::ZERO;
    let nonce = 0;
    
    let result = executor.deploy_contract(
        deployer,
        Bytes::from(bytecode),
        gas_limit,
        value,
        nonce,
    );
    
    assert_eq!(result.success, 0, "Deployment should have failed");
    
    if !result.error_message.is_null() {
        let error_msg = unsafe {
            std::ffi::CStr::from_ptr(result.error_message)
                .to_string_lossy()
                .to_string()
        };
        assert!(
            error_msg.contains("complexity") || error_msg.contains("storage operations"),
            "Expected complexity error, got: {}",
            error_msg
        );
        println!("✅ Rejected complex bytecode with error: {}", error_msg);
    }
}

#[test]
fn test_excessive_calldata_gas() {
    let mut executor = create_test_executor();
    
    let caller = Address::ZERO;
    let to = Address::from([1u8; 20]);
    
    // Create large calldata (1MB - maximum allowed)
    let large_data = vec![0xFFu8; 1_048_576];
    let data = Bytes::from(large_data);
    
    // Calldata gas: 1MB * 16 gas/byte = 16,777,216 gas
    // This should work with 30M gas limit
    let gas_limit = 30_000_000;
    let value = U256::ZERO;
    let nonce = 0;
    
    let result = executor.execute_call(caller, to, data.clone(), gas_limit, value, nonce);
    
    // Should succeed (we have enough gas)
    assert_eq!(result.success, 1, "Large calldata should succeed with sufficient gas");
    
    // Now try with insufficient gas
    let low_gas = 1_000_000; // Only 1M gas
    let nonce2 = 1;
    
    let result2 = executor.execute_call(caller, to, data, low_gas, value, nonce2);
    
    // Should fail (not enough gas for calldata)
    assert_eq!(result2.success, 0, "Should fail with insufficient gas");
    
    if !result2.error_message.is_null() {
        let error_msg = unsafe {
            std::ffi::CStr::from_ptr(result2.error_message)
                .to_string_lossy()
                .to_string()
        };
        println!("✅ Correctly rejected insufficient gas: {}", error_msg);
    }
}

#[test]
fn test_gas_refund_tracking() {
    let mut executor = create_test_executor();
    
    let caller = Address::ZERO;
    let to = Address::from([1u8; 20]);
    let data = Bytes::default();
    let gas_limit = 1_000_000;
    let value = U256::ZERO;
    let nonce = 0;
    
    let result = executor.execute_call(caller, to, data, gas_limit, value, nonce);
    
    // Gas used should be less than gas limit (simple call)
    assert!(result.gas_used < gas_limit, "Gas used should be less than limit");
    assert!(result.gas_used > 0, "Should have used some gas");
    
    println!(
        "✅ Gas tracking correct: used {}/{} ({}%)",
        result.gas_used,
        gas_limit,
        (result.gas_used as f64 / gas_limit as f64 * 100.0) as u64
    );
}

#[test]
fn test_circuit_breaker_window_reset() {
    let mut executor = create_test_executor();
    
    let caller = Address::ZERO;
    let to = Address::from([1u8; 20]);
    let data = Bytes::default();
    let gas_limit = 300_000;
    let value = U256::ZERO;
    
    // Fill up the first window
    for i in 0..1000 {
        let result = executor.execute_call(caller, to, data.clone(), gas_limit, value, i);
        assert_eq!(result.success, 1, "Transaction {} should succeed", i);
    }
    
    // Next should fail (window full)
    let result_fail = executor.execute_call(caller, to, data.clone(), gas_limit, value, 1000);
    assert_eq!(result_fail.success, 0, "Should fail when window is full");
    
    // Wait for window to reset (10 seconds)
    std::thread::sleep(std::time::Duration::from_secs(11));
    
    // Now should succeed again (new window)
    let result_success = executor.execute_call(caller, to, data, gas_limit, value, 1001);
    assert_eq!(result_success.success, 1, "Should succeed after window reset");
    
    println!("✅ Circuit breaker window reset working correctly");
}