package workbench

import (
	"math"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/html/css"
	"github.com/diiyw/onix/html/dom"
	"github.com/diiyw/onix/html/style"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

func pencodeComputedTheme(t *testing.T, theme pencodeTheme, page string) map[string]*style.ComputedStyle {
	t.Helper()
	document, err := dom.Parse(page)
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := css.Parse(theme.css)
	if err != nil {
		t.Fatal(err)
	}
	styles, err := style.ComputeWithViewport(document, sheet, style.Viewport{Width: 1440, Height: 900})
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]*style.ComputedStyle)
	for node, computed := range styles {
		if ref, ok := node.Attr("data-ref"); ok {
			result[ref] = computed
		}
	}
	return result
}

func TestPencodeWorkbenchThemeColors(t *testing.T) {
	source, err := os.ReadFile("../resources/workbench/assets/ui/pencode.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range syntax.BuiltinThemeIDs {
		t.Run(id, func(t *testing.T) {
			theme, err := syntax.BuiltinTheme(id)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := preparePencodeTheme(id, theme, string(source))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(prepared.css, "var(") {
				t.Fatal("unresolved theme color")
			}
			for severity, color := range prepared.diagnosticColors {
				if color.A == 0 {
					t.Fatalf("severity %d has no diagnostic color", severity)
				}
				for previous := 1; previous < severity; previous++ {
					if color == prepared.diagnosticColors[previous] {
						t.Fatalf("severities %d and %d use the same color", previous, severity)
					}
				}
			}
			inline := pencodeComputedTheme(t, prepared, `<div class="sc-inline-change added" data-ref="added"></div><div class="sc-inline-change deleted" data-ref="deleted"></div>`)
			for _, kind := range []string{"added", "deleted"} {
				if value := inline[kind]; value == nil || value.BackgroundColor.Color.A == 0 {
					t.Fatalf("%s inline difference has no background", kind)
				}
			}
			connections := pencodeComputedTheme(t, prepared, `<div class="pencode-connection-feedback error" data-ref="error"></div><div class="pencode-connection-row connected" data-ref="connected"></div><span class="pencode-connection-dot connected" data-ref="connected-base"></span>`)
			for _, test := range []struct {
				ref                string
				background, border float64
			}{
				{"error", .08, .4},
				{"connected", .08, 0},
			} {
				computed := connections[test.ref]
				if computed == nil {
					t.Fatalf("missing connection %s", test.ref)
				}
				base := computed.Color.Color
				if test.ref == "connected" {
					base = connections["connected-base"].BackgroundColor.Color
				}
				want := base
				want.A = uint8(math.Round(float64(base.A) * test.background))
				if computed.BackgroundColor.Color != want {
					t.Fatalf("%s mixed theme background = %+v, want %+v", test.ref, computed.BackgroundColor.Color, want)
				}
				if test.border != 0 {
					want.A = uint8(math.Round(float64(base.A) * test.border))
					if computed.BorderColor.Top.Color != want {
						t.Fatalf("mixed error border = %+v, want %+v", computed.BorderColor.Top.Color, want)
					}
				}
			}

			link := pencodeComputedTheme(t, prepared, `<div class="pencode-lsp-hover-underline"><span data-ref="link"></span></div>`)["link"]
			if link == nil || prepared.link != pencodeSyntaxColor(syntax.Color(link.BackgroundColor.Color)) {
				t.Fatal("Ctrl hover text and underline use different theme colors")
			}
			for _, pageName := range []string{"launcher.html", "workspace.html"} {
				page, err := os.ReadFile("../resources/workbench/assets/ui/" + pageName)
				if err != nil {
					t.Fatal(err)
				}
				computed := pencodeComputedTheme(t, prepared, string(page))
				pairs := []struct{ ref, key string }{
					{"root", "editor.background"},
					{"settings-panel", "editorWidget.background"}, {"theme-select", "settings.dropdownBackground"},
					{"quick-open", "quickInput.background"},
				}
				if pageName == "workspace.html" {
					completion := computed["lsp-completion"]
					if completion == nil || completion.ScrollbarWidth != style.Keyword("thin") || completion.ScrollbarThumbColor != style.ColorValue(style.Color(theme.Color("scrollbarSlider.background"))) || completion.ScrollbarTrackColor != style.ColorValue(style.Color{}) {
						t.Fatal("completion scrollbar did not follow the workbench theme")
					}
					pairs = append(pairs, struct{ ref, key string }{"tabs", "editorGroupHeader.tabsBackground"}, struct{ ref, key string }{"find-bar", "editorWidget.background"})
					if projects := computed["projects"]; projects == nil || projects.BackgroundColor.Color.A != 0 {
						t.Fatal("project list should have a transparent background")
					}
				}
				for _, pair := range pairs {
					value := computed[pair.ref]
					if value == nil {
						t.Fatalf("%s is missing %s", pageName, pair.ref)
					}
					if value.BackgroundColor.Color != style.Color(theme.Color(pair.key)) {
						t.Fatalf("%s %s background = %v, want %v", pageName, pair.ref, value.BackgroundColor.Color, theme.Color(pair.key))
					}
				}
				if titlebar := computed["titlebar"]; titlebar == nil || titlebar.BackgroundColor.Color.A != 0 {
					t.Fatal("titlebar should have a transparent background")
				}
				if computed["theme-select"].Color.Color != style.Color(theme.Color("settings.dropdownForeground")) {
					t.Fatal("theme selector foreground did not follow the theme")
				}
			}
		})
	}
}

