package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReaderReleasesLargeMessageBeforeNextFrame(t *testing.T) {
	var stream bytes.Buffer
	w := Writer{Target: &stream}
	data := bytes.Repeat([]byte("x"), 2<<20)
	if err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	r := NewReader(&stream)
	got, err := r.Read()
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("large frame was not read intact", err)
	}
	// Starting the next read invalidates the borrowed message, even if no new
	// frame has arrived. The reader must then release its oversized storage.
	if _, err := r.Read(); !errors.Is(err, io.EOF) || cap(r.buffer) != 0 {
		t.Fatal("idle reader retained an oversized message", err, cap(r.buffer))
	}
	if err := w.Write([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Read(); err != nil || string(got) != "{}" {
		t.Fatal("small frame after large response failed", err)
	}
}

type signaledStream struct {
	net.Conn
	once    sync.Once
	entered chan struct{}
}

func (s *signaledStream) Write(p []byte) (int, error) {
	s.once.Do(func() { close(s.entered) })
	return s.Conn.Write(p)
}

func TestNotificationOwnsEncodedBytesUntilWritten(t *testing.T) {
	local, peer := net.Pipe()
	defer peer.Close()
	_ = peer.SetDeadline(time.Now().Add(5 * time.Second))
	stream := &signaledStream{Conn: local, entered: make(chan struct{})}
	c := NewConn(stream, stream, func() { stream.Close() }, nil)
	defer c.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	want := strings.Repeat("中🙂<&\n", 100)
	params := map[string]string{"text": want}
	if err := c.Notify(ctx, "test/notification", params); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stream.entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	cancel()
	params["text"] = "changed after Notify returned"
	for range 32 {
		data, err := encodeMessage(map[string]string{"other": strings.Repeat("x", len(want))})
		if err != nil {
			t.Fatal(err)
		}
		releaseMessage(data)
	}
	raw, err := NewReader(peer).Read()
	if err != nil {
		t.Fatal(err)
	}
	var message struct {
		Method string            `json:"method"`
		Params map[string]string `json:"params"`
	}
	if err := json.Unmarshal(raw, &message); err != nil || message.Method != "test/notification" || message.Params["text"] != want {
		t.Fatal("in-flight notification storage was reused", err)
	}
}

type blockedMessageWriter struct {
	entered, stopped chan struct{}
	once             sync.Once
}

func (w *blockedMessageWriter) Write([]byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.stopped
	return 0, io.ErrClosedPipe
}

func TestCloseDrainsMessagesWithConcurrentEnqueuers(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	w := &blockedMessageWriter{entered: make(chan struct{}), stopped: make(chan struct{})}
	c := &Conn{ctx: ctx, cancel: cancel, done: make(chan struct{}), outgoing: make(chan outbound, 64), closeIO: func() { close(w.stopped) }}
	finished := make(chan struct{})
	go func() { defer close(finished); c.write(w) }()
	defer c.Close()
	if err := c.Notify(ctx, "first", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	for range cap(c.outgoing) {
		if err := c.Notify(ctx, "queued", nil); err != nil {
			t.Fatal(err)
		}
	}
	var senders sync.WaitGroup
	for range 32 {
		senders.Go(func() {
			if err := c.Notify(context.Background(), "concurrent", nil); !errors.Is(err, ErrClosed) {
				t.Errorf("closed notification returned %v", err)
			}
		})
	}
	c.Close()
	senders.Wait()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("writer shutdown blocked on enqueuers")
	}
	if len(c.outgoing) != 0 {
		t.Fatal("closed connection retained queued message buffers")
	}
	if err := c.Notify(context.Background(), "after-close", nil); !errors.Is(err, ErrClosed) || len(c.outgoing) != 0 {
		t.Fatal("closed connection accepted a message", err)
	}
}

func TestNotificationJSONValuesAndErrors(t *testing.T) {
	local, peer := net.Pipe()
	defer peer.Close()
	_ = peer.SetDeadline(time.Now().Add(5 * time.Second))
	c := NewConn(local, local, func() { local.Close() }, nil)
	defer c.Close()
	r := NewReader(peer)
	for _, params := range []any{nil, (*int)(nil), struct{}{}, map[string]string{"text": "中🙂<&\n"}, json.RawMessage(` { "field": [1, 2, null] } `)} {
		if err := c.Notify(t.Context(), "custom/method", params); err != nil {
			t.Fatal(err)
		}
		raw, err := r.Read()
		if err != nil {
			t.Fatal(err)
		}
		var got, want map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		expected := map[string]any{"jsonrpc": "2.0", "method": "custom/method"}
		if params != nil {
			expected["params"] = params
		}
		encoded, _ := json.Marshal(expected)
		_ = json.Unmarshal(encoded, &want)
		if !reflect.DeepEqual(got, want) {
			t.Fatal("changed notification JSON", got, want)
		}
	}
	for _, params := range []any{make(chan int), json.RawMessage(`invalid`), strings.Repeat("x", MaxMessage)} {
		if err := c.Notify(t.Context(), "invalid", params); err == nil {
			t.Fatal("accepted invalid or oversized notification")
		}
	}
	if err := c.Notify(t.Context(), "after-error", nil); err != nil {
		t.Fatal(err)
	}
	raw, err := r.Read()
	if err != nil || !strings.Contains(string(raw), `"method":"after-error"`) {
		t.Fatal("encoding error corrupted a later notification", err)
	}
}

func TestDocumentNotificationPayloads(t *testing.T) {
	type wireDocument struct {
		URI        URI    `json:"uri"`
		LanguageID string `json:"languageId"`
		Version    int    `json:"version"`
		Text       string `json:"text"`
	}
	for _, kind := range []int{1, 2} {
		t.Run(strconv.Itoa(kind), func(t *testing.T) {
			local, peer := net.Pipe()
			defer peer.Close()
			_ = peer.SetDeadline(time.Now().Add(5 * time.Second))
			conn := NewConn(local, local, func() { local.Close() }, nil)
			defer conn.Close()
			c := &Client{Conn: conn, documents: make(map[URI]document), syncKind: kind, Encoding: UTF16, openClose: true, save: true, willSave: true}
			r := NewReader(peer)
			read := func(method string) json.RawMessage {
				t.Helper()
				raw, err := r.Read()
				var message Message
				if err != nil || json.Unmarshal(raw, &message) != nil || message.Method != method {
					t.Fatalf("expected %s, received %s: %v", method, raw, err)
				}
				return message.Params
			}
			const uri URI = "file:///tmp/test.go"
			const initial = "中🙂\r\ntext"
			if err := c.SyncDocument(t.Context(), uri, "go", initial, 1); err != nil {
				t.Fatal(err)
			}
			var opened struct {
				TextDocument wireDocument `json:"textDocument"`
			}
			if err := json.Unmarshal(read("textDocument/didOpen"), &opened); err != nil || opened.TextDocument != (wireDocument{uri, "go", 1, initial}) {
				t.Fatal("incorrect didOpen", opened, err)
			}
			if err := c.SyncDocument(t.Context(), uri, "go", "", 2); err != nil {
				t.Fatal(err)
			}
			var changed struct {
				TextDocument   wireDocument `json:"textDocument"`
				ContentChanges []struct {
					Range *Range `json:"range"`
					Text  string `json:"text"`
				} `json:"contentChanges"`
			}
			if err := json.Unmarshal(read("textDocument/didChange"), &changed); err != nil || changed.TextDocument != (wireDocument{URI: uri, Version: 2}) || len(changed.ContentChanges) != 1 {
				t.Fatal("incorrect didChange", changed, err)
			}
			change := changed.ContentChanges[0]
			if change.Text != "" || (change.Range != nil) != (kind == 2) {
				t.Fatal("changed full/incremental or empty replacement semantics", change)
			}
			if change.Range != nil {
				if result, err := ApplyTextEdits(initial, []TextEdit{{*change.Range, change.Text}}, UTF16); err != nil || result != "" {
					t.Fatal("incremental range failed to clear Unicode text", err)
				}
			}
			for _, includeText := range []bool{false, true} {
				c.saveText = includeText
				if err := c.DidSave(t.Context(), uri); err != nil {
					t.Fatal(err)
				}
				var saved map[string]json.RawMessage
				if err := json.Unmarshal(read("textDocument/didSave"), &saved); err != nil || (saved["text"] != nil) != includeText || includeText && string(saved["text"]) != `""` {
					t.Fatal("changed optional/empty saved text semantics", saved, err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := c.SyncDocument(ctx, uri, "go", "cancelled", 3); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if version, _ := c.DocumentVersion(uri); version != 2 {
				t.Fatal("failed synchronization advanced the version")
			}
			if err := c.WillSave(t.Context(), uri); err != nil {
				t.Fatal(err)
			}
			read("textDocument/willSave")
			if err := c.CloseDocument(t.Context(), uri); err != nil {
				t.Fatal(err)
			}
			read("textDocument/didClose")
		})
	}
}
