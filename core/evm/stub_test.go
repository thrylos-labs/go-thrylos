//go:build test
// +build test

package evm

// Stub implementation for tests - no CGO/Rust needed
type RevmExecutor struct{}

func NewRevmExecutor(chainID uint64) (*RevmExecutor, error) {
	return &RevmExecutor{}, nil
}

func (e *RevmExecutor) Execute( /* params */ ) ([]byte, error) {
	return nil, nil
}

// Add any other methods your code calls on RevmExecutor
