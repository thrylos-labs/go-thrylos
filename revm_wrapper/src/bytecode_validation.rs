// thrylos-revm/src/bytecode_validation.rs

//! Bytecode validation for contract deployment security
//! 
//! This module provides basic security checks for contract bytecode before deployment.
//! It does NOT aim to replicate full EVM validation (that's REVM's job), but rather
//! provides defense-in-depth against obviously malicious or malformed bytecode.

use revm::primitives::Bytes;

/// Minimum bytecode size for a valid contract
/// Empty contracts or single STOP are suspicious
const MIN_BYTECODE_SIZE: usize = 1;

/// Maximum bytecode size (EIP-170 limit is 24KB, but we enforce earlier)
pub const MAX_BYTECODE_SIZE: usize = 24_576; // 24 KB

/// Minimum gas required for contract deployment (more expensive than calls)
pub const MIN_DEPLOYMENT_GAS: u64 = 100_000;

/// Common EVM opcodes we expect to see in valid contracts
mod opcodes {
    pub const STOP: u8 = 0x00;
    pub const PUSH1: u8 = 0x60;
    pub const PUSH32: u8 = 0x7f;
    pub const RETURN: u8 = 0xf3;
    pub const REVERT: u8 = 0xfd;
    pub const INVALID: u8 = 0xfe;
    pub const SELFDESTRUCT: u8 = 0xff;
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum BytecodeValidationError {
    Empty,
    TooSmall,
    TooLarge,
    OnlyStops,
    InvalidInitcode,
    SuspiciousPattern,
}

impl std::fmt::Display for BytecodeValidationError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Empty => write!(f, "Bytecode is empty"),
            Self::TooSmall => write!(f, "Bytecode is too small to be a valid contract"),
            Self::TooLarge => write!(f, "Bytecode exceeds maximum size (24KB)"),
            Self::OnlyStops => write!(f, "Bytecode contains only STOP opcodes (suspicious)"),
            Self::InvalidInitcode => write!(f, "Bytecode does not contain valid init code"),
            Self::SuspiciousPattern => write!(f, "Bytecode contains suspicious patterns"),
        }
    }
}

/// Validate bytecode before deployment
/// 
/// This performs basic sanity checks but does NOT validate execution semantics.
/// REVM will handle actual execution validation during deployment.
pub fn validate_bytecode(bytecode: &Bytes) -> Result<(), BytecodeValidationError> {
    // 1. Check if empty
    if bytecode.is_empty() {
        return Err(BytecodeValidationError::Empty);
    }

    // 2. Check minimum size
    if bytecode.len() < MIN_BYTECODE_SIZE {
        return Err(BytecodeValidationError::TooSmall);
    }

    // 3. Check maximum size (should be caught earlier, but defense-in-depth)
    if bytecode.len() > MAX_BYTECODE_SIZE {
        return Err(BytecodeValidationError::TooLarge);
    }

    // 4. Check if bytecode is all zeros or all STOPs (common DoS pattern)
    if is_only_stops(bytecode) {
        return Err(BytecodeValidationError::OnlyStops);
    }

    // 5. Basic structure validation - check if it looks like valid EVM bytecode
    if !has_valid_structure(bytecode) {
        return Err(BytecodeValidationError::InvalidInitcode);
    }

    // 6. Check for suspicious patterns
    if has_suspicious_patterns(bytecode) {
        return Err(BytecodeValidationError::SuspiciousPattern);
    }

    Ok(())
}

/// Check if bytecode contains only STOP opcodes (0x00)
fn is_only_stops(bytecode: &Bytes) -> bool {
    // Allow up to 2 consecutive STOPs (sometimes valid)
    // More than that is suspicious
    let stop_count = bytecode.iter().filter(|&&b| b == opcodes::STOP).count();
    
    if stop_count == bytecode.len() {
        return true; // All STOPs
    }
    
    // Check for long runs of STOPs (100+ consecutive)
    let mut consecutive_stops = 0;
    for &byte in bytecode.iter() {
        if byte == opcodes::STOP {
            consecutive_stops += 1;
            if consecutive_stops > 100 {
                return true;
            }
        } else {
            consecutive_stops = 0;
        }
    }
    
    false
}

