package workbench

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/html/dom"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	htmlui "github.com/onix-engine/onix/ui/html"
)

// A grid carries sparse values, not a full HTML table. Coordinates are one based;
// index zero is reserved for the row/column headings created by the host.
type pencodeGridCell struct {
	value              string
	row, column, style int
	start, end         int
}

func (c *pencodeGridCell) UnmarshalJSON(data []byte) error {
	var tuple []json.RawMessage
	if err := json.Unmarshal(data, &tuple); err != nil {
		return err
	}
	if len(tuple) != 4 {
		return fmt.Errorf("grid cell requires row, column, value and style")
	}
	for i, target := range []any{&c.row, &c.column, &c.value, &c.style} {
		if err := json.Unmarshal(tuple[i], target); err != nil {
			return err
		}
	}
	return nil
}

type pencodeGridData struct {
	Source                 string            `json:"source"`
	Styles                 []string          `json:"styles"`
	Cells                  []pencodeGridCell `json:"cells"`
	RowSizes               [][2]float64      `json:"rowSizes"`
	ColumnSizes            [][2]float64      `json:"columnSizes"`
	Rows, Columns          int
	RowHeight, ColumnWidth float64
}

type pencodeGridAxis struct {
	sizes  [][2]float64
	deltas []float64
	step   float64
	count  int
}

func newPencodeGridAxis(count int, step, heading float64, sizes [][2]float64) (pencodeGridAxis, error) {
	axis := pencodeGridAxis{step: step, count: count + 1}
	if math.IsNaN(step) || math.IsInf(step, 0) || step < 1 || step > 4096 {
		return axis, fmt.Errorf("invalid grid size")
	}
	axis.sizes = append(sizes, [2]float64{0, heading})
	sort.Slice(axis.sizes, func(i, j int) bool { return axis.sizes[i][0] < axis.sizes[j][0] })
	axis.deltas = make([]float64, len(axis.sizes))
	for i, size := range axis.sizes {
		if math.IsNaN(size[0]) || math.IsInf(size[0], 0) || size[0] != math.Trunc(size[0]) || size[0] < 0 || size[0] > float64(count) ||
			math.IsNaN(size[1]) || math.IsInf(size[1], 0) || size[1] < 1 || size[1] > 4096 || i > 0 && size[0] == axis.sizes[i-1][0] {
			return axis, fmt.Errorf("invalid grid size override")
		}
		axis.deltas[i] = size[1] - step
		if i > 0 {
			axis.deltas[i] += axis.deltas[i-1]
		}
	}
	return axis, nil
}

func (a *pencodeGridAxis) offset(index int) float64 {
	i := sort.Search(len(a.sizes), func(i int) bool { return a.sizes[i][0] >= float64(index) })
	position := float64(index) * a.step
	if i > 0 {
		position += a.deltas[i-1]
	}
	return position
}

func (a *pencodeGridAxis) window(position, viewport float64, overscan int) (int, int) {
	first := sort.Search(a.count, func(i int) bool { return a.offset(i+1) > position })
	end := sort.Search(a.count, func(i int) bool { return a.offset(i) >= position+viewport })
	return max(0, first-overscan), min(a.count, end+overscan)
}

type pencodeVirtualGrid struct {
	node                          *dom.Node
	remote                        *pencodeGridSource
	ref, id, content              string
	styles                        []string
	cells                         []pencodeGridCell
	nodes                         []htmlui.VNode
	records                       []htmlui.VNode
	attributes                    [][6]htmlui.VNodeAttribute
	metadata                      []*dom.Node
	chunks                        []pencodeRenderedFindChunk
	rows, columns                 pencodeGridAxis
	component, area               ecs.Entity
	firstRow, endRow              int
	focusOffset                   int
	firstColumn, endColumn        int
	originX, originY              float32
	cacheRows, cacheColumns       int
	viewportWidth, viewportHeight float64
}

