package workbench

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/protocol"
)

type fileIOKind uint8

const (
	fileOpenWorkspace fileIOKind = iota
	fileOpen
	fileSave
	fileCreate
	fileRemove
	fileRename
	fileTransfer
	fileReload
	fileCloseWorkspace
	fileLanguageEdit
	fileSearchReplace
)
const fileIOCapacity = 32

type fileMove struct {
	file     *openFile
	document *client.Document
	relative string
}
type fileReloadResult struct {
	file                      *openFile
	relative, before, content string
	document, previous        *client.Document
	retired                   bool
	err                       error
}

// Jobs contain immutable inputs. Project/file pointers are identity tokens;
// only the foreground may dereference them. The worker owns returned handles.
type fileIOJob struct {
	id                                                    uint64
	finished                                              bool
	err                                                   error
	kind                                                  fileIOKind
	ctx                                                   context.Context
	cancel                                                context.CancelFunc
	backend                                               *client.Client
	project, target                                       *pencodeProject
	workspace, targetWorkspace                            uint64
	file                                                  *openFile
	document                                              *client.Document
	root, relative, destination, content, baseline        string
	preview, reveal, directory, cut, duplicate, clipboard bool
	moves                                                 []fileMove
	reload                                                []fileReloadResult
	navigation                                            *fileNavigation
	language                                              *languageEditPlan
	searchReplace                                         *folderReplacePlan
}
type fileNavigation struct {
	line        int
	offset      func(string) (int, error)
	history     bool
	query       string
	searchMatch *protocol.TextMatch
	digest      string
}
type fileIOResult struct {
	job                  *fileIOJob
	workspace            protocol.Workspace
	document             *client.Document
	content, destination string
	moveErrors           map[*client.Document]error
	reload               []fileReloadResult
	err                  error
}

func (r *fileIOResult) release() {
	if r.job.language != nil {
		r.job.language.release()
	}
	if r.document != nil {
		r.document.Release()
		r.document = nil
	}
	for i := range r.reload {
		if r.reload[i].document != nil {
			r.reload[i].document.Release()
			r.reload[i].document = nil
		}
	}
}

type pencodeFileIO struct {
	ctx     context.Context
	cancel  context.CancelFunc
	jobs    chan *fileIOJob
	results chan fileIOResult
	done    chan struct{}
	pending map[uint64]*fileIOJob // foreground only
	next    uint64
}

