// ============================================================================
// RUST TESTS - Add to thrylos-revm/tests/memory_tracking_test.rs
// ============================================================================

#[cfg(test)]
mod memory_tracking_integration_tests {
    use super::*;
    use std::ffi::CString;
    
    // Helper to create a test executor
    fn create_test_executor() -> *mut EVMExecutor {
        extern "C" fn mock_balance(_addr: CAddress) -> CU256 {
            CU256 { bytes: [0u8; 32] }
        }
        
        extern "C" fn mock_nonce(_addr: CAddress) -> u64 {
            0
        }
        
        extern "C" fn mock_code(_addr: CAddress) -> CByteSlice {
            CByteSlice { data: std::ptr::null(), len: 0 }
        }
        
        extern "C" fn mock_storage(_addr: CAddress, _key: CU256) -> CU256 {
            CU256 { bytes: [0u8; 32] }
        }
        
        unsafe {
            revm_executor_new(1, mock_balance, mock_nonce, mock_code, mock_storage)
        }
    }
    
    #[test]
    fn test_no_memory_leaks_on_successful_execution() {
        let executor = create_test_executor();
        assert!(!executor.is_null());
        
        let tracker = get_memory_tracker();
        let initial_leaks = tracker.get_leak_count();
        
        // Execute a simple call
        let caller = CAddress { bytes: [1u8; 20] };
        let to = CAddress { bytes: [2u8; 20] };
        let data = CByteSlice { data: std::ptr::null(), len: 0 };
        let value = CU256 { bytes: [0u8; 32] };
        
        let result = revm_execute_call(executor, caller, to, data, 1000000, value, 0);
        
        // Free the result
        revm_free_result(result);
        
        // Check for leaks
        let final_leaks = tracker.get_leak_count();
        assert_eq!(
            initial_leaks, final_leaks,
            "Memory leaked after successful execution"
        );
        
        // Cleanup
        revm_executor_free(executor);
    }
    
    #[test]
    fn test_no_memory_leaks_on_error() {
        let executor = create_test_executor();
        assert!(!executor.is_null());
        
        let tracker = get_memory_tracker();
        let initial_leaks = tracker.get_leak_count();
        
        // Execute with invalid parameters to trigger an error
        let caller = CAddress { bytes: [1u8; 20] };
        let to = CAddress { bytes: [2u8; 20] };
        let data = CByteSlice { data: std::ptr::null(), len: 0 };
        let value = CU256 { bytes: [0u8; 32] };
        
        // Use gas limit of 0 to trigger error
        let result = revm_execute_call(executor, caller, to, data, 0, value, 0);
        
        // Should have an error
        assert_eq!(result.success, 0);
        
        // Free the result
        revm_free_result(result);
        
        // Check for leaks
        let final_leaks = tracker.get_leak_count();
        assert_eq!(
            initial_leaks, final_leaks,
            "Memory leaked after error execution"
        );
        
        // Cleanup
        revm_executor_free(executor);
    }
    
    #[test]
    fn test_double_free_protection() {
        let tracker = get_memory_tracker();
        
        // Create an error message
        let msg = CString::new("Test error").unwrap();
        let ptr = msg.into_raw();
        tracker.track_error_message(ptr);
        
        // First free should succeed
        revm_free_error_message(ptr);
        
        // Second free should be prevented (no crash)
        revm_free_error_message(ptr);
        
        // Should have detected the invalid free attempt
        let stats = tracker.get_stats();
        assert!(stats.invalid_free_attempts > 0 || stats.double_free_attempts > 0);
    }
    
    #[test]
    fn test_stress_no_leaks() {
        let executor = create_test_executor();
        assert!(!executor.is_null());
        
        let tracker = get_memory_tracker();
        let initial_leaks = tracker.get_leak_count();
        
        // Execute many calls
        for i in 0..1000 {
            let caller = CAddress { bytes: [1u8; 20] };
            let to = CAddress { bytes: [2u8; 20] };
            let data = CByteSlice { data: std::ptr::null(), len: 0 };
            let value = CU256 { bytes: [0u8; 32] };
            
            let result = revm_execute_call(executor, caller, to, data, 1000000, value, i);
            revm_free_result(result);
        }
        
        // Check for leaks
        let final_leaks = tracker.get_leak_count();
        assert_eq!(
            initial_leaks, final_leaks,
            "Memory leaked during stress test"
        );
        
        // Cleanup
        revm_executor_free(executor);
    }
    
    #[test]
    fn test_memory_stats_accuracy() {
        let tracker = get_memory_tracker();
        let initial_stats = tracker.get_stats();
        
        // Allocate some error messages
        let msg1 = CString::new("Error 1").unwrap().into_raw();
        let msg2 = CString::new("Error 2").unwrap().into_raw();
        
        tracker.track_error_message(msg1);
        tracker.track_error_message(msg2);
        
        let after_alloc_stats = tracker.get_stats();
        assert_eq!(
            after_alloc_stats.current_error_messages,
            initial_stats.current_error_messages + 2
        );
        
        // Free one
        revm_free_error_message(msg1);
        
        let after_free_stats = tracker.get_stats();
        assert_eq!(
            after_free_stats.current_error_messages,
            initial_stats.current_error_messages + 1
        );
        
        // Free the other
        revm_free_error_message(msg2);
        
        let final_stats = tracker.get_stats();
        assert_eq!(
            final_stats.current_error_messages,
            initial_stats.current_error_messages
        );
    }
}
