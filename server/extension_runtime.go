package server

import (
	"bytes"
	"encoding/binary"
	"io"
	"sync"

	"github.com/pencode-labs/pencode/peb"
	"github.com/pencode-labs/pencode/protocol"
)

// extensionRuntime belongs to one connection/window and outlives its views.
// Each virtual stdio stream retains its own request IDs and unsolicited updates.
type extensionRuntime struct {
	peer        io.ReadWriteCloser
	closing     chan *extensionPipe
	sessions    map[uint32]*extensionPipe
	done        chan struct{}
	reaped      chan struct{}
	readerDone  chan struct{}
	logsDone    chan struct{}
	controlDone chan struct{}
	writer      peb.Writer
	logs        extensionLogs
	once        sync.Once
	mu          sync.Mutex
	next        uint32
	retiring    int
}

func newExtensionRuntime(peer io.ReadWriteCloser, logs io.ReadCloser) *extensionRuntime {
	r := &extensionRuntime{peer: peer, writer: peb.Writer{Target: peer}, sessions: make(map[uint32]*extensionPipe),
		done: make(chan struct{}), reaped: make(chan struct{}), readerDone: make(chan struct{}), logsDone: make(chan struct{}), controlDone: make(chan struct{}), closing: make(chan *extensionPipe, peb.MaxSessions)}
	r.logs.ready.L = &r.logs.mu
	go func() { defer close(r.logsDone); defer logs.Close(); _, _ = io.Copy(&r.logs, logs); r.logs.finish() }()
	go func() {
		defer close(r.readerDone)
		reader := peb.Reader{Source: peer}
		for {
			frame, err := reader.Read()
			if err != nil {
				r.stop(err)
				return
			}
			r.mu.Lock()
			s := r.sessions[frame.Session]
			invalid := frame.Session == 0 || frame.Session > r.next
			r.mu.Unlock()
			if invalid {
				r.stop(peb.ErrProtocol)
				return
			}
			if s == nil {
				continue
			} // A closed view may still have a reply in flight.
			if err := s.output.Write(frame.Kind, frame.ID, frame.Payload); err != nil {
				_ = s.Close()
			}
		}
	}()
	go func() {
		defer close(r.controlDone)
		for {
			select {
			case <-r.done:
				return
			case s := <-r.closing:
				s.mu.Lock()
				var err error
				if s.initialized {
					err = r.writer.WriteSession(s.id, peb.Shutdown, 0, nil)
				}
				s.mu.Unlock()
				r.mu.Lock()
				r.retiring--
				r.mu.Unlock()
				if err != nil {
					r.stop(err)
					return
				}
			}
		}
	}()
	return r
}

func (r *extensionRuntime) open() (*extensionPipe, *extensionLogReader, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	select {
	case <-r.done:
		return nil, nil, io.ErrClosedPipe
	default:
	}
	if len(r.sessions)+r.retiring >= peb.MaxSessions || r.next == ^uint32(0) {
		return nil, nil, &protocol.Error{Code: protocol.Busy, Message: "extension session budget exceeded"}
	}
	r.next++
	s := &extensionPipe{runtime: r, id: r.next}
	s.buffer.ready.L = &s.buffer.mu
	s.reader.Source = &s.source
	s.output.Target = &s.buffer
	log := &extensionLogReader{logs: &r.logs}
	r.logs.mu.Lock()
	if r.logs.written > uint64(len(r.logs.data)) {
		log.offset = r.logs.written - uint64(len(r.logs.data))
	}
	r.logs.mu.Unlock()
	s.logs = log
	r.sessions[s.id] = s
	return s, log, nil
}

func (r *extensionRuntime) stop(err error) {
	r.once.Do(func() {
		r.mu.Lock()
		close(r.done)
		for _, s := range r.sessions {
			s.buffer.finish(err)
		}
		clear(r.sessions)
		r.mu.Unlock()
		go func() { _ = r.peer.Close(); close(r.reaped) }()
	})
}
func (r *extensionRuntime) Close() {
	r.stop(io.EOF)
	<-r.reaped
	<-r.readerDone
	<-r.logsDone
	<-r.controlDone
}
func (r *extensionRuntime) alive() bool {
	select {
	case <-r.done:
		return false
	default:
		return true
	}
}

type extensionPipe struct {
	runtime     *extensionRuntime
	logs        *extensionLogReader
	input       []byte
	output      peb.Writer
	source      bytes.Reader
	buffer      extensionBuffer
	reader      peb.Reader
	once        sync.Once
	mu          sync.Mutex
	id          uint32
	initialized bool
}

