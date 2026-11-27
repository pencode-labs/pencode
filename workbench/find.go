package workbench

import (
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	"github.com/onix-engine/onix/text/piecetree"
	engineui "github.com/onix-engine/onix/ui"
	"github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/textsearch"
)

const pencodeFindMatchLimit = 5000

var pencodeFindOptionRefs = [...]string{"find-case", "find-word", "find-regex"}
var pencodeFolderOptionRefs = [...]string{"folder-search-case", "folder-search-word", "folder-search-regex"}

func (m *workspaceModel) searchOptionKey(state *input.State) int {
	for i, id := range [...]string{"search-case", "search-word", "search-regex", "search-ignore"} {
		if m.commandMatches(id, state, runtime.GOOS) {
			return i
		}
	}
	return -1
}

func toggleSearchOption(options *textsearch.Options, index int) {
	switch index {
	case 0:
		options.CaseSensitive = !options.CaseSensitive
	case 1:
		options.WholeWord = !options.WholeWord
	case 2:
		options.Regex = !options.Regex
	case 3:
		options.PreserveCase = !options.PreserveCase
	}
}

func (model *workspaceModel) canFind() bool {
	return model.languageEditorActive() || model.renderedFind() != nil
}

func (model *workspaceModel) findOnly() bool {
	return model.sourceState().visible() || model.renderedFind() != nil
}

func (model *workspaceModel) openFind(replace bool) {
	previous := model.findPreview
	findFocused := model.findInputFocused(model.fontResources)
	model.findPreview = nil
	if document := model.customDocuments[model.activeFile()]; !model.sourceState().visible() && document != nil && document.find != nil &&
		(!model.textEditorActive() || findFocused && previous == document || !findFocused && model.fontResources != nil && model.fontResources.FocusedControl() != model.editor.control) {
		model.findPreview = document
	}
	if !model.canFind() {
		return
	}
	model.findOpen = true
	model.replaceOpen = replace && !model.findOnly()
	model.findNeedsFocus = true
	model.settingsOpen = false
	model.quickOpen = false
	if model.renderedFind() != nil {
		model.findSelection = false
		model.refreshFindMatches()
		model.selectFindMatch(model.fontResources)
		return
	}
	file := model.languageFile()
	if model.findScope.file != file {
		model.findScope = pencodeFindScope{}
		model.findSelection = false
	}
	if world := model.fontResources; world != nil {
		if field, ok := world.Get[engineui.TextField](model.languageEditor().control); ok && field.Focused && field.Caret != field.Anchor {
			if !model.findSelection && !model.findOnly() {
				model.captureFindScope(world)
			}
			buffer := file.snapshotBuffer()
			start, end := buffer.ByteOffset(min(field.Caret, field.Anchor)), buffer.ByteOffset(max(field.Caret, field.Anchor))
			if selected := file.snapshot().Slice(start, end); !strings.ContainsAny(selected, "\r\n") {
				model.findQuery = strings.Clone(selected)
			}
		}
	}
	model.refreshFindMatches()
	model.findFromCaret(model.fontResources)
}

func (model *workspaceModel) findFromCaret(world *module.Content) {
	if model.renderedFind() != nil {
		model.selectFindMatch(world)
		return
	}
	if count := model.findCount(); world != nil && count > 0 {
		if field, ok := world.Get[engineui.TextField](model.languageEditor().control); ok {
			offset := model.languageFile().snapshotBuffer().ByteOffset(min(field.Anchor, field.Caret))
			matches, base := model.findMatches, 0
			if model.findDiff != nil && model.sourceState().languageSide == 1 {
				matches, base = model.findDiff.matches, len(model.findMatches)
			}
			index := sort.Search(len(matches), func(i int) bool { return matches[i].Start >= offset })
			model.findIndex = (base + index) % count
			model.updateFindCountLabel()
		}
	}
	model.selectFindMatch(world)
}

func (model *workspaceModel) closeFind(world *module.Content) {
	model.findOpen = false
	model.replaceOpen = false
	model.findNeedsFocus = false
	if preview := model.renderedFind(); preview != nil && world != nil {
		preview.focus(world)
	} else if world != nil {
		model.editor.restoreFocus = true
	}
	model.clearFindCache()
}

func (model *workspaceModel) clearFindCache() {
	model.releaseDiffFind()
	model.findContent = piecetree.Snapshot{}
	model.findPreview = nil
	model.findScope, model.findCachedScope = pencodeFindScope{}, pencodeFindScope{}
	model.findSelection = false
	clear(model.findMatches)
	if cap(model.findMatches) > 256 {
		model.findMatches = nil
	} else {
		model.findMatches = model.findMatches[:0]
	}
	model.findCountLabel = ""
	model.findRevision++
}

func (model *workspaceModel) releaseFileFind(file *openFile) {
	if model.findContent == file.snapshot() || model.findScope.file == file || model.findCachedScope.file == file || model.findPreview == model.customDocuments[file] && model.findPreview != nil {
		model.clearFindCache()
	}
}

