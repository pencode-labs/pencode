package server

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/pencode-labs/pencode/protocol"
)

type serviceIndex struct {
	mu      sync.Mutex
	root    string
	entries []protocol.IndexEntry
	changed chan struct{}
	done    bool
	err     error
	cancel  context.CancelFunc
}

func (p *serviceIndex) signal() { close(p.changed); p.changed = make(chan struct{}) }
func (p *serviceIndex) walk(ctx context.Context) {
	root, err := os.OpenRoot(p.root)
	if err == nil {
		defer root.Close()
		err = p.scan(ctx, root, ".", 0)
	}
	p.mu.Lock()
	p.done = true
	if p.err == nil {
		p.err = err
	}
	p.signal()
	p.mu.Unlock()
}
func (p *serviceIndex) scan(ctx context.Context, root *os.Root, directory string, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > 256 {
		return fmt.Errorf("directory depth exceeds budget")
	}
	f, err := root.Open(directory)
	if err != nil {
		p.mu.Lock()
		if p.err == nil {
			p.err = err
		}
		p.mu.Unlock()
		return nil
	}
	children, err := f.ReadDir(262145)
	f.Close()
	if err != nil && err != io.EOF {
		p.mu.Lock()
		if p.err == nil {
			p.err = err
		}
		p.mu.Unlock()
	}
	if len(children) > 262144 {
		return fmt.Errorf("directory exceeds index budget")
	}
	slices.SortFunc(children, func(a, b fs.DirEntry) int {
		if a.IsDir() != b.IsDir() {
			if a.IsDir() {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name(), b.Name())
	})
	prefix := ""
	if directory != "." {
		prefix = directory + "/"
	}
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := child.Name()
		switch name {
		case ".git", "dist", "node_modules":
			continue
		}
		entry := protocol.IndexEntry{Path: prefix + name, Name: name, Depth: depth, Directory: child.IsDir()}
		p.mu.Lock()
		if len(p.entries) >= 262144 {
			p.mu.Unlock()
			return fmt.Errorf("index exceeds 262144 entry budget")
		}
		p.entries = append(p.entries, entry)
		if len(p.entries)%256 == 0 || len(p.entries) == 1 {
			p.signal()
		}
		p.mu.Unlock()
		if entry.Directory {
			if err := p.scan(ctx, root, entry.Path, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *services) index(ctx context.Context, op protocol.Operation, root string, d *protocol.Decoder, e *protocol.Encoder) error {
	if op == protocol.IndexOpen {
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
		// Replace an obsolete scan instead of retaining multiple copies of the tree.
		for id, p := range s.indexes {
			if p.root == root {
				p.cancel()
				delete(s.indexes, id)
			}
		}
		if len(s.indexes) >= 32 {
			return &protocol.Error{Code: protocol.Busy, Message: "index budget exceeded"}
		}
		scanCtx, cancel := context.WithCancel(s.ctx)
		s.next++
		id := s.next
		p := &serviceIndex{root: root, changed: make(chan struct{}), cancel: cancel}
		s.indexes[id] = p
		e.U64(id)
		s.background.Add(1)
		go func() { defer s.background.Done(); p.walk(scanCtx) }()
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
				end := min(offset+256, len(p.entries))
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
					e.String(entry.Name)
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
		query := d.String()
		count := d.U32()
		if count > 128 {
			return protocol.ErrProtocol
		}
		preferred := make([]string, count)
		for i := range preferred {
			preferred[i] = d.String()
		}

		ruleCount := d.U32()
		if ruleCount > 128 {
			return protocol.ErrProtocol
		}
		rules := make([]protocol.PathRule, ruleCount)
		for i := range rules {
			pattern, suffix := d.String(), d.U32()
			if suffix > 1 {
				return protocol.ErrProtocol
			}
			rules[i] = protocol.PathRule{Pattern: pattern, Suffix: suffix != 0}
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
			openable := protocol.IsTextFile(entry.Path)
			if !openable {
				for _, rule := range rules {
					if rule.Suffix && strings.HasSuffix(entry.Name, rule.Pattern) || !rule.Suffix && entry.Name == rule.Pattern {
						openable = true
						break
					}
				}
			}
			if !openable {
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
