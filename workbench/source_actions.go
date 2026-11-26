package workbench

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onix-engine/onix/input"
)

func (a *workspaceModel) sourceClick(id string, now time.Time) {
	s := a.sourceState()
	double := s.lastClick == id && !s.lastClickAt.IsZero() && now.Sub(s.lastClickAt) <= 500*time.Millisecond
	s.lastClick, s.lastClickAt = id, now
	if double && (strings.HasPrefix(id, "sc-open:") || strings.HasPrefix(id, "sc-tab:")) {
		s.lastClick = ""
		if s.PendingOpen == id {
			s.PendingPinned = true
			return
		}
		key := strings.TrimPrefix(id, "sc-tab:")
		if id == s.OpenID {
			key, s.OpenPinned = s.OpenKey, true
		}
		for _, tab := range s.Tabs {
			if tab.Key == key {
				tab.Preview = false
			}
		}
		a.sourceChanged()
		return
	}
	a.sourceAction(id)
}

func (a *workspaceModel) sourceMutation(r sourceRepository, message string, operation func() error) {
	a.writeSourceRepository(r, func() sourceResult {
		return sourceResult{Err: operation(), Message: message}
	})
}

func (a *workspaceModel) writeSourceRepository(r sourceRepository, operation func() sourceResult) {
	s := a.sourceState()
	if r.Root == "" || s.Done != nil || s.repositoryBusy(r.Root) {
		return
	}
	active := r.Root == s.Snapshot.Repository.Root
	if !active {
		s.RefreshPending = true
	}
	a.projectSourceJob(s, r.Root, sourceWrite, func() sourceResult {
		result := operation()
		snapshot, err := r.snapshot(sourceHistoryPageSize)
		if active || err == nil {
			result.Snapshot = &snapshot
		}
		if result.Err == nil {
			result.Err = err
		}
		return result
	})
}

