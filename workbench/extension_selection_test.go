package workbench

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/onix-engine/onix/font"
	"github.com/onix-engine/onix/html/dom"
	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestExtensionSelectableTextWhitespace(t *testing.T) {
	for _, test := range []struct{ name, html, want string }{
		{"paragraphs", "<p>第一段</p>\n<p>第二段</p>", "第一段\n\n第二段"},
		{"line breaks", "<p>第一行<br>\n第二行<br>\n<br>\n第四行</p>", "第一行\n第二行\n\n第四行"},
		{"inline whitespace", "<p><strong>one</strong>\n<em>two</em>   three</p>", "one two three"},
		{"controls", `<p>正文 <button>查看变更</button><button role="link">main.go</button></p><input value="draft"><textarea>draft</textarea><select><option>选项</option></select>`, "正文 main.go"},
		{"code", "<p>代码</p>\n<pre><code>first\n\n  last\n</code></pre>\n<p>结束</p>", "代码\n\nfirst\n\n  last\n\n结束"},
		{"streaming", "<div data-pencode-preserve-whitespace>\nfirst\n\n  last\n</div>", "\nfirst\n\n  last\n"},
		{"code whitespace", "<pre><code> \tfirst  \n\t last \t\n\n</code></pre>", " \tfirst  \n\t last \t\n\n"},
		{"streaming whitespace", "<div data-pencode-preserve-whitespace> \tfirst  \n\t last \t\n\n</div>", " \tfirst  \n\t last \t\n\n"},
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

func TestExtensionSelectionWhitespaceOnly(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	m.app.GetViewport = func() (int, int) { return 1440, 900 }
	app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	for _, camera := range app.World.Query[scene.Camera]().All {
		camera.Mut().UpdateViewport(1440, 900)
	}
	state, _ := app.World.GetResource[*input.State]()
	if err := m.applyExtensionUI("test", &extensions.ActionSession{}, peb.UI{ID: "whitespace", Kind: peb.EditorTab, View: &peb.EditorView{
		HTML: `<div data-ref="transcript" data-pencode-selectable><pre><code>first	 	  last

end</code></pre></div>`,
		CSS: `[data-ref="transcript"] { overflow: auto; height: 300px; } pre { white-space: pre; font-size: 14px; line-height: 24px; }`,
	}}); err != nil {
		t.Fatal(err)
	}
	markPencodeRootDirtyByModel(w, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	point := func(label string, caret int) (float64, float64, float64) {
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
			matrix := values.B.Ref().Matrix
			return float64(matrix[12] + x*matrix[0]), float64(matrix[13] + (y+height*.5)*matrix[5]), float64(height * matrix[5])
		}
		t.Fatalf("missing text %q", label)
		return 0, 0, 0
	}
	var copied string
	state.SetClipboard(func(value string) bool { copied = value; return true }, nil)
	copyRange := func(x, y, endX, endY float64, want string) {
		t.Helper()
		selection := &m.actions.ui.selection
		selection.lastClick = selection.lastClick.Add(-1e9)
		state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
		languageInteractionStep(t, app)
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: endX, Y: endY})
		state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: endX, Y: endY})
		languageInteractionStep(t, app)
		primary := input.KeyLeftControl
		if runtime.GOOS == "darwin" {
			primary = input.KeyLeftSuper
		}
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: primary})
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyC})
		languageInteractionStep(t, app)
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyC})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: primary})
		languageInteractionStep(t, app)
		if copied != want {
			t.Fatalf("copied %q, want whitespace %q", copied, want)
		}
	}
	x, y, _ := point("first", 5)
	endX, endY, _ := point("last", 0)
	copyRange(x+1, y, endX, endY, "\t \t  ")
	x, y, height := point("last", 4)
	endX, endY, _ = point("end", 0)
	copyRange(x+1, y+height*.5+1, endX, endY, "\n\n")
	x, y, _ = point("first", 0)
	endX, endY, _ = point("end", 3)
	copyRange(x+.1, y, endX, endY, "first\t \t  last\n\nend")
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

