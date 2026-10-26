package workbench

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/diiyw/onix/module"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/actions"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

type pencodeActionState struct {
	notifications    []pencodeNotification
	notificationView *htmlui.Definition[pencodeNotificationProps, struct{}]
	ui               pencodeExtensionUIState
	local            *actions.Registry
	revision         uint64
	snapshot         []peb.Action
	rows             []pencodeActionProps
	theme            *pencodeTheme
	owner            *workspaceModel
	file             *openFile
	fileName         string
	openWithCount    int
	// Context references include the existing built-in items and visible actions.
	leftRefs, rightRefs []string
	sessions            map[string]*extensions.ActionSession
}
type pencodeActionProps struct {
	Model            *workspaceModel
	Action           peb.Action
	Ref, Icon, Style string
	Active           bool
}

var pencodeActionSlots = [...]string{"actions-statusbar", "actions-left-context", "actions-right-context", "actions-titlebar", "actions-open-with"}

func (model *workspaceModel) actionRegistry() *actions.Registry {
	if model.session != nil {
		return &model.session.Actions
	}
	if model.actions.local == nil {
		model.actions.local = &actions.Registry{}
	}
	return model.actions.local
}
func (model *workspaceModel) syncActions() bool {
	state := &model.actions
	file := model.activeFile()
	name := ""
	if file != nil {
		name = file.Name
	}
	previous := state.revision
	state.snapshot, state.revision = model.actionRegistry().Snapshot(state.snapshot, state.revision)
	if previous == state.revision && state.theme == model.workbenchTheme && state.owner == model && state.file == file && state.fileName == name {
		return false
	}
	state.theme, state.owner, state.file, state.fileName = model.workbenchTheme, model, file, name
	state.openWithCount = 0
	clear(state.rows)
	state.rows = state.rows[:0]
	state.leftRefs = append(state.leftRefs[:0], pencodeFileContextRefs[:]...)
	state.rightRefs = append(state.rightRefs[:0], pencodeEditorContextRefs[:]...)
	for _, a := range state.snapshot {
		if a.Hidden {
			continue
		}
		if a.Location == peb.OpenWith {
			if state.file == nil || editorMatch(&a, state.file.Name) == 0 {
				continue
			}
			state.openWithCount++
			if a.Icon == "" {
				a.Icon = "icons/file-text.svg"
			}
		}
		ref := "action-" + a.ID
		icon := a.Icon
		if icon != "" {
			if a.IconColor != "" {
				icon += "?foreground=" + a.IconColor[1:]
			} else if model.workbenchTheme != nil {
				suffix := strings.TrimPrefix(model.themedIcon("icons/file-text.svg", "icon.foreground"), "icons/file-text.svg")
				icon += suffix
			}
		}
		style := ""
		if a.Color != "" {
			style = "color:" + a.Color
		}
		state.rows = append(state.rows, pencodeActionProps{Model: model, Action: a, Ref: ref, Icon: icon, Style: style})
		if a.Location == peb.LeftContextMenu {
			state.leftRefs = append(state.leftRefs, ref)
		}
		if a.Location == peb.RightContextMenu {
			state.rightRefs = append(state.rightRefs, ref)
		}
	}
	// A live removal or relocation must never leave a menu index out of range.
	model.fileContext.index = min(model.fileContext.index, max(0, len(state.leftRefs)-1))
	model.languageUI.contextIndex = min(model.languageUI.contextIndex, max(0, len(state.rightRefs)-1))
	return true
}
func (model *workspaceModel) fileContextRefs() []string {
	if model.actions.leftRefs != nil {
		return model.actions.leftRefs
	}
	return pencodeFileContextRefs[:]
}
func (model *workspaceModel) editorContextRefs() []string {
	if model.actions.rightRefs != nil {
		return model.actions.rightRefs
	}
	return pencodeEditorContextRefs[:]
}
func (model *workspaceModel) renderActions(view *htmlui.View, definition *htmlui.Definition[pencodeActionProps, struct{}]) {
	model.syncActions()
	selected := model.fileEditor(model.activeFile())
	left, right := len(pencodeFileContextRefs), len(pencodeEditorContextRefs)
	for _, props := range model.actions.rows {
		switch props.Action.Location {
		case peb.OpenWith:
			if model.sourceState().visible() {
				continue
			}
			props.Active = selected != nil && selected.ID == props.Action.ID
		case peb.LeftContextMenu:
			props.Active = model.fileContext.index == left
			left++
		case peb.RightContextMenu:
			props.Active = model.languageUI.contextGroup == 0 && model.languageUI.contextIndex == right
			right++
		}
		htmlui.AppendComponentChild(view, pencodeActionSlots[props.Action.Location], componentKey(props.Action.ID), definition, props, struct{}{})
	}
}
func renderPencodeAction(context module.ContentRenderContext[pencodeActionProps, struct{}], view *htmlui.View) {
	props := context.Props
	a := props.Action
	view.Event("run", htmlui.EventClick)
	view.Attr("run", "id", props.Ref)
	label := a.Text
	if a.Tooltip != "" {
		label = a.Tooltip
	}
	view.Attr("run", "aria-label", label)
	view.Attr("run", "title", label)
	class := "pencode-action"
	if props.Active {
		class += " active"
	}
	view.Attr("run", "class", class)
	view.Attr("run", "style", props.Style)
	if a.Location == peb.LeftContextMenu || a.Location == peb.RightContextMenu {
		view.Attr("run", "role", "menuitem")
		view.Attr("run", "tabindex", "-1")
	}
	if a.Disabled {
		view.Attr("run", "disabled", "")
	} else {
		view.RemoveAttr("run", "disabled")
	}
	if a.Location == peb.OpenWith {
		view.Attr("run", "aria-pressed", strconv.FormatBool(props.Active))
		view.Attr("text", "hidden", "")
	} else {
		view.RemoveAttr("run", "aria-pressed")
		view.RemoveAttr("text", "hidden")
		view.Text("text", a.Text)
	}
	if a.Icon == "" {
		view.Attr("icon", "hidden", "")
	} else {
		view.RemoveAttr("icon", "hidden")
		view.Attr("icon", "src", props.Icon)
		view.Attr("icon", "style", a.IconStyle)
	}
}
func handlePencodeAction(context *module.ContentEventContext[pencodeActionProps, struct{}]) {
	if context.Ref != "run" {
		return
	}
	model := context.Props.Model
	model.runAction(context.World, context.Props.Action.ID)
	markPencodeRootDirtyByModel(context.World, model)
}
func (model *workspaceModel) runAction(world *module.Content, id string) {
	var a *peb.Action
	for i := range model.actions.snapshot {
		if model.actions.snapshot[i].ID == id {
			a = &model.actions.snapshot[i]
			break
		}
	}
	if overlay := model.actions.ui.overlay; overlay != nil && overlay.ui.Kind == peb.Dialog {
		return
	}
	if a == nil || a.Disabled || a.Hidden || model.closing.action != pencodeCloseNone || model.fileContext.dialog != "" {
		return
	}
	c := peb.ActionContext{ID: id, ProjectPath: model.projectPath}
	if model.activePath != "" {
		c.Path = path.Join(strings.ReplaceAll(model.projectPath, "\\", "/"), model.activePath)
	}
	if world != nil {
		if field, ok := module.ContentGet[engineui.TextField](world, model.editor.control); ok {
			c.Caret, c.Anchor = uint32(max(0, field.Caret)), uint32(max(0, field.Anchor))
		}
	}
	switch a.Location {
	case peb.LeftContextMenu:
		if !model.fileContext.open || model.fileContext.project == nil {
			return
		}
		c.ProjectPath = model.fileContext.project.Path
		c.Path = path.Join(strings.ReplaceAll(c.ProjectPath, "\\", "/"), model.fileContext.path)
		c.Directory = model.fileContext.directory
		model.fileContext.open = false
	case peb.RightContextMenu:
		ui := &model.languageUI
		if !ui.contextOpen || ui.contextFile != model.activeFile() || ui.contextContent != model.activeContent() {
			return
		}
		c.Caret, c.Anchor = uint32(max(0, ui.contextCaret)), uint32(max(0, ui.contextAnchor))
		model.dismissLanguageContext()
	}
	if a.Location == peb.OpenWith {
		model.selectEditor(id)
		return
	}
	model.captureExtensionAction(world, c)
	if err := model.actionRegistry().Run(c); err != nil {
		model.extensionFeedback = err.Error()
	}
}
func (model *workspaceModel) handleActionRef(world *module.Content, ref string) bool {
	if !strings.HasPrefix(ref, "action-") {
		return false
	}
	model.runAction(world, strings.TrimPrefix(ref, "action-"))
	return true
}
func (model *workspaceModel) importActions(extension *extensions.ActionExtension) {
	if extension == nil {
		return
	}
	state := &model.actions
	if state.sessions == nil {
		state.sessions = make(map[string]*extensions.ActionSession)
	}
	if state.sessions[extension.ID] != nil {
		model.removeActions(extension.ID)
	}
	prefix := "extensions/" + extension.ID + "/"
	extension.Output = model.outputStore().Channel("Extension: " + extension.ID)
	session := extension.Start(model.backgroundWake())
	state.sessions[extension.ID] = session
	for _, a := range extension.Actions {
		model.registerExtensionAction(a, session, prefix)
	}
}
func (model *workspaceModel) registerExtensionAction(a peb.Action, session *extensions.ActionSession, prefix string) {
	id := a.ID
	err := model.actionRegistry().Register(actions.Definition{Action: a, Run: func(c peb.ActionContext) error {
		c.ID = strings.TrimPrefix(c.ID, prefix)
		if !session.Submit(c) {
			return fmt.Errorf("扩展 action 暂时无法执行：%s", id)
		}
		return nil
	}})
	if err != nil {
		model.extensionFeedback = err.Error()
	}
}
func (model *workspaceModel) removeActions(id string) {
	model.clearNotifications(id)
	model.removeEditors("extensions/" + id + "/")
	model.clearExtensionUI(id)
	if session := model.actions.sessions[id]; session != nil {
		session.Close()
		delete(model.actions.sessions, id)
	}
	model.actionRegistry().RemovePrefix("extensions/" + id + "/")
}
func (model *workspaceModel) closeActions() {
	model.clearNotifications("")
	model.clearExtensionUI("")
	for _, session := range model.actions.sessions {
		session.Close()
	}
	model.actionRegistry().RemovePrefix("extensions/")
	clear(model.actions.sessions)
	model.actionRegistry().SetWake(nil)
}
func (model *workspaceModel) pollActions() bool {
	changed := false
	for id, session := range model.actions.sessions {
		prefix := "extensions/" + id + "/"
		for range 16 {
			select {
			case result, ok := <-session.Results:
				if !ok {
					model.removeActions(id)
					changed = true
					goto nextSession
				}
				changed = true
				if result.Notification != nil {
					model.showNotification(id, session, *result.Notification)
					continue
				}
				if result.Err != nil {
					model.extensionFeedback = fmt.Sprintf("扩展 %s action：%v", id, result.Err)
					model.logOutput("Extension: "+id, model.extensionFeedback)
					if result.Fatal {
						model.removeActions(id)
						goto nextSession
					}
					continue
				}
				for source, data := range result.Icons {
					if model.extensionIcons == nil {
						model.extensionIcons = make(map[string][]byte)
					}
					model.extensionIcons[source] = data
				}
				if result.UI != nil {
					if err := model.applyExtensionUI(id, session, *result.UI); err != nil {
						model.extensionFeedback = fmt.Sprintf("扩展 %s UI：%v", id, err)
						model.logOutput("Extension: "+id, model.extensionFeedback)
					}
					for source, data := range result.Assets {
						if model.extensionImages == nil {
							model.extensionImages = make(map[string][]byte)
						}
						model.extensionImages[source] = data
					}
					continue
				}
				change := result.Change
				// A live description may change its location or associations.
				if change.Remove {
					model.removeEditors(change.Action.ID)
					model.actionRegistry().Remove(change.Action.ID)
				} else if change.Action.Location == peb.OpenWith {
					if !model.updateEditorAction(change.Action) {
						model.actionRegistry().Remove(change.Action.ID)
						model.importEditors([]extensions.Editor{session.Editor(change.Action)})
					}
				} else {
					model.removeEditors(change.Action.ID)
					if err := model.actionRegistry().Update(change.Action); err != nil {
						model.registerExtensionAction(change.Action, session, prefix)
					}
				}
			default:
				goto nextSession
			}
		}
		if len(session.Results) > 0 {
			if wake := model.backgroundWake(); wake != nil {
				wake()
			}
		}
	nextSession:
	}
	return model.syncActions() || changed
}
