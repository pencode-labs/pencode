package workbench

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/onix-engine/onix/html/dom"
	"github.com/onix-engine/onix/input"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/peb"
)

// Viewport requests and their continuations share the editor's ordered event
// queue. One transfer at a time bounds traffic even during fast scrollbar drags.
type pencodeGridRequest struct {
	Request     int  `json:"request"`
	Offset      int  `json:"offset"`
	FirstRow    int  `json:"firstRow"`
	EndRow      int  `json:"endRow"`
	FirstColumn int  `json:"firstColumn"`
	EndColumn   int  `json:"endColumn"`
	All         bool `json:"all"`
	Metadata    bool `json:"metadata"`
}

type pencodeGridSource struct {
	name                                       string
	payload                                    []byte
	request                                    pencodeGridRequest
	loaded                                     [4]int
	sequence                                   int
	pending, complete, metadata, wantAll, copy bool
}

type pencodeGridChunk struct {
	Source  string `json:"source"`
	Data    []byte `json:"data"`
	Request int    `json:"request"`
	Offset  int    `json:"offset"`
	Done    bool   `json:"done"`
}

func (d *pencodeCustomDocument) requestGrid(all bool) {
	g := d.grid
	if g == nil || g.remote == nil || g.remote.complete {
		return
	}
	source := g.remote
	source.wantAll = source.wantAll || all
	window := [4]int{g.firstRow, g.endRow, g.firstColumn, g.endColumn}
	if source.pending || !source.wantAll && source.metadata && window == source.loaded {
		return
	}
	source.sequence++
	source.request = pencodeGridRequest{Request: source.sequence, FirstRow: window[0], EndRow: window[1], FirstColumn: window[2], EndColumn: window[3], All: source.wantAll, Metadata: !source.metadata}
	source.pending = true
	d.sendGridRequest()
}

func (d *pencodeCustomDocument) sendGridRequest() {
	source := d.grid.remote
	data, _ := json.Marshal(source.request)
	d.enqueue(peb.EditorEvent{Kind: "grid", Ref: source.name, Value: string(data)})
	d.dispatch()
}

func (d *pencodeCustomDocument) acceptGridChunk(view *peb.EditorView) error {
	tree, err := dom.Parse(view.HTML)
	if err != nil {
		return err
	}
	var raw string
	var visit func(*dom.Node)
	visit = func(node *dom.Node) {
		if value, ok := node.Attr("data-pencode-grid-chunk"); ok {
			raw = value
		}
		for _, child := range node.Children {
			visit(child)
		}
	}
	visit(tree)
	var chunk pencodeGridChunk
	if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
		return err
	}
	g := d.grid
	if g == nil || g.remote == nil || g.remote.name != chunk.Source {
		return nil
	}
	source := g.remote
	if !source.pending || chunk.Request != source.request.Request {
		return nil
	}
	if chunk.Offset != len(source.payload) || len(chunk.Data) == 0 && !chunk.Done || len(chunk.Data) > 64<<10 || chunk.Offset+len(chunk.Data) > 128<<20 {
		return fmt.Errorf("invalid grid chunk range")
	}
	source.payload = append(source.payload, chunk.Data...)
	if !chunk.Done {
		source.request.Offset = len(source.payload)
		d.sendGridRequest()
		return nil
	}
	var data pencodeGridData
	if err := json.Unmarshal(source.payload, &data); err != nil {
		return err
	}
	source.payload = nil
	if err := d.applyGridData(data); err != nil {
		return err
	}
	source.pending, source.metadata, source.complete = false, true, source.request.All
	source.loaded = [4]int{source.request.FirstRow, source.request.EndRow, source.request.FirstColumn, source.request.EndColumn}
	if model := d.model; model != nil {
		selection := &model.actions.ui.selection
		if source.complete && selection.grid == g {
			if selection.all {
				selection.anchorOffset, selection.caretOffset = 0, len(g.content)
			}
			if source.copy && model.fontResources != nil {
				if state, ok := model.fontResources.Resource[*input.State](); ok {
					state.SetClipboardText(selection.text())
				}
			}
		}
		if source.complete {
			source.copy = false
		}
		if model.renderedFind() == d.find && model.findOpen {
			model.refreshFindMatches()
		}
		if world := model.fontResources; world != nil {
			world.UpdateState[*pencodeCustomDocument, struct{}](g.component, func(*struct{}) {})
		}
	}
	if source.wantAll {
		d.requestGrid(true)
	}
	return nil
}