func TestExtensionSelectionDuringTranscriptUpdates(t *testing.T) {
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
	key := "task"
	frames := 3
	show := func(heading, upper, lower string) {
		t.Helper()
		if err := m.applyExtensionUI("codex", &extensions.ActionSession{}, peb.UI{ID: "composer", Kind: peb.EditorTab, Busy: true, View: &peb.EditorView{
			HTML: `<main class="codex-workspace" data-pencode-layout="fill"><section class="codex-chat-pane"><div class="codex-transcript" data-ref="transcript" data-pencode-selectable data-pencode-scroll="end" data-pencode-scroll-key="` + key + `"><article class="codex-chat codex-chat-running"><section data-ref="heading" data-pencode-region><p>` + heading + `</p></section><section data-ref="upper" data-pencode-region><div class="codex-text" data-pencode-preserve-whitespace>` + upper + `</div></section><section data-ref="lower" data-pencode-region><div class="codex-text" data-pencode-preserve-whitespace>` + lower + `</div></section></article></div><section class="codex-composer"><textarea data-ref="prompt" class="codex-prompt">draft</textarea></section></section></main>`,
			CSS:  string(css), Events: []peb.EditorBinding{{Ref: "prompt", Kind: "input"}},
		}}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(w, m)
		for range frames {
			languageInteractionStep(t, app)
		}
	}
	const upper = "upper-original"
	show("status", upper, "lower-original")
	selection := &m.actions.ui.selection
	var copied string
	state.SetClipboard(func(value string) bool { copied = value; return true }, nil)
	copySelection := func(want string) {
		t.Helper()
		primary := input.KeyLeftControl
		if runtime.GOOS == "darwin" {
			primary = input.KeyLeftSuper
		}
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: primary})
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyC})
		languageInteractionStep(t, app)
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyC})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: primary})
		languageInteractionStep(t, app)
		if copied != want {
			t.Fatalf("copied %q, want %q", copied, want)
		}
	}
	enqueuePointer := func(kind input.EventKind, label string, caret int) {
		t.Helper()
		found := false
		for _, values := range w.Query2[scene.Text2D, scene.UIGlobalTransform]() {
			text := values.A.Ref()
			if text.Value != label {
				continue
			}
			x, y, _, height, ok := scene.TextCaretRect2D(text, text.Text(), caret)
			if !ok {
				t.Fatal("missing text geometry")
			}
			matrix := values.B.Ref().Matrix
			state.Enqueue(input.Event{Kind: kind, Button: input.MouseLeft, X: float64(matrix[12] + (x+.1)*matrix[0]), Y: float64(matrix[13] + (y+height*.5)*matrix[5])})
			found = true
			break
		}
		if !found {
			t.Fatalf("missing text %q", label)
		}
	}
	pointer := func(kind input.EventKind, label string, caret int) {
		t.Helper()
		enqueuePointer(kind, label, caret)
		languageInteractionStep(t, app)
	}
	drag := func(label string, start, end int) {
		t.Helper()
		selection.lastClick = selection.lastClick.Add(-1e9)
		pointer(input.EventMouseDown, label, start)
		pointer(input.EventMouseMove, label, end)
		pointer(input.EventMouseUp, label, end)
	}
	drag("lower-original", 0, 5)
	if got := selection.text(); got != "lower" {
		t.Fatalf("first selection: %q", got)
	}
	copySelection("lower")
	drag(upper, 0, 5)
	if got := selection.text(); got != "upper" {
		t.Fatalf("second selection retained the previous anchor: %q", got)
	}
	copySelection("upper")
	for _, entity := range selection.quads {
		if visible, _ := w.Get[scene.Visibility](entity); visible.Visible {
			parent, _ := w.Get[scene.Parent](entity)
			if text, _ := w.Get[scene.Text2D](parent.Entity); text.Value != upper {
				t.Fatalf("second selection highlighted old text: %q", text.Value)
			}
		}
	}
	// A busy frame can receive the complete next drag in one input batch.
	selection.lastClick = selection.lastClick.Add(-1e9)
	enqueuePointer(input.EventMouseDown, "lower-original", 5)
	enqueuePointer(input.EventMouseMove, upper, 5)
	enqueuePointer(input.EventMouseUp, upper, 5)
	languageInteractionStep(t, app)
	if got := selection.text(); got != "-original\nlower" {
		t.Fatalf("batched drag lost its press position: %q", got)
	}
	copySelection("-original\nlower")
	// The first streamed token replaces the selected message's render entities.
	selection.lastClick = selection.lastClick.Add(-1e9)
	pointer(input.EventMouseDown, upper, 0)
	show("status", upper+" appended", "lower-original")
	pointer(input.EventMouseMove, upper, 5)
	pointer(input.EventMouseUp, upper, 5)
	if !selection.valid(w) || selection.text() != "upper" {
		t.Fatalf("streaming interrupted drag: valid=%v text=%q", selection.valid(w), selection.text())
	}
	show("status", upper+" appended again", "lower-original")
	if !selection.valid(w) || selection.text() != "upper" {
		t.Fatalf("streaming discarded selection: valid=%v text=%q", selection.valid(w), selection.text())
	}
	copySelection("upper")
	// Copy can arrive in the same frame as the next streamed publication.
	frames = 0
	show("status", upper+" appended once more", "lower-original")
	copySelection("upper")
	frames = 3
	// Timer and output regions can both change around the selected text.
	show("longer status", upper+" appended again", "lower-original")
	if !selection.valid(w) || selection.text() != "upper" {
		t.Fatalf("preceding update displaced selection: valid=%v text=%q", selection.valid(w), selection.text())
	}
	copySelection("upper")
	show("longer status", "replaced message", "lower-original")
	if selection.valid(w) {
		t.Fatal("selection survived an edit to selected text")
	}
	drag("lower-original", 5, 0)
	copySelection("lower")
	key = "another task"
	show("longer status", "replaced message", "lower-original")
	if selection.valid(w) {
		t.Fatal("selection leaked into another conversation with matching text")
	}
}