func (model *workspaceModel) refreshFindMatches() {
	content := model.activeSnapshot()
	tab := model.languageDiffTab()
	if model.findDiff != nil && model.findDiff.tab != tab {
		model.releaseDiffFind()
	}
	var modified piecetree.Snapshot
	if tab != nil {
		if model.findDiff == nil {
			model.findDiff = &pencodeDiffFind{tab: tab}
			model.findCountLabel = ""
		}
		content, modified = snapshotText(tab.sourceText(0)), snapshotText(tab.sourceText(1))
		model.findSelection, model.replaceOpen = false, false
	} else if preview := model.renderedFind(); preview != nil {
		if preview.grid != nil && preview.grid.remote != nil && !preview.grid.remote.complete && model.findQuery != "" {
			model.customDocuments[model.activeFile()].requestGrid(true)
			model.findMatches = model.findMatches[:0]
			model.findCountLabel = model.tr("正在加载…")
			return
		}
		content = preview.content
		model.findSelection, model.replaceOpen = false, false
	}
	if model.findSelection && model.findScope.file != model.activeFile() {
		model.findSelection = false
	}
	scope := pencodeFindScope{}
	if model.findSelection {
		scope = model.findScope
	}
	patternChanged := model.findCachedQuery != model.findQuery || model.findCachedOptions != model.findOptions
	refreshBoth := patternChanged || model.findCountLabel == ""
	refreshContent := refreshBoth || model.findContent != content || model.findCachedScope != scope
	refreshModified := model.findDiff != nil && (refreshBoth || model.findDiff.content != modified)
	if !refreshContent && !refreshModified {
		return
	}
	if model.findPattern == nil || patternChanged {
		var err error
		model.findPattern, err = textsearch.Compile(model.findQuery, model.findOptions)
		model.findError = ""
		if err != nil {
			model.findError = err.Error()
		}
	}
	model.findContent, model.findCachedQuery, model.findCachedOptions = content, model.findQuery, model.findOptions
	model.findCachedScope = scope
	if refreshContent {
		model.findMatches = model.scopedFindMatches(content, model.findMatches, pencodeFindMatchLimit)
	}
	if refreshModified {
		model.findDiff.content = modified
		model.findDiff.matches = model.scopedFindMatches(modified, model.findDiff.matches, pencodeFindMatchLimit)
	}
	model.findRevision++
	model.findIndex = max(0, min(model.findIndex, model.findCount()-1))
	model.updateFindCountLabel()
}

func (model *workspaceModel) findCount() int {
	count := len(model.findMatches)
	if diff := model.findDiff; diff != nil {
		count += len(diff.matches)
	}
	return count
}

func (model *workspaceModel) updateFindCountLabel() {
	count := model.findCount()
	if count == 0 {
		model.findCountLabel = "0/0"
		return
	}
	model.findCountLabel = strconv.Itoa(model.findIndex+1) + "/" + strconv.Itoa(count)
	if len(model.findMatches) == pencodeFindMatchLimit || model.findDiff != nil && len(model.findDiff.matches) == pencodeFindMatchLimit {
		model.findCountLabel += "+"
	}
}

func (model *workspaceModel) selectFindMatch(world *module.Content) {
	if preview := model.renderedFind(); preview != nil {
		preview.reveal = true
		return
	}
	if model.findCount() == 0 || world == nil {
		return
	}
	matches, index := model.findMatches, model.findIndex
	if diff := model.findDiff; diff != nil {
		model.sourceState().languageSide = 0
		if index >= len(matches) {
			model.sourceState().languageSide = 1
			matches, index = diff.matches, index-len(matches)
		}
	}
	editor, file := model.languageEditor(), model.languageFile()
	field := world.Mut[engineui.TextField](editor.control)
	if field == nil || file == nil {
		return
	}
	match := matches[index]
	buffer := file.snapshotBuffer()
	anchor, caret := buffer.RuneOffset(match.Start), buffer.RuneOffset(match.End)
	if model.findDiff != nil && !model.findOpen {
		world.Focus(editor.control)
	}
	field.Anchor, field.Caret = anchor, caret
	if model.findDiff != nil {
		model.revealDiffFind(world, editor, file.snapshot(), anchor)
		return
	}
	model.revealEditorCaret(world, caret)
}

func (model *workspaceModel) revealEditorCaret(world *module.Content, caret int) {
	content := model.activeSnapshot()
	text := world.Mut[scene.Text2D](model.editor.text)
	textarea := world.Mut[engineui.Textarea](model.editor.control)
	if text == nil || textarea == nil {
		return
	}
	if file := model.activeFile(); file != nil && file.folds != nil && file.folds.source == content {
		file.folds.reveal(file.snapshotBuffer(), caret)
	}
	x, y, width, height, ok := scene.TextCaretRect2D(text, content, caret)
	if !ok {
		return
	}
	link, ok := world.Get[html.ControlText](model.editor.text)
	if !ok {
		return
	}
	viewport := link.Content
	if textarea.Wrap {
		textarea.ScrollX = 0
	} else {
		textarea.ScrollX = max(0, x+width/2-viewport.Width/2)
	}
	textarea.ScrollY = max(0, y+height/2-viewport.Height/2)
}

