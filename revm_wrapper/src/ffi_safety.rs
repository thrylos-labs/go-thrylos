// thrylos-revm/src/ffi_safety.rs
use std::panic::{self, UnwindSafe};
use std::ffi::CString;
use std::os::raw::c_char;
use crate::memory_tracker::get_memory_tracker;

/// FFI-safe error code
#[repr(C)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FFIErrorCode {
    Success = 0,
    PanicCaught = 1,
    InvalidInput = 2,
    ExecutionFailed = 3,
    #[allow(dead_code)]  // ✅ Reserved for future use
    OutOfGas = 4,
    Revert = 5,
    #[allow(dead_code)]  // ✅ Reserved for future use
    MemoryError = 6,
}

/// FFI-safe result wrapper
#[repr(C)]
pub struct FFIResult<T> {
    pub value: T,
    pub error_code: FFIErrorCode,
    pub error_message: *mut c_char,
}

impl<T: Default> FFIResult<T> {
    pub fn success(value: T) -> Self {
        FFIResult {
            value,
            error_code: FFIErrorCode::Success,
            error_message: std::ptr::null_mut(),
        }
    }

    pub fn error(code: FFIErrorCode, message: &str) -> Self {
        let c_message = CString::new(message)
            .unwrap_or_else(|_| CString::new("Invalid error message").unwrap());
        
        let ptr = c_message.into_raw();
        
        // ✅ Track the allocation
        get_memory_tracker().track_error_message(ptr);
        
        FFIResult {
            value: T::default(),
            error_code: code,
            error_message: ptr,
        }
    }
}

/// Safely execute Rust code and catch panics for FFI
pub fn ffi_safe_exec<F, T>(f: F) -> FFIResult<T>
where
    F: FnOnce() -> Result<T, String> + UnwindSafe, 
    T: Default,
{
    match panic::catch_unwind(f) {
        Ok(Ok(value)) => FFIResult::success(value),
        Ok(Err(err_msg)) => {
            eprintln!("🔴 FFI Error: {}", err_msg);
            FFIResult::error(FFIErrorCode::ExecutionFailed, &err_msg)
        }
        Err(panic_info) => {
            let panic_msg = if let Some(s) = panic_info.downcast_ref::<&str>() {
                s.to_string()
            } else if let Some(s) = panic_info.downcast_ref::<String>() {
                s.clone()
            } else {
                "Unknown panic".to_string()
            };

            eprintln!("🚨 CRITICAL: Rust panic caught in FFI: {}", panic_msg);
            FFIResult::error(FFIErrorCode::PanicCaught, &panic_msg)
        }
    }
}

/// Free error message allocated by Rust
/// This function now includes double-free protection via memory tracking
#[no_mangle]
pub extern "C" fn revm_free_error_message(ptr: *mut c_char) {
    if ptr.is_null() {
        return;
    }
    
    // ✅ Check if pointer is tracked (prevents double-free)
    if !get_memory_tracker().untrack_error_message(ptr) {
        eprintln!("⚠️ revm_free_error_message: Prevented double-free or invalid free at {:p}", ptr);
        return;
    }
    
    // Free the actual memory
    unsafe {
        let _ = CString::from_raw(ptr);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    
    #[test]
    fn test_ffi_result_success() {
        let result = FFIResult::success(42);
        assert_eq!(result.value, 42);
        assert_eq!(result.error_code, FFIErrorCode::Success);
        assert!(result.error_message.is_null());
    }
    
    #[test]
    fn test_ffi_result_error_tracking() {
        let tracker = get_memory_tracker();
        let initial_count = tracker.get_error_message_count();
        
        let result = FFIResult::<i32>::error(FFIErrorCode::ExecutionFailed, "Test error");
        
        // Should have tracked the allocation
        assert_eq!(tracker.get_error_message_count(), initial_count + 1);
        assert!(!result.error_message.is_null());
        
        // Free it
        revm_free_error_message(result.error_message);
        
        // Should have untracked
        assert_eq!(tracker.get_error_message_count(), initial_count);
    }
    
    #[test]
    fn test_double_free_protection() {
        let result = FFIResult::<i32>::error(FFIErrorCode::ExecutionFailed, "Test error");
        let ptr = result.error_message;
        
        // First free should succeed
        revm_free_error_message(ptr);
        
        // Second free should be prevented
        revm_free_error_message(ptr);
        
        // No crash or undefined behavior should occur
    }
    
    #[test]
    fn test_ffi_safe_exec_success() {
        let result = ffi_safe_exec(|| Ok(42));
        assert_eq!(result.value, 42);
        assert_eq!(result.error_code, FFIErrorCode::Success);
        assert!(result.error_message.is_null());
    }
    
    #[test]
    fn test_ffi_safe_exec_error() {
        let result = ffi_safe_exec(|| Err("Test error".to_string()));
        assert_eq!(result.error_code, FFIErrorCode::ExecutionFailed);
        assert!(!result.error_message.is_null());
        
        // Clean up
        revm_free_error_message(result.error_message);
    }
    
    #[test]
    fn test_ffi_safe_exec_panic() {
        let result = ffi_safe_exec(|| {
            panic!("Test panic");
            #[allow(unreachable_code)]
            Ok(42)
        });
        
        assert_eq!(result.error_code, FFIErrorCode::PanicCaught);
        assert!(!result.error_message.is_null());
        
        // Clean up
        revm_free_error_message(result.error_message);
    }
}