package workbench

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/diiyw/onix/input"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/remote"
)

func waitProjectPicker(t *testing.T, model *workspaceModel) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for model.session.connection.picker.busy {
		model.pollConnection()
		if time.Now().After(deadline) {
			t.Fatal("project picker did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSSHProjectPickerRequiresSelectionAndKeepsCurrentWorkspace(t *testing.T) {
	model, project := resourceTestModel(t)
	resourceTestWrite(t, project, "main.go", "package main\n")
	resourceTestWrite(t, project, "nested/main.go", "package nested\n")
	openProjectFile(project, "main.go")
	model.syncActiveProjectFields()
	model.updateActiveContent("package unsaved\n")
	file, document := model.activeFile(), model.activeFile().document
	s := NewSession(project.backend)
	model.session = s
	s.ConfigureConnection(nil, connection.Profile{})
	t.Cleanup(s.Close)
	profile := connection.Profile{SSH: remote.Target{Host: "remote"}, Directory: project.Path}
	s.connection.workspaceProfile = profile
	s.connection.links[profile.SSH] = &pencodeSSHLink{backend: project.backend, profile: profile}
	model.chooseProject()
	waitProjectPicker(t, model)
	p := &s.connection.picker
	if !p.open || !s.connection.open || len(model.projects) != 1 || model.fileIO != nil {
		t.Fatal("opening the picker loaded a project")
	}
	if p.path != filepath.ToSlash(project.Path) || len(p.names) != 1 || p.names[0] != "nested" {
		t.Fatalf("unexpected project choices: %+v", p)
	}
	if model.activeFile() != file || file.document != document || file.Content != "package unsaved\n" || !file.Dirty {
		t.Fatal("browsing changed the current buffer")
	}
	model.browseProjectDirectory(p.path + "/nested")
	waitProjectPicker(t, model)
	model.connectionEvent("connection-project-up", "")
	waitProjectPicker(t, model)
	if p.path != filepath.ToSlash(project.Path) {
		t.Fatal("parent navigation did not return to the project")
	}
	model.connectionEvent("connection-project-cancel", "")
	if p.open || s.connection.open || len(model.projects) != 1 || model.backend != project.backend {
		t.Fatal("cancel changed the workspace")
	}
	if err := savePencodeFile(project.backend, project.Path, file); err != nil {
		t.Fatalf("browsing retired the open document: %v", err)
	}
	model.openSSHProject(profile)
	waitProjectPicker(t, model)
	model.browseProjectDirectory(p.path + "/nested")
	waitProjectPicker(t, model)
	model.connectionEvent("connection-project-open", "")
	if p.open || s.connection.open {
		t.Fatal("confirm did not close the picker")
	}
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	if len(model.projects) != 2 || model.activeProjectState().Path != filepath.Join(project.Path, "nested") {
		t.Fatal("confirmation did not open the selected directory")
	}
	t.Cleanup(model.activeProjectState().stopScan)
	if allocations := testing.AllocsPerRun(100, func() { model.pollConnection() }); allocations != 0 {
		t.Fatalf("idle project picker allocated %g times", allocations)
	}
}

func TestSSHProjectPickerDismissRestoresConnectionVisibility(t *testing.T) {
	model, project := resourceTestModel(t)
	s := NewSession(project.backend)
	model.session = s
	s.ConfigureConnection(nil, connection.Profile{})
	t.Cleanup(s.Close)
	profile := connection.Profile{SSH: remote.Target{Host: "remote"}, Directory: project.Path}
	s.connection.links[profile.SSH] = &pencodeSSHLink{backend: project.backend, profile: profile}
	for _, visible := range []bool{false, true} {
		for _, action := range []string{"connection-project-cancel", "connection-project-close", "escape"} {
			s.connection.open = visible
			model.openSSHProject(profile)
			if action == "escape" {
				state := input.NewState()
				state.PressKey(input.KeyEscape)
				if !model.handleShortcutKeys(nil, state, "linux") {
					t.Fatal("Escape did not dismiss the project picker")
				}
			} else {
				model.connectionEvent(action, "")
			}
			if s.connection.picker.open || s.connection.picker.busy || s.connection.open != visible {
				t.Fatalf("%s: connection visibility = %v, want %v; picker open = %v, busy = %v", action, s.connection.open, visible, s.connection.picker.open, s.connection.picker.busy)
			}
			if !model.connectionConnected(profile) {
				t.Fatalf("%s disconnected SSH", action)
			}
		}
	}
}

func TestSSHProjectPickerCancelAndFailureKeepLocalBackend(t *testing.T) {
	model, project := resourceTestModel(t)
	s := NewSession(project.backend)
	model.session = s
	s.ConfigureConnection(nil, connection.Profile{})
	t.Cleanup(s.Close)
	backend := newPencodeBackend(t)
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "app"), 0700); err != nil {
		t.Fatal(err)
	}
	profile := connection.Profile{SSH: remote.Target{Host: "remote"}, Directory: directory}
	s.connection.links[profile.SSH] = &pencodeSSHLink{backend: backend, profile: profile}
	model.openSSHProject(profile)
	// Cancellation also drains an in-flight request before the picker is reused.
	model.connectionEvent("connection-project-cancel", "")
	model.openSSHProject(profile)
	waitProjectPicker(t, model)
	p := &s.connection.picker
	model.connectionEvent("connection-project-path", directory+"/missing")
	model.connectionEvent("connection-project-go", "")
	waitProjectPicker(t, model)
	model.connectionEvent("connection-project-open", "")
	if !p.failed || !p.open || s.backend != project.backend || model.backend != project.backend || model.fileIO != nil {
		t.Fatal("failed selection changed the local workspace")
	}
	model.connectionEvent("connection-project-path", directory)
	model.connectionEvent("connection-project-go", "")
	waitProjectPicker(t, model)
	model.connectionEvent("connection-project-open", "")
	if !p.failed || !p.open || s.backend != project.backend || len(model.projects) != 1 {
		t.Fatal("cross-device selection replaced a live project")
	}
	model.connectionEvent("connection-close", "")
	if p.open || p.busy {
		t.Fatal("closing connections left the picker active")
	}
}