func TestExtensionTranscriptButtonClicksDoNotSelectText(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	w := m.fontResources
	css, err := os.ReadFile("../extensions/codex/resources/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.applyExtensionUI("codex", &extensions.ActionSession{}, peb.UI{ID: "composer", Kind: peb.EditorTab, Busy: true, View: &peb.EditorView{
		HTML: `<main class="codex-workspace" data-pencode-layout="fill"><section class="codex-chat-pane"><div class="codex-transcript" data-ref="transcript" data-pencode-selectable data-pencode-scroll="end"><article class="codex-chat codex-chat-running"><p>Selectable reply</p><section class="codex-activity-group"><button class="codex-activity-summary" type="button" data-ref="activity" data-pencode-copy="clicked"><img src="resources/icons/terminal.svg" alt=""><span>Running command</span><img src="resources/icons/chevron-down.svg" alt=""></button></section></article></div></section></main>`,
		CSS:  string(css),
	}}); err != nil {
		t.Fatal(err)
	}
	markPencodeRootDirtyByModel(w, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	clicks := 0
	state.SetClipboard(func(value string) bool {
		if value == "clicked" {
			clicks++
		}
		return true
	}, nil)
	selection := &m.actions.ui.selection
	for click := range 2 {
		editorContextClick(t, app, m, state, m.actions.ui.editor.prefix+"control-activity")
		if clicks != click+1 {
			t.Fatalf("button click %d was swallowed: clicks=%d selection=%q", click+1, clicks, selection.text())
		}
		if selection.anchor != selection.caret || selection.dragging || selection.dragged {
			t.Fatalf("button click %d started text selection", click+1)
		}
	}
}

