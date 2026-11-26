package workbench

import (
	"slices"
	"strings"

	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/terminal"
)

type extensionTerminalBinding struct {
	paint                *extensionTerminal
	ref, id, key, source string
}

// Keep the renderer across snapshots of the same stream. Release its native
// decoration entities when an output is replaced, removed or closed.
func (s *pencodeExtensionSurface) setTerminals(next []extensionTerminalBinding) {
	w := s.model.fontResources
	for _, old := range s.terminals {
		i := slices.IndexFunc(next, func(b extensionTerminalBinding) bool { return b.ref == old.ref && b.key == old.key })
		if i < 0 {
			old.paint.unbind(w)
			continue
		}
		old.paint.capture(w)
		next[i].paint = old.paint
	}
	for i := range next {
		b := &next[i]
		if b.paint == nil {
			b.paint = &extensionTerminal{follow: true, dirty: true}
		}
		b.paint.setSource(b.source)
	}
	s.terminals = next
}

// A readonly textarea keeps native text selection, clipboard and scrollbars.
// Its stream key identifies which snapshots share a reading position.
type extensionTerminal struct {
	screen *terminal.Screen
	theme  *pencodeTheme
	source string
	terminalPaint
	control, visual     ecs.Entity
	cols, rows, parsed  int
	anchor, caretOffset int
	scrollX, scrollY    float32
	dirty, follow       bool
}

// parsed counts bytes already fed to the screen, including across several
// snapshots published before the next frame. A replacement requires replay.
func (t *extensionTerminal) setSource(source string) {
	if t.source == source {
		return
	}
	if t.parsed >= 0 && !strings.HasPrefix(source, t.source[:t.parsed]) {
		t.parsed = -1
	}
	t.source, t.dirty = source, true
}

func (t *extensionTerminal) unbind(w *module.Content) {
	for _, e := range t.decorations {
		w.Despawn(e)
	}
	t.decorations = t.decorations[:0]
	t.control, t.visual = 0, 0
}

func (t *extensionTerminal) capture(w *module.Content) {
	if w == nil {
		return
	}
	area := w.Ref[engineui.Textarea](t.control)
	field := w.Ref[engineui.TextField](t.control)
	text := w.Ref[scene.Text2D](t.visual)
	link := w.Ref[htmlui.ControlText](t.visual)
	if area == nil || field == nil || text == nil || link == nil {
		return
	}
	_, height, ok := scene.MeasureText2D(text)
	t.anchor, t.caretOffset = field.Anchor, field.Caret
	t.scrollX, t.scrollY = area.ScrollX, area.ScrollY
	t.follow = t.anchor == t.caretOffset && (!ok || height-link.Content.Height-area.ScrollY < 24)
}

func (t *extensionTerminal) render(m *workspaceModel, cols, rows int) {
	replay := t.screen == nil || t.cols != cols || t.rows != rows || t.parsed < 0
	t.cols, t.rows, t.theme, t.dirty = cols, rows, m.workbenchTheme, false
	t.reset()
	if replay {
		if t.screen == nil {
			t.screen = terminal.NewScreen(cols, rows)
		} else {
			t.screen.Reset(cols, rows)
		}
		t.screen.HistoryLimit = max(t.screen.HistoryLimit, (256<<10)/cols)
		t.parsed = 0
	}
	screen := t.screen
	// Docker CLI pipes use LF. A PTY would translate LF to CRLF on output.
	for line := range strings.SplitAfterSeq(t.source[t.parsed:], "\n") {
		if content, ok := strings.CutSuffix(line, "\n"); ok {
			screen.Feed([]byte(content))
			screen.Feed([]byte("\r\n"))
		} else {
			screen.Feed([]byte(line))
		}
	}
	t.parsed = len(t.source)
	screen.Replies() // A read-only log has no PTY to receive terminal replies.
	if replay {
		screen.TrimHistory()
	}
	fg, bg := m.terminalColors()
	_, cursorRow := screen.Cursor()
	history := len(screen.History) / cols
	first := int(screen.Scrolled) - history
	last := int(screen.Scrolled) + cursorRow
	for row := int(screen.Scrolled) + rows - 1; row > last; row-- {
		if len(terminalLogRow(terminalRow(screen, row))) > 0 {
			last = row
			break
		}
	}
	for row := first; row <= last; row++ {
		if row > first {
			t.buffer = append(t.buffer, '\n')
			t.runes++
		}
		t.appendRow(terminalLogRow(terminalRow(screen, row)), row-first, -1, fg, bg)
	}
	t.value = string(t.buffer)
	if replay {
		if cap(t.buffer) > 64<<10 && len(t.buffer) <= cap(t.buffer)/4 {
			t.buffer = nil
		}
		t.spans = trimTerminalRuns(t.spans)
		t.backgrounds = trimTerminalRuns(t.backgrounds)
		t.underlines = trimTerminalRuns(t.underlines)
	}
}

