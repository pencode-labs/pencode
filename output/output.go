// Package output retains bounded, named output streams shared by the workbench
// and extension workers. Idle reads and writes after warm-up do not allocate.
package output

import (
	"context"
	"sync"
	"unicode/utf8"
)

const Capacity = 64 * 1024

type Store struct {
	mu       sync.Mutex
	byName   map[string]*Channel
	channels []*Channel
	revision uint64
	wake     func()
}

type Channel struct {
	store                    *Store
	Name                     string
	data                     []byte
	start, used              int
	revision, cachedRevision uint64
	text                     string
}

func (s *Store) SetWake(wake func()) {
	s.mu.Lock()
	s.wake = wake
	s.mu.Unlock()
}

func (s *Store) Channel(name string) *Channel {
	s.mu.Lock()
	if c := s.byName[name]; c != nil {
		s.mu.Unlock()
		return c
	}
	if s.byName == nil {
		s.byName = make(map[string]*Channel)
	}
	c := &Channel{store: s, Name: name}
	s.byName[name] = c
	s.channels = append(s.channels, c)
	s.revision++
	wake := s.wake
	s.mu.Unlock()
	if wake != nil {
		wake()
	}
	return c
}

func (s *Store) Revision() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revision
}

// Channels reuses the caller's slice; channels have stable identity and order.
func (s *Store) Channels(dst []*Channel) []*Channel {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(dst[:0], s.channels...)
}

func (c *Channel) Write(p []byte) (int, error) {
	n := len(p)
	if n == 0 {
		return 0, nil
	}
	s := c.store
	s.mu.Lock()
	if c.data == nil {
		c.data = make([]byte, Capacity)
	}
	if len(p) >= Capacity {
		p = p[len(p)-Capacity:]
		c.start, c.used = 0, 0
	}
	end := (c.start + c.used) % Capacity
	first := copy(c.data[end:], p)
	copy(c.data, p[first:])
	overflow := max(0, c.used+len(p)-Capacity)
	c.start = (c.start + overflow) % Capacity
	c.used = min(Capacity, c.used+len(p))
	c.revision++
	s.revision++
	wake := s.wake
	s.mu.Unlock()
	if wake != nil {
		wake()
	}
	return n, nil
}

func (c *Channel) Clear() {
	s := c.store
	s.mu.Lock()
	c.start, c.used = 0, 0
	c.text = ""
	c.revision++
	s.revision++
	wake := s.wake
	s.mu.Unlock()
	if wake != nil {
		wake()
	}
}

func (c *Channel) Snapshot() (string, uint64) {
	s := c.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.cachedRevision != c.revision {
		first := min(c.used, Capacity-c.start)
		c.text = string(c.data[c.start:c.start+first]) + string(c.data[:c.used-first])
		// A ring wrap may drop the start of a UTF-8 character.
		for len(c.text) > 0 && !utf8.RuneStart(c.text[0]) {
			c.text = c.text[1:]
		}
		c.cachedRevision = c.revision
	}
	return c.text, c.revision
}

type storeKey struct{}

func WithStore(ctx context.Context, store *Store) context.Context {
	return context.WithValue(ctx, storeKey{}, store)
}

func FromContext(ctx context.Context) *Store {
	s, _ := ctx.Value(storeKey{}).(*Store)
	return s
}
