package workbench

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	"github.com/onix-engine/onix/text/piecetree"
	"github.com/pencode-labs/pencode/protocol"
)

const localHistoryOpenPrefix = "sc-open:local-history:"
const localHistoryDayPrefix = "sc-local-day:"

type localHistoryRow struct {
	id, date, tooltip string
	version           protocol.FileVersion
}

type localHistory struct {
	loading                                  *fileIOJob
	collapsedDays                            map[string]bool
	scope, err                               string
	rows                                     []localHistoryRow
	selected                                 protocol.FileVersion
	revision                                 uint64
	expanded, directory, more, menu, refresh bool
}

func (h *localHistory) invalidate(files []protocol.FileEvent, reset bool) {
	if !h.expanded || h.refresh {
		return
	}
	h.refresh = reset
	for _, f := range files {
		if h.scope == "" || f.Path == h.scope || h.directory && pencodeResourceContains(h.scope, f.Path) || f.Directory && pencodeResourceContains(f.Path, h.scope) {
			h.refresh = true
			break
		}
	}
}

func (m *workspaceModel) showLocalHistory(p *pencodeProject, scope string, directory bool) {
	m.activateProject(p)
	if m.bottom.active != pencodePanelSourceControl {
		m.sourceAction("sc-toggle")
	}
	h := &p.sourceControl.history
	if h.loading != nil {
		h.loading.cancel()
	}
	*h = localHistory{scope: scope, expanded: true, directory: directory, revision: h.revision + 1}
	m.loadLocalHistory(protocol.FileVersion{})
	m.sourceChanged()
}

func (m *workspaceModel) loadLocalHistory(before protocol.FileVersion) {
	p := m.activeProjectState()
	if p == nil {
		return
	}
	h := &p.sourceControl.history
	h.refresh = false
	j := &fileIOJob{kind: fileHistoryList, root: p.Path, project: p, relative: h.scope, directory: h.directory || h.scope == "", version: before}
	h.err = ""
	if err := m.submitFileIO(j); err != nil {
		h.err = err.Error()
	} else {
		h.loading = j
	}
	h.revision++
}

func (m *workspaceModel) acceptLocalHistory(r *fileIOResult) {
	j := r.job
	p := j.project
	h := &p.sourceControl.history
	if j.kind == fileHistoryRestore {
		if r.err != nil {
			m.setProjectStatus(p, r.err.Error())
			h.err = r.err.Error()
		} else {
			if f := p.Files[j.version.Path]; f != nil {
				f.externalChanged = true
				m.reloadExternalFiles(p)
			}
			m.refreshResourceProject(p)
			p.sourceControl.RefreshPending = true
			h.refresh = h.expanded
			m.setProjectStatus(p, fmt.Sprintf(m.tr("已恢复 %s"), j.version.Path))
		}
		h.revision++
		return
	}
	if h.loading != j {
		return
	}
	h.loading = nil
	h.revision++
	if r.err != nil {
		h.err = r.err.Error()
		return
	}
	if j.version.Time == 0 {
		clear(h.rows)
		h.rows = h.rows[:0]
	}
	for _, version := range r.versions {
		id := localHistoryOpenPrefix + strconv.FormatUint(version.Time, 16) + ":" + url.PathEscape(version.Path)
		date := time.Unix(0, int64(version.Time)).Local().Format("2006-01-02 15:04:05.000")
		h.rows = append(h.rows, localHistoryRow{version: version, id: id, date: date, tooltip: version.Path + "\n" + date})
	}
	h.more = r.historyMore
}

func (m *workspaceModel) localHistoryAction(id string) {
	p := m.activeProjectState()
	if p == nil {
		return
	}
	h := &p.sourceControl.history
	switch id {
	case "sc-local-toggle":
		h.expanded = !h.expanded
		h.menu = false
		h.refresh = h.expanded
		if !h.expanded && h.loading != nil {
			h.loading.cancel()
			h.loading = nil
		}
	case "sc-local-more":
		if h.more && h.loading == nil && len(h.rows) != 0 {
			m.loadLocalHistory(h.rows[len(h.rows)-1].version)
		}
	case "sc-local-restore":
		if !h.menu || h.selected.Time == 0 {
			return
		}
		h.menu = false
		if m.historyTargetDirty(p, h.selected.Path) {
			h.err = m.tr("请先保存此文件的修改，再恢复历史版本。")
		} else if err := m.submitResourceIO(&fileIOJob{kind: fileHistoryRestore, project: p, root: p.Path, version: h.selected}); err != nil {
			h.err = err.Error()
		} else {
			h.err = ""
		}
	default:
		day, ok := strings.CutPrefix(id, localHistoryDayPrefix)
		if !ok {
			return
		}
		h.collapsedDays = togglePencodeFlag(h.collapsedDays, day)
		h.menu = false
	}
	h.revision++
}

