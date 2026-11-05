// Package filewatch normalizes native filesystem notifications. Only Linux
// installs per-directory watches; macOS and Windows watch whole subtrees.
package filewatch

import (
	"errors"
	"os"
	"sync"
)

type Op uint8

const (
	Create Op = 1 << iota
	Write
	Remove
	Rename
	Chmod
)

type Event struct {
	Name      string
	Op        Op
	Directory bool
}

func (e Event) Has(op Op) bool { return e.Op&op != 0 }

var ErrOverflow = errors.New("filesystem event queue overflow")

type Watcher struct {
	Events    chan Event
	Errors    chan error
	done      chan struct{}
	native    nativeWatcher
	closeOnce sync.Once
	mu        sync.Mutex
}

func New() (*Watcher, error) {
	w := &Watcher{Events: make(chan Event, 256), Errors: make(chan error, 1), done: make(chan struct{})}
	if err := w.native.open(w); err != nil {
		return nil, err
	}
	return w, nil
}
func (w *Watcher) Add(name string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case <-w.done:
		return os.ErrClosed
	default:
		return w.native.add(name)
	}
}
func (w *Watcher) Remove(name string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case <-w.done:
		return os.ErrClosed
	default:
		return w.native.remove(name)
	}
}
func (w *Watcher) Close() error {
	w.closeOnce.Do(func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		close(w.done)
		w.native.close()
	})
	return nil
}
func (w *Watcher) emit(e Event) {
	select {
	case <-w.done:
		return
	default:
	}
	select {
	case w.Events <- e:
	default:
		w.fail(ErrOverflow)
	}
}
func (w *Watcher) fail(err error) {
	select {
	case w.Errors <- err:
	case <-w.done:
	default:
	}
}
