package workbench

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
	htmlui "github.com/diiyw/onix/ui/html"
)

func hoverNodeText(nodes []htmlui.VNode) string {
	var text strings.Builder
	for _, node := range nodes {
		text.WriteString(node.Text)
		text.WriteString(hoverNodeText(node.Children))
	}
	return text.String()
}

func hoverTokenStyle(nodes []htmlui.VNode, text string) string {
	for _, node := range nodes {
		if node.Text == text {
			for _, attribute := range node.Attributes {
				if attribute.Name == "style" {
					return attribute.Value
				}
			}
		}
		if style := hoverTokenStyle(node.Children, text); style != "" {
			return style
		}
	}
	return ""
}

func TestLanguageHoverCodeUsesThemeAndCaptureDefinitions(t *testing.T) {
	model := workspaceModel{backend: newPencodeBackend(t)}
	t.Cleanup(model.closeHoverWorker)
	h := &model.languageUI.hover
	code := "func (t time.Time) Before(u time.Time) bool"
	h.result, _ = json.Marshal(map[string]any{"contents": map[string]any{"kind": "markdown", "value": "```go\n" + code + "\n```\nBefore reports whether the time instant t is before u."}})
	previous := ""
	for _, id := range []string{"monokai", "dark-plus", "light-plus"} {
		model.syntaxTheme, _ = syntax.BuiltinTheme(id)
		settleHover(t, &model)
		if len(h.nodes) != 2 || h.nodes[0].Tag != "pre" || h.nodes[1].Tag != "p" {
			t.Fatalf("unexpected Markdown structure: %+v", h.nodes)
		}
		if got := hoverNodeText(h.nodes[0].Children); got != code+"\n" {
			t.Fatalf("signature changed: %q", got)
		}
		style := hoverTokenStyle(h.nodes, "func")
		if style == "" || style == previous {
			t.Fatalf("%s did not recolor the signature keyword: %q", id, style)
		}
		if hoverTokenStyle(h.nodes, "Before") == "" || hoverTokenStyle(h.nodes, "bool") == "" {
			t.Fatal("method name or return type has no syntax style")
		}
		previous = style
	}
	custom, err := syntax.LoadTheme(fstest.MapFS{"custom.json": {Data: []byte(`{"tokenColors":[{"scope":"hover.keyword","settings":{"foreground":"#123456"}}]}`)}}, "custom.json")
	if err != nil {
		t.Fatal(err)
	}
	model.syntaxTheme = custom
	overrides := &syntax.Definitions{Captures: map[string]string{"keyword": "hover.keyword", "keyword.function": "hover.keyword"}}
	model.syntaxDefinitions = syntax.DefaultDefinitions().WithOverrides(overrides)
	settleHover(t, &model)
	if style := hoverTokenStyle(h.nodes, "func"); !strings.Contains(style, "#123456") {
		t.Fatalf("custom capture definitions were ignored: %q", style)
	}
	if allocations := testing.AllocsPerRun(100, model.highlightLanguageHover); allocations != 0 {
		t.Fatalf("cached hover highlighting allocated %g times", allocations)
	}
}

func TestLanguageHoverContentFormats(t *testing.T) {
	for _, test := range []struct {
		name, contents, want string
		highlight            bool
	}{
		{"plaintext", `{"kind":"plaintext","value":"func example() bool\n\n<literal> & **text**"}`, "func example() bool\n\n<literal> & **text**", false},
		{"marked string", `{"language":"go","value":"var 中文 = 1"}`, "var 中文 = 1", true},
		{"marked array", `[{"language":"go","value":"var n = 1"},"Some **documentation**."]`, "var n = 1Some documentation.", true},
		{"escaped code", "{\"kind\":\"markdown\",\"value\":\"```go\\nvar text = \\\"<tag> & 中文\\\"\\n```\"}", "var text = \"<tag> & 中文\"\n", true},
		{"Markdown string", "\"```go\\nvar n = 1\\n```\"", "var n = 1\n", true},
		{"unknown language", "{\"kind\":\"markdown\",\"value\":\"```unknown-language\\n<code> & text\\n```\"}", "<code> & text\n", false},
		{"implicit language", "{\"kind\":\"markdown\",\"value\":\"```\\nvar n = 1\\n```\"}", "var n = 1\n", true},
		{"empty", `null`, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := workspaceModel{backend: newPencodeBackend(t)}
			t.Cleanup(model.closeHoverWorker)
			h := &model.languageUI.hover
			h.file = &openFile{Path: "example.go"}
			h.result = json.RawMessage(`{"contents":` + test.contents + `}`)
			settleHover(t, &model)
			if got := hoverNodeText(h.nodes); got != test.want {
				t.Fatalf("text = %q, want %q", got, test.want)
			}
			if got := hoverTokenStyle(h.nodes, "var") != ""; got != test.highlight {
				t.Fatalf("highlight = %v, want %v", got, test.highlight)
			}
		})
	}
}

