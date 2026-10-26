// Package dap implements the Debug Adapter Protocol independently of the UI
// and of the transport used to reach the adapter.
package dap

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

const MaxMessage = 32 << 20

var ErrClosed = errors.New("dap: connection closed")

type Message struct {
	Seq        int             `json:"seq"`
	Type       string          `json:"type"`
	Command    string          `json:"command,omitempty"`
	Arguments  json.RawMessage `json:"arguments,omitempty"`
	RequestSeq int             `json:"request_seq,omitempty"`
	Success    bool            `json:"success"`
	Message    string          `json:"message,omitempty"`
	Event      string          `json:"event,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
}

type Handler func(context.Context, string, json.RawMessage) (any, error)

type outbound struct {
	ctx  context.Context
	data []byte
}

type Conn struct {
	stream     io.ReadWriteCloser
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	eventsDone chan struct{}
	once       sync.Once
	mu         sync.Mutex
	writeMu    sync.Mutex
	next       int
	pending    map[int]chan Message
	err        error
	events     chan Message
	outgoing   chan outbound
	onEvent    func(string, json.RawMessage)
	handler    Handler
}

// New owns stream. Event callbacks must return promptly; requests may be made
// from a callback because responses are routed by the independent reader.
func New(stream io.ReadWriteCloser, event func(string, json.RawMessage), handler Handler) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{stream: stream, ctx: ctx, cancel: cancel, done: make(chan struct{}), eventsDone: make(chan struct{}), pending: make(map[int]chan Message), events: make(chan Message, 256), outgoing: make(chan outbound, 64), onEvent: event, handler: handler}
	go c.read()
	go c.write()
	go c.dispatch()
	return c
}

func (c *Conn) Done() <-chan struct{} { return c.done }

// EventsDone closes after queued adapter events have been delivered, including
// terminal events immediately preceding EOF.
func (c *Conn) EventsDone() <-chan struct{} { return c.eventsDone }
func (c *Conn) Err() error                  { c.mu.Lock(); defer c.mu.Unlock(); return c.err }
func (c *Conn) Close() error                { c.fail(ErrClosed); return nil }

func (c *Conn) fail(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.err = err
		clear(c.pending)
		c.mu.Unlock()
		c.cancel()
		close(c.done)
		_ = c.stream.Close()
	})
}

func (c *Conn) send(m Message) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.next++
	m.Seq = c.next
	c.mu.Unlock()
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	select {
	case c.outgoing <- outbound{c.ctx, data}:
		return nil
	case <-c.done:
		return c.Err()
	}
}

// Call preserves adapter-specific arguments and results without a closed
// command enumeration. Cancellation removes the pending request immediately.
func (c *Conn) Call(ctx context.Context, command string, args any, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(args)
	if err != nil {
		return err
	}
	// Register and write under the same lock so every outgoing sequence is
	// monotonic, including responses to adapter-initiated requests.
	c.writeMu.Lock()
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		c.writeMu.Unlock()
		return err
	}
	c.next++
	id := c.next
	reply := make(chan Message, 1)
	c.pending[id] = reply
	c.mu.Unlock()
	m, err := json.Marshal(Message{Seq: id, Type: "request", Command: command, Arguments: data})
	if err == nil {
		select {
		case c.outgoing <- outbound{ctx, m}:
		case <-ctx.Done():
			err = ctx.Err()
		case <-c.done:
			err = c.Err()
		}
	}
	c.writeMu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case response := <-reply:
		if !response.Success {
			return fmt.Errorf("dap %s: %s", command, response.Message)
		}
		if result != nil && len(response.Body) != 0 {
			return json.Unmarshal(response.Body, result)
		}
		return nil
	case <-c.done:
		return c.Err()
	}
}

func (c *Conn) write() {
	for {
		select {
		case <-c.done:
			return
		case message := <-c.outgoing:
			if message.ctx.Err() != nil {
				continue
			}
			if err := writeMessage(c.stream, message.data); err != nil {
				c.fail(err)
				return
			}
		}
	}
}

func Call[A, R any](ctx context.Context, c *Conn, command string, args A) (R, error) {
	var result R
	err := c.Call(ctx, command, args, &result)
	return result, err
}

func writeMessage(w io.Writer, data []byte) error {
	if len(data) == 0 || len(data) > MaxMessage {
		return errors.New("dap: invalid message size")
	}
	var header [64]byte
	h := append(header[:0], "Content-Length: "...)
	h = strconv.AppendInt(h, int64(len(data)), 10)
	h = append(h, '\r', '\n', '\r', '\n')
	for _, p := range [][]byte{h, data} {
		for len(p) > 0 {
			n, err := w.Write(p)
			if err != nil {
				return err
			}
			if n <= 0 || n > len(p) {
				return io.ErrShortWrite
			}
			p = p[n:]
		}
	}
	return nil
}

func readMessage(r *bufio.Reader, buffer []byte) ([]byte, error) {
	length, size := -1, 0
	for {
		line, err := r.ReadSlice('\n')
		if err != nil {
			return nil, err
		}
		size += len(line)
		if size > 8192 || len(line) < 2 || line[len(line)-2] != '\r' {
			return nil, errors.New("dap: invalid header")
		}
		line = line[:len(line)-2]
		if len(line) == 0 {
			break
		}
		name, value, ok := bytes.Cut(line, []byte(":"))
		if !ok {
			return nil, errors.New("dap: invalid header field")
		}
		if bytes.EqualFold(name, []byte("Content-Length")) {
			if length != -1 {
				return nil, errors.New("dap: duplicate Content-Length")
			}
			value = bytes.TrimSpace(value)
			if len(value) == 0 {
				return nil, errors.New("dap: empty Content-Length")
			}
			for _, b := range value {
				if b < '0' || b > '9' {
					return nil, errors.New("dap: invalid Content-Length")
				}
			}
			length, err = strconv.Atoi(string(value))
			if err != nil || length <= 0 || length > MaxMessage {
				return nil, errors.New("dap: invalid message size")
			}
		}
	}
	if length < 0 {
		return nil, errors.New("dap: missing Content-Length")
	}
	if cap(buffer) < length {
		buffer = make([]byte, length)
	}
	buffer = buffer[:length]
	_, err := io.ReadFull(r, buffer)
	return buffer, err
}

func (c *Conn) read() {
	defer close(c.events)
	r := bufio.NewReaderSize(c.stream, 8192)
	var buffer []byte
	for {
		var err error
		buffer, err = readMessage(r, buffer)
		if err != nil {
			c.fail(err)
			return
		}
		var m Message
		if err = json.Unmarshal(buffer, &m); err != nil {
			c.fail(err)
			return
		}
		switch m.Type {
		case "response":
			c.mu.Lock()
			ch := c.pending[m.RequestSeq]
			delete(c.pending, m.RequestSeq)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		case "event", "request":
			select {
			case c.events <- m:
			case <-c.done:
				return
			}
		default:
			c.fail(errors.New("dap: invalid message type"))
			return
		}
	}
}

func (c *Conn) dispatch() {
	defer close(c.eventsDone)
	for m := range c.events {
		if m.Type == "event" {
			if c.onEvent != nil {
				c.onEvent(m.Event, m.Body)
			}
			continue
		}
		// A terminal launch may take time. Keep dispatching lifecycle events
		// while the adapter waits for its response.
		go func() {
			var result any
			err := fmt.Errorf("unsupported adapter request: %s", m.Command)
			if c.handler != nil {
				result, err = c.handler(c.ctx, m.Command, m.Arguments)
			}
			response := Message{Type: "response", RequestSeq: m.Seq, Command: m.Command, Success: err == nil}
			if err != nil {
				response.Message = err.Error()
			} else if result != nil {
				response.Body, err = json.Marshal(result)
				if err != nil {
					response.Success = false
					response.Message = err.Error()
				}
			}
			_ = c.send(response)
		}()
	}
}
