// thrylos-revm/src/memory_tracker.rs
//
// Comprehensive memory tracking system for FFI allocations
// Detects memory leaks and prevents double-frees

use parking_lot::Mutex;
use std::collections::HashSet;
use std::os::raw::c_char;
use std::sync::atomic::{AtomicUsize, Ordering};

/// Global memory tracker for FFI allocations
pub struct MemoryTracker {
    // Track allocated pointers by type
    allocated_error_messages: Mutex<HashSet<usize>>,
    allocated_return_data: Mutex<HashSet<usize>>,

    // Counters
    total_allocated: AtomicUsize,
    total_freed: AtomicUsize,
    error_message_count: AtomicUsize,
    return_data_count: AtomicUsize,

    // Leak detection
    double_free_attempts: AtomicUsize,
    invalid_free_attempts: AtomicUsize,
}

impl MemoryTracker {
    /// Create a new memory tracker
    pub fn new() -> Self {
        Self {
            allocated_error_messages: Mutex::new(HashSet::new()),
            allocated_return_data: Mutex::new(HashSet::new()),
            total_allocated: AtomicUsize::new(0),
            total_freed: AtomicUsize::new(0),
            error_message_count: AtomicUsize::new(0),
            return_data_count: AtomicUsize::new(0),
            double_free_attempts: AtomicUsize::new(0),
            invalid_free_attempts: AtomicUsize::new(0),
        }
    }

    /// Track an error message allocation
    pub fn track_error_message(&self, ptr: *mut c_char) {
        if ptr.is_null() {
            return;
        }

        let addr = ptr as usize;
        let mut set = self.allocated_error_messages.lock();

        if set.contains(&addr) {
            eprintln!(
                "⚠️ WARNING: Error message at {:p} already tracked (possible duplicate allocation)",
                ptr
            );
        }

        set.insert(addr);
        self.total_allocated.fetch_add(1, Ordering::Relaxed);
        self.error_message_count.fetch_add(1, Ordering::Relaxed);

        #[cfg(debug_assertions)]
        eprintln!("✅ Tracked error message at {:p}", ptr);
    }

    /// Track a return data allocation
    pub fn track_return_data(&self, ptr: *const u8, len: usize) {
        if ptr.is_null() || len == 0 {
            return;
        }

        let addr = ptr as usize;
        let mut set = self.allocated_return_data.lock();

        if set.contains(&addr) {
            eprintln!(
                "⚠️ WARNING: Return data at {:p} already tracked (possible duplicate allocation)",
                ptr
            );
        }

        set.insert(addr);
        self.total_allocated.fetch_add(1, Ordering::Relaxed);
        self.return_data_count.fetch_add(1, Ordering::Relaxed);

        #[cfg(debug_assertions)]
        eprintln!("✅ Tracked return data at {:p} (len: {})", ptr, len);
    }

    /// Untrack an error message (called when freeing)
    /// Returns true if the pointer was tracked, false otherwise
    pub fn untrack_error_message(&self, ptr: *mut c_char) -> bool {
        if ptr.is_null() {
            return false;
        }

        let addr = ptr as usize;
        let mut set = self.allocated_error_messages.lock();

        if !set.contains(&addr) {
            // Pointer not tracked - either already freed or never allocated by us
            self.invalid_free_attempts.fetch_add(1, Ordering::Relaxed);
            eprintln!(
                "⚠️ WARNING: Attempting to free untracked error message at {:p}",
                ptr
            );
            return false;
        }

        let removed = set.remove(&addr);
        if removed {
            self.total_freed.fetch_add(1, Ordering::Relaxed);

            #[cfg(debug_assertions)]
            eprintln!("✅ Untracked error message at {:p}", ptr);
        } else {
            // This shouldn't happen since we just checked contains()
            self.double_free_attempts.fetch_add(1, Ordering::Relaxed);
            eprintln!(
                "⚠️ WARNING: Double-free detected for error message at {:p}",
                ptr
            );
        }

        removed
    }

