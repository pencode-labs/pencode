package workbench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/diiyw/onix/module"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/remote"
)

func TestRecentProjectsPersistsBoundedOrderAndDeletion(t *testing.T) {
	directory := t.TempDir()
	h := loadPencodeRecentProjects(directory)
	for i := range 12 {
		entry := pencodeRecentProject{Path: fmt.Sprintf("/projects/项目 %d", i)}
		entry.prepare()
		if err := h.remember(entry); err != nil {
			t.Fatal(err)
		}
	}
	h = loadPencodeRecentProjects(directory)
	if h.count != 10 || h.entries[0].Path != "/projects/项目 11" || h.entries[9].Path != "/projects/项目 2" {
		t.Fatalf("unexpected recent projects: %+v", h)
	}
	reopened := h.entries[5]
	if err := h.remember(reopened); err != nil {
		t.Fatal(err)
	}
	h = loadPencodeRecentProjects(directory)
	if h.count != 10 || h.entries[0] != reopened {
		t.Fatal("reopening did not persist recency or duplicated a project")
	}
	for h.count > 0 {
		if err := h.remove(h.entries[h.count/2]); err != nil {
			t.Fatal(err)
		}
		h = loadPencodeRecentProjects(directory)
	}
	if h.entries != [pencodeRecentProjectLimit]pencodeRecentProject{} {
		t.Fatal("removed entries retained references")
	}
}

func TestRecentProjectsDeviceIdentityAndAllocations(t *testing.T) {
	h := loadPencodeRecentProjects("")
	entries := [...]pencodeRecentProject{
		{Path: "/projects/app"},
		{Path: "/projects/app", Profile: connection.Profile{SSH: remote.Target{Host: "one"}}},
		{Path: "/projects/app", Profile: connection.Profile{SSH: remote.Target{Host: "two"}}},
		{Path: "/projects/app", Profile: connection.Profile{WebSocket: remote.WebSocketTarget{Address: "ws://one/ws", Token: "secret"}}},
		{Path: "/projects/app", Profile: connection.Profile{WebSocket: remote.WebSocketTarget{Address: "ws://two/ws", Token: "secret"}}},
	}
	for i := range entries {
		entries[i].prepare()
		h.remember(entries[i])
	}
	if h.count != len(entries) {
		t.Fatal("identical paths on separate devices were merged")
	}
	rotated := entries[3]
	rotated.Profile.WebSocket.Token = "rotated"
	h.remember(rotated)
	if h.count != len(entries) {
		t.Fatal("token rotation duplicated a recent project")
	}
	if allocations := testing.AllocsPerRun(100, func() {
		h.remember(entries[0])
		h.remember(entries[1])
	}); allocations != 0 {
		t.Fatalf("in-memory recency allocated %g times", allocations)
	}
}

func TestRecentProjectsIgnoresCorruptStorage(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "recent-projects.json"), []byte(`[{"path":"/partial"},`), 0o600); err != nil {
		t.Fatal(err)
	}
	if h := loadPencodeRecentProjects(directory); h.count != 0 {
		t.Fatal("corrupt storage supplied recent projects")
	}
}

func TestRecentProjectsLauncherOpenAndRemove(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	directory := m.projectPath
	step := func() {
		t.Helper()
		for range 3 {
			if err := app.Modules().UpdateFrame(1.0 / 60); err != nil {
				t.Fatal(err)
			}
			if err := app.Modules().Dispatch(); err != nil {
				t.Fatal(err)
			}
			languageInteractionStep(t, app)
		}
		m = session.active.model
	}
	closeProject := func() {
		m.discardProject(m.activeProjectState())
		step()
		if session.active.screen != pencodeLauncher {
			t.Fatal("did not return to launcher")
		}
	}
	click := func(ref string) {
		t.Helper()
		for entity, access := range module.ContentQuery[htmlui.Component[pencodeRecentProjectProps, struct{}]](m.fontResources) {
			component := access.Get()
			if component.Props.Model == m && component.Props.Entry.Path == directory {
				component.Definition.Behavior.HandleEvent(&htmlui.ComponentEventContext[pencodeRecentProjectProps, struct{}]{
					World: app.World, Component: entity, Props: &component.Props, Ref: ref, Event: htmlui.Event{Kind: htmlui.EventClick},
				})
				return
			}
		}
		t.Fatal("recent project row missing from launcher")
	}
	closeProject()
	click("open")
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	step()
	if session.active.screen != pencodeWorkspace || m.projectPath != directory || session.recentProjects.count != 1 {
		t.Fatal("recent project did not open or was duplicated")
	}
	closeProject()
	click("remove")
	step()
	for _, access := range module.ContentQuery[htmlui.Component[pencodeRecentProjectProps, struct{}]](m.fontResources) {
		if access.Get().Props.Model == m {
			t.Fatal("removed recent row remained mounted")
		}
	}
	if h := loadPencodeRecentProjects(filepath.Dir(session.recentProjects.file)); h.count != 0 {
		t.Fatal("removed project reappeared after reload")
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatal("removing history changed the project directory", err)
	}
	m.openRecentProject(pencodeRecentProject{Path: filepath.Join(directory, "missing")})
	if err := awaitFileIO(t, m); err == nil || session.recentProjects.count != 0 || m.screen != pencodeLauncher {
		t.Fatal("failed project open entered history or left the launcher")
	}
}

func TestRecentProjectsReconnectsSSHBeforeOpening(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	m.discardProject(m.activeProjectState())
	for range 3 {
		if err := app.Modules().UpdateFrame(1.0 / 60); err != nil {
			t.Fatal(err)
		}
		if err := app.Modules().Dispatch(); err != nil {
			t.Fatal(err)
		}
	}
	m = session.active.model
	backend := newPencodeBackend(t)
	localBackend := newPencodeBackend(t)
	profile := connection.Profile{Name: "测试设备", SSH: remote.Target{Host: "recent-device"}}
	entry := pencodeRecentProject{Path: filepath.ToSlash(t.TempDir()), Profile: profile}
	entry.prepare()
	calls := 0
	session.ConfigureConnection(func(_ context.Context, got connection.Profile, _ func(string)) (*client.Client, error) {
		calls++
		if !got.Remote() {
			return localBackend, nil
		}
		if got.SSH != profile.SSH {
			return nil, fmt.Errorf("unexpected SSH profile: %+v", got)
		}
		return backend, nil
	}, connection.Profile{})
	t.Cleanup(session.Close)
	m.openRecentProject(entry)
	waitConnection(t, m)
	waitProjectPicker(t, m)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || m.backend != backend || m.projectPath != entry.Path || session.connection.picker.open {
		t.Fatal("SSH history did not connect and open its exact directory")
	}
	if newest := session.recentProjects.entries[0]; newest.Path != entry.Path || newest.Profile.SSH != profile.SSH {
		t.Fatal("SSH project was recorded against the wrong device")
	}
	m.discardProject(m.activeProjectState())
	local := pencodeRecentProject{Path: filepath.ToSlash(t.TempDir())}
	m.openRecentProject(local)
	waitConnection(t, m)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	m.pollConnection()
	if calls != 2 || m.backend != localBackend || m.projectPath != local.Path || session.connection.busy {
		t.Fatal("opening local history did not switch back or restarted the retired SSH connection")
	}
}