// Saved endpoints use cell coordinates while incoming windows change byte
// offsets. Sparse merging retains previously visited cells for drag selections.
type pencodeGridPoint struct{ row, column, offset int }

func (g *pencodeVirtualGrid) point(offset int) pencodeGridPoint {
	if len(g.cells) == 0 {
		return pencodeGridPoint{}
	}
	i := min(sort.Search(len(g.cells), func(i int) bool { return g.cells[i].end >= offset }), len(g.cells)-1)
	cell := &g.cells[i]
	return pencodeGridPoint{cell.row, cell.column, max(0, offset-cell.start)}
}
func (g *pencodeVirtualGrid) pointOffset(point pencodeGridPoint) int {
	i := sort.Search(len(g.cells), func(i int) bool {
		c := &g.cells[i]
		return c.row > point.row || c.row == point.row && c.column >= point.column
	})
	if i == len(g.cells) {
		return len(g.content)
	}
	cell := &g.cells[i]
	return cell.start + min(point.offset, len(cell.value))
}
func comparePencodeGridCell(a, b pencodeGridCell) int {
	if a.row != b.row {
		return a.row - b.row
	}
	return a.column - b.column
}
func (d *pencodeCustomDocument) applyGridData(data pencodeGridData) error {
	g := d.grid
	next := *g
	var selection *pencodeExtensionTextSelection
	var anchor, caret pencodeGridPoint
	if d.model != nil && d.model.actions.ui.selection.grid == g {
		selection = &d.model.actions.ui.selection
		anchor, caret = g.point(selection.anchorOffset), g.point(selection.caretOffset)
	}
	if data.Styles != nil {
		rows, err := newPencodeGridAxis(g.rows.count-1, g.rows.step, 22, data.RowSizes)
		if err != nil {
			return err
		}
		columns, err := newPencodeGridAxis(g.columns.count-1, g.columns.step, 48, data.ColumnSizes)
		if err != nil {
			return err
		}
		next.styles, next.rows, next.columns = data.Styles, rows, columns
	}
	cells := data.Cells
	if !g.remote.request.All {
		// Both lists are ordered. A repeated window replaces equal coordinates.
		cells = make([]pencodeGridCell, 0, len(g.cells)+len(data.Cells))
		i, j := 0, 0
		for i < len(g.cells) || j < len(data.Cells) {
			if j == len(data.Cells) || i < len(g.cells) && comparePencodeGridCell(g.cells[i], data.Cells[j]) < 0 {
				cells = append(cells, g.cells[i])
				i++
			} else {
				if i < len(g.cells) && comparePencodeGridCell(g.cells[i], data.Cells[j]) == 0 {
					i++
				}
				cells = append(cells, data.Cells[j])
				j++
			}
		}
	}
	if !slices.IsSortedFunc(cells, comparePencodeGridCell) {
		return fmt.Errorf("grid cells must be sorted")
	}
	if err := next.setCells(cells); err != nil {
		return err
	}
	g.styles, g.rows, g.columns = next.styles, next.rows, next.columns
	g.cells, g.content = next.cells, next.content
	if selection != nil {
		selection.anchorOffset, selection.caretOffset = g.pointOffset(anchor), g.pointOffset(caret)
		selection.content = g.content
	}
	if d.find != nil {
		d.find.content = snapshotText(g.content)
		d.find.dirty, d.find.reveal = true, true
	}
	// Template extent is also used on theme recompilation. Live extent overrides
	// update with the same native component state as the newly loaded cells.
	g.node.Attributes["style"] = fmt.Sprintf("position:relative;width:%gpx;height:%gpx", g.columns.offset(g.columns.count), g.rows.offset(g.rows.count))
	return nil
}

func (s *pencodeExtensionTextSelection) copy(state *input.State) {
	if s.grid != nil && s.grid.remote != nil && !s.grid.remote.complete {
		s.grid.remote.copy = true
		s.surface.document.requestGrid(true)
		return
	}
	state.SetClipboardText(s.text())
}

func (g *pencodeVirtualGrid) renderExtent(target *htmlui.View) {
	if g.remote != nil && g.remote.metadata {
		target.Attr(g.ref, "style", g.node.Attributes["style"])
	}
}

func isPencodeGridChunk(view *peb.EditorView) bool {
	return strings.Contains(view.HTML, `data-pencode-grid-chunk="`)
}
