package workbench

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/remote"
)

func TestConnectionMenuAnchoredToButton(t *testing.T) {
	for _, tc := range []struct {
		name   string
		screen pencodeScreen
	}{{"launcher", pencodeLauncher}, {"workspace", pencodeWorkspace}} {
		t.Run(tc.name, func(t *testing.T) {
			testConnectionMenuAnchoredToButton(t, tc.screen)
		})
	}
}

func testConnectionMenuAnchoredToButton(t *testing.T, screen pencodeScreen) {
	t.Helper()
	app, model, _, state := languageInteractionFixture(t)
	if screen == pencodeLauncher {
		model.screen = pencodeLauncher
		if err := app.Modules().UpdateFrame(1.0 / 60); err != nil {
			t.Fatal(err)
		}
		if err := app.Modules().Dispatch(); err != nil {
			t.Fatal(err)
		}
		languageInteractionStep(t, app)
		model = model.session.active.model
	}
	s := model.session
	s.ConfigureConnection(nil, connection.Profile{})
	t.Cleanup(s.Close)
	s.connection.profiles = []connection.Profile{{Name: "Local", SSH: remote.Target{Host: "zhuoh@127.0.0.1"}}}
	markPencodeRootDirtyByModel(model.fontResources, model)
	languageInteractionStep(t, app)
	editorContextClick(t, app, model, state, "connection-open")
	anchor := editorContextBounds(t, model, "connection-more-0")
	editorContextClick(t, app, model, state, "connection-more-0")
	menu := editorContextBounds(t, model, "connection-menu")
	if math.Abs(float64(menu.X+menu.Width-anchor.X-anchor.Width)) > 2 || math.Abs(float64(menu.Y-anchor.Y-anchor.Height-4)) > 2 {
		t.Fatalf("connection menu drifted from its button: button=%+v menu=%+v position=(%g,%g)", anchor, menu, s.connection.menuX, s.connection.menuY)
	}
	editorContextClick(t, app, model, state, "connection-more-0")
	if s.connection.menu != -1 {
		t.Fatal("second click did not close the connection menu")
	}
}

