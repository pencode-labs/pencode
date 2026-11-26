package workbench

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/onix-engine/onix/font"
	"github.com/onix-engine/onix/html/css"
	"github.com/onix-engine/onix/html/layout"
	"github.com/onix-engine/onix/html/parse"
	"github.com/onix-engine/onix/html/style"
	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	"github.com/onix-engine/onix/syntax"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/remote"
)

func TestProjectTitlebarSwitchAndClosePreserveEdits(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	m.languages = nil
	first := m.activeProjectState()
	m.updateActiveContent("first unsaved\n")
	secondRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(secondRoot, "other.txt"), []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadProjectAndWait(t, m, secondRoot); err != nil {
		t.Fatal(err)
	}
	second := m.activeProjectState()
	openFileAndWait(t, m, "other.txt")
	m.updateActiveContent("second unsaved\n")
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	assertTree := func(want *pencodeProject) {
		t.Helper()
		count := 0
		for _, access := range m.fontResources.Query[htmlui.Component[projectProps, struct{}]]() {
			p := access.Get().Props
			if p.Model == m {
				count++
				if p.Project != want {
					t.Fatal("inactive project tree remained mounted")
				}
			}
		}
		if count != 1 {
			t.Fatalf("mounted project trees=%d", count)
		}
	}
	assertTree(second)
	editorContextClick(t, app, m, state, "project-tab-select:"+first.Path)
	if m.activeProjectState() != first || m.activeContent() != "first unsaved\n" || !first.Files[first.ActivePath].Dirty {
		t.Fatalf("switching projects lost the first edit: active=%s first=%s content=%q dirty=%v", m.projectPath, first.Path, m.activeContent(), first.Files[first.ActivePath].Dirty)
	}
	assertTree(first)
	// Closing an inactive project keeps the current editor and asks about its edits.
	selectTab := editorContextBounds(t, m, "project-tab-select:"+second.Path)
	scale := float64(m.settings.Zoom) / 100
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(selectTab.X+selectTab.Width/2) * scale, Y: float64(selectTab.Y+selectTab.Height/2) * scale})
	languageInteractionStep(t, app)
	editorContextClick(t, app, m, state, "project-tab-close:"+second.Path)
	if len(m.projects) != 2 || m.closing.project != second || m.activeProjectState() != first {
		t.Fatal("closing inactive project skipped confirmation or changed selection")
	}
	editorContextClick(t, app, m, state, "close-cancel")
	if m.closing.action != pencodeCloseNone || second.Files["other.txt"].content() != "second unsaved\n" {
		t.Fatal("cancel did not retain the project's contents")
	}
	editorContextClick(t, app, m, state, "project-tab-select:"+second.Path)
	if m.activeProjectState() != second || m.activeContent() != "second unsaved\n" {
		t.Fatal("switching back lost the second edit")
	}
	assertTree(second)
	editorContextClick(t, app, m, state, "project-tab-close:"+second.Path)
	editorContextClick(t, app, m, state, "close-discard")
	languageInteractionStep(t, app)
	if len(m.projects) != 1 || m.activeProjectState() != first || m.activeContent() != "first unsaved\n" {
		t.Fatal("closing the active project failed to select the remaining project")
	}
	assertTree(first)
	// The titlebar plus lists saved hosts before opening the directory picker.
	s := m.session
	s.ConfigureConnection(nil, connection.Profile{})
	profile := connection.Profile{SSH: remote.Target{Host: "remote"}, Directory: first.Path}
	s.connection.workspaceProfile = profile
	s.connection.profiles = []connection.Profile{profile}
	s.connection.links[profile.Endpoint()] = &pencodeRemoteLink{backend: m.backend, profile: profile}
	editorContextClick(t, app, m, state, "add-project")
	if !m.projectMenu.open || s.connection.picker.open {
		t.Fatal("titlebar plus did not show the project menu")
	}
	editorContextClick(t, app, m, state, "project-menu-host:"+profile.Address())
	if !s.connection.picker.open || len(m.projects) != 1 {
		t.Fatal("titlebar plus did not use the project opener")
	}
}