func parsePencodeGrid(node *dom.Node, source string) (*pencodeVirtualGrid, error) {
	var data pencodeGridData
	if err := json.Unmarshal([]byte(source), &data); err != nil {
		return nil, fmt.Errorf("invalid grid: %w", err)
	}
	if data.Rows < 1 || data.Rows > 1048576 || data.Columns < 1 || data.Columns > 16384 || len(node.Children) != 0 {
		return nil, fmt.Errorf("invalid grid extent or children")
	}
	g := &pencodeVirtualGrid{node: node, styles: data.Styles, focusOffset: -1}
	g.ref, _ = node.Attr("data-ref")
	g.id, _ = node.Attr("id")
	if g.ref == "" || g.id == "" {
		return nil, fmt.Errorf("grid requires data-ref and id")
	}
	var err error
	g.rows, err = newPencodeGridAxis(data.Rows, data.RowHeight, 22, data.RowSizes)
	if err != nil {
		return nil, err
	}
	g.columns, err = newPencodeGridAxis(data.Columns, data.ColumnWidth, 48, data.ColumnSizes)
	if err != nil {
		return nil, err
	}
	if data.Source != "" {
		g.remote = &pencodeGridSource{name: data.Source}
	}
	if err := g.setCells(data.Cells); err != nil {
		return nil, err
	}
	// Remove the payload before the template is cloned into the live DOM.
	node.RemoveAttr("data-pencode-grid")
	node.SetAttr("role", "grid")
	node.SetAttr("aria-rowcount", strconv.Itoa(data.Rows+1))
	node.SetAttr("aria-colcount", strconv.Itoa(data.Columns+1))
	node.SetAttr("style", fmt.Sprintf("position:relative;width:%gpx;height:%gpx", g.columns.offset(g.columns.count), g.rows.offset(g.rows.count)))
	g.setWindow(0, 0, 1200, 800)
	return g, nil
}

func (g *pencodeVirtualGrid) setCells(cells []pencodeGridCell) error {
	var b strings.Builder
	for i := range cells {
		cell := &cells[i]
		if cell.row < 1 || cell.row >= g.rows.count || cell.column < 1 || cell.column >= g.columns.count || cell.style < 0 || cell.style >= len(g.styles) || cell.value == "" {
			return fmt.Errorf("invalid grid cell")
		}
		if i > 0 {
			previous := &cells[i-1]
			if cell.row < previous.row || cell.row == previous.row && cell.column <= previous.column {
				return fmt.Errorf("grid cells must be unique and sorted")
			}
			if cell.row == previous.row {
				b.WriteByte('\t')
			} else {
				b.WriteByte('\n')
			}
		}
		cell.start = b.Len()
		b.WriteString(cell.value)
		cell.end = b.Len()
	}
	g.cells, g.content = cells, b.String()
	clear(g.records)
	return nil
}

func (g *pencodeVirtualGrid) setWindow(x, y, width, height float64) bool {
	if width == g.viewportWidth && height == g.viewportHeight {
		firstRow, endRow := g.rows.window(y, height, 0)
		firstColumn, endColumn := g.columns.window(x, width, 0)
		if firstRow >= g.firstRow && endRow <= g.endRow && firstColumn >= g.firstColumn && endColumn <= g.endColumn {
			return false
		}
	}
	g.viewportWidth, g.viewportHeight = width, height
	firstRow, endRow := g.rows.window(y, height, 8)
	firstColumn, endColumn := g.columns.window(x, width, 2)
	if g.firstRow == firstRow && g.endRow == endRow && g.firstColumn == firstColumn && g.endColumn == endColumn {
		return false
	}
	g.firstRow, g.endRow, g.firstColumn, g.endColumn = firstRow, endRow, firstColumn, endColumn
	return true
}

func pencodeGridColumn(index int) string {
	var name [3]byte
	i := len(name)
	for index > 0 {
		i--
		name[i] = byte('A' + (index-1)%26)
		index = (index - 1) / 26
	}
	return string(name[i:])
}

