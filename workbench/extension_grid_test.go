package workbench

import (
	"fmt"
	"html"
	"os"
	"strings"
	"testing"

	"github.com/onix-engine/onix/html/dom"
	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestOfficeVirtualGridScrollSearchAndCopy(t *testing.T) {
	app, m, _, keys := languageInteractionFixture(t)
	w, file := m.fontResources, m.activeFile()
	file.Name = "virtual.xlsx"
	provider := &extensions.Editor{Action: peb.Action{ID: "extensions/office-viewer/office", Default: true, Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".xlsx"}}}}
	m.customEditors = append(m.customEditors, provider)
	m.editorGeneration++
	file.editorID, file.editorGeneration = provider.ID, 0
	doc := &pencodeCustomDocument{model: m, file: file, project: m.activeProjectState(), provider: provider}
	m.customDocuments = map[*openFile]*pencodeCustomDocument{file: doc}
	css, err := os.ReadFile("../extensions/office-viewer/resources/viewer.css")
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"rows":10000,"columns":200,"rowHeight":21,"columnWidth":64,"rowSizes":[[3,64],[5000,42]],"columnSizes":[[150,180]],"styles":["","text-align:center;vertical-align:middle;","white-space:pre-wrap;overflow-wrap:break-word;"],"cells":[[1,1,"repeat",0],[1,3,"alpha  beta",0],[2,2,"copy-end",0],[3,1,"wrapped words across lines",2],[5000,150,"中文🙂 far-target",1],[10000,200,"repeat",0]]}`
	view := &peb.EditorView{HTML: `<section class="office-viewer"><header class="office-toolbar">Sheet</header><article class="office-content workbook" data-ref="office-content" data-pencode-selectable data-pencode-searchable data-pencode-preserve-whitespace><div class="sheet-grid" data-ref="grid" data-pencode-grid="` + html.EscapeString(payload) + `"></div></article></section>`, CSS: string(css)}
	if err := doc.setView(view); err != nil {
		t.Fatal(err)
	}
	markPencodeRootDirtyByModel(w, m)
	step := func() {
		for range 4 {
			languageInteractionStep(t, app)
		}
	}
	step()
	g := doc.grid
	if g == nil || g.component == 0 || g.area == 0 {
		t.Fatalf("missing grid: %+v", g)
	}
	titlePosition := func() [2]float32 {
		t.Helper()
		for _, values := range w.Query2[scene.Text2D, scene.UIGlobalTransform]() {
			if values.A.Ref().Value == "Sheet" {
				matrix := values.B.Ref().Matrix
				return [2]float32{matrix[12], matrix[13]}
			}
		}
		t.Fatal("worksheet toolbar disappeared")
		return [2]float32{}
	}
	title := titlePosition()
	viewport, ok := w.HTMLNode(m.surface, "custom-editor")
	if !ok {
		t.Fatal("missing editor viewport")
	}
	outerArea, _ := viewport.ScrollContainer()
	if outerArea == g.area {
		t.Fatal("worksheet must scroll independently of its toolbar")
	}
	for _, position := range [][2]float32{{-640, 0}, {0, -2100}, {-640, -2100}, {0, 0}} {
		w.Mut[scene.UIScroll](g.area).Position = position
		step()
		if got := titlePosition(); got != title {
			t.Fatalf("scroll %v moved the worksheet toolbar: %v, want %v", position, got, title)
		}
	}
	cellID := func(r, c int) string { return fmt.Sprintf("%s/cell/%d/%d", g.id, r, c) }
	if _, ok := w.HTMLNode(m.surface, cellID(5000, 150)); ok {
		t.Fatal("offscreen cell was mounted")
	}
	verify := func() {
		t.Helper()
		scroll := w.Ref[scene.UIScroll](g.area)
		if scroll.ContentSize[0] < float32(g.columns.offset(g.columns.count)) || scroll.ContentSize[1] < float32(g.rows.offset(g.rows.count)) {
			t.Fatalf("truncated grid extent: %+v", scroll)
		}
		count := (g.endRow - g.firstRow) * (g.endColumn - g.firstColumn)
		if count <= 0 || count > 2500 {
			t.Fatalf("window not bounded: %d rows=%d..%d cols=%d..%d", count, g.firstRow, g.endRow, g.firstColumn, g.endColumn)
		}
		if _, ok := g.node.Attr("data-pencode-grid"); ok || len(g.node.Children) != 0 {
			t.Fatal("template retained full grid payload or children")
		}
	}
	verify()
	var wrappedTop, wrappedBottom float32
	// Wrapped text remains inside its saved row height.
	for _, values := range w.Query2[scene.Text2D, scene.UIGlobalTransform]() {
		if values.A.Ref().Value == "wrapped" {
			wrappedTop = values.B.Ref().Matrix[13]
		}
		if values.A.Ref().Value == "lines" {
			wrappedBottom = values.B.Ref().Matrix[13]
		}
	}
	if wrappedBottom <= wrappedTop {
		t.Fatal("explicit cell wrapping was lost")
	}
	if n := testing.AllocsPerRun(100, func() { m.syncCustomGrid(w) }); n != 0 {
		t.Fatalf("stable grid allocated %g", n)
	}
	// Use the real pointer path to select across a blank column.
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
	x, y := point("repeat", 0)
	keys.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
	x, y = point("alpha", 5)
	keys.Enqueue(input.Event{Kind: input.EventMouseMove, X: x, Y: y})
	languageInteractionStep(t, app)
	keys.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
	if got := m.actions.ui.selection.text(); got != "repeat\t\talpha" {
		t.Fatalf("pointer selected %q", got)
	}
	// Keep selection offsets while their original DOM nodes are evicted.
	selection := &m.actions.ui.selection
	selection.surface, selection.area = doc.selection, g.area
	selection.region = doc.selection.selectables[0].id
	selection.collect(w, doc.selection, "office-content")
	if len(selection.spans) < 3 {
		t.Fatalf("missing initial selection spans: %+v", selection.spans)
	}
	selection.anchorOffset, selection.caretOffset = 0, len("repeat\talpha  beta\ncopy-end")
	if got := selection.text(); got != "repeat\t\talpha  beta\n\tcopy-end" {
		t.Fatalf("sparse TSV copied %q", got)
	}
	// Both axes must move when revealing an unmounted match.
	m.findQuery = "中文🙂 far-target"
	m.runShortcut(w, pencodeShortcutFind)
	markPencodeRootDirtyByModel(w, m)
	for range 10 {
		languageInteractionStep(t, app)
	}
	if len(m.findMatches) != 1 {
		t.Fatalf("whole grid search: %v", m.findMatches)
	}
	if _, ok := w.HTMLNode(m.surface, cellID(5000, 150)); !ok {
		t.Fatalf("search target not mounted: rows=%d..%d cols=%d..%d scroll=%+v", g.firstRow, g.endRow, g.firstColumn, g.endColumn, w.Ref[scene.UIScroll](g.area))
	}
	if node, ok := w.HTMLNode(m.surface, cellID(5000, 150)); ok {
		bounds, _ := node.Bounds()
		if bounds.Width != 180 || bounds.Height != 42 {
			t.Fatalf("saved dimensions lost: %+v", bounds)
		}
	}
	if _, ok := w.HTMLNode(m.surface, cellID(1, 1)); ok {
		t.Fatal("old viewport cell survived eviction")
	}
	verify()
	visible := false
	for _, entity := range doc.find.text.quads {
		if value, ok := w.Get[scene.Visibility](entity); ok && value.Visible {
			visible = true
		}
	}
	if !visible {
		t.Fatalf("target has no search highlight: spans=%+v chunks=%+v", doc.find.text.spans, doc.find.chunks)
	}
	if selection.surface == nil || selection.text() != "repeat\t\talpha  beta\n\tcopy-end" {
		t.Fatal("scrolling discarded selection offsets")
	}
	// Search repeated values at the far end, independently of the mounted text.
	m.findQuery = "repeat"
	m.refreshFindMatches()
	m.findIndex = 1
	doc.find.reveal = true
	step()
	step()
	if len(m.findMatches) != 2 {
		t.Fatalf("repeat matches: %v", m.findMatches)
	}
	if _, ok := w.HTMLNode(m.surface, cellID(10000, 200)); !ok {
		t.Fatal("last row/column was not mounted")
	}
	verify()
	visible = false
	for _, entity := range doc.find.text.quads {
		if v, ok := w.Get[scene.Visibility](entity); ok && v.Visible {
			visible = true
		}
	}
	if !visible {
		t.Fatal("repeated far match lost its offset/highlight")
	}
	if n := testing.AllocsPerRun(100, func() { m.syncCustomGrid(w); m.syncRenderedFind(w); selection.sync(w, m) }); n != 0 {
		t.Fatalf("stable grid/find/selection allocated %g", n)
	}
	// Direct native scrollbar jumps must load blank regions too.
	scroll := w.Mut[scene.UIScroll](g.area)
	scroll.Position[0], scroll.Position[1] = -64*60, -21*2500
	step()
	if g.firstRow > 2500 || g.endRow < 2500 || g.firstColumn > 60 || g.endColumn < 60 {
		t.Fatal("scroll jump did not load blank window")
	}
	verify()
	// Resize/zoom keeps the window bounded and copies all data, including evicted rows.
	m.settings.Zoom = 150
	markPencodeRootDirtyByModel(w, m)
	step()
	verify()
	selection.selectAll()
	if got := selection.text(); !strings.Contains(got, "中文🙂 far-target") || !strings.HasSuffix(got, "repeat") {
		t.Fatal("select all omitted unmounted data")
	}
	// A theme/CSS refresh preserves data offsets; switching worksheets clears them.
	next := *view
	next.CSS += " .pencode-grid-cell { color: #abcdef; }"
	if err := doc.setView(&next); err != nil {
		t.Fatal(err)
	}
	markPencodeRootDirtyByModel(w, m)
	step()
	if selection.surface == nil || selection.grid != doc.grid || !selection.all {
		t.Fatal("style refresh lost virtual selection")
	}
	replacement := next
	replacement.HTML = strings.Replace(view.HTML, "far-target", "new-target", 1)
	if err := doc.setView(&replacement); err != nil {
		t.Fatal(err)
	}
	markPencodeRootDirtyByModel(w, m)
	step()
	if selection.surface != nil || selection.grid != nil {
		t.Fatal("replacing grid retained old selection/data")
	}
}

func TestOfficeVirtualGridSparseExtentAndValidation(t *testing.T) {
	parse := func(payload string) (*pencodeVirtualGrid, error) {
		node := dom.NewElement("div", dom.Attribute{Name: "data-ref", Value: "grid"}, dom.Attribute{Name: "id", Value: "pencode-extension-grid"})
		return parsePencodeGrid(node, payload)
	}
	payload := `{"rows":1048576,"columns":16384,"rowHeight":21,"columnWidth":64,"styles":[""],"cells":[[1048576,16384,"last",0]]}`
	g, err := parse(payload)
	if err != nil {
		t.Fatal(err)
	}
	if g.content != "last" || len(g.rows.sizes) != 1 || len(g.columns.sizes) != 1 {
		t.Fatal("sparse extent expanded into dense data")
	}
	if g.columns.offset(16385) != 48+16384*64 || g.rows.offset(1048577) != 22+1048576*21 {
		t.Fatal("incorrect sparse geometry")
	}
	for _, broken := range []string{
		strings.Replace(payload, "1048576,16384", "1048577,16384", 1),
		strings.Replace(payload, `"rowHeight":21`, `"rowHeight":0`, 1),
		strings.Replace(payload, `"styles":[""]`, `"styles":[]`, 1),
		strings.Replace(payload, `"last",0]`, `"last",0,1]`, 1),
		strings.Replace(payload, `"cells":`, `"rowSizes":[[1,20],[1,30]],"cells":`, 1),
	} {
		if _, err := parse(broken); err == nil {
			t.Fatalf("accepted %s", broken)
		}
	}
}

func TestOfficeVirtualGridCopyUsedColumns(t *testing.T) {
	node := dom.NewElement("div", dom.Attribute{Name: "data-ref", Value: "grid"}, dom.Attribute{Name: "id", Value: "pencode-extension-grid"})
	grid, err := parsePencodeGrid(node, `{"rows":100,"columns":26,"rowHeight":21,"columnWidth":64,"styles":[""],"cells":[[3,3,"left",0],[4,2,"🙂 value",0],[4,4,"right",0]]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := grid.copyText(0, len(grid.content), true); got != "\tleft\n🙂 value\t\tright" {
		t.Fatalf("used column alignment lost: %q", got)
	}
	if got := grid.copyText(1, len(grid.content)-2, false); got != "eft\n\t🙂 value\t\trig" {
		t.Fatalf("partial range lost: %q", got)
	}
}
