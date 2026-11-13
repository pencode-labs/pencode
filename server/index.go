package server

import (
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
)

type serviceIndex struct {
	err     error
	changed chan struct{}
	cancel  context.CancelFunc
	watch   *workspaceWatch
	root    string
	entries []protocol.IndexEntry
	mu      sync.Mutex
	done    bool
}

func (p *serviceIndex) signal() { close(p.changed); p.changed = make(chan struct{}) }
func (p *serviceIndex) walk(ctx context.Context) {
	p.walkSnapshot(ctx, nil, workspaceTreeDelta{full: true})
}

func (p *serviceIndex) walkSnapshot(ctx context.Context, previous []protocol.IndexEntry, delta workspaceTreeDelta) {
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
			err = p.scan(ctx, root, ".", 0)
		} else {
			p.mu.Lock()
			p.entries = make([]protocol.IndexEntry, 0, len(previous))
			p.mu.Unlock()
			err = p.reconcile(ctx, root, ".", 0, previous, &delta)
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

func (p *serviceIndex) append(entries ...protocol.IndexEntry) error {
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

func (p *serviceIndex) reuse(ctx context.Context, entries []protocol.IndexEntry) error {
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

func indexSubtreeEnd(entries []protocol.IndexEntry, start int) int {
	end := start + 1
	if entries[start].Directory {
		for end < len(entries) && entries[end].Depth > entries[start].Depth {
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
func (p *serviceIndex) reconcile(ctx context.Context, root *os.Root, directory string, depth int, previous []protocol.IndexEntry, delta *workspaceTreeDelta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > protocol.IndexDepthLimit {
		return fmt.Errorf("directory depth exceeds budget")
	}
	if delta != nil && !delta.full && !delta.contains(directory) {
		start := 0
		for i := 0; i < len(previous); i++ {
			entry := previous[i]
			if entry.Directory && delta.contains(entry.Path) {
				if err := p.reuse(ctx, previous[start:i+1]); err != nil {
					return err
				}
				end := indexSubtreeEnd(previous, i)
				if err := p.reconcile(ctx, root, entry.Path, int(entry.Depth)+1, previous[i+1:end], delta); err != nil {
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
	prefix := ""
	if directory != "." {
		prefix = directory + "/"
	}
	offset := 0
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := child.Name()
		switch name {
		case ".DS_Store", ".git", "dist", "node_modules":
			continue
		}
		for offset < len(previous) && compareIndexNames(previous[offset].Name(), previous[offset].Directory, name, child.IsDir()) < 0 {
			offset = indexSubtreeEnd(previous, offset)
		}
		var entry protocol.IndexEntry
		var cached []protocol.IndexEntry
		matched := offset < len(previous) && previous[offset].Name() == name && previous[offset].Directory == child.IsDir()
		if matched {
			entry = previous[offset]
			end := indexSubtreeEnd(previous, offset)
			cached = previous[offset+1 : end]
			offset = end
		} else {
			entry = protocol.IndexEntry{Path: prefix + name, Depth: uint16(depth), Directory: child.IsDir()}
		}
		if err := p.append(entry); err != nil {
			return err
		}
		if entry.Directory {
			if matched {
				err = p.reconcile(ctx, root, entry.Path, depth+1, cached, delta)
			} else {
				err = p.scan(ctx, root, entry.Path, depth+1)
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}
func (p *serviceIndex) scan(ctx context.Context, root *os.Root, directory string, depth int) error {
	return p.reconcile(ctx, root, directory, depth, nil, nil)
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
		var previous []protocol.IndexEntry
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
					e.String(entry.Path)
					e.U32(uint32(entry.Depth))
					if entry.Directory {
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
		preferred := make([]string, count)
		for i := range preferred {
			preferred[i] = d.String()
		}

		if err := d.Done(); err != nil {
			return err
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		var results [128]string
		var ranks [128]int
		n := 0
		history := make(map[string]int, len(preferred))
		for i, name := range preferred {
			history[name] = i
		}
		for _, entry := range p.entries {
			if entry.Directory || query != "" && !containsFold(entry.Path, query) {
				continue
			}
			rank, known := history[entry.Path]
			if !known {
				rank = 128
			}
			index := n
			for index > 0 && rank < ranks[index-1] {
				index--
			}
			if index < len(results) {
				last := min(n, len(results)-1)
				copy(results[index+1:last+1], results[index:last])
				copy(ranks[index+1:last+1], ranks[index:last])
				results[index], ranks[index] = entry.Path, rank
				n = min(n+1, len(results))
			}
		}

		e.U32(uint32(n))
		for _, name := range results[:n] {
			e.String(name)
		}
	default:
		return protocol.ErrProtocol
	}
	return nil
}
func containsFold(value, query string) bool {
	for i := 0; i+len(query) <= len(value); i++ {
		if strings.EqualFold(value[i:i+len(query)], query) {
			return true
		}
	}
	return false
}