func (g *pencodeVirtualGrid) render(target *htmlui.View) {
	rowCount, columnCount := g.endRow-g.firstRow, g.endColumn-g.firstColumn
	count := rowCount * columnCount
	// A bounded ring retains attributes for overlapping cells as the window moves.
	// Only incoming cells need new strings; native keyed reconciliation reuses DOM.
	if rowCount > g.cacheRows || columnCount > g.cacheColumns {
		g.cacheRows, g.cacheColumns = max(g.cacheRows, rowCount), max(g.cacheColumns, columnCount)
		for len(g.records) < g.cacheRows*g.cacheColumns {
			g.records = append(g.records, htmlui.VNode{})
			g.attributes = append(g.attributes, [6]htmlui.VNodeAttribute{})
			g.metadata = append(g.metadata, &dom.Node{Type: dom.ElementNode, Data: "div", Parent: g.node})
		}
		clear(g.records)
	}
	for len(g.nodes) < count {
		g.nodes = append(g.nodes, htmlui.VNode{})
	}
	g.chunks = g.chunks[:0]
	n := 0
	for r := g.firstRow; r < g.endRow; r++ {
		cellIndex := sort.Search(len(g.cells), func(i int) bool { c := &g.cells[i]; return c.row > r || c.row == r && c.column >= g.firstColumn })
		for c := g.firstColumn; c < g.endColumn; c++ {
			key := uint64(r)*uint64(g.columns.count) + uint64(c) + 1
			var cell *pencodeGridCell
			if cellIndex < len(g.cells) && g.cells[cellIndex].row == r && g.cells[cellIndex].column == c {
				cell = &g.cells[cellIndex]
				cellIndex++
			}
			visible, renderStart := "", 0
			if cell != nil {
				visible, renderStart = g.cellText(cell)
			}
			slot := (r%g.cacheRows)*g.cacheColumns + c%g.cacheColumns
			vnode, attrs := &g.records[slot], &g.attributes[slot]
			if vnode.Key != key {
				value, css, class, role := "", "", "pencode-grid-cell", "gridcell"
				if r == 0 {
					value, role = pencodeGridColumn(c), "columnheader"
				} else if c == 0 {
					value, role = strconv.Itoa(r), "rowheader"
				} else if cell != nil {
					value, css = visible, g.styles[cell.style]
					if strings.Contains(css, "vertical-align:middle;") {
						css += "display:flex;flex-direction:column;justify-content:center;"
					} else if strings.Contains(css, "vertical-align:bottom;") {
						css += "display:flex;flex-direction:column;justify-content:flex-end;"
					}
				}
				if r == 0 || c == 0 {
					class += " pencode-grid-header"
				}
				x, y := g.columns.offset(c), g.rows.offset(r)
				*attrs = [6]htmlui.VNodeAttribute{
					{Name: "id", Value: g.id + "/cell/" + strconv.Itoa(r) + "/" + strconv.Itoa(c)},
					{Name: "class", Value: class}, {Name: "role", Value: role},
					{Name: "style", Value: fmt.Sprintf("left:%gpx;top:%gpx;width:%gpx;height:%gpx;%s", x, y, g.columns.offset(c+1)-x, g.rows.offset(r+1)-y, css)},
					{Name: "aria-rowindex", Value: strconv.Itoa(r + 1)}, {Name: "aria-colindex", Value: strconv.Itoa(c + 1)},
				}
				*vnode = htmlui.VNode{Key: key, Tag: "div", Text: value}
				g.metadata[slot].SetAttr("id", attrs[0].Value)
			}
			// The attribute backing array can move while growing the first window.
			vnode.Attributes = attrs[:]
			g.nodes[n] = *vnode
			if cell != nil {
				g.chunks = append(g.chunks, pencodeRenderedFindChunk{node: g.metadata[slot], start: cell.start + renderStart, end: cell.start + renderStart + len(visible)})
			}
			n++
		}
	}
	target.Keyed(g.ref, g.nodes[:count])
}

// Bound text shaping inside clipped cells. A find result requests the slice
// containing its byte offset; the source remains intact for search and copying.
func (g *pencodeVirtualGrid) cellText(cell *pencodeGridCell) (string, int) {
	if len(cell.value) <= 8192 {
		return cell.value, 0
	}
	start := 0
	if g.focusOffset >= cell.start && g.focusOffset < cell.end {
		start = g.focusOffset - cell.start
	}
	for start > 0 && !utf8.RuneStart(cell.value[start]) {
		start--
	}
	end := min(len(cell.value), start+8192)
	for end < len(cell.value) && !utf8.RuneStart(cell.value[end]) {
		end--
	}
	return cell.value[start:end], start
}

