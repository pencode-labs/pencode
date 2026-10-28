package workbench

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	"github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/protocol"
	"github.com/pencode-labs/pencode/textsearch"
)

type folderSnapshot struct {
	file          *openFile
	path, content string
}
type folderSearchJob struct {
	ctx                    context.Context
	backend                *client.Client
	project                *pencodeProject
	root, directory, query string
	options                textsearch.Options
	revision               uint64
	snapshots              []folderSnapshot
}
type folderSearchResult struct {
	job    folderSearchJob
	result protocol.SearchResult
	err    error
}
type folderSearchRow struct{ file, match int }
type pencodeFolderSearch struct {
	open, replace, needsFocus, busy, replacing bool
	project                                    *pencodeProject
	directory, query, replacement, message     string
	options                                    textsearch.Options
	revision                                   uint64
	worker                                     *latestWorker[folderSearchJob, folderSearchResult]
	cancel                                     context.CancelFunc
	result                                     protocol.SearchResult
	rows                                       []folderSearchRow
	index                                      int
	view                                       sourceVNodeView
	preview                                    folderSearchHighlight
	icons                                      *pencodeFileIcons
	theme                                      *pencodeTheme
	nodes                                      []html.VNode
	pageLabel                                  string
	dirty                                      bool
	revealResult                               bool
	nextPath                                   string
	nextOffset                                 int
	navigate                                   *openFile
	navigateStart, navigateEnd                 int
}

func (m *workspaceModel) openFolderSearch(project *pencodeProject, directory string, replace bool) {
	if project == nil {
		return
	}
	s := &m.folderSearch
	if s.replacing {
		return
	}
	if directory == "." {
		directory = ""
	}
	s.nextPath = ""
	s.navigate = nil
	s.open, s.replace, s.needsFocus = true, replace, true
	s.project, s.directory = project, directory
	m.quickOpen, m.settingsOpen, m.findOpen = false, false, false
	m.languageUI.contextOpen = false
	m.fileContext.open = false
	m.startFolderSearch()
}

func (m *workspaceModel) closeFolderSearch(world *module.Content) {
	s := &m.folderSearch
	s.open, s.needsFocus = false, false
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.revision++
	s.busy = false
	if world != nil && m.activeFile() != nil {
		world.Focus(m.editor.control)
	}
}

func folderContains(directory, name string) bool {
	return directory == "" || directory == "." || strings.HasPrefix(name, directory+"/")
}

