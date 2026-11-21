package workbench

import (
	"context"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	sourceRepositoryBranchPrefix = "sc-repository-branches:"
	sourceRepositoryActionPrefix = "sc-repository-action:"
	sourceRepositoryCommit       = "commit"
	sourceRepositorySync         = "sync"
)

var sourceRepositoryActions = [...]struct{ Name, Label, Icon, IconClass string }{
	{"refresh", "Refresh", "refresh-cw", ""},
	{"pull", "Pull", "arrow-right", "sc-sync-down"},
	{"push", "Push", "arrow-right", "sc-sync-up"},
}

type sourceRepositoryJob struct {
	Done chan sourceResult
	Name string
}

// Retain drafts and comparisons per repository while another repository is selected.
type sourceRepositoryView struct {
	Active, Message string
	Tabs            []*sourceTab
	Snapshot        sourceSnapshot
	Anchor, Caret   int
	Multiline       bool
}

func sourceRepositoryForPath(roots []string, name string) string {
	best := ""
	for _, root := range roots {
		prefix := strings.TrimRight(root, "/")
		if len(root) <= len(best) || len(name) < len(prefix) {
			continue
		}
		matches := name[:len(prefix)] == prefix
		if len(prefix) > 1 && prefix[1] == ':' {
			matches = strings.EqualFold(name[:len(prefix)], prefix)
		}
		if matches && (len(name) == len(prefix) || name[len(prefix)] == '/') {
			best = root
		}
	}
	return best
}

func sourceRepositoryLabel(project, root string) string {
	if relative, err := filepath.Rel(project, filepath.FromSlash(root)); err == nil && filepath.IsLocal(relative) && relative != "." {
		return filepath.ToSlash(relative)
	}
	return path.Base(root)
}

