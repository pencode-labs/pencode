package workbench

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/diiyw/onix/font"
	"github.com/diiyw/onix/html/dom"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestExtensionSelectableTextWhitespace(t *testing.T) {
	for _, test := range []struct{ name, html, want string }{
		{"paragraphs", "<p>第一段</p>\n<p>第二段</p>", "第一段\n\n第二段"},
		{"line breaks", "<p>第一行<br>\n第二行<br>\n<br>\n第四行</p>", "第一行\n第二行\n\n第四行"},
		{"inline whitespace", "<p><strong>one</strong>\n<em>two</em>   three</p>", "one two three"},
		{"code", "<p>代码</p>\n<pre><code>first\n\n  last\n</code></pre>\n<p>结束</p>", "代码\n\nfirst\n\n  last\n\n结束"},
		{"streaming", "<div data-pencode-preserve-whitespace>\nfirst\n\n  last\n</div>", "\nfirst\n\n  last\n"},
		{"table", "<p>结果</p><table><tr><th>名称</th><th>是否通过</th></tr>\n<tr><td>8 个中文</td><td>✅</td></tr>\n<tr><td>超出</td><td>❌</td></tr></table><p>结束</p>", "结果\n\n名称\t是否通过\n8 个中文\t✅\n超出\t❌\n\n结束"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tree, err := dom.Parse(`<div data-ref="transcript">` + test.html + `</div>`)
			if err != nil {
				t.Fatal(err)
			}
			surface := &pencodeExtensionSurface{tree: tree}
			if got := surface.selectableText("transcript"); got != test.want {
				t.Fatalf("copied %q, want %q", got, test.want)
			}
		})
	}
}

func TestExtensionSelectionRangeRetainsWhitespace(t *testing.T) {
	s := pencodeExtensionTextSelection{
		content: "\nfirst\n\n  last\n",
		spans:   []pencodeExtensionTextSpan{{offset: 1, value: "first"}, {offset: 10, value: "last"}},
		anchor:  pencodeExtensionTextPoint{0, 2}, caret: pencodeExtensionTextPoint{1, 2},
	}
	if got := s.text(); got != "rst\n\n  la" {
		t.Fatalf("partial selection lost whitespace: %q", got)
	}
	s.selectAll()
	if got := s.text(); got != s.content {
		t.Fatalf("select all lost surrounding whitespace: %q", got)
	}
	if allocations := testing.AllocsPerRun(100, func() { _ = s.text() }); allocations != 0 {
		t.Fatalf("copy allocated %g", allocations)
	}
}

