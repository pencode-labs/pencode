package workbench

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	htmlui "github.com/onix-engine/onix/ui/html"
)

type sourceBranch struct {
	Ref, Name, Upstream string
	Remote              bool
}

type sourceBranchPicker struct {
	Root, Current string
	Branches      []sourceBranch
	Integrating   bool
}

func (p sourceBranchPicker) branch(ref string) (sourceBranch, bool) {
	for _, branch := range p.Branches {
		if branch.Ref == ref {
			return branch, true
		}
	}
	return sourceBranch{}, false
}

func (r sourceRepository) deleteBranch(branch sourceBranch) error {
	if !branch.Remote {
		_, err := r.command("branch", "--delete", "--", branch.Name)
		return err
	}
	data, err := r.command("remote")
	if err != nil {
		return err
	}
	// Remote names can contain slashes. Prefer the longest matching name.
	remote := ""
	for name := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if name != "" && len(name) > len(remote) && strings.HasPrefix(branch.Name, name+"/") {
			remote = name
		}
	}
	if remote == "" {
		return errors.New("该远程分支所属的远程仓库已不存在，请刷新分支列表。")
	}
	_, err = r.command("push", "--delete", "--", remote, "refs/heads/"+strings.TrimPrefix(branch.Name, remote+"/"))
	if err == nil {
		// A custom push refspec may not prune the local remote-tracking ref.
		_, err = r.command("update-ref", "-d", branch.Ref)
	}
	return err
}

func (r sourceRepository) renameBranch(branch sourceBranch, name string) error {
	if branch.Remote {
		return errors.New("只能重命名本地分支。")
	}
	_, err := r.command("branch", "--move", "--", branch.Name, name)
	return err
}

func (r sourceRepository) branches() ([]sourceBranch, error) {
	data, err := r.command("for-each-ref", "--sort=refname", "--format=%(refname)%00%(symref)%00%(upstream)", "refs/heads/", "refs/remotes/")
	if err != nil {
		return nil, err
	}
	var branches []sourceBranch
	for line := range strings.SplitSeq(string(data), "\n") {
		ref, rest, ok := strings.Cut(line, "\x00")
		if !ok {
			continue
		}
		symref, upstream, _ := strings.Cut(rest, "\x00")
		if symref != "" { // Remote HEAD aliases are not selectable branches.
			continue
		}
		branch := sourceBranch{Ref: ref, Upstream: upstream}
		branch.Name, branch.Remote = strings.CutPrefix(ref, "refs/remotes/")
		if !branch.Remote {
			branch.Name = strings.TrimPrefix(ref, "refs/heads/")
		}
		branches = append(branches, branch)
	}
	return branches, nil
}

func (r sourceRepository) switchBranch(branch sourceBranch, branches []sourceBranch) error {
	if !branch.Remote {
		_, err := r.command("switch", "--no-guess", "--", branch.Name)
		return err
	}
	for _, local := range branches {
		if !local.Remote && local.Upstream == branch.Ref {
			_, err := r.command("switch", "--no-guess", "--", local.Name)
			return err
		}
	}
	// Git derives the local name from its configured remote and sets upstream.
	// An existing unrelated local branch is never reset or overwritten.
	_, err := r.command("switch", "--track", "--", branch.Ref)
	return err
}

func (m *workspaceModel) switchSourceBranch(picker sourceBranchPicker, branch sourceBranch) {
	s := m.sourceState()
	if m.sourceHasDraftsIn(picker.Root) {
		s.Notice = m.tr("请先保存编辑器中的项目修改，再切换分支。")
		return
	}
	r := s.Snapshot.Repository
	r.Root = picker.Root
	m.writeSourceRepository(r, func() sourceResult {
		err := r.switchBranch(branch, picker.Branches)
		return sourceResult{Err: err, Message: "Branch switched", Reload: err == nil}
	})
}

type sourceBranchMenuKey struct {
	Theme                *pencodeTheme
	Branches             *sourceBranch
	Current              string
	Count, Index         int
	SubIndex             int
	X, Y, Width, Height  float32
	SubX, SubY, SubWidth float32
	Submenu              bool
	Integrating          bool
	Loading              bool
}

