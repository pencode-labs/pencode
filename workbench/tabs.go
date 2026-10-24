package workbench

import (
	"time"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
)

type pencodeTabScroll struct {
	sourceKey                   string
	area                        ecs.Entity
	file                        *openFile
	project                     *pencodeProject
	terminal                    *pencodeTerminal
	viewportWidth, contentWidth float32
}

// Reveal only after selection or geometry changes, so manually scrolling away
// from the active tab keeps its position on subsequent frames.
func (model *workspaceModel) syncTabScroll(world *module.Content) {
	viewportNode, ok := world.HTMLNode(model.surface, "tabs")
	if !ok {
		return
	}
	area, ok := viewportNode.ScrollContainer()
	if !ok {
		model.tabScroll = pencodeTabScroll{}
		return
	}
	scroll := module.ContentMut[scene.UIScroll](world, area)
	if scroll == nil {
		return
	}
	next := pencodeTabScroll{area: area, file: model.activeFile(), project: model.activeProjectState(), viewportWidth: scroll.ViewportSize.X(), contentWidth: scroll.ContentSize.X()}
	s := model.sourceState()
	if s.Active != "" {
		next.sourceKey = s.Active
	}
	if model.tabScroll == next {
		return
	}
	model.tabScroll = next
	id := "active-tab"
	if next.sourceKey != "" {
		id = "sc-tab-group:" + next.sourceKey
	}
	model.revealTab(world, viewportNode, id, scroll)
}

func (model *workspaceModel) revealTab(world *module.Content, viewportNode htmlui.Node, id string, scroll *scene.UIScroll) {
	tabNode, tabOK := world.HTMLNode(model.surface, id)
	if !tabOK {
		return
	}
	viewport, viewportOK := viewportNode.Bounds()
	tab, tabOK := tabNode.Bounds()
	if !viewportOK || !tabOK {
		return
	}
	// Bounds are unscrolled layout coordinates; use the smallest displacement
	// that fits the tab, aligning its start if it exceeds the viewport width.
	left := tab.X - viewport.X
	position := max(scroll.Position.X(), scroll.ViewportSize.X()-left-tab.Width)
	position = min(position, -left)
	position = min(max(position, -max(scroll.ContentSize.X()-scroll.ViewportSize.X(), 0)), 0)
	if position != scroll.Position.X() {
		scroll.Position[0], scroll.Momentum[0] = position, 0
		model.app.RequestRender()
	}
}

// Native horizontal/diagonal wheel input stays with UIScroll. A vertical-only
// mouse wheel over the tab strip moves the horizontal content instead.
func (model *workspaceModel) updateTabWheel(world *module.Content, state *input.State) {
	pointer := state.Pointer()
	if pointer.WheelY == 0 || pointer.WheelX != 0 || model.screen != pencodeWorkspace || model.settingsOpen || model.quickOpen || model.closing.action != pencodeCloseNone || model.fileContext.dialog != "" {
		return
	}
	zoom := max(float32(model.settings.Zoom)/100, .01)
	x, y := pointer.X/zoom, pointer.Y/zoom
	if model.fileContext.open && model.pointerInsideRefs(world, x, y, []string{"file-context"}) ||
		model.languageUI.contextOpen && model.pointerInsideRefs(world, x, y, []string{"lsp-context", "lsp-context-submenu"}) ||
		model.market != nil && model.market.open && model.pointerInsideRefs(world, x, y, []string{"marketplace-panel"}) || model.terminalView.context.open {
		return
	}
	var area ecs.Entity
	if model.pointerInsideRefs(world, x, y, []string{"tabs"}) {
		area = model.tabScroll.area
	} else if model.bottom.active == pencodePanelTerminal && model.pointerInsideRefs(world, x, y, []string{"terminal-tabs"}) {
		area = model.terminalView.tabScroll.area
	} else {
		return
	}
	scroll := module.ContentMut[scene.UIScroll](world, area)
	if scroll == nil || !scroll.Horizontal || scroll.DisableInput {
		return
	}
	position := min(max(scroll.Position.X()+pointer.WheelY*scroll.WheelStep, -max(scroll.ContentSize.X()-scroll.ViewportSize.X(), 0)), 0)
	if position != scroll.Position.X() {
		scroll.Position[0], scroll.Momentum[0] = position, 0
		model.app.RequestRender()
	}
}

func pinProjectFile(project *pencodeProject, relative string) {
	if file := project.Files[relative]; file != nil {
		file.Preview = false
	}
	if project.PreviewPath == relative {
		project.PreviewPath = ""
	}
}

func (model *workspaceModel) fileDoubleClick(component ecs.Entity, now time.Time) bool {
	double := model.lastFileClick == component && !model.lastFileClickAt.IsZero() && now.Sub(model.lastFileClickAt) <= 500*time.Millisecond
	model.lastFileClick, model.lastFileClickAt = component, now
	if double {
		model.lastFileClick = 0
	}
	return double
}

type pencodeCloseAction uint8

const (
	pencodeCloseNone pencodeCloseAction = iota
	pencodeCloseTab
	pencodeCloseProject
	pencodeCloseWindow
)

type pencodeCloseTarget struct {
	project *pencodeProject
	file    *openFile
}

type pencodeCloseRequest struct {
	action     pencodeCloseAction
	project    *pencodeProject
	path       string
	targets    []pencodeCloseTarget
	index      int
	message    string
	error      string
	needsFocus bool
	saveID     uint64
}

