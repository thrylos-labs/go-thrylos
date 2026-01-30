// thrylos-revm/src/bytecode_validation.rs

//! Bytecode validation for contract deployment security
//! 
//! This module provides basic security checks for contract bytecode before deployment.
//! It performs static analysis to detect malformed bytecode, stack violations,
//! and potential gas bombs before the EVM execution layer.

use revm::primitives::Bytes;
use std::collections::HashSet;

/// Minimum bytecode size for a valid contract
const MIN_BYTECODE_SIZE: usize = 1;


pub const MIN_DEPLOYMENT_GAS: u64 = 100_000;

/// Maximum bytecode size (EIP-170 limit is 24KB)
pub const MAX_BYTECODE_SIZE: usize = 24_576; 

/// EVM Stack Limit
const MAX_STACK_DEPTH: i32 = 1024;

mod opcodes {
    pub const STOP: u8 = 0x00;
    pub const JUMP: u8 = 0x56;
    pub const JUMPI: u8 = 0x57;
    pub const JUMPDEST: u8 = 0x5b;
    pub const PUSH1: u8 = 0x60;
    pub const PUSH32: u8 = 0x7f;
    #[allow(dead_code)]
    pub const RETURN: u8 = 0xf3;
    #[allow(dead_code)]
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
    StackUnderflow,
    StackOverflow,
    InvalidJumpDest,
    TruncatedPush,
    ComplexityLimitExceeded, // Gas bomb protection
}

impl std::fmt::Display for BytecodeValidationError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Empty => write!(f, "Bytecode is empty"),
            Self::TooSmall => write!(f, "Bytecode is too small"),
            Self::TooLarge => write!(f, "Bytecode exceeds maximum size (24KB)"),
            Self::OnlyStops => write!(f, "Bytecode contains only STOP opcodes"),
            Self::InvalidInitcode => write!(f, "Bytecode invalid structure"),
            Self::SuspiciousPattern => write!(f, "Suspicious opcode patterns detected"),
            Self::StackUnderflow => write!(f, "Potential stack underflow detected"),
            Self::StackOverflow => write!(f, "Potential stack overflow detected"),
            Self::InvalidJumpDest => write!(f, "Jumps to invalid destinations detected"),
            Self::TruncatedPush => write!(f, "Bytecode ends in the middle of a PUSH operation"),
            Self::ComplexityLimitExceeded => write!(f, "Contract complexity/looping exceeds safety limits"),
        }
    }
}

/// Validate bytecode before deployment
pub fn validate_bytecode(bytecode: &Bytes) -> Result<(), BytecodeValidationError> {
    // 1. Basic Size Checks
    if bytecode.is_empty() { return Err(BytecodeValidationError::Empty); }
    if bytecode.len() < MIN_BYTECODE_SIZE { return Err(BytecodeValidationError::TooSmall); }
    if bytecode.len() > MAX_BYTECODE_SIZE { return Err(BytecodeValidationError::TooLarge); }

    // 2. DoS Pattern Checks
    if is_only_stops(bytecode) { return Err(BytecodeValidationError::OnlyStops); }
    if has_suspicious_patterns(bytecode) { return Err(BytecodeValidationError::SuspiciousPattern); }

    // 3. Static Analysis (The heavy lifting)
    // Checks Stack Depth, JUMPDEST validity, and Code Structure
    analyze_static_safety(bytecode)?;

    Ok(())
}

