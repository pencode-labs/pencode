package workbench

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestCachedSelectionRetainsTreeNodes(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	p := m.activeProjectState()
	for _, name := range []string{"other.go", "unchanged.go"} {
		resourceTestWrite(t, p, name, "package demo\n")
	}
	p.stopWatch()
	m.startProjectScan(p, false)
	waitPencodeProjectScan(t, p, func() { languageInteractionStep(t, app) })
	openFileAndWait(t, m, "other.go")
	markPencodeRootDirtyByModel(m.fontResources, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	settlePencodeHighlight(t, m)
	languageInteractionStep(t, app)
	w := m.fontResources
	row, ok := w.HTMLControl(m.surface, m.projectTreeRowID(p, "unchanged.go"))
	if !ok {
		t.Fatal("missing unchanged row")
	}
	for _, name := range []string{"main.go", "other.go", "main.go"} {
		m.selectProjectTab(p, name)
		if m.fullRefresh || m.refresh&pencodeRefreshSelection == 0 {
			t.Fatal("cached selection requested full refresh")
		}
		languageInteractionStep(t, app)
		// Layout may resynchronize geometry; the retained tree control must survive.
		if next, ok := w.HTMLControl(m.surface, m.projectTreeRowID(p, "unchanged.go")); !ok || next != row {
			t.Fatalf("unrelated tree control replaced on %s", name)
		}
		active := 0
		for _, v := range w.Query[htmlui.Component[tabProps, struct{}]]() {
			props := v.Ref().Props
			if props.Model == m && props.Active {
				active++
				if props.Path != name {
					t.Fatal("wrong active tab", props.Path)
				}
			}
		}
		if active != 1 || m.editor.file != p.Files[name] {
			t.Fatal("selection did not reach editor/tab", active)
		}
	}
	m.selectProjectTab(p, "other.go")
	m.selectProjectTab(p, "main.go")
	if m.selectionPrevious != "main.go" {
		t.Fatal("batched switches lost the painted selection")
	}
	languageInteractionStep(t, app)
	before := m.selectionSnapshot()
	p.tree.revision++
	m.refreshFileSelection(before)
	if !m.fullRefresh {
		t.Fatal("tree structure change did not request full refresh")
	}
}

func streamingView(count int, tail string) *peb.EditorView {
	var b strings.Builder
	b.WriteString(`<main class="codex-workspace" data-pencode-layout="fill"><section class="codex-chat-pane"><div class="codex-transcript" data-ref="transcript" data-pencode-scroll="end" data-pencode-scroll-key="task"><article class="codex-chat">`)
	for i := 0; i < count; i++ {
		fmt.Fprintf(&b, `<section class="codex-message" data-ref="message/%d" data-pencode-region><p>Earlier message with some text</p></section>`, i)
	}
	b.WriteString(`<section class="codex-message" data-ref="stream" data-pencode-region><p>`)
	b.WriteString(tail)
	b.WriteString(`</p></section></article></div><section class="codex-composer"><textarea data-ref="prompt" class="codex-prompt">draft</textarea></section></section><aside><section data-ref="history-list" data-pencode-region>History</section></aside></main>`)
	return &peb.EditorView{HTML: b.String(), Events: []peb.EditorBinding{{Ref: "prompt", Kind: "input"}}}
}

func TestStreamingRegionRetainsTemplateAndValidatesAcrossRegions(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	show := func(view *peb.EditorView) {
		t.Helper()
		if err := m.applyExtensionUI("chat", &extensions.ActionSession{}, peb.UI{ID: "chat", Kind: peb.EditorTab, View: view}); err != nil {
			t.Fatal(err)
		}
	}
	show(streamingView(20, "first"))
	markPencodeRootDirtyByModel(m.fontResources, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	surface := m.actions.ui.editor
	definition, tree, region := surface.definition, surface.tree, surface.regions[0].definition
	prompt, ok := m.fontResources.HTMLControl(m.surface, surface.prefix+"control-prompt")
	if !ok {
		t.Fatal("missing prompt")
	}
	show(streamingView(20, "second"))
	m.refreshExtensionSurface(surface)
	languageInteractionStep(t, app)
	if surface.definition != definition || surface.tree != tree || surface.regions[0].definition != region {
		t.Fatal("stream replaced static template/history")
	}
	if next, _ := m.fontResources.HTMLControl(m.surface, surface.prefix+"control-prompt"); next != prompt {
		t.Fatal("stream replaced input")
	}
	invalid := &peb.EditorView{HTML: `<main><div data-ref="a" data-pencode-region><button data-ref="duplicate">A</button></div><div data-ref="b" data-pencode-region><button data-ref="duplicate">B</button></div></main>`}
	if err := surface.setView(invalid); err == nil {
		t.Fatal("duplicate ref across regions accepted")
	}
	if surface.definition != definition {
		t.Fatal("failed validation replaced live template")
	}
	hidden := `<main hidden><section data-ref="area" data-pencode-region><input data-ref="field"></section></main>`
	if err := surface.setView(&peb.EditorView{HTML: hidden}); err != nil || len(surface.focus) != 0 {
		t.Fatal("region lost inherited hidden state", err)
	}
	retained := surface.regions[0].tree
	if err := surface.setView(&peb.EditorView{HTML: strings.Replace(hidden, " hidden", "", 1)}); err != nil || len(surface.focus) != 1 || surface.regions[0].tree != retained {
		t.Fatal("cached region did not inherit its new visibility", err)
	}
}

func BenchmarkExtensionStreamingUpdate(b *testing.B) {
	for _, count := range []int{10, 100, 500} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			_, session := newPencodeTestApp(b)
			m := session.active.model
			views := [2]*peb.EditorView{streamingView(count, "Streaming A"), streamingView(count, "Streaming B")}
			css, err := os.ReadFile("testdata/codex/chat.css")
			if err != nil {
				b.Fatal(err)
			}
			for _, view := range views {
				view.CSS, view.Events = string(css), nil
			}
			if err := m.applyExtensionUI("chat", &extensions.ActionSession{}, peb.UI{ID: "chat", Kind: peb.EditorTab, View: views[0]}); err != nil {
				b.Fatal(err)
			}
			surface := m.actions.ui.editor
			index := 0
			b.ReportAllocs()
			for b.Loop() {
				index = 1 - index
				if err := surface.setView(views[index]); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkNativeEditFrame(b *testing.B) {
	for _, size := range []int{64 << 10, 512 << 10, 2 << 20} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			dir := b.TempDir()
			text := strings.Repeat("let value = 42; // text line\n", size/len("let value = 42; // text line\n"))
			if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte(text), 0644); err != nil {
				b.Fatal(err)
			}
			b.Setenv("ONIX_PENCODE_PROFILE_PROJECT", dir)
			b.Setenv("ONIX_PENCODE_PROFILE_FILE", "main.txt")
			app, session := newPencodeTestApp(b)
			m := session.active.model
			file := m.activeFile()
			middle := file.snapshotBuffer().Runes() / 2
			if !m.fontResources.Focus(m.editor.control) {
				b.Fatal("editor could not receive focus")
			}
			state, _ := app.World.GetResource[*input.State]()
			edits := [2]string{"x", "y"}
			index := 0
			b.ReportAllocs()
			editFrame := func() {
				index = 1 - index
				before := file.snapshot()
				field := m.fontResources.Mut[engineui.TextField](m.editor.control)
				field.Anchor, field.Caret = middle, middle+1
				state.Enqueue(input.Event{Kind: input.EventTextInput, Text: edits[index]})
				app.RequestRender()
				if _, err := app.Step(); err != nil {
					b.Fatal(err)
				}
				if file.snapshot() == before {
					b.Fatal("native input did not publish an edited version")
				}
			}
			for range 3 {
				editFrame()
			}
			for b.Loop() {
				editFrame()
			}
			if file.snapshot().Materialized() {
				b.Fatal("native edit materialized the document")
			}
		})
	}
}