    /// Untrack return data (called when freeing)
    /// Returns true if the pointer was tracked, false otherwise
    pub fn untrack_return_data(&self, ptr: *const u8) -> bool {
        if ptr.is_null() {
            return false;
        }

        let addr = ptr as usize;
        let mut set = self.allocated_return_data.lock();

        if !set.contains(&addr) {
            // Pointer not tracked - either already freed or never allocated by us
            self.invalid_free_attempts.fetch_add(1, Ordering::Relaxed);
            eprintln!(
                "⚠️ WARNING: Attempting to free untracked return data at {:p}",
                ptr
            );
            return false;
        }

        let removed = set.remove(&addr);
        if removed {
            self.total_freed.fetch_add(1, Ordering::Relaxed);

            #[cfg(debug_assertions)]
            eprintln!("✅ Untracked return data at {:p}", ptr);
        } else {
            // This shouldn't happen since we just checked contains()
            self.double_free_attempts.fetch_add(1, Ordering::Relaxed);
            eprintln!(
                "⚠️ WARNING: Double-free detected for return data at {:p}",
                ptr
            );
        }

        removed
    }

    /// Get the number of potential memory leaks
    pub fn get_leak_count(&self) -> usize {
        let allocated = self.total_allocated.load(Ordering::Relaxed);
        let freed = self.total_freed.load(Ordering::Relaxed);
        allocated.saturating_sub(freed)
    }

    /// Get the number of currently tracked error messages
    pub fn get_error_message_count(&self) -> usize {
        self.allocated_error_messages.lock().len()
    }

    /// Get the number of currently tracked return data allocations
    pub fn get_return_data_count(&self) -> usize {
        self.allocated_return_data.lock().len()
    }

    /// Get statistics about memory tracking
    pub fn get_stats(&self) -> MemoryStats {
        MemoryStats {
            total_allocated: self.total_allocated.load(Ordering::Relaxed),
            total_freed: self.total_freed.load(Ordering::Relaxed),
            error_messages_allocated: self.error_message_count.load(Ordering::Relaxed),
            return_data_allocated: self.return_data_count.load(Ordering::Relaxed),
            current_error_messages: self.get_error_message_count(),
            current_return_data: self.get_return_data_count(),
            double_free_attempts: self.double_free_attempts.load(Ordering::Relaxed),
            invalid_free_attempts: self.invalid_free_attempts.load(Ordering::Relaxed),
            potential_leaks: self.get_leak_count(),
        }
    }

    /// Print a detailed memory report
    pub fn report(&self) {
        let stats = self.get_stats();

        eprintln!("╔════════════════════════════════════════════════════════════════╗");
        eprintln!("║          REVM FFI Memory Tracker Report                       ║");
        eprintln!("╠════════════════════════════════════════════════════════════════╣");
        eprintln!(
            "║ Total Allocations:          {:>10}                      ║",
            stats.total_allocated
        );
        eprintln!(
            "║ Total Frees:                {:>10}                      ║",
            stats.total_freed
        );
        eprintln!(
            "║ Potential Leaks:            {:>10}                      ║",
            stats.potential_leaks
        );
        eprintln!("╠════════════════════════════════════════════════════════════════╣");
        eprintln!(
            "║ Error Messages Allocated:   {:>10}                      ║",
            stats.error_messages_allocated
        );
        eprintln!(
            "║ Current Error Messages:     {:>10}                      ║",
            stats.current_error_messages
        );
        eprintln!("╠════════════════════════════════════════════════════════════════╣");
        eprintln!(
            "║ Return Data Allocated:      {:>10}                      ║",
            stats.return_data_allocated
        );
        eprintln!(
            "║ Current Return Data:        {:>10}                      ║",
            stats.current_return_data
        );
        eprintln!("╠════════════════════════════════════════════════════════════════╣");
        eprintln!(
            "║ Double-Free Attempts:       {:>10}                      ║",
            stats.double_free_attempts
        );
        eprintln!(
            "║ Invalid Free Attempts:      {:>10}                      ║",
            stats.invalid_free_attempts
        );
        eprintln!("╚════════════════════════════════════════════════════════════════╝");

        if stats.potential_leaks > 0 {
            eprintln!(
                "⚠️  WARNING: {} potential memory leak(s) detected!",
                stats.potential_leaks
            );
        } else {
            eprintln!("✅ No memory leaks detected");
        }

        if stats.double_free_attempts > 0 {
            eprintln!(
                "⚠️  WARNING: {} double-free attempt(s) prevented!",
                stats.double_free_attempts
            );
        }

        if stats.invalid_free_attempts > 0 {
            eprintln!(
                "⚠️  WARNING: {} invalid free attempt(s) detected!",
                stats.invalid_free_attempts
            );
        }
    }

