package client

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diiyw/onix/projects/pencode/protocol"
	"github.com/diiyw/onix/projects/pencode/server"
)

func peer(t *testing.T, wrap func(net.Conn) io.ReadWriteCloser) (*Client, protocol.Workspace) {
	t.Helper()
	a, b := net.Pipe()
	var conn io.ReadWriteCloser = b
	if wrap != nil {
		conn = wrap(b)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(context.Background(), conn, server.Options{ExtensionDirectory: t.TempDir()})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := New(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); <-done })
	w, err := c.OpenWorkspace(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return c, w
}

func TestChunkedDocumentRoundTrip(t *testing.T) {
	c, w := peer(t, nil)
	name := filepath.Join(w.Path, "source.go")
	original := strings.Repeat("中🙂\r\n", 20000)
	if err := os.WriteFile(name, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	doc, content, err := c.OpenDocument(ctx, w.ID, "source.go", true)
	if err != nil || content != original {
		t.Fatal(len(content), err)
	}
	defer doc.Close()
	updated := "start\n" + strings.Repeat("文字🙂\n", 40000) + "end\r\n"
	doc.Update(content, updated, 0, len(content), len(updated))
	if err := doc.Save(ctx, updated); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(name)
	if err != nil || string(data) != updated {
		t.Fatal("large edit did not survive chunk boundaries", err)
	}
	if err := os.WriteFile(name, []byte("external"), 0644); err != nil {
		t.Fatal(err)
	}
	err = doc.Save(ctx, updated)
	var remote *protocol.Error
	if !errors.As(err, &remote) || remote.Code != protocol.Conflict {
		t.Fatalf("external conflict=%v", err)
	}
	if err := c.CloseWorkspace(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if err := doc.Save(ctx, updated); !errors.As(err, &remote) || remote.Code != protocol.NotFound {
		t.Fatalf("closed document save=%v", err)
	}
}

type gated struct {
	net.Conn
	pause            atomic.Bool
	entered, release chan struct{}
	once             sync.Once
}

func (g *gated) Write(p []byte) (int, error) {
	if g.pause.Load() {
		g.once.Do(func() { close(g.entered) })
		<-g.release
	}
	return g.Conn.Write(p)
}

func TestEditingDoesNotWaitForServerAndCoalesces(t *testing.T) {
	var gate *gated
	c, w := peer(t, func(conn net.Conn) io.ReadWriteCloser {
		gate = &gated{Conn: conn, entered: make(chan struct{}), release: make(chan struct{})}
		return gate
	})
	unblock := sync.OnceFunc(func() { close(gate.release) })
	defer unblock()
	if err := os.WriteFile(filepath.Join(w.Path, "text"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	doc, _, err := c.OpenDocument(ctx, w.ID, "text", true)
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	gate.pause.Store(true)
	doc.Update("base", "first", 0, 4, 5)
	select {
	case <-gate.entered:
	case <-ctx.Done():
		t.Fatal("did not block server response")
	}
	values := []string{"中文", "中文🙂", "final\r\n"}
	i := 0
	if n := testing.AllocsPerRun(100, func() { next := values[i%len(values)]; doc.Update("", next, -1, 0, 0); i++ }); n != 0 {
		t.Fatalf("UI edit submission allocated %g times", n)
	}
	doc.Update("", "final\r\n", -1, 0, 0)
	unblock()
	if err := doc.Save(ctx, "final\r\n"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(w.Path, "text"))
	if string(data) != "final\r\n" {
		t.Fatal("coalesced updates lost the final content")
	}
}

func TestRequestTimeoutDoesNotCorruptLaterResponse(t *testing.T) {
	var gate *gated
	c, w := peer(t, func(conn net.Conn) io.ReadWriteCloser {
		gate = &gated{Conn: conn, entered: make(chan struct{}), release: make(chan struct{})}
		return gate
	})
	unblock := sync.OnceFunc(func() { close(gate.release) })
	defer unblock()
	gate.pause.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := c.OpenWorkspace(ctx, w.Path)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	unblock()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	got, err := c.OpenWorkspace(ctx2, w.Path)
	if err != nil || got != w {
		t.Fatalf("late reply corrupted next request: %+v %v", got, err)
	}
}

func TestResourceOperationsAndEnumeration(t *testing.T) {
	c, w := peer(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Create(ctx, w.ID, "src", true); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(ctx, w.ID, "src/text", false); err != nil {
		t.Fatal(err)
	}
	if err := c.Rename(ctx, w.ID, "src/text", "src/renamed"); err != nil {
		t.Fatal(err)
	}
	if name, err := c.Transfer(ctx, w.ID, w.ID, "src", "", false, true); err != nil || name != "src 副本 1" {
		t.Fatal(name, err)
	}
	entries, err := (Directories{Client: c, Workspace: w.ID, Context: ctx}).ReadDir("src")
	if err != nil || len(entries) != 1 || entries[0].Name() != "renamed" {
		t.Fatal(entries, err)
	}
	if entries[0].Type()&fs.ModeDir != 0 {
		t.Fatal("file became a directory")
	}
	if err := c.Remove(ctx, w.ID, "src"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.Path, "src")); !os.IsNotExist(err) {
		t.Fatal("remove did not execute on server")
	}
	if err := c.Remove(ctx, w.ID, "../outside"); err == nil {
		t.Fatal("accepted escaping resource mutation")
	}
}

func TestChangedRangeUnicodeBoundaries(t *testing.T) {
	for _, pair := range [][2]string{{"中a", "文a"}, {"🙂tail", "🙃tail"}, {"tail中", "tail文"}, {"a中b", "a文b"}, {"", "🙂"}, {"🙂", ""}, {"same", "same"}} {
		start, end, next := changedRange(pair[0], pair[1])
		if pair[0][:start]+pair[1][start:next]+pair[0][end:] != pair[1] {
			t.Fatal(pair, start, end, next)
		}
	}
}
