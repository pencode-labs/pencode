package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pencode-labs/pencode/protocol"
	"github.com/pencode-labs/pencode/terminal"
	"github.com/pencode-labs/pencode/transport"
)

type debugSocket struct {
	net.Conn
	child io.ReadWriteCloser
	once  sync.Once
}

func (p *debugSocket) Close() error {
	p.once.Do(func() {
		_ = p.Conn.Close()
		if p.child != nil {
			_ = p.child.Close()
		}
	})
	return nil
}
func (p *debugSocket) Wait() error {
	if child, ok := p.child.(interface{ Wait() error }); ok {
		return child.Wait()
	}
	return nil
}

func (s *services) openDebugAdapter(ctx context.Context, root string, d *protocol.Decoder, e *protocol.Encoder) error {
	adapter, command, address := d.String(), d.String(), d.String()
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
	if root == "" {
		return protocol.ErrProtocol
	}
	var extension uint64
	var entry *extensionEntry
	var prefix []string
	if command == "" && adapter != "tcp" {
		s.extensions.mu.Lock()
		var err error
		command, prefix, extension, err = s.extensions.debugAdapter(adapter)
		entry = s.extensions.entries[extension]
		s.extensions.mu.Unlock()
		if err != nil {
			return err
		}
	}
	s.mu.Lock()
	full := len(s.streams)+2 > 32
	s.mu.Unlock()
	if full {
		return &protocol.Error{Code: protocol.Busy, Message: "debug stream budget exceeded"}
	}
	// A contributed launcher may need to install its tool on first use.
	startup, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if entry != nil {
		stop := context.AfterFunc(entry.ctx, cancel)
		defer stop()
	}
	logs, childLogs, err := os.Pipe()
	if err != nil {
		return err
	}
	var stream io.ReadWriteCloser
	switch adapter {
	case "stdio":
		if command == "" {
			err = protocol.ErrProtocol
			break
		}
		stream, err = transport.StartWithOutput(root, command, childLogs, append(append([]string(nil), prefix...), args...)...)
	case "tcp":
		var conn net.Conn
		conn, err = (&net.Dialer{}).DialContext(startup, "tcp", address)
		if err == nil {
			stream = &debugSocket{Conn: conn}
		}
	case "Delve":
		if command == "" {
			command = "dlv"
		}
		var child io.ReadWriteCloser
		adapterArgs := append(append(append([]string(nil), prefix...), "dap", "--listen=127.0.0.1:0"), args...)
		child, err = transport.StartWithOutput(root, command, childLogs, adapterArgs...)
		if err != nil {
			err = fmt.Errorf("cannot start Delve (install dlv on the workspace machine): %w", err)
			break
		}
		stop := context.AfterFunc(startup, func() { _ = child.Close() })
		reader := bufio.NewReaderSize(child, 8192)
		var conn net.Conn
		for lines := 0; lines < 100; lines++ {
			var line []byte
			line, err = reader.ReadSlice('\n')
			if err != nil {
				break
			}
			if value, ok := strings.CutPrefix(string(line), "DAP server listening at: "); ok {
				conn, err = (&net.Dialer{}).DialContext(startup, "tcp", strings.TrimSpace(value))
				break
			}
		}
		if !stop() && startup.Err() != nil {
			err = startup.Err()
		}
		if err != nil || conn == nil {
			_ = child.Close()
			if conn != nil {
				_ = conn.Close()
			}
			if err == nil {
				err = fmt.Errorf("Delve did not announce its DAP address")
			}
			break
		}
		stream = &debugSocket{Conn: conn, child: child}
		// Adapter stdout is diagnostic after its readiness announcement. DAP
		// itself travels only over the socket.
		go func() { _, _ = io.Copy(childLogs, reader) }()
	default:
		err = fmt.Errorf("unknown debug adapter %q", adapter)
	}
	if adapter != "Delve" || err != nil {
		_ = childLogs.Close()
	} else {
		// The stdout drainer and child's stderr share this descriptor until exit.
		go func() {
			if p, ok := stream.(interface{ Wait() error }); ok {
				_ = p.Wait()
			}
			_ = childLogs.Close()
		}()
	}
	if err != nil {
		_ = logs.Close()
		return err
	}
	if extension != 0 {
		s.extensions.mu.Lock()
		defer s.extensions.mu.Unlock()
		if s.extensions.entries[extension] != entry || startup.Err() != nil {
			_ = stream.Close()
			_ = logs.Close()
			return fmt.Errorf("debug adapter extension was closed during startup")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil || s.ctx.Err() != nil || !s.roots[root] || len(s.streams)+2 > 32 {
		_ = stream.Close()
		_ = logs.Close()
		return &protocol.Error{Code: protocol.Busy, Message: "debug workspace closed or stream budget exceeded"}
	}
	s.attachProcess(stream, logs, root, extension, e)
	return nil
}

func (s *services) openDebugTerminal(ctx context.Context, root string, d *protocol.Decoder, e *protocol.Encoder) error {
	directory := d.String()
	n := d.U32()
	if root == "" || n == 0 || n > 4096 {
		return protocol.ErrProtocol
	}
	args := make([]string, n)
	for i := range args {
		args[i] = d.String()
	}
	n = d.U32()
	if n > 4096 {
		return protocol.ErrProtocol
	}
	env := make(map[string]*string, n)
	for range n {
		key := d.String()
		present := d.U32()
		if present > 1 || key == "" || strings.ContainsAny(key, "=\x00") {
			return protocol.ErrProtocol
		}
		if present == 1 {
			value := d.String()
			env[key] = &value
		} else {
			env[key] = nil
		}
	}
	if err := d.Done(); err != nil {
		return err
	}
	if directory == "" {
		directory = root
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.roots[root] || ctx.Err() != nil || len(s.streams) >= 32 {
		return &protocol.Error{Code: protocol.Busy, Message: "debug terminal budget exceeded or workspace closed"}
	}
	p, pid, err := terminal.OpenCommand(directory, args, env, 80, 12)
	if err != nil {
		return err
	}
	e.U32(uint32(pid))
	s.attachProcess(p, nil, root, 0, e)
	return nil
}