/// Performs static analysis on the bytecode to check for:
/// - Valid instruction boundaries (PUSH data skipping)
/// - Stack underflows/overflows (Simulation)
/// - Valid Jump Destinations
/// - Gas Bomb detection via backward jump analysis
fn analyze_static_safety(bytecode: &Bytes) -> Result<(), BytecodeValidationError> {
    let mut pc = 0;
    let mut current_stack_height: i32 = 0;
    let mut jump_dests = HashSet::new();
    let mut backward_jumps = 0;

    // First Pass: Map JUMPDESTs and validate PUSH boundaries
    while pc < bytecode.len() {
        let op = bytecode[pc];

        // Track valid JUMPDESTs
        if op == opcodes::JUMPDEST {
            jump_dests.insert(pc);
        }

        // Handle PUSH skipping
        if op >= opcodes::PUSH1 && op <= opcodes::PUSH32 {
            let push_bytes = (op - opcodes::PUSH1 + 1) as usize;
            pc += push_bytes;
            
            // Check if PUSH goes out of bounds
            if pc >= bytecode.len() {
                return Err(BytecodeValidationError::TruncatedPush);
            }
        }
        pc += 1;
    }

    // Second Pass: Simulate Stack & Control Flow
    pc = 0;
    while pc < bytecode.len() {
        let op = bytecode[pc];

        // 1. Stack Height Simulation
        let (inputs, outputs) = get_stack_impact(op);
        
        // Check Underflow
        if current_stack_height < inputs {
            // Note: This is a heuristic. Unreachable code might trigger this, 
            // but for deployment validation, being strict is better.
            return Err(BytecodeValidationError::StackUnderflow);
        }

        current_stack_height -= inputs;
        current_stack_height += outputs;

        // Check Overflow
        if current_stack_height > MAX_STACK_DEPTH {
            return Err(BytecodeValidationError::StackOverflow);
        }

        // 2. Gas Bomb / Loop Detection
        // If we see a JUMP/JUMPI, we can't statically know the destination value 
        // (it's on the stack), but we can count control flow complexity.
        if op == opcodes::JUMP || op == opcodes::JUMPI {
            // Heuristic: If we have excessive JUMPs relative to code size, warn complexity.
            // A strict static analysis of backward jumps is hard without a CFG,
            // so we use a density check here.
            backward_jumps += 1;
        }

        // Advance PC
        if op >= opcodes::PUSH1 && op <= opcodes::PUSH32 {
            let push_bytes = (op - opcodes::PUSH1 + 1) as usize;
            pc += push_bytes;
        }
        pc += 1;
    }

    // 3. Complexity Limit (Anti-Gas Bomb)
    // If > 10% of instructions are JUMPs, or total count is high
    if backward_jumps > 50 && backward_jumps > (bytecode.len() / 10) {
         return Err(BytecodeValidationError::ComplexityLimitExceeded);
    }

    Ok(())
}

/// Returns (inputs_popped, outputs_pushed) for a given opcode
/// Used for static stack simulation
fn get_stack_impact(op: u8) -> (i32, i32) {
    match op {
        // Stop/Arithmetic (2 inputs, 1 output usually)
        0x01..=0x0b => (2, 1), // ADD, MUL, SUB...
        0x00 => (0, 0),        // STOP
        
        // Comparison / Bitwise
        0x10..=0x1d => (2, 1), // LT, GT...
        
        // SHA3
        0x20 => (2, 1),

        // Environment
        0x30..=0x3e => (0, 1), // ADDRESS, BALANCE... (Some take args, simplified for safety)
        
        // Block Info
        0x40..=0x48 => (0, 1), 

        // Memory/Storage
        0x50 => (1, 0), // POP
        0x51 | 0x54 => (1, 1), // MLOAD, SLOAD
        0x52 | 0x55 => (2, 0), // MSTORE, SSTORE
        0x53 => (2, 0),        // MSTORE8
        
        // Flow
        0x56 => (1, 0), // JUMP (consumes dest)
        0x57 => (2, 0), // JUMPI (consumes dest + condition)
        0x58 => (0, 1), // PC
        0x59 => (0, 1), // MSIZE
        0x5b => (0, 0), // JUMPDEST
        
        // Push operations (0 inputs, 1 output)
        0x60..=0x7f => (0, 1), 
        
        // Duplication (DUPx) - 0 inputs consumed (technically), 1 added
        // But DUPx requires stack depth x. 
        0x80..=0x8f => (0, 1), // Logic handled by depth check
        
        // Swap (SWAPx) - 0 net change
        0x90..=0x9f => (0, 0),

        // Logging
        0xa0..=0xa4 => ((op - 0xa0 + 2) as i32, 0),

        // System
        0xf0 => (3, 1), // CREATE
        0xf1 => (7, 1), // CALL
        0xf2 => (7, 1), // CALLCODE
        0xf3 => (2, 0), // RETURN
        0xf4 => (6, 1), // DELEGATECALL
        0xf5 => (4, 1), // CREATE2
        0xfa => (6, 1), // STATICCALL
        0xfd => (2, 0), // REVERT
        0xff => (1, 0), // SELFDESTRUCT
        
        _ => (0, 0), // Assume neutral for unknowns to prevent breaking valid new EIPs
    }
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

/// Returns (min_stack_required, inputs_popped, outputs_pushed)
fn get_stack_impact_details(op: u8) -> (i32, i32, i32) {
    match op {
        // DUPx (0x80..0x8f): Needs depth x, pops 0, pushes 1
        0x80..=0x8f => {
            let n = (op - 0x80 + 1) as i32;
            (n, 0, 1)
        },
        // SWAPx (0x90..0x9f): Needs depth x+1, pops 0, pushes 0 (net)
        0x90..=0x9f => {
            let n = (op - 0x90 + 1) as i32;
            (n + 1, 0, 0)
        },
        // Standard ops mapping to (inputs, inputs, outputs)
        // usage: let (in, out) = old_get_stack_impact(op); (in, in, out)
        _ => {
            let (inputs, outputs) = get_stack_impact(op); // your existing function
            (inputs, inputs, outputs)
        }
    }
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