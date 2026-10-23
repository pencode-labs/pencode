package extensions

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/diiyw/onix/projects/pencode/peb"
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
	root := filepath.Join(t.TempDir(), "go-lsp")
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(root, "resources"), os.DirFS("go-lsp/resources")); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(root, "bin", "go-lsp")
	if runtime.GOOS == "windows" {
		t.Skip("manifest example uses a Unix executable name")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-p", "1", "-o", entry, "./go-lsp")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	// Exercise the PATH fallback independently of a locally installed GOPATH/bin/gopls.
	t.Setenv("GOPATH", t.TempDir())
	bundle, err := Load(ctx, extensionBackend(t, filepath.Dir(root)), "go-lsp")
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.LanguageServers) != 1 || bundle.LanguageServers[0].remote.Info.Path != root || bundle.LanguageServers[0].Command != "gopls" {
		t.Fatalf("%+v", bundle)
	}
}
