package engine

import (
	"testing"

	"edge-sync/pkg/protocol"
)

func m(pairs ...[2]string) protocol.Manifest {
	m := protocol.Manifest{}
	for _, p := range pairs {
		m.Entries = append(m.Entries, protocol.FileEntry{Path: p[0], Fingerprint: p[1]})
	}
	return m
}

func paths(entries []protocol.FileEntry) map[string]bool {
	out := map[string]bool{}
	for _, e := range entries {
		out[e.Path] = true
	}
	return out
}

func TestDiffNoChange(t *testing.T) {
	prev := m([2]string{"a", "h1"}, [2]string{"b/c", "h2"})
	cs := Diff(prev, prev)
	if cs.HasChanges {
		t.Fatalf("expected no changes, got %+v", cs)
	}
}

func TestDiffAddedModifiedRemoved(t *testing.T) {
	prev := m(
		[2]string{"same", "h1"},
		[2]string{"modified", "old"},
		[2]string{"removed", "h3"},
	)
	next := m(
		[2]string{"same", "h1"},
		[2]string{"modified", "new"},
		[2]string{"added", "h4"},
	)
	cs := Diff(prev, next)

	if len(cs.Added) != 1 || cs.Added[0].Path != "added" {
		t.Errorf("added = %+v", cs.Added)
	}
	if len(cs.Modified) != 1 || cs.Modified[0].Path != "modified" {
		t.Errorf("modified = %+v", cs.Modified)
	}
	if len(cs.Removed) != 1 || cs.Removed[0].Path != "removed" {
		t.Errorf("removed = %+v", cs.Removed)
	}
	if !cs.HasChanges {
		t.Error("HasChanges should be true")
	}
}

func TestDiffFirstSyncAllAdded(t *testing.T) {
	cs := Diff(protocol.Manifest{}, m([2]string{"a", "h1"}, [2]string{"b", "h2"}))
	if len(cs.Added) != 2 || cs.HasChanges == false {
		t.Errorf("expected 2 added, got %+v", cs)
	}
}

func TestDiffAddedSetIsUsableForApply(t *testing.T) {
	// runner 用 Added+Modified 构造 staging 链接来源，Removed 不参与。
	prev := m([2]string{"gone", "h1"})
	next := m([2]string{"new", "h2"})
	cs := Diff(prev, next)
	if len(paths(cs.Added)) != 1 || len(cs.Removed) != 1 {
		t.Errorf("unexpected %+v", cs)
	}
	if len(cs.Modified) != 0 {
		t.Errorf("modified should be empty: %+v", cs.Modified)
	}
}