func TestExtensionTranscriptSelection(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	m.app.GetViewport = func() (int, int) { return 1440, 900 }
	app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	for _, camera := range app.World.Query[scene.Camera]().All {
		camera.Mut().UpdateViewport(1440, 900)
	}
	state, _ := app.World.GetResource[*input.State]()
	css, err := os.ReadFile("testdata/codex/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	show := func(tail string) {
		t.Helper()
		if err := m.applyExtensionUI("codex", &extensions.ActionSession{}, peb.UI{ID: "composer", Kind: peb.EditorTab, View: &peb.EditorView{
			HTML: `<main class="codex-workspace" data-pencode-layout="fill"><section class="codex-chat-pane"><div class="codex-transcript" data-ref="transcript" data-pencode-selectable><article class="codex-chat"><section data-ref="message" data-pencode-region><p>你好🙂 世界</p><p>正文 <strong>加粗</strong> <button type="button" class="codex-link" data-ref="link" role="link">main.go</button></p><pre><code>echo hello

输出 中文</code></pre><p>第一行<br>
第二行<br><br>第四行</p><table><tr><th>名称</th><th>是否通过</th></tr><tr><td>正常</td><td>✅</td></tr><tr><td>超出</td><td>❌</td></tr></table><p>` + tail + `</p></section></article></div><section class="codex-composer"><textarea data-ref="prompt" class="codex-prompt">keep draft</textarea></section></section></main>`,
			CSS: string(css), Events: []peb.EditorBinding{{Ref: "link", Kind: "click"}, {Ref: "prompt", Kind: "input"}},
		}}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(w, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
	}
	show("tail-end")
	installed, _ := font.SystemFonts()
	for _, source := range installed {
		if source.Family != "Apple Color Emoji" && source.Family != "Noto Color Emoji" && source.Family != "Segoe UI Emoji" {
			continue
		}
		for _, r := range "✅❌" {
			found := false
			for _, access := range w.Query[scene.Text2D]() {
				text := access.Ref()
				if !strings.ContainsRune(text.Value, r) {
					continue
				}
				found = true
				if text.Font == nil || !text.Font.GlyphCache(text.FontSize, text.DPI).Glyph(r).RGBA() {
					t.Fatalf("table emoji %U did not reach a color glyph atlas", r)
				}
			}
			if !found {
				t.Fatalf("table emoji %U disappeared from rendered text", r)
			}
		}
		break
	}
	selection := &m.actions.ui.selection
	var copied string
	state.SetClipboard(func(value string) bool { copied = value; return true }, nil)
	point := func(label string, caret int) (float64, float64) {
		t.Helper()
		for _, values := range w.Query2[scene.Text2D, scene.UIGlobalTransform]() {
			text := values.A.Ref()
			if text.Value != label {
				continue
			}
			x, y, _, height, ok := scene.TextCaretRect2D(text, text.Text(), caret)
			if !ok {
				t.Fatal("missing text geometry")
			}
			if caret > 0 {
				x -= .2
			}
			matrix := values.B.Ref().Matrix
			return float64(matrix[12] + (x+.1)*matrix[0]), float64(matrix[13] + (y+height*.5)*matrix[5])
		}
		t.Fatalf("missing text %q", label)
		return 0, 0
	}
	drag := func(from string, start int, to string, end int) {
		t.Helper()
		selection.lastClick = selection.lastClick.Add(-1e9)
		x, y := point(from, start)
		state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
		languageInteractionStep(t, app)
		x, y = point(to, end)
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x, Y: y})
		languageInteractionStep(t, app)
		state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
		languageInteractionStep(t, app)
	}
	shortcut := func(key input.KeyCode) {
		t.Helper()
		primary := input.KeyLeftControl
		if runtime.GOOS == "darwin" {
			primary = input.KeyLeftSuper
		}
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: primary})
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
		languageInteractionStep(t, app)
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: primary})
		languageInteractionStep(t, app)
	}
	drag("好", 0, "🙂", 1)
	shortcut(input.KeyC)
	if copied != "好🙂" {
		t.Fatalf("Unicode selection copied %q", copied)
	}
	if len(selection.quads) == 0 {
		t.Fatal("selection has no highlight")
	}
	m.extensionFeedback = ""
	drag("main.go", 2, "main.go", 5)
	if m.extensionFeedback != "" {
		t.Fatal("dragging link text activated the link")
	}
	selection.lastClick = selection.lastClick.Add(-1e9)
	editorContextClick(t, app, m, state, m.actions.ui.editor.prefix+"control-link")
	for range 3 {
		languageInteractionStep(t, app)
	}
	if m.extensionFeedback == "" {
		t.Fatal("ordinary link clicks were suppressed")
	}

	for _, zoom := range []int{70, 150, 100} {
		m.setZoom(w, zoom)
		languageInteractionStep(t, app)
		drag("tail-end", 8, "你", 0)
		shortcut(input.KeyC)
		if !strings.HasPrefix(copied, "你好🙂 世界\n\n正文 ") || !strings.Contains(copied, "加粗") || !strings.Contains(copied, "main.go") || !strings.HasSuffix(copied, "tail-end") || strings.Contains(copied, "keep draft") {
			t.Fatalf("zoom %d: copied %q", zoom, copied)
		}
		if !strings.Contains(copied, "echo hello\n\n输出 中文\n\n第一行\n第二行\n\n第四行\n\n") {
			t.Fatalf("zoom %d: clipboard lost blank lines or line breaks: %q", zoom, copied)
		}
		if !strings.Contains(copied, "名称\t是否通过\n正常\t✅\n超出\t❌\n\n") {
			t.Fatalf("zoom %d: clipboard lost table boundaries or emoji: %q", zoom, copied)
		}
	}
	drag("你", 0, "🙂", 1)
	x, y := point("好", 0)
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseRight, X: x, Y: y})
	languageInteractionStep(t, app)
	if !m.inputContext.open || m.inputContext.selection != selection {
		t.Fatal("missing selection context menu")
	}
	m.runInputContextEdit(app.World, "input-context-copy")
	if copied != "你好🙂" {
		t.Fatalf("context copy: %q", copied)
	}
	languageInteractionStep(t, app)
	shortcut(input.KeyA)
	shortcut(input.KeyC)
	if !strings.Contains(copied, "输出 中文") || !strings.Contains(copied, "tail-end") {
		t.Fatalf("select all: %q", copied)
	}
	if n := testing.AllocsPerRun(100, func() { selection.sync(w, m) }); n != 0 {
		t.Fatalf("warm selection highlight allocated %g", n)
	}
	prompt, _ := w.HTMLControl(m.surface, m.actions.ui.editor.prefix+"control-prompt")
	w.Focus(prompt)
	field := w.Mut[engineui.TextField](prompt)
	field.Anchor, field.Caret = 0, 4
	shortcut(input.KeyC)
	if copied != "keep" {
		t.Fatalf("selection stole composer copy: %q", copied)
	}
	show("更新之后")
	if selection.valid(w) {
		t.Fatal("stale selection survived changed text")
	}
	longText := strings.Repeat("wrapped words ", 300) + "end-of-transcript"
	show(longText)
	node, _ := w.HTMLNode(m.surface, m.actions.ui.editor.prefix+"control-transcript")
	area, _ := node.ScrollContainer()
	before := w.Ref[scene.UIScroll](area).Position
	drag("输出", 0, "你", 0)
	if after := w.Ref[scene.UIScroll](area).Position; after != before {
		t.Fatalf("selecting transcript text scrolled the list: %v -> %v", before, after)
	}
	drag("你", 0, "🙂", 1)
	shortcut(input.KeyA)
	shortcut(input.KeyC)
	if !strings.HasSuffix(copied, longText) {
		t.Fatalf("copy lost offscreen text or inserted soft line breaks: bytes=%d content=%d spans=%d last=%q", len(copied), len(selection.content), len(selection.spans), copied[max(0, len(copied)-80):])
	}
	if n := testing.AllocsPerRun(100, func() { _ = selection.text() }); n != 0 {
		t.Fatalf("copying a selected range allocated %g", n)
	}
}