func waitConnection(t *testing.T, model *workspaceModel) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for model.session.connection.busy {
		model.pollConnection()
		if time.Now().After(deadline) {
			t.Fatal("connection did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestConnectionRestoresCurrentUnsavedBufferAndHistory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	model, project := resourceTestModel(t)
	resourceTestWrite(t, project, "main.go", "package saved\n")
	openProjectFile(project, "main.go")
	model.syncActiveProjectFields()
	model.updateActiveContent("package offline\n")
	file, old := model.activeFile(), model.activeFile().document
	next := newPencodeBackend(t)
	s := newPencodeTestSession(t, project.backend)
	model.session = s
	s.ConfigureConnection(func(context.Context, connection.Profile, func(string)) (*client.Client, error) { return next, nil }, connection.Profile{})
	t.Cleanup(s.Close)
	model.newTerminal()
	item := model.terminals.active
	item.screen.Feed([]byte("retained output\r\n"))
	screenRevision := item.screen.Revision
	t.Cleanup(func() {
		model.closeTerminal("")
		select {
		case <-item.session.Done():
		case <-time.After(5 * time.Second):
			t.Error("retired terminal leaked")
		}
	})
	if err := s.backend.Close(); err != nil {
		t.Fatal(err)
	}
	if !model.pollConnection() || !s.connection.busy {
		t.Fatal("disconnect did not start recovery")
	}
	// Input may continue while fresh server handles are being opened.
	model.updateActiveContent("package latest\n// 中文🙂\n")
	model.submitHighlight(file)
	if model.highlightWorker != nil {
		t.Fatal("recovery started a parser bound to the retired backend")
	}
	undoCount := len(file.undo)
	waitConnection(t, model)
	model.pollTerminals()
	if model.terminals.active != item || !item.exited || item.screen.Revision != screenRevision {
		t.Fatal("recovery discarded the retired terminal screen")
	}
	if s.backend != next || model.backend != next || project.backend != next || file.document == nil || file.document == old || file.Content != "package latest\n// 中文🙂\n" || !file.Dirty || len(file.undo) != undoCount {
		t.Fatal("recovery lost the current buffer, history or connection ownership", s.connection.message)
	}
	data, err := os.ReadFile(filepath.Join(project.Path, "main.go"))
	if err != nil || string(data) != "package saved\n" {
		t.Fatal("recovery replayed a disk write", err)
	}
	if err := savePencodeFile(next, project.Path, file); err != nil {
		t.Fatal("explicit save after recovery failed", err)
	}
	data, _ = os.ReadFile(filepath.Join(project.Path, "main.go"))
	if string(data) != file.Content {
		t.Fatal("explicit save did not use the new backend")
	}
	if allocations := testing.AllocsPerRun(100, func() { model.pollConnection() }); allocations != 0 {
		t.Fatalf("idle connection polling allocated %g times", allocations)
	}
}

func TestConnectionRecoveryPreservesExternalConflict(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	model, project := resourceTestModel(t)
	resourceTestWrite(t, project, "main.go", "package saved\n")
	openProjectFile(project, "main.go")
	model.syncActiveProjectFields()
	model.updateActiveContent("package local\n")
	file := model.activeFile()
	next := newPencodeBackend(t)
	s := newPencodeTestSession(t, project.backend)
	model.session = s
	s.ConfigureConnection(func(context.Context, connection.Profile, func(string)) (*client.Client, error) { return next, nil }, connection.Profile{})
	t.Cleanup(s.Close)
	s.backend.Close()
	resourceTestWrite(t, project, "main.go", "package external\n")
	model.pollConnection()
	waitConnection(t, model)
	if file.Content != "package local\n" || !file.Dirty || file.document != nil || !strings.Contains(s.connection.message, "远端内容已变化") {
		t.Fatal("external conflict was silently reconciled", s.connection.message)
	}
	if err := savePencodeFile(next, project.Path, file); err == nil {
		t.Fatal("recovery enabled overwriting external changes")
	}
	data, _ := os.ReadFile(filepath.Join(project.Path, "main.go"))
	if string(data) != "package external\n" {
		t.Fatal("external content was overwritten")
	}
}

func TestConnectionCancellationPreservesOpenProject(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	model, project := resourceTestModel(t)
	s := newPencodeTestSession(t, project.backend)
	model.session = s
	s.ConfigureConnection(func(ctx context.Context, _ connection.Profile, _ func(string)) (*client.Client, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}, connection.Profile{})
	t.Cleanup(s.Close)
	profile := connection.Profile{SSH: remote.Target{Host: "remote"}}
	model.beginConnection(profile, false)
	if !s.connection.busy || s.connection.restoring || s.backend.Err() != nil || !model.backendConnected() {
		t.Fatal("SSH connection interfered with the current project")
	}
	model.connectionEvent("connection-cancel", "")
	waitConnection(t, model)
	if s.connection.busy || !strings.Contains(s.connection.message, context.Canceled.Error()) {
		t.Fatal("cancellation left connection busy", s.connection.message)
	}
}

func TestConnectionFailedAttemptRetainsRequestedEndpoint(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	s := newPencodeTestSession(t, newPencodeBackend(t))
	model := &workspaceModel{session: s, backend: s.backend}
	s.ConfigureConnection(func(context.Context, connection.Profile, func(string)) (*client.Client, error) {
		return nil, errors.New("authentication refused")
	}, connection.Profile{})
	t.Cleanup(s.Close)
	profile := connection.Profile{SSH: remote.Target{Host: "requested"}}
	model.beginConnection(profile, false)
	waitConnection(t, model)
	if s.connection.profile.SSH.Host != "requested" || !strings.Contains(s.connection.message, "authentication refused") {
		t.Fatal("failed attempt lost its retry endpoint", s.connection.message)
	}
}

func TestConnectionStopsRapidDisconnectLoopAndAllowsManualRetry(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	s := newPencodeTestSession(t, newPencodeBackend(t))
	model := &workspaceModel{session: s, backend: s.backend}
	calls := 0
	s.ConfigureConnection(func(context.Context, connection.Profile, func(string)) (*client.Client, error) {
		calls++
		return newPencodeBackend(t), nil
	}, connection.Profile{})
	t.Cleanup(s.Close)
	for range 4 {
		s.backend.Close()
		model.pollConnection()
		waitConnection(t, model)
	}
	if calls != 3 || !s.connection.lostHandled || !strings.Contains(s.connection.message, "已停止自动重连") {
		t.Fatal("rapid losses kept starting new automatic recovery cycles", calls, s.connection.message)
	}
	for range 10 {
		model.pollConnection()
	}
	if calls != 3 {
		t.Fatal("idle polling restarted the disconnected endpoint")
	}
	model.retryConnection(s.connection.profile)
	waitConnection(t, model)
	if calls != 4 || s.backend.Err() != nil {
		t.Fatal("manual retry did not reset the recovery limit")
	}
}

func TestConnectionDialogInputAndCancellation(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	s := model.session
	s.ConfigureConnection(func(ctx context.Context, _ connection.Profile, _ func(string)) (*client.Client, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}, connection.Profile{})
	t.Cleanup(s.Close)
	markPencodeRootDirtyByModel(model.fontResources, model)
	languageInteractionStep(t, app)
	editorContextClick(t, app, model, state, "connection-open")
	if !s.connection.open {
		t.Fatal("connection button did not open the dialog")
	}
	editorContextClick(t, app, model, state, "connection-add")
	editorContextClick(t, app, model, state, "connection-name")
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "开发机"})
	languageInteractionStep(t, app)
	editorContextClick(t, app, model, state, "connection-host")
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "devbox"})
	languageInteractionStep(t, app)
	if s.connection.form.Name != "开发机" || s.connection.form.SSH.Host != "devbox" {
		t.Fatal("host input did not reach the connection form")
	}
	editorContextClick(t, app, model, state, "connection-save")
	if len(s.connection.profiles) != 1 || s.connection.profiles[0].SSH.Host != "devbox" {
		t.Fatal("connection form did not save the SSH profile", s.connection.message)
	}
	model.beginConnection(s.connection.profiles[0], false)
	if !s.connection.busy || s.connection.restoring || !model.backendConnected() {
		t.Fatal("dialog did not connect independently of the current project")
	}
	markPencodeRootDirtyByModel(model.fontResources, model)
	languageInteractionStep(t, app)
	editorContextClick(t, app, model, state, "connection-cancel")
	waitConnection(t, model)
	if !strings.Contains(s.connection.message, context.Canceled.Error()) {
		t.Fatal("dialog did not cancel the background connection", s.connection.message)
	}
}

