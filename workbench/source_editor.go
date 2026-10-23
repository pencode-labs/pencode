package workbench

import (
	"fmt"
	"strconv"
	"strings"
)

func (a *workspaceModel) renderSourceTabs(b *sourceVNodeView) {
	s := a.sourceState()
	b.open("section", "editor", "class", "sc-editor", "id", "sc-editor", "aria-label", "Code comparison editor")
	tab := s.activeTab()
	if tab == nil {
		b.close()
		return
	}
	b.open("div", "code", "id", "sc-code-panel", "class", "sc-code-panel", "role", "tabpanel", "aria-label", tab.Path)
	if tab.Kind == "merge" && !tab.Binary {
		b.open("div", "toolbar", "class", "sc-toolbar")
		sourceButton(b, "sc-save-merge", "Complete Merge · Save & Stage", s.busy())
		sourceButton(b, "sc-accept:local", "Use Local File", s.busy())
		sourceButton(b, "sc-accept:incoming", "Use Incoming File", s.busy())
		b.close()
	}
	if tab.Binary {
		b.text("p", "binary", "Binary files cannot be displayed in the text editor.", "class", "muted")
	} else if tab.Kind == "merge" {
		conflicts := sourceConflicts(tab.Result)
		b.open("div", "conflicts", "class", "sc-conflict-actions", "aria-label", "Resolve conflicts")
		b.text("span", "count", fmt.Sprintf("%d unresolved conflicts", len(conflicts)))
		for i := range conflicts {
			b.open("div", strconv.Itoa(i), "class", "sc-toolbar")
			b.text("span", "label", fmt.Sprintf("Conflict %d", i+1))
			for _, choice := range []struct{ ID, Label string }{{"local", "Accept Local"}, {"incoming", "Accept Incoming"}, {"both", "Accept Both"}} {
				sourceButton(b, fmt.Sprintf("sc-resolve:%d:%s", i, choice.ID), choice.Label, s.busy())
			}
			b.close()
		}
		b.close()
		b.open("div", "merge", "class", "sc-merge")
		b.open("section", "local", "class", "sc-merge-side")
		b.text("h3", "label", "Local / Current", "class", "sc-heading")
		sourceCodeLines(b, tab, true)
		b.close()
		b.open("section", "result", "class", "sc-merge-result")
		b.text("label", "label", "Merge Result", "for", "sc-merge-result", "class", "sc-heading")
		b.open("textarea", "text", "id", "sc-merge-result", "class", "sc-result-input", "wrap", "soft", "spellcheck", "false", "aria-label", "Editable merge result")
		if s.busy() {
			b.attr("readonly", "")
		}
		b.content(tab.Result)
		b.close()
		b.close()
		b.open("section", "incoming", "class", "sc-merge-side")
		b.text("h3", "label", "Incoming", "class", "sc-heading")
		sourceCodeLines(b, tab, false)
		b.close()
		b.close()
	} else {
		b.open("div", "headings", "class", "sc-diff-headings")
		left, right := "Original · Index", "Modified · Working Tree"
		if tab.Kind == "staged" {
			left, right = "Original · HEAD", "Staged · Index"
		}
		if tab.Kind == "history" {
			left, right = "Parent", "Commit · "+tab.Revision[:8]
		}
		b.text("h3", "left", left, "class", "sc-heading")
		b.text("div", "separator", "", "class", "sc-diff-gutter-heading", "aria-hidden", "true")
		b.text("h3", "right", right, "class", "sc-heading")
		b.close()
		b.open("div", "diff-panes", "class", "sc-diff-panes")
		sourceCodeLines(b, tab, true)
		a.renderDiffGutter(b, tab)
		sourceCodeLines(b, tab, false)
		b.close()
	}
	b.close()
	b.close()
}

