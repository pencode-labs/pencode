package workbench

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/internal/languagetool"
)

func TestDebugSettingsInstallAndOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX Go installer fixture")
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	t.Setenv("PENCODE_GO_TOOLS_DIR", t.TempDir())
	installer := "#!/bin/sh\nset -eu\n[ \"$*\" = 'install github.com/go-delve/delve/cmd/dlv@latest' ]\n/bin/mkdir -p \"$GOBIN\"\n/bin/cp /bin/true \"$GOBIN/dlv\"\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(installer), 0755); err != nil {
		t.Fatal(err)
	}
	app, m, _, state := languageInteractionFixture(t)
	m.settingsOpen = true
	m.settings.ServerPaths = map[string]string{"gopls": "/bin/true"}
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	click := func(id string) { t.Helper(); editorContextClick(t, app, m, state, id) }
	wait := func() {
		t.Helper()
		for until := time.Now().Add(5 * time.Second); m.builtinTools.cancel != nil; {
			if time.Now().After(until) {
				t.Fatal("tool request timed out")
			}
			languageInteractionStep(t, app)
		}
	}

	checkDelveLabel := func(want bool) {
		t.Helper()
		row, ok := m.fontResources.HTMLNode(m.surface, "language-choice-go")
		if !ok {
			t.Fatal("missing Go debugger row")
		}
		markup, err := row.Serialize()
		if err != nil || strings.Contains(markup, ">Delve<") != want {
			t.Fatalf("want Delve label present = %t, got %s (%v)", want, markup, err)
		}
	}
	click("settings-debug-adapters-nav")
	wait()
	checkDelveLabel(false)
	systemDelve := filepath.Join(bin, "dlv")
	if err := os.Symlink("/bin/true", systemDelve); err != nil {
		t.Fatal(err)
	}
	click("language-tools-refresh")
	wait()
	checkDelveLabel(true)
	if err := os.Remove(systemDelve); err != nil {
		t.Fatal(err)
	}
	click("language-tools-refresh")
	wait()
	checkDelveLabel(false)
	click("language-choice-go")
	click("language-tool-install-Delve")
	wait()
	info := m.builtinTools.items[languagetool.Delve]
	checkDelveLabel(true)
	if !info.Managed || filepath.Base(info.Command) != "dlv" {
		t.Fatalf("managed Delve missing: %+v, %s", info, m.builtinTools.feedback)
	}
	click("language-tool-details-Delve")
	control, ok := m.fontResources.HTMLControl(m.surface, "language-tool-path-Delve")
	if !ok {
		t.Fatal("missing adapter path control")
	}
	app.World.WriteMessage(htmlui.Event{Entity: m.surface, Target: control, Kind: htmlui.EventChange, Value: "/bin/false", Bubbles: true})
	languageInteractionStep(t, app)
	wait()
	saved, _ := loadPencodeSettings()
	if saved.DebugAdapterPaths[languagetool.Delve] != "/bin/false" || saved.ServerPaths["gopls"] != "/bin/true" || m.builtinTools.items[languagetool.Delve].Command != "/bin/false" {
		t.Fatalf("path override lost: %+v, %+v", saved, m.builtinTools.items[languagetool.Delve])
	}
	click("language-tool-uninstall-Delve")
	wait()
	checkDelveLabel(true)
	if info := m.builtinTools.items[languagetool.Delve]; info.Managed || info.Command != "/bin/false" {
		t.Fatal(info)
	}
	click("language-choice-php")
	editorContextBounds(t, m, "language-tool-install-Xdebug")
	click("settings-language-servers-nav")
	wait()
	click("language-choice-php")
	editorContextBounds(t, m, "language-tool-install-intelephense")
	if _, ok := m.fontResources.HTMLNode(m.surface, "language-tool-install-Xdebug"); ok {
		t.Fatal("debug adapter shown on language server page")
	}
}
