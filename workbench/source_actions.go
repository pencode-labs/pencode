package workbench

import (
	"fmt"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
)

func (a *workspaceModel) sourceMutation(message string, operation func(sourceRepository) error) {
	s := a.sourceState()
	r := s.Snapshot.Repository
	if r.Root == "" {
		return
	}
	a.sourceJob(sourceWrite, func() sourceResult {
		err := operation(r)
		snapshot, refreshErr := r.snapshot(sourceHistoryPageSize)
		if err == nil {
			err = refreshErr
		}
		return sourceResult{Snapshot: &snapshot, Err: err, Message: message}
	})
}

func (a *workspaceModel) sourceAction(id string) {
	s := a.sourceState()
	if id == "sc-notice-close" {
		s.Notice = ""
		s.FocusedModal, s.FocusedTab = "", ""
		s.NextRefresh = time.Now().Add(5 * time.Second)
		a.sourceChanged()
		return
	}
	if id == "sc-prompt-cancel" {
		s.Prompt = ""
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
		s.Active, s.RestoreScroll = strings.TrimPrefix(id, "sc-tab:"), true
		s.Selection++
		a.sourceChanged()
		return
	}
	if strings.HasPrefix(id, "sc-folder:") || strings.HasPrefix(id, "sc-group:") {
		height := s.graphDetailsHeight()
		if s.Collapsed == nil {
			s.Collapsed = make(map[string]bool)
		}
		key := strings.TrimPrefix(strings.TrimPrefix(id, "sc-"), "folder:")
		s.Collapsed[key] = !s.Collapsed[key]
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
	// Periodic reads must not swallow clicks or block foreground operations.
	// Their private context and mailbox can be discarded without cancelling writes.
	s.cancelRefresh()
	if s.Done != nil {
		return
	}
	if strings.HasPrefix(id, "sc-branch:") {
		ref := strings.TrimPrefix(id, "sc-branch:")
		for i, branch := range s.Snapshot.Branches {
			if branch.Ref == ref {
				s.BranchIndex, s.BranchSubmenu, s.BranchSubIndex = i, true, -1
				a.positionSourceBranchSubmenu(a.fontResources)
				break
			}
		}
		a.sourceChanged()
		return
	}
	for i, action := range sourceBranchActions {
		if action.ID != id {
			continue
		}
		if !s.BranchMenuOpen || !s.BranchSubmenu || !s.branchActionEnabled(i) {
			return
		}
		branch := s.Snapshot.Branches[s.BranchIndex]
		s.BranchMenuOpen, s.BranchSubmenu, s.BranchFocus = false, false, true
		switch action.ID {
		case "sc-branch-checkout":
			a.switchSourceBranch(branch.Ref)
		case "sc-branch-create":
			s.Prompt, s.PromptHash, s.PromptValue = "branch", branch.Ref, ""
		case "sc-branch-tag":
			s.Prompt, s.PromptHash, s.PromptValue = "tag", branch.Ref, ""
		case "sc-branch-rename":
			s.Prompt, s.PromptHash, s.PromptValue = "rename-branch", branch.Ref, branch.Name
		case "sc-branch-delete":
			s.Prompt, s.PromptHash = "delete-branch", branch.Ref
		}
		a.sourceChanged()
		return
	}
	s.MenuOpen = false
	if id != "sc-branches" {
		s.BranchMenuOpen, s.BranchSubmenu = false, false
	}
	r := s.Snapshot.Repository
	switch {
	case id == "sc-branches":
		a.toggleSourceBranches()
	case id == "sc-refresh":
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
				s.Notice = "请先保存此文件的编辑内容，再打开工作区比较或合并。"
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
					s.Active, s.RestoreScroll = key, true
					s.Selection++
					a.sourceChanged()
					return
				}
			}
			s.Selection++
			a.sourceJob(sourceQuery, func() sourceResult { tab, err := r.openDiff(file, kind, hash); return sourceResult{Tab: tab, Err: err} })
			break
		}
	case strings.HasPrefix(id, "sc-discard:"), strings.HasPrefix(id, "sc-discard-folder:"):
		op, prefix := "discard-file", "sc-discard:"
		if strings.HasPrefix(id, "sc-discard-folder:") {
			op, prefix = "discard-folder", "sc-discard-folder:"
		}
		kind, encoded, ok := strings.Cut(strings.TrimPrefix(id, prefix), ":")
		if !ok || kind != "changes" {
			return
		}
		s.Prompt, s.PromptHash = op, kind+":"+encoded
	case strings.HasPrefix(id, "sc-stage:"), strings.HasPrefix(id, "sc-stage-folder:"), strings.HasPrefix(id, "sc-unstage:"), id == "sc-stage-all", id == "sc-unstage-all":
		unstage := strings.HasPrefix(id, "sc-unstage")
		all := id == "sc-stage-all" || id == "sc-unstage-all"
		folder := strings.HasPrefix(id, "sc-stage-folder:")
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
				s.Notice = "请先保存文件的编辑内容，再暂存。"
				a.sourceChanged()
				return
			}
			files = append(files, f)
		}
		head := s.Snapshot.HasHead
		a.sourceMutation("Index updated", func(r sourceRepository) error {
			if unstage {
				return r.unstage(files, head)
			}
			return r.stage(files)
		})
	case id == "sc-create-commit":
		if s.Snapshot.syncPending() {
			if a.sourceHasDrafts() || s.unsaved() {
				s.Notice = "请先保存编辑内容，再同步远程。"
				break
			}
			a.sourceJob(sourceSync, func() sourceResult {
				err := r.syncChanges()
				snapshot, refreshErr := r.snapshot(sourceHistoryPageSize)
				if err == nil {
					err = refreshErr
				}
				return sourceResult{Snapshot: &snapshot, Err: err, Message: "Changes synchronized", Reload: true}
			})
			break
		}
		if len(s.Snapshot.Files) == 0 {
			break
		}
		if s.unsaved() || len(s.Snapshot.Groups[0]) != 0 {
			s.Notice = "请先完成并保存所有合并，再提交。"
			break
		}
		if len(s.Snapshot.Groups[1]) == 0 && a.sourceHasDrafts() {
			s.Notice = "请先保存编辑内容，再自动暂存并提交。"
			break
		}
		message := s.CommitMessage
		a.sourceMutation("Commit created", func(r sourceRepository) error { return r.commit(message) })
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
				s.Notice = "该文件还有未保存的普通编辑内容，请先保存后重新打开合并。"
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
				s.Notice = "该文件还有未保存的普通编辑内容，请先保存后重新打开差异。"
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
				if state, ok := module.ContentResource[*input.State](a.fontResources); ok {
					state.SetClipboardText(s.MenuHash)
				}
			}
		} else if op == "fetch" {
			a.sourceMutation("Fetch completed", func(r sourceRepository) error { _, err := r.command("fetch", "--all", "--prune"); return err })
		} else {
			s.Prompt, s.PromptHash, s.PromptValue = op, s.MenuHash, ""
		}
	case id == "sc-prompt-confirm":
		a.confirmSourceOperation()
	}
	a.sourceChanged()
}

