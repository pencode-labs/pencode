package server

import (
	"bufio"
	"context"
	"io"
	"strings"

	"github.com/pencode-labs/pencode/internal/ripgrep"
	"github.com/pencode-labs/pencode/protocol"
)

// Stream the immutable index in tree order, preserving the snapshot selected
// by the client. NUL separates paths even when a filename contains a newline.
type indexPaths struct {
	parts   [3]string
	entries []indexEntry
}

func (r *indexPaths) Read(buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		for i, part := range r.parts {
			used := copy(buf[n:], part)
			r.parts[i] = part[used:]
			n += used
			if n == len(buf) {
				return n, nil
			}
		}
		if len(r.entries) == 0 {
			return n, io.EOF
		}
		entry := r.entries[0]
		r.entries = r.entries[1:]
		if !entry.isDir() {
			r.parts = [3]string{entry.directory.prefix, entry.filename, "\x00"}
		}
	}
	return n, nil
}

func (p *serviceIndex) searchPaths(ctx context.Context, d *protocol.Decoder, e *protocol.Encoder) error {
	query := strings.ReplaceAll(d.String(), `\`, "/")
	root := strings.TrimRight(strings.ReplaceAll(p.root, `\`, "/"), "/")
	if len(query) > len(root) && query[len(root)] == '/' && strings.EqualFold(query[:len(root)], root) {
		query = query[len(root)+1:]
	}
	count := d.U32()
	if count > 128 {
		return protocol.ErrProtocol
	}
	preferred := make(map[string]int, count)
	for i := range count {
		preferred[d.String()] = int(i)
	}
	if err := d.Done(); err != nil {
		return err
	}
	if strings.IndexByte(query, 0) >= 0 {
		e.U32(0)
		return nil
	}
	p.mu.Lock()
	paths := indexPaths{entries: p.entries}
	p.mu.Unlock()

	var ranked, other [128]string
	n, recent := 0, 0
	args := []string{"--null-data", "--fixed-strings", "--ignore-case", "--text", "--encoding=none"}
	if count == 0 {
		args = append(args, "--max-count=128")
	}
	args = append(args, "-e", query, "--", "-")
	err := ripgrep.Run(ctx, "", &paths, args, func(r *bufio.Reader) error {
		return ripgrep.Records(r, func(path []byte) error {
			if i, ok := preferred[string(path)]; ok {
				ranked[i] = string(path)
				recent++
			} else if n < len(other) {
				other[n] = string(path)
				n++
			}
			return nil
		})
	})
	if err != nil {
		return err
	}
	n = min(n, len(other)-recent)
	e.U32(uint32(recent + n))
	for _, name := range ranked[:count] {
		if name != "" {
			e.String(name)
		}
	}
	for _, name := range other[:n] {
		e.String(name)
	}
	return nil
}