func (a *workspaceModel) sourceAction(id string) {
	s := a.sourceState()
	if id == "sc-notice-close" {
		s.Notice = ""
		s.FocusedModal, s.FocusedTab = "", ""
		a.sourceChanged()
		return
	}
	if id == "sc-prompt-cancel" {
		s.TabClose = nil
		s.Prompt, s.promptPicker = "", nil
		s.FocusedModal, s.FocusedTab = "", ""
		a.sourceChanged()
		return
	}
	if s.Notice != "" {
		return
	}
	if s.Prompt != "" && id != "sc-prompt-confirm" {
		return
	}
	if strings.HasPrefix(id, "sc-local-") {
		a.localHistoryAction(id)
		a.sourceChanged()
		return
	}
	if id == "sc-panel-close" && a.bottom.active == pencodePanelSourceControl {
		a.handleBottomPanelEvent(a.fontResources, "panel-close", "")
		a.sourceChanged()
		return
	}
	if id == "sc-toggle" {
		if a.projectPath == "" {
			return
		}
		if a.bottom.active == pencodePanelSourceControl {
			a.bottom.active = pencodePanelClosed
			a.sourceChanged()
			return
		}
		a.bottom.active = pencodePanelSourceControl
		a.findOpen, a.quickOpen = false, false
		a.clearFindCache()
		a.dismissLanguageContext()
		a.dismissLanguagePanel()
		if a.fontResources != nil {
			a.fontResources.Focus(0)
		}
		s.cancelRefresh()
		a.refreshSourceControl()
		a.sourceChanged()
		return
	}
	if strings.HasPrefix(id, "sc-tab:") {
		a.actions.ui.editor = nil
		s.Active, s.RestoreScroll = strings.TrimPrefix(id, "sc-tab:"), true
		s.Selection++
		a.sourceChanged()
		return
	}
	if strings.HasPrefix(id, "sc-folder:") || strings.HasPrefix(id, "sc-group:") {
		height := s.graphDetailsHeight()
		key := strings.TrimPrefix(strings.TrimPrefix(id, "sc-"), "folder:")
		s.Collapsed = togglePencodeFlag(s.Collapsed, key)
		s.TreeRevision++
		s.resizeGraphDetails(height)
		a.sourceChanged()
		return
	}
	if strings.HasPrefix(id, "sc-commit:") && strings.TrimPrefix(id, "sc-commit:") == s.SelectedHash {
		height := s.graphDetailsHeight()
		s.SelectedHash, s.CommitFiles = "", nil
		s.resizeGraphDetails(height)
		a.sourceChanged()
		return
	}
	if id == "sc-repositories-toggle" && a.bottom.active == pencodePanelSourceControl {
		s.RepositoriesCollapsed = !s.RepositoriesCollapsed
		s.BranchMenuOpen, s.BranchSubmenu, s.BranchFocus = false, false, false
		a.sourceChanged()
		return
	}
	if root, ok := strings.CutPrefix(id, "sc-repository:"); ok {
		if a.bottom.active == pencodePanelSourceControl && slices.Contains(s.Repositories, root) {
			a.switchSourceRepository(root, false)
		}
		return
	}
	if target, ok := strings.CutPrefix(id, sourceRepositoryActionPrefix); ok {
		if a.bottom.active == pencodePanelSourceControl && !s.RepositoriesCollapsed {
			op, root, _ := strings.Cut(target, ":")
			for _, action := range sourceRepositoryActions {
				if op == action.Name {
					a.startSourceRepositoryAction(root, op)
					break
				}
			}
		}
		return
	}
	// Periodic reads must not swallow clicks or block foreground operations.
	// Their private context and mailbox can be discarded without cancelling writes.
	s.cancelRefresh()
	open := strings.HasPrefix(id, "sc-open:") || strings.HasPrefix(id, "sc-diff:")
	if s.Done != nil || open && s.repositoryJobs[s.Snapshot.Repository.Root].Done != nil {
		if open {
			s.PendingPinned = s.PendingPinned && s.PendingOpen == id
			s.PendingOpen = id
			s.PendingSelection = s.Selection
		}
		return
	}
	if root, ok := strings.CutPrefix(id, sourceRepositoryBranchPrefix); ok {
		if a.bottom.active == pencodePanelSourceControl && !s.RepositoriesCollapsed && root != s.Snapshot.Repository.Root && !s.repositoryBusy(root) && slices.Contains(s.Repositories, root) {
			s.PendingOpen, s.PendingPinned = "", false
			a.toggleSourceBranches(id)
			a.sourceChanged()
		}
		return
	}
	if ref, ok := strings.CutPrefix(id, "sc-branch:"); ok {
		if !s.BranchMenuOpen {
			return
		}
		picker := s.branchMenu()
		for i, branch := range picker.Branches {
			if branch.Ref != ref {
				continue
			}
			if s.branchPicker != nil {
				s.BranchMenuOpen, s.BranchSubmenu, s.BranchFocus = false, false, true
				if branch.Remote || branch.Name != picker.Current {
					a.switchSourceBranch(picker, branch)
				}
			} else {
				s.BranchIndex, s.BranchSubmenu, s.BranchSubIndex = i, true, -1
				a.positionSourceBranchSubmenu(a.fontResources)
			}
			break
		}
		a.sourceChanged()
		return
	}
	for i, action := range sourceBranchActions {
		if action.ID != id {
			continue
		}
		picker := s.branchMenu()
		if !s.BranchMenuOpen || !s.BranchSubmenu || !picker.actionEnabled(i, s.BranchIndex) {
			return
		}
		branch := picker.Branches[s.BranchIndex]
		s.BranchMenuOpen, s.BranchSubmenu, s.BranchFocus = false, false, true
		switch action.ID {
		case "sc-branch-checkout":
			a.switchSourceBranch(picker, branch)
		case "sc-branch-create":
			s.Prompt, s.PromptHash, s.PromptValue = "branch", branch.Ref, ""
		case "sc-branch-tag":
			s.Prompt, s.PromptHash, s.PromptValue = "tag", branch.Ref, ""
		case "sc-branch-rename":
			s.Prompt, s.PromptHash, s.PromptValue = "rename-branch", branch.Ref, branch.Name
		case "sc-branch-delete":
			s.Prompt, s.PromptHash = "delete-branch", branch.Ref
		case "sc-branch-merge", "sc-branch-rebase":
			s.Prompt, s.PromptHash = strings.TrimPrefix(action.ID, "sc-branch-"), branch.Ref
		}
		if s.Prompt != "" {
			s.promptPicker = s.branchPicker
		}
		a.sourceChanged()
		return
	}
	if s.repositoryJobs[s.Snapshot.Repository.Root].Done != nil && !(id == "sc-prompt-confirm" && s.promptPicker != nil) {
		return
	}
	s.MenuOpen = false
	if id != "sc-branches" {
		s.BranchMenuOpen, s.BranchSubmenu = false, false
	}
	r := s.Snapshot.Repository
	switch {
	case id == "sc-branches":
		a.toggleSourceBranches(id)
	case id == "sc-refresh":
		s.history.refresh = s.history.expanded
		s.RepositoriesLoaded = false
		a.refreshSourceControl()
	case id == "sc-more":
		a.loadSourceHistory()
	case strings.HasPrefix(id, "sc-commit:"):
		hash := strings.TrimPrefix(id, "sc-commit:")
		index := slices.IndexFunc(s.Snapshot.Commits, func(c sourceCommit) bool { return c.Hash == hash })
		if index < 0 {
			return
		}
		height := s.graphDetailsHeight()
		s.SelectedHash, s.CommitFiles = "", nil
		s.resizeGraphDetails(height)
		s.SelectedHash, s.ExpandedCommit = hash, index
		a.sourceJob(sourceQuery, func() sourceResult {
			files, err := r.commitFiles(hash)
			return sourceResult{Hash: hash, Files: files, Err: err}
		})
	case strings.HasPrefix(id, "sc-diff:"):
		name, err := url.PathUnescape(strings.TrimPrefix(id, "sc-diff:"))
		if err != nil {
			return
		}
		s.Selection++
		if s.Context == nil {
			s.Context, s.Cancel = context.WithCancel(context.Background())
		}
		backend, project, ctx, knownRoots := a.backend, a.projectPath, s.Context, s.Repositories
		owner := a.activeProjectState()
		reuseRoots := owner != nil && owner.watch != nil && s.RepositoriesLoaded
		a.sourceJob(sourceQuery, func() sourceResult {
			var roots []string
			root := sourceRepositoryForPath(knownRoots, name)
			if reuseRoots && root != "" {
				// A nested repository can appear before its watch event arrives.
				data, err := backend.Git(ctx, project, "-C", path.Dir(name), "rev-parse", "--show-toplevel")
				reuseRoots = err == nil && strings.TrimSuffix(string(data), "\n") == root
			}
			if !reuseRoots || root == "" {
				var err error
				roots, err = backend.GitRepositories(ctx, project)
				if err != nil {
					return sourceResult{Err: err}
				}
				root = sourceRepositoryForPath(roots, name)
			}
			if root == "" {
				return sourceResult{Err: fmt.Errorf("文件不在 Git 仓库中：%s", name)}
			}
			repository := sourceRepository{Root: root, Backend: backend, Context: ctx}
			snapshot, err := repository.refreshSnapshot(true)
			result := sourceResult{Repositories: roots, Snapshot: &snapshot, DiffPath: name, Err: err, StatusOnly: true}
			if roots != nil && !slices.Equal(knownRoots, roots) {
				result.RepositoryRefs, result.RepositorySync = repository.repositoryHeads(roots, &snapshot)
			}
			return result
		})
	case strings.HasPrefix(id, localHistoryOpenPrefix):
		a.openLocalHistoryDiff(id)
	case strings.HasPrefix(id, "sc-open:"):
		parts := strings.SplitN(strings.TrimPrefix(id, "sc-open:"), ":", 2)
		if len(parts) != 2 {
			return
		}
		name, err := url.PathUnescape(parts[1])
		if err != nil {
			return
		}
		files := s.Snapshot.Files
		if parts[0] == "history" {
			files = s.CommitFiles
		}
		for _, file := range files {
			if file.Path != name {
				continue
			}
			if parts[0] != "history" && parts[0] != "staged" && a.sourcePathDirty(file.Path) {
				s.Notice = a.tr("请先保存此文件的编辑内容，再打开工作区比较或合并。")
				break
			}
			kind, hash := parts[0], s.SelectedHash
			if file.Conflict {
				kind = "merge"
			}
			key := kind + ":" + hash + ":" + file.Path
			if kind != "history" {
				hash = ""
				key = kind + "::" + file.Path
			}
			for _, tab := range s.Tabs {
				if tab.Key == key && tab.Result != tab.Saved {
					tab.Preview = false
					s.Active, s.RestoreScroll = key, true
					s.Selection++
					a.sourceChanged()
					return
				}
			}
			s.Selection++
			s.OpenID, s.OpenKey, s.OpenPinned = id, key, false
			a.sourceJob(sourceQuery, func() sourceResult {
				tab, err := r.openDiff(file, kind, hash)
				if tab != nil {
					tab.Preview = true
				}
				return sourceResult{Tab: tab, Err: err}
			})
			break
		}
	case id == "sc-discard-all":
		s.Prompt, s.PromptHash = "discard-all", ""
	case strings.HasPrefix(id, "sc-discard:"), strings.HasPrefix(id, "sc-discard-folder:"):
		op, prefix := "discard-file", "sc-discard:"
		if strings.HasPrefix(id, "sc-discard-folder:") {
			op, prefix = "discard-folder", "sc-discard-folder:"
		}
		kind, encoded, ok := strings.Cut(strings.TrimPrefix(id, prefix), ":")
		if !ok || kind != "changes" {
			return
		}
		name, err := url.PathUnescape(encoded)
		if err != nil || name == "" {
			return
		}
		s.Prompt, s.PromptHash = op, name
	case strings.HasPrefix(id, "sc-stage:"), strings.HasPrefix(id, "sc-stage-folder:"), strings.HasPrefix(id, "sc-unstage:"), strings.HasPrefix(id, "sc-unstage-folder:"), id == "sc-stage-all", id == "sc-unstage-all":
		unstage := strings.HasPrefix(id, "sc-unstage")
		all := id == "sc-stage-all" || id == "sc-unstage-all"
		folder := strings.HasPrefix(id, "sc-stage-folder:") || strings.HasPrefix(id, "sc-unstage-folder:")
		var files []sourceFile
		name := ""
		if !all {
			_, encoded, _ := strings.Cut(id, ":")
			var err error
			name, err = url.PathUnescape(encoded)
			if err != nil {
				return
			}
			if folder {
				name += "/"
			}
		}
		for _, f := range s.Snapshot.Files {
			if !all && (folder && !strings.HasPrefix(f.Path, name) || !folder && f.Path != name) {
				continue
			}
			if unstage && (f.Index == ' ' || f.Index == '?' || f.Conflict) || !unstage && f.Work == ' ' && !f.Conflict {
				continue
			}
			if !unstage && a.sourcePathDirty(f.Path) {
				s.Notice = a.tr("请先保存文件的编辑内容，再暂存。")
				a.sourceChanged()
				return
			}
			files = append(files, f)
		}
		head := s.Snapshot.HasHead
		a.sourceMutation(r, "Index updated", func() error {
			if unstage {
				return r.unstage(files, head)
			}
			return r.stage(files)
		})
	case id == "sc-create-commit":
		if s.Snapshot.Rebasing {
			s.Prompt = "rebase-continue"
			a.confirmSourceOperation()
			break
		}
		if s.Snapshot.syncPending() {
			if a.sourceHasDrafts() || s.unsaved() {
				s.Notice = a.tr("请先保存编辑内容，再同步远程。")
				break
			}
			a.startSourceRepositoryJob(r, sourceRepositorySync, func(r sourceRepository) sourceResult {
				reload, err := r.syncChanges()
				return sourceResult{Err: err, Message: "Changes synchronized", Reload: reload}
			})
			break
		}
		if len(s.Snapshot.Files) == 0 && s.Snapshot.MergeMessage == "" {
			break
		}
		if s.unsaved() || len(s.Snapshot.Groups[0]) != 0 {
			s.Notice = a.tr("请先完成并保存所有合并，再提交。")
			break
		}
		if len(s.Snapshot.Groups[1]) == 0 && a.sourceHasDrafts() {
			s.Notice = a.tr("请先保存编辑内容，再自动暂存并提交。")
			break
		}
		message := s.CommitMessage
		a.startSourceRepositoryJob(r, sourceRepositoryCommit, func(r sourceRepository) sourceResult {
			return sourceResult{Err: r.commit(message), Message: "Commit created"}
		})
	case id == "sc-close-tab" || strings.HasPrefix(id, "sc-close-tab:"):
		key := s.Active
		if id != "sc-close-tab" {
			key = strings.TrimPrefix(id, "sc-close-tab:")
		}
		for _, tab := range s.Tabs {
			if tab.Key == key && tab.Result != tab.Saved {
				s.Prompt, s.PromptHash = "discard-tab", key
				key = ""
				break
			}
		}
		if key != "" {
			a.closeSourceTab(key)
		}
	case id == "sc-save-merge":
		if tab := s.activeTab(); tab != nil && tab.Kind == "merge" {
			if a.sourcePathDirty(tab.Path) {
				s.Notice = a.tr("该文件还有未保存的普通编辑内容，请先保存后重新打开合并。")
				break
			}
			value := *tab
			a.sourceJob(sourceWrite, func() sourceResult {
				err := r.saveMerge(value)
				savedKey := ""
				if disk, readErr := r.workFile(value.Path); readErr == nil && string(disk) == value.Result {
					savedKey = value.Key
				}
				snapshot, e := r.snapshot(sourceHistoryPageSize)
				if err == nil {
					err = e
				}
				return sourceResult{Snapshot: &snapshot, SavedKey: savedKey, Err: err, Message: "Merge resolved and staged", Reload: true}
			})
		}
	case strings.HasPrefix(id, "sc-revert-block:"):
		if index, err := strconv.Atoi(strings.TrimPrefix(id, "sc-revert-block:")); err == nil {
			if tab := s.activeTab(); tab != nil {
				tab.revertBlock(index)
			}
		}
	case id == "sc-save-diff":
		if tab := s.activeTab(); tab != nil && tab.editable() && tab.Result != tab.Saved {
			if a.sourcePathDirty(tab.Path) {
				s.Notice = a.tr("该文件还有未保存的普通编辑内容，请先保存后重新打开差异。")
				break
			}
			value := *tab
			a.sourceJob(sourceWrite, func() sourceResult {
				err := r.saveWorktree(value)
				if err != nil {
					return sourceResult{Err: err}
				}
				snapshot, err := r.snapshot(sourceHistoryPageSize)
				return sourceResult{Snapshot: &snapshot, SavedKey: value.Key, Message: "File saved", Reload: true, Err: err}
			})
		}
	case strings.HasPrefix(id, "sc-use:"):
		a.useSourceSide(strings.TrimPrefix(id, "sc-use:"), s.MenuPath)
	case strings.HasPrefix(id, "sc-accept:"):
		if tab := s.activeTab(); tab != nil && tab.Kind == "merge" {
			s.Prompt, s.PromptHash = strings.TrimPrefix(id, "sc-accept:"), tab.Key
		}
	case strings.HasPrefix(id, "sc-resolve:"):
		parts := strings.Split(strings.TrimPrefix(id, "sc-resolve:"), ":")
		if len(parts) == 2 {
			index, err := strconv.Atoi(parts[0])
			if tab := s.activeTab(); err == nil && tab != nil && tab.Kind == "merge" {
				tab.editResult(resolveSourceConflict(tab.Result, index, parts[1]))
			}
		}
	case strings.HasPrefix(id, "sc-op:"):
		op := strings.TrimPrefix(id, "sc-op:")
		if op == "copy" {
			if a.fontResources != nil {
				if state, ok := a.fontResources.Resource[*input.State](); ok {
					state.SetClipboardText(s.MenuHash)
				}
			}
		} else if op == "fetch" {
			a.sourceMutation(r, "Fetch completed", func() error { _, err := r.command("fetch", "--all", "--prune"); return err })
		} else {
			s.Prompt, s.PromptHash, s.PromptValue = op, s.MenuHash, ""
		}
	case id == "sc-prompt-confirm":
		a.confirmSourceOperation()
	}
	a.sourceChanged()
}

// Resolve against freshly read status: Codex can publish a file before the
// workspace watcher has refreshed Git, or while another repository is selected.
func (a *workspaceModel) openSourceDiffPath(name string) {
	s := a.sourceState()
	relative, err := filepath.Rel(filepath.FromSlash(s.Snapshot.Repository.Root), filepath.FromSlash(name))
	if err != nil || !filepath.IsLocal(relative) {
		return
	}
	relative = filepath.ToSlash(relative)
	for _, file := range s.Snapshot.Files {
		if file.Path != relative {
			continue
		}
		kind := "changes"
		if file.Work == ' ' && file.Index != '?' {
			kind = "staged"
		}
		a.sourceAction("sc-open:" + kind + ":" + url.PathEscape(relative))
		return
	}
	s.Notice = a.tr("此文件当前没有 Git 差异。")
	a.sourceChanged()
}

func (a *workspaceModel) closeSourceTab(key string) {
	s := a.sourceState()
	for i, tab := range s.Tabs {
		if tab.Key == key {
			s.releaseTab(tab)
			if s.Active == key {
				s.Active = ""
				s.Selection++
				s.RestoreScroll = true
				a.selectEditorTabNeighbor(pencodeEditorTab{project: a.activeProjectState(), source: tab})
			}
			s.Tabs = slices.Delete(s.Tabs, i, i+1)
			return
		}
	}
}

func (s *pencodeSourceControl) releaseTab(tab *sourceTab) {
	if s.overview.dragTab == tab {
		s.overview.dragTab = nil
	}
	if s.gutterTab == tab {
		s.gutterTab = nil
	}
	if s.diffPaneTab == tab {
		s.diffPaneTab = nil
		s.diffEditors = [2]pencodeEditor{}
		s.inlinePaint = [2]sourceInlinePaint{}
	}
	if s.Active == tab.Key && tab.Kind == "merge" {
		s.mergeEditor = pencodeEditor{}
	}
}

func (a *workspaceModel) confirmSourceOperation() {
	s := a.sourceState()
	op, hash, value := s.Prompt, s.PromptHash, strings.TrimSpace(s.PromptValue)
	if (op == "branch" || op == "tag" || op == "rename-branch") && value == "" {
		return
	}
	picker := s.promptBranches()
	r := s.Snapshot.Repository
	r.Root = picker.Root
	s.Prompt, s.promptPicker = "", nil
	s.FocusedModal, s.FocusedTab = "", ""
	if op == "discard-tabs" {
		tabs := s.TabClose
		s.TabClose = nil
		a.discardEditorTabs(tabs)
		return
	}
	if op == "discard-tab" {
		a.closeSourceTab(hash)
		return
	}
	if op == "discard-file" || op == "discard-folder" || op == "discard-all" {
		a.discardSourceChanges(op, hash)
		return
	}
	if op == "local" || op == "incoming" || op == "both" {
		if tab := s.activeTab(); tab != nil && tab.Key == hash {
			result := tab.Result
			switch op {
			case "local":
				result = tab.Local
			case "incoming":
				result = tab.Incoming
			case "both":
				result = tab.Local
				if result != "" && !strings.HasSuffix(result, "\n") {
					result += "\n"
				}
				result += tab.Incoming
			}
			tab.editResult(result)
		}
		return
	}
	if op == "delete-branch" || op == "rename-branch" {
		if branch, ok := picker.branch(hash); ok {
			if op == "rename-branch" {
				a.sourceMutation(r, "Branch renamed", func() error { return r.renameBranch(branch, value) })
			} else {
				a.sourceMutation(r, "Branch deleted", func() error { return r.deleteBranch(branch) })
			}
		}
		return
	}
	if a.sourceHasDraftsIn(r.Root) {
		s.Notice = a.tr("请先保存编辑器中的项目修改，再执行 Git 工作区操作。")
		return
	}
	var args []string
	var mergeBranch sourceBranch
	switch op {
	case "checkout":
		args = []string{"switch", "--detach", hash}
	case "branch":
		args = []string{"switch", "-c", value, hash}
	case "tag":
		args = []string{"tag", "--", value, hash}
	case "merge":
		var ok bool
		if mergeBranch, ok = picker.branch(hash); !ok {
			mergeBranch = s.Snapshot.branchAt(hash)
		}
	case "rebase":
		args = []string{"rebase", "--no-autostash", hash}
	case "rebase-continue":
		args = []string{"-c", "core.editor=true", "rebase", "--continue"}
	case "cherry-pick":
		args = []string{"cherry-pick", hash}
	case "revert":
		args = []string{"revert", "--no-edit", hash}
	case "reset-soft":
		args = []string{"reset", "--soft", hash}
	case "reset-mixed":
		args = []string{"reset", "--mixed", hash}
	case "pull":
		args = []string{"pull", "--no-rebase", "--no-edit"}
	case "push":
		args = []string{"push"}
	case "abort":
		args = []string{"merge", "--abort"}
		if s.Snapshot.Rebasing {
			args = []string{"rebase", "--abort"}
		}
	default:
		return
	}
	a.writeSourceRepository(r, func() sourceResult {
		var err error
		switch op {
		case "push":
			err = r.push()
		case "merge":
			err = r.merge(hash, mergeBranch)
		default:
			_, err = r.command(args...)
		}
		return sourceResult{Err: err, Message: "Git " + op + " completed", Reload: op != "push" && op != "tag" && op != "reset-soft"}
	})
}

