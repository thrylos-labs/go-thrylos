package pos

import (
	"encoding/json"
	"time"
)

// GetSlashingMetrics returns security metrics for monitoring
func (sm *SlashingManager) GetSlashingMetrics() ([]byte, error) {
	stats := sm.metrics.GetStats()
	pendingCount := sm.confirmations.GetPendingCount()

	metrics := map[string]interface{}{
		"submissions":           stats,
		"pending_confirmations": pendingCount,
		"timestamp":             time.Now().Unix(),
	}

	return json.Marshal(metrics)
}
