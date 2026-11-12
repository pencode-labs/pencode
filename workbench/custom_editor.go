package workbench

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onix-engine/onix/html/dom"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/actions"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

const pencodeTextEditor = "text"

type pencodeCustomDocument struct {
	project     *pencodeProject
	provider    *extensions.Editor
	process     *extensions.EditorSession
	definition  *htmlui.Definition[*pencodeCustomDocument, struct{}]
	view        *peb.EditorView
	theme       *pencodeTheme
	model       *workspaceModel
	file        *openFile
	images      map[string][]byte
	links       map[string]string
	find        *pencodeRenderedFind
	selection   *pencodeExtensionSurface
	grid        *pencodeVirtualGrid
	content     string
	message     string
	bindings    []peb.EditorBinding
	queue       [32]peb.EditorEvent
	head, count int
	inflight    bool
	savePending bool
}

func editorMatch(editor *peb.Action, name string) int {
	score := 0
	for _, rule := range editor.Selectors {
		switch rule.Match {
		case peb.FileName:
			if name == rule.Pattern {
				return int(^uint(0) >> 1)
			}
		case peb.FileSuffix:
			if strings.HasSuffix(name, rule.Pattern) {
				score = max(score, len(rule.Pattern))
			}
		}
	}
	return score
}

// File associations are resolved once per file/registry change. Exact names
// beat suffixes; longest suffix wins; stable IDs break ties deterministically.
func (model *workspaceModel) fileEditor(file *openFile) *extensions.Editor {
	if file == nil || file.page.Paged || file.editorID == pencodeTextEditor {
		return nil
	}
	if file.editorGeneration != 0 && file.editorGeneration == model.editorGeneration {
		return file.customEditor
	}
	file.editorGeneration, file.customEditor = model.editorGeneration, nil
	score := 0
	for _, editor := range model.customEditors {
		match := editorMatch(&editor.Action, file.Name)
		if match == 0 || editor.Hidden || editor.Disabled {
			continue
		}
		if file.editorID == editor.ID {
			file.customEditor = editor
			return editor
		}
		if file.editorID == "" && (editor.Default || !file.text()) && match > score {
			file.customEditor, score = editor, match
		}
	}
	if file.customEditor == nil && imageFile(file.Name) && (file.editorID == "" || file.editorID == pencodeImageEditor.ID) {
		file.customEditor = &pencodeImageEditor
	}
	return file.customEditor
}

func (model *workspaceModel) activeCustomEditor() *extensions.Editor {
	return model.fileEditor(model.activeFile())
}
func (model *workspaceModel) textEditorActive() bool {
	file := model.activeFile()
	return !model.sourceState().visible() && file != nil && file.text() && (model.fileEditor(file) == nil || file.editorSplit)
}

// Every resource can open a tab; unsupported types show an explicit text choice.
func (model *workspaceModel) canOpenFile(name string) bool { return name != "" }

func (model *workspaceModel) importEditors(editors []extensions.Editor) {
	for i := range editors {
		if editors[i].Output == nil {
			editors[i].Output = model.outputStore().Channel("Editor: " + editors[i].Text)
		}
		editor := &editors[i]
		model.customEditors = append(model.customEditors, editor)
		if err := model.actionRegistry().Register(actions.Definition{Action: editor.Action, Run: func(c peb.ActionContext) error {
			target := model
			if model.session != nil && model.session.active != nil {
				target = model.session.active.model
			}
			if !target.selectEditor(c.ID) {
				return fmt.Errorf("无法切换打开方式")
			}
			return nil
		}}); err != nil {
			model.extensionFeedback = err.Error()
		}
	}
	sort.Slice(model.customEditors, func(i, j int) bool { return model.customEditors[i].ID < model.customEditors[j].ID })
	model.editorGeneration++
	model.quickCount = -1
	model.refreshQuickMatches()
}

