package terminal

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

var ErrInputFull = errors.New("terminal input queue is full")
var ErrClosed = errors.New("terminal session is closed")

type Process interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
	Wait() error
}

// Session bounds both queues. Output applies backpressure instead of dropping
// bytes (dropping even one byte can corrupt a VT sequence). No worker touches UI.
type Session struct {
	mu                    sync.Mutex
	output, input         byteRing
	changed               *sync.Cond
	signal                chan struct{}
	done                  chan struct{}
	process               Process
	closed, ready, exited bool
	err                   error
	cols, rows            int
	wake                  func()
}
type byteRing struct {
	data       []byte
	head, size int
}

func (r *byteRing) write(p []byte) int {
	n := min(len(p), len(r.data)-r.size)
	tail := (r.head + r.size) % len(r.data)
	first := copy(r.data[tail:], p[:n])
	copy(r.data, p[first:n])
	r.size += n
	return n
}
func (r *byteRing) read(p []byte) int {
	n := min(len(p), r.size)
	first := copy(p[:n], r.data[r.head:])
	copy(p[first:n], r.data[:n-first])
	r.head = (r.head + n) % len(r.data)
	r.size -= n
	return n
}
func Start(directory string, cols, rows int, wake func()) *Session {
	return Attach(cols, rows, wake, func() (Process, error) { return startNative(directory, cols, rows) })
}

// OpenProcess exposes the native PTY only to headless service owners.
func OpenProcess(directory string, cols, rows int) (Process, error) {
	return startNative(directory, max(2, min(cols, 1000)), max(1, min(rows, 500)))
}

// Attach keeps VT queues local while the process can live on another machine.
func Attach(cols, rows int, wake func(), open func() (Process, error)) *Session {
	s := &Session{cols: cols, rows: rows, wake: wake, signal: make(chan struct{}, 1), done: make(chan struct{}), output: byteRing{data: make([]byte, 256<<10)}, input: byteRing{data: make([]byte, 64<<10)}}
	s.changed = sync.NewCond(&s.mu)
	go s.run(open)
	return s
}
func (s *Session) notify() {
	if s.wake != nil {
		s.wake()
	}
}
func (s *Session) run(open func() (Process, error)) {
	defer close(s.done)
	p, err := open()
	s.mu.Lock()
	s.ready = true
	s.err = err
	if err != nil {
		s.exited = true
		s.mu.Unlock()
		s.notify()
		return
	}
	s.process = p
	closed := s.closed
	s.mu.Unlock()
	if closed {
		// ConPTY must always have a reader while ClosePseudoConsole drains output.
		drained := make(chan struct{})
		go func() { _, _ = io.Copy(io.Discard, p); close(drained) }()
		_ = p.Close()
		_ = p.Wait()
		<-drained
		return
	}
	s.notify()
	s.poke()
	writerDone := make(chan struct{})
	go func() { defer close(writerDone); s.writeLoop(p) }()
	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		err := p.Wait()
		s.mu.Lock()
		s.exited = true
		if s.err == nil {
			s.err = err
		}
		s.mu.Unlock()
		s.notify()
	}()
	var buffer [16384]byte
	for {
		n, readErr := p.Read(buffer[:])
		if n > 0 {
			offset := 0
			s.mu.Lock()
			for offset < n && !s.closed {
				count := s.output.write(buffer[offset:n])
				offset += count
				if count > 0 {
					s.mu.Unlock()
					s.notify()
					s.mu.Lock()
				}
				if offset < n && !s.closed && s.output.size == len(s.output.data) {
					s.changed.Wait()
				}
			}
			s.mu.Unlock()
			// After cancellation keep draining until the native close signals
			// EOF. Windows may emit final VT output while closing ConPTY.
		}
		if readErr != nil {
			break
		}
	}
	_ = p.Close()
	s.mu.Lock()
	s.closed = true
	s.changed.Broadcast()
	s.mu.Unlock()
	s.poke()
	<-writerDone
	<-waitDone
	s.notify()
}
func (s *Session) poke() {
	select {
	case s.signal <- struct{}{}:
	default:
	}
}
func (s *Session) writeLoop(p Process) {
	var buffer [16384]byte
	for {
		<-s.signal
		for {
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}
			n := s.input.read(buffer[:])
			cols, rows := s.cols, s.rows
			s.mu.Unlock()
			if err := p.Resize(cols, rows); err != nil {
				s.fail(err)
				return
			}
			if n == 0 {
				break
			}
			data := buffer[:n]
			for len(data) > 0 {
				written, err := p.Write(data)
				if err != nil {
					s.fail(err)
					return
				}
				if written == 0 {
					s.fail(io.ErrShortWrite)
					return
				}
				data = data[written:]
			}
		}
	}
}
func (s *Session) fail(err error) {
	s.mu.Lock()
	if !s.closed && !s.exited {
		s.err = err
	}
	s.mu.Unlock()
	s.notify()
	s.Close()
}
func (s *Session) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.exited {
		return 0, ErrClosed
	}
	if len(data) > len(s.input.data)-s.input.size {
		return 0, ErrInputFull
	}
	n := s.input.write(data)
	s.poke()
	return n, nil
}
func (s *Session) ReadOutput(dst []byte) int {
	s.mu.Lock()
	n := s.output.read(dst)
	more := s.output.size > 0
	s.changed.Signal()
	s.mu.Unlock()
	if more {
		s.notify()
	}
	return n
}
func (s *Session) Status() (ready, exited bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready, s.exited, s.err
}
func (s *Session) Resize(cols, rows int) {
	cols, rows = max(2, min(cols, 1000)), max(1, min(rows, 500))
	s.mu.Lock()
	changed := s.cols != cols || s.rows != rows
	s.cols, s.rows = cols, rows
	s.mu.Unlock()
	if changed {
		s.poke()
	}
}

// Close never waits for a process or pipe on the foreground thread.
func (s *Session) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	p := s.process
	s.changed.Broadcast()
	s.mu.Unlock()
	s.poke()
	if p != nil {
		go p.Close()
	}
}
func (s *Session) Done() <-chan struct{} { return s.done }
func shellCommand(directory string) *exec.Cmd {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
		if runtime.GOOS == "android" {
			shell = "/system/bin/sh"
		}
	}
	cmd := exec.Command(shell, "-i")
	cmd.Dir = directory
	env := os.Environ()
	cmd.Env = make([]string, 0, len(env)+3)
	for _, entry := range env {
		if strings.HasPrefix(entry, "TERM=") || strings.HasPrefix(entry, "COLORTERM=") || strings.HasPrefix(entry, "TERM_PROGRAM=") {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=pencode")
	return cmd
}
