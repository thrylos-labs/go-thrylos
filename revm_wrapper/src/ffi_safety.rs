// ffi_safety.rs
use std::panic::{self, UnwindSafe}; // ✅ IMPORT UnwindSafe TRAIT
use std::ffi::CString;
use std::os::raw::c_char;

/// FFI-safe error code
#[repr(C)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FFIErrorCode {
    Success = 0,
    PanicCaught = 1,
    InvalidInput = 2,
    ExecutionFailed = 3,
    OutOfGas = 4,
    Revert = 5,
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
        
        FFIResult {
            value: T::default(),
            error_code: code,
            error_message: c_message.into_raw(),
        }
    }
}

/// Safely execute Rust code and catch panics for FFI
pub fn ffi_safe_exec<F, T>(f: F) -> FFIResult<T>
where
    // ✅ CRITICAL FIX: You must add "+ UnwindSafe" here.
    // Since lib.rs passes 'AssertUnwindSafe<Closure>', that TYPE implements UnwindSafe.
    // This bound allows that type to be passed through to panic::catch_unwind.
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
#[no_mangle]
pub extern "C" fn revm_free_error_message(ptr: *mut c_char) {
    if !ptr.is_null() {
        unsafe {
            let _ = CString::from_raw(ptr);
        }
    }
}