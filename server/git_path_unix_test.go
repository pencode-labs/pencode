//go:build darwin || linux

package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitExecutableFollowsPathChanges(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	t.Setenv("PATH", first+":"+second)
	install := func(dir string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	check := func() {
		t.Helper()
		path, err := exec.LookPath("git")
		got := gitExecutable()
		if err != nil || got != path {
			t.Fatalf("git path = %q, LookPath = %q, %v", got, path, err)
		}
	}
	install(second)
	check()
	install(first)
	check()
	if err := os.Chmod(filepath.Join(first, "git"), 0600); err != nil {
		t.Fatal(err)
	}
	check()
	t.Setenv("PATH", second)
	check()
	t.Setenv("PATH", ".:"+second)
	if got := gitExecutable(); got != "git" {
		t.Fatalf("relative PATH bypassed os/exec lookup: %q", got)
	}
}
