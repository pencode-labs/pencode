package server

import (
	"context"
	"github.com/diiyw/onix/projects/pencode/protocol"
	"github.com/odvcencio/gotreesitter"
	"strings"
	"testing"
)

func TestSyntaxCaptureDeltaTransfer(t *testing.T) {
	services := newServices(context.Background(), Options{})
	defer services.close()
	source := "package demo\n" + strings.Repeat("// 中文🙂 comment\n", 2000) + "var Value=1\n"
	analyze := func(version uint64, start, end int, text string) protocol.Encoder {
		t.Helper()
		request := protocol.Encoder{}
		request.U64(1)
		request.U64(version)
		request.String("main.go")
		request.U32(uint32(start))
		request.U32(uint32(end))
		request.String(text)
		decoder := protocol.Decoder{Buffer: request.Buffer}
		response := protocol.Encoder{}
		if err := services.analyze(t.Context(), protocol.SyntaxAnalyze, &decoder, &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	first := analyze(0, 0, 0, source)
	// A one-character edit near the start shifts thousands of unchanged captures.
	offset := strings.Index(source, "中文")
	second := analyze(1, offset, offset, "字")
	if len(second.Buffer) > len(first.Buffer)/20 {
		t.Fatalf("delta response=%d initial=%d", len(second.Buffer), len(first.Buffer))
	}
	decoder := protocol.Decoder{Buffer: second.Buffer}
	if decoder.U64() != 2 || decoder.String() != "go" {
		t.Fatal("invalid delta response header")
	}
	prefix, removed, inserted := decoder.U32(), decoder.U32(), decoder.U32()
	if prefix == 0 || removed > 1 || inserted > 1 {
		t.Fatalf("single comment edit invalidated unrelated captures: %d %d %d", prefix, removed, inserted)
	}
	t.Logf("capture transfer: initial=%d bytes, edit=%d bytes", len(first.Buffer), len(second.Buffer))
}

func TestSyntaxCaptureDeltaMatchesShiftedSuffix(t *testing.T) {
	before := []gotreesitter.HighlightRange{{StartByte: 0, EndByte: 2, Capture: "keyword"}, {StartByte: 3, EndByte: 4, Capture: "variable"}, {StartByte: 5, EndByte: 8, Capture: "string"}}
	for _, test := range []struct {
		after                 []gotreesitter.HighlightRange
		shift, prefix, suffix int
	}{
		{before, 0, 3, 0},
		{[]gotreesitter.HighlightRange{{StartByte: 0, EndByte: 2, Capture: "keyword"}, {StartByte: 3, EndByte: 5, Capture: "variable"}, {StartByte: 6, EndByte: 9, Capture: "string"}}, 1, 1, 1},
		{[]gotreesitter.HighlightRange{{StartByte: 0, EndByte: 9, Capture: "comment"}}, 1, 0, 0},
		{nil, -8, 0, 0},
	} {
		prefix, suffix := syntaxCaptureDelta(before, test.after, test.shift)
		if prefix != test.prefix || suffix != test.suffix {
			t.Fatalf("delta=(%d,%d) want=(%d,%d)", prefix, suffix, test.prefix, test.suffix)
		}
	}
}
