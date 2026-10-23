// Go LSP demonstrates registering an installed language server with Pencode.
// The extension exits after registration; the host owns gopls and its documents.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pencode-labs/pencode/peb"
)

// goplsCommand resolves the gopls executable, preferring the $GOPATH/bin copies
// produced by `go install` before falling back to PATH.
func goplsCommand() string {
	for _, gopath := range gopaths() {
		for _, name := range []string{"gopls", "gopls.exe"} {
			path := filepath.Join(gopath, "bin", name)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path
			}
		}
	}
	return "gopls"
}

// gopaths returns the GOPATH entries, probing `go env` when the variable is unset.
func gopaths() []string {
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		env := exec.Command("go", "env", "GOPATH")
		hideWindow(env)
		if out, err := env.Output(); err == nil {
			gopath = strings.TrimSpace(string(out))
		}
	}
	var paths []string
	for _, path := range filepath.SplitList(gopath) {
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func main() {
	if err := peb.Serve(os.Stdin, os.Stdout, peb.Contributions{LanguageServers: []peb.LanguageServer{{
		ID: "gopls", Label: "Go (gopls)", LanguageID: "go", Command: goplsCommand(),
		Selectors:     []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".go"}},
		Configuration: `{"gopls":{"analyses":{"unusedparams":true},"staticcheck":true}}`,
	}}}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
