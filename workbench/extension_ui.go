package workbench

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/html/dom"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/output"
	"github.com/pencode-labs/pencode/peb"
)

const maxExtensionSurfaces = 32

type pencodeExtensionInvocation struct {
	context peb.ActionContext
	anchor  string
	x, y    float32
}
type pencodeExtensionUIState struct {
	surfaces            []*pencodeExtensionSurface
	invocations         map[string]pencodeExtensionInvocation
	overlay, panel      *pencodeExtensionSurface
	editor              *pencodeExtensionSurface
	wrapper             *htmlui.Definition[pencodeExtensionSurfaceProps, struct{}]
	revealNotifications bool
}
type pencodeExtensionSurfaceProps struct {
	Surface  *pencodeExtensionSurface
	Theme    *pencodeTheme
	Revision uint64
	Visible  bool
}
type pencodeExtensionFocus struct {
	ref, id    string
	submit     string
	showOutput bool
	autosize   bool
	style      string
	height     float32
}
type pencodeExtensionIcon struct {
	ref, source string
}
type pencodeExtensionFocusTarget struct {
	ref, target string
}
type pencodeExtensionScroll struct {
	ref, id, key         string
	position             [2]float32
	follow, restore, end bool
}
type pencodeExtensionSurface struct {
	model                          *workspaceModel
	session                        *extensions.ActionSession
	owner                          string
	ui                             peb.UI
	invocation                     pencodeExtensionInvocation
	definition                     *htmlui.Definition[pencodeExtensionSurfaceProps, struct{}]
	view                           *peb.EditorView
	regions                        []pencodeExtensionRegion
	theme                          *pencodeTheme
	bindings                       []peb.EditorBinding
	focus                          []pencodeExtensionFocus
	focusTargets                   []pencodeExtensionFocusTarget
	icons                          []pencodeExtensionIcon
	pointerFocus                   string
	submitEdits                    []input.TextEdit
	scrolls                        []pencodeExtensionScroll
	output                         *output.Channel
	focused                        int
	revision                       uint64
	prefix, rootID, closeID, style string
	contentID                      string
	scrollKey                      string
	inputID, inputValue            string
	inputAnchor, inputCaret        int
	fill                           bool
	x, y                           float32
	needsFocus, visible            bool
	expiresAt                      time.Time
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
	anchor, err := model.extensionUIAnchor(owner, ui.Anchor)
	if err != nil {
		return err
	}
	fresh := surface == nil
	if fresh {
		if len(state.surfaces) >= maxExtensionSurfaces && (ui.Kind == peb.Panel || ui.Kind == peb.Notification || ui.Kind == peb.EditorTab || state.overlay == nil) {
			return fmt.Errorf("扩展 UI 数量超过 %d", maxExtensionSurfaces)
		}
		prefix := "pencode-extension-ui/" + owner + "/" + ui.ID + "/"
		surface = &pencodeExtensionSurface{model: model, session: session, owner: owner, ui: peb.UI{Kind: ui.Kind}, prefix: prefix, rootID: prefix + "surface", closeID: prefix + "close", contentID: prefix + "content", focused: -1}
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
		surface.invocation.anchor = anchor
	} else if surface.invocation.context.ID != "" {
		surface.invocation.anchor = "action-extensions/" + owner + "/" + surface.invocation.context.ID
	}
	if ui.Kind != peb.Notification && ui.Kind != peb.EditorTab {
		surface.updateStyle()
	}
	surface.expiresAt = time.Time{}
	if ui.Kind == peb.Notification {
		state.revealNotifications = fresh || state.revealNotifications
		if ui.AutoCloseMS > 0 {
			surface.expiresAt = time.Now().Add(time.Duration(ui.AutoCloseMS) * time.Millisecond)
			if model.app != nil && model.app.WakeAt != nil {
				model.app.WakeAt(surface.expiresAt)
			}
		}
	} else if ui.Kind == peb.EditorTab {
		if fresh || ui.View == nil {
			model.selectExtensionTab(surface)
		}
	} else if ui.Kind == peb.Panel {
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
	surface.needsFocus = ui.Kind != peb.Notification && ui.Kind != peb.EditorTab && (fresh || ui.View != nil)
	if fresh {
		surface.visible = true
		surface.send(peb.EditorEvent{Kind: "open"}, "")
	}
	return nil
}

func (model *workspaceModel) extensionUIAnchor(owner, anchor string) (string, error) {
	if anchor == "" {
		return "", nil
	}
	if target, ok := strings.CutPrefix(anchor, "ui:"); ok {
		id, ref, valid := strings.Cut(target, "#")
		if valid && ref != "" {
			for _, s := range model.actions.ui.surfaces {
				if s.owner == owner && s.ui.ID == id {
					for _, f := range s.focus {
						if f.ref == ref {
							return f.id, nil
						}
					}
				}
			}
		}
		return "", fmt.Errorf("扩展 UI 锚点不存在: %s", anchor)
	}
	return "action-extensions/" + owner + "/" + anchor, nil
}

func (s *pencodeExtensionSurface) setView(view *peb.EditorView) error {
	template, regions, err := splitExtensionRegions(view.HTML)
	if err != nil {
		return err
	}
	replace := s.definition == nil || s.definition.Template.Source != template || s.view.CSS != view.CSS || s.theme != s.model.workbenchTheme
	if replace && s.ui.Kind == peb.EditorTab && s.live() && s.model.fontResources != nil {

		world := s.model.fontResources
		for _, f := range s.focus {
			control, ok := world.HTMLControl(s.model.surface, f.id)
			if !ok || world.FocusedControl() != control {
				continue
			}
			if field, ok := module.ContentGet[engineui.TextField](world, control); ok {
				s.inputID, s.inputValue = f.id, field.Value
				s.inputAnchor, s.inputCaret = field.Anchor, field.Caret
			}
			break
		}
	}
	focus := make([]pencodeExtensionFocus, 0, len(view.Events))
	var focusTargets []pencodeExtensionFocusTarget
	var icons []pencodeExtensionIcon
	var iconErr error
	var channel *output.Channel
	var scrollKey string
	scrolls := make([]pencodeExtensionScroll, 0, len(s.scrolls))
	followsOutput := false
	fill := false
	if err := validateExtensionView(view, func(node *dom.Node, hidden bool) {
		if name, ok := node.Attr("data-pencode-icon"); ok {
			ref, _ := node.Attr("data-ref")
			source := "icons/" + name + ".svg"
			_, known := s.model.workbenchTheme.icons[pencodeIconKey{source, "icon.foreground"}]
			if node.Data != "img" || ref == "" || !known {
				iconErr = fmt.Errorf("无效的 IDE 图标：%s", name)
			} else {
				icons = append(icons, pencodeExtensionIcon{ref: ref, source: source})
			}
		}
		if layout, _ := node.Attr("data-pencode-layout"); layout == "fill" && !hidden {
			fill = true
		}
		if mode, _ := node.Attr("data-pencode-scroll"); (mode == "end" || mode == "preserve") && !hidden {
			if ref, _ := node.Attr("data-ref"); ref != "" {
				id, _ := node.Attr("id")
				if id == "" {
					id = s.prefix + "control-" + ref
				}
				key, _ := node.Attr("data-pencode-scroll-key")
				follow := mode == "end"
				if follow {
					scrollKey, followsOutput = key, true
				}
				scrolls = append(scrolls, s.captureScroll(ref, id, key, follow))
			}
		}
		if ref, _ := node.Attr("data-ref"); ref != "" {
			if target, _ := node.Attr("data-pencode-focus"); target != "" && !hidden {
				focusTargets = append(focusTargets, pencodeExtensionFocusTarget{ref: ref, target: target})
			}
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
				action, _ := node.Attr("data-pencode-action")
				showOutput := node.Data == "button" && action == "show-output"
				if showOutput {
					channel = s.model.outputStore().Channel("Extension: " + s.owner)
				}
				_, autosize := node.Attr("data-pencode-autosize")
				style, _ := node.Attr("style")
				var submit string
				if node.Data == "textarea" {
					submit, _ = node.Attr("data-pencode-submit-on-enter")
				}
				focus = append(focus, pencodeExtensionFocus{ref: ref, id: id, submit: submit, showOutput: showOutput, autosize: node.Data == "textarea" && autosize, style: style})
			}
		}
	}); err != nil {
		return err
	}
	if iconErr != nil {
		return iconErr
	}
	for i := range focusTargets {
		target := &focusTargets[i]
		found := false
		for _, f := range focus {
			if f.ref == target.target {
				target.target, found = f.id, true
				break
			}
		}
		if !found {
			return fmt.Errorf("扩展 UI 聚焦目标不存在：%s", target.target)
		}
	}
	if s.ui.Kind == peb.EditorTab && !followsOutput {
		scrolls = append(scrolls, s.captureScroll("", s.contentID, "", true))
	}
	behavior := htmlui.Behavior[pencodeExtensionSurfaceProps, struct{}]{
		Render: func(c htmlui.RenderContext[pencodeExtensionSurfaceProps, struct{}], target *htmlui.View) {
			surface := c.Props.Surface
			for _, icon := range surface.icons {
				target.Attr(icon.ref, "src", surface.model.themedIcon(icon.source, "icon.foreground"))
			}
			for _, scroll := range surface.scrolls {
				if scroll.ref != "" {
					target.Attr(scroll.ref, "id", scroll.id)
				}
			}
			for _, f := range surface.focus {
				target.Attr(f.ref, "id", f.id)
				target.Event(f.ref, htmlui.EventFocus)
				if f.showOutput {
					target.Event(f.ref, htmlui.EventClick)
				}
			}
			for _, delegate := range surface.focusTargets {
				target.Event(delegate.ref, htmlui.EventClick)
			}
			for _, binding := range surface.bindings {
				target.Event(binding.Ref, htmlui.EventKind(binding.Kind))
			}
			for _, region := range surface.regions {
				htmlui.AppendComponentChild(target, region.ref, componentKey(region.ref), region.definition, *c.Props, struct{}{})
			}
		},
		HandleEvent: func(c *htmlui.ComponentEventContext[pencodeExtensionSurfaceProps, struct{}]) {
			surface := c.Props.Surface
			if !surface.live() || surface.model.closing.action != pencodeCloseNone {
				return
			}
			event := c.Event
			if event.Kind == htmlui.EventClick {
				for _, delegate := range surface.focusTargets {
					if delegate.ref == c.Ref {
						surface.pointerFocus = delegate.target
						break
					}
				}
				for _, f := range surface.focus {
					if f.ref == c.Ref && f.showOutput {
						p := &surface.model.bottom
						p.active, p.source, p.filter = pencodePanelOutput, surface.output, ""
						p.options = p.options[:0]
						markPencodeRootDirtyByModel(surface.model.fontResources, surface.model)
						return
					}
				}
			}
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
	}
	css := s.model.workbenchTheme.resolveCSS(view.CSS)
	for i := range regions {
		region := &regions[i]
		if s.theme == s.model.workbenchTheme && s.view != nil && s.view.CSS == view.CSS {
			for _, previous := range s.regions {
				if region.ref == previous.ref && region.html == previous.html {
					region.definition = previous.definition
					break
				}
			}
		}
		if region.definition == nil {
			region.definition, err = htmlui.DefineScoped(htmlui.NewTemplate(region.html), css, behavior)
			if err != nil {
				return err
			}
		}
	}
	definition := s.definition
	if replace {
		definition, err = htmlui.DefineScoped(htmlui.NewTemplate(template), css, behavior)
		if err != nil {
			return err
		}
	}
	if s.scrollKey != scrollKey {
		s.inputID = ""
	}
	s.scrolls, s.scrollKey, s.fill = scrolls, scrollKey, fill
	keepInput := false
	for _, f := range focus {
		keepInput = keepInput || f.id == s.inputID
	}
	if !keepInput {
		s.inputID, s.inputValue = "", ""
	}
	s.definition, s.bindings, s.focus, s.regions = definition, view.Events, focus, regions
	s.focusTargets, s.icons = focusTargets, icons
	s.view, s.theme = view, s.model.workbenchTheme
	s.output = channel
	s.focused = -1
	return nil
}

func (s *pencodeExtensionSurface) captureScroll(ref, id, key string, follow bool) pencodeExtensionScroll {
	state := pencodeExtensionScroll{ref: ref, id: id, key: key, follow: follow, end: follow}
	for _, previous := range s.scrolls {
		if previous.ref != ref || previous.key != key || s.model.fontResources == nil {
			continue
		}
		if node, ok := s.model.fontResources.HTMLNode(s.model.surface, previous.id); ok {
			if area, ok := node.ScrollContainer(); ok {
				if scroll, ok := module.ContentGet[scene.UIScroll](s.model.fontResources, area); ok {
					state.position, state.restore = [2]float32{scroll.Position.X(), scroll.Position.Y()}, true
					state.end = follow && scroll.ContentSize.Y()-scroll.ViewportSize.Y()+scroll.Position.Y() < 24
				}
			}
		}
		break
	}
	return state
}

func (s *pencodeExtensionSurface) scrollTarget() string {
	for _, state := range s.scrolls {
		if state.follow {
			return state.id
		}
	}
	return s.contentID
}

func (s *pencodeExtensionSurface) live() bool {
	state := &s.model.actions.ui
	if s.ui.Kind == peb.EditorTab {
		return state.editor == s
	}
	if s.ui.Kind == peb.Notification {
		for _, candidate := range state.surfaces {
			if candidate == s {
				return true
			}
		}
		return false
	}
	return state.overlay == s || state.panel == s && s.model.bottom.active == pencodePanelExtension
}

func newExtensionSurfaceDefinition() (*htmlui.Definition[pencodeExtensionSurfaceProps, struct{}], error) {
	return htmlui.DefineScoped(htmlui.NewTemplate(`<section data-ref="surface"><header data-ref="header" class="pencode-extension-header"><strong data-ref="title"></strong><button data-ref="close" class="pencode-icon-button" type="button" title="关闭" aria-label="关闭"><img data-ref="close-icon" class="pencode-close-icon" alt=""></button></header><div data-ref="content" class="pencode-extension-content"></div></section>`), "", htmlui.Behavior[pencodeExtensionSurfaceProps, struct{}]{
		Render: func(c htmlui.RenderContext[pencodeExtensionSurfaceProps, struct{}], view *htmlui.View) {
			s := c.Props.Surface
			view.Attr("surface", "id", s.rootID)
			view.Attr("content", "id", s.contentID)
			if s.fill && s.ui.Kind == peb.EditorTab {
				view.Attr("content", "class", "pencode-extension-content pencode-extension-fill")
			}
			view.Attr("surface", "aria-label", s.ui.Title)
			view.Attr("close", "id", s.closeID)
			view.Attr("close-icon", "src", s.model.themedIcon("icons/close.svg", "icon.foreground"))
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
			case peb.Notification:
				role, class = "status", "pencode-extension-notification"
				view.Attr("surface", "aria-live", "polite")
			case peb.EditorTab:
				class = "pencode-extension-editor"
				view.Attr("header", "hidden", "")
			}
			if s.ui.Kind != peb.Panel && s.ui.Kind != peb.Notification && s.ui.Kind != peb.EditorTab {
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
		if s.view != nil && s.theme != model.workbenchTheme {
			if err := s.setView(s.view); err != nil {
				model.logOutput("Extension: "+s.owner, err.Error())
			}
		}
		slot := "extension-overlays"
		if s.ui.Kind == peb.Panel {
			slot = "extension-pane"
		} else if s.ui.Kind == peb.Notification {
			slot = "extension-notifications"
		} else if s.ui.Kind == peb.EditorTab {
			slot = "extension-editor"
		}
		htmlui.AppendComponentChild(view, slot, componentKey(s.rootID), state.wrapper, pencodeExtensionSurfaceProps{Surface: s, Theme: model.workbenchTheme, Revision: s.revision, Visible: s.live()}, struct{}{})
	}
	setPanelVisible(view, "extension-pane", model.bottom.active == pencodePanelExtension)
	setPanelVisible(view, "extension-editor", state.editor != nil)
}

func (model *workspaceModel) closeExtensionSurface(s *pencodeExtensionSurface, reason string, restore bool) {
	state := &model.actions.ui
	wasLive := s.live()
	if restore && (s.ui.Kind == peb.Popup || s.ui.Kind == peb.Notification) && model.fontResources != nil {
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
	if state.editor == s {
		state.editor = nil
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
		if state.editor == s {
			state.editor = nil
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

// Deadlines use the app loop's existing wake scheduler, without per-message
// timers or goroutines. Re-arm after early wakes (for example after an update).
func (model *workspaceModel) expireExtensionNotifications(now time.Time) (changed bool) {
	var next time.Time
	for i := 0; i < len(model.actions.ui.surfaces); {
		s := model.actions.ui.surfaces[i]
		if !s.expiresAt.IsZero() {
			if !now.Before(s.expiresAt) {
				model.closeExtensionSurface(s, "timeout", true)
				changed = true
				continue
			}
			if next.IsZero() || s.expiresAt.Before(next) {
				next = s.expiresAt
			}
		}
		i++
	}
	if !next.IsZero() && model.app != nil && model.app.WakeAt != nil {
		model.app.WakeAt(next)
	}
	return changed
}

// Called after layout: position overlays inside the viewport and focus new surfaces.
func (model *workspaceModel) syncExtensionUI(world *module.Content) (scrollChanged bool) {
	state := &model.actions.ui
	if state.revealNotifications {
		if node, ok := world.HTMLNode(model.surface, "pencode-extension-notifications"); ok {
			if area, ok := node.ScrollContainer(); ok {
				if scroll := module.ContentMut[scene.UIScroll](world, area); scroll != nil {
					previous := scroll.Position
					scroll.Position[1] = -max(0, scroll.ContentSize.Y()-scroll.ViewportSize.Y())
					scroll.Momentum[1] = 0
					scrollChanged = scrollChanged || scroll.Position != previous
					if model.app != nil {
						model.app.RequestRender()
					}
				}
			}
			state.revealNotifications = false
		}
	}
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
		if s.resizeInputs(world) {
			if err := world.SyncHTML(); err != nil {
				model.setActiveStatus(err.Error())
				return
			}
			if err := world.SyncUI(); err != nil {
				model.setActiveStatus(err.Error())
				return
			}
		}
		for i := range s.scrolls {
			state := &s.scrolls[i]
			if !state.restore && !state.end {
				continue
			}
			if node, ok := world.HTMLNode(model.surface, state.id); ok {
				if area, ok := node.ScrollContainer(); ok {
					if scroll := module.ContentMut[scene.UIScroll](world, area); scroll != nil {
						previous := scroll.Position
						for axis := range 2 {
							scroll.Position[axis] = max(-max(0, scroll.ContentSize[axis]-scroll.ViewportSize[axis]), min(0, state.position[axis]))
							scroll.Momentum[axis] = 0
						}
						if state.end {
							scroll.Position[1] = -max(0, scroll.ContentSize.Y()-scroll.ViewportSize.Y())
						}
						scrollChanged = scrollChanged || scroll.Position != previous
						state.restore, state.end = false, false
						if model.app != nil {
							model.app.RequestRender()
						}
					}
				}
			}
		}
		if model.closing.action != pencodeCloseNone {
			s.needsFocus = s.ui.Kind != peb.Notification && s.ui.Kind != peb.EditorTab
			continue
		}
		if s.pointerFocus != "" {
			// Native pointer focus is resolved before layout. Only redirect a
			// blank click; keep child controls and text selections intact.
			if (state.overlay == nil || state.overlay == s) && world.FocusedControl() == 0 {
				if control, ok := model.extensionFocusControl(world, s.pointerFocus); ok {
					world.Focus(control)
				}
			}
			s.pointerFocus = ""
		}
		if s.inputID != "" {
			if control, ok := world.HTMLControl(model.surface, s.inputID); ok {
				if field := module.ContentMut[engineui.TextField](world, control); field != nil {
					if state.overlay == nil && world.Focus(control) {
						field.Focused = true
						if field.Value == s.inputValue {
							field.Anchor, field.Caret = s.inputAnchor, s.inputCaret
						}
					}
					s.inputID, s.inputValue = "", ""
				}
			}
		}
		if s.ui.Kind != peb.Panel && s.ui.Kind != peb.Notification && s.ui.Kind != peb.EditorTab {
			menu, menuOK := world.HTMLNode(model.surface, s.rootID)
			root, rootOK := world.HTMLNode(model.surface, "pencode-extension-overlays")
			if menuOK && rootOK {
				bounds, _ := menu.Bounds()
				viewport, _ := root.Bounds()
				placement := s.ui.Placement
				x, y := float32(0), float32(0)
				switch {
				case placement.Mode == peb.PositionAbsolute:
				case placement.Mode == peb.PositionAnchor || placement.Mode == peb.PositionAnchorAbove || s.ui.Kind == peb.ContextMenu && placement.Mode == peb.PositionDefault:
					x, y = s.invocation.x-viewport.X, s.invocation.y-viewport.Y
					if anchor, ok := world.HTMLNode(model.surface, s.invocation.anchor); ok {
						if rect, ok := anchor.Bounds(); ok {
							x, y = rect.X-viewport.X, rect.Y+rect.Height-viewport.Y
							if placement.Mode == peb.PositionAnchorAbove && rect.Y-viewport.Y-bounds.Height+placement.Y >= 4 {
								y = rect.Y - viewport.Y - bounds.Height
							}
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
		for _, f := range s.focus {
			if f.submit != "" || s.ui.Kind != peb.Panel && s.ui.Kind != peb.EditorTab {
				s.bindKeys(world, f.id)
			}
		}
		if s.ui.Kind != peb.Panel && s.ui.Kind != peb.EditorTab {
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
	if scrollChanged {
		// Restore paint culled using the previous offset before finalizing the
		// native scroll transforms and scrollbar geometry in LayoutSystem.
		if err := world.SyncHTML(); err != nil {
			model.setActiveStatus(err.Error())
			return
		}
		if err := world.SyncUI(); err != nil {
			model.setActiveStatus(err.Error())
		}
	}
	return
}

// Use native text metrics so newlines, soft wrapping and IME composition share
// the control's layout. CSS min/max-height bound the requested border-box size.
func (s *pencodeExtensionSurface) resizeInputs(world *module.Content) (changed bool) {
	for i := range s.focus {
		f := &s.focus[i]
		if !f.autosize {
			continue
		}
		control, ok := world.HTMLControl(s.model.surface, f.id)
		if !ok {
			continue
		}
		visual, ok := module.ContentFindPart[engineui.ValuePart, scene.Text2D](world, control)
		if !ok {
			continue
		}
		text := module.ContentMut[scene.Text2D](world, visual)
		link, ok := module.ContentGet[htmlui.ControlText](world, visual)
		node, exists := world.HTMLNode(s.model.surface, f.id)
		if !ok || !exists || text == nil {
			continue
		}
		bounds, ok := node.Bounds()
		if !ok || bounds.Width <= 0 {
			continue
		}
		_, height, _ := scene.MeasureText2D(text)
		height = float32(math.Ceil(float64(height + bounds.Height - link.Content.Height)))
		if f.height == height {
			continue
		}
		f.height = height
		node.SetAttribute("style", f.style+";height:"+strconv.Itoa(int(height))+"px")
		changed = true
	}
	return changed
}

func (model *workspaceModel) updateExtensionUI(world *module.Content, state *input.State) (handled, changed bool) {
	if model.closing.action != pencodeCloseNone || model.fileContext.dialog != "" {
		return false, false
	}
	s := model.actions.ui.overlay
	if s == nil || s.ui.Kind == peb.Popup && !s.ownsFocus(world) {
		for _, candidate := range model.actions.ui.surfaces {
			if candidate.ui.Kind == peb.Notification && candidate.ownsFocus(world) {
				s = candidate
				break
			}
		}
	}
	if s == nil {
		if model.bottom.active == pencodePanelExtension && model.actions.ui.panel != nil && state.KeyPressed(input.KeyEscape) && !state.Composing() {
			model.closeExtensionSurface(model.actions.ui.panel, "escape", true)
			return true, true
		}
		return false, false
	}
	nonmodal := s.ui.Kind == peb.Popup || s.ui.Kind == peb.Notification
	if state.PointerPressed() || state.MousePressed(input.MouseRight) || state.MousePressed(input.MouseMiddle) {
		zoom := float32(model.settings.Zoom) / 100
		pointer := state.Pointer()
		if !model.pointerInsideRefs(world, pointer.X/zoom, pointer.Y/zoom, []string{s.rootID}) {
			if s.ui.Kind == peb.ContextMenu || s.ui.Kind == peb.Popup && s.ui.CloseOnOutsideClick {
				model.closeExtensionSurface(s, "outside", false)
				return !nonmodal, true
			}
			return !nonmodal, false
		}
	}
	// Check outside presses before focus: a pointer press may already have moved
	// native focus to the workbench when this pass runs.
	if nonmodal && !s.ownsFocus(world) {
		return false, false
	}
	if state.KeyPressed(input.KeyEscape) && !state.Composing() {
		model.closeExtensionSurface(s, "escape", true)
		return true, true
	}
	if !nonmodal && (state.KeyPressed(input.KeyTab) || s.ui.Kind == peb.ContextMenu && (state.KeyPressed(input.KeyDown) || state.KeyPressed(input.KeyUp))) {
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
	if !s.live() || state.Composing() {
		return false
	}
	if state.KeyPressed(input.KeyEnter) && !state.KeyPressedDuringComposition(input.KeyEnter) &&
		!state.KeyHeld(input.KeyLeftShift) && !state.KeyHeld(input.KeyRightShift) {
		world := s.model.fontResources
		control := world.FocusedControl()
		for _, f := range s.focus {
			if f.submit == "" {
				continue
			}
			if target, ok := world.HTMLControl(s.model.surface, f.id); !ok || target != control {
				continue
			}
			field := module.ContentMut[engineui.TextField](world, control)
			if native, ok := module.ContentGet[htmlui.Control](world, control); field == nil || !ok || native.ReadOnly || native.Disabled {
				return false
			}
			// Apply text queued alongside Enter before submitting the draft.
			s.submitEdits = state.AppendTextEdits(s.submitEdits[:0])
			edits := s.submitEdits[:0]
			for _, edit := range s.submitEdits {
				if edit.Command != input.TextCommandEnter {
					edits = append(edits, edit)
				}
			}
			anchor, caret := field.Anchor, field.Caret
			if selection, ok := state.TextSelection(); ok {
				anchor, caret = selection.Start, selection.End
			}
			value, caret, _ := state.ApplyTextEditsInRange(field.Value, min(anchor, caret), max(anchor, caret), field.MaxRunes, true, edits)
			if value != field.Value {
				world.SetControlValue(control, value)
				field.Caret, field.Anchor = caret, caret
			}
			clear(s.submitEdits)
			s.submitEdits = s.submitEdits[:0]
			s.send(peb.EditorEvent{Kind: "input", Ref: f.ref, Value: value, Anchor: uint32(max(0, field.Anchor)), Focus: uint32(max(0, field.Caret))}, "")
			s.send(peb.EditorEvent{Kind: "submit", Ref: f.submit}, "")
			return true
		}
	}
	if s.ui.Kind == peb.Panel || s.ui.Kind == peb.EditorTab {
		return false
	}
	return state.KeyPressed(input.KeyEscape) || s.ui.Kind != peb.Popup && s.ui.Kind != peb.Notification && state.KeyPressed(input.KeyTab) || s.ui.Kind == peb.ContextMenu && (state.KeyPressed(input.KeyUp) || state.KeyPressed(input.KeyDown))
}
