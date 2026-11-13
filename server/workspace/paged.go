package workspace

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"slices"
	"sort"
	"unicode/utf8"

	"github.com/pencode-labs/pencode/internal/buffer"
	"github.com/pencode-labs/pencode/protocol"
)

const pagedPieceSpareLimit = 256

// Pieces reference the original file or an append-only edit spool. Neither
// opening nor saving retains a copy of the entire file in the Go heap.
type filePiece struct {
	// Negative offsets are complemented positions in the edit spool.
	// end is the exclusive logical offset, making pieces a searchable index.
	offset, end int64
}

type pagedDocument struct {
	file, edits *os.File
	info        fs.FileInfo
	pieces      []filePiece
	page        protocol.DocumentPage
	size        int64
	editEnd     int64
	forced      bool
}

func (p *pagedDocument) close() {
	_ = p.file.Close()
	if p.edits != nil {
		name := p.edits.Name()
		_ = p.edits.Close()
		_ = os.Remove(name)
	}
}

func (p *pagedDocument) readAt(dst []byte, offset int64) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	n := 0
	for i := p.pieceAt(offset); i < len(p.pieces); i++ {
		piece := p.pieces[i]
		start := int64(0)
		if i > 0 {
			start = p.pieces[i-1].end
		}
		f := p.file
		position := piece.offset
		if position < 0 {
			f = p.edits
			position = ^position
		}
		count := min(int64(len(dst)-n), piece.end-offset)
		read, err := f.ReadAt(dst[n:n+int(count)], position+offset-start)
		n += read
		if err != nil {
			return n, err
		}
		if n == len(dst) {
			return n, nil
		}
		offset = piece.end
	}
	return n, io.EOF
}

func (p *pagedDocument) replace(start, end int64, text string) error {
	if start == end && text == "" {
		return nil
	}
	var inserted filePiece
	if text != "" {
		if p.edits == nil {
			var err error
			p.edits, err = os.CreateTemp("", "pencode-edits-*")
			if err != nil {
				return err
			}
		}
		offset := p.editEnd
		n, err := p.edits.WriteString(text)
		p.editEnd += int64(n)
		if err != nil {
			return err
		}
		inserted = filePiece{offset: ^offset, end: start + int64(len(text))}
	}
	first, last := p.pieceAt(start), p.pieceAt(end)
	left, right := first, min(last+1, len(p.pieces))
	delta := int64(len(text)) - (end - start)
	var storage [5]filePiece
	// Reconcile both neighbours with the edited prefix, insertion and suffix.
	// Equal-length edits leave the tail's cumulative offsets unchanged.
	parts := storage[:0]
	base := int64(0)
	if left > 0 {
		left--
		if left > 0 {
			base = p.pieces[left-1].end
		}
		parts = appendFilePiece(parts, p.pieces[left], base)
	}
	if first < len(p.pieces) {
		prefix := p.pieces[first]
		prefix.end = start
		parts = appendFilePiece(parts, prefix, base)
	}
	if text != "" {
		parts = appendFilePiece(parts, inserted, base)
	}
	if last < len(p.pieces) {
		position := int64(0)
		if last > 0 {
			position = p.pieces[last-1].end
		}
		suffix := p.pieces[last]
		suffix.offset = suffix.advance(end - position)
		suffix.end += delta
		parts = appendFilePiece(parts, suffix, base)
	}
	if right < len(p.pieces) {
		next := p.pieces[right]
		next.end += delta
		parts = appendFilePiece(parts, next, base)
		right++
	}
	length := left + len(parts) + len(p.pieces) - right
	pieces := slices.Grow(p.pieces, max(0, length-len(p.pieces)))[:length]
	copy(pieces[left+len(parts):], p.pieces[right:])
	copy(pieces[left:], parts)
	if delta != 0 {
		for i := left + len(parts); i < len(pieces); i++ {
			pieces[i].end += delta
		}
	}
	p.pieces = pieces
	p.size += delta
	p.page.Size = uint64(p.size)
	return nil
}

func (p *pagedDocument) pieceAt(offset int64) int {
	return sort.Search(len(p.pieces), func(i int) bool { return p.pieces[i].end > offset })
}

func (piece filePiece) advance(n int64) int64 {
	if piece.offset < 0 {
		return piece.offset - n
	}
	return piece.offset + n
}

