package dap

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestConcurrentRequestsCancellationAndReverseRequest(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	events := make(chan string, 1)
	c := New(a, func(name string, _ json.RawMessage) { events <- name }, func(ctx context.Context, command string, _ json.RawMessage) (any, error) {
		if command != "runInTerminal" {
			return nil, fmt.Errorf("unexpected %s", command)
		}
		return map[string]int{"processId": 42}, nil
	})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	adapter := make(chan error, 1)
	go func() {
		r := bufio.NewReader(b)
		read := func() (Message, error) {
			data, err := readMessage(r, nil)
			var m Message
			if err == nil {
				err = json.Unmarshal(data, &m)
			}
			return m, err
		}
		write := func(m Message) error {
			data, err := json.Marshal(m)
			if err == nil {
				err = writeMessage(b, data)
			}
			return err
		}
		first, err := read()
		if err != nil {
			adapter <- err
			return
		}
		second, err := read()
		if err != nil {
			adapter <- err
			return
		}
		if err = write(Message{Seq: 1, Type: "request", Command: "runInTerminal", Arguments: json.RawMessage(`{}`)}); err != nil {
			adapter <- err
			return
		}
		reverse, err := read()
		if err != nil {
			adapter <- err
			return
		}
		if reverse.Type != "response" || reverse.RequestSeq != 1 || !reverse.Success || string(reverse.Body) != `{"processId":42}` {
			adapter <- fmt.Errorf("bad reverse response: %+v", reverse)
			return
		}
		for _, request := range []Message{second, first} {
			if err = write(Message{Type: "response", RequestSeq: request.Seq, Success: true, Body: json.RawMessage(`{"value":7}`)}); err != nil {
				adapter <- err
				return
			}
		}
		if err = write(Message{Type: "event", Event: "stopped", Body: json.RawMessage(`{}`)}); err != nil {
			adapter <- err
			return
		}
		_, err = read() // cancellation removes a pending request even without a response
		adapter <- err
	}()
	results := make(chan error, 2)
	for range 2 {
		go func() {
			var result struct {
				Value int `json:"value"`
			}
			err := c.Call(ctx, "threads", struct{}{}, &result)
			if err == nil && result.Value != 7 {
				err = fmt.Errorf("bad result %+v", result)
			}
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	select {
	case name := <-events:
		if name != "stopped" {
			t.Fatal(name)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	short, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	if err := c.Call(short, "pause", struct{}{}, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if err := <-adapter; err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	pending := len(c.pending)
	c.mu.Unlock()
	if pending != 0 {
		t.Fatal("cancelled request retained")
	}
}

func TestFramingRejectsInvalidLengthsAndReusesBuffer(t *testing.T) {
	for _, header := range []string{"Content-Length: -1", "Content-Length: +1", "Content-Length: 0", "Content-Length: 999999999", "Content-Length: 2\r\nContent-Length: 2", "Other: 1"} {
		if _, err := readMessage(bufio.NewReader(strings.NewReader(header+"\r\n\r\n{}")), nil); err == nil {
			t.Fatal("accepted", header)
		}
	}
	r := bufio.NewReader(strings.NewReader("Content-Length: 2\r\n\r\n{}Content-Length: 2\r\n\r\n{}"))
	first, err := readMessage(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := readMessage(r, first)
	if err != nil || &first[0] != &second[0] {
		t.Fatal("framing storage not reused", err)
	}
}

func TestCallCancellationWhileAdapterDoesNotRead(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	c := New(a, nil, nil)
	defer c.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := c.Call(ctx, "launch", struct{}{}, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked write prevented cancellation: %v", err)
	}
}
