package state

import (
	"os"
	"path/filepath"
	"testing"

	"edge-sync/pkg/protocol"
)

func TestRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	st := &TaskState{
		ManifestFingerprint: "fp-1",
		LastSyncAt:          "2025-06-01T00:00:00Z",
		LastSuccessAt:       "2025-06-01T00:00:00Z",
		Stats:               Stats{TotalSyncs: 3, TotalFiles: 10, TotalBytes: 2048},
	}
	st.LastManifest.Entries = []protocol.FileEntry{{Path: "a", Fingerprint: "h"}}

	if err := store.Save("t1", st); err != nil {
		t.Fatal(err)
	}
	got := store.Load("t1")
	if got.ManifestFingerprint != "fp-1" || got.Stats.TotalSyncs != 3 {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if len(got.LastManifest.Entries) != 1 || got.LastManifest.Entries[0].Path != "a" {
		t.Fatalf("manifest mismatch: %+v", got.LastManifest)
	}
}

func TestLoadMissingReturnsEmpty(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	got := store.Load("nope")
	if got.ConsecutiveFailures != 0 || len(got.LastManifest.Entries) != 0 {
		t.Fatalf("expected zero state, got %+v", got)
	}
}

func TestLoadCorruptReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := store.Load("bad")
	if len(got.LastManifest.Entries) != 0 {
		t.Fatalf("corrupt state should yield zero state, got %+v", got)
	}
}