func (m *workspaceModel) openLocalHistoryDiff(id string) {
	p := m.activeProjectState()
	if p == nil || !p.sourceControl.history.expanded {
		return
	}
	s := &p.sourceControl
	for _, row := range s.history.rows {
		if row.id != id {
			continue
		}
		s.history.selected = row.version
		s.history.revision++
		s.Selection++
		s.OpenID, s.OpenKey, s.OpenPinned = id, id, false
		r := sourceRepository{Root: p.Path, Backend: m.backend, Context: s.Context}
		var draft *piecetree.Snapshot
		if f := p.Files[row.version.Path]; f != nil && f.Dirty {
			if f.page.Paged || f.snapshot().Len() > protocol.FileComparisonLimit {
				s.Notice = "file exceeds the 2 MiB text comparison limit"
				return
			}
			snapshot := f.snapshot()
			draft = &snapshot
		}
		m.sourceJob(sourceQuery, func() sourceResult {
			before, err := r.Backend.ReadFileHistory(r.baseContext(), r.Root, row.version)
			if err != nil {
				return sourceResult{Err: err}
			}
			var current string
			if draft != nil {
				current = draft.String()
			} else {
				data, err := r.workFile(row.version.Path)
				if err != nil {
					return sourceResult{Err: err}
				}
				current = string(data)
			}
			tab := &sourceTab{Key: id, Kind: "local-history", Revision: row.date, Path: row.version.Path, Preview: true}
			tab, err = r.compareFile(tab, string(before), current)
			return sourceResult{Tab: tab, Err: err}
		})
		return
	}
}

func (m *workspaceModel) historyTargetDirty(project *pencodeProject, name string) bool {
	target := filepath.Join(project.Path, filepath.FromSlash(name))
	for _, p := range m.projects {
		rel, err := filepath.Rel(p.Path, target)
		if err == nil && filepath.IsLocal(rel) {
			relative := filepath.ToSlash(rel)
			if f := p.Files[relative]; f != nil && (f.Dirty || m.resourceBusy(p, relative)) {
				return true
			}
		}
		s := &p.sourceControl
		if dirtyHistoryTab(s.Snapshot.Repository.Root, target, s.Tabs) {
			return true
		}
		for root, view := range s.RepositoryViews {
			if dirtyHistoryTab(root, target, view.Tabs) {
				return true
			}
		}
	}
	return false
}

func dirtyHistoryTab(root, target string, tabs []*sourceTab) bool {
	for _, tab := range tabs {
		if tab.Result != tab.Saved && filepath.Join(root, filepath.FromSlash(tab.Path)) == target {
			return true
		}
	}
	return false
}

