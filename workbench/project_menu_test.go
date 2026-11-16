package workbench

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/window"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/remote"
)

func TestProjectMenuInteraction(t *testing.T) {
	for _, screen := range []pencodeScreen{pencodeLauncher, pencodeWorkspace} {
		t.Run(fmt.Sprint(screen), func(t *testing.T) {
			app, m, _, state := languageInteractionFixture(t)
			m.languages = nil
			if screen == pencodeLauncher {
				m.screen = screen
				if err := app.Modules().UpdateFrame(1.0 / 60); err != nil {
					t.Fatal(err)
				}
				if err := app.Modules().Dispatch(); err != nil {
					t.Fatal(err)
				}
				languageInteractionStep(t, app)
				m = m.session.active.model
			}
			s := m.session
			s.ConfigureConnection(nil, connection.Profile{})
			t.Cleanup(s.Close)
			s.connection.profiles = nil
			markPencodeRootDirtyByModel(m.fontResources, m)
			languageInteractionStep(t, app)
			click := func(ref string) { editorContextClick(t, app, m, state, ref) }
			press := func(key input.KeyCode) {
				state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
				state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
				languageInteractionStep(t, app)
			}
			click("add-project")
			menu := editorContextBounds(t, m, "project-menu")
			anchor := editorContextBounds(t, m, "add-project")
			if !m.projectMenu.open || s.connection.picker.open || math.Abs(float64(menu.X-anchor.X)) > 1 || math.Abs(float64(menu.Y-anchor.Y-anchor.Height-4)) > 1 {
				t.Fatalf("menu was not anchored below plus: menu=%+v anchor=%+v", menu, anchor)
			}
			local, ok := m.fontResources.HTMLControl(m.surface, "project-menu-local")
			if !ok || m.fontResources.FocusedControl() != local {
				t.Fatal("menu did not focus the local action")
			}
			click("add-project")
			if m.projectMenu.open {
				t.Fatal("second plus click did not dismiss menu")
			}
			click("add-project")
			press(input.KeyEscape)
			plus, _ := m.fontResources.HTMLControl(m.surface, "add-project")
			if m.projectMenu.open || m.fontResources.FocusedControl() != plus {
				t.Fatalf("Escape did not close menu and restore focus: menu=%+v focus=%v plus=%v", m.projectMenu, m.fontResources.FocusedControl(), plus)
			}
			click("add-project")
			click("settings-button")
			if m.projectMenu.open || !m.settingsOpen {
				t.Fatal("outside click did not dismiss menu or reach settings")
			}
			click("settings-button")
			click("add-project")
			press(input.KeyDown)
			press(input.KeyEnter)
			if m.projectMenu.open || !s.connection.open || !s.connection.formOpen {
				t.Fatal("remote action did not offer adding the first host")
			}
			m.connectionEvent("connection-close", "")
			profile := connection.Profile{Name: "Development", SSH: remote.Target{Host: "dev"}, Directory: t.TempDir()}
			s.connection.profiles = []connection.Profile{profile}
			s.connection.links[profile.Endpoint()] = &pencodeRemoteLink{backend: m.backend, profile: profile}
			markPencodeRootDirtyByModel(m.fontResources, m)
			languageInteractionStep(t, app)
			click("add-project")
			editorContextBounds(t, m, "project-menu-host:"+profile.Address())
			press(input.KeyUp)
			if m.projectMenu.index != 2 {
				t.Fatal("up did not wrap to saved host")
			}
			press(input.KeyEnter)
			waitProjectPicker(t, m)
			markPencodeRootDirtyByModel(m.fontResources, m)
			languageInteractionStep(t, app)
			if m.projectMenu.open || !s.connection.picker.open || s.connection.picker.profile != profile {
				t.Fatal("selecting host did not open its directory dialog")
			}
			editorContextBounds(t, m, "connection-project-picker")
			press(input.KeyEscape)
			if s.connection.picker.open || s.connection.open {
				t.Fatal("cancelling the directory dialog left an unrelated popup open")
			}
			if allocations := testing.AllocsPerRun(100, func() { m.focusProjectMenu(m.fontResources) }); allocations != 0 {
				t.Fatalf("idle project menu allocated %g times", allocations)
			}
		})
	}
}

