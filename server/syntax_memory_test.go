package server

import (
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/pencode-labs/pencode/protocol"
)

func syntaxMemorySession(t testing.TB) (*services, func(int, int, string)) {
	t.Helper()
	s := newServices(t.Context(), Options{})
	t.Cleanup(s.close)
	var request, response protocol.Encoder
	var version uint64
	return s, func(start, end int, text string) {
		t.Helper()
		request.Buffer, response.Buffer = request.Buffer[:0], response.Buffer[:0]
		request.U64(1)
		request.U64(version)
		request.String("main.go")
		request.U32(uint32(start))
		request.U32(uint32(end))
		request.String(text)
		decoder := protocol.Decoder{Buffer: request.Buffer}
		if err := s.analyze(t.Context(), protocol.SyntaxAnalyze, &decoder, &response); err != nil {
			t.Fatal(err)
		}
		version++
	}
}

func TestSyntaxSnapshotEditsAndShrinkReleaseStorage(t *testing.T) {
	s, apply := syntaxMemorySession(t)
	source := "package demo\n" + strings.Repeat("// comment text\n", 100000)
	apply(0, 0, source)
	offset := strings.Index(source, "comment")
	before := s.syntax[1].source
	apply(offset, offset+1, "x")
	if s.syntax[1].source.Materialized() || before.ByteAt(offset) != 'c' {
		t.Fatal("incremental syntax edit materialized or mutated its preceding snapshot")
	}
	apply(0, s.syntax[1].source.Len(), "package demo\n")
	if cap(s.syntax[1].previous) > 4096 || s.syntax[1].source.Len() != 13 {
		t.Fatal("short document retained its previous capture array")
	}
	apply(8, 12, "test")
	if got := s.syntax[1].source.Slice(0, 13); got != "package test\n" {
		t.Fatal("syntax session stopped updating after shrinking", got)
	}
}

func BenchmarkSyntaxIncrementalEdit(b *testing.B) {
	for _, size := range []int{64 << 10, 2 << 20} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			_, apply := syntaxMemorySession(b)
			source := "package demo\n" + strings.Repeat("// comment text\n", size/16)
			apply(0, 0, source)
			offset := strings.Index(source, "comment")
			apply(offset, offset+1, "x")
			apply(offset, offset+1, "y")
			index := 1
			b.ReportAllocs()
			for b.Loop() {
				index = 1 - index
				apply(offset, offset+1, [...]string{"x", "y"}[index])
			}
		})
	}
}

func BenchmarkSyntaxReopenAfterGC(b *testing.B) {
	s := newServices(b.Context(), Options{})
	b.Cleanup(s.close)
	var request, release, response protocol.Encoder
	request.U64(1)
	request.U64(0)
	request.String("main.go")
	request.U32(0)
	request.U32(0)
	request.String("package main\nimport \"fmt\"\nfunc main() { fmt.Println(1) }\n")
	release.U64(1)
	openClose := func() {
		response.Reset()
		d := protocol.Decoder{Buffer: request.Buffer}
		if err := s.analyze(b.Context(), protocol.SyntaxAnalyze, &d, &response); err != nil {
			b.Fatal(err)
		}
		d = protocol.Decoder{Buffer: release.Buffer}
		if err := s.analyze(b.Context(), protocol.SyntaxRelease, &d, &response); err != nil {
			b.Fatal(err)
		}
	}
	openClose()
	b.ReportAllocs()
	for b.Loop() {
		runtime.GC()
		runtime.GC()
		openClose()
	}
}