func newFileIO(wake func()) *pencodeFileIO {
	ctx, cancel := context.WithCancel(context.Background())
	q := &pencodeFileIO{ctx: ctx, cancel: cancel, jobs: make(chan *fileIOJob, fileIOCapacity+32), results: make(chan fileIOResult, fileIOCapacity), done: make(chan struct{}), pending: make(map[uint64]*fileIOJob)}
	go func() {
		defer close(q.done)
		defer func() {
			for {
				select {
				case <-q.jobs:
				default:
					return
				}
			}
		}()
		defer func() {
			for {
				select {
				case r := <-q.results:
					r.release()
				default:
					return
				}
			}
		}()
		for {
			var job *fileIOJob
			select {
			case <-ctx.Done():
				return
			case job = <-q.jobs:
			}
			r := executeFileIO(job)
			if job.kind == fileCloseWorkspace {
				continue
			}
			select {
			case <-ctx.Done():
				r.release()
				return
			case q.results <- r:
				if wake != nil {
					wake()
				}
			}
		}
	}()
	return q
}
func executeFileIO(j *fileIOJob) (r fileIOResult) {
	r.job = j
	if r.err = j.ctx.Err(); r.err != nil {
		return
	}
	switch j.kind {
	case fileCloseWorkspace:
		ctx, cancel := context.WithTimeout(j.ctx, time.Second)
		defer cancel()
		r.err = j.backend.CloseWorkspace(ctx, j.workspace)
	case fileOpenWorkspace:
		r.workspace, r.err = j.backend.OpenWorkspace(j.ctx, j.root)
	case fileOpen:
		workspace := j.workspace
		if workspace == 0 {
			var w protocol.Workspace
			w, r.err = j.backend.OpenWorkspace(j.ctx, j.root)
			workspace = w.ID
		}
		if r.err == nil {
			r.document, r.content, r.err = j.backend.OpenDocument(j.ctx, workspace, j.relative, isTextFile(j.relative))
		}
	case fileSave:
		document := j.document
		if document == nil {
			var workspace protocol.Workspace
			workspace, r.err = j.backend.OpenWorkspace(j.ctx, j.root)
			if r.err != nil {
				return
			}
			var saved string
			document, saved, r.err = j.backend.OpenDocument(j.ctx, workspace.ID, j.relative, true)
			if r.err != nil {
				return
			}
			r.document = document
			if saved != j.baseline {
				r.err = &protocol.Error{Code: protocol.Conflict, Message: "文件已被其他程序修改，请先比较或重新加载"}
				return
			}
			document.Update("", j.content, -1, 0, 0)
		}
		r.err = document.SaveSnapshot(j.ctx, j.content)
	case fileCreate:
		r.err = j.backend.Create(j.ctx, j.workspace, j.destination, j.directory)
	case fileRemove:
		r.err = j.backend.Remove(j.ctx, j.workspace, j.relative)
	case fileRename:
		r.destination = j.destination
		r.err = j.backend.Rename(j.ctx, j.workspace, j.relative, j.destination)
	case fileTransfer:
		r.destination, r.err = j.backend.Transfer(j.ctx, j.workspace, j.targetWorkspace, j.relative, j.destination, j.cut, j.duplicate)
	case fileLanguageEdit:
		r.err = executeLanguageEdit(j)
	case fileSearchReplace:
		r.err = executeFolderReplace(j)
	case fileReload:
		r.reload = j.reload
		for i := range r.reload {
			item := &r.reload[i]
			if item.previous != nil {
				item.previous.Close()
			}
			item.retired = true
			item.document, item.content, item.err = j.backend.OpenDocument(j.ctx, j.workspace, item.relative, isTextFile(item.relative))
			if j.ctx.Err() != nil {
				r.err = j.ctx.Err()
				return
			}
		}
	}
	if r.err == nil && (j.kind == fileRename || j.kind == fileTransfer && j.cut) {
		for _, move := range j.moves {
			if move.document == nil {
				continue
			}
			destination := r.destination + move.relative[len(j.relative):]
			workspace := j.workspace
			if j.kind == fileTransfer {
				workspace = j.targetWorkspace
			}
			if err := move.document.Move(j.ctx, workspace, destination); err != nil {
				if r.moveErrors == nil {
					r.moveErrors = make(map[*client.Document]error)
				}
				r.moveErrors[move.document] = err
			}
		}
	}
	return
}
func (m *workspaceModel) submitFileIO(j *fileIOJob) error {
	if !m.backendConnected() {
		return fmt.Errorf("工作区服务未连接，请等待连接恢复")
	}
	if m.fileIO == nil {
		m.fileIO = newFileIO(m.backgroundWake())
	}
	q := m.fileIO
	if len(q.pending) >= fileIOCapacity {
		return fmt.Errorf("文件任务队列已满，请稍后重试")
	}
	j.ctx, j.cancel = context.WithTimeout(q.ctx, 30*time.Second)
	q.next++
	j.id = q.next
	j.backend = m.backend
	q.pending[j.id] = j
	q.jobs <- j // pending includes queued, running and unaccepted results; capacity is reserved above.
	return nil
}
func (m *workspaceModel) stopFileIO() {
	m.folderSearch.replacing = false
	if m.fileIO != nil {
		m.fileIO.cancel()
		for _, j := range m.fileIO.pending {
			if j.language != nil {
				j.language.finish(fmt.Errorf("文件任务已取消，编辑未应用"))
			}
		}
		clear(m.fileIO.pending)
		m.fileIO = nil
	}
}
func (m *workspaceModel) cancelProjectFileIO(p *pencodeProject) {
	if m.fileIO == nil {
		return
	}
	for _, j := range m.fileIO.pending {
		if j.project == p || j.target == p {
			j.cancel()
		}
	}
}
func (m *workspaceModel) fileIOMutating(p *pencodeProject) bool {
	if m.fileIO != nil {
		for _, j := range m.fileIO.pending {
			if (j.project == p || j.target == p) && (j.kind >= fileCreate && j.kind <= fileTransfer || j.kind == fileSearchReplace) {
				return true
			}
		}
	}
	return false
}
func (m *workspaceModel) projectLive(p *pencodeProject) bool {
	if p == nil {
		return false
	}
	for _, current := range m.projects {
		if current == p {
			return true
		}
	}
	return false
}
func (m *workspaceModel) fileLive(p *pencodeProject, relative string, f *openFile) bool {
	if p == nil {
		return m.files[relative] == f
	}
	return m.projectLive(p) && p.Files[relative] == f
}
func (m *workspaceModel) pollFileIO() bool {
	q := m.fileIO
	if q == nil {
		return false
	}
	changed := false
	// Bound reconciliation work per frame as well as the queue itself.
	for range 4 {
		select {
		case r := <-q.results:
			j := r.job
			delete(q.pending, j.id)
			j.finished, j.err = true, r.err
			changed = true
			if j.backend != m.backend {
				j.cancel()
				r.release()
				continue
			}
			m.acceptFileIO(&r)
			j.cancel()
			r.release()
			m.finishFileClose()
		default:
			return changed
		}
	}
	if m.app != nil && m.app.RequestRender != nil {
		m.app.RequestRender()
	}
	return changed
}
func (m *workspaceModel) acceptFileIO(r *fileIOResult) {
	j := r.job
	if j.kind == fileSearchReplace {
		m.acceptFolderReplace(r)
		return
	}
	if j.kind == fileOpenWorkspace {
		if r.err != nil {
			m.setActiveStatus("打开项目失败：" + r.err.Error())
			return
		}
		m.acceptWorkspace(j.backend, r.workspace)
		return
	}
	if j.kind == fileLanguageEdit {
		m.acceptLanguageEdit(r)
		return
	}
	if j.project != nil && !m.projectLive(j.project) {
		if j.target != nil && m.projectLive(j.target) {
			m.refreshResourceProject(j.target)
		}
		return
	}
	switch j.kind {
	case fileOpen:
		m.acceptFileOpen(r)
	case fileSave:
		m.acceptFileSave(r)
	case fileReload:
		m.acceptFileReload(r)
	default:
		m.acceptResourceIO(r)
	}
}
func (m *workspaceModel) requestFileOpen(p *pencodeProject, relative string, preview, reveal bool, navigation *fileNavigation) error {
	if p != nil && !m.projectLive(p) {
		return fmt.Errorf("项目已关闭")
	}
	if m.fileIOMutating(p) {
		return fmt.Errorf("请等待文件操作完成")
	}
	m.cancelFileOpens()
	files := m.files
	if p != nil {
		files = p.Files
	}
	if f := files[relative]; f != nil {
		if p != nil {
			selectProjectFile(p, relative, preview)
			if reveal {
				m.queueProjectFileReveal(p)
			}
			m.syncActiveProjectFields()
		} else {
			m.activePath = relative
		}
		m.finishFileNavigation(p, f, navigation)
		return nil
	}
	j := &fileIOJob{kind: fileOpen, project: p, relative: relative, preview: preview, reveal: reveal, navigation: navigation, root: m.projectPath}
	if p != nil {
		j.workspace = p.workspace.ID
		j.root = p.Path
	}
	if err := m.submitFileIO(j); err != nil {
		return err
	}
	m.setProjectStatus(p, "正在打开 "+path.Base(relative)+"…")
	return nil
}
func (m *workspaceModel) acceptFileOpen(r *fileIOResult) {
	j := r.job
	// A cancelled open may have completed on the server, so check the intent,
	// not just its error; never resurrect a closed tab or steal a later selection.
	if j.ctx.Err() == context.Canceled || j.navigation != nil && j.navigation.history && (!m.quickOpen || m.quickQuery != j.navigation.query) {
		return
	}
	if r.err != nil {
		m.setProjectStatus(j.project, "打开文件失败："+r.err.Error())
		return
	}
	if j.project != nil {
		p := j.project
		if p.Files[j.relative] != nil {
			return
		}
		if j.preview && p.PreviewPath != "" {
			m.highlightCache.retainPreview(p.Files[p.PreviewPath])
		}
		acceptProjectFile(p, j.relative, j.preview, r.document, r.content)
		m.highlightCache.restorePreview(p.Files[j.relative])
		r.document = nil
		if j.reveal {
			m.queueProjectFileReveal(p)
		}
		m.syncActiveProjectFields()
		if !j.reveal {
			m.revealProject, m.revealPath = nil, ""
		}
		m.finishFileNavigation(p, p.Files[j.relative], j.navigation)
	} else {
		if m.files == nil {
			m.files = make(map[string]*openFile)
		}
		if m.files[j.relative] != nil {
			return
		}
		f := &openFile{document: r.document, Path: j.relative, Name: path.Base(j.relative), Content: r.content, savedContent: r.content}
		r.document = nil
		m.files[j.relative] = f
		m.tabs = append(m.tabs, j.relative)
		m.activePath = j.relative
		m.finishFileNavigation(nil, f, j.navigation)
	}
	m.status = ""
}
func (m *workspaceModel) finishFileNavigation(p *pencodeProject, f *openFile, n *fileNavigation) {
	if n == nil || f == nil {
		return
	}
	if n.searchMatch != nil {
		m.finishSearchNavigation(p, f, n)
		return
	}
	if n.history {
		if m.session != nil && m.session.quickHistory != nil {
			key := m.projectPath
			if p != nil {
				key = p.historyKey
			}
			if err := m.session.quickHistory.remember(key, f.Path); err != nil {
				m.setProjectStatus(p, "保存搜索历史失败："+err.Error())
			}
		}
		m.quickOpen, m.quickNeedsFocus = false, false
		m.quickQuery = ""
		m.quickCount = -1
		if m.fontResources != nil {
			if control, ok := m.fontResources.HTMLControl(m.surface, "quick-open-query"); ok {
				m.fontResources.SetControlValue(control, "")
			}
		}
	}
	m.quickFocusEditor = true
	m.quickTargetLine = n.line
	if n.offset != nil {
		offset, err := n.offset(f.Content)
		if err != nil {
			m.setProjectStatus(p, err.Error())
			return
		}
		if m.languages != nil {
			m.languages.panel.navigate = f
			m.languages.panel.offset = offset
		}
	}
}
func (m *workspaceModel) requestFileSave(p *pencodeProject, f *openFile) (*fileIOJob, error) {
	if f == nil {
		return nil, fmt.Errorf("没有打开的文件")
	}
	if !isTextFile(f.Name) {
		return nil, fmt.Errorf("当前编辑工具不支持保存二进制文件")
	}
	if m.fileIOMutating(p) {
		return nil, fmt.Errorf("请等待文件操作完成")
	}
	if m.fileIO != nil {
		for _, j := range m.fileIO.pending {
			if j.kind == fileReload && j.project == p {
				return nil, fmt.Errorf("请等待重新加载完成")
			}
			if j.kind == fileSave && j.file == f {
				if j.content == f.Content {
					return j, nil
				}
				return nil, fmt.Errorf("该文件正在保存，新更改保留在编辑器中，请稍后再保存")
			}
		}
	}
	j := &fileIOJob{kind: fileSave, project: p, file: f, document: f.document, relative: f.Path, root: m.projectPath, content: f.Content, baseline: f.savedContent}
	if p != nil {
		j.root = p.Path
		j.workspace = p.workspace.ID
	}
	if err := m.submitFileIO(j); err != nil {
		return nil, err
	}
	m.prepareLanguageSave(f)
	m.setProjectStatus(p, "正在保存 "+f.Name+"…")
	return j, nil
}
func (m *workspaceModel) acceptFileSave(r *fileIOResult) {
	j := r.job
	if !m.fileLive(j.project, j.relative, j.file) {
		return
	}
	f := j.file
	if r.err == nil {
		if r.document != nil {
			if f.document == nil {
				f.document = r.document
				r.document = nil
				f.document.Update("", f.Content, -1, 0, 0)
			}
		}
		f.savedContent = j.content
		f.Dirty = f.Content != j.content
		f.saveRevision++
		m.syncDebugBreakpoints(j.project, f)
		m.setProjectStatus(j.project, "已保存 "+f.Name)
		m.syncLanguageDocuments()
	} else {
		m.setProjectStatus(j.project, fmt.Sprintf("保存 %s 失败：%v", f.Name, r.err))
	}
	if m.closing.saveID == j.id {
		m.closing.saveID = 0
		if r.err != nil {
			m.closing.error = r.err.Error()
		} else if f.Dirty {
			m.closing.error = "保存期间内容已改变，请再次保存"
		} else {
			m.resolveClose(false)
		}
	}
}
func (m *workspaceModel) acceptFileReload(r *fileIOResult) {
	p := r.job.project
	for i := range r.reload {
		item := &r.reload[i]
		if p.Files[item.relative] != item.file {
			continue
		}
		f := item.file
		if !item.retired {
			continue
		}
		if f.document == item.previous {
			f.document = nil
		}
		if f.Content != item.before {
			m.setProjectStatus(p, "重新加载期间内容已改变，保留当前编辑")
			continue
		}
		if item.err != nil {
			m.setProjectStatus(p, "重新加载失败："+item.err.Error())
			continue
		}
		m.highlightCache.remove(f)
		f.closeDocument()
		f.document = item.document
		item.document = nil
		start, oldEnd, newEnd, _ := pencodeChangedRange(f.Content, item.content)
		m.rebaseDebugBreakpoints(p, f, item.content, start, oldEnd, newEnd)
		f.Content = item.content
		f.savedContent = item.content
		f.Dirty = false
		f.pencodeEditHistory = pencodeEditHistory{}
		m.syncDebugBreakpoints(p, f)
	}
	m.resetHighlight()
	m.syncActiveProjectFields()
	m.syncLanguageDocuments()
}