func TestPencodeCustomWorkbenchThemeAndLightDefaults(t *testing.T) {
	source, err := os.ReadFile("../resources/workbench/assets/ui/pencode.css")
	if err != nil {
		t.Fatal(err)
	}
	theme, err := syntax.LoadTheme(fstest.MapFS{"resources/custom.json": &fstest.MapFile{Data: []byte(`{
  "type":"light",
  "colors":{
   "titleBar.activeBackground":"#112233", "sideBar.background":"#ddeeff",
   "tab.activeBackground":"#fedcba", "tab.activeBorder":"#654321",
   "statusBar.background":"#abcdef", "statusBar.foreground":"#123456",
   "input.background":"#eeddcc", "dropdown.background":"#ffeedd",
   "widget.shadow":"#00000000", "icon.foreground":"#2468ac",
   "window.activeBorder":"#135790", "editorRuler.foreground":"#456789",
   "scmGraph.foreground2":"#8c36b0"
  }
 }`)}}, "resources/custom.json")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparePencodeTheme("custom", theme, string(source))
	if err != nil {
		t.Fatal(err)
	}
	fixture := `<div data-ref="root" class="pencode-root">
  <header data-ref="titlebar" class="pencode-titlebar"></header>
  <nav data-ref="projects" class="pencode-project-list"></nav>
  <div data-ref="tab" class="pencode-tab active"><button data-ref="tab-label" class="pencode-tab-select">main.go</button></div>
  <footer data-ref="status" class="pencode-statusbar">Ready</footer>
  <section data-ref="find" class="pencode-find"><input data-ref="query"></section>
  <select data-ref="theme-select" class="pencode-settings-select"></select>
  <span data-ref="graph-main" class="sc-graph-color-0"></span>
  <span data-ref="graph-branch" class="sc-graph-color-1"></span>
 </div>`
	computed := pencodeComputedTheme(t, prepared, fixture)
	if computed["graph-main"].Color.Color != (style.Color{G: 109, B: 190, A: 255}) || computed["graph-branch"].Color.Color != (style.Color{R: 140, G: 54, B: 176, A: 255}) {
		t.Fatal("graph colors lost the light palette or custom theme override")
	}
	for ref, expected := range map[string]style.Color{
		"root": {R: 255, G: 255, B: 255, A: 255}, "titlebar": {},
		"projects": {}, "tab": {R: 0xfe, G: 0xdc, B: 0xba, A: 255},
		"status": {}, "query": {R: 0xee, G: 0xdd, B: 0xcc, A: 255},
		"theme-select": {R: 0xff, G: 0xee, B: 0xdd, A: 255},
	} {
		if computed[ref].BackgroundColor.Color != expected {
			t.Fatalf("custom %s background = %v, want %v", ref, computed[ref].BackgroundColor.Color, expected)
		}
	}
	if computed["tab-label"].Color.Color != (style.Color{R: 0x33, G: 0x33, B: 0x33, A: 255}) || theme.Color("editor.foreground") != (syntax.Color{A: 255}) {
		t.Fatal("light theme did not default to dark editor text")
	}
	if computed["status"].Color.Color != style.Color(theme.Color("editor.foreground")) {
		t.Fatal("status bar foreground should follow the editor theme")
	}
	if computed["find"].BoxShadow[0].Color.A != 0 {
		t.Fatal("custom transparent shadow was lost")
	}
	if prepared.border != graphics.ColorFromRGB(0x13, 0x57, 0x90) || prepared.guide != graphics.ColorFromRGB(0x45, 0x67, 0x89) || !strings.HasSuffix(prepared.icons[pencodeIconKey{"icons/close.svg", "icon.foreground"}], "foreground=2468ac") {
		t.Fatal("native decorations or icon palette did not adopt custom colors")
	}
}