func appendFilePiece(pieces []filePiece, next filePiece, base int64) []filePiece {
	start := base
	if len(pieces) != 0 {
		start = pieces[len(pieces)-1].end
	}
	if next.end == start {
		return pieces
	}
	if n := len(pieces); n > 0 {
		last := &pieces[n-1]
		previous := base
		if n > 1 {
			previous = pieces[n-2].end
		}
		if last.advance(last.end-previous) == next.offset {
			last.end = next.end
			return pieces
		}
	}
	return append(pieces, next)
}

func (p *pagedDocument) check(root *os.Root, name string) error {
	info, err := root.Stat(name)
	if err != nil {
		return err
	}
	if !os.SameFile(info, p.info) || info.Size() != p.info.Size() || !info.ModTime().Equal(p.info.ModTime()) {
		return failure(protocol.Conflict, "文件已被其他程序修改，请重新加载")
	}
	return nil
}

func (p *pagedDocument) readPage(offset uint64) (string, error) {
	start := min(offset, uint64(p.size))
	if start == uint64(p.size) && start > 0 {
		start--
	}
	// Include room to back up to the start of a UTF-8 rune.
	var edge [utf8.UTFMax]byte
	back := min(start, utf8.UTFMax-1)
	n := min(uint64(len(edge)), uint64(p.size)-start+back)
	if _, err := p.readAt(edge[:n], int64(start-back)); err != nil && err != io.EOF {
		return "", err
	}
	for back > 0 && !utf8.RuneStart(edge[back]) {
		back--
		start--
	}
	// A page is intentionally bounded even for a single multi-gigabyte line.
	storage := buffer.Get(int(min(uint64(protocol.TextPageSize+utf8.UTFMax), uint64(p.size)-start)))
	defer buffer.Put(storage)
	data := storage.Bytes
	if _, err := p.readAt(data, int64(start)); err != nil && err != io.EOF {
		return "", err
	}
	end := min(len(data), protocol.TextPageSize)
	if start+uint64(end) < uint64(p.size) {
		for end > 0 && !utf8.RuneStart(data[end]) {
			end--
		}
		// Prefer a complete line while keeping long lines bounded.
		if newline := bytes.LastIndexByte(data[:end], '\n'); newline >= end/2 {
			end = newline + 1
		}
	}
	if end < len(data) && end > 0 && data[end-1] == '\r' && data[end] == '\n' {
		end--
	}
	data = data[:end]
	if !p.forced && (bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data)) {
		return "", errUnsupportedText
	}
	text := string(data)
	if !utf8.ValidString(text) {
		return "", failure(protocol.Invalid, "此分段使用不支持的文本编码")
	}
	p.page = protocol.DocumentPage{Offset: start, Size: uint64(p.size), Paged: true}
	return text, nil
}

func (s *Store) ReadDocument(id, version, offset uint64) (protocol.Document, error) {
	d := s.documents[id]
	if d == nil {
		return protocol.Document{}, failure(protocol.NotFound, "文档已关闭")
	}
	if version != d.version {
		return protocol.Document{}, failure(protocol.VersionMismatch, "文档版本已改变")
	}
	if d.paged == nil {
		return protocol.Document{}, failure(protocol.Invalid, "文档未分页")
	}
	if err := d.paged.check(d.project.root, d.path); err != nil {
		return protocol.Document{}, err
	}
	text, err := d.paged.readPage(offset)
	return protocol.Document{ID: id, Version: version, Content: text, Page: d.paged.page}, err
}

// change uses absolute 64-bit page offsets; reading or cancelling a viewport
// never changes the target of a queued edit.
func (p *pagedDocument) change(offset uint64, start, end uint32, text string) error {
	if start > end || offset > uint64(p.size) || uint64(end) > uint64(p.size)-offset || !utf8.ValidString(text) {
		return failure(protocol.Invalid, "无效的 UTF-8 编辑范围")
	}
	var edge [1]byte
	for _, local := range [...]uint32{start, end} {
		position := offset + uint64(local)
		if position == uint64(p.size) {
			continue
		}
		if _, err := p.readAt(edge[:], int64(position)); err != nil {
			return err
		}
		if !utf8.RuneStart(edge[0]) {
			return failure(protocol.Invalid, "无效的 UTF-8 编辑范围")
		}
	}
	return p.replace(int64(offset)+int64(start), int64(offset)+int64(end), text)
}
