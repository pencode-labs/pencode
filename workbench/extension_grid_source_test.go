package workbench

import (
	"encoding/json"
	"html"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/onix-engine/onix/html/dom"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func deferredGrid(t *testing.T) *pencodeCustomDocument {
	t.Helper()
	node := dom.NewElement("div", map[string]string{"data-ref": "grid", "id": "deferred-grid"})
	g, err := parsePencodeGrid(node, `{"source":"sheet-one","rows":10000,"columns":200,"rowHeight":21,"columnWidth":64,"styles":[""],"cells":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	return &pencodeCustomDocument{grid: g, find: &pencodeRenderedFind{grid: g}}
}
func gridReply(t *testing.T, source string, request, offset int, done bool, data []byte) *peb.EditorView {
	t.Helper()
	payload, err := json.Marshal(pencodeGridChunk{Source: source, Request: request, Offset: offset, Done: done, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	return &peb.EditorView{HTML: `<div data-pencode-grid-chunk="` + html.EscapeString(string(payload)) + `"></div>`}
}
func TestDeferredGridChunksSelectionAndRequestCoalescing(t *testing.T) {
	d := deferredGrid(t)
	g := d.grid
	d.requestGrid(false)
	first := g.remote.request
	if first.All || !first.Metadata || d.count != 1 {
		t.Fatal("opening must request only its window")
	}
	if allocations := testing.AllocsPerRun(100, func() { d.requestGrid(false) }); allocations != 0 {
		t.Fatalf("pending request allocated %g", allocations)
	}
	g.setWindow(6000, 60000, 1000, 700)
	d.requestGrid(false)
	if d.count != 1 {
		t.Fatal("scrolling queued obsolete windows")
	}
	bytes := []byte(`{"styles":[""],"rowSizes":[[5,40]],"columnSizes":[[90,100]],"cells":[[5,90,"中文🙂 selected",0]]}`)
	split := strings.Index(string(bytes), "🙂") + 1 // Decode only after joining a split UTF-8 rune.
	if err := d.setView(gridReply(t, g.remote.name, first.Request, 0, false, bytes[:split])); err != nil {
		t.Fatal(err)
	}
	if len(g.cells) != 0 || g.remote.request.Offset != split {
		t.Fatal("partial data became visible")
	}
	if err := d.setView(gridReply(t, g.remote.name, first.Request, split, true, bytes[split:])); err != nil {
		t.Fatal(err)
	}
	if g.content != "中文🙂 selected" || !g.remote.metadata || g.remote.complete {
		t.Fatal("initial window failed", g.content)
	}
	d.model = &workspaceModel{}
	selection := &d.model.actions.ui.selection
	selection.grid, selection.content = g, g.content
	selection.anchorOffset, selection.caretOffset = len("中文🙂 "), len(g.content)
	d.requestGrid(true)
	full := g.remote.request
	d.requestGrid(false)
	if !full.All || full.Metadata {
		t.Fatal("full demand was not coalesced")
	}
	bytes = []byte(`{"cells":[[1,1,"prefix",0],[5,90,"中文🙂 selected",0],[9999,150,"far-target",0]]}`)
	if err := d.setView(gridReply(t, g.remote.name, full.Request, 0, true, bytes)); err != nil {
		t.Fatal(err)
	}
	if !g.remote.complete || selection.text() != "selected" {
		t.Fatal("full index lost coordinate based selection", selection.text())
	}
	if d.find.content.String() != "prefix\n中文🙂 selected\nfar-target" {
		t.Fatal("full sheet search did not include offscreen data")
	}
	selection.selectAll()
	if !strings.HasSuffix(selection.text(), "far-target") {
		t.Fatal("full copy omitted offscreen cells")
	}
	if allocations := testing.AllocsPerRun(100, func() { d.requestGrid(false) }); allocations != 0 {
		t.Fatalf("complete grid request allocated %g", allocations)
	}
}
func TestDeferredGridIgnoresStaleRepliesAndRejectsInvalidRanges(t *testing.T) {
	for _, stale := range []string{"other-sheet", "sheet-one"} {
		d := deferredGrid(t)
		d.requestGrid(false)
		request := d.grid.remote.request.Request
		if stale == "sheet-one" {
			request++
		}
		if err := d.setView(gridReply(t, stale, request, 0, true, []byte(`{"cells":[[1,1,"stale",0]]}`))); err != nil {
			t.Fatal(err)
		}
		if d.grid.content != "" || !d.grid.remote.pending {
			t.Fatal("stale data changed current transfer")
		}
	}
	d := deferredGrid(t)
	d.requestGrid(false)
	if err := d.setView(gridReply(t, "sheet-one", 1, 7, true, []byte(`{}`))); err == nil {
		t.Fatal("accepted discontinuous byte ranges")
	}
}
func TestDeferredGridNativeLayoutThemeFindAndPendingCopy(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	w, file := m.fontResources, m.activeFile()
	file.Name = "deferred.xlsx"
	provider := &extensions.Editor{Action: peb.Action{ID: "extensions/office-viewer/office", Default: true, Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".xlsx"}}}}
	m.customEditors = append(m.customEditors, provider)
	m.editorGeneration++
	file.editorID, file.editorGeneration = provider.ID, 0
	d := &pencodeCustomDocument{model: m, file: file, project: m.activeProjectState(), provider: provider}
	m.customDocuments = map[*openFile]*pencodeCustomDocument{file: d}
	css, err := os.ReadFile("../extensions/office-viewer/resources/viewer.css")
	if err != nil {
		t.Fatal(err)
	}
	view := &peb.EditorView{HTML: `<section class="office-viewer"><article class="office-content workbook" data-ref="content" data-pencode-selectable data-pencode-searchable><div class="sheet-grid" data-ref="grid" data-pencode-grid="` + html.EscapeString(`{"source":"sheet-one","rows":10000,"columns":200,"rowHeight":21,"columnWidth":64,"styles":[""],"cells":[]}`) + `"></div></article></section>`, CSS: string(css)}
	if err := d.setView(view); err != nil {
		t.Fatal(err)
	}
	markPencodeRootDirtyByModel(w, m)
	step := func() {
		for range 4 {
			languageInteractionStep(t, app)
		}
	}
	step()
	g := d.grid
	if !g.remote.pending || d.count != 1 {
		t.Fatal("native viewport did not request data")
	}
	selection := &m.actions.ui.selection
	selection.grid, selection.surface, selection.all = g, d.selection, true
	selection.copy(state) // Copy can arrive while the first viewport is in flight.
	if err := d.setView(gridReply(t, "sheet-one", g.remote.request.Request, 0, true, []byte(`{"styles":[""],"rowSizes":[[5000,42]],"cells":[[1,1,"first",0]]}`))); err != nil {
		t.Fatal(err)
	}
	if !g.remote.copy || !g.remote.request.All {
		t.Fatal("pending copy was lost while finishing the viewport")
	}
	var copied string
	state.SetClipboard(func(value string) bool { copied = value; return true }, nil)
	m.findOpen, m.findQuery = true, "far-target"
	if err := d.setView(gridReply(t, "sheet-one", g.remote.request.Request, 0, true, []byte(`{"cells":[[1,1,"first",0],[5000,150,"far-target",0]]}`))); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(copied, "far-target") {
		t.Fatal("pending full copy did not complete", copied)
	}
	m.findPreview = d
	m.refreshFindMatches()
	if len(m.findMatches) != 1 {
		t.Fatal("search did not use complete sparse index", m.findMatches)
	}
	replacement := *view
	replacement.CSS += "\n.sheet-grid{color:var(--vscode-editor-foreground)}"
	if err := d.setView(&replacement); err != nil {
		t.Fatal(err)
	}
	markPencodeRootDirtyByModel(w, m)
	step()
	if d.grid.remote != g.remote || d.grid.content != g.content || selection.grid != d.grid {
		t.Fatal("style recompilation discarded fetched cells or selection")
	}
	if d.grid.rows.offset(5001) != g.rows.offset(5001) {
		t.Fatal("style recompilation lost geometry")
	}
}
func TestVirtualGridLongCellRendersAFindSlice(t *testing.T) {
	d := deferredGrid(t)
	g := d.grid
	value := strings.Repeat("中🙂", 4000) + "needle" + strings.Repeat("尾", 3000)
	if err := g.setCells([]pencodeGridCell{{row: 1, column: 1, value: value}}); err != nil {
		t.Fatal(err)
	}
	visible, start := g.cellText(&g.cells[0])
	if len(visible) > 8192 || start != 0 || !utf8.ValidString(visible) {
		t.Fatal("initial shaping was not bounded")
	}
	g.focusOffset = strings.Index(value, "needle")
	visible, start = g.cellText(&g.cells[0])
	if !strings.Contains(visible, "needle") || start == 0 || !utf8.ValidString(visible) {
		t.Fatal("find target was not rendered")
	}
	if g.copyText(0, len(value), true) != value {
		t.Fatal("rendering slice truncated source copy")
	}
}

func TestDeferredGridInvalidDataDoesNotMutateLiveGrid(t *testing.T) {
	d := deferredGrid(t)
	g := d.grid
	if err := g.setCells([]pencodeGridCell{{row: 1, column: 1, value: "visible"}}); err != nil {
		t.Fatal(err)
	}
	d.requestGrid(false)
	before := g.rows.offset(10)
	reply := gridReply(t, "sheet-one", 1, 0, true, []byte(`{"styles":[],"rowSizes":[[5,100]],"cells":[[1,1,"invalid",0]]}`))
	if err := d.setView(reply); err == nil {
		t.Fatal("accepted invalid style reference")
	}
	if len(g.styles) != 1 || g.content != "visible" || g.rows.offset(10) != before {
		t.Fatal("invalid transfer changed live grid before validation")
	}
}
