package workbench

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/diiyw/onix"
	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/lsp"
	"github.com/pencode-labs/pencode/peb"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

func languageInteractionFixture(t *testing.T) (*onix.App, *workspaceModel, *pencodeLanguageSession, *input.State) {
	t.Helper()
	app, session := newPencodeTestApp(t)
	model := session.active.model
	server := &extensions.LanguageServer{LanguageServer: peb.LanguageServer{ID: "test/lsp", LanguageID: "go", Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".go"}}}}
	provider := &pencodeLanguageSession{key: pencodeLanguageKey{model.projectPath, server.ID}, server: server, jobs: make(chan pencodeLanguageJob, 64), cancel: func() {}, client: &lsp.Client{Encoding: lsp.UTF16, Capabilities: lsp.ServerCapabilities{
		"completionProvider":    json.RawMessage(`{"triggerCharacters":["."]}`),
		"signatureHelpProvider": json.RawMessage(`{"triggerCharacters":["("],"retriggerCharacters":[","]}`),
		"hoverProvider":         json.RawMessage(`true`), "referencesProvider": json.RawMessage(`true`), "definitionProvider": json.RawMessage(`true`), "documentRangeFormattingProvider": json.RawMessage(`true`),
	}}}
	model.languages = &pencodeLanguageState{servers: []*extensions.LanguageServer{server}, sessions: map[pencodeLanguageKey]*pencodeLanguageSession{provider.key: provider}, documents: make(map[*openFile]*pencodeLanguageDocument), events: make(chan pencodeLanguageEvent, 64)}
	model.syncLanguageDocuments()
	model.fontResources.Focus(model.editor.control)
	model.app.GetViewport = func() (int, int) { return 1440, 900 }
	ecs.InsertResource(app.World, scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	for _, camera := range ecs.Query[scene.Camera](app.World).All {
		camera.Mut().UpdateViewport(1440, 900)
	}
	languageInteractionStep(t, app)
	state, ok := ecs.GetResource[*input.State](app.World)
	if !ok {
		t.Fatal("missing input state")
	}
	return app, model, provider, state
}

func languageInteractionStep(t *testing.T, app *onix.App) {
	t.Helper()
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
}

func languageInteractionJob(t *testing.T, session *pencodeLanguageSession, method string) pencodeLanguageJob {
	t.Helper()
	for {
		select {
		case job := <-session.jobs:
			if job.method == method {
				return job
			}
		default:
			t.Fatalf("no queued %s request", method)
			return pencodeLanguageJob{}
		}
	}
}

func TestLanguageUnderlineStyleReusesString(t *testing.T) {
	style := languageUnderlineStyle("", 12.5, 24.25, 60.75, 1)
	if style != "left:12.50px;top:24.25px;width:60.75px;height:1.00px;" {
		t.Fatalf("unexpected underline style: %s", style)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		style = languageUnderlineStyle(style, 12.5, 24.25, 60.75, 1)
	}); allocations != 0 {
		t.Fatalf("unchanged underline style allocated %g times", allocations)
	}
}

func TestLanguageHoverPointUsesActualSize(t *testing.T) {
	body := htmlui.Rect{X: 200, Y: 80, Width: 1000, Height: 600}
	for _, test := range []struct {
		name                string
		x, y, width, height float32
		wantX, wantY        float32
	}{
		{"short near right", 750, 100, 180, 50, 750, 112},
		{"short near bottom", 750, 500, 180, 50, 750, 512},
		{"right edge", 950, 100, 180, 50, 816, 112},
		{"above pointer", 750, 560, 180, 50, 750, 498},
		{"large result", 750, 500, 560, 260, 436, 228},
		{"oversized result", 2, 2, 1200, 800, 4, 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			x, y := languageHoverPoint(test.x, test.y, body, test.width, test.height)
			if x != test.wantX || y != test.wantY {
				t.Fatalf("hover at (%g, %g), want (%g, %g)", x, y, test.wantX, test.wantY)
			}
		})
	}
}

