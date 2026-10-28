package transport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func webSocketEcho(tb testing.TB) io.ReadWriteCloser {
	tb.Helper()
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		stream := NewWebSocket(conn)
		defer stream.Close()
		var buffer [32 << 10]byte
		for {
			n, err := stream.Read(buffer[:])
			if err != nil {
				return
			}
			if _, err := stream.Write(buffer[:n]); err != nil {
				return
			}
		}
	}))
	tb.Cleanup(server.Close)
	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		tb.Fatal(err)
	}
	stream := NewWebSocket(conn)
	tb.Cleanup(func() {
		_ = stream.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			tb.Error("closing WebSocket did not unblock the peer")
		}
	})
	return stream
}

func TestWebSocketStream(t *testing.T) {
	stream := webSocketEcho(t)
	want := strings.Repeat("binary\x00中文\xff\n", 7000)
	written := make(chan error, 1)
	go func() {
		// Empty WebSocket messages must not appear as EOF to the byte stream.
		_, err := stream.Write(nil)
		if err == nil {
			_, err = io.WriteString(stream, want)
		}
		written <- err
	}()
	data := make([]byte, len(want))
	if _, err := io.ReadFull(stream, data); err != nil || string(data) != want {
		t.Fatal("binary stream changed data across message boundaries", err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	reading := make(chan error, 1)
	go func() {
		_, err := stream.Read(data)
		reading <- err
	}()
	stream.Close()
	select {
	case err := <-reading:
		if err == nil {
			t.Fatal("close did not interrupt Read")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked behind Read")
	}
}

func BenchmarkWebSocketStream(b *testing.B) {
	stream := webSocketEcho(b)
	data, response := make([]byte, 32<<10), make([]byte, 32<<10)
	roundTrip := func() {
		if _, err := stream.Write(data); err != nil {
			b.Fatal(err)
		}
		if _, err := io.ReadFull(stream, response); err != nil {
			b.Fatal(err)
		}
	}
	roundTrip()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		roundTrip()
	}
}
