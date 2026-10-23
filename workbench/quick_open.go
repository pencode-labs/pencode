package workbench

import (
	"context"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/peb"
	"github.com/pencode-labs/pencode/protocol"
	"strconv"
	"strings"
	"time"

	"github.com/diiyw/onix/module"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

var pencodeQuickRowRefs = [...]string{
	"quick-row-0", "quick-row-1", "quick-row-2", "quick-row-3",
	"quick-row-4", "quick-row-5", "quick-row-6", "quick-row-7",
}

var pencodeQuickLabelRefs = [...]string{
	"quick-label-0", "quick-label-1", "quick-label-2", "quick-label-3",
	"quick-label-4", "quick-label-5", "quick-label-6", "quick-label-7",
}

var pencodeQuickIconRefs = [...]string{
	"quick-icon-0", "quick-icon-1", "quick-icon-2", "quick-icon-3",
	"quick-icon-4", "quick-icon-5", "quick-icon-6", "quick-icon-7",
}

func pencodeQuickLocation(query string) (string, int) {
	if colon := strings.LastIndexByte(query, ':'); colon >= 0 {
		if line, err := strconv.Atoi(query[colon+1:]); err == nil && line > 0 {
			return query[:colon], line
		}
	}
	return query, 0
}

func pencodeLineCaret(content string, line int) int {
	caret := 0
	for _, char := range content {
		if line <= 1 {
			break
		}
		caret++
		if char == '\n' {
			line--
		}
	}
	return caret
}

func (model *workspaceModel) openQuickOpen() {
	if model.activeProjectState() == nil && len(model.entries) == 0 {
		return
	}
	model.quickOpen = true
	model.quickNeedsFocus = true
	model.quickCount = -1
	model.quickIndex = 0
	model.settingsOpen = false
	if model.market != nil {
		model.market.open = false
	}
	if model.session != nil && model.session.connection != nil {
		model.session.connection.open, model.session.connection.formOpen = false, false
	}
	model.findOpen = false
	model.findNeedsFocus = false
	model.refreshQuickMatches()
}

func (model *workspaceModel) closeQuickOpen(world *module.Content) {
	model.quickOpen = false
	model.quickNeedsFocus = false
	if world != nil && model.activeFile() != nil && world.IsAlive(model.editor.control) {
		world.Focus(model.editor.control)
	}
}

type pencodeQuickJob struct {
	backend         *client.Client
	project         *pencodeProject
	root, query     string
	index, revision uint64
	history         [pencodeQuickHistoryLimit]string
	historyCount    int
	rules           [128]protocol.PathRule
	ruleCount       int
}
type pencodeQuickResult struct {
	job   pencodeQuickJob
	paths []string
	err   error
}

func (model *workspaceModel) pollQuickMatches() bool {
	if model.quickWorker == nil {
		return false
	}
	changed := false
	model.quickWorker.poll(func(result *pencodeQuickResult) {
		if result.job.revision != model.quickRevision || result.job.project != model.activeProjectState() || result.job.project.index != result.job.index || result.job.query != model.quickCachedQuery {
			return
		}
		model.quickApplied = result.job.revision
		clear(model.quickMatches[:])
		model.quickCount = 0
		if result.err != nil {
			model.setActiveStatus("搜索文件失败：" + result.err.Error())
		} else {
			for _, name := range result.paths {
				if model.canOpenFile(name) {
					model.quickMatches[model.quickCount] = name
					model.quickCount++
					if model.quickCount == len(model.quickMatches) {
						break
					}
				}
			}
		}
		model.quickIndex = min(model.quickIndex, max(0, model.quickCount-1))
		changed = true
	})
	return changed
}
func (model *workspaceModel) refreshQuickMatches() {
	project := model.activeProjectState()
	if project == nil || project.index == 0 {
		model.quickCount = 0
		return
	}
	if model.quickCachedQuery == model.quickQuery && model.quickProjectPath == project.Path && model.quickCount >= 0 {
		return
	}
	if model.quickWorker == nil {
		workerCtx, stop := context.WithCancel(context.Background())
		model.quickCancel = stop
		model.quickWorker = newLatestWorker(func(job pencodeQuickJob, result *pencodeQuickResult) bool {
			result.job = job
			ctx, cancel := context.WithTimeout(workerCtx, 10*time.Second)
			defer cancel()
			query, _ := pencodeQuickLocation(job.query)
			result.paths, result.err = job.backend.SearchPaths(ctx, job.index, query, job.history[:job.historyCount], job.rules[:job.ruleCount]...)
			return true
		}, func(result *pencodeQuickResult) { *result = pencodeQuickResult{} }, nil, model.backgroundWake())
	}
	model.quickRevision++
	job := pencodeQuickJob{backend: model.backend, project: project, index: project.index, root: project.Path, query: model.quickQuery, revision: model.quickRevision}
	if model.session != nil && model.session.quickHistory != nil {
		history := model.session.quickHistory
		for _, entry := range history.entries[:history.count] {
			if entry.project == model.historyProject() {
				job.history[job.historyCount] = entry.path
				job.historyCount++
			}
		}
	}
	for _, editor := range model.customEditors {
		for _, rule := range editor.Selectors {
			if job.ruleCount == len(job.rules) {
				break
			}
			job.rules[job.ruleCount] = protocol.PathRule{Pattern: rule.Pattern, Suffix: rule.Match == peb.FileSuffix}
			job.ruleCount++
		}
	}
	model.quickCachedQuery, model.quickProjectPath = model.quickQuery, project.Path
	clear(model.quickMatches[:])
	model.quickCount = 0
	model.quickWorker.submit(job)
}
func (model *workspaceModel) closeQuickWorker() {
	if model.quickWorker != nil {
		model.quickCancel()
		model.quickWorker.close()
		model.quickWorker = nil
	}
}

func pencodeContainsFold(value, query string) bool {
	if query == "" {
		return true
	}
	for index := 0; index+len(query) <= len(value); index++ {
		if strings.EqualFold(value[index:index+len(query)], query) {
			return true
		}
	}
	return false
}

func (model *workspaceModel) advanceQuickMatch(step int) {
	model.refreshQuickMatches()
	if model.quickCount == 0 {
		return
	}
	model.quickIndex = (model.quickIndex + step + model.quickCount) % model.quickCount
}

func (model *workspaceModel) openQuickMatch(world *module.Content, index int) {
	model.refreshQuickMatches()
	if index < 0 || index >= model.quickCount {
		return
	}
	path := model.quickMatches[index]
	_, line := pencodeQuickLocation(model.quickQuery)
	navigation := &fileNavigation{line: max(1, line), history: true, query: model.quickQuery}
	if err := model.requestFileOpen(model.activeProjectState(), path, false, true, navigation); err != nil {
		model.setActiveStatus(err.Error())
		return
	}
	setPencodeEditorValue(world, model.surface, model.activeContent())

}

func (model *workspaceModel) handleQuickOpenEvent(context *module.ContentEventContext[appProps, appState]) bool {
	if context.Ref == "quick-open-query" && context.Event.Kind == htmlui.EventFocus {
		// Focus notifications can arrive after a result has already been opened.
		field, ok := module.ContentGet[ui.TextField](context.World, context.Event.Target)
		if model.quickFocusEditor || !ok || !field.Focused {
			return true
		}
	}
	if context.Ref == "open-quick-search" || context.Ref == "quick-open-query" && context.Event.Kind != htmlui.EventInput {
		if !model.quickOpen {
			model.openQuickOpen()
		}
		return true
	}
	if context.Ref == "quick-open-query" {
		model.quickQuery = context.Event.Value
		model.quickIndex = 0
		if model.quickOpen {
			model.refreshQuickMatches()
		} else {
			model.openQuickOpen()
		}
		return true
	}
	for index, ref := range pencodeQuickRowRefs {
		if context.Ref == ref {
			model.openQuickMatch(context.World, index)
			return true
		}
	}
	return false
}

func (model *workspaceModel) focusQuickControls(world *module.Content, surface ecs.Entity) {
	if model.quickFocusEditor && !model.textEditorActive() {
		model.quickFocusEditor, model.quickTargetLine = false, 0
	}
	if model.quickFocusEditor && model.editor.file == model.activeFile() && world.IsAlive(model.editor.control) && world.Focus(model.editor.control) {
		model.quickFocusEditor = false
		if model.quickTargetLine > 0 {
			caret := pencodeLineCaret(model.activeContent(), model.quickTargetLine)
			if field := module.ContentMut[ui.TextField](world, model.editor.control); field != nil {
				field.Caret, field.Anchor = caret, caret
			}
			model.revealEditorCaret(world, caret)
			model.quickTargetLine = 0
		}
	}
	if !model.quickNeedsFocus || !model.quickOpen {
		return
	}
	entity, ok := world.HTMLControl(surface, "quick-open-query")
	if !ok {
		return
	}
	// Newly mounted DOM controls must bind their native text fields before focus.
	if !module.ContentHas[ui.TextField](world, entity) {
		if err := world.SyncHTML(); err != nil {
			return
		}
		entity, ok = world.HTMLControl(surface, "quick-open-query")
		if !ok || !module.ContentHas[ui.TextField](world, entity) {
			return
		}
	}
	if world.Focus(entity) {
		model.quickNeedsFocus = false
	}
}