func TestRemoteConnectionsDoNotChangeCurrentProject(t *testing.T) {
	model, project := resourceTestModel(t)
	resourceTestWrite(t, project, "main.go", "package saved\n")
	openProjectFile(project, "main.go")
	model.syncActiveProjectFields()
	model.updateActiveContent("package editing\n")
	file, document := model.activeFile(), model.activeFile().document
	local := project.backend
	s := newPencodeTestSession(t, local)
	model.session = s
	calls := 0
	s.ConfigureConnection(func(ctx context.Context, profile connection.Profile, _ func(string)) (*client.Client, error) {
		calls++
		return newPencodeBackend(t), nil
	}, connection.Profile{})
	t.Cleanup(s.Close)
	one := connection.Profile{Name: "one", WebSocket: remote.WebSocketTarget{Address: "ws://one/ws", Token: "token"}, Directory: "/must-not-open"}
	two := connection.Profile{Name: "two", WebSocket: remote.WebSocketTarget{Address: "ws://two/ws", Token: "token"}}
	ssh := connection.Profile{SSH: remote.Target{Host: "ssh"}}
	for _, profile := range []connection.Profile{one, two, ssh} {
		model.beginConnection(profile, false)
		if !model.backendConnected() {
			t.Fatal("remote dialing blocked local project operations")
		}
		waitConnection(t, model)
	}
	first := s.connection.links[one.Endpoint()].backend
	first.Close()
	model.pollConnection()
	waitConnection(t, model)
	if calls != 4 || !model.connectionConnected(one) || !model.connectionConnected(two) || !model.connectionConnected(ssh) {
		t.Fatal("independent remote links did not reconnect")
	}
	if s.backend != local || model.backend != local || project.backend != local || model.activeFile() != file || file.document != document || file.Content != "package editing\n" || !file.Dirty || len(model.projects) != 1 {
		t.Fatal("maintaining remote connections changed the current project")
	}
	if allocations := testing.AllocsPerRun(100, func() { model.pollConnection() }); allocations != 0 {
		t.Fatalf("idle remote polling allocated %g times", allocations)
	}
	model.disconnectRemote(one)
	for range 5 {
		model.pollConnection()
	}
	if calls != 4 || model.connectionConnected(one) || !model.connectionConnected(two) || !model.connectionConnected(ssh) || !model.backendConnected() {
		t.Fatal("disconnect affected other connections or reconnected itself")
	}
	if err := savePencodeFile(local, project.Path, file); err != nil {
		t.Fatal("local save failed after remote connection changes", err)
	}
}

func TestWebSocketConnectionDialog(t *testing.T) {
	for _, tc := range []struct {
		name   string
		screen pencodeScreen
	}{{"launcher", pencodeLauncher}, {"workspace", pencodeWorkspace}} {
		t.Run(tc.name, func(t *testing.T) {
			app, model, _, state := languageInteractionFixture(t)
			if tc.screen == pencodeLauncher {
				model.screen = tc.screen
				if err := app.Modules().UpdateFrame(1.0 / 60); err != nil {
					t.Fatal(err)
				}
				if err := app.Modules().Dispatch(); err != nil {
					t.Fatal(err)
				}
				languageInteractionStep(t, app)
				model = model.session.active.model
			}
			s := model.session
			s.ConfigureConnection(nil, connection.Profile{})
			t.Cleanup(s.Close)
			markPencodeRootDirtyByModel(model.fontResources, model)
			languageInteractionStep(t, app)
			editorContextClick(t, app, model, state, "connection-open")
			editorContextClick(t, app, model, state, "connection-add")
			editorContextClick(t, app, model, state, "connection-type")
			state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyDown})
			state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyDown})
			languageInteractionStep(t, app)
			if !s.connection.webSocket {
				t.Fatal("connection type selector did not select WebSocket")
			}
			languageInteractionStep(t, app)
			for _, field := range []struct{ ref, value string }{
				{"connection-name", "直连开发机"}, {"connection-address", "ws://localhost:8080/ws"}, {"connection-token", "saved-secret"},
			} {
				if tc.screen == pencodeWorkspace && field.ref == "connection-name" {
					continue
				}
				editorContextClick(t, app, model, state, field.ref)
				entity, ok := model.fontResources.HTMLControl(model.surface, field.ref)
				control, mounted := model.fontResources.Get[ui.TextField](entity)
				if !ok || !mounted || !control.Focused {
					t.Fatalf("%s not focused: ok=%v mounted=%v focused=%v", field.ref, ok, mounted, control.Focused)
				}
				state.Enqueue(input.Event{Kind: input.EventTextInput, Text: field.value})
				languageInteractionStep(t, app)
			}
			entity, ok := model.fontResources.HTMLControl(model.surface, "connection-token")
			control, mounted := model.fontResources.Get[ui.TextField](entity)
			if !ok || !mounted || !control.Password {
				t.Fatal("token input is not masked")
			}
			// Hidden SSH fields cannot invalidate a direct connection.
			s.connection.port = "invalid"
			editorContextClick(t, app, model, state, "connection-save")
			profiles := connection.LoadProfiles(connection.ProfilePath())
			if len(profiles) != 1 || profiles[0].WebSocket.Address != "ws://localhost:8080/ws" || profiles[0].WebSocket.Token != "saved-secret" || profiles[0].SSH != (remote.Target{}) {
				t.Fatalf("direct connection form did not persist: %s; mode=%v address=%q", s.connection.message, s.connection.webSocket, s.connection.form.WebSocket.Address)
			}
			if tc.screen == pencodeWorkspace && profiles[0].Name != profiles[0].WebSocket.Address {
				t.Fatal("empty display name did not default to the service address")
			}
			s.connection.menu = 0
			model.connectionEvent("connection-menu-edit", "")
			markPencodeRootDirtyByModel(model.fontResources, model)
			languageInteractionStep(t, app)
			languageInteractionStep(t, app)
			entity, ok = model.fontResources.HTMLControl(model.surface, "connection-type")
			selector, mounted := model.fontResources.Get[ui.Selector](entity)
			if !ok || !mounted || selector.Selected != 1 {
				t.Fatalf("editing WebSocket profile selected the wrong transport: selected=%d ok=%v mounted=%v form=%v mode=%v", selector.Selected, ok, mounted, s.connection.formOpen, s.connection.webSocket)
			}
			link := &pencodeRemoteLink{profile: profiles[0]}
			s.connection.links[profiles[0].Endpoint()] = link
			model.connectionEvent("connection-token", "rotated-secret")
			model.connectionEvent("connection-save", "")
			if link.profile.WebSocket.Token != "rotated-secret" || len(s.connection.links) != 1 {
				t.Fatal("token rotation did not update the existing connection")
			}
		})
	}
}

