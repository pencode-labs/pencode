package workbench

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestOfficeViewerSelectionCopyAndSearch(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	w, file := m.fontResources, m.activeFile()
	file.Name = "preview.xlsx"
	provider := &extensions.Editor{Action: peb.Action{ID: "extensions/office-viewer/office", Text: "Office Viewer", Default: true, Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".xlsx"}}}}
	m.customEditors = append(m.customEditors, provider)
	m.editorGeneration++
	file.editorID, file.editorGeneration = provider.ID, 0
	doc := &pencodeCustomDocument{model: m, file: file, project: m.activeProjectState(), provider: provider}
	m.customDocuments = map[*openFile]*pencodeCustomDocument{file: doc}
	css, err := os.ReadFile("../extensions/office-viewer/resources/viewer.css")
	if err != nil {
		t.Fatal(err)
	}
	show := func(body string) {
		t.Helper()
		if err := doc.setView(&peb.EditorView{
			HTML: `<section class="office-viewer"><header class="office-toolbar"><nav class="sheet-tabs"><button data-ref="sheet-0" aria-pressed="true">Sheet1</button><button data-ref="sheet-1" aria-pressed="false">detail</button></nav></header><article class="office-content workbook" data-ref="office-content" data-pencode-selectable data-pencode-searchable data-pencode-preserve-whitespace>` + body + `</article></section>`,
			CSS:  string(css),
		}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(w, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
	}
	show(`<table class="sheet"><tr><td>alpha  beta</td><td>copy-end</td></tr><tr><td>next-row</td><td></td><td>value</td></tr></table>`)
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
	x, y := point("alpha", 0)
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
	x, y = point("copy-end", 8)
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x, Y: y})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
	shortcut(input.KeyC)
	if copied != "alpha  beta\tcopy-end" {
		t.Fatalf("spreadsheet drag/copy lost text or cell boundaries: %q", copied)
	}
	selection := &m.actions.ui.selection
	if n := testing.AllocsPerRun(100, func() { selection.sync(w, m) }); n != 0 {
		t.Fatalf("warm custom document selection allocated %g", n)
	}
	shortcut(input.KeyA)
	shortcut(input.KeyC)
	if copied != "alpha  beta\tcopy-end\nnext-row\t\tvalue" {
		t.Fatalf("spreadsheet select all copied %q", copied)
	}
	// Search the complete sheet, including text outside both paint axes.
	body := `<table class="sheet">` + strings.Repeat(`<tr><td>filler</td><td>value</td></tr>`, 70) + `<tr>` + strings.Repeat(`<td></td>`, 25) + `<td>far-target</td></tr></table>`
	show(body)
	if selection.surface != nil {
		t.Fatal("replacing the worksheet retained its old selection")
	}
	m.findQuery = "far-target"
	shortcut(input.KeyF)
	for range 8 {
		languageInteractionStep(t, app)
	}
	if !m.findOpen || len(m.findMatches) != 1 {
		t.Fatalf("sheet search failed: open=%v matches=%v", m.findOpen, m.findMatches)
	}
	visible := false
	for _, entity := range doc.find.text.quads {
		if value, ok := w.Get[scene.Visibility](entity); ok && value.Visible {
			visible = true
		}
	}
	if !visible {
		t.Fatalf("offscreen sheet match was not revealed: spans=%d reveal=%v", len(doc.find.text.spans), doc.find.reveal)
	}
	m.closeFind(w)
	markPencodeRootDirtyByModel(w, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	x, y = point("far-target", 0)
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
	x, y = point("far-target", 10)
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x, Y: y})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
	shortcut(input.KeyC)
	if copied != "far-target" {
		t.Fatalf("copy after scrolling selected the wrong cells: %q", copied)
	}
	m.customDocuments = nil
	if doc.selection.live() {
		t.Fatal("closed custom document selection remained live")
	}
}

func TestOfficeViewerClippedSelectionCopiesOnlySelectedText(t *testing.T) {
	selection := pencodeExtensionTextSelection{
		content: "before\nvisible\nafter",
		spans:   []pencodeExtensionTextSpan{{offset: 7, value: "visible"}},
		anchor:  pencodeExtensionTextPoint{0, 0}, caret: pencodeExtensionTextPoint{0, 7},
	}
	if got := selection.text(); got != "visible" {
		t.Fatalf("clipped selection copied unselected text: %q", got)
	}
	selection.selectAll()
	if got := selection.text(); got != selection.content {
		t.Fatalf("select all did not include offscreen text: %q", got)
	}
}
