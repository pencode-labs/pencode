package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/lsp"
)

type pencodeLanguagePanel struct {
	cancel                     context.CancelFunc
	open, focus, pending       bool
	method, query, title, body string
	rows                       []pencodeLanguageRow
	referenceGroups            []pencodeReferenceGroup
	page                       int
	selection                  int
	token                      uint64
	session                    *pencodeLanguageSession
	job                        pencodeLanguageJob
	message                    chan pencodeLanguageReply
	navigate                   *openFile
	offset                     int
	completion                 bool
	completionIndex            int
	controlClick               bool
	anchorX, anchorY           float32
	originX, originY           float32 // Unclamped caret anchor, retained across popup size changes.
}
type pencodeLanguageRow struct {
	label                string
	matchStart, matchEnd int
	fileName             string
	path                 string
	count                string
	group                int
	session              *pencodeLanguageSession
	location             *lsp.Location
	raw                  json.RawMessage
	kind                 string
	sortText             string
}

var pencodeLanguageRowRefs = [...]string{"lsp-row-0", "lsp-row-1", "lsp-row-2", "lsp-row-3", "lsp-row-4", "lsp-row-5", "lsp-row-6", "lsp-row-7", "lsp-row-8", "lsp-row-9", "lsp-row-10", "lsp-row-11"}
var pencodeLanguageRowIconRefs = [...]string{"lsp-row-0-icon", "lsp-row-1-icon", "lsp-row-2-icon", "lsp-row-3-icon", "lsp-row-4-icon", "lsp-row-5-icon", "lsp-row-6-icon", "lsp-row-7-icon", "lsp-row-8-icon", "lsp-row-9-icon", "lsp-row-10-icon", "lsp-row-11-icon"}
var pencodeLanguageRowLabelRefs = [...]string{"lsp-row-0-label", "lsp-row-1-label", "lsp-row-2-label", "lsp-row-3-label", "lsp-row-4-label", "lsp-row-5-label", "lsp-row-6-label", "lsp-row-7-label", "lsp-row-8-label", "lsp-row-9-label", "lsp-row-10-label", "lsp-row-11-label"}
var pencodeLanguageRowDetailRefs = [...]struct{ path, count, toggle string }{
	{"lsp-row-0-path", "lsp-row-0-count", "lsp-row-0-toggle"},
	{"lsp-row-1-path", "lsp-row-1-count", "lsp-row-1-toggle"},
	{"lsp-row-2-path", "lsp-row-2-count", "lsp-row-2-toggle"},
	{"lsp-row-3-path", "lsp-row-3-count", "lsp-row-3-toggle"},
	{"lsp-row-4-path", "lsp-row-4-count", "lsp-row-4-toggle"},
	{"lsp-row-5-path", "lsp-row-5-count", "lsp-row-5-toggle"},
	{"lsp-row-6-path", "lsp-row-6-count", "lsp-row-6-toggle"},
	{"lsp-row-7-path", "lsp-row-7-count", "lsp-row-7-toggle"},
	{"lsp-row-8-path", "lsp-row-8-count", "lsp-row-8-toggle"},
	{"lsp-row-9-path", "lsp-row-9-count", "lsp-row-9-toggle"},
	{"lsp-row-10-path", "lsp-row-10-count", "lsp-row-10-toggle"},
	{"lsp-row-11-path", "lsp-row-11-count", "lsp-row-11-toggle"},
}
var pencodeCompletionRefs = [...]string{"lsp-completion-0", "lsp-completion-1", "lsp-completion-2", "lsp-completion-3", "lsp-completion-4", "lsp-completion-5", "lsp-completion-6", "lsp-completion-7", "lsp-completion-8", "lsp-completion-9", "lsp-completion-10", "lsp-completion-11"}
var pencodeLanguageContextRefs = [...]string{"lsp-context-0", "lsp-context-1", "lsp-context-2", "lsp-context-3", "lsp-context-4", "lsp-context-5", "lsp-context-6", "lsp-context-7", "lsp-context-8", "lsp-context-9", "lsp-context-10", "lsp-context-11", "lsp-context-12", "lsp-context-13", "lsp-context-14", "lsp-context-15", "lsp-context-16", "lsp-context-17", "lsp-context-18", "lsp-context-19", "lsp-context-20", "lsp-context-21", "lsp-context-22"}
var pencodeLanguageActions = [...]struct{ method, capability, label string }{
	{"textDocument/completion", "completionProvider", "代码补全"},
	{"textDocument/hover", "hoverProvider", "悬浮信息"},
	{"textDocument/signatureHelp", "signatureHelpProvider", "参数提示"},
	{"textDocument/definition", "definitionProvider", "转到定义"},
	{"textDocument/declaration", "declarationProvider", "转到声明"},
	{"textDocument/typeDefinition", "typeDefinitionProvider", "转到类型定义"},
	{"textDocument/implementation", "implementationProvider", "转到实现"},
	{"textDocument/references", "referencesProvider", "查找引用"},
	{"textDocument/rename", "renameProvider", "重命名符号"},
	{"textDocument/formatting", "documentFormattingProvider", "格式化文档"},
	{"textDocument/rangeFormatting", "documentRangeFormattingProvider", "格式化选区"},
	{"textDocument/codeAction", "codeActionProvider", "代码操作"},
	{"textDocument/documentSymbol", "documentSymbolProvider", "文档符号"},
	{"workspace/symbol", "workspaceSymbolProvider", "工作区符号"},
	{"textDocument/documentHighlight", "documentHighlightProvider", "符号高亮位置"},
	{"textDocument/codeLens", "codeLensProvider", "Code Lens"},
	{"textDocument/inlayHint", "inlayHintProvider", "内嵌提示"},
	{"textDocument/foldingRange", "foldingRangeProvider", "折叠区域"},
	{"textDocument/selectionRange", "selectionRangeProvider", "扩展选择"},
	{"textDocument/documentLink", "documentLinkProvider", "文档链接"},
	{"textDocument/prepareCallHierarchy", "callHierarchyProvider", "调用层级"},
	{"textDocument/prepareTypeHierarchy", "typeHierarchyProvider", "类型层级"},
	{"textDocument/diagnostic", "diagnosticProvider", "拉取诊断"},
}

