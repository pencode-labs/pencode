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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/protocol"
	"github.com/pencode-labs/pencode/server"
)

// shapedConnection delays propagation independently from serialization. Packets
// can be in flight together, unlike a sleep on every Read/Write (which charges a
// new RTT for each frame). Bounded queues also exercise transport backpressure.
func shapedConnection(t testing.TB, latency time.Duration, bytesPerSecond, fragment int) (net.Conn, net.Conn) {
	t.Helper()
	a, left := net.Pipe()
	right, b := net.Pipe()
	done := make(chan struct{})
	closeAll := sync.OnceFunc(func() {
		close(done)
		a.Close()
		left.Close()
		right.Close()
		b.Close()
	})
	var workers sync.WaitGroup
	t.Cleanup(func() { closeAll(); workers.Wait() })
	type packet struct {
		data []byte
		due  time.Time
	}
	forward := func(from, to net.Conn) {
		queue := make(chan packet, 64)
		workers.Add(2)
		go func() {
			defer workers.Done()
			defer close(queue)
			var buffer [protocol.MaxPayload]byte
			var sent time.Time
			for {
				n, err := from.Read(buffer[:])
				if err != nil {
					closeAll()
					return
				}
				now := time.Now()
				if sent.Before(now) {
					sent = now
				}
				if bytesPerSecond > 0 {
					sent = sent.Add(time.Duration(n) * time.Second / time.Duration(bytesPerSecond))
				}
				p := packet{data: bytes.Clone(buffer[:n]), due: sent.Add(latency)}
				select {
				case queue <- p:
				case <-done:
					return
				}
			}
		}()
		go func() {
			defer workers.Done()
			timer := time.NewTimer(time.Hour)
			timer.Stop()
			defer timer.Stop()
			for {
				var p packet
				select {
				case <-done:
					return
				case next, ok := <-queue:
					if !ok {
						return
					}
					p = next
				}
				if delay := time.Until(p.due); delay > 0 {
					timer.Reset(delay)
					select {
					case <-timer.C:
					case <-done:
						return
					}
				}
				for len(p.data) > 0 {
					n := len(p.data)
					if fragment > 0 {
						n = min(n, fragment)
					}
					written, err := to.Write(p.data[:n])
					if err != nil {
						closeAll()
						return
					}
					p.data = p.data[written:]
				}
			}
		}()
	}
	forward(left, right)
	forward(right, left)
	return a, b
}

func weakPeer(t testing.TB, latency time.Duration, bandwidth, fragment int) (*Client, protocol.Workspace) {
	t.Helper()
	a, b := shapedConnection(t, latency, bandwidth, fragment)
	done := make(chan struct{})
	options := server.Options{ExtensionDirectory: t.TempDir()}
	go func() { defer close(done); _ = server.Serve(context.Background(), b, options) }()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
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

func requestCount(c *Client) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.next
}

type downloadGate struct {
	net.Conn
	entered, release chan struct{}
	finals           chan uint64
	once             sync.Once
	dataFrames       atomic.Int32
}

func (g *downloadGate) Write(p []byte) (int, error) {
	if len(p) == protocol.HeaderSize && string(p[:4]) == "pcs\x00" {
		kind := protocol.Kind(binary.LittleEndian.Uint16(p[6:]))
		if kind == protocol.Data {
			g.dataFrames.Add(1)
			g.once.Do(func() { close(g.entered); <-g.release })
		} else {
			g.finals <- binary.LittleEndian.Uint64(p[8:])
		}
	}
	return g.Conn.Write(p)
}

