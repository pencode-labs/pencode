package extensionhost

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdatePublishRestoresPreviousPackageOnRenameFailure(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "test")
	if err := os.MkdirAll(filepath.Join(target, "resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"id":"test","version":"1.0.0","entry":"bin/test","disabled":true}`)
	if err := os.WriteFile(filepath.Join(target, "resources/extension.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	upload, err := Begin(directory, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer upload.Close()
	// Force failure after the installed package has been moved aside.
	upload.sealed = true
	upload.root.Close()
	upload.root = nil
	if err := os.RemoveAll(upload.Directory); err != nil {
		t.Fatal(err)
	}
	if _, err := upload.Publish(directory, "test"); err == nil {
		t.Fatal("publish unexpectedly succeeded")
	}
	data, err := os.ReadFile(filepath.Join(target, "resources/extension.json"))
	if err != nil || string(data) != string(manifest) {
		t.Fatal("previous package was not restored", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "test" {
		t.Fatal(entries, err)
	}
}