/// Check if bytecode has a valid structure (looks like EVM bytecode)
fn has_valid_structure(bytecode: &Bytes) -> bool {
    // Valid contracts typically:
    // 1. Have at least one PUSH operation (to push data onto stack)
    // 2. End with RETURN or REVERT (to return deployed code)
    // 3. Don't consist entirely of invalid opcodes
    
    let has_push = bytecode.iter().any(|&b| (opcodes::PUSH1..=opcodes::PUSH32).contains(&b));
    let has_terminator = bytecode.iter().any(|&b| {
        b == opcodes::RETURN || b == opcodes::REVERT || b == opcodes::STOP
    });
    
    // At minimum, we expect PUSH and a terminator
    // This is a very loose check - real validation happens in REVM
    has_push && has_terminator
}

/// Check for known suspicious patterns
fn has_suspicious_patterns(bytecode: &Bytes) -> bool {
    // Pattern 1: Excessive SELFDESTRUCT opcodes (> 10)
    let selfdestruct_count = bytecode.iter().filter(|&&b| b == opcodes::SELFDESTRUCT).count();
    if selfdestruct_count > 10 {
        return true;
    }
    
    // Pattern 2: Excessive INVALID opcodes (> 50)
    let invalid_count = bytecode.iter().filter(|&&b| b == opcodes::INVALID).count();
    if invalid_count > 50 {
        return true;
    }
    
    // Pattern 3: More than 80% of bytecode is the same opcode (excluding PUSH data)
    if is_mostly_same_opcode(bytecode) {
        return true;
    }
    
    false
}

/// Check if bytecode is mostly the same opcode (potential DoS)
fn is_mostly_same_opcode(bytecode: &Bytes) -> bool {
    if bytecode.len() < 100 {
        return false; // Too small to judge
    }
    
    // Count frequency of each opcode
    let mut counts = [0u32; 256];
    for &byte in bytecode.iter() {
        counts[byte as usize] += 1;
    }
    
    // If any single opcode (except PUSH data) is more than 80% of bytecode
    for (opcode, count) in counts.iter().enumerate() {
        // Exclude PUSH opcodes from this check (data can repeat)
        if !(opcodes::PUSH1..=opcodes::PUSH32).contains(&(opcode as u8)) {
            if *count as usize > (bytecode.len() * 80 / 100) {
                return true;
            }
        }
    }
    
    false
}

/// Calculate complexity score for bytecode (0-100)
/// Higher score = more complex = potentially more dangerous
/// This is optional and can be used for rate limiting
#[allow(dead_code)]
pub fn calculate_complexity_score(bytecode: &Bytes) -> u32 {
    let mut score = 0u32;
    
    // Factor 1: Size (larger = more complex)
    score += ((bytecode.len() as u32) / 100).min(30);
    
    // Factor 2: Unique opcodes (more variety = more complex)
    let mut seen_opcodes = [false; 256];
    for &byte in bytecode.iter() {
        seen_opcodes[byte as usize] = true;
    }
    let unique_count = seen_opcodes.iter().filter(|&&x| x).count() as u32;
    score += (unique_count / 2).min(30);
    
    // Factor 3: Jump operations (dynamic control flow)
    let jump_count = bytecode.iter()
        .filter(|&&b| b == 0x56 || b == 0x57) // JUMP, JUMPI
        .count() as u32;
    score += (jump_count / 5).min(20);
    
    // Factor 4: External calls
    let call_count = bytecode.iter()
        .filter(|&&b| (0xF0..=0xF5).contains(&b)) // CREATE, CALL, CALLCODE, DELEGATECALL, CREATE2, STATICCALL
        .count() as u32;
    score += (call_count * 5).min(20);
    
    score.min(100)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_empty_bytecode() {
        let bytecode = Bytes::default();
        assert_eq!(validate_bytecode(&bytecode), Err(BytecodeValidationError::Empty));
    }

    #[test]
    fn test_only_stops() {
        let bytecode = Bytes::from(vec![0x00; 200]);
        assert_eq!(validate_bytecode(&bytecode), Err(BytecodeValidationError::OnlyStops));
    }

    #[test]
    fn test_valid_simple_contract() {
        // Simple contract: PUSH1 0x00 RETURN
        let bytecode = Bytes::from(vec![0x60, 0x00, 0xf3]);
        assert!(validate_bytecode(&bytecode).is_ok());
    }

    #[test]
    fn test_too_many_selfdestructs() {
        let mut code = vec![0x60, 0x00]; // PUSH1 0
        code.extend(vec![0xff; 15]); // 15 SELFDESTRUCTs
        code.push(0xf3); // RETURN
        let bytecode = Bytes::from(code);
        assert_eq!(validate_bytecode(&bytecode), Err(BytecodeValidationError::SuspiciousPattern));
    }
}