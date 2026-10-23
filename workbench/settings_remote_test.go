package workbench

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/diiyw/onix/input"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/configuration"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/remote"
	"github.com/pencode-labs/pencode/server"
)

func settingsBackend(t *testing.T, directory string) *client.Client {
	t.Helper()
	a, b := net.Pipe()
	done := make(chan struct{})
	options := server.Options{ConfigurationDirectory: directory, ExtensionDirectory: t.TempDir()}
	go func() { defer close(done); _ = server.Serve(context.Background(), b, options) }()
	c, err := client.New(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); <-done })
	return c
}

func TestRemoteConfigurationFollowsWorkspaceAndSavesOnServer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	localDirectory, err := configuration.Directory()
	if err != nil {
		t.Fatal(err)
	}
	localData := []byte(`{"fontSize":11,"theme":"dark-plus"}`)
	if err := configuration.Save(t.Context(), localDirectory, localData); err != nil {
		t.Fatal(err)
	}
	remoteDirectory := filepath.Join(t.TempDir(), "resources")
	if err := os.MkdirAll(filepath.Join(remoteDirectory, "themes", "includes"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"settings.json":             `{"fontSize":21,"uiFontSize":18,"editorWidth":65,"zoom":120,"theme":"themes/custom.jsonc","iconTheme":"extensions/remote/icons","codeFont":"extensions/remote/code","bindings":{"save":{"key":` + "\"KeyL\"" + `,"primary":true}}}`,
		"highlights.json":           `{"captures":{"comment":"remote.comment"}}`,
		"themes/custom.jsonc":       `{"include":"includes/base.json","name":"Remote",}`,
		"themes/includes/base.json": `{"colors":{"editor.foreground":"#abcdef"}}`,
	} {
		if err := os.WriteFile(filepath.Join(remoteDirectory, filepath.FromSlash(name)), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Use the key's native JSON representation to exercise the shared settings parser.
	settings := parsePencodeSettings([]byte(`{"fontSize":21,"uiFontSize":18,"editorWidth":65,"zoom":120,"theme":"themes/custom.jsonc","iconTheme":"extensions/remote/icons","codeFont":"extensions/remote/code"}`))
	settings.Bindings["save"] = pencodeKeybinding{Key: input.KeyL, Primary: true}
	local, backend := settingsBackend(t, localDirectory), settingsBackend(t, remoteDirectory)
	s := NewSession(local)
	model := &workspaceModel{session: s, backend: local}
	model.settings, model.settingsPath = loadPencodeSettings()
	if err := model.loadSyntaxSettings(); err != nil {
		t.Fatal(err)
	}
	// Save the native key code through the same server protocol as a live edit.
	remoteModel := &workspaceModel{session: s, backend: backend, settingsRemote: true, settings: settings}
	remoteModel.saveSettings()
	s.closeSettingsWriters()
	profile := connection.Profile{SSH: remote.Target{Host: "remote"}, Directory: t.TempDir()}
	s.ConfigureConnection(func(context.Context, connection.Profile, func(string)) (*client.Client, error) { return backend, nil }, connection.Profile{})
	t.Cleanup(func() {
		model.stopFileIO()
		for _, project := range model.projects {
			project.stopScan()
		}
		s.market.stop()
		s.extensions.Close()
		s.Close()
	})
	model.beginConnection(profile, false)
	waitConnection(t, model)
	if model.settings.FontSize != 11 || model.settings.Theme != "dark-plus" || model.backend != local {
		t.Fatal("connecting changed local settings")
	}
	model.openSSHProject(profile)
	waitProjectPicker(t, model)
	model.connectionEvent("connection-project-cancel", "")
	if model.settings.FontSize != 11 || model.settingsRemote {
		t.Fatal("canceling browsing applied remote settings")
	}
	model.openSSHProject(profile)
	waitProjectPicker(t, model)
	model.connectionEvent("connection-project-open", "")
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	if !model.settingsRemote || model.settings.FontSize != 21 || model.settings.UIFontSize != 18 || model.settings.EditorWidth != 65 || model.settings.Zoom != 120 || model.settings.Bindings["save"].Key != input.KeyL {
		t.Fatalf("remote editor settings were not applied: %+v", model.settings)
	}
	if model.syntaxTheme.Name != "Remote" || model.syntaxDefinitions.Scope("comment") != "remote.comment" || model.settings.IconTheme != "extensions/remote/icons" || model.settings.CodeFont != "extensions/remote/code" {
		t.Fatal("remote themes, definitions or pending extension selections lost")
	}
	for size := 22; size <= 28; size++ {
		model.settings.FontSize = size
		model.saveSettings()
	}
	s.closeSettingsWriters() // Session shutdown flushes the newest queued snapshot.
	snapshot, err := backend.ReadConfiguration(t.Context())
	if err != nil || parsePencodeSettings(snapshot.Files["settings.json"]).FontSize != 28 {
		t.Fatalf("remote save failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(localDirectory, "settings.json"))
	if err != nil || string(data) != string(localData) {
		t.Fatalf("remote save modified local settings: %s, %v", data, err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	model.settings.FontSize = 29
	model.saveSettings()
	if !strings.Contains(model.settingsFeedback, "断开") {
		t.Fatal("disconnected save was not reported")
	}
	data, _ = os.ReadFile(filepath.Join(remoteDirectory, "settings.json"))
	if parsePencodeSettings(data).FontSize != 28 {
		t.Fatal("disconnected save changed disk")
	}
}

func TestConnectionRecoveryReloadsRemoteConfiguration(t *testing.T) {
	model, project := resourceTestModel(t)
	directory := filepath.Join(t.TempDir(), "resources")
	if err := configuration.Save(t.Context(), directory, []byte(`{"fontSize":25,"theme":"light-plus"}`)); err != nil {
		t.Fatal(err)
	}
	next := settingsBackend(t, directory)
	s := NewSession(project.backend)
	model.session = s
	profile := connection.Profile{SSH: remote.Target{Host: "remote"}}
	s.ConfigureConnection(func(context.Context, connection.Profile, func(string)) (*client.Client, error) { return next, nil }, connection.Profile{})
	s.connection.workspaceProfile = profile
	s.connection.links[profile.SSH] = &pencodeSSHLink{backend: project.backend, profile: profile}
	model.settings = defaultPencodeSettings()
	model.settings.FontSize = 11
	t.Cleanup(func() { s.market.stop(); s.extensions.Close(); s.Close() })
	model.beginConnection(profile, true)
	waitConnection(t, model)
	if model.backend != next || model.settings.FontSize != 25 || model.settings.Theme != "light-plus" || !model.settingsRemote {
		t.Fatalf("recovery kept stale settings: %+v", model.settings)
	}
	if allocations := testing.AllocsPerRun(100, func() { model.pollSettingsWrites() }); allocations != 0 {
		t.Fatalf("idle settings polling allocated %g times", allocations)
	}
}

func TestRemoteSettingsSaveReportsServerFailure(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "resources")
	if err := os.MkdirAll(filepath.Join(directory, "settings.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	backend := settingsBackend(t, directory)
	s := NewSession(backend)
	t.Cleanup(s.Close)
	model := &workspaceModel{session: s, backend: backend, settingsRemote: true, settings: defaultPencodeSettings()}
	model.saveSettings()
	deadline := time.Now().Add(5 * time.Second)
	for !model.pollSettingsWrites() {
		if time.Now().After(deadline) {
			t.Fatal("save result did not arrive")
		}
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(model.settingsFeedback, "失败") {
		t.Fatalf("server error was hidden: %s", model.settingsFeedback)
	}
}
