package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/onix-engine/onix"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/remote"
	"github.com/pencode-labs/pencode/workbench"
)

func TestApplicationWindowQueueOwnsPaths(t *testing.T) {
	var a pencodeApplication
	paths := []string{"/projects/first", "/projects/second"}
	if err := a.queueWindow(paths, connection.Profile{}); err != nil {
		t.Fatal(err)
	}
	paths[0] = "changed"
	if err := a.queueWindow(nil, connection.Profile{}); err != nil {
		t.Fatal(err)
	}
	if len(a.pending) != 2 || !slices.Equal(a.pending[0].Paths, []string{"/projects/first", "/projects/second"}) || len(a.pending[1].Paths) != 0 {
		t.Fatal("queued window requests shared caller-owned paths")
	}
	if len(a.windows) != 0 {
		t.Fatal("window creation ran inside the requesting frame")
	}
	profile := connection.Profile{SSH: remote.Target{Host: "dev"}, Directory: "/workspace/app"}
	if err := a.queueWindow(nil, profile); err != nil {
		t.Fatal(err)
	}
	if a.pending[2].Profile != profile || len(a.pending[2].Paths) != 0 {
		t.Fatal("remote window request lost its host or directory")
	}
}

func TestQueuedWindowRoutesDirectoryAndFilesToExistingOwner(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "main.go")
	if err := os.WriteFile(file, []byte("package main"), 0600); err != nil {
		t.Fatal(err)
	}
	local, remoteWindow := new(workbench.Session), new(workbench.Session)
	local.OpenPaths(workbench.LocalOpenPath(root))
	profile := connection.Profile{SSH: remote.Target{Host: "dev"}, Directory: root}
	remoteWindow.ConfigureConnection(nil, profile)
	a := pencodeApplication{windows: []*pencodeEditorWindow{
		{app: onix.NewApp(), session: remoteWindow},
		{app: onix.NewApp(), session: local},
	}}
	if err := a.queueWindow([]string{root, file}, connection.Profile{}); err != nil {
		t.Fatal(err)
	}
	if err := a.queueWindow(nil, profile); err != nil {
		t.Fatal(err)
	}
	next := profile
	next.Directory = "/another"
	if err := a.queueWindow(nil, next); err != nil {
		t.Fatal(err)
	}
	if err := a.openPending(); err != nil {
		t.Fatal(err)
	}
	if !local.ActivateProject(connection.Profile{}, file) || !remoteWindow.ActivateProject(profile, next.Directory) || len(a.windows) != 2 {
		t.Fatal("queued open failed to use the window owning the path and host")
	}
}
