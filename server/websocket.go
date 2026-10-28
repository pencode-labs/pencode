package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/pencode-labs/pencode/remote"
	"github.com/pencode-labs/pencode/transport"
)

// ServeWebSocket owns listener and serves independent authenticated sessions at
// /ws. Cancellation closes hijacked connections and waits for their services.
func ServeWebSocket(parent context.Context, listener net.Listener, token string, options Options) error {
	defer listener.Close()
	if err := remote.ValidateToken(token); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var mu sync.Mutex
	var sessions sync.WaitGroup
	stopping := false
	expected := sha256.Sum256([]byte("Bearer " + token))
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		authorization := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(authorization[:], expected[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		if stopping {
			mu.Unlock()
			http.Error(w, "Server shutting down", http.StatusServiceUnavailable)
			return
		}
		sessions.Add(1)
		mu.Unlock()
		defer sessions.Done()
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = Serve(ctx, transport.NewWebSocket(conn), options)
	})
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	stop := context.AfterFunc(ctx, func() { _ = httpServer.Close() })
	defer stop()
	err := httpServer.Serve(listener)
	mu.Lock()
	stopping = true
	mu.Unlock()
	cancel()
	_ = httpServer.Close()
	sessions.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