func languageAction(method string) (string, string) {
	for _, a := range pencodeLanguageActions {
		if a.method == method {
			return a.capability, a.label
		}
	}
	return "", method
}

func (model *workspaceModel) renderLanguageTools(view *htmlui.View) {
	state := model.languages
	if state == nil || model.settingsOpen || model.quickOpen || model.findOpen || !model.textEditorActive() {
		view.Attr("lsp-panel", "hidden", "")
		return
	}
	panel := &state.panel
	if !panel.open {
		view.Attr("lsp-panel", "hidden", "")
		return
	}
	view.RemoveAttr("lsp-panel", "hidden")
	view.Attr("lsp-panel", "style", languagePopupStyle(panel.anchorX, panel.anchorY))
	title := panel.title
	if panel.method == "textDocument/references" {
		title = ""
		view.Attr("lsp-panel-header", "hidden", "")
	} else {
		view.RemoveAttr("lsp-panel-header", "hidden")
	}
	view.Text("lsp-title", title)
	view.Text("lsp-body", panel.body)
	if panel.body == "" {
		view.Attr("lsp-body", "hidden", "")
	} else {
		view.RemoveAttr("lsp-body", "hidden")
	}
	view.Attr("lsp-query", "value", panel.query)
	query := panel.method == "textDocument/rename" || panel.method == "workspace/symbol"
	if query {
		view.RemoveAttr("lsp-query-row", "hidden")
	} else {
		view.Attr("lsp-query-row", "hidden", "")
	}
	for _, ref := range []string{"lsp-query", "lsp-submit", "lsp-close", "lsp-previous", "lsp-next"} {
		kind := htmlui.EventClick
		if ref == "lsp-query" {
			kind = htmlui.EventInput
		}
		view.Event(ref, kind)
	}
	start := panel.page * len(pencodeLanguageRowRefs)
	for i, ref := range pencodeLanguageRowRefs {
		view.Event(ref, htmlui.EventClick)
		if start+i >= len(panel.rows) {
			view.Attr(ref, "hidden", "")
			continue
		}
		view.RemoveAttr(ref, "hidden")
		row := &panel.rows[start+i]
		var label [3]htmlui.VNode
		label[0] = htmlui.TextVNode(1, "span", row.label)
		length := 1
		if row.matchEnd > row.matchStart {
			label[0].Text = row.label[:row.matchStart]
			label[1] = htmlui.TextVNode(2, "mark", row.label[row.matchStart:row.matchEnd])
			label[2] = htmlui.TextVNode(3, "span", row.label[row.matchEnd:])
			length = 3
		}
		view.Keyed(pencodeLanguageRowLabelRefs[i], label[:length])
		details := pencodeLanguageRowDetailRefs[i]
		view.Text(details.path, row.path)
		view.Text(details.count, row.count)
		if row.kind == "reference-file" {
			view.RemoveAttr(details.path, "hidden")
			view.RemoveAttr(details.count, "hidden")
			view.RemoveAttr(details.toggle, "hidden")
			view.Attr(details.toggle, "src", model.themedIcon("icons/chevron-right.svg", "icon.foreground"))
			if panel.referenceGroups[row.group].expanded {
				view.Attr(ref, "aria-expanded", "true")
			} else {
				view.Attr(ref, "aria-expanded", "false")
			}
		} else {
			view.Attr(details.path, "hidden", "")
			view.Attr(details.count, "hidden", "")
			view.Attr(details.toggle, "hidden", "")
			view.RemoveAttr(ref, "aria-expanded")
		}
		iconRef := pencodeLanguageRowIconRefs[i]
		if row.fileName == "" {
			view.Attr(iconRef, "hidden", "")
		} else {
			source := "icons/file-text.svg"
			if model.fileIcons != nil {
				if themed := model.fileIcons.source(row.fileName, false); themed != "" {
					source = themed
				}
			}
			view.Attr(iconRef, "src", model.themedIcon(source, "icon.foreground"))
			view.RemoveAttr(iconRef, "hidden")
		}
		class := "pencode-lsp-button pencode-lsp-result"
		activeClass := "pencode-lsp-button pencode-lsp-result active"
		if row.kind == "reference-file" {
			class = "pencode-lsp-button pencode-lsp-result pencode-lsp-reference-file"
			activeClass = "pencode-lsp-button pencode-lsp-result pencode-lsp-reference-file active"
		} else if row.kind == "reference" {
			class = "pencode-lsp-button pencode-lsp-result pencode-lsp-reference-location"
			activeClass = "pencode-lsp-button pencode-lsp-result pencode-lsp-reference-location active"
		}
		if start+i == panel.selection {
			class = activeClass
		}
		view.Attr(ref, "class", class)
	}
	if len(panel.rows) > len(pencodeLanguageRowRefs) {
		view.RemoveAttr("lsp-pagination", "hidden")
		view.Text("lsp-page", fmt.Sprintf("%d / %d", panel.page+1, (len(panel.rows)+len(pencodeLanguageRowRefs)-1)/len(pencodeLanguageRowRefs)))
	} else {
		view.Attr("lsp-pagination", "hidden", "")
	}
}

func (model *workspaceModel) renderLanguageEditorPopups(view *htmlui.View) {
	ui := &model.languageUI
	view.RemoveAttr("editor", "aria-activedescendant")
	available := model.textEditorActive() && !model.settingsOpen && !model.quickOpen && !model.findOpen && model.closing.action == pencodeCloseNone
	for _, ref := range []string{"lsp-hover", "lsp-completion", "lsp-context", "lsp-context-submenu", "lsp-hover-underline"} {
		view.Attr(ref, "hidden", "")
	}
	if !available {
		return
	}
	h := &ui.hover
	if h.open {
		model.highlightLanguageHover()
		view.RemoveAttr("lsp-hover", "hidden")
		class := "pencode-lsp-hover"
		if !h.positioned {
			class = "pencode-lsp-hover pencode-lsp-hover-measuring"
		}
		view.Attr("lsp-hover", "class", class)
		view.Keyed("lsp-hover-body", h.nodes)
		view.Attr("lsp-hover", "style", languagePopupStyle(h.x, h.y))
	}
	if h.link {
		view.RemoveAttr("lsp-hover-underline", "hidden")
		for i, rect := range ui.underlineRects {
			if i == len(ui.underlineNodes) {
				ui.underlineNodes = append(ui.underlineNodes, htmlui.VNode{Key: uint64(i + 1), Tag: "span", Attributes: []htmlui.VNodeAttribute{{Name: "style"}}})
			}
			attribute := &ui.underlineNodes[i].Attributes[0]
			attribute.Value = languageUnderlineStyle(attribute.Value, ui.underlineOriginX+rect.X, ui.underlineOriginY+rect.Y, rect.Width, rect.Height)
		}
		view.Keyed("lsp-hover-underline", ui.underlineNodes[:len(ui.underlineRects)])
	}
	if model.languages != nil && model.languages.panel.completion {
		panel := &model.languages.panel
		view.RemoveAttr("lsp-completion", "hidden")
		view.Attr("lsp-completion", "style", languagePopupStyle(panel.anchorX, panel.anchorY))
		start := (panel.completionIndex / len(pencodeCompletionRefs)) * len(pencodeCompletionRefs)
		view.Event("lsp-completion-previous", htmlui.EventClick)
		view.Event("lsp-completion-next", htmlui.EventClick)
		if len(panel.rows) > len(pencodeCompletionRefs) {
			view.RemoveAttr("lsp-completion-pagination", "hidden")
			view.Text("lsp-completion-page", fmt.Sprintf("%d / %d", start/len(pencodeCompletionRefs)+1, (len(panel.rows)+len(pencodeCompletionRefs)-1)/len(pencodeCompletionRefs)))
		} else {
			view.Attr("lsp-completion-pagination", "hidden", "")
		}
		view.Attr("editor", "aria-activedescendant", pencodeCompletionRefs[panel.completionIndex%len(pencodeCompletionRefs)])
		for i, ref := range pencodeCompletionRefs {
			view.Event(ref, htmlui.EventClick)
			if start+i >= len(panel.rows) {
				view.Attr(ref, "hidden", "")
				continue
			}
			view.RemoveAttr(ref, "hidden")
			view.Text(ref, panel.rows[start+i].label)
			view.Attr(ref, "aria-selected", boolAttribute(start+i == panel.completionIndex))
		}
	} else {
		view.RemoveAttr("editor", "aria-activedescendant")
	}
	model.renderEditorContextMenu(view)
}

func (model *workspaceModel) dismissLanguagePanel() {
	if model.languages == nil {
		return
	}
	p := &model.languages.panel
	if p.message != nil {
		p.message <- pencodeLanguageReply{result: nil}
		p.message = nil
	}
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.job = pencodeLanguageJob{}
	p.open = false
	p.pending = false
	p.focus = false
	p.completion = false
	p.completionIndex = 0
	p.selection = 0
	p.token++
	p.rows = nil
	p.referenceGroups = nil
	p.body = ""
}
func (model *workspaceModel) showLanguageMessage(event pencodeLanguageEvent) {
	model.dismissLanguagePanel()
	p := &model.languages.panel
	var params struct {
		Message string            `json:"message"`
		Actions []json.RawMessage `json:"actions"`
	}
	if err := json.Unmarshal(event.params, &params); err != nil {
		event.reply <- pencodeLanguageReply{err: &lsp.ResponseError{Code: lsp.InvalidParams, Message: err.Error()}}
		return
	}
	model.cancelLanguageAutomatic()
	model.cancelLanguageHover()
	model.dismissLanguageContext()
	p.open = true
	p.title = event.session.server.Label
	p.body = params.Message
	p.message = event.reply
	p.page = 0
	for _, raw := range params.Actions {
		var a struct {
			Title string `json:"title"`
		}
		if json.Unmarshal(raw, &a) == nil {
			p.rows = append(p.rows, pencodeLanguageRow{label: a.Title, raw: raw, kind: "message"})
		}
	}
}
func (model *workspaceModel) handleLanguageEvent(c *module.ContentEventContext[appProps, appState]) bool {
	if model.handleEditorContextEvent(c.World, c.Ref) {
		return true
	}
	if model.languages == nil {
		return false
	}
	p := &model.languages.panel
	for i, ref := range pencodeCompletionRefs {
		if c.Ref == ref {
			index := (p.completionIndex/len(pencodeCompletionRefs))*len(pencodeCompletionRefs) + i
			model.chooseLanguageCompletion(c.World, index)
			return true
		}
	}
	switch c.Ref {
	case "lsp-completion-previous":
		p.completionIndex = max(0, (p.completionIndex/len(pencodeCompletionRefs)-1)*len(pencodeCompletionRefs))
	case "lsp-completion-next":
		p.completionIndex = min(len(p.rows)-1, (p.completionIndex/len(pencodeCompletionRefs)+1)*len(pencodeCompletionRefs))
	case "lsp-query":
		p.query = c.Event.Value
	case "lsp-submit":
		model.runLanguageAction(c.World, p.method, true)
	case "lsp-close":
		model.dismissLanguagePanel()
		c.World.Focus(model.editor.control)
	case "lsp-previous":
		p.page = max(0, p.page-1)
		p.selection = p.page * len(pencodeLanguageRowRefs)
	case "lsp-next":
		p.page = min(max(0, (len(p.rows)-1)/len(pencodeLanguageRowRefs)), p.page+1)
		p.selection = p.page * len(pencodeLanguageRowRefs)
	default:
		for i, ref := range pencodeLanguageRowRefs {
			if c.Ref == ref {
				model.chooseLanguageRow(c.World, p.page*len(pencodeLanguageRowRefs)+i)
				return true
			}
		}
		return false
	}
	return true
}
func (model *workspaceModel) runLanguageAction(world *module.Content, method string, submit bool) {
	automatic := model.languageUI.completionFile != nil && model.languageUI.completionMethod == method
	trigger := model.languageUI.trigger
	model.cancelLanguageAutomatic()
	model.cancelLanguageHover()
	model.dismissLanguageContext()
	doc := model.languageDocument()
	if doc == nil {
		model.setActiveStatus("请安装匹配此文件的 LSP 扩展")
		return
	}
	p := &model.languages.panel
	_, title := languageAction(method)
	p.method = method
	p.completion = false
	session := model.languageProvider(doc, method)
	if session == nil {
		if !automatic {
			model.setActiveStatus("当前语言服务尚未就绪或不支持" + title)
		}
		return
	}
	if (method == "textDocument/rename" || method == "workspace/symbol") && !submit {
		model.dismissLanguagePanel()
		p.open = true
		p.focus = true
		p.title = title
		p.query = ""
		p.job = pencodeLanguageJob{document: doc, content: doc.content, version: doc.version}
		if world != nil {
			if field, ok := module.ContentGet[engineui.TextField](world, model.editor.control); ok {
				p.anchorX, p.anchorY = model.editorPopupPoint(world, runeOffsetBytes(doc.content, field.Caret))
				p.originX, p.originY = p.anchorX, p.anchorY
			}
		}
		return
	}
	if doc.servers[session].version != doc.version {
		model.setActiveStatus("语言服务正在同步文档，请重试")
		return
	}
	caret, anchor := 0, 0
	if world != nil {
		if field, ok := module.ContentGet[engineui.TextField](world, model.editor.control); ok {
			caret, anchor = field.Caret, field.Anchor
		}
	}
	begin, end := pencodeSelectionBytes(doc.content, min(caret, anchor), max(caret, anchor))
	offset := end
	if caret < anchor {
		offset = begin
	}
	position, err := lsp.PositionAt(doc.content, offset, session.client.Encoding)
	if err != nil {
		model.setActiveStatus(err.Error())
		return
	}
	start, _ := lsp.PositionAt(doc.content, begin, session.client.Encoding)
	finish, _ := lsp.PositionAt(doc.content, end, session.client.Encoding)
	params := map[string]any{"textDocument": lsp.TextDocumentIdentifier{URI: doc.uri}, "position": position}
	switch method {
	case "textDocument/completion", "textDocument/signatureHelp":
		triggerKind := 1
		if trigger != "" {
			triggerKind = 2
		}
		params["context"] = map[string]any{"triggerKind": triggerKind}
		if trigger != "" {
			params["context"].(map[string]any)["triggerCharacter"] = trigger
		}
		if method == "textDocument/signatureHelp" {
			params["context"].(map[string]any)["isRetrigger"] = false
		}
	case "textDocument/references":
		params["context"] = map[string]bool{"includeDeclaration": true}
	case "textDocument/rename":
		if strings.TrimSpace(p.query) == "" {
			return
		}
		params["newName"] = p.query
	case "textDocument/formatting", "textDocument/rangeFormatting":
		delete(params, "position")
		params["options"] = map[string]any{"tabSize": 4, "insertSpaces": true}
		if method == "textDocument/rangeFormatting" {
			params["range"] = lsp.Range{Start: start, End: finish}
		}
	case "textDocument/codeAction":
		delete(params, "position")
		params["range"] = lsp.Range{Start: start, End: finish}
		diagnostics := doc.diagnostics[session]
		if diagnostics == nil {
			diagnostics = []lsp.Diagnostic{}
		}
		params["context"] = map[string]any{"diagnostics": diagnostics, "triggerKind": 1}
	case "workspace/symbol":
		params = map[string]any{"query": p.query}
	case "textDocument/inlayHint":
		delete(params, "position")
		last, _ := lsp.PositionAt(doc.content, len(doc.content), session.client.Encoding)
		params["range"] = lsp.Range{End: last}
	case "textDocument/selectionRange":
		delete(params, "position")
		params["positions"] = []lsp.Position{position}
	case "textDocument/documentSymbol", "textDocument/codeLens", "textDocument/foldingRange", "textDocument/documentLink", "textDocument/diagnostic":
		delete(params, "position")
	}
	if p.message != nil {
		model.dismissLanguagePanel()
	}
	if p.cancel != nil {
		p.cancel()
	}
	requestCtx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.token++
	p.session = session
	p.open = true
	p.controlClick = false
	if automatic || method == "textDocument/definition" {
		p.open = false
	}
	p.title = title
	p.body = "请求中…"
	p.rows = nil
	p.referenceGroups = nil
	p.page = 0
	p.anchorX, p.anchorY = model.editorPopupPoint(world, offset)
	p.originX, p.originY = p.anchorX, p.anchorY
	job := pencodeLanguageJob{document: doc, uri: doc.uri, content: doc.content, version: doc.version, method: method, params: params, token: p.token, offset: offset, context: requestCtx, automatic: automatic}
	p.job = job
	select {
	case session.jobs <- job:
		p.pending = true
		if submit && world != nil {
			world.Focus(model.editor.control)
		}
	default:
		p.pending = false
		p.body = "语言服务繁忙，请重试"
	}
}
func (model *workspaceModel) requestLanguageFollowup(method string, params any) {
	p := &model.languages.panel
	job := p.job
	job.method = method
	job.params = params
	job.token = p.token
	p.job = job
	select {
	case p.session.jobs <- job:
		p.pending = true
		p.body = "请求中…"
	default:
		p.pending = false
		p.body = "语言服务繁忙，请重试"
	}
}
func (model *workspaceModel) acceptLanguageResult(world *module.Content, event pencodeLanguageEvent) {
	p := &model.languages.panel
	job := event.job
	if job.hover {
		h := &model.languageUI.hover
		if model.languageHoverBlocked() || h.token != job.token || job.document == nil || job.document.file != model.activeFile() || h.file != job.document.file || job.document.file.Content != job.content || job.document.version != job.version || !job.document.seen {
			return
		}
		if h.cancel != nil {
			h.cancel()
			h.cancel = nil
		}
		h.body = languageText(event.result)
		h.result = event.result
		h.theme = nil
		h.resetPosition(h.viewport)
		h.open = h.body != "" && h.offset == job.offset
		return
	}
	if job.token != p.token || job.document == nil {
		return
	}
	p.pending = false
	if job.method != "workspace/executeCommand" && (job.document.file.Content != job.content || job.document.version != job.version || !job.document.seen) {
		p.body = "文档已改变，请重新请求"
		p.rows = nil
		p.referenceGroups = nil
		return
	}
	p.body = ""
	p.rows = nil
	p.referenceGroups = nil
	p.page = 0
	p.selection = 0
	switch job.method {
	case "textDocument/formatting", "textDocument/rangeFormatting":
		var edits []lsp.TextEdit
		if err := json.Unmarshal(event.result, &edits); err != nil {
			p.body = err.Error()
			return
		}
		model.applyLanguageEdits(world, event.session, job.document, edits)
	case "textDocument/rename":
		var edit lsp.WorkspaceEdit
		if err := json.Unmarshal(event.result, &edit); err != nil {
			p.body = err.Error()
			return
		}
		p.body = "正在应用重命名…"
		complete := func(err error) {
			if err != nil {
				p.body = err.Error()
			} else {
				p.body = "重命名已应用，可撤销或保存"
			}
		}
		if err := model.applyLanguageWorkspaceEdit(event.session, edit, complete); err != nil {
			complete(err)
		}
	case "textDocument/completion":
		var items []json.RawMessage
		if len(event.result) > 0 && event.result[0] == '[' {
			_ = json.Unmarshal(event.result, &items)
		} else {
			var list struct {
				Items []json.RawMessage `json:"items"`
			}
			_ = json.Unmarshal(event.result, &list)
			items = list.Items
		}
		p.completion = true
		p.completionIndex = 0
		p.open = false
		p.body = ""
		prefixStart := languageWordStart(job.content, job.offset)
		prefix := strings.ToLower(job.content[prefixStart:job.offset])
		for _, raw := range items {
			var item lsp.CompletionItem
			if json.Unmarshal(raw, &item) == nil {
				filter := item.FilterText
				if filter == "" {
					filter = item.Label
				}
				if prefix != "" && !languageCompletionMatches(strings.ToLower(filter), prefix) {
					continue
				}
				label := item.Label
				if item.Detail != "" {
					label += " — " + item.Detail
				}
				order := item.SortText
				if order == "" {
					order = item.Label
				}
				p.rows = append(p.rows, pencodeLanguageRow{label: label, raw: raw, kind: "completion", sortText: order})
			}
		}
		sort.SliceStable(p.rows, func(i, j int) bool { return p.rows[i].sortText < p.rows[j].sortText })
		if len(p.rows) == 0 {
			p.completion = false
		}
	case "completionItem/resolve":
		model.applyLanguageCompletion(world, event.session, event.result)
	case "textDocument/signatureHelp":
		p.body = languageText(event.result)
		p.open = p.body != ""
	case "textDocument/definition", "textDocument/declaration", "textDocument/typeDefinition", "textDocument/implementation", "textDocument/references":
		model.languageLocationRows(event.result)
		if job.method == "textDocument/definition" && p.controlClick && languageDefinitionIsClickedSymbol(job, event.session.client.Encoding, p.rows) {
			model.runLanguageAction(world, "textDocument/references", false)
			return
		}
		if job.method == "textDocument/definition" {
			if len(p.rows) > 0 && p.rows[0].location != nil {
				model.navigateLanguageLocation(world, *p.rows[0].location, nil)
			}
			p.open = false
			p.pending = false
		} else if job.method != "textDocument/references" && len(p.rows) == 1 && p.rows[0].location != nil {
			if model.navigateLanguageLocation(world, *p.rows[0].location, nil) {
				p.open = false
				p.pending = false
			}
		}
	case "textDocument/documentSymbol", "workspace/symbol":
		model.languageSymbolRows(event.result, job.uri, 0)
	case "textDocument/codeAction":
		model.languageActionRows(event.result)
	case "codeAction/resolve":
		model.applyLanguageCodeAction(world, event.session, event.result)
	case "textDocument/codeLens":
		var items []json.RawMessage
		_ = json.Unmarshal(event.result, &items)
		for _, raw := range items {
			var item struct {
				Range   lsp.Range    `json:"range"`
				Command *lsp.Command `json:"command"`
			}
			if json.Unmarshal(raw, &item) == nil {
				label := fmt.Sprintf("行 %d", item.Range.Start.Line+1)
				if item.Command != nil {
					label += " · " + item.Command.Title
				}
				p.rows = append(p.rows, pencodeLanguageRow{label: label, raw: raw, kind: "lens"})
			}
		}
	case "codeLens/resolve":
		var item struct {
			Command *lsp.Command `json:"command"`
		}
		if json.Unmarshal(event.result, &item) == nil && item.Command != nil {
			model.executeLanguageCommand(*item.Command)
		}
	case "workspace/executeCommand":
		p.body = "命令已执行"
	case "textDocument/selectionRange":
		var items []struct {
			Range lsp.Range `json:"range"`
		}
		if json.Unmarshal(event.result, &items) == nil && len(items) > 0 {
			model.selectLanguageRange(world, job.document, items[0].Range, event.session.client.Encoding)
		}
	case "textDocument/documentHighlight":
		var items []struct {
			Range lsp.Range `json:"range"`
		}
		_ = json.Unmarshal(event.result, &items)
		for _, item := range items {
			location := lsp.Location{URI: job.uri, Range: item.Range}
			p.rows = append(p.rows, pencodeLanguageRow{label: fmt.Sprintf("行 %d，列 %d", item.Range.Start.Line+1, item.Range.Start.Character+1), location: &location})
		}
	case "textDocument/prepareCallHierarchy", "textDocument/prepareTypeHierarchy", "callHierarchy/incomingCalls", "callHierarchy/outgoingCalls", "typeHierarchy/supertypes", "typeHierarchy/subtypes":
		model.languageHierarchyRows(job.method, event.result)
	case "textDocument/diagnostic":
		var report struct {
			Kind  string           `json:"kind"`
			Items []lsp.Diagnostic `json:"items"`
		}
		if json.Unmarshal(event.result, &report) == nil {
			if report.Kind != "unchanged" {
				job.document.diagnostics[event.session] = report.Items
				model.setProblems(event.session, job.uri, report.Items)
			}
			model.showLanguageProblems()
		}
	default:
		p.body = languageText(event.result)
	}
	if len(p.rows) == 0 && p.body == "" {
		p.body = "没有结果"
	}
}

func (model *workspaceModel) chooseLanguageCompletion(world *module.Content, index int) {
	if model.languages == nil {
		return
	}
	p := &model.languages.panel
	if index < 0 || index >= len(p.rows) || !p.completion {
		return
	}
	if world != nil {
		world.Focus(model.editor.control)
	}
	model.chooseLanguageRow(world, index)
	p.completion = false
}
func languageText(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	var out strings.Builder
	var appendValue func(any)
	appendValue = func(v any) {
		switch v := v.(type) {
		case string:
			if out.Len() > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(v)
		case []any:
			for _, item := range v {
				appendValue(item)
			}
		case map[string]any:
			if x, ok := v["contents"]; ok {
				appendValue(x)
				return
			}
			if x, ok := v["value"]; ok {
				appendValue(x)
				return
			}
			if x, ok := v["signatures"]; ok {
				appendValue(x)
				return
			}
			if x, ok := v["label"]; ok {
				appendValue(x)
				if d, ok := v["documentation"]; ok {
					appendValue(d)
				}
				return
			}
			data, _ := json.MarshalIndent(v, "", "  ")
			appendValue(string(data))
		}
	}
	appendValue(value)
	if out.Len() > 64<<10 {
		return out.String()[:64<<10]
	}
	return out.String()
}
func (model *workspaceModel) languageLocationRows(raw json.RawMessage) {
	var items []json.RawMessage
	if len(raw) > 0 && raw[0] == '[' {
		_ = json.Unmarshal(raw, &items)
	} else if string(raw) != "null" {
		items = []json.RawMessage{raw}
	}
	p := &model.languages.panel
	references := p.method == "textDocument/references"
	var groups map[lsp.URI]int
	root := model.projectPath
	if root == "" && p.session != nil {
		root = p.session.key.root
	}
	if references {
		p.referenceGroups = nil
		groups = make(map[lsp.URI]int)
	}
	for _, item := range items {
		var location lsp.Location
		if json.Unmarshal(item, &location) != nil {
			continue
		}
		if location.URI == "" {
			var link lsp.LocationLink
			if json.Unmarshal(item, &link) != nil {
				continue
			}
			location = lsp.Location{URI: link.TargetURI, Range: link.TargetSelectionRange}
		}
		if location.URI == "" {
			continue
		}
		if references {
			p.appendReferenceLocation(location, root, groups)
			continue
		}
		name := string(location.URI)
		if path, err := location.URI.Path(); err == nil {
			name = path
			if p.session != nil {
				if relative, err := filepath.Rel(p.session.key.root, path); err == nil {
					name = filepath.ToSlash(relative)
				}
			}
		}
		p.rows = append(p.rows, pencodeLanguageRow{label: fmt.Sprintf("%s:%d:%d", name, location.Range.Start.Line+1, location.Range.Start.Character+1), fileName: filepath.Base(name), location: &location})
	}
	if references {
		model.prepareReferencePreviews(groups)
		p.rows = make([]pencodeLanguageRow, 0, len(items)+len(groups))
		p.rebuildReferenceRows(0)
	}
}
func (model *workspaceModel) languageSymbolRows(raw json.RawMessage, uri lsp.URI, depth int) {
	if depth > 64 {
		return
	}
	var items []struct {
		Name           string          `json:"name"`
		SelectionRange lsp.Range       `json:"selectionRange"`
		Location       *lsp.Location   `json:"location"`
		Children       json.RawMessage `json:"children"`
	}
	if json.Unmarshal(raw, &items) != nil {
		return
	}
	for _, item := range items {
		location := item.Location
		if location == nil {
			location = &lsp.Location{URI: uri, Range: item.SelectionRange}
		}
		model.languages.panel.rows = append(model.languages.panel.rows, pencodeLanguageRow{label: strings.Repeat("  ", depth) + item.Name, location: location})
		model.languageSymbolRows(item.Children, uri, depth+1)
	}
}
func (model *workspaceModel) languageActionRows(raw json.RawMessage) {
	var items []json.RawMessage
	_ = json.Unmarshal(raw, &items)
	for _, item := range items {
		var action struct {
			Title    string `json:"title"`
			Disabled *struct {
				Reason string `json:"reason"`
			} `json:"disabled"`
		}
		if json.Unmarshal(item, &action) == nil {
			label := action.Title
			kind := "action"
			if action.Disabled != nil {
				label += "（" + action.Disabled.Reason + "）"
				kind = "disabled"
			}
			model.languages.panel.rows = append(model.languages.panel.rows, pencodeLanguageRow{label: label, raw: item, kind: kind})
		}
	}
}
func (model *workspaceModel) showLanguageProblems() {
	model.cancelLanguageAutomatic()
	model.cancelLanguageHover()
	model.dismissLanguagePanel()
	model.bottom.active = pencodePanelProblems
}
func (model *workspaceModel) chooseLanguageRow(world *module.Content, index int) {
	p := &model.languages.panel
	if index < 0 || index >= len(p.rows) {
		return
	}
	row := p.rows[index]
	if row.kind == "reference-file" {
		group := &p.referenceGroups[row.group]
		group.expanded = !group.expanded
		p.rebuildReferenceRows(row.group)
		return
	}
	if row.kind == "message" {
		if p.message != nil {
			p.message <- pencodeLanguageReply{result: row.raw}
			p.message = nil
		}
		model.dismissLanguagePanel()
		return
	}
	if row.location != nil {
		if row.session != nil {
			p.session = row.session
		}
		if model.navigateLanguageLocation(world, *row.location, nil) {
			p.open = false
			p.completion = false
		}
		return
	}
	if p.job.document == nil || p.job.document.content != p.job.content || p.job.document.file.Content != p.job.content {
		p.body = "文档已改变，请重新请求"
		return
	}
	switch row.kind {
	case "completion":
		var provider struct {
			ResolveProvider bool `json:"resolveProvider"`
		}
		_ = json.Unmarshal(p.session.client.ProviderOptions("textDocument/completion", "completionProvider"), &provider)
		if provider.ResolveProvider {
			model.requestLanguageFollowup("completionItem/resolve", row.raw)
		} else {
			model.applyLanguageCompletion(world, p.session, row.raw)
		}
	case "action":
		var provider struct {
			ResolveProvider bool `json:"resolveProvider"`
		}
		_ = json.Unmarshal(p.session.client.Capabilities["codeActionProvider"], &provider)
		var action lsp.CodeAction
		if json.Unmarshal(row.raw, &action) == nil && action.Edit == nil && action.Command == nil && provider.ResolveProvider {
			model.requestLanguageFollowup("codeAction/resolve", row.raw)
		} else {
			model.applyLanguageCodeAction(world, p.session, row.raw)
		}
	case "lens":
		var lens struct {
			Command *lsp.Command `json:"command"`
		}
		_ = json.Unmarshal(row.raw, &lens)
		if lens.Command != nil {
			model.executeLanguageCommand(*lens.Command)
		} else {
			model.requestLanguageFollowup("codeLens/resolve", row.raw)
		}
	case "incoming", "outgoing", "super", "sub":
		method := map[string]string{"incoming": "callHierarchy/incomingCalls", "outgoing": "callHierarchy/outgoingCalls", "super": "typeHierarchy/supertypes", "sub": "typeHierarchy/subtypes"}[row.kind]
		model.requestLanguageFollowup(method, map[string]any{"item": row.raw})
	}
}
func (model *workspaceModel) applyLanguageEdits(world *module.Content, session *pencodeLanguageSession, doc *pencodeLanguageDocument, edits []lsp.TextEdit) bool {
	content, err := lsp.ApplyTextEdits(doc.file.Content, edits, session.client.Encoding)
	if err != nil {
		model.languages.panel.body = err.Error()
		return false
	}
	model.applyFileContent(doc.file, doc.project, content, true)
	if doc.file == model.activeFile() && world != nil {
		setPencodeEditorValue(world, model.surface, content)
	}
	model.languages.panel.body = "编辑已应用，可撤销或保存"
	model.languages.panel.rows = nil
	return true
}
func (model *workspaceModel) applyLanguageCompletion(world *module.Content, session *pencodeLanguageSession, raw json.RawMessage) {
	p := &model.languages.panel
	var item lsp.CompletionItem
	if err := json.Unmarshal(raw, &item); err != nil {
		p.body = err.Error()
		return
	}
	if item.InsertTextFormat == 2 {
		p.body = "服务器返回了未协商的 snippet 补全"
		return
	}
	edits := item.AdditionalTextEdits
	var primary lsp.TextEdit
	if item.TextEdit != nil {
		primary = *item.TextEdit
	} else {
		text := item.InsertText
		if text == "" {
			text = item.Label
		}
		start := languageWordStart(p.job.content, p.job.offset)
		begin, _ := lsp.PositionAt(p.job.content, start, session.client.Encoding)
		end, _ := lsp.PositionAt(p.job.content, p.job.offset, session.client.Encoding)
		primary = lsp.TextEdit{Range: lsp.Range{Start: begin, End: end}, NewText: text}
	}
	edits = append(edits, primary)
	if !model.applyLanguageEdits(world, session, p.job.document, edits) {
		return
	}
	// Additional imports may shift the primary edit. Compute its final caret from
	// the change in prefix length using the same validated edit set.
	caretByte, _ := lsp.OffsetAt(p.job.content, primary.Range.Start, session.client.Encoding)
	originalStart := caretByte
	for _, edit := range item.AdditionalTextEdits {
		start, _ := lsp.OffsetAt(p.job.content, edit.Range.Start, session.client.Encoding)
		end, _ := lsp.OffsetAt(p.job.content, edit.Range.End, session.client.Encoding)
		if end <= originalStart {
			caretByte += len(edit.NewText) - (end - start)
		}
	}
	caretByte += len(primary.NewText)
	if world != nil && p.job.document.file == model.activeFile() {
		content := p.job.document.file.Content
		model.setEditorValueAndCaret(world, content, utf8.RuneCountInString(content[:min(caretByte, len(content))]))
	}
	p.open, p.completion = false, false
	if item.Command != nil {
		model.executeLanguageCommand(*item.Command)
	}
}
func (model *workspaceModel) applyLanguageCodeAction(world *module.Content, session *pencodeLanguageSession, raw json.RawMessage) {
	var action lsp.CodeAction
	if err := json.Unmarshal(raw, &action); err != nil {
		var command lsp.Command
		if json.Unmarshal(raw, &command) == nil && command.Command != "" {
			model.executeLanguageCommand(command)
		} else {
			model.languages.panel.body = err.Error()
		}
		return
	}
	if action.Disabled != nil {
		return
	}
	complete := func(err error) {
		if err != nil {
			model.languages.panel.body = err.Error()
			return
		}
		if action.Command != nil {
			model.executeLanguageCommand(*action.Command)
		} else {
			model.languages.panel.body = "代码操作已应用"
		}
	}
	if action.Edit != nil {
		if err := model.applyLanguageWorkspaceEdit(session, *action.Edit, complete); err != nil {
			complete(err)
		}
	} else {
		complete(nil)
	}
}
func (model *workspaceModel) executeLanguageCommand(command lsp.Command) {
	model.requestLanguageFollowup("workspace/executeCommand", map[string]any{"command": command.Command, "arguments": command.Arguments})
}
func (model *workspaceModel) selectLanguageRange(world *module.Content, doc *pencodeLanguageDocument, r lsp.Range, encoding lsp.PositionEncoding) {
	if world == nil || doc.file != model.activeFile() {
		return
	}
	start, err := lsp.OffsetAt(doc.content, r.Start, encoding)
	if err != nil {
		return
	}
	end, err := lsp.OffsetAt(doc.content, r.End, encoding)
	if err != nil {
		return
	}
	if field := module.ContentMut[engineui.TextField](world, model.editor.control); field != nil {
		field.Anchor = utf8.RuneCountInString(doc.content[:start])
		field.Caret = utf8.RuneCountInString(doc.content[:end])
		model.revealEditorCaret(world, field.Caret)
	}
}
func (model *workspaceModel) languageHierarchyRows(method string, raw json.RawMessage) {
	var items []json.RawMessage
	_ = json.Unmarshal(raw, &items)
	for _, item := range items {
		if method == "callHierarchy/incomingCalls" || method == "callHierarchy/outgoingCalls" {
			var call struct {
				From json.RawMessage `json:"from"`
				To   json.RawMessage `json:"to"`
			}
			_ = json.Unmarshal(item, &call)
			item = call.From
			if item == nil {
				item = call.To
			}
		}
		var symbol struct {
			Name           string    `json:"name"`
			URI            lsp.URI   `json:"uri"`
			SelectionRange lsp.Range `json:"selectionRange"`
		}
		if json.Unmarshal(item, &symbol) != nil {
			continue
		}
		location := lsp.Location{URI: symbol.URI, Range: symbol.SelectionRange}
		p := &model.languages.panel
		p.rows = append(p.rows, pencodeLanguageRow{label: symbol.Name, location: &location})
		if strings.Contains(method, "CallHierarchy") || strings.HasPrefix(method, "callHierarchy/") {
			p.rows = append(p.rows, pencodeLanguageRow{label: "  调用者 → " + symbol.Name, raw: item, kind: "incoming"}, pencodeLanguageRow{label: "  调用目标 → " + symbol.Name, raw: item, kind: "outgoing"})
		} else {
			p.rows = append(p.rows, pencodeLanguageRow{label: "  父类型 → " + symbol.Name, raw: item, kind: "super"}, pencodeLanguageRow{label: "  子类型 → " + symbol.Name, raw: item, kind: "sub"})
		}
	}
}
func (model *workspaceModel) focusLanguageControls(world *module.Content) {
	if world != nil && model.languageUI.contextFocus {
		if field := module.ContentMut[engineui.TextField](world, model.editor.control); field != nil {
			caret, anchor := field.Caret, field.Anchor
			world.Focus(model.editor.control)
			field.Caret, field.Anchor = caret, anchor
		}
		model.languageUI.contextFocus = false
	}
	if model.languages == nil || world == nil {
		return
	}
	p := &model.languages.panel
	if p.focus && p.open {
		if entity, ok := world.HTMLControl(model.surface, "lsp-query"); ok && world.Focus(entity) {
			p.focus = false
		}
	}
	if p.navigate != nil && model.editor.file == p.navigate {
		caret := utf8.RuneCountInString(p.navigate.Content[:min(p.offset, len(p.navigate.Content))])
		// The editor has already bound this document; navigation only moves the caret.
		if field := module.ContentMut[engineui.TextField](world, model.editor.control); field != nil {
			field.Caret, field.Anchor = caret, caret
		}
		model.revealEditorCaret(world, caret)
		p.navigate = nil
	}
}
func (model *workspaceModel) handleLanguageKeys(world *module.Content, state *input.State) (handled bool) {
	if model.settingsOpen || model.quickOpen || model.findOpen || !model.textEditorActive() {
		return false
	}
	defer func() {
		if handled {
			if field, ok := module.ContentGet[engineui.TextField](world, model.editor.control); ok && field.Focused {
				model.languageUI.keysHandled = true
			}
		}
	}()
	if model.languageUI.contextOpen {
		model.languageUI.keysHandled = true
		return model.handleEditorContextKeys(world, state)
	}
	if state.KeyPressed(input.KeyEscape) && (model.languageUI.contextOpen || model.languageUI.hover.file != nil || model.languageUI.completionFile != nil) {
		model.dismissLanguageContext()
		model.cancelLanguageHover()
		model.cancelLanguageAutomatic()
		model.dismissLanguagePanel()
		return true
	}
	if model.languages == nil {
		return false
	}
	p := &model.languages.panel
	if state.KeyPressed(input.KeyEscape) {
		if model.languageUI.contextOpen || model.languageUI.hover.open || model.languageUI.hover.link || p.completion || p.job.automatic && p.cancel != nil {
			model.dismissLanguageContext()
			model.cancelLanguageHover()
			model.cancelLanguageAutomatic()
			model.dismissLanguagePanel()
			return true
		}
	}
	if p.open && state.KeyPressed(input.KeyEscape) {
		model.dismissLanguagePanel()
		return true
	}
	if p.open && (p.method == "textDocument/rename" || p.method == "workspace/symbol") && state.KeyPressed(input.KeyEnter) {
		model.runLanguageAction(world, p.method, true)
		return true
	}
	field, ok := module.ContentGet[engineui.TextField](world, model.editor.control)
	if !ok || !field.Focused {
		return false
	}
	if p.completion && len(p.rows) > 0 {
		switch {
		case state.KeyPressed(input.KeyDown):
			p.completionIndex = (p.completionIndex + 1) % len(p.rows)
			return true
		case state.KeyPressed(input.KeyUp):
			p.completionIndex = (p.completionIndex + len(p.rows) - 1) % len(p.rows)
			return true
		case state.KeyPressed(input.KeyEnter), state.KeyPressed(input.KeyTab):
			model.chooseLanguageCompletion(world, p.completionIndex)
			return true
		}
	}
	if p.open && len(p.rows) > 0 {
		switch {
		case state.KeyPressed(input.KeyDown):
			p.selection = (p.selection + 1) % len(p.rows)
			p.page = p.selection / len(pencodeLanguageRowRefs)
			return true
		case state.KeyPressed(input.KeyUp):
			p.selection = (p.selection + len(p.rows) - 1) % len(p.rows)
			p.page = p.selection / len(pencodeLanguageRowRefs)
			return true
		case state.KeyPressed(input.KeyEnter):
			model.chooseLanguageRow(world, p.selection)
			return true
		}
	}
	ctrl := state.KeyHeld(input.KeyLeftControl) || state.KeyHeld(input.KeyRightControl)
	shift := state.KeyHeld(input.KeyLeftShift) || state.KeyHeld(input.KeyRightShift)
	alt := state.KeyHeld(input.KeyLeftAlt) || state.KeyHeld(input.KeyRightAlt)
	method := ""
	switch {
	case ctrl && state.KeyPressed(input.KeySpace):
		method = "textDocument/completion"
		if shift {
			method = "textDocument/signatureHelp"
		}
	case state.KeyPressed(input.KeyF12):
		method = "textDocument/definition"
		if shift {
			method = "textDocument/references"
		}
		if ctrl {
			method = "textDocument/implementation"
		}
	case state.KeyPressed(input.KeyF2):
		method = "textDocument/rename"
	case alt && shift && state.KeyPressed(input.KeyF):
		method = "textDocument/formatting"
	case ctrl && state.KeyPressed(input.KeyPeriod):
		method = "textDocument/codeAction"
	}
	if method == "" {
		return false
	}
	model.runLanguageAction(world, method, false)
	return true
}