func (a *workspaceModel) discardSourceChanges(op, name string) {
	s := a.sourceState()
	files := s.Snapshot.Groups[2]
	if op != "discard-all" {
		folder := op == "discard-folder"
		files = nil
		for _, f := range s.Snapshot.Groups[2] {
			if folder && strings.HasPrefix(f.Path, name+"/") || !folder && f.Path == name {
				files = append(files, f)
			}
		}
	}
	for _, f := range files {
		if a.sourcePathDirty(f.Path) {
			s.Notice = a.tr("请先保存此文件的编辑内容，再放弃修改。")
			return
		}
	}
	if len(files) == 0 {
		return
	}
	r := s.Snapshot.Repository
	a.sourceMutation(r, "Changes discarded", func() error { return r.discardChanges(files) })
}

func (a *workspaceModel) renderSourceControl(b *sourceVNodeView) {
	s := a.sourceState()
	if a.bottom.active != pencodePanelSourceControl {
		return
	}
	key := sourcePanelKey{
		HistoryRevision: s.history.revision, Locale: a.locale.ID,
		Theme: a.workbenchTheme, Icons: a.fileIcons, Files: sourceFirst(s.Snapshot.Files), CommitFiles: sourceFirst(s.CommitFiles), Commits: sourceFirst(s.Snapshot.Commits),
		FileCount: len(s.Snapshot.Files), CommitCount: len(s.Snapshot.Commits), CommitFileCount: len(s.CommitFiles),
		First: s.GraphFirst, Visible: s.GraphVisible, Changes: s.changeRanges(), TreeRevision: s.TreeRevision,
		Sync:    s.Snapshot.Sync,
		Message: s.CommitMessage, MergeMessage: s.Snapshot.MergeMessage, Multiline: s.MessageMultiline, Selected: s.SelectedHash, Busy: s.busy(), Modal: s.modal(), HasMore: s.Snapshot.HasMore,
		Syncing:           s.repositoryJobs[s.Snapshot.Repository.Root].Name == sourceRepositorySync,
		RepositoryError:   s.RepositoryError,
		Repositories:      a.sourceRepositoryKey(),
		ChangesWidthStyle: a.sourceChangesWidthStyle, Resizing: a.resize.target == pencodeResizeSourceChanges,
		Rebasing: s.Snapshot.Rebasing,
	}
	if b.used != 0 && s.panelKey == key {
		return
	}
	s.panelKey = key
	b.reset()
	b.open("section", "source", "id", "sc-panel", "class", "sc-panel", "aria-label", "Source Control")
	b.open("fieldset", "controls", "class", "sc-content-frame")
	if s.modal() {
		b.attr("disabled", "")
	}
	b.open("div", "columns", "class", "sc-body")
	a.renderLocalHistory(b)
	b.open("div", "body", "id", "sc-body", "class", "sc-body")
	if s.RepositoryError != "" {
		b.open("div", "unavailable", "class", "sc-panel")
		a.renderSourceGraphHeader(b)
		b.close()
		b.close()
		b.close()
		b.close()
		b.close()
		return
	}
	a.renderSourceRepositories(b, key.Repositories)
	b.open("div", "main", "id", "sc-main", "class", "sc-main")
	b.open("aside", "changes", "id", "sc-changes", "class", "sc-changes", "aria-label", "Repository changes")
	if key.ChangesWidthStyle != "" {
		b.attr("style", key.ChangesWidthStyle)
	}
	messageTag, messageClass := "input", "sc-message"
	if s.MessageMultiline {
		messageTag, messageClass = "textarea", "sc-message sc-message-multiline"
	}
	b.open(messageTag, "message", "id", "sc-message", "aria-label", "Commit message", "placeholder", "Commit message", "class", messageClass)
	if s.busy() {
		b.attr("readonly", "")
	}
	if s.MessageMultiline {
		rows := min(strings.Count(s.CommitMessage, "\n")+1, 6)
		b.attr("rows", strconv.Itoa(rows))
		b.attr("style", fmt.Sprintf("height:%dpx", rows*20+14))
		b.content(s.CommitMessage)
	} else {
		b.attr("type", "text")
		b.attr("value", s.CommitMessage)
	}
	b.close()
	b.open("button", "commit", "id", "sc-create-commit", "type", "button", "class", "sc-commit-action")
	if s.busy() || len(s.Snapshot.Files) == 0 && s.Snapshot.MergeMessage == "" && !s.Snapshot.Rebasing && !s.Snapshot.syncPending() {
		b.attr("disabled", "")
	}
	icon, label, iconClass := "git-commit", "Commit", "sc-icon"
	if s.Snapshot.Rebasing {
		label = "Continue Rebase"
	}
	if s.Snapshot.syncPending() {
		icon, label = "refresh-cw", "Sync Changes"
		b.attr("aria-label", fmt.Sprintf("Sync Changes, %d incoming, %d outgoing", s.Snapshot.Sync.Behind, s.Snapshot.Sync.Ahead))
		if key.Syncing {
			b.attr("aria-busy", "true")
			iconClass = "sc-icon sc-sync-spinning"
		}
	}
	b.text("img", "icon", "", "src", a.themedIcon("icons/"+icon+".svg", "button.foreground"), "alt", "", "class", iconClass, "aria-hidden", "true")
	b.text("span", "label", label)
	if s.Snapshot.syncPending() {
		for _, direction := range [...]struct {
			key, class string
			count      int
		}{
			{"incoming", "sc-sync-down", s.Snapshot.Sync.Behind},
			{"outgoing", "sc-sync-up", s.Snapshot.Sync.Ahead},
		} {
			if direction.count == 0 {
				continue
			}
			b.open("span", direction.key, "class", "sc-sync-count", "aria-hidden", "true")
			b.text("img", "arrow", "", "src", a.themedIcon("icons/arrow-right.svg", "button.foreground"), "alt", "", "class", "sc-icon "+direction.class)
			b.text("span", "count", strconv.Itoa(direction.count))
			b.close()
		}
	}
	b.close()
	b.open("div", "changes-list", "id", "sc-changes-list", "class", "sc-changes-list")
	for i, group := range sourceChangeGroups {
		if len(s.Snapshot.Groups[i]) > 0 {
			a.sourceFileTree(b, group.Kind, group.Label, s.Snapshot.Groups[i], 14)
		}
	}
	if len(s.Snapshot.Files) == 0 {
		b.text("p", "empty", "No changes", "class", "muted")
	}
	b.close()
	b.close()
	resizeClass := "pencode-sidebar-resize"
	if key.Resizing {
		resizeClass += " resizing"
	}
	b.text("div", "changes-resize", "", "id", "sc-changes-resize", "class", resizeClass, "role", "separator", "aria-orientation", "vertical", "aria-label", a.tr("拖动调整改动列表宽度"), "aria-controls", "sc-changes")
	b.open("section", "history", "id", "sc-history", "class", "sc-history", "aria-label", "Source Control Graph")
	a.renderSourceGraphHeader(b)
	b.open("div", "graph", "id", "sc-graph", "class", "sc-graph")
	if len(s.Snapshot.Commits) == 0 {
		b.text("p", "empty", "No commits yet. Commit changes to create your first commit.", "class", "muted")
	}
	b.open("div", "graph-height", "class", "sc-graph-height", "style", fmt.Sprintf("height:%dpx", s.graphOffset(len(s.Snapshot.Commits))))
	b.open("div", "graph-window", "class", "sc-graph-window", "style", fmt.Sprintf("top:%dpx", s.graphOffset(s.GraphFirst)))
	for _, c := range s.graphWindow() {
		expanded := c.Hash == s.SelectedHash
		b.open("div", c.Hash, "class", "sc-commit-row")
		b.open("button", "commit", "id", "sc-commit:"+c.Hash, "type", "button", "class", "sc-commit", "aria-pressed", strconv.FormatBool(expanded), "aria-expanded", strconv.FormatBool(expanded), "title", c.Hash+"\n"+c.Subject+"\n"+c.Author+" · "+c.Date)
		if expanded {
			b.attr("aria-controls", "sc-commit-files")
		}
		width := renderSourceGraphLanes(b, c)
		b.text("span", "subject", c.Subject, "class", "sc-subject")
		if c.Refs != "" {
			b.text("span", "refs", c.Refs, "class", "sc-refs")
		}
		b.text("span", "meta", c.Hash[:8]+" · "+c.Author+" · "+c.Date, "class", "sc-meta")
		b.close()
		if expanded {
			b.open("div", "details", "class", "sc-commit-details", "style", fmt.Sprintf("height:%dpx", s.graphDetailsHeight()))
			renderSourceGraphContinuation(b, c, width)
			b.open("div", "commit-files", "id", "sc-commit-files", "class", "sc-commit-files", "role", "region", "aria-label", "Commit files", "aria-busy", strconv.FormatBool(s.Done != nil && s.CommitFiles == nil))
			a.sourceFileTree(b, "history", "Commit files", s.CommitFiles, width+12)
			b.close()
			b.close()
		}
		b.close()
	}
	b.close()
	b.close()
	if s.Snapshot.HasMore {
		sourceButton(b, "sc-more", "Load more commits", s.busy())
	}
	b.close()
	b.close()
	b.close()
	b.close()
	b.close()
	b.close()
	b.close()
}

