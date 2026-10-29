package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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
	for _, regex := range []bool{false, true} {
		r, err = c.SearchText(t.Context(), w.Path, "", "a", textsearch.Options{WholeWord: true, Regex: regex}, nil)
		if err != nil || !r.Truncated {
			t.Fatal("missing truncation", err)
		}
		found := false
		for _, file := range r.Files {
			if file.Path == "many.txt" {
				found = true
				if file.Count != protocol.TextSearchLimit+2 {
					t.Fatal("truncated search lost the exact count", file.Count)
				}
			}
		}
		if !found {
			t.Fatal("missing truncated search file")
		}
	}
}

func TestSearchPreviewContainsExactMatch(t *testing.T) {
	c, w := peer(t, nil)
	for _, test := range []struct {
		name, content, query string
		options              textsearch.Options
	}{
		{"long.txt", "target" + strings.Repeat(" tail", 200), "target", textsearch.Options{}},
		{"unicode.txt", strings.Repeat("中🙂", 80) + "TARGET target\r\n", "target", textsearch.Options{}},
		{"regex.txt", "prefix 12345 suffix", `\d+`, textsearch.Options{Regex: true}},
		{"multiline.txt", "first target\nnext line", "target\nnext", textsearch.Options{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(w.Path, test.name), []byte(test.content), 0644); err != nil {
				t.Fatal(err)
			}
			result, err := c.SearchText(t.Context(), w.Path, "", test.query, test.options, nil)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, file := range result.Files {
				if file.Path != test.name {
					continue
				}
				for _, match := range file.Matches {
					found = true
					start := match.Start - match.PreviewStart
					end := min(len(match.Preview), match.End-match.PreviewStart)
					if !utf8.ValidString(match.Preview) || start < 0 || start >= len(match.Preview) || end <= start || match.Preview[start:end] != test.content[match.Start:match.PreviewStart+end] {
						t.Fatalf("preview lost the match: %+v", match)
					}
				}
			}
			if !found {
				t.Fatal("no result for preview fixture")
			}
		})
	}
}
