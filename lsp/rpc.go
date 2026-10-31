// Package lsp implements the Language Server Protocol over a full-duplex,
// cancellable JSON-RPC connection. Unknown methods and extension fields remain
// available through Call and Notify rather than a closed method enumeration.
package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

const MaxMessage = 32 << 20

var ErrClosed = errors.New("lsp: connection closed")

const (
	ParseError       = -32700
	InvalidRequest   = -32600
	MethodNotFound   = -32601
	InvalidParams    = -32602
	InternalError    = -32603
	RequestCancelled = -32800
	ContentModified  = -32801
)

type ResponseError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *ResponseError) Error() string { return fmt.Sprintf("lsp (%d): %s", e.Code, e.Message) }

// Message.ID is absent for notifications. Raw JSON preserves string and numeric
// server IDs, result unions, custom methods and future protocol additions.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

type Reader struct {
	source *bufio.Reader
	buffer []byte
}

func NewReader(source io.Reader) *Reader { return &Reader{source: bufio.NewReaderSize(source, 8192)} }

// Read borrows its result until the next Read; framing storage is reused.
func (r *Reader) Read() ([]byte, error) {
	if cap(r.buffer) > maxRetainedMessage {
		r.buffer = nil
	}
	length, headers := -1, 0
	for {
		line, err := r.source.ReadSlice('\n')
		if err != nil {
			return nil, err
		}
		headers += len(line)
		if headers > 8192 || len(line) < 2 || line[len(line)-2] != '\r' {
			return nil, errors.New("lsp: invalid header")
		}
		line = line[:len(line)-2]
		if len(line) == 0 {
			break
		}
		name, value, ok := bytes.Cut(line, []byte(":"))
		if !ok {
			return nil, errors.New("lsp: invalid header field")
		}
		switch {
		case bytes.EqualFold(name, []byte("Content-Length")):
			if length >= 0 {
				return nil, errors.New("lsp: duplicate Content-Length")
			}
			value = bytes.TrimSpace(value)
			if len(value) == 0 {
				return nil, errors.New("lsp: missing Content-Length")
			}
			for _, c := range value {
				if c < '0' || c > '9' {
					return nil, errors.New("lsp: invalid Content-Length")
				}
			}
			length, err = strconv.Atoi(string(value))
			if err != nil || length <= 0 || length > MaxMessage {
				return nil, errors.New("lsp: message too large or empty")
			}
		case bytes.EqualFold(name, []byte("Content-Type")):
			_, params, err := mime.ParseMediaType(string(bytes.TrimSpace(value)))
			if err != nil || params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8") {
				return nil, errors.New("lsp: unsupported content encoding")
			}
		}
	}
	if length < 0 {
		return nil, errors.New("lsp: missing Content-Length")
	}
	if cap(r.buffer) < length {
		r.buffer = make([]byte, length)
	}
	r.buffer = r.buffer[:length]
	_, err := io.ReadFull(r.source, r.buffer)
	return r.buffer, err
}

type Writer struct {
	Target io.Writer
	header [64]byte
}

func (w *Writer) Write(data []byte) error {
	if len(data) == 0 || len(data) > MaxMessage {
		return errors.New("lsp: invalid message size")
	}
	h := append(w.header[:0], "Content-Length: "...)
	h = strconv.AppendInt(h, int64(len(data)), 10)
	h = append(h, "\r\n\r\n"...)
	for _, part := range [][]byte{h, data} {
		for len(part) != 0 {
			n, err := w.Target.Write(part)
			if n < 0 || n > len(part) {
				return io.ErrShortWrite
			}
			part = part[n:]
			if err != nil {
				return err
			}
			if n == 0 {
				return io.ErrShortWrite
			}
		}
	}
	return nil
}

type Handler func(context.Context, string, json.RawMessage) (any, error)
type response struct {
	data json.RawMessage
	err  error
}
type outbound struct {
	data *bytes.Buffer
	ack  chan struct{}
}
type inbound struct {
	ctx     context.Context
	message Message
}

type Conn struct {
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	stop     sync.Once
	closeIO  func()
	outgoing chan outbound
	incoming chan inbound
	handler  Handler
	sendMu   sync.RWMutex
	mu       sync.Mutex
	next     uint64
	pending  map[string]chan response
	running  map[string]context.CancelFunc
	err      error
}

