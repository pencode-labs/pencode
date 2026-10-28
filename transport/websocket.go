package transport

import (
	"context"
	"fmt"
	"io"

	"github.com/coder/websocket"
)

// webSocket streams binary messages through a reusable reader. Payloads are
// never collected or copied into a separate message-sized buffer here.
type webSocket struct {
	conn   *websocket.Conn
	reader io.Reader
}

func NewWebSocket(conn *websocket.Conn) io.ReadWriteCloser {
	// The byte-stream protocol bounds its own frames; a WebSocket message may
	// contain any number of them and is consumed incrementally.
	conn.SetReadLimit(-1)
	return &webSocket{conn: conn}
}

func (s *webSocket) Read(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	for {
		if s.reader == nil {
			kind, reader, err := s.conn.Reader(context.Background())
			if err != nil {
				if status := websocket.CloseStatus(err); status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway {
					return 0, io.EOF
				}
				return 0, err
			}
			if kind != websocket.MessageBinary {
				_ = s.Close()
				return 0, fmt.Errorf("pencode: expected binary WebSocket message")
			}
			s.reader = reader
		}
		n, err := s.reader.Read(data)
		if err == io.EOF {
			s.reader, err = nil, nil
		}
		if n != 0 || err != nil {
			return n, err
		}
	}
}

func (s *webSocket) Write(data []byte) (int, error) {
	if err := s.conn.Write(context.Background(), websocket.MessageBinary, data); err != nil {
		return 0, err
	}
	return len(data), nil
}

// CloseNow interrupts blocked I/O without waiting for a peer's close handshake.
func (s *webSocket) Close() error {
	return s.conn.CloseNow()
}