func TestLanguageAutomaticCompletionAndNativeAcceptance(t *testing.T) {
	for _, key := range []input.KeyCode{input.KeyEnter, input.KeyTab} {
		t.Run(fmt.Sprint(key), func(t *testing.T) {
			app, model, provider, state := languageInteractionFixture(t)
			world := model.fontResources
			value := "package demo\n\nfunc main() { fmt.P }\n"
			model.updateActiveContent(value)
			caret := strings.Index(value, "fmt.P") + len("fmt.P")
			model.setEditorValueAndCaret(world, value, caret)
			model.syncLanguageDocuments()
			state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "r"})
			languageInteractionStep(t, app)
			if model.languageUI.completionFile != model.activeFile() {
				t.Fatalf("native typing did not schedule suggestions: state=%+v field=%+v content=%q", model.languageUI, module.ContentRef[engineui.TextField](world, model.editor.control), model.activeContent())
			}
			model.languageUI.completionDue = time.Now().Add(-time.Millisecond)
			languageInteractionStep(t, app)
			job := languageInteractionJob(t, provider, "textDocument/completion")
			var items []map[string]any
			for i := range 25 {
				items = append(items, map[string]any{"label": fmt.Sprintf("Print%02d", i)})
			}
			raw, _ := json.Marshal(items)
			model.languages.events <- pencodeLanguageEvent{session: provider, method: "result", job: job, result: raw}
			languageInteractionStep(t, app)
			if !model.languages.panel.completion || len(model.languages.panel.rows) != 25 {
				t.Fatal("suggestions were not displayed")
			}
			field, _ := module.ContentGet[engineui.TextField](world, model.editor.control)
			originalCaret := field.Caret
			for range 13 {
				state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyDown})
				state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyDown})
				languageInteractionStep(t, app)
			}
			field, _ = module.ContentGet[engineui.TextField](world, model.editor.control)
			if model.languages.panel.completionIndex != 13 || field.Caret != originalCaret {
				t.Fatal("suggestion navigation moved the document caret")
			}
			state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
			state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
			languageInteractionStep(t, app)
			want := strings.Replace(value, "fmt.P", "fmt.Print13", 1)
			if model.activeContent() != want {
				t.Fatalf("completion inserted an extra newline/tab or wrong item:\n%q\nwant %q", model.activeContent(), want)
			}
			if model.languages.panel.completion {
				t.Fatal("completion stayed open after acceptance")
			}
		})
	}
}

