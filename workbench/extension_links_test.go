package workbench

import (
	"math"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestExtensionLinksAlignWithSurroundingText(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	m.app.GetViewport = func() (int, int) { return 1440, 900 }
	app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	css, err := os.ReadFile("testdata/codex/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, font := range []string{"12px/1.2 ui", "14px/1.7 ui", "18px/2 monospace"} {
		if err := m.applyExtensionUI("codex", &extensions.ActionSession{}, peb.UI{ID: "link-baseline", Kind: peb.EditorTab, View: &peb.EditorView{
			HTML: `<main data-pencode-layout="fill"><article class="codex-chat"><section data-ref="message" data-pencode-region><p><span>Before</span> <button type="button" role="link" class="codex-link" data-ref="site" data-pencode-href="http://127.0.0.1:8000"><img class="codex-link-icon" data-ref="globe" data-pencode-icon="globe" alt="">http://127.0.0.1:8000</button> <span>After</span></p></section></article></main>`,
			CSS:  string(css) + `.codex-chat { font: ` + font + `; }`,
		}}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(m.fontResources, m)
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
		positions := map[string]float32{}
		for entity, text := range m.fontResources.Query[scene.Text2D]() {
			label := strings.TrimSpace(text.Ref().Text().String())
			switch label {
			case "Before", "http://127.0.0.1:8000", "After":
				positions[label] = m.fontResources.Ref[scene.UIGlobalTransform](entity).Matrix[13]
			}
		}
		if len(positions) != 3 {
			t.Fatalf("%s: missing text positions: %v", font, positions)
		}
		for _, label := range []string{"http://127.0.0.1:8000", "After"} {
			if math.Abs(float64(positions[label]-positions["Before"])) > .01 {
				t.Errorf("%s: %s is not aligned with surrounding text: %v", font, label, positions)
			}
		}
	}
}

func TestExtensionLinkIconsRenderOnce(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m, provider := session.active.model, session.active.icons
	css, err := os.ReadFile("testdata/codex/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.applyExtensionUI("codex", &extensions.ActionSession{}, peb.UI{ID: "link-icons", Kind: peb.EditorTab, View: &peb.EditorView{
		HTML: `<main data-pencode-layout="fill"><article class="codex-chat"><section data-ref="message" data-pencode-region><p>Running at <button type="button" role="link" class="codex-link" data-ref="site" data-pencode-href="http://127.0.0.1:8000"><img class="codex-link-icon" data-ref="globe" data-pencode-icon="globe" alt="">http://127.0.0.1:8000</button>. <button type="button" role="link" class="codex-link" data-ref="file" data-pencode-href="main.go"><img class="codex-link-icon" data-ref="code" data-pencode-icon="code" alt=""><code>main.go</code></button></p></section></article><section class="codex-directory"><nav><button type="button" role="link" class="codex-link" data-ref="directory" data-pencode-href="docs/"><img class="codex-link-icon" data-ref="folder" data-pencode-icon="folder" alt="">docs</button><button type="button" role="link" class="codex-link" data-ref="notes" data-pencode-href="notes.md"><img class="codex-link-icon" data-ref="file-text" data-pencode-icon="file-text" alt="">notes.md</button></nav></section></main>`,
		CSS:  string(css),
	}}); err != nil {
		t.Fatal(err)
	}
	for _, theme := range []string{"light-plus", "dark-plus"} {
		m.selectTheme(theme)
		markPencodeRootDirtyByModel(m.fontResources, m)
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"globe", "code", "folder", "file-text"} {
			source := "icons/" + name + ".svg"
			colored := m.themedIcon(source, "textLink.foreground")
			if want := source + "?foreground=" + m.workbenchTheme.theme.Color("textLink.foreground").Hex()[1:]; colored != want {
				t.Fatalf("%s: %s color = %s, want %s", theme, name, colored, want)
			}
			texture, ok := provider.ResolveHTMLImage(colored)
			if !ok {
				t.Fatalf("missing %s texture", name)
			}
			count := 0
			for _, sprite := range m.fontResources.Query[scene.Sprite2D]() {
				if sprite.Ref().Texture == texture {
					count++
					if sprite.Ref().Width != 14 || sprite.Ref().Height != 14 {
						t.Errorf("%s: %s size = %gx%g, want 14x14", theme, name, sprite.Ref().Width, sprite.Ref().Height)
					}
				}
			}
			if count != 1 {
				t.Errorf("%s: %s rendered %d times, want once", theme, name, count)
			}
			if n := testing.AllocsPerRun(100, func() { m.themedIcon(source, "textLink.foreground") }); n != 0 {
				t.Fatalf("warm link icon lookup allocated: %g", n)
			}
		}
	}
}

