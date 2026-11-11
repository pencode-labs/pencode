package client

import (
	"context"
	"sync"
	"time"

	"github.com/onix-engine/onix/text/piecetree"
	"github.com/pencode-labs/pencode/protocol"
	"github.com/pencode-labs/pencode/textchange"
)

type documentUpdate struct {
	base, content         piecetree.Snapshot
	start, oldEnd, newEnd int
	sequence              uint64
}

type documentPageResult struct {
	err     error
	content string
	page    protocol.DocumentPage
}
type documentPageRequest struct {
	ctx    context.Context
	result chan documentPageResult
	offset uint64
}

type documentSave struct {
	ctx     context.Context
	result  chan error
	content piecetree.Snapshot
}

// Document keeps one latest immutable edit snapshot. Submitting an edit never
// waits for I/O and does not allocate. A worker coalesces intermediate snapshots
// and sends ordered, versioned deltas; saves explicitly wait for that barrier.
type Document struct {
	err        error
	done       chan struct{}
	cancel     context.CancelFunc
	closed     chan struct{}
	saves      chan documentSave
	pages      chan documentPageRequest
	operations chan struct{}
	notify     chan struct{}
	signal     chan struct{}
	client     *Client
	latest     documentUpdate
	version    uint64
	id         uint64
	applied    uint64
	page       protocol.DocumentPage
	once       sync.Once
	mu         sync.Mutex
}

func newDocument(c *Client, id, version uint64, content string) *Document {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Document{client: c, id: id, version: version, latest: documentUpdate{content: piecetree.FromString(content)}, notify: make(chan struct{}), signal: make(chan struct{}, 1), done: make(chan struct{}), cancel: cancel, saves: make(chan documentSave, 8), pages: make(chan documentPageRequest, 1), closed: make(chan struct{}), operations: make(chan struct{}, 1)}
	go d.run(ctx, version, piecetree.FromString(content))
	return d
}

func (d *Document) Update[V piecetree.Value](baseValue, contentValue V, start, oldEnd, newEnd int) {
	base, content := piecetree.Text(baseValue), piecetree.Text(contentValue)
	d.mu.Lock()
	if d.latest.content != content {
		d.latest = documentUpdate{base: base, content: content, start: start, oldEnd: oldEnd, newEnd: newEnd, sequence: d.latest.sequence + 1}
		select {
		case d.signal <- struct{}{}:
		default:
		}
	}
	d.mu.Unlock()
}

func (d *Document) run(ctx context.Context, version uint64, content piecetree.Snapshot) {
	defer close(d.done)
	var e protocol.Encoder
	for {
		var save *documentSave
		var page *documentPageRequest
		select {
		case <-ctx.Done():
			return
		case <-d.client.done:
			return
		case request := <-d.pages:
			page = &request
		case request := <-d.saves:
			save = &request
		case <-d.signal:
		}
		d.mu.Lock()
		job, err := d.latest, d.err
		ready := job.sequence <= d.applied
		d.mu.Unlock()
		if save != nil {
			if save.ctx.Err() != nil {
				save.result <- save.ctx.Err()
				continue
			}
			job = documentUpdate{content: save.content, start: -1}
		} else if page == nil && (err != nil || ready) {
			continue
		}
		requestCtx, stop := context.WithTimeout(ctx, 30*time.Second)
		var stopSave func() bool
		if save != nil {
			stopSave = context.AfterFunc(save.ctx, stop)
		}
		if err == nil && content != job.content {
			start, end, next := job.start, job.oldEnd, job.newEnd
			if job.base != content || start < 0 || start > end || end > content.Len() || next < start || next > job.content.Len() {
				start, end, next = content.ChangedRange(job.content)
			}
			e.Reset()
			e.U64(d.id)
			e.U64(version)
			d.mu.Lock()
			e.U64(d.page.Offset)
			d.mu.Unlock()
			e.U32(uint32(start))
			e.U32(uint32(end))
			e.U32(uint32(next - start))
			for part := range job.content.Chunks(start, next) {
				e.Buffer = append(e.Buffer, part...)
			}
			var payload []byte
			payload, err = d.client.request(requestCtx, protocol.DocumentChange, e.Buffer)
			if err == nil {
				r := protocol.Decoder{Buffer: payload}
				version = r.U64()
				err = r.Done()
			}
		}
		d.mu.Lock()
		if err != nil {
			d.err = err
		} else {
			content, d.version = job.content, version
			if save == nil {
				d.applied = job.sequence
			} else if d.latest.content == content {
				d.applied = d.latest.sequence
			} else {
				// A save may temporarily install an older snapshot. Restore the
				// latest GUI text after its disk barrier, including already sent edits.
				d.latest.sequence++
				select {
				case d.signal <- struct{}{}:
				default:
				}
			}
		}
		close(d.notify)
		d.notify = make(chan struct{})
		d.mu.Unlock()
		if save != nil {
			if err == nil {
				e.Reset()
				e.U64(d.id)
				e.U64(version)
				_, err = d.client.request(requestCtx, protocol.DocumentSave, e.Buffer)
			}
			if save.ctx.Err() != nil {
				err = save.ctx.Err()
			}
			save.result <- err
			stopSave()
		}
		if page != nil {
			result := documentPageResult{err: err}
			if result.err == nil {
				e.Reset()
				e.U64(d.id)
				e.U64(version)
				e.U64(page.offset)
				var payload []byte
				payload, result.err = d.client.request(page.ctx, protocol.DocumentRead, e.Buffer)
				if result.err == nil {
					r := protocol.Decoder{Buffer: payload}
					result.content, result.page = r.String(), r.DocumentPage()
					result.err = r.Done()
				}
			}
			if result.err == nil {
				content = piecetree.FromString(result.content)
				d.mu.Lock()
				d.latest = documentUpdate{content: content, sequence: d.latest.sequence}
				d.applied = d.latest.sequence
				d.page = result.page
				d.mu.Unlock()
			}
			page.result <- result
		}

		stop()
		if cap(e.Buffer) > protocol.MaxPayload {
			e.Buffer = nil
		}
	}
}

