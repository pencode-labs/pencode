package workbench

import (
	"time"
	"unicode/utf8"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/terminal"
)

type terminalTextPoint struct{ row, rune int }

type terminalTextSelection struct {
	nextScroll                           time.Time
	anchor, caret                        terminalTextPoint
	active, dragging, capture, alternate bool
}

func terminalRow(s *terminal.Screen, row int) []terminal.Cell {
	if row < int(s.Scrolled) {
		return s.HistoryRow(row - int(s.Scrolled) + len(s.History)/s.Cols)
	}
	start := (row - int(s.Scrolled)) * s.Cols
	return s.Cells[start : start+s.Cols]
}

func terminalRowRunes(cells []terminal.Cell) int {
	count := 0
	for col := 0; col < len(cells); {
		cell := cells[col]
		count++
		for _, r := range cell.Combining {
			if r != 0 {
				count++
			}
		}
		col += max(1, int(cell.Width))
	}
	return count
}

func (m *workspaceModel) terminalTextPoint(index int) terminalTextPoint {
	v := &m.terminalView
	point := terminalTextPoint{row: v.firstRow}
	for _, r := range v.value {
		if index <= 0 {
			break
		}
		index--
		if r == '\n' {
			point.row++
			point.rune = 0
		} else {
			point.rune++
		}
	}
	return point
}

// Store positions in scrollback coordinates; the native field only contains
// visible rows, so its offsets cannot preserve a selection across scrolling.
func (m *workspaceModel) updateTerminalSelection(world *module.Content, state *input.State, x, y float32, inside bool) bool {
	item := m.terminals.active
	s := item.screen
	selection := &item.selection
	field := world.Mut[engineui.TextField](m.terminalView.control)
	if field == nil || m.terminalView.active != item {
		return false
	}
	if state.MousePressed(input.MouseLeft) {
		previous := *selection
		*selection = terminalTextSelection{}
		shift := state.KeyHeld(input.KeyLeftShift) || state.KeyHeld(input.KeyRightShift)
		if inside && (s.MouseTracking == 0 || shift) {
			selection.anchor = previous.anchor
			selection.active, selection.dragging, selection.capture = true, true, true
			selection.alternate = s.Alternate()
			if !shift || !previous.active {
				selection.anchor = terminalTextPoint{row: -1}
			}
		}
	}
	if !selection.dragging {
		return false
	}
	if !state.MouseHeld(input.MouseLeft) {
		selection.dragging = false
		selection.nextScroll = time.Time{}
		if !state.MouseReleased(input.MouseLeft) {
			return false
		}
	}
	// Retain native click and double-click selection until the pointer moves.
	p := state.Pointer()
	if state.MousePressed(input.MouseLeft) || p.DeltaX == 0 && p.DeltaY == 0 && p.WheelY == 0 && selection.nextScroll.IsZero() {
		return false
	}
	node, ok := world.HTMLNode(m.surface, "terminal-content")
	if !ok {
		return false
	}
	bounds, ok := node.Bounds()
	if !ok {
		return false
	}
	text := world.Mut[scene.Text2D](m.terminalView.text)
	width, height, ok := scene.MeasureTextValue2D(text, "M")
	if !ok || width <= 0 || height <= 0 {
		return false
	}
	link, ok := world.Get[htmlui.ControlText](m.terminalView.text)
	if !ok {
		return false
	}
	top := bounds.Y + link.Content.Y
	bottom := top + float32(s.Rows)*height
	delta := 0
	if y < top {
		delta = 1
	} else if y >= bottom {
		delta = -1
	}
	changed := false
	if delta != 0 && !s.Alternate() && selection.dragging {
		now := time.Now()
		if !now.Before(selection.nextScroll) {
			next := max(0, min(len(s.History)/s.Cols, item.scroll+delta))
			changed = next != item.scroll
			item.scroll = next
			selection.nextScroll = now.Add(40 * time.Millisecond)
		}
		if m.app != nil && m.app.WakeAt != nil {
			m.app.WakeAt(selection.nextScroll)
		}
	} else {
		selection.nextScroll = time.Time{}
	}
	row := max(0, min(s.Rows-1, int((y-top)/height)))
	point := terminalTextPoint{row: int(s.Scrolled) - item.scroll + row}
	column := (x - bounds.X - link.Content.X) / width
	cells := terminalRow(s, point.row)
	for col := 0; col < len(cells); {
		cell := cells[col]
		span := max(1, int(cell.Width))
		if column < float32(col)+float32(span)/2 {
			break
		}
		point.rune++
		for _, r := range cell.Combining {
			if r != 0 {
				point.rune++
			}
		}
		col += span
	}
	if y < top {
		point.rune = 0
	} else if y >= bottom {
		point.rune = terminalRowRunes(cells)
	}
	selection.caret = point
	return changed
}

