package workbench

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/internal/languagetool"
)

func TestBuiltinGoplsDiscoveryAndHighlight(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls unavailable")
	}
	m, _, file := languageTestModel(t)
	defer m.closeLanguageServers()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		m.pollBuiltinTools()
		m.pollLanguageServers(nil)
		doc := m.languages.documents[file]
		if doc != nil && doc.semantic != nil && doc.semantic.version == doc.version && len(doc.semantic.tokens) > 0 {
			if len(doc.servers) != 1 || doc.semantic.session.server.LocalID != "gopls" {
				t.Fatal("wrong semantic provider")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no semantic tokens from built-in gopls: %s", m.status)
}

func TestLanguageSettingsChooseAndPersist(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m.settings.LanguageServers = map[string]string{"php": "phpactor"}
	m.settings.ServerPaths = map[string]string{"intelephense": executable, "phpactor": executable}
	m.settingsOpen = true
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	click := func(id string) { t.Helper(); editorContextClick(t, app, m, state, id) }
	click("settings-language-servers-nav")
	if !m.settingsLanguageServers {
		t.Fatal("settings tab was not selected", m.settingsOpen)
	}
	for until := time.Now().Add(time.Second * 3); m.builtinTools.cancel != nil && time.Now().Before(until); {
		languageInteractionStep(t, app)
	}
	click("language-choice-php")
	click("language-tool-activate-intelephense")
	saved, _ := loadPencodeSettings()
	if saved.LanguageServers["php"] != "intelephense" || saved.ServerPaths["phpactor"] != executable {
		t.Fatal("lost preference", saved.LanguageServers)
	}
	var count int
	for _, s := range m.languages.servers {
		if s.Match("page.blade.php") {
			count++
			if s.LocalID != "intelephense" || s.Command != executable || s.DocumentLanguage("page.blade.php") != "blade" {
				t.Fatal(s)
			}
		}
	}
	if count != 1 {
		t.Fatal("active PHP providers", count)
	}
	click("settings-appearance-nav")
	if m.settingsLanguageServers {
		t.Fatal("language pane still selected")
	}
}

func TestLanguageToolInstallUpdateRollbackAndUninstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX go installer fixture")
	}
	bin := t.TempDir()
	for name, body := range map[string]string{
		"gopls": "#!/bin/sh\nexit 0\n",
		"go":    "#!/bin/sh\nset -eu\n[ \"$*\" = 'install golang.org/x/tools/gopls@latest' ]\n/bin/mkdir -p \"$GOBIN\"\n/bin/cp \"$PENCODE_FIXTURE_BINARY\" \"$GOBIN/gopls\"\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	system := filepath.Join(bin, "gopls")
	t.Setenv("PATH", bin)
	t.Setenv("PENCODE_FIXTURE_BINARY", system)
	backend := settingsBackend(t, t.TempDir())
	run := func(op string) languagetool.Installation {
		t.Helper()
		id := "gopls"
		if op == "" {
			id = ""
		}
		items, err := backend.LanguageTools(t.Context(), op, id, nil)
		if err != nil {
			t.Fatal(err)
		}
		return items["gopls"]
	}
	if s := run(""); s.Managed || s.Command != system {
		t.Fatal(s)
	}
	first := run("install")
	if !first.Managed || first.Command == system {
		t.Fatal(first)
	}
	t.Setenv("PENCODE_FIXTURE_BINARY", "missing")
	if _, err := backend.LanguageTools(t.Context(), "update", "gopls", nil); err == nil {
		t.Fatal("expected installer failure")
	}
	if s := run(""); s != first {
		t.Fatal("failed update replaced the working copy", s)
	}
	t.Setenv("PENCODE_FIXTURE_BINARY", system)
	second := run("update")
	if !second.Managed || second.Command == first.Command || second.Command == "" {
		t.Fatal(second)
	}
	if s := run("uninstall"); s.Managed || s.Command != system {
		t.Fatal(s)
	}
	if _, err := os.Stat(second.Command); !os.IsNotExist(err) {
		t.Fatal("managed binary retained", err)
	}
}
