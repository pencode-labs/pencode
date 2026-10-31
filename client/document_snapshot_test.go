package client

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestDocumentSaveSnapshotRetainsNewerEdits(t *testing.T) {
	var gate *gated
	c, w := peer(t, func(conn net.Conn) io.ReadWriteCloser {
		gate = &gated{Conn: conn, entered: make(chan struct{}), release: make(chan struct{})}
		return gate
	})
	unblock := sync.OnceFunc(func() { close(gate.release) })
	defer unblock()
	filename := filepath.Join(w.Path, "text")
	if err := os.WriteFile(filename, []byte("disk"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	doc, _, err := c.OpenDocument(ctx, w.ID, "text", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(doc.Release)
	gate.pause.Store(true)
	saved := make(chan error, 1)
	go func() { saved <- doc.SaveSnapshot(ctx, "snapshot") }()
	select {
	case <-gate.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	doc.Update("", "newer GUI text", -1, 0, 0)
	// A second operation waiting for the save barrier remains cancellable.
	waiting, stop := context.WithCancel(ctx)
	stop()
	if err := doc.Move(waiting, w.ID, "other"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	unblock()
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	if _, err := doc.flush(ctx); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filename)
	if string(data) != "snapshot" {
		t.Fatalf("saved %q", data)
	}
	doc.mu.Lock()
	latest := doc.latest.content
	doc.mu.Unlock()
	if latest.String() != "newer GUI text" {
		t.Fatal("save replaced latest mailbox", latest)
	}
	// Saving an older, already superseded snapshot must restore confirmed text too.
	if err := doc.SaveSnapshot(ctx, "older snapshot"); err != nil {
		t.Fatal(err)
	}
	if _, err := doc.flush(ctx); err != nil {
		t.Fatal(err)
	}
	doc.mu.Lock()
	latest = doc.latest.content
	doc.mu.Unlock()
	data, _ = os.ReadFile(filename)
	if string(data) != "older snapshot" || latest.String() != "newer GUI text" {
		t.Fatal(string(data), latest)
	}
	if err := doc.SaveSnapshot(ctx, latest); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filename)
	if string(data) != latest.String() {
		t.Fatal("explicit subsequent save lost edits")
	}
}

func TestDocumentReleaseDoesNotWaitForTransport(t *testing.T) {
	var gate *gated
	c, w := peer(t, func(conn net.Conn) io.ReadWriteCloser {
		gate = &gated{Conn: conn, entered: make(chan struct{}), release: make(chan struct{})}
		return gate
	})
	unblock := sync.OnceFunc(func() { close(gate.release) })
	defer unblock()
	if err := os.WriteFile(filepath.Join(w.Path, "text"), []byte("disk"), 0600); err != nil {
		t.Fatal(err)
	}
	doc, _, err := c.OpenDocument(t.Context(), w.ID, "text", true)
	if err != nil {
		t.Fatal(err)
	}
	gate.pause.Store(true)
	doc.Update("", "pending", -1, 0, 0)
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no gated edit")
	}
	start := time.Now()
	doc.Release()
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("release blocked GUI")
	}
	unblock()
	doc.Close()
}
