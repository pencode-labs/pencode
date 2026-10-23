package workbench

import (
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/diiyw/onix/module"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	"github.com/diiyw/onix/ui/html"
)

const pencodeFindMatchLimit = 5000

func (model *workspaceModel) openFind(replace bool) {
	if !model.textEditorActive() {
		return
	}
	model.findOpen = true
	model.replaceOpen = replace
	model.findNeedsFocus = true
	model.settingsOpen = false
	model.refreshFindMatches()
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
	if model.findContent == content && model.findCachedQuery == model.findQuery && model.findCountLabel != "" {
		return
	}
	model.findContent, model.findCachedQuery = content, model.findQuery
	model.findMatches = model.findMatches[:0]
	if model.findQuery != "" {
		for offset := 0; offset <= len(content)-len(model.findQuery) && len(model.findMatches) < pencodeFindMatchLimit; {
			index := strings.Index(content[offset:], model.findQuery)
			if index < 0 {
				break
			}
			offset += index
			model.findMatches = append(model.findMatches, offset)
			offset += len(model.findQuery)
		}
	}
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
	start := model.findMatches[model.findIndex]
	end := start + len(model.findQuery)
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
		value := strings.ReplaceAll(content, model.findQuery, model.replaceQuery)
		if model.updateActiveContent(value) {
			model.setEditorValueAndCaret(world, value, 0)
		}
	} else {
		start := model.findMatches[model.findIndex]
		nextStart = start + len(model.replaceQuery)
		value := content[:start] + model.replaceQuery + content[start+len(model.findQuery):]
		if model.updateActiveContent(value) {
			caret := utf8.RuneCountInString(value[:start+len(model.replaceQuery)])
			model.setEditorValueAndCaret(world, value, caret)
		}
	}
	model.refreshFindMatches()
	if !all && len(model.findMatches) > 0 {
		model.findIndex = sort.SearchInts(model.findMatches, nextStart)
		if model.findIndex == len(model.findMatches) {
			model.findIndex = 0
		}
		model.updateFindCountLabel()
	}
	model.selectFindMatch(world)
}

func (model *workspaceModel) handleFindEvent(context *module.ContentEventContext[appProps, appState]) bool {
	switch context.Ref {
	case "find-query":
		model.findQuery = context.Event.Value
		model.findIndex = 0
		model.refreshFindMatches()
		model.selectFindMatch(context.World)
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
	if entity, ok := world.HTMLControl(surface, "find-query"); ok && world.Focus(entity) {
		model.findNeedsFocus = false
	}
}