// Keep a document session when a live action only changes its description.
func (model *workspaceModel) updateEditorAction(action peb.Action) bool {
	for _, editor := range model.customEditors {
		if editor.ID != action.ID {
			continue
		}
		if err := model.actionRegistry().Update(action); err != nil {
			model.extensionFeedback = err.Error()
			return true
		}
		editor.Action = action
		model.editorGeneration++
		model.quickCount = -1
		model.refreshQuickMatches()
		return true
	}
	return false
}

func (model *workspaceModel) removeEditors(prefix string) {
	editors := model.customEditors[:0]
	for _, editor := range model.customEditors {
		if strings.HasPrefix(editor.ID, prefix) {
			model.actionRegistry().Remove(editor.ID)
		} else {
			editors = append(editors, editor)
		}
	}
	if len(editors) == len(model.customEditors) {
		return
	}
	clear(model.customEditors[len(editors):])
	model.customEditors = editors
	model.editorGeneration++
	model.quickCount = -1
	model.refreshQuickMatches()
}

func (model *workspaceModel) customDocument() *pencodeCustomDocument {
	if !model.backendConnected() {
		return nil
	}
	file := model.activeFile()
	provider := model.fileEditor(file)
	if provider == nil {
		return nil
	}
	if document := model.customDocuments[file]; document != nil {
		if document.provider == provider {
			return document
		}
		document.close()
	}
	if model.customDocuments == nil {
		model.customDocuments = make(map[*openFile]*pencodeCustomDocument)
	}
	document := &pencodeCustomDocument{file: file, project: model.activeProjectState(), provider: provider, model: model, content: file.content(), message: model.tr("正在加载编辑工具…")}
	root := model.projectPath
	if document.project != nil {
		root = document.project.Path
	}
	model.customDocuments[file] = document
	if provider == &pencodeImageEditor {
		document.loadImage()
		return document
	}
	document.process = provider.Start(root, model.extensionSettingValues(extensionOwner(provider.ID)), model.backgroundWake())
	document.enqueue(peb.EditorEvent{Kind: "open"})
	document.dispatch()
	return document
}

func (document *pencodeCustomDocument) enqueue(event peb.EditorEvent) {
	if event.Kind != "open" && event.Kind != "documentchange" && document.project != nil {
		pinProjectFile(document.project, document.file.Path)
	}
	if document.count == len(document.queue) {
		document.fail(fmt.Errorf("编辑工具事件队列已满，请重新打开文件"))
		return
	}
	index := (document.head + document.count) % len(document.queue)
	document.queue[index] = event
	document.count++
}

func (document *pencodeCustomDocument) dispatch() {
	if document.inflight || document.process == nil {
		return
	}
	if document.count == 0 {
		if document.content == document.file.content() {
			return
		}
		document.enqueue(peb.EditorEvent{Kind: "documentchange"})
	}
	root := document.model.projectPath
	if document.project != nil {
		root = document.project.Path
	}
	message := peb.EditorMessage{Editor: document.provider.LocalID, Path: path.Join(strings.ReplaceAll(root, "\\", "/"), document.file.Path), Content: document.file.content(), Binary: !document.file.text(), Event: document.queue[document.head]}
	if message.Binary {
		message.Content = ""
	}
	if !document.process.Submit(message) {
		document.fail(fmt.Errorf("编辑工具已停止"))
		return
	}
	document.content = document.file.content()
	document.inflight = true
	document.queue[document.head] = peb.EditorEvent{}
	document.head = (document.head + 1) % len(document.queue)
	document.count--
}

func (document *pencodeCustomDocument) fail(err error) {
	document.message = "编辑工具失败：" + err.Error()
	document.model.setActiveStatus(document.message)
	document.process.Close()
	document.process, document.inflight, document.count = nil, false, 0
	clear(document.queue[:])
	if document.model.fontResources != nil {
		markPencodeRootDirtyByModel(document.model.fontResources, document.model)
	}
}