var sourceBranchActions = [...]struct{ ID, Label string }{
	{"sc-branch-checkout", "Checkout"},
	{"sc-branch-create", "Create Branch…"},
	{"sc-branch-tag", "Create Tag…"},
	{"sc-branch-rename", "Rename…"},
	{"sc-branch-delete", "Delete…"},
	{"sc-branch-merge", "Merge…"},
	{"sc-branch-rebase", "Rebase…"},
}

func (p sourceBranchPicker) actionEnabled(index, branchIndex int) bool {
	if branchIndex < 0 || branchIndex >= len(p.Branches) {
		return false
	}
	branch := p.Branches[branchIndex]
	switch sourceBranchActions[index].ID {
	case "sc-branch-checkout", "sc-branch-delete":
		return branch.Remote || branch.Name != p.Current
	case "sc-branch-create", "sc-branch-tag":
		return true
	case "sc-branch-rename":
		return !branch.Remote
	case "sc-branch-merge", "sc-branch-rebase":
		return !p.Integrating && (branch.Remote || branch.Name != p.Current)
	}
	return false
}

func (s *pencodeSourceControl) stepBranchAction(direction int) {
	picker := s.branchMenu()
	for range sourceBranchActions {
		s.BranchSubIndex = (s.BranchSubIndex + direction + len(sourceBranchActions)) % len(sourceBranchActions)
		if picker.actionEnabled(s.BranchSubIndex, s.BranchIndex) {
			return
		}
	}
}

func (s *sourceSnapshot) branchPicker() sourceBranchPicker {
	return sourceBranchPicker{Root: s.Repository.Root, Branches: s.Branches, Current: s.Branch, Integrating: s.Rebasing || s.MergeMessage != ""}
}

func (s *pencodeSourceControl) branchMenu() sourceBranchPicker {
	if s.BranchTrigger == "sc-branches" {
		return s.Snapshot.branchPicker()
	}
	if s.branchPicker != nil {
		return *s.branchPicker
	}
	return sourceBranchPicker{}
}

func (s *pencodeSourceControl) promptBranches() sourceBranchPicker {
	if s.promptPicker != nil {
		return *s.promptPicker
	}
	return s.Snapshot.branchPicker()
}

func (m *workspaceModel) toggleSourceBranches(trigger string) {
	s := m.sourceState()
	s.BranchMenuOpen = !s.BranchMenuOpen || s.BranchTrigger != trigger
	s.MenuOpen, s.BranchSubmenu = false, false
	s.BranchTrigger, s.BranchFocus = trigger, true
	s.branchPicker = nil
	if !s.BranchMenuOpen {
		return
	}
	s.selectCurrentBranch()
	m.positionSourceBranches(m.fontResources)
	if root, ok := strings.CutPrefix(trigger, sourceRepositoryBranchPrefix); ok && s.BranchMenuOpen {
		r := s.Snapshot.Repository
		r.Root = root
		m.sourceJob(sourceQuery, func() sourceResult {
			branches, err := r.branches()
			picker := &sourceBranchPicker{Root: root, Branches: branches}
			if err == nil {
				picker.Current = r.headLabel()
				_, mergeErr := r.command("rev-parse", "--verify", "--quiet", "MERGE_HEAD")
				rebasing, rebaseErr := r.Backend.GitRebasing(r.baseContext(), root)
				picker.Integrating, err = mergeErr == nil || rebasing, rebaseErr
			}
			return sourceResult{BranchPicker: picker, Err: err}
		})
	}
}

func (s *pencodeSourceControl) selectCurrentBranch() {
	s.BranchIndex, s.BranchSubIndex = 0, -1
	picker := s.branchMenu()
	for i, branch := range picker.Branches {
		if !branch.Remote && branch.Name == picker.Current {
			s.BranchIndex = i
			break
		}
	}
}

func (m *workspaceModel) sourceBranchAnchor(world *module.Content) (htmlui.Rect, bool) {
	trigger, _ := world.HTMLNode(m.surface, m.sourceState().BranchTrigger)
	anchor, ok := trigger.Bounds()
	if !ok {
		return anchor, false
	}
	list, _ := world.HTMLNode(m.surface, "sc-repositories-list")
	viewport, ok := list.Bounds()
	if entity, scrollable := list.ScrollContainer(); scrollable {
		if scroll := world.Ref[scene.UIScroll](entity); scroll != nil {
			anchor.Y += scroll.Position[1]
		}
	}
	bottom := min(anchor.Y+anchor.Height, viewport.Y+viewport.Height)
	anchor.Y = max(anchor.Y, viewport.Y)
	anchor.Height = bottom - anchor.Y
	return anchor, ok && anchor.Height > 0
}

