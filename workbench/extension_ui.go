package workbench

import (
	"fmt"
	"strings"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/html/dom"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

const maxExtensionSurfaces = 32

type pencodeExtensionInvocation struct {
	context peb.ActionContext
	anchor  string
	x, y    float32
}
type pencodeExtensionUIState struct {
	surfaces       []*pencodeExtensionSurface
	invocations    map[string]pencodeExtensionInvocation
	overlay, panel *pencodeExtensionSurface
	wrapper        *htmlui.Definition[pencodeExtensionSurfaceProps, struct{}]
}
type pencodeExtensionSurfaceProps struct {
	Surface  *pencodeExtensionSurface
	Revision uint64
	Visible  bool
}
type pencodeExtensionFocus struct{ ref, id string }
type pencodeExtensionSurface struct {
	model                          *workspaceModel
	session                        *extensions.ActionSession
	owner                          string
	ui                             peb.UI
	invocation                     pencodeExtensionInvocation
	definition                     *htmlui.Definition[pencodeExtensionSurfaceProps, struct{}]
	bindings                       []peb.EditorBinding
	focus                          []pencodeExtensionFocus
	focused                        int
	revision                       uint64
	prefix, rootID, closeID, style string
	x, y                           float32
	needsFocus, visible            bool
}

func (model *workspaceModel) captureExtensionAction(world *module.Content, c peb.ActionContext) {
	rest, ok := strings.CutPrefix(c.ID, "extensions/")
	if !ok {
		return
	}
	owner, local, ok := strings.Cut(rest, "/")
	if !ok {
		return
	}
	c.ID = local
	invocation := pencodeExtensionInvocation{context: c, anchor: "action-extensions/" + owner + "/" + local}
	if world != nil {
		if node, ok := world.HTMLNode(model.surface, invocation.anchor); ok {
			if bounds, ok := node.Bounds(); ok {
				invocation.x, invocation.y = bounds.X, bounds.Y+bounds.Height
			}
		}
	}
	if model.actions.ui.invocations == nil {
		model.actions.ui.invocations = make(map[string]pencodeExtensionInvocation)
	}
	model.actions.ui.invocations[owner] = invocation
}

func (s *pencodeExtensionSurface) send(event peb.EditorEvent, reason string) {
	if !s.session.SubmitUI(peb.UIEvent{ID: s.ui.ID, Reason: reason, Context: s.invocation.context, Event: event}) {
		s.model.extensionFeedback = "扩展 UI 事件队列已满或进程已停止"
		s.model.logOutput("Extension: "+s.owner, s.model.extensionFeedback)
	}
}

func (model *workspaceModel) applyExtensionUI(owner string, session *extensions.ActionSession, ui peb.UI) error {
	state := &model.actions.ui
	var surface *pencodeExtensionSurface
	for _, candidate := range state.surfaces {
		if candidate.owner == owner && candidate.ui.ID == ui.ID {
			surface = candidate
			break
		}
	}
	if ui.Close {
		if surface != nil {
			model.closeExtensionSurface(surface, "extension", true)
		}
		return nil
	}
	fresh := surface == nil
	if fresh {
		if len(state.surfaces) >= maxExtensionSurfaces && (ui.Kind == peb.Panel || state.overlay == nil) {
			return fmt.Errorf("扩展 UI 数量超过 %d", maxExtensionSurfaces)
		}
		prefix := "pencode-extension-ui/" + owner + "/" + ui.ID + "/"
		surface = &pencodeExtensionSurface{model: model, session: session, owner: owner, ui: peb.UI{Kind: ui.Kind}, prefix: prefix, rootID: prefix + "surface", closeID: prefix + "close", focused: -1}
	} else if surface.ui.Kind != ui.Kind {
		return fmt.Errorf("更新 UI 不能改变 Kind")
	}
	if ui.View != nil {
		if err := surface.setView(ui.View); err != nil {
			return err
		}
	} else if surface.definition == nil {
		return fmt.Errorf("首次打开 UI 必须提供 View")
	}
	if state.wrapper == nil {
		definition, err := newExtensionSurfaceDefinition()
		if err != nil {
			return err
		}
		state.wrapper = definition
	}
	surface.ui = ui
	surface.ui.View = nil // The compiled template owns the retained view.
	surface.revision++
	if fresh {
		surface.invocation = state.invocations[owner]
		if ui.Context.ID != "" {
			surface.invocation.context = ui.Context
			surface.invocation.anchor = "action-extensions/" + owner + "/" + ui.Context.ID
		}
		surface.x, surface.y = surface.invocation.x, surface.invocation.y
		state.surfaces = append(state.surfaces, surface)
	}
	if ui.Anchor != "" {
		surface.invocation.anchor = "action-extensions/" + owner + "/" + ui.Anchor
	} else if surface.invocation.context.ID != "" {
		surface.invocation.anchor = "action-extensions/" + owner + "/" + surface.invocation.context.ID
	}
	surface.updateStyle()
	if ui.Kind == peb.Panel {
		state.panel = surface
		model.bottom.active = pencodePanelExtension
	} else {
		if state.overlay != nil && state.overlay != surface {
			model.closeExtensionSurface(state.overlay, "replace", false)
		}
		state.overlay = surface
		model.fileContext.open = false
		model.quickOpen, model.settingsOpen = false, false
		if model.market != nil {
			model.market.open = false
		}
		model.dismissLanguageContext()
	}
	surface.needsFocus = fresh || ui.View != nil
	if fresh {
		surface.visible = true
		surface.send(peb.EditorEvent{Kind: "open"}, "")
	}
	return nil
}

func (s *pencodeExtensionSurface) setView(view *peb.EditorView) error {
	focus := make([]pencodeExtensionFocus, 0, len(view.Events))
	if err := validateExtensionView(view, func(node *dom.Node, hidden bool) {
		if ref, _ := node.Attr("data-ref"); ref != "" {
			_, disabled := node.Attr("disabled")
			tabindex, hasTabindex := node.Attr("tabindex")
			typ, _ := node.Attr("type")
			_, href := node.Attr("href")
			editable, hasEditable := node.Attr("contenteditable")
			if !disabled && !hidden && typ != "hidden" && (s.ui.Kind == peb.ContextMenu || !hasTabindex || tabindex != "-1") && (node.Data == "button" || node.Data == "input" || node.Data == "select" || node.Data == "textarea" || node.Data == "a" && href || hasEditable && editable != "false" || hasTabindex && tabindex != "-1") {
				id, _ := node.Attr("id")
				if id == "" {
					id = s.prefix + "control-" + ref
				}
				focus = append(focus, pencodeExtensionFocus{ref: ref, id: id})
			}
		}
	}); err != nil {
		return err
	}
	definition, err := htmlui.DefineScoped(htmlui.NewTemplate(view.HTML), view.CSS, htmlui.Behavior[pencodeExtensionSurfaceProps, struct{}]{
		Render: func(c htmlui.RenderContext[pencodeExtensionSurfaceProps, struct{}], target *htmlui.View) {
			surface := c.Props.Surface
			for _, f := range surface.focus {
				target.Attr(f.ref, "id", f.id)
				target.Event(f.ref, htmlui.EventFocus)
			}
			for _, binding := range surface.bindings {
				target.Event(binding.Ref, htmlui.EventKind(binding.Kind))
			}
		},
		HandleEvent: func(c *htmlui.ComponentEventContext[pencodeExtensionSurfaceProps, struct{}]) {
			surface := c.Props.Surface
			if !surface.live() || surface.model.closing.action != pencodeCloseNone {
				return
			}
			event := c.Event
			if event.Kind == htmlui.EventFocus {
				for i, f := range surface.focus {
					if f.ref == c.Ref {
						surface.focused = i
						break
					}
				}
			}
			for _, binding := range surface.bindings {
				if binding.Ref == c.Ref && binding.Kind == string(event.Kind) {
					surface.send(peb.EditorEvent{Kind: string(event.Kind), Ref: c.Ref, Value: event.Value, InputType: event.InputType, Data: event.Data, Checked: event.Checked, Selected: uint32(max(0, event.Selected)), Anchor: uint32(max(0, event.Anchor)), Focus: uint32(max(0, event.Focus))}, "")
					break
				}
			}
		},
	})
	if err != nil {
		return err
	}
	s.definition, s.bindings, s.focus = definition, view.Events, focus
	s.focused = -1
	return nil
}

func (s *pencodeExtensionSurface) live() bool {
	state := &s.model.actions.ui
	return state.overlay == s || state.panel == s && s.model.bottom.active == pencodePanelExtension
}

func newExtensionSurfaceDefinition() (*htmlui.Definition[pencodeExtensionSurfaceProps, struct{}], error) {
	return htmlui.DefineScoped(htmlui.NewTemplate(`<section data-ref="surface"><header data-ref="header" class="pencode-extension-header"><strong data-ref="title"></strong><button data-ref="close" type="button" aria-label="关闭">×</button></header><div data-ref="content" class="pencode-extension-content"></div></section>`), "", htmlui.Behavior[pencodeExtensionSurfaceProps, struct{}]{
		Render: func(c htmlui.RenderContext[pencodeExtensionSurfaceProps, struct{}], view *htmlui.View) {
			s := c.Props.Surface
			view.Attr("surface", "id", s.rootID)
			view.Attr("surface", "aria-label", s.ui.Title)
			view.Attr("close", "id", s.closeID)
			view.Text("title", s.ui.Title)
			view.Event("close", htmlui.EventClick)
			view.Event("close", htmlui.EventFocus)
			role, class := "region", "pencode-extension-panel"
			switch s.ui.Kind {
			case peb.Dialog:
				role, class = "dialog", "pencode-extension-dialog"
				view.Attr("surface", "aria-modal", "true")
			case peb.ContextMenu:
				role, class = "menu", "pencode-extension-menu"
				view.Attr("header", "hidden", "")
			case peb.Popup:
				role, class = "dialog", "pencode-extension-popup"
			}
			if s.ui.Kind != peb.Panel {
				view.Attr("surface", "style", s.style)
			}
			view.Attr("surface", "role", role)
			view.Attr("surface", "class", class)
			if !c.Props.Visible {
				view.Attr("surface", "hidden", "")
			}
			htmlui.AppendComponentChild(view, "content", 1, s.definition, *c.Props, struct{}{})
		},
		HandleEvent: func(c *htmlui.ComponentEventContext[pencodeExtensionSurfaceProps, struct{}]) {
			s := c.Props.Surface
			if !s.live() || s.model.closing.action != pencodeCloseNone || c.Ref != "close" {
				return
			}
			if c.Event.Kind == htmlui.EventFocus {
				s.focused = len(s.focus)
				return
			}
			s.model.closeExtensionSurface(s, "button", true)
			markPencodeRootDirtyByModel(s.model.fontResources, s.model)
		},
	})
}

func (model *workspaceModel) renderExtensionUI(view *htmlui.View) {
	state := &model.actions.ui
	dialog := state.overlay != nil && state.overlay.ui.Kind == peb.Dialog
	setPanelVisible(view, "extension-modal-shade", dialog)
	for _, s := range state.surfaces {
		slot := "extension-overlays"
		if s.ui.Kind == peb.Panel {
			slot = "extension-pane"
		}
		htmlui.AppendComponentChild(view, slot, componentKey(s.rootID), state.wrapper, pencodeExtensionSurfaceProps{Surface: s, Revision: s.revision, Visible: s.live()}, struct{}{})
	}
	setPanelVisible(view, "extension-pane", model.bottom.active == pencodePanelExtension)
}

func (model *workspaceModel) closeExtensionSurface(s *pencodeExtensionSurface, reason string, restore bool) {
	state := &model.actions.ui
	wasLive := s.live()
	if restore && s.ui.Kind == peb.Popup && model.fontResources != nil {
		restore = s.ownsFocus(model.fontResources)
	}
	for i, candidate := range state.surfaces {
		if candidate == s {
			copy(state.surfaces[i:], state.surfaces[i+1:])
			state.surfaces[len(state.surfaces)-1] = nil
			state.surfaces = state.surfaces[:len(state.surfaces)-1]
			break
		}
	}
	if state.overlay == s {
		state.overlay = nil
	}
	if state.panel == s {
		state.panel = nil
		if model.bottom.active == pencodePanelExtension {
			model.bottom.active = pencodePanelClosed
		}
	}
	s.send(peb.EditorEvent{Kind: "close"}, reason)
	if restore && wasLive && state.overlay == nil && model.fontResources != nil {
		if control, ok := model.fontResources.HTMLControl(model.surface, s.invocation.anchor); ok && model.fontResources.Focus(control) {
			return
		}
		model.fontResources.Focus(model.editor.control)
	}
}

func (model *workspaceModel) clearExtensionUI(owner string) {
	state := &model.actions.ui
	retained := state.surfaces[:0]
	for _, s := range state.surfaces {
		if owner != "" && s.owner != owner {
			retained = append(retained, s)
			continue
		}
		// No lifecycle RPC after unload/disconnect.
		if state.overlay == s {
			state.overlay = nil
		}
		if state.panel == s {
			state.panel = nil
			if model.bottom.active == pencodePanelExtension {
				model.bottom.active = pencodePanelClosed
			}
		}
	}
	clear(state.surfaces[len(retained):])
	state.surfaces = retained
	if owner == "" {
		clear(state.invocations)
	} else {
		delete(state.invocations, owner)
	}
}

func (s *pencodeExtensionSurface) updateStyle() {
	s.style = fmt.Sprintf("left:%.1fpx;top:%.1fpx", s.x, s.y)
	if s.ui.Placement.Width > 0 {
		s.style += fmt.Sprintf(";width:%.1fpx", s.ui.Placement.Width)
	}
	if s.ui.Placement.Height > 0 {
		s.style += fmt.Sprintf(";height:%.1fpx", s.ui.Placement.Height)
	}
}

// Called after layout: position overlays inside the viewport and focus new surfaces.
func (model *workspaceModel) syncExtensionUI(world *module.Content) {
	state := &model.actions.ui
	for _, s := range state.surfaces {
		visible := s.live()
		if visible != s.visible {
			s.visible = visible
			value := "hidden"
			if visible {
				value = "visible"
			}
			s.send(peb.EditorEvent{Kind: "visibilitychange", Value: value}, "")
		}
		if !visible {
			continue
		}
		if model.closing.action != pencodeCloseNone {
			s.needsFocus = true
			continue
		}
		if s.ui.Kind != peb.Panel {
			menu, menuOK := world.HTMLNode(model.surface, s.rootID)
			root, rootOK := world.HTMLNode(model.surface, "pencode-extension-overlays")
			if menuOK && rootOK {
				bounds, _ := menu.Bounds()
				viewport, _ := root.Bounds()
				placement := s.ui.Placement
				x, y := float32(0), float32(0)
				switch {
				case placement.Mode == peb.PositionAbsolute:
				case placement.Mode == peb.PositionAnchor || s.ui.Kind == peb.ContextMenu && placement.Mode == peb.PositionDefault:
					x, y = s.invocation.x-viewport.X, s.invocation.y-viewport.Y
					if anchor, ok := world.HTMLNode(model.surface, s.invocation.anchor); ok {
						if rect, ok := anchor.Bounds(); ok {
							x, y = rect.X-viewport.X, rect.Y+rect.Height-viewport.Y
						}
					}
				default:
					x, y = (viewport.Width-bounds.Width)/2, (viewport.Height-bounds.Height)/2
				}
				x, y = x+placement.X, y+placement.Y
				x, y = max(4, min(x, viewport.Width-bounds.Width-4)), max(4, min(y, viewport.Height-bounds.Height-4))
				if x != s.x || y != s.y {
					s.x, s.y = x, y
					s.updateStyle()
					s.revision++
					// Layout is available now, but paint has not started. Publish the
					// corrected DOM and native controls in this frame; scheduling a
					// component update here would paint one frame at the old position.
					menu.SetAttribute("style", s.style)
					if err := world.SyncUI(); err != nil {
						model.setActiveStatus(err.Error())
						return
					}
					if model.app != nil {
						model.app.RequestRender()
					}
				}
			}
		}
		if s.ui.Kind != peb.Panel {
			for _, f := range s.focus {
				s.bindKeys(world, f.id)
			}
			if s.ui.Kind != peb.ContextMenu {
				s.bindKeys(world, s.closeID)
			}
		}
		if s.needsFocus && (s.ui.Kind != peb.Panel || state.overlay == nil) {
			id := s.closeID
			if s.ui.Kind == peb.Panel {
				id = "panel-close"
			}
			if len(s.focus) > 0 {
				id = s.focus[0].id
			}
			if control, ok := model.extensionFocusControl(world, id); ok && world.Focus(control) {
				s.needsFocus = false
			}
		}
	}
}

func (model *workspaceModel) updateExtensionUI(world *module.Content, state *input.State) (handled, changed bool) {
	if model.closing.action != pencodeCloseNone || model.fileContext.dialog != "" {
		return false, false
	}
	s := model.actions.ui.overlay
	if s == nil {
		if model.bottom.active == pencodePanelExtension && model.actions.ui.panel != nil && state.KeyPressed(input.KeyEscape) && !state.Composing() {
			model.closeExtensionSurface(model.actions.ui.panel, "escape", true)
			return true, true
		}
		return false, false
	}
	if state.PointerPressed() || state.MousePressed(input.MouseRight) || state.MousePressed(input.MouseMiddle) {
		zoom := float32(model.settings.Zoom) / 100
		pointer := state.Pointer()
		if !model.pointerInsideRefs(world, pointer.X/zoom, pointer.Y/zoom, []string{s.rootID}) {
			if s.ui.Kind == peb.ContextMenu || s.ui.Kind == peb.Popup && s.ui.CloseOnOutsideClick {
				model.closeExtensionSurface(s, "outside", false)
				return s.ui.Kind != peb.Popup, true
			}
			return s.ui.Kind != peb.Popup, false
		}
	}
	// Check outside presses before focus: a pointer press may already have moved
	// native focus to the workbench when this pass runs.
	if s.ui.Kind == peb.Popup && !s.ownsFocus(world) {
		return false, false
	}
	if state.KeyPressed(input.KeyEscape) && !state.Composing() {
		model.closeExtensionSurface(s, "escape", true)
		return true, true
	}
	if s.ui.Kind != peb.Popup && (state.KeyPressed(input.KeyTab) || s.ui.Kind == peb.ContextMenu && (state.KeyPressed(input.KeyDown) || state.KeyPressed(input.KeyUp))) {
		count := len(s.focus)
		if s.ui.Kind == peb.Dialog {
			count++
		}
		if count > 0 {
			step := 1
			if state.KeyPressed(input.KeyUp) || state.KeyHeld(input.KeyLeftShift) || state.KeyHeld(input.KeyRightShift) {
				step = -1
			}
			for range count {
				s.focused = (s.focused + step + count) % count
				id := s.closeID
				if s.focused < len(s.focus) {
					id = s.focus[s.focused].id
				}
				if control, ok := model.extensionFocusControl(world, id); ok && world.Focus(control) {
					break
				}
			}
		}
	}
	return true, false
}

func (s *pencodeExtensionSurface) ownsFocus(world *module.Content) bool {
	target, ok := module.ContentGet[htmlui.Focusable](world, world.FocusedControl())
	if !ok || target.Owner != s.model.surface {
		return false
	}
	if target.ID == s.closeID {
		return true
	}
	for _, control := range s.focus {
		if control.id == target.ID {
			return true
		}
	}
	return false
}

func (model *workspaceModel) extensionFocusControl(world *module.Content, id string) (ecs.Entity, bool) {
	if control, ok := world.HTMLControl(model.surface, id); ok {
		return control, true
	}
	for entity, access := range module.ContentQuery[htmlui.Focusable](world) {
		f := access.Get()
		if f.Owner == model.surface && f.ID == id {
			return entity, true
		}
	}
	return 0, false
}

// Navigation is applied in the workbench input pass. Suppress native traversal
// for the same key so it cannot advance focus a second time.
func (s *pencodeExtensionSurface) bindKeys(world *module.Content, id string) {
	if control, ok := s.model.extensionFocusControl(world, id); ok && !module.ContentHas[engineui.TextFieldKeyHandler](world, control) {
		_ = module.ContentInsert(world, control, engineui.TextFieldKeyHandler{Handle: s.interceptKeys})
	}
}
func (s *pencodeExtensionSurface) interceptKeys(state *input.State) bool {
	return s.model.actions.ui.overlay == s && !state.Composing() && (state.KeyPressed(input.KeyEscape) || s.ui.Kind != peb.Popup && state.KeyPressed(input.KeyTab) || s.ui.Kind == peb.ContextMenu && (state.KeyPressed(input.KeyUp) || state.KeyPressed(input.KeyDown)))
}