func (document *pencodeCustomDocument) busy() bool {
	if document.provider == &pencodeImageEditor {
		return false
	}
	return document.inflight || document.count != 0 || document.process != nil && document.content != document.file.content()
}

func (model *workspaceModel) pollCustomEditors() bool {
	changed := false
	for file, document := range model.customDocuments {
		files := model.files
		if document.project != nil {
			files = document.project.Files
		}
		if files[file.Path] != file || model.fileEditor(file) != document.provider {
			document.close()
			delete(model.customDocuments, file)
			changed = true
			continue
		}
		if document.provider == &pencodeImageEditor {
			if !document.inflight && document.content != file.content() {
				document.loadImage()
				changed = true
			}
			continue
		}
		if document.process == nil {
			continue
		}
		select {
		case result, ok := <-document.process.Results:
			changed = true
			document.inflight = false
			if !ok {
				document.fail(fmt.Errorf("%s", model.tr("扩展进程已退出")))
				continue
			}
			if result.Err != nil {
				document.fail(result.Err)
				continue
			}
			// Undo/reload may have replaced the snapshot during this exchange.
			// Discard stale changes and send the current host document next.
			if !result.Message.Binary && result.Message.Content != file.content() {
				document.dispatch()
				continue
			}
			if result.Reply.Content != nil {
				if !file.text() {
					document.fail(fmt.Errorf("%s", model.tr("当前仅支持文本文件内容修改")))
					continue
				}
				model.applyFileContent(file, document.project, *result.Reply.Content, true)
				document.content = file.content()
			}
			if result.Reply.View != nil {
				if err := document.setView(result.Reply.View); err != nil {
					document.fail(err)
					continue
				}
				document.images = model.replaceExtensionImages(document.images, result.Assets)
			} else if document.definition == nil {
				document.fail(fmt.Errorf("%s", model.tr("扩展没有提供 HTML 视图")))
				continue
			}
			document.message = ""
		default:
		}
		document.dispatch()
		if document.savePending && !document.busy() {
			document.savePending = false
			if _, err := model.requestFileSave(document.project, file); err != nil {
				model.setProjectStatus(document.project, err.Error())
			}
			changed = true
		}
	}
	if model.customClose.action != pencodeCloseNone {
		request := model.customClose
		model.customClose = pencodeCloseRequest{}
		switch {
		case request.editorTabs != nil:
			model.beginEditorClose(request)
		case request.action == pencodeCloseTab:
			if request.project != nil {
				model.closeProjectTab(request.project, request.path)
			} else {
				model.closeTab(request.path)
			}
		case request.action == pencodeCloseProject:
			model.closeProject(request.project)
		case request.action >= pencodeCloseOtherTabs && request.action <= pencodeCloseAllTabs:
			model.closeProjectTabs(request.action, request.project, request.path)
		case request.action == pencodeCloseWindow:
			if model.requestWindowClose() && model.app != nil {
				model.app.RequestClose()
			}
		}
		changed = changed || model.customClose.action == pencodeCloseNone
	}
	return changed
}