func (m *workspaceModel) positionSourceBranches(world *module.Content) {
	if world == nil {
		return
	}
	s := m.sourceState()
	root, _ := world.HTMLNode(m.surface, "pencode-root")
	area, ok := root.Bounds()
	anchor, anchored := m.sourceBranchAnchor(world)
	if !ok || !anchored {
		s.BranchMenuOpen, s.BranchSubmenu = false, false
		return
	}
	const margin, gap = float32(12), float32(4)
	height := float32(40)
	branches := s.branchMenu().Branches
	if count := len(branches); count != 0 {
		groups := 1
		if branches[0].Remote != branches[count-1].Remote {
			groups++
		}
		height = float32(12 + 28*(count+groups))
	}
	width := min(float32(320), max(0, area.Width-2*margin))
	above, below := anchor.Y-area.Y-gap-margin, area.Y+area.Height-anchor.Y-anchor.Height-gap-margin
	available := max(0, max(above, below))
	x, y := anchor.X-area.X+anchor.Width-width, anchor.Y-area.Y+anchor.Height+gap
	if above >= min(height, available) {
		y = anchor.Y - area.Y - gap - min(height, available)
	}
	s.MenuX, s.MenuY = max(margin, min(x, area.Width-width-margin)), max(margin, y)
	s.BranchWidth, s.BranchHeight = width, available
}

func (m *workspaceModel) positionSourceBranchSubmenu(world *module.Content) bool {
	s := m.sourceState()
	if !s.BranchSubmenu || s.BranchIndex >= len(s.branchView.clicks) {
		return false
	}
	root, _ := world.HTMLNode(m.surface, "pencode-root")
	area, _ := root.Bounds()
	menu, ok := world.HTMLNode(m.surface, "sc-branch-menu")
	bounds, bounded := menu.Bounds()
	item, _ := world.HTMLNode(m.surface, s.branchView.clicks[s.BranchIndex])
	row, _ := item.Bounds()
	if !ok || !bounded {
		return false
	}
	if entity, ok := menu.ScrollContainer(); ok {
		if scroll, ok := world.Get[scene.UIScroll](entity); ok {
			row.Y += scroll.Position[1]
		}
	}
	if row.Y+row.Height <= bounds.Y+5 || row.Y >= bounds.Y+bounds.Height-5 {
		s.BranchSubmenu = false
		return true
	}
	width := min(float32(420), max(0, area.Width-24))
	x := bounds.X + bounds.Width - area.X
	if x+width > area.Width-12 {
		x = bounds.X - area.X - width
	}
	x = max(12, x)
	height := float32(12 + 28*len(sourceBranchActions))
	y := max(12, min(row.Y-area.Y-6, area.Height-height-12))
	changed := x != s.BranchSubX || y != s.BranchSubY || width != s.BranchSubWidth
	s.BranchSubX, s.BranchSubY, s.BranchSubWidth = x, y, width
	return changed
}

func (m *workspaceModel) hoverSourceBranches(world *module.Content, x, y float32) bool {
	s := m.sourceState()
	picker := s.branchMenu()
	if s.BranchSubmenu && m.pointerInsideRefs(world, x, y, []string{"sc-branch-submenu"}) {
		for i, action := range sourceBranchActions {
			if m.pointerInsideRefs(world, x, y, []string{action.ID}) && picker.actionEnabled(i, s.BranchIndex) {
				changed := s.BranchSubIndex != i
				s.BranchSubIndex = i
				return changed
			}
		}
		return false
	}
	menu, ok := world.HTMLNode(m.surface, "sc-branch-menu")
	bounds, bounded := menu.Bounds()
	if !ok || !bounded || !languageRectContains(bounds, x, y) {
		return false
	}
	var offset float32
	if entity, ok := menu.ScrollContainer(); ok {
		if scroll, ok := world.Get[scene.UIScroll](entity); ok {
			offset = scroll.Position[1]
		}
	}
	for i := range picker.Branches {
		if i >= len(s.branchView.clicks) {
			break
		}
		item, _ := world.HTMLNode(m.surface, s.branchView.clicks[i])
		row, bounded := item.Bounds()
		row.Y += offset
		// The open row extends through the menu padding on the way to its submenu.
		if s.BranchSubmenu && i == s.BranchIndex {
			row.X, row.Width = bounds.X, bounds.Width
		}
		if !bounded || !languageRectContains(row, x, y) {
			continue
		}
		changed := s.BranchIndex != i || !s.BranchSubmenu || s.BranchSubIndex != -1
		s.BranchIndex, s.BranchSubmenu, s.BranchSubIndex = i, true, -1
		return m.positionSourceBranchSubmenu(world) || changed
	}
	if s.BranchSubmenu {
		s.BranchSubmenu = false
		return true
	}
	return false
}