func TestPencodeWorkbenchDiagnosticThemeOverrides(t *testing.T) {
	theme, err := syntax.LoadTheme(fstest.MapFS{"diagnostics.json": &fstest.MapFile{Data: []byte(`{
  "type":"light", "colors":{
   "editorError.foreground":"#cc1122", "editorWarning.foreground":"#bb990080",
   "editorInfo.foreground":"#1177cc", "editorHint.foreground":"#77777700"
  }
 }`)}}, "diagnostics.json")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparePencodeTheme("diagnostics", theme, "")
	if err != nil {
		t.Fatal(err)
	}
	for severity, key := range [...]string{"editorInfo.foreground", "editorError.foreground", "editorWarning.foreground", "editorInfo.foreground", "editorHint.foreground"} {
		if prepared.diagnosticColors[severity] != pencodeSyntaxColor(theme.Color(key)) {
			t.Fatalf("severity %d ignored theme color %s", severity, key)
		}
	}
}

func TestPencodeWorkbenchThemeSyncReusesCache(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	content := model.fontResources
	editor := model.editor.control
	field := module.ContentMut[engineui.TextField](content, editor)
	field.Caret, field.Anchor = 18, 8
	model.selectTheme("light-plus")
	markPencodeRootDirtyByModel(content, model)
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	surface, _ := module.ContentGet[htmlui.Surface](content, model.surface)
	border, _ := module.ContentGet[scene.UIBorder2D](content, model.windowBorder)
	guide, _ := module.ContentGet[scene.Sprite2D](content, model.editor.guide)
	if surface.CSS != model.workbenchTheme.css || border.Shape.Border != model.workbenchTheme.border || guide.Shape.Fill != model.workbenchTheme.guide || editor != model.editor.control {
		t.Fatal("theme switch left stale workbench styles or replaced the editor")
	}
	field = module.ContentMut[engineui.TextField](content, editor)
	nativeStyle, ok := module.ContentGet[engineui.StyleOverride[engineui.TextFieldStyle]](content, editor)
	if field.Caret != 18 || field.Anchor != 8 || !ok || nativeStyle.Value.Caret.Background.Fill != graphics.Black || nativeStyle.Value.Selection.Background.Fill != pencodeSyntaxColor(model.syntaxTheme.Color("editor.selectionBackground")) {
		t.Fatalf("theme switch: caret=%d anchor=%d style=%v caretColor=%+v selectionColor=%+v", field.Caret, field.Anchor, ok, nativeStyle.Value.Caret.Background.Fill, nativeStyle.Value.Selection.Background.Fill)
	}
	var treeIconFound bool
	for _, access := range module.ContentQuery[htmlui.Component[treeProps, struct{}]](content) {
		component := access.Ref()
		if component.Props.Theme != model.workbenchTheme {
			t.Fatal("directory row retained the previous theme")
		}
		treeIconFound = true
	}
	if !treeIconFound {
		t.Fatal("fixture has no directory row")
	}
	for _, access := range module.ContentQuery[htmlui.Component[tabProps, struct{}]](content) {
		if access.Ref().Props.Theme != model.workbenchTheme {
			t.Fatal("tab retained the previous theme")
		}
	}
	if allocations := testing.AllocsPerRun(100, func() {
		model.syncWorkbenchTheme(content)
		model.themedIcon("icons/close.svg", "icon.foreground")
		model.selectTheme("light-plus")
	}); allocations != 0 {
		t.Fatalf("unchanged theme allocated %g times", allocations)
	}
}