func TestExtensionHistoricalSelectionIgnoresClippedToolOutput(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	w := m.fontResources
	if err := m.applyExtensionUI("codex", &extensions.ActionSession{}, peb.UI{ID: "composer", Kind: peb.EditorTab, Busy: true, View: &peb.EditorView{
		HTML: `<div data-ref="transcript" data-pencode-selectable><section class="tools" data-ref="tools" data-pencode-scroll="preserve"><p>first output</p><p>second output</p>` + strings.Repeat(`<p>hidden output extending across the historical reply</p>`, 20) + `</section><p class="reply">Historical reply</p><div style="height:180px"></div><p>Running reply</p></div>`,
		CSS:  `[data-ref="transcript"] { height:700px; overflow:auto; font-size:14px; line-height:24px; } p { margin:0; height:24px; } .tools { height:48px; overflow:auto; } .reply { padding-left:40px; }`,
	}}); err != nil {
		t.Fatal(err)
	}
	markPencodeRootDirtyByModel(w, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	selection := &m.actions.ui.selection
	point := func(label string, caret int) (float64, float64) {
		t.Helper()
		for _, values := range w.Query2[scene.Text2D, scene.UIGlobalTransform]() {
			text := values.A.Ref()
			if text.Value != label {
				continue
			}
			x, y, _, height, ok := scene.TextCaretRect2D(text, text.Text(), caret)
			if !ok {
				t.Fatal("missing historical text geometry")
			}
			matrix := values.B.Ref().Matrix
			return float64(matrix[12] + (x+.1)*matrix[0]), float64(matrix[13] + (y+height*.5)*matrix[5])
		}
		t.Fatalf("missing reply %q", label)
		return 0, 0
	}
	var copied string
	state.SetClipboard(func(value string) bool { copied = value; return true }, nil)
	for _, label := range []string{"Historical", "Running"} {
		selection.lastClick = selection.lastClick.Add(-1e9)
		x, y := point(label, 0)
		state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
		languageInteractionStep(t, app)
		if selection.dragged || selection.anchor != selection.caret {
			t.Fatalf("stationary press on %s selected clipped output: %q", label, selection.text())
		}
		x, y = point(label, len(label))
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x, Y: y})
		state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
		languageInteractionStep(t, app)
		if got := selection.text(); got != label {
			t.Fatalf("dragging %s selected %q", label, got)
		}
		primary := input.KeyLeftControl
		if runtime.GOOS == "darwin" {
			primary = input.KeyLeftSuper
		}
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: primary})
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyC})
		languageInteractionStep(t, app)
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyC})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: primary})
		languageInteractionStep(t, app)
		if copied != label {
			t.Fatalf("copying %s produced %q", label, copied)
		}
	}
	// A tiny movement across a caret boundary remains a click.
	selection.lastClick = selection.lastClick.Add(-1e9)
	x, y := point("Historical", 4)
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x - .2, Y: y})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x + .2, Y: y})
	languageInteractionStep(t, app)
	if selection.dragged || selection.anchor != selection.caret {
		t.Fatalf("pointer jitter started text selection: %q", selection.text())
	}
	tools, _ := w.HTMLNode(m.surface, m.actions.ui.editor.prefix+"control-tools")
	area, _ := tools.ScrollContainer()
	found := false
	for entity, part := range w.Query[htmlui.ScrollbarPart]() {
		if part.Ref().Area != area || part.Ref().Axis != htmlui.ScrollbarVertical || part.Ref().Kind != htmlui.ScrollbarThumb {
			continue
		}
		transform := w.Ref[scene.UIGlobalTransform](entity)
		size := w.Ref[scene.Size2D](entity)
		x, y = float64(transform.Matrix[12]+size.Width/2), float64(transform.Matrix[13]+size.Height/2)
		found = true
		break
	}
	if !found {
		t.Fatal("missing tool output scrollbar")
	}
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x, Y: y + 12})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y + 12})
	languageInteractionStep(t, app)
	if selection.surface != nil || selection.dragged {
		t.Fatalf("scrollbar drag started transcript selection: %q", selection.text())
	}
	scroll := w.Ref[scene.UIScroll](area)
	if scroll.Position.Y() >= 0 {
		t.Fatal("tool output did not scroll")
	}
	x, y = point("Historical", 0)
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
	x, y = point("Historical", 10)
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x, Y: y})
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
	if got := selection.text(); got != "Historical" {
		t.Fatalf("tool scrolling displaced historical selection: %q", got)
	}
}

