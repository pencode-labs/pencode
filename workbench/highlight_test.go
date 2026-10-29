package workbench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
	"github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/lsp"
)

func pencodeTestColour(spans []scene.TextSpan2D, offset int) graphics.ColorRGBA {
	for _, span := range spans {
		if span.Start <= offset && offset < span.End {
			return span.Color
		}
	}
	return graphics.ColorRGBA{}
}

func TestPencodeRetypedMethodRestoresHighlight(t *testing.T) {
	const prefix = "package demo\nfunc (app *App) Frame() error {\n\t_, err := app"
	const suffix = "\n\treturn err\n}\n"
	for _, parseEachEdit := range []bool{false, true} {
		file := &openFile{Path: "main.go", Name: "main.go", Content: prefix + ".Step()" + suffix}
		model := workspaceModel{activePath: file.Path, files: map[string]*openFile{file.Path: file}}
		t.Cleanup(model.closeHighlightWorker)
		settlePencodeHighlight(t, &model)
		expected := slices.Clone(model.highlightScratch)
		for _, edit := range []string{"", ".", ".S", ".St", ".Ste", ".Step", ".Step(", ".Step()"} {
			model.updateActiveContent(prefix + edit + suffix)
			if parseEachEdit {
				model.highlighted = false
				settlePencodeHighlight(t, &model)
			}
		}
		model.highlighted = false
		settlePencodeHighlight(t, &model)
		if !slices.Equal(model.highlightScratch, expected) {
			t.Fatalf("retyped method changed highlight (parseEachEdit=%v):\nactual: %+v\nexpected: %+v", parseEachEdit, model.highlightScratch, expected)
		}
	}
}

func TestPencodeRetypedMethodRefreshesSemanticHighlight(t *testing.T) {
	const prefix = "package demo\nfunc (app *App) Frame() error {\n\t_, err := app"
	const suffix = "\n\treturn err\n}\n"
	file := &openFile{Path: "main.go", Name: "main.go", Content: prefix + ".Step()" + suffix}
	model := workspaceModel{activePath: file.Path, files: map[string]*openFile{file.Path: file}}
	defer model.closeHighlightWorker()
	model.syntaxTheme, _ = syntax.BuiltinTheme("dark-plus")
	settlePencodeHighlight(t, &model)
	semantic := &pencodeSemanticTokens{
		session: &pencodeLanguageSession{client: &lsp.Client{Capabilities: lsp.ServerCapabilities{
			"semanticTokensProvider": json.RawMessage(`{"legend":{"tokenTypes":["variable"]}}`),
		}}},
		version: 1,
		tokens:  []lsp.SemanticToken{{Start: len(prefix) - len("app"), End: len(prefix), Type: 0}},
	}
	doc := &pencodeLanguageDocument{file: file, content: file.Content, version: 1, semantic: semantic}
	model.languages = &pencodeLanguageState{documents: map[*openFile]*pencodeLanguageDocument{file: doc}}
	expected := slices.Clone(model.languageHighlight(model.highlightScratch))
	model.updateActiveContent(prefix + suffix)
	model.updateActiveContent(prefix + ".Step()" + suffix)
	doc.version++
	semantic.version = doc.version
	semantic.theme, semantic.baseSource = nil, ""
	// Semantic tokens can arrive while syntax still has the temporary colors
	// inherited from app. The parser must replace that merged paint afterward.
	if slices.Equal(model.languageHighlight(model.highlightScratch), expected) {
		t.Fatal("fixture did not cache temporary edit colors")
	}
	model.highlighted = false
	settlePencodeHighlight(t, &model)
	if !slices.Equal(model.highlightScratch, expected) {
		t.Fatal("syntax parser did not restore the method color")
	}
	if actual := model.languageHighlight(model.highlightScratch); !slices.Equal(actual, expected) {
		t.Fatalf("semantic cache retained temporary edit colors:\nactual: %+v\nexpected: %+v", actual, expected)
	}
	if allocs := testing.AllocsPerRun(100, func() { model.languageHighlight(model.highlightScratch) }); allocs != 0 {
		t.Fatalf("cached semantic highlighting allocated %g times", allocs)
	}
}