func TestLanguageHoverAndControlClickUseRenderedSymbols(t *testing.T) {
	app, model, provider, state := languageInteractionFixture(t)
	world := model.fontResources
	longSymbol := strings.Repeat("longSymbol", 8)
	value := "package demo\n\tvar 中文变量 = 1\n" + strings.Repeat("// wrapped text ", 30) + "\n\tvar wrappedSymbol = 中文变量\n" + strings.Repeat(" ", 65) + "edgeSymbol\nvar " + longSymbol + " = 1\nvar path = \"github.com/diiyw/onix/scene\"\n"
	model.updateActiveContent(value)
	// Compare Ctrl styling against final tokens, not a debounced edit preview.
	model.highlightDue = time.Time{}
	model.setEditorValueAndCaret(world, value, 0)
	model.syncLanguageDocuments()
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	for _, zoom := range []int{100, 150, 70} {
		model.setZoom(world, zoom)
		languageInteractionStep(t, app)
		for _, name := range []string{"中文变量", "wrappedSymbol", "edgeSymbol", longSymbol, "github.com/diiyw/onix/scene"} {
			index := strings.LastIndex(value, name)
			caret := utf8.RuneCountInString(value[:index]) + utf8.RuneCountInString(name)/2
			model.setEditorValueAndCaret(world, value, caret)
			model.revealEditorCaret(world, caret)
			languageInteractionStep(t, app)
			editorNode, _ := world.HTMLNode(model.surface, "editor")
			bodyNode, _ := world.HTMLNode(model.surface, "editor-body")
			editorRect, _ := editorNode.Bounds()
			bodyRect, _ := bodyNode.Bounds()
			text, ox, oy := model.languageEditorGeometry(world, editorRect)
			x, y, _, h, ok := scene.TextCaretRect2D(text, value, caret)
			if !ok {
				t.Fatal("text layout unavailable")
			}
			px, py := ox+x+1, oy+y+h*.5
			offset, start, end, underline, valid := model.languageSymbolAtPointer(world, px, py, editorRect, bodyRect)
			if !valid || start != index || end != index+len(name) || underline.Width < 1 {
				t.Fatalf("zoom %d %s: offset=%d range=%d:%d valid=%v", zoom, name, offset, start, end, valid)
			}
			scale := float64(zoom) / 100
			state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(px) * scale, Y: float64(py) * scale})
			languageInteractionStep(t, app)
			model.languageUI.hover.due = time.Now().Add(-time.Millisecond)
			languageInteractionStep(t, app)
			job := languageInteractionJob(t, provider, "textDocument/hover")
			if job.offset < index || job.offset >= index+len(name) {
				t.Fatalf("hover sent rune offset as bytes: %d", job.offset)
			}
			model.languages.events <- pencodeLanguageEvent{session: provider, method: "result", job: job, result: json.RawMessage(`{"contents":{"kind":"plaintext","value":"var symbol int"}}`)}
			languageInteractionStep(t, app)
			languageInteractionStep(t, app)
			if !model.languageUI.hover.open || model.languageUI.hover.body != "var symbol int" {
				t.Fatal("plain hover did not show content")
			}
			hoverNode, _ := world.HTMLNode(model.surface, "lsp-hover")
			hoverRect, _ := hoverNode.Bounds()
			if hoverRect.Width <= 0 || hoverRect.Width > 240 {
				t.Fatalf("short hover expanded beyond its content at zoom %d: %+v", zoom, hoverRect)
			}
			wantX, wantY := languageHoverPoint(px-bodyRect.X, py-bodyRect.Y, bodyRect, hoverRect.Width, hoverRect.Height)
			// Bounds include the containing block's border and rounded CSS coordinates.
			if dx, dy := hoverRect.X-bodyRect.X-wantX, hoverRect.Y-bodyRect.Y-wantY; dx < -1.1 || dx > 1.1 || dy < -1.1 || dy > 1.1 {
				t.Fatalf("zoom %d %s: hover detached from mouse: bounds=%+v body=%+v, want local (%g, %g)", zoom, name, hoverRect, bodyRect, wantX, wantY)
			}
			rendered, _ := module.ContentGet[scene.Text2D](world, model.editor.text)
			originalSpans := slices.Clone(rendered.Spans)
			state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyLeftControl})
			languageInteractionStep(t, app)
			if !model.languageUI.hover.link {
				t.Fatal("control hover did not underline a symbol with a definition provider")
			}
			rendered, _ = module.ContentGet[scene.Text2D](world, model.editor.text)
			for offset := range value {
				want := pencodeTestColour(originalSpans, offset)
				if offset >= index && offset < index+len(name) {
					want = model.workbenchTheme.link
				}
				if got := pencodeTestColour(rendered.Spans, offset); got != want {
					t.Fatalf("Ctrl hover changed the wrong color at byte %d: got %v want %v", offset, got, want)
				}
			}
			field, _ := module.ContentGet[engineui.TextField](world, model.editor.control)
			valueText := module.ContentMut[scene.Text2D](world, model.editor.text)
			if allocations := testing.AllocsPerRun(100, func() { model.editor.syncHighlight(valueText, field, model) }); allocations != 0 {
				t.Fatalf("cached Ctrl hover highlighting allocated %g times", allocations)
			}
			// Releasing Ctrl also restores colors while the pointer is in the tooltip.
			state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(hoverRect.X+hoverRect.Width/2) * scale, Y: float64(hoverRect.Y+hoverRect.Height/2) * scale})
			state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyLeftControl})
			languageInteractionStep(t, app)
			rendered, _ = module.ContentGet[scene.Text2D](world, model.editor.text)
			if model.languageUI.hover.link || !slices.Equal(rendered.Spans, originalSpans) {
				t.Fatalf("zoom %d %s: releasing Ctrl did not restore the original syntax highlighting", zoom, name)
			}
			state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(px) * scale, Y: float64(py) * scale})
			state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyLeftControl})
			languageInteractionStep(t, app)
			state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: float64(px) * scale, Y: float64(py) * scale})
			state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: float64(px) * scale, Y: float64(py) * scale})
			languageInteractionStep(t, app)
			definition := languageInteractionJob(t, provider, "textDocument/definition")
			if model.languageUI.hover.file != nil || model.languageUI.hover.open || model.languageUI.hover.link || job.context.Err() == nil {
				t.Fatal("control-click did not stop the hover interaction")
			}
			// A response already in transit must not reopen the old hover.
			model.languages.events <- pencodeLanguageEvent{session: provider, method: "result", job: job, result: json.RawMessage(`{"contents":"stale hover"}`)}
			languageInteractionStep(t, app)
			if model.languageUI.hover.file != nil || model.languageUI.hover.open {
				t.Fatal("hover restarted while navigation was loading")
			}
			location := lsp.Location{URI: lsp.FileURI(filepath.Join(model.projectPath, model.activePath)), Range: lsp.Range{Start: lsp.Position{Line: 1, Character: 5}, End: lsp.Position{Line: 1, Character: 9}}}
			other := lsp.Location{URI: location.URI, Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 0}}}
			raw, _ := json.Marshal([]lsp.Location{location, other})
			model.languages.events <- pencodeLanguageEvent{session: provider, method: "result", job: definition, result: raw}
			languageInteractionStep(t, app)
			if model.languages.panel.open || model.languages.panel.pending || model.activeFile() == nil || model.activeFile().Path != "main.go" {
				t.Fatal("control-click did not navigate to the definition")
			}
			field, _ = module.ContentGet[engineui.TextField](world, model.editor.control)
			if field.Caret != utf8.RuneCountInString(model.activeContent()[:strings.Index(model.activeContent(), "\n")+1])+5 {
				t.Fatal("definition navigation did not select the first returned location")
			}
			state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyLeftControl})
			model.dismissLanguagePanel()
			model.cancelLanguageHover()
			languageInteractionStep(t, app)
		}
	}
}

