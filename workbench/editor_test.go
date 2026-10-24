package workbench

import (
	"strings"
	"testing"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

func TestPencodeTabsPreserveEditorView(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	world := model.fontResources
	model.app.GetViewport = func() (int, int) { return 1440, 900 }
	ecs.InsertResource(app.World, scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	for _, camera := range ecs.Query[scene.Camera](app.World).All {
		camera.Mut().UpdateViewport(1440, 900)
	}
	project := model.activeProjectState()
	first := model.activeFile()
	first.Content = strings.Repeat(strings.Repeat("x", 256)+"\n", 150)
	model.setEditorValueAndCaret(world, first.Content, 80)
	module.ContentMut[engineui.TextField](world, model.editor.control).Anchor = 5
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	// Scroll away from the caret, as with the wheel or scrollbar thumb.
	textarea := module.ContentMut[engineui.Textarea](world, model.editor.control)
	textarea.ScrollX, textarea.ScrollY = 100, 600
	languageInteractionStep(t, app)
	want := first.editorView
	if want.caret != 80 || want.anchor != 5 || want.scrollX != 100 || want.scrollY != 600 {
		t.Fatalf("could not establish a scrolled view: %+v", want)
	}
	other := &openFile{Path: "other.go", Name: "other.go", Content: "中🙂"}
	project.Files[other.Path] = other
	project.Tabs = append(project.Tabs, other.Path)
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	selectTab := func(file *openFile) {
		t.Helper()
		// Exercise the tab event, including its immediate SetControlValue, which
		// clamps the shared control's caret when the destination is shorter.
		world.Focus(0)
		var component ecs.Entity
		for entity, access := range module.ContentQuery[htmlui.Component[tabProps, struct{}]](world) {
			if access.Get().Props.Path == file.Path {
				component = entity
				break
			}
		}
		if component == 0 {
			t.Fatalf("missing tab for %s", file.Path)
		}
		handleTabEvent(&module.ContentEventContext[tabProps, struct{}]{World: world, Component: component,
			Props: &tabProps{Model: model, Project: project, Path: file.Path}, Ref: "select"})
		for range 3 {
			languageInteractionStep(t, app)
		}
	}
	assertView := func(want pencodeEditorView) {
		t.Helper()
		field := module.ContentRef[engineui.TextField](world, model.editor.control)
		textarea := module.ContentRef[engineui.Textarea](world, model.editor.control)
		got := pencodeEditorView{field.Caret, field.Anchor, textarea.ScrollX, textarea.ScrollY}
		if got != want || !field.Focused {
			t.Fatalf("editor view = %+v (focused=%v), want %+v", got, field.Focused, want)
		}
		link, _ := module.ContentGet[htmlui.ControlText](world, model.editor.text)
		transform := module.ContentRef[scene.UITransform](world, model.editor.text)
		if transform.Translation.X.Value != link.Content.X-want.scrollX || transform.Translation.Y.Value != link.Content.Y-want.scrollY {
			t.Fatalf("text transform does not match restored scroll: %+v", transform)
		}
	}
	selectTab(other)
	assertView(pencodeEditorView{})
	model.setEditorValueAndCaret(world, other.Content, 2)
	module.ContentMut[engineui.TextField](world, model.editor.control).Anchor = 1
	languageInteractionStep(t, app)
	for range 2 {
		selectTab(first)
		assertView(want)
		selectTab(other)
		assertView(pencodeEditorView{caret: 2, anchor: 1})
	}
	selectTab(first)
	assertView(want)
	// Comparison tabs hide the shared file editor and later reuse it.
	source := model.sourceState()
	tab := &sourceTab{Key: "changes::" + first.Path, Kind: "changes", Path: first.Path, Local: first.Content, Result: first.Content}
	source.Tabs, source.Active = []*sourceTab{tab}, tab.Key
	model.sourceChanged()
	languageInteractionStep(t, app)
	selectTab(first)
	assertView(want)
	// A background reload can shorten an inactive file; clamp by rune count.
	selectTab(other)
	first.Content = "中🙂"
	selectTab(first)
	assertView(pencodeEditorView{caret: 2, anchor: 2})
	if allocations := testing.AllocsPerRun(100, func() { model.editor.saveView(world, model) }); allocations != 0 {
		t.Fatalf("saving editor view allocated %g times", allocations)
	}
}

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
	languageInteractionStep(t, app)
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
	if field, _ := module.ContentGet[engineui.TextField](world, model.editor.control); !field.Focused || field.Caret != 60 || field.Anchor != 60 {
		t.Fatalf("returning to a file lost its caret: %+v", field)
	}
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
