package workbench

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
)

// Retain drafts and comparisons per repository. Background jobs finish before a
// switch, so a completion can never apply a previous repository's result.
type sourceRepositoryView struct {
	Snapshot        sourceSnapshot
	Tabs            []*sourceTab
	Active, Message string
	Multiline       bool
	Anchor, Caret   int
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
	s.MessageMultiline, s.MessageAnchor, s.MessageCaret = view.Multiline, view.Anchor, view.Caret
	s.RestoreScroll = s.Active != ""
	s.MessageFocus, s.BranchFocus = false, false
	s.RepositoryError, s.SelectedHash, s.FocusedTab = "", "", ""
	s.CommitFiles, s.Collapsed = nil, nil
	s.TreeRevision++
	s.GraphFirst, s.GraphScrollY, s.RestoreGraph = 0, 0, true
	s.MenuOpen, s.BranchMenuOpen, s.RepositoryMenuOpen = false, false, false
	s.Selection++
}

func (m *workspaceModel) switchSourceRepository(root string, follow bool) bool {
	s := m.sourceState()
	if s.modal() || s.Snapshot.Repository.Root == root {
		return false
	}
	s.cancelRefresh()
	if s.Done != nil {
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

func (m *workspaceModel) followSourceRepository() bool {
	s := m.sourceState()
	if !s.RepositoriesLoaded || s.FollowPath == m.activePath || s.visible() || s.modal() || s.Done != nil && s.JobKind != sourceRefresh {
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

type sourceRepositoryMenuKey struct {
	Theme               *pencodeTheme
	Roots               *string
	Count, Index        int
	Current, Project    string
	X, Y, Width, Height float32
}

func (m *workspaceModel) toggleSourceRepositories() {
	s := m.sourceState()
	if len(s.Repositories) < 2 {
		return
	}
	s.RepositoryMenuOpen = !s.RepositoryMenuOpen
	s.MenuOpen, s.BranchMenuOpen, s.BranchSubmenu = false, false, false
	s.RepositoryFocus = true
	s.RepositoryIndex = max(0, slices.Index(s.Repositories, s.Snapshot.Repository.Root))
	m.positionSourceRepositories(m.fontResources)
}

func (m *workspaceModel) positionSourceRepositories(world *module.Content) {
	if world == nil {
		return
	}
	s := m.sourceState()
	root, _ := world.HTMLNode(m.surface, "pencode-root")
	area, ok := root.Bounds()
	trigger, _ := world.HTMLNode(m.surface, "sc-repositories")
	anchor, anchored := trigger.Bounds()
	if !ok || !anchored {
		return
	}
	const margin, gap = float32(12), float32(4)
	width := min(float32(360), max(0, area.Width-2*margin))
	height := float32(12 + 28*len(s.Repositories))
	above, below := anchor.Y-area.Y-gap-margin, area.Y+area.Height-anchor.Y-anchor.Height-gap-margin
	available := max(0, max(above, below))
	x, y := anchor.X-area.X+anchor.Width-width, anchor.Y-area.Y+anchor.Height+gap
	if above >= min(height, available) {
		y = anchor.Y - area.Y - gap - min(height, available)
	}
	s.RepositoryX, s.RepositoryY = max(margin, min(x, area.Width-width-margin)), max(margin, y)
	s.RepositoryWidth, s.RepositoryHeight = width, available
}

func (m *workspaceModel) renderSourceRepositories(overlay *sourceVNodeView) {
	s := m.sourceState()
	key := sourceRepositoryMenuKey{Theme: m.workbenchTheme, Roots: sourceFirst(s.Repositories), Count: len(s.Repositories), Index: s.RepositoryIndex,
		Current: s.Snapshot.Repository.Root, Project: m.projectPath, X: s.RepositoryX, Y: s.RepositoryY, Width: s.RepositoryWidth, Height: s.RepositoryHeight}
	b := &s.repositoryView
	if b.used == 0 || s.repositoryKey != key {
		s.repositoryKey = key
		b.reset()
		b.open("div", "repositories", "id", "sc-repository-menu", "class", "pencode-editor-context sc-branch-menu", "role", "menu", "aria-label", "Git repositories", "style", fmt.Sprintf("left:%gpx;top:%gpx;width:%gpx;max-height:%gpx", key.X, key.Y, key.Width, key.Height))
		for i, root := range s.Repositories {
			class, tabindex := "sc-branch-item", "-1"
			if i == s.RepositoryIndex {
				class, tabindex = class+" active", "0"
			}
			b.open("button", root, "id", "sc-repository:"+root, "type", "button", "class", class, "role", "menuitemradio", "aria-checked", strconv.FormatBool(root == key.Current), "tabindex", tabindex, "title", root)
			b.text("span", "name", sourceRepositoryLabel(m.projectPath, root), "class", "sc-branch-name")
			if root == key.Current {
				m.sourceIcon(b, "current", "check", "")
			}
			b.close()
		}
		b.close()
	}
	overlay.append(b.nodes())
}

func (m *workspaceModel) sourceRepositoryShortcut(state *input.State) bool {
	s := m.sourceState()
	count := len(s.Repositories)
	switch {
	case m.uiKey(state, input.KeyEscape), m.uiKey(state, input.KeyTab):
		s.RepositoryMenuOpen, s.RepositoryFocus = false, true
	case count == 0:
		return false
	case m.uiKey(state, input.KeyDown):
		s.RepositoryIndex = (s.RepositoryIndex + 1) % count
	case m.uiKey(state, input.KeyUp):
		s.RepositoryIndex = (s.RepositoryIndex + count - 1) % count
	case m.uiKey(state, input.KeyHome):
		s.RepositoryIndex = 0
	case m.uiKey(state, input.KeyEnd):
		s.RepositoryIndex = count - 1
	case m.uiKey(state, input.KeyEnter), m.uiKey(state, input.KeySpace):
		m.sourceAction("sc-repository:" + s.Repositories[s.RepositoryIndex])
	default:
		return false
	}
	s.RepositoryFocus = true
	return true
}

func (m *workspaceModel) focusSourceRepositories(world *module.Content) {
	s := m.sourceState()
	if !s.RepositoryFocus {
		return
	}
	id := "sc-repositories"
	if s.RepositoryMenuOpen && s.RepositoryIndex < len(s.Repositories) {
		id = "sc-repository:" + s.Repositories[s.RepositoryIndex]
	}
	if entity, ok := world.HTMLControl(m.surface, id); ok && world.Focus(entity) {
		s.RepositoryFocus = false
		if s.RepositoryMenuOpen {
			menu, _ := world.HTMLNode(m.surface, "sc-repository-menu")
			item, _ := world.HTMLNode(m.surface, id)
			bounds, _ := menu.Bounds()
			row, _ := item.Bounds()
			if entity, ok := menu.ScrollContainer(); ok {
				if scroll := module.ContentMut[scene.UIScroll](world, entity); scroll != nil {
					top, bottom := row.Y-bounds.Y, row.Y+row.Height-bounds.Y
					position := scroll.Position[1]
					if top+position < 5 {
						position = 5 - top
					} else if bottom+position > bounds.Height-5 {
						position = bounds.Height - 5 - bottom
					}
					scroll.Position[1] = min(0, max(-max(0, scroll.ContentSize[1]-scroll.ViewportSize[1]), position))
					scroll.Momentum[1] = 0
				}
			}
		}
	}
}
