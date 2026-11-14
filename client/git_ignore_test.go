package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/pencode-labs/pencode/protocol"
)

func TestCheckGitIgnore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git unavailable")
	}
	c, w := peer(t, nil)
	git := func(args ...string) {
		t.Helper()
		if _, err := c.Git(t.Context(), w.Path, args...); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, content string) {
		t.Helper()
		name = filepath.Join(w.Path, name)
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write(".gitignore", "cache/\n*.log\n!keep.log\nspace name.txt\nline?break.txt\n\\!literal\n")
	write("nested/.gitignore", "*.tmp\n!keep.tmp\n")
	write(".git/info/exclude", "private.txt\n")
	global := filepath.Join(t.TempDir(), "global-ignore")
	if err := os.WriteFile(global, []byte("*.global\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("config", "core.excludesFile", global)
	for _, name := range []string{"cache/deep/file.go", "tracked.log", "space name.txt", "line\nbreak.txt", "!literal"} {
		write(name, "")
	}
	git("add", "-f", "tracked.log")
	paths := []string{"cache", "cache/deep", "cache/deep/file.go", "error.log", "keep.log", "nested/file.tmp", "nested/keep.tmp", "private.txt", "file.global", "tracked.log", "space name.txt", "line\nbreak.txt", "!literal", "main.go"}
	want := []bool{true, true, true, true, false, true, false, true, true, false, true, true, true, false}
	dst := make([]bool, 0, len(paths))
	got, err := c.CheckGitIgnore(t.Context(), w.Path, paths, dst)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("ignored=%v, want=%v, err=%v", got, want, err)
	}
	if &got[0] != &dst[:cap(dst)][0] {
		t.Fatal("ignore check did not reuse the supplied buffer")
	}
	// Git exits with status 1 when nothing matches; this is still a valid result.
	got, err = c.CheckGitIgnore(t.Context(), w.Path, []string{"main.go", "keep.log", "tracked.log"}, dst[:0])
	if err != nil || !slices.Equal(got, []bool{false, false, false}) {
		t.Fatalf("unignored paths=%v, err=%v", got, err)
	}
	for _, paths := range [][]string{{""}, {"bad\x00path"}, make([]string, protocol.IndexPageSize+1)} {
		if _, err := c.CheckGitIgnore(t.Context(), w.Path, paths, nil); err == nil {
			t.Fatal("invalid path batch accepted")
		}
	}
}
