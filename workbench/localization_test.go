package workbench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onix-engine/onix/scene"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/localization"
)

func TestDisplayLanguageSwitchAndPersistence(t *testing.T) {
	app, m, _, _ := languageInteractionFixture(t)
	original := m.activeFile().snapshot()
	m.settingsOpen = true
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	control, ok := m.fontResources.HTMLControl(m.surface, "language-select")
	if !ok {
		t.Fatal("missing language control")
	}
	change := func(id string) {
		t.Helper()
		app.World.WriteMessage(htmlui.Event{Entity: m.surface, Target: control, Kind: htmlui.EventChange, Value: id, Bubbles: true})
		languageInteractionStep(t, app)
	}
	change("en")
	if m.settings.Language != "en" || m.locale.ID != "en" {
		t.Fatalf("locale not changed: %s", m.locale.ID)
	}
	assertLabel := func(id, want string) {
		t.Helper()
		if _, ok := m.fontResources.HTMLNode(m.surface, id); !ok {
			t.Fatalf("missing %s", id)
		}
		for _, text := range m.fontResources.Query[scene.Text2D]() {
			if text.Ref().Text().String() == want {
				return
			}
		}
		t.Fatalf("%s: rendered label %q not found", id, want)
	}
	assertLabel("settings-appearance-nav", "Appearance")
	closeID := "file-tab-close:" + m.activeProjectState().Path + ":" + m.activePath
	closeNode, ok := m.fontResources.HTMLNode(m.surface, closeID)
	closeTitle, _ := closeNode.Attribute("title")
	if !ok || closeTitle != "Close file" {
		t.Fatalf("retained tab language not updated: %q", closeTitle)
	}
	if saved, _ := loadPencodeSettings(); saved.Language != "en" {
		t.Fatal("locale not persisted")
	}
	if node, ok := m.fontResources.HTMLNode(m.surface, "quick-open-query"); !ok {
		t.Fatal("missing search input")
	} else if placeholder, _ := node.Attribute("placeholder"); placeholder != "Search files (filename:line)" {
		t.Fatalf("placeholder: %q", placeholder)
	}
	for id, want := range map[string]string{"ja": "外観", "ko": "모양", "de": "Darstellung", "fr": "Apparence", "es": "Apariencia", "zh-CN": "外观"} {
		change(id)
		if m.tr("外观") != want {
			t.Fatalf("wrong locale %s", id)
		}
		node, ok := m.fontResources.HTMLNode(m.surface, "settings-button")
		title, _ := node.Attribute("title")
		if !ok || title != m.tr("设置") {
			t.Fatalf("title not updated for %s: %q", id, title)
		}
	}
	if !m.activeFile().snapshot().Equal(original) {
		t.Fatal("switching language altered document content")
	}
	change("zh-CN")
	change("../invalid")
	if m.settings.Language != "zh-CN" {
		t.Fatal("unknown locale was saved")
	}
}

func TestPluginLanguageLifecycle(t *testing.T) {
	m := &workspaceModel{settings: parsePencodeSettings([]byte(`{"language":"it"}`))}
	m.ensureLanguagePacks()
	if m.locale.ID != "en" || m.settings.Language != "it" {
		t.Fatal("pending selection lost")
	}
	pack, err := localization.Parse([]byte(`{"id":"it","label":"Italiano","messages":{"设置":"Impostazioni"}}`))
	if err != nil {
		t.Fatal(err)
	}
	m.importLanguagePacks("italian", []localization.Pack{pack})
	if m.locale.ID != "it" || m.tr("设置") != "Impostazioni" || m.tr("保存") != "Save" {
		t.Fatal("plugin locale not activated")
	}
	count := len(m.languagePacks)
	m.importLanguagePacks("duplicate", []localization.Pack{pack})
	if len(m.languagePacks) != count {
		t.Fatal("duplicate locale accepted")
	}
	m.removeLanguagePacks("extensions/italian/")
	if m.locale.ID != "en" || m.settings.Language != "it" {
		t.Fatal("unloading did not preserve selection and fallback")
	}
	m.importLanguagePacks("italian", []localization.Pack{pack})
	if m.locale.ID != "it" {
		t.Fatal("reloading did not restore selection")
	}
}

func TestAuthoredTranslationsHaveEnglishFallback(t *testing.T) {
	for _, name := range []string{"launcher.html", "workspace.html"} {
		data, err := os.ReadFile(filepath.Join("..", "resources", "workbench", "assets", "ui", name))
		if err != nil {
			t.Fatal(err)
		}
		_, bindings, err := localizedPencodeTemplate(string(data))
		if err != nil {
			t.Fatal(err)
		}
		if len(bindings) == 0 {
			t.Fatal("no localized bindings")
		}
		for _, binding := range bindings {
			if localization.English().Text(binding.source) == binding.source {
				t.Errorf("%s: missing English: %q", name, binding.source)
			}
		}
	}
	source := `<button data-ref="menu"><img src="test.svg">扩展</button><input data-ref="user" value="设置" placeholder="搜索文本">`
	page, bindings, err := localizedPencodeTemplate(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 || !strings.Contains(page, `value="设置"`) || !strings.Contains(page, `<img src="test.svg"/>`) {
		t.Fatalf("authored translation changed user values or icons: %s", page)
	}
	settings := parsePencodeSettings([]byte(`{"language":"it"}`))
	data, err := json.Marshal(settings)
	if err != nil || parsePencodeSettings(data).Language != "it" {
		t.Fatal("pending plugin language not preserved")
	}
}

func TestRemoteDisplayLanguagePersistence(t *testing.T) {
	directory := t.TempDir()
	backend := settingsBackend(t, directory)
	session := &Session{}
	m := &workspaceModel{session: session, backend: backend, settingsRemote: true, settings: defaultPencodeSettings()}
	m.ensureLanguagePacks()
	if !m.changeLanguage("de") {
		t.Fatal("language not changed")
	}
	session.closeSettingsWriters()
	data, err := os.ReadFile(filepath.Join(directory, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if parsePencodeSettings(data).Language != "de" {
		t.Fatal("language not saved on workspace host")
	}
}