func TestPencodeEditKeepsHighlightUntilParsed(t *testing.T) {
	for _, edit := range []struct{ name, before, after string }{
		{"insert", "// ab\nvar n = 1\n", "// axb\nvar n = 1\n"},
		{"append", "// ab", "// ab中"},
		{"unicode", "// 中文\nvar n = 1\n", "// 中新增文\nvar n = 1\n"},
		{"replace", "// ab\nvar n = 1\n", "// 中文\nvar n = 1\n"},
		{"multiline", "// ab\nvar n = 1\n", "// a\n// b\nvar n = 1\n"},
		{"delete", "// ab\nvar n = 1\n", "// a\nvar n = 1\n"},
	} {
		t.Run(edit.name, func(t *testing.T) {
			file := &openFile{Path: "main.go", Name: "main.go", Content: edit.before}
			model := workspaceModel{activePath: file.Path, files: map[string]*openFile{file.Path: file}}
			defer model.closeHighlightWorker()
			settlePencodeHighlight(t, &model)
			colour := pencodeTestColour(model.highlightScratch, 3)
			if colour == (graphics.ColorRGBA{}) || colour == graphics.White {
				t.Fatal("fixture comment has no highlight")
			}
			if !model.updateActiveContent(edit.after) || !model.highlightPending {
				t.Fatal("edit did not schedule highlighting")
			}
			// Every byte of the edited comment remains colored before parsing.
			end := strings.Index(edit.after, "var")
			if end < 0 {
				end = len(edit.after)
			}
			for offset := 0; offset < end; offset++ {
				if edit.after[offset] != '\n' && pencodeTestColour(model.highlightScratch, offset) != colour {
					t.Fatalf("edited comment lost its colour at byte %d: %v", offset, model.highlightScratch)
				}
			}
			if model.highlightedContent != edit.after || model.lineCount != strings.Count(edit.after, "\n")+1 || model.characterCount != utf8.RuneCountInString(edit.after) {
				t.Fatal("rebased source or document counts are stale")
			}
			model.highlighted = false
			settlePencodeHighlight(t, &model)
			if pencodeTestColour(model.highlightScratch, 3) != colour {
				t.Fatal("final parser result changed the comment colour")
			}
		})
	}
}

func TestPencodeThemeSelectionAndEditorColors(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	content := model.fontResources
	model.settingsOpen = true
	markPencodeRootDirtyByModel(content, model)
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	comment := strings.Index(model.activeContent(), "//")
	before := pencodeTestColour(model.highlightScratch, comment)
	context := module.ContentEventContext[appProps, appState]{
		World: content, Props: &appProps{Model: model}, Ref: "theme-select",
		Event: htmlui.Event{Kind: htmlui.EventChange, Value: "light-plus"},
	}
	if !model.handleSettingsEvent(&context) {
		t.Fatal("theme selector change was not handled")
	}
	markPencodeRootDirtyByModel(content, model)
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	settlePencodeHighlight(t, model)
	if pencodeTestColour(model.highlightScratch, comment) == before {
		t.Fatal("theme selection did not update syntax colors")
	}
	text, _ := content.Get[scene.Text2D](model.editor.text)
	if text.Color != graphics.Black {
		t.Fatalf("plain editor text did not adopt the light theme: %+v", text.Color)
	}
	numbers, _ := content.Get[scene.Text2D](model.editor.numbers)
	if numbers.Color != pencodeSyntaxColor(model.syntaxTheme.Color("editorLineNumber.foreground")) {
		t.Fatal("gutter did not adopt the theme")
	}
	selector, ok := content.HTMLControl(model.surface, "theme-select")
	if !ok {
		t.Fatal("theme selector is missing")
	}
	field, _ := content.Get[ui.Selector](selector)
	control, _ := content.Get[htmlui.Control](selector)
	if field.Selected != 2 || len(control.Options) != 3 {
		t.Fatalf("theme options or selection are stale: %+v", field)
	}
	saved, _ := loadPencodeSettings()
	if saved.Theme != "light-plus" {
		t.Fatal("selected theme was not persisted")
	}
	if allocs := testing.AllocsPerRun(100, func() { renderPencodeHighlight(model) }); allocs != 0 {
		t.Fatalf("unchanged editor highlighting allocated %g times", allocs)
	}
}