func sourceCodeLines(b *sourceVNodeView, tab *sourceTab, left bool) {
	id := "sc-code-right"
	if left {
		id = "sc-code-left"
	}
	b.open("div", id+"-pane", "class", "sc-code-pane")
	b.open("div", id, "id", id, "class", "sc-code-scroll", "tabindex", "0", "aria-label", "Code lines")
	// Both sides retain the full document's width outside the visible window.
	b.open("div", "height", "class", "sc-code-height", "style", fmt.Sprintf("height:%dpx;width:%dpx", (len(tab.Rows)+1)*20, tab.CodeColumns*8+10))
	b.open("div", "window", "class", "sc-code-window", "style", fmt.Sprintf("top:%dpx", tab.FirstRow*20))
	first, end := min(tab.FirstRow, len(tab.Rows)), min(len(tab.Rows), tab.FirstRow+160)
	var numbers strings.Builder
	numbers.Grow((end - first) * 8)
	var digits [20]byte
	for i := first; i < end; i++ {
		row := tab.Rows[i]
		text, number, kind := row.Right, row.RightNumber, row.RightKind
		if left {
			text, number, kind = row.Left, row.LeftNumber, row.LeftKind
		}
		if number > 0 {
			numbers.Write(strconv.AppendInt(digits[:0], int64(number), 10))
		}
		numbers.WriteByte('\n')
		highlight := &tab.Highlight[1]
		if left {
			highlight = &tab.Highlight[0]
		}
		if tab.Kind == "merge" {
			sourceCodeCell(b, strconv.Itoa(i), text, number, kind, highlight)
		} else {
			sourceCodeCell(b, strconv.Itoa(i), "", number, kind, nil)
			side := 1
			if left {
				side = 0
			}
			for index, change := range row.Changes {
				if change.Start[side] == change.End[side] {
					continue
				}
				style := change.Style[side]
				if style == "" {
					style = "display:none"
				}
				id := sourceInlineID(side, i, index)
				b.text("span", id, "", "id", id, "class", "sc-inline-change "+kind.class(), "aria-hidden", "true", "style", style)
			}
		}
	}
	b.close()
	if tab.Kind != "merge" {
		label, value := "Original code (read only)", tab.Local
		if !left {
			label, value = "Modified code", tab.sourceText(1)
		}
		b.open("textarea", "input", "id", id+"-input", "class", "sc-code-input", "wrap", "soft", "spellcheck", "false", "aria-label", label)
		if left || !tab.editable() {
			b.attr("readonly", "")
		}
		b.content(value)
		b.close()
	}
	b.close()
	b.close()
	// Keep the gutter outside the horizontal viewport; only its vertical offset
	// follows the code, including when the virtual row window is replaced.
	b.open("div", "gutter", "class", "sc-code-gutter", "aria-hidden", "true")
	b.open("div", "gutter-track", "id", id+"-gutter-track", "class", "sc-code-gutter-track")
	b.text("pre", "numbers", numbers.String(), "id", id+"-numbers", "class", "sc-line-numbers", "style", fmt.Sprintf("top:%dpx", tab.FirstRow*20))
	b.close()
	b.close()
	b.close()
}

func sourceCodeCell(b *sourceVNodeView, key, text string, number int, kind sourceDiffKind, highlight *sourceHighlight) {
	class := kind.class()
	if number == 0 {
		class = "empty"
	}
	b.open("div", key, "class", "sc-code-line "+class)
	b.open("code", "text", "class", "sc-line-text")
	if highlight != nil {
		highlight.renderLine(b, text, number)
	}
	b.close()
	b.close()
}

func (a *workspaceModel) renderDiffGutter(b *sourceVNodeView, tab *sourceTab) {
	b.open("div", "gutter", "id", "sc-diff-gutter", "class", "sc-diff-gutter", "aria-label", "Diff block actions")
	b.open("div", "track", "id", "sc-diff-gutter-track", "class", "sc-diff-gutter-track")
	end := min(len(tab.Rows), tab.FirstRow+160)
	for index, block := range tab.Blocks {
		if block.EndRow <= tab.FirstRow || block.FirstRow >= end {
			continue
		}
		b.open("div", strconv.Itoa(index), "class", "sc-diff-block-action", "style", fmt.Sprintf("top:%dpx", max(block.FirstRow, tab.FirstRow)*20))
		if tab.editable() {
			a.sourceIconButton(b, "sc-revert-block:"+strconv.Itoa(index), "Revert block", "arrow-right", a.sourceState().Done != nil || tab.DiffContent != tab.Result, "")
		}
		b.close()
	}
	b.close()
	b.close()
}

var sourceCommitActions = []struct{ ID, Label string }{
	{"copy", "Copy Commit Hash"},
	{"checkout", "Checkout Commit (Detached)"}, {"branch", "Create Branch Here…"}, {"tag", "Create Tag…"},
	{"merge", "Merge into Current Branch…"}, {"cherry-pick", "Cherry-pick Commit…"}, {"revert", "Revert Commit…"},
	{"reset-soft", "Reset to Here (Soft)…"}, {"reset-mixed", "Reset to Here (Mixed)…"},
}

