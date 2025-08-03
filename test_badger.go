// Create a new file: test_badger.go
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/dgraph-io/badger/v3"
)

func main() {
	// Create unique directory
	dataDir := fmt.Sprintf("/tmp/badger-test-%d", time.Now().UnixNano())
	fmt.Printf("Testing BadgerDB with directory: %s\n", dataDir)

	// Create directory
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		fmt.Printf("Failed to create directory: %v\n", err)
		return
	}

	// Try to open BadgerDB with minimal config
	opts := badger.DefaultOptions(dataDir).WithLogger(nil)

	fmt.Println("Attempting to open BadgerDB...")
	db, err := badger.Open(opts)
	if err != nil {
		fmt.Printf("Failed to open BadgerDB: %v\n", err)
		return
	}

	fmt.Println("✅ BadgerDB opened successfully!")

	// Test a simple operation
	err = db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte("test"), []byte("value"))
	})
	if err != nil {
		fmt.Printf("Failed to write: %v\n", err)
	} else {
		fmt.Println("✅ Write operation successful!")
	}

	// Close the database
	if err := db.Close(); err != nil {
		fmt.Printf("Failed to close: %v\n", err)
	} else {
		fmt.Println("✅ Database closed successfully!")
	}

	// Clean up
	os.RemoveAll(dataDir)
	fmt.Println("✅ Cleanup completed!")
}