func (document *pencodeCustomDocument) setView(view *peb.EditorView) error {
	if isPencodeGridChunk(view) {
		return document.acceptGridChunk(view)
	}
	previousGrid := document.grid
	preserveGridSelection := document.view != nil && document.view.HTML == view.HTML
	var theme *pencodeTheme
	if document.model != nil {
		theme = document.model.workbenchTheme
	}
	if previous := document.view; previous != nil && document.theme == theme && previous.HTML == view.HTML && previous.CSS == view.CSS && slices.Equal(previous.Events, view.Events) {
		document.view, document.bindings = view, view.Events
		return nil
	}
	var links map[string]string
	var searchable *dom.Node
	var grid *pencodeVirtualGrid
	var gridErr error
	var selectables []pencodeExtensionSelectable
	var focus []pencodeExtensionFocus
	tree, err := dom.Parse(view.HTML)
	if err != nil {
		return err
	}
	prefix := fmt.Sprintf("pencode-extension-editor/%p/", document)
	textView := strings.Contains(view.HTML, "data-pencode-searchable") || strings.Contains(view.HTML, "data-pencode-selectable") || strings.Contains(view.HTML, "data-pencode-grid")
	index := 0
	if err := validateExtensionTree(view, tree, nil, func(node *dom.Node, hidden bool) {
		prepareExtensionViewNode(node, prefix, &index, textView)
		if source, ok := node.Attr("data-pencode-grid"); ok && !hidden && gridErr == nil {
			if grid != nil {
				gridErr = fmt.Errorf("a custom editor supports one virtual grid")
			} else {
				grid, gridErr = parsePencodeGrid(node, source)
			}
		}
		if !hidden {
			ref, _ := node.Attr("data-ref")
			id, _ := node.Attr("id")
			if _, ok := node.Attr("data-pencode-selectable"); ok && ref != "" {
				selectables = append(selectables, pencodeExtensionSelectable{ref: ref, id: id})
			}
			switch node.Data {
			case "button", "input", "textarea", "select", "a":
				role, _ := node.Attr("role")
				focus = append(focus, pencodeExtensionFocus{ref: ref, id: id, selectText: node.Data == "a" || role == "link"})
			}
		}
		if _, ok := node.Attr("data-pencode-searchable"); ok && !hidden && searchable == nil {
			if ref, _ := node.Attr("data-ref"); ref != "" {
				searchable = node
			}
		}
		if node.Data != "a" || hidden {
			return
		}
		ref, _ := node.Attr("data-ref")
		href, _ := node.Attr("href")
		if ref != "" && href != "" {
			if extensionOwnsClick(view.Events, ref) {
				return
			}
			if links == nil {
				links = make(map[string]string)
			}
			links[ref] = href
		}
	}); err != nil {
		return err
	}
	if gridErr != nil {
		return gridErr
	}
	if preserveGridSelection && previousGrid != nil && grid != nil && previousGrid.remote != nil && grid.remote != nil && previousGrid.remote.name == grid.remote.name {
		node := grid.node
		*grid = *previousGrid
		grid.node = node
		node.SetAttr("style", previousGrid.node.AttrValue("style"))
		for _, metadata := range grid.metadata {
			metadata.Parent = node
		}
	}
	componentTemplate, err := htmlui.NewDocumentTemplate(tree)
	if err != nil {
		return err
	}
	definition, err := htmlui.DefineScoped(componentTemplate, theme.resolveCSS(view.CSS), htmlui.Behavior[*pencodeCustomDocument, struct{}]{
		Render: func(context htmlui.RenderContext[*pencodeCustomDocument, struct{}], target *htmlui.View) {
			doc := *context.Props
			if doc.grid != nil {
				doc.grid.component = context.Component
				doc.grid.renderExtent(target)
				doc.grid.render(target)
				if doc.find != nil {
					doc.find.chunks, doc.find.dirty = doc.grid.chunks, true
				}
			}
			for _, event := range (*context.Props).bindings {
				target.Event(event.Ref, htmlui.EventKind(event.Kind))
			}
			for ref := range (*context.Props).links {
				target.Event(ref, htmlui.EventClick)
			}
		},
		HandleEvent: func(context *htmlui.ComponentEventContext[*pencodeCustomDocument, struct{}]) {
			doc := *context.Props
			if doc.process == nil || doc.model.closing.action != pencodeCloseNone {
				return
			}
			event := context.Event
			if href := doc.links[context.Ref]; event.Kind == htmlui.EventClick && href != "" {
				doc.openLink(href)
				return
			}
			doc.enqueue(peb.EditorEvent{Kind: string(event.Kind), Ref: context.Ref, Value: event.Value, InputType: event.InputType, Data: event.Data, Checked: event.Checked, Selected: uint32(max(0, event.Selected)), Anchor: uint32(max(0, event.Anchor)), Focus: uint32(max(0, event.Focus))})
			doc.dispatch()
		},
	})
	if err != nil {
		return err
	}
	if len(selectables) > 0 {
		if document.selection == nil {
			document.selection = &pencodeExtensionSurface{model: document.model, document: document}
		}
		if document.view != nil && document.view.HTML != view.HTML && document.model.actions.ui.selection.surface == document.selection {
			document.model.actions.ui.selection.surface, document.model.actions.ui.selection.grid = nil, nil
		}
		document.selection.tree, document.selection.view = tree, view
		document.selection.selectables, document.selection.focus = selectables, focus
	} else {
		document.selection = nil
	}
	document.definition, document.bindings, document.links = definition, view.Events, links
	document.view, document.theme, document.grid = view, theme, grid
	if preserveGridSelection && previousGrid != nil && document.model != nil {
		selection := &document.model.actions.ui.selection
		if selection.surface == document.selection && selection.grid == previousGrid {
			selection.grid = grid
		}
	}
	if searchable != nil {
		if document.find == nil {
			document.find = &pencodeRenderedFind{}
		}
		if grid != nil {
			document.find.tree, document.find.grid = searchable, grid
			document.find.content = snapshotText(grid.content)
			document.find.chunks = nil
			document.find.dirty, document.find.reveal = true, true
		} else {
			document.find.setTree(searchable)
		}
	} else {
		document.find = nil
	}
	return nil
}

