package server_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/protocol"
	"github.com/pencode-labs/pencode/remote"
	"github.com/pencode-labs/pencode/server"
)

func startWebSocketServer(t *testing.T) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- server.ServeWebSocket(ctx, listener, "test-secret", server.Options{})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("WebSocket server did not stop")
		}
	})
	return "ws://" + listener.Addr().String() + "/ws", cancel, done
}

func TestWebSocketAuthentication(t *testing.T) {
	address, _, _ := startWebSocketServer(t)
	for _, token := range []string{"", "wrong-token"} {
		req, err := http.NewRequestWithContext(t.Context(), "GET", "http"+strings.TrimPrefix(address, "ws"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthenticated request: %s", response.Status)
		}
	}
	_, err := (connection.Dialer{}).Dial(t.Context(), connection.Profile{
		WebSocket: remote.WebSocketTarget{Address: address, Token: "wrong-token"},
	}, nil)
	if err == nil || strings.Contains(err.Error(), "wrong-token") {
		t.Fatalf("wrong token accepted or leaked: %v", err)
	}
}

func TestWebSocketWorkspaceAndReconnect(t *testing.T) {
	address, cancel, _ := startWebSocketServer(t)
	profile := connection.Profile{WebSocket: remote.WebSocketTarget{Address: address, Token: "test-secret"}}
	directory := t.TempDir()
	want := strings.Repeat("binary\x00中文🙂\n", protocol.MaxPayload/8)
	name := filepath.Join(directory, "text.txt")
	if err := os.WriteFile(name, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		backend, err := (connection.Dialer{}).Dial(t.Context(), profile, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { backend.Close() })
		workspace, err := backend.OpenWorkspace(t.Context(), directory)
		if err != nil {
			t.Fatal(err)
		}
		document, text, err := backend.OpenDocument(t.Context(), workspace.ID, "text.txt", true)
		if err != nil || text != want {
			t.Fatalf("chunked document: %d bytes, %v", len(text), err)
		}
		want += "saved\n"
		if err := document.Save(t.Context(), want); err != nil {
			t.Fatal(err)
		}
		document.Close()
		data, err := os.ReadFile(name)
		if err != nil || string(data) != want {
			t.Fatal("remote save failed", err)
		}
		backend.Close()
	}
	// An active session must be interrupted even though net/http has hijacked it.
	backend, err := (connection.Dialer{}).Dial(t.Context(), profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	cancel()
	select {
	case <-backend.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("server cancellation left the session open")
	}
}

func TestWebSocketDialCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
			_, _ = io.Copy(io.Discard, conn)
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := remote.ConnectWebSocket(ctx, remote.WebSocketTarget{Address: "ws://" + listener.Addr().String() + "/ws", Token: "secret"}, nil)
		done <- err
	}()
	select {
	case conn := <-accepted:
		defer conn.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("dial did not reach listener")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled handshake succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled handshake blocked")
	}
}
