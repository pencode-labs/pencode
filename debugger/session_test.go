package debugger

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/dap"
	"github.com/pencode-labs/pencode/lsp"
	"github.com/pencode-labs/pencode/output"
)

func evaluationTestSession(t *testing.T, handler dap.Handler) (*Session, func(func(Snapshot) bool) Snapshot) {
	t.Helper()
	a, b := net.Pipe()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	wake := make(chan struct{}, 1)
	var store output.Store
	s := &Session{ctx: ctx, cancel: cancel, loading: make(map[int]bool), Output: store.Channel("debug"), view: Snapshot{Revision: 1, Epoch: 1, FrameID: 10, State: Stopped}, wake: func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}}
	adapter := dap.New(b, nil, handler)
	s.conn = dap.New(a, s.event, nil)
	t.Cleanup(func() { s.Close(); _ = adapter.Close() })
	wait := func(ready func(Snapshot) bool) Snapshot {
		t.Helper()
		for {
			view := s.Snapshot()
			if ready(view) {
				return view
			}
			select {
			case <-wake:
			case <-ctx.Done():
				t.Fatalf("waiting for evaluation: %v, %+v", ctx.Err(), view)
			}
		}
	}
	return s, wait
}

func TestSessionEvaluationsExpandAndClear(t *testing.T) {
	results := []dap.EvaluateResponse{
		{Result: "42", Type: "int"},
		{Result: "Options {Debug: true}", Type: "Options", VariablesReference: 20, NamedVariables: 1},
		{Result: "[]int len: 2", Type: "[]int", VariablesReference: 30, IndexedVariables: 2},
		{Result: "map[string]int [...]", Type: "map[string]int", VariablesReference: 40, NamedVariables: 1},
		{Result: "(*Options)(0x1234)", Type: "*Options", VariablesReference: 50, NamedVariables: 1},
	}
	s, wait := evaluationTestSession(t, func(_ context.Context, command string, payload json.RawMessage) (any, error) {
		switch command {
		case "evaluate":
			var args struct {
				Expression string `json:"expression"`
				FrameID    int    `json:"frameId"`
				Context    string `json:"context"`
			}
			if err := json.Unmarshal(payload, &args); err != nil {
				return nil, err
			}
			if args.FrameID != 10 || args.Context != "repl" {
				return nil, fmt.Errorf("wrong evaluation context: %+v", args)
			}
			if args.Expression == "bad" {
				return nil, fmt.Errorf("unknown expression")
			}
			var index int
			if _, err := fmt.Sscan(args.Expression, &index); err != nil {
				return nil, err
			}
			return results[index], nil
		case "variables":
			var args struct {
				Reference int `json:"variablesReference"`
			}
			if err := json.Unmarshal(payload, &args); err != nil {
				return nil, err
			}
			children := []dap.Variable{{Name: "child", Value: "nested", VariablesReference: 60}}
			if args.Reference == 60 {
				children = []dap.Variable{{Name: "value", Value: "42"}}
			}
			return struct {
				Variables []dap.Variable `json:"variables"`
			}{children}, nil
		}
		return nil, fmt.Errorf("unexpected request: %s", command)
	})
	for i, result := range results {
		s.Evaluate(fmt.Sprint(i))
		view := wait(func(v Snapshot) bool { return len(v.Evaluations) == i+1 })
		got := view.Evaluations[i]
		if got.Value != result.Result || got.Type != result.Type || got.VariablesReference != result.VariablesReference || got.NamedVariables != result.NamedVariables || got.IndexedVariables != result.IndexedVariables {
			t.Fatalf("evaluation metadata lost: %+v, want %+v", got, result)
		}
		if got.VariablesReference > 0 {
			s.Expand(got.VariablesReference)
			wait(func(v Snapshot) bool { return len(v.Variables[got.VariablesReference]) == 1 })
		}
	}
	s.Expand(60)
	wait(func(v Snapshot) bool { return len(v.Variables[60]) == 1 && v.Variables[60][0].Value == "42" })
	s.Evaluate("bad")
	view := wait(func(v Snapshot) bool { return len(v.Evaluations) == len(results)+1 })
	if !strings.Contains(view.Evaluations[len(results)].Value, "unknown expression") {
		t.Fatal("evaluation errors missing from console", view)
	}
	// Continuing preserves text history while freezing expired object references.
	s.event("continued", json.RawMessage(`{}`))
	for _, result := range s.Snapshot().Evaluations {
		if result.VariablesReference != 0 {
			t.Fatal("expired reference retained", result)
		}
	}
	if view.Evaluations[1].VariablesReference != 20 {
		t.Fatal("published snapshot was mutated")
	}
	_, _ = s.Output.Write([]byte("log output"))
	s.ClearConsole()
	if text, _ := s.Output.Snapshot(); text != "" || len(s.Snapshot().Evaluations) != 0 {
		t.Fatal("console clear retained output or evaluations")
	}
	_, _ = s.Output.Write([]byte("new output"))
	if text, _ := s.Output.Snapshot(); text != "new output" {
		t.Fatal("console stopped accepting output after clear")
	}
}

func TestSessionDiscardsLateEvaluations(t *testing.T) {
	for _, action := range []string{"clear", "continue", "frame"} {
		t.Run(action, func(t *testing.T) {
			pending, release := make(chan struct{}), make(chan struct{})
			s, _ := evaluationTestSession(t, func(ctx context.Context, command string, _ json.RawMessage) (any, error) {
				if command != "evaluate" {
					return nil, fmt.Errorf("unexpected request: %s", command)
				}
				close(pending)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return dap.EvaluateResponse{Result: "stale", VariablesReference: 20}, nil
			})
			processed := make(chan struct{}, 1)
			s.wake = func() { processed <- struct{}{} }
			s.Evaluate("delayed")
			select {
			case <-pending:
			case <-s.ctx.Done():
				t.Fatal(s.ctx.Err())
			}
			switch action {
			case "clear":
				s.ClearConsole()
				<-processed
			case "continue":
				s.event("continued", json.RawMessage(`{}`))
				<-processed
			case "frame":
				s.mu.Lock()
				s.selectFrameLocked(11)
				s.mu.Unlock()
			}
			close(release)
			select {
			case <-processed:
			case <-s.ctx.Done():
				t.Fatal(s.ctx.Err())
			}
			if view := s.Snapshot(); len(view.Evaluations) != 0 {
				t.Fatalf("stale evaluation published after %s: %+v", action, view)
			}
		})
	}
}

func TestSessionDiscardsLateVariablesAfterContinueAndDetaches(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var writeMu sync.Mutex
	write := func(m dap.Message) {
		data, _ := json.Marshal(m)
		writeMu.Lock()
		defer writeMu.Unlock()
		_, _ = fmt.Fprintf(b, "Content-Length: %d\r\n\r\n", len(data))
		_, _ = b.Write(data)
	}
	respond := func(request dap.Message, body any) {
		data, _ := json.Marshal(body)
		write(dap.Message{Type: "response", RequestSeq: request.Seq, Command: request.Command, Success: true, Body: data})
	}
	event := func(name, body string) { write(dap.Message{Type: "event", Event: name, Body: json.RawMessage(body)}) }
	configurationDone := make(chan struct{})
	variablesPending := make(chan struct{})
	releaseVariables := make(chan struct{})
	variablesResponded := make(chan struct{})
	disconnected := make(chan bool, 1)
	go func() {
		r := lsp.NewReader(bufio.NewReader(b))
		for {
			data, err := r.Read()
			if err != nil {
				return
			}
			var request dap.Message
			if json.Unmarshal(data, &request) != nil {
				return
			}
			go func() {
				switch request.Command {
				case "initialize":
					respond(request, map[string]bool{"supportsConfigurationDoneRequest": true})
				case "attach":
					event("initialized", `{}`)
					<-configurationDone
					respond(request, struct{}{})
				case "configurationDone":
					respond(request, struct{}{})
					close(configurationDone)
					event("stopped", `{"threadId":1,"reason":"breakpoint","allThreadsStopped":true}`)
				case "threads":
					respond(request, map[string]any{"threads": []dap.Thread{{ID: 1, Name: "main"}}})
				case "stackTrace":
					respond(request, map[string]any{"stackFrames": []dap.StackFrame{{ID: 10, Name: "main", Line: 2}}})
				case "scopes":
					respond(request, map[string]any{"scopes": []dap.Scope{{Name: "locals", VariablesReference: 20}}})
				case "variables":
					close(variablesPending)
					select {
					case <-ctx.Done():
						return
					case <-releaseVariables:
					}
					respond(request, map[string]any{"variables": []dap.Variable{{Name: "old", Value: "stale"}}})
					close(variablesResponded)
				case "continue":
					event("continued", `{"threadId":1,"allThreadsContinued":true}`)
					respond(request, struct{}{})
				case "disconnect":
					var args struct {
						TerminateDebuggee bool `json:"terminateDebuggee"`
					}
					_ = json.Unmarshal(request.Arguments, &args)
					disconnected <- args.TerminateDebuggee
					respond(request, struct{}{})
				}
			}()
		}
	}()
	s := Start(ctx, Config{Adapter: "test", Request: "attach", Configuration: json.RawMessage(`{}`)}, nil, func(context.Context) (io.ReadWriteCloser, error) { return a, nil }, nil, nil, nil)
	defer s.Close()
	select {
	case <-variablesPending:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	before := s.Snapshot().Epoch
	s.Control("continue")
	for s.Snapshot().State != Running {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	close(releaseVariables)
	select {
	case <-variablesResponded:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// A subsequent serialized request ensures the reader has consumed the
	// delayed variables response before checking the published view.
	if err := s.request("threads", struct{}{}, nil); err != nil {
		t.Fatal(err)
	}
	view := s.Snapshot()
	if view.Epoch <= before || len(view.Variables) != 0 {
		t.Fatalf("stale suspended data published: %+v", view)
	}
	if allocs := testing.AllocsPerRun(100, func() { _ = s.Snapshot() }); allocs != 0 {
		t.Fatal("idle snapshots allocate", allocs)
	}
	s.Stop()
	select {
	case terminate := <-disconnected:
		if terminate {
			t.Fatal("attach stop attempted to kill debuggee")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-s.Done():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestConfigPreservesAdapterFieldsAndEscapesWorkspace(t *testing.T) {
	root := `/tmp/项目/"quoted"`
	configs, err := ParseConfigs([]byte(`[{"label":"test","adapter":"stdio","command":"adapter","request":"launch","configuration":{"program":"${workspaceFolder}/main","args":["${workspaceFolder}"],"stopOnEntry":true,"custom":{"depth":7}}}]`), root)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Program     string
		Args        []string
		StopOnEntry bool
		Custom      struct{ Depth int }
	}
	if err := json.Unmarshal(configs[0].Configuration, &result); err != nil {
		t.Fatal(err)
	}
	if result.Program != root+"/main" || result.Args[0] != root || !result.StopOnEntry || result.Custom.Depth != 7 {
		t.Fatalf("payload changed: %+v", result)
	}
	for _, value := range []string{`[]`, `[{}]`, `[{"label":"bad","adapter":"stdio","request":"launch","configuration":{}}]`, `[{"label":"bad","adapter":"Delve","request":"launch","configuration":null}]`} {
		if _, err := ParseConfigs([]byte(value), root); err == nil {
			t.Fatal("accepted invalid config", value)
		}
	}
	if strings.Contains(string(configs[0].Configuration), "${workspaceFolder}") {
		t.Fatal("workspace substitution incomplete")
	}
}
