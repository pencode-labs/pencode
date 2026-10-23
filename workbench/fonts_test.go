package workbench

import (
	"go/format"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diiyw/onix/font"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/projects/pencode/extensions"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/text"
	htmlui "github.com/diiyw/onix/ui/html"
)

func TestPencodePlatformFontPreference(t *testing.T) {
	for _, goos := range []string{"darwin", "ios", "windows", "linux", "android"} {
		t.Run(goos, func(t *testing.T) {
			model := workspaceModel{fonts: make(map[string]pencodeFont)}
			for _, id := range []string{"Menlo", "Monaco", "Courier New"} {
				model.fonts[id] = pencodeFont{regular: &text.Font{}}
			}
			want := "Courier New"
			if goos == "darwin" || goos == "ios" {
				want = "Menlo"
			}
			if got := model.resolveDefaultFont(goos); got != want || defaultPencodeFont(goos) != want {
				t.Fatalf("default = %q, want %q", got, want)
			}
			delete(model.fonts, "Menlo")
			delete(model.fonts, "Courier New")
			if got := model.resolveDefaultFont(goos); got != "Monaco" {
				t.Fatalf("available fallback = %q, want Monaco", got)
			}
		})
	}
}

func TestPencodeConsolasChineseFallback(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model, world := session.active.model, session.active.model.fontResources
	if !model.selectFont("Consolas", false) {
		t.Skip("Consolas is not installed")
	}
	selected := model.codeFont()
	if selected.GlyphIndex('中') != 0 {
		t.Fatal("fixture requires Consolas without Chinese coverage")
	}
	chinese, id := selected.ResolveGlyph('中')
	if chinese == selected.Face || id == 0 {
		t.Fatal("Consolas did not acquire a Chinese system fallback")
	}
	const value = "A中文B"
	model.updateActiveContent(value)
	model.setEditorValueAndCaret(world, value, 0)
	for _, zoom := range []int{100, 150} {
		model.setZoom(world, zoom)
		markPencodeRootDirtyByModel(world, model)
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
		text := module.ContentMut[scene.Text2D](world, model.editor.text)
		if text.Font != selected {
			t.Fatal("editor lost the selected Latin font")
		}
		pen, previous, caret := float32(0), rune(-1), 0
		scaled := selected.FontFace(text.FontSize, text.DPI)
		for _, r := range value {
			owner, glyphID := selected.ResolveGlyph(r)
			metrics, err := owner.HorizontalMetrics(glyphID)
			if err != nil {
				t.Fatal(err)
			}
			if previous >= 0 {
				pen += float32(scaled.Kern(previous, r)) / 64
			}
			pen += float32(math.Round(float64(metrics.AdvanceWidth)*text.FontSize*text.DPI/(72*float64(owner.Head.UnitsPerEm))*64) / 64)
			caret++
			x, y, _, height, ok := scene.TextCaretRect2D(text, value, caret)
			if !ok || math.Abs(float64(x-pen)) > .001 {
				t.Fatalf("zoom %d: caret %d x=%v, want %v", zoom, caret, x, pen)
			}
			if got := scene.TextCaretIndex2D(text, value, x, y+height/2); got != caret {
				t.Fatalf("mixed-script hit test = %d, want %d", got, caret)
			}
			previous = r
		}
		before, _, _, _, _ := scene.TextCaretRect2D(text, value, 1)
		after, _, _, _, _ := scene.TextCaretRect2D(text, value, 3)
		rects, ok := scene.MeasureTextRangeRects2D(text, 1, len("A中文"))
		// Range rectangles describe padded ink, which can overhang advances.
		if !ok || len(rects) != 1 || rects[0].Width <= 0 || rects[0].X >= after || rects[0].X+rects[0].Width <= before {
			t.Fatalf("Chinese ink bounds %v fall outside caret positions %v,%v", rects, before, after)
		}
	}
}

