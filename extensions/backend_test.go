package extensions

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/server"
)

func extensionBackend(t testing.TB, directory string) *client.Client {
	t.Helper()
	a, b := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(context.Background(), b, server.Options{ExtensionDirectory: directory})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := client.New(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); <-done })
	return c
}

func actionFixture(t testing.TB) (*client.Client, string) {
	t.Helper()
	directory := t.TempDir()
	installed := filepath.Join(directory, "test")
	if err := os.MkdirAll(filepath.Join(installed, "resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Base(executable)
	target := filepath.Join(installed, entry)
	if err := os.Link(executable, target); err != nil {
		input, err := os.Open(executable)
		if err != nil {
			t.Fatal(err)
		}
		defer input.Close()
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, 0o755)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(output, input)
		closeErr := output.Close()
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
	}
	data, err := json.Marshal(Manifest{ID: "test", Entry: entry, Args: []string{"-test.run=^TestActionHelperProcess$", "--", "peb-action-helper"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installed, "resources", "extension.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return extensionBackend(t, directory), directory
}
