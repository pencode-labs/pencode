package extensions

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/debugger"
	"github.com/pencode-labs/pencode/peb"
)

func TestLanguageServerValidation(t *testing.T) {
	valid := peb.LanguageServer{ID: "go", Label: "Go", LanguageID: "go", Command: "gopls", Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".go"}}, Configuration: `{"gopls":{}}`}
	servers, err := loadLanguageServers("test", []peb.LanguageServer{valid})
	if err != nil || len(servers) != 1 {
		t.Fatal(servers, err)
	}
	if servers[0].ID != "extensions/test/go" || !servers[0].Match("main.go") || servers[0].Match("main.go.bak") {
		t.Fatalf("%+v", servers[0])
	}
	for _, mutate := range []func(*peb.LanguageServer){
		func(s *peb.LanguageServer) { s.ID = "../bad" }, func(s *peb.LanguageServer) { s.Label = "" }, func(s *peb.LanguageServer) { s.LanguageID = "" }, func(s *peb.LanguageServer) { s.Command = "" }, func(s *peb.LanguageServer) { s.Args = []string{"bad\x00arg"} }, func(s *peb.LanguageServer) { s.Selectors = nil }, func(s *peb.LanguageServer) {
			s.Selectors = []peb.DocumentSelector{{Match: peb.FolderName, Pattern: "src"}}
		}, func(s *peb.LanguageServer) { s.Configuration = "{" }, func(s *peb.LanguageServer) { s.InitializationOptions = "not-json" },
	} {
		s := valid
		mutate(&s)
		if _, err := loadLanguageServers("test", []peb.LanguageServer{s}); err == nil {
			t.Fatalf("accepted invalid server: %+v", s)
		}
	}
	if _, err := loadLanguageServers("test", []peb.LanguageServer{valid, valid}); err == nil {
		t.Fatal("accepted duplicate servers")
	}
}
func TestGoLanguageExtensionRegistration(t *testing.T) {
	root := filepath.Join(t.TempDir(), "go")
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(root, "resources"), os.DirFS("go/resources")); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(root, "bin", "go")
	if runtime.GOOS == "windows" {
		t.Skip("manifest example uses a Unix executable name")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-p", "1", "-o", entry, ".")
	cmd.Dir = "go"
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	// Registration must succeed without installed tools or running go env.
	t.Setenv("GOPATH", t.TempDir())
	bundle, err := Load(ctx, extensionBackend(t, filepath.Dir(root)), "go")
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.LanguageServers) != 1 || bundle.LanguageServers[0].remote.Info.Path != root || bundle.LanguageServers[0].Command != "bin/go" || bundle.Actions == nil {
		t.Fatalf("%+v", bundle)
	}
	server := &bundle.LanguageServers[0]
	if len(server.Args) != 2 || server.Args[1] != "gopls" || server.DocumentLanguage("go.mod") != "go.mod" || server.DocumentLanguage("go.work") != "go.work" {
		t.Fatalf("invalid Go launcher or document types: %+v", server)
	}
	adapters := bundle.DebugAdapters
	if len(adapters) != 1 || adapters[0].ID != "Delve" || adapters[0].Command != "bin/go" || adapters[0].Args[1] != "dlv" {
		t.Fatalf("missing Delve launcher: %+v", adapters)
	}
	templates, err := debugger.ParseConfigs([]byte(adapters[0].Templates), "${workspaceFolder}")
	if err != nil || len(templates) != 2 || templates[0].Adapter != "Delve" {
		t.Fatalf("missing imported Go templates: %+v, %v", templates, err)
	}
}
