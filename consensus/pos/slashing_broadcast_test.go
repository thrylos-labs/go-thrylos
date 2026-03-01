package pos

import (
	"os"
	"testing"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/thrylos-labs/go-thrylos/config"
	accountpkg "github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/storage"
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

func TestApplySlashing_RequiresSlashingManager(t *testing.T) {
	engine := &ConsensusEngine{}

	err := engine.applySlashing(&SlashingEvidence{
		ID:   "missing-manager",
		Type: EvidenceDoubleVoting,
	})
	if err == nil {
		t.Fatalf("expected error when slashing manager is not initialized")
	}
	if err.Error() != "slashing manager not initialized" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEvidenceProcessing_UsesPersistentStoreAsAuthority(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "thrylos-slashing-evidence-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(tmpDir)
	})

	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	if err != nil {
		t.Fatalf("failed to create badger store: %v", err)
	}
	t.Cleanup(func() {
		_ = badgerStore.Close()
	})

	ws, err := state.NewWorldState(tmpDir, accountpkg.ShardID(0), 1, config.DefaultConfig(), badgerStore)
	if err != nil {
		t.Fatalf("failed to create world state: %v", err)
	}

	engine := &ConsensusEngine{
		worldState:      ws,
		evidenceTracker: NewEvidenceTracker(),
		slashingManager: &SlashingManager{
			storage: storage.NewSlashingStorage(badgerStore.GetDB()),
		},
	}

	evidence := &SlashingEvidence{
		ID:               "persisted-evidence",
		Type:             EvidenceDoubleVoting,
		ValidatorAddress: "0x1234",
	}

	if err := engine.markEvidenceProcessed(evidence); err != nil {
		t.Fatalf("failed to mark evidence processed: %v", err)
	}

	engine.evidenceTracker = NewEvidenceTracker()

	processed, err := engine.isEvidenceProcessed(evidence.ID)
	if err != nil {
		t.Fatalf("failed to query processed evidence: %v", err)
	}
	if !processed {
		t.Fatalf("expected persistent store to report evidence as processed")
	}
	if !engine.evidenceTracker.IsProcessed(evidence.ID) {
		t.Fatalf("expected persistent lookup to repopulate the LRU cache")
	}
}
