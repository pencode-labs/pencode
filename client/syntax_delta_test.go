package client

import (
	"github.com/diiyw/onix/projects/pencode/protocol"
	"github.com/diiyw/onix/syntax"
	"reflect"
	"strings"
	"testing"
)

func TestRemoteSyntaxCaptureEditsMatchFullParse(t *testing.T) {
	client, _ := peer(t, nil)
	remote := client.NewSyntax(t.Context())
	defer remote.Close()
	theme, _ := syntax.BuiltinTheme("monokai")
	// These edits shift suffixes, change token counts, open/close multiline
	// comments, change grammars, remove everything and restart after release.
	for _, test := range []struct{ path, source string }{
		{"main.go", "package demo\nvar 名字=1\nfunc f(){println(名字)}\n"},
		{"main.go", "// 中文🙂\npackage demo\nvar 名字=1\nfunc f(){println(名字)}\n"},
		{"main.go", "/* 中文🙂\npackage demo\nvar 名字=1\nfunc f(){println(名字)}\n"},
		{"main.go", "/* 中文🙂 */\npackage demo\nvar 名字=42\nfunc f(){println(名字)}\n"},
		{"main.go", "package demo\n"},
		{"main.go", ""},
		{"other.py", "name = '中文🙂'\nprint(name)\n"},
		{"plain.txt", "ordinary text\n"},
		{"main.go", "package demo\nvar 名字=1\n"},
	} {
		if err := remote.Update(test.path, test.source); err != nil {
			t.Fatal(test.path, err)
		}
		var full syntax.Document
		if err := full.Update(test.path, test.source); err != nil {
			t.Fatal(err)
		}
		expected := full.Tokens(theme, nil)
		actual := remote.Tokens(theme, nil)
		if !reflect.DeepEqual(actual, expected) && !(len(actual) == 0 && len(expected) == 0) {
			t.Fatalf("delta differs from full parse for %q:\n got=%+v\nwant=%+v", test.source, actual, expected)
		}
		full.Close()
	}
}

func TestSyntaxRejectsMalformedCaptureDelta(t *testing.T) {
	for _, kind := range []string{"prefix", "removed", "count", "capture-id", "reversed", "outside", "overlap", "suffix-shift", "truncated", "trailing"} {
		t.Run(kind, func(t *testing.T) {
			session := Syntax{source: "abcd", captures: []syntax.Token{{Start: 0, End: 1, Capture: "a"}, {Start: 2, End: 3, Capture: "a"}}, names: []string{"a"}}
			prefix, removed, count := uint32(0), uint32(2), uint32(1)
			start, end, id := uint32(0), uint32(1), uint32(0)
			source := "abcd"
			switch kind {
			case "prefix":
				prefix = 3
			case "removed":
				removed = 3
			case "count":
				count = 100
			case "capture-id":
				id = 2
			case "reversed":
				start, end = 2, 1
			case "outside":
				end = 5
			case "overlap":
				prefix, removed = 1, 1
			case "suffix-shift":
				prefix, removed, count = 0, 0, 0
				source = ""
			}
			encoded := protocol.Encoder{}
			encoded.U32(prefix)
			encoded.U32(removed)
			encoded.U32(count)
			if count != 0 {
				encoded.U32(start)
				encoded.U32(end)
				encoded.U32(id)
			}
			if kind == "truncated" {
				encoded.Buffer = encoded.Buffer[:len(encoded.Buffer)-1]
			}
			if kind == "trailing" {
				encoded.U32(0)
			}
			decoder := protocol.Decoder{Buffer: encoded.Buffer}
			if err := session.applyCaptureDelta(&decoder, source); err == nil {
				t.Fatal("malformed capture delta accepted")
			}
		})
	}
}

func BenchmarkSyntaxCaptureDeltaApply(b *testing.B) {
	source := strings.Repeat("word ", 10000)
	session := Syntax{source: source, captures: make([]syntax.Token, 10000), names: []string{"variable"}}
	for i := range session.captures {
		session.captures[i] = syntax.Token{Start: i * 5, End: i*5 + 4, Capture: "variable"}
	}
	encoded := protocol.Encoder{}
	encoded.U32(5000)
	encoded.U32(1)
	encoded.U32(1)
	encoded.U32(25000)
	encoded.U32(25004)
	encoded.U32(0)
	b.ReportAllocs()
	for b.Loop() {
		decoder := protocol.Decoder{Buffer: encoded.Buffer}
		if err := session.applyCaptureDelta(&decoder, source); err != nil {
			b.Fatal(err)
		}
	}
}