func TestLanguageStringHoverCoversContents(t *testing.T) {
	app, model, _, _ := languageInteractionFixture(t)
	world := model.fontResources
	for _, zoom := range []int{100, 150, 70} {
		model.setZoom(world, zoom)
		for _, literal := range []string{
			`"github.com/diiyw/onix/scene"`,
			`" 你好 世界🙂/path "`,
			`"hello \"world\"/path"`,
			"`first/path\n你好 世界/scene`",
			`"` + strings.Repeat("segment/", 24) + `"`,
		} {
			value := "package demo\nvar value = " + literal + "\n"
			start := strings.Index(value, literal) + 1
			end := start + len(literal) - 2
			model.updateActiveContent(value)
			model.setEditorValueAndCaret(world, value, 0)
			markPencodeRootDirtyByModel(world, model)
			languageInteractionStep(t, app)
			editorNode, _ := world.HTMLNode(model.surface, "editor")
			bodyNode, _ := world.HTMLNode(model.surface, "editor-body")
			editorRect, _ := editorNode.Bounds()
			bodyRect, _ := bodyNode.Bounds()
			text, ox, oy := model.languageEditorGeometry(world, editorRect)
			var px, py float32
			for offset, character := range value[start:end] {
				if character == '\n' {
					continue
				}
				caret := utf8.RuneCountInString(value[:start+offset])
				x, y, _, h, ok := scene.TextCaretRect2D(text, value, caret)
				if !ok {
					t.Fatal("string glyph geometry unavailable")
				}
				// A caret at a soft wrap can sit at the previous line's end;
				// hover the glyph at the following line's start instead.
				for line := 0; line < scene.TextLineCount2D(text); line++ {
					lineStart, _, rect, _ := scene.TextLineRange2D(text, line)
					if lineStart == start+offset {
						x, y, h = rect.X, rect.Y, rect.Height
						break
					}
				}
				px, py = ox+x+1, oy+y+h*.5
				gotOffset, gotStart, gotEnd, _, valid := model.languageSymbolAtPointer(world, px, py, editorRect, bodyRect)
				if !valid || gotStart != start || gotEnd != end || gotOffset != start+offset {
					t.Fatalf("zoom %d, %q at byte %d: offset=%d range=%d:%d valid=%v, want %d:%d", zoom, literal, offset, gotOffset, gotStart, gotEnd, valid, start, end)
				}
			}
			if strings.Contains(literal, "\n") && len(model.languageUI.underlineRects) < 2 {
				t.Fatal("multiline string did not underline all lines")
			}
			if allocations := testing.AllocsPerRun(100, func() {
				model.languageSymbolAtPointer(world, px, py, editorRect, bodyRect)
			}); allocations != 0 {
				t.Fatalf("string hover allocated %g times", allocations)
			}
		}
	}
}

