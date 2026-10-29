package workbench

import (
	"unicode/utf8"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

type pencodeInputContext struct {
	target               ecs.Entity
	value                string
	caret, anchor, index int
	x, y                 float32
	open, focus, restore bool
	activate             bool
}

var pencodeInputContextRefs = [...]string{
	"input-context-cut", "input-context-copy", "input-context-paste", "input-context-delete", "input-context-select-all",
}

type pencodeInputContextProps struct {
	Model    *workspaceModel
	Open     bool
	Activate bool
	Index    int
	X, Y     float32
	Enabled  [len(pencodeInputContextRefs)]bool
}

func newInputContextDefinition() (*htmlui.Definition[pencodeInputContextProps, struct{}], error) {
	return htmlui.Define(htmlui.NewTemplate(`
<div id="input-context" data-ref="input-context" class="pencode-editor-context pencode-input-context" role="menu" aria-label="文本操作" hidden>
  <button id="input-context-cut" data-ref="input-context-cut" type="button" role="menuitem" tabindex="-1">剪切</button>
  <button id="input-context-copy" data-ref="input-context-copy" type="button" role="menuitem" tabindex="-1">复制</button>
  <button id="input-context-paste" data-ref="input-context-paste" type="button" role="menuitem" tabindex="-1">粘贴</button>
  <button id="input-context-delete" data-ref="input-context-delete" type="button" role="menuitem" tabindex="-1">删除</button>
  <div class="pencode-context-separator" role="separator"></div>
  <button id="input-context-select-all" data-ref="input-context-select-all" type="button" role="menuitem" tabindex="-1">全选</button>
</div>`), "", htmlui.Behavior[pencodeInputContextProps, struct{}]{
		Render: func(c htmlui.RenderContext[pencodeInputContextProps, struct{}], view *htmlui.View) {
			p := c.Props
			if !p.Open {
				view.Attr("input-context", "hidden", "")
				return
			}
			view.RemoveAttr("input-context", "hidden")
			view.Attr("input-context", "style", languagePopupStyle(p.X, p.Y))
			for i, ref := range pencodeInputContextRefs {
				view.Event(ref, htmlui.EventClick)
				setContextMenuEnabled(view, ref, p.Enabled[i])
				class := ""
				if i == p.Index {
					class = "active"
				}
				view.Attr(ref, "class", class)
			}
		},
		HandleEvent: func(c *htmlui.ComponentEventContext[pencodeInputContextProps, struct{}]) {
			if c.Event.Kind == htmlui.EventClick {
				c.Props.Model.runInputContextEdit(c.World, c.Ref)
			}
		},
		Update: func(c *htmlui.ComponentContext[pencodeInputContextProps, struct{}]) {
			if c.Props.Activate {
				m := c.Props.Model
				m.inputContext.activate = false
				m.runInputContextEdit(c.World, pencodeInputContextRefs[m.inputContext.index])
			}
		},
	})
}

func inputContextEnabled(field engineui.TextField, readOnly bool) [len(pencodeInputContextRefs)]bool {
	selected := field.Caret != field.Anchor
	return [len(pencodeInputContextRefs)]bool{
		selected && !readOnly && !field.Password, selected && !field.Password,
		!readOnly, selected && !readOnly, field.Value != "",
	}
}

func (m *workspaceModel) renderInputContext(view *htmlui.View, definition *htmlui.Definition[pencodeInputContextProps, struct{}]) {
	c := &m.inputContext
	p := pencodeInputContextProps{Model: m, Open: c.open, Activate: c.activate, Index: c.index, X: c.x, Y: c.y}
	if c.open {
		field, _ := m.fontResources.Get[engineui.TextField](c.target)
		control, _ := m.fontResources.Get[htmlui.Control](c.target)
		field.Caret, field.Anchor = c.caret, c.anchor
		p.Enabled = inputContextEnabled(field, control.ReadOnly)
	}
	view.AppendComponentChild("input-context-slot", 1, definition, p, struct{}{})
}

// Use the native hover receiver so clipping, scroll offsets, and overlays are
// resolved by the same hit test that focuses and edits the control.
func (m *workspaceModel) updateInputContext(world *module.Content, state *input.State) (handled, changed bool) {
	c := &m.inputContext
	if c.restore {
		return true, false
	}
	pointer := state.Pointer()
	zoom := max(float32(m.settings.Zoom)/100, .01)
	x, y := pointer.X/zoom, pointer.Y/zoom
	if c.open {
		field, ok := world.Get[engineui.TextField](c.target)
		control, _ := world.Get[htmlui.Control](c.target)
		if !ok || control.Disabled || field.Value != c.value {
			c.open = false
			return false, true
		}
		inside := m.pointerInsideRefs(world, x, y, []string{"input-context"})
		if inside {
			if pointer.DeltaX != 0 || pointer.DeltaY != 0 {
				enabled := inputContextEnabled(field, control.ReadOnly)
				for i, ref := range pencodeInputContextRefs {
					if enabled[i] && i != c.index && m.pointerInsideRefs(world, x, y, []string{ref}) {
						c.index, c.focus = i, true
						return true, true
					}
				}
			}
		} else if state.PointerPressed() || state.MousePressed(input.MouseRight) || state.MousePressed(input.MouseMiddle) || pointer.WheelX != 0 || pointer.WheelY != 0 {
			c.open = false
			changed = true
		}
		if c.open {
			if m.uiKey(state, input.KeyEnter) || m.uiKey(state, input.KeySpace) {
				c.activate = true
				return true, true
			}
			if m.uiKey(state, input.KeyEscape) || m.uiKey(state, input.KeyTab) {
				c.open, c.restore = false, true
				return true, true
			}
			step := 0
			if m.uiKey(state, input.KeyDown) {
				step = 1
			} else if m.uiKey(state, input.KeyUp) {
				step = -1
			}
			if step != 0 {
				enabled := inputContextEnabled(field, control.ReadOnly)
				for range len(enabled) {
					c.index = (c.index + step + len(enabled)) % len(enabled)
					if enabled[c.index] {
						break
					}
				}
				c.focus = true
				return true, true
			}
			return true, false
		}
	}
	if !state.MousePressed(input.MouseRight) || state.Composing() {
		return false, changed
	}
	for entity, access := range world.Query2[engineui.TextField, scene.InputState2D]() {
		if !access.B.Get().Hovered || entity == m.editor.control || entity == m.terminalView.control {
			continue
		}
		control, _ := world.Get[htmlui.Control](entity)
		target, _ := world.Get[scene.InputTarget2D](entity)
		if control.Disabled || target.Disabled {
			continue
		}
		root, ok := world.HTMLNode(m.surface, "pencode-root")
		if !ok {
			break
		}
		bounds, _ := root.Bounds()
		field := access.A.Get()
		*c = pencodeInputContext{target: entity, value: field.Value, caret: field.Caret, anchor: field.Anchor, open: true, focus: true}
		c.x, c.y = clampLanguagePopup(x-bounds.X, y-bounds.Y, bounds, 240, 160)
		enabled := inputContextEnabled(field, control.ReadOnly)
		for c.index < len(enabled)-1 && !enabled[c.index] {
			c.index++
		}
		m.fileContext.open, m.terminalView.context.open = false, false
		m.dismissLanguageContext()
		m.cancelLanguageHover()
		return true, true
	}
	return false, changed
}

// Run edits through the normal native input message pipeline, including the
// editable host's cancellable beforeinput event and extension input bindings.
func (m *workspaceModel) runInputContextEdit(world *ecs.World, ref string) {
	c := &m.inputContext
	field := world.GetMut[engineui.TextField](c.target)
	control, _ := world.Get[htmlui.Control](c.target)
	if !c.open || field == nil || control.Disabled || field.Value != c.value || !scene.IsInteractive(world, c.target) {
		return
	}
	selection := *field
	selection.Caret, selection.Anchor = c.caret, c.anchor
	enabled := inputContextEnabled(selection, control.ReadOnly)
	index := -1
	for i, candidate := range pencodeInputContextRefs {
		if candidate == ref {
			index = i
			break
		}
	}
	if index < 0 || !enabled[index] {
		return
	}
	c.open, c.restore = false, true
	defer markPencodeRootDirtyByModel(m.fontResources, m)
	field.Caret, field.Anchor, field.Selecting = c.caret, c.anchor, false
	if index == 4 {
		c.caret, c.anchor = utf8.RuneCountInString(field.Value), 0
		field.Caret, field.Anchor = c.caret, c.anchor
		return
	}
	state, _ := world.GetResource[*input.State]()
	start, end := min(c.caret, c.anchor), max(c.caret, c.anchor)
	var edit input.TextEdit
	inputType := "deleteContentBackward"
	switch index {
	case 0, 1:
		begin, finish := pencodeSelectionBytes(field.Value, start, end)
		if !state.SetClipboardText(field.Value[begin:finish]) || index == 1 {
			return
		}
		inputType = "deleteByCut"
		edit.Command = input.TextCommandBackspace
	case 2:
		value, ok := state.ClipboardText()
		if !ok || value == "" {
			return
		}
		inputType, edit.Text = "insertFromPaste", value
	case 3:
		edit.Command = input.TextCommandBackspace
	}
	editable, isEditable := world.Get[htmlui.Editable](c.target)
	if isEditable {
		event := htmlui.DispatchAction(world, htmlui.Event{Entity: editable.Owner, Target: c.target, ID: editable.ID,
			Kind: htmlui.EventBeforeInput, Value: field.Value, InputType: inputType, Data: edit.Text,
			Anchor: c.anchor, Focus: c.caret, Bubbles: true, Cancelable: true})
		world.WriteMessage(event)
		if event.DefaultPrevented {
			return
		}
	}
	multiline := world.Has[engineui.Textarea](c.target) || isEditable
	apply := state.ApplyTextEditsInRange
	if field.Buffer != nil {
		apply = field.Buffer.ApplyTextEditsInRange
	}
	value, caret, anchor := apply(field.Value, start, end, field.MaxRunes, multiline, []input.TextEdit{edit})
	if value == field.Value {
		return
	}
	field.Value, field.Caret, field.Anchor, field.Composition = value, caret, anchor, ""
	c.caret, c.anchor = caret, anchor
	if number := world.GetMut[engineui.NumberInput](c.target); number != nil {
		number.Changed = true
	}
	if isEditable {
		htmlui.SetEditableValue(world, c.target, value)
		htmlui.SetEditableSelection(world, c.target, anchor, caret)
		world.WriteMessage(htmlui.Event{Entity: editable.Owner, Target: c.target, ID: editable.ID,
			Kind: htmlui.EventInput, Value: value, InputType: inputType, Data: edit.Text,
			Anchor: anchor, Focus: caret, Bubbles: true})
	} else {
		world.WriteMessage(engineui.InputEvent[string]{Entity: c.target, Value: value})
	}
}

func (m *workspaceModel) syncInputContext(world *module.Content) {
	c := &m.inputContext
	if c.restore {
		c.restore = false
		if world.Focus(c.target) {
			if field := world.Mut[engineui.TextField](c.target); field != nil {
				field.Caret, field.Anchor, field.Selecting = c.caret, c.anchor, false
			}
		}
	}
	if !c.open {
		c.value = ""
		return
	}
	if c.focus {
		if control, ok := world.HTMLControl(m.surface, pencodeInputContextRefs[c.index]); ok && world.Focus(control) {
			c.focus = false
		}
	}
	menu, menuOK := world.HTMLNode(m.surface, "input-context")
	root, rootOK := world.HTMLNode(m.surface, "pencode-root")
	if menuOK && rootOK {
		bounds, _ := menu.Bounds()
		viewport, _ := root.Bounds()
		x, y := clampLanguagePopup(c.x, c.y, viewport, bounds.Width, bounds.Height)
		if x != c.x || y != c.y {
			c.x, c.y = x, y
			markPencodeRootDirtyByModel(world, m)
		}
	}
}
