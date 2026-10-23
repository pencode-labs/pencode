package workbench

import (
	"fmt"
	"path"
	"strings"
)

func pencodeResourceContains(parent, name string) bool {
	return name == parent || len(name) > len(parent) && name[len(parent)] == '/' && strings.HasPrefix(name, parent)
}

func (model *workspaceModel) resourceBusy(project *pencodeProject, relative string) bool {
	for file, document := range model.customDocuments {
		if document.project == project && pencodeResourceContains(relative, file.Path) && document.busy() {
			return true
		}
	}
	return false
}

func (model *workspaceModel) applyFileDialog() error {
	c := &model.fileContext
	project := c.project
	if project == nil || model.projectIndex(project.Path) < 0 {
		return fmt.Errorf("项目已关闭")
	}
	if c.dialog == "delete" {
		if c.path == "" {
			return fmt.Errorf("不能删除项目根目录")
		}
		if model.resourceBusy(project, c.path) {
			return fmt.Errorf("请等待编辑工具完成操作")
		}
		return model.submitResourceIO(&fileIOJob{kind: fileRemove, project: project, relative: c.path})
	}
	name := strings.TrimSpace(c.name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("请输入有效名称，不能包含路径分隔符")
	}
	directory := model.fileContextDirectory()
	if c.dialog == "rename" {
		if c.path == "" {
			return fmt.Errorf("不能重命名项目根目录")
		}
		if model.resourceBusy(project, c.path) {
			return fmt.Errorf("请等待编辑工具完成操作")
		}
		directory = path.Dir(c.path)
	}
	relative := path.Join(directory, name)
	if c.dialog == "rename" && relative == c.path {
		return nil
	}
	if project.Files[relative] != nil {
		return fmt.Errorf("名称“%s”已存在", name)
	}

	j := &fileIOJob{project: project, relative: c.path, destination: relative}
	switch c.dialog {
	case "new-file":
		j.kind = fileCreate
	case "new-folder":
		j.kind, j.directory = fileCreate, true
	case "rename":
		j.kind = fileRename
	default:
		return fmt.Errorf("未知文件操作")
	}
	return model.submitResourceIO(j)
}

func (model *workspaceModel) refreshResourceProject(project *pencodeProject) {
	// Rescan entries without reloading buffers: unsaved edits and history belong
	// to the open documents, independently of the filesystem tree.
	model.startProjectScan(project, false)
	model.syncActiveProjectFields()
	model.syncLanguageDocuments()
}

// Compact tabs and rebase their documents in one pass. A renamed document gets
// a new identity so language services close the old URI before opening the new
// one, while its buffer, save state and undo history retain their ownership.
func (model *workspaceModel) relocateProjectResources(source, target *pencodeProject, old, next string) {
	if target != nil && target.Files == nil {
		target.Files = make(map[string]*openFile)
	}
	write := 0
	for _, relative := range source.Tabs {
		if !pencodeResourceContains(old, relative) {
			source.Tabs[write] = relative
			write++
			continue
		}
		file := source.Files[relative]
		delete(source.Files, relative)
		if file != nil {
			model.highlightCache.remove(file)
		}
		if target != nil && file != nil {
			updated := *file
			updated.Path, updated.Name = next+strings.TrimPrefix(relative, old), path.Base(next+strings.TrimPrefix(relative, old))

			updated.editorGeneration = 0
			target.Files[updated.Path] = &updated
			if target == source {
				source.Tabs[write] = updated.Path
				write++
			} else {
				target.Tabs = append(target.Tabs, updated.Path)
			}
		} else if file != nil {
			file.closeDocument()
		}
	}
	clear(source.Tabs[write:])
	source.Tabs = source.Tabs[:write]
	for _, selected := range [...]*string{&source.ActivePath, &source.PreviewPath} {
		if *selected != "" && pencodeResourceContains(old, *selected) {
			if target == source {
				*selected = next + strings.TrimPrefix(*selected, old)
			} else {
				*selected = ""
			}
		}
	}
	if source.ActivePath == "" && len(source.Tabs) > 0 {
		source.ActivePath = source.Tabs[0]
	}
	if target != nil && target.ActivePath == "" && len(target.Tabs) > 0 {
		target.ActivePath = target.Tabs[0]
	}
	if target != nil && target.ExpandedDirs == nil {
		target.ExpandedDirs = make(map[string]bool)
	}
	source.tree.revision++
	if target != nil {
		target.tree.revision++
	}
	for relative, expanded := range source.ExpandedDirs {
		if pencodeResourceContains(old, relative) {
			delete(source.ExpandedDirs, relative)
			if target != nil {
				target.ExpandedDirs[next+strings.TrimPrefix(relative, old)] = expanded
			}
		}
	}
	if model.fileClose.action == pencodeCloseTab && model.fileClose.project == source && pencodeResourceContains(old, model.fileClose.path) {
		if target == nil {
			model.fileClose = pencodeCloseRequest{}
		} else {
			model.fileClose.project = target
			model.fileClose.path = next + strings.TrimPrefix(model.fileClose.path, old)
		}
	}
	clipboard := &model.fileClipboard
	if clipboard.project == source && pencodeResourceContains(old, clipboard.path) {
		if target == nil {
			*clipboard = pencodeFileClipboard{}
		} else {
			clipboard.project, clipboard.path = target, next+strings.TrimPrefix(clipboard.path, old)
		}
	}
	model.resetHighlight()
}

