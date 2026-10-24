package client

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/pencode-labs/pencode/protocol"
)

func (c *Client) ListeningPorts(ctx context.Context) ([]protocol.Port, error) {
	e := service(protocol.PortList, "")
	d, err := c.execute(ctx, &e)
	if err != nil {
		return nil, err
	}
	count := d.U32()
	if count > 65535 {
		return nil, protocol.ErrProtocol
	}
	ports := make([]protocol.Port, count)
	for i := range ports {
		number := d.U32()
		ports[i] = protocol.Port{Number: uint16(number), Host: d.String(), PID: d.U32(), Process: d.String()}
		if number == 0 || number > 65535 || !protocol.ValidPortHost(ports[i].Host) {
			return nil, protocol.ErrProtocol
		}
	}
	return ports, d.Done()
}

func (c *Client) connectPort(ctx context.Context, port protocol.Port) (*Process, error) {
	e := service(protocol.PortConnect, "")
	e.U32(uint32(port.Number))
	e.String(port.Host)
	d, err := c.execute(ctx, &e)
	if err != nil {
		return nil, err
	}
	return c.decodeProcess(&d, 0, 0)
}

// Forward owns a local TCP listener and every connection accepted on it.
// Both the caller context and the backend connection bound its lifetime.
type Forward struct {
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
	once     sync.Once
	workers  sync.WaitGroup
	done     chan struct{}
	mu       sync.Mutex
	err      error
}

type ForwardOptions struct {
	Port          uint16
	AllInterfaces bool
}

func (c *Client) ForwardPort(parent context.Context, remote protocol.Port, options ForwardOptions) (*Forward, error) {
	if remote.Number == 0 || !protocol.ValidPortHost(remote.Host) {
		return nil, protocol.ErrProtocol
	}
	if err := c.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	host := "127.0.0.1"
	if options.AllInterfaces {
		host = "0.0.0.0"
	}
	l, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", net.JoinHostPort(host, strconv.Itoa(int(options.Port))))
	if err != nil {
		cancel()
		return nil, err
	}
	f := &Forward{listener: l, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	slots := make(chan struct{}, 16)
	f.workers.Add(1)
	go func() {
		defer f.workers.Done()
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				conn.Close()
				return
			}
			f.workers.Add(1)
			go func() { defer f.workers.Done(); defer func() { <-slots }(); f.proxy(c, remote, conn.(*net.TCPConn)) }()
		}
	}()
	go func() {
		select {
		case <-ctx.Done():
		case <-c.Done():
		case <-f.done:
			return
		}
		_ = f.Close()
	}()
	return f, nil
}

var forwardBuffers = sync.Pool{New: func() any { return new([protocol.ChunkSize]byte) }}

func copyForward(dst io.Writer, src io.Reader) error {
	buffer := forwardBuffers.Get().(*[protocol.ChunkSize]byte)
	defer forwardBuffers.Put(buffer)
	_, err := io.CopyBuffer(dst, src, buffer[:])
	return err
}

func (f *Forward) proxy(c *Client, remote protocol.Port, local *net.TCPConn) {
	defer local.Close()
	stopLocal := context.AfterFunc(f.ctx, func() { local.Close() })
	defer stopLocal()
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	p, err := c.connectPort(ctx, remote)
	cancel()
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
	if err != nil {
		return
	}
	defer p.Close()
	stopRemote := context.AfterFunc(f.ctx, func() { p.Close() })
	defer stopRemote()
	upload := make(chan struct{})
	go func() {
		defer close(upload)
		if copyForward(p, local) != nil {
			p.Close()
			local.Close()
			return
		}
		ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
		defer cancel()
		_, err := p.request(ctx, protocol.StreamCloseWrite, nil)
		if err != nil {
			p.Close()
			local.Close()
		}
	}()
	err = copyForward(local, p)
	if err != nil {
		p.Close()
		local.Close()
	} else {
		local.CloseWrite()
	}
	<-upload
}

func (f *Forward) Address() string { return f.listener.Addr().String() }
func (f *Forward) Err() error      { f.mu.Lock(); defer f.mu.Unlock(); return f.err }
func (f *Forward) Close() error {
	f.once.Do(func() {
		f.cancel()
		f.listener.Close()
		f.workers.Wait()
		close(f.done)
	})
	return nil
}