func TestLanguageSymbolActionUsesDeclarationModifiers(t *testing.T) {
	_, model, provider, _ := languageInteractionFixture(t)
	model.updateActiveContent("package demo\nfunc main() { main() }\n")
	model.syncLanguageDocuments()
	doc := model.languages.documents[model.activeFile()]
	provider.client.Capabilities["semanticTokensProvider"] = json.RawMessage(`{"legend":{"tokenTypes":["variable"],"tokenModifiers":["declaration","definition"]},"full":true}`)
	declaration := strings.Index(doc.content, "func main") + len("func ")
	usage := strings.LastIndex(doc.content, "main()")
	doc.semantic = &pencodeSemanticTokens{
		session: provider,
		version: doc.version,
		tokens: []lsp.SemanticToken{
			{Start: declaration, End: declaration + len("main"), Modifiers: 1},
			{Start: usage, End: usage + len("main")},
		},
	}
	if action := languageSymbolAction(doc, declaration+1); action != "textDocument/references" {
		t.Fatalf("declaration selected %s, want references", action)
	}
	if action := languageSymbolAction(doc, usage+1); action != "textDocument/definition" {
		t.Fatalf("usage selected %s, want definition", action)
	}
	doc.semantic.version--
	if action := languageSymbolAction(doc, declaration+1); action != "textDocument/definition" {
		t.Fatalf("stale semantic tokens selected %s, want definition", action)
	}
}

func TestLanguageControlClickDefinitionFallsBackToReferences(t *testing.T) {
	app, model, provider, _ := languageInteractionFixture(t)
	world := model.fontResources
	content := "package demo\nconst StageStartup = \"startup\"\n"
	model.updateActiveContent(content)
	model.setEditorValueAndCaret(world, content, strings.Index(content, "StageStartup")+4)
	model.syncLanguageDocuments()
	languageInteractionStep(t, app)
	model.runLanguageAction(world, "textDocument/definition", false)
	model.languages.panel.controlClick = true
	definition := languageInteractionJob(t, provider, "textDocument/definition")
	location := lsp.Location{
		URI:   definition.uri,
		Range: lsp.Range{Start: lsp.Position{Line: 1, Character: 6}, End: lsp.Position{Line: 1, Character: 18}},
	}
	raw, _ := json.Marshal([]lsp.Location{location})
	model.languages.events <- pencodeLanguageEvent{session: provider, method: "result", job: definition, result: raw}
	languageInteractionStep(t, app)
	references := languageInteractionJob(t, provider, "textDocument/references")
	if !model.languages.panel.pending || references.method != "textDocument/references" {
		t.Fatal("definition self-location did not start a references request")
	}
}

