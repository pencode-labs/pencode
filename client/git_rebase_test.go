package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitRebasingWorktreeIsolation(t *testing.T) {
	c, workspace := peer(t, nil)
	git := func(root string, args ...string) string {
		t.Helper()
		data, err := c.Git(t.Context(), root, args...)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	git(workspace.Path, "init", "-b", "main")
	git(workspace.Path, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "base")
	linked := filepath.Join(t.TempDir(), "linked")
	git(workspace.Path, "worktree", "add", "-b", "topic", linked)
	opened, err := c.OpenWorkspace(t.Context(), linked)
	if err != nil {
		t.Fatal(err)
	}
	check := func(root string, want bool) {
		t.Helper()
		if got, err := c.GitRebasing(t.Context(), root); err != nil || got != want {
			t.Fatalf("GitRebasing(%q)=%v, %v; want %v", root, got, err, want)
		}
	}
	check(workspace.Path, false)
	check(opened.Path, false)
	for _, state := range []string{"rebase-merge", "rebase-apply"} {
		// The linked worktree stores state in the main repository's metadata,
		// never in a .git directory inside the linked checkout.
		directory := filepath.Join(workspace.Path, ".git", "worktrees", "linked", state)
		if err := os.Mkdir(directory, 0755); err != nil {
			t.Fatal(err)
		}
		check(opened.Path, true)
		check(workspace.Path, false)
		if err := os.Remove(directory); err != nil {
			t.Fatal(err)
		}
		check(opened.Path, false)
	}
	// REBASE_HEAD can remain after rebase completion and is not a state flag.
	git(workspace.Path, "update-ref", "REBASE_HEAD", "HEAD")
	check(workspace.Path, false)
}
