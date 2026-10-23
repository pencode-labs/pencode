package workbench

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

type pencodeFileContext struct {
	project                           *pencodeProject
	path                              string
	directory                         bool
	depth, index                      int
	x, y                              float32
	open, needsFocus                  bool
	dialog, name, title, message, err string
}

type pencodeFileClipboard struct {
	project *pencodeProject
	path    string
	cut     bool
}

var pencodeFileContextRefs = [...]string{
	"file-context-open", "file-context-new-file", "file-context-new-folder",
	"file-context-cut", "file-context-copy", "file-context-paste",
	"file-context-copy-path", "file-context-copy-relative", "file-context-rename", "file-context-delete", "file-context-refresh",
}

func (model *workspaceModel) renderFileContext(view *htmlui.View) {
	c := &model.fileContext
	if c.open {
		view.RemoveAttr("file-context", "hidden")
		view.Attr("file-context", "style", languagePopupStyle(c.x, c.y))
		label := "打开"
		if c.directory {
			label = "展开 / 折叠"
		}
		view.Text("file-context-open", label)
		for i, ref := range pencodeFileContextRefs {
			view.Event(ref, htmlui.EventClick)
			class := ""
			if i == c.index {
				class = "active"
			}
			view.Attr(ref, "class", class)
			setContextMenuEnabled(view, ref, model.fileContextEnabled(ref))
		}
	} else {
		view.Attr("file-context", "hidden", "")
	}
	if c.dialog == "" {
		view.Attr("file-dialog", "hidden", "")
		return
	}
	view.RemoveAttr("file-dialog", "hidden")
	view.Attr("content", "disabled", "")
	view.Text("file-dialog-title", c.title)
	view.Text("file-dialog-message", c.message)
	view.Text("file-dialog-error", c.err)
	view.Attr("file-dialog-name", "value", c.name)
	if c.dialog == "delete" {
		view.Attr("file-dialog-name", "hidden", "")
		view.Text("file-dialog-submit", "删除")
	} else {
		view.RemoveAttr("file-dialog-name", "hidden")
		view.Text("file-dialog-submit", "确定")
	}
	if c.err == "" {
		view.Attr("file-dialog-error", "hidden", "")
	} else {
		view.RemoveAttr("file-dialog-error", "hidden")
	}
	view.Event("file-dialog-name", htmlui.EventInput)
	view.Event("file-dialog-submit", htmlui.EventClick)
	view.Event("file-dialog-cancel", htmlui.EventClick)
}

func (model *workspaceModel) fileContextEnabled(ref string) bool {
	c := &model.fileContext
	switch ref {
	case "file-context-open":
		return c.path != "" && (c.directory || model.canOpenFile(c.path))
	case "file-context-rename", "file-context-delete", "file-context-cut", "file-context-copy":
		return c.path != ""
	case "file-context-paste":
		return model.fileClipboard.project != nil && model.fileClipboard.path != ""
	}
	return true
}

func (model *workspaceModel) updateFileContext(world *module.Content, state *input.State) bool {
	c := &model.fileContext
	if model.settingsOpen || model.quickOpen || model.sidebarHidden || model.closing.action != pencodeCloseNone || model.market != nil && model.market.open {
		changed := c.open || c.dialog != ""
		*c = pencodeFileContext{}
		return changed
	}
	if c.project != nil && model.projectIndex(c.project.Path) < 0 {
		*c = pencodeFileContext{}
		return true
	}
	if c.dialog != "" {
		return false
	}
	pointer := state.Pointer()
	zoom := max(float32(model.settings.Zoom)/100, .01)
	x, y := pointer.X/zoom, pointer.Y/zoom
	if c.open && model.pointerInsideRefs(world, x, y, []string{"file-context"}) {
		for i, ref := range model.fileContextRefs() {
			if i != c.index && model.pointerInsideRefs(world, x, y, []string{ref}) && (pointer.DeltaX != 0 || pointer.DeltaY != 0) {
				c.index = i
				return true
			}
		}
		return false
	}
	changed := false
	if c.open && (state.MousePressed(input.MouseLeft) || state.MousePressed(input.MouseRight)) {
		c.open = false
		changed = true
	}
	if !state.MousePressed(input.MouseRight) || !model.pointerInsideRefs(world, x, y, []string{"sidebar"}) {
		return changed
	}
	project, relative, directory, depth := model.fileContextTarget(world, x, y)
	if project == nil {
		return changed
	}
	root, ok := world.HTMLNode(model.surface, "pencode-root")
	if !ok {
		return changed
	}
	bounds, _ := root.Bounds()
	*c = pencodeFileContext{project: project, path: relative, directory: directory, depth: depth, open: true,
		x: max(4, min(x-bounds.X, bounds.Width-244)), y: max(4, min(y-bounds.Y, bounds.Height-367))}
	model.lastFileClick = 0
	return true
}

