package server

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/pencode-labs/pencode/protocol"
)

func indexSnapshot(t *testing.T, root string, previous []protocol.IndexEntry, delta workspaceTreeDelta) []protocol.IndexEntry {
	t.Helper()
	p := &serviceIndex{root: root, changed: make(chan struct{})}
	p.walkSnapshot(t.Context(), previous, delta)
	if p.err != nil {
		t.Fatal(p.err)
	}
	return p.entries
}

func TestIndexReconcilesDirectoryChanges(t *testing.T) {
	root := t.TempDir()
	write := func(name string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"a/deep/original.go", "b/keep.go", "replace/old.go", "kind/file.go", "root.go"} {
		write(name)
	}
	previous := indexSnapshot(t, root, nil, workspaceTreeDelta{full: true})
	for _, name := range []string{"replace", "kind", "root.go"} {
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"a/deep/new.go", "a/file.go", "replace/nested/new.go", "new/nested/added.go", "kind", "dist/ignored.go"} {
		write(name)
	}
	delta := workspaceTreeDelta{count: 5}
	copy(delta.directories[:], []string{".", "a", "a/deep", "replace", "replace/nested"})
	next := indexSnapshot(t, root, previous, delta)
	full := indexSnapshot(t, root, nil, workspaceTreeDelta{full: true})
	if !slices.Equal(next, full) {
		t.Fatalf("incremental snapshot differs from full scan:\n%+v\n%+v", next, full)
	}
	// The parent directory is clean in this second refresh. A nested hint
	// must still reach its subtree without rereading its siblings.
	write("a/deep/last.go")
	delta = workspaceTreeDelta{count: 1}
	delta.directories[0] = "a/deep"
	next = indexSnapshot(t, root, next, delta)
	full = indexSnapshot(t, root, nil, workspaceTreeDelta{full: true})
	if !slices.Equal(next, full) {
		t.Fatalf("nested directory refresh differs from full scan: %+v", next)
	}
}

func TestIndexReusesUnchangedSubtrees(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"changed", "cached/deep"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	name := filepath.Join(root, "cached/deep/keep.go")
	if err := os.WriteFile(name, nil, 0600); err != nil {
		t.Fatal(err)
	}
	previous := indexSnapshot(t, root, nil, workspaceTreeDelta{full: true})
	// Deliberately withhold this change from the directory hints. If refresh
	// walks the cached subtree it would drop keep.go from the result.
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "changed/new.go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	delta := workspaceTreeDelta{count: 1}
	delta.directories[0] = "changed"
	next := indexSnapshot(t, root, previous, delta)
	for _, path := range []string{"cached/deep/keep.go", "changed/new.go"} {
		if !slices.ContainsFunc(next, func(e protocol.IndexEntry) bool { return e.Path == path }) {
			t.Fatalf("snapshot omitted %s", path)
		}
	}
}

func BenchmarkIndexSnapshot(b *testing.B) {
	root := b.TempDir()
	for i := range 100 {
		directory := filepath.Join(root, fmt.Sprint(i))
		if err := os.Mkdir(directory, 0700); err != nil {
			b.Fatal(err)
		}
		for j := range 10 {
			if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("%d.go", j)), nil, 0600); err != nil {
				b.Fatal(err)
			}
		}
	}
	base := &serviceIndex{root: root, changed: make(chan struct{})}
	base.walk(b.Context())
	if base.err != nil {
		b.Fatal(base.err)
	}
	for _, full := range []bool{true, false} {
		b.Run(fmt.Sprintf("full=%v", full), func(b *testing.B) {
			b.ReportAllocs()
			delta := workspaceTreeDelta{full: full, count: 1}
			delta.directories[0] = "0"
			for b.Loop() {
				p := &serviceIndex{root: root, changed: make(chan struct{})}
				p.walkSnapshot(b.Context(), base.entries, delta)
				if p.err != nil {
					b.Fatal(p.err)
				}
			}
		})
	}
}