func (m *workspaceModel) renderSourceBranches(overlay *sourceVNodeView) {
	s := m.sourceState()
	picker := s.branchMenu()
	branches, current := picker.Branches, picker.Current
	key := sourceBranchMenuKey{
		Theme: m.workbenchTheme, Branches: sourceFirst(branches), Count: len(branches), Index: s.BranchIndex, Current: current,
		X: s.MenuX, Y: s.MenuY, Width: s.BranchWidth, Height: s.BranchHeight,
		Submenu: s.BranchSubmenu, SubIndex: s.BranchSubIndex, SubX: s.BranchSubX, SubY: s.BranchSubY, SubWidth: s.BranchSubWidth,
		Integrating: picker.Integrating, Loading: s.BranchTrigger != "sc-branches" && s.branchPicker == nil,
	}
	b := &s.branchView
	if b.used == 0 || s.branchKey != key {
		s.branchKey = key
		b.reset()
		b.open("div", "branches", "id", "sc-branch-menu", "class", "pencode-editor-context sc-branch-menu", "role", "menu", "aria-label", "Git branches", "style", fmt.Sprintf("left:%gpx;top:%gpx;width:%gpx;max-height:%gpx", s.MenuX, s.MenuY, s.BranchWidth, s.BranchHeight))
		lastGroup := ""
		for i, branch := range branches {
			group := "Local branches"
			if branch.Remote {
				group = "Remote branches"
			}
			if group != lastGroup {
				if lastGroup != "" {
					b.close()
				}
				b.open("div", group, "role", "group", "aria-label", group)
				b.text("div", "heading", group, "class", "sc-branch-heading", "aria-hidden", "true")
				lastGroup = group
			}
			checked := !branch.Remote && branch.Name == current
			class, tabindex := "sc-branch-item", "-1"
			if i == s.BranchIndex {
				class, tabindex = class+" active", "0"
			}
			b.open("button", branch.Ref, "id", "sc-branch:"+branch.Ref, "type", "button", "class", class, "tabindex", tabindex, "title", branch.Name)
			b.attr("role", "menuitem")
			b.attr("aria-haspopup", "menu")
			b.attr("aria-expanded", strconv.FormatBool(i == s.BranchIndex && s.BranchSubmenu))
			if checked {
				b.attr("aria-current", "true")
			}
			if i == s.BranchIndex && s.BranchSubmenu {
				b.attr("aria-controls", "sc-branch-submenu")
			}
			b.text("span", "name", branch.Name, "class", "sc-branch-name")
			if checked {
				m.sourceIcon(b, "current", "check", "")
			}
			m.sourceIcon(b, "submenu", "chevron-right", "")
			b.close()
		}
		if lastGroup != "" {
			b.close()
		} else if key.Loading {
			b.text("div", "loading", "Loading branches…", "class", "sc-branch-heading", "role", "status")
		} else {
			b.text("div", "empty", "No branches yet", "class", "sc-branch-heading")
		}
		b.close()
		if s.BranchSubmenu && s.BranchIndex < len(branches) {
			branch := branches[s.BranchIndex]
			b.open("div", "branch-actions", "id", "sc-branch-submenu", "class", "pencode-editor-context sc-branch-submenu", "role", "menu", "aria-label", branch.Name+" actions", "aria-labelledby", "sc-branch:"+branch.Ref, "style", fmt.Sprintf("left:%gpx;top:%gpx;width:%gpx", s.BranchSubX, s.BranchSubY, s.BranchSubWidth))
			for i, action := range sourceBranchActions {
				label := action.Label
				switch action.ID {
				case "sc-branch-merge":
					label = "Merge “" + branch.Name + "” into “" + current + "”…"
				case "sc-branch-rebase":
					label = "Rebase “" + current + "” onto “" + branch.Name + "”…"
				}
				class, tabindex := "", "-1"
				if i == s.BranchSubIndex {
					class, tabindex = "active", "0"
				}
				b.open("button", action.ID, "id", action.ID, "type", "button", "role", "menuitem", "class", class, "tabindex", tabindex, "title", label)
				if !picker.actionEnabled(i, s.BranchIndex) {
					b.attr("disabled", "")
				}
				b.text("span", "label", label, "class", "sc-branch-name")
				b.close()
			}
			b.close()
		}
	}
	overlay.append(b.nodes())
}

