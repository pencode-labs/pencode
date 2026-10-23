package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLSPServerProcess(t *testing.T) {
	if len(os.Args) == 0 || os.Args[len(os.Args)-1] != "--lsp-test-server" {
		return
	}
	err := runTestServer()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}
func runTestServer() error {
	r, w := NewReader(os.Stdin), Writer{Target: os.Stdout}
	send := func(m Message) error {
		m.JSONRPC = "2.0"
		raw, err := json.Marshal(m)
		if err != nil {
			return err
		}
		return w.Write(raw)
	}
	text := ""
	version, saves := 0, 0
	initialized, closed := false, false
	for {
		raw, err := r.Read()
		if err != nil {
			return err
		}
		var m Message
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		var result any
		switch m.Method {
		case "initialize":
			if err := send(Message{ID: json.RawMessage(`"configuration-during-initialize"`), Method: "workspace/configuration", Params: json.RawMessage(`{"items":[{"section":"test.enabled"},{"section":"missing"}]}`)}); err != nil {
				return err
			}
			raw, err := r.Read()
			if err != nil {
				return err
			}
			var response Message
			_ = json.Unmarshal(raw, &response)
			if string(response.Result) != "[true,null]" {
				return fmt.Errorf("bad configuration: %s", raw)
			}
			result = map[string]any{"capabilities": map[string]any{"positionEncoding": "utf-16", "textDocumentSync": map[string]any{"openClose": true, "change": 2, "willSave": true, "save": map[string]bool{"includeText": true}}, "hoverProvider": true}}
		case "initialized":
			initialized = true
			continue
		case "workspace/didChangeConfiguration", "textDocument/willSave":
			continue
		case "textDocument/didOpen":
			var p struct {
				TextDocument struct {
					Text    string `json:"text"`
					Version int    `json:"version"`
					URI     URI    `json:"uri"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(m.Params, &p)
			text, version = p.TextDocument.Text, p.TextDocument.Version
			params, _ := json.Marshal(PublishDiagnosticsParams{URI: p.TextDocument.URI, Version: &version, Diagnostics: []Diagnostic{{Range: Range{}, Severity: 2, Message: "test diagnostic"}}})
			if err := send(Message{Method: "textDocument/publishDiagnostics", Params: params}); err != nil {
				return err
			}
			continue
		case "textDocument/didChange":
			var p struct {
				TextDocument struct {
					Version int `json:"version"`
				} `json:"textDocument"`
				ContentChanges []struct {
					Range *Range `json:"range"`
					Text  string `json:"text"`
				} `json:"contentChanges"`
			}
			_ = json.Unmarshal(m.Params, &p)
			for _, change := range p.ContentChanges {
				if change.Range == nil {
					text = change.Text
				} else {
					text, err = ApplyTextEdits(text, []TextEdit{{*change.Range, change.Text}}, UTF16)
					if err != nil {
						return err
					}
				}
			}
			version = p.TextDocument.Version
			continue
		case "textDocument/didSave":
			var p struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(m.Params, &p)
			if p.Text != text {
				return fmt.Errorf("didSave text mismatch")
			}
			saves++
			continue
		case "textDocument/didClose":
			closed = true
			continue
		case "test/inspect":
			result = map[string]any{"text": text, "version": version, "saves": saves, "initialized": initialized, "closed": closed}
		case "shutdown":
			result = nil
		case "exit":
			return nil
		default:
			if m.Method == "" {
				continue
			}
			result = m.Params
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if err = send(Message{ID: m.ID, Result: encoded}); err != nil {
			return err
		}
	}
}
func TestClientLifecycleDocumentSyncAndConfiguration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	diagnostics := make(chan PublishDiagnosticsParams, 1)
	init, cancelInit := context.WithCancel(ctx)
	client, err := startLocal(init, os.Args[0], []string{"-test.run=^TestLSPServerProcess$", "--", "--lsp-test-server"}, Options{Root: t.TempDir(), Configuration: json.RawMessage(`{"test":{"enabled":true}}`), Handler: func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method == "textDocument/publishDiagnostics" {
			var p PublishDiagnosticsParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, err
			}
			diagnostics <- p
			return nil, nil
		}
		return nil, &ResponseError{Code: MethodNotFound, Message: method}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	cancelInit() // initialization context must not own the session lifetime.
	uri := FileURI(filepath.Join(t.TempDir(), "中.go"))
	if err := client.SyncDocument(ctx, uri, "go", "中😀\r\nold", 1); err != nil {
		t.Fatal(err)
	}
	if err := client.SyncDocument(ctx, uri, "go", "中😀\r\nnew", 2); err != nil {
		t.Fatal(err)
	}
	if err := client.SyncDocument(ctx, uri, "go", "中😀\r\nnew", 3); err != nil {
		t.Fatal(err)
	}
	if err := client.WillSave(ctx, uri); err != nil {
		t.Fatal(err)
	}
	if err := client.DidSave(ctx, uri); err != nil {
		t.Fatal(err)
	}
	if err := client.CloseDocument(ctx, uri); err != nil {
		t.Fatal(err)
	}
	var state struct {
		Text        string `json:"text"`
		Version     int    `json:"version"`
		Saves       int    `json:"saves"`
		Initialized bool   `json:"initialized"`
		Closed      bool   `json:"closed"`
	}
	if err := client.Call(ctx, "test/inspect", nil, &state); err != nil {
		t.Fatal(err)
	}
	if state.Text != "中😀\r\nnew" || state.Version != 3 || state.Saves != 1 || !state.Initialized || !state.Closed {
		t.Fatalf("%+v", state)
	}
	select {
	case p := <-diagnostics:
		if len(p.Diagnostics) != 1 || p.URI != uri {
			t.Fatalf("%+v", p)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := client.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.ProcessDone:
	default:
		t.Fatal("server process not reaped")
	}
}
func TestGoplsIntegration(t *testing.T) {
	command, err := exec.LookPath("gopls")
	if err != nil {
		t.Skip("gopls is not installed")
	}
	root := t.TempDir()
	source := "package demo\n\nfunc Add(a,b int) int{return a+b}\nfunc Use() int { return Add(1,2) }\n"
	for name, content := range map[string]string{"go.mod": "module example.com/lspdemo\n\ngo 1.26.0\n", "main.go": source} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := startLocal(ctx, command, nil, Options{Root: root, Handler: func(context.Context, string, json.RawMessage) (any, error) { return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = client.Shutdown(stop)
	}()
	uri := FileURI(filepath.Join(root, "main.go"))
	if err := client.SyncDocument(ctx, uri, "go", source, 1); err != nil {
		t.Fatal(err)
	}
	var edits []TextEdit
	if err := client.Call(ctx, "textDocument/formatting", map[string]any{"textDocument": TextDocumentIdentifier{uri}, "options": map[string]any{"tabSize": 4, "insertSpaces": true}}, &edits); err != nil {
		t.Fatal(err)
	}
	formatted, err := ApplyTextEdits(source, edits, client.Encoding)
	if err != nil || !strings.Contains(formatted, "a, b int") {
		t.Fatalf("%q %v", formatted, err)
	}
	offset := strings.LastIndex(source, "Add(")
	position, _ := PositionAt(source, offset, client.Encoding)
	var locations []Location
	if err := client.Call(ctx, "textDocument/definition", TextDocumentPositionParams{TextDocumentIdentifier{uri}, position}, &locations); err != nil || len(locations) != 1 || locations[0].URI != uri {
		t.Fatalf("%+v %v", locations, err)
	}
}

func TestDynamicProviderOptionsFollowRegistrationLifecycle(t *testing.T) {
	client := &Client{registrations: make(map[string]Registration), Capabilities: ServerCapabilities{}}
	options := json.RawMessage(`{"triggerCharacters":["."],"resolveProvider":true}`)
	params, _ := json.Marshal(map[string]any{"registrations": []Registration{{ID: "completion", Method: "textDocument/completion", RegisterOptions: options}}})
	if _, err := client.handle(context.Background(), "client/registerCapability", params); err != nil {
		t.Fatal(err)
	}
	if !client.Supports("textDocument/completion", "completionProvider") || string(client.ProviderOptions("textDocument/completion", "completionProvider")) != string(options) {
		t.Fatal("dynamic completion trigger and resolve options unavailable")
	}
	if _, err := client.handle(context.Background(), "client/unregisterCapability", json.RawMessage(`{"unregisterations":[{"id":"completion","method":"textDocument/completion"}]}`)); err != nil {
		t.Fatal(err)
	}
	if client.Supports("textDocument/completion", "completionProvider") || client.ProviderOptions("textDocument/completion", "completionProvider") != nil {
		t.Fatal("unregistered provider retained")
	}
}
