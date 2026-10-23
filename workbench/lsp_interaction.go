package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/pencode-labs/pencode/lsp"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

const (
	pencodeCompletionDelay = 120 * time.Millisecond
	pencodeHoverDelay      = 350 * time.Millisecond
	pencodeHoverWidthRatio = 0.8
)

type pencodeLanguageInteraction struct {
	completionDue                                time.Time
	completionFile                               *openFile
	completionContent, completionMethod, trigger string
	completionCaret                              int
	hover                                        pencodeLanguageHover
	underlineRects                               []scene.TextRangeRect2D
	underlineNodes                               []htmlui.VNode
	underlineOriginX, underlineOriginY           float32
	contextOpen                                  bool
	contextFocus                                 bool
	contextX, contextY                           float32
	contextCaret, contextAnchor, contextIndex    int
	contextGroup, contextSubIndex                int
	contextSubX, contextSubY                     float32
	contextPointerX, contextPointerY             float32
	contextFile                                  *openFile
	contextContent                               string
	keysHandled                                  bool
}

type pencodeLanguageHover struct {
	cancel             context.CancelFunc
	due                time.Time
	token              uint64
	file               *openFile
	content, body      string
	result             json.RawMessage
	nodes              []htmlui.VNode
	theme              *syntax.Theme
	definitions        *syntax.Definitions
	offset, start, end int // UTF-8 byte offsets, including the request position.
	x, y               float32
	pointerX, pointerY float32 // Mouse anchor relative to editor-body, before viewport clamping.
	underline          htmlui.Rect
	viewport           htmlui.Rect
	open, link         bool
	positioned         bool
}

func (model *workspaceModel) wakeLanguageAt(due time.Time) {
	if !due.IsZero() && model.app != nil && model.app.WakeAt != nil {
		model.app.WakeAt(due)
	}
}

func (model *workspaceModel) languageProvider(doc *pencodeLanguageDocument, method string) *pencodeLanguageSession {
	if doc == nil || model.languages == nil {
		return nil
	}
	capability, _ := languageAction(method)
	for _, server := range model.languages.servers {
		candidate := model.languages.sessions[pencodeLanguageKey{doc.root, server.ID}]
		if synced, ok := doc.servers[candidate]; ok && candidate != nil && candidate.client != nil && !candidate.failed && synced.version == doc.version && candidate.client.Supports(method, capability) {
			return candidate
		}
	}
	return nil
}

// Only native typing/retriggering opens suggestions; paste, undo and IME preedit
// must not dispatch requests for intermediate documents.
func (model *workspaceModel) scheduleLanguageCompletion(world *module.Content, event htmlui.Event, old string) {
	ui := &model.languageUI
	wasCompletion := model.languages != nil && model.languages.panel.completion
	model.cancelLanguageAutomatic()
	model.cancelLanguageHover()
	if model.languages == nil || world == nil {
		return
	}
	model.dismissLanguagePanel()
	field, ok := module.ContentGet[engineui.TextField](world, model.editor.control)
	state, _ := module.ContentResource[*input.State](world)
	if !ok || !field.Focused || field.Composition != "" || state != nil && state.Composing() || field.Anchor != field.Caret {
		return
	}
	start, oldEnd, newEnd, _ := pencodeChangedRange(old, event.Value)
	inserted := event.Value[start:newEnd]
	last, size := utf8.DecodeLastRuneInString(inserted)
	if inserted != "" && (size != len(inserted) && event.InputType != "insertCompositionText" || strings.ContainsAny(inserted, "\r\n")) {
		return
	}
	caretByte, _ := pencodeSelectionBytes(event.Value, field.Caret, field.Caret)
	if inserted != "" {
		last, _ = utf8.DecodeLastRuneInString(event.Value[:caretByte])
	}
	doc := model.languageDocument()
	method, trigger := "", ""
	// The chosen provider owns its trigger characters. The same provider will
	// be selected when the delayed request is submitted.
	for _, candidate := range []string{"textDocument/signatureHelp", "textDocument/completion"} {
		session := model.languageProvider(doc, candidate)
		if session == nil {
			continue
		}
		capability, _ := languageAction(candidate)
		var provider struct {
			TriggerCharacters   []string `json:"triggerCharacters"`
			RetriggerCharacters []string `json:"retriggerCharacters"`
		}
		_ = json.Unmarshal(session.client.ProviderOptions(candidate, capability), &provider)
		for _, characters := range [][]string{provider.TriggerCharacters, provider.RetriggerCharacters} {
			for _, character := range characters {
				if inserted != "" && character == string(last) {
					method, trigger = candidate, character
					break
				}
			}
		}
		if method != "" {
			break
		}
		if candidate == "textDocument/completion" && (inserted != "" && isLanguageWordRune(last) || inserted == "" && oldEnd > start && wasCompletion) {
			method = candidate
		}
	}
	if method == "" {
		return
	}
	ui.completionFile, ui.completionContent = model.activeFile(), event.Value
	ui.completionMethod, ui.trigger, ui.completionCaret = method, trigger, field.Caret
	ui.completionDue = time.Now().Add(pencodeCompletionDelay)
	model.wakeLanguageAt(ui.completionDue)
}

