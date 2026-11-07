package extensionhost

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pencode-labs/pencode/peb"
)

func TestJavaScriptEntryTypes(t *testing.T) {
	for _, entry := range []string{"dist/main.js", "dist/main.mjs", "dist/main.cjs"} {
		if !(Manifest{Entry: entry}).JavaScript() {
			t.Errorf("not JavaScript: %s", entry)
		}
	}
	for _, entry := range []string{"bin/extension", "bin/extension.exe", "src/main.ts", "dist/file.js/extension"} {
		if (Manifest{Entry: entry}).JavaScript() {
			t.Errorf("not native: %s", entry)
		}
	}
}
func TestJavaScriptRuntimeUsesHostPATH(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("PATH", directory)
	if _, err := JavaScriptRuntime(); err == nil || !strings.Contains(err.Error(), "Node.js") {
		t.Fatalf("missing runtime: %v", err)
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	for _, name := range []string{"bun", "node"} {
		filename := filepath.Join(directory, name+suffix)
		if err := os.WriteFile(filename, []byte("fixture"), 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := JavaScriptRuntime()
		if err != nil || got != filename {
			t.Fatalf("runtime = %q, %v, want %q", got, err, filename)
		}
	}
}
func TestJavaScriptPeerPreservesArgumentsAndWorkingDirectory(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		if _, err := exec.LookPath("bun"); err != nil {
			t.Skip("Node.js or Bun required")
		}
	}
	directory := t.TempDir()
	script := `process.stdout.write(JSON.stringify({cwd:process.cwd(),args:process.argv.slice(2)}));`
	if err := os.WriteFile(filepath.Join(directory, "entry.mjs"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	peer, err := Open(root, directory, Manifest{Entry: "entry.mjs", Args: []string{"--argument", "你好 $(literal)"}}, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	output, err := io.ReadAll(peer)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), `"--argument","你好 $(literal)"`) {
		t.Fatalf("arguments: %s", output)
	}
	if err := peer.(interface{ Wait() error }).Wait(); err != nil {
		t.Fatal(err)
	}
}
func TestOfficeViewerRegistersWithGoHost(t *testing.T) {
	directory, err := filepath.Abs("../../extensions/office-viewer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "dist/office-viewer.mjs")); err != nil {
		t.Skip("build office-viewer first with npm ci && npm run build")
	}
	registration, err := Register(t.Context(), directory, "office-viewer", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(registration.Contributions.Actions) != 1 {
		t.Fatalf("actions: %+v", registration.Contributions.Actions)
	}
	action := registration.Contributions.Actions[0]
	if action.Location != peb.OpenWith || !action.Default || len(action.Selectors) != 6 {
		t.Fatalf("office registration: %+v", action)
	}
}