func trimTerminalRuns[T any](runs []T) []T {
	if cap(runs) > 1024 && len(runs) <= cap(runs)/4 {
		return slices.Clone(runs)
	}
	return runs
}

func terminalLogRow(cells []terminal.Cell) []terminal.Cell {
	for len(cells) > 0 {
		c := cells[len(cells)-1]
		if c.Rune != ' ' || c.Background != terminal.Default || c.Inverse || c.Underline || c.Combining[0] != 0 {
			break
		}
		cells = cells[:len(cells)-1]
	}
	return cells
}

func (t *extensionTerminal) sync(w *module.Content, m *workspaceModel, id string) bool {
	control, ok := w.HTMLControl(m.surface, id)
	if !ok {
		return false
	}
	visual, ok := w.FindPart[engineui.ValuePart, scene.Text2D](control)
	if !ok {
		return false
	}
	text := w.Mut[scene.Text2D](visual)
	link := w.Ref[htmlui.ControlText](visual)
	if text == nil || link == nil {
		return false
	}
	font := m.codeFont()
	if font != nil {
		text.Font = font
		if binding := w.Mut[htmlui.ControlTextFont](control); binding != nil {
			binding.Font = font
		} else {
			_ = w.Insert(control, htmlui.ControlTextFont{Font: font})
		}
	}
	width, height, ok := scene.MeasureTextValue2D(text, "M")
	if !ok || width <= 0 || height <= 0 || link.Content.Width <= 0 || link.Content.Height <= 0 {
		return false
	}
	cols := max(2, min(1000, int(link.Content.Width/width)))
	rows := max(1, min(500, int(link.Content.Height/height)))
	changed := t.dirty || t.cols != cols || t.rows != rows || t.theme != m.workbenchTheme
	if t.control != control {
		t.unbind(w)
	}
	if changed {
		t.render(m, cols, rows)
	}
	field := w.Mut[engineui.TextField](control)
	area := w.Mut[engineui.Textarea](control)
	if field == nil || area == nil {
		return false
	}
	restore := changed || t.control != control || field.Value != t.value
	t.control, t.visual = control, visual
	if restore {
		w.SetControlValue(control, t.value)
		field = w.Mut[engineui.TextField](control)
		area = w.Mut[engineui.Textarea](control)
		field.Anchor, field.Caret = min(t.anchor, t.runes), min(t.caretOffset, t.runes)
		area.Wrap, area.FollowTail = false, t.follow
		area.RestoreScroll(t.scrollX, t.scrollY)
		if err := w.SyncUI(); err != nil {
			m.setActiveStatus(err.Error())
			return false
		}
	}
	text = w.Mut[scene.Text2D](visual)
	text.Spans = t.spans
	transform, _ := w.Get[scene.UITransform](visual)
	stack, _ := w.Get[scene.UIStackIndex](visual)
	first := max(0, int(area.ScrollY/height))
	used := t.paintRows(w, control, stack, transform, width, height, first, first+rows+2)
	for _, e := range t.decorations[used:] {
		w.SetIfChanged(e, scene.Visibility{})
	}
	return restore
}
