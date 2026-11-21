package workbench

import (
	"unicode/utf8"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	htmlui "github.com/onix-engine/onix/ui/html"
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
			if field := world.Mut[engineui.TextField](control); field != nil {
				field.Buffer = tab.textBuffer(tab.Result)
				field.Source = snapshotText(tab.Result)
			}
		}
	}
	if tab == nil || tab.Binary || tab.SubmoduleSummary != "" || tab.Kind == "merge" {
		s.diffPaneTab = nil
		s.inlinePaint = [2]sourceInlinePaint{}
		s.diffEditors = [2]pencodeEditor{}
		s.gutterTab = nil
		return
	}
	for side, id := range diffInputs {
		control, ok := world.HTMLControl(m.surface, id)
		if !ok {
			continue
		}
		if !world.Has[engineui.TextFieldKeyHandler](control) {
			_ = world.Insert(control, engineui.TextFieldKeyHandler{Handle: m.diffLanguageKeyHandled})
		}
		if binding := world.Mut[htmlui.Control](control); binding != nil {
			binding.Wrap = false
			binding.ReadOnly = side == 0 || !tab.editable() || s.busy()
		}
		if field := world.Mut[engineui.Textarea](control); field != nil {
			field.Wrap, field.CaptureTab, field.SelectTokens = false, true, true
			field.BarSize = -1
		}
		font := m.codeFont()
		if binding := world.Mut[htmlui.ControlTextFont](control); binding != nil {
			binding.Font = font
		} else {
			_ = world.Insert(control, htmlui.ControlTextFont{Font: font})
		}
		value := tab.sourceText(side)
		field := world.Mut[engineui.TextField](control)
		if field == nil {
			continue
		}
		if side == 1 && tab.editable() {
			field.Buffer = tab.textBuffer(value)
			field.Source = snapshotText(value)
		} else {
			field.Buffer = nil
		}
		if field.Value != value {
			world.SetControlValue(control, value)
		}
		if s.diffPaneTab != tab {
			field.Caret, field.Anchor, field.Composition, field.Selecting = 0, 0, "", false
		}
		if s.diffEditors[side].caretContent != snapshotText(value) {
			count := utf8.RuneCountInString(value)
			field.Caret, field.Anchor = min(field.Caret, count), min(field.Anchor, count)
		}
		entity, ok := world.FindPart[engineui.ValuePart, scene.Text2D](control)
		if !ok {
			continue
		}
		s.diffEditors[side].control, s.diffEditors[side].text = control, entity
		text := world.Mut[scene.Text2D](entity)
		text.Font, text.LineHeight, text.LineLayout = font, 20, &tab.LineLayouts[side]
		if width, ok := world.ControlTextTabWidth(control, text); ok {
			text.TabWidth = width
		}
		h := &tab.Highlight[side]
		var spans []scene.TextSpan2D
		if h.ready && h.source == value && h.theme == m.syntaxTheme && h.definitions == m.syntaxDefinitions {
			spans = h.spans
		}
		if m.languages != nil {
			spans = m.documentLanguageHighlight(m.languages.documents[tab.languageFiles[side]], spans, h.revision)
		}
		spans = s.diffEditors[side].languageLinkSpans(m, *field, spans, tab.languageFiles[side], h.revision)
		paint := pencodeOverlayPaintKey(m, value, spans, 0, 0)
		paint.paint = h.revision
		s.diffEditors[side].syncPaint(text, *field, spans, paint)
		s.inlinePaint[side].sync(world, m, tab, side, text)
		// The outer viewport owns scrolling, including automatic caret reveal.
		if field.Focused && (s.diffEditors[side].caretContent != snapshotText(value) || s.diffEditors[side].caretIndex != field.Caret) {
			if node, ok := world.HTMLNode(m.surface, diffViewports[side]); ok {
				if area, ok := node.ScrollContainer(); ok {
					if scroll := world.Mut[scene.UIScroll](area); scroll != nil {
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
		s.diffEditors[side].caretContent, s.diffEditors[side].caretIndex = snapshotText(value), field.Caret
	}
	s.diffPaneTab = tab
}

func (m *workspaceModel) diffLanguageKeyHandled(*input.State) bool { return m.languageUI.keysHandled }