func sourceButton(b *sourceVNodeView, id, label string, disabled bool) {
	b.open("button", id, "id", id, "type", "button")
	if disabled {
		b.attr("disabled", "")
	}
	b.content(label)
	b.close()
}

func (a *workspaceModel) sourceFileTree(b *sourceVNodeView, kind, label string, files []sourceFile, indent int) {
	s := a.sourceState()
	b.open("section", kind, "class", "sc-file-group")
	treeID := "sc-tree:" + kind
	if kind != "history" {
		collapsed := s.Collapsed["group:"+kind]
		b.open("h3", "heading", "class", "sc-heading sc-group-heading")
		b.open("button", "group", "id", "sc-group:"+kind, "type", "button", "class", "sc-group-toggle", "aria-expanded", strconv.FormatBool(!collapsed), "aria-controls", treeID)
		icon := "chevron-down"
		if collapsed {
			icon = "chevron-right"
		}
		a.sourceIcon(b, "arrow", icon, "")
		b.text("span", "label", label, "class", "sc-group-label")
		b.close()
		switch kind {
		case "changes":
			a.sourceIconButton(b, "sc-discard-all", "Discard All Changes", "discard", s.busy(), "sc-group-action")
			a.sourceIconButton(b, "sc-stage-all", "Stage All Changes", "add", s.busy(), "sc-group-action")
		case "staged":
			a.sourceIconButton(b, "sc-unstage-all", "Unstage All Changes", "remove", s.busy(), "sc-group-action")
		}
		b.close()
		if collapsed {
			b.text("ul", "tree", "", "id", treeID, "class", "sc-tree", "aria-label", label, "hidden", "")
			b.close()
			return
		}
	}
	b.open("ul", "tree", "id", treeID, "class", "sc-tree", "aria-label", label)
	var rows []sourceTreeRow
	var window sourceRowRange
	if kind == "history" {
		rows = s.historyTree()
		window.End = len(rows)
	} else {
		ranges := s.changeRanges()
		for i, group := range sourceChangeGroups {
			if group.Kind == kind {
				rows, window = s.changeTrees[i].rows, ranges[i]
				break
			}
		}
	}
	if window.First > 0 {
		b.text("li", "\x00before", "", "class", "sc-tree-spacer", "aria-hidden", "true", "style", fmt.Sprintf("height:%dpx", window.First*sourceChangeRowHeight))
	}
	for _, row := range rows[window.First:window.End] {
		a.sourceFileTreeRow(b, kind, row, indent)
	}
	if window.End < len(rows) {
		b.text("li", "\x00after", "", "class", "sc-tree-spacer", "aria-hidden", "true", "style", fmt.Sprintf("height:%dpx", (len(rows)-window.End)*sourceChangeRowHeight))
	}
	if len(files) == 0 {
		label := "No files"
		if kind == "history" && s.Done != nil && s.CommitFiles == nil {
			label = "Loading files…"
		}
		b.text("li", "empty", label, "class", "muted", "style", fmt.Sprintf("padding-left:%dpx", indent))
	}
	b.close()
	b.close()
}

