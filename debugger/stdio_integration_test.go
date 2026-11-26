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
	"github.com/pencode-labs/pencode/internal/languagetool"
	"github.com/pencode-labs/pencode/output"
	"github.com/pencode-labs/pencode/server"
)

func TestBuiltinStdioDebuggers(t *testing.T) {
	for _, fixture := range []struct {
		adapter, filename, source string
		line                      int
	}{
		{languagetool.Debugpy, "main.py", "value = 40\nvalue += 2\nprint(value)\n", 2},
		{languagetool.LLDB, "main.c", "int main(void) {\n int value = 40;\n value += 2;\n return value == 42 ? 0 : 1;\n}\n", 3},
		{languagetool.DartDAP, "main.dart", "void main() {\n  var value = 40;\n  value += 2;\n  print(value);\n}\n", 3},
		{languagetool.NetCoreDbg, "Program.cs", "int value = 40;\nvalue += 2;\nSystem.Console.WriteLine(value);\n", 2},
	} {
		t.Run(fixture.adapter, func(t *testing.T) {
			adapter := fixture.adapter
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			root := t.TempDir()
			a, b := net.Pipe()
			done := make(chan struct{})
			options := server.Options{ExtensionDirectory: t.TempDir(), ConfigurationDirectory: t.TempDir()}
			go func() {
				defer close(done)
				_ = server.Serve(ctx, b, options)
			}()
			c, err := client.New(ctx, a)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Close(); <-done }()
			workspace, err := c.OpenWorkspace(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			var action, id string
			if adapter == languagetool.Debugpy && os.Getenv("PENCODE_TEST_INSTALL_DEBUGPY") != "" {
				action, id = "install", adapter
			}
			tools, err := c.LanguageTools(ctx, action, id, nil)
			if err != nil {
				t.Fatal(err)
			}
			info := tools[adapter]
			if action == "install" && (!info.Managed || info.Command == "" || info.Entry == "") {
				t.Fatalf("managed adapter is unavailable: %+v", info)
			}
			if info.Command == "" {
				t.Skipf("%s is unavailable", adapter)
			}
			if adapter == languagetool.NetCoreDbg {
				if _, err := exec.LookPath("dotnet"); err != nil {
					t.Skip("dotnet SDK unavailable")
				}
				cmd := exec.CommandContext(ctx, "dotnet", "new", "console", "--no-restore", "--name", "Fixture", "--output", root)
				if logs, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("create .NET project: %v\n%s", err, logs)
				}
			}
			file := filepath.Join(root, fixture.filename)
			if err := os.WriteFile(file, []byte(fixture.source), 0o600); err != nil {
				t.Fatal(err)
			}
			configs, err := debugger.ParseConfigs([]byte(debugger.BuiltinTemplates), workspace.Path)
			if err != nil {
				t.Fatal(err)
			}
			var config debugger.Config
			for _, candidate := range configs {
				if candidate.Adapter == adapter {
					config = candidate
					break
				}
			}
			if config.Adapter == "" {
				t.Fatal("missing built-in template")
			}
			config, err = config.ResolveFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var program string
			switch adapter {
			case languagetool.LLDB:
				cc, err := exec.LookPath("clang")
				if err != nil {
					t.Skip("clang is unavailable")
				}
				program = filepath.Join(root, "fixture.exe")
				if logs, err := exec.CommandContext(ctx, cc, "-g", "-O0", file, "-o", program).CombinedOutput(); err != nil {
					t.Fatalf("compile fixture: %v\n%s", err, logs)
				}
			case languagetool.NetCoreDbg:
				out := filepath.Join(root, "out")
				cmd := exec.CommandContext(ctx, "dotnet", "build", filepath.Join(root, "Fixture.csproj"), "-o", out)
				if logs, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("build .NET fixture: %v\n%s", err, logs)
				}
				program = filepath.Join(out, "Fixture.dll")
			}
			if program != "" {
				var launch map[string]any
				if err := json.Unmarshal(config.Configuration, &launch); err != nil {
					t.Fatal(err)
				}
				launch["program"] = program
				config.Configuration, err = json.Marshal(launch)
				if err != nil {
					t.Fatal(err)
				}
			}
			var store output.Store
			channel := store.Channel(adapter)
			open := func(ctx context.Context) (io.ReadWriteCloser, error) {
				stream, err := c.OpenDebugAdapter(ctx, workspace.Path, config.AdapterCommand(""))
				if err == nil {
					go io.Copy(channel, stream.Stderr())
				}
				return stream, err
			}
			session := debugger.Start(ctx, config, map[string][]dap.SourceBreakpoint{file: {{Line: fixture.line}}}, open, nil, channel, nil)
			defer session.Close()
			tick := time.NewTicker(5 * time.Millisecond)
			defer tick.Stop()
			for {
				view := session.Snapshot()
				if view.State == debugger.Stopped && len(view.Frames) > 0 && len(view.Scopes) > 0 {
					if view.Frames[0].Line != fixture.line {
						t.Fatalf("stopped at line %d, want %d", view.Frames[0].Line, fixture.line)
					}
					break
				}
				if view.State == debugger.Failed || view.State == debugger.Ended || ctx.Err() != nil {
					logs, _ := channel.Snapshot()
					t.Fatalf("did not stop at breakpoint: %+v\n%s", view, logs)
				}
				<-tick.C
			}
			session.Control("continue")
			select {
			case <-session.Done():
				if view := session.Snapshot(); view.State != debugger.Ended {
					t.Fatalf("debugger failed to finish: %+v", view)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}