func (m *workspaceModel) sourceBranchShortcut(state *input.State) bool {
	s := m.sourceState()
	branches := s.branchMenu().Branches
	count := len(branches)
	switch {
	case m.uiKey(state, input.KeyTab):
		s.BranchMenuOpen, s.BranchFocus = false, true
	case m.uiKey(state, input.KeyEscape), m.uiKey(state, input.KeyLeft):
		if s.BranchSubmenu {
			s.BranchSubmenu, s.BranchSubIndex, s.BranchFocus = false, -1, true
		} else {
			s.BranchMenuOpen, s.BranchFocus = false, true
		}
	case count == 0:
		return false
	case s.BranchSubmenu && s.BranchSubIndex >= 0 && m.uiKey(state, input.KeyDown):
		s.stepBranchAction(1)
		s.BranchFocus = true
	case s.BranchSubmenu && s.BranchSubIndex >= 0 && m.uiKey(state, input.KeyUp):
		s.stepBranchAction(-1)
		s.BranchFocus = true
	case m.uiKey(state, input.KeyDown):
		s.BranchIndex = (s.BranchIndex + 1) % count
		s.BranchSubmenu, s.BranchFocus = false, true
	case m.uiKey(state, input.KeyUp):
		s.BranchIndex = (s.BranchIndex + count - 1) % count
		s.BranchSubmenu, s.BranchFocus = false, true
	case m.uiKey(state, input.KeyHome):
		if s.BranchSubmenu && s.BranchSubIndex >= 0 {
			s.BranchSubIndex = -1
			s.stepBranchAction(1)
		} else {
			s.BranchIndex, s.BranchSubmenu = 0, false
		}
		s.BranchFocus = true
	case m.uiKey(state, input.KeyEnd):
		if s.BranchSubmenu && s.BranchSubIndex >= 0 {
			s.BranchSubIndex = 0
			s.stepBranchAction(-1)
		} else {
			s.BranchIndex, s.BranchSubmenu = count-1, false
		}
		s.BranchFocus = true
	case m.uiKey(state, input.KeyRight), m.uiKey(state, input.KeyEnter), m.uiKey(state, input.KeySpace):
		if s.BranchSubmenu && s.BranchSubIndex >= 0 {
			if !m.uiKey(state, input.KeyRight) {
				m.sourceAction(sourceBranchActions[s.BranchSubIndex].ID)
			}
		} else if s.BranchTrigger != "sc-branches" && !m.uiKey(state, input.KeyRight) {
			m.sourceAction("sc-branch:" + branches[s.BranchIndex].Ref)
		} else {
			s.BranchSubmenu, s.BranchSubIndex, s.BranchFocus = true, -1, true
			s.stepBranchAction(1)
			m.positionSourceBranchSubmenu(m.fontResources)
		}
	default:
		return false
	}
	return true
}

func (m *workspaceModel) focusSourceBranches(world *module.Content) {
	s := m.sourceState()
	if !s.BranchFocus {
		return
	}
	id := s.BranchTrigger
	branches := s.branchMenu().Branches
	if s.BranchMenuOpen && len(branches) != 0 {
		id = s.branchView.clicks[s.BranchIndex]
		if s.BranchSubmenu && s.BranchSubIndex >= 0 {
			id = sourceBranchActions[s.BranchSubIndex].ID
		}
	}
	if entity, ok := world.HTMLControl(m.surface, id); ok && world.Focus(entity) {
		s.BranchFocus = false
		if s.BranchMenuOpen && (!s.BranchSubmenu || s.BranchSubIndex < 0) {
			menu, _ := world.HTMLNode(m.surface, "sc-branch-menu")
			item, _ := world.HTMLNode(m.surface, id)
			bounds, _ := menu.Bounds()
			row, _ := item.Bounds()
			if entity, ok := menu.ScrollContainer(); ok {
				if scroll := world.Mut[scene.UIScroll](entity); scroll != nil {
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
