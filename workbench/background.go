package workbench

import (
	"context"
	"sync"
)

// latestWorker owns its processing state on one goroutine. The foreground
// submits immutable snapshots; obsolete queued requests/results are replaced.
// Two result buffers are exchanged and reused without per-submit allocation.
type latestWorker[J, R any] struct {
	mu      sync.Mutex
	request J
	result  R
	pending bool
	ready   bool
	signal  chan struct{}
	cancel  context.CancelFunc
	done    chan struct{}
	reset   func(*R)
}

func newLatestWorker[J, R any](process func(J, *R) bool, reset func(*R), dispose, wake func()) *latestWorker[J, R] {
	ctx, cancel := context.WithCancel(context.Background())
	w := &latestWorker[J, R]{signal: make(chan struct{}, 1), cancel: cancel, done: make(chan struct{}), reset: reset}
	go func() {
		var scratch R
		defer close(w.done)
		defer func() {
			if dispose != nil {
				dispose()
			}
			w.mu.Lock()
			var job J
			var result R
			w.request, w.result = job, result
			w.pending, w.ready = false, false
			w.mu.Unlock()
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.signal:
			}
			w.mu.Lock()
			job, pending := w.request, w.pending
			var zero J
			w.request, w.pending = zero, false
			w.mu.Unlock()
			if !pending || ctx.Err() != nil {
				continue
			}
			publish := process(job, &scratch)
			if ctx.Err() != nil {
				return
			}
			if publish {
				w.mu.Lock()
				scratch, w.result = w.result, scratch
				w.ready = true
				w.mu.Unlock()
				if wake != nil {
					wake()
				}
			}
			reset(&scratch)
		}
	}()
	return w
}

func (w *latestWorker[J, R]) submit(job J) {
	w.mu.Lock()
	w.request, w.pending = job, true
	w.mu.Unlock()
	select {
	case w.signal <- struct{}{}:
	default:
	}
}

// accept may copy or exchange exclusively owned buffers with the result. It
// must not retain shared mutable buffers or call submit. Only the foreground
// calls poll and close.
func (w *latestWorker[J, R]) poll(accept func(*R)) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.ready {
		return false
	}
	accept(&w.result)
	w.reset(&w.result)
	w.ready = false
	return true
}

func (w *latestWorker[J, R]) close() {
	w.cancel()
	<-w.done
}
