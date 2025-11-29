// Add to slashing_manager_test.go
package pos

import (
	"fmt"
	"sync"
	"testing"
)

// MockWorldStateBalancer implements WorldStateBalancer for testing
type MockWorldStateBalancer struct {
	balances map[string]int64
	mu       sync.RWMutex
}

func (m *MockWorldStateBalancer) GetBalance(address string) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	balance, exists := m.balances[address]
	if !exists {
		return 0, fmt.Errorf("validator not found: %s", address)
	}
	return balance, nil
}

func (m *MockWorldStateBalancer) UpdateBalance(address string, newBalance int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.balances[address]; !exists {
		return fmt.Errorf("validator not found: %s", address)
	}
	m.balances[address] = newBalance
	return nil
}

// Test case
func TestReportBlockWithholding_PreventDoubleSlashing(t *testing.T) {
	mockWorldState := &MockWorldStateBalancer{
		balances: map[string]int64{
			"validator1": 1000000,
		},
	}

	sm := NewSlashingManager(nil, mockWorldState, nil)

	// First call
	err1 := sm.ReportBlockWithholding("validator1")
	if err1 != nil {
		t.Fatalf("First call failed: %v", err1)
	}

	balance1, _ := mockWorldState.GetBalance("validator1")

	// Second call (should be idempotent)
	err2 := sm.ReportBlockWithholding("validator1")
	if err2 != nil {
		t.Fatalf("Second call failed: %v", err2)
	}

	balance2, _ := mockWorldState.GetBalance("validator1")

	// Verify only slashed once
	if balance1 != balance2 {
		t.Errorf("Double slashing detected: balance after 1st=%d, after 2nd=%d (should be equal)", balance1, balance2)
	}

	// Verify only one record
	records := sm.GetSlashingRecords("validator1")
	if len(records) != 1 {
		t.Errorf("Expected 1 slashing record, got %d", len(records))
	}
}