    /// Reset all counters (useful for testing)
    #[cfg(test)]
    #[allow(dead_code)]
    pub fn reset(&self) {
        self.allocated_error_messages.lock().clear();
        self.allocated_return_data.lock().clear();
        self.total_allocated.store(0, Ordering::Relaxed);
        self.total_freed.store(0, Ordering::Relaxed);
        self.error_message_count.store(0, Ordering::Relaxed);
        self.return_data_count.store(0, Ordering::Relaxed);
        self.double_free_attempts.store(0, Ordering::Relaxed);
        self.invalid_free_attempts.store(0, Ordering::Relaxed);
    }
}

/// Statistics about memory tracking
#[derive(Debug, Clone, Copy)]
pub struct MemoryStats {
    pub total_allocated: usize,
    pub total_freed: usize,
    pub error_messages_allocated: usize,
    pub return_data_allocated: usize,
    pub current_error_messages: usize,
    pub current_return_data: usize,
    pub double_free_attempts: usize,
    pub invalid_free_attempts: usize,
    pub potential_leaks: usize,
}

// Global memory tracker instance
static MEMORY_TRACKER: OnceLock<MemoryTracker> = OnceLock::new();

use std::sync::OnceLock;

pub fn get_memory_tracker() -> &'static MemoryTracker {
    MEMORY_TRACKER.get_or_init(|| MemoryTracker::new())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::ffi::CString;

    #[test]
    fn test_error_message_tracking() {
        let tracker = MemoryTracker::new();

        // Create a test error message
        let msg = CString::new("Test error").unwrap();
        let ptr = msg.into_raw();

        // Track it
        tracker.track_error_message(ptr);
        assert_eq!(tracker.get_error_message_count(), 1);
        assert_eq!(tracker.get_leak_count(), 1);

        // Untrack it
        assert!(tracker.untrack_error_message(ptr));
        assert_eq!(tracker.get_error_message_count(), 0);
        assert_eq!(tracker.get_leak_count(), 0);

        // Free the actual memory
        unsafe {
            let _ = CString::from_raw(ptr);
        }
    }

    #[test]
    fn test_double_free_detection() {
        let tracker = MemoryTracker::new();

        let msg = CString::new("Test error").unwrap();
        let ptr = msg.into_raw();

        tracker.track_error_message(ptr);

        // First untrack should succeed
        assert!(tracker.untrack_error_message(ptr));

        // Second untrack should fail (double-free detected)
        assert!(!tracker.untrack_error_message(ptr));
        assert_eq!(tracker.double_free_attempts.load(Ordering::Relaxed), 0);
        assert_eq!(tracker.invalid_free_attempts.load(Ordering::Relaxed), 1);

        // Free the actual memory
        unsafe {
            let _ = CString::from_raw(ptr);
        }
    }

    #[test]
    fn test_return_data_tracking() {
        let tracker = MemoryTracker::new();

        let data = vec![1u8, 2, 3, 4, 5];
        let ptr = data.as_ptr();
        let len = data.len();

        tracker.track_return_data(ptr, len);
        assert_eq!(tracker.get_return_data_count(), 1);
        assert_eq!(tracker.get_leak_count(), 1);

        assert!(tracker.untrack_return_data(ptr));
        assert_eq!(tracker.get_return_data_count(), 0);
        assert_eq!(tracker.get_leak_count(), 0);
    }

    #[test]
    fn test_stats() {
        let tracker = MemoryTracker::new();

        // Allocate some error messages
        let msg1 = CString::new("Error 1").unwrap().into_raw();
        let msg2 = CString::new("Error 2").unwrap().into_raw();

        tracker.track_error_message(msg1);
        tracker.track_error_message(msg2);

        // Allocate some return data
        let data = vec![1u8; 100];
        tracker.track_return_data(data.as_ptr(), data.len());

        let stats = tracker.get_stats();
        assert_eq!(stats.total_allocated, 3);
        assert_eq!(stats.total_freed, 0);
        assert_eq!(stats.potential_leaks, 3);
        assert_eq!(stats.current_error_messages, 2);
        assert_eq!(stats.current_return_data, 1);

        // Free one error message
        tracker.untrack_error_message(msg1);
        let stats = tracker.get_stats();
        assert_eq!(stats.total_freed, 1);
        assert_eq!(stats.potential_leaks, 2);

        // Cleanup
        unsafe {
            let _ = CString::from_raw(msg1);
            let _ = CString::from_raw(msg2);
        }
    }
}
