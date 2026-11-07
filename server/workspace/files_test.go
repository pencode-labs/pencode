package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pencode-labs/pencode/protocol"
)

func importFixture(t *testing.T) (*Store, protocol.Workspace, string) {
	t.Helper()
	target, source := t.TempDir(), t.TempDir()
	s := new(Store)
	t.Cleanup(s.Close)
	p, err := s.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	return s, p, source
}

func TestImportFilesConflictAndOverwrite(t *testing.T) {
	s, p, source := importFixture(t)
	name := "资料 中文.bin"
	original := filepath.Join(source, name)
	data := []byte{0, 1, 2, 255, 128}
	if err := os.WriteFile(original, data, 0644); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Import(p.ID, original, "", false); err != nil || got != name {
		t.Fatalf("import: %q %v", got, err)
	}
	write(t, p, name, "keep")
	_, err := s.Import(p.ID, original, ".", false)
	var conflict *protocol.Error
	if !errors.As(err, &conflict) || conflict.Code != protocol.Conflict {
		t.Fatalf("missing typed conflict: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(p.Path, name)); string(got) != "keep" {
		t.Fatal("conflict changed destination")
	}
	if _, err := s.Import(p.ID, original, ".", true); err != nil {
		t.Fatal(err)
	}
	for _, filename := range []string{original, filepath.Join(p.Path, name)} {
		if got, err := os.ReadFile(filename); err != nil || string(got) != string(data) {
			t.Fatalf("binary bytes changed: %q %v", got, err)
		}
	}
	entries, err := os.ReadDir(p.Path)
	if err != nil || len(entries) != 1 {
		t.Fatalf("left temporary copies: %v %v", entries, err)
	}
}

func TestImportDirectoryMergesAndProtectsSource(t *testing.T) {
	s, p, source := importFixture(t)
	folder := filepath.Join(source, "folder")
	if err := os.MkdirAll(filepath.Join(folder, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "nested", "new.txt"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Import(p.ID, folder, "", false); err != nil {
		t.Fatal(err)
	}
	write(t, p, "folder/keep.txt", "keep")
	write(t, p, "folder/nested/new.txt", "old")
	if _, err := s.Import(p.ID, folder, "", false); err == nil {
		t.Fatal("merged an existing folder without confirmation")
	}
	if _, err := s.Import(p.ID, folder, "", true); err != nil {
		t.Fatal(err)
	}
	for filename, want := range map[string]string{"folder/keep.txt": "keep", "folder/nested/new.txt": "new"} {
		if got, err := os.ReadFile(filepath.Join(p.Path, filename)); err != nil || string(got) != want {
			t.Fatalf("merge %s: %q %v", filename, got, err)
		}
	}
	local := filepath.Join(p.Path, "folder")
	for _, directory := range []string{"", "folder", "folder/nested"} {
		if _, err := s.Import(p.ID, local, directory, true); err == nil {
			t.Fatalf("accepted self/descendant import into %q", directory)
		}
	}
	if _, err := s.Import(p.ID, filepath.Join(p.Path, "folder/keep.txt"), "folder", true); err == nil {
		t.Fatal("overwrote a file with itself")
	}
	if _, err := s.Import(p.ID, folder, "../escape", true); err == nil {
		t.Fatal("escaped workspace")
	}
}

func TestImportReplacesDifferentResourceTypes(t *testing.T) {
	s, p, source := importFixture(t)
	filename := filepath.Join(source, "item")
	if err := os.WriteFile(filename, []byte("file"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(p.Path, "item"), 0755); err != nil {
		t.Fatal(err)
	}
	write(t, p, "item/old", "old")
	if _, err := s.Import(p.ID, filename, "", true); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(p.Path, "item")); err != nil || string(got) != "file" {
		t.Fatalf("replace directory: %q %v", got, err)
	}
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filename, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Import(p.ID, filename, "", true); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(p.Path, "item")); err != nil || !info.IsDir() {
		t.Fatalf("replace file: %v %v", info, err)
	}
}