func TestProjectMenuConnectsHostBeforeChoosingDirectory(t *testing.T) {
	m, project := resourceTestModel(t)
	resourceTestWrite(t, project, "main.go", "package saved\n")
	openProjectFile(project, "main.go")
	m.syncActiveProjectFields()
	m.updateActiveContent("package unsaved\n")
	file, document := m.activeFile(), m.activeFile().document
	s := newPencodeTestSession(t, project.backend)
	m.session = s
	backend := newPencodeBackend(t)
	directory := filepath.ToSlash(t.TempDir())
	if err := os.Mkdir(filepath.Join(directory, "app"), 0700); err != nil {
		t.Fatal(err)
	}
	profile := connection.Profile{Name: "Development", SSH: remote.Target{Host: "dev"}, Directory: directory}
	var dialed, opened connection.Profile
	s.ConfigureConnection(func(_ context.Context, value connection.Profile, _ func(string)) (*client.Client, error) {
		dialed = value
		return backend, nil
	}, connection.Profile{})
	t.Cleanup(s.Close)
	s.SetWindowOpener(func(paths []string, value connection.Profile) error {
		if len(paths) != 0 {
			t.Fatal("remote directory was forwarded as a local path")
		}
		opened = value
		return nil
	})
	m.projectMenu.open = true
	m.openProjectHost(profile)
	if m.projectMenu.open || !s.connection.busy || s.connection.picker.open {
		t.Fatal("host selection did not start connecting before browsing")
	}
	waitConnection(t, m)
	waitProjectPicker(t, m)
	p := &s.connection.picker
	if dialed != profile || !p.open || p.failed || p.path != directory || len(p.names) != 1 || p.names[0] != "app" {
		t.Fatalf("connected host did not populate its directory dialog: %+v", p)
	}
	m.browseProjectDirectory(directory + "/app")
	waitProjectPicker(t, m)
	m.projectPickerEvent("connection-project-open", "")
	want := profile
	want.Directory += "/app"
	if opened != want || p.open || s.connection.open {
		t.Fatal("confirmation did not open the selected remote directory in a window")
	}
	if m.backend != project.backend || s.backend != project.backend || len(m.projects) != 1 || m.activeFile() != file || file.document != document || file.content() != "package unsaved\n" || !file.Dirty {
		t.Fatal("opening another device changed the current workspace or edits")
	}
}

func TestProjectMenuConnectionFailureDoesNotOpenPicker(t *testing.T) {
	m, project := resourceTestModel(t)
	s := newPencodeTestSession(t, project.backend)
	m.session = s
	s.ConfigureConnection(func(context.Context, connection.Profile, func(string)) (*client.Client, error) {
		return nil, errors.New("host unavailable")
	}, connection.Profile{})
	t.Cleanup(s.Close)
	m.openProjectHost(connection.Profile{SSH: remote.Target{Host: "dev"}})
	waitConnection(t, m)
	if !s.connection.open || !s.connection.messageError || s.connection.pickProject || s.connection.picker.open || m.backend != project.backend {
		t.Fatal("failed connection opened a directory picker or lost its feedback")
	}
}

func TestRemoteWindowOpensRequestedDirectory(t *testing.T) {
	_, s := newPencodeTestLauncher(t)
	t.Cleanup(s.Close)
	m := s.active.model
	backend := newPencodeBackend(t)
	profile := connection.Profile{SSH: remote.Target{Host: "dev"}, Directory: filepath.ToSlash(t.TempDir())}
	s.ConfigureConnection(func(context.Context, connection.Profile, func(string)) (*client.Client, error) {
		return backend, nil
	}, profile)
	m.pollConnection()
	waitConnection(t, m)
	waitProjectPicker(t, m)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if s.connection.picker.open || m.backend != backend || len(m.projects) != 1 || m.activeProjectState().Path != profile.Directory {
		t.Fatal("new remote window did not open its requested directory")
	}
}

func TestProjectMenuLocalChoiceUsesLocalDialog(t *testing.T) {
	for _, remoteWorkspace := range []bool{false, true} {
		t.Run(fmt.Sprint(remoteWorkspace), func(t *testing.T) {
			m, project := resourceTestModel(t)
			s := newPencodeTestSession(t, project.backend)
			m.session = s
			s.ConfigureConnection(nil, connection.Profile{})
			t.Cleanup(s.Close)
			if remoteWorkspace {
				s.connection.workspaceProfile = connection.Profile{SSH: remote.Target{Host: "dev"}}
			}
			directory := t.TempDir()
			var opened []string
			s.SetWindowOpener(func(paths []string, profile connection.Profile) error {
				if profile.Remote() {
					t.Fatal("local action forwarded a remote profile")
				}
				opened = paths
				return nil
			})
			m.lastDirectory = project.Path
			m.app = &module.Host{Context: &window.Context{}, RequestRender: func() {}, OpenFolder: func(options window.FileDialogOptions) (string, error) {
				if remoteWorkspace && options.DefaultPath != "" || !remoteWorkspace && options.DefaultPath != project.Path {
					t.Fatal("local dialog used a directory from another device")
				}
				return directory, nil
			}}
			m.projectMenu.open = true
			m.projectMenuEvent("project-menu-local")
			if err := awaitFileIO(t, m); err != nil {
				t.Fatal(err)
			}
			if m.projectMenu.open || s.connection.picker.open {
				t.Fatal("local choice left a project menu or remote picker open")
			}
			if remoteWorkspace {
				if len(opened) != 1 || opened[0] != directory || len(m.projects) != 1 {
					t.Fatal("local choice did not preserve the remote workspace and open a local window")
				}
			} else if len(opened) != 0 || len(m.projects) != 2 || m.activeProjectState().Path != directory {
				t.Fatal("local choice did not add a project in the current local window")
			}
		})
	}
}
