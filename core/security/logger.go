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

// LogGasOverflowAttempt logs when gas calculations would overflow
func LogGasOverflowAttempt(context string, a, b uint64) {
	securityLog.Printf(
		"[SECURITY] GAS_OVERFLOW_ATTEMPT context=%s a=%d b=%d timestamp=%s",
		context, a, b, time.Now().UTC().Format(time.RFC3339),
	)
}

// LogSuspiciousGasValue logs when gas values are suspiciously high
func LogSuspiciousGasValue(context string, value uint64) {
	securityLog.Printf(
		"[SECURITY] SUSPICIOUS_GAS_VALUE context=%s value=%d timestamp=%s",
		context, value, time.Now().UTC().Format(time.RFC3339),
	)
}

// LogInvalidGasLimit logs when gas limit validation fails
func LogInvalidGasLimit(context string, gasLimit, maxAllowed uint64) {
	securityLog.Printf(
		"[SECURITY] INVALID_GAS_LIMIT context=%s gasLimit=%d maxAllowed=%d timestamp=%s",
		context, gasLimit, maxAllowed, time.Now().UTC().Format(time.RFC3339),
	)
}