// NewConn owns its streams. closeIO must interrupt both reads and writes.
// Responses are dispatched independently of handlers, so a server request can
// make a nested client request without deadlocking the reader.
func NewConn(input io.Reader, output io.Writer, closeIO func(), handler Handler) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{ctx: ctx, cancel: cancel, done: make(chan struct{}), closeIO: closeIO, outgoing: make(chan outbound, 64), incoming: make(chan inbound, 256), handler: handler, pending: make(map[string]chan response), running: make(map[string]context.CancelFunc)}
	go c.read(input)
	go c.write(output)
	go c.dispatch()
	return c
}
func (c *Conn) Done() <-chan struct{} { return c.done }
func (c *Conn) Err() error            { c.mu.Lock(); defer c.mu.Unlock(); return c.err }
func (c *Conn) Close()                { c.fail(ErrClosed) }
func (c *Conn) fail(err error) {
	c.stop.Do(func() {
		c.mu.Lock()
		c.err = err
		c.cancel()
		for _, cancel := range c.running {
			cancel()
		}
		clear(c.running)
		clear(c.pending)
		close(c.done)
		c.mu.Unlock()
		if c.closeIO != nil {
			c.closeIO()
		}
	})
}
func (c *Conn) send(ctx context.Context, m Message) error {
	m.JSONRPC = "2.0"
	return c.sendValue(ctx, m)
}

