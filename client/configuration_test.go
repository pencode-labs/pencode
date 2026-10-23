package client

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pencode-labs/pencode/configuration"
	"github.com/pencode-labs/pencode/server"
)

func TestConfigurationUsesServerResourcesWithoutWorkspace(t *testing.T) {
	local, remote := t.TempDir(), filepath.Join(t.TempDir(), "resources")
	t.Setenv("XDG_CONFIG_HOME", local)
	t.Setenv("APPDATA", local)
	if err := os.MkdirAll(filepath.Join(remote, "themes", "includes"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"settings.json":             `{"fontSize":19,"theme":"themes/custom.jsonc"}`,
		"highlights.json":           `{"captures":{"comment":"remote.comment"}}`,
		"themes/custom.jsonc":       `{"include":"includes/base.json","name":"Remote",}`,
		"themes/includes/base.json": `{"colors":{"editor.foreground":"#abcdef"}}`,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(remote, filepath.FromSlash(name)), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a, b := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(t.Context(), b, server.Options{ConfigurationDirectory: remote})
	}()
	c, err := New(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); <-done })
	snapshot, err := c.ReadConfiguration(t.Context())
	if err != nil || snapshot.Directory != remote || len(snapshot.Files) != len(files) {
		t.Fatalf("snapshot = %+v, %v", snapshot, err)
	}
	for name, data := range files {
		if string(snapshot.Files[name]) != data {
			t.Fatalf("resource %s differs", name)
		}
	}
	want := `{"fontSize":23}`
	if err := c.SaveSettings(t.Context(), []byte(want)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(remote, "settings.json"))
	if err != nil || string(data) != want {
		t.Fatalf("saved = %s, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(local, "pencode", "resources", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("client configuration was touched: %v", err)
	}
	for _, invalid := range []string{`{`, `null`, `[]`, `"value"`, ""} {
		if err := c.SaveSettings(t.Context(), []byte(invalid)); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.SaveSettings(ctx, []byte(`{"fontSize":30}`)); err == nil {
		t.Fatal("canceled save succeeded")
	}
	data, _ = os.ReadFile(filepath.Join(remote, "settings.json"))
	if string(data) != want {
		t.Fatal("failed saves changed existing settings")
	}
	// The normal protocol chunking also applies to configuration resources.
	large := `{"padding":"` + strings.Repeat("中", 30000) + `"}`
	if err := c.SaveSettings(t.Context(), []byte(large)); err != nil {
		t.Fatal(err)
	}
	snapshot, err = c.ReadConfiguration(t.Context())
	if err != nil || string(snapshot.Files["settings.json"]) != large {
		t.Fatalf("chunked settings failed: %v", err)
	}
}

func TestConfigurationMissingFilesAndBudget(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "resources")
	snapshot, err := configuration.Read(t.Context(), directory)
	if err != nil || len(snapshot.Files) != 0 {
		t.Fatalf("missing configuration = %+v, %v", snapshot, err)
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(directory, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(configuration.MaxBytes + 1); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, err := configuration.Read(t.Context(), directory); err == nil {
		t.Fatal("oversized configuration accepted")
	}
}
