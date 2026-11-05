package workbench

import (
	"html"
	"reflect"
	"strconv"
	"testing"
	"unicode/utf8"

	"github.com/diiyw/onix"
	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/text/piecetree"
	engineui "github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestInputLinksParseReferences(t *testing.T) {
	for _, test := range []struct {
		source string
		labels []string
	}{
		{`查看 [main.go](src/main.go) [目录/](src/%E7%9B%AE%E5%BD%95/)`, []string{"main.go", "目录/"}},
		{`[空 \[目录\]/](src/space%20(dir)/)`, []string{`空 \[目录\]/`}},
		{"`[code](path)` ``[code](path)``\n```\n[code](path)\n```\n[review](/skills/SKILL.md)", []string{"review"}},
		{`\[escaped](path) ![image](path) [incomplete](path [empty]() [](path)`, nil},
		{"[label](bad path) [line\nbreak](path) [label](line\nbreak)", nil},
	} {
		t.Run(test.source, func(t *testing.T) {
			var labels []string
			for _, link := range parseInputLinks(nil, test.source) {
				labels = append(labels, test.source[link.start+1:link.labelEnd])
			}
			if !reflect.DeepEqual(labels, test.labels) {
				t.Fatalf("labels = %q, want %q", labels, test.labels)
			}
		})
	}
}

func TestInputLinksPaintKeepsSourceAndComposition(t *testing.T) {
	const value = `前缀 [中文 \[笔记\].go](src/%E4%B8%AD%E6%96%87.go) 后续`
	field := engineui.TextField{Value: value, Caret: utf8.RuneCountInString(value), Anchor: utf8.RuneCountInString(value), Focused: true}
	var links pencodeInputLinks
	text := scene.Text2D{Value: value}
	color := graphics.ColorFromRGB(30, 140, 230)
	links.paint(&text, field, color)
	if len(text.InlineObjects) != 4 || len(text.Spans) != 1 || text.Spans[0].Color != color || text.Text().String() != value {
		t.Fatalf("reference was not concealed and colored without changing source: %+v", text)
	}
	field.Anchor, field.Caret = 6, 6
	links.paint(&text, field, color)
	if len(text.InlineObjects) != 4 {
		t.Fatal("caret inside a reference expanded its source")
	}
	links.snapSelection(&field)
	if field.Caret != utf8.RuneCountInString("前缀 ") || field.Anchor != field.Caret {
		t.Fatal("pointer caret entered the source of a reference", field)
	}
	field.Anchor, field.Caret = 6, 8
	links.snapSelection(&field)
	if field.Anchor != utf8.RuneCountInString("前缀 ") || field.Caret != utf8.RuneCountInString(value)-utf8.RuneCountInString(" 后续") {
		t.Fatal("partial selection split a reference", field)
	}
	field.Anchor, field.Caret = 0, utf8.RuneCountInString(value)
	links.paint(&text, field, color)
	if len(text.InlineObjects) != 4 {
		t.Fatal("selecting a reference changed its layout")
	}
	field.Composition = "输入"
	links.paint(&text, field, color)
	if len(text.InlineObjects) != 0 || len(text.Spans) != 0 {
		t.Fatal("source byte offsets were applied to IME composition")
	}
	field.Composition = ""
	if n := testing.AllocsPerRun(100, func() { links.paint(&text, field, color) }); n != 0 {
		t.Fatalf("warm link paint allocated %g", n)
	}
}