func (r sourceRepository) headLabel() string {
	if data, err := r.command("symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		return strings.TrimSpace(string(data))
	}
	data, err := r.command("rev-parse", "--short=8", "--verify", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (r sourceRepository) repositoryHeads(roots []string, current *sourceSnapshot) ([]string, []sourceSyncStatus) {
	refs := make([]string, len(roots))
	counts := make([]sourceSyncStatus, len(roots))
	for i, root := range roots {
		if root == r.Root {
			refs[i], counts[i] = strings.TrimSuffix(current.Branch, " (detached)"), current.Sync
		} else {
			repository := r
			repository.Root = root
			refs[i] = repository.headLabel()
			counts[i], _ = repository.syncStatus()
		}
	}
	return refs, counts
}

func (s *pencodeSourceControl) repositoryBusy(root string) bool {
	jobRoot := s.jobRoot
	if jobRoot == "" {
		jobRoot = s.Snapshot.Repository.Root
	}
	return s.repositoryJobs[root].Done != nil ||
		s.Done != nil && s.JobKind != sourceRefresh && root == jobRoot
}

func (m *workspaceModel) startSourceRepositoryAction(root, op string) {
	s := m.sourceState()
	if root == "" || root != s.Snapshot.Repository.Root && !slices.Contains(s.Repositories, root) || s.repositoryBusy(root) {
		return
	}
	if op == "pull" && m.sourceHasDraftsIn(root) {
		m.setProjectStatus(m.activeProjectState(), root+": "+m.tr("请先保存编辑器中的项目修改，再执行 Git 工作区操作。"))
		m.sourceChanged()
		return
	}
	m.startSourceRepositoryJob(sourceRepository{Root: root, Backend: m.backend}, op, func(r sourceRepository) sourceResult {
		result := sourceResult{}
		switch op {
		case "refresh":
			_, result.Err = r.command("fetch", "--all", "--prune")
			result.Message = "Fetch completed"
		case "pull":
			_, result.Err = r.command("pull", "--no-rebase", "--no-edit")
			result.Message, result.Reload = "Git pull completed", true
		case "push":
			result.Err = r.push()
			result.Message = "Git push completed"
		}
		return result
	})
}

func (m *workspaceModel) startSourceRepositoryJob(r sourceRepository, name string, operation func(sourceRepository) sourceResult) {
	s := m.sourceState()
	root := r.Root
	if root == "" || s.repositoryBusy(root) {
		return
	}
	if root == s.Snapshot.Repository.Root {
		s.cancelRefresh()
	}
	if s.Context == nil {
		s.Context, s.Cancel = context.WithCancel(context.Background())
	}
	if s.repositoryJobs == nil {
		s.repositoryJobs = make(map[string]sourceRepositoryJob)
	}
	r.Context = s.Context
	done, wake := make(chan sourceResult, 1), m.backgroundWake()
	s.repositoryJobs[root] = sourceRepositoryJob{Done: done, Name: name}
	s.syncRevision++
	m.sourceChanged()
	go func() {
		result := operation(r)
		snapshot, err := r.snapshot(sourceHistoryPageSize)
		if err == nil {
			result.Snapshot = &snapshot
		} else if result.Err == nil {
			result.Err = err
		}
		done <- result
		if r.Context.Err() == nil && wake != nil {
			wake()
		}
	}()
}

func (m *workspaceModel) pollSourceRepositoryJobs(s *pencodeSourceControl, project *pencodeProject) bool {
	changed := false
	for root, job := range s.repositoryJobs {
		select {
		case result := <-job.Done:
			delete(s.repositoryJobs, root)
			m.applySourceRepositoryResult(s, project, root, job.Name, result)
			changed = true
		default:
		}
	}
	return changed
}

func (m *workspaceModel) applySourceRepositoryResult(s *pencodeSourceControl, project *pencodeProject, root, name string, result sourceResult) {
	s.syncRevision++
	// A refresh started during a Git operation can carry older refs or tree status.
	s.cancelRefresh()
	active := root == s.Snapshot.Repository.Root
	primary := name == sourceRepositoryCommit || name == sourceRepositorySync
	if snapshot := result.Snapshot; snapshot != nil {
		if active {
			s.applySourceSnapshot(snapshot, false)
		} else {
			s.cacheRepositorySnapshot(root, snapshot)
		}
		if project != nil {
			project.treeGit.update(project.Path, s.Repositories, nil, snapshot)
		}
	}
	message := result.Message
	if result.Err != nil {
		var expected bool
		message, expected = m.sourceErrorMessage(result)
		if primary && active && !expected && !s.modal() &&
			(!s.BranchMenuOpen || s.BranchTrigger == "sc-branches") && (s.Done == nil || s.jobRoot == root) {
			s.Notice, message = message, ""
		}
	} else if name == sourceRepositoryCommit {
		if active {
			s.CommitMessage = ""
			s.MessageMultiline, s.MessageFocus = false, false
			s.MessageAnchor, s.MessageCaret = 0, 0
			if m.fontResources != nil && s == m.sourceState() {
				if entity, ok := m.fontResources.HTMLControl(m.surface, "sc-message"); ok {
					m.fontResources.SetControlValue(entity, "")
				}
			}
		} else if view, ok := s.RepositoryViews[root]; ok {
			view.Message, view.Multiline, view.Anchor, view.Caret = "", false, 0, 0
			s.RepositoryViews[root] = view
		}
	}
	if message != "" {
		if !primary || !active {
			message = root + ": " + message
		}
		m.setProjectStatus(project, message)
	}
	if result.Reload && project != nil {
		m.reloadSourceFiles(project)
	}
	s.RefreshPending = true
}

func (s *pencodeSourceControl) cacheRepositorySnapshot(root string, snapshot *sourceSnapshot) {
	view := s.RepositoryViews[root]
	view.Snapshot = *snapshot
	if view.Message == "" {
		view.Message = snapshot.MergeMessage
	}
	if s.RepositoryViews == nil {
		s.RepositoryViews = make(map[string]sourceRepositoryView)
	}
	s.RepositoryViews[root] = view
	s.updateRepositoryHead(snapshot)
}

func (s *pencodeSourceControl) updateRepositoryHead(snapshot *sourceSnapshot) {
	changed := false
	if i := slices.Index(s.Repositories, snapshot.Repository.Root); i >= 0 {
		if i < len(s.RepositoryRefs) {
			ref := strings.TrimSuffix(snapshot.Branch, " (detached)")
			changed = s.RepositoryRefs[i] != ref
			s.RepositoryRefs[i] = ref
		}
		if i < len(s.RepositorySync) {
			changed = changed || s.RepositorySync[i] != snapshot.Sync
			s.RepositorySync[i] = snapshot.Sync
		}
	}
	if changed {
		s.syncRevision++
	}
}

func (s *pencodeSourceControl) selectRepository(repository sourceRepository) {
	if s.RepositoryViews == nil {
		s.RepositoryViews = make(map[string]sourceRepositoryView)
	}
	if root := s.Snapshot.Repository.Root; root != "" {
		s.RepositoryViews[root] = sourceRepositoryView{
			Snapshot: s.Snapshot, Tabs: s.Tabs, Active: s.Active, Message: s.CommitMessage,
			Multiline: s.MessageMultiline, Anchor: s.MessageAnchor, Caret: s.MessageCaret,
		}
	}
	view := s.RepositoryViews[repository.Root]
	delete(s.RepositoryViews, repository.Root)
	s.Snapshot, s.Tabs, s.Active, s.CommitMessage = view.Snapshot, view.Tabs, view.Active, view.Message
	s.Snapshot.Repository = repository
	s.OpenID, s.OpenKey, s.OpenPinned = "", "", false
	s.PendingOpen, s.PendingPinned, s.lastClick = "", false, ""
	s.MessageMultiline, s.MessageAnchor, s.MessageCaret = view.Multiline, view.Anchor, view.Caret
	s.RestoreScroll = s.Active != ""
	s.MessageFocus, s.BranchFocus = false, false
	s.RepositoryError, s.SelectedHash, s.FocusedTab = "", "", ""
	s.CommitFiles, s.Collapsed = nil, nil
	s.TreeRevision++
	s.GraphFirst, s.GraphScrollY, s.RestoreGraph = 0, 0, true
	s.MenuOpen, s.BranchMenuOpen = false, false
	s.Selection++
}

func (m *workspaceModel) switchSourceRepository(root string, follow bool) bool {
	s := m.sourceState()
	if s.modal() || s.Snapshot.Repository.Root == root {
		return false
	}
	s.cancelRefresh()
	if s.repositorySwitchBlocked() {
		return false
	}
	repository := sourceRepository{Root: root, Backend: m.backend, Context: s.Context}
	s.selectRepository(repository)
	if follow {
		s.Active, s.RestoreScroll = "", false
	}
	// An explicit choice lasts until another editor file becomes active.
	s.FollowPath = m.activePath
	m.refreshSourceControl()
	m.sourceChanged()
	return true
}

func (s *pencodeSourceControl) repositorySwitchBlocked() bool {
	return s.Done != nil && s.JobKind != sourceRefresh
}

func (m *workspaceModel) followSourceRepository() bool {
	s := m.sourceState()
	if !s.RepositoriesLoaded || s.FollowPath == m.activePath || s.visible() || s.modal() || s.repositorySwitchBlocked() {
		return false
	}
	s.FollowPath = m.activePath
	if m.activePath == "" {
		return false
	}
	name := filepath.ToSlash(filepath.Join(m.projectPath, filepath.FromSlash(m.activePath)))
	root := sourceRepositoryForPath(s.Repositories, name)
	return root != "" && m.switchSourceRepository(root, true)
}

type sourceRepositoryListKey struct {
	Theme                                    *pencodeTheme
	Roots, Refs                              *string
	Counts                                   *sourceSyncStatus
	Current, Branch, WidthStyle              string
	BranchTrigger, JobRoot                   string
	Sync                                     sourceSyncStatus
	SyncRevision                             uint64
	Count                                    int
	Busy, SwitchBlocked, Collapsed, Resizing bool
}

func (m *workspaceModel) sourceRepositoryKey() sourceRepositoryListKey {
	s := m.sourceState()
	key := sourceRepositoryListKey{
		Theme: m.workbenchTheme, Roots: sourceFirst(s.Repositories), Refs: sourceFirst(s.RepositoryRefs),
		Counts: sourceFirst(s.RepositorySync), Sync: s.Snapshot.Sync,
		Current: s.Snapshot.Repository.Root, Branch: s.Snapshot.Branch, Count: len(s.Repositories),
		WidthStyle: s.RepositoriesWidthStyle, Busy: s.Done != nil && s.JobKind != sourceRefresh, SwitchBlocked: s.repositorySwitchBlocked(), SyncRevision: s.syncRevision,
		Collapsed: s.RepositoriesCollapsed, Resizing: m.resize.target == pencodeResizeSourceRepositories,
	}
	if key.Busy {
		key.JobRoot = s.jobRoot
	}
	if s.BranchMenuOpen {
		key.BranchTrigger = s.BranchTrigger
	}
	return key
}

func (m *workspaceModel) renderSourceRepositories(panel *sourceVNodeView, key sourceRepositoryListKey) {
	s := m.sourceState()
	b := &s.repositoryView
	if b.used == 0 || s.repositoryKey != key {
		s.repositoryKey = key
		b.reset()
		b.open("aside", "repositories", "id", "sc-repositories", "class", "sc-repositories", "aria-label", "Git Repositories", "data-collapsed", strconv.FormatBool(key.Collapsed))
		if !key.Collapsed && key.WidthStyle != "" {
			b.attr("style", key.WidthStyle)
		}
		b.open("header", "header", "class", "sc-repositories-header")
		if !key.Collapsed {
			b.text("h2", "title", "Git Repositories", "class", "sc-heading")
		}
		label := "Collapse Git Repositories"
		if key.Collapsed {
			label = "Expand Git Repositories"
		}
		b.open("button", "toggle", "id", "sc-repositories-toggle", "type", "button", "class", "pencode-icon-button sc-repositories-toggle", "title", label, "aria-label", label, "aria-controls", "sc-repositories-list", "aria-expanded", strconv.FormatBool(!key.Collapsed))
		m.sourceIcon(b, "icon", "chevron-right", "")
		b.close()
		b.close()
		b.open("div", "list", "id", "sc-repositories-list", "class", "sc-repositories-list")
		if key.Collapsed {
			b.attr("hidden", "")
		}
		roots := s.Repositories
		if len(roots) == 0 && key.Current != "" {
			roots = []string{key.Current}
		}
		for i, root := range roots {
			active := root == key.Current
			ref := ""
			var counts sourceSyncStatus
			if i < len(s.RepositoryRefs) {
				ref = s.RepositoryRefs[i]
			}
			if i < len(s.RepositorySync) {
				counts = s.RepositorySync[i]
			} else if active {
				counts = key.Sync
			}
			if active && key.Branch != "" {
				ref = key.Branch
			}
			b.open("div", root, "class", "sc-repository-row", "data-active", strconv.FormatBool(active))
			b.open("button", root, "id", "sc-repository:"+root, "type", "button", "class", "sc-repository-button", "title", root, "aria-pressed", strconv.FormatBool(active), "aria-controls", "sc-main")
			if key.SwitchBlocked {
				b.attr("disabled", "")
			}
			m.sourceIcon(b, "icon", "repo", "")
			b.text("span", "name", sourceRepositoryLabel(m.projectPath, root), "class", "sc-repository-name")
			b.close()
			trigger := "sc-branches"
			if !active {
				trigger = sourceRepositoryBranchPrefix + root
			}
			if ref == "" {
				ref = "Branches"
			}
			label := "Switch Branch: " + ref
			b.open("button", "branch", "id", trigger, "type", "button", "class", "sc-branch-button", "title", label, "aria-label", label, "aria-haspopup", "menu", "aria-expanded", strconv.FormatBool(key.BranchTrigger == trigger), "aria-controls", "sc-branch-menu")
			busy := s.repositoryBusy(root)
			if key.Busy || busy {
				b.attr("disabled", "")
			}
			b.open("span", "content", "class", "sc-branch-button-content")
			m.sourceIcon(b, "icon", "git-branch", "")
			b.text("span", "name", ref, "class", "sc-branch")
			b.close()
			b.close()
			m.renderSourceRepositoryActions(b, root, s.repositoryJobs[root].Name, counts, busy)
			b.close()
		}
		b.close()
		b.close()
		if !key.Collapsed {
			resizeClass := "pencode-sidebar-resize"
			if key.Resizing {
				resizeClass += " resizing"
			}
			b.text("div", "resize", "", "id", "sc-repositories-resize", "class", resizeClass, "role", "separator", "aria-orientation", "vertical", "aria-label", "Resize Git Repositories", "aria-controls", "sc-repositories")
		}
	}
	panel.append(b.nodes())
}

func (m *workspaceModel) renderSourceRepositoryActions(b *sourceVNodeView, root, running string, counts sourceSyncStatus, disabled bool) {
	for _, action := range sourceRepositoryActions {
		label, count := action.Label, 0
		switch action.Name {
		case "pull":
			count = counts.Behind
		case "push":
			count = counts.Ahead
		}
		countText := ""
		if count > 0 {
			countText = strconv.Itoa(count)
			label += " (" + countText + ")"
		}
		b.open("button", action.Name, "id", sourceRepositoryActionPrefix+action.Name+":"+root, "type", "button",
			"class", "pencode-icon-button sc-repository-action", "title", label, "aria-label", label)
		if disabled {
			b.attr("disabled", "")
		}
		icon, class := action.Icon, action.IconClass
		if running == action.Name {
			b.attr("aria-busy", "true")
			icon, class = "refresh-cw", "sc-sync-spinning"
		}
		m.sourceIcon(b, "icon", icon, class)
		if countText != "" {
			b.text("span", "count", countText, "class", "sc-repository-action-count", "aria-hidden", "true")
		}
		b.close()
	}
}