func TestProjectTitlebarOverflowRevealsSelectionAndKeepsPlusVisible(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	m.languages = nil
	first := m.activeProjectState()
	for i := range 12 {
		m.projects = append(m.projects, &pencodeProject{Path: fmt.Sprintf("/project/%d", i), Name: fmt.Sprintf("Project %02d", i)})
	}
	m.activateProject(m.projects[len(m.projects)-1])
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	checkVisible := func() {
		t.Helper()
		viewport := editorContextBounds(t, m, "project-tabs")
		tab := editorContextBounds(t, m, "active-project-tab")
		plus := editorContextBounds(t, m, "add-project")
		search := editorContextBounds(t, m, "file-search")
		scroll := m.fontResources.Ref[scene.UIScroll](m.projectTabScroll.area)
		if scroll == nil || tab.X+scroll.Position.X() < viewport.X-.1 || tab.X+scroll.Position.X()+tab.Width > viewport.X+viewport.Width+.1 {
			t.Fatalf("selected project is clipped: tab=%+v viewport=%+v scroll=%+v", tab, viewport, scroll)
		}
		if plus.X < viewport.X+viewport.Width || plus.X+plus.Width > search.X {
			t.Fatalf("plus overlaps tabs or file search: plus=%+v tabs=%+v search=%+v", plus, viewport, search)
		}
	}
	checkVisible()
	m.activateProject(first)
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	checkVisible()
	if allocations := testing.AllocsPerRun(100, func() { m.syncProjectTabScroll(m.fontResources) }); allocations != 0 {
		t.Fatalf("idle project tab layout allocated %g times", allocations)
	}
	bounds := editorContextBounds(t, m, "project-tabs")
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(bounds.X + 5), Y: float64(bounds.Y + 5)})
	state.Enqueue(input.Event{Kind: input.EventMouseWheel, WheelY: -3})
	languageInteractionStep(t, app)
	if scroll := m.fontResources.Ref[scene.UIScroll](m.projectTabScroll.area); scroll == nil || scroll.Position.X() >= 0 {
		t.Fatal("vertical wheel did not scroll project tabs horizontally")
	}
}

func TestProjectTitlebarIcon(t *testing.T) {
	for name, want := range map[string]string{"Pencode": "P", "项目": "项", "  demo": "d", "": "?"} {
		if got := projectTabInitial(name); got != want {
			t.Errorf("initial of %q = %q, want %q", name, got, want)
		}
	}

	styles := make(map[string]bool)
	var projects []*pencodeProject
	for i := range len(projectTabIconStyles) + 4 {
		path := fmt.Sprintf("/workspace/%d/project", i)
		iconStyle := projectTabIconStyle(path, projects)
		if styles[iconStyle] {
			t.Fatalf("color repeated for %s: %s", path, iconStyle)
		}
		styles[iconStyle] = true
		projects = append(projects, &pencodeProject{Path: path, tabIconStyle: iconStyle})
	}
}