// HTML bounds omit runtime scroll offsets. Clip the translated row against its
// own tree viewport before choosing it, including in multi-project workspaces.
func (model *workspaceModel) fileContextTarget(world *module.Content, x, y float32) (*pencodeProject, string, bool, int) {
	if project, relative, directory, depth := model.fileTreeTarget(world, x, y); project != nil {
		return project, relative, directory, depth
	}
	return model.activeProjectState(), "", true, 0
}

func (model *workspaceModel) fileTreeTarget(world *module.Content, x, y float32) (*pencodeProject, string, bool, int) {
	for _, access := range module.ContentQuery[htmlui.Component[treeProps, struct{}]](world) {
		props := access.Get().Props
		id := props.RowID
		node, ok := world.HTMLNode(model.surface, id)
		if !ok {
			continue
		}
		row, ok := node.Bounds()
		if !ok {
			continue
		}
		if entity, ok := pencodeTreeScrollArea(world, model.surface, id); ok {
			if scroll, ok := module.ContentGet[scene.UIScroll](world, entity); ok {
				row.X += scroll.Position.X()
				row.Y += scroll.Position.Y()
			}
		}
		if languageRectContains(row, x, y) && model.pointerInsideRefs(world, x, y, []string{props.TreeID}) {
			return props.Project, props.Path, props.IsDir, props.Depth
		}
	}
	for _, access := range module.ContentQuery[htmlui.Component[projectProps, struct{}]](world) {
		props := access.Get().Props
		if props.Model == model && model.pointerInsideRefs(world, x, y, []string{props.TreeID, props.SummaryID}) {
			return props.Project, "", true, 0
		}
	}
	return nil, "", false, 0
}

func (model *workspaceModel) constrainFileContext(world *module.Content) {
	c := &model.fileContext
	if !c.open {
		return
	}
	menu, menuOK := world.HTMLNode(model.surface, "file-context")
	root, rootOK := world.HTMLNode(model.surface, "pencode-root")
	if !menuOK || !rootOK {
		return
	}
	bounds, _ := menu.Bounds()
	viewport, _ := root.Bounds()
	x, y := max(4, min(c.x, viewport.Width-bounds.Width-4)), max(4, min(c.y, viewport.Height-bounds.Height-4))
	if c.x != x || c.y != y {
		c.x, c.y = x, y
		markPencodeRootDirtyByModel(world, model)
	}
}

func (model *workspaceModel) focusFileContext(world *module.Content) {
	c := &model.fileContext
	if !c.needsFocus || c.dialog == "" {
		return
	}
	ref := "file-dialog-name"
	if c.dialog == "delete" {
		ref = "file-dialog-cancel"
	}
	if control, ok := world.HTMLControl(model.surface, ref); ok {
		if c.dialog != "delete" {
			if _, ready := module.ContentGet[engineui.TextField](world, control); !ready {
				if model.app != nil && model.app.WakeAt != nil {
					model.app.WakeAt(time.Now())
				}
				return
			}
		}
		if !world.Focus(control) {
			return
		}
		c.needsFocus = false
		if field := module.ContentMut[engineui.TextField](world, control); field != nil {
			field.Anchor = 0
		}
	}
}

