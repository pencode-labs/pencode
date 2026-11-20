package workbench

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pencode-labs/pencode/internal/clipboard"
	"github.com/pencode-labs/pencode/protocol"
)

func pencodeResourceContains(parent, name string) bool {
	return name == parent || len(name) > len(parent) && name[len(parent)] == '/' && strings.HasPrefix(name, parent)
}

// Resource batches keep paths in lexical order and omit nested selections.
func resourceIndex(paths []string, relative string) int {
	for parent := relative; parent != "."; parent = path.Dir(parent) {
		if i, found := slices.BinarySearch(paths, parent); found {
			return i
		}
	}
	return -1
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
		return fmt.Errorf("%s", model.tr("项目已关闭"))
	}
	if c.dialog == "overwrite" {
		j := c.transfer
		if j == nil || !model.projectLive(j.project) || j.target != nil && !model.projectLive(j.target) {
			return fmt.Errorf("%s", model.tr("项目已关闭"))
		}
		if model.resourceBusy(project, path.Join(j.destination, filepath.Base(j.imports[0]))) {
			return fmt.Errorf("%s", model.tr("请等待编辑工具完成操作"))
		}
		return model.submitResourceIO(&fileIOJob{kind: fileImport, project: project,
			destination: j.destination, imports: j.imports, overwrite: true})
	}
	if c.dialog == "delete" {
		if len(c.paths) == 0 {
			return fmt.Errorf("%s", model.tr("不能删除项目根目录"))
		}
		return model.submitResourceIO(&fileIOJob{kind: fileRemove, project: project}, c.paths...)
	}
	name := strings.TrimSpace(c.name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("%s", model.tr("请输入有效名称，不能包含路径分隔符"))
	}
	directory := model.fileContextDirectory()
	if c.dialog == "rename" {
		if c.path == "" {
			return fmt.Errorf("%s", model.tr("不能重命名项目根目录"))
		}
		directory = path.Dir(c.path)
	}
	relative := path.Join(directory, name)
	if c.dialog == "rename" && relative == c.path {
		return nil
	}
	if project.Files[relative] != nil {
		return fmt.Errorf(model.tr("名称“%s”已存在"), name)
	}
	for _, entry := range project.Entries {
		if entry.Path == relative {
			return fmt.Errorf(model.tr("名称“%s”已存在"), name)
		}
	}

	j := &fileIOJob{project: project, relative: c.path, destination: relative}
	switch c.dialog {
	case "new-file":
		j.kind = fileCreate
	case "new-folder":
		j.kind, j.directory = fileCreate, true
	case "rename":
		j.kind = fileRename
		return model.submitResourceIO(j, c.path)
	default:
		return fmt.Errorf("%s", model.tr("未知文件操作"))
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
	model.relocateDebugBreakpoints(source, target, old, next)
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
	if (model.fileClose.action == pencodeCloseTab || model.fileClose.action == pencodeCloseOtherTabs || model.fileClose.action == pencodeCloseRightTabs) && model.fileClose.project == source && pencodeResourceContains(old, model.fileClose.path) {
		if target == nil {
			model.fileClose = pencodeCloseRequest{}
		} else {
			model.fileClose.project = target
			model.fileClose.path = next + strings.TrimPrefix(model.fileClose.path, old)
		}
	}
	model.resetHighlight()
}

func (model *workspaceModel) pasteProjectResource(project *pencodeProject, directory string) error {
	return model.transferProjectResources(model.fileClipboard, project, directory, true)
}

