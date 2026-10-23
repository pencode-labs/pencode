package server

import (
	"context"
	"io"
	"os"

	"github.com/diiyw/onix/projects/pencode/protocol"
	"github.com/diiyw/onix/projects/pencode/terminal"
	"github.com/diiyw/onix/projects/pencode/transport"
)

func (s *services) process(ctx context.Context, op protocol.Operation, root string, d *protocol.Decoder, e *protocol.Encoder) error {
	switch op {
	case protocol.ProcessOpen, protocol.TerminalOpen:
		var p io.ReadWriteCloser
		var logs *os.File
		var err error
		cols, rows := int(d.U32()), int(d.U32())
		name := d.String()
		count := d.U32()
		if count > 4096 {
			return protocol.ErrProtocol
		}
		args := make([]string, count)
		for i := range args {
			args[i] = d.String()
		}
		if err := d.Done(); err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		if root != "" && !s.roots[root] {
			return &protocol.Error{Code: protocol.NotFound, Message: "workspace is closed"}
		}
		needed := 1
		if op == protocol.ProcessOpen {
			needed = 2
		}
		if len(s.streams)+needed > 16 {
			return &protocol.Error{Code: protocol.Busy, Message: "process budget exceeded"}
		}
		if op == protocol.TerminalOpen {
			p, err = terminal.OpenProcess(root, cols, rows)
		} else {
			var childErr *os.File
			logs, childErr, err = os.Pipe()
			if err == nil {
				p, err = transport.StartWithOutput(root, name, childErr, args...)
				childErr.Close()
			}
		}
		if err != nil {
			if logs != nil {
				logs.Close()
			}
			return err
		}
		s.attachProcess(p, logs, root, 0, e)

	case protocol.StreamRead, protocol.StreamWrite, protocol.StreamResize, protocol.StreamClose, protocol.StreamWait:
		id := d.U64()
		if op == protocol.StreamClose {
			if err := d.Done(); err != nil {
				return err
			}
		}
		s.mu.Lock()
		p := s.streams[id]
		if op == protocol.StreamClose {
			delete(s.streams, id)
		}
		s.mu.Unlock()
		if p == nil {
			return &protocol.Error{Code: protocol.NotFound, Message: "process is closed"}
		}
		switch op {
		case protocol.StreamWait:
			if err := d.Done(); err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-p.waitDone:
				return p.waitErr
			}
		case protocol.StreamRead:
			if err := d.Done(); err != nil {
				return err
			}
			return p.read(ctx, e)
		case protocol.StreamWrite:
			data := d.Bytes()
			if err := d.Done(); err != nil {
				return err
			}
			if len(data) > protocol.StreamWindow {
				return protocol.ErrProtocol
			}
			stop := context.AfterFunc(ctx, p.close)
			defer stop()
			p.writeMu.Lock()
			defer p.writeMu.Unlock()
			for len(data) > 0 {
				n, err := p.process.Write(data)
				if err != nil {
					return err
				}
				if n == 0 {
					return io.ErrShortWrite
				}
				data = data[n:]
			}
		case protocol.StreamResize:
			p.writeMu.Lock()
			defer p.writeMu.Unlock()
			cols, rows := int(d.U32()), int(d.U32())
			if err := d.Done(); err != nil {
				return err
			}
			if terminal, ok := p.process.(interface{ Resize(int, int) error }); ok {
				return terminal.Resize(max(2, min(cols, 1000)), max(1, min(rows, 500)))
			}
			return protocol.ErrProtocol
		case protocol.StreamClose:
			if err := d.Done(); err != nil {
				return err
			}
			p.close()
		}
	}
	return nil
}

// The caller owns s.mu, so process creation and handle publication are atomic
// with respect to workspace/extension revocation and the stream budget.
func (s *services) attachProcess(p io.ReadWriteCloser, logs *os.File, root string, extension uint64, e *protocol.Encoder) {
	s.next++
	id, logID := s.next, uint64(0)
	var logStream *serviceStream
	if logs != nil {
		s.next++
		logID = s.next
		logStream = newServiceStream(s.ctx, readOnlyStream{logs}, nil)
		logStream.root, logStream.extension = root, extension
		s.streams[logID] = logStream
	}
	stream := newServiceStream(s.ctx, p, logStream)
	stream.root, stream.extension = root, extension
	s.streams[id] = stream
	e.U64(id)
	e.U64(logID)
}