func TestInputLinksPaintDuringComposition(t *testing.T) {
	const first = `[中文 \[笔记\].go](src/note.go)`
	const second = `[main.go](src/main.go)`
	const value = first + " 文本 " + second
	source := piecetree.FromString(value)
	for _, test := range []struct {
		name, composition string
		anchor, caret     int
		count             int
	}{
		{"after", "de", source.Runes(), source.Runes(), 2},
		{"before", "拼音", 0, 0, 2},
		{"between", "de", utf8.RuneCountInString(first), utf8.RuneCountInString(first), 2},
		{"replace text", "字", utf8.RuneCountInString(first + " "), utf8.RuneCountInString(first + " 文本"), 2},
		{"replace first", "新文件", utf8.RuneCountInString(first), 0, 1},
		{"replace second", "新文件", utf8.RuneCountInString(first + " 文本 "), source.Runes(), 1},
		{"replace all", "重写", 0, source.Runes(), 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			field := engineui.TextField{Value: value, Anchor: test.anchor, Caret: test.caret, Composition: test.composition}
			start, end := source.ByteOffset(min(test.anchor, test.caret)), source.ByteOffset(max(test.anchor, test.caret))
			visual := source.Replace(start, end, test.composition)
			text := scene.Text2D{Source: visual}
			color := graphics.ColorFromRGB(30, 140, 230)
			var links pencodeInputLinks
			if !links.paint(&text, field, color) {
				t.Fatal("composition did not invalidate link geometry")
			}
			var want pencodeInputLinks
			want.paint(&scene.Text2D{}, engineui.TextField{Value: visual.String()}, color)
			if len(text.Spans) != test.count || !reflect.DeepEqual(text.Spans, want.spans) || !reflect.DeepEqual(text.InlineObjects, want.objects) {
				t.Fatalf("composition link offsets differ from visual text: spans=%+v want=%+v objects=%+v want=%+v", text.Spans, want.spans, text.InlineObjects, want.objects)
			}
			if links.paint(&text, field, color) {
				t.Fatal("unchanged composition invalidated geometry")
			}
			if n := testing.AllocsPerRun(100, func() { links.paint(&text, field, color) }); n != 0 {
				t.Fatalf("warm composition paint allocated %g", n)
			}
			field.Composition += "ng"
			if !links.paint(&text, field, color) {
				t.Fatal("updated composition did not invalidate geometry")
			}
			field.Composition = ""
			if !links.paint(&text, field, color) || len(text.Spans) != 2 || field.Text() != source {
				t.Fatal("cancelling composition lost the references or committed source")
			}
		})
	}
}