func TestPencodeLoadsCustomThemeAndCaptureDefinitions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	settingsPath, err := pencodeSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(settingsPath)
	if err := os.MkdirAll(filepath.Join(root, "themes"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"themes/custom.jsonc": `{"name":"Custom", "colors":{"editor.foreground":"#abcdef"}, "tokenColors":[{"scope":"custom.comment", "settings":{"foreground":"#123456"}},],}`,
		"highlights.json":     `{"captures":{"comment":"custom.comment"}, "sourceScopes":{"go":"source.go"}}`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	model := workspaceModel{settings: defaultPencodeSettings(), settingsPath: settingsPath}
	model.settings.Theme = "themes/custom.jsonc"
	if err := model.loadSyntaxSettings(); err != nil {
		t.Fatal(err)
	}
	defer model.closeHighlightWorker()
	file := &openFile{Path: "main.go", Name: "main.go", Content: "// comment\n"}
	model.files, model.activePath = map[string]*openFile{file.Path: file}, file.Path
	settlePencodeHighlight(t, &model)
	if model.syntaxTheme.Name != "Custom" || len(model.themeOptions) != 4 || pencodeTestColour(model.highlightScratch, 3) != graphics.ColorFromRGBA(0x12, 0x34, 0x56, 255) {
		t.Fatal("custom theme and capture scopes were not applied")
	}
}

func TestPencodeInputFramePreservesHighlight(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	editor := &model.editor
	content := model.fontResources
	step := func() {
		t.Helper()
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
	}
	if !content.Focus(editor.control) {
		t.Fatal("editor did not receive keyboard focus")
	}
	step()
	prefix := strings.Index(model.activeContent(), "text") + 2
	field := content.Mut[ui.TextField](editor.control)
	source := field.Value
	field.Caret = utf8.RuneCountInString(field.Value[:prefix])
	field.Anchor = field.Caret
	colour := pencodeTestColour(model.highlightScratch, prefix)
	keywordColour := pencodeTestColour(model.highlightScratch, 0)
	state, _ := app.World.GetResource[*input.State]()
	assertColour := func(offset int) {
		t.Helper()
		value, _ := content.Get[scene.Text2D](editor.text)
		if pencodeTestColour(value.Spans, offset) != colour {
			t.Fatalf("input frame lost colour at %d: %v", offset, value.Spans)
		}
	}
	state.Enqueue(input.Event{Kind: input.EventTextComposition, Composition: input.TextComposition{Text: "中文", Active: true}})
	step()
	assertColour(prefix)
	assertColour(prefix + len("中文"))
	assertColour(prefix - 1)
	value, _ := content.Get[scene.Text2D](editor.text)
	if value.Value != source[:prefix]+"中文"+source[prefix:] || pencodeTestColour(value.Spans, 0) != keywordColour || model.activeContent() != source {
		field, _ := content.Get[ui.TextField](editor.control)
		t.Fatalf("composition changed the source or cleared unrelated syntax colours: value=%q caret=%d composition=%q colour=%v want=%v sourceChanged=%v", value.Value[:min(80, len(value.Value))], field.Caret, field.Composition, pencodeTestColour(value.Spans, 0), keywordColour, model.activeContent() != source)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		field, _ := content.Get[ui.TextField](editor.control)
		value := content.Mut[scene.Text2D](editor.text)
		editor.syncHighlight(value, field, model)
	}); allocs != 0 {
		t.Fatalf("composition highlight allocated %g times", allocs)
	}
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "中文"})
	step()
	assertColour(prefix)
	assertColour(prefix + len("中文"))
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "x"})
	step()
	assertColour(prefix + len("中文"))
	committed, _ := content.Get[ui.TextField](editor.control)
	if committed.Composition != "" || committed.Value != source[:prefix]+"中文x"+source[prefix:] || model.activeContent() != committed.Value {
		t.Fatal("committed composition did not reach the editor and source in the same frame")
	}
	model.highlightDue = time.Time{}
	step()
	assertColour(prefix)
}

func TestPencodeSpanRebaseReusesStorage(t *testing.T) {
	source := []scene.TextSpan2D{{Start: 0, End: 8, Color: graphics.White}, {Start: 8, End: 16, Color: graphics.Black}}
	dst := make([]scene.TextSpan2D, 0, len(source)+1)
	if allocs := testing.AllocsPerRun(100, func() {
		dst = rebasePencodeSpans(dst[:0], source, 4, 4, 7)
	}); allocs != 0 {
		t.Fatalf("span rebase allocated %g times", allocs)
	}
	if len(dst) != 2 || dst[0].End != 11 || dst[1].Start != 11 || dst[1].End != 19 {
		t.Fatalf("rebasing fragmented or shifted tokens incorrectly: %v", dst)
	}
}