func (a *workspaceModel) renderSourceMenu(b *sourceVNodeView) {
	s := a.sourceState()
	b.open("div", "context", "id", "sc-context-menu", "class", "pencode-editor-context sc-context-menu", "role", "menu", "aria-label", "Git commit actions", "style", fmt.Sprintf("left:%gpx;top:%gpx", s.MenuX, s.MenuY))
	for _, action := range sourceCommitActions {
		b.button("sc-op:"+action.ID, action.Label, "role", "menuitem")
	}
	b.close()
}

func (a *workspaceModel) renderSourcePrompt(b *sourceVNodeView) {
	s := a.sourceState()
	b.open("div", "backdrop", "class", "sc-backdrop pencode-close-overlay")
	role := "dialog"
	if s.Notice != "" {
		role = "alertdialog"
	}
	b.open("section", "prompt", "class", "sc-prompt pencode-close-dialog", "role", role, "aria-modal", "true", "aria-labelledby", "sc-prompt-title", "aria-describedby", "sc-prompt-message")
	if s.Notice != "" {
		b.text("h2", "title", "Source Control", "id", "sc-prompt-title")
		b.text("p", "message", s.Notice, "id", "sc-prompt-message", "class", "pencode-close-message")
		b.open("div", "actions", "class", "pencode-close-actions")
		b.button("sc-notice-close", "确定", "class", "pencode-close-save")
		b.close()
	} else {
		b.text("h2", "title", "Confirm Git Operation", "id", "sc-prompt-title")
		message := "Execute " + s.Prompt + "?"
		switch s.Prompt {
		case "branch":
			if branch, ok := s.branch(s.PromptHash); ok {
				message = "基于分支 “" + branch.Name + "” 创建并切换到新分支："
			}
		case "tag":
			if branch, ok := s.branch(s.PromptHash); ok {
				message = "在分支 “" + branch.Name + "” 的最新提交上创建标签："
			}
		case "rename-branch":
			if branch, ok := s.branch(s.PromptHash); ok {
				message = "重命名本地分支 “" + branch.Name + "”："
			}
		case "delete-branch":
			if branch, ok := s.branch(s.PromptHash); ok {
				message = "删除本地分支 “" + branch.Name + "”？未合并的分支会由 Git 拒绝删除。"
				if branch.Remote {
					message = "从远程仓库删除分支 “" + branch.Name + "”？此操作会影响其他协作者，本地分支将保留。"
				}
			}
		case "discard-tab":
			message = "丢弃此标签页尚未保存的编辑内容？"
		case "discard-file":
			message = "放弃此文件的未暂存修改？此操作无法撤销。"
		case "discard-folder":
			message = "放弃此文件夹下所有文件的未暂存修改？此操作无法撤销。"
		case "local":
			message = "使用本地完整文件替换合并结果？结果仍需保存并暂存。"
		case "incoming":
			message = "使用传入的完整文件替换合并结果？结果仍需保存并暂存。"
		case "reset-soft":
			message = "将当前分支移动到该提交，保留索引和工作区中的修改？"
		case "reset-mixed":
			message = "将当前分支移动到该提交并取消暂存，保留工作区文件？"
		case "abort":
			message = "取消当前合并？合并期间的解决结果将被丢弃。"
		case "checkout":
			message = "以 detached HEAD 模式检出此提交？"
		case "push":
			message = "将当前分支的提交推送至已配置的远程仓库？"
		case "pull":
			message = "从远程拉取并仅以 fast-forward 方式更新当前分支？"
		}
		b.text("p", "message", message, "id", "sc-prompt-message", "class", "pencode-close-message")
		if s.Prompt != "delete-branch" && s.Prompt != "rename-branch" && len(s.PromptHash) >= 40 {
			b.text("code", "hash", s.PromptHash)
		}
		if s.Prompt == "branch" || s.Prompt == "tag" || s.Prompt == "rename-branch" {
			b.input("sc-prompt-value", "Name", s.PromptValue)
		}
		b.open("div", "actions", "class", "pencode-close-actions")
		b.button("sc-prompt-cancel", "Cancel")
		b.button("sc-prompt-confirm", "Confirm", "class", "pencode-close-save")
		b.close()
	}
	b.close()
	b.close()
}
