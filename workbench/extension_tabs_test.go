package workbench

import (
	"math"
	"strings"
	"testing"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestExtensionEditorTabFollowsOutputUntilUserScrolls(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	m.app.GetViewport = func() (int, int) { return 1440, 900 }
	ecs.InsertResource(app.World, scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	show := func(lines int) {
		t.Helper()
		if err := m.applyExtensionUI("codex-test", &extensions.ActionSession{}, peb.UI{ID: "stream", Title: "Stream", Kind: peb.EditorTab, View: &peb.EditorView{HTML: strings.Repeat(`<p>streamed output</p>`, lines)}}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(w, m)
		languageInteractionStep(t, app)
	}
	scroll := func() *scene.UIScroll {
		t.Helper()
		node, ok := w.HTMLNode(m.surface, m.actions.ui.editor.contentID)
		if !ok {
			t.Fatal("content missing")
		}
		area, ok := node.ScrollContainer()
		if !ok {
			t.Fatal("content has no scroll container")
		}
		s := module.ContentMut[scene.UIScroll](w, area)
		if s == nil {
			t.Fatal("content scroll missing")
		}
		return s
	}
	show(100)
	s := scroll()
	if end := s.ContentSize.Y() - s.ViewportSize.Y(); end < 100 || math.Abs(float64(s.Position.Y()+end)) > 1 {
		t.Fatalf("new conversation did not show latest output: %+v", s)
	}
	s.Position[1] = 0
	show(120)
	if scroll().Position.Y() != 0 {
		t.Fatal("streaming displaced a user reading old messages")
	}
	s = scroll()
	s.Position[1] = -(s.ContentSize.Y() - s.ViewportSize.Y())
	show(140)
	s = scroll()
	if end := s.ContentSize.Y() - s.ViewportSize.Y(); math.Abs(float64(s.Position.Y()+end)) > 1 {
		t.Fatal("streaming did not follow the bottom")
	}
}

func TestExtensionEditorTabsRetainSelectionAndFile(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	world := m.fontResources
	m.app.GetViewport = func() (int, int) { return 1440, 900 }
	ecs.InsertResource(app.World, scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	file, project := m.activeFile(), m.activeProjectState()
	process := &extensions.ActionSession{}
	show := func(id, title, text string) {
		t.Helper()
		if err := m.applyExtensionUI("codex-test", process, peb.UI{ID: id, Title: title, Kind: peb.EditorTab, View: &peb.EditorView{HTML: `<p>` + text + `</p><button data-ref="compose" type="button">继续对话</button>`, CSS: `p { color: var(--vscode-editor-foreground); }`, Events: []peb.EditorBinding{{Ref: "compose", Kind: "click"}}}}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(world, m)
		languageInteractionStep(t, app)
	}
	show("one", "First task", "first reply")
	one := m.actions.ui.editor
	if one == nil || m.activeFile() != nil || project.Files[file.Path] != file || project.ActivePath != file.Path {
		t.Fatal("extension tab did not preserve the underlying document")
	}
	if node, ok := world.HTMLNode(m.surface, one.rootID); !ok {
		t.Fatal("chat missing from editor area")
	} else if bounds, ok := node.Bounds(); !ok || bounds.Width < 100 || bounds.Height < 100 {
		t.Fatalf("chat has no usable bounds: %+v", bounds)
	}
	show("two", "Second task", "second reply")
	two := m.actions.ui.editor
	show("one", "Renamed first task", "streamed first reply")
	if m.actions.ui.editor != two {
		t.Fatal("background reply stole tab selection")
	}
	var tabCount int
	for _, access := range module.ContentQuery[htmlui.Component[tabProps, struct{}]](world) {
		p := access.Get().Props
		if p.Surface != nil {
			tabCount++
			if p.Surface == one && p.Name != "Renamed first task" {
				t.Fatal("tab title did not update")
			}
		}
	}
	if tabCount != 2 {
		t.Fatalf("got %d chat tabs", tabCount)
	}
	if err := m.applyExtensionUI("codex-test", process, peb.UI{ID: "one", Title: "Renamed first task", Kind: peb.EditorTab}); err != nil {
		t.Fatal(err)
	}
	if m.actions.ui.editor != one {
		t.Fatal("nil View did not reveal the existing tab")
	}
	m.selectProjectTab(project, file.Path)
	if m.actions.ui.editor != nil || m.activeFile() != file {
		t.Fatal("file tab did not restore source editing")
	}
	m.selectExtensionTab(one)
	m.closeExtensionSurface(one, "button", false)
	if m.actions.ui.editor != nil || m.activeFile() != file {
		t.Fatal("closing chat did not restore the file")
	}
	m.selectExtensionTab(two)
	m.clearExtensionUI("codex-test")
	if m.actions.ui.editor != nil || len(m.actions.ui.surfaces) != 0 {
		t.Fatal("unload left orphaned chat tabs")
	}
}

func TestExtensionEditorTabSteadyStateDoesNotAllocate(t *testing.T) {
	m := &workspaceModel{}
	s := &pencodeExtensionSurface{model: m, ui: peb.UI{Kind: peb.EditorTab}}
	m.actions.ui.editor = s
	if n := testing.AllocsPerRun(1000, func() {
		if !s.live() || m.activeFile() != nil {
			panic("wrong editor selection")
		}
	}); n != 0 {
		t.Fatalf("idle tab checks allocated %g times", n)
	}
	if !strings.Contains(m.workbenchTheme.resolveCSS("color: inherit"), "inherit") {
		t.Fatal("nil theme changed CSS")
	}
}
