package workbench

import (
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/internal/clipboard"
)

type pencodeFileContext struct {
	project                           *pencodeProject
	transfer                          *fileIOJob
	dialog, name, title, message, err string
	path                              string
	paths                             []string
	depth, index                      int
	x, y                              float32
	pending                           bool
	open, needsFocus                  bool
	directory                         bool
	externalFiles                     bool
}

type pencodeFileClipboard struct {
	project *pencodeProject
	paths   []string
	cut     bool
}

func (c *pencodeFileContext) inline() bool {
	return c.creating() || c.dialog == "rename"
}

func (c *pencodeFileContext) creating() bool {
	return c.dialog == "new-file" || c.dialog == "new-folder"
}

func (model *workspaceModel) renderFileName(view *htmlui.View, props treeProps) {
	c := &model.fileContext
	label, padding := "新建文件", 24
	switch c.dialog {
	case "rename":
		label, padding = "重命名", 4
		view.Attr("edit-row", "id", "file-tree-rename")
		view.Attr("edit-row", "class", treeRowClass(&props))
		view.RemoveAttr("edit-arrow", "hidden")
		if props.IsDir {
			view.Attr("edit-arrow", "src", model.themedIcon("icons/chevron-right.svg", "icon.foreground"))
			view.Attr("edit-row", "aria-expanded", boolAttribute(model.isProjectDirectoryExpanded(props.Project, props.Path, props.Depth)))
		}
	case "new-folder":
		label = "新建文件夹"
	}
	if c.creating() {
		props.Name, props.IsDir = c.name, c.dialog == "new-folder"
	}
	view.Attr("edit-row", "style", fmt.Sprintf("padding-left:%dpx", padding+props.Depth*16))
	view.Attr("edit-icon", "src", model.themedIcon(treeIcon(&props), treeIconForeground(&props)))
	view.Attr("edit-name", "value", c.name)
	view.Attr("edit-name", "aria-label", model.tr(label))
	view.Attr("edit-name", "aria-invalid", boolAttribute(c.err != ""))
	if c.pending {
		view.Attr("edit-name", "disabled", "")
	} else {
		view.RemoveAttr("edit-name", "disabled")
	}
	view.Event("edit-name", htmlui.EventInput)
	view.Text("edit-error", c.err)
	if c.err == "" {
		view.Attr("edit-error", "hidden", "")
	} else {
		view.RemoveAttr("edit-error", "hidden")
	}
}

var pencodeFileContextRefs = [...]string{
	"file-context-open", "file-context-new-file", "file-context-new-folder",
	"file-context-find", "file-context-history", "file-context-reveal",
	"file-context-cut", "file-context-copy", "file-context-paste",
	"file-context-copy-path", "file-context-copy-relative", "file-context-rename", "file-context-delete", "file-context-refresh",
}

var pencodeFileContextHints = func() [len(pencodeFileContextRefs)]struct{ ref string } {
	var hints [len(pencodeFileContextRefs)]struct{ ref string }
	for i, ref := range pencodeFileContextRefs {
		hints[i].ref = ref + "-shortcut"
	}
	return hints
}()

