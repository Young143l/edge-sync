package engine

import (
	"edge-sync/pkg/protocol"
)

// ChangeSet 两次 Manifest 之间的差异。
type ChangeSet struct {
	Added    []protocol.FileEntry
	Modified []protocol.FileEntry
	Removed  []protocol.FileEntry

	HasChanges bool
}

// Diff 对比新旧 Manifest（按 path 索引），产出变更集。
// fingerprint 只做字符串相等比较，不解释内容。
func Diff(prev, next protocol.Manifest) ChangeSet {
	prevFP := make(map[string]string, len(prev.Entries))
	for i := range prev.Entries {
		e := &prev.Entries[i]
		prevFP[e.Path] = e.Fingerprint
	}
	nextPaths := make(map[string]bool, len(next.Entries))

	var cs ChangeSet
	for i := range next.Entries {
		e := &next.Entries[i]
		nextPaths[e.Path] = true
		oldFP, existed := prevFP[e.Path]
		switch {
		case !existed:
			cs.Added = append(cs.Added, *e)
		case oldFP != e.Fingerprint:
			cs.Modified = append(cs.Modified, *e)
		}
	}
	for i := range prev.Entries {
		e := &prev.Entries[i]
		if !nextPaths[e.Path] {
			cs.Removed = append(cs.Removed, *e)
		}
	}
	cs.HasChanges = len(cs.Added)+len(cs.Modified)+len(cs.Removed) > 0
	return cs
}
