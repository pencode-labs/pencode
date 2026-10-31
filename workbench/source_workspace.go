package workbench

import (
	"context"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

type sourceJobKind uint8

const (
	sourceQuery sourceJobKind = iota
	sourceWrite
	sourceRefresh
	sourceSync
)

type sourceResult struct {
	Repositories      []string
	Selection         uint64
	Page              *sourceHistoryPage
	PageRefs          string
	PageOffset        int
	Snapshot          *sourceSnapshot
	StatusOnly        bool
	Files             []sourceFile
	Hash              string
	Tab               *sourceTab
	SavedKey, Message string
	Err               error
	RepositoryError   string
	Reload            bool
}

type sourceClickBinding struct {
	Node    htmlui.Node
	Handler htmlui.ClickHandler
}

type sourcePanelKey struct {
	Theme                                    *pencodeTheme
	Icons                                    *pencodeFileIcons
	Files, CommitFiles                       *sourceFile
	Commits                                  *sourceCommit
	FileCount, CommitCount, CommitFileCount  int
	First, Visible                           int
	TreeRevision                             uint64
	Sync                                     sourceSyncStatus
	Message, Selected                        string
	RepositoryError                          string
	Busy, Modal, HasMore, Multiline, Syncing bool
}

type sourceToolsKey struct {
	Theme           *pencodeTheme
	Branch          string
	Busy            bool
	BranchMenu      bool
	Unavailable     bool
	Repositories    *string
	RepositoryCount int
	Repository      string
	RepositoryMenu  bool
}

func sourceFirst[T any](values []T) *T {
	if len(values) != 0 {
		return &values[0]
	}
	return nil
}

// UI state belongs to one project. A job only receives captured immutable values;
// its bounded result is applied by the foreground, including for inactive projects.
type pencodeSourceControl struct {
	Repositories                            []string
	RepositoriesLoaded                      bool
	RepositoryViews                         map[string]sourceRepositoryView
	FollowPath                              string
	RepositoryMenuOpen, RepositoryFocus     bool
	RepositoryIndex                         int
	RepositoryX, RepositoryY                float32
	RepositoryWidth, RepositoryHeight       float32
	repositoryView                          sourceVNodeView
	repositoryKey                           sourceRepositoryMenuKey
	Snapshot                                sourceSnapshot
	RepositoryError                         string
	Done                                    chan sourceResult
	JobKind                                 sourceJobKind
	RefreshCancel                           context.CancelFunc
	Cancel                                  context.CancelFunc
	Context                                 context.Context
	RefreshPending, RefreshStarted          bool
	CommitMessage, SelectedHash             string
	MessageMultiline, MessageFocus          bool
	MessageAnchor, MessageCaret             int
	CommitFiles                             []sourceFile
	commitTree                              []sourceTreeRow
	commitTreeFiles                         *sourceFile
	commitTreeCount                         int
	commitTreeRevision                      uint64
	Tabs                                    []*sourceTab
	Active                                  string
	MenuHash                                string
	BranchMenuOpen                          bool
	BranchHeight, BranchWidth               float32
	BranchIndex                             int
	BranchFocus                             bool
	BranchSubmenu                           bool
	BranchSubIndex                          int
	BranchSubX, BranchSubY, BranchSubWidth  float32
	branchView                              sourceVNodeView
	branchKey                               sourceBranchMenuKey
	MenuOpen                                bool
	MenuX, MenuY                            float32
	Prompt, PromptHash, PromptValue, Notice string
	Collapsed                               map[string]bool
	RestoreScroll                           bool
	FocusedModal, FocusedTab                string
	view, tabsView, overlayView             sourceVNodeView
	panelView, toolsView                    sourceVNodeView
	panelKey                                sourcePanelKey
	toolsKey                                sourceToolsKey
	changeCount                             int
	changeCountText                         string
	TreeRevision                            uint64
	GraphFirst, GraphVisible                int
	ExpandedCommit                          int
	GraphScrollY                            float32
	RestoreGraph                            bool
	RenderRevision, BoundRevision           uint64
	Selection                               uint64
	Handlers                                map[string]sourceClickBinding
	Owner                                   *workspaceModel
	mergeEditor                             pencodeEditor
	diffEditors                             [2]pencodeEditor
	inlinePaint                             [2]sourceInlinePaint
	diffPaneTab                             *sourceTab
	gutterTab                               *sourceTab
	gutterY                                 float32
	gutterRevision                          uint64
}

func (s *pencodeSourceControl) activeTab() *sourceTab {
	for _, t := range s.Tabs {
		if t.Key == s.Active {
			return t
		}
	}
	return nil
}
func (s *pencodeSourceControl) unsaved() bool {
	for _, tab := range s.Tabs {
		if tab.Result != tab.Saved {
			return true
		}
	}
	return false
}
func (s *pencodeSourceControl) unsavedRepositories() bool {
	if s.unsaved() {
		return true
	}
	for _, view := range s.RepositoryViews {
		for _, tab := range view.Tabs {
			if tab.Result != tab.Saved {
				return true
			}
		}
	}
	return false
}
func (s *pencodeSourceControl) busy() bool {
	return s.Done != nil && (s.JobKind != sourceRefresh || s.Snapshot.Repository.Root == "")
}

func (s *pencodeSourceControl) cancelRefresh() {
	if s.RefreshCancel != nil {
		s.RefreshCancel()
		s.RefreshCancel = nil
		s.Done = nil
	}
}

func (s *pencodeSourceControl) modal() bool   { return s.Prompt != "" || s.Notice != "" }
func (s *pencodeSourceControl) visible() bool { return s.Active != "" }
func (s *pencodeSourceControl) stop() {
	s.cancelRefresh()
	if s.Cancel != nil {
		s.Cancel()
		s.Cancel = nil
	}
	s.Context = nil
	s.Done = nil
	s.RepositoriesLoaded = false
	s.RefreshPending, s.RefreshStarted = false, false
}
func (m *workspaceModel) sourceState() *pencodeSourceControl {
	if p := m.activeProjectState(); p != nil {
		return &p.sourceControl
	}
	return &m.sourceControl
}
func (m *workspaceModel) sourceChanged() {
	if m.fontResources != nil {
		markPencodeRootDirtyByModel(m.fontResources, m)
	}
}
func (m *workspaceModel) sourceHasDrafts() bool {
	// Repository roots can enclose more than one open project.
	root := m.sourceState().Snapshot.Repository.Root
	for _, p := range m.projects {
		for _, f := range p.Files {
			if f.Dirty && sourceRepositoryForPath([]string{root}, filepath.ToSlash(filepath.Join(p.Path, filepath.FromSlash(f.Path)))) != "" {
				return true
			}
		}
		if sourceRepositoryForPath([]string{root}, filepath.ToSlash(p.Path)) != "" && p.sourceControl.unsavedRepositories() {
			return true
		}
	}
	return false
}
func (m *workspaceModel) sourceJob(kind sourceJobKind, job func() sourceResult) {
	s := m.sourceState()
	if s.Done != nil {
		return
	}
	if s.Context == nil {
		s.Context, s.Cancel = context.WithCancel(context.Background())
	}
	ctx, done, wake := s.Context, make(chan sourceResult, 1), m.backgroundWake()
	selection := s.Selection
	s.Done, s.JobKind = done, kind
	m.sourceChanged()
	go func() {
		result := job()
		result.Selection = selection
		done <- result
		if ctx.Err() == nil && wake != nil {
			wake()
		}
	}()
}
func (m *workspaceModel) refreshSourceControl() {
	s := m.sourceState()
	if s.Done != nil || m.projectPath == "" {
		return
	}
	s.RefreshPending, s.RefreshStarted = false, true
	if s.Context == nil {
		s.Context, s.Cancel = context.WithCancel(context.Background())
	}
	directory := s.Snapshot.Repository.Root
	if directory == "" {
		directory = m.projectPath
	}
	project, discover := m.projectPath, !s.RepositoriesLoaded || s.Snapshot.Repository.Root == ""
	activePath := m.activePath
	if s.FollowPath == activePath && s.Snapshot.Repository.Root != "" {
		activePath = ""
	}
	backend := m.backend
	statusOnly := !s.visible() && m.bottom.active != pencodePanelSourceControl && !s.modal()
	ctx, cancel := context.WithCancel(s.Context)
	s.RefreshCancel = cancel
	m.sourceJob(sourceRefresh, func() sourceResult {
		r := sourceRepository{Root: directory, Context: ctx, Backend: backend}
		var repositories []string
		if discover {
			var err error
			repositories, err = backend.GitRepositories(ctx, project)
			if err != nil {
				return sourceResult{RepositoryError: err.Error()}
			}
			if len(repositories) == 0 {
				return sourceResult{Repositories: repositories, RepositoryError: "No Git repositories found"}
			}
			if !slices.Contains(repositories, directory) {
				directory = repositories[0]
			}
			if activePath != "" {
				if root := sourceRepositoryForPath(repositories, filepath.ToSlash(filepath.Join(project, filepath.FromSlash(activePath)))); root != "" {
					directory = root
				}
			}
			r.Root = directory
		}
		if statusOnly {
			snapshot, err := r.statusSnapshot()
			return sourceResult{Repositories: repositories, Snapshot: &snapshot, Err: err, StatusOnly: true}
		}
		snapshot, err := r.snapshot(sourceHistoryPageSize)
		return sourceResult{Repositories: repositories, Snapshot: &snapshot, Err: err}
	})
}
func (m *workspaceModel) pollSourceResult(s *pencodeSourceControl, project *pencodeProject) bool {
	if s.Done == nil {
		return false
	}
	select {
	case result := <-s.Done:
		s.Done = nil
		s.cancelRefresh()
		if result.Repositories != nil {
			if !slices.Equal(result.Repositories, s.Repositories) {
				s.Repositories = result.Repositories
				s.FollowPath = ""
			}
			s.RepositoriesLoaded = true
		}
		if result.RepositoryError != "" {
			if s.Snapshot.Repository.Root != "" {
				s.selectRepository(sourceRepository{})
			}
			s.RepositoryError = result.RepositoryError
			s.Snapshot = sourceSnapshot{}
			s.SelectedHash, s.CommitFiles = "", nil
			s.GraphFirst, s.GraphScrollY, s.RestoreGraph = 0, 0, true
			s.MenuOpen, s.BranchMenuOpen = false, false
		}
		if result.Snapshot != nil && result.Snapshot.Repository.Root != "" {
			if result.Snapshot.Repository.Root != s.Snapshot.Repository.Root {
				visible := s.visible()
				s.selectRepository(result.Snapshot.Repository)
				if !visible {
					s.Active, s.RestoreScroll = "", false
				}
			}
			s.RepositoryError = ""
			next := *result.Snapshot
			if result.StatusOnly {
				next = s.Snapshot
				next.Repository, next.Files, next.Groups = result.Snapshot.Repository, result.Snapshot.Files, result.Snapshot.Groups
			}
			next.Repository.Context = s.Context
			if slices.Equal(next.Branches, s.Snapshot.Branches) {
				next.Branches = s.Snapshot.Branches
			}
			if slices.Equal(next.Files, s.Snapshot.Files) {
				next.Files, next.Groups = s.Snapshot.Files, s.Snapshot.Groups
			}
			if next.HistoryRefs == s.Snapshot.HistoryRefs && len(s.Snapshot.Commits) >= len(next.Commits) {
				next.sourceHistoryPage = s.Snapshot.sourceHistoryPage
			} else if next.HistoryRefs != s.Snapshot.HistoryRefs {
				s.GraphFirst, s.GraphScrollY, s.RestoreGraph = 0, 0, true
				s.SelectedHash, s.CommitFiles = "", nil
			}
			s.Snapshot = next
		}
		if result.Page != nil && result.Err == nil && result.PageRefs == s.Snapshot.HistoryRefs && result.PageOffset == len(s.Snapshot.Commits) {
			s.Snapshot.Commits = append(s.Snapshot.Commits, result.Page.Commits...)
			s.Snapshot.Graph, s.Snapshot.HasMore = result.Page.Graph, result.Page.HasMore
		}
		if result.Err != nil && !result.StatusOnly {
			s.Notice = result.Err.Error()
		}
		if result.Hash != "" && result.Hash == s.SelectedHash && result.Err == nil {
			height := s.graphDetailsHeight()
			s.CommitFiles = result.Files
			s.resizeGraphDetails(height)
		}
		if tab := result.Tab; tab != nil && result.Err == nil {
			found := false
			for i, old := range s.Tabs {
				if old.Key == tab.Key {
					if old.Result != old.Saved {
						tab = old
					} else {
						tab.ScrollX, tab.ScrollY, tab.FirstRow = old.ScrollX, old.ScrollY, old.FirstRow
						s.Tabs[i] = tab
					}
					found = true
					break
				}
			}
			if !found {
				s.Tabs = append(s.Tabs, tab)
			}
			if result.Selection == s.Selection {
				m.actions.ui.editor = nil
				s.Active, s.RestoreScroll = tab.Key, true
			}
		}
		if result.SavedKey != "" {
			for _, tab := range s.Tabs {
				if tab.Key == result.SavedKey {
					tab.Saved, tab.Disk = tab.Result, tab.Result
				}
			}
		}
		if result.Message != "" && result.Err == nil {
			if project != nil {
				m.setProjectStatus(project, result.Message)
			} else {
				m.status = result.Message
			}
			if result.Message == "Commit created" {
				s.CommitMessage = ""
				s.MessageMultiline, s.MessageFocus = false, false
				s.MessageAnchor, s.MessageCaret = 0, 0
				if m.fontResources != nil && s == m.sourceState() {
					if entity, ok := m.fontResources.HTMLControl(m.surface, "sc-message"); ok {
						m.fontResources.SetControlValue(entity, "")
					}
				}
			}
		}
		if result.Reload && project != nil {
			m.reloadSourceFiles(project)
		}
		return true
	default:
		return false
	}
}

// Refresh only clean buffers after a workspace mutation. An edit made while Git
// was running must never be overwritten by the completion callback.
func (m *workspaceModel) reloadSourceFiles(project *pencodeProject) {
	m.startProjectScan(project, false)
	if err := m.requestFileReload(project, true); err != nil {
		m.setProjectStatus(project, err.Error())
	}
}

func (m *workspaceModel) updateSourceControl(world *module.Content, state *input.State) bool {
	changed := false
	for _, p := range m.projects {
		if m.pollSourceResult(&p.sourceControl, p) {
			changed = true
		}
	}
	if len(m.projects) == 0 && m.pollSourceResult(&m.sourceControl, nil) {
		changed = true
	}
	s := m.sourceState()
	if m.followSourceRepository() {
		changed = true
	}
	if (s.MenuOpen || s.BranchMenuOpen || s.RepositoryMenuOpen) && m.bottom.active != pencodePanelSourceControl {
		s.MenuOpen, s.BranchMenuOpen, s.RepositoryMenuOpen = false, false, false
		changed = true
	}
	if m.projectPath != "" && (s.RefreshPending || !s.RefreshStarted) && s.Done == nil && !s.modal() && !s.BranchMenuOpen && !s.MenuOpen && !s.RepositoryMenuOpen {
		m.refreshSourceControl()
		changed = true
	}
	if !s.visible() && m.bottom.active != pencodePanelSourceControl && !s.modal() {
		return changed
	}
	if s.BranchMenuOpen {
		x, y, width, height := s.MenuX, s.MenuY, s.BranchWidth, s.BranchHeight
		m.positionSourceBranches(world)
		changed = changed || x != s.MenuX || y != s.MenuY || width != s.BranchWidth || height != s.BranchHeight
	}
	if s.RepositoryMenuOpen {
		x, y, width, height := s.RepositoryX, s.RepositoryY, s.RepositoryWidth, s.RepositoryHeight
		m.positionSourceRepositories(world)
		changed = changed || x != s.RepositoryX || y != s.RepositoryY || width != s.RepositoryWidth || height != s.RepositoryHeight
	}
	if m.bottom.active == pencodePanelSourceControl && m.syncSourceGraph(world) {
		changed = true
	}
	if state == nil {
		return changed
	}
	pointer := state.Pointer()
	zoom := max(float32(m.settings.Zoom)/100, .01)
	x, y := pointer.X/zoom, pointer.Y/zoom
	if s.RepositoryMenuOpen && (state.MousePressed(input.MouseLeft) || state.MousePressed(input.MouseRight)) && !m.pointerInsideRefs(world, x, y, []string{"sc-repository-menu", "sc-repositories"}) {
		s.RepositoryMenuOpen = false
		changed = true
	}
	if s.BranchMenuOpen && (state.MousePressed(input.MouseLeft) || state.MousePressed(input.MouseRight)) && !m.pointerInsideRefs(world, x, y, []string{"sc-branch-menu", "sc-branch-submenu", "sc-branches"}) {
		s.BranchMenuOpen = false
		changed = true
	}
	if s.MenuOpen && (state.MousePressed(input.MouseLeft) || state.MousePressed(input.MouseRight)) && !m.pointerInsideRefs(world, x, y, []string{"sc-context-menu"}) {
		s.MenuOpen = false
		changed = true
	}
	if !s.modal() && !s.busy() && s.BranchMenuOpen {
		if pointer.DeltaX != 0 || pointer.DeltaY != 0 || pointer.WheelY != 0 || state.MousePressed(input.MouseLeft) || state.MousePressed(input.MouseRight) {
			changed = m.hoverSourceBranches(world, x, y) || changed
		}
		changed = m.positionSourceBranchSubmenu(world) || changed
	}
	if !s.modal() && m.bottom.active == pencodePanelSourceControl && state.MousePressed(input.MouseRight) && m.pointerInsideRefs(world, x, y, []string{"sc-graph"}) {
		var offset float32
		if node, ok := world.HTMLNode(m.surface, "sc-graph"); ok {
			if entity, ok := node.ScrollContainer(); ok {
				if scroll, ok := world.Get[scene.UIScroll](entity); ok {
					offset = scroll.Position[1]
				}
			}
		}
		for _, c := range s.graphWindow() {
			if node, ok := world.HTMLNode(m.surface, "sc-commit:"+c.Hash); ok {
				bounds, ok := node.Bounds()
				bounds.Y += offset
				if ok && languageRectContains(bounds, x, y) {
					root, _ := world.HTMLNode(m.surface, "pencode-root")
					area, _ := root.Bounds()
					s.MenuHash, s.MenuOpen, s.BranchMenuOpen = c.Hash, true, false
					s.RepositoryMenuOpen = false
					s.MenuX, s.MenuY = max(0, min(x-area.X, area.Width-270)), max(0, min(y-area.Y, area.Height-300))
					changed = true
					break
				}
			}
		}
	}
	return changed
}

func (m *workspaceModel) handleSourceEvent(event htmlui.Event) bool {
	id := event.ID
	if id == "status-source-control" {
		id = "sc-toggle"
	}
	if !strings.HasPrefix(id, "sc-") {
		return false
	}
	s := m.sourceState()
	switch event.Kind {
	case htmlui.EventInput, htmlui.EventChange:
		switch id {
		case "sc-message":
			// A replaced single-line input can still have a queued change event.
			if s.MessageFocus {
				return true
			}
			if m.fontResources != nil && event.Target != 0 {
				if current, ok := m.fontResources.HTMLControl(m.surface, id); !ok || current != event.Target {
					return true
				}
			}
			s.CommitMessage = event.Value
			if s.MessageMultiline && !strings.ContainsRune(event.Value, '\n') {
				s.MessageMultiline = false
				if m.fontResources != nil {
					if field, ok := m.fontResources.Get[engineui.TextField](event.Target); ok && field.Focused {
						s.MessageFocus = true
						s.MessageAnchor, s.MessageCaret = field.Anchor, field.Caret
					}
				}
			}
		case "sc-prompt-value":
			s.PromptValue = event.Value
		case "sc-merge-result":
			if t := s.activeTab(); t != nil && !s.busy() {
				value := event.Value
				if event.Document {
					value = event.Snapshot.String()
				}
				t.editResult(value)
			}
		case "sc-code-right-input":
			if t := s.activeTab(); t != nil && t.editable() && !s.busy() {
				value := event.Value
				if event.Document {
					value = event.Snapshot.String()
				}
				t.editResult(value)
			}
		}
	case htmlui.EventClick:
		m.sourceAction(id)
	}
	return true
}

func (m *workspaceModel) renderSourceWorkspace(view *htmlui.View) {
	s := m.sourceState()
	s.RenderRevision++
	if count := len(s.Snapshot.Files); s.changeCount != count || s.changeCountText == "" {
		s.changeCount, s.changeCountText = count, strconv.Itoa(count)
	}
	view.Text("status-source-count", s.changeCountText)
	view.Event("status-source-control", htmlui.EventClick)
	view.Attr("status-source-control", "aria-expanded", strconv.FormatBool(m.bottom.active == pencodePanelSourceControl))
	for _, ref := range []string{"source-content", "source-tabs", "source-overlay", "source-pane", "source-tools"} {
		view.Event(ref, htmlui.EventInput)
		view.Event(ref, htmlui.EventChange)
	}
	if m.projectPath == "" {
		view.Attr("status-source-control", "disabled", "")
	} else {
		view.RemoveAttr("status-source-control", "disabled")
	}
	setPanelVisible(view, "source-content", s.visible())
	if s.visible() {
		view.Attr("editor-body", "class", "pencode-editor-body source-active")
		if tab := s.activeTab(); tab != nil {
			// Git paths are relative to the repository, which may enclose the project.
			full := filepath.Join(s.Snapshot.Repository.Root, filepath.FromSlash(tab.Path))
			path := tab.Path
			if relative, err := filepath.Rel(m.projectPath, full); err == nil {
				path = filepath.ToSlash(relative)
			}
			m.renderEditorPath(view, path)
		}
		view.Attr("editor-empty", "hidden", "")
		b := &s.view
		b.reset()
		m.renderSourceTabs(b)
		view.Keyed("source-content", b.nodes())
	} else {
		view.Attr("editor-body", "class", "pencode-editor-body")
	}
	b := &s.tabsView
	b.reset()
	for _, tab := range s.Tabs {
		class := "pencode-tab"
		if s.Active == tab.Key {
			class += " active"
		}
		label := path.Base(tab.Path) + " · " + tab.Kind
		if tab.Kind == "history" {
			label += " · " + tab.Revision[:8]
		}
		if tab.Result != tab.Saved {
			class += " dirty"
		}
		foreground := "tab.inactiveForeground"
		if tab.Key == s.Active {
			foreground = "tab.activeForeground"
		}
		icon := "icons/file-text.svg"
		if m.fileIcons != nil {
			if themed := m.fileIcons.source(path.Base(tab.Path), false); themed != "" {
				icon = themed
			}
		}
		b.open("div", tab.Key, "id", "sc-tab-group:"+tab.Key, "class", class, "title", tab.Path)
		b.open("button", "select", "id", "sc-tab:"+tab.Key, "type", "button", "class", "pencode-tab-select", "role", "tab", "aria-selected", strconv.FormatBool(tab.Key == s.Active), "aria-controls", "source-content")
		b.text("img", "icon", "", "class", "pencode-tab-icon", "src", m.themedIcon(icon, foreground), "alt", "")
		b.text("span", "label", label, "class", "pencode-tab-name")
		b.close()
		b.open("button", "close", "id", "sc-close-tab:"+tab.Key, "type", "button", "class", "pencode-tab-close", "title", "关闭比较", "aria-label", "关闭 "+label)
		if s.busy() {
			b.attr("disabled", "")
		}
		if tab.Result != tab.Saved {
			b.text("span", "dot", "", "class", "pencode-tab-dot", "aria-hidden", "true")
		}
		b.text("img", "close-icon", "", "class", "pencode-close-icon", "src", m.themedIcon("icons/close.svg", foreground), "alt", "")
		b.close()
		b.close()
	}
	view.Keyed("source-tabs", b.nodes())
	if m.bottom.active == pencodePanelSourceControl {
		m.renderSourceControl(&s.panelView)
		m.renderSourceToolbar(&s.toolsView)
	}
	// Retain the hidden panel so reopening it reuses DOM nodes, controls and
	// native event targets instead of mounting the repository again.
	if s.panelView.used != 0 {
		view.Keyed("source-pane", s.panelView.nodes())
		view.Keyed("source-tools", s.toolsView.nodes())
	}
	b = &s.overlayView
	b.reset()
	if s.MenuOpen && m.bottom.active == pencodePanelSourceControl {
		m.renderSourceMenu(b)
	}
	if s.BranchMenuOpen && m.bottom.active == pencodePanelSourceControl {
		m.renderSourceBranches(b)
	}
	if s.RepositoryMenuOpen && m.bottom.active == pencodePanelSourceControl {
		m.renderSourceRepositories(b)
	}
	if s.modal() {
		m.renderSourcePrompt(b)
		view.Attr("content", "disabled", "")
	}
	view.Keyed("source-overlay", b.nodes())
}

func (m *workspaceModel) syncSourceScroll(world *module.Content) {
	s := m.sourceState()
	tab := s.activeTab()
	if world == nil || tab == nil || tab.Binary || s.RestoreScroll {
		return
	}
	var scrolls [2]*scene.UIScroll
	for i, id := range [...]string{"sc-code-left", "sc-code-right"} {
		if node, ok := world.HTMLNode(m.surface, id); ok {
			if entity, ok := node.ScrollContainer(); ok {
				scrolls[i] = world.Mut[scene.UIScroll](entity)
			}
		}
	}
	if tab.syncScroll(scrolls, false) && m.app != nil {
		m.app.RequestRender()
	}
	first := sourceFirstVisibleRow(tab.ScrollY)
	if first != tab.FirstRow {
		tab.FirstRow = first
		m.sourceChanged()
	}
}
func (m *workspaceModel) bindSourceControls(world *module.Content) {
	s := m.sourceState()
	if s.Owner != m {
		s.Handlers = nil
		s.Owner = m
		s.BoundRevision = 0
		s.FocusedModal = ""
		s.FocusedTab = ""
	}
	if s.RenderRevision != s.BoundRevision {
		if s.Handlers == nil {
			s.Handlers = make(map[string]sourceClickBinding)
		}
		bound := false
		for _, builder := range []*sourceVNodeView{&s.view, &s.tabsView, &s.overlayView, &s.panelView, &s.toolsView, &s.branchView, &s.repositoryView} {
			for _, id := range builder.clicks {
				if node, ok := world.HTMLNode(m.surface, id); ok {
					binding := s.Handlers[id]
					if binding.Node == node {
						continue
					}
					if binding.Handler == nil {
						binding.Handler = func(*htmlui.PointerEvent) { m.sourceAction(id) }
					}
					binding.Node = node
					s.Handlers[id] = binding
					node.OnClick(binding.Handler)
					bound = true
				}
			}
		}
		s.BoundRevision = s.RenderRevision
		if bound {
			if err := world.SyncHTML(); err != nil {
				m.setActiveStatus(err.Error())
			}
		}
	}
	if s.RestoreGraph && m.bottom.active == pencodePanelSourceControl {
		if node, ok := world.HTMLNode(m.surface, "sc-graph"); ok {
			if entity, ok := node.ScrollContainer(); ok {
				if scroll := world.Mut[scene.UIScroll](entity); scroll != nil {
					scroll.Position[1] = s.GraphScrollY
					s.RestoreGraph = false
				}
			}
		}
	}
	if s.MessageFocus && m.bottom.active == pencodePanelSourceControl {
		// Switching between input and textarea mounts a new native field. Bind it
		// before focus so the UI records both keyboard focus and caret state.
		if err := world.SyncHTML(); err != nil {
			m.setActiveStatus(err.Error())
			return
		}
		if entity, ok := world.HTMLControl(m.surface, "sc-message"); ok && world.Focus(entity) {
			if field := world.Mut[engineui.TextField](entity); field != nil {
				field.Anchor, field.Caret = s.MessageAnchor, s.MessageCaret
			}
			s.MessageFocus = false
		}
	}
	m.focusSourceBranches(world)
	m.focusSourceRepositories(world)
	if s.BranchMenuOpen || s.RepositoryMenuOpen {
		return
	}
	if !s.visible() && !s.modal() {
		s.FocusedTab = ""
		return
	}
	if s.activeTab() == nil {
		s.FocusedTab = ""
	}
	key, focus := s.Prompt, "sc-prompt-confirm"
	if key == "delete-branch" {
		focus = "sc-prompt-cancel"
	}
	if key == "branch" || key == "tag" || key == "rename-branch" {
		focus = "sc-prompt-value"
	}
	if s.Notice != "" {
		key, focus = "notice", "sc-notice-close"
	}
	if key != s.FocusedModal {
		if key == "" {
			s.FocusedModal = ""
		} else if entity, ok := world.HTMLControl(m.surface, focus); ok {
			world.Focus(entity)
			s.FocusedModal = key
		}
	}
	if entity, ok := world.HTMLControl(m.surface, "sc-merge-result"); ok {
		if field := world.Mut[engineui.Textarea](entity); field != nil {
			field.CaptureTab, field.SelectTokens, field.Wrap = true, true, false
		}
		if font := world.Mut[htmlui.ControlTextFont](entity); font != nil {
			font.Font = m.codeFont()
		} else {
			_ = world.Insert(entity, htmlui.ControlTextFont{Font: m.codeFont()})
		}
		if tab := s.activeTab(); tab != nil {
			field, _ := world.Get[engineui.TextField](entity)
			if field.Value != tab.Result {
				world.SetControlValue(entity, tab.Result)
			}
			if s.FocusedTab != tab.Key && !s.modal() {
				world.Focus(entity)
				s.FocusedTab = tab.Key
			}
		}
	}
	if s.RestoreScroll {
		if tab := s.activeTab(); tab != nil {
			var scrolls [2]*scene.UIScroll
			for i, id := range [...]string{"sc-code-left", "sc-code-right"} {
				if node, ok := world.HTMLNode(m.surface, id); ok {
					if entity, ok := node.ScrollContainer(); ok {
						if scroll := world.Mut[scene.UIScroll](entity); scroll != nil {
							scrolls[i] = scroll
						}
					}
				}
			}
			if tab.syncScroll(scrolls, true) && m.app != nil {
				m.app.RequestRender()
			}
			if first := sourceFirstVisibleRow(tab.ScrollY); first != tab.FirstRow {
				tab.FirstRow = first
				m.sourceChanged()
			}
		}
		s.RestoreScroll = false
	}
	m.syncSourceMergePaint(world)
	m.bindSourceDiffControls(world)
	if tab := s.activeTab(); tab != nil && !tab.Binary && (s.gutterTab != tab || s.gutterY != tab.ScrollY || s.gutterRevision != s.RenderRevision) {
		world.HTMLSetOffset(m.surface, "sc-diff-gutter-track", 0, tab.ScrollY)
		world.HTMLSetOffset(m.surface, "sc-code-left-gutter-track", 0, tab.ScrollY)
		world.HTMLSetOffset(m.surface, "sc-code-right-gutter-track", 0, tab.ScrollY)
		s.gutterTab, s.gutterY, s.gutterRevision = tab, tab.ScrollY, s.RenderRevision
	}
}

func (m *workspaceModel) sourceShortcut(world *module.Content, state *input.State, goos string) (bool, bool) {
	s := m.sourceState()
	if s.modal() {
		if m.uiKey(state, input.KeyEscape) {
			s.Prompt, s.Notice = "", ""
			s.FocusedModal, s.FocusedTab = "", ""
			return true, true
		}
		return true, false
	}
	if s.BranchMenuOpen {
		return true, m.sourceBranchShortcut(state)
	}
	if s.RepositoryMenuOpen {
		return true, m.sourceRepositoryShortcut(state)
	}
	if m.uiKey(state, input.KeyEscape) && s.MenuOpen {
		s.MenuOpen = false
		return true, true
	}
	if m.bottom.active == pencodePanelSourceControl && !s.MessageMultiline && !s.busy() && m.commandMatches("source-newline", state, goos) {
		if entity, ok := world.HTMLControl(m.surface, "sc-message"); ok {
			if field, ok := world.Get[engineui.TextField](entity); ok && field.Focused {
				start, end := min(field.Anchor, field.Caret), max(field.Anchor, field.Caret)
				first, last := pencodeSelectionBytes(field.Value, start, end)
				s.CommitMessage = field.Value[:first] + "\n" + field.Value[last:]
				s.MessageMultiline, s.MessageFocus = true, true
				s.MessageAnchor, s.MessageCaret = start+1, start+1
				return true, true
			}
		}
	}
	if !s.visible() {
		return false, false
	}
	if m.settings.Bindings[pencodeShortcutDefinitions[pencodeShortcutSave].id].matches(state, goos) {
		if tab := s.activeTab(); tab != nil && tab.editable() {
			m.sourceAction("sc-save-diff")
		} else {
			m.sourceAction("sc-save-merge")
		}
		return true, true
	}
	if m.settings.Bindings[pencodeShortcutDefinitions[pencodeShortcutClose].id].matches(state, goos) {
		m.sourceAction("sc-close-tab")
		return true, true
	}
	// Route history to the merge result while comparison tabs own the editor.
	for _, action := range []pencodeShortcutAction{pencodeShortcutUndo, pencodeShortcutRedo, pencodeShortcutFind, pencodeShortcutReplace} {
		if !m.settings.Bindings[pencodeShortcutDefinitions[action].id].matches(state, goos) {
			continue
		}
		if tab := s.activeTab(); tab != nil && (tab.Kind == "merge" || tab.editable()) && !s.busy() && (action == pencodeShortcutUndo || action == pencodeShortcutRedo) {
			if entity, ok := world.HTMLControl(m.surface, "sc-code-left-input"); ok {
				if field, ok := world.Get[engineui.TextField](entity); ok && field.Focused {
					return true, false
				}
			}
			return true, tab.undoResult(action == pencodeShortcutRedo)
		}
		return true, false
	}
	return false, false
}

func (m *workspaceModel) sourceCloseBlocked(project *pencodeProject) bool {
	for _, p := range m.projects {
		if project != nil && project != p {
			continue
		}
		s := &p.sourceControl
		if m.pollSourceResult(s, p) {
			m.sourceChanged()
		}
		if s.unsavedRepositories() || s.Done != nil && (s.JobKind == sourceWrite || s.JobKind == sourceSync) {
			m.activateProject(p)
			if s.unsavedRepositories() {
				s.Notice = "差异或合并编辑标签中有未保存的修改，请保存或关闭这些标签后再关闭项目或窗口。"
			} else {
				s.Notice = "Git 正在修改仓库，请等待操作完成后再关闭项目或窗口。"
			}
			m.sourceChanged()
			return true
		}
	}
	return false
}

func (m *workspaceModel) sourcePathDirty(name string) bool {
	full := filepath.Join(m.sourceState().Snapshot.Repository.Root, filepath.FromSlash(name))
	for _, p := range m.projects {
		relative, err := filepath.Rel(p.Path, full)
		if err == nil {
			if file := p.Files[filepath.ToSlash(relative)]; file != nil && file.Dirty {
				return true
			}
		}
	}
	return false
}

func (s *pencodeSourceControl) graphWindow() []sourceCommit {
	start := min(s.GraphFirst, len(s.Snapshot.Commits))
	return s.Snapshot.Commits[start:min(len(s.Snapshot.Commits), start+max(24, s.GraphVisible))]
}
func (m *workspaceModel) loadSourceHistory() {
	s := m.sourceState()
	if s.Done != nil || !s.Snapshot.HasMore || s.Snapshot.HistoryRefs == "" {
		return
	}
	r, refs, offset, graph := s.Snapshot.Repository, s.Snapshot.HistoryRefs, len(s.Snapshot.Commits), s.Snapshot.Graph
	m.sourceJob(sourceQuery, func() sourceResult {
		page, err := r.historyPage(refs, offset, sourceHistoryPageSize, graph)
		return sourceResult{Page: &page, PageRefs: refs, PageOffset: offset, Err: err}
	})
}
func (m *workspaceModel) syncSourceGraph(world *module.Content) bool {
	if world == nil {
		return false
	}
	s := m.sourceState()
	node, ok := world.HTMLNode(m.surface, "sc-graph")
	if !ok {
		return false
	}
	entity, ok := node.ScrollContainer()
	if !ok {
		return false
	}
	scroll := world.Mut[scene.UIScroll](entity)
	if scroll == nil {
		return false
	}
	if s.RestoreGraph {
		return false
	}
	s.GraphScrollY = scroll.Position[1]
	first := max(0, s.graphCommitAt(int(-s.GraphScrollY))-5) / 8 * 8
	visible := int(scroll.ViewportSize.Y())/sourceGraphRowHeight + 12
	changed := first != s.GraphFirst || visible != s.GraphVisible
	s.GraphFirst, s.GraphVisible = first, visible
	if s.Done == nil && !s.modal() && s.Snapshot.HasMore && -s.GraphScrollY+scroll.ViewportSize.Y() >= float32(s.graphOffset(len(s.Snapshot.Commits))-64) {
		m.loadSourceHistory()
		return true
	}
	return changed
}