func TestPencodeSystemFontSelectionAndPersistence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	model := workspaceModel{}
	model.settings, model.settingsPath = loadPencodeSettings()
	if err := model.initializeFonts(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(model.disposeFonts)
	model.rebuildFontOptions()
	installed, err := font.SystemFonts()
	if err != nil {
		t.Fatal(err)
	}
	families := make(map[string]bool)
	for _, f := range installed {
		families[f.Family] = true
	}
	if len(model.codeFontOptions) != len(families) || len(model.uiFontOptions) != len(families) {
		t.Fatal("font selectors do not match installed families")
	}
	loaded := 0
	for _, f := range model.fonts {
		if f.regular != nil {
			loaded++
		}
	}
	if loaded != 1 {
		t.Fatalf("startup loaded %d families, want only the default", loaded)
	}
	selected := ""
	for _, option := range model.uiFontOptions {
		id := option.Attributes[0].Value
		if id != model.defaultFontID && model.selectFont(id, true) {
			selected = id
			break
		}
	}
	if selected == "" {
		t.Skip("no second usable system font")
	}
	if filepath.Base(filepath.Dir(model.settingsPath)) != "resources" {
		t.Fatal("settings are outside resources")
	}
	model.saveSettings()
	saved, _ := loadPencodeSettings()
	restored := workspaceModel{fonts: model.fonts, settings: saved}
	if err := restored.initializeFonts(); err != nil {
		t.Fatal(err)
	}
	uiFont, _ := restored.uiFont()
	if saved.UIFont != selected || uiFont != model.fonts[selected].regular || restored.codeFont() != model.defaultFont {
		t.Fatal("saved family selection or shared cache was lost")
	}
	if allocations := testing.AllocsPerRun(100, func() {
		model.selectFont(model.defaultFontID, true)
		model.selectFont(selected, true)
		_, _ = model.uiFont()
		_ = model.codeFont()
	}); allocations != 0 {
		t.Fatalf("cached system font selection allocated %g times", allocations)
	}
	if model.selectFont("missing-font", false) {
		t.Fatal("selected a font outside the system catalog")
	}
}

func TestPencodeDefaultCodeFontAlignsColumns(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model, world := session.active.model, session.active.model.fontResources
	uiFont, _ := model.uiFont()
	if model.codeFont() != model.defaultFont || uiFont != model.defaultFont {
		t.Fatal("code editor and UI do not share the system default font")
	}
	model.settings.CodeFont, model.settings.UIFont = "missing-font", "missing-font"
	model.saveSettings()
	model.settings, model.settingsPath = loadPencodeSettings()
	if err := model.initializeFonts(); err != nil {
		t.Fatal(err)
	}
	if model.settings.CodeFont != model.defaultFontID || model.settings.UIFont != model.defaultFontID {
		t.Fatal("invalid saved fonts did not reset to the shared monospace default")
	}
	face := model.codeFont().FontFace(13, 72)
	defer face.Close()
	cell, _ := face.GlyphAdvance(' ')
	for character := rune('!'); character <= '~'; character++ {
		if width, ok := face.GlyphAdvance(character); !ok || width != cell {
			t.Fatalf("advance of %q = %v, want %v", character, width, cell)
		}
	}
	formatted, err := format.Source([]byte(`package demo
const (
StageStartup Stage = "startup"
StageInputGUI Stage = "input_gui"
StageInputUnhandled Stage = "input_unhandled"
StageBeforeFixedUpdateLoop Stage = "before_fixed_update_loop"
StageRender Stage = "render"
)`))
	if err != nil {
		t.Fatal(err)
	}
	value := string(formatted)
	model.updateActiveContent(value)
	model.setEditorValueAndCaret(world, value, 0)
	for _, zoom := range []int{100, 150, 70} {
		model.setZoom(world, zoom)
		markPencodeRootDirtyByModel(world, model)
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
		valueText := module.ContentMut[scene.Text2D](world, model.editor.text)
		binding, _ := module.ContentGet[htmlui.ControlTextFont](world, model.editor.control)
		if valueText.Font != model.codeFont() || binding.Font != model.codeFont() {
			t.Fatal("editor layout and rendering disagree on the code font")
		}
		spaceWidth, _, _ := scene.MeasureTextValue2D(valueText, " ")
		if math.Abs(float64(valueText.TabWidth-2*spaceWidth)) > .001 {
			t.Fatalf("tab width = %v, want two code-font cells (%v)", valueText.TabWidth, 2*spaceWidth)
		}
		column := float32(-1)
		for start := 0; start < len(value); {
			offset := strings.Index(value[start:], "Stage =")
			if offset < 0 {
				break
			}
			caret := start + offset // The fixture contains only ASCII.
			x, y, _, height, ok := scene.TextCaretRect2D(valueText, value, caret)
			if !ok {
				t.Fatal("missing column geometry")
			}
			if column >= 0 && math.Abs(float64(x-column)) > .001 {
				t.Fatalf("zoom %d: type column x=%v, want %v", zoom, x, column)
			}
			if got := scene.TextCaretIndex2D(valueText, value, x, y+height/2); got != caret {
				t.Fatalf("zoom %d: hit test returned %d, want %d", zoom, got, caret)
			}
			column, start = x, caret+len("Stage =")
		}
		if column < 0 {
			t.Fatal("fixture has no type columns")
		}
	}
}

