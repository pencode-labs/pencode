package workbench

import (
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/diiyw/onix/module"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	"github.com/diiyw/onix/ui/html"
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

func (model *workspaceModel) openFind(replace bool) {
	if !model.textEditorActive() {
		return
	}
	model.findOpen = true
	model.replaceOpen = replace
	model.findNeedsFocus = true
	model.settingsOpen = false
	model.quickOpen = false
	if model.findScope.file != model.activeFile() {
		model.findScope = pencodeFindScope{}
		model.findSelection = false
	}
	if world := model.fontResources; world != nil {
		if field, ok := module.ContentGet[engineui.TextField](world, model.editor.control); ok && field.Focused && field.Caret != field.Anchor {
			if !model.findSelection {
				model.captureFindScope(world)
			}
			buffer := model.activeFile().textBuffer(model.activeContent())
			start, end := buffer.ByteOffset(min(field.Caret, field.Anchor)), buffer.ByteOffset(max(field.Caret, field.Anchor))
			if selected := model.activeContent()[start:end]; !strings.ContainsAny(selected, "\r\n") {
				model.findQuery = selected
			}
		}
	}
	model.refreshFindMatches()
	model.findFromCaret(model.fontResources)
}

func (model *workspaceModel) findFromCaret(world *module.Content) {
	if world != nil && len(model.findMatches) > 0 {
		if field, ok := module.ContentGet[engineui.TextField](world, model.editor.control); ok {
			offset := model.activeFile().textBuffer(model.activeContent()).ByteOffset(min(field.Anchor, field.Caret))
			model.findIndex = sort.Search(len(model.findMatches), func(i int) bool { return model.findMatches[i].Start >= offset })
			if model.findIndex == len(model.findMatches) {
				model.findIndex = 0
			}
			model.updateFindCountLabel()
		}
	}
	model.selectFindMatch(world)
}

func (model *workspaceModel) closeFind(world *module.Content) {
	model.findOpen = false
	model.replaceOpen = false
	model.findNeedsFocus = false
	if world != nil && world.IsAlive(model.editor.control) {
		world.Focus(model.editor.control)
	}
}

func (model *workspaceModel) refreshFindMatches() {
	content := model.activeContent()
	if model.findSelection && model.findScope.file != model.activeFile() {
		model.findSelection = false
	}
	scope := pencodeFindScope{}
	if model.findSelection {
		scope = model.findScope
	}
	if model.findContent == content && model.findCachedQuery == model.findQuery && model.findCachedOptions == model.findOptions && model.findCachedScope == scope && model.findCountLabel != "" {
		return
	}
	if model.findPattern == nil || model.findCachedQuery != model.findQuery || model.findCachedOptions != model.findOptions {
		var err error
		model.findPattern, err = textsearch.Compile(model.findQuery, model.findOptions)
		model.findError = ""
		if err != nil {
			model.findError = err.Error()
		}
	}
	model.findContent, model.findCachedQuery, model.findCachedOptions = content, model.findQuery, model.findOptions
	model.findCachedScope = scope
	model.findMatches = model.scopedFindMatches(content, model.findMatches, pencodeFindMatchLimit)
	model.findRevision++
	if len(model.findMatches) == 0 {
		model.findIndex = 0
		model.findCountLabel = "0/0"
		return
	}
	model.findIndex = min(model.findIndex, len(model.findMatches)-1)
	model.updateFindCountLabel()
}

func (model *workspaceModel) updateFindCountLabel() {
	count := len(model.findMatches)
	if count == 0 {
		model.findCountLabel = "0/0"
		return
	}
	model.findCountLabel = strconv.Itoa(model.findIndex+1) + "/" + strconv.Itoa(count)
	if count == pencodeFindMatchLimit {
		model.findCountLabel += "+"
	}
}

func (model *workspaceModel) selectFindMatch(world *module.Content) {
	if len(model.findMatches) == 0 || world == nil || !world.IsAlive(model.editor.control) {
		return
	}
	content := model.activeContent()
	match := model.findMatches[model.findIndex]
	start, end := match.Start, match.End
	anchor := utf8.RuneCountInString(content[:start])
	caret := anchor + utf8.RuneCountInString(content[start:end])
	if field := module.ContentMut[engineui.TextField](world, model.editor.control); field != nil {
		field.Anchor, field.Caret = anchor, caret
	}
	model.revealEditorCaret(world, caret)
}

func (model *workspaceModel) revealEditorCaret(world *module.Content, caret int) {
	content := model.activeContent()
	text := module.ContentMut[scene.Text2D](world, model.editor.text)
	textarea := module.ContentMut[engineui.Textarea](world, model.editor.control)
	if text == nil || textarea == nil {
		return
	}
	x, y, width, height, ok := scene.TextCaretRect2D(text, content, caret)
	if !ok {
		return
	}
	link, ok := module.ContentGet[html.ControlText](world, model.editor.text)
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
	if len(model.findMatches) == 0 {
		return
	}
	model.findIndex = (model.findIndex + step + len(model.findMatches)) % len(model.findMatches)
	model.updateFindCountLabel()
	model.selectFindMatch(world)
}

func (model *workspaceModel) replaceFindMatch(world *module.Content, all bool) {
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
		model.replaceOpen = !model.replaceOpen
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
	if entity, ok := world.HTMLControl(surface, "find-query"); ok && module.ContentHas[engineui.TextField](world, entity) && world.Focus(entity) {
		model.findNeedsFocus = false
		if field := module.ContentMut[engineui.TextField](world, entity); field != nil {
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
	field, ok := module.ContentGet[engineui.TextField](world, entity)
	return ok && field.Focused
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
