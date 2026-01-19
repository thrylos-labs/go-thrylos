// core/security/logger.go
package security

import (
	"log"
	"time"
)

// Logger for security events
var securityLog *log.Logger

func init() {
	// Initialize security logger
	// In production, this should write to a separate security log file
	securityLog = log.Default()
}

// ✅ ADD THESE NEW FUNCTIONS:

// LogSlashing logs validator slashing events
func LogSlashing(validator, reason, amount string) {
	securityLog.Printf(
		"[SECURITY] SLASHING validator=%s reason=%s amount=%s timestamp=%s",
		validator, reason, amount, time.Now().UTC().Format(time.RFC3339),
	)
}

// LogInvalidSignature logs failed signature verifications
func LogInvalidSignature(from, txID string) {
	securityLog.Printf(
		"[SECURITY] INVALID_SIGNATURE from=%s tx=%s timestamp=%s",
		from, txID, time.Now().UTC().Format(time.RFC3339),
	)
}

// LogRateLimit logs rate limit violations
func LogRateLimit(ip, tier string) {
	securityLog.Printf(
		"[SECURITY] RATE_LIMIT_EXCEEDED ip=%s tier=%s timestamp=%s",
		ip, tier, time.Now().UTC().Format(time.RFC3339),
	)
}

// LogIPBlocked logs when an IP is blocked for repeated violations
func LogIPBlocked(ip string, violations int) {
	securityLog.Printf(
		"[SECURITY] IP_BLOCKED ip=%s violations=%d timestamp=%s",
		ip, violations, time.Now().UTC().Format(time.RFC3339),
	)
}

// LogInvalidTransaction logs invalid transactions
func LogInvalidTransaction(txID, from, reason string) {
	securityLog.Printf(
		"[SECURITY] INVALID_TX tx=%s from=%s reason=%s timestamp=%s",
		txID, from, reason, time.Now().UTC().Format(time.RFC3339),
	)
}

// LogConsensusFailure logs consensus failures
func LogConsensusFailure(slot uint64, reason string) {
	securityLog.Printf(
		"[SECURITY] CONSENSUS_FAILURE slot=%d reason=%s timestamp=%s",
		slot, reason, time.Now().UTC().Format(time.RFC3339),
	)
}

// LogValidatorExit logs when a validator is deactivated
func LogValidatorExit(validator, reason string) {
	securityLog.Printf(
		"[SECURITY] VALIDATOR_EXIT validator=%s reason=%s timestamp=%s",
		validator, reason, time.Now().UTC().Format(time.RFC3339),
	)
}

// LogReorg logs chain reorganizations
func LogReorg(depth int64, oldHash, newHash string) {
	securityLog.Printf(
		"[SECURITY] CHAIN_REORG depth=%d old_hash=%s new_hash=%s timestamp=%s",
		depth, oldHash, newHash, time.Now().UTC().Format(time.RFC3339),
	)
}

// LogDoubleSign logs double signing attempts
func LogDoubleSign(validator string, slot uint64) {
	securityLog.Printf(
		"[SECURITY] DOUBLE_SIGN validator=%s slot=%d timestamp=%s",
		validator, slot, time.Now().UTC().Format(time.RFC3339),
	)
}

// Existing functions below...
func LogGasOverflowAttempt(context string, a, b uint64) {
	securityLog.Printf(
		"[SECURITY] GAS_OVERFLOW_ATTEMPT context=%s a=%d b=%d timestamp=%s",
		context, a, b, time.Now().UTC().Format(time.RFC3339),
	)
}

func LogSuspiciousGasValue(context string, value uint64) {
	securityLog.Printf(
		"[SECURITY] SUSPICIOUS_GAS_VALUE context=%s value=%d timestamp=%s",
		context, value, time.Now().UTC().Format(time.RFC3339),
	)
}

func LogInvalidGasLimit(context string, gasLimit, maxAllowed uint64) {
	securityLog.Printf(
		"[SECURITY] INVALID_GAS_LIMIT context=%s gasLimit=%d maxAllowed=%d timestamp=%s",
		context, gasLimit, maxAllowed, time.Now().UTC().Format(time.RFC3339),
	)
}