// Collect the dirty files once. Project/window closure only happens after all
// decisions, so cancelling a later prompt also preserves earlier discarded edits.
func (model *workspaceModel) beginClose(action pencodeCloseAction, project *pencodeProject, relative string) bool {
	if model.fileCloseBlocked(action, project) {
		model.fileClose = pencodeCloseRequest{action: action, project: project, path: relative}
		model.setActiveStatus("正在等待文件操作完成后关闭…")
		return false
	}
	if action != pencodeCloseTab && model.sourceCloseBlocked(project) {
		return false
	}
	// A pending extension reply may still turn a clean file dirty. Keep it
	// open until the host has received those edits before collecting targets.
	for file, document := range model.customDocuments {
		if !document.busy() {
			continue
		}
		if action == pencodeCloseWindow || document.project == project && (action == pencodeCloseProject || file.Path == relative) {
			model.customClose = pencodeCloseRequest{action: action, project: project, path: relative}
			model.setActiveStatus("正在等待编辑工具完成操作…")
			return false
		}
	}
	request := &model.closing
	if request.action != pencodeCloseNone {
		return false
	}
	request.targets = request.targets[:0]
	if action == pencodeCloseTab {
		files := model.files
		if project != nil {
			files = project.Files
		}
		if file := files[relative]; file != nil && file.Dirty {
			request.targets = append(request.targets, pencodeCloseTarget{project, file})
		}
	} else if action == pencodeCloseProject {
		model.collectCloseTargets(project)
	} else if len(model.projects) > 0 {
		for _, candidate := range model.projects {
			model.collectCloseTargets(candidate)
		}
	} else {
		for _, path := range model.tabs {
			if file := model.files[path]; file != nil && file.Dirty {
				request.targets = append(request.targets, pencodeCloseTarget{file: file})
			}
		}
	}
	if len(request.targets) == 0 {
		return true
	}
	request.action, request.project, request.path = action, project, relative
	request.index = 0
	model.settingsOpen, model.quickOpen = false, false
	model.capturingShortcut, model.quickNeedsFocus, model.findNeedsFocus = false, false, false
	model.quickFocusEditor = false
	model.showCloseTarget()
	return false
}

func (model *workspaceModel) collectCloseTargets(project *pencodeProject) {
	if project == nil {
		return
	}
	for _, relative := range project.Tabs {
		if file := project.Files[relative]; file != nil && file.Dirty {
			model.closing.targets = append(model.closing.targets, pencodeCloseTarget{project, file})
		}
	}
}

func (model *workspaceModel) requestWindowClose() bool {
	return model.beginClose(pencodeCloseWindow, nil, "")
}

func (model *workspaceModel) showCloseTarget() {
	request := &model.closing
	target := request.targets[request.index]
	name := target.file.Path
	if target.project != nil {
		name = target.project.Name + "/" + name
	}
	request.message = "是否保存对 “" + name + "” 的更改？"
	request.error = ""
	request.needsFocus = true
}

func (model *workspaceModel) clearCloseRequest() {
	targets := model.closing.targets
	clear(targets)
	model.closing = pencodeCloseRequest{targets: targets[:0]}
	model.quickFocusEditor = true
}

func (model *workspaceModel) resolveClose(save bool) {
	request := &model.closing
	if request.action == pencodeCloseNone || request.saveID != 0 {
		return
	}
	if save {
		target := request.targets[request.index]
		job, err := model.requestFileSave(target.project, target.file)
		if err != nil {
			request.error = err.Error()
			return
		}
		request.saveID = job.id
		request.error = "正在保存…"
		return
	}

	request.index++
	if request.index < len(request.targets) {
		model.showCloseTarget()
		return
	}
	action, project, relative := request.action, request.project, request.path
	model.clearCloseRequest()
	switch action {
	case pencodeCloseTab:
		if project != nil {
			model.discardProjectTab(project, relative)
		} else {
			model.discardTab(relative)
		}
	case pencodeCloseProject:
		model.discardProject(project)
	case pencodeCloseWindow:
		if model.app != nil && model.app.Context != nil {
			model.app.RequestClose()
		}
	}
}

func (model *workspaceModel) handleCloseDialogEvent(world *module.Content, ref string) {
	switch ref {
	case "close-save":
		model.resolveClose(true)
	case "close-discard":
		model.resolveClose(false)
	case "close-cancel":
		model.clearCloseRequest()
	default:
		return
	}
	setPencodeEditorValue(world, model.surface, model.activeContent())
}

func (model *workspaceModel) renderCloseDialog(view *htmlui.View) {
	view.Event("close-save", htmlui.EventClick)
	view.Event("close-discard", htmlui.EventClick)
	view.Event("close-cancel", htmlui.EventClick)
	if model.closing.action == pencodeCloseNone {
		view.Attr("close-dialog", "hidden", "")
		view.RemoveAttr("content", "disabled")
	} else {
		view.RemoveAttr("close-dialog", "hidden")
		view.Attr("content", "disabled", "")
	}
	for _, id := range [...]string{"close-save", "close-discard"} {
		if model.closing.saveID != 0 {
			view.Attr(id, "disabled", "")
		} else {
			view.RemoveAttr(id, "disabled")
		}
	}
	view.Text("close-message", model.closing.message)
	view.Text("close-error", model.closing.error)
	if model.closing.error == "" {
		view.Attr("close-error", "hidden", "")
	} else {
		view.RemoveAttr("close-error", "hidden")
	}
}

func (model *workspaceModel) focusCloseDialog(world *module.Content, surface ecs.Entity) {
	if !model.closing.needsFocus {
		return
	}
	for entity, access := range module.ContentQuery[htmlui.Focusable](world) {
		focusable := access.Get()
		if focusable.Owner == surface && focusable.ID == "close-save" && world.Focus(entity) {
			model.closing.needsFocus = false
			return
		}
	}
}
