package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

type fragmented struct{ bytes.Buffer }

func (f *fragmented) Read(p []byte) (int, error)  { return f.Buffer.Read(p[:min(len(p), 3)]) }
func (f *fragmented) Write(p []byte) (int, error) { return f.Buffer.Write(p[:min(len(p), 2)]) }
func TestFraming(t *testing.T) {
	var stream fragmented
	w := Writer{Target: &stream}
	want := []byte(`{"jsonrpc":"2.0","method":"中文/😀"}`)
	for range 2 {
		if err := w.Write(want); err != nil {
			t.Fatal(err)
		}
	}
	r := NewReader(&stream)
	for range 2 {
		got, err := r.Read()
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%q %v", got, err)
		}
	}
	for _, wire := range []string{
		"\r\n", "Content-Length: -1\r\n\r\n", "Content-Length: 0\r\n\r\n", "Content-Length: 33554433\r\n\r\n", "Content-Length: 2\r\nContent-Length: 2\r\n\r\n{}", "Content-Length: 2\n\n{}", "Content-Length: 2\r\nContent-Type: application/vscode-jsonrpc; charset=latin1\r\n\r\n{}", "Content-Length: 5\r\n\r\n{}", strings.Repeat("x", 9000),
	} {
		if _, err := NewReader(strings.NewReader(wire)).Read(); err == nil {
			t.Fatalf("accepted %q", wire[:min(80, len(wire))])
		}
	}
}
func TestCachedFramingAllocatesNothing(t *testing.T) {
	var wire bytes.Buffer
	w := Writer{Target: &wire}
	data := []byte(`{"jsonrpc":"2.0","method":"ping"}`)
	if err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	framed := bytes.Clone(wire.Bytes())
	source := bytes.NewReader(framed)
	r := NewReader(source)
	_, _ = r.Read()
	if n := testing.AllocsPerRun(100, func() { wire.Reset(); _ = w.Write(data); source.Reset(framed); _, _ = r.Read() }); n != 0 {
		t.Fatalf("cached framing allocated %g", n)
	}
}
func TestDuplexRequestsCancellationAndServerStringIDs(t *testing.T) {
	a, b := net.Pipe()
	serverRead := NewReader(b)
	serverWrite := Writer{Target: b}
	called := make(chan string, 8)
	conn := NewConn(a, a, func() { a.Close() }, func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		called <- method
		if method == "server/nested" {
			return map[string]bool{"ok": true}, nil
		}
		return nil, &ResponseError{Code: MethodNotFound, Message: method}
	})
	defer conn.Close()
	defer b.Close()
	serverErr := make(chan error, 1)
	go func() {
		fail := func(err error) { serverErr <- err }
		raw, err := serverRead.Read()
		if err != nil {
			fail(err)
			return
		}
		var req Message
		if err = json.Unmarshal(raw, &req); err != nil {
			fail(err)
			return
		}
		nested := Message{JSONRPC: "2.0", ID: json.RawMessage(`"request-1"`), Method: "server/nested", Params: json.RawMessage(`{}`)}
		data, _ := json.Marshal(nested)
		if err = serverWrite.Write(data); err != nil {
			fail(err)
			return
		}
		raw, err = serverRead.Read()
		if err != nil {
			fail(err)
			return
		}
		var reply Message
		_ = json.Unmarshal(raw, &reply)
		if string(reply.ID) != `"request-1"` || string(reply.Result) != `{"ok":true}` {
			fail(fmt.Errorf("bad server reply %s", raw))
			return
		}
		data, _ = json.Marshal(Message{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"items":[1,2]}`)})
		if err = serverWrite.Write(data); err != nil {
			fail(err)
			return
		}
		raw, err = serverRead.Read()
		if err != nil {
			fail(err)
			return
		}
		_ = json.Unmarshal(raw, &req)
		raw, err = serverRead.Read()
		if err != nil {
			fail(err)
			return
		}
		var cancel Message
		_ = json.Unmarshal(raw, &cancel)
		if cancel.Method != "$/cancelRequest" {
			fail(fmt.Errorf("missing cancellation: %s", raw))
			return
		}
		serverErr <- nil
	}()
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	var result struct {
		Items []int `json:"items"`
	}
	if err := conn.Call(ctx, "custom/anything", map[string]bool{"enabled": true}, &result); err != nil || len(result.Items) != 2 {
		t.Fatalf("%+v %v", result, err)
	}
	select {
	case method := <-called:
		if method != "server/nested" {
			t.Fatal(method)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	canceled, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := conn.Call(canceled, "slow", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
func TestCloseReleasesPendingRequests(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	conn := NewConn(a, a, func() { a.Close() }, nil)
	result := make(chan error, 1)
	go func() { result <- conn.Call(context.Background(), "blocked", nil, nil) }()
	conn.Close()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("closed call succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("pending call leaked")
	}
}
func TestRejectMalformedRPC(t *testing.T) {
	for _, raw := range []string{`[]`, `{"jsonrpc":"1.0","method":"ping"}`, `{"jsonrpc":"2.0","id":1}`, `{"jsonrpc":"2.0","id":true,"method":"ping"}`, `{"jsonrpc":"2.0","id":1,"result":null,"error":{"code":1,"message":"bad"}}`} {
		a, b := net.Pipe()
		conn := NewConn(a, a, func() { a.Close() }, nil)
		w := Writer{Target: b}
		_ = w.Write([]byte(raw))
		select {
		case <-conn.Done():
		case <-time.After(time.Second):
			t.Fatal("malformed RPC accepted", raw)
		}
		if conn.Err() == nil || errors.Is(conn.Err(), io.EOF) {
			t.Fatal(conn.Err())
		}
		b.Close()
	}
}
