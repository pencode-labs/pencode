package workbench

import (
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

const sourceRepositoryBranchPrefix = "sc-repository-branches:"

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

func (r sourceRepository) repositoryRefs(roots []string, current string) []string {
	refs := make([]string, len(roots))
	for i, root := range roots {
		if root == r.Root {
			refs[i] = strings.TrimSuffix(current, " (detached)")
		} else {
			repository := r
			repository.Root = root
			refs[i] = repository.headLabel()
		}
	}
	return refs
}

func (s *pencodeSourceControl) selectRepository(repository sourceRepository) {
	if s.RepositoryViews == nil {
		s.RepositoryViews = make(map[string]sourceRepositoryView)
	}
	if root := s.Snapshot.Repository.Root; root != "" {
		if s.Done != nil && s.JobKind == sourceSync {
			if s.backgroundSyncs == nil {
				s.backgroundSyncs = make(map[string]chan sourceResult)
			}
			s.backgroundSyncs[root] = s.Done
			s.Done = nil
			s.syncRevision++
		}
		s.RepositoryViews[root] = sourceRepositoryView{
			Snapshot: s.Snapshot, Tabs: s.Tabs, Active: s.Active, Message: s.CommitMessage,
			Multiline: s.MessageMultiline, Anchor: s.MessageAnchor, Caret: s.MessageCaret,
		}
	}
	if done := s.backgroundSyncs[repository.Root]; done != nil {
		s.Done, s.JobKind = done, sourceSync
		delete(s.backgroundSyncs, repository.Root)
		s.syncRevision++
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
	return s.Done != nil && s.JobKind != sourceSync && s.JobKind != sourceRefresh
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
	Current, Branch, WidthStyle              string
	BranchTrigger                            string
	SyncRevision                             uint64
	Count                                    int
	Busy, SwitchBlocked, Collapsed, Resizing bool
}

func (m *workspaceModel) sourceRepositoryKey() sourceRepositoryListKey {
	s := m.sourceState()
	key := sourceRepositoryListKey{
		Theme: m.workbenchTheme, Roots: sourceFirst(s.Repositories), Refs: sourceFirst(s.RepositoryRefs),
		Current: s.Snapshot.Repository.Root, Branch: s.Snapshot.Branch, Count: len(s.Repositories),
		WidthStyle: s.RepositoriesWidthStyle, Busy: s.busy(), SwitchBlocked: s.repositorySwitchBlocked(), SyncRevision: s.syncRevision,
		Collapsed: s.RepositoriesCollapsed, Resizing: m.resize.target == pencodeResizeSourceRepositories,
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
			if i < len(s.RepositoryRefs) {
				ref = s.RepositoryRefs[i]
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
			if key.Busy || s.backgroundSyncs[root] != nil {
				b.attr("disabled", "")
			}
			b.open("span", "content", "class", "sc-branch-button-content")
			m.sourceIcon(b, "icon", "git-branch", "")
			b.text("span", "name", ref, "class", "sc-branch")
			b.close()
			b.close()
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