func (s *extensionPipe) Read(p []byte) (int, error) { return s.buffer.Read(p) }
func (s *extensionPipe) Wait() error {
	b := &s.buffer
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.err == nil {
		b.ready.Wait()
	}
	if b.err == io.EOF {
		return nil
	}
	return b.err
}
func (s *extensionPipe) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buffer.mu.Lock()
	closed := s.buffer.err != nil
	s.buffer.mu.Unlock()
	if closed {
		return 0, io.ErrClosedPipe
	}
	size := len(data)
	// CS writes may split a frame anywhere, including inside the PEB header.
	for len(data) != 0 {
		need := peb.HeaderSize
		if len(s.input) >= peb.HeaderSize {
			payload := binary.LittleEndian.Uint32(s.input[12:])
			if payload > peb.MaxPayload {
				return 0, peb.ErrProtocol
			}
			need += int(payload)
		}
		n := min(need-len(s.input), len(data))
		s.input = append(s.input, data[:n]...)
		data = data[n:]
		if len(s.input) < peb.HeaderSize {
			continue
		}
		payload := binary.LittleEndian.Uint32(s.input[12:])
		if payload > peb.MaxPayload {
			return 0, peb.ErrProtocol
		}
		if len(s.input) != peb.HeaderSize+int(payload) {
			continue
		}
		s.source.Reset(s.input)
		f, err := s.reader.Read()
		if err != nil {
			return 0, err
		}
		if f.Session != 0 {
			return 0, peb.ErrProtocol
		}
		if err = s.runtime.writer.WriteSession(s.id, f.Kind, f.ID, f.Payload); err != nil {
			s.runtime.stop(err)
			return 0, err
		}
		if f.Kind == peb.Initialize {
			s.initialized = true
		}
		s.input = s.input[:0]
	}
	return size, nil
}
func (s *extensionPipe) Close() error {
	s.once.Do(func() {
		s.runtime.mu.Lock()
		delete(s.runtime.sessions, s.id)
		s.runtime.retiring++
		s.runtime.mu.Unlock()
		s.buffer.finish(io.EOF)
		_ = s.logs.Close()
		select {
		case s.runtime.closing <- s:
		case <-s.runtime.done:
		default:
			s.runtime.stop(io.ErrShortBuffer)
		}
	})
	return nil
}

// A bounded growing ring keeps a slow/abandoned view off the process reader.
// Cached frame routing does not allocate. Overflow closes only that view.
type extensionBuffer struct {
	err   error
	ready sync.Cond
	data  []byte
	start int
	used  int
	mu    sync.Mutex
}

const extensionBacklog = 2 * (peb.MaxPayload + peb.HeaderSize)

func (b *extensionBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return 0, b.err
	}
	required := b.used + len(data)
	if required > extensionBacklog {
		return 0, io.ErrShortBuffer
	}
	if required > len(b.data) {
		next := make([]byte, min(extensionBacklog, max(4096, required, 2*len(b.data))))
		n := copy(next, b.data[b.start:min(len(b.data), b.start+b.used)])
		copy(next[n:], b.data[:b.used-n])
		b.data, b.start = next, 0
	}
	if len(data) > 0 {
		end := (b.start + b.used) % len(b.data)
		n := copy(b.data[end:], data)
		copy(b.data, data[n:])
	}
	b.used = required
	b.ready.Broadcast()
	return len(data), nil
}
func (b *extensionBuffer) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.used == 0 && b.err == nil {
		b.ready.Wait()
	}
	if b.used == 0 {
		return 0, b.err
	}
	n := copy(p, b.data[b.start:min(len(b.data), b.start+b.used)])
	n += copy(p[n:], b.data[:min(b.used-n, len(p)-n)])
	b.start = (b.start + n) % len(b.data)
	b.used -= n
	return n, nil
}
func (b *extensionBuffer) finish(err error) {
	b.mu.Lock()
	if b.err == nil {
		b.err = err
	}
	b.ready.Broadcast()
	b.mu.Unlock()
}

// Diagnostics are retained once per process. Independent readers replay the
// bounded tail and then follow it without blocking stderr or other views.
type extensionLogs struct {
	ready   sync.Cond
	written uint64
	mu      sync.Mutex
	closed  bool
	data    [64 << 10]byte
}

func (l *extensionLogs) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	size := len(data)
	if len(data) > len(l.data) {
		l.written += uint64(len(data) - len(l.data))
		data = data[len(data)-len(l.data):]
	}
	start := int(l.written % uint64(len(l.data)))
	n := copy(l.data[start:], data)
	copy(l.data[:], data[n:])
	l.written += uint64(len(data))
	l.ready.Broadcast()
	return size, nil
}
func (l *extensionLogs) finish() { l.mu.Lock(); l.closed = true; l.ready.Broadcast(); l.mu.Unlock() }

type extensionLogReader struct {
	logs   *extensionLogs
	offset uint64
	closed bool
}

func (r *extensionLogReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	l := r.logs
	l.mu.Lock()
	defer l.mu.Unlock()
	for r.offset == l.written && !l.closed && !r.closed {
		l.ready.Wait()
	}
	if r.closed || r.offset == l.written {
		return 0, io.EOF
	}
	if l.written-r.offset > uint64(len(l.data)) {
		r.offset = l.written - uint64(len(l.data))
	}
	start := int(r.offset % uint64(len(l.data)))
	count := min(len(p), int(l.written-r.offset))
	n := copy(p[:count], l.data[start:])
	n += copy(p[n:count], l.data[:count-n])
	r.offset += uint64(n)
	return n, nil
}
func (r *extensionLogReader) Close() error {
	r.logs.mu.Lock()
	r.closed = true
	r.logs.ready.Broadcast()
	r.logs.mu.Unlock()
	return nil
}