func (m *workspaceModel) renderLocalHistory(b *sourceVNodeView) {
	h := &m.sourceState().history
	title := m.tr("本地历史")
	b.open("aside", "local-history", "id", "sc-local-history", "class", "sc-local-history", "aria-label", title, "data-collapsed", strconv.FormatBool(!h.expanded))
	b.open("header", "header", "class", "sc-repositories-header")
	if h.expanded {
		b.text("h2", "title", title, "id", "sc-local-title", "class", "sc-heading")
	}
	b.open("button", "toggle", "id", "sc-local-toggle", "type", "button", "class", "pencode-icon-button sc-repositories-toggle", "title", title, "aria-label", title, "aria-expanded", strconv.FormatBool(h.expanded), "aria-controls", "sc-local-list")
	m.sourceIcon(b, "icon", "chevron-right", "")
	b.close()
	b.close()
	if !h.expanded {
		b.text("div", "list", "", "id", "sc-local-list", "hidden", "")
		b.close()
		return
	}
	if h.scope != "" {
		b.text("div", "scope", h.scope, "class", "sc-local-scope", "title", h.scope)
	}
	b.open("div", "list", "id", "sc-local-list", "class", "sc-repositories-list", "aria-busy", strconv.FormatBool(h.loading != nil))
	if h.err != "" {
		b.text("p", "error", h.err, "class", "muted", "role", "status")
	} else if len(h.rows) == 0 {
		label := "暂无本地历史"
		if h.loading != nil {
			label = "正在加载…"
		}
		b.text("p", "empty", m.tr(label), "class", "muted")
	}
	var day string
	var collapsed bool
	for _, row := range h.rows {
		date, clock, _ := strings.Cut(row.date, " ")
		if date != day {
			day = date
			collapsed = h.collapsedDays[day]
			b.open("h3", day, "class", "sc-local-day")
			b.open("button", "toggle", "id", localHistoryDayPrefix+day, "type", "button", "class", "sc-group-toggle", "aria-expanded", strconv.FormatBool(!collapsed))
			icon := "chevron-down"
			if collapsed {
				icon = "chevron-right"
			}
			m.sourceIcon(b, "arrow", icon, "")
			b.text("span", "label", day, "class", "sc-group-label")
			b.close()
			b.close()
		}
		if collapsed {
			continue
		}
		clock, _, _ = strings.Cut(clock, ".")
		name := path.Base(row.version.Path)
		b.open("button", row.id, "id", row.id, "type", "button", "class", "sc-local-version", "aria-pressed", strconv.FormatBool(h.selected == row.version), "title", row.tooltip)
		m.sourceFileIcon(b, "icon", name, false, false)
		b.open("span", "labels", "class", "sc-local-labels")
		b.open("span", "summary", "class", "sc-local-summary")
		b.text("span", "name", name, "class", "sc-repository-name")
		b.text("span", "time", clock, "class", "sc-local-time")
		b.close()
		b.text("span", "path", row.version.Path, "class", "sc-local-path")
		b.close()
		b.close()
	}
	if h.more {
		sourceButton(b, "sc-local-more", m.tr("加载更早的版本"), h.loading != nil)
	}
	b.close()
	b.close()
}

func (m *workspaceModel) renderLocalHistoryMenu(b *sourceVNodeView) {
	s := m.sourceState()
	b.open("div", "local-menu", "id", "sc-local-menu", "class", "pencode-editor-context sc-context-menu", "role", "menu", "aria-label", m.tr("本地历史"), "style", languagePopupStyle(s.MenuX, s.MenuY))
	b.button("sc-local-restore", m.tr("恢复此版本"), "role", "menuitem")
	b.close()
}

func (m *workspaceModel) updateLocalHistoryMenu(world *module.Content, state *input.State, x, y float32) bool {
	s := m.sourceState()
	h := &s.history
	if !h.expanded || m.bottom.active != pencodePanelSourceControl || s.modal() || !state.MousePressed(input.MouseRight) && !state.MousePressed(input.MouseLeft) {
		return false
	}
	if h.menu && m.pointerInsideRefs(world, x, y, []string{"sc-local-menu"}) {
		return false
	}
	changed := h.menu
	h.menu = false
	if !state.MousePressed(input.MouseRight) || !m.pointerInsideRefs(world, x, y, []string{"sc-local-list"}) {
		return changed
	}
	list, _ := world.HTMLNode(m.surface, "sc-local-list")
	var offset float32
	if entity, ok := list.ScrollContainer(); ok {
		if scroll := world.Ref[scene.UIScroll](entity); scroll != nil {
			offset = scroll.Position.Y()
		}
	}
	for _, row := range h.rows {
		node, ok := world.HTMLNode(m.surface, row.id)
		if !ok {
			continue
		}
		bounds, ok := node.Bounds()
		bounds.Y += offset
		if ok && languageRectContains(bounds, x, y) {
			root, _ := world.HTMLNode(m.surface, "pencode-root")
			area, _ := root.Bounds()
			s.MenuX, s.MenuY = max(0, min(x-area.X, area.Width-270)), max(0, min(y-area.Y, area.Height-45))
			s.MenuOpen, s.BranchMenuOpen = false, false
			h.menu, h.selected = true, row.version
			h.revision++
			return true
		}
	}
	return changed
}
