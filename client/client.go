// Package client exposes typed Pencode services over any owned byte stream.
package client

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/diiyw/onix/projects/pencode/protocol"
)

const (
	maxPending       = 64
	maxBufferedBytes = 128 << 20
)

var ErrClosed = errors.New("pencode: server connection closed")
var ErrBusy = errors.New("pencode: request queue is full")

type call struct {
	id                uint64
	offset            int
	kind              protocol.Kind
	payload, response []byte
	done              chan struct{}
	err               error
}
type Client struct {
	conn    io.ReadWriteCloser
	mu      sync.Mutex
	next    uint64
	pending map[uint64]*call
	queue   chan *call
	cancels chan uint64
	done    chan struct{}
	stopped sync.WaitGroup
	err     error
	// Upload bytes stay charged while the writer owns them, even after a
	// caller cancels. Response bytes cover incomplete replies only.
	uploadBytes, responseBytes int
}

func New(ctx context.Context, conn io.ReadWriteCloser) (*Client, error) {
	c := &Client{conn: conn, pending: make(map[uint64]*call), queue: make(chan *call, maxPending), cancels: make(chan uint64, maxPending), done: make(chan struct{})}
	c.stopped.Add(2)
	go c.read()
	go c.write()
	if _, err := c.request(ctx, protocol.Hello, nil); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = err
	close(c.done)
	for id, r := range c.pending {
		r.err = err
		close(r.done)
		delete(c.pending, id)
	}
	c.responseBytes = 0
	c.mu.Unlock()
	_ = c.conn.Close()
}
func (c *Client) Close() error { c.fail(ErrClosed); c.stopped.Wait(); return nil }

// Done closes as soon as transport failure makes every handle unusable.
func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Client) request(ctx context.Context, kind protocol.Kind, payload []byte) ([]byte, error) {
	if c == nil {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(payload) > protocol.MaxMessage {
		return nil, protocol.ErrProtocol
	}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return nil, err
	}
	if len(c.pending) >= maxPending || c.uploadBytes+len(payload) > maxBufferedBytes {
		c.mu.Unlock()
		return nil, ErrBusy
	}
	r := &call{kind: kind, payload: append([]byte(nil), payload...), done: make(chan struct{})}
	c.next++
	r.id = c.next
	c.pending[r.id] = r
	select {
	case c.queue <- r:
		c.uploadBytes += len(payload)
	default:
		delete(c.pending, r.id)
		c.mu.Unlock()
		return nil, ErrBusy
	}
	c.mu.Unlock()
	select {
	case <-r.done:
		return r.response, r.err
	case <-ctx.Done():
		c.mu.Lock()
		if c.pending[r.id] != r {
			c.mu.Unlock()
			return r.response, r.err
		}
		delete(c.pending, r.id)
		c.responseBytes -= len(r.response)
		r.response = nil
		c.mu.Unlock()
		select {
		case c.cancels <- r.id:
		default:
			c.fail(ErrBusy)
		}
		return nil, ctx.Err()
	}
}

func (c *Client) releaseUpload(r *call) {
	c.mu.Lock()
	c.uploadBytes -= len(r.payload)
	r.payload = nil
	c.mu.Unlock()
}