func (model *workspaceModel) pasteProjectResource(project *pencodeProject, directory string) error {
	return model.transferProjectResource(model.fileClipboard, project, directory, true)
}

// Dragging uses the same transfer as paste, but preserves the clipboard and
// rejects collisions instead of inventing a different resource name.
func (model *workspaceModel) moveProjectResource(source *pencodeProject, relative string, target *pencodeProject, directory string) error {
	return model.transferProjectResource(pencodeFileClipboard{project: source, path: relative, cut: true}, target, directory, false)
}

func (model *workspaceModel) transferProjectResource(clipboard pencodeFileClipboard, project *pencodeProject, directory string, duplicate bool) error {
	if project == nil || clipboard.project == nil || clipboard.path == "" || model.projectIndex(clipboard.project.Path) < 0 || model.projectIndex(project.Path) < 0 {
		return fmt.Errorf("请先复制或剪切文件")
	}
	if clipboard.project.backend != project.backend {
		return fmt.Errorf("文件传输需要位于同一个工作区服务")
	}
	if clipboard.cut && model.resourceBusy(clipboard.project, clipboard.path) {
		return fmt.Errorf("请等待编辑工具完成操作")
	}
	return model.submitResourceIO(&fileIOJob{kind: fileTransfer, project: clipboard.project, target: project, relative: clipboard.path, destination: directory, cut: clipboard.cut, duplicate: duplicate, clipboard: duplicate})
}

func (model *workspaceModel) submitResourceIO(j *fileIOJob) error {
	if model.fileIO != nil {
		for _, pending := range model.fileIO.pending {
			if pending.project == j.project || pending.target == j.project || j.target != nil && (pending.project == j.target || pending.target == j.target) {
				return fmt.Errorf("请等待当前文件任务完成")
			}
		}
	}
	j.workspace = j.project.workspace.ID
	if j.target != nil {
		j.targetWorkspace = j.target.workspace.ID
	}
	if j.kind == fileRename || j.kind == fileTransfer && j.cut {
		for relative, file := range j.project.Files {
			if pencodeResourceContains(j.relative, relative) {
				j.moves = append(j.moves, fileMove{file: file, document: file.document, relative: relative})
			}
		}
	}
	if err := model.submitFileIO(j); err != nil {
		return err
	}
	model.setProjectStatus(j.project, "正在处理文件操作…")
	return nil
}

func (model *workspaceModel) acceptResourceIO(r *fileIOResult) {
	j := r.job
	if r.err != nil {
		model.refreshResourceProject(j.project)
		model.setProjectStatus(j.project, r.err.Error())
		return
	}
	target := j.project
	if j.kind == fileTransfer {
		target = j.target
	}
	if target != nil && !model.projectLive(target) {
		model.refreshResourceProject(j.project)
		return
	}
	for _, move := range j.moves {
		if err := r.moveErrors[move.document]; err != nil && model.fileLive(j.project, move.relative, move.file) && move.file.document == move.document {
			move.file.closeDocument()
		}
	}
	switch j.kind {
	case fileRemove:
		model.relocateProjectResources(j.project, nil, j.relative, "")
	case fileRename:
		model.relocateProjectResources(j.project, j.project, j.relative, r.destination)
	case fileTransfer:
		if j.cut {
			model.relocateProjectResources(j.project, target, j.relative, r.destination)
			if j.project != target {
				model.refreshResourceProject(j.project)
			}
			if j.clipboard && model.fileClipboard.project == target && model.fileClipboard.path == r.destination {
				model.fileClipboard = pencodeFileClipboard{}
			}
		}
	}
	if target.ExpandedDirs == nil {
		target.ExpandedDirs = make(map[string]bool)
	}
	target.tree.revision++
	for parent := path.Dir(j.destination); parent != "."; parent = path.Dir(parent) {
		target.ExpandedDirs[parent] = true
	}
	if j.kind == fileTransfer && j.destination != "" && j.destination != "." {
		target.ExpandedDirs[j.destination] = true
	}
	model.refreshResourceProject(target)
	for _, err := range r.moveErrors {
		model.setProjectStatus(target, "资源已移动，文档重新连接失败："+err.Error())
		break
	}
	if j.kind == fileCreate && !j.directory && model.canOpenFile(j.destination) {
		model.activateProject(target)
		model.openProjectFile(target, j.destination)
	}
}