func (m *workspaceModel) startFolderSearch() {
	s := &m.folderSearch
	if s.replacing {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.revision++
	s.busy, s.index, s.message = false, 0, ""
	s.result = protocol.SearchResult{}
	s.rows, s.dirty = s.rows[:0], true
	if s.query == "" {
		s.message = "输入文本以搜索此文件夹"
		return
	}
	if _, err := textsearch.Compile(s.query, s.options); err != nil {
		s.message = err.Error()
		return
	}
	if !m.projectLive(s.project) || !m.backendConnected() {
		s.message = "工作区服务未连接或项目已关闭"
		return
	}
	if s.worker == nil {
		s.worker = newLatestWorker(func(job folderSearchJob, r *folderSearchResult) bool {
			r.job = job
			documents := make([]protocol.SearchDocument, len(job.snapshots))
			for i, snapshot := range job.snapshots {
				documents[i] = protocol.SearchDocument{Path: snapshot.path, Content: snapshot.content}
			}
			r.result, r.err = job.backend.SearchText(job.ctx, job.root, job.directory, job.query, job.options, documents)
			return job.ctx.Err() != context.Canceled
		}, func(r *folderSearchResult) { *r = folderSearchResult{} }, nil, m.backgroundWake())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	s.cancel = cancel
	job := folderSearchJob{ctx: ctx, backend: m.backend, project: s.project, root: s.project.Path, directory: s.directory, query: s.query, options: s.options, revision: s.revision}
	for name, f := range s.project.Files {
		if isTextFile(name) && folderContains(s.directory, name) {
			job.snapshots = append(job.snapshots, folderSnapshot{f, name, f.Content})
		}
	}
	s.busy, s.message = true, "正在搜索…"
	s.worker.submit(job)
}

func (m *workspaceModel) folderSnapshotsCurrent(job folderSearchJob) bool {
	if !m.projectLive(job.project) || job.backend != m.backend {
		return false
	}
	count := 0
	for name := range job.project.Files {
		if isTextFile(name) && folderContains(job.directory, name) {
			count++
		}
	}
	if count != len(job.snapshots) {
		return false
	}
	for _, snapshot := range job.snapshots {
		if job.project.Files[snapshot.path] != snapshot.file || snapshot.file.Content != snapshot.content {
			return false
		}
	}
	return true
}

func (m *workspaceModel) pollFolderSearch() bool {
	s := &m.folderSearch
	highlightChanged := m.pollFolderSearchHighlight()
	if !s.open {
		if s.worker != nil {
			s.worker.poll(func(*folderSearchResult) {})
		}
		return false
	}
	if !m.projectLive(s.project) {
		m.closeFolderSearch(m.fontResources)
		return true
	}
	if s.worker == nil {
		return highlightChanged
	}
	changed, retry := highlightChanged, false
	s.worker.poll(func(r *folderSearchResult) {
		if r.job.revision != s.revision {
			return
		}
		changed = true
		if !m.folderSnapshotsCurrent(r.job) {
			retry = true
			return
		}
		s.busy = false
		if s.cancel != nil {
			s.cancel()
			s.cancel = nil
		}
		s.result = r.result
		s.rows = s.rows[:0]
		for i, file := range s.result.Files {
			for j := range file.Matches {
				s.rows = append(s.rows, folderSearchRow{i, j})
			}
		}
		count := 0
		for _, file := range s.result.Files {
			count += file.Count
		}
		s.message = fmt.Sprintf("%d 个结果，%d 个文件", count, len(s.result.Files))
		if s.nextPath != "" {
			for i, row := range s.rows {
				file := s.result.Files[row.file]
				if file.Path > s.nextPath || file.Path == s.nextPath && file.Matches[row.match].Start >= s.nextOffset {
					s.index = i
					break
				}
			}
			s.nextPath = ""
		}
		if s.result.Truncated {
			s.message += "（显示前 2000 处，全部替换包含未展示的结果）"
		}
		if s.result.Skipped > 0 {
			s.message += fmt.Sprintf("；跳过 %d 个无法读取、过大或非文本文件", s.result.Skipped)
		}
		if r.err != nil {
			s.message = r.err.Error()
		}
		s.dirty, s.revealResult = true, true
	})
	if retry {
		m.startFolderSearch()
	}
	return changed
}

func (m *workspaceModel) closeFolderSearchWorker() {
	s := &m.folderSearch
	m.closeFolderSearchHighlight()
	if s.cancel != nil {
		s.cancel()
	}
	if s.worker != nil {
		s.worker.close()
		s.worker = nil
	}
}

func (m *workspaceModel) renderFolderSearch(view *html.View) {
	s := &m.folderSearch
	if !s.open {
		view.Attr("folder-search", "hidden", "")
		return
	}
	view.RemoveAttr("folder-search", "hidden")
	view.Attr("content", "disabled", "")
	view.Text("folder-search-scope", s.project.Name+" / "+s.directory)
	view.Text("folder-search-status", s.message)
	view.Attr("folder-search-query", "value", s.query)
	view.Attr("folder-search-replacement", "value", s.replacement)
	renderReplaceToggle(view, "folder-search-toggle", s.replace)
	renderFindToggle(view, "folder-search-preserve", s.options.PreserveCase)
	if s.replace {
		view.RemoveAttr("folder-search-replace-row", "hidden")
	} else {
		view.Attr("folder-search-replace-row", "hidden", "")
	}
	view.Event("folder-search-query", html.EventInput)
	view.Event("folder-search-replacement", html.EventInput)
	for _, ref := range [...]string{"folder-search-toggle", "folder-search-close", "folder-search-run", "folder-search-one", "folder-search-all", "folder-search-previous", "folder-search-next", "folder-search-preserve"} {
		view.Event(ref, html.EventClick)
	}
	renderSearchOptions(view, pencodeFolderOptionRefs, s.options)
	for _, ref := range [...]string{"folder-search-one", "folder-search-all"} {
		enabled := !s.busy && !s.replacing && len(s.rows) > 0
		if enabled {
			view.RemoveAttr(ref, "disabled")
		} else {
			view.Attr(ref, "disabled", "")
		}
	}
	for _, ref := range [...]string{"folder-search-query", "folder-search-replacement", "folder-search-case", "folder-search-word", "folder-search-regex", "folder-search-run", "folder-search-preserve"} {
		if s.replacing {
			view.Attr(ref, "disabled", "")
		} else {
			view.RemoveAttr(ref, "disabled")
		}
	}
	m.highlightFolderSearch()
	if s.icons != m.fileIcons || s.theme != m.workbenchTheme {
		s.icons, s.theme, s.dirty = m.fileIcons, m.workbenchTheme, true
	}
	if s.dirty {
		b := &s.view
		b.reset()
		// Bound retained DOM work independently of the result budget.
		first := s.index / folderSearchPageSize * folderSearchPageSize
		pages := (len(s.rows) + folderSearchPageSize - 1) / folderSearchPageSize
		s.pageLabel = fmt.Sprintf("%d / %d", min(1+s.index/folderSearchPageSize, pages), pages)
		for i := first; i < min(first+folderSearchPageSize, len(s.rows)); i++ {
			row := s.rows[i]
			file := s.result.Files[row.file]
			match := file.Matches[row.match]
			id := "folder-search-result-" + strconv.Itoa(i)
			class := "pencode-folder-result"
			if i == s.index {
				class += " active"
			}
			b.open("button", id, "id", id, "type", "button", "class", class, "role", "option", "aria-selected", boolAttribute(i == s.index))
			b.open("span", id+"-location", "class", "pencode-folder-location")
			source := m.fileIcons.source(path.Base(file.Path), false)
			if source == "" {
				source = "icons/file-text.svg"
			}
			b.text("img", id+"-icon", "", "src", m.themedIcon(source, "icon.foreground"), "class", "pencode-folder-file-icon", "alt", "", "aria-hidden", "true")
			b.text("span", id+"-path", fmt.Sprintf("%s:%d:%d", file.Path, match.Line, match.Column), "class", "pencode-folder-path")
			b.close()
			b.open("span", id+"-preview", "class", "pencode-folder-preview")
			renderFolderSearchPreview(b, match, s.preview.tokens(i-first))
			b.close()
			b.close()
		}
		s.nodes, s.dirty = b.nodes(), false
	}
	view.Keyed("folder-search-results", s.nodes)
	view.Text("folder-search-page", s.pageLabel)
	for _, ref := range [...]string{"folder-search-previous", "folder-search-next"} {
		if len(s.rows) <= folderSearchPageSize {
			view.Attr(ref, "disabled", "")
		} else {
			view.RemoveAttr(ref, "disabled")
		}
	}
	for _, ref := range s.view.clicks {
		view.Event(ref, html.EventClick)
	}
}

func (m *workspaceModel) focusFolderSearch(world *module.Content) {
	s := &m.folderSearch
	m.revealFolderSearchResult(world)
	if s.navigate != nil && !s.open && m.editor.file == s.navigate && m.activeFile() == s.navigate && world.Focus(m.editor.control) {
		if field := module.ContentMut[engineui.TextField](world, m.editor.control); field != nil {
			field.Anchor, field.Caret = s.navigateStart, s.navigateEnd
		}
		m.revealEditorCaret(world, s.navigateEnd)
		s.navigate = nil
	}
	if !s.open || !s.needsFocus {
		return
	}
	for _, ref := range [...]string{"folder-search-query", "folder-search-replacement"} {
		if entity, ok := world.HTMLControl(m.surface, ref); ok && !module.ContentHas[engineui.TextFieldKeyHandler](world, entity) {
			_ = module.ContentInsert(world, entity, engineui.TextFieldKeyHandler{Handle: func(state *input.State) bool {
				return s.open && !state.Composing() && (m.uiKey(state, input.KeyUp) || m.uiKey(state, input.KeyDown) || m.uiKey(state, input.KeyEnter) || m.uiKey(state, input.KeyEscape) || m.uiKey(state, input.KeyPageUp) || m.uiKey(state, input.KeyPageDown))
			}})
		}
	}
	if entity, ok := world.HTMLControl(m.surface, "folder-search-query"); ok && module.ContentHas[engineui.TextField](world, entity) && world.Focus(entity) {
		s.needsFocus = false
		if field := module.ContentMut[engineui.TextField](world, entity); field != nil {
			field.Anchor = 0
		}
	}
}

func (m *workspaceModel) revealFolderSearchResult(world *module.Content) {
	s := &m.folderSearch
	if !s.open || !s.revealResult || len(s.rows) == 0 {
		return
	}
	list, ok := world.HTMLNode(m.surface, "folder-search-results")
	if !ok {
		return
	}
	item, ok := world.HTMLNode(m.surface, "folder-search-result-"+strconv.Itoa(s.index))
	if !ok {
		return
	}
	area, ok := list.ScrollContainer()
	if !ok {
		return
	}
	if scroll := module.ContentMut[scene.UIScroll](world, area); scroll != nil {
		bounds, _ := list.Bounds()
		row, _ := item.Bounds()
		top, bottom := row.Y-bounds.Y, row.Y+row.Height-bounds.Y
		position := scroll.Position[1]
		if top+position < 0 {
			position = -top
		} else if bottom+position > bounds.Height {
			position = bounds.Height - bottom
		}
		scroll.Position[1] = min(0, max(-max(0, scroll.ContentSize[1]-scroll.ViewportSize[1]), position))
		scroll.Momentum[1] = 0
		s.revealResult = false
	}
}

func (m *workspaceModel) advanceFolderSearch(step int) {
	s := &m.folderSearch
	if len(s.rows) > 0 {
		if step == folderSearchPageSize || step == -folderSearchPageSize {
			pages := (len(s.rows) + folderSearchPageSize - 1) / folderSearchPageSize
			s.index = (s.index/folderSearchPageSize + step/folderSearchPageSize + pages) % pages * folderSearchPageSize
		} else {
			s.index = (s.index + step%len(s.rows) + len(s.rows)) % len(s.rows)
		}
		s.dirty, s.revealResult = true, true
	}
}

func (m *workspaceModel) openFolderSearchMatch(world *module.Content, index int) {
	s := &m.folderSearch
	if index < 0 || index >= len(s.rows) || s.busy || s.replacing {
		return
	}
	row := s.rows[index]
	file := s.result.Files[row.file]
	match := file.Matches[row.match]
	navigation := &fileNavigation{searchMatch: &match, digest: file.Digest}
	m.activateProject(s.project)
	if err := m.requestFileOpen(s.project, file.Path, false, true, navigation); err != nil {
		s.message = err.Error()
		return
	}
	m.closeFolderSearch(world)
}

func (m *workspaceModel) finishSearchNavigation(project *pencodeProject, file *openFile, n *fileNavigation) {
	sum := sha256.Sum256([]byte(file.Content))
	if string(sum[:]) != n.digest {
		m.setProjectStatus(project, "文件已改变，请重新搜索")
		return
	}
	match := n.searchMatch
	s := &m.folderSearch
	s.navigate, s.navigateStart, s.navigateEnd = file, utf8.RuneCountInString(file.Content[:match.Start]), utf8.RuneCountInString(file.Content[:match.End])
	file.editorView.anchor, file.editorView.caret = s.navigateStart, s.navigateEnd
	m.editor.needsFocus = true
}

func (m *workspaceModel) handleFolderSearchEvent(world *module.Content, ref, value string) bool {
	if !strings.HasPrefix(ref, "folder-search-") {
		return false
	}
	s := &m.folderSearch
	if !s.open {
		return true
	}
	if ref == "folder-search-close" {
		m.closeFolderSearch(world)
		return true
	}
	if s.replacing {
		return true
	}
	switch ref {
	case "folder-search-query":
		s.query = value
		m.startFolderSearch()
	case "folder-search-replacement":
		s.replacement = value
	case "folder-search-preserve":
		s.options.PreserveCase = !s.options.PreserveCase
	case "folder-search-toggle":
		s.replace = !s.replace
	case "folder-search-run":
		m.startFolderSearch()
	case "folder-search-case":
		s.options.CaseSensitive = !s.options.CaseSensitive
		m.startFolderSearch()
	case "folder-search-word":
		s.options.WholeWord = !s.options.WholeWord
		m.startFolderSearch()
	case "folder-search-regex":
		s.options.Regex = !s.options.Regex
		m.startFolderSearch()
	case "folder-search-one":
		m.replaceFolderSearch(false)
	case "folder-search-all":
		m.replaceFolderSearch(true)
	case "folder-search-previous":
		m.advanceFolderSearch(-folderSearchPageSize)
	case "folder-search-next":
		m.advanceFolderSearch(folderSearchPageSize)
	default:
		if suffix, ok := strings.CutPrefix(ref, "folder-search-result-"); ok {
			if index, err := strconv.Atoi(suffix); err == nil {
				m.openFolderSearchMatch(world, index)
			}
		}
	}
	return true
}

func (m *workspaceModel) folderSearchKeys(world *module.Content, state *input.State) bool {
	if m.uiKey(state, input.KeyEscape) {
		m.closeFolderSearch(world)
		return true
	}
	if m.folderSearch.replacing {
		return false
	}
	if option := m.searchOptionKey(state); option >= 0 {
		toggleSearchOption(&m.folderSearch.options, option)
		if option != 3 {
			m.startFolderSearch()
		}
		return true
	}
	switch {
	case m.uiKey(state, input.KeyDown):
		m.advanceFolderSearch(1)
	case m.uiKey(state, input.KeyUp):
		m.advanceFolderSearch(-1)
	case m.uiKey(state, input.KeyPageDown):
		m.advanceFolderSearch(folderSearchPageSize)
	case m.uiKey(state, input.KeyPageUp):
		m.advanceFolderSearch(-folderSearchPageSize)
	case m.uiKey(state, input.KeyEnter), m.commandMatches("search-replace-all", state, runtime.GOOS):
		if pencodeControlFocused(world, m.surface, "folder-search-replacement") {
			m.replaceFolderSearch(m.commandMatches("search-replace-all", state, runtime.GOOS))
		} else {
			m.openFolderSearchMatch(world, m.folderSearch.index)
		}
	default:
		return false
	}
	return true
}

type folderReplaceTarget struct {
	file                        *openFile
	path, digest, before, after string
	start, end, count           int
	err                         error
	processed                   bool
}
type folderReplacePlan struct {
	query, replacement string
	options            textsearch.Options
	all                bool
	targets            []folderReplaceTarget
}

func (m *workspaceModel) replaceFolderSearch(all bool) {
	s := &m.folderSearch
	if s.busy || s.replacing || len(s.rows) == 0 || !m.projectLive(s.project) {
		return
	}
	if m.fileIOMutating(s.project) {
		s.message = "请等待文件操作完成"
		return
	}
	if m.fileIO != nil {
		for _, job := range m.fileIO.pending {
			if job.project == s.project || job.target == s.project {
				s.message = "请等待文件操作完成"
				return
			}
		}
	}
	plan := &folderReplacePlan{query: s.query, replacement: s.replacement, options: s.options, all: all}
	add := func(file protocol.SearchFile, start, end int) {
		t := folderReplaceTarget{path: file.Path, digest: file.Digest, start: start, end: end}
		t.file = s.project.Files[file.Path]
		if t.file != nil {
			t.before = t.file.Content
		}
		plan.targets = append(plan.targets, t)
	}
	if all {
		for _, file := range s.result.Files {
			add(file, 0, 0)
		}
	} else {
		row := s.rows[s.index]
		file := s.result.Files[row.file]
		match := file.Matches[row.match]
		add(file, match.Start, match.End)
	}
	if err := m.submitFileIO(&fileIOJob{kind: fileSearchReplace, project: s.project, root: s.project.Path, searchReplace: plan}); err != nil {
		s.message = err.Error()
		return
	}
	s.replacing, s.message = true, "正在替换…"
}

func executeFolderReplace(j *fileIOJob) error {
	plan := j.searchReplace
	p, err := textsearch.Compile(plan.query, plan.options)
	if err != nil {
		return err
	}
	var matches []textsearch.Match
	for i := range plan.targets {
		t := &plan.targets[i]
		if err := j.ctx.Err(); err != nil {
			return err
		}
		if t.file == nil {
			data, err := j.backend.ReadFile(j.ctx, j.root, t.path)
			if err != nil {
				t.err = err
				continue
			}
			t.before = string(data)
		}
		sum := sha256.Sum256([]byte(t.before))
		if string(sum[:]) != t.digest {
			t.err = fmt.Errorf("文件已改变，请重新搜索")
			continue
		}
		matches = p.FindAll(t.before, matches, -1)
		if !plan.all {
			found := false
			for _, match := range matches {
				if match.Start == t.start && match.End == t.end {
					matches = append(matches[:0], match)
					found = true
					break
				}
			}
			if !found {
				t.err = fmt.Errorf("匹配已改变，请重新搜索")
				continue
			}
		}
		t.count = len(matches)
		t.after = p.Replace(t.before, plan.replacement, matches)
		if t.file == nil && t.before != t.after {
			t.err = j.backend.SaveFile(j.ctx, j.root, t.path, t.before, t.after)
		}
		if plan.all && t.file == nil {
			t.before, t.after = "", ""
		}
		t.processed = true
	}
	return nil
}

func (m *workspaceModel) acceptFolderReplace(r *fileIOResult) {
	j := r.job
	s := &m.folderSearch
	s.replacing = false
	count, failed := 0, 0
	message := ""
	for _, t := range j.searchReplace.targets {
		err := t.err
		if err == nil && !t.processed {
			err = r.err
			if err == nil {
				err = fmt.Errorf("替换未完成")
			}
		}
		if err == nil && !m.projectLive(j.project) {
			err = fmt.Errorf("项目已关闭")
		}
		if err == nil && t.file != nil {
			if j.project.Files[t.path] != t.file || t.file.Content != t.before {
				err = fmt.Errorf("文档已改变，保留当前编辑")
			} else {
				m.applyFileContent(t.file, j.project, t.after, true)
			}
		}
		if err != nil {
			failed++
			message = t.path + "：" + err.Error()
		} else {
			count += t.count
			if !j.searchReplace.all {
				s.nextPath, s.nextOffset = t.path, t.end+len(t.after)-len(t.before)
				if t.start == t.end {
					_, size := utf8.DecodeRuneInString(t.after[min(s.nextOffset, len(t.after)):])
					s.nextOffset += max(1, size)
				}
			}
		}
	}
	if m.projectLive(j.project) {
		m.syncActiveProjectFields()
		m.syncLanguageDocuments()
	}
	if m.fontResources != nil {
		setPencodeEditorValue(m.fontResources, m.surface, m.activeContent())
	}
	if s.open {
		m.startFolderSearch()
	}
	status := fmt.Sprintf("已替换 %d 处", count)
	if failed > 0 {
		status += fmt.Sprintf("；%d 个文件失败：%s", failed, message)
	}
	s.message = status
	m.setProjectStatus(j.project, status)
}
