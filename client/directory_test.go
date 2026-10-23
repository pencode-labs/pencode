package client

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestBrowseDirectoryPreservesWorkspaceAndExpandsServerHome(t *testing.T) {
	c, w := peer(t, nil)
	t.Setenv("HOME", w.Path)
	t.Setenv("USERPROFILE", w.Path)
	for _, name := range []string{"project", ".hidden"} {
		if err := os.Mkdir(filepath.Join(w.Path, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(w.Path, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	document, _, err := c.OpenDocument(t.Context(), w.ID, "main.go", true)
	if err != nil {
		t.Fatal(err)
	}
	defer document.Release()
	want := []string{".hidden", "project"}
	if runtime.GOOS != "windows" {
		if err := os.Symlink("project", filepath.Join(w.Path, "linked")); err != nil {
			t.Fatal(err)
		}
		want = []string{".hidden", "linked", "project"}
	}
	current, parent, names, err := c.BrowseDirectory(t.Context(), "~")
	if err != nil || current != filepath.ToSlash(w.Path) || parent != filepath.ToSlash(filepath.Dir(w.Path)) || !reflect.DeepEqual(names, want) {
		t.Fatalf("browse home = %q, %q, %v, %v", current, parent, names, err)
	}
	current, _, names, err = c.BrowseDirectory(t.Context(), "~/project")
	if err != nil || current != filepath.ToSlash(filepath.Join(w.Path, "project")) || len(names) != 0 {
		t.Fatalf("browse child = %q, %v, %v", current, names, err)
	}
	if _, _, _, err := c.BrowseDirectory(t.Context(), filepath.Join(w.Path, "missing")); err == nil {
		t.Fatal("missing directory succeeded")
	}
	if _, _, _, err := c.BrowseDirectory(t.Context(), filepath.Join(w.Path, "main.go")); err == nil {
		t.Fatal("file accepted as directory")
	}
	// Browsing more than the workspace budget must leave all existing handles usable.
	for i := range 35 {
		directory := filepath.Join(w.Path, fmt.Sprint(i))
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := c.BrowseDirectory(t.Context(), directory); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := (Directories{Client: c, Workspace: w.ID, Context: t.Context()}).ReadDir("."); err != nil {
		t.Fatalf("browsing invalidated workspace: %v", err)
	}
	if err := document.Save(t.Context(), "package main\n"); err != nil {
		t.Fatalf("browsing invalidated document: %v", err)
	}
}