func (model *workspaceModel) handleFileContextKeys(world *module.Content, state *input.State) bool {
	c := &model.fileContext
	if state.KeyPressed(input.KeyEscape) {
		*c = pencodeFileContext{}
		return true
	}
	if c.dialog != "" {
		if c.dialog != "delete" && state.KeyPressed(input.KeyEnter) {
			model.submitFileDialog(world)
			return true
		}
		return false
	}
	switch {
	case state.KeyPressed(input.KeyDown):
		c.index = (c.index + 1) % len(model.fileContextRefs())
	case state.KeyPressed(input.KeyUp):
		c.index = (c.index + len(model.fileContextRefs()) - 1) % len(model.fileContextRefs())
	case state.KeyPressed(input.KeyEnter), state.KeyPressed(input.KeySpace):
		model.handleFileContextEvent(world, model.fileContextRefs()[c.index], "")
	default:
		return false
	}
	return true
}

func (model *workspaceModel) handleFileContextEvent(world *module.Content, ref, value string) bool {
	if model.handleActionRef(world, ref) {
		return true
	}
	c := &model.fileContext
	if c.dialog != "" {
		switch ref {
		case "file-dialog-name":
			c.name, c.err = value, ""
		case "file-dialog-submit":
			model.submitFileDialog(world)
		case "file-dialog-cancel":
			*c = pencodeFileContext{}
		}
		return true
	}
	if !strings.HasPrefix(ref, "file-context-") {
		return false
	}
	if !c.open || !model.fileContextEnabled(ref) {
		return true
	}
	c.open = false
	project, relative := c.project, c.path
	switch action := strings.TrimPrefix(ref, "file-context-"); action {
	case "new-file", "new-folder", "rename", "delete":
		c.dialog, c.needsFocus, c.err, c.name = action, true, "", ""
		switch action {
		case "new-file":
			c.title = "新建文件"
		case "new-folder":
			c.title = "新建文件夹"
		case "rename":
			c.title = "重命名"
		case "delete":
			c.title = "删除"
		}
		c.message = "位置：" + filepath.Join(project.Path, filepath.FromSlash(model.fileContextDirectory()))
		if action == "rename" {
			c.name = path.Base(relative)
		}
		if action == "delete" {
			c.message = fmt.Sprintf("确定永久删除“%s”及其内容？未保存的更改也会丢失。", relative)
		}
	case "open":
		model.activateProject(project)
		if c.directory {
			model.toggleProjectDirectory(project, relative, c.depth)
		} else {
			model.openProjectFile(project, relative)
			model.syncActiveProjectFields()
			setPencodeEditorValue(world, model.surface, model.activeContent())
		}
	case "copy-path", "copy-relative":
		text := relative
		if action == "copy-path" {
			text = filepath.Join(project.Path, filepath.FromSlash(relative))
		} else if text == "" {
			text = "."
		}
		state, _ := module.ContentResource[*input.State](world)
		if !state.SetClipboardText(text) {
			model.setProjectStatus(project, "无法写入剪贴板")
		}
	case "cut", "copy":
		model.fileClipboard = pencodeFileClipboard{project, relative, action == "cut"}
	case "paste":
		if err := model.pasteProjectResource(project, model.fileContextDirectory()); err != nil {
			model.setProjectStatus(project, err.Error())
		}
		setPencodeEditorValue(world, model.surface, model.activeContent())
	case "refresh":
		model.startProjectScan(project, false)
	}
	return true
}

func (model *workspaceModel) fileContextDirectory() string {
	c := &model.fileContext
	if c.directory {
		return c.path
	}
	return path.Dir(c.path)
}

func (model *workspaceModel) submitFileDialog(world *module.Content) {
	c := &model.fileContext
	if err := model.applyFileDialog(); err != nil {
		c.err = err.Error()
		return
	}
	*c = pencodeFileContext{}
	setPencodeEditorValue(world, model.surface, model.activeContent())
}
