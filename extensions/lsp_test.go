package extensions

import (
	"github.com/pencode-labs/pencode/peb"
	"testing"
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