func TestExtensionLinksNativeClickAndThemeIcons(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	project := m.activeProjectState()
	target := m.activeFile()
	if err := m.applyExtensionUI("codex", &extensions.ActionSession{}, peb.UI{ID: "links", Title: "Links", Kind: peb.EditorTab, Context: peb.ActionContext{ProjectPath: project.Path}, View: &peb.EditorView{
		HTML: `<section data-ref="message" data-pencode-region><p>Created <button type="button" role="link" class="codex-link" data-ref="file" data-pencode-href="` + target.Path + `"><img data-ref="code" data-pencode-icon="code" alt="">` + target.Name + `</button>. <a data-ref="site" href="https://example.com"><img data-ref="globe" data-pencode-icon="globe" alt="">Website</a></p></section>`,
		CSS:  `img { width: 14px; height: 14px; } .codex-link { display: inline-block; max-width: 100%; } a { color: var(--vscode-textLink-foreground); cursor: pointer; }`,
	}}); err != nil {
		t.Fatal(err)
	}
	s := m.actions.ui.editor
	markPencodeRootDirtyByModel(m.fontResources, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	if len(s.icons) != 2 {
		t.Fatal("missing theme icons", s.icons)
	}
	for _, icon := range s.icons {
		if icon.foreground != "textLink.foreground" {
			t.Fatal("link icon did not use the link color", icon)
		}
	}
	editorContextClick(t, app, m, state, s.prefix+"control-file")
	for range 3 {
		languageInteractionStep(t, app)
	}
	if m.actions.ui.editor != nil || m.activeFile() != target || target.Preview {
		t.Fatalf("native link click: editor=%p surface=%p active=%p target=%p preview=%t status=%s feedback=%s", m.actions.ui.editor, s, m.activeFile(), target, target.Preview, m.status, m.extensionFeedback)
	}
	if allocations := testing.AllocsPerRun(100, func() { m.syncExtensionUI(m.fontResources) }); allocations != 0 {
		t.Fatalf("unchanged links allocate: %g", allocations)
	}
}

func TestExtensionInternetLinksUseSystemBrowser(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("intercepts the Linux browser launcher")
	}
	directory := t.TempDir()
	result := filepath.Join(directory, "address")
	if err := os.WriteFile(filepath.Join(directory, "xdg-open"), []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$PENCODE_TEST_LINK_ADDRESS\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	t.Setenv("PENCODE_TEST_LINK_ADDRESS", result)
	m, _ := resourceTestModel(t)
	s := &pencodeExtensionSurface{model: m, owner: "codex"}
	const address = "https://example.com/path?q=1&other=2#section"
	s.openLink(address)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(result); err == nil && string(data) == address {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("system browser did not receive the complete URL")
}

func TestExtensionLinksOpenPermanentTabsAndTrackRegions(t *testing.T) {
	m, project := resourceTestModel(t)
	resourceTestWrite(t, project, "notes with spaces.md", "first\nsecond\nthird\n")
	s := &pencodeExtensionSurface{model: m, ui: peb.UI{Kind: peb.EditorTab}, invocation: pencodeExtensionInvocation{context: peb.ActionContext{ProjectPath: project.Path}}, prefix: "pencode-extension-ui/test/", contentID: "content"}
	m.actions.ui.editor = s
	view := &peb.EditorView{HTML: `<section data-ref="message" data-pencode-region><a data-ref="file" href="notes%20with%20spaces.md:3">Notes</a><a data-ref="owned" href="owned.md">Owned</a><div hidden><a data-ref="hidden" href="hidden.md">Hidden</a></div></section>`, Events: []peb.EditorBinding{{Ref: "owned", Kind: "click"}}}
	if err := s.setView(view); err != nil {
		t.Fatal(err)
	}
	if len(s.links) != 1 {
		t.Fatal("hidden or extension-owned link captured", s.links)
	}
	props := pencodeExtensionSurfaceProps{Surface: s}
	s.definition.Behavior.HandleEvent(&htmlui.ComponentEventContext[pencodeExtensionSurfaceProps, struct{}]{Props: &props, Ref: "file", Event: htmlui.Event{Kind: htmlui.EventClick}})
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	f := m.activeFile()
	if f == nil || f.Path != "notes with spaces.md" || f.Preview || project.PreviewPath != "" || m.quickTargetLine != 3 || m.actions.ui.editor != nil {
		t.Fatal("link did not open a permanent tab at its line", f, m.quickTargetLine)
	}
	view.HTML = `<section data-ref="message" data-pencode-region><a data-ref="new" href="https://example.com">Site</a></section>`
	view.Events = nil
	if err := s.setView(view); err != nil {
		t.Fatal(err)
	}
	if len(s.links) != 1 || s.links["new"] != "https://example.com" || s.links["file"] != "" {
		t.Fatal("region update retained stale navigation", s.links)
	}
}

func TestExtensionLinksResolveAbsolutePathsAndRejectInvalidTargets(t *testing.T) {
	m, project := resourceTestModel(t)
	resourceTestWrite(t, project, "main.go", "package main\n// line two\n")
	s := &pencodeExtensionSurface{model: m, invocation: pencodeExtensionInvocation{context: peb.ActionContext{ProjectPath: project.Path}}}
	u := url.URL{Scheme: "file", Path: project.Path + "/main.go", Fragment: "L2"}
	s.openLink(u.String())
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if f := m.activeFile(); f == nil || f.Path != "main.go" || m.quickTargetLine != 2 {
		t.Fatal("absolute path did not resolve to workspace file", f)
	}
	for _, href := range []string{"javascript:alert(1)", "data:text/plain,secret", "#section", "../../outside.md", "file://another-host/main.go"} {
		s.openLink(href)
		if m.activeFile().Path != "main.go" || len(project.Tabs) != 1 {
			t.Fatalf("invalid link changed tabs: %s", href)
		}
	}
	for _, target := range []string{"./main.go:12", "./main.go:12:3", "main.go#L12", "main.go#L12-L15", "main.go#L12C3"} {
		u, _ := url.Parse(target)
		name, line := extensionLinkLocation(u)
		if name != "main.go" || line != 12 {
			t.Fatalf("%s resolved to %s:%d", target, name, line)
		}
	}
	if !strings.Contains(m.status, "已打开的项目") {
		t.Fatal("outside-workspace error not visible", m.status)
	}
}
