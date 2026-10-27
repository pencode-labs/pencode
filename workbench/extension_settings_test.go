package workbench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/module"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/peb"
)

func extensionSettingsFixture() []peb.Setting {
	return []peb.Setting{
		{ID: "name", Label: "名称", Description: "显示的名称", Default: json.RawMessage(`"Pencode"`)},
		{ID: "enabled", Label: "启用", Type: peb.SettingBoolean, Default: json.RawMessage(`true`)},
		{ID: "scale", Label: "比例", Type: peb.SettingNumber, Default: json.RawMessage(`1.5`)},
		{ID: "limit", Label: "数量", Type: peb.SettingInteger, Default: json.RawMessage(`10`)},
		{ID: "mode", Label: "模式", Default: json.RawMessage(`"auto"`), Options: []peb.SettingOption{{Value: "auto", Label: "自动"}, {Value: "manual", Label: "手动"}}},
	}
}

func TestExtensionSettingsNavigationControlsAndPersistence(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	m.importExtensionSettings("test", "测试扩展", extensionSettingsFixture())
	m.importExtensionSettings("static", "静态扩展", nil)
	m.settingsOpen = true
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	editorContextClick(t, app, m, state, "settings-extensions-nav")
	if !m.settingsExtensionsExpanded || !m.settingsExtensions || len(m.extensionSettings) != 1 {
		t.Fatal("extension category did not expand or included unconfigurable extensions")
	}
	editorContextClick(t, app, m, state, "settings-extension:test")
	if m.settingsExtension != "test" {
		t.Fatal("extension selection did not reach the model")
	}
	for _, id := range []string{"name", "enabled", "scale", "limit", "mode"} {
		editorContextBounds(t, m, "extension-setting:test:"+id)
	}
	change := func(id, value string, checked bool) {
		t.Helper()
		control, ok := m.fontResources.HTMLControl(m.surface, "extension-setting:test:"+id)
		if !ok {
			t.Fatalf("missing control %s", id)
		}
		ecs.WriteMessage(app.World, htmlui.Event{Entity: m.surface, Target: control, Kind: htmlui.EventChange, Value: value, Checked: checked, Bubbles: true})
		languageInteractionStep(t, app)
	}
	change("name", "中文名称", false)
	change("enabled", "", false)
	change("scale", "2.5", false)
	change("limit", "24", false)
	change("mode", "manual", false)
	values := m.extensionSettingValues("test")
	if string(values["name"]) != `"中文名称"` || string(values["enabled"]) != "false" || string(values["scale"]) != "2.5" || string(values["limit"]) != "24" || string(values["mode"]) != `"manual"` {
		t.Fatalf("control changes were not saved: %s", values)
	}
	change("limit", "1.5", false)
	if string(m.extensionSettingValues("test")["limit"]) != "24" || m.settingsFeedback == "" {
		t.Fatal("invalid integer was saved or validation error was hidden")
	}
	saved, _ := loadPencodeSettings()
	if string(saved.Extensions["test"]["mode"]) != `"manual"` {
		t.Fatal("settings were not persisted")
	}
	// Cached rows must bind to the foreground model after switching between
	// the launcher and workspace, including errors stored outside shared maps.
	for _, screen := range []pencodeScreen{pencodeLauncher, pencodeWorkspace} {
		m.screen = screen
		if err := app.Modules().UpdateFrame(1.0 / 60); err != nil {
			t.Fatal(err)
		}
		if err := app.Modules().Dispatch(); err != nil {
			t.Fatal(err)
		}
		m = m.session.active.model
		m.settingsFeedback = ""
		languageInteractionStep(t, app)
		editorContextBounds(t, m, "extension-setting:test:name")
		change("limit", "invalid", false)
		if m.settingsFeedback == "" {
			t.Fatal("settings event mutated a previous scene's model")
		}
	}
	// Built-in saves must retain extension settings, including unloaded entries.
	m.settings.Extensions["uninstalled"] = peb.SettingValues{"future": json.RawMessage(`42`)}
	m.settings.FontSize++
	m.saveSettings()
	saved, _ = loadPencodeSettings()
	if string(saved.Extensions["uninstalled"]["future"]) != "42" || string(saved.Extensions["test"]["limit"]) != "24" {
		t.Fatal("saving appearance discarded extension values")
	}
	editorContextClick(t, app, m, state, "settings-shortcuts-nav")
	if m.settingsExtensions || !m.settingsShortcuts {
		t.Fatal("built-in settings navigation stopped working")
	}
	editorContextClick(t, app, m, state, "settings-extension:test")
	context := module.ContentEventContext[appProps, appState]{Ref: "reset-extension-settings"}
	m.handleSettingsEvent(&context)
	if m.settings.Extensions["test"] != nil || string(m.extensionSettingValues("test")["limit"]) != "10" {
		t.Fatal("reset did not restore defaults")
	}
	m.removeExtensionSettings("test")
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	if m.settingsExtension != "" || len(m.extensionSettings) != 0 {
		t.Fatal("unloading left stale settings navigation")
	}
}

func TestExtensionSettingsRemotePersistenceAndIsolation(t *testing.T) {
	directory := t.TempDir()
	backend := settingsBackend(t, directory)
	s := &Session{}
	m := &workspaceModel{session: s, backend: backend, settingsRemote: true, settings: defaultPencodeSettings()}
	m.importExtensionSettings("test", "Test", extensionSettingsFixture())
	m.importExtensionSettings("other", "Other", extensionSettingsFixture())
	m.updateExtensionSetting(m.extensionSettings[1], 0, "remote", false)
	s.closeSettingsWriters()
	data, err := os.ReadFile(filepath.Join(directory, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	saved := parsePencodeSettings(data)
	if string(saved.Extensions["test"]["name"]) != `"remote"` || string(m.extensionSettingValues("other")["name"]) != `"Pencode"` {
		t.Fatalf("settings crossed extension namespaces: %s", data)
	}
	if allocations := testing.AllocsPerRun(100, func() { m.extensionSettingValues("test") }); allocations != 0 {
		t.Fatalf("cached setting lookup allocated %g times", allocations)
	}
	// Disabled/uninstalled extensions retain saved values for a later reload.
	m.removeExtensionSettings("test")
	m.importExtensionSettings("test", "Test", extensionSettingsFixture())
	if string(m.extensionSettingValues("test")["name"]) != `"remote"` {
		t.Fatal("reloading lost persisted settings")
	}
	m.unloadContributions("extensions/", "peb://")
	if len(m.extensionSettings) != 0 || !strings.Contains(string(data), `"extensions"`) {
		t.Fatal("switching workspace hosts retained settings schemas")
	}
}