func (c *Conn) sendValue[T any](ctx context.Context, message T) error {
	select {
	case <-c.done:
		return c.Err()
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	data, err := encodeMessage(message)
	if err != nil {
		return err
	}
	return c.enqueue(ctx, outbound{data: data})
}

func (c *Conn) enqueue(ctx context.Context, packet outbound) (err error) {
	// Shutdown waits for enqueuers before draining, including a select that
	// races between a ready queue and a closed connection.
	c.sendMu.RLock()
	defer c.sendMu.RUnlock()
	defer func() {
		if err != nil {
			releaseMessage(packet.data)
		}
	}()
	select {
	case <-c.done:
		return c.Err()
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	select {
	case <-c.done:
		return c.Err()
	case <-ctx.Done():
		return ctx.Err()
	case c.outgoing <- packet:
		return nil
	}
}

func (c *Conn) Notify(ctx context.Context, method string, params any) error {
	return c.notify(ctx, method, params)
}

func (c *Conn) notify[P any](ctx context.Context, method string, params P) error {
	if method == "" {
		return errors.New("lsp: empty method")
	}
	return c.sendValue(ctx, requestMessage[P]{JSONRPC: "2.0", Method: method, Params: params})
}

// PendingCall owns one response slot. Exactly one Wait must follow BeginCall,
// including when its context has been cancelled, to release the registration.
type PendingCall struct {
	conn     *Conn
	id       string
	response <-chan response
}

// BeginCall enqueues the request before returning. A session can therefore keep
// didOpen/didChange/requests ordered while waiting for responses concurrently.
func (c *Conn) BeginCall(ctx context.Context, method string, params any) (PendingCall, error) {
	if method == "" {
		return PendingCall{}, errors.New("lsp: empty method")
	}
	ch := make(chan response, 1)
	c.mu.Lock()
	c.next++
	id := strconv.FormatUint(c.next, 10)
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.sendValue(ctx, requestMessage[any]{JSONRPC: "2.0", ID: json.RawMessage(id), Method: method, Params: params}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return PendingCall{}, err
	}
	return PendingCall{conn: c, id: id, response: ch}, nil
}

func (call PendingCall) Wait(ctx context.Context, result any) error {
	c := call.conn
	defer func() { c.mu.Lock(); delete(c.pending, call.id); c.mu.Unlock() }()
	select {
	case reply := <-call.response:
		if reply.err != nil {
			return reply.err
		}
		if result == nil {
			return nil
		}
		return json.Unmarshal(reply.data, result)
	case <-c.done:
		return c.Err()
	case <-ctx.Done():
		// Cancellation must never wait for a blocked server pipe or a full queue.
		c.cancelRequest(call.id)
		return ctx.Err()
	}
}

func (c *Conn) cancelRequest(id string) {
	type params struct {
		ID json.RawMessage `json:"id"`
	}
	data, err := encodeMessage(requestMessage[params]{JSONRPC: "2.0", Method: "$/cancelRequest", Params: params{json.RawMessage(id)}})
	if err != nil {
		return
	}
	c.sendMu.RLock()
	defer c.sendMu.RUnlock()
	select {
	case <-c.done:
		releaseMessage(data)
		return
	default:
	}
	select {
	case c.outgoing <- outbound{data: data}:
	default:
		releaseMessage(data)
	}
}

func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	call, err := c.BeginCall(ctx, method, params)
	if err != nil {
		return err
	}
	return call.Wait(ctx, result)
}

// Request is a typed convenience without a method whitelist.
func Request[P, R any](ctx context.Context, c *Conn, method string, params P) (R, error) {
	var result R
	err := c.Call(ctx, method, params, &result)
	return result, err
}
func validID(id json.RawMessage) bool {
	if len(id) == 0 || bytes.Equal(id, []byte("null")) {
		return false
	}
	var value any
	if json.Unmarshal(id, &value) != nil {
		return false
	}
	switch v := value.(type) {
	case string:
		return true
	case float64:
		return v == float64(int64(v))
	}
	return false
}
func (c *Conn) read(input io.Reader) {
	r := NewReader(input)
	for {
		data, err := r.Read()
		if err != nil {
			c.fail(err)
			return
		}
		var m Message
		if err := json.Unmarshal(data, &m); err != nil || !utf8.Valid(data) || m.JSONRPC != "2.0" || len(m.ID) != 0 && !validID(m.ID) {
			c.fail(errors.New("lsp: invalid JSON-RPC message"))
			return
		}
		if m.Method == "" {
			if !validID(m.ID) || (m.Result == nil) == (m.Error == nil) {
				c.fail(errors.New("lsp: invalid response"))
				return
			}
			c.mu.Lock()
			ch := c.pending[string(m.ID)]
			if ch != nil {
				delete(c.pending, string(m.ID))
			}
			c.mu.Unlock()
			if ch != nil {
				reply := response{data: m.Result}
				if m.Error != nil {
					reply.err = m.Error
				}
				ch <- reply
			}
			continue
		}
		if m.Result != nil || m.Error != nil {
			c.fail(errors.New("lsp: invalid request"))
			return
		}
		if m.Method == "$/cancelRequest" {
			var p struct {
				ID json.RawMessage `json:"id"`
			}
			if json.Unmarshal(m.Params, &p) == nil {
				c.mu.Lock()
				cancel := c.running[string(p.ID)]
				c.mu.Unlock()
				if cancel != nil {
					cancel()
				}
			}
			continue
		}
		if len(m.ID) != 0 {
			ctx, cancel := context.WithCancel(c.ctx)
			c.mu.Lock()
			if _, exists := c.running[string(m.ID)]; exists {
				c.mu.Unlock()
				cancel()
				c.fail(errors.New("lsp: duplicate request id"))
				return
			}
			c.running[string(m.ID)] = cancel
			c.mu.Unlock()
			// Keep the context alongside the ordered work item without another queue.
			mctx := inbound{ctx: ctx, message: m}
			select {
			case c.incoming <- mctx:
			case <-c.done:
				cancel()
				return
			default:
				cancel()
				c.fail(errors.New("lsp: server request queue full"))
				return
			}
		} else {
			select {
			case c.incoming <- inbound{c.ctx, m}:
			case <-c.done:
				return
			default:
				c.fail(errors.New("lsp: notification queue full"))
				return
			}
		}
	}
}
func (c *Conn) write(output io.Writer) {
	defer func() {
		c.sendMu.Lock()
		defer c.sendMu.Unlock()
		for {
			select {
			case packet := <-c.outgoing:
				releaseMessage(packet.data)
			default:
				return
			}
		}
	}()
	w := Writer{Target: output}
	for {
		select {
		case <-c.done:
			return
		case packet := <-c.outgoing:
			if packet.data != nil {
				err := w.Write(packet.data.Bytes())
				releaseMessage(packet.data)
				if err != nil {
					c.fail(err)
					return
				}
			}
			if packet.ack != nil {
				close(packet.ack)
			}
		}
	}
}
func (c *Conn) dispatch() {
	for {
		select {
		case <-c.done:
			return
		case work := <-c.incoming:
			m := work.message
			var result any
			var err error
			if c.handler != nil {
				result, err = c.handler(work.ctx, m.Method, m.Params)
			} else {
				err = &ResponseError{Code: MethodNotFound, Message: m.Method}
			}
			if len(m.ID) == 0 {
				continue
			}
			if work.ctx.Err() != nil {
				err = &ResponseError{Code: RequestCancelled, Message: work.ctx.Err().Error()}
			}
			c.mu.Lock()
			cancel := c.running[string(m.ID)]
			delete(c.running, string(m.ID))
			c.mu.Unlock()
			if cancel != nil {
				cancel()
			}
			reply := Message{ID: m.ID}
			if err != nil {
				if !errors.As(err, &reply.Error) {
					reply.Error = &ResponseError{Code: InternalError, Message: err.Error()}
				}
			} else {
				reply.Result, err = json.Marshal(result)
				if err != nil {
					reply.Error = &ResponseError{Code: InternalError, Message: err.Error()}
				}
			}
			if err := c.send(context.Background(), reply); err != nil {
				return
			}
		}
	}
}

// Flush waits until preceding messages have reached the server pipe.
func (c *Conn) Flush(ctx context.Context) error {
	ack := make(chan struct{})
	if err := c.enqueue(ctx, outbound{ack: ack}); err != nil {
		return err
	}
	select {
	case <-ack:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.Err()
	}
}