func (document *pencodeCustomDocument) syncTheme() error {
	if document.view == nil || document.theme == document.model.workbenchTheme {
		return nil
	}
	if !strings.Contains(document.view.CSS, "var(--vscode-") {
		document.theme = document.model.workbenchTheme
		return nil
	}
	return document.setView(document.view)
}

func (document *pencodeCustomDocument) openLink(href string) {
	u, err := url.Parse(href)
	if err != nil {
		return
	}
	if u.Scheme == "" && u.Host != "" {
		u.Scheme = "https"
	}
	switch u.Scheme {
	case "http", "https", "mailto":
		output := document.provider.Output
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := openExternalCommand(ctx, u.String()).Run(); err != nil && output != nil {
				fmt.Fprintf(output, "无法打开链接：%v\n", err)
			}
		}()
	case "":
		if u.Path == "" {
			return
		}
		name := path.Clean(path.Join(path.Dir(document.file.Path), u.Path))
		if strings.HasPrefix(u.Path, "/") {
			name = strings.TrimPrefix(path.Clean(u.Path), "/")
		}
		if name == ".." || strings.HasPrefix(name, "../") {
			return
		}
		if document.project != nil {
			document.model.openProjectFile(document.project, name)
		} else {
			document.model.openFile(name)
		}
	}
}

