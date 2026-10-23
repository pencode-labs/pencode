package lsp

import (
	"reflect"
	"testing"
)

func TestSemanticTokensUnicodeAndDeltas(t *testing.T) {
	source := "中😀 name\r\nnext"
	data := []uint32{0, 0, 1, 0, 0, 0, 1, 2, 1, 1, 0, 3, 4, 2, 0, 1, 0, 4, 2, 0}
	tokens, err := DecodeSemanticTokens(nil, data, source, UTF16, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []SemanticToken{{0, 3, 0, 0}, {3, 7, 1, 1}, {8, 12, 2, 0}, {14, 18, 2, 0}}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("%+v", tokens)
	}
	next, err := ApplySemanticDelta(data, []SemanticTokensEdit{{Start: 5, DeleteCount: 5, Data: []uint32{0, 1, 2, 2, 1}}})
	if err != nil || next[8] != 2 || data[8] != 1 {
		t.Fatal(next, err)
	}
	for _, bad := range [][]uint32{{1}, {0, 2, 1, 0, 0}, {0, 0, 100, 0, 0}, {0, 0, 1, 3, 0}, {0, 0, 1, 0, 0, 0, 0, 1, 0, 0}} {
		if _, err := DecodeSemanticTokens(nil, bad, source, UTF16, 3); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	for _, edits := range [][]SemanticTokensEdit{{{Start: -1}}, {{Start: len(data) + 1}}, {{Start: 0, DeleteCount: len(data) + 1}}, {{Start: 0, DeleteCount: 1}}, {{Start: 0, DeleteCount: 10}, {Start: 5, DeleteCount: 5}}} {
		if _, err := ApplySemanticDelta(data, edits); err == nil {
			t.Fatal("accepted", edits)
		}
	}
	if allocations := testing.AllocsPerRun(100, func() { tokens, _ = DecodeSemanticTokens(tokens[:0], data, source, UTF16, 3) }); allocations != 0 {
		t.Fatalf("cached semantic decoding allocated %g", allocations)
	}
}