func (m *workspaceModel) cancelFileOpens() {
	if m.fileIO != nil {
		for _, j := range m.fileIO.pending {
			if j.kind == fileOpen {
				j.cancel()
			}
		}
	}
}
func (m *workspaceModel) cancelFileOpen(p *pencodeProject, relative string) {
	if m.fileIO != nil {
		for _, j := range m.fileIO.pending {
			if j.kind == fileOpen && j.project == p && j.relative == relative {
				j.cancel()
			}
		}
	}
}

// Cleanup shares the ordered worker: closing and immediately reopening a root
// must not let an old CloseWorkspace invalidate the new project's handle.
func (m *workspaceModel) releaseProjectWorkspace(p *pencodeProject) {
	m.queueWorkspaceRelease(p.backend, p.workspace.ID)
}
func (m *workspaceModel) queueWorkspaceRelease(backend *client.Client, workspace uint64) {
	if backend == nil || workspace == 0 {
		return
	}
	if m.fileIO == nil {
		m.fileIO = newFileIO(m.backgroundWake())
	}
	q := m.fileIO
	q.jobs <- &fileIOJob{kind: fileCloseWorkspace, backend: backend, workspace: workspace, ctx: q.ctx}
}

func (m *workspaceModel) fileCloseBlocked(action pencodeCloseAction, p *pencodeProject) bool {
	if m.fileIO != nil {
		for _, j := range m.fileIO.pending {
			if j.kind >= fileCreate && j.kind <= fileTransfer && (action == pencodeCloseWindow || j.project == p || j.target == p) {
				return true
			}
		}
	}
	return false
}
func (m *workspaceModel) finishFileClose() {
	r := m.fileClose
	if r.action == pencodeCloseNone || m.fileCloseBlocked(r.action, r.project) {
		return
	}
	m.fileClose = pencodeCloseRequest{}
	switch r.action {
	case pencodeCloseTab:
		if r.project != nil {
			m.closeProjectTab(r.project, r.path)
		} else {
			m.closeTab(r.path)
		}
	case pencodeCloseProject:
		m.closeProject(r.project)
	case pencodeCloseOtherTabs, pencodeCloseRightTabs, pencodeCloseSavedTabs, pencodeCloseAllTabs:
		m.closeProjectTabs(r.action, r.project, r.path)
	case pencodeCloseWindow:
		if m.requestWindowClose() && m.app != nil {
			m.app.RequestClose()
		}
	}
}

func (m *workspaceModel) requestFileReload(p *pencodeProject, cleanOnly bool) error {
	if m.fileIO != nil {
		for _, job := range m.fileIO.pending {
			if job.project == p || job.target == p {
				return fmt.Errorf("请等待文件操作完成后重新加载")
			}
		}
	}
	job := &fileIOJob{kind: fileReload, project: p, workspace: p.workspace.ID}
	for _, relative := range p.Tabs {
		if f := p.Files[relative]; f != nil && (!cleanOnly || !f.Dirty) {
			job.reload = append(job.reload, fileReloadResult{file: f, relative: relative, before: f.Content, previous: f.document})
		}
	}
	return m.submitFileIO(job)
}
