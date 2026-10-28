package workbench

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
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

func TestProjectPickerFiltersAndSynchronizesEditedInput(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	session.ConfigureConnection(nil, connection.Profile{})
	directory := filepath.ToSlash(t.TempDir())
	for _, name := range []string{"app/nested", "assets", "other"} {
		if err := os.MkdirAll(filepath.Join(directory, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	profile := connection.Profile{SSH: remote.Target{Host: "remote"}, Directory: directory}
	session.connection.links[profile.Endpoint()] = &pencodeRemoteLink{backend: model.backend, profile: profile}
	model.openRemoteProject(profile)
	waitProjectPicker(t, model)
	step := func() {
		t.Helper()
		markPencodeRootDirtyByModel(model.fontResources, model)
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
	}
	step()
	field, ok := model.fontResources.HTMLControl(model.surface, "connection-project-path")
	if !ok {
		t.Fatal("missing path input")
	}
	typePath := func(value string) {
		t.Helper()
		model.fontResources.SetControlValue(field, value)
		ecs.WriteMessage(app.World, htmlui.Event{Entity: model.surface, Target: field, Kind: htmlui.EventInput, Value: value, Bubbles: true})
		step()
	}
	checkChoices := func(want ...string) {
		t.Helper()
		var names []string
		for _, access := range module.ContentQuery[htmlui.Component[pencodeProjectDirectoryProps, struct{}]](model.fontResources) {
			names = append(names, access.Get().Props.Name)
		}
		slices.Sort(names)
		if !slices.Equal(names, want) {
			t.Fatalf("rendered directories = %v, want %v", names, want)
		}
	}
	checkInput := func(want string) {
		t.Helper()
		control, _ := module.ContentGet[htmlui.Control](model.fontResources, field)
		text, _ := module.ContentGet[ui.TextField](model.fontResources, field)
		if control.Value != want || text.Value != want {
			t.Fatalf("displayed path = %q / %q, want %q", control.Value, text.Value, want)
		}
	}
	checkChoices("app", "assets", "other")
	typePath(directory + "/a")
	checkChoices("app", "assets")
	typePath(directory + "/missing")
	checkChoices()
	if session.connection.picker.selected() {
		t.Fatal("incomplete path selected the parent directory")
	}
	typePath(directory + "/ap")
	checkChoices("app")
	checkInput(directory + "/ap")
	for entity, access := range module.ContentQuery[htmlui.Component[pencodeProjectDirectoryProps, struct{}]](model.fontResources) {
		component := access.Get()
		component.Definition.Behavior.HandleEvent(&htmlui.ComponentEventContext[pencodeProjectDirectoryProps, struct{}]{World: app.World, Component: entity, Props: &component.Props, Ref: "directory", Event: htmlui.Event{Kind: htmlui.EventClick}})
		break
	}
	checkInput(directory + "/app")
	waitProjectPicker(t, model)
	step()
	checkChoices("nested")
	checkInput(directory + "/app")
	model.connectionEvent("connection-project-up", "")
	waitProjectPicker(t, model)
	step()
	checkChoices("app", "assets", "other")
	checkInput(directory)
	typePath("")
	checkChoices()
	if session.connection.picker.selected() {
		t.Fatal("empty path selected a directory")
	}
}

func TestProjectPickerLatestInputWinsAndCachedFilteringDoesNotAllocate(t *testing.T) {
	model, project := resourceTestModel(t)
	s := NewSession(project.backend)
	model.session = s
	s.ConfigureConnection(nil, connection.Profile{})
	t.Cleanup(s.Close)
	directory := filepath.ToSlash(t.TempDir())
	t.Setenv("HOME", directory)
	t.Setenv("USERPROFILE", directory)
	for _, name := range []string{"app/nested", "assets"} {
		if err := os.MkdirAll(filepath.Join(directory, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	profile := connection.Profile{SSH: remote.Target{Host: "remote"}, Directory: directory}
	s.connection.links[profile.Endpoint()] = &pencodeRemoteLink{backend: project.backend, profile: profile}
	model.openRemoteProject(profile)
	// Leave the initial result unpolled while more input arrives.
	model.connectionEvent("connection-project-path", directory+"/missing/n")
	model.connectionEvent("connection-project-path", directory+"/app/ne")
	waitProjectPicker(t, model)
	p := &s.connection.picker
	if p.path != directory+"/app" || p.input != directory+"/app/ne" || !p.matches("nested") || p.failed {
		t.Fatalf("latest input was lost: %+v", p)
	}
	// A stale failure must also leave the latest query intact.
	model.connectionEvent("connection-project-path", directory+"/missing/n")
	model.connectionEvent("connection-project-path", directory+"/app/")
	waitProjectPicker(t, model)
	if p.path != directory+"/app" || p.input != directory+"/app/" || p.failed || !p.selected() {
		t.Fatalf("stale failure changed the selected directory: %+v", p)
	}
	model.connectionEvent("connection-project-path", "~/a")
	waitProjectPicker(t, model)
	if p.path != directory || p.input != "~/a" || !p.matches("app") || !p.matches("assets") {
		t.Fatalf("home query did not filter: %+v", p)
	}
	queries := [...]string{directory + "/a", directory + "/ap", directory + "/missing", directory + "/"}
	if allocations := testing.AllocsPerRun(100, func() {
		for _, query := range queries {
			model.connectionEvent("connection-project-path", query)
			if p.busy {
				t.Fatal("filtering reread the current directory")
			}
		}
	}); allocations != 0 {
		t.Fatalf("cached directory filtering allocated %g times", allocations)
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
	s.connection.links[profile.Endpoint()] = &pencodeRemoteLink{backend: project.backend, profile: profile}
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
	model.openRemoteProject(profile)
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
	s.connection.links[profile.Endpoint()] = &pencodeRemoteLink{backend: project.backend, profile: profile}
	for _, visible := range []bool{false, true} {
		for _, action := range []string{"connection-project-cancel", "connection-project-close", "escape"} {
			s.connection.open = visible
			model.openRemoteProject(profile)
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
	s.connection.links[profile.Endpoint()] = &pencodeRemoteLink{backend: backend, profile: profile}
	model.openRemoteProject(profile)
	// Cancellation also drains an in-flight request before the picker is reused.
	model.connectionEvent("connection-project-cancel", "")
	model.openRemoteProject(profile)
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
	s.connection.links[profile.Endpoint()] = &pencodeRemoteLink{backend: backend, profile: profile}
	model.openRemoteProject(profile)
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
