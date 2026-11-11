package workspace

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"unicode/utf8"

	"github.com/pencode-labs/pencode/internal/buffer"
	"github.com/pencode-labs/pencode/protocol"
)

// Pieces reference the original file or an append-only edit spool. Neither
// opening nor saving retains a copy of the entire file in the Go heap.
type filePiece struct {
	offset, length int64
	added          bool
}

type pagedDocument struct {
	file, edits     *os.File
	info            fs.FileInfo
	pieces, scratch []filePiece
	page            protocol.DocumentPage
	size            int64
	editEnd         int64
	forced          bool
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
	n := 0
	for _, piece := range p.pieces {
		if offset >= piece.length {
			offset -= piece.length
			continue
		}
		f := p.file
		if piece.added {
			f = p.edits
		}
		count := min(int64(len(dst)-n), piece.length-offset)
		read, err := f.ReadAt(dst[n:n+int(count)], piece.offset+offset)
		n += read
		if err != nil {
			return n, err
		}
		if n == len(dst) {
			return n, nil
		}
		offset = 0
	}
	return n, io.EOF
}

func (p *pagedDocument) replace(start, end int64, text string) error {
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
		inserted = filePiece{offset: offset, length: int64(len(text)), added: true}
	}
	pieces := p.scratch[:0]
	position := int64(0)
	for _, piece := range p.pieces {
		next := position + piece.length
		if position < start {
			prefix := piece
			prefix.length = min(next, start) - position
			pieces = appendFilePiece(pieces, prefix)
		}
		if inserted.length != 0 && next >= start {
			pieces = appendFilePiece(pieces, inserted)
			inserted.length = 0
		}
		if next > end {
			suffix := piece
			skip := max(end-position, 0)
			suffix.offset += skip
			suffix.length -= skip
			pieces = appendFilePiece(pieces, suffix)
		}
		position = next
	}
	if inserted.length != 0 {
		pieces = appendFilePiece(pieces, inserted)
	}
	p.pieces, p.scratch = pieces, p.pieces[:0]
	p.size += int64(len(text)) - (end - start)
	p.page.Size = uint64(p.size)
	return nil
}

func appendFilePiece(pieces []filePiece, next filePiece) []filePiece {
	if next.length == 0 {
		return pieces
	}
	if n := len(pieces); n > 0 {
		last := &pieces[n-1]
		if last.added == next.added && last.offset+last.length == next.offset {
			last.length += next.length
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
