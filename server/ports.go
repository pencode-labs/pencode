package server

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/pencode-labs/pencode/protocol"
)

func (s *services) port(ctx context.Context, op protocol.Operation, d *protocol.Decoder, e *protocol.Encoder) error {
	if op == protocol.PortList {
		if err := d.Done(); err != nil {
			return err
		}
		ports, err := listeningPorts(ctx)
		if err != nil {
			return err
		}
		e.U32(uint32(len(ports)))
		for _, p := range ports {
			e.U32(uint32(p.Number))
			e.String(p.Host)
			e.U32(p.PID)
			e.String(p.Process)
		}
		return nil
	}
	port, host := d.U32(), d.String()
	if err := d.Done(); err != nil {
		return err
	}
	if port == 0 || port > 65535 || !protocol.ValidPortHost(host) {
		return protocol.ErrProtocol
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil && host == "127.0.0.1" {
		conn, err = (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort("::1", strconv.Itoa(int(port))))
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		conn.Close()
		return err
	}
	if len(s.streams) >= 32 || s.ctx.Err() != nil {
		conn.Close()
		return &protocol.Error{Code: protocol.Busy, Message: "port stream budget exceeded"}
	}
	s.attachProcess(conn, nil, "", 0, e)
	return nil
}
