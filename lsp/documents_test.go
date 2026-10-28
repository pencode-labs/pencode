package lsp

import (
	"encoding/json"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPositionEncodingsAndURIs(t *testing.T) {
	text := "中😀x\r\nβ\n"
	for _, test := range []struct {
		encoding PositionEncoding
		offset   int
		position Position
	}{{UTF8, 7, Position{0, 7}}, {UTF16, 7, Position{0, 3}}, {UTF32, 7, Position{0, 2}}, {UTF16, 12, Position{1, 1}}, {UTF16, len(text), Position{2, 0}}} {
		p, err := PositionAt(text, test.offset, test.encoding)
		if err != nil || p != test.position {
			t.Fatalf("%+v: %+v %v", test, p, err)
		}
		offset, err := OffsetAt(text, p, test.encoding)
		if err != nil || offset != test.offset {
			t.Fatalf("%+v: %d %v", test, offset, err)
		}
	}
	for _, p := range []Position{{-1, 0}, {0, -1}, {0, 2}, {0, 9}, {9, 0}} {
		if _, err := OffsetAt(text, p, UTF16); err == nil {
			t.Fatal("accepted", p)
		}
	}
	if _, err := PositionAt(text, 2, UTF16); err == nil {
		t.Fatal("accepted a byte offset between CRLF bytes")
	}
	path := t.TempDir() + "/space #中😀.go"
	uri := FileURI(path)
	got, err := uri.Path()
	if err != nil || got != path {
		t.Fatalf("%s %s %v", uri, got, err)
	}
	for _, uri := range []URI{"https://example.com/a", "file://elsewhere/a", "file:///a?b", "file:///a#b", "file:relative", "file:///a%00b"} {
		if _, err := uri.Path(); err == nil {
			t.Fatal("accepted", uri)
		}
	}
}
func TestApplyEditsAtomicAndOrdered(t *testing.T) {
	text := "中😀 value\r\nnext"
	edits := []TextEdit{{Range: Range{Position{0, 4}, Position{0, 9}}, NewText: "name"}, {Range: Range{Position{0, 0}, Position{0, 0}}, NewText: "a"}, {Range: Range{Position{0, 0}, Position{0, 0}}, NewText: "b"}}
	got, err := ApplyTextEdits(text, edits, UTF16)
	if err != nil || got != "ab中😀 name\r\nnext" {
		t.Fatalf("%q %v", got, err)
	}
	overlapping := []TextEdit{{Range: Range{Position{0, 0}, Position{0, 4}}}, {Range: Range{Position{0, 3}, Position{0, 5}}}}
	if _, err := ApplyTextEdits(text, overlapping, UTF16); err == nil {
		t.Fatal("overlap accepted")
	}
	if got, err := ApplyTextEdits(text, nil, UTF16); err != nil || got != text {
		t.Fatal(got, err)
	}
}
func TestIncrementalChangesReconstructUnicodeAndLineEndings(t *testing.T) {
	words := []string{"", "a", "中", "😀", "\r\n", "\n", "β", "\r"}
	random := rand.New(rand.NewPCG(11, 39))
	for range 2000 {
		var a, b strings.Builder
		for range random.IntN(20) {
			a.WriteString(words[random.IntN(len(words))])
		}
		for range random.IntN(20) {
			b.WriteString(words[random.IntN(len(words))])
		}
		old, next := a.String(), b.String()
		for _, encoding := range []PositionEncoding{UTF8, UTF16, UTF32} {
			r, replacement := IncrementalChange(old, next, encoding)
			if !utf8.ValidString(replacement) {
				t.Fatal("split rune")
			}
			got, err := ApplyTextEdits(old, []TextEdit{{r, replacement}}, encoding)
			if err != nil || got != next {
				t.Fatalf("%q -> %q: %+v %q => %q %v", old, next, r, replacement, got, err)
			}
		}
	}
}
func TestConfigurationSections(t *testing.T) {
	config := json.RawMessage(`{"gopls":{"analyses":{"unusedparams":true}}}`)
	if got := string(configurationSection(config, "gopls.analyses")); got != `{"unusedparams":true}` {
		t.Fatal(got)
	}
	if got := string(configurationSection(config, "missing")); got != "null" {
		t.Fatal(got)
	}
}

var benchmarkChange Range
var benchmarkReplacement string

func BenchmarkIncrementalTail(b *testing.B) {
	for _, size := range []int{64 << 10, 512 << 10, 2 << 20} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			line := "let value = 42; // 中文🙂\n"
			old := strings.Repeat(line, size/len(line))
			next := old + "x"
			b.ReportAllocs()
			for b.Loop() {
				benchmarkChange, benchmarkReplacement = IncrementalChange(old, next, UTF16)
			}
		})
	}
}
