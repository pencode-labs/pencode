package workspace

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/pencode-labs/pencode/protocol"
)

func TestPagedOpenBeyondFourGiBHasBoundedMemory(t *testing.T) {
	s, w := fixture(t)
	f, err := os.Create(filepath.Join(w.Path, "huge.txt"))
	if err != nil {
		t.Fatal(err)
	}
	const size = int64(5 << 30)
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	block := strings.Repeat("中🙂", protocol.TextPageSize/7+1)
	for _, offset := range []int64{0, 4 << 30} {
		if _, err := f.WriteAt([]byte(block), offset); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	doc, err := s.OpenDocument(w.ID, "huge.txt", protocol.DocumentText)
	runtime.ReadMemStats(&after)
	if err != nil || !doc.Page.Paged || doc.Page.Size != uint64(size) || len(doc.Content) > protocol.TextPageSize || !utf8.ValidString(doc.Content) {
		t.Fatalf("open: %+v %v", doc.Page, err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 2<<20 {
		t.Fatalf("opening allocated %d bytes", allocated)
	}
	page, err := s.ReadDocument(doc.ID, doc.Version, 4<<30)
	if err != nil || page.Page.Offset != 4<<30 || !strings.HasPrefix(page.Content, "中🙂") {
		t.Fatalf("64-bit page: %+v %v", page.Page, err)
	}
	version, err := s.Change(doc.ID, doc.Version, page.Page.Offset, 0, 3, "文")
	if err != nil {
		t.Fatal(err)
	}
	page, err = s.ReadDocument(doc.ID, version, page.Page.Offset)
	if err != nil || !strings.HasPrefix(page.Content, "文🙂") {
		t.Fatal("64-bit edit", err)
	}
	// A warm disk read allocates nothing, regardless of the source size.
	var scratch [128]byte
	if n := testing.AllocsPerRun(100, func() { _, _ = s.documents[doc.ID].paged.readAt(scratch[:], 4<<30) }); n != 0 {
		t.Fatalf("read allocated %g", n)
	}
}

func TestPagedSavePreservesUnloadedBytesAndChecksConflicts(t *testing.T) {
	s, w := fixture(t)
	name := filepath.Join(w.Path, "large.txt")
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	block := bytes.Repeat([]byte("abcdefghijklmno\n"), 4096)
	const blocks = 1100 // Beyond the old 64 MiB open limit.
	for range blocks {
		if _, err := f.Write(block); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	doc, err := s.OpenDocument(w.ID, "large.txt", protocol.DocumentText)
	if err != nil {
		t.Fatal(err)
	}
	version, err := s.Change(doc.ID, doc.Version, 0, 0, 1, "中文")
	if err != nil {
		t.Fatal(err)
	}
	p := s.documents[doc.ID].paged
	spool := p.edits.Name()
	if err := s.Save(doc.ID, version); err != nil {
		t.Fatal(err)
	}
	got, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	prefix := make([]byte, 6)
	io.ReadFull(got, prefix)
	if string(prefix) != "中文" {
		t.Fatalf("prefix %q", prefix)
	}
	// Compare every remaining byte using a fixed buffer, including unloaded pages.
	var scratch [64 << 10]byte
	remaining := int64(blocks*len(block) - 1)
	position := 1
	for remaining > 0 {
		n := int(min(int64(len(scratch)), remaining))
		if _, err := io.ReadFull(got, scratch[:n]); err != nil {
			t.Fatal(err)
		}
		for _, b := range scratch[:n] {
			if b != block[position%len(block)] {
				t.Fatal("unloaded content changed")
			}
			position++
		}
		remaining -= int64(n)
	}
	if n, _ := got.Read(scratch[:1]); n != 0 {
		t.Fatal("unexpected trailing bytes")
	}
	// Saving again uses the new file identity as its conflict baseline.
	if err := s.Save(doc.ID, version); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	assertCode(t, s.Save(doc.ID, version), protocol.Conflict)
	s.CloseDocument(doc.ID)
	if _, err := os.Stat(spool); !os.IsNotExist(err) {
		t.Fatal("edit spool leaked", err)
	}
}

func TestPagedPieceEditsMatchReference(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "original")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat("abc\n", 100)
	f.WriteString(want)
	p := &pagedDocument{file: f, size: int64(len(want)), pieces: []filePiece{{end: int64(len(want))}}}
	defer p.close()
	r := rand.New(rand.NewPCG(7, 11))
	for range 300 {
		start := r.IntN(len(want) + 1)
		end := start + r.IntN(len(want)-start+1)
		text := strings.Repeat("x", r.IntN(20))
		if err := p.replace(int64(start), int64(end), text); err != nil {
			t.Fatal(err)
		}
		want = want[:start] + text + want[end:]
		got := make([]byte, len(want))
		if _, err := p.readAt(got, 0); err != nil && err != io.EOF {
			t.Fatal(err)
		}
		if string(got) != want || p.size != int64(len(want)) {
			t.Fatalf("edit %d:%d mismatch", start, end)
		}
	}
}

func TestPagedFragmentedEditsAndReadsMatchReference(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "original")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat("abcdef\n", 2048)
	if _, err := f.WriteString(want); err != nil {
		t.Fatal(err)
	}
	p := &pagedDocument{file: f, size: int64(len(want)), pieces: []filePiece{{end: int64(len(want))}}}
	defer p.close()
	r := rand.New(rand.NewPCG(31, 57))
	for i := range 2000 {
		start := r.IntN(len(want) + 1)
		end := start + r.IntN(min(24, len(want)-start)+1)
		text := strings.Repeat(string(byte('a'+i%26)), r.IntN(25))
		if err := p.replace(int64(start), int64(end), text); err != nil {
			t.Fatal(err)
		}
		want = want[:start] + text + want[end:]
		offset := r.IntN(len(want) + 1)
		var scratch [257]byte
		n, err := p.readAt(scratch[:], int64(offset))
		length := min(len(scratch), len(want)-offset)
		if n != length || string(scratch[:n]) != want[offset:offset+length] || err != nil && err != io.EOF {
			t.Fatalf("edit %d: range read differs at %d: n=%d err=%v", i, offset, n, err)
		}
		if n < len(scratch) && err != io.EOF || p.size != int64(len(want)) {
			t.Fatalf("edit %d: wrong EOF or document size", i)
		}
	}
	got := make([]byte, len(want))
	if n, err := p.readAt(got, 0); n != len(want) || err != nil || string(got) != want {
		t.Fatal("fragmented document differs from the edited reference", err)
	}
}

func TestPagedFragmentedWarmEditsDoNotAllocate(t *testing.T) {
	p := fragmentedPagedDocument(t, 128)
	if n := testing.AllocsPerRun(100, func() {
		if err := p.replace(100, 100, "x"); err != nil {
			panic(err)
		}
		if err := p.replace(100, 101, ""); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("warm fragmented edits allocated %g times", n)
	}
}

func TestPagedSaveReleasesFragmentIndex(t *testing.T) {
	s, w := fixture(t)
	write(t, w, "fragmented.txt", strings.Repeat("a", protocol.TextPagingThreshold+1))
	doc, err := s.OpenDocument(w.ID, "fragmented.txt", protocol.DocumentText)
	if err != nil {
		t.Fatal(err)
	}
	version := doc.Version
	for i := range 512 {
		version, err = s.Change(doc.ID, version, 0, uint32(i*64), uint32(i*64+1), "b")
		if err != nil {
			t.Fatal(err)
		}
	}
	p := s.documents[doc.ID].paged
	if err := s.Save(doc.ID, version); err != nil {
		t.Fatal(err)
	}
	if len(p.pieces) != 1 || cap(p.pieces) > pagedPieceSpareLimit || p.edits != nil {
		t.Fatal("save retained fragmented metadata or the edit spool")
	}
	page, err := s.ReadDocument(doc.ID, version, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 512 {
		if page.Content[i*64] != 'b' || page.Content[i*64+1] != 'a' {
			t.Fatal("save changed fragmented content")
		}
	}
}

func TestPagedUTF8BoundariesAndNoNewline(t *testing.T) {
	s, w := fixture(t)
	text := strings.Repeat("中🙂", protocol.TextPagingThreshold/7+100)
	write(t, w, "unicode.txt", text)
	doc, err := s.OpenDocument(w.ID, "unicode.txt", protocol.DocumentText)
	if err != nil {
		t.Fatal(err)
	}
	for _, offset := range []uint64{1, 2, 3, 4, 5, 6, protocol.TextPageSize - 1, uint64(len(text) - 1)} {
		page, err := s.ReadDocument(doc.ID, doc.Version, offset)
		if err != nil || !utf8.ValidString(page.Content) || len(page.Content) > protocol.TextPageSize || !strings.HasPrefix(text[page.Page.Offset:], page.Content) {
			t.Fatalf("offset %d: %+v %v", offset, page.Page, err)
		}
	}
	if _, err := s.Change(doc.ID, doc.Version, 0, 1, 3, "x"); err == nil {
		t.Fatal("accepted split rune")
	}
}

func TestPagedWarmAppendAllocations(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "original")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("original")
	p := &pagedDocument{file: f, size: 8, pieces: []filePiece{{end: 8}}}
	defer p.close()
	for range 10 {
		if err := p.replace(p.size, p.size, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if n := testing.AllocsPerRun(100, func() {
		if err := p.replace(p.size, p.size, "x"); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("warm append allocated %g times", n)
	}
	if len(p.pieces) != 2 {
		t.Fatalf("consecutive typing fragmented into %d pieces", len(p.pieces))
	}
}

func fragmentedPagedDocument(t testing.TB, count int) *pagedDocument {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "original")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("a", count*16+1)
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	p := &pagedDocument{file: f, size: int64(len(text)), pieces: []filePiece{{end: int64(len(text))}}}
	t.Cleanup(p.close)
	for i := range count {
		position := int64((i*4051)%count*16 + 1)
		if err := p.replace(position, position+1, "b"); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func BenchmarkPagedFragments(b *testing.B) {
	for _, count := range []int{128, 4096} {
		for _, operation := range []string{"read-tail", "replace", "insert-delete", "append"} {
			b.Run(fmt.Sprintf("edits=%d/%s", count, operation), func(b *testing.B) {
				p := fragmentedPagedDocument(b, count)
				var scratch [1]byte
				i := 0
				b.ReportAllocs()
				for b.Loop() {
					position := int64((i*4051)%count*16 + 1)
					i++
					var err error
					switch operation {
					case "read-tail":
						_, err = p.readAt(scratch[:], p.size-1)
					case "replace":
						err = p.replace(position, position+1, "c")
					case "insert-delete":
						err = p.replace(position, position, "c")
						if err == nil {
							err = p.replace(position, position+1, "")
						}
					case "append":
						err = p.replace(p.size, p.size, "c")
					}
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