func TestPencodeIconOnlyThemeChangeUpdatesTexture(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model, provider := session.active.model, session.active.icons
	original, ok := provider.ResolveHTMLImage(model.themedIcon("icons/settings.svg", "icon.foreground"))
	if !ok {
		t.Fatal("settings icon was not decoded")
	}
	data, err := os.ReadFile("../../../syntax/resources/themes/monokai.json")
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"icon.foreground": "`+model.syntaxTheme.Color("icon.foreground").Hex()+`"`, `"icon.foreground": "#ff0000"`, 1))
	theme, err := syntax.LoadTheme(fstest.MapFS{"resources/red.json": &fstest.MapFile{Data: data}}, "resources/red.json")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparePencodeTheme("red-icons", theme, model.themeSourceCSS)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.css != model.workbenchTheme.css {
		t.Fatal("fixture changed more than icon colors")
	}
	model.themes = append(model.themes, prepared)
	model.selectTheme("red-icons")
	markPencodeRootDirtyByModel(model.fontResources, model)
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	changed, ok := provider.ResolveHTMLImage(model.themedIcon("icons/settings.svg", "icon.foreground"))
	if !ok || changed == original {
		t.Fatal("icon color reused the previous texture")
	}
	var found bool
	for _, access := range module.ContentQuery[scene.Sprite2D](model.fontResources) {
		if access.Ref().Texture == original {
			t.Fatal("rendered icon retained its old texture")
		}
		found = found || access.Ref().Texture == changed
	}
	if !found {
		t.Fatal("new icon texture was not rendered")
	}
}

func TestPencodeSettingsIndicatorsAndOptionsFollowThemes(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	model.settingsOpen = true
	for _, id := range []string{"light-plus", "dark-plus", "monokai"} {
		model.selectTheme(id)
		markPencodeRootDirtyByModel(model.fontResources, model)
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
		found := 0
		for entity, access := range module.ContentQuery[scene.UIImage2D](model.fontResources) {
			parent, ok := module.ContentGet[scene.Parent](model.fontResources, entity)
			if !ok {
				continue
			}
			control, ok := module.ContentGet[htmlui.Control](model.fontResources, parent.Entity)
			if !ok || control.Tag != "select" {
				continue
			}
			image := access.Ref()
			want := pencodeSyntaxColor(model.syntaxTheme.Color("settings.dropdownForeground"))
			if image.Tint != want {
				t.Fatalf("%s selector %s indicator tint = %v, want %v", id, control.ID, image.Tint, want)
			}
			found++
		}
		if found == 0 {
			t.Fatal("no rendered settings indicators")
		}
		palette, ok := module.ContentGet[engineui.HTMLSelectStyle](model.fontResources, model.surface)
		if !ok {
			t.Fatal("HTML surface has no themed option palette")
		}
		for _, state := range []engineui.ControlState{{Selected: true}, {Hovered: true}, {Pressed: true}} {
			background := palette.Item.Resolve(state).Background.Fill
			foreground := palette.Text.Resolve(state)
			if background != pencodeSyntaxColor(model.syntaxTheme.Color("list.activeSelectionBackground")) || foreground != pencodeSyntaxColor(model.syntaxTheme.Color("list.activeSelectionForeground")) {
				t.Fatalf("%s option state %+v has stale theme colors: %v, %v", id, state, background, foreground)
			}
		}
	}
}