func TestLanguageLocationNavigationCentersTarget(t *testing.T) {
	app, model, _, _ := languageInteractionFixture(t)
	world := model.fontResources
	content := strings.Repeat("package demo\n\n", 60)
	model.updateActiveContent(content)
	model.setEditorValueAndCaret(world, content, 0)
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	location := lsp.Location{URI: lsp.FileURI(filepath.Join(model.projectPath, model.activePath)), Range: lsp.Range{Start: lsp.Position{Line: 50}}}
	if !model.navigateLanguageLocation(world, location, nil) {
		t.Fatal("language location was not accepted")
	}
	languageInteractionStep(t, app)
	text, _ := module.ContentGet[scene.Text2D](world, model.editor.text)
	textarea, _ := module.ContentGet[engineui.Textarea](world, model.editor.control)
	link, _ := module.ContentGet[htmlui.ControlText](world, model.editor.text)
	_, y, _, height, ok := scene.TextCaretRect2D(&text, content, pencodeLineCaret(content, 51))
	if !ok || math.Abs(float64(y+height/2-textarea.ScrollY-link.Content.Height/2)) > 1 {
		t.Fatalf("language target is not centered: y=%v height=%v scroll=%v viewport=%v", y, height, textarea.ScrollY, link.Content.Height)
	}
}

func TestLanguageContextPreservesSelectionAndStaleHoverIsRejected(t *testing.T) {
	app, model, provider, state := languageInteractionFixture(t)
	world := model.fontResources
	value := "package demo\nvar value = 1\n"
	model.updateActiveContent(value)
	caret := strings.Index(value, "value")
	model.setEditorValueAndCaret(world, value, caret+len("value"))
	module.ContentMut[engineui.TextField](world, model.editor.control).Anchor = caret
	model.syncLanguageDocuments()
	languageInteractionStep(t, app)
	editorNode, _ := world.HTMLNode(model.surface, "editor")
	rect, _ := editorNode.Bounds()
	text, ox, oy := model.languageEditorGeometry(world, rect)
	x, y, _, height, _ := scene.TextCaretRect2D(text, value, caret+1)
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseRight, X: float64(ox + x), Y: float64(oy + y + height/2)})
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseRight, X: float64(ox + x), Y: float64(oy + y + height/2)})
	languageInteractionStep(t, app)
	if !model.languageUI.contextOpen || model.languageUI.contextAnchor != caret || model.languageUI.contextCaret != caret+len("value") {
		t.Fatalf("context menu lost the selection: state=%+v field=%+v", model.languageUI, module.ContentRef[engineui.TextField](world, model.editor.control))
	}
	group, _ := world.HTMLNode(model.surface, "editor-context-group-2")
	groupRect, _ := group.Bounds()
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(groupRect.X + groupRect.Width/2), Y: float64(groupRect.Y + groupRect.Height/2)})
	languageInteractionStep(t, app)
	formatNode, ok := world.HTMLNode(model.surface, "lsp-context-10")
	if !ok {
		t.Fatal("missing selection format menu item")
	}
	formatRect, _ := formatNode.Bounds()
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: float64(formatRect.X + formatRect.Width/2), Y: float64(formatRect.Y + formatRect.Height/2)})
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: float64(formatRect.X + formatRect.Width/2), Y: float64(formatRect.Y + formatRect.Height/2)})
	languageInteractionStep(t, app)
	formatJob := languageInteractionJob(t, provider, "textDocument/rangeFormatting")
	params := formatJob.params.(map[string]any)
	if params["range"] != (lsp.Range{Start: lsp.Position{Line: 1, Character: 4}, End: lsp.Position{Line: 1, Character: 9}}) {
		t.Fatalf("wrong format selection: %+v", params["range"])
	}
	model.dismissLanguagePanel()
	model.languageUI.hover = pencodeLanguageHover{file: model.activeFile(), content: value, offset: caret, token: 42}
	doc := model.languages.documents[model.activeFile()]
	job := pencodeLanguageJob{document: doc, content: value, version: doc.version, offset: caret, token: 42, hover: true}
	model.cancelLanguageHover()
	model.acceptLanguageResult(world, pencodeLanguageEvent{job: job, result: json.RawMessage(`{"contents":"stale"}`)})
	if model.languageUI.hover.open {
		t.Fatal("dismissed hover reappeared after the response")
	}
}

