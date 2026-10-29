package debugger_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/dap"
	"github.com/pencode-labs/pencode/debugger"
	"github.com/pencode-labs/pencode/output"
	"github.com/pencode-labs/pencode/server"
)

// Opt in with an installed PHP extension directory and, when PHP does not load
// Xdebug by default, PENCODE_TEST_PHP_XDEBUG pointing to its runtime module.
func TestPHPWithXdebugExtension(t *testing.T) {
	directory := os.Getenv("PENCODE_TEST_PHP_EXTENSIONS")
	if directory == "" {
		t.Skip("set PENCODE_TEST_PHP_EXTENSIONS to test the real PHP adapter")
	}
	php, err := exec.LookPath("php")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	filename := filepath.Join(root, "main.php")
	if err := os.WriteFile(filename, []byte("<?php\n$value = 40;\n$value += 2;\necho $value, PHP_EOL;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(context.Background(), b, server.Options{ExtensionDirectory: directory})
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	c, err := client.New(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(); <-done }()
	extension, err := c.LoadExtension(ctx, "php")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := c.OpenWorkspace(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	configs, err := debugger.ParseConfigs([]byte(extension.Contributions.DebugAdapters[0].Templates), root)
	if err != nil {
		t.Fatal(err)
	}
	var config debugger.Config
	for _, candidate := range configs {
		if strings.Contains(string(candidate.Configuration), "${file}") {
			config = candidate
			break
		}
	}
	config, err = config.ResolveFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var launch map[string]any
	if err := json.Unmarshal(config.Configuration, &launch); err != nil {
		t.Fatal("missing current file template", err)
	}
	launch["runtimeExecutable"], launch["stopOnEntry"] = php, false
	if module := os.Getenv("PENCODE_TEST_PHP_XDEBUG"); module != "" {
		args := launch["runtimeArgs"].([]any)
		launch["runtimeArgs"] = append([]any{"-dzend_extension=" + module}, args...)
	}
	config.Configuration, err = json.Marshal(launch)
	if err != nil {
		t.Fatal(err)
	}
	var logs output.Store
	channel := logs.Channel("PHP")
	open := func(ctx context.Context) (io.ReadWriteCloser, error) {
		stream, err := c.OpenDebugAdapter(ctx, workspace.Path, config.Adapter, "", "", nil)
		if err == nil {
			go func() { _, _ = io.Copy(channel, stream.Stderr()) }()
		}
		return stream, err
	}
	session := debugger.Start(ctx, config, map[string][]dap.SourceBreakpoint{filename: {{Line: 3}}}, open, nil, channel, nil)
	defer session.Close()
	wait := func(ready func(debugger.Snapshot) bool) debugger.Snapshot {
		t.Helper()
		for {
			view := session.Snapshot()
			if view.State == debugger.Failed || view.State == debugger.Ended || ctx.Err() != nil {
				text, _ := channel.Snapshot()
				t.Fatalf("PHP debug stopped before expected state: %+v\n%s", view, text)
			}
			if ready(view) {
				return view
			}
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	view := wait(func(v debugger.Snapshot) bool {
		return v.State == debugger.Stopped && len(v.Frames) > 0 && len(v.Scopes) > 0
	})
	if view.Frames[0].Line != 3 {
		t.Fatalf("breakpoint stopped at line %d", view.Frames[0].Line)
	}
	session.Evaluate("$value")
	wait(func(v debugger.Snapshot) bool { return len(v.Evaluations) == 1 && v.Evaluations[0].Value == "40" })
	session.Control("next")
	wait(func(v debugger.Snapshot) bool {
		return v.State == debugger.Stopped && v.Epoch > view.Epoch && len(v.Frames) > 0 && v.Frames[0].Line == 4
	})
	session.Evaluate("$value")
	wait(func(v debugger.Snapshot) bool { return len(v.Evaluations) == 2 && v.Evaluations[1].Value == "42" })
	session.Control("continue")
	select {
	case <-session.Done():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	text, _ := channel.Snapshot()
	if session.Snapshot().State != debugger.Ended || !strings.Contains(text, "42") {
		t.Fatalf("PHP did not finish: %+v\n%s", session.Snapshot(), text)
	}
}
