package workbench

import (
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/html/dom"
	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/output"
	"github.com/pencode-labs/pencode/peb"
)

const maxExtensionSurfaces = 32

type pencodeExtensionInvocation struct {
	anchor  string
	context peb.ActionContext
	x, y    float32
}
type pencodeExtensionUIState struct {
	invocations         map[string]pencodeExtensionInvocation
	overlay, panel      *pencodeExtensionSurface
	editor              *pencodeExtensionSurface
	wrapper             *htmlui.Definition[pencodeExtensionSurfaceProps, struct{}]
	surfaces            []*pencodeExtensionSurface
	selection           pencodeExtensionTextSelection
	revealNotifications bool
}
type pencodeExtensionSurfaceProps struct {
	Surface  *pencodeExtensionSurface
	Theme    *pencodeTheme
	Revision uint64
	Visible  bool
}
type pencodeExtensionFocus struct {
	inputLinks  *pencodeInputLinks
	ref, id     string
	submit      string
	style       string
	caret       string
	height      float32
	showOutput  bool
	autosize    bool
	completion  bool
	pasteImages bool
	placeCaret  bool
	selectText  bool
}
type pencodeExtensionIcon struct {
	ref, source, foreground, copyRef string
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
	expiresAt                      time.Time
	tree                           *dom.Node
	model                          *workspaceModel
	document                       *pencodeCustomDocument
	session                        *extensions.ActionSession
	output                         *output.Channel
	images                         map[string][]byte
	copies                         map[string]string
	definition                     *htmlui.Definition[pencodeExtensionSurfaceProps, struct{}]
	styles                         *htmlui.Definition[pencodeExtensionSurfaceProps, struct{}]
	view                           *peb.EditorView
	links                          map[string]string
	theme                          *pencodeTheme
	contentID                      string
	inputID, inputValue            string
	owner                          string
	scrollKey                      string
	prefix, rootID, closeID, style string
	pointerFocus                   string
	copiedRef                      string
	invocation                     pencodeExtensionInvocation
	regionSplitter                 peb.ViewRegionSplitter
	regions                        []pencodeExtensionRegion
	bindings                       []peb.EditorBinding
	scrolls                        []pencodeExtensionScroll
	icons                          []pencodeExtensionIcon
	focusTargets                   []pencodeExtensionFocusTarget
	focus                          []pencodeExtensionFocus
	expanded                       []pencodeExtensionExpanded
	selectables                    []pencodeExtensionSelectable
	submitEdits                    []input.TextEdit
	ui                             peb.UI
	focused                        int
	inputAnchor, inputCaret        int
	revision                       uint64
	x, y                           float32
	fill                           bool
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
		if bounds, ok := model.extensionAnchorBounds(world, invocation.anchor); ok {
			invocation.x, invocation.y = bounds.X, bounds.Y+bounds.Height
		}
	}
	if model.actions.ui.invocations == nil {
		model.actions.ui.invocations = make(map[string]pencodeExtensionInvocation)
	}
	model.actions.ui.invocations[owner] = invocation
}

// Native control transforms include scrolling and CSS transforms. Convert back
// to surface coordinates so placement remains correct at every workspace zoom.
func (model *workspaceModel) extensionAnchorBounds(world *module.Content, id string) (htmlui.Rect, bool) {
	control, ok := world.HTMLControl(model.surface, id)
	if !ok {
		return htmlui.Rect{}, false
	}
	position := world.Ref[scene.UIGlobalTransform](control)
	surface := world.Ref[scene.UIGlobalTransform](model.surface)
	size := world.Ref[scene.Size2D](control)
	if position == nil || surface == nil || size == nil {
		return htmlui.Rect{}, false
	}
	relative := surface.Matrix.Inv().Mul4(position.Matrix)
	return htmlui.Rect{X: relative[12], Y: relative[13], Width: size.Width * relative[0], Height: size.Height * relative[5]}, true
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
			return fmt.Errorf(model.tr("扩展 UI 数量超过 %d"), maxExtensionSurfaces)
		}
		prefix := "pencode-extension-ui/" + owner + "/" + ui.ID + "/"
		surface = &pencodeExtensionSurface{model: model, session: session, owner: owner, ui: peb.UI{Kind: ui.Kind}, prefix: prefix, rootID: prefix + "surface", closeID: prefix + "close", contentID: prefix + "content", focused: -1}
	} else if surface.ui.Kind != ui.Kind {
		return fmt.Errorf("%s", model.tr("更新 UI 不能改变 Kind"))
	}
	if ui.View != nil {
		if err := surface.setView(ui.View); err != nil {
			return err
		}
	} else if surface.definition == nil {
		return fmt.Errorf("%s", model.tr("首次打开 UI 必须提供 View"))
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
		return "", fmt.Errorf(model.tr("扩展 UI 锚点不存在: %s"), anchor)
	}
	return "action-extensions/" + owner + "/" + anchor, nil
}