func TestLanguageHoverMarkdownResponseReplacesRenderedContent(t *testing.T) {
	for _, test := range []struct {
		name   string
		indent int
		zoom   int
		width  int
		lines  int
	}{{"left", 0, 100, 1440, 0}, {"right", 180, 100, 1440, 0}, {"zoom in", 100, 150, 1440, 0}, {"zoom out", 300, 70, 1440, 0}, {"narrow", 0, 100, 900, 0}, {"lower half", 0, 100, 1440, 24}, {"bottom edge", 0, 100, 1440, 32}} {
		t.Run(test.name, func(t *testing.T) { testLanguageHoverRenderedBounds(t, test.indent, test.zoom, test.width, test.lines) })
	}
}

func testLanguageHoverRenderedBounds(t *testing.T, indent, zoom, width, lines int) {
	app, model, provider, state := languageInteractionFixture(t)
	world := model.fontResources
	model.app.GetViewport = func() (int, int) { return width, 900 }
	ecs.InsertResource(app.World, scene.ViewportSize{Width: float32(width), Height: 900, ScaleFactor: 1})
	for _, camera := range ecs.Query[scene.Camera](app.World).All {
		camera.Mut().UpdateViewport(width, 900)
	}
	model.setZoom(world, zoom)
	value := "package demo\n" + strings.Repeat("\n", lines) + strings.Repeat(" ", indent) + "func Before(u int) bool { return true }\n"
	model.updateActiveContent(value)
	model.setEditorValueAndCaret(world, value, 0)
	model.syncLanguageDocuments()
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	editorNode, _ := world.HTMLNode(model.surface, "editor")
	editorRect, _ := editorNode.Bounds()
	text, ox, oy := model.languageEditorGeometry(world, editorRect)
	x, y, _, height, ok := scene.TextCaretRect2D(text, value, strings.Index(value, "Before")+2)
	if !ok {
		t.Fatal("editor text layout unavailable")
	}
	scale := float64(zoom) / 100
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(ox+x+1) * scale, Y: float64(oy+y+height/2) * scale})
	languageInteractionStep(t, app)
	h := &model.languageUI.hover
	h.due = time.Now().Add(-time.Millisecond)
	languageInteractionStep(t, app)
	job := languageInteractionJob(t, provider, "textDocument/hover")
	hoverScroll := func() scene.UIScroll {
		node, _ := world.HTMLNode(model.surface, "lsp-hover")
		entity, ok := node.ScrollContainer()
		if !ok {
			t.Fatal("hover scroll container is missing")
		}
		scroll, ok := ecs.Get[scene.UIScroll](app.World, entity)
		if !ok {
			t.Fatal("hover scroll state is missing")
		}
		return scroll
	}
	for _, contents := range []struct {
		kind, value string
		lines       int
		fullWidth   bool
	}{
		{kind: "markdown", value: "```go\nfunc Before(u time.Time) bool\n```\nBefore reports whether t is before u."},
		{kind: "markdown", value: "```go\nfunc Before(progress func(string), format string, " + strings.Repeat("argument string, ", 30) + "args ...any)\n```", fullWidth: true},
		{kind: "markdown", value: "```go\nfunc Before(u time.Time) bool\n```\nWakeAt schedules one update at the earliest requested deadline. It can be called from another goroutine; ECS writes must stay on the app's loop thread.\n\n[(onix.App).WakeAt on pkg.go.dev](https://pkg.go.dev/github.com/diiyw/onix#App.WakeAt)"},
		{kind: "markdown", value: "```go\nfunc Before(u time.Time) bool\n```\n" + strings.Repeat("Documentation.\n\n", 10), lines: 10},
		{kind: "markdown", value: "```go\nfunc Before(u time.Time) bool\n```\n" + strings.Repeat("Documentation.\n\n", 100), lines: 100},
		{kind: "plaintext", value: "a short explanation"},
	} {
		result, _ := json.Marshal(map[string]any{"contents": map[string]string{"kind": contents.kind, "value": contents.value}})
		model.acceptLanguageResult(world, pencodeLanguageEvent{session: provider, job: job, result: result})
		settleHover(t, model)
		markPencodeRootDirtyByModel(world, model)
		languageInteractionStep(t, app)
		if !hoverScroll().DisableInput {
			t.Fatal("hover was visible and interactive at its provisional position")
		}
		languageInteractionStep(t, app)
		if hoverScroll().DisableInput {
			t.Fatal("measured hover did not become visible")
		}
		if !h.open || len(h.nodes) == 0 {
			t.Fatal("hover response was not rendered")
		}
		node, ok := world.HTMLNode(model.surface, "lsp-hover")
		bounds, _ := node.Bounds()
		if !ok || bounds.Width <= 0 || bounds.Height <= 0 {
			t.Fatalf("invalid hover bounds: %+v", bounds)
		}
		body, _ := world.HTMLNode(model.surface, "editor-body")
		viewport, _ := body.Bounds()
		maxWidth := viewport.Width * 0.8
		if bounds.Width > maxWidth+.01 || contents.fullWidth && bounds.Width < maxWidth-2 {
			t.Fatalf("hover width does not follow the 80%% editor limit: hover=%+v viewport=%+v", bounds, viewport)
		}
		if bounds.X < viewport.X || bounds.X+bounds.Width > viewport.X+viewport.Width {
			t.Fatalf("hover escaped the editor: hover=%+v viewport=%+v", bounds, viewport)
		}
		if bounds.Y < viewport.Y || bounds.Y+bounds.Height > viewport.Y+viewport.Height || bounds.Height > viewport.Height-8 {
			t.Fatalf("hover exceeded the editor height: hover=%+v viewport=%+v", bounds, viewport)
		}
		scroll := hoverScroll()
		if scroll.Horizontal || scroll.Vertical != (contents.lines == 100) {
			t.Fatalf("unexpected hover scrollbars for %d paragraphs: %+v", contents.lines, scroll)
		}
		contentNode, _ := world.HTMLNode(model.surface, "lsp-hover-body")
		if _, ok := contentNode.ScrollContainer(); ok {
			t.Fatal("hover content created an inset scroll container")
		}
		if contents.lines > 0 && bounds.Height <= 260 {
			t.Fatalf("hover retained its fixed height limit: %+v", bounds)
		}
		if contents.lines == 100 {
			if bounds.Height < viewport.Height-11 {
				t.Fatalf("long hover did not use the available editor height: hover=%+v viewport=%+v", bounds, viewport)
			}
			if delta := scroll.ViewportSize.X() - (bounds.Width - 2); delta < -.01 || delta > .01 {
				t.Fatalf("hover scrollport does not reach the inside border: bounds=%+v scroll=%+v", bounds, scroll)
			}
		}
		for frame := range 16 {
			languageInteractionStep(t, app)
			next, _ := node.Bounds()
			if next != bounds {
				t.Fatalf("stationary hover moved or resized at frame %d: before=%+v after=%+v", frame, bounds, next)
			}
		}
		if allocations := testing.AllocsPerRun(100, func() { model.constrainLanguagePopups(world) }); allocations != 0 {
			t.Fatalf("stationary hover placement allocated %g times", allocations)
		}
		if contents.kind == "markdown" {
			if hoverTokenStyle(h.nodes, "Before") == "" {
				t.Fatal("rendered hover lost its code highlighting")
			}
		} else {
			if len(h.nodes) != 1 || hoverNodeText(h.nodes) != "a short explanation" {
				t.Fatal("new response retained previous Markdown content")
			}
			if bounds.Width > 240 {
				t.Fatalf("short hover did not shrink to its content: %+v", bounds)
			}
			if bounds.Height > 80 {
				t.Fatalf("short hover retained the previous content height: %+v", bounds)
			}
		}
	}
}