func TestLanguageEditorActionsSuspendHoverOutsidePopup(t *testing.T) {
	app, model, provider, state := languageInteractionFixture(t)
	world := model.fontResources
	value := "package demo\nvar symbol = 1\n"
	model.updateActiveContent(value)
	model.setEditorValueAndCaret(world, value, 0)
	model.syncLanguageDocuments()
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	editorNode, _ := world.HTMLNode(model.surface, "editor")
	editorRect, _ := editorNode.Bounds()
	text, ox, oy := model.languageEditorGeometry(world, editorRect)
	offset := strings.Index(value, "symbol") + 1
	x, y, _, height, _ := scene.TextCaretRect2D(text, value, offset)
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(ox + x), Y: float64(oy + y + height/2)})
	languageInteractionStep(t, app)
	initial := model.languageUI.hover
	if initial.file == nil {
		t.Fatal("pointer did not reach the symbol")
	}
	doc := model.languages.documents[model.activeFile()]
	for _, test := range []struct {
		name                                string
		context, panel, completion, pending bool
		delayed                             bool
	}{
		{name: "context menu", context: true},
		{name: "references", panel: true},
		{name: "completion list", completion: true},
		{name: "pending automatic request", pending: true},
		{name: "scheduled automatic request", delayed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			model.languages.panel = pencodeLanguagePanel{open: test.panel, completion: test.completion, pending: test.pending}
			ui := &model.languageUI
			ui.contextOpen, ui.contextFile, ui.contextContent = test.context, initial.file, value
			model.cancelLanguageAutomatic()
			if test.delayed {
				ui.completionFile, ui.completionContent = initial.file, value
				ui.completionCaret = 0
				ui.completionDue = time.Now().Add(time.Hour)
			}
			ui.hover = initial
			cancelled := false
			ui.hover.cancel = func() { cancelled = true }
			if !model.updateLanguageInteractions(world, state) || !cancelled || ui.hover.file != nil || !ui.hover.due.IsZero() {
				t.Fatal("active action did not cancel the hover interaction")
			}
			if allocations := testing.AllocsPerRun(100, func() { model.updateLanguageInteractions(world, state) }); allocations != 0 {
				t.Fatalf("suspended hover allocated %g times", allocations)
			}
			// Guard both dispatch and acceptance, even before the next input update.
			ui.hover = initial
			model.requestLanguageHover()
			for len(provider.jobs) > 0 {
				if job := <-provider.jobs; job.hover {
					t.Fatal("active action allowed a hover request")
				}
			}
			job := pencodeLanguageJob{document: doc, content: value, version: doc.version, offset: initial.offset, token: initial.token, hover: true}
			model.acceptLanguageResult(world, pencodeLanguageEvent{job: job, result: json.RawMessage(`{"contents":"hover"}`)})
			if ui.hover.open {
				t.Fatal("active action accepted a hover response")
			}
		})
	}
	model.dismissLanguageContext()
	model.dismissLanguagePanel()
	model.cancelLanguageAutomatic()
	model.cancelLanguageHover()
	model.updateLanguageInteractions(world, state)
	if model.languageUI.hover.file == nil || model.languageUI.hover.due.IsZero() {
		t.Fatal("hover did not resume after the action ended")
	}
}

