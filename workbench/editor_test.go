package workbench

import (
	"strings"
	"testing"

	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

func TestPencodeOpeningFileFocusesEditorAtStart(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model, world := session.active.model, session.active.model.fontResources
	assertStart := func() {
		t.Helper()
		field, _ := module.ContentGet[engineui.TextField](world, model.editor.control)
		if !field.Focused || field.Caret != 0 || field.Anchor != 0 || field.ScrollX != 0 {
			t.Fatalf("opened file is not active at its start: %+v", field)
		}
	}
	assertStart()
	file := model.activeFile()
	model.setEditorValueAndCaret(world, file.Content, 60)
	world.Focus(0)
	// Switching documents reuses the native textarea and its previous scroll.
	other := &openFile{Path: "other.go", Name: "other.go", Content: strings.Repeat("// next\n", 80)}
	project := model.activeProjectState()
	project.Files[other.Path] = other
	project.Tabs = append(project.Tabs, other.Path)
	model.selectTab(other.Path)
	markPencodeRootDirtyByModel(world, model)
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	assertStart()
	model.selectTab(file.Path)
	markPencodeRootDirtyByModel(world, model)
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	assertStart()
	// An ordinary render must preserve the user's caret.
	model.setEditorValueAndCaret(world, file.Content, 5)
	markPencodeRootDirtyByModel(world, model)
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	if field, _ := module.ContentGet[engineui.TextField](world, model.editor.control); !field.Focused || field.Caret != 5 {
		t.Fatalf("render moved caret: %+v", field)
	}
}

func TestPencodeCaretStatus(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	world := model.fontResources
	assertStatus := func(want string) {
		t.Helper()
		// Include the pass requested by a caret change after native input/layout.
		for range 2 {
			if _, err := app.Step(); err != nil {
				t.Fatal(err)
			}
		}
		node, found := world.HTMLNode(model.surface, "caret-info")
		if !found {
			t.Fatal("missing caret status node")
		}
		value, err := node.Serialize()
		if err != nil {
			t.Fatal(err)
		}
		if expected := `<span data-ref="caret-info" id="caret-info">` + want + `</span>`; value != expected {
			t.Fatalf("caret status = %q, want %q", value, expected)
		}
	}
	for _, fixture := range []struct {
		value string
		caret int
		want  string
	}{
		{"", 0, "Ln 1, Col 1"},
		{"中文🙂\r\n\talpha\n", 3, "Ln 1, Col 4"},
		{"中文🙂\r\n\talpha\n", 5, "Ln 2, Col 1"},
		{"中文🙂\r\n\talpha\n", 8, "Ln 2, Col 4"},
		{"中文🙂\r\n\talpha\n", 12, "Ln 3, Col 1"},
		{"中文🙂\r\n\talpha\n", 1, "Ln 1, Col 2"},
		{"a\nb", 1, "Ln 1, Col 2"},
	} {
		model.activeFile().Content = fixture.value
		model.setEditorValueAndCaret(world, fixture.value, fixture.caret)
		// A selection reports the moving caret, regardless of its anchor.
		module.ContentMut[engineui.TextField](world, model.editor.control).Anchor = 0
		markPencodeRootDirtyByModel(world, model)
		assertStatus(fixture.want)
	}
	module.ContentMut[engineui.TextField](world, model.editor.control).Caret = 3
	app.RequestRender()
	assertStatus("Ln 2, Col 2")
	// Root renders must preserve the status even when the caret stays still.
	markPencodeRootDirtyByModel(world, model)
	assertStatus("Ln 2, Col 2")
	if allocations := testing.AllocsPerRun(100, func() {
		model.editor.syncCaretStatus(world, model.surface, model)
	}); allocations != 0 {
		t.Fatalf("unchanged caret status allocated %g times", allocations)
	}
	model.closeTab(model.activePath)
	markPencodeRootDirtyByModel(world, model)
	assertStatus("")
}

func TestPencodeEditorWidthPercentSettingsAndLayout(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	model.settingsOpen = true
	dispatch := func(ref string) {
		t.Helper()
		context := module.ContentEventContext[appProps, appState]{World: model.fontResources, Props: &appProps{Model: model}, Ref: ref, Event: htmlui.Event{Kind: htmlui.EventClick}}
		if !model.handleSettingsEvent(&context) {
			t.Fatalf("unhandled setting: %s", ref)
		}
	}
	for _, fixture := range []struct {
		initial int
		ref     string
		want    int
	}{
		{100, "editor-width-decrease", 95},
		{100, "editor-width-increase", 100},
		{20, "editor-width-decrease", 20},
		{45, "editor-width-increase", 50},
	} {
		model.settings.EditorWidth = fixture.initial
		dispatch(fixture.ref)
		saved, _ := loadPencodeSettings()
		if model.settings.EditorWidth != fixture.want || saved.EditorWidth != fixture.want {
			t.Fatalf("width setting=%d saved=%d want=%d", model.settings.EditorWidth, saved.EditorWidth, fixture.want)
		}
	}
	if model.editorWidthLabel != "50%" {
		t.Fatalf("width label=%q", model.editorWidthLabel)
	}
	for _, zoom := range []int{100, 150, 70} {
		model.setZoom(model.fontResources, zoom)
		markPencodeRootDirtyByModel(model.fontResources, model)
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
		editor := &model.editor
		link, _ := module.ContentGet[htmlui.ControlText](model.fontResources, editor.text)
		text, _ := module.ContentGet[scene.Text2D](model.fontResources, editor.text)
		textarea, _ := module.ContentGet[engineui.Textarea](model.fontResources, editor.control)
		guide, _ := module.ContentGet[scene.UITransform](model.fontResources, editor.guide)
		if text.WrapMode != scene.TextWrapTokens || text.WrapWidth != link.Content.Width*.5 ||
			!textarea.WrapTokens || textarea.WrapWidthPercent != 50 || textarea.DisableHorizontalScroll ||
			guide.Translation.X.Value != link.Content.X+text.WrapWidth {
			t.Fatalf("zoom %d: percentage wrap/guide disagreed with viewport: text=%g viewport=%g", zoom, text.WrapWidth, link.Content.Width)
		}
	}
}
