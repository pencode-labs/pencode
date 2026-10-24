package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/diiyw/onix/syntax"
	"github.com/pencode-labs/pencode/protocol"
	"github.com/pencode-labs/pencode/server"
)

func TestServiceChild(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--pencode-service-child" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "bulk":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("中🙂\r\n"), 262144))
		os.Exit(0)
	case "flood":
		_, _ = fmt.Fprintln(os.Stdout, os.Getpid())
		_, _ = fmt.Fprint(os.Stderr, "language server stderr\n")
		block := bytes.Repeat([]byte("中🙂"), 8192)
		for range 16 {
			if _, err := os.Stdout.Write(block); err != nil {
				return
			}
		}
		_, _ = io.Copy(os.Stdout, os.Stdin)
	case "echo":
		_, _ = io.Copy(os.Stdout, os.Stdin)
	}
}
func openServiceChild(t *testing.T, c *Client, root, mode string) *Process {
	t.Helper()
	p, err := c.OpenProcess(t.Context(), root, os.Args[0], "-test.run=^TestServiceChild$", "--", "--pencode-service-child", mode)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}
func TestProcessBackpressureDoesNotBlockSaveAndDisconnectReaps(t *testing.T) {
	c, w := peer(t, nil)
	path := filepath.Join(w.Path, "main.go")
	if err := os.WriteFile(path, []byte("package before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := openServiceChild(t, c, w.Path, "flood")
	// Read only the pid; the remaining output deliberately fills every queue.
	var first [32]byte
	count := 0
	for count < len(first) {
		n, err := p.Read(first[count : count+1])
		if err != nil {
			t.Fatal(err)
		}
		count += n
		if first[count-1] == '\n' {
			break
		}
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(first[:count])))
	if err != nil {
		t.Fatal(err)
	}
	var log [64]byte
	n, err := p.Stderr().Read(log[:])
	if err != nil || !strings.Contains(string(log[:n]), "language server stderr") {
		t.Fatal("separate stderr stream", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	doc, _, err := c.OpenDocument(ctx, w.ID, "main.go", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Save(ctx, "package after\n"); err != nil {
		t.Fatal("terminal output blocked document save", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		process, _ := os.FindProcess(pid)
		deadline := time.Now().Add(3 * time.Second)
		for process.Signal(syscall.Signal(0)) == nil {
			if time.Now().After(deadline) {
				t.Fatal("server child survived disconnect")
			}
			time.Sleep(time.Millisecond)
		}
	}
}
func TestCancelledStreamReceivesReleaseServiceSlots(t *testing.T) {
	c, w := peer(t, nil)
	p := openServiceChild(t, c, w.Path, "echo")
	for range 64 {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
		_, err := p.request(ctx, protocol.StreamRead, nil)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("cancelled receive leaked a service slot: %v", err)
		}
	}
	if _, err := p.Write([]byte("中文🙂")); err != nil {
		t.Fatal(err)
	}
	var output [32]byte
	n, err := p.Read(output[:])
	if err != nil || string(output[:n]) != "中文🙂" {
		t.Fatal("stream was lost after cancellation", err)
	}
}

func TestProcessConcurrentWritesPreserveWholeMessages(t *testing.T) {
	c, w := peer(t, nil)
	p := openServiceChild(t, c, w.Path, "echo")
	values := [2][]byte{bytes.Repeat([]byte("a"), 2*protocol.StreamWindow+3), bytes.Repeat([]byte("b"), 2*protocol.StreamWindow+3)}
	start := make(chan struct{})
	written := make(chan error, 2)
	for _, value := range values {
		go func() {
			<-start
			_, err := p.Write(value)
			written <- err
		}()
	}
	output := make([]byte, len(values[0])+len(values[1]))
	read := make(chan error, 1)
	go func() { _, err := io.ReadFull(p, output); read <- err }()
	close(start)
	for range 3 {
		select {
		case err := <-written:
			if err != nil {
				t.Fatal(err)
			}
		case err := <-read:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent bulk stream writes deadlocked")
		}
	}
	a, b := values[0], values[1]
	if !(bytes.Equal(output[:len(a)], a) && bytes.Equal(output[len(a):], b)) && !(bytes.Equal(output[:len(b)], b) && bytes.Equal(output[len(b):], a)) {
		t.Fatal("concurrent writes interleaved logical message fragments")
	}
}
func TestServerIndexPagesSearchAndWorkspaceLifetime(t *testing.T) {
	c, w := peer(t, nil)
	for _, name := range []string{"src", "node_modules", ".git", "dist"} {
		if err := os.Mkdir(filepath.Join(w.Path, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 301 {
		if err := os.WriteFile(filepath.Join(w.Path, "src", fmt.Sprintf("file-%03d.go", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"中文.go", "image.png", "node_modules/hidden.go"} {
		if err := os.WriteFile(filepath.Join(w.Path, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	id, err := c.OpenIndex(t.Context(), w.Path)
	if err != nil {
		t.Fatal(err)
	}
	var all []protocol.IndexEntry
	var page []protocol.IndexEntry
	for {
		var done bool
		var message string
		page, done, message, err = c.IndexPage(t.Context(), id, len(all), page[:0])
		if err != nil || message != "" || len(page) > 256 {
			t.Fatal("index page", message, err)
		}
		all = append(all, page...)
		if done {
			break
		}
	}
	if len(all) != 304 || all[0].Path != "src" {
		t.Fatalf("wrong index order or excludes: %d %+v", len(all), all[:1])
	}
	paths, err := c.SearchPaths(t.Context(), id, "", []string{"中文.go", "missing.go", "image.png"})
	if err != nil || paths[0] != "中文.go" {
		t.Fatal("history order", paths, err)
	}
	for _, name := range paths {
		if strings.HasSuffix(name, ".png") || strings.Contains(name, "hidden") {
			t.Fatal("excluded file", name)
		}
	}
	paths, err = c.SearchPaths(t.Context(), id, "IMAGE", nil, protocol.PathRule{Pattern: ".png", Suffix: true})
	if err != nil || !reflect.DeepEqual(paths, []string{"image.png"}) {
		t.Fatal("custom editor search", paths, err)
	}
	for _, query := range []string{
		"file-042.go",
		"src/file-042.go",
		`src\file-042.go`,
		`SRC\FILE-042.GO`,
		filepath.Join(w.Path, "src", "file-042.go"),
		filepath.ToSlash(filepath.Join(w.Path, "src", "file-042.go")),
		strings.ToUpper(filepath.Join(w.Path, "src", "file-042.go")),
	} {
		paths, err = c.SearchPaths(t.Context(), id, query, nil)
		if err != nil || !reflect.DeepEqual(paths, []string{"src/file-042.go"}) {
			t.Fatalf("path search %q = %v, %v", query, paths, err)
		}
	}
	paths, err = c.SearchPaths(t.Context(), id, w.Path+"-other/src/file-042.go", nil)
	if err != nil || len(paths) != 0 {
		t.Fatal("path outside workspace matched", paths, err)
	}
	if err := c.CloseWorkspace(t.Context(), w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SearchPaths(t.Context(), id, "", nil); err == nil {
		t.Fatal("closed workspace retained index")
	}
	if _, err := c.OpenProcess(t.Context(), w.Path, os.Args[0]); err == nil {
		t.Fatal("closed workspace allowed process creation")
	}
}
func TestRemoteSyntaxLargeDeltaAndLocalRecolor(t *testing.T) {
	c, _ := peer(t, nil)
	remote := c.NewSyntax(t.Context())
	defer remote.Close()
	source := "package demo\n" + strings.Repeat("// 中文🙂 comment\n", 6000) + "var Value=1\n"
	for _, source := range []string{source, strings.Replace(source, "Value=1", "名字=42", 1)} {
		if err := remote.Update("main.go", source); err != nil {
			t.Fatal(err)
		}
		var local syntax.Document
		if err := local.Update("main.go", source); err != nil {
			t.Fatal(err)
		}
		c.mu.Lock()
		before := c.next
		c.mu.Unlock()
		for _, name := range []string{"monokai", "light-plus"} {
			theme, _ := syntax.BuiltinTheme(name)
			if !reflect.DeepEqual(remote.Tokens(theme, nil), local.Tokens(theme, nil)) {
				t.Fatal("remote captures differ from local syntax", name)
			}
		}
		if err := remote.Update("main.go", source); err != nil {
			t.Fatal(err)
		}
		c.mu.Lock()
		after := c.next
		c.mu.Unlock()
		if before != after {
			t.Fatal("theme recoloring issued RPC")
		}
		local.Close()
	}
	theme, _ := syntax.BuiltinTheme("monokai")
	remote.Tokens(theme, nil)
	if allocs := testing.AllocsPerRun(100, func() { remote.Tokens(theme, nil) }); allocs != 0 {
		t.Fatalf("local recoloring allocated %g times", allocs)
	}
	remote.Close()
	if err := remote.Update("main.go", "package reparsed\n"); err != nil {
		t.Fatal("idle parser could not restart", err)
	}
}
func TestServiceFileConflictAndSymlinkConfinement(t *testing.T) {
	c, w := peer(t, nil)
	if err := os.WriteFile(filepath.Join(w.Path, "main.go"), []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveFile(t.Context(), w.Path, "main.go", "wrong", "after"); err == nil {
		t.Fatal("external edit overwritten")
	}
	if err := c.SaveFile(t.Context(), w.Path, "main.go", "before", "after"); err != nil {
		t.Fatal(err)
	}
	data, err := c.ReadFile(t.Context(), w.Path, "main.go")
	if err != nil || string(data) != "after" {
		t.Fatal(string(data), err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Symlink("main.go", filepath.Join(w.Path, "internal.go")); err != nil {
		t.Fatal(err)
	}
	data, err = c.ReadFile(t.Context(), w.Path, "internal.go")
	if err != nil || string(data) != "after" {
		t.Fatal("internal link", string(data), err)
	}
	data, err = c.WorkFile(t.Context(), w.Path, "internal.go")
	if err != nil || string(data) != "main.go" {
		t.Fatal("Git link content", string(data), err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(w.Path, "outside")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadFile(t.Context(), w.Path, "outside/private"); err == nil {
		t.Fatal("escaping symlink was followed")
	}
}
func TestRemotePTYOutputAndExitStatus(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("POSIX shell")
	}
	c, w := peer(t, nil)
	p, err := c.OpenTerminal(t.Context(), w.Path, 80, 12)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	waited := make(chan error, 1)
	go func() { waited <- p.Wait() }()
	if err := p.Resize(101, 23); err != nil {
		t.Fatal(err)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		if err := p.Resize(101, 23); err != nil {
			t.Fatal(err)
		}
	}); allocations != 0 {
		t.Fatalf("stable terminal size allocated %g times", allocations)
	}
	if _, err := p.Write([]byte("printf 'REMOTE_中文🙂'; exit 7\n")); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(p)
	if err != nil || !bytes.Contains(data, []byte("REMOTE_中文🙂")) {
		t.Fatal("PTY output", string(data), err)
	}
	select {
	case err := <-waited:
		if err == nil {
			t.Fatal("exit code was lost")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PTY was not reaped")
	}
}

type uploadGate struct {
	io.ReadWriteCloser
	first, second               chan struct{}
	releaseFirst, releaseSecond chan struct{}
	chunks                      int
}

func (g *uploadGate) Write(p []byte) (int, error) {
	if len(p) == protocol.HeaderSize && string(p[:4]) == "pcs\x00" && protocol.Kind(binary.LittleEndian.Uint16(p[6:])) == protocol.Data {
		g.chunks++
		if g.chunks == 1 {
			close(g.first)
			<-g.releaseFirst
		}
		if g.chunks == 2 {
			close(g.second)
			<-g.releaseSecond
		}
	}
	return g.ReadWriteCloser.Write(p)
}
func TestChunkedUploadYieldsToInteractiveRequest(t *testing.T) {
	a, b := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(t.Context(), b, server.Options{ExtensionDirectory: t.TempDir()})
	}()
	gated := &uploadGate{ReadWriteCloser: a, first: make(chan struct{}), second: make(chan struct{}), releaseFirst: make(chan struct{}), releaseSecond: make(chan struct{})}
	releaseFirst := sync.OnceFunc(func() { close(gated.releaseFirst) })
	defer releaseFirst()
	releaseSecond := sync.OnceFunc(func() { close(gated.releaseSecond) })
	defer releaseSecond()
	c, err := New(t.Context(), gated)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); <-done })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	large := make(chan error, 1)
	go func() { _, err := c.request(ctx, protocol.Service, make([]byte, 4*protocol.MaxPayload)); large <- err }()
	select {
	case <-gated.first:
	case <-time.After(3 * time.Second):
		t.Fatal("upload did not start")
	}
	small := make(chan error, 1)
	root := t.TempDir()
	go func() { _, err := c.OpenWorkspace(t.Context(), root); small <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		c.mu.Lock()
		count := len(c.pending)
		c.mu.Unlock()
		if count == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("interactive request did not queue")
		}
		time.Sleep(time.Millisecond)
	}
	releaseFirst()
	select {
	case <-gated.second:
	case <-time.After(3 * time.Second):
		t.Fatal("second upload chunk did not start")
	}
	select {
	case err := <-small:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("large upload starved interactive request")
	}
	cancel()
	releaseSecond()
	select {
	case <-large:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled upload did not finish")
	}
}