func TestVisibleGutterUsesDocumentLinesAndPreservesIMEBuffer(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	value := strings.Repeat(strings.Repeat("中a", 60)+"\n", 300)
	m.updateActiveContent(value)
	m.setEditorValueAndCaret(w, value, 0)
	m.refreshRegion(w, pencodeRefreshEditor)
	languageInteractionStep(t, app)
	document := m.activeFile().textBuffer(value)
	text := w.Mut[scene.Text2D](m.editor.text)
	text.WrapWidth, text.WrapMode = 180, scene.TextWrapCharacters
	transform := w.Mut[scene.UITransform](m.editor.text)
	transform.Translation.Y.Value = -600
	for _, visual := range []string{value, "输入中\n" + value, value} {
		text.Value = ""
		text.Source = snapshotText(visual)
		m.editor.syncGutter(w, m.surface, m)
		if m.editor.firstLine == 0 || len(m.editor.sourceLines) != m.editor.lastLine-m.editor.firstLine || len(m.editor.sourceLines) >= scene.TextLineCount2D(text) {
			t.Fatal("gutter did not retain only the visible lines")
		}
		continuation := false
		for i, got := range m.editor.sourceLines {
			start, _, _, _ := scene.TextLineRange2D(text, m.editor.firstLine+i)
			want := 0
			if start == 0 || visual[start-1] == '\n' {
				want = strings.Count(visual[:start], "\n") + 1
			} else {
				continuation = true
			}
			if got != want {
				t.Fatalf("line %d: %d, want %d", i, got, want)
			}
		}
		if !continuation {
			t.Fatal("fixture did not exercise soft wrapping")
		}
		if !document.Matches(value) || m.activeContent() != value {
			t.Fatal("IME changed the document buffer")
		}
	}
}