func TestExtensionInputLinksUseNativeLayoutAndMarkdownEvents(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	const value = `查看 [main.go](src/a/very/long/path/main.go) 和 [目录/](src/directory/) `
	frames := 3
	show := func(source string) {
		t.Helper()
		if err := m.applyExtensionUI("test", &extensions.ActionSession{}, peb.UI{ID: "links", Kind: peb.EditorTab, View: &peb.EditorView{
			HTML: `<textarea data-ref="prompt" data-pencode-markdown-links data-pencode-autosize data-pencode-caret="` +
				strconv.Itoa(utf8.RuneCountInString(source)) + ":" + strconv.Itoa(len(source)) + `" style="width:600px;min-height:38px;max-height:240px">` + html.EscapeString(source) + `</textarea>`,
			Events: []peb.EditorBinding{{Ref: "prompt", Kind: "input"}},
		}}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(w, m)
		for range frames {
			languageInteractionStep(t, app)
		}
	}
	show(value)
	s := m.actions.ui.editor
	control, _ := w.HTMLControl(m.surface, s.focus[0].id)
	visual, _ := w.FindPart[engineui.ValuePart, scene.Text2D](control)
	text := w.Mut[scene.Text2D](visual)
	width, _, _ := scene.MeasureText2D(text)
	objects := text.InlineObjects
	text.InlineObjects = nil
	rawWidth, _, _ := scene.MeasureText2D(text)
	text.InlineObjects = objects
	if len(objects) != 4 || width >= rawWidth || text.Text().String() != value {
		t.Fatalf("native layout retained Markdown syntax: rendered=%v source=%v objects=%v", width, rawWidth, objects)
	}
	field := w.Mut[engineui.TextField](control)
	field.Anchor, field.Caret = utf8.RuneCountInString(value), utf8.RuneCountInString(value)
	languageInteractionStep(t, app)
	width, _, _ = scene.MeasureText2D(text)
	x, _, _, _, ok := scene.TextCaretRect2D(text, value, field.Caret)
	if !ok || x-width > 2 || width-x > 2 {
		t.Fatalf("caret left a source-width gap after the rendered links: caret=%v rendered=%v", x, width)
	}
	caretVisual, found := w.FindPart[engineui.CaretPart, engineui.SurfaceStyle](control)
	if !found {
		t.Fatal("missing native caret visual")
	}
	caretTransform, _ := w.Get[scene.UITransform](caretVisual)
	textTransform, _ := w.Get[scene.UITransform](visual)
	if actual := caretTransform.Translation.X.Value - textTransform.Translation.X.Value; actual-x > 1 || x-actual > 1 {
		t.Fatalf("native caret does not follow rendered text: actual=%v expected=%v", actual, x)
	}
	state := input.NewState()
	state.CommitText("继续")
	event := s.commitInput(state, control, field, "prompt")
	if event.Value != value+"继续" || event.Focus != uint32(utf8.RuneCountInString(value+"继续")) {
		t.Fatalf("native input lost source or caret: %+v", event)
	}
	languageInteractionStep(t, app)
	liveState, _ := app.World.GetResource[*input.State]()
	w.Focus(control)
	for _, composition := range []string{"de", "deng", "", "bianji"} {
		liveState.Enqueue(input.Event{Kind: input.EventTextComposition, Composition: input.TextComposition{Text: composition, Active: composition != ""}})
		languageInteractionStep(t, app)
		field = w.Mut[engineui.TextField](control)
		text = w.Mut[scene.Text2D](visual)
		if field.Text().String() != value+"继续" || text.Text().String() != value+"继续"+composition || len(text.InlineObjects) != 4 || len(text.Spans) != 2 {
			t.Fatalf("IME composition expanded references or changed the draft: composition=%q field=%+v text=%+v", composition, field, text)
		}
		caretVisual, _ = w.FindPart[engineui.CaretPart, engineui.SurfaceStyle](control)
		caretTransform, _ = w.Get[scene.UITransform](caretVisual)
		textTransform, _ = w.Get[scene.UITransform](visual)
		x, _, _, _, ok = scene.TextCaretRect2D(text, text.Text(), field.Caret+utf8.RuneCountInString(composition))
		if actual := caretTransform.Translation.X.Value - textTransform.Translation.X.Value; !ok || actual-x > 1 || x-actual > 1 {
			t.Fatalf("IME caret left rendered references: composition=%q actual=%v expected=%v", composition, actual, x)
		}
	}
	liveState.Enqueue(input.Event{Kind: input.EventTextInput, Text: "编辑"})
	languageInteractionStep(t, app)
	liveState.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyLeftControl})
	liveState.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyZ})
	languageInteractionStep(t, app)
	liveState.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyZ})
	field = w.Mut[engineui.TextField](control)
	if field.Value != value+"继续" {
		t.Fatal("undo lost Markdown source", field.Value)
	}
	var copied string
	liveState.SetClipboard(func(text string) bool { copied = text; return true }, nil)
	field.Anchor, field.Caret = 0, utf8.RuneCountInString(field.Value)
	liveState.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyC})
	languageInteractionStep(t, app)
	liveState.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyC})
	liveState.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyLeftControl})
	languageInteractionStep(t, app)
	if copied != value+"继续" {
		t.Fatal("copy lost Markdown source", copied)
	}
	if n := testing.AllocsPerRun(100, func() { m.syncExtensionUI(w) }); n != 0 {
		t.Fatalf("unchanged rendered references allocated %g", n)
	}
	frames = 2 // Mount the replacement control, then inspect its first laid-out frame.
	const next = `中文 [review](/a/long/skill/path/SKILL.md) `
	show(next)
	control, _ = w.HTMLControl(m.surface, s.focus[0].id)
	visual, _ = w.FindPart[engineui.ValuePart, scene.Text2D](control)
	text = w.Mut[scene.Text2D](visual)
	x, _, _, _, ok = scene.TextCaretRect2D(text, next, utf8.RuneCountInString(next))
	caretVisual, _ = w.FindPart[engineui.CaretPart, engineui.SurfaceStyle](control)
	caretTransform, _ = w.Get[scene.UITransform](caretVisual)
	textTransform, _ = w.Get[scene.UITransform](visual)
	if actual := caretTransform.Translation.X.Value - textTransform.Translation.X.Value; !ok || actual-x > 1 || x-actual > 1 {
		t.Fatalf("inserting a completion left stale caret geometry in its first frame: actual=%v expected=%v", actual, x)
	}
	show("plain draft")
	control, _ = w.HTMLControl(m.surface, s.focus[0].id)
	visual, _ = w.FindPart[engineui.ValuePart, scene.Text2D](control)
	if text = w.Mut[scene.Text2D](visual); len(text.InlineObjects) != 0 || len(text.Spans) != 0 {
		t.Fatal("switching drafts retained reference paint")
	}
}