func (c *Client) write() {
	defer c.stopped.Done()
	w := protocol.Writer{Target: c.conn}
	var uploads [maxPending]*call
	head, count := 0, 0
	defer func() {
		for _, r := range uploads {
			if r != nil {
				c.releaseUpload(r)
			}
		}
		for {
			select {
			case r := <-c.queue:
				c.releaseUpload(r)
			default:
				return
			}
		}
	}()
	preferNew := true
	cancel := func(id uint64) bool {
		if err := w.Write(protocol.Cancel, id, nil); err != nil {
			c.fail(err)
			return false
		}
		return true
	}
	for {
		// Cancellation has priority over another upload chunk.
		select {
		case <-c.done:
			return
		case id := <-c.cancels:
			if !cancel(id) {
				return
			}
			continue
		default:
		}
		var r *call
		if count > 0 {
			if preferNew && count < len(uploads) {
				select {
				case r = <-c.queue:
				default:
				}
			}
			preferNew = !preferNew
			if r == nil {
				r, uploads[head] = uploads[head], nil
				head = (head + 1) % len(uploads)
				count--
			}
		} else {
			select {
			case <-c.done:
				return
			case id := <-c.cancels:
				if !cancel(id) {
					return
				}
				continue
			case r = <-c.queue:
			}
		}
		// A cancellation may have arrived while the blocking select also
		// selected a new call. Preserve cancellation priority in that race.
	drainCancels:
		for {
			select {
			case id := <-c.cancels:
				if !cancel(id) {
					c.releaseUpload(r)
					return
				}
			default:
				break drainCancels
			}
		}
		c.mu.Lock()
		live := c.pending[r.id] == r
		c.mu.Unlock()
		if !live {
			c.releaseUpload(r)
			continue
		}
		payload := r.payload[r.offset:]
		kind := r.kind
		if len(payload) > protocol.MaxPayload {
			kind = protocol.Data
			payload = payload[:protocol.MaxPayload]
		}
		if err := w.Write(kind, r.id, payload); err != nil {
			c.releaseUpload(r)
			c.fail(err)
			return
		}
		if kind == protocol.Data {
			r.offset += len(payload)
			uploads[(head+count)%len(uploads)] = r
			count++
		} else {
			c.releaseUpload(r)
		}
	}
}

func (c *Client) read() {
	defer c.stopped.Done()
	r := protocol.Reader{Source: c.conn}
	for {
		f, err := r.Read()
		if err != nil {
			c.fail(err)
			return
		}
		if f.Kind != protocol.Reply && f.Kind != protocol.Data && f.Kind != protocol.Failure {
			c.fail(protocol.ErrProtocol)
			return
		}
		c.mu.Lock()
		pending := c.pending[f.ID]
		if pending != nil {
			if len(pending.response)+len(f.Payload) > protocol.MaxMessage || c.responseBytes+len(f.Payload) > maxBufferedBytes {
				c.mu.Unlock()
				c.fail(protocol.ErrProtocol)
				return
			}
			pending.response = append(pending.response, f.Payload...)
			c.responseBytes += len(f.Payload)
			if f.Kind != protocol.Data {
				if f.Kind == protocol.Failure {
					d := protocol.Decoder{Buffer: pending.response}
					e := &protocol.Error{Code: protocol.Code(d.U32()), Message: d.String()}
					pending.err = e
					if d.Done() != nil {
						pending.err = protocol.ErrProtocol
					}
				}
				delete(c.pending, f.ID)
				c.responseBytes -= len(pending.response)
				close(pending.done)
			}
		}
		c.mu.Unlock()
	}
}

func (c *Client) OpenWorkspace(ctx context.Context, path string) (protocol.Workspace, error) {
	e := protocol.Encoder{}
	e.String(path)
	p, err := c.request(ctx, protocol.WorkspaceOpen, e.Buffer)
	if err != nil {
		return protocol.Workspace{}, err
	}
	d := protocol.Decoder{Buffer: p}
	v := protocol.Workspace{ID: d.U64(), Path: d.String(), Name: d.String()}
	return v, d.Done()
}
func (c *Client) CloseWorkspace(ctx context.Context, id uint64) error {
	e := protocol.Encoder{}
	e.U64(id)
	_, err := c.request(ctx, protocol.WorkspaceClose, e.Buffer)
	return err
}
func (c *Client) OpenDocument(ctx context.Context, workspace uint64, path string, text bool) (*Document, string, error) {
	e := protocol.Encoder{}
	e.U64(workspace)
	e.String(path)
	if text {
		e.U32(1)
	} else {
		e.U32(0)
	}
	p, err := c.request(ctx, protocol.DocumentOpen, e.Buffer)
	if err != nil {
		return nil, "", err
	}
	d := protocol.Decoder{Buffer: p}
	id, version, content := d.U64(), d.U64(), d.String()
	if err := d.Done(); err != nil {
		return nil, "", err
	}
	if !text {
		return nil, content, nil
	}
	return newDocument(c, id, version, content), content, nil
}
