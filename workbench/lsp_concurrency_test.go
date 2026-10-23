package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/diiyw/onix/projects/pencode/lsp"
)

func languageDispatchFixture(t *testing.T) (*pencodeLanguageSession, <-chan lsp.Message, <-chan pencodeLanguageEvent, func(lsp.Message)) {
	t.Helper()
	local, peer := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	client := &lsp.Client{Conn: lsp.NewConn(local, local, func() { local.Close() }, nil)}
	session := &pencodeLanguageSession{jobs: make(chan pencodeLanguageJob, 64), cancel: cancel, done: make(chan struct{})}
	events := make(chan pencodeLanguageEvent, 128)
	go func() {
		defer close(session.done)
		session.runJobs(ctx, client, func(event pencodeLanguageEvent) bool {
			select {
			case events <- event:
				return true
			case <-ctx.Done():
				return false
			}
		})
	}()
	packets := make(chan lsp.Message, 128)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		reader := lsp.NewReader(peer)
		for {
			data, err := reader.Read()
			if err != nil {
				return
			}
			var message lsp.Message
			if json.Unmarshal(data, &message) != nil {
				return
			}
			select {
			case packets <- message:
			case <-ctx.Done():
				return
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		client.Close()
		peer.Close()
		waitPencodeScanSignal(t, session.done)
		waitPencodeScanSignal(t, readerDone)
	})
	writer := lsp.Writer{Target: peer}
	return session, packets, events, func(request lsp.Message) {
		t.Helper()
		data, err := json.Marshal(lsp.Message{JSONRPC: "2.0", ID: request.ID, Result: json.RawMessage(`null`)})
		if err == nil {
			err = writer.Write(data)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func receiveLanguagePacket(t *testing.T, packets <-chan lsp.Message, method string) lsp.Message {
	t.Helper()
	select {
	case packet := <-packets:
		if packet.Method != method {
			t.Fatalf("received %s, want %s", packet.Method, method)
		}
		return packet
	case <-time.After(2 * time.Second):
		t.Fatalf("%s was blocked behind a server response", method)
		return lsp.Message{}
	}
}

func TestLanguageSlowResponseDoesNotBlockNotificationsOrCompletion(t *testing.T) {
	session, packets, events, reply := languageDispatchFixture(t)
	request, cancel := context.WithCancel(context.Background())
	defer cancel()
	session.jobs <- pencodeLanguageJob{method: "textDocument/semanticTokens/full", context: request}
	receiveLanguagePacket(t, packets, "textDocument/semanticTokens/full")
	session.jobs <- pencodeLanguageJob{method: "textDocument/didChange", notify: true}
	session.jobs <- pencodeLanguageJob{method: "textDocument/completion"}
	receiveLanguagePacket(t, packets, "textDocument/didChange")
	fast := receiveLanguagePacket(t, packets, "textDocument/completion")
	reply(fast)
	select {
	case event := <-events:
		if event.job.method != fast.Method || event.err != nil {
			t.Fatalf("completion failed or slow response came first: %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("completion waited for the slow response")
	}
	cancel()
	select {
	case event := <-events:
		if !errors.Is(event.err, context.Canceled) {
			t.Fatalf("request cancellation failed: %v", event.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled request kept a response goroutine alive")
	}
}

func TestLanguageResponseConcurrencyIsBoundedAndQueueKeepsNotificationsFlowing(t *testing.T) {
	session, packets, events, reply := languageDispatchFixture(t)
	var first lsp.Message
	for index := range pencodeLanguageConcurrency {
		session.jobs <- pencodeLanguageJob{method: "slow"}
		packet := receiveLanguagePacket(t, packets, "slow")
		if index == 0 {
			first = packet
		}
	}
	for range 64 {
		session.jobs <- pencodeLanguageJob{method: "queued"}
	}
	session.jobs <- pencodeLanguageJob{method: "overflow"}
	session.jobs <- pencodeLanguageJob{method: "textDocument/didChange", notify: true}
	// No ninth RPC may reach the wire before a response releases a slot.
	receiveLanguagePacket(t, packets, "textDocument/didChange")
	select {
	case event := <-events:
		if event.job.method != "overflow" || !errors.Is(event.err, errPencodeLanguageBusy) {
			t.Fatalf("unbounded or silently dropped queue overflow: %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queue overflow was not reported")
	}
	reply(first)
	receiveLanguagePacket(t, packets, "queued")
	// Cleanup cancels all other active/queued requests and joins their goroutines.
}
