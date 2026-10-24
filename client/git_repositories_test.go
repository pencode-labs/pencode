package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestGitRepositoriesDiscoveryAndLifetime(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git unavailable")
	}
	c, workspace := peer(t, nil)
	git := func(args ...string) {
		t.Helper()
		if _, err := c.Git(t.Context(), workspace.Path, args...); err != nil {
			t.Fatal(err)
		}
	}
	// The project itself need not be a repository. Recognize ordinary .git
	// directories and the .git files used by submodules and linked worktrees.
	one, two := filepath.Join(workspace.Path, "one"), filepath.Join(workspace.Path, "two")
	git("init", "-b", "main", one)
	git("init", "--separate-git-dir", filepath.Join(t.TempDir(), "metadata"), two)
	if err := os.WriteFile(filepath.Join(workspace.Path, ".git"), []byte("invalid metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	roots, err := c.GitRepositories(t.Context(), workspace.Path)
	want := []string{filepath.ToSlash(one), filepath.ToSlash(two)}
	if err != nil || !slices.Equal(roots, want) {
		t.Fatalf("repositories=%q, want=%q, err=%v", roots, want, err)
	}
	for _, root := range roots {
		if _, err := c.Git(t.Context(), root, "status", "--porcelain"); err != nil {
			t.Fatal("discovered root was not authorized", err)
		}
	}
	// Keep a nested root alive while a second open project still contains it.
	if err := os.MkdirAll(filepath.Join(workspace.Path, "container"), 0755); err != nil {
		t.Fatal(err)
	}
	container := filepath.Join(workspace.Path, "container")
	inside := filepath.Join(container, "nested")
	git("init", inside)
	if _, err := c.GitRepositories(t.Context(), workspace.Path); err != nil {
		t.Fatal(err)
	}
	second, err := c.OpenWorkspace(t.Context(), container)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CloseWorkspace(t.Context(), workspace.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Git(t.Context(), filepath.ToSlash(inside), "status", "--porcelain"); err != nil {
		t.Fatal("closing parent revoked another project's repository", err)
	}
	for _, root := range roots {
		if _, err := c.Git(t.Context(), root, "status", "--porcelain"); err == nil {
			t.Fatal("closed workspace retained repository access", root)
		}
	}
	if err := c.CloseWorkspace(t.Context(), second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Git(t.Context(), filepath.ToSlash(inside), "status", "--porcelain"); err == nil {
		t.Fatal("last workspace retained nested repository access")
	}
}
