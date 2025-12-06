package storage

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/dgraph-io/badger/v3"
	"github.com/stretchr/testify/require"
)

func TestBadgerBackupAndRestore(t *testing.T) {
	// 1. Setup Source DB
	dir := t.TempDir()
	storage, err := NewBadgerStorage(dir)
	require.NoError(t, err)
	defer storage.Close()

	// Write some data
	testKey := []byte("test-key")
	testVal := []byte("test-value-123")
	err = storage.Set(testKey, testVal)
	require.NoError(t, err)

	// 2. Perform Backup
	var backupBuffer bytes.Buffer

	// Backup from version 0 (full backup)
	newSince, err := storage.Backup(&backupBuffer, 0)
	require.NoError(t, err)
	require.Greater(t, newSince, uint64(0), "Backup should return new sequence number")
	require.Greater(t, backupBuffer.Len(), 0, "Backup buffer should not be empty")

	fmt.Printf("✅ Backup created: %d bytes, version: %d\n", backupBuffer.Len(), newSince)

	// 3. Restore to a NEW DB
	restoreDir := t.TempDir()

	// Open raw badger DB for restoration (since our wrapper doesn't expose Load yet)
	restoreOpts := badger.DefaultOptions(restoreDir)
	restoreOpts.Logger = nil
	restoreDB, err := badger.Open(restoreOpts)
	require.NoError(t, err)
	defer restoreDB.Close()

	// Perform Restore
	err = restoreDB.Load(&backupBuffer, 2) // maxPendingWrites=2
	require.NoError(t, err)

	// 4. Verify Data Exists in Restored DB
	err = restoreDB.View(func(txn *badger.Txn) error {
		item, err := txn.Get(testKey)
		if err != nil {
			return err
		}
		val, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		require.Equal(t, testVal, val, "Restored value must match original")
		return nil
	})
	require.NoError(t, err)

	fmt.Println("✅ Backup L-03 Fix Verified: Data successfully backed up and restored.")
}