func (model *workspaceModel) renderCustomEditor(view *htmlui.View) {
	model.renderFilePage(view)
	view.RemoveAttr("editor-body", "data-editor-split")
	view.Attr("editor-split", "hidden", "")
	view.Attr("editor-open-text", "hidden", "")
	view.Attr("editor-image", "hidden", "")
	if model.sourceState().visible() {
		view.Attr("custom-editor", "hidden", "")
		view.Attr("editor-tools", "hidden", "")
		return
	}
	file := model.activeFile()
	model.renderEditorHeader(view, file)
	if file == nil {
		view.Attr("editor-tools", "hidden", "")
		view.Attr("custom-editor", "hidden", "")
		return
	}
	model.syncActions()
	image := imageFile(file.Name) && !file.page.Paged
	available := model.actions.openWithCount > 0 || image
	if image {
		view.RemoveAttr("editor-image", "hidden")
		view.Event("editor-image", htmlui.EventClick)
		view.Attr("editor-image", "aria-pressed", strconv.FormatBool(model.fileEditor(file) == &pencodeImageEditor))
		view.Attr("editor-image-icon", "src", model.themedIcon("icons/image.svg", "icon.foreground"))
	}
	view.Event("editor-split", htmlui.EventClick)
	view.Event("editor-text", htmlui.EventClick)
	view.Attr("editor-split-icon", "src", model.themedIcon("icons/split-horizontal.svg", "icon.foreground"))
	view.Attr("editor-text-icon", "src", model.themedIcon("icons/code.svg", "icon.foreground"))
	selected := model.fileEditor(file)
	view.Attr("editor-text", "aria-pressed", strconv.FormatBool(selected == nil))
	split := file.editorSplit && selected != nil && file.text()
	view.Attr("editor-split", "aria-pressed", strconv.FormatBool(split))
	if split {
		view.Attr("editor-body", "data-editor-split", "")
	} else {
		view.RemoveAttr("editor-body", "data-editor-split")
	}
	if available {
		view.RemoveAttr("editor-tools", "hidden")
	} else {
		view.Attr("editor-tools", "hidden", "")
	}
	if available {
		view.RemoveAttr("editor-text", "hidden")
		if file.text() {
			view.RemoveAttr("editor-split", "hidden")
		}
	} else {
		view.Attr("editor-text", "hidden", "")
		view.Attr("editor-split", "hidden", "")
	}
	document := model.customDocument()
	if document == nil && model.textEditorActive() {
		view.Attr("custom-editor", "hidden", "")
		return
	}
	view.RemoveAttr("custom-editor", "hidden")
	message := model.tr("此文件是二进制文件、使用不支持的文本编码，或没有匹配的编辑器。是否使用代码编辑器打开？")
	if document == nil {
		view.RemoveAttr("editor-open-text", "hidden")
		view.Event("editor-open-text", htmlui.EventClick)
	}
	if document != nil {
		if err := document.syncTheme(); err != nil {
			document.fail(err)
		}
		message = document.message
		if document.definition != nil {
			view.AppendComponentChild("custom-editor-content", 1, document.definition, document, struct{}{})
		}
	}
	view.Text("custom-editor-status", message)
	if message == "" {
		view.Attr("custom-editor-status", "hidden", "")
	} else {
		view.RemoveAttr("custom-editor-status", "hidden")
	}
}

func (model *workspaceModel) selectEditor(id string) bool {
	file := model.activeFile()
	if file == nil {
		return false
	}
	if document := model.customDocuments[file]; document != nil && document.busy() {
		model.setActiveStatus(model.tr("请等待编辑工具完成操作"))
		return false
	}
	if id == pencodeTextEditor {
		if !file.text() {
			if err := model.requestTextEditor(file); err != nil {
				model.setActiveStatus(err.Error())
				return false
			}
			return true
		}
		file.editorID, file.editorSplit = id, false
	} else if id == pencodeImageEditor.ID {
		if !imageFile(file.Name) || file.page.Paged {
			return false
		}
		file.editorID = id
	} else {
		found := false
		for _, editor := range model.customEditors {
			if editor.ID == id && !editor.Hidden && !editor.Disabled && editorMatch(&editor.Action, file.Name) != 0 {
				found = true
				break
			}
		}
		if !found {
			return false
		}
		file.editorID = id
	}
	if id != pencodeTextEditor && model.fileIO != nil {
		for _, job := range model.fileIO.pending {
			if job.kind == fileReopenText && job.file == file {
				job.cancel()
			}
		}
	}
	file.editorGeneration = 0
	model.findOpen, model.replaceOpen = false, false
	model.clearFindCache()
	model.resetHighlight()
	return true
}

