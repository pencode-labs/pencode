package workbench

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/onix-engine/onix/input"
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

func TestBuiltinClangdSharesSession(t *testing.T) {
	m, project, goFile := languageTestModel(t)
	defer m.closeLanguageServers()
	delete(project.Files, goFile.Path)
	names := []string{"main.c", "main.cpp", "main.m", "main.mm"}
	languages := []string{"c", "cpp", "objective-c", "objective-cpp"}
	files := make([]*openFile, 0, len(names))
	for _, name := range names {
		const source = "int main(void) { return 0; }\n"
		if err := os.WriteFile(filepath.Join(project.Path, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		file := &openFile{Path: name, Name: name, contentSnapshot: snapshotText(source)}
		file.savedContent = file.snapshot()
		project.Files[name] = file
		files = append(files, file)
	}
	project.ActivePath, project.Tabs = names[0], names
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		m.pollBuiltinTools()
		if m.builtinTools.cancel == nil && m.builtinTools.items["clangd"].Command == "" {
			t.Skip("clangd unavailable")
		}
		m.pollLanguageServers(nil)
		var shared *pencodeLanguageSession
		synced := 0
		for i, file := range files {
			doc := m.languages.documents[file]
			if doc == nil || len(doc.servers) == 0 {
				continue
			}
			for session := range doc.servers {
				if shared != nil && session != shared || session.server.LocalID != "clangd" || session.server.DocumentLanguage(file.Name) != languages[i] {
					t.Fatalf("unexpected clangd session or document language for %s", file.Name)
				}
				shared = session
				synced++
			}
		}
		if synced == len(files) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("clangd did not sync all four files: %s", m.status)
}

func TestLanguageSettingsChooseAndPersist(t *testing.T) {
	bin := t.TempDir()
	detected := filepath.Join(bin, "phpantom_lsp")
	if runtime.GOOS == "windows" {
		detected += ".exe"
	}
	if err := os.WriteFile(detected, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
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
	if m.settingsToolPage != languageServerSettings {
		t.Fatal("settings tab was not selected", m.settingsOpen)
	}
	for until := time.Now().Add(time.Second * 3); m.builtinTools.cancel != nil && time.Now().Before(until); {
		languageInteractionStep(t, app)
	}
	checkAttribute := func(id, attribute, want string) {
		t.Helper()
		node, ok := m.fontResources.HTMLNode(m.surface, id)
		if got, _ := node.Attribute(attribute); !ok || got != want {
			t.Fatalf("%s %s = %q, want %q", id, attribute, got, want)
		}
	}
	checkAttribute("language-choice-php", "aria-expanded", "false")
	click("language-choice-go")
	checkAttribute("language-choice-go", "aria-expanded", "true")
	click("language-choice-php")
	checkAttribute("language-choice-go", "aria-expanded", "false")
	checkAttribute("language-choice-php", "aria-expanded", "true")
	if _, ok := m.fontResources.HTMLNode(m.surface, "language-tool-activate-gopls"); ok {
		t.Fatal("Go server still shown after expanding PHP")
	}
	php := editorContextBounds(t, m, "language-choice-php")
	python := editorContextBounds(t, m, "language-choice-python")
	if height := python.Y - (php.Y + php.Height); height > 4*php.Height {
		t.Fatalf("four collapsed PHP tools occupy %.0fpx, language row height %.0fpx", height, php.Height)
	}
	for _, id := range []string{"intelephense", "devsense-php-ls", "phpactor", "phpantom_lsp"} {
		button := editorContextBounds(t, m, "language-tool-activate-"+id)
		if button.Y < php.Y+php.Height || button.Y+button.Height > python.Y {
			t.Fatalf("%s is outside the expanded PHP group", id)
		}
	}
	checkAttribute("language-tool-activate-phpactor", "aria-pressed", "true")
	checkAttribute("language-tool-details-intelephense", "aria-expanded", "false")
	details := editorContextBounds(t, m, "language-tool-details-intelephense")
	scale := float64(m.settings.Zoom) / 100
	clickRowAt := func(x float32) {
		y := float64(details.Y+details.Height/2) * scale
		for _, kind := range []input.EventKind{input.EventMouseDown, input.EventMouseUp} {
			state.Enqueue(input.Event{Kind: kind, Button: input.MouseLeft, X: float64(x) * scale, Y: y})
		}
		languageInteractionStep(t, app)
	}
	clickRowAt(details.X + details.Width + 12)
	checkAttribute("language-tool-details-intelephense", "aria-expanded", "true")
	clickRowAt(php.X + 2)
	checkAttribute("language-tool-details-intelephense", "aria-expanded", "false")
	click("language-tool-details-intelephense")
	checkAttribute("language-tool-details-intelephense", "aria-expanded", "true")
	checkAttribute("language-tool-details-phpactor", "aria-expanded", "false")
	editorContextBounds(t, m, "language-tool-path-intelephense")
	checkAttribute("language-tool-path-intelephense", "value", executable)
	checkAttribute("language-tool-path-intelephense", "placeholder", m.tr("留空以自动检测"))
	click("language-tool-details-intelephense")
	checkAttribute("language-tool-details-intelephense", "aria-expanded", "false")
	click("language-tool-details-phpantom_lsp")
	checkAttribute("language-tool-path-phpantom_lsp", "value", "")
	checkAttribute("language-tool-path-phpantom_lsp", "placeholder", detected)
	click("language-tool-details-phpantom_lsp")
	click("language-tool-activate-intelephense")
	checkAttribute("language-tool-details-intelephense", "aria-expanded", "false")
	logs, _ := m.outputStore().Channel("pencode").Snapshot()
	if want := "PHP · Intelephense · " + m.tr("已激活"); m.builtinTools.feedback != want || !strings.Contains(logs, "[Info] "+want) {
		t.Fatalf("activation feedback %q, output %q", m.builtinTools.feedback, logs)
	}
	checkAttribute("language-tool-activate-phpactor", "aria-pressed", "false")
	checkAttribute("language-tool-activate-intelephense", "aria-pressed", "true")
	click("language-choice-php")
	checkAttribute("language-choice-php", "aria-expanded", "false")
	if _, ok := m.fontResources.HTMLNode(m.surface, "language-tool-activate-intelephense"); ok {
		t.Fatal("PHP servers still shown after collapsing PHP")
	}
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
	if m.settingsToolPage == languageServerSettings {
		t.Fatal("language pane still selected")
	}
}

func TestLanguageToolInstallFeedback(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	t.Setenv("PATH", t.TempDir())
	m.settingsOpen = true
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	click := func(id string) { t.Helper(); editorContextClick(t, app, m, state, id) }
	click("settings-language-servers-nav")
	wait := func() {
		t.Helper()
		for until := time.Now().Add(5 * time.Second); m.builtinTools.cancel != nil; {
			if time.Now().After(until) {
				t.Fatal("language tool request timed out")
			}
			languageInteractionStep(t, app)
		}
	}
	wait()
	listTop := editorContextBounds(t, m, "language-choice-go").Y
	click("language-choice-go")
	nextRow := editorContextBounds(t, m, "language-choice-php")
	click("language-tool-activate-gopls")
	channel := m.outputStore().Channel("pencode")
	warning := fmt.Sprintf(m.tr("请先安装 %s 再激活。"), "gopls")
	if logs, _ := channel.Snapshot(); m.builtinTools.feedback != warning || !strings.Contains(logs, "[Warning] "+warning) {
		t.Fatalf("missing activation warning: feedback %q, output %q", m.builtinTools.feedback, logs)
	}
	if editorContextBounds(t, m, "language-choice-php").Y <= nextRow.Y {
		t.Fatal("activation warning was not displayed under gopls")
	}
	for _, language := range []string{"go", "php"} {
		click("language-choice-" + language)
		if top := editorContextBounds(t, m, "language-choice-go").Y; top != listTop {
			t.Fatalf("tool feedback moved above the list: top %v, want %v", top, listTop)
		}
	}
	click("language-choice-go")
	click("language-tool-install-gopls")
	wait()
	logs, _ := channel.Snapshot()
	start := fmt.Sprintf(m.tr("正在安装 %s…"), "gopls")
	_, afterStart, ok := strings.Cut(logs, "[Info] "+start)
	failure := m.builtinTools.feedback
	if !ok || !strings.Contains(failure, "gopls") || !strings.Contains(failure, "PATH") || !strings.Contains(afterStart, "[Error] "+failure) {
		t.Fatalf("missing installer failure: feedback %q, output %q", failure, logs)
	}
}

func TestLanguageToolInstallCompletionFeedback(t *testing.T) {
	state := &pencodeBuiltinTools{action: "install", feedbackID: "gopls", results: make(chan pencodeToolResult, 1), cancel: func() {}}
	m := &workspaceModel{builtinTools: state}
	state.results <- pencodeToolResult{items: map[string]languagetool.Installation{"gopls": {Command: "gopls", Managed: true}}}
	if !m.pollBuiltinTools() {
		t.Fatal("installation did not finish")
	}
	logs, _ := m.outputStore().Channel("pencode").Snapshot()
	if want := fmt.Sprintf(m.tr("已安装 %s"), "gopls"); state.feedback != want || !strings.Contains(logs, "[Info] "+want) {
		t.Fatalf("missing completion: feedback %q, output %q", state.feedback, logs)
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
