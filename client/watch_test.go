package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/protocol"
)

func TestWorkspaceWatchNativeBurst(t *testing.T) {
	c, workspace := peer(t, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	watch := c.WatchFiles(workspace.Path)
	if _, err := nextWorkspaceChanges(ctx, watch); err != nil {
		t.Fatal(err)
	}
	settleWorkspaceWatch(t, watch)
	for i := range 512 {
		name := filepath.Join(workspace.Path, fmt.Sprintf("generated-source-file-with-a-long-name-%04d.go", i))
		if err := os.WriteFile(name, []byte("package generated"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(name, name+".renamed"); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(name + ".renamed"); err != nil {
			t.Fatal(err)
		}
	}
	if changes, err := nextWorkspaceChanges(ctx, watch); err != nil || changes&protocol.WorkspaceTreeChanged == 0 {
		t.Fatalf("burst refresh=%b, err=%v", changes, err)
	}
	settleWorkspaceWatch(t, watch)
	if err := os.WriteFile(filepath.Join(workspace.Path, "after-burst.go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if changes, err := nextWorkspaceChanges(ctx, watch); err != nil || changes != protocol.WorkspaceTreeChanged|protocol.WorkspaceGitChanged {
		t.Fatalf("watch did not recover after burst: changes=%b, err=%v", changes, err)
	}
}

func TestWorkspaceWatchNativeEvents(t *testing.T) {
	all := protocol.WorkspaceTreeChanged | protocol.WorkspaceGitChanged
	for _, test := range []struct {
		name string
		want protocol.WorkspaceChange
	}{
		{"write", protocol.WorkspaceGitChanged},
		{"create", all},
		{"remove", all},
		{"rename", all},
		{"git-index", all | protocol.WorkspaceIgnoreChanged},
		{"git-ref", protocol.WorkspaceGitChanged},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, workspace := peer(t, nil)
			for _, name := range []string{"src/nested", ".git/refs/heads"} {
				if err := os.MkdirAll(filepath.Join(workspace.Path, name), 0700); err != nil {
					t.Fatal(err)
				}
			}
			name := filepath.Join(workspace.Path, "src/nested/file.go")
			if err := os.WriteFile(name, []byte("before"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			watch := c.WatchFiles(workspace.Path)
			if changes, err := nextWorkspaceChanges(ctx, watch); err != nil || changes != all|protocol.WorkspaceRepositoriesChanged {
				t.Fatalf("initialize watch: %b, %v", changes, err)
			}
			settleWorkspaceWatch(t, watch)
			var err error
			switch test.name {
			case "write":
				err = os.WriteFile(name, []byte("after"), 0600)
			case "create":
				err = os.WriteFile(filepath.Join(workspace.Path, "src/nested/new.go"), nil, 0600)
			case "remove":
				err = os.Remove(name)
			case "rename":
				err = os.Rename(name, name+".renamed")
			case "git-index":
				err = os.WriteFile(filepath.Join(workspace.Path, ".git/index"), nil, 0600)
			case "git-ref":
				err = os.WriteFile(filepath.Join(workspace.Path, ".git/refs/heads/main"), nil, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			changes, err := nextWorkspaceChanges(ctx, watch)
			allowed := test.want
			// FSEvents can retain Created alongside Modified for a recently
			// created path, even after an earlier batch was delivered.
			if runtime.GOOS == "darwin" && test.name == "write" {
				allowed |= protocol.WorkspaceTreeChanged
			}
			if err != nil || changes&test.want != test.want || changes & ^allowed != 0 {
				t.Fatalf("filesystem changes=%b, want %b, err=%v", changes, test.want, err)
			}
		})
	}
}

func TestWorkspaceWatchCancellationAndClose(t *testing.T) {
	c, workspace := peer(t, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	watch := c.WatchFiles(workspace.Path)
	if _, err := nextWorkspaceChanges(ctx, watch); err != nil {
		t.Fatal(err)
	}
	idle, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stop()
	if _, err := nextWorkspaceChanges(idle, watch); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("idle watch did not block: %v", err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := nextWorkspaceChanges(ctx, watch)
		result <- err
	}()
	if err := c.CloseWorkspace(ctx, workspace.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("closed workspace watch succeeded")
		}
	case <-ctx.Done():
		t.Fatal("closing a workspace did not release its blocked watch")
	}
}

func TestWorkspaceWatchAddsMovedDirectories(t *testing.T) {
	c, workspace := peer(t, nil)
	source := filepath.Join(t.TempDir(), "moved")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested/file.go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	watch := c.WatchFiles(workspace.Path)
	if _, err := nextWorkspaceChanges(ctx, watch); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(workspace.Path, "moved")
	if err := os.Rename(source, destination); err != nil {
		t.Fatal(err)
	}
	if changes, err := nextWorkspaceChanges(ctx, watch); err != nil || changes&protocol.WorkspaceTreeChanged == 0 {
		t.Fatalf("moved directory: changes=%b, err=%v", changes, err)
	}
	settleWorkspaceWatch(t, watch)
	if err := os.WriteFile(filepath.Join(destination, "nested/file.go"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if changes, err := nextWorkspaceChanges(ctx, watch); err != nil || changes != protocol.WorkspaceGitChanged {
		t.Fatalf("new descendant: changes=%b, err=%v", changes, err)
	}
}

func TestWorkspaceWatchExternalGitMetadata(t *testing.T) {
	for _, layout := range []string{"parent-repository", "separate-git-dir", "worktree"} {
		t.Run(layout, func(t *testing.T) {
			c, workspace := peer(t, nil)
			root := workspace.Path
			metadata := filepath.Join(root, ".git")
			if layout != "parent-repository" {
				metadata = t.TempDir()
				if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+metadata+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				root = filepath.Join(root, "project")
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
				if _, err := c.OpenWorkspace(t.Context(), root); err != nil {
					t.Fatal(err)
				}
			}
			if layout == "worktree" {
				common := t.TempDir()
				if err := os.WriteFile(filepath.Join(metadata, "commondir"), []byte(common+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				metadata = common
			}
			if err := os.MkdirAll(filepath.Join(metadata, "refs/heads"), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			watch := c.WatchFiles(root)
			if _, err := nextWorkspaceChanges(ctx, watch); err != nil {
				t.Fatal(err)
			}
			settleWorkspaceWatch(t, watch)
			if err := os.WriteFile(filepath.Join(metadata, "refs/heads/main"), []byte("reference\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if changes, err := nextWorkspaceChanges(ctx, watch); err != nil || changes != protocol.WorkspaceGitChanged {
				t.Fatalf("external Git metadata: changes=%b, err=%v", changes, err)
			}
		})
	}
}

func settleWorkspaceWatch(t *testing.T, watch *FileWatch) {
	t.Helper()
	// Windows may still deliver setup writes after watches are registered.
	// Drain those notifications before testing a separate filesystem change.
	ctx, cancel := context.WithTimeout(t.Context(), 450*time.Millisecond)
	defer cancel()
	for {
		if _, err := nextWorkspaceChanges(ctx, watch); err != nil {
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			break
		}
	}
}

func TestWorkspaceWatchDeliversLastChangeInBurst(t *testing.T) {
	c, workspace := peer(t, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	watch := c.WatchFiles(workspace.Path)
	if _, err := nextWorkspaceChanges(ctx, watch); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first.go", "last.go"} {
		if err := os.WriteFile(filepath.Join(workspace.Path, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
		if changes, err := nextWorkspaceChanges(ctx, watch); err != nil || changes&protocol.WorkspaceTreeChanged == 0 {
			t.Fatalf("change to %s was dropped: %b, %v", name, changes, err)
		}
	}
}

func TestWorkspaceIndexRefreshUsesNativeDirectoryChanges(t *testing.T) {
	c, workspace := peer(t, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	watch := c.WatchFiles(workspace.Path)
	for _, name := range []string{"src/nested", "cached/deep"} {
		if err := os.MkdirAll(filepath.Join(workspace.Path, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	var previous uint64
	read := func(refresh bool) (uint64, []protocol.IndexEntry) {
		t.Helper()
		var id uint64
		var err error
		if refresh {
			id, err = c.RefreshIndex(ctx, workspace.Path, previous)
		} else {
			id, err = c.OpenIndex(ctx, workspace.Path)
		}
		if err != nil {
			t.Fatal(err)
		}
		if refresh && previous != 0 {
			if _, err := c.SearchPaths(ctx, previous, "", nil); err != nil {
				t.Fatalf("refresh closed the visible index before commit: %v", err)
			}
		}
		var entries, page []protocol.IndexEntry
		for {
			var done bool
			var warning string
			page, done, warning, err = c.IndexPage(ctx, id, len(entries), page[:0])
			if err != nil || warning != "" {
				t.Fatalf("index page: %s, %v", warning, err)
			}
			entries = append(entries, page...)
			if done {
				previous = id
				return id, entries
			}
		}
	}
	// Refresh has a full-scan fallback before a native watch is installed.
	read(true)
	if _, err := nextWorkspaceChanges(ctx, watch); err != nil {
		t.Fatal(err)
	}
	read(false)
	for _, name := range []string{"src/nested/new.go", "root.go"} {
		if err := os.WriteFile(filepath.Join(workspace.Path, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := nextWorkspaceChanges(ctx, watch); err != nil {
			t.Fatal(err)
		}
		id, refreshed := read(true)
		paths, err := c.SearchPaths(ctx, id, name, nil)
		if err != nil || !slices.Equal(paths, []string{name}) {
			t.Fatalf("refreshed path search = %v, %v", paths, err)
		}
		_, full := read(false)
		if !slices.Equal(refreshed, full) {
			t.Fatalf("refreshed index differs from full scan: %+v, %+v", refreshed, full)
		}
	}
	// Discard a replacement after it consumes the directory hints. Reusing the
	// older visible snapshot must now fall back to a full scan.
	base := previous
	if err := os.WriteFile(filepath.Join(workspace.Path, "after-cancel.go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := nextWorkspaceChanges(ctx, watch); err != nil {
		t.Fatal(err)
	}
	abandoned, _ := read(true)
	if err := c.CloseIndex(ctx, abandoned); err != nil {
		t.Fatal(err)
	}
	previous = base
	_, refreshed := read(true)
	if !slices.ContainsFunc(refreshed, func(e protocol.IndexEntry) bool { return e.Path == "after-cancel.go" }) {
		t.Fatal("cancelled snapshot discarded directory hints for an older baseline")
	}
}

func TestFileServiceIndependentSubscribers(t *testing.T) {
	c, workspace := peer(t, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	editor, extension := c.WatchFiles(workspace.Path), c.WatchFiles(workspace.Path)
	for _, watch := range []*FileWatch{editor, extension} {
		if first, err := watch.Next(ctx, nil); err != nil || !first.Reset {
			t.Fatalf("initial subscriber: %+v %v", first, err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "文件.go"), []byte("package p"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, watch := range []*FileWatch{editor, extension} {
		batch, err := watch.Next(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, event := range batch.Files {
			found = found || event.Path == "文件.go" && event.Op&protocol.FileCreated != 0
		}
		if !found {
			t.Fatalf("subscriber missed file service event: %+v", batch)
		}
	}
}

func nextWorkspaceChanges(ctx context.Context, watch *FileWatch) (protocol.WorkspaceChange, error) {
	batch, err := watch.Next(ctx, nil)
	return batch.Changes, err
}
