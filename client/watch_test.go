package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/protocol"
)

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
		{"git-index", protocol.WorkspaceGitChanged},
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
			if changes, err := c.WatchWorkspace(ctx, workspace.Path); err != nil || changes != all {
				t.Fatalf("initialize watch: %b, %v", changes, err)
			}
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
			if changes, err := c.WatchWorkspace(ctx, workspace.Path); err != nil || changes != test.want {
				t.Fatalf("filesystem changes=%b, want %b, err=%v", changes, test.want, err)
			}
		})
	}
}

func TestWorkspaceWatchCancellationAndClose(t *testing.T) {
	c, workspace := peer(t, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := c.WatchWorkspace(ctx, workspace.Path); err != nil {
		t.Fatal(err)
	}
	idle, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stop()
	if _, err := c.WatchWorkspace(idle, workspace.Path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("idle watch did not block: %v", err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := c.WatchWorkspace(ctx, workspace.Path)
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
	if _, err := c.WatchWorkspace(ctx, workspace.Path); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(workspace.Path, "moved")
	if err := os.Rename(source, destination); err != nil {
		t.Fatal(err)
	}
	if changes, err := c.WatchWorkspace(ctx, workspace.Path); err != nil || changes&protocol.WorkspaceTreeChanged == 0 {
		t.Fatalf("moved directory: changes=%b, err=%v", changes, err)
	}
	// The event gate has no timer; allow its timestamp window to elapse before
	// sending the next native event inside a newly discovered descendant.
	time.Sleep(1100 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(destination, "nested/file.go"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if changes, err := c.WatchWorkspace(ctx, workspace.Path); err != nil || changes != protocol.WorkspaceGitChanged {
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
			if _, err := c.WatchWorkspace(ctx, root); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(metadata, "refs/heads/main"), []byte("reference\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if changes, err := c.WatchWorkspace(ctx, root); err != nil || changes != protocol.WorkspaceGitChanged {
				t.Fatalf("external Git metadata: changes=%b, err=%v", changes, err)
			}
		})
	}
}