func (a *workspaceModel) closeSourceTab(key string) {
	s := a.sourceState()
	for i, tab := range s.Tabs {
		if tab.Key == key {
			s.Tabs = slices.Delete(s.Tabs, i, i+1)
			if s.Active == key {
				s.Active = ""
				s.Selection++
				s.RestoreScroll = true
				if len(s.Tabs) > 0 {
					s.Active = s.Tabs[min(i, len(s.Tabs)-1)].Key
				}
			}
			return
		}
	}
}

func (a *workspaceModel) confirmSourceOperation() {
	s := a.sourceState()
	op, hash, value := s.Prompt, s.PromptHash, strings.TrimSpace(s.PromptValue)
	if (op == "branch" || op == "tag" || op == "rename-branch") && value == "" {
		return
	}
	s.Prompt = ""
	s.FocusedModal, s.FocusedTab = "", ""
	if op == "discard-tab" {
		a.closeSourceTab(hash)
		return
	}
	if op == "discard-file" || op == "discard-folder" {
		a.discardSourceChanges(op == "discard-folder", hash)
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
		if branch, ok := s.branch(hash); ok {
			if op == "rename-branch" {
				a.sourceMutation("Branch renamed", func(r sourceRepository) error { return r.renameBranch(branch, value) })
			} else {
				a.sourceMutation("Branch deleted", func(r sourceRepository) error { return r.deleteBranch(branch) })
			}
		}
		return
	}
	if a.sourceHasDrafts() || s.unsaved() {
		s.Notice = "请先保存编辑器中的项目修改，再执行 Git 工作区操作。"
		return
	}
	var args []string
	switch op {
	case "checkout":
		args = []string{"switch", "--detach", hash}
	case "branch":
		args = []string{"switch", "-c", value, hash}
	case "tag":
		args = []string{"tag", "--", value, hash}
	case "merge":
		args = []string{"merge", "--no-edit", hash}
	case "cherry-pick":
		args = []string{"cherry-pick", hash}
	case "revert":
		args = []string{"revert", "--no-edit", hash}
	case "reset-soft":
		args = []string{"reset", "--soft", hash}
	case "reset-mixed":
		args = []string{"reset", "--mixed", hash}
	case "pull":
		args = []string{"pull", "--ff-only"}
	case "push":
		args = []string{"push"}
	case "abort":
		args = []string{"merge", "--abort"}
	default:
		return
	}
	r := s.Snapshot.Repository
	a.sourceJob(sourceWrite, func() sourceResult {
		_, err := r.command(args...)
		snapshot, refreshErr := r.snapshot(sourceHistoryPageSize)
		if err == nil {
			err = refreshErr
		}
		return sourceResult{Snapshot: &snapshot, Err: err, Message: "Git " + op + " completed", Reload: op != "push" && op != "tag" && op != "reset-soft"}
	})
}

