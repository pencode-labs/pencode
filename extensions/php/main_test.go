package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pencode-labs/pencode/peb"
)

func cleanEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"LANGUAGE_SERVER", "SERVER_COMMAND", "SERVER_ARGS", "CONFIGURATION", "INITIALIZATION_OPTIONS", "XDEBUG_PATH"} {
		t.Setenv("PENCODE_PHP_"+key, "")
	}
}

func TestRegistrationAndDocumentLanguages(t *testing.T) {
	cleanEnvironment(t)
	directory := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("PENCODE_PHP_TOOLS_DIR", directory)
	for _, server := range []string{"", "intelephense", "phptools", "phpactor", "phpantom"} {
		t.Setenv("PENCODE_PHP_LANGUAGE_SERVER", server)
		contributions, err := phpContributions("php.exe")
		if err != nil {
			t.Fatal(err)
		}
		var input, output bytes.Buffer
		writer := peb.Writer{Target: &input}
		hello, _ := peb.EncodeHello(nil, peb.Hello{Host: "test", Capabilities: peb.Capabilities})
		if err := writer.Write(peb.Initialize, 1, hello); err != nil {
			t.Fatal(err)
		}
		if err := writer.Write(peb.Shutdown, 0, nil); err != nil {
			t.Fatal(err)
		}
		if err := peb.ServeExtension(&input, &output, contributions, peb.Handlers{}); err != nil {
			t.Fatal(err)
		}
		reader := peb.Reader{Source: &output}
		frame, err := reader.Read()
		if err != nil || frame.Kind != peb.Register || frame.ID != 1 {
			t.Fatal(frame, err)
		}
		decoded, err := peb.DecodeContributions(frame.Payload)
		if err != nil || len(decoded.LanguageServers) != 1 || len(decoded.DebugAdapters) != 1 || decoded.ActionService {
			t.Fatal(decoded, err)
		}
		ls := decoded.LanguageServers[0]
		if server == "" {
			server = "intelephense"
		}
		if ls.ID != server || ls.Command != "bin/php.exe" || strings.Join(ls.Args, " ") != "--tool "+server {
			t.Fatal(ls)
		}
		for file, want := range map[string]string{"index.php": "php", "test.phpt": "php", "view.phtml": "php", "view.blade.php": "blade", "index.js": ""} {
			got := ""
			for _, selector := range ls.Selectors {
				if strings.HasSuffix(file, selector.Pattern) {
					got = selector.LanguageID
					if got == "" {
						got = ls.LanguageID
					}
					break
				}
			}
			if got != want {
				t.Errorf("%s: got %s, want %s", file, got, want)
			}
		}
		adapter := decoded.DebugAdapters[0]
		if adapter.ID != "Xdebug" || adapter.Command != ls.Command || strings.Join(adapter.Args, " ") != "--tool xdebug" {
			t.Fatal(adapter)
		}
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("registration touched the tool cache", err)
	}
}

func TestServerOverridesAndConfiguration(t *testing.T) {
	cleanEnvironment(t)
	t.Setenv("PENCODE_PHP_LANGUAGE_SERVER", "phpantom")
	t.Setenv("PENCODE_PHP_SERVER_COMMAND", "/custom/phpantom_lsp")
	t.Setenv("PENCODE_PHP_SERVER_ARGS", `["--flag", "argument with spaces"]`)
	t.Setenv("PENCODE_PHP_CONFIGURATION", `{"phpantom":{"enabled":true}}`)
	t.Setenv("PENCODE_PHP_INITIALIZATION_OPTIONS", `{"index":true}`)
	c, err := phpContributions("php")
	if err != nil {
		t.Fatal(err)
	}
	s := c.LanguageServers[0]
	if s.Command != "/custom/phpantom_lsp" || len(s.Args) != 2 || s.Args[1] != "argument with spaces" || !json.Valid([]byte(s.Configuration)) || !json.Valid([]byte(s.InitializationOptions)) {
		t.Fatal(s)
	}
	t.Setenv("PENCODE_PHP_SERVER_COMMAND", "")
	c, err = phpContributions("php")
	if err != nil || strings.Join(c.LanguageServers[0].Args, "|") != "--tool|phpantom|--flag|argument with spaces" {
		t.Fatal(c, err)
	}
	for key, value := range map[string]string{"LANGUAGE_SERVER": "missing", "SERVER_ARGS": "not json", "CONFIGURATION": "{", "INITIALIZATION_OPTIONS": "{"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv("PENCODE_PHP_"+key, value)
			if _, err := phpContributions("php"); err == nil {
				t.Fatal("accepted invalid", key)
			}
		})
	}
}