func (model *workspaceModel) advanceFind(world *module.Content, step int) {
	model.refreshFindMatches()
	count := model.findCount()
	if count == 0 {
		return
	}
	model.findIndex = (model.findIndex + step + count) % count
	model.updateFindCountLabel()
	model.selectFindMatch(world)
}

func (model *workspaceModel) replaceFindMatch(world *module.Content, all bool) {
	if model.findOnly() {
		return
	}
	model.refreshFindMatches()
	if len(model.findMatches) == 0 || model.findQuery == "" {
		return
	}
	content := model.activeContent()
	nextStart := 0
	if all {
		matches := model.scopedFindMatches(content, nil, -1)
		value := model.findPattern.Replace(content, model.replaceQuery, matches)
		if model.updateActiveContent(value) {
			model.setEditorValueAndCaret(world, value, 0)
		}
	} else {
		match := model.findMatches[model.findIndex]
		start := match.Start
		replacement := model.findPattern.Replacement(content, match, model.replaceQuery)
		nextStart = start + len(replacement)
		if match.Start == match.End {
			_, size := utf8.DecodeRuneInString(content[match.End:])
			nextStart += max(1, size)
		}
		value := content[:start] + replacement + content[match.End:]
		if model.updateActiveContent(value) {
			caret := utf8.RuneCountInString(value[:start+len(replacement)])
			model.setEditorValueAndCaret(world, value, caret)
		}
	}
	model.refreshFindMatches()
	if !all && len(model.findMatches) > 0 {
		model.findIndex = sort.Search(len(model.findMatches), func(i int) bool { return model.findMatches[i].Start >= nextStart })
		if model.findIndex == len(model.findMatches) {
			model.findIndex = 0
		}
		model.updateFindCountLabel()
	}
	model.selectFindMatch(world)
}

func (model *workspaceModel) handleFindEvent(context *module.ContentEventContext[appProps, appState]) bool {
	switch context.Ref {
	case "find-selection":
		if model.findOnly() {
			return true
		}
		if model.findSelection {
			model.findSelection = false
		} else {
			if model.findScope.file != model.activeFile() {
				model.captureFindScope(context.World)
			}
			model.findSelection = model.findScope.file == model.activeFile() && model.findScope.start < model.findScope.end
		}
		model.refreshFindMatches()
		model.findFromCaret(context.World)
	case "find-preserve":
		model.findOptions.PreserveCase = !model.findOptions.PreserveCase
	case "find-toggle-replace":
		model.replaceOpen = !model.replaceOpen && !model.findOnly()
	case "find-case", "find-word", "find-regex":
		switch context.Ref {
		case "find-case":
			model.findOptions.CaseSensitive = !model.findOptions.CaseSensitive
		case "find-word":
			model.findOptions.WholeWord = !model.findOptions.WholeWord
		case "find-regex":
			model.findOptions.Regex = !model.findOptions.Regex
		}
		model.refreshFindMatches()
		model.findFromCaret(context.World)
	case "find-query":
		model.findQuery = context.Event.Value
		model.refreshFindMatches()
		model.findFromCaret(context.World)
	case "replace-query":
		model.replaceQuery = context.Event.Value
	case "find-previous":
		model.advanceFind(context.World, -1)
	case "find-next":
		model.advanceFind(context.World, 1)
	case "find-close":
		model.closeFind(context.World)
	case "replace-one":
		model.replaceFindMatch(context.World, false)
	case "replace-all":
		model.replaceFindMatch(context.World, true)
	default:
		return false
	}
	return true
}

func (model *workspaceModel) focusFindInput(world *module.Content, surface ecs.Entity) {
	if !model.findNeedsFocus || !model.findOpen {
		return
	}
	if entity, ok := world.HTMLControl(surface, "find-query"); ok && world.Has[engineui.TextField](entity) && world.Focus(entity) {
		model.findNeedsFocus = false
		if field := world.Mut[engineui.TextField](entity); field != nil {
			field.Anchor = 0
		}
	}
}

func pencodeControlFocused(world *module.Content, surface ecs.Entity, ref string) bool {
	if world == nil {
		return false
	}
	entity, ok := world.HTMLControl(surface, ref)
	if !ok {
		return false
	}
	field, ok := world.Get[engineui.TextField](entity)
	return ok && field.Focused
}

func (model *workspaceModel) findInputFocused(world *module.Content) bool {
	return model.findOpen && (pencodeControlFocused(world, model.surface, "find-query") || pencodeControlFocused(world, model.surface, "replace-query"))
}

func renderSearchOptions(view *html.View, refs [3]string, options textsearch.Options) {
	for i, ref := range refs {
		view.Event(ref, html.EventClick)
		active := [...]bool{options.CaseSensitive, options.WholeWord, options.Regex}[i]
		view.Attr(ref, "aria-pressed", boolAttribute(active))
		class := "pencode-find-action pencode-find-option"
		if active {
			class = "pencode-find-action pencode-find-option active"
		}
		view.Attr(ref, "class", class)
	}
}