func (s *pencodeExtensionSurface) setView(view *peb.EditorView) error {
	template, expanded, err := splitExtensionExpanded(view.HTML)
	if err != nil {
		return err
	}
	template, parts, err := s.regionSplitter.Split(template)
	if err != nil {
		return err
	}
	scratch := pencodeExtensionScratches.Get().(*pencodeExtensionScratch)
	defer scratch.release()
	regions := scratch.resizeRegions(len(parts))
	for i, part := range parts {
		regions[i] = pencodeExtensionRegion{ref: part.Ref, html: part.HTML}
	}
	previous, trees := scratch.previous, scratch.trees
	for i := range s.regions {
		previous[s.regions[i].ref] = &s.regions[i]
	}
	for i := range regions {
		r := &regions[i]
		if old := previous[r.ref]; old != nil && old.html == r.html {
			r.tree = old.tree
		}
		if r.tree == nil {
			r.tree, err = dom.Parse(r.html)
			if err != nil {
				return err
			}
		}
		trees[r.ref] = r.tree
	}
	tree := s.tree
	if tree == nil || s.definition.Template.Source != template {
		tree, err = dom.Parse(template)
		if err != nil {
			return err
		}
	}
	replace := s.definition == nil || s.definition.Template.Source != template || s.view.CSS != view.CSS || s.theme != s.model.workbenchTheme
	if replace && s.ui.Kind == peb.EditorTab && s.live() && s.model.fontResources != nil {

		world := s.model.fontResources
		for _, f := range s.focus {
			control, ok := world.HTMLControl(s.model.surface, f.id)
			if !ok || world.FocusedControl() != control {
				continue
			}
			if field, ok := world.Get[engineui.TextField](control); ok {
				s.inputID, s.inputValue = f.id, field.Value
				s.inputAnchor, s.inputCaret = field.Anchor, field.Caret
			}
			break
		}
	}
	focus := make([]pencodeExtensionFocus, 0, len(view.Events))
	var focusTargets []pencodeExtensionFocusTarget
	var selectables []pencodeExtensionSelectable
	var icons []pencodeExtensionIcon
	var links map[string]string
	var copies map[string]string
	var iconErr error
	var channel *output.Channel
	var scrollKey string
	scrolls := make([]pencodeExtensionScroll, 0, len(s.scrolls))
	followsOutput := false
	fill := false
	if err := scratch.validate(view, tree, trees, func(node *dom.Node, hidden bool) {
		if !hidden {
			ref, _ := node.Attr("data-ref")
			href, _ := node.Attr("data-pencode-href")
			if node.Data == "a" {
				href, _ = node.Attr("href")
			}
			if ref != "" && href != "" && !extensionOwnsClick(view.Events, ref) {
				if links == nil {
					links = make(map[string]string)
				}
				links[ref] = href
			}
			if value, ok := node.Attr("data-pencode-copy"); ok && node.Data == "button" && ref != "" && !extensionOwnsClick(view.Events, ref) {
				if _, disabled := node.Attr("disabled"); !disabled {
					if copies == nil {
						copies = make(map[string]string)
					}
					copies[ref] = value
				}
			}
		}
		if name, ok := node.Attr("data-pencode-icon"); ok {
			ref, _ := node.Attr("data-ref")
			source := "icons/" + name + ".svg"
			_, known := s.model.workbenchTheme.icons[pencodeIconKey{source, "icon.foreground"}]
			if node.Data != "img" || ref == "" || !known {
				iconErr = fmt.Errorf("无效的 IDE 图标：%s", name)
			} else {
				foreground := "icon.foreground"
				for parent := node.Parent; parent != nil; parent = parent.Parent {
					role, _ := parent.Attr("role")
					if parent.Data == "a" || role == "link" {
						foreground = "textLink.foreground"
						break
					}
				}
				copyRef := ""
				if parent := node.Parent; parent != nil {
					if _, ok := parent.Attr("data-pencode-copy"); ok {
						copyRef, _ = parent.Attr("data-ref")
					}
				}
				icons = append(icons, pencodeExtensionIcon{ref: ref, source: source, foreground: foreground, copyRef: copyRef})
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
			if _, selectable := node.Attr("data-pencode-selectable"); selectable && !hidden {
				id, _ := node.Attr("id")
				if id == "" {
					id = s.prefix + "control-" + ref
				}
				selectables = append(selectables, pencodeExtensionSelectable{ref, id})
			}
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
				completion, _ := node.Attr("data-pencode-completion")
				_, pasteImages := node.Attr("data-pencode-paste-images")
				caret, _ := node.Attr("data-pencode-caret")
				var inputLinks *pencodeInputLinks
				if _, enabled := node.Attr("data-pencode-markdown-links"); enabled && node.Data == "textarea" {
					for _, previous := range s.focus {
						if previous.ref == ref {
							inputLinks = previous.inputLinks
							break
						}
					}
					if inputLinks == nil {
						inputLinks = &pencodeInputLinks{}
					}
				}
				placeCaret := caret != ""
				for _, previous := range s.focus {
					if previous.ref == ref && previous.caret == caret {
						placeCaret = previous.placeCaret
						break
					}
				}
				role, _ := node.Attr("role")
				focus = append(focus, pencodeExtensionFocus{ref: ref, id: id, submit: submit, showOutput: showOutput, autosize: node.Data == "textarea" && autosize, style: style, completion: completion == "true", pasteImages: pasteImages, caret: caret, placeCaret: placeCaret, inputLinks: inputLinks, selectText: node.Data == "a" || role == "link"})
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
			for _, state := range surface.expanded {
				target.Attr(state.ref, "aria-expanded", state.value)
			}
			for _, icon := range surface.icons {
				source := icon.source
				if icon.copyRef != "" && icon.copyRef == surface.copiedRef {
					source = "icons/check.svg"
				}
				target.Attr(icon.ref, "src", surface.model.themedIcon(source, icon.foreground))
			}
			for _, region := range surface.selectables {
				target.Attr(region.ref, "id", region.id)
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
			for ref := range surface.links {
				target.Event(ref, htmlui.EventClick)
			}
			for ref := range surface.copies {
				target.Event(ref, htmlui.EventClick)
			}
		},
		HandleEvent: func(c *htmlui.ComponentEventContext[pencodeExtensionSurfaceProps, struct{}]) {
			surface := c.Props.Surface
			if !surface.live() || surface.model.closing.action != pencodeCloseNone {
				return
			}
			event := c.Event
			if event.Kind == htmlui.EventInput {
				if field, ok := surface.model.fontResources.Get[engineui.TextField](event.Target); ok {
					event.Anchor, event.Focus = field.Anchor, field.Caret
				}
			}
			if event.Kind == htmlui.EventClick {
				if selection := &surface.model.actions.ui.selection; selection.surface == surface && selection.dragged {
					return
				}
				if value, ok := surface.copies[c.Ref]; ok {
					if state, ok := surface.model.fontResources.Resource[*input.State](); ok {
						if state.SetClipboardText(value) && surface.copiedRef != c.Ref {
							surface.copiedRef = c.Ref
							markPencodeRootDirtyByModel(surface.model.fontResources, surface.model)
						}
					}
					return
				}
				if href := surface.links[c.Ref]; href != "" {
					surface.openLink(href)
					markPencodeRootDirtyByModel(surface.model.fontResources, surface.model)
					return
				}
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
	// Keep CSS mounted independently. Content and region templates
	// inherit its scope without changing the workbench stylesheet on each update.
	styles := s.styles
	if styles == nil || s.theme != s.model.workbenchTheme || s.view.CSS != view.CSS {
		styles, err = htmlui.DefineScoped(htmlui.NewTemplate(""), s.model.workbenchTheme.resolveCSS(view.CSS), htmlui.Behavior[pencodeExtensionSurfaceProps, struct{}]{})
		if err != nil {
			return err
		}
	}
	for i := range regions {
		region := &regions[i]
		if old := previous[region.ref]; old != nil && old.html == region.html && s.theme == s.model.workbenchTheme && s.view.CSS == view.CSS {
			region.definition = old.definition
		}
		if region.definition == nil {
			region.definition, err = styles.DefineScopedChild(htmlui.NewTemplate(strings.Clone(region.html)), behavior)
			if err != nil {
				return err
			}
		}
	}
	definition := s.definition
	if replace {
		outer := behavior
		outer.Render = func(c htmlui.RenderContext[pencodeExtensionSurfaceProps, struct{}], target *htmlui.View) {
			behavior.Render(c, target)
			for _, region := range c.Props.Surface.regions {
				target.AppendComponentChild(region.ref, componentKey(region.ref), region.definition, *c.Props, struct{}{})
			}
		}
		definition, err = styles.DefineScopedChild(htmlui.NewTemplate(template), outer)
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
	s.definition, s.bindings, s.focus = definition, view.Events, focus
	s.regions, scratch.regions = regions, s.regions
	s.tree = tree
	s.expanded = expanded
	s.styles = styles
	s.focusTargets, s.icons = focusTargets, icons
	s.selectables = selectables
	s.links = links
	s.copies = copies
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
				if scroll, ok := s.model.fontResources.Get[scene.UIScroll](area); ok {
					state.position, state.restore = [2]float32{scroll.Position.X(), scroll.Position.Y()}, true
					state.end = follow && scroll.ContentSize.Y()-scroll.ViewportSize.Y()+scroll.Position.Y() < 24
					selection := &s.model.actions.ui.selection
					if selection.surface == s && selection.region == previous.id && (selection.dragging || selection.anchor != selection.caret) {
						state.end = false
					}
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
	if d := s.document; d != nil {
		return d.selection == s && d.definition != nil && s.model.customDocuments[s.model.activeFile()] == d &&
			s.model.fileEditor(d.file) == d.provider && !s.model.sourceState().visible() && s.model.actions.ui.editor == nil
	}
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
	return htmlui.DefineScoped(htmlui.NewTemplate(`<section data-ref="surface"><header data-ref="header" class="pencode-extension-header"><img data-ref="severity-icon" class="pencode-notification-icon" alt=""><strong data-ref="title"></strong><button data-ref="close" class="pencode-icon-button" type="button" title="关闭" aria-label="关闭"><img data-ref="close-icon" class="pencode-close-icon" alt=""></button></header><div data-ref="content" class="pencode-extension-content"></div></section>`), "", htmlui.Behavior[pencodeExtensionSurfaceProps, struct{}]{
		Render: func(c htmlui.RenderContext[pencodeExtensionSurfaceProps, struct{}], view *htmlui.View) {
			s := c.Props.Surface
			view.Attr("surface", "id", s.rootID)
			view.Attr("content", "id", s.contentID)
			if s.fill && (s.ui.Kind == peb.EditorTab || s.ui.Kind == peb.Panel) {
				view.Attr("content", "class", "pencode-extension-content pencode-extension-fill")
			}
			view.Attr("surface", "aria-label", s.ui.Title)
			view.Attr("close", "id", s.closeID)
			view.Attr("close-icon", "src", s.model.themedIcon("icons/close.svg", "icon.foreground"))
			view.Text("title", s.ui.Title)
			view.Event("close", htmlui.EventClick)
			view.Event("close", htmlui.EventFocus)
			role, class := "region", "pencode-extension-panel"
			if s.ui.Kind != peb.Notification {
				view.Attr("severity-icon", "hidden", "")
			}
			switch s.ui.Kind {
			case peb.Dialog:
				role, class = "dialog", "pencode-extension-dialog"
				view.Attr("surface", "aria-modal", "true")
			case peb.ContextMenu:
				role, class = "menu", "pencode-extension-menu"
				view.Attr("header", "hidden", "")
			case peb.Popup:
				role, class = "dialog", "pencode-extension-popup"
			case peb.Panel:
				if s.fill {
					class += " pencode-extension-panel-fill"
					view.Attr("header", "hidden", "")
				}
			case peb.Notification:
				role, class = "status", "pencode-extension-notification pencode-notification-info"
				icon, foreground, label, live := "icons/info.svg", "notificationsInfoIcon.foreground", "信息", "polite"
				switch s.ui.Severity {
				case peb.NotificationWarning:
					class = "pencode-extension-notification pencode-notification-warning"
					icon, foreground, label = "icons/warning.svg", "notificationsWarningIcon.foreground", "警告"
				case peb.NotificationError:
					role, class = "alert", "pencode-extension-notification pencode-notification-error"
					icon, foreground, label, live = "icons/error.svg", "notificationsErrorIcon.foreground", "错误", "assertive"
				}
				view.Attr("surface", "aria-live", live)
				view.Attr("severity-icon", "src", s.model.themedIcon(icon, foreground))
				view.Attr("severity-icon", "alt", label)
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
			view.AppendComponentChild("content", 0, s.styles, *c.Props, struct{}{})
			view.AppendComponentChild("content", 1, s.definition, *c.Props, struct{}{})
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

// Content publications update the mounted wrapper directly, retaining the
// explorer, source editor and other extension tabs outside this surface.
func (model *workspaceModel) refreshExtensionSurface(s *pencodeExtensionSurface) bool {
	world := model.fontResources
	if world == nil {
		return false
	}
	for entity, access := range world.Query[htmlui.Component[tabProps, struct{}]]() {
		props := access.Ref().Props
		if props.Surface == s && (props.Name != s.ui.Title || props.Busy != s.ui.Busy || props.Unread != s.ui.Unread) {
			props.Name, props.Busy, props.Unread = s.ui.Title, s.ui.Busy, s.ui.Unread
			world.Mut[htmlui.Component[tabProps, struct{}]](entity).Props = props
			world.UpdateState[tabProps, struct{}](entity, func(*struct{}) {})
		}
	}
	for entity, access := range world.Query[htmlui.Component[pencodeExtensionSurfaceProps, struct{}]]() {
		component := access.Get()
		if component.Definition != model.actions.ui.wrapper || component.Props.Surface != s {
			continue
		}
		world.Mut[htmlui.Component[pencodeExtensionSurfaceProps, struct{}]](entity).Props = pencodeExtensionSurfaceProps{
			Surface: s, Theme: model.workbenchTheme, Revision: s.revision, Visible: s.live(),
		}
		world.UpdateState[pencodeExtensionSurfaceProps, struct{}](entity, func(*struct{}) {})
		if model.app != nil {
			model.app.RequestRender()
		}
		return true
	}
	return false
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
		view.AppendComponentChild(slot, componentKey(s.rootID), state.wrapper, pencodeExtensionSurfaceProps{Surface: s, Theme: model.workbenchTheme, Revision: s.revision, Visible: s.live()}, struct{}{})
	}
	setPanelVisible(view, "extension-pane", model.bottom.active == pencodePanelExtension)
	setPanelVisible(view, "extension-editor", state.editor != nil)
}

func (model *workspaceModel) closeExtensionSurface(s *pencodeExtensionSurface, reason string, restore bool) {
	if s.ui.Kind == peb.EditorTab && s.ui.InterceptClose && (reason == "button" || reason == "escape") {
		s.send(peb.EditorEvent{Kind: "closerequest"}, reason)
		return
	}
	state := &model.actions.ui
	wasLive := s.live()
	if restore && (s.ui.Kind == peb.Popup || s.ui.Kind == peb.Notification) && model.fontResources != nil {
		restore = s.ownsFocus(model.fontResources)
	}
	if state.editor == s {
		model.selectEditorTabNeighbor(pencodeEditorTab{surface: s})
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
	s.images = model.replaceExtensionImages(s.images, nil)
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
		s.images = model.replaceExtensionImages(s.images, nil)
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
				if scroll := world.Mut[scene.UIScroll](area); scroll != nil {
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
		if s.syncInputLinks(world) {
			scrollChanged = true
			// Complete newly mounted native controls before autosizing and
			// restoring focus/caret, all within the publication's render frame.
			if err := world.SyncUI(); err != nil {
				model.setActiveStatus(err.Error())
				return
			}
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
					if scroll := world.Mut[scene.UIScroll](area); scroll != nil {
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
				if field := world.Mut[engineui.TextField](control); field != nil {
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
		for i := range s.focus {
			f := &s.focus[i]
			if !f.placeCaret {
				continue
			}
			if control, ok := world.HTMLControl(model.surface, f.id); ok {
				if field := world.Mut[engineui.TextField](control); field != nil {
					// The suffix is an extension-owned revision, so two edits can
					// request the same caret without resetting it on every render.
					position, _, _ := strings.Cut(f.caret, ":")
					if caret, err := strconv.Atoi(position); err == nil {
						caret = max(0, min(caret, field.Text().Runes()))
						field.Anchor, field.Caret = caret, caret
						if state.overlay == nil || state.overlay == s {
							world.Focus(control)
						}
					}
					f.placeCaret = false
				}
			}
		}
		scrollChanged = s.syncInputLinks(world) || scrollChanged
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
					if rect, ok := model.extensionAnchorBounds(world, s.invocation.anchor); ok {
						x, y = rect.X-viewport.X, rect.Y+rect.Height-viewport.Y
						if placement.Mode == peb.PositionAnchorAbove && rect.Y-viewport.Y-bounds.Height+placement.Y >= 4 {
							y = rect.Y - viewport.Y - bounds.Height
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
			if f.submit != "" || f.completion || f.pasteImages || f.inputLinks != nil || s.ui.Kind != peb.Panel && s.ui.Kind != peb.EditorTab {
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
		// Reconcile paint after scroll or inline text changes before finalizing
		// native controls and scroll geometry in LayoutSystem.
		if err := world.SyncHTML(); err != nil {
			model.setActiveStatus(err.Error())
			return
		}
		if err := world.SyncUI(); err != nil {
			model.setActiveStatus(err.Error())
			return
		}
		for _, s := range state.surfaces {
			if s.live() {
				// Apply link paint to the final native parts after reconciliation.
				s.syncInputLinks(world)
			}
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
		visual, ok := world.FindPart[engineui.ValuePart, scene.Text2D](control)
		if !ok {
			continue
		}
		text := world.Mut[scene.Text2D](visual)
		link, ok := world.Get[htmlui.ControlText](visual)
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
		if model.bottom.active == pencodePanelExtension && model.actions.ui.panel != nil && model.uiKey(state, input.KeyEscape) && !state.Composing() {
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
	if model.uiKey(state, input.KeyEscape) && !state.Composing() {
		model.closeExtensionSurface(s, "escape", true)
		return true, true
	}
	if !nonmodal && (model.uiKey(state, input.KeyTab) || s.ui.Kind == peb.ContextMenu && (model.uiKey(state, input.KeyDown) || model.uiKey(state, input.KeyUp))) {
		count := len(s.focus)
		if s.ui.Kind == peb.Dialog {
			count++
		}
		if count > 0 {
			step := 1
			if model.uiKey(state, input.KeyUp) || model.commandMatches("ui-focus-previous", state, runtime.GOOS) {
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
	target, ok := world.Get[htmlui.Focusable](world.FocusedControl())
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
	for entity, access := range world.Query[htmlui.Focusable]() {
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
	if control, ok := s.model.extensionFocusControl(world, id); ok && !world.Has[engineui.TextFieldKeyHandler](control) {
		_ = world.Insert(control, engineui.TextFieldKeyHandler{Handle: s.interceptKeys})
	}
}
func (s *pencodeExtensionSurface) interceptKeys(state *input.State) bool {
	if !s.live() || state.Composing() {
		return false
	}
	if s.interceptImagePaste(state, readClipboardImage) {
		return true
	}
	if s.interceptCompletion(state) {
		return true
	}
	if s.interceptInputLinkKeys(state) {
		return true
	}
	if s.model.commandMatches("extension-submit", state, runtime.GOOS) && !state.KeyPressedDuringComposition(input.KeyEnter) {
		world := s.model.fontResources
		control := world.FocusedControl()
		for _, f := range s.focus {
			if f.submit == "" {
				continue
			}
			if target, ok := world.HTMLControl(s.model.surface, f.id); !ok || target != control {
				continue
			}
			field := world.Mut[engineui.TextField](control)
			if native, ok := world.Get[htmlui.Control](control); field == nil || !ok || native.ReadOnly || native.Disabled {
				return false
			}
			s.send(s.commitInput(state, control, field, f.ref), "")
			s.send(peb.EditorEvent{Kind: "submit", Ref: f.submit}, "")
			return true
		}
	}
	if s.ui.Kind == peb.Panel || s.ui.Kind == peb.EditorTab {
		return false
	}
	return s.model.uiKey(state, input.KeyEscape) || s.ui.Kind != peb.Popup && s.ui.Kind != peb.Notification && s.model.uiKey(state, input.KeyTab) || s.ui.Kind == peb.ContextMenu && (s.model.uiKey(state, input.KeyUp) || s.model.uiKey(state, input.KeyDown))
}