func TestSSHProjectPickerSwitchesLauncherBackendOnlyOnConfirm(t *testing.T) {
	local, backend := newPencodeBackend(t), newPencodeBackend(t)
	s := NewSession(local)
	s.ConfigureConnection(nil, connection.Profile{})
	t.Cleanup(s.Close)
	model := &workspaceModel{session: s, backend: local}
	t.Cleanup(model.stopFileIO)
	t.Cleanup(func() { s.market.stop(); s.extensions.Close() })
	directory := t.TempDir()
	t.Setenv("HOME", directory)
	t.Setenv("USERPROFILE", directory)
	if err := os.Mkdir(filepath.Join(directory, "app"), 0700); err != nil {
		t.Fatal(err)
	}
	profile := connection.Profile{SSH: remote.Target{Host: "remote"}}
	s.connection.links[profile.SSH] = &pencodeSSHLink{backend: backend, profile: profile}
	model.openSSHProject(profile)
	waitProjectPicker(t, model)
	p := &s.connection.picker
	if p.path != filepath.ToSlash(directory) || s.backend != local || model.backend != local || len(model.projects) != 0 {
		t.Fatal("home was opened as a project or browsing switched the launcher backend")
	}
	model.browseProjectDirectory(p.path + "/app")
	waitProjectPicker(t, model)
	model.connectionEvent("connection-project-open", "")
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	if s.backend != backend || model.backend != backend || len(model.projects) != 1 || model.activeProjectState().Path != filepath.Join(directory, "app") {
		t.Fatal("confirmation did not open the chosen project on its SSH backend")
	}
	t.Cleanup(model.activeProjectState().stopScan)
}