// changedRange visits only the shared prefix/suffix and preserves rune edges.
func changedRange(a, b string) (start, oldEnd, newEnd int) {
	return textchange.Between(a, b)
}

func (d *Document) flush(ctx context.Context) (uint64, error) {
	for {
		d.mu.Lock()
		err, ready, notify, version := d.err, d.applied == d.latest.sequence, d.notify, d.version
		d.mu.Unlock()
		if err != nil {
			return 0, err
		}
		if ready {
			return version, nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-d.done:
			return 0, ErrClosed
		case <-notify:
		}
	}
}

func (d *Document) Save(ctx context.Context, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.Update("", content, -1, 0, 0)
	return d.SaveSnapshot(ctx, content)
}

// SaveSnapshot persists exactly content while Update may continue accepting
// newer GUI edits. The document worker owns both the version and save barrier;
// newer text is restored in memory afterwards and is never saved implicitly.
func (d *Document) SaveSnapshot[V piecetree.Value](ctx context.Context, value V) error {
	content := piecetree.Text(value)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-d.done:
		return ErrClosed
	case d.operations <- struct{}{}:
	}
	defer func() { <-d.operations }()
	request := documentSave{ctx: ctx, content: content, result: make(chan error, 1)}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-d.done:
		return ErrClosed
	case d.saves <- request:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-d.done:
		return ErrClosed
	case err := <-request.result:
		return err
	}
}

// Page describes only the currently loaded window; Content and snapshots for a
// paged document never represent the unloaded remainder of the file.
func (d *Document) Page() protocol.DocumentPage {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.page
}

// ReadPage flushes edits to the server's disk-backed document, without saving.
// The caller must stop submitting edits until this operation has returned.
func (d *Document) ReadPage(ctx context.Context, offset uint64) (string, protocol.DocumentPage, error) {
	select {
	case <-ctx.Done():
		return "", protocol.DocumentPage{}, ctx.Err()
	case <-d.done:
		return "", protocol.DocumentPage{}, ErrClosed
	case d.operations <- struct{}{}:
	}
	defer func() { <-d.operations }()
	request := documentPageRequest{ctx: ctx, offset: offset, result: make(chan documentPageResult, 1)}
	select {
	case <-ctx.Done():
		return "", protocol.DocumentPage{}, ctx.Err()
	case <-d.done:
		return "", protocol.DocumentPage{}, ErrClosed
	case d.pages <- request:
	}
	select {
	case <-d.done:
		return "", protocol.DocumentPage{}, ErrClosed
	case result := <-request.result:
		return result.content, result.page, result.err
	}
}

func (d *Document) Move(ctx context.Context, workspace uint64, path string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-d.done:
		return ErrClosed
	case d.operations <- struct{}{}:
	}
	defer func() { <-d.operations }()
	if _, err := d.flush(ctx); err != nil {
		return err
	}
	e := protocol.Encoder{}
	e.U64(d.id)
	e.U64(workspace)
	e.String(path)
	_, err := d.client.request(ctx, protocol.DocumentMove, e.Buffer)
	if err != nil {
		d.mu.Lock()
		d.err = err
		close(d.notify)
		d.notify = make(chan struct{})
		d.mu.Unlock()
	}
	return err
}

// Release retires a handle without waiting on the transport. Close additionally
// waits for cleanup and is intended for workers and connection shutdown.
func (d *Document) Release() {
	d.once.Do(func() {
		d.cancel()
		go func() {
			defer close(d.closed)
			<-d.done
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			e := protocol.Encoder{}
			e.U64(d.id)
			_, _ = d.client.request(ctx, protocol.DocumentClose, e.Buffer)
		}()
	})
}

func (d *Document) Close() {
	d.Release()
	<-d.closed
}