func (model *workspaceModel) cancelLanguageAutomatic() {
	ui := &model.languageUI
	ui.completionDue = time.Time{}
	ui.completionFile, ui.completionContent, ui.trigger = nil, "", ""
}

func (model *workspaceModel) cancelLanguageHover() {
	h := &model.languageUI.hover
	if h.cancel != nil {
		h.cancel()
	}
	token := h.token + 1
	*h = pencodeLanguageHover{token: token}
}

// Active editor actions own the interaction until dismissed or completed.
// Hover detection stays suspended even when the pointer is outside their popup.
func (model *workspaceModel) languageHoverBlocked() bool {
	ui := &model.languageUI
	return ui.contextOpen || ui.completionFile != nil || model.languages != nil && (model.languages.panel.open || model.languages.panel.completion || model.languages.panel.pending)
}

func (model *workspaceModel) dismissLanguageContext() {
	ui := &model.languageUI
	ui.contextOpen, ui.contextFile, ui.contextContent = false, nil, ""
	ui.contextGroup = 0
}

func (model *workspaceModel) runLanguageContext(world *module.Content, index int) {
	ui := &model.languageUI
	file, content, caret, anchor := ui.contextFile, ui.contextContent, ui.contextCaret, ui.contextAnchor
	model.dismissLanguageContext()
	if world == nil || index < 0 || index >= len(pencodeLanguageActions) || file != model.activeFile() || content != model.activeContent() {
		return
	}
	world.Focus(model.editor.control)
	ui.contextFocus = true
	if field := module.ContentMut[engineui.TextField](world, model.editor.control); field != nil {
		field.Caret, field.Anchor = caret, anchor
	}
	model.runLanguageAction(world, pencodeLanguageActions[index].method, false)
}

func (model *workspaceModel) updateLanguageInteractions(world *module.Content, state *input.State) bool {
	if world == nil || state == nil {
		return false
	}
	ui := &model.languageUI
	ui.keysHandled = false
	if model.fileContext.open || model.fileContext.dialog != "" || !model.textEditorActive() || model.settingsOpen || model.quickOpen || model.findOpen || model.closing.action != pencodeCloseNone || model.market != nil && model.market.open {
		changed := ui.contextOpen || ui.hover.open || ui.hover.link || model.languages != nil && (model.languages.panel.open || model.languages.panel.completion)
		model.cancelLanguageAutomatic()
		model.cancelLanguageHover()
		model.dismissLanguageContext()
		model.dismissLanguagePanel()
		return changed
	}
	changed := false
	field, _ := module.ContentGet[engineui.TextField](world, model.editor.control)
	file := model.activeFile()
	if ui.contextOpen && (ui.contextFile != file || ui.contextContent != model.activeContent()) {
		model.dismissLanguageContext()
		changed = true
	}
	if model.languages != nil {
		p := &model.languages.panel
		if p.job.document != nil && p.job.method != "workspace/executeCommand" && (p.job.document.file != file || p.job.content != file.Content || (p.method == "textDocument/completion" || p.method == "textDocument/signatureHelp") && p.job.offset != runeOffsetBytes(file.Content, field.Caret)) {
			model.dismissLanguagePanel()
			changed = true
		}
	}
	if ui.completionFile != nil {
		if ui.completionFile != file || ui.completionContent != file.Content || field.Caret != ui.completionCaret || !field.Focused || state.Composing() || field.Composition != "" {
			model.cancelLanguageAutomatic()
		} else if time.Now().Before(ui.completionDue) {
			model.wakeLanguageAt(ui.completionDue)
		} else {
			model.runLanguageAction(world, ui.completionMethod, false)
			model.cancelLanguageAutomatic()
			changed = true
		}
	}
	body, bodyOK := world.HTMLNode(model.surface, "editor-body")
	editor, editorOK := world.HTMLNode(model.surface, "editor")
	if !bodyOK || !editorOK {
		return changed
	}
	bodyRect, _ := body.Bounds()
	editorRect, _ := editor.Bounds()
	zoom := max(float32(model.settings.Zoom)/100, .01)
	pointer := state.Pointer()
	x, y := pointer.X/zoom, pointer.Y/zoom
	ctrl := state.KeyHeld(input.KeyLeftControl) || state.KeyHeld(input.KeyRightControl)
	if ui.hover.link && !ctrl {
		ui.hover.link = false
		changed = true
	}
	popup := model.pointerInsideRefs(world, x, y, []string{"lsp-context", "lsp-context-submenu", "lsp-completion", "lsp-panel", "lsp-hover"})
	inside := languageRectContains(editorRect, x, y) && !popup
	if state.MousePressed(input.MouseRight) && inside {
		model.cancelLanguageAutomatic()
		model.cancelLanguageHover()
		model.dismissLanguagePanel()
		caret := model.editorOffsetAtPointer(world, x, y, editorRect)
		ui.contextCaret, ui.contextAnchor = caret, caret
		if caret >= min(field.Caret, field.Anchor) && caret <= max(field.Caret, field.Anchor) {
			ui.contextCaret, ui.contextAnchor = field.Caret, field.Anchor
		}
		ui.contextOpen, ui.contextFile, ui.contextContent = true, file, file.Content
		ui.contextX, ui.contextY = clampLanguagePopup(x-bodyRect.X, y-bodyRect.Y, bodyRect, 240, 364)
		ui.contextIndex, ui.contextGroup = 0, 0
		ui.contextPointerX, ui.contextPointerY = x, y
		world.Focus(model.editor.control)
		return true
	}
	if ui.contextOpen && pointer.Device == input.PointerDeviceMouse && (x != ui.contextPointerX || y != ui.contextPointerY) {
		ui.contextPointerX, ui.contextPointerY = x, y
		changed = model.hoverEditorContext(world, x, y) || changed
	}
	if state.PointerPressed() || state.MousePressed(input.MouseRight) || state.MousePressed(input.MouseMiddle) {
		if model.languages != nil && (!popup || model.languages.panel.open && !model.pointerInsideRefs(world, x, y, []string{"lsp-panel"})) {
			model.dismissLanguagePanel()
			changed = true
		}
		if !popup {
			model.dismissLanguageContext()
			model.cancelLanguageAutomatic()
			model.cancelLanguageHover()
			changed = true
		}
	}
	if model.languageHoverBlocked() {
		if ui.hover.file != nil || ui.hover.open || ui.hover.link {
			model.cancelLanguageHover()
			changed = true
		}
		return changed
	}
	if popup || pointer.Down || !inside || pointer.Device != input.PointerDeviceMouse {
		if !model.pointerInsideRefs(world, x, y, []string{"lsp-hover"}) && (ui.hover.file != nil || ui.hover.open || ui.hover.link) {
			model.cancelLanguageHover()
			changed = true
		}
		return changed
	}
	offset, start, end, underline, valid := model.languageSymbolAtPointer(world, x, y, editorRect, bodyRect)
	if !valid || state.Composing() || model.languages == nil {
		if ui.hover.file != nil {
			model.cancelLanguageHover()
			changed = true
		}
		return changed
	}
	if state.MouseReleased(input.MouseLeft) && ctrl {
		model.cancelLanguageAutomatic()
		model.cancelLanguageHover()
		model.dismissLanguageContext()
		world.Focus(model.editor.control)
		if f := module.ContentMut[engineui.TextField](world, model.editor.control); f != nil {
			f.Caret, f.Anchor, f.Selecting = utf8.RuneCountInString(file.Content[:offset]), utf8.RuneCountInString(file.Content[:offset]), false
		}
		var doc *pencodeLanguageDocument
		if model.languages != nil {
			doc = model.languages.documents[file]
		}
		action := languageSymbolAction(doc, offset)
		model.runLanguageAction(world, action, false)
		if action == "textDocument/definition" && model.languages != nil {
			model.languages.panel.controlClick = true
		}
		return true
	}
	h := &ui.hover
	if h.file != file || h.content != file.Content || h.start != start || h.end != end || h.underline != underline {
		model.cancelLanguageHover()
		h = &ui.hover
		h.file, h.content, h.offset, h.start, h.end, h.underline = file, file.Content, offset, start, end, underline
		h.pointerX, h.pointerY = x-bodyRect.X, y-bodyRect.Y
		h.resetPosition(bodyRect)
		h.due = time.Now().Add(pencodeHoverDelay)
		model.wakeLanguageAt(h.due)
		changed = true
	}
	if !h.open {
		h.pointerX, h.pointerY = x-bodyRect.X, y-bodyRect.Y
	}
	var doc *pencodeLanguageDocument
	if model.languages != nil {
		doc = model.languages.documents[file]
	}
	action := languageSymbolAction(doc, offset)
	link := ctrl && model.languageProvider(doc, action) != nil
	if h.link != link {
		h.link = link
		changed = true
	}
	if !h.due.IsZero() {
		if time.Now().Before(h.due) {
			model.wakeLanguageAt(h.due)
		} else {
			h.due = time.Time{}
			model.requestLanguageHover()
		}
	}
	return changed
}

func languageRectContains(r htmlui.Rect, x, y float32) bool {
	return r.Width > 0 && r.Height > 0 && x >= r.X && x < r.X+r.Width && y >= r.Y && y < r.Y+r.Height
}

func clampLanguagePopup(x, y float32, body htmlui.Rect, width, height float32) (float32, float32) {
	x = max(4, min(x, body.Width-min(width, body.Width-8)-4))
	if y+height > body.Height-4 {
		y = max(4, y-height-24)
	}
	return x, max(4, min(y, body.Height-40))
}

func languageHoverPoint(x, y float32, body htmlui.Rect, width, height float32) (float32, float32) {
	const gap = 12
	x = max(4, min(x, body.Width-width-4))
	below := y + gap
	if below+height > body.Height-4 {
		below = y - gap - height
	}
	return x, max(4, min(below, body.Height-height-4))
}

func (h *pencodeLanguageHover) resetPosition(viewport htmlui.Rect) {
	h.viewport = viewport
	h.positioned = false
	// Reserve the full CSS width limit before measuring. Starting closer to
	// the right edge makes shrink-to-fit width depend on successive clamps.
	h.x, h.y = languageHoverPoint(h.pointerX, h.pointerY, viewport, viewport.Width*pencodeHoverWidthRatio, max(0, viewport.Height-8))
}

func runeOffsetBytes(value string, offset int) int {
	index, _ := pencodeSelectionBytes(value, max(0, offset), max(0, offset))
	return index
}

func isLanguageWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

func languageWordStart(content string, offset int) int {
	for offset > 0 {
		r, size := utf8.DecodeLastRuneInString(content[:offset])
		if !isLanguageWordRune(r) {
			break
		}
		offset -= size
	}
	return offset
}

func languageCompletionMatches(text, prefix string) bool {
	for _, character := range prefix {
		index := strings.IndexRune(text, character)
		if index < 0 {
			return false
		}
		text = text[index+utf8.RuneLen(character):]
	}
	return true
}

func (model *workspaceModel) requestLanguageHover() {
	if model.languageHoverBlocked() {
		return
	}
	h := &model.languageUI.hover
	doc := model.languageDocument()
	session := model.languageProvider(doc, "textDocument/hover")
	if session == nil {
		return
	}
	position, err := lsp.PositionAt(doc.content, h.offset, session.client.Encoding)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.token++
	job := pencodeLanguageJob{document: doc, uri: doc.uri, content: doc.content, version: doc.version, method: "textDocument/hover", params: map[string]any{"textDocument": lsp.TextDocumentIdentifier{URI: doc.uri}, "position": position}, token: h.token, offset: h.offset, hover: true, context: ctx}
	select {
	case session.jobs <- job:
	default:
		cancel()
		h.cancel = nil
	}
}

// Text hit testing shares the textarea's cached layout (including wrapping,
// variable-width glyphs, tabs and font settings) and its current scroll offset.
func (model *workspaceModel) languageEditorGeometry(world *module.Content, rect htmlui.Rect) (*scene.Text2D, float32, float32) {
	text := module.ContentMut[scene.Text2D](world, model.editor.text)
	transform, ok := module.ContentGet[scene.UITransform](world, model.editor.text)
	if text == nil || !ok {
		return nil, 0, 0
	}
	return text, rect.X + transform.Translation.X.Value, rect.Y + transform.Translation.Y.Value
}

func (model *workspaceModel) editorOffsetAtPointer(world *module.Content, x, y float32, rect htmlui.Rect) int {
	text, originX, originY := model.languageEditorGeometry(world, rect)
	return scene.TextCaretIndex2D(text, model.activeContent(), x-originX, y-originY)
}

func (model *workspaceModel) languageSymbolAtPointer(world *module.Content, x, y float32, editor, body htmlui.Rect) (offset, start, end int, underline htmlui.Rect, valid bool) {
	text, originX, originY := model.languageEditorGeometry(world, editor)
	if text == nil || text.Value != model.activeContent() {
		return
	}
	value := text.Value
	localX, localY := x-originX, y-originY
	caret := scene.TextCaretIndex2D(text, value, localX, localY)
	cx, cy, _, caretHeight, ok := scene.TextCaretRect2D(text, value, caret)
	if !ok {
		return
	}
	if caret > 0 && (localY < cy || localY < cy+caretHeight && localX < cx) {
		caret--
	}
	token := engineui.CodeTokenAt(value, caret)
	offset, start, end = token.Offset, token.Start, token.End
	if offset >= len(value) {
		return
	}
	r, _ := utf8.DecodeRuneInString(value[offset:])
	if !token.Quoted && !isLanguageWordRune(r) {
		return
	}
	ui := &model.languageUI
	ui.underlineRects = ui.underlineRects[:0]
	ui.underlineOriginX, ui.underlineOriginY = originX-body.X, originY-body.Y
	// String contents share one link across visual lines. Other long tokens
	// retain their underline on the hovered line.
	for i, count := 0, scene.TextLineCount2D(text); i < count; i++ {
		lineStart, lineEnd, line, _ := scene.TextLineRange2D(text, i)
		hovered := localY >= line.Y && localY < line.Y+line.Height
		if !token.Quoted && !hovered {
			continue
		}
		segmentStart, segmentEnd := max(start, lineStart), min(end, lineEnd)
		if segmentEnd <= segmentStart {
			continue
		}
		var bounds scene.TextRangeRect2D
		ui.underlineRects, bounds, ok = scene.AppendTextUnderlineRects2D(ui.underlineRects, text, i, segmentStart, segmentEnd, 1)
		if hovered && ok && localX >= bounds.X && localX < bounds.X+bounds.Width {
			underline = htmlui.Rect{X: ui.underlineOriginX + bounds.X, Y: ui.underlineOriginY + bounds.Y, Width: bounds.Width, Height: bounds.Height}
			valid = true
		}
		if !token.Quoted {
			return
		}
	}
	return
}

// offset is a byte position, as used by all LSP jobs.
func (model *workspaceModel) editorPopupPoint(world *module.Content, offset int) (float32, float32) {
	if world == nil {
		return 8, 8
	}
	body, bodyOK := world.HTMLNode(model.surface, "editor-body")
	editor, editorOK := world.HTMLNode(model.surface, "editor")
	if !bodyOK || !editorOK {
		return 8, 8
	}
	bodyRect, _ := body.Bounds()
	editorRect, _ := editor.Bounds()
	text, originX, originY := model.languageEditorGeometry(world, editorRect)
	value := model.activeContent()
	caret := utf8.RuneCountInString(value[:min(offset, len(value))])
	x, y, _, height, ok := scene.TextCaretRect2D(text, value, caret)
	if !ok {
		return 8, 8
	}
	return originX - bodyRect.X + x, originY - bodyRect.Y + y + height
}

func languagePopupStyle(x, y float32) string { return fmt.Sprintf("left:%.1fpx;top:%.1fpx", x, y) }

func languageUnderlineStyle(previous string, x, y, width, height float32) string {
	var buffer [128]byte
	value := buffer[:0]
	for _, property := range [...]struct {
		name  string
		value float32
	}{{"left:", x}, {"top:", y}, {"width:", width}, {"height:", height}} {
		value = append(value, property.name...)
		value = strconv.AppendFloat(value, float64(property.value), 'f', 2, 32)
		value = append(value, "px;"...)
	}
	if previous == string(value) {
		return previous
	}
	return string(value)
}

// Measure after HTML layout so popups stay inside the code area and hovers
// return to their mouse anchor using their actual size.
func (model *workspaceModel) constrainLanguagePopups(world *module.Content) {
	body, ok := world.HTMLNode(model.surface, "editor-body")
	if !ok {
		return
	}
	viewport, ok := body.Bounds()
	if !ok {
		return
	}
	changed := false
	constrain := func(ref string, x, y *float32) {
		node, ok := world.HTMLNode(model.surface, ref)
		if !ok {
			return
		}
		bounds, ok := node.Bounds()
		if !ok {
			return
		}
		anchorX, anchorY := *x, *y
		if ref == "lsp-panel" || ref == "lsp-completion" {
			p := &model.languages.panel
			if p.job.document != nil && p.job.document.file == model.activeFile() {
				anchorX, anchorY = p.originX, p.originY
			}
		}
		nextX := max(4, min(anchorX, viewport.Width-bounds.Width-4))
		nextY := max(4, min(anchorY, viewport.Height-bounds.Height-4))
		if ref == "lsp-hover" {
			h := &model.languageUI.hover
			nextX, nextY = languageHoverPoint(h.pointerX, h.pointerY, viewport, bounds.Width, bounds.Height)
			if !h.positioned {
				h.positioned = true
				changed = true
			}
		}
		if nextX != *x || nextY != *y {
			*x, *y = nextX, nextY
			changed = true
		}
	}
	ui := &model.languageUI
	if ui.hover.open {
		if ui.hover.viewport.Width != viewport.Width || ui.hover.viewport.Height != viewport.Height {
			ui.hover.resetPosition(viewport)
			changed = true
		} else {
			constrain("lsp-hover", &ui.hover.x, &ui.hover.y)
		}
	}
	if ui.contextOpen {
		constrain("lsp-context", &ui.contextX, &ui.contextY)
		if ui.contextGroup != 0 {
			changed = model.positionEditorSubmenu(world) || changed
		}
	}
	if model.languages != nil {
		p := &model.languages.panel
		if p.open {
			constrain("lsp-panel", &p.anchorX, &p.anchorY)
		}
		if p.completion {
			constrain("lsp-completion", &p.anchorX, &p.anchorY)
		}
	}
	if changed {
		markPencodeRootDirtyByModel(world, model)
	}
}