func (a *workspaceModel) sourceFileTreeRow(b *sourceVNodeView, kind string, row sourceTreeRow, indent int) {
	s := a.sourceState()
	if row.File == nil {
		folder := row.Path
		key := kind + ":" + folder
		b.open("li", "folder:"+folder, "class", "sc-folder", "style", fmt.Sprintf("padding-left:%dpx", indent+row.Depth*14))
		b.open("button", "sc-folder:"+key, "id", "sc-folder:"+key, "type", "button", "aria-expanded", strconv.FormatBool(!s.Collapsed[key]))
		icon := "chevron-down"
		if s.Collapsed[key] {
			icon = "chevron-right"
		}
		a.sourceIcon(b, "arrow", icon, "")
		a.sourceFileIcon(b, "folder", path.Base(folder), true, !s.Collapsed[key])
		b.text("span", "name", path.Base(folder))
		b.close()
		if kind == "changes" {
			name := url.PathEscape(folder)
			a.sourceIconButton(b, "sc-discard-folder:"+kind+":"+name, "Discard Changes in "+folder, "discard", s.busy(), "sc-file-action")
		}
		if kind != "history" {
			id, title, icon := "sc-stage-folder:", "Stage Changes in ", "add"
			if kind == "staged" {
				id, title, icon = "sc-unstage-folder:", "Unstage Changes in ", "remove"
			}
			a.sourceIconButton(b, id+url.PathEscape(folder), title+folder, icon, s.busy(), "sc-file-action")
		}
		b.close()
		return
	}
	f := row.File
	b.open("li", f.Path, "class", "sc-file", "style", fmt.Sprintf("padding-left:%dpx", indent+row.Depth*14))
	name := url.PathEscape(f.Path)
	status := f.Work
	if kind == "staged" || kind == "history" {
		status = f.Index
	}
	if f.Conflict {
		status = 'U'
	} else if status == '?' {
		status = 'A'
	}
	b.open("button", f.Path, "id", "sc-open:"+kind+":"+name, "type", "button", "class", "sc-file-open", "title", f.Path)
	a.sourceFileIcon(b, "file", path.Base(f.Path), false, false)
	b.text("span", "name", path.Base(f.Path), "class", "sc-file-name")
	b.close()
	if kind != "history" {
		if kind == "changes" {
			a.sourceIconButton(b, "sc-discard:"+kind+":"+name, "Discard Changes "+f.Path, "discard", s.busy(), "sc-file-action")
		}
		id, title := "sc-stage:", "Stage Changes"
		if kind == "staged" {
			id, title = "sc-unstage:", "Unstage Changes"
		}
		icon := "add"
		if kind == "staged" {
			icon = "remove"
		}
		a.sourceIconButton(b, id+name, title+" "+f.Path, icon, s.busy(), "sc-file-action")
	}
	b.text("span", "status", string(status), "class", "sc-file-status sc-status-"+strings.ToLower(string(status)))
	b.close()
}