func TestSSHPasswordPromptIsFreshOnReconnectAndClearedOnCancel(t *testing.T) {
	local := newPencodeBackend(t)
	s := newPencodeTestSession(t, local)
	model := &workspaceModel{session: s, backend: local}
	calls := 0
	s.ConfigureConnection(func(ctx context.Context, _ connection.Profile, _ func(string)) (*client.Client, error) {
		value, err := s.connection.requestAuthentication(ctx, remote.AuthenticationPrompt{Text: "user@host password:"}, nil)
		if err != nil {
			return nil, err
		}
		if value != "temporary-secret" {
			return nil, errors.New("incorrect password")
		}
		calls++
		return newPencodeBackend(t), nil
	}, connection.Profile{})
	t.Cleanup(s.Close)
	profile := connection.Profile{SSH: remote.Target{Host: "host"}}
	waitPrompt := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for s.connection.passwordRequest == nil {
			model.pollConnection()
			if time.Now().After(deadline) {
				t.Fatal("password dialog did not open")
			}
			time.Sleep(time.Millisecond)
		}
		if !s.connection.open || s.connection.password != "" {
			t.Fatal("password dialog reused a password")
		}
	}
	for attempt := range 2 {
		if attempt == 0 {
			model.beginConnection(profile, false)
		} else {
			s.connection.links[profile.Endpoint()].backend.Close()
			model.pollConnection()
		}
		waitPrompt()
		if calls != attempt {
			t.Fatal("reconnect authenticated without a fresh answer")
		}
		model.connectionEvent("connection-password", "temporary-secret")
		model.connectionEvent("connection-password-submit", "")
		if s.connection.password != "" || s.connection.passwordRequest != nil {
			t.Fatal("submitted password was retained")
		}
		waitConnection(t, model)
	}
	model.retryConnection(profile)
	waitPrompt()
	model.connectionEvent("connection-password", "discard-secret")
	model.connectionEvent("connection-password-cancel", "")
	waitConnection(t, model)
	if s.connection.password != "" || s.connection.passwordRequest != nil || calls != 2 || !model.backendConnected() {
		t.Fatal("cancel retained credentials or affected the local backend")
	}
}

func TestConnectionPasswordDialogMasksInputAndClearsAfterSubmit(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	s := model.session
	s.ConfigureConnection(func(ctx context.Context, _ connection.Profile, _ func(string)) (*client.Client, error) {
		value, err := s.connection.requestAuthentication(ctx, remote.AuthenticationPrompt{Text: "user@host password:"}, nil)
		if err != nil {
			return nil, err
		}
		if value != "temporary-secret" {
			return nil, errors.New("incorrect password")
		}
		return newPencodeBackend(t), nil
	}, connection.Profile{})
	t.Cleanup(s.Close)
	profile := connection.Profile{SSH: remote.Target{Host: "host"}}
	showPrompt := func() {
		t.Helper()
		model.beginConnection(profile, false)
		deadline := time.Now().Add(5 * time.Second)
		for s.connection.passwordRequest == nil {
			model.pollConnection()
			if time.Now().After(deadline) {
				t.Fatal("password dialog did not open")
			}
			time.Sleep(time.Millisecond)
		}
		markPencodeRootDirtyByModel(model.fontResources, model)
		languageInteractionStep(t, app)
	}
	showPrompt()
	field, ok := model.fontResources.HTMLControl(model.surface, "connection-password")
	if !ok {
		t.Fatal("password input was not mounted")
	}
	control, ok := model.fontResources.Get[ui.TextField](field)
	if !ok || !control.Password || !control.Focused {
		t.Fatalf("password input was not masked and focused: mounted=%v masked=%v focused=%v pendingFocus=%v", ok, control.Password, control.Focused, s.connection.passwordFocus)
	}
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "temporary-secret"})
	languageInteractionStep(t, app)
	if s.connection.password != "temporary-secret" {
		t.Fatal("password input did not reach authentication")
	}
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEnter})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEnter})
	languageInteractionStep(t, app)
	waitConnection(t, model)
	control, _ = model.fontResources.Get[ui.TextField](field)
	if s.connection.password != "" || control.Value != "" || !model.connectionConnected(profile) {
		t.Fatal("password was retained after submitting")
	}
	showPrompt()
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "discard-secret"})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEscape})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEscape})
	languageInteractionStep(t, app)
	waitConnection(t, model)
	control, _ = model.fontResources.Get[ui.TextField](field)
	if s.connection.password != "" || control.Value != "" || !model.backendConnected() {
		t.Fatal("cancel retained password input or affected the project")
	}
}

