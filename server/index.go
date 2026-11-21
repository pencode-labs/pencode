package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/pencode-labs/pencode/protocol"
	"github.com/pencode-labs/pencode/server/workspace"
)

// Files borrow a directory prefix shared by all siblings. A directory entry
// has an empty filename and points to its own prefix, including the final slash.
type indexEntry struct {
	directory *indexDirectory
	filename  string
}

type indexDirectory struct {
	prefix string
	depth  uint16 // Depth of its immediate children.
}

var indexRootDirectory indexDirectory

func (d *indexDirectory) path() string {
	if d.prefix == "" {
		return "."
	}
	return d.prefix[:len(d.prefix)-1]
}

func (e indexEntry) isDir() bool { return e.filename == "" }

func (e indexEntry) name() string {
	if !e.isDir() {
		return e.filename
	}
	path := e.directory.path()
	return path[strings.LastIndexByte(path, '/')+1:]
}

func (e indexEntry) depth() uint16 {
	if e.isDir() {
		return e.directory.depth - 1
	}
	return e.directory.depth
}

func (e indexEntry) encodePath(out *protocol.Encoder) {
	if e.isDir() {
		out.String(e.directory.path())
		return
	}
	prefix := e.directory.prefix
	out.U32(uint32(len(prefix) + len(e.filename)))
	out.Buffer = append(out.Buffer, prefix...)
	out.Buffer = append(out.Buffer, e.filename...)
}

type serviceIndex struct {
	err     error
	changed chan struct{}
	cancel  context.CancelFunc
	watch   *workspaceWatch
	root    string
	entries []indexEntry
	mu      sync.Mutex
	done    bool
}

func (p *serviceIndex) signal() { close(p.changed); p.changed = make(chan struct{}) }
func (p *serviceIndex) walk(ctx context.Context) {
	p.walkSnapshot(ctx, nil, workspaceTreeDelta{full: true})
}

func (p *serviceIndex) walkSnapshot(ctx context.Context, previous []indexEntry, delta workspaceTreeDelta) {
	if !delta.full && delta.count == 0 {
		// Completed snapshots are immutable, so a clean refresh can share them.
		p.mu.Lock()
		p.entries, p.err, p.done = previous, ctx.Err(), true
		p.signal()
		p.mu.Unlock()
		return
	}
	root, err := os.OpenRoot(p.root)
	if err == nil {
		defer root.Close()
		if delta.full {
			err = p.scan(ctx, root, &indexRootDirectory)
		} else {
			p.mu.Lock()
			p.entries = make([]indexEntry, 0, len(previous))
			p.mu.Unlock()
			err = p.reconcile(ctx, root, &indexRootDirectory, previous, &delta)
		}
	}
	p.mu.Lock()
	p.done = true
	if p.err == nil {
		p.err = err
	}
	p.signal()
	p.mu.Unlock()
}

func (p *serviceIndex) append(entries ...indexEntry) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	previous := len(p.entries)
	if len(entries) > protocol.IndexEntryLimit-previous {
		return fmt.Errorf("index exceeds %d entry budget", protocol.IndexEntryLimit)
	}
	p.entries = append(p.entries, entries...)
	if len(p.entries) > previous && (previous == 0 || previous/protocol.IndexPageSize != len(p.entries)/protocol.IndexPageSize) {
		p.signal()
	}
	return nil
}

func (p *serviceIndex) reuse(ctx context.Context, entries []indexEntry) error {
	for len(entries) != 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(len(entries), protocol.IndexPageSize)
		if err := p.append(entries[:n]...); err != nil {
			return err
		}
		entries = entries[n:]
	}
	return nil
}

func indexSubtreeEnd(entries []indexEntry, start int) int {
	end := start + 1
	if entries[start].isDir() {
		for end < len(entries) && entries[end].depth() > entries[start].depth() {
			end++
		}
	}
	return end
}