// Observe native scrolling, including scrollbar drags and search navigation.
// A stable window does not rebuild nodes or allocate.
func (m *workspaceModel) syncCustomGrid(world *module.Content) (changed bool) {
	document := m.customDocuments[m.activeFile()]
	if document == nil || document.grid == nil || m.sourceState().visible() {
		return false
	}
	g := document.grid
	viewport, ok := world.HTMLNode(m.surface, "custom-editor")
	if !ok {
		return false
	}
	// Grid data can scroll independently below an editor's fixed toolbar.
	for parent := g.node.Parent; parent != nil; parent = parent.Parent {
		id, _ := parent.Attr("id")
		if node, found := world.HTMLNode(m.surface, id); found {
			if _, scrolling := node.ScrollContainer(); scrolling {
				viewport = node
				break
			}
		}
	}
	g.area, ok = viewport.ScrollContainer()
	if !ok {
		return false
	}
	scroll := world.Ref[scene.UIScroll](g.area)
	node, ok := world.HTMLNode(m.surface, g.id)
	if !ok || scroll == nil {
		return false
	}
	bounds, valid := node.Bounds()
	outer, validOuter := viewport.Bounds()
	if !valid || !validOuter {
		return false
	}
	g.originX, g.originY = bounds.X-outer.X, bounds.Y-outer.Y
	if g.setWindow(float64(-scroll.Position[0]-g.originX), float64(-scroll.Position[1]-g.originY), float64(scroll.ViewportSize[0]), float64(scroll.ViewportSize[1])) {
		changed = world.UpdateState[*pencodeCustomDocument, struct{}](g.component, func(*struct{}) {})
		if changed && m.app != nil {
			m.app.RequestRender()
		}
	}
	document.requestGrid(false)
	return changed
}

func (g *pencodeVirtualGrid) reveal(world *module.Content, offset int) bool {
	i := sort.Search(len(g.cells), func(i int) bool { return g.cells[i].end > offset })
	if i == len(g.cells) {
		return false
	}
	scroll := world.Mut[scene.UIScroll](g.area)
	if scroll == nil {
		return false
	}
	changed := false
	cell := &g.cells[i]
	if len(cell.value) > 8192 && (g.focusOffset != offset) {
		g.focusOffset = offset
		clear(g.records)
		world.UpdateState[*pencodeCustomDocument, struct{}](g.component, func(*struct{}) {})
		changed = true
	}
	center := [2]float32{g.originX + float32((g.columns.offset(cell.column)+g.columns.offset(cell.column+1))/2), g.originY + float32((g.rows.offset(cell.row)+g.rows.offset(cell.row+1))/2)}
	for axis := range 2 {
		position := min(float32(0), max(-max(float32(0), scroll.ContentSize[axis]-scroll.ViewportSize[axis]), scroll.ViewportSize[axis]/2-center[axis]))
		changed = changed || position != scroll.Position[axis]
		scroll.Position[axis], scroll.Momentum[axis] = position, 0
	}
	return changed
}

// Only copying expands sparse cell boundaries into TSV. The search index stays
// proportional to populated text even for cells at the last Excel row/column.
func (g *pencodeVirtualGrid) copyText(start, end int, all bool) string {
	firstColumn := 1
	if all {
		firstColumn = g.columns.count
		for _, cell := range g.cells {
			firstColumn = min(firstColumn, cell.column)
		}
	}
	first := sort.Search(len(g.cells), func(i int) bool { return g.cells[i].end > start })
	var b strings.Builder
	for i := first; i < len(g.cells) && g.cells[i].start < end; i++ {
		cell := &g.cells[i]
		if i == first && all {
			for range cell.column - firstColumn {
				b.WriteByte('\t')
			}
		}
		if i > first {
			previous := &g.cells[i-1]
			if cell.row == previous.row {
				for range cell.column - previous.column {
					b.WriteByte('\t')
				}
			} else {
				for range cell.row - previous.row {
					b.WriteByte('\n')
				}
				for range max(0, cell.column-firstColumn) {
					b.WriteByte('\t')
				}
			}
		}
		b.WriteString(cell.value[max(0, start-cell.start):min(len(cell.value), end-cell.start)])
	}
	return b.String()
}
