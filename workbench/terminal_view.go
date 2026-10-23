package workbench

import (
	"unicode/utf8"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/projects/pencode/terminal"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

type terminalBackground struct {
	x, y, width int
	color       graphics.ColorRGBA
}
type pencodeTerminalView struct {
	control, text  ecs.Entity
	active         *pencodeTerminal
	revision       uint64
	scroll         int
	theme          *pencodeTheme
	value          string
	buffer         []byte
	spans          []scene.TextSpan2D
	backgrounds    []terminalBackground
	underlines     []terminalBackground
	decorations    []ecs.Entity
	tabs           sourceVNodeView
	tabsGeneration uint64
	tabsCount      int
	tabsActive     *pencodeTerminal
	tabsTheme      *pencodeTheme
	context        pencodeTerminalContext
	caret          int
	edits          []input.TextEdit
}

func (m *workspaceModel) renderTerminal(view *htmlui.View) {
	active := m.bottom.active == pencodePanelTerminal
	view.Attr("status-terminal", "aria-expanded", boolAttribute(active))
	setPanelVisible(view, "terminal-pane", active)
	setPanelVisible(view, "terminal-tools", active)
	for _, ref := range [...]string{"status-terminal", "terminal-new"} {
		view.Event(ref, htmlui.EventClick)
	}
	if active {
		view.Attr("bottom-panel", "class", "pencode-bottom-panel pencode-terminal-panel")
	} else {
		view.Attr("bottom-panel", "class", "pencode-bottom-panel")
	}
	m.renderTerminalContext(view, active)
	if !active {
		return
	}
	t := m.terminalState()
	v := &m.terminalView
	if v.tabs.used == 0 || v.tabsGeneration != t.nextID || v.tabsCount != len(t.items) || v.tabsActive != t.active || v.tabsTheme != m.workbenchTheme {
		v.tabsGeneration, v.tabsCount, v.tabsActive, v.tabsTheme = t.nextID, len(t.items), t.active, m.workbenchTheme
		b := &v.tabs
		b.reset()
		for _, item := range t.items {
			class, foreground, tabindex := "pencode-terminal-tab", "tab.inactiveForeground", "-1"
			if item == t.active {
				class, foreground, tabindex = class+" active", "tab.activeForeground", "0"
			}
			b.open("div", item.id, "class", class, "role", "presentation")
			b.text("button", item.selectRef, item.label, "id", item.selectRef, "type", "button", "role", "tab", "aria-selected", boolAttribute(item == t.active), "aria-controls", "terminal-pane", "tabindex", tabindex, "title", item.root)
			b.open("button", item.closeRef, "id", item.closeRef, "type", "button", "class", "pencode-icon-button", "title", "关闭终端", "aria-label", "关闭 "+item.label)
			b.text("img", "close", "", "src", m.themedIcon("icons/close.svg", foreground), "alt", "", "class", "pencode-close-icon")
			b.close()
			b.close()
		}
	}
	view.Keyed("terminal-tabs", v.tabs.nodes())
	if t.active != nil {
		view.Attr("terminal-pane", "aria-labelledby", t.active.selectRef)
	} else {
		view.Attr("terminal-pane", "aria-labelledby", "status-terminal")
	}
	m.prepareTerminalView()
	view.Text("terminal-content", v.value)
}
func terminalColor(c terminal.Color, fallback graphics.ColorRGBA) graphics.ColorRGBA {
	if c == terminal.Default {
		return fallback
	}
	return graphics.ColorFromRGB(uint8(c>>16), uint8(c>>8), uint8(c))
}
func (m *workspaceModel) prepareTerminalView() {
	v := &m.terminalView
	t := m.terminals
	if t == nil || t.active == nil {
		v.active = nil
		v.value = ""
		v.spans = v.spans[:0]
		v.backgrounds = v.backgrounds[:0]
		v.underlines = v.underlines[:0]
		return
	}
	item := t.active
	s := item.screen
	if s.Alternate() {
		item.scroll = 0
	}
	item.scroll = max(0, min(item.scroll, len(s.History)/s.Cols))
	if v.active == item && v.revision == s.Revision && v.scroll == item.scroll && v.theme == m.workbenchTheme {
		return
	}
	v.active, v.revision, v.scroll, v.theme = item, s.Revision, item.scroll, m.workbenchTheme
	v.buffer = v.buffer[:0]
	v.spans = v.spans[:0]
	v.backgrounds = v.backgrounds[:0]
	v.underlines = v.underlines[:0]
	v.caret = -1
	foreground, background := graphics.White, graphics.Black
	if m.workbenchTheme != nil {
		foreground = pencodeSyntaxColor(m.syntaxTheme.Color("editor.foreground"))
		background = pencodeSyntaxColor(m.syntaxTheme.Color("editor.background"))
	}
	s.DefaultForeground = terminal.Color(uint32(foreground.R*255)<<16 | uint32(foreground.G*255)<<8 | uint32(foreground.B*255))
	s.DefaultBackground = terminal.Color(uint32(background.R*255)<<16 | uint32(background.G*255)<<8 | uint32(background.B*255))
	runeIndex := 0
	cx, cy := s.Cursor()
	historyRows := len(s.History) / s.Cols
	for row := 0; row < s.Rows; row++ {
		line := historyRows - item.scroll + row
		cells := s.Cells
		offset := (line - historyRows) * s.Cols
		if line < historyRows {
			cells = s.HistoryRow(line)
			offset = 0
		}
		for col := 0; col < s.Cols; {
			c := cells[offset+col]
			width := max(1, int(c.Width))
			ink := c.Foreground
			if c.Bold {
				for i, color := range terminal.Palette[:8] {
					if ink == color {
						ink = terminal.Palette[i+8]
						break
					}
				}
			}
			fg, bg := terminalColor(ink, foreground), terminalColor(c.Background, background)
			if c.Inverse {
				fg, bg = bg, fg
			}
			if row == cy+item.scroll && col <= cx && cx < col+width {
				v.caret = runeIndex
			}
			start := len(v.buffer)
			r := c.Rune
			if r == 0 {
				r = ' '
			}
			v.buffer = utf8.AppendRune(v.buffer, r)
			runeIndex++
			for _, combining := range c.Combining {
				if combining == 0 {
					break
				}
				v.buffer = utf8.AppendRune(v.buffer, combining)
				runeIndex++
			}
			end := len(v.buffer)
			if n := len(v.spans); n > 0 && v.spans[n-1].Color == fg && v.spans[n-1].End == start {
				v.spans[n-1].End = end
			} else {
				v.spans = append(v.spans, scene.TextSpan2D{Start: start, End: end, Color: fg})
			}
			if bg != background {
				n := len(v.backgrounds)
				if n > 0 && v.backgrounds[n-1].color == bg && v.backgrounds[n-1].y == row && v.backgrounds[n-1].x+v.backgrounds[n-1].width == col {
					v.backgrounds[n-1].width += width
				} else {
					v.backgrounds = append(v.backgrounds, terminalBackground{col, row, width, bg})
				}
			}
			if c.Underline {
				n := len(v.underlines)
				if n > 0 && v.underlines[n-1].color == fg && v.underlines[n-1].y == row && v.underlines[n-1].x+v.underlines[n-1].width == col {
					v.underlines[n-1].width += width
				} else {
					v.underlines = append(v.underlines, terminalBackground{col, row, width, fg})
				}
			}

			col += width
		}
		if row < s.Rows-1 {
			v.buffer = append(v.buffer, '\n')
			runeIndex++
		}
	}
	v.value = string(v.buffer)
}
func (m *workspaceModel) bindTerminal(world *module.Content) {
	v := &m.terminalView
	visible := m.bottom.active == pencodePanelTerminal && m.screen == pencodeWorkspace
	if !visible {
		for _, e := range v.decorations {
			module.ContentSetIfChanged(world, e, scene.Visibility{})
		}
		return
	}
	bound := false
	if m.terminals != nil {
		for _, item := range m.terminals.items {
			for i, ref := range [...]string{item.selectRef, item.closeRef} {
				node, ok := world.HTMLNode(m.surface, ref)
				if !ok {
					continue
				}
				binding := &item.bindings[i]
				if binding.node == node {
					continue
				}
				if binding.handler == nil {
					binding.handler = func(*htmlui.PointerEvent) {
						m.terminalEvent(ref)
						markPencodeRootDirtyByModel(world, m)
					}
				}
				binding.node = node
				node.OnClick(binding.handler)
				bound = true
			}
		}
	}
	if bound {
		if err := world.SyncHTML(); err != nil {
			m.setActiveStatus(err.Error())
		}
	}
	control, ok := world.HTMLControl(m.surface, "terminal-content")
	if !ok {
		return
	}
	if v.control != control {
		for _, e := range v.decorations {
			world.Despawn(e)
		}
		v.decorations = v.decorations[:0]
		v.control = control
		v.text, _ = module.ContentFindPart[engineui.ValuePart, scene.Text2D](world, control)
		_ = module.ContentInsert(world, control, engineui.TextFieldKeyHandler{Handle: m.terminalKeys})
	}
	if !world.IsAlive(v.text) {
		v.text, _ = module.ContentFindPart[engineui.ValuePart, scene.Text2D](world, control)
	}

	text := module.ContentMut[scene.Text2D](world, v.text)
	if text == nil {
		if m.app != nil {
			m.app.RequestRender()
		}
		return
	}
	if m.terminals != nil && m.terminals.focus {
		if m.terminalFocused(world) {
			m.terminals.focus = false
		} else {
			world.Focus(control)
			if m.app != nil {
				m.app.RequestRender()
			}
		}
	}
	font := m.codeFont()
	if font != nil {
		text.Font = font
	}
	if binding := module.ContentMut[htmlui.ControlTextFont](world, control); binding != nil {
		binding.Font = font
	} else {
		_ = module.ContentInsert(world, control, htmlui.ControlTextFont{Font: font})
	}
	if m.workbenchTheme != nil {
		style := m.workbenchTheme.editorStyle
		style.Value.Caret.Background.Fill.A = 0
		if existing := module.ContentMut[engineui.StyleOverride[engineui.TextFieldStyle]](world, control); existing != nil {
			*existing = style
		} else {
			_ = module.ContentInsert(world, control, style)
		}
	}
	text.WrapMode = scene.TextWrapNone
	text.Spans = v.spans
	area := module.ContentMut[engineui.Textarea](world, control)
	if area != nil {
		area.Wrap = false
		area.DisableHorizontalScroll = true
		area.CaptureTab = true
		area.BarSize = -1
		area.ScrollX, area.ScrollY = 0, 0
	}
	if m.terminals == nil || m.terminals.active == nil {
		for _, e := range v.decorations {
			module.ContentSetIfChanged(world, e, scene.Visibility{})
		}
		return
	}
	item := m.terminals.active
	s := item.screen
	link, ok := module.ContentGet[htmlui.ControlText](world, v.text)
	if !ok {
		return
	}
	width, height, ok := scene.MeasureTextValue2D(text, "M")
	if !ok || width <= 0 || height <= 0 {
		return
	}
	cols, rows := max(2, int(link.Content.Width/width)), max(1, int(link.Content.Height/height))
	if cols != s.Cols || rows != s.Rows {
		s.Resize(cols, rows)
		item.session.Resize(s.Cols, s.Rows)
		markPencodeRootDirtyByModel(world, m)
		if m.app != nil {
			m.app.RequestRender()
		}
	}

	used := 0
	transform, _ := module.ContentGet[scene.UITransform](world, v.text)
	stack, _ := module.ContentGet[scene.UIStackIndex](world, v.text)
	for _, bg := range v.backgrounds {
		m.terminalQuad(world, &used, control, stack-1, transform.Translation.X.Value+float32(bg.x)*width, transform.Translation.Y.Value+float32(bg.y)*height, float32(bg.width)*width, height, bg.color)
	}
	for _, line := range v.underlines {
		m.terminalQuad(world, &used, control, stack, transform.Translation.X.Value+float32(line.x)*width, transform.Translation.Y.Value+float32(line.y+1)*height-1, float32(line.width)*width, 1, line.color)
	}

	if s.CursorVisible && v.caret >= 0 && m.terminalFocused(world) && item.scroll == 0 && !item.exited {
		if x, y, w, h, ok := scene.TextCaretRect2D(text, text.Value, v.caret); ok {
			color := text.Color
			if m.workbenchTheme != nil {
				color = pencodeSyntaxColor(m.syntaxTheme.Color("editorCursor.foreground"))
			}
			m.terminalQuad(world, &used, control, stack, transform.Translation.X.Value+x, transform.Translation.Y.Value+y+h-2, max(width, w), 2, color)
		}
	}
	for _, e := range v.decorations[used:] {
		module.ContentSetIfChanged(world, e, scene.Visibility{})
	}
	// Native IME positioning follows the shell cursor rather than the selection.
	if m.terminalFocused(world) {
		if state, ok := module.ContentResource[*input.State](world); ok {
			node, _ := world.HTMLNode(m.surface, "terminal-content")
			if bounds, ok := node.Bounds(); ok {
				cx, cy := s.Cursor()
				state.SetTextInputCaret(input.TextInputRect{X: float32(bounds.X) + float32(cx)*width, Y: float32(bounds.Y) + float32(cy)*height, Width: width, Height: height})
			}
		}
	}
}
func (m *workspaceModel) terminalQuad(world *module.Content, used *int, parent ecs.Entity, stack scene.UIStackIndex, x, y, width, height float32, color graphics.ColorRGBA) {
	v := &m.terminalView
	if *used == len(v.decorations) {
		bundle := ecs.NewBundle(9)
		ecs.Put(&bundle, scene.UITransformFromXY(0, 0))
		ecs.Put(&bundle, scene.UIGlobalTransform{})
		ecs.Put(&bundle, scene.Parent{Entity: parent})
		ecs.Put(&bundle, scene.AnchorTopLeft)
		ecs.Put(&bundle, scene.Visibility{})
		ecs.Put(&bundle, scene.Size2D{})
		ecs.Put(&bundle, scene.NewSprite2D())
		ecs.Put(&bundle, stack)
		v.decorations = append(v.decorations, world.Spawn(bundle))
	}
	e := v.decorations[*used]
	*used++
	module.ContentSetIfChanged(world, e, stack)
	module.ContentSetIfChanged(world, e, scene.Visibility{Visible: true})
	module.ContentSetIfChanged(world, e, scene.Size2D{Width: width, Height: height})
	transform := module.ContentMut[scene.UITransform](world, e)
	transform.Translation.X.Value, transform.Translation.Y.Value = x, y
	sprite := module.ContentMut[scene.Sprite2D](world, e)
	sprite.Shape = scene.RoundedRectShape2D(color, 0)
	sprite.Width, sprite.Height = width, height
}