func compareIndexNames(a string, aDir bool, b string, bDir bool) int {
	if aDir != bDir {
		if aDir {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

// Reconcile direct children of dirty directories. Existing subtrees retain
// their paths and entries; only newly discovered directories require a walk.
func (p *serviceIndex) reconcile(ctx context.Context, root *os.Root, dir *indexDirectory, previous []indexEntry, delta *workspaceTreeDelta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if dir.depth > protocol.IndexDepthLimit {
		return fmt.Errorf("directory depth exceeds budget")
	}
	directory := dir.path()
	if delta != nil && !delta.full && !delta.contains(directory) {
		start := 0
		for i := 0; i < len(previous); i++ {
			entry := previous[i]
			if entry.isDir() && delta.contains(entry.directory.path()) {
				if err := p.reuse(ctx, previous[start:i+1]); err != nil {
					return err
				}
				end := indexSubtreeEnd(previous, i)
				if err := p.reconcile(ctx, root, entry.directory, previous[i+1:end], delta); err != nil {
					return err
				}
				start = end
				i = end - 1
			}
		}
		return p.reuse(ctx, previous[start:])
	}
	f, err := root.Open(directory)
	if err != nil {
		if delta == nil {
			p.mu.Lock()
			if p.err == nil {
				p.err = err
			}
			p.mu.Unlock()
			return nil
		}
		return err
	}
	children, err := f.ReadDir(protocol.IndexEntryLimit + 1)
	f.Close()
	if err != nil && err != io.EOF {
		return err
	}
	if len(children) > protocol.IndexEntryLimit {
		return fmt.Errorf("directory exceeds index budget")
	}
	slices.SortFunc(children, func(a, b fs.DirEntry) int {
		return compareIndexNames(a.Name(), a.IsDir(), b.Name(), b.IsDir())
	})
	offset := 0
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := child.Name()
		if strings.HasPrefix(name, workspace.SaveTemporaryPrefix) {
			continue
		}
		switch name {
		case ".DS_Store", ".git", "dist", "node_modules":
			continue
		}
		for offset < len(previous) && compareIndexNames(previous[offset].name(), previous[offset].isDir(), name, child.IsDir()) < 0 {
			offset = indexSubtreeEnd(previous, offset)
		}
		var entry indexEntry
		var cached []indexEntry
		matched := offset < len(previous) && previous[offset].name() == name && previous[offset].isDir() == child.IsDir()
		if matched {
			entry = previous[offset]
			end := indexSubtreeEnd(previous, offset)
			cached = previous[offset+1 : end]
			offset = end
		} else if child.IsDir() {
			entry.directory = &indexDirectory{prefix: dir.prefix + name + "/", depth: dir.depth + 1}
		} else {
			entry = indexEntry{directory: dir, filename: name}
		}
		if err := p.append(entry); err != nil {
			return err
		}
		if entry.isDir() {
			if matched {
				err = p.reconcile(ctx, root, entry.directory, cached, delta)
			} else {
				err = p.scan(ctx, root, entry.directory)
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}
func (p *serviceIndex) scan(ctx context.Context, root *os.Root, dir *indexDirectory) error {
	return p.reconcile(ctx, root, dir, nil, nil)
}
func (s *services) index(ctx context.Context, op protocol.Operation, root string, d *protocol.Decoder, e *protocol.Encoder) error {
	if op == protocol.IndexOpen {
		previousID := d.U64()
		if err := d.Done(); err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		if root != "" && !s.roots[root] {
			return &protocol.Error{Code: protocol.NotFound, Message: "workspace is closed"}
		}
		if p := s.indexes[previousID]; p != nil && p.root != root {
			return protocol.ErrProtocol
		}
		s.next++
		id := s.next
		watch := s.watches[root]
		var baseline uint64
		var previous []indexEntry
		delta := workspaceTreeDelta{full: true}
		if watch != nil {
			baseline, watch.index = watch.index, id
			delta = watch.tree.take()
			select {
			case <-watch.done:
				delta.full = true
			default:
			}
			for i, name := range delta.directories[:delta.count] {
				relative, err := filepath.Rel(filepath.FromSlash(root), name)
				if err != nil || !filepath.IsLocal(relative) {
					delta.full = true
					break
				}
				delta.directories[i] = filepath.ToSlash(relative)
			}
		}
		// Keep the visible snapshot queryable until the client commits the new
		// one. Directory hints apply only to the most recently started snapshot;
		// a cancelled or failed replacement cannot make an older baseline clean.
		for oldID, p := range s.indexes {
			if p.root == root {
				p.mu.Lock()
				if oldID == previousID && oldID == baseline && watch != nil && p.watch == watch && p.done && p.err == nil {
					previous = p.entries
				}
				p.mu.Unlock()
				if oldID != previousID {
					p.cancel()
					delete(s.indexes, oldID)
				}
			}
		}
		if previous == nil {
			delta.full = true
		}
		if len(s.indexes) >= 64 {
			return &protocol.Error{Code: protocol.Busy, Message: "index budget exceeded"}
		}
		scanCtx, cancel := context.WithCancel(s.ctx)
		p := &serviceIndex{root: root, changed: make(chan struct{}), cancel: cancel, watch: watch}
		s.indexes[id] = p
		e.U64(id)
		s.background.Add(1)
		go func() { defer s.background.Done(); p.walkSnapshot(scanCtx, previous, delta) }()
		return nil
	}
	id := d.U64()
	s.mu.Lock()
	p := s.indexes[id]
	if op == protocol.IndexClose {
		delete(s.indexes, id)
	}
	s.mu.Unlock()
	if p == nil {
		return &protocol.Error{Code: protocol.NotFound, Message: "index is closed"}
	}
	switch op {
	case protocol.IndexClose:
		if err := d.Done(); err != nil {
			return err
		}
		p.cancel()
	case protocol.IndexNext:
		offset := int(d.U32())
		if err := d.Done(); err != nil {
			return err
		}
		for {
			p.mu.Lock()
			if offset > len(p.entries) {
				p.mu.Unlock()
				return protocol.ErrProtocol
			}
			if offset < len(p.entries) || p.done {
				end := min(offset+protocol.IndexPageSize, len(p.entries))
				done := p.done && end == len(p.entries)
				if done {
					e.U32(1)
				} else {
					e.U32(0)
				}
				message := ""
				if done && p.err != nil {
					message = p.err.Error()
				}
				e.String(message)
				e.U32(uint32(end - offset))
				for _, entry := range p.entries[offset:end] {
					entry.encodePath(e)
					e.U32(uint32(entry.depth()))
					if entry.isDir() {
						e.U32(1)
					} else {
						e.U32(0)
					}
				}
				p.mu.Unlock()
				return nil
			}
			changed := p.changed
			p.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changed:
			}
		}
	case protocol.PathSearch:
		query := strings.ReplaceAll(d.String(), `\`, "/")
		root := strings.TrimRight(strings.ReplaceAll(p.root, `\`, "/"), "/")
		if len(query) > len(root) && query[len(root)] == '/' && strings.EqualFold(query[:len(root)], root) {
			query = query[len(root)+1:]
		}
		count := d.U32()
		if count > 128 {
			return protocol.ErrProtocol
		}
		history := make(map[string]int, count)
		for i := range count {
			history[d.String()] = int(i)
		}

		if err := d.Done(); err != nil {
			return err
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		var results [128]int
		var ranks [128]int
		n := 0
		var scratch [256]byte
		path := scratch[:0]
		needle := []byte(query)
		for pos, entry := range p.entries {
			if entry.isDir() {
				continue
			}
			rank := 128
			if query != "" || count != 0 {
				path = append(path[:0], entry.directory.prefix...)
				path = append(path, entry.filename...)
				if query != "" && !containsFold(path, needle) {
					continue
				}
				if r, ok := history[string(path)]; ok {
					rank = r
				}
			}
			index := n
			for index > 0 && rank < ranks[index-1] {
				index--
			}
			if index < len(results) {
				last := min(n, len(results)-1)
				copy(results[index+1:last+1], results[index:last])
				copy(ranks[index+1:last+1], ranks[index:last])
				results[index], ranks[index] = pos, rank
				n = min(n+1, len(results))
				if count == 0 && n == len(results) {
					break
				}
			}
		}

		e.U32(uint32(n))
		for _, pos := range results[:n] {
			p.entries[pos].encodePath(e)
		}
	default:
		return protocol.ErrProtocol
	}
	return nil
}
func containsFold(value, query []byte) bool {
	for i := 0; i+len(query) <= len(value); i++ {
		if bytes.EqualFold(value[i:i+len(query)], query) {
			return true
		}
	}
	return false
}