func (a *workspaceModel) renderSourceGraphHeader(b *sourceVNodeView) {
	s := a.sourceState()
	b.open("header", "graph-header", "class", "sc-graph-header")
	if s.RepositoryError == "" {
		b.text("h2", "title", "Source Control Graph", "class", "sc-heading")
	} else {
		b.text("p", "repository-error", a.tr("当前文件夹未检测到 Git 仓库。"), "id", "sc-repository-error", "class", "muted", "role", "status", "title", s.RepositoryError)
	}
	a.renderSourceToolbar(&s.toolsView)
	b.append(s.toolsView.nodes())
	b.close()
}

func (a *workspaceModel) renderSourceToolbar(b *sourceVNodeView) {
	s := a.sourceState()
	key := sourceToolsKey{Theme: a.workbenchTheme, Busy: s.busy(), Unavailable: s.RepositoryError != "", Rebasing: s.Snapshot.Rebasing}
	if b.used != 0 && s.toolsKey == key {
		return
	}
	s.toolsKey = key
	b.reset()
	if s.busy() {
		b.text("span", "busy", "Git…", "role", "status")
	}
	for _, item := range []struct{ ID, Label, Icon string }{
		{"sc-refresh", "Refresh", "refresh-cw"},
		{"sc-op:fetch", "Fetch", "git-fetch"}, {"sc-op:pull", "Pull", "repo-pull"}, {"sc-op:push", "Push", "repo-push"},
		{"sc-op:abort", "Abort Merge", "git-merge-abort"},
	} {
		if item.ID == "sc-op:abort" && key.Rebasing {
			item.Label = "Abort Rebase"
		}
		a.sourceIconButton(b, item.ID, item.Label, item.Icon, key.Busy || key.Unavailable && item.ID != "sc-refresh", "")
	}
	a.sourceIconButton(b, "sc-panel-close", a.tr("收起面板"), "close", false, "")
}
func (a *workspaceModel) sourceFileIcon(b *sourceVNodeView, key, name string, directory, expanded bool) {
	source := a.fileIcons.source(name, directory)
	if source == "" {
		source = "icons/file-text.svg"
		if directory {
			source = "icons/folder.svg"
			if expanded {
				source = "icons/folder-opened.svg"
			}
		}
	}
	b.text("img", key, "", "src", a.themedIcon(source, "icon.foreground"), "alt", "", "class", "sc-icon", "aria-hidden", "true")
}
func (a *workspaceModel) sourceIcon(b *sourceVNodeView, key, name, class string) {
	b.text("img", key, "", "src", a.themedIcon("icons/"+name+".svg", "icon.foreground"), "alt", "", "class", "sc-icon "+class, "aria-hidden", "true")
}
func (a *workspaceModel) sourceIconButton(b *sourceVNodeView, id, label, icon string, disabled bool, class string) {
	classes := "pencode-icon-button"
	if class != "" {
		classes += " " + class
	}
	b.open("button", id, "id", id, "type", "button", "class", classes, "title", label, "aria-label", label)
	if disabled {
		b.attr("disabled", "")
	}
	a.sourceIcon(b, "icon", icon, "")
	b.close()
}

func (a *workspaceModel) useSourceSide(side, name string) {
	s := a.sourceState()
	if side != "local" && side != "incoming" || s.busy() {
		return
	}
	index := slices.IndexFunc(s.Snapshot.Groups[0], func(f sourceFile) bool { return f.Path == name })
	if index < 0 {
		return
	}
	if a.sourcePathDirty(name) {
		s.Notice = a.tr("请先保存此文件的编辑内容，再解决合并。")
		return
	}
	for _, tab := range s.Tabs {
		if tab.Path == name && tab.Result != tab.Saved {
			s.Notice = a.tr("请先保存此文件的编辑内容，再解决合并。")
			return
		}
	}
	r, file := s.Snapshot.Repository, s.Snapshot.Groups[0][index]
	a.sourceJob(sourceWrite, func() sourceResult {
		tab, err := r.openDiff(file, "merge", "")
		if err == nil {
			if tab.Binary || tab.SubmoduleSummary != "" {
				err = fmt.Errorf("请在合并编辑器中处理此文件。")
			} else {
				tab.Result = tab.Local
				if side == "incoming" {
					tab.Result = tab.Incoming
				}
				err = r.saveMerge(*tab)
			}
		}
		snapshot, refreshErr := r.snapshot(sourceHistoryPageSize)
		if err == nil {
			err = refreshErr
		}
		if err != nil {
			tab = nil
		} else {
			tab.Disk, tab.Saved = tab.Result, tab.Result
		}
		return sourceResult{Snapshot: &snapshot, Tab: tab, Err: err, Message: "Merge resolved and staged", Reload: true}
	})
}
