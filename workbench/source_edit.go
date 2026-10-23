package workbench

import (
	"unicode/utf8"

	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

func (tab *sourceTab) revertBlock(index int) bool {
	if !tab.editable() || tab.DiffContent != tab.Result || index < 0 || index >= len(tab.Blocks) {
		return false
	}
	block := tab.Blocks[index]
	tab.editResult(tab.Result[:block.RightStart] + tab.Local[block.LeftStart:block.LeftEnd] + tab.Result[block.RightEnd:])
	return true
}

func (m *workspaceModel) bindSourceDiffControls(world *module.Content) {
	s := m.sourceState()
	tab := s.activeTab()
	if tab != nil && !tab.Binary && tab.Kind == "merge" {
		if control, ok := world.HTMLControl(m.surface, "sc-merge-result"); ok {
			if field := module.ContentMut[engineui.TextField](world, control); field != nil {
				field.Buffer = tab.textBuffer(tab.Result)
			}
		}
	}
	if tab == nil || tab.Binary || tab.Kind == "merge" {
		s.diffPaneTab = nil
		s.inlinePaint = [2]sourceInlinePaint{}
		return
	}
	for side, id := range [...]string{"sc-code-left-input", "sc-code-right-input"} {
		control, ok := world.HTMLControl(m.surface, id)
		if !ok {
			continue
		}
		if binding := module.ContentMut[htmlui.Control](world, control); binding != nil {
			binding.Wrap = false
			binding.ReadOnly = side == 0 || !tab.editable() || s.busy()
		}
		if field := module.ContentMut[engineui.Textarea](world, control); field != nil {
			field.Wrap, field.CaptureTab, field.SelectTokens = false, true, true
			field.BarSize = -1
		}
		font := m.codeFont()
		if binding := module.ContentMut[htmlui.ControlTextFont](world, control); binding != nil {
			binding.Font = font
		} else {
			_ = module.ContentInsert(world, control, htmlui.ControlTextFont{Font: font})
		}
		value := tab.sourceText(side)
		field := module.ContentMut[engineui.TextField](world, control)
		if field == nil {
			continue
		}
		if side == 1 && tab.editable() {
			field.Buffer = tab.textBuffer(value)
		} else {
			field.Buffer = nil
		}
		if field.Value != value {
			world.SetControlValue(control, value)
		}
		if s.diffPaneTab != tab {
			field.Caret, field.Anchor, field.Composition, field.Selecting = 0, 0, "", false
		}
		if s.diffEditors[side].caretContent != value {
			count := utf8.RuneCountInString(value)
			field.Caret, field.Anchor = min(field.Caret, count), min(field.Anchor, count)
		}
		entity, ok := module.ContentFindPart[engineui.ValuePart, scene.Text2D](world, control)
		if !ok {
			continue
		}
		text := module.ContentMut[scene.Text2D](world, entity)
		text.Font, text.LineHeight, text.LineLayout = font, 20, &tab.LineLayouts[side]
		if width, ok := world.ControlTextTabWidth(control, text); ok {
			text.TabWidth = width
		}
		h := &tab.Highlight[side]
		var spans []scene.TextSpan2D
		if h.ready && h.source == value && h.theme == m.syntaxTheme && h.definitions == m.syntaxDefinitions {
			spans = h.spans
		}
		paint := pencodeOverlayPaintKey(m, value, spans, 0, 0)
		paint.paint = h.revision
		s.diffEditors[side].syncPaint(text, *field, spans, paint)
		s.inlinePaint[side].sync(world, m, tab, side, text)
		// The outer viewport owns scrolling, including automatic caret reveal.
		if field.Focused && (s.diffEditors[side].caretContent != value || s.diffEditors[side].caretIndex != field.Caret) {
			if node, ok := world.HTMLNode(m.surface, [...]string{"sc-code-left", "sc-code-right"}[side]); ok {
				if area, ok := node.ScrollContainer(); ok {
					if scroll := module.ContentMut[scene.UIScroll](world, area); scroll != nil {
						x, y, _, height, ok := scene.TextCaretRect2D(text, value, field.Caret)
						if ok {
							left, top := -scroll.Position[0], -scroll.Position[1]
							if x < left {
								left = x
							} else if x+1 > left+scroll.ViewportSize[0] {
								left = x + 1 - scroll.ViewportSize[0]
							}
							if y < top {
								top = y
							} else if y+height > top+scroll.ViewportSize[1] {
								top = y + height - scroll.ViewportSize[1]
							}
							scroll.Position[0], scroll.Position[1] = -left, -top
						}
					}
				}
			}
		}
		s.diffEditors[side].caretContent, s.diffEditors[side].caretIndex = value, field.Caret
	}
	s.diffPaneTab = tab
}