func TestConnectionRestoresOnlyWorkspaceOnItsOwnSSHLink(t *testing.T) {
	model, project := resourceTestModel(t)
	resourceTestWrite(t, project, "main.go", "package saved\n")
	openProjectFile(project, "main.go")
	model.syncActiveProjectFields()
	model.updateActiveContent("package editing\n")
	file := model.activeFile()
	local, next := newPencodeBackend(t), newPencodeBackend(t)
	s := newPencodeTestSession(t, project.backend)
	s.localBackend = local
	model.session = s
	s.ConfigureConnection(func(context.Context, connection.Profile, func(string)) (*client.Client, error) { return next, nil }, connection.Profile{})
	t.Cleanup(s.Close)
	profile := connection.Profile{SSH: remote.Target{Host: "workspace"}}
	s.connection.workspaceProfile, s.connection.namespace = profile, profile.Namespace()
	s.connection.links[profile.Endpoint()] = &pencodeRemoteLink{profile: profile, backend: project.backend}
	s.backend.Close()
	model.pollConnection()
	if !s.connection.restoring {
		t.Fatal("workspace SSH loss did not retire stale handles")
	}
	waitConnection(t, model)
	if s.backend != next || project.backend != next || s.localBackend != local || local.Err() != nil || file.Content != "package editing\n" || file.document == nil || !file.Dirty || !model.connectionConnected(profile) {
		t.Fatal("SSH workspace recovery lost local state or changed another connection")
	}
}

func TestConnectionHostTrustDialogThenPassword(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	s := model.session
	const fingerprintPrompt = "The authenticity of host 'host (127.0.0.1)' can't be established.\nED25519 key fingerprint is SHA256:example.\nAre you sure you want to continue connecting (yes/no/[fingerprint])?"
	s.ConfigureConnection(func(ctx context.Context, _ connection.Profile, _ func(string)) (*client.Client, error) {
		answer, err := s.connection.requestAuthentication(ctx, remote.AuthenticationPrompt{Text: fingerprintPrompt, Confirm: true}, nil)
		if err != nil {
			return nil, err
		}
		if answer != "yes" {
			return nil, errors.New("host was not trusted")
		}
		answer, err = s.connection.requestAuthentication(ctx, remote.AuthenticationPrompt{Text: "user@host password:"}, nil)
		if err != nil {
			return nil, err
		}
		if answer != "temporary-secret" {
			return nil, errors.New("host confirmation was reused as a password")
		}
		return newPencodeBackend(t), nil
	}, connection.Profile{})
	t.Cleanup(s.Close)
	profile := connection.Profile{SSH: remote.Target{Host: "host"}}
	waitPrompt := func(confirm bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for s.connection.passwordRequest == nil || s.connection.passwordRequest.prompt.Confirm != confirm {
			model.pollConnection()
			if time.Now().After(deadline) {
				t.Fatal("authentication prompt did not open")
			}
			time.Sleep(time.Millisecond)
		}
		markPencodeRootDirtyByModel(model.fontResources, model)
		languageInteractionStep(t, app)
	}
	model.beginConnection(profile, false)
	waitPrompt(true)
	if s.connection.passwordRequest.prompt.Text != fingerprintPrompt {
		t.Fatal("host fingerprint was lost")
	}
	field, _ := model.fontResources.HTMLControl(model.surface, "connection-password")
	if _, mounted := model.fontResources.Get[ui.TextField](field); mounted {
		t.Fatal("host confirmation displayed a password input")
	}
	editorContextClick(t, app, model, state, "connection-password-submit")
	waitPrompt(false)
	field, _ = model.fontResources.HTMLControl(model.surface, "connection-password")
	control, mounted := model.fontResources.Get[ui.TextField](field)
	if !mounted || !control.Password || !control.Focused || control.Value != "" {
		t.Fatal("host confirmation did not transition to a fresh password input")
	}
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "temporary-secret"})
	languageInteractionStep(t, app)
	editorContextClick(t, app, model, state, "connection-password-submit")
	waitConnection(t, model)
	if !model.connectionConnected(profile) || !model.backendConnected() {
		t.Fatal("host confirmation affected the local project", s.connection.message)
	}
	model.retryConnection(profile)
	waitPrompt(true)
	editorContextClick(t, app, model, state, "connection-password-cancel")
	waitConnection(t, model)
	if s.connection.passwordRequest != nil || !model.backendConnected() || !strings.Contains(s.connection.message, context.Canceled.Error()) {
		t.Fatal("host confirmation cancellation did not stop the connection", s.connection.message)
	}
}