func (model *workspaceModel) renderFileContext(view *htmlui.View) {
	c := &model.fileContext
	if c.open {
		view.RemoveAttr("file-context", "hidden")
		view.Attr("file-context", "style", languagePopupStyle(c.x, c.y))
		label := model.tr("打开")
		if c.directory {
			label = model.tr("展开 / 折叠")
		}
		view.Text("file-context-open-label", label)
		view.Text("file-context-history-label", model.tr("查看本地历史"))
		view.Text("file-context-reveal-label", model.tr(fileManagerLabel(runtime.GOOS)))
		for i, ref := range pencodeFileContextRefs {
			model.ensureSettings()
			view.Text(pencodeFileContextHints[i].ref, model.settings.Bindings[pencodeFileContextRefs[i]].label(runtime.GOOS))
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
	if c.dialog == "" || c.inline() {
		view.Attr("file-dialog", "hidden", "")
		return
	}
	view.RemoveAttr("file-dialog", "hidden")
	view.Attr("content", "disabled", "")
	view.Text("file-dialog-title", c.title)
	view.Text("file-dialog-message", c.message)
	view.Text("file-dialog-error", c.err)
	label, class := "删除", "danger"
	if c.dialog == "overwrite" {
		label, class = "覆盖", "pencode-close-save"
	}
	view.Text("file-dialog-submit", model.tr(label))
	view.Attr("file-dialog-submit", "class", class)
	if c.err == "" {
		view.Attr("file-dialog-error", "hidden", "")
	} else {
		view.RemoveAttr("file-dialog-error", "hidden")
	}
	view.Event("file-dialog-submit", htmlui.EventClick)
	view.Event("file-dialog-cancel", htmlui.EventClick)
}

func (model *workspaceModel) fileContextEnabled(ref string) bool {
	c := &model.fileContext
	multiple := c.project != nil && c.project.tree.selection.multiple(c.path)
	switch ref {
	case "file-context-open":
		return !multiple && c.path != "" && (c.directory || model.canOpenFile(c.path))
	case "file-context-rename":
		return !multiple && c.path != ""
	case "file-context-delete", "file-context-cut", "file-context-copy":
		return c.path != ""
	case "file-context-paste":
		return c.externalFiles || len(model.fileClipboard.paths) != 0
	case "file-context-reveal":
		return !multiple && !model.remoteWorkspace()
	case "file-context-history", "file-context-find", "file-context-new-file", "file-context-new-folder":
		return !multiple
	}
	return true
}

func (model *workspaceModel) updateFileContext(world *module.Content, state *input.State) bool {
	c := &model.fileContext
	if model.folderSearch.open || model.settingsOpen || model.quickOpen || model.sidebarHidden() || model.closing.action != pencodeCloseNone || model.market != nil && model.market.open {
		changed := c.open || c.dialog != ""
		*c = pencodeFileContext{}
		return changed
	}
	if c.project != nil && model.projectIndex(c.project.Path) < 0 {
		*c = pencodeFileContext{}
		return true
	}
	if c.dialog != "" {
		if c.inline() && !c.needsFocus && !c.pending && state.MousePressed(input.MouseLeft) {
			pointer := state.Pointer()
			zoom := max(float32(model.settings.Zoom)/100, .01)
			x, y := pointer.X/zoom, pointer.Y/zoom
			row := "file-tree-create"
			if c.dialog == "rename" {
				row = "file-tree-rename"
			}
			inside := false
			if node, ok := world.HTMLNode(model.surface, row); ok {
				if bounds, ok := node.Bounds(); ok {
					if scroll := world.Ref[scene.UIScroll](c.project.tree.area); scroll != nil {
						bounds.X += scroll.Position.X()
						bounds.Y += scroll.Position.Y()
					}
					inside = languageRectContains(bounds, x, y) && model.pointerInsideRefs(world, x, y, []string{treeRowID(c.project.Path)})
				}
			}
			if !inside {
				if strings.TrimSpace(c.name) == "" {
					*c = pencodeFileContext{}
				} else {
					model.submitFileDialog(world)
				}
				return true
			}
		}
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
	selection := &project.tree.selection
	if relative != "" && !selection.contains(relative) {
		selection.set(relative)
	}
	*c = pencodeFileContext{project: project, path: relative, directory: directory, depth: depth, open: true,
		externalFiles: clipboard.HasFiles(),
		x:             max(4, min(x-bounds.X, bounds.Width-276)), y: max(4, min(y-bounds.Y, bounds.Height-395))}
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
	for _, access := range world.Query[htmlui.Component[treeProps, treeRowState]]() {
		props := access.Get().Props
		if props.Editing {
			continue
		}
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
			if scroll, ok := world.Get[scene.UIScroll](entity); ok {
				row.X += scroll.Position.X()
				row.Y += scroll.Position.Y()
			}
		}
		if languageRectContains(row, x, y) && model.pointerInsideRefs(world, x, y, []string{props.TreeID}) {
			return props.Project, props.Path, props.IsDir, props.Depth
		}
	}
	for _, access := range world.Query[htmlui.Component[projectProps, struct{}]]() {
		props := access.Get().Props
		if props.Model == model && model.pointerInsideRefs(world, x, y, []string{props.TreeID, "sidebar-actions"}) {
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
	ref := "file-dialog-cancel"
	if c.inline() {
		ref = "file-tree-name"
	}
	if control, ok := world.HTMLControl(model.surface, ref); ok {
		field := world.Mut[engineui.TextField](control)
		if c.inline() && field == nil {
			if model.app != nil && model.app.WakeAt != nil {
				model.app.WakeAt(time.Now())
			}
			return
		}
		if !world.Focus(control) {
			return
		}
		c.needsFocus = false
		if field != nil {
			end := len(c.name)
			if c.dialog == "rename" && !c.directory && c.err == "" {
				if dot := strings.LastIndexByte(c.name, '.'); dot > 0 {
					end = dot
				}
			}
			field.Anchor, field.Caret = 0, utf8.RuneCountInString(c.name[:end])
		}
	}
}

func (model *workspaceModel) handleFileContextKeys(world *module.Content, state *input.State, goos string) bool {
	c := &model.fileContext
	if model.uiKey(state, input.KeyEscape) {
		*c = pencodeFileContext{}
		return true
	}
	if c.dialog != "" {
		if c.inline() && model.uiKey(state, input.KeyEnter) {
			model.submitFileDialog(world)
			return true
		}
		if c.inline() && model.uiKey(state, input.KeyTab) {
			if strings.TrimSpace(c.name) == "" {
				*c = pencodeFileContext{}
			} else {
				model.submitFileDialog(world)
			}
			return true
		}
		return false
	}
	for _, ref := range pencodeFileContextRefs {
		if model.commandMatches(ref, state, goos) {
			model.handleFileContextEvent(world, ref, "")
			return true
		}
	}
	switch {
	case model.uiKey(state, input.KeyDown):
		c.index = (c.index + 1) % len(model.fileContextRefs())
	case model.uiKey(state, input.KeyUp):
		c.index = (c.index + len(model.fileContextRefs()) - 1) % len(model.fileContextRefs())
	case model.uiKey(state, input.KeyEnter), model.uiKey(state, input.KeySpace):
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
		if c.inline() {
			return false
		}
		switch ref {
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
	case "history":
		model.showLocalHistory(project, relative, c.directory)
	case "find":
		model.openFolderSearch(project, model.fileContextDirectory(), false)
	case "new-file", "new-folder":
		directory := model.fileContextDirectory()
		model.activateProject(project)
		*c = pencodeFileContext{project: project, dialog: action, needsFocus: true, path: directory, directory: true}
		if c.path == "." {
			c.path = ""
		}
		c.depth = 0
		if project.ExpandedDirs == nil {
			project.ExpandedDirs = make(map[string]bool)
		}
		for parent := c.path; parent != "" && parent != "."; parent = path.Dir(parent) {
			project.ExpandedDirs[parent] = true
			c.depth++
		}
		project.tree.revision++
		model.revealProject, model.revealPath = nil, ""
	case "rename":
		c.dialog, c.needsFocus, c.err, c.name = action, true, "", path.Base(relative)
		model.revealProject, model.revealPath = nil, ""
	case "delete":
		c.dialog, c.needsFocus, c.err = action, true, ""
		c.title = model.tr("删除")
		c.paths = project.tree.selection.targets(relative, true)
		if len(c.paths) == 1 {
			c.message = fmt.Sprintf(model.tr("确定永久删除“%s”及其内容？未保存的更改也会丢失。"), c.paths[0])
		} else {
			c.message = fmt.Sprintf(model.tr("确定永久删除选中的 %d 项及其内容？未保存的更改也会丢失。"), len(c.paths))
		}
	case "open":
		directory, depth := c.directory, c.depth
		model.activateProject(project)
		if directory {
			model.toggleProjectDirectory(project, relative, depth)
		} else {
			model.openProjectFile(project, relative)
			model.syncActiveProjectFields()
			setPencodeEditorValue(world, model.surface, model.activeContent())
		}
	case "reveal":
		target := filepath.Join(project.Path, filepath.FromSlash(relative))
		if err := revealInFileManager(target, c.directory); err != nil {
			model.setProjectStatus(project, model.tr("无法打开文件管理器：")+err.Error())
		}
	case "copy-path", "copy-relative":
		model.copyTreePaths(world, project, project.tree.selection.targets(relative, false), action == "copy-path")
	case "cut", "copy":
		// Taking an internal copy replaces any older native file clipboard.
		paths := project.tree.selection.targets(relative, true)
		model.copyTreePaths(world, project, paths, true)
		model.fileClipboard = pencodeFileClipboard{project: project, paths: paths, cut: action == "cut"}
		c.externalFiles = false
	case "paste":
		if err := model.pasteFileClipboard(project, model.fileContextDirectory()); err != nil {
			model.setProjectStatus(project, err.Error())
		}
		setPencodeEditorValue(world, model.surface, model.activeContent())
	case "refresh":
		model.startProjectScan(project, false)
	}
	return true
}

func (model *workspaceModel) copyTreePaths(world *module.Content, project *pencodeProject, paths []string, absolute bool) {
	var text strings.Builder
	for i, relative := range paths {
		if i != 0 {
			text.WriteByte('\n')
		}
		if absolute {
			relative = filepath.Join(project.Path, filepath.FromSlash(relative))
		} else if relative == "" {
			relative = "."
		}
		text.WriteString(relative)
	}
	state, ok := world.Resource[*input.State]()
	if !ok || !state.SetClipboardText(text.String()) {
		model.setProjectStatus(project, model.tr("无法写入剪贴板"))
	}
}

func (model *workspaceModel) fileTreeShortcut(world *module.Content, state *input.State, goos string) bool {
	if world == nil || model.sidebarHidden() || model.settingsOpen || model.quickOpen {
		return false
	}
	ref := ""
	for _, candidate := range [...]string{"file-context-paste", "file-context-rename", "file-context-cut", "file-context-copy", "file-context-delete", "file-context-copy-path", "file-context-copy-relative"} {
		if model.commandMatches(candidate, state, goos) {
			ref = candidate
			break
		}
	}
	if ref == "" {
		return false
	}
	paste := ref == "file-context-paste"
	focused := world.FocusedControl()
	for _, access := range world.Query[htmlui.Component[treeProps, treeRowState]]() {
		props := access.Get().Props
		if props.Model != model || props.Editing {
			continue
		}
		if control, ok := world.HTMLControl(model.surface, props.RowID); ok && control == focused {
			model.fileContext = pencodeFileContext{project: props.Project, path: props.Path, directory: props.IsDir, depth: props.Depth,
				open: true, externalFiles: paste && clipboard.HasFiles()}
			model.handleFileContextEvent(world, ref, "")
			model.fileContext.open = false
			return true
		}
	}
	if !paste {
		return false
	}
	for _, access := range world.Query[htmlui.Component[projectProps, struct{}]]() {
		props := access.Get().Props
		if props.Model != model {
			continue
		}
		if control, ok := world.HTMLControl(model.surface, "sidebar-actions"); ok && control == focused {
			model.fileContext = pencodeFileContext{project: props.Project, directory: true,
				open: true, externalFiles: clipboard.HasFiles()}
			model.handleFileContextEvent(world, "file-context-paste", "")
			model.fileContext.open = false
			return true
		}
	}
	return false
}

func fileManagerLabel(goos string) string {
	switch goos {
	case "darwin":
		return "在 Finder 中显示"
	case "windows":
		return "在文件资源管理器中显示"
	default:
		return "在文件管理器中显示"
	}
}

func fileManagerCommand(goos, target string, directory bool) *exec.Cmd {
	switch goos {
	case "darwin":
		return exec.Command("open", "-R", target)
	case "windows":
		return exec.Command("explorer", "/select,"+target)
	default:
		if !directory {
			target = filepath.Dir(target)
		}
		return exec.Command("xdg-open", target)
	}
}

func revealInFileManager(target string, directory bool) error {
	cmd := fileManagerCommand(runtime.GOOS, target, directory)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
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
	if c.pending {
		return
	}
	if c.dialog == "rename" {
		current := path.Base(c.path)
		if c.name == current || strings.TrimSpace(c.name) == current {
			*c = pencodeFileContext{}
			return
		}
	}
	if err := model.applyFileDialog(); err != nil {
		c.err = err.Error()
		c.needsFocus = true
		return
	}
	if c.inline() {
		c.pending = true
		return
	}
	*c = pencodeFileContext{}
	setPencodeEditorValue(world, model.surface, model.activeContent())
}