func TestCancelledDownloadStopsBetweenFrames(t *testing.T) {
	var gate *downloadGate
	c, w := peer(t, func(conn net.Conn) io.ReadWriteCloser {
		gate = &downloadGate{Conn: conn, entered: make(chan struct{}), release: make(chan struct{}), finals: make(chan uint64, 64)}
		return gate
	})
	unblock := sync.OnceFunc(func() { close(gate.release) })
	defer unblock()
	if err := os.WriteFile(filepath.Join(w.Path, "large"), bytes.Repeat([]byte("a"), 2<<20), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	downloadCtx, stopDownload := context.WithCancel(ctx)
	defer stopDownload()
	finished := make(chan error, 1)
	go func() {
		_, _, err := c.OpenDocument(downloadCtx, w.ID, "large", true)
		finished <- err
	}()
	select {
	case <-gate.entered:
	case <-ctx.Done():
		t.Fatal("download did not begin")
	}
	stopDownload()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled request waited for download", err)
	}
	next := make(chan error, 1)
	go func() { next <- c.Create(ctx, w.ID, "after-cancel", false) }()
	// The writer sends Cancel before the next request. Its disk effect proves
	// the server has consumed that cancellation while the first frame stalls.
	for {
		if _, err := os.Stat(filepath.Join(w.Path, "after-cancel")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("server did not receive cancellation")
		case <-time.After(time.Millisecond):
		}
	}
	unblock()
	if err := <-next; err != nil {
		t.Fatal("cancelled response disrupted following request", err)
	}
	if frames := gate.dataFrames.Load(); frames != 1 {
		t.Fatalf("cancelled download kept transmitting: %d data frames", frames)
	}
}

func TestSlowDocumentDownloadDoesNotBlockMutation(t *testing.T) {
	var gate *downloadGate
	c, w := peer(t, func(conn net.Conn) io.ReadWriteCloser {
		gate = &downloadGate{Conn: conn, entered: make(chan struct{}), release: make(chan struct{}), finals: make(chan uint64, 64)}
		return gate
	})
	unblock := sync.OnceFunc(func() { close(gate.release) })
	defer unblock()
	content := strings.Repeat("中🙂", 300000)
	if err := os.WriteFile(filepath.Join(w.Path, "large"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	largeID := requestCount(c) + 1
	large := make(chan error, 1)
	go func() {
		doc, got, err := c.OpenDocument(ctx, w.ID, "large", true)
		if doc != nil {
			defer doc.Close()
		}
		if err == nil && got != content {
			err = errors.New("large download corrupted")
		}
		large <- err
	}()
	select {
	case <-gate.entered:
	case <-ctx.Done():
		t.Fatal("download did not begin")
	}
	smallID := largeID + 1
	small := make(chan error, 1)
	go func() { small <- c.Create(ctx, w.ID, "interactive", false) }()
	// The response writer is deliberately stopped. Receiving and applying a
	// following mutation must still work before the download can continue.
	for {
		if _, err := os.Stat(filepath.Join(w.Path, "interactive")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("download held the server request reader")
		case <-time.After(time.Millisecond):
		}
	}
	unblock()
	if err := <-small; err != nil {
		t.Fatal(err)
	}
	if err := <-large; err != nil {
		t.Fatal(err)
	}
	for {
		id := <-gate.finals
		if id == largeID {
			t.Fatal("large response monopolized the wire until completion")
		}
		if id == smallID {
			break
		}
	}
}

func TestInterruptedLargeDeltaHasNoPartialMutation(t *testing.T) {
	a, b := net.Pipe()
	done := make(chan struct{})
	options := server.Options{ExtensionDirectory: t.TempDir()}
	go func() { defer close(done); _ = server.Serve(t.Context(), b, options) }()
	gate := &uploadGate{ReadWriteCloser: a, first: make(chan struct{}), second: make(chan struct{}), releaseFirst: make(chan struct{}), releaseSecond: make(chan struct{})}
	first := sync.OnceFunc(func() { close(gate.releaseFirst) })
	second := sync.OnceFunc(func() { close(gate.releaseSecond) })
	defer first()
	defer second()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, err := New(ctx, gate)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); <-done })
	w, err := c.OpenWorkspace(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Path, "text"), []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	doc, _, err := c.OpenDocument(ctx, w.ID, "text", true)
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	doc.Update("before", strings.Repeat("中🙂", 100000), 0, 6, 700000)
	select {
	case <-gate.first:
	case <-ctx.Done():
		t.Fatal("delta did not begin")
	}
	first()
	select {
	case <-gate.second:
	case <-ctx.Done():
		t.Fatal("delta did not fragment")
	}
	// The server has received a full Data frame, but no mutation frame yet.
	doc.cancel()
	select {
	case <-doc.done:
	case <-ctx.Done():
		t.Fatal("cancellation waited for the blocked transport")
	}
	second()
	if _, err := c.OpenWorkspace(ctx, w.Path); err != nil {
		t.Fatal("cancelled upload corrupted the following request", err)
	}
	// Save using the original version: any partial application would either
	// reject the version or persist an incomplete paste.
	e := protocol.Encoder{}
	e.U64(doc.id)
	e.U64(1)
	if _, err := c.request(ctx, protocol.DocumentSave, e.Buffer); err != nil {
		t.Fatal("incomplete upload changed the document version", err)
	}
	got, err := os.ReadFile(filepath.Join(w.Path, "text"))
	if err != nil || string(got) != "before" {
		t.Fatal("incomplete upload changed disk content", err)
	}
	c.Close()
	c.mu.Lock()
	uploadBytes, responseBytes := c.uploadBytes, c.responseBytes
	c.mu.Unlock()
	if uploadBytes != 0 || responseBytes != 0 {
		t.Fatalf("disconnect retained wire budgets: upload=%d response=%d", uploadBytes, responseBytes)
	}
}