func TestPencodeFontExtensionRestoresSelectionAndUnloads(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	if len(session.views) < 2 {
		t.Fatal("fixture must retain the launcher alongside the workspace")
	}
	const id = "extensions/fonts/multi"
	systemFontCount := len(model.fonts)
	model.settings.CodeFont, model.settings.UIFont = id, id
	model.saveSettings()
	saved, _ := loadPencodeSettings()
	if saved.CodeFont != id || saved.UIFont != id || model.codeFont() != model.defaultFont {
		t.Fatal("pending font selection was lost")
	}
	face, err := font.Load("../../../testdata/fonts/Noto_Sans_SC/static/NotoSansSC-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	if err := model.importExtension(&extensions.Bundle{Fonts: []extensions.Font{{ID: id, Label: "Multilingual", Regular: face, Bold: face}}}); err != nil {
		t.Fatal(err)
	}
	selected := model.fonts[id]
	if selected.regular != selected.bold {
		t.Fatal("shared face created duplicate glyph caches")
	}
	surface, _ := module.ContentGet[htmlui.Surface](model.fontResources, model.surface)
	if surface.Font != selected.regular || surface.BoldFont != selected.bold || model.codeFont() != selected.regular {
		t.Fatal("font did not restore after registration")
	}
	markPencodeRootDirtyByModel(model.fontResources, model)
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	// A retained view may still render an extension face after its scene loses
	// the foreground. Unload must rebind that view before closing the face.
	for view := range session.views {
		if view == session.active {
			continue
		}
		surface := module.ContentMut[htmlui.Surface](view.model.fontResources, view.model.surface)
		surface.Font, surface.BoldFont = selected.regular, selected.bold
	}
	if allocations := testing.AllocsPerRun(100, func() {
		model.selectFont(model.defaultFontID, false)
		model.selectFont(id, false)
		_, _ = model.uiFont()
		_ = model.codeFont()
	}); allocations != 0 {
		t.Fatalf("font selection/lookups allocated %g times", allocations)
	}
	if model.selectFont("extensions/missing/font", true) {
		t.Fatal("selected an unregistered font")
	}
	model.removeExtension("fonts")
	if model.settings.CodeFont != model.defaultFontID || model.settings.UIFont != model.defaultFontID || len(model.fonts) != systemFontCount || face.Data != nil {
		t.Fatal("unload did not reset selection and dispose font")
	}
	if binding, _ := module.ContentGet[htmlui.ControlTextFont](model.fontResources, model.editor.control); binding.Font != model.defaultFont {
		t.Fatal("unloaded code font did not fall back to the system monospace font")
	}
	for _, access := range module.ContentQuery[scene.Text2D](model.fontResources) {
		if access.Get().Font == selected.regular {
			t.Fatal("text still references disposed font")
		}
	}
	surface, _ = module.ContentGet[htmlui.Surface](model.fontResources, model.surface)
	if surface.Font != model.defaultFont || surface.BoldFont != nil {
		t.Fatal("surface still references disposed font")
	}
	for view := range session.views {
		surface, _ := module.ContentGet[htmlui.Surface](view.model.fontResources, view.model.surface)
		if surface.Font == selected.regular || surface.BoldFont == selected.bold {
			t.Fatal("retained view still references disposed font")
		}
	}
	saved, _ = loadPencodeSettings()
	if saved.CodeFont != model.defaultFontID || saved.UIFont != model.defaultFontID {
		t.Fatal("fallback selection was not saved")
	}
	markPencodeRootDirtyByModel(model.fontResources, model)
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	model.screen = pencodeLauncher
	if err := app.Modules().UpdateFrame(1.0 / 60); err != nil {
		t.Fatal(err)
	}
	if err := app.Modules().Dispatch(); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	if session.active.model.screen != pencodeLauncher {
		t.Fatal("retained launcher did not resume")
	}
}