func TestExtensionInputLinksNavigateAndDeleteAtomically(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m, w := session.active.model, session.active.model.fontResources
	const prefix, suffix = "前缀 ", "后续"
	for _, reference := range []string{`[main.go](src/main.go)`, `[目录/](src/%E7%9B%AE%E5%BD%95/)`, `[review](/skills/code-review/SKILL.md)`} {
		for _, key := range []input.KeyCode{input.KeyBackspace, input.KeyDelete} {
			value := prefix + reference + " " + suffix
			if err := m.applyExtensionUI("test", &extensions.ActionSession{}, peb.UI{ID: "atomic", Kind: peb.EditorTab, View: &peb.EditorView{
				HTML:   `<textarea data-ref="prompt" data-pencode-markdown-links style="width:600px;height:100px">` + html.EscapeString(value) + `</textarea>`,
				Events: []peb.EditorBinding{{Ref: "prompt", Kind: "input"}},
			}}); err != nil {
				t.Fatal(err)
			}
			markPencodeRootDirtyByModel(w, m)
			for range 3 {
				languageInteractionStep(t, app)
			}
			s := m.actions.ui.editor
			control, _ := w.HTMLControl(m.surface, s.focus[0].id)
			w.Focus(control)
			field := w.Mut[engineui.TextField](control)
			start := utf8.RuneCountInString(prefix)
			end := start + utf8.RuneCountInString(reference)
			field.Anchor, field.Caret = end, end
			left := input.NewState()
			left.PressKey(input.KeyLeft)
			if !s.interceptKeys(left) || field.Caret != start || field.Anchor != start {
				t.Fatal("left arrow entered a reference", reference, *field)
			}
			right := input.NewState()
			right.PressKey(input.KeyRight)
			if !s.interceptKeys(right) || field.Caret != end || field.Anchor != end {
				t.Fatal("right arrow entered a reference", reference, *field)
			}
			field.Caret = start
			if key == input.KeyBackspace {
				field.Caret = end + 1 // The space inserted by completion belongs to the reference.
			}
			field.Anchor = field.Caret
			state, _ := app.World.GetResource[*input.State]()
			state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
			languageInteractionStep(t, app)
			state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
			languageInteractionStep(t, app)
			field = w.Mut[engineui.TextField](control)
			if field.Value != prefix+suffix || field.Caret != start || field.Anchor != start {
				t.Fatalf("deleting %q left reference source: %+v", reference, *field)
			}
			state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyLeftControl})
			state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyZ})
			languageInteractionStep(t, app)
			state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyZ})
			state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyLeftControl})
			languageInteractionStep(t, app)
			if field = w.Mut[engineui.TextField](control); field.Value != value {
				t.Fatal("undo did not restore the complete reference", field.Value)
			}
		}
	}
}

func TestExtensionInputLinksRenderPublicationWithoutPointerInput(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m, w := session.active.model, session.active.model.fontResources
	results := make(chan extensions.ActionResult, 1)
	process := &extensions.ActionSession{Results: results}
	m.actions.sessions = map[string]*extensions.ActionSession{"codex": process}
	t.Cleanup(func() { delete(m.actions.sessions, "codex") })
	show := func(value string, completion bool) {
		menu := ""
		if completion {
			menu = `<section><button data-ref="candidate">file</button></section>`
		}
		results <- extensions.ActionResult{UI: &peb.UI{ID: "composer", Kind: peb.EditorTab, View: &peb.EditorView{
			HTML: menu + `<textarea data-ref="prompt" data-pencode-markdown-links data-pencode-autosize data-pencode-caret="` +
				strconv.Itoa(utf8.RuneCountInString(value)) + ":" + strconv.Itoa(len(value)) + `" style="width:600px;min-height:38px;max-height:240px">` + html.EscapeString(value) + `</textarea>`,
			Events: []peb.EditorBinding{{Ref: "prompt", Kind: "input"}},
		}}}
		app.RequestRender() // The extension publication wakes the otherwise idle app once.
	}
	show("@ma", true)
	for range 4 {
		languageInteractionStep(t, app)
	}
	app.SetRenderMode(onix.RenderOnDemand)
	app.SetIdleEnabled(true)
	for _, value := range []string{`[main.go](src/main.go) `, `[目录/](src/%E7%9B%AE%E5%BD%95/) `, `[review](/skills/code-review/SKILL.md) `} {
		show(value, false)
		painted := false
		for frame := range 6 {
			rendered, err := app.Step()
			if err != nil {
				t.Fatal(err)
			}
			s := m.actions.ui.editor
			control, ok := w.HTMLControl(m.surface, s.prefix+"control-prompt")
			if !ok {
				continue
			}
			visual, ok := w.FindPart[engineui.ValuePart, scene.Text2D](control)
			if !ok {
				continue
			}
			text, _ := w.Get[scene.Text2D](visual)
			if !rendered || text.Font == nil || text.Text().String() != value {
				continue
			}
			if len(text.InlineObjects) != 2 || len(text.Spans) != 1 {
				t.Fatalf("publication painted Markdown source without pointer input on frame %d: %q objects=%v spans=%v", frame, value, text.InlineObjects, text.Spans)
			}
			field, _ := w.Get[engineui.TextField](control)
			if s.focus[0].placeCaret || field.Caret != utf8.RuneCountInString(value) || field.Anchor != field.Caret {
				t.Fatalf("completion needed another input event to restore the caret: %+v", field)
			}
			painted = true
		}
		if !painted {
			t.Fatal("completion was not rendered without another input event", value)
		}
	}
}
