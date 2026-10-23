package client

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/pencode-labs/pencode/protocol"
)

// Process is a flow-controlled stdio/PTY stream. Each reader has at most one
// outstanding receive of up to StreamWindow bytes; an idle stream cannot block
// the connection reader. Writes are serialized so concurrent callers cannot
// interleave the fragments of a logical Write.
type Process struct {
	logs       *Process
	client     *Client
	id         uint64
	ctx        context.Context
	cancel     context.CancelFunc
	readMu     sync.Mutex
	writeMu    sync.Mutex
	sizeMu     sync.Mutex
	cols, rows int
	pending    []byte
	eof        bool
	once       sync.Once
	closeOnce  sync.Once
	closeErr   error
	done       chan struct{}
}

func (c *Client) openProcess(ctx context.Context, op protocol.Operation, root, name string, args []string, cols, rows int) (*Process, error) {
	e := service(op, root)
	e.U32(uint32(cols))
	e.U32(uint32(rows))
	e.String(name)
	e.U32(uint32(len(args)))
	for _, v := range args {
		e.String(v)
	}
	d, err := c.execute(ctx, &e)
	if err != nil {
		return nil, err
	}
	return c.decodeProcess(&d, cols, rows)
}

func (c *Client) decodeProcess(d *protocol.Decoder, cols, rows int) (*Process, error) {
	id, logID := d.U64(), d.U64()
	if err := d.Done(); err != nil {
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(context.Background())
	p := &Process{client: c, id: id, ctx: streamCtx, cancel: cancel, done: make(chan struct{}), cols: cols, rows: rows}
	if logID != 0 {
		logCtx, logCancel := context.WithCancel(context.Background())
		p.logs = &Process{client: c, id: logID, ctx: logCtx, cancel: logCancel, done: make(chan struct{})}
	}
	return p, nil
}
func (c *Client) OpenProcess(ctx context.Context, root, name string, args ...string) (*Process, error) {
	return c.openProcess(ctx, protocol.ProcessOpen, root, name, args, 0, 0)
}
func (c *Client) OpenTerminal(ctx context.Context, root string, cols, rows int) (*Process, error) {
	return c.openProcess(ctx, protocol.TerminalOpen, root, "", nil, cols, rows)
}
func (p *Process) request(ctx context.Context, op protocol.Operation, encode func(*protocol.Encoder)) (protocol.Decoder, error) {
	e := service(op, "")
	e.U64(p.id)
	if encode != nil {
		encode(&e)
	}
	return p.client.execute(ctx, &e)
}
func (p *Process) Read(dst []byte) (int, error) {
	p.readMu.Lock()
	defer p.readMu.Unlock()
	if len(dst) == 0 {
		return 0, nil
	}
	if len(p.pending) == 0 && !p.eof {
		d, err := p.request(p.ctx, protocol.StreamRead, nil)
		if err != nil {
			return 0, err
		}
		p.eof = d.U32() != 0
		p.pending = d.Bytes()
		if err := d.Done(); err != nil {
			return 0, err
		}
	}
	n := copy(dst, p.pending)
	p.pending = p.pending[n:]
	if n > 0 {
		return n, nil
	}
	if p.eof {
		p.once.Do(func() { close(p.done) })
	}
	return 0, io.EOF
}
func (p *Process) Write(data []byte) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	total := 0
	for len(data) > 0 {
		n := min(len(data), protocol.StreamWindow)
		d, err := p.request(p.ctx, protocol.StreamWrite, func(e *protocol.Encoder) { e.Bytes(data[:n]) })
		if err != nil {
			return total, err
		}
		if err := d.Done(); err != nil {
			return total, err
		}
		total += n
		data = data[n:]
	}
	return total, nil
}
func (p *Process) Resize(cols, rows int) error {
	cols, rows = max(2, min(cols, 1000)), max(1, min(rows, 500))
	p.sizeMu.Lock()
	defer p.sizeMu.Unlock()
	if p.ctx.Err() != nil {
		return ErrClosed
	}
	if p.cols == cols && p.rows == rows {
		return nil
	}
	d, err := p.request(p.ctx, protocol.StreamResize, func(e *protocol.Encoder) { e.U32(uint32(cols)); e.U32(uint32(rows)) })
	if err != nil {
		return err
	}
	if err := d.Done(); err != nil {
		return err
	}
	p.cols, p.rows = cols, rows
	return nil
}

func (p *Process) Wait() error {
	d, err := p.request(p.ctx, protocol.StreamWait, nil)
	if err != nil {
		return err
	}
	return d.Done()
}

func (p *Process) Close() error {
	p.closeOnce.Do(func() {
		p.cancel()
		p.once.Do(func() { close(p.done) })
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, p.closeErr = p.request(ctx, protocol.StreamClose, nil)
		if p.logs != nil {
			_ = p.logs.Close()
		}
	})
	return p.closeErr
}

func (p *Process) Done() <-chan struct{} { return p.done }

func (p *Process) Stderr() io.Reader {
	if p.logs == nil {
		return strings.NewReader("")
	}
	return p.logs
}