func (model *workspaceModel) pasteFileClipboard(project *pencodeProject, directory string) error {
	if !clipboard.HasFiles() {
		return model.pasteProjectResource(project, directory)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	files, err := clipboard.ReadFiles(ctx)
	if err != nil {
		return fmt.Errorf(model.tr("无法读取剪贴板文件：%w"), err)
	}
	if len(files) != 0 {
		if model.remoteWorkspace() {
			return fmt.Errorf("%s", model.tr("外部文件粘贴仅支持本地工作区"))
		}
		return model.submitResourceIO(&fileIOJob{kind: fileImport, project: project, destination: directory, imports: files})
	}
	return model.pasteProjectResource(project, directory)
}

func (model *workspaceModel) transferProjectResources(clipboard pencodeFileClipboard, project *pencodeProject, directory string, duplicate bool) error {
	if len(clipboard.paths) == 0 || !model.projectLive(clipboard.project) || !model.projectLive(project) {
		return fmt.Errorf("%s", model.tr("请先复制或剪切文件"))
	}
	if clipboard.project.backend != project.backend {
		return fmt.Errorf("%s", model.tr("文件传输需要位于同一个工作区服务"))
	}
	if clipboard.project == project && resourceIndex(clipboard.paths, directory) >= 0 {
		return fmt.Errorf("%s", model.tr("不能将文件夹粘贴到自身或子文件夹中"))
	}
	if clipboard.cut && !duplicate && clipboard.project == project {
		parent := path.Clean(directory)
		if !slices.ContainsFunc(clipboard.paths, func(name string) bool { return path.Dir(name) != parent }) {
			return nil
		}
	}
	j := &fileIOJob{kind: fileTransfer, project: clipboard.project, target: project, destination: directory, cut: clipboard.cut, duplicate: duplicate}
	if duplicate && clipboard.cut {
		j.cutClipboard = clipboard.paths
	}
	return model.submitResourceIO(j, clipboard.paths...)
}

func (model *workspaceModel) submitResourceIO(j *fileIOJob, paths ...string) error {
	if model.fileIO != nil {
		for _, pending := range model.fileIO.pending {
			if pending.project == j.project || pending.target == j.project || j.target != nil && (pending.project == j.target || pending.target == j.target) {
				return fmt.Errorf("%s", model.tr("请等待当前文件任务完成"))
			}
		}
	}
	j.workspace = j.project.workspace.ID
	if j.target != nil {
		j.targetWorkspace = j.target.workspace.ID
	}
	if len(paths) != 0 {
		j.resources = make([]fileResource, len(paths))
		for i, relative := range paths {
			j.resources[i].path = relative
		}
		if j.kind != fileTransfer || j.cut {
			for file, document := range model.customDocuments {
				if document.project == j.project && document.busy() && resourceIndex(paths, file.Path) >= 0 {
					return fmt.Errorf("%s", model.tr("请等待编辑工具完成操作"))
				}
			}
		}
		if j.kind == fileRename || j.kind == fileTransfer && j.cut {
			for relative, file := range j.project.Files {
				if i := resourceIndex(paths, relative); i >= 0 {
					j.resources[i].moves = append(j.resources[i].moves, fileMove{file: file, document: file.document, relative: relative})
				}
			}
		}
	}
	if err := model.submitFileIO(j); err != nil {
		return err
	}
	model.setProjectStatus(j.project, model.tr("正在处理文件操作…"))
	return nil
}

func (model *workspaceModel) acceptResourceIO(r *fileIOResult) {
	j := r.job
	if j.kind == fileImport {
		completed := len(j.imports)
		if r.err != nil {
			completed = r.importIndex
		}
		for _, file := range j.project.Files {
			if file.Dirty {
				continue
			}
			for _, source := range j.imports[:completed] {
				if pencodeResourceContains(path.Join(j.destination, filepath.Base(source)), file.Path) {
					file.externalChanged = true
					break
				}
			}
		}
	}
	if c := &model.fileContext; c.pending && c.project == j.project && c.path == j.relative &&
		(j.kind == fileCreate && c.creating() || j.kind == fileRename && c.dialog == "rename") {
		if r.err != nil {
			c.pending, c.needsFocus, c.err = false, true, r.err.Error()
		} else {
			*c = pencodeFileContext{}
		}
	}
	target := j.project
	if j.kind == fileTransfer {
		target = j.target
		if !model.projectLive(target) {
			model.startProjectSnapshot(j.project, false, true)
			return
		}
	}
	if j.kind == fileRemove || j.kind == fileRename || j.kind == fileTransfer && j.cut {
		model.acceptResourceMoves(r, target)
	}
	if r.err == nil || len(r.completed) != 0 {
		if target.ExpandedDirs == nil {
			target.ExpandedDirs = make(map[string]bool)
		}
		target.tree.revision++
		directory := j.destination
		if j.kind != fileTransfer && j.kind != fileImport {
			directory = path.Dir(directory)
		}
		for ; directory != "" && directory != "."; directory = path.Dir(directory) {
			target.ExpandedDirs[directory] = true
		}
	}
	if j.kind == fileTransfer && j.cut {
		model.refreshMovedResources(r)
	} else if r.err != nil {
		model.refreshResourceProject(j.project)
		if target != j.project {
			model.refreshResourceProject(target)
		}
	} else {
		model.refreshResourceProject(target)
	}
	if r.err != nil {
		var conflict *protocol.Error
		if j.kind == fileImport && errors.As(r.err, &conflict) && conflict.Code == protocol.Conflict {
			next := *j
			next.imports = j.imports[r.importIndex:]
			name := filepath.Base(next.imports[0])
			model.fileContext = pencodeFileContext{project: j.project, transfer: &next, dialog: "overwrite", title: model.tr("覆盖文件？"), needsFocus: true,
				message: fmt.Sprintf(model.tr("“%s”已存在，是否覆盖？文件夹将合并，并覆盖其中的同名文件。未保存的编辑会保留。"), path.Join(j.destination, name))}
			return
		}
		status := r.err.Error()
		if len(r.completed) != 0 {
			status = fmt.Sprintf(model.tr("已完成 %d/%d 项：%s"), len(r.completed), len(j.resources), status)
		}
		model.setProjectStatus(target, status)
		return
	}
	if j.kind == fileImport {
		model.reloadExternalFiles(target)
	}
	for _, err := range r.moveErrors {
		model.setProjectStatus(target, model.tr("资源已移动，文档重新连接失败：")+err.Error())
		break
	}
	if j.kind == fileCreate && !j.directory && model.canOpenFile(j.destination) {
		model.activateProject(target)
		model.openProjectFile(target, j.destination)
	}
}

func (model *workspaceModel) refreshMovedResources(r *fileIOResult) {
	j := r.job
	source, target := j.project, j.target
	// A scan already in flight may contain pre-move paths. Keep its visible
	// rows until a fresh snapshot can replace them atomically.
	reconcile := source.scan != nil || target.scan != nil
	if !reconcile {
		for i, next := range r.completed {
			old := j.resources[i].path
			if source == target && old == next {
				continue
			}
			if !source.moveTreeEntries(target, old, next) {
				reconcile = true
				break
			}
		}
	}
	// The native watch updates search and Git decorations in the background.
	// Projects without a subscription need an explicit snapshot as well.
	source.tree.moved, target.tree.moved = true, true
	if reconcile || r.err != nil || source.watch == nil {
		model.startProjectSnapshot(source, false, true)
	}
	if source != target && (reconcile || r.err != nil || target.watch == nil) {
		model.startProjectSnapshot(target, false, true)
	}
	model.setProjectStatus(source, "")
	if !j.duplicate && source == target && source == model.activeProjectState() && model.activePath != source.ActivePath {
		// The active document moved under the pointer. Keep the tree at the
		// drop position instead of queuing navigation to its new path.
		model.activePath = source.ActivePath
		model.revealProject, model.revealPath = nil, ""
	}
	model.syncActiveProjectFields()
	model.syncLanguageDocuments()
}

func (model *workspaceModel) acceptResourceMoves(r *fileIOResult, target *pencodeProject) {
	j := r.job
	if len(r.completed) == 0 {
		return
	}
	if j.kind == fileRemove {
		target = nil
	}
	destinations := make(map[string]string, len(r.completed))
	for i, next := range r.completed {
		resource := &j.resources[i]
		destinations[resource.path] = next
		if j.project == target && next == resource.path {
			continue
		}
		for _, move := range resource.moves {
			if r.moveErrors[move.document] != nil && model.fileLive(j.project, move.relative, move.file) && move.file.document == move.document {
				move.file.closeDocument()
			}
		}
		model.relocateProjectResources(j.project, target, resource.path, next)
	}
	j.project.tree.selection.relocate(destinations, target == j.project)
	c := &model.fileClipboard
	if c.project != j.project {
		return
	}
	if j.cutClipboard != nil && sourceFirst(c.paths) == sourceFirst(j.cutClipboard) {
		c.paths = c.paths[len(r.completed):]
	} else {
		paths := make([]string, 0, len(c.paths))
		crossProject := target != nil && target != j.project
		var remaining []string
		for _, name := range c.paths {
			next, moved := resourceDestination(destinations, name)
			if next != "" {
				paths = append(paths, next)
			}
			if crossProject && !moved {
				remaining = append(remaining, name)
			}
		}
		if crossProject {
			// Keep the source workspace when some clipboard paths stayed there.
			if len(remaining) != 0 {
				paths = remaining
			} else {
				c.project = target
			}
		}
		slices.Sort(paths)
		write := 0
		for _, name := range paths {
			if resourceIndex(paths[:write], name) < 0 {
				paths[write] = name
				write++
			}
		}
		clear(paths[write:])
		c.paths = paths[:write]
	}
	if len(c.paths) == 0 {
		*c = pencodeFileClipboard{}
	}
}

func resourceDestination(destinations map[string]string, relative string) (string, bool) {
	for parent := relative; parent != "."; parent = path.Dir(parent) {
		if next, ok := destinations[parent]; ok {
			if next == "" {
				return "", true
			}
			return next + relative[len(parent):], true
		}
	}
	return relative, false
}