func TestExtensionSelectionAfterFileChanges(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	w := m.fontResources
	css, err := os.ReadFile("../extensions/codex/resources/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.applyExtensionUI("codex", &extensions.ActionSession{}, peb.UI{ID: "composer", Kind: peb.EditorTab, View: &peb.EditorView{
		HTML: `<main class="codex-workspace" data-pencode-layout="fill"><section class="codex-chat-pane"><div class="codex-transcript" data-ref="transcript" data-pencode-selectable data-pencode-scroll="end"><article class="codex-chat">
<section class="codex-message" data-ref="message/first" data-pencode-region><p>CLI 仍报 keychain access blocked。请在本机终端执行后回复“已完成”，我再继续完善：</p><pre id="pencode-extension-selection-first-command"><code>lark-cli docs +fetch --doc https://example.com/wiki --as user --doc-format markdown &gt; /tmp/plan.json</code></pre></section>
<section class="codex-changes" data-ref="changes/first/region" data-pencode-region><div class="codex-changes-header"><div class="codex-changes-title"><span>已编辑 1 个文件</span><span class="codex-change-counts"><span class="codex-change-added">+137</span><span class="codex-change-removed">-0</span></span></div><button type="button" data-ref="changes-diff/first">查看变更</button></div><div class="codex-changes-list codex-changes-preview"><button class="codex-change-file" type="button" data-ref="change-file/first/0"><span class="codex-change-path">琼崖岛开发文档.md</span><span class="codex-change-counts"><span class="codex-change-added">+137</span><span class="codex-change-removed">-0</span></span></button></div><button class="codex-changes-toggle" type="button" data-ref="changes/first">展开文件</button></section>
<section class="codex-message codex-user" data-ref="message/user" data-pencode-region><div class="codex-user-bubble"><p id="pencode-extension-selection-question">CLI 仍报 keychain access blocked 怎么修复？</p></div><div class="codex-message-actions"><span class="codex-message-time">15:04</span><button type="button" class="codex-icon-button" data-ref="copy-message/user" data-pencode-copy="question">复制</button></div></section>
<section class="codex-turn" data-ref="turn/second/region" data-pencode-region><div class="codex-turn-progress"><button class="codex-turn-toggle" type="button" data-ref="turn/second">用时 27s</button></div><section class="codex-message" data-ref="message/commentary" data-pencode-region><p id="pencode-extension-selection-commentary">我先检查 CLI 的诊断和钥匙串修复命令，确认具体操作。</p></section><section class="codex-activity-group"><button class="codex-activity-summary" type="button" data-ref="activity"><span>读取了文件、运行了命令</span></button></section></section>
<section class="codex-message" data-ref="message/final" data-pencode-region><p id="pencode-extension-selection-answer">CLI 自带这个修复命令。请在 macOS 的终端或 iTerm2 中手动执行：</p><pre id="pencode-extension-selection-repair-command"><code>lark-cli auth repair</code></pre></section>
</article></div></section></main>`,
		CSS: string(css),
	}}); err != nil {
		t.Fatal(err)
	}
	markPencodeRootDirtyByModel(w, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	selection := &m.actions.ui.selection
	point := func(ref string, end bool) (float64, float64) {
		t.Helper()
		bounds := editorContextBounds(t, m, "pencode-extension-selection-"+ref)
		var chosen *scene.Text2D
		var transform *scene.UIGlobalTransform
		for _, values := range w.Query2[scene.Text2D, scene.UIGlobalTransform]() {
			text, matrix := values.A.Ref(), values.B.Ref()
			x, y := matrix.Matrix[12], matrix.Matrix[13]
			if text.Value == "" || x < bounds.X || x >= bounds.X+bounds.Width || y < bounds.Y || y >= bounds.Y+bounds.Height {
				continue
			}
			if transform != nil {
				before := y < transform.Matrix[13] || y == transform.Matrix[13] && x < transform.Matrix[12]
				if before == end {
					continue
				}
			}
			chosen, transform = text, matrix
		}
		if chosen == nil {
			t.Fatalf("missing rendered text in %s", ref)
		}
		caret := 0
		if end {
			caret = utf8.RuneCountInString(chosen.Value)
		}
		x, y, _, height, ok := scene.TextCaretRect2D(chosen, chosen.Text(), caret)
		if !ok {
			t.Fatal("missing caret geometry")
		}
		return float64(transform.Matrix[12] + x*transform.Matrix[0]), float64(transform.Matrix[13] + (y+height*.5)*transform.Matrix[5])
	}
	var copied string
	state.SetClipboard(func(value string) bool { copied = value; return true }, nil)
	for _, test := range []struct{ ref, want string }{
		{"first-command", "lark-cli docs +fetch --doc https://example.com/wiki --as user --doc-format markdown > /tmp/plan.json"},
		{"question", "CLI 仍报 keychain access blocked 怎么修复？"},
		{"commentary", "我先检查 CLI 的诊断和钥匙串修复命令，确认具体操作。"},
		{"answer", "CLI 自带这个修复命令。请在 macOS 的终端或 iTerm2 中手动执行："},
		{"repair-command", "lark-cli auth repair"},
	} {
		selection.lastClick = selection.lastClick.Add(-1e9)
		x, y := point(test.ref, false)
		state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
		languageInteractionStep(t, app)
		x, y = point(test.ref, true)
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x, Y: y})
		state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
		languageInteractionStep(t, app)
		if got := selection.text(); got != test.want {
			t.Fatalf("dragging %s selected %q, want %q", test.ref, got, test.want)
		}
		primary := input.KeyLeftControl
		if runtime.GOOS == "darwin" {
			primary = input.KeyLeftSuper
		}
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: primary})
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyC})
		languageInteractionStep(t, app)
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyC})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: primary})
		languageInteractionStep(t, app)
		if copied != test.want {
			t.Fatalf("copying %s produced %q, want %q", test.ref, copied, test.want)
		}
	}
}