func (a *workspaceModel) discardSourceChanges(folder bool, key string) {
	s := a.sourceState()
	_, encoded, ok := strings.Cut(key, ":")
	if !ok {
		return
	}
	name, err := url.PathUnescape(encoded)
	if err != nil {
		return
	}
	var files []sourceFile
	for _, f := range s.Snapshot.Groups[2] {
		if folder && !strings.HasPrefix(f.Path, name+"/") || !folder && f.Path != name {
			continue
		}
		if a.sourcePathDirty(f.Path) {
			s.Notice = "请先保存此文件的编辑内容，再放弃修改。"
			return
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		return
	}
	a.sourceMutation("Changes discarded", func(r sourceRepository) error { return r.discardChanges(files) })
}

func (a *workspaceModel) renderSourceControl(b *sourceVNodeView) {
	s := a.sourceState()
	if a.bottom.active != pencodePanelSourceControl {
		return
	}
	key := sourcePanelKey{
		Theme: a.workbenchTheme, Icons: a.fileIcons, Files: sourceFirst(s.Snapshot.Files), CommitFiles: sourceFirst(s.CommitFiles), Commits: sourceFirst(s.Snapshot.Commits),
		FileCount: len(s.Snapshot.Files), CommitCount: len(s.Snapshot.Commits), CommitFileCount: len(s.CommitFiles),
		First: s.GraphFirst, Visible: s.GraphVisible, TreeRevision: s.TreeRevision,
		Sync:    s.Snapshot.Sync,
		Message: s.CommitMessage, Multiline: s.MessageMultiline, Selected: s.SelectedHash, Busy: s.busy(), Modal: s.modal(), HasMore: s.Snapshot.HasMore,
		Syncing: s.Done != nil && s.JobKind == sourceSync,
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
	b.open("div", "body", "class", "sc-body")
	b.open("aside", "changes", "class", "sc-changes", "aria-label", "Repository changes")
	messageTag, messageClass := "input", "sc-message"
	if s.MessageMultiline {
		messageTag, messageClass = "textarea", "sc-message sc-message-multiline"
	}
	b.open(messageTag, "message", "id", "sc-message", "aria-label", "Commit message", "placeholder", "Message (Shift+Enter for multiple lines)", "class", messageClass)
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
	if s.busy() || len(s.Snapshot.Files) == 0 && !s.Snapshot.syncPending() {
		b.attr("disabled", "")
	}
	icon, label, iconClass := "git-commit", "Commit", "sc-icon"
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
	for i, group := range []struct{ Kind, Label string }{{"merge", "Merge Changes"}, {"staged", "Staged Changes"}, {"changes", "Changes"}} {
		if len(s.Snapshot.Groups[i]) > 0 {
			a.sourceFileTree(b, group.Kind, group.Label, s.Snapshot.Groups[i], 14)
		}
	}
	if len(s.Snapshot.Files) == 0 {
		b.text("p", "empty", "No changes", "class", "muted")
	}
	b.close()
	b.close()
	b.open("section", "history", "class", "sc-history", "aria-label", "Source Control Graph")
	b.text("h2", "title", "Source Control Graph", "class", "sc-heading")
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
		b.text("span", "label", label)
		b.close()
		b.close()
		if collapsed {
			b.text("ul", "tree", "", "id", treeID, "class", "sc-tree", "aria-label", label, "hidden", "")
			b.close()
			return
		}
	}
	b.open("ul", "tree", "id", treeID, "class", "sc-tree", "aria-label", label)
	rows := sourceTreeRows(kind, files, s.Collapsed)
	if kind == "history" {
		rows = slices.Values(s.historyTree())
	}
	for row := range rows {
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
				a.sourceIconButton(b, "sc-stage-folder:"+name, "Stage Changes in "+folder, "add", s.busy(), "sc-file-action")
			}
			b.close()
			continue
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

func (a *workspaceModel) renderSourceToolbar(b *sourceVNodeView) {
	s := a.sourceState()
	key := sourceToolsKey{Theme: a.workbenchTheme, Branch: s.Snapshot.Branch, Busy: s.busy(), BranchMenu: s.BranchMenuOpen}
	if b.used != 0 && s.toolsKey == key {
		return
	}
	s.toolsKey = key
	b.reset()
	b.open("button", "branch", "id", "sc-branches", "type", "button", "class", "sc-branch-button", "title", "Switch Branch: "+s.Snapshot.Branch, "aria-label", "Switch Branch: "+s.Snapshot.Branch, "aria-haspopup", "menu", "aria-expanded", strconv.FormatBool(s.BranchMenuOpen), "aria-controls", "sc-branch-menu")
	if s.busy() {
		b.attr("disabled", "")
	}
	a.sourceIcon(b, "icon", "git-branch", "")
	b.text("span", "name", s.Snapshot.Branch, "class", "sc-branch")
	b.close()
	if s.busy() {
		b.text("span", "busy", "Git…", "role", "status")
	}
	for _, item := range []struct{ ID, Label, Icon string }{
		{"sc-refresh", "Refresh", "refresh-cw"},
		{"sc-op:fetch", "Fetch", "git-fetch"}, {"sc-op:pull", "Pull", "repo-pull"}, {"sc-op:push", "Push", "repo-push"},
		{"sc-op:abort", "Abort Merge", "git-merge-abort"},
	} {
		a.sourceIconButton(b, item.ID, item.Label, item.Icon, s.busy(), "")
	}
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