func (m *workspaceModel) syncTerminalSelection(world *module.Content) bool {
	v := &m.terminalView
	if m.bottom.active != pencodePanelTerminal || v.active == nil || !v.active.selection.active {
		return false
	}
	selection := &v.active.selection
	field := world.Mut[engineui.TextField](v.control)
	if field == nil {
		return false
	}
	if selection.capture {
		if selection.anchor.row < 0 {
			selection.anchor = m.terminalTextPoint(field.Anchor)
		}
		selection.caret = m.terminalTextPoint(field.Caret)
		selection.capture = false
	}
	s := v.active.screen
	first := int(s.Scrolled) - len(s.History)/s.Cols
	if selection.alternate != s.Alternate() || selection.anchor.row < first || selection.caret.row < first || selection.anchor.row >= int(s.Scrolled)+s.Rows || selection.caret.row >= int(s.Scrolled)+s.Rows {
		*selection = terminalTextSelection{}
		changed := field.Anchor != field.Caret
		field.Anchor = field.Caret
		return changed
	}
	index := func(point terminalTextPoint) int {
		row := v.firstRow
		if point.row < row {
			return 0
		}
		count := 0
		for _, r := range v.value {
			if row == point.row && point.rune == 0 {
				return count
			}
			count++
			if r == '\n' {
				if row == point.row {
					return count - 1
				}
				row++
			} else if row == point.row {
				point.rune--
			}
		}
		return count
	}
	anchor, caret := index(selection.anchor), index(selection.caret)
	changed := field.Anchor != anchor || field.Caret != caret
	field.Anchor, field.Caret = anchor, caret
	return changed
}

func (m *workspaceModel) terminalSelectedText() string {
	item := m.terminals.active
	selection := &item.selection
	s := item.screen
	start, end := selection.anchor, selection.caret
	if start.row > end.row || start.row == end.row && start.rune > end.rune {
		start, end = end, start
	}
	if start == end {
		return ""
	}
	first := int(s.Scrolled) - len(s.History)/s.Cols
	if start.row < first || end.row >= int(s.Scrolled)+s.Rows {
		return ""
	}
	buffer := m.terminalView.selectionBuffer[:0]
	for row := start.row; row <= end.row; row++ {
		a, b := 0, int(^uint(0)>>1)
		if row == start.row {
			a = start.rune
		}
		if row == end.row {
			b = end.rune
		}
		index, offset := 0, len(buffer)
		cells := terminalRow(s, row)
		for col := 0; col < len(cells); {
			cell := cells[col]
			r := cell.Rune
			if r == 0 {
				r = ' '
			}
			if index >= a && index < b {
				buffer = utf8.AppendRune(buffer, r)
			}
			index++
			for _, r := range cell.Combining {
				if r == 0 {
					break
				}
				if index >= a && index < b {
					buffer = utf8.AppendRune(buffer, r)
				}
				index++
			}
			col += max(1, int(cell.Width))
		}
		if row < end.row {
			for len(buffer) > offset && buffer[len(buffer)-1] == ' ' {
				buffer = buffer[:len(buffer)-1]
			}
			buffer = append(buffer, '\n')
		}
	}
	m.terminalView.selectionBuffer = buffer
	return string(buffer)
}
