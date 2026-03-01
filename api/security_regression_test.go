package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleJSONRPC_RejectsOversizedBody(t *testing.T) {
	server := &Server{}
	oversized := bytes.Repeat([]byte("a"), int(maxJSONRequestBodyBytes)+1)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(oversized))
	recorder := httptest.NewRecorder()

	server.handleJSONRPC(recorder, req)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status %d, got %d", http.StatusRequestEntityTooLarge, recorder.Code)
	}
}

func TestAwardFaucet_PersistsCooldownBeforeSuccess(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "thrylos-points-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(tmpDir)
	})

	path := filepath.Join(tmpDir, "points.json")
	pm := NewPointsManager(path)

	total, awarded, err := pm.AwardFaucet("0xabc")
	if err != nil {
		t.Fatalf("unexpected faucet error: %v", err)
	}
	if total != 0 {
		t.Fatalf("expected zero points, got %d", total)
	}
	if awarded {
		t.Fatalf("expected faucet points award flag to remain false")
	}

	reloaded := NewPointsManager(path)
	user := reloaded.GetUserPoints("0xabc")
	if user.LastFaucet.IsZero() {
		t.Fatalf("expected faucet cooldown to be persisted to disk")
	}
}

func TestAwardFaucet_FailsClosedWhenPersistenceFails(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "thrylos-points-dir-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(tmpDir)
	})

	pm := NewPointsManager(tmpDir)

	_, awarded, err := pm.AwardFaucet("0xdef")
	if err == nil {
		t.Fatalf("expected faucet persistence error")
	}
	if awarded {
		t.Fatalf("expected faucet award flag to remain false on persistence failure")
	}

	user := pm.GetUserPoints("0xdef")
	if !user.LastFaucet.IsZero() {
		t.Fatalf("expected faucet cooldown to roll back on persistence failure")
	}
}
