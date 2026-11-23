package windowstate

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/pencode-labs/pencode/internal/filewatch"
)

// LaunchInbox forwards desktop launches to the process that owns the windows.
// Atomic files retain requests across startup and native event coalescing.
type LaunchInbox[T any] struct {
	lease      *os.File
	watcher    *filewatch.Watcher
	stop, done chan struct{}
	dir        string
	changed    atomic.Bool
}

// ForwardLaunch returns nil after handing the request to a running instance.
func ForwardLaunch[T any](dir string, request T, wake func()) (*LaunchInbox[T], error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	guard, err := lock(filepath.Join(dir, "dispatch.lock"), true)
	if err != nil {
		return nil, err
	}
	defer guard.Close()
	lease, err := lock(filepath.Join(dir, "desktop.lock"), false)
	if errors.Is(err, errLocked) {
		desktopForeground(guard, false)
		return nil, Write(filepath.Join(dir, rand.Text()+".json"), &request)
	}
	if err != nil {
		return nil, err
	}
	desktopForeground(guard, true)
	w, err := filewatch.New()
	if err == nil {
		err = w.Add(dir)
	}
	if err != nil {
		if w != nil {
			w.Close()
		}
		lease.Close()
		return nil, err
	}
	inbox := &LaunchInbox[T]{dir: dir, lease: lease, watcher: w, stop: make(chan struct{}), done: make(chan struct{})}
	inbox.changed.Store(true)
	go func() {
		defer close(inbox.done)
		for {
			select {
			case <-inbox.stop:
				return
			case event := <-w.Events:
				if !strings.HasSuffix(event.Name, ".json") || !event.Has(filewatch.Create|filewatch.Rename|filewatch.Write) {
					continue
				}
			case <-w.Errors:
			}
			inbox.changed.Store(true)
			wake()
		}
	}()
	return inbox, nil
}

// Drain releases the lease under the dispatch lock when the last window closes
// and no launch is waiting. A concurrent launch can then become the next owner.
func (q *LaunchInbox[T]) Drain(lastWindowClosed bool, receive func(T)) error {
	if !q.changed.Swap(false) && !lastWindowClosed {
		return nil
	}
	if lastWindowClosed {
		guard, err := lock(filepath.Join(q.dir, "dispatch.lock"), true)
		if err != nil {
			return err
		}
		defer guard.Close()
	}
	entries, err := os.ReadDir(q.dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(q.dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var request T
		err = json.Unmarshal(data, &request)
		if err := os.Remove(path); err != nil {
			return err
		}
		if err == nil {
			receive(request)
			lastWindowClosed = false
		}
	}
	if lastWindowClosed {
		q.Close()
	}
	return nil
}

func (q *LaunchInbox[T]) Close() {
	if q.lease != nil {
		close(q.stop)
		q.watcher.Close()
		<-q.done
		q.lease.Close()
		q.lease = nil
	}
}
