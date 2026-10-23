package server

import (
	"context"
	"encoding/binary"
	"io"
	"sync"

	"github.com/pencode-labs/pencode/protocol"
)

type serviceChunk struct {
	data [protocol.ChunkSize]byte
	n    int
	err  error
}
type readOnlyStream struct{ io.ReadCloser }

func (readOnlyStream) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

type serviceStream struct {
	root                 string
	extension            uint64
	related              *serviceStream
	readerDone, waitDone chan struct{}
	waitErr              error
	process              io.ReadWriteCloser
	ready                chan *serviceChunk
	free                 chan *serviceChunk
	done                 chan struct{}
	once                 sync.Once
	readMu, writeMu      sync.Mutex
	ended                bool
}

func newServiceStream(ctx context.Context, p io.ReadWriteCloser, related *serviceStream) *serviceStream {
	const chunks = protocol.StreamWindow / protocol.ChunkSize
	s := &serviceStream{process: p, related: related, ready: make(chan *serviceChunk, chunks), free: make(chan *serviceChunk, chunks), done: make(chan struct{}), readerDone: make(chan struct{}), waitDone: make(chan struct{})}
	for range chunks {
		s.free <- new(serviceChunk)
	}
	if p, ok := p.(interface{ Wait() error }); ok {
		go func() { defer close(s.waitDone); s.waitErr = p.Wait() }()
	} else {
		close(s.waitDone)
	}
	go func() {
		defer close(s.readerDone)
		stop := context.AfterFunc(ctx, s.close)
		defer stop()
		for {
			var chunk *serviceChunk
			select {
			case <-s.done:
				_, _ = io.Copy(io.Discard, p)
				return
			case chunk = <-s.free:
			}
			chunk.n, chunk.err = p.Read(chunk.data[:])
			if chunk.n == 0 && chunk.err == nil {
				chunk.err = io.ErrNoProgress
			}
			ended := chunk.err != nil
			select {
			case <-s.done:
				_, _ = io.Copy(io.Discard, p)
				return
			case s.ready <- chunk:
			}
			if ended {
				return
			}
		}
	}()
	return s
}
func (s *serviceStream) read(ctx context.Context, e *protocol.Encoder) error {
	s.readMu.Lock()
	defer s.readMu.Unlock()
	if s.ended {
		e.U32(1)
		e.Bytes(nil)
		return nil
	}
	// Wait for the first bytes only. Drain already available chunks without
	// delaying interactive shell output to fill the bulk transfer window.
	var chunk *serviceChunk
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		e.U32(1)
		e.Bytes(nil)
		return nil
	case chunk = <-s.ready:
	}
	header := len(e.Buffer)
	e.U32(0)
	e.U32(0)
	end := header + 8 + protocol.StreamWindow
	for {
		s.ended = chunk.err != nil
		e.Buffer = append(e.Buffer, chunk.data[:chunk.n]...)
		chunk.n = 0
		chunk.err = nil
		s.free <- chunk
		if s.ended || len(e.Buffer)+protocol.ChunkSize > end {
			break
		}
		select {
		case chunk = <-s.ready:
			continue
		default:
		}
		break
	}
	if s.ended {
		binary.LittleEndian.PutUint32(e.Buffer[header:], 1)
	}
	binary.LittleEndian.PutUint32(e.Buffer[header+4:], uint32(len(e.Buffer)-header-8))
	return nil
}
func (s *serviceStream) close() {
	s.once.Do(func() {
		close(s.done)
		_ = s.process.Close()
		<-s.readerDone
		<-s.waitDone
		if s.related != nil {
			s.related.close()
		}
	})
}