func TestLanguageTriggerCharactersWakeAndCancelPendingRequests(t *testing.T) {
	app, model, provider, state := languageInteractionFixture(t)
	world := model.fontResources
	var wake time.Time
	originalWake := model.app.WakeAt
	model.app.WakeAt = func(due time.Time) {
		wake = due
		if originalWake != nil {
			originalWake(due)
		}
	}
	t.Cleanup(func() { model.app.WakeAt = originalWake })
	for _, fixture := range []struct{ typed, method string }{{".", "textDocument/completion"}, {"(", "textDocument/signatureHelp"}, {",", "textDocument/signatureHelp"}} {
		model.cancelLanguageAutomatic()
		model.dismissLanguagePanel()
		value := "package demo\nfunc main() { value }\n"
		model.updateActiveContent(value)
		caret := strings.Index(value, "value") + len("value")
		model.setEditorValueAndCaret(world, value, caret)
		world.Focus(model.editor.control)
		model.syncLanguageDocuments()
		state.Enqueue(input.Event{Kind: input.EventTextInput, Text: fixture.typed})
		languageInteractionStep(t, app)
		if model.languageUI.completionMethod != fixture.method || model.languageUI.trigger != fixture.typed || !wake.Equal(model.languageUI.completionDue) {
			t.Fatalf("trigger %q did not schedule/wake %s: %+v wake=%v", fixture.typed, fixture.method, model.languageUI, wake)
		}
		model.languageUI.completionDue = time.Now().Add(-time.Millisecond)
		languageInteractionStep(t, app)
		job := languageInteractionJob(t, provider, fixture.method)
		context := job.params.(map[string]any)["context"].(map[string]any)
		if context["triggerKind"] != 2 || context["triggerCharacter"] != fixture.typed {
			t.Fatalf("trigger context=%+v", context)
		}
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEscape})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEscape})
		languageInteractionStep(t, app)
		if job.context.Err() == nil {
			t.Fatal("Escape did not cancel the pending automatic request")
		}
		model.languages.events <- pencodeLanguageEvent{session: provider, method: "result", job: job, result: json.RawMessage(`[{"label":"stale"}]`)}
		languageInteractionStep(t, app)
		if model.languages.panel.completion || model.languages.panel.open {
			t.Fatal("cancelled automatic request reopened its popup")
		}
	}
	state.Enqueue(input.Event{Kind: input.EventTextComposition, Composition: input.TextComposition{Active: true, Text: "中"}})
	languageInteractionStep(t, app)
	if model.languageUI.completionFile != nil {
		t.Fatal("IME preedit triggered suggestions")
	}
}

func TestLanguageCompletionResolvesAdditionalEditsAndRejectsChangedContent(t *testing.T) {
	app, model, provider, _ := languageInteractionFixture(t)
	world := model.fontResources
	value := "package demo\n\nvar value = Pr\n"
	model.updateActiveContent(value)
	caret := strings.Index(value, "Pr") + len("Pr")
	model.setEditorValueAndCaret(world, value, caret)
	model.syncLanguageDocuments()
	provider.client.Capabilities["completionProvider"] = json.RawMessage(`{"resolveProvider":true}`)
	model.runLanguageAction(world, "textDocument/completion", false)
	job := languageInteractionJob(t, provider, "textDocument/completion")
	model.languages.events <- pencodeLanguageEvent{session: provider, method: "result", job: job, result: json.RawMessage(`[{"label":"Print","data":{"id":1}}]`)}
	languageInteractionStep(t, app)
	model.chooseLanguageCompletion(world, 0)
	resolve := languageInteractionJob(t, provider, "completionItem/resolve")
	model.languages.events <- pencodeLanguageEvent{session: provider, method: "result", job: resolve, result: json.RawMessage(`{"label":"Print","insertText":"fmt.Print","additionalTextEdits":[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":0}},"newText":"import \"fmt\"\n"}]}`)}
	languageInteractionStep(t, app)
	want := "package demo\nimport \"fmt\"\n\nvar value = fmt.Print\n"
	if model.activeContent() != want {
		t.Fatalf("resolved completion=%q, want %q", model.activeContent(), want)
	}
	model.syncLanguageDocuments()
	model.runLanguageAction(world, "textDocument/completion", false)
	job = languageInteractionJob(t, provider, "textDocument/completion")
	model.activeFile().Content = "package changed\n"
	model.acceptLanguageResult(world, pencodeLanguageEvent{session: provider, job: job, result: json.RawMessage(`[{"label":"outdated"}]`)})
	if model.languages.panel.completion {
		t.Fatal("completion accepted a stale document snapshot")
	}
}

func TestLanguageIdleInteractionDoesNotAllocate(t *testing.T) {
	app, model, _, _ := languageInteractionFixture(t)
	languageInteractionStep(t, app)
	state, _ := module.ContentResource[*input.State](model.fontResources)
	model.updateLanguageInteractions(model.fontResources, state)
	if count := testing.AllocsPerRun(100, func() { model.updateLanguageInteractions(model.fontResources, state) }); count != 0 {
		t.Fatalf("idle editor interaction allocated %g times", count)
	}
}