func TestProjectTitlebarFitsNativeControlsAndSearch(t *testing.T) {
	source, err := os.ReadFile("../resources/workbench/assets/ui/pencode.css")
	if err != nil {
		t.Fatal(err)
	}
	theme, err := syntax.BuiltinTheme("monokai")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparePencodeTheme("monokai", theme, string(source))
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := css.Parse(prepared.css)
	if err != nil {
		t.Fatal(err)
	}
	face, err := font.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = face.Close() })
	for _, zoom := range []int{0, pencodeMinZoom, 90, pencodeDefaultZoom, pencodeMaxZoom} {
		for _, width := range []float64{520, 560, 640, 800, 910, 960, 1440} {
			t.Run(fmt.Sprintf("nativeZoom=%d/width=%g", zoom, width), func(t *testing.T) {
				class := "pencode-titlebar"
				inline := ""
				if zoom != 0 {
					class += " native-controls"
					if zoom < pencodeDefaultZoom {
						inline = nativeProjectSwitcherStyles[zoom-pencodeMinZoom]
					}
				}
				page := `<html style="font-size:14px"><body><div class="pencode-root"><div class="pencode-content"><header class="` + class + `">
<div id="switcher" class="pencode-project-switcher" style="` + inline + `"><div id="tabs" class="pencode-project-tabs"><div id="active" class="pencode-project-tab active">
<button id="select" class="pencode-project-tab-select"><span id="icon" class="pencode-project-tab-icon" style="` + projectTabIconStyles[0] + `"><span>项</span></span><span id="name" class="pencode-project-tab-name">A very long project name for a narrow window</span></button>
<button id="close" class="pencode-project-tab-close"></button></div></div><button id="add" class="pencode-icon-button"></button></div>
<div id="search" class="pencode-titlebar-search"></div></header></div></div></body></html>`
				document, err := parse.Parse(page)
				if err != nil {
					t.Fatal(err)
				}
				styles, err := style.ComputeWithViewport(document, sheet, style.Viewport{Width: width, Height: 900})
				if err != nil {
					t.Fatal(err)
				}
				root, err := layout.BuildWithFonts(document, styles, face, face, width, 900)
				if err != nil {
					t.Fatal(err)
				}
				boxes := make(map[string]*layout.Box)
				var visit func(*layout.Box)
				visit = func(box *layout.Box) {
					if id, ok := box.Node.Attr("id"); ok {
						boxes[id] = box
					}
					for _, child := range box.Children {
						visit(child)
					}
				}
				visit(root)
				tabs, tab, close, add, search := boxes["tabs"].BorderBox(), boxes["active"].BorderBox(), boxes["close"].BorderBox(), boxes["add"].BorderBox(), boxes["search"].BorderBox()
				if zoom != 0 && tabs.X*float64(zoom)/100 < 88 {
					t.Fatalf("tabs overlap native controls at %d%%: tabs=%+v", zoom, tabs)
				}
				if zoom == pencodeDefaultZoom && tabs.X != 88 {
					t.Fatalf("tabs start at %g, want 88 at default zoom", tabs.X)
				}
				if tabs.Width <= 0 || add.X+add.Width > search.X-.1 || tab.Width > tabs.Width+.1 || close.X+close.Width > tabs.X+tabs.Width+.1 {
					t.Fatalf("titlebar overlaps or clips controls: tabs=%+v tab=%+v close=%+v add=%+v search=%+v", tabs, tab, close, add, search)
				}
				wantWidth := min(120, boxes["switcher"].BorderBox().Width-add.Width-4)
				if tab.Width < wantWidth-.1 || tab.Width > wantWidth+.1 {
					t.Fatalf("project tab width = %g, want %g", tab.Width, wantWidth)
				}
				icon, name := boxes["icon"].BorderBox(), boxes["name"].BorderBox()
				if icon.Width != 16 || icon.Height != 16 || icon.X+icon.Width > name.X || name.X+name.Width > close.X {
					t.Fatalf("project icon or name overlaps: icon=%+v name=%+v close=%+v", icon, name, close)
				}
			})
		}
	}
}

func TestProjectTitlebarRestoresExplorerScroll(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	m.languages = nil
	first := m.activeProjectState()
	for i := range 100 {
		first.Entries = append(first.Entries, treeEntry{Path: fmt.Sprintf("row-%03d.txt", i)})
	}
	first.tree.entriesChanged()
	second := &pencodeProject{Path: "/second", Name: "second"}
	m.projects = append(m.projects, second)
	m.revealProject, m.revealPath = nil, ""
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	m.fontResources.Mut[scene.UIScroll](first.tree.area).Position[1] = -600
	m.syncTreeScroll(m.fontResources)
	editorContextClick(t, app, m, state, "project-tab-select:"+second.Path)
	editorContextClick(t, app, m, state, "project-tab-select:"+first.Path)
	languageInteractionStep(t, app)
	scroll := m.fontResources.Ref[scene.UIScroll](first.tree.area)
	if scroll == nil || scroll.Position.Y() != -600 {
		t.Fatalf("project switch lost explorer scroll: %+v", scroll)
	}
}