func TestWeakNetworkDocumentAndStream(t *testing.T) {
	c, w := weakPeer(t, 25*time.Millisecond, 4<<20, 997)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	name := filepath.Join(w.Path, "text")
	if err := os.WriteFile(name, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	doc, _, err := c.OpenDocument(ctx, w.ID, "text", true)
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	content := strings.Repeat("中文🙂\r\n", 90000)
	before, start := requestCount(c), time.Now()
	if err := doc.Save(ctx, content); err != nil {
		t.Fatal(err)
	}
	if requests := requestCount(c) - before; requests != 2 {
		t.Fatalf("large edit/save used %d RPCs, want one mutation and one save", requests)
	}
	doc.mu.Lock()
	version := doc.version
	doc.mu.Unlock()
	if version != 2 {
		t.Fatalf("one edit applied as multiple mutations: version=%d", version)
	}
	data, err := os.ReadFile(name)
	if err != nil || string(data) != content {
		t.Fatal("fragmented UTF-8/CRLF delta corrupted", err)
	}
	t.Logf("50 ms RTT, 4 MiB/s: %d-byte edit/save in %s (2 RPCs)", len(content), time.Since(start))

	p := openServiceChild(t, c, w.Path, "bulk")
	before, start = requestCount(c), time.Now()
	data, err = io.ReadAll(p)
	want := bytes.Repeat([]byte("中🙂\r\n"), 262144)
	if err != nil || !bytes.Equal(data, want) {
		t.Fatalf("bulk stream corrupted: %d/%d bytes: %v", len(data), len(want), err)
	}
	requests := requestCount(c) - before
	if requests > 24 {
		t.Fatalf("bulk output still depends on small stop-and-wait reads: %d RPCs", requests)
	}
	t.Logf("50 ms RTT, 4 MiB/s: %d-byte stream in %s (%d RPCs)", len(data), time.Since(start), requests)

	echo := openServiceChild(t, c, w.Path, "echo")
	if _, err := echo.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	if _, err := io.ReadFull(echo, one[:]); err != nil || one[0] != 'x' {
		t.Fatal("interactive output waited for a full window", err)
	}
}

func BenchmarkWeakNetworkDocumentSave(b *testing.B) {
	for _, rtt := range []time.Duration{0, 50 * time.Millisecond, 200 * time.Millisecond} {
		b.Run(fmt.Sprintf("RTT_%dms", rtt.Milliseconds()), func(b *testing.B) {
			c, w := weakPeer(b, rtt/2, 8<<20, 0)
			if err := os.WriteFile(filepath.Join(w.Path, "text"), nil, 0600); err != nil {
				b.Fatal(err)
			}
			doc, _, err := c.OpenDocument(b.Context(), w.ID, "text", true)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(doc.Close)
			values := [2]string{strings.Repeat("a", 1<<20), strings.Repeat("b", 1<<20)}
			b.SetBytes(int64(len(values[0])))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := doc.Save(b.Context(), values[i%2]); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
		})
	}
}
