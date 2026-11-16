package main

import (
	"slices"
	"testing"

	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/remote"
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
	if len(a.pending) != 2 || !slices.Equal(a.pending[0].paths, []string{"/projects/first", "/projects/second"}) || len(a.pending[1].paths) != 0 {
		t.Fatal("queued window requests shared caller-owned paths")
	}
	if len(a.windows) != 0 {
		t.Fatal("window creation ran inside the requesting frame")
	}
	profile := connection.Profile{SSH: remote.Target{Host: "dev"}, Directory: "/workspace/app"}
	if err := a.queueWindow(nil, profile); err != nil {
		t.Fatal(err)
	}
	if a.pending[2].profile != profile || len(a.pending[2].paths) != 0 {
		t.Fatal("remote window request lost its host or directory")
	}
}
