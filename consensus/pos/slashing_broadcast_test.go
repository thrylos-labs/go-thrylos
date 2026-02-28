package pos

import (
	"testing"

	lru "github.com/hashicorp/golang-lru/v2"
)

func TestEvidenceTracker_EvictsLeastRecentlyUsed(t *testing.T) {
	tracker := NewEvidenceTracker()

	cache, err := lru.New[string, *SlashingEvidence](2)
	if err != nil {
		t.Fatalf("failed to create test cache: %v", err)
	}
	tracker.evidenceByID = cache

	e1 := &SlashingEvidence{ID: "e1"}
	e2 := &SlashingEvidence{ID: "e2"}
	e3 := &SlashingEvidence{ID: "e3"}

	tracker.MarkProcessed(e1)
	tracker.MarkProcessed(e2)

	if !tracker.IsProcessed("e1") {
		t.Fatalf("expected e1 to be tracked")
	}

	tracker.MarkProcessed(e3)

	if !tracker.IsProcessed("e1") {
		t.Fatalf("expected most recently accessed evidence to remain tracked")
	}
	if tracker.IsProcessed("e2") {
		t.Fatalf("expected least recently used evidence to be evicted")
	}
	if !tracker.IsProcessed("e3") {
		t.Fatalf("expected new evidence to be tracked")
	}
}