// Splitting keeps the native source on the left and the selected view on the right.
func (model *workspaceModel) toggleEditorSplit() bool {
	file := model.activeFile()
	if file == nil || !file.text() || model.sourceState().visible() {
		return false
	}
	if file.editorSplit {
		file.editorSplit = false
		model.resetHighlight()
		return true
	}
	if model.fileEditor(file) == nil {
		for _, editor := range model.customEditors {
			if !editor.Hidden && !editor.Disabled && editorMatch(&editor.Action, file.Name) != 0 {
				if !model.selectEditor(editor.ID) {
					return false
				}
				break
			}
		}
		if model.fileEditor(file) == nil && imageFile(file.Name) {
			model.selectEditor(pencodeImageEditor.ID)
		}
	}
	if model.fileEditor(file) == nil {
		return false
	}
	file.editorSplit = true
	model.resetHighlight()
	return true
}

func (document *pencodeCustomDocument) close() {
	if document.provider == &pencodeImageEditor && document.model.fileIO != nil {
		for _, job := range document.model.fileIO.pending {
			if job.imageDocument == document {
				job.cancel()
			}
		}
	}
	if document.model.findPreview == document {
		document.model.findPreview = nil
	}
	document.process.Close()
	document.images = document.model.replaceExtensionImages(document.images, nil)
	document.definition, document.view, document.links = nil, nil, nil
	document.bindings, document.content = nil, ""
	document.find, document.selection, document.grid = nil, nil, nil
	clear(document.queue[:])
}

func (model *workspaceModel) closeCustomEditors() {
	for file, document := range model.customDocuments {
		document.close()
		delete(model.customDocuments, file)
	}
}

// Validate retained region trees in their original parent context, including
// inherited visibility and uniqueness across both template and region nodes.
func validateExtensionTree(view *peb.EditorView, root *dom.Node, regions map[string]*dom.Node, visit func(*dom.Node, bool)) error {
	scratch := pencodeExtensionScratches.Get().(*pencodeExtensionScratch)
	defer scratch.release()
	return scratch.validate(view, root, regions, visit)
}

func (s *pencodeExtensionScratch) validate(view *peb.EditorView, root *dom.Node, regions map[string]*dom.Node, visit func(*dom.Node, bool)) error {
	refs, ids := s.refs, s.ids
	var validate func(*dom.Node, bool) error
	validate = func(node *dom.Node, hidden bool) error {
		_, nodeHidden := node.Attr("hidden")
		hidden = hidden || nodeHidden
		// Validate generated identifiers too when the visitor annotates nodes.
		if visit != nil {
			visit(node, hidden)
		}
		if id, _ := node.Attr("id"); id != "" && !strings.HasPrefix(id, "pencode-extension-") {
			return fmt.Errorf("扩展元素 id 必须以 pencode-extension- 开头：%s", id)
		}
		if id, _ := node.Attr("id"); id != "" {
			if ids[id] {
				return fmt.Errorf("重复的 id：%s", id)
			}
			ids[id] = true
		}
		if ref, _ := node.Attr("data-ref"); ref != "" {
			if refs[ref] {
				return fmt.Errorf("重复的 data-ref：%s", ref)
			}
			refs[ref] = true
		}
		children := node.Children
		if ref, _ := node.Attr("data-ref"); regions[ref] != nil {
			children = regions[ref].Children
		}
		for _, child := range children {
			if err := validate(child, hidden); err != nil {
				return err
			}
		}
		return nil
	}
	if err := validate(root, false); err != nil {
		return err
	}
	for _, event := range view.Events {
		if !refs[event.Ref] {
			return fmt.Errorf("事件缺少对应的 data-ref：%s", event.Ref)
		}
		switch htmlui.EventKind(event.Kind) {
		case htmlui.EventClick, htmlui.EventInput, htmlui.EventChange, htmlui.EventFocus, htmlui.EventBlur, htmlui.EventSubmit, htmlui.EventReset:
		default:
			return fmt.Errorf("不支持的扩展视图事件 %q", event.Kind)
		}
	}
	return nil
}
