package workbench

import (
	"unicode/utf8"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

var pencodeEditorContextRefs = [...]string{
	"editor-context-cut", "editor-context-copy", "editor-context-paste", "editor-context-select-all",
	"lsp-context-3", "lsp-context-7", "lsp-context-8", "lsp-context-11", "lsp-context-9",
	"editor-context-group-1", "editor-context-group-2", "editor-context-group-3",
}

var pencodeEditorContextGroups = [...]struct {
	label string
	refs  []string
}{
	{},
	{"导航与符号", []string{"lsp-context-4", "lsp-context-5", "lsp-context-6", "lsp-context-12", "lsp-context-13", "lsp-context-14"}},
	{"代码辅助", []string{"lsp-context-0", "lsp-context-1", "lsp-context-2", "lsp-context-10", "lsp-context-18"}},
	{"更多语言工具", []string{"lsp-context-15", "lsp-context-16", "lsp-context-17", "lsp-context-19", "lsp-context-20", "lsp-context-21", "lsp-context-22"}},
}

var pencodeEditorActionGroups = [...]int{2, 2, 2, 0, 1, 1, 1, 0, 0, 0, 2, 0, 1, 1, 1, 3, 3, 3, 2, 3, 3, 3, 3}

func (model *workspaceModel) renderEditorContextMenu(view *htmlui.View) {
	ui := &model.languageUI
	if !ui.contextOpen {
		return
	}
	for _, c := range pencodeEditorClipboardCommands {
		view.Text(c.ref+"-shortcut", model.commandLabel(c.id))
	}
	view.RemoveAttr("lsp-context", "hidden")
	view.Attr("lsp-context", "style", languagePopupStyle(ui.contextX, ui.contextY))
	active := model.editorContextRefs()[ui.contextIndex]
	if ui.contextGroup != 0 {
		group := &pencodeEditorContextGroups[ui.contextGroup]
		view.RemoveAttr("lsp-context-submenu", "hidden")
		view.Attr("lsp-context-submenu", "style", languagePopupStyle(ui.contextSubX, ui.contextSubY))
		view.Attr("lsp-context-submenu", "aria-label", group.label)
		active = group.refs[ui.contextSubIndex]
	}
	for i, ref := range pencodeEditorContextRefs {
		if i >= 4 && i < 9 {
			continue
		}
		view.Event(ref, htmlui.EventClick)
		class := ""
		if ref == active || i >= 9 && i-8 == ui.contextGroup {
			class = "active"
		}
		view.Attr(ref, "class", class)
		if i >= 9 {
			view.Attr(ref, "aria-expanded", boolAttribute(i-8 == ui.contextGroup))
		} else if i < 4 {
			setContextMenuEnabled(view, ref, i >= 2 || ui.contextCaret != ui.contextAnchor)
		}
	}
	var doc *pencodeLanguageDocument
	if model.languages != nil {
		doc = model.languages.documents[model.activeFile()]
	}
	for i, action := range pencodeLanguageActions {
		ref := pencodeLanguageContextRefs[i]
		view.Event(ref, htmlui.EventClick)
		if group := pencodeEditorActionGroups[i]; group != 0 {
			if group == ui.contextGroup {
				view.RemoveAttr(ref, "hidden")
			} else {
				view.Attr(ref, "hidden", "")
			}
		}
		class := ""
		if ref == active {
			class = "active"
		}
		view.Attr(ref, "class", class)
		setContextMenuEnabled(view, ref, model.languageProvider(doc, action.method) != nil)
	}
}

func setContextMenuEnabled(view *htmlui.View, ref string, enabled bool) {
	if enabled {
		view.RemoveAttr(ref, "disabled")
	} else {
		view.Attr(ref, "disabled", "")
	}
}

func (model *workspaceModel) openEditorContextGroup(world *module.Content, group int) {
	ui := &model.languageUI
	ui.contextGroup, ui.contextSubIndex, ui.contextIndex = group, 0, group+8
	model.positionEditorSubmenu(world)
}

// Both menus use the editor body as their containing block. Keep the submenu
// touching its parent so moving the pointer between them never crosses a gap.
func (model *workspaceModel) positionEditorSubmenu(world *module.Content) bool {
	ui := &model.languageUI
	body, bodyOK := world.HTMLNode(model.surface, "editor-body")
	anchor, anchorOK := world.HTMLNode(model.surface, pencodeEditorContextRefs[ui.contextGroup+8])
	if !bodyOK || !anchorOK {
		return false
	}
	viewport, _ := body.Bounds()
	row, _ := anchor.Bounds()
	menu, ok := world.HTMLNode(model.surface, "lsp-context")
	if !ok {
		return false
	}
	bounds, _ := menu.Bounds()
	width := min(float32(240), viewport.Width-20)
	x := bounds.X - viewport.X + bounds.Width
	if x+width > viewport.Width-4 {
		x = bounds.X - viewport.X - width
	}
	x = max(4, x)
	height := float32(len(pencodeEditorContextGroups[ui.contextGroup].refs)*28 + 10)
	y := max(4, min(row.Y-viewport.Y-5, viewport.Height-height-4))
	if ui.contextSubX == x && ui.contextSubY == y {
		return false
	}
	ui.contextSubX, ui.contextSubY = x, y
	return true
}

func (model *workspaceModel) hoverEditorContext(world *module.Content, x, y float32) bool {
	ui := &model.languageUI
	refs := model.editorContextRefs()
	submenu := ui.contextGroup != 0 && model.pointerInsideRefs(world, x, y, []string{"lsp-context-submenu"})
	if submenu {
		refs = pencodeEditorContextGroups[ui.contextGroup].refs
	}
	for i, ref := range refs {
		if !model.pointerInsideRefs(world, x, y, []string{ref}) {
			continue
		}
		if submenu {
			changed := ui.contextSubIndex != i
			ui.contextSubIndex = i
			return changed
		}
		changed := ui.contextIndex != i
		ui.contextIndex = i
		if i >= 9 && i < len(pencodeEditorContextRefs) {
			if ui.contextGroup != i-8 {
				model.openEditorContextGroup(world, i-8)
				return true
			}
		} else if ui.contextGroup != 0 {
			ui.contextGroup = 0
			return true
		}
		return changed
	}
	return false
}

func (model *workspaceModel) handleEditorContextKeys(world *module.Content, state *input.State) bool {
	ui := &model.languageUI
	refs, index := model.editorContextRefs(), &ui.contextIndex
	if ui.contextGroup != 0 {
		refs, index = pencodeEditorContextGroups[ui.contextGroup].refs, &ui.contextSubIndex
	}
	switch {
	case model.uiKey(state, input.KeyEscape), model.uiKey(state, input.KeyLeft):
		if ui.contextGroup != 0 {
			ui.contextGroup = 0
		} else {
			model.dismissLanguageContext()
		}
	case model.uiKey(state, input.KeyDown):
		*index = (*index + 1) % len(refs)
	case model.uiKey(state, input.KeyUp):
		*index = (*index + len(refs) - 1) % len(refs)
	case model.uiKey(state, input.KeyRight):
		if ui.contextGroup == 0 && ui.contextIndex >= 9 && ui.contextIndex < len(pencodeEditorContextRefs) {
			model.openEditorContextGroup(world, ui.contextIndex-8)
		}
	case model.uiKey(state, input.KeyEnter), model.uiKey(state, input.KeySpace):
		model.handleEditorContextEvent(world, refs[*index])
	default:
		return false
	}
	return true
}

func (model *workspaceModel) handleEditorContextEvent(world *module.Content, ref string) bool {
	if model.handleActionRef(world, ref) {
		return true
	}
	ui := &model.languageUI
	for i := 9; i < len(pencodeEditorContextRefs); i++ {
		if ref == pencodeEditorContextRefs[i] {
			if ui.contextOpen {
				model.openEditorContextGroup(world, i-8)
				world.Focus(model.editor.control)
				if field := module.ContentMut[engineui.TextField](world, model.editor.control); field != nil {
					field.Caret, field.Anchor = ui.contextCaret, ui.contextAnchor
				}
				ui.contextFocus = true
			}
			return true
		}
	}
	for i, candidate := range pencodeLanguageContextRefs {
		if ref == candidate {
			if ui.contextOpen && model.languageProvider(model.languageDocument(), pencodeLanguageActions[i].method) != nil {
				model.runLanguageContext(world, i)
			}
			return true
		}
	}
	for _, candidate := range pencodeEditorContextRefs[:4] {
		if ref == candidate {
			model.runEditorContextEdit(world, ref)
			return true
		}
	}
	return false
}

func (model *workspaceModel) runEditorContextEdit(world *module.Content, ref string) {
	ui := &model.languageUI
	if world == nil || !ui.contextOpen || ui.contextFile != model.activeFile() || ui.contextContent != model.activeContent() {
		model.dismissLanguageContext()
		return
	}
	caret, anchor := ui.contextCaret, ui.contextAnchor
	model.dismissLanguageContext()
	world.Focus(model.editor.control)
	ui.contextFocus = true
	field := module.ContentMut[engineui.TextField](world, model.editor.control)
	if field == nil {
		return
	}
	field.Caret, field.Anchor, field.Selecting = caret, anchor, false
	value := model.activeContent()
	if ref == "editor-context-select-all" {
		field.Caret, field.Anchor = utf8.RuneCountInString(value), 0
		return
	}
	state, _ := module.ContentResource[*input.State](world)
	start, end := min(caret, anchor), max(caret, anchor)
	var edit input.TextEdit
	switch ref {
	case "editor-context-copy", "editor-context-cut":
		if start == end {
			return
		}
		begin, finish := pencodeSelectionBytes(value, start, end)
		if !state.SetClipboardText(value[begin:finish]) || ref == "editor-context-copy" {
			return
		}
		edit.Command = input.TextCommandBackspace
	case "editor-context-paste":
		pasted, ok := state.ClipboardText()
		if !ok || pasted == "" {
			return
		}
		edit.Text = pasted
	}
	value, caret, _ = state.ApplyTextEditsInRange(value, start, end, field.MaxRunes, true, []input.TextEdit{edit})
	model.updateActiveContent(value)
	model.setEditorValueAndCaret(world, value, caret)
	model.revealEditorCaret(world, caret)
}
