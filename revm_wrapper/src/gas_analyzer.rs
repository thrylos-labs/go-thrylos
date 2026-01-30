// thrylos-revm/src/gas_analyzer.rs

use revm::primitives::Bytes;

pub const BASE_TX_GAS: u64 = 21_000;
#[allow(dead_code)]
pub const CONTRACT_CREATION_GAS: u64 = 32_000;
pub const CODE_DEPOSIT_PER_BYTE: u64 = 200;
pub const CALLDATA_ZERO_BYTE_GAS: u64 = 4;
pub const CALLDATA_NONZERO_BYTE_GAS: u64 = 16;
pub const SSTORE_SET_GAS: u64 = 20_000;
pub const SLOAD_GAS: u64 = 2_100;
pub const JUMP_DEST_GAS: u64 = 1;
pub const PUSH_GAS: u64 = 3;
pub const CALL_GAS: u64 = 700;
pub const CREATE_GAS: u64 = 32_000;

// EVM Opcodes we care about for gas estimation
const SSTORE: u8 = 0x55;
const SLOAD: u8 = 0x54;
const CALL: u8 = 0xF1;
const CALLCODE: u8 = 0xF2;
const DELEGATECALL: u8 = 0xF4;
const STATICCALL: u8 = 0xFA;
const CREATE: u8 = 0xF0;
const CREATE2: u8 = 0xF5;
const JUMPDEST: u8 = 0x5B;

#[derive(Debug, Clone)]
pub struct GasEstimate {
    pub min_gas: u64,
    pub max_gas: u64,
    pub storage_operations: u64,
    pub external_calls: u64,
    pub contract_creations: u64,
    pub complexity_score: u64,
}

pub struct GasAnalyzer;

impl GasAnalyzer {
    /// Perform static analysis on bytecode to estimate gas requirements
    pub fn analyze_bytecode(bytecode: &Bytes) -> GasEstimate {
        let mut estimate = GasEstimate {
            min_gas: BASE_TX_GAS,
            max_gas: BASE_TX_GAS,
            storage_operations: 0,
            external_calls: 0,
            contract_creations: 0,
            complexity_score: 0,
        };

        let mut i = 0;
        let code = bytecode.as_ref();

        while i < code.len() {
            let opcode = code[i];
            
            match opcode {
                SSTORE => {
                    estimate.storage_operations += 1;
                    estimate.max_gas += SSTORE_SET_GAS;
                    estimate.min_gas += 5_000; // Minimum for updating existing slot
                    estimate.complexity_score += 10;
                }
                SLOAD => {
                    estimate.storage_operations += 1;
                    estimate.max_gas += SLOAD_GAS;
                    estimate.min_gas += SLOAD_GAS;
                    estimate.complexity_score += 5;
                }
                CALL | CALLCODE | DELEGATECALL | STATICCALL => {
                    estimate.external_calls += 1;
                    estimate.max_gas += CALL_GAS + 9_000; // Base + potential value transfer
                    estimate.min_gas += CALL_GAS;
                    estimate.complexity_score += 15;
                }
                CREATE | CREATE2 => {
                    estimate.contract_creations += 1;
                    estimate.max_gas += CREATE_GAS;
                    estimate.min_gas += CREATE_GAS;
                    estimate.complexity_score += 20;
                }
                JUMPDEST => {
                    estimate.max_gas += JUMP_DEST_GAS;
                    estimate.complexity_score += 1;
                }
                0x60..=0x7F => {
                    // PUSH1-PUSH32
                    let push_size = (opcode - 0x5F) as usize;
                    estimate.max_gas += PUSH_GAS;
                    i += push_size; // Skip the pushed bytes
                }
                _ => {
                    // Generic opcode (most are 3 gas)
                    estimate.max_gas += 3;
                }
            }
            
            i += 1;
        }

        // Add code deposit cost for contract creation
        estimate.max_gas += code.len() as u64 * CODE_DEPOSIT_PER_BYTE;

        estimate
    }

    /// Analyze calldata gas cost
    pub fn analyze_calldata(data: &[u8]) -> u64 {
        let mut gas = 0u64;
        
        for &byte in data {
            if byte == 0 {
                gas += CALLDATA_ZERO_BYTE_GAS;
            } else {
                gas += CALLDATA_NONZERO_BYTE_GAS;
            }
        }
        
        gas
    }

    /// Check if gas estimate is reasonable for the operation
    pub fn validate_gas_estimate(estimate: &GasEstimate, provided_gas: u64) -> Result<(), String> {
        // Must provide at least minimum gas
        if provided_gas < estimate.min_gas {
            return Err(format!(
                "Insufficient gas: provided {}, minimum required {}",
                provided_gas, estimate.min_gas
            ));
        }

        // Warn if complexity is extremely high
        if estimate.complexity_score > 1000 {
            return Err(format!(
                "Bytecode complexity too high (score: {}). Possible DoS attempt.",
                estimate.complexity_score
            ));
        }

        // Check for suspicious patterns
        if estimate.storage_operations > 100 {
            return Err(format!(
                "Too many storage operations ({}). Possible gas griefing attack.",
                estimate.storage_operations
            ));
        }

        if estimate.external_calls > 50 {
            return Err(format!(
                "Too many external calls ({}). Possible reentrancy setup.",
                estimate.external_calls
            ));
        }

        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use revm::primitives::Bytes;

    #[test]
    fn test_simple_bytecode_analysis() {
        // PUSH1 0x01, PUSH1 0x02, ADD, STOP
        let bytecode = Bytes::from(vec![0x60, 0x01, 0x60, 0x02, 0x01, 0x00]);
        let estimate = GasAnalyzer::analyze_bytecode(&bytecode);
        
        assert!(estimate.min_gas >= BASE_TX_GAS);
        assert_eq!(estimate.storage_operations, 0);
        assert_eq!(estimate.external_calls, 0);
    }

    #[test]
    fn test_storage_operations() {
        // PUSH1 0x01, PUSH1 0x00, SSTORE
        let bytecode = Bytes::from(vec![0x60, 0x01, 0x60, 0x00, 0x55]);
        let estimate = GasAnalyzer::analyze_bytecode(&bytecode);
        
        assert_eq!(estimate.storage_operations, 1);
        assert!(estimate.max_gas >= SSTORE_SET_GAS);
    }

    #[test]
    fn test_calldata_gas() {
        let data = vec![0x00, 0x01, 0xFF];
        let gas = GasAnalyzer::analyze_calldata(&data);
        
        // 1 zero byte (4 gas) + 2 nonzero bytes (16 gas each) = 36 gas
        assert_eq!(gas, 36);
    }
}