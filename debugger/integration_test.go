package debugger_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"

	"testing"
	"time"

	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/dap"
	"github.com/pencode-labs/pencode/debugger"
	"github.com/pencode-labs/pencode/output"

	"github.com/pencode-labs/pencode/server"
)

// Runs the real adapter through the production CS boundary. Set
// PENCODE_TEST_DLV when Delve is not on PATH.
func TestDelveBreakpointStepVariablesAndDisconnect(t *testing.T) {
	dlv := os.Getenv("PENCODE_TEST_DLV")
	if dlv == "" {
		var err error
		dlv, err = exec.LookPath("dlv")
		if err != nil {
			t.Skip("Delve is not installed")
		}
	}
	extensions := t.TempDir()
	root := t.TempDir()
	filename := filepath.Join(root, "main.go")
	source := "package main\nimport \"fmt\"\nfunc main() {\n value := 40; values := []int{40, 42}; _ = values\n value += 2\n fmt.Println(value)\n}\n"
	if err := os.WriteFile(filename, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module debugfixture\ngo 1.27.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		_ = server.Serve(context.Background(), b, server.Options{ExtensionDirectory: extensions})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c, err := client.New(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(); <-serverDone }()
	w, err := c.OpenWorkspace(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"mode": "debug", "program": root, "cwd": root})
	var store output.Store
	channel := store.Channel("debug")
	open := func(ctx context.Context) (io.ReadWriteCloser, error) {
		p, err := c.OpenDebugAdapter(ctx, w.Path, "Delve", dlv, "", nil)
		if err == nil {
			go io.Copy(channel, p.Stderr())
		}
		return p, err
	}
	s := debugger.Start(ctx, debugger.Config{Adapter: "Delve", Request: "launch", Configuration: config}, map[string][]dap.SourceBreakpoint{filename: {{Line: 5}}}, open, nil, channel, nil)
	defer s.Close()
	wait := func(predicate func(debugger.Snapshot) bool) debugger.Snapshot {
		t.Helper()
		for {
			view := s.Snapshot()
			if view.State == debugger.Failed {
				text, _ := channel.Snapshot()
				t.Fatalf("debug failed: %s\n%s", view.Error, text)
			}
			if predicate(view) {
				return view
			}
			select {
			case <-ctx.Done():
				text, _ := channel.Snapshot()
				t.Fatalf("timeout: %+v\n%s", view, text)
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	view := wait(func(v debugger.Snapshot) bool {
		if v.State != debugger.Stopped || len(v.Frames) == 0 || len(v.Scopes) == 0 {
			return false
		}
		for _, vars := range v.Variables {
			for _, v := range vars {
				if v.Name == "value" && v.Value == "40" {
					return true
				}
			}
		}
		return false
	})
	if bps := view.Breakpoints[filename]; len(bps) != 1 || !bps[0].Verified {
		t.Fatalf("unverified breakpoints: %+v", bps)
	}
	if view.Frames[0].Line != 5 {
		t.Fatalf("stopped at %d", view.Frames[0].Line)
	}
	epoch := view.Epoch
	s.Control("next")
	view = wait(func(v debugger.Snapshot) bool {
		if v.Epoch <= epoch || v.State != debugger.Stopped || len(v.Frames) == 0 || v.Frames[0].Line != 6 {
			return false
		}
		for _, vars := range v.Variables {
			for _, v := range vars {
				if v.Name == "value" && v.Value == "42" {
					return true
				}
			}
		}
		return false
	})
	s.Evaluate("value + 1")
	wait(func(v debugger.Snapshot) bool {
		return len(v.Evaluations) == 1 && v.Evaluations[0].Value == "43"
	})
	s.Evaluate("values")
	view = wait(func(v debugger.Snapshot) bool { return len(v.Evaluations) == 2 })
	result := view.Evaluations[1]
	if result.Type != "[]int" || result.VariablesReference <= 0 {
		t.Fatalf("slice evaluation lost its type or children: %+v", result)
	}
	s.Expand(result.VariablesReference)
	view = wait(func(v debugger.Snapshot) bool { return len(v.Variables[result.VariablesReference]) == 2 })
	if children := view.Variables[result.VariablesReference]; children[0].Value != "40" || children[1].Value != "42" {
		t.Fatalf("slice evaluation children: %+v", children)
	}
	s.ClearConsole()
	if len(s.Snapshot().Evaluations) != 0 {
		t.Fatal("clearing the console retained evaluations")
	}
	s.SetBreakpoints(filename, nil)
	wait(func(v debugger.Snapshot) bool { return len(v.Breakpoints[filename]) == 0 })
	s.Control("continue")
	select {
	case <-s.Done():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if view := s.Snapshot(); view.State != debugger.Ended || view.Error != "" {
		t.Fatalf("bad exit: %+v", view)
	}
}
