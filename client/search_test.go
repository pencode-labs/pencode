package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pencode-labs/pencode/protocol"
	"github.com/pencode-labs/pencode/textsearch"
)

func TestSearchTextScopeBuffersAndLimits(t *testing.T) {
	c, w := peer(t, nil)
	write := func(name, value string) {
		t.Helper()
		filename := filepath.Join(w.Path, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/a.txt", "old disk\n")
	write("src/nested/b.txt", "中🙂 Hello hello\r\nhello")
	write("src/.git/config", "hello")
	write("src/node_modules/a.txt", "hello")
	write("other/a.txt", "hello")
	write("src/raw.dat", "hello\x00hello")
	documents := []protocol.SearchDocument{{Path: "src/a.txt", Content: "HELLO unsaved"}, {Path: "src/removed.txt", Content: "hello"}, {Path: "other/b.txt", Content: "hello"}}
	r, err := c.SearchText(t.Context(), w.Path, "src", "hello", textsearch.Options{}, documents)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Files) != 3 || r.Skipped != 1 || r.Truncated {
		t.Fatalf("result=%+v", r)
	}
	count := 0
	for _, f := range r.Files {
		count += len(f.Matches)
		if len(f.Digest) != 32 || strings.HasPrefix(f.Path, "other/") {
			t.Fatalf("file=%+v", f)
		}
		if f.Path == "src/nested/b.txt" {
			if f.Matches[0].Column != 4 || f.Matches[2].Line != 2 {
				t.Fatalf("Unicode/CRLF locations=%+v", f.Matches)
			}
		}
	}
	if count != 5 {
		t.Fatal(count)
	}
	if _, err := c.SearchText(t.Context(), w.Path, "../", "hello", textsearch.Options{}, nil); err == nil {
		t.Fatal("scope escaped workspace")
	}
	if _, err := c.SearchText(t.Context(), w.Path, "src", "[", textsearch.Options{Regex: true}, nil); err == nil {
		t.Fatal("invalid regex succeeded")
	}
	write("many.txt", strings.Repeat("a ", protocol.TextSearchLimit+2))
	r, err = c.SearchText(t.Context(), w.Path, "", "a", textsearch.Options{WholeWord: true}, nil)
	if err != nil || !r.Truncated {
		t.Fatal("missing truncation", err)
	}
}
