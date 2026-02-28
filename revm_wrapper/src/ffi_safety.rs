// revm_wrapper/src/ffi_safety.rs
//
// FIND-07: Hardened FFI boundary.
//
// Changes from original:
//   1. CStr::from_ptr() calls now live only inside dedicated safe wrappers
//      with explicit lifetime and null-check documentation.
//   2. FFIResult<T> documents exact ownership rules for error_message.
//   3. revm_free_error_message enforces that only Rust frees Rust memory
//      (Go must never call free() on these pointers directly).
//   4. Added revm_free_return_data for symmetric return-data deallocation.

use crate::memory_tracker::get_memory_tracker;
#[cfg(test)]
use std::ffi::CStr;
use std::ffi::CString;
use std::os::raw::c_char;
use std::panic::{self, UnwindSafe};

/// FFI-safe error code returned in every FFIResult.
#[repr(C)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FFIErrorCode {
    Success = 0,
    PanicCaught = 1,
    InvalidInput = 2,
    ExecutionFailed = 3,
    #[allow(dead_code)]
    OutOfGas = 4,
    Revert = 5,
    #[allow(dead_code)]
    MemoryError = 6,
}

/// FFI-safe result wrapper.
///
/// # Ownership contract for `error_message`
///
/// - When `error_code == Success`, `error_message` is **null**. Do not free it.
/// - When `error_code != Success`, `error_message` points to a
///   **Rust-allocated** `CString`. The Go caller **must** free it by calling
///   `revm_free_error_message(result.error_message)` exactly once.
/// - Go must **never** call `C.free()` on this pointer. Only
///   `revm_free_error_message` is safe to call.
/// - After calling `revm_free_error_message`, the pointer is invalid;
///   do not read from or pass it again.
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
        // Replace interior nuls to prevent CString::new from panicking on
        // adversarial input (e.g. a revert reason that contains a nul byte).
        let safe_message = message.replace('\0', "<nul>");

        let c_message = CString::new(safe_message)
            .unwrap_or_else(|_| CString::new("Invalid error message").unwrap());

        let ptr = c_message.into_raw();

        // Register the allocation so revm_free_error_message can verify it
        // and prevent double-free / invalid-free.
        get_memory_tracker().track_error_message(ptr);

        FFIResult {
            value: T::default(),
            error_code: code,
            error_message: ptr,
        }
    }
}

/// Safely execute Rust code across an FFI boundary, catching panics.
///
/// All `extern "C"` entry points should wrap their logic in this function.
/// Panics are caught and converted to `FFIErrorCode::PanicCaught`; they are
/// never allowed to unwind across the FFI boundary (which is undefined
/// behaviour in C/Go callers).
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
            eprintln!(
                "🚨 CRITICAL: Rust panic caught at FFI boundary: {}",
                panic_msg
            );
            FFIResult::error(FFIErrorCode::PanicCaught, &panic_msg)
        }
    }
}

// ── String helpers for use inside Rust (not exported to C) ───────────────────

/// Safely borrow a C string pointer as a Rust `&str`.
///
/// # Safety contract
///
/// - `ptr` must be non-null.
/// - `ptr` must point to a valid, nul-terminated UTF-8 (or ASCII) string.
/// - The returned `&str` lifetime is tied to the pointed-to memory; the caller
///   must not free `ptr` while the `&str` is live.
/// - This function **does not take ownership** of `ptr`.
///
/// Returns `None` if `ptr` is null or contains invalid UTF-8.
///
/// # FIND-07 note
/// Use this wrapper instead of calling `CStr::from_ptr()` directly. It
/// centralises null-checking and documents the lifetime requirement explicitly.
#[cfg(test)]
pub(crate) unsafe fn c_str_to_str<'a>(ptr: *const c_char) -> Option<&'a str> {
    if ptr.is_null() {
        return None;
    }
    // SAFETY: caller guarantees ptr is non-null and nul-terminated.
    CStr::from_ptr(ptr).to_str().ok()
}

// ── Deallocation entry points (called from Go via CGo) ───────────────────────

/// Free an error message string previously returned inside an `FFIResult`.
///
/// # Rules
/// - Call exactly **once** per non-null `error_message`.
/// - Do **not** call on null pointers (no-op but logged).
/// - Do **not** call `C.free()` — only this function may free these pointers.
/// - After this call the pointer is invalid; do not use it again.
///
/// Double-free and invalid-free attempts are detected by the memory tracker
/// and logged without crashing; they do not trigger UB.
#[no_mangle]
pub extern "C" fn revm_free_error_message(ptr: *mut c_char) {
    if ptr.is_null() {
        return;
    }
    if !get_memory_tracker().untrack_error_message(ptr) {
        eprintln!(
            "⚠️  revm_free_error_message: prevented double-free or invalid free at {:p}",
            ptr
        );
        return;
    }
    // SAFETY: ptr was allocated by CString::into_raw() and is now being
    // reclaimed. The memory tracker ensures this path runs at most once.
    unsafe {
        let _ = CString::from_raw(ptr);
    }
}

/// Free a return-data buffer previously allocated by the EVM executor.
///
/// Symmetric to `revm_free_error_message`. Go callers must call this once
/// on any non-null return_data pointer from an FFIResult before discarding it.
#[no_mangle]
pub extern "C" fn revm_free_return_data(ptr: *mut u8, len: usize) {
    if ptr.is_null() || len == 0 {
        return;
    }
    if !get_memory_tracker().untrack_return_data(ptr) {
        eprintln!(
            "⚠️  revm_free_return_data: prevented double-free or invalid free at {:p}",
            ptr
        );
        return;
    }
    // SAFETY: ptr was allocated by Vec::into_raw_parts() or Box::into_raw()
    // in the executor and is now being reclaimed.
    unsafe {
        let _ = Vec::from_raw_parts(ptr, len, len);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_success_result_has_null_error_message() {
        let result: FFIResult<i32> = FFIResult::success(42);
        assert_eq!(result.error_code, FFIErrorCode::Success);
        assert!(result.error_message.is_null());
    }

    #[test]
    fn test_error_result_is_tracked_and_freeable() {
        let result: FFIResult<i32> = FFIResult::error(FFIErrorCode::ExecutionFailed, "test error");
        assert_eq!(result.error_code, FFIErrorCode::ExecutionFailed);
        assert!(!result.error_message.is_null());
        // Free it — should not panic or double-free
        revm_free_error_message(result.error_message);
    }

    #[test]
    fn test_double_free_is_prevented() {
        let result: FFIResult<i32> =
            FFIResult::error(FFIErrorCode::PanicCaught, "double free test");
        let ptr = result.error_message;
        revm_free_error_message(ptr);
        // Second call must not cause UB — memory tracker blocks it
        revm_free_error_message(ptr);
    }

    #[test]
    fn test_null_free_is_safe() {
        revm_free_error_message(std::ptr::null_mut());
    }

    #[test]
    fn test_nul_byte_in_error_message_does_not_panic() {
        let result: FFIResult<i32> =
            FFIResult::error(FFIErrorCode::ExecutionFailed, "error\0with nul");
        assert!(!result.error_message.is_null());
        revm_free_error_message(result.error_message);
    }

    #[test]
    fn test_ffi_safe_exec_catches_panic() {
        let result: FFIResult<i32> = ffi_safe_exec(|| panic!("test panic"));
        assert_eq!(result.error_code, FFIErrorCode::PanicCaught);
        revm_free_error_message(result.error_message);
    }

    #[test]
    fn test_c_str_to_str_null_returns_none() {
        let result = unsafe { c_str_to_str(std::ptr::null()) };
        assert!(result.is_none());
    }
}
