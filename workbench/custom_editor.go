package workbench

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/diiyw/onix/internal/dom"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
	htmlui "github.com/diiyw/onix/ui/html"
)

const pencodeTextEditor = "text"

type pencodeCustomDocument struct {
	file        *openFile
	project     *pencodeProject
	provider    *extensions.Editor
	process     *extensions.EditorSession
	definition  *htmlui.Definition[*pencodeCustomDocument, struct{}]
	model       *workspaceModel
	bindings    []peb.EditorBinding
	queue       [32]peb.EditorEvent
	head, count int
	inflight    bool
	content     string
	message     string
	savePending bool
}

func editorMatch(editor *extensions.Editor, name string) int {
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
	if file == nil || file.editorID == pencodeTextEditor {
		return nil
	}
	if file.editorGeneration == model.editorGeneration {
		return file.customEditor
	}
	file.editorGeneration, file.customEditor = model.editorGeneration, nil
	score := 0
	for _, editor := range model.customEditors {
		match := editorMatch(editor, file.Name)
		if match == 0 {
			continue
		}
		if file.editorID == editor.ID {
			file.customEditor = editor
			return editor
		}
		if file.editorID == "" && (editor.Default || !isTextFile(file.Name)) && match > score {
			file.customEditor, score = editor, match
		}
	}
	return file.customEditor
}

func (model *workspaceModel) activeCustomEditor() *extensions.Editor {
	return model.fileEditor(model.activeFile())
}
func (model *workspaceModel) textEditorActive() bool {
	file := model.activeFile()
	return !model.sourceState().visible() && file != nil && isTextFile(file.Name) && model.fileEditor(file) == nil
}

func (model *workspaceModel) canOpenFile(name string) bool {
	if isTextFile(name) {
		return true
	}
	name = path.Base(name)
	for _, editor := range model.customEditors {
		if editorMatch(editor, name) != 0 {
			return true
		}
	}
	return false
}

func (model *workspaceModel) importEditors(editors []extensions.Editor) {
	for i := range editors {
		if editors[i].Output == nil {
			editors[i].Output = model.outputStore().Channel("Editor: " + editors[i].Label)
		}
		model.customEditors = append(model.customEditors, &editors[i])
	}
	sort.Slice(model.customEditors, func(i, j int) bool { return model.customEditors[i].ID < model.customEditors[j].ID })
	model.editorGeneration++
	model.editorOptionsFile = nil
	model.quickCount = -1
	model.refreshQuickMatches()
}

func (model *workspaceModel) removeEditors(prefix string) {
	editors := model.customEditors[:0]
	for _, editor := range model.customEditors {
		if !strings.HasPrefix(editor.ID, prefix) {
			editors = append(editors, editor)
		}
	}
	clear(model.customEditors[len(editors):])
	model.customEditors = editors
	model.editorGeneration++
	model.editorOptionsFile = nil
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
		document.process.Close()
	}
	if model.customDocuments == nil {
		model.customDocuments = make(map[*openFile]*pencodeCustomDocument)
	}
	document := &pencodeCustomDocument{file: file, project: model.activeProjectState(), provider: provider, model: model, content: file.Content, message: "正在加载编辑工具…"}
	root := model.projectPath
	if document.project != nil {
		root = document.project.Path
	}
	document.process = provider.Start(root, model.backgroundWake())
	model.customDocuments[file] = document
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
		if document.content == document.file.Content {
			return
		}
		document.enqueue(peb.EditorEvent{Kind: "documentchange"})
	}
	root := document.model.projectPath
	if document.project != nil {
		root = document.project.Path
	}
	message := peb.EditorMessage{Editor: document.provider.LocalID, Path: path.Join(strings.ReplaceAll(root, "\\", "/"), document.file.Path), Content: document.file.Content, Binary: !isTextFile(document.file.Name), Event: document.queue[document.head]}
	if !document.process.Submit(message) {
		document.fail(fmt.Errorf("编辑工具已停止"))
		return
	}
	document.content = message.Content
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
	return document.inflight || document.count != 0 || document.process != nil && document.content != document.file.Content
}

func (model *workspaceModel) pollCustomEditors() bool {
	changed := false
	for file, document := range model.customDocuments {
		files := model.files
		if document.project != nil {
			files = document.project.Files
		}
		if files[file.Path] != file || model.fileEditor(file) != document.provider {
			document.process.Close()
			delete(model.customDocuments, file)
			changed = true
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
				document.fail(fmt.Errorf("扩展进程已退出"))
				continue
			}
			if result.Err != nil {
				document.fail(result.Err)
				continue
			}
			// Undo/reload may have replaced the snapshot during this exchange.
			// Discard stale changes and send the current host document next.
			if result.Message.Content != file.Content {
				document.dispatch()
				continue
			}
			if result.Reply.Content != nil {
				if !isTextFile(file.Name) {
					document.fail(fmt.Errorf("当前仅支持文本文件内容修改"))
					continue
				}
				model.applyFileContent(file, document.project, *result.Reply.Content, true)
				document.content = file.Content
			}
			if result.Reply.View != nil {
				if len(result.Assets) != 0 {
					if model.extensionImages == nil {
						model.extensionImages = make(map[string][]byte, len(result.Assets))
					}
					for source, data := range result.Assets {
						model.extensionImages[source] = data
					}
				}
				if err := document.setView(result.Reply.View); err != nil {
					document.fail(err)
					continue
				}
			} else if document.definition == nil {
				document.fail(fmt.Errorf("扩展没有提供 HTML 视图"))
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
		switch request.action {
		case pencodeCloseTab:
			if request.project != nil {
				model.closeProjectTab(request.project, request.path)
			} else {
				model.closeTab(request.path)
			}
		case pencodeCloseProject:
			model.closeProject(request.project)
		case pencodeCloseWindow:
			if model.requestWindowClose() && model.app != nil {
				model.app.RequestClose()
			}
		}
		changed = changed || model.customClose.action == pencodeCloseNone
	}
	return changed
}

func (document *pencodeCustomDocument) setView(view *peb.EditorView) error {
	if err := validateExtensionView(view, nil); err != nil {
		return err
	}
	definition, err := htmlui.DefineScoped(htmlui.NewTemplate(view.HTML), view.CSS, htmlui.Behavior[*pencodeCustomDocument, struct{}]{
		Render: func(context htmlui.RenderContext[*pencodeCustomDocument, struct{}], target *htmlui.View) {
			for _, event := range (*context.Props).bindings {
				target.Event(event.Ref, htmlui.EventKind(event.Kind))
			}
		},
		HandleEvent: func(context *htmlui.ComponentEventContext[*pencodeCustomDocument, struct{}]) {
			doc := *context.Props
			if doc.process == nil || doc.model.closing.action != pencodeCloseNone {
				return
			}
			event := context.Event
			doc.enqueue(peb.EditorEvent{Kind: string(event.Kind), Ref: context.Ref, Value: event.Value, InputType: event.InputType, Data: event.Data, Checked: event.Checked, Selected: uint32(max(0, event.Selected)), Anchor: uint32(max(0, event.Anchor)), Focus: uint32(max(0, event.Focus))})
			doc.dispatch()
		},
	})
	if err != nil {
		return err
	}
	document.definition, document.bindings = definition, view.Events
	return nil
}

func (model *workspaceModel) renderCustomEditor(view *htmlui.View) {
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
	view.Event("editor-tool-select", htmlui.EventChange)
	if model.editorOptionsFile != file || model.editorOptionsGeneration != model.editorGeneration || model.editorOptionsID != file.editorID {
		model.editorOptionsFile, model.editorOptionsGeneration, model.editorOptionsID = file, model.editorGeneration, file.editorID
		model.editorOptions = model.editorOptions[:0]
		selected := model.fileEditor(file)
		if isTextFile(file.Name) {
			model.editorOptions = append(model.editorOptions, appearanceOption(componentKey(pencodeTextEditor), pencodeTextEditor, "文本编辑器", selected == nil))
		}
		for _, editor := range model.customEditors {
			if editorMatch(editor, file.Name) != 0 {
				model.editorOptions = append(model.editorOptions, appearanceOption(componentKey(editor.ID), editor.ID, editor.Label, selected == editor))
			}
		}
	}
	view.Keyed("editor-tool-select", model.editorOptions)
	if len(model.editorOptions) > 1 {
		view.RemoveAttr("editor-tools", "hidden")
	} else {
		view.Attr("editor-tools", "hidden", "")
	}
	document := model.customDocument()
	if document == nil && model.textEditorActive() {
		view.Attr("custom-editor", "hidden", "")
		return
	}
	view.RemoveAttr("custom-editor", "hidden")
	message := "没有可用的编辑工具，请安装支持此文件类型的扩展"
	if document != nil {
		message = document.message
		if document.definition != nil {
			htmlui.AppendComponentChild(view, "custom-editor-content", 1, document.definition, document, struct{}{})
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
		model.setActiveStatus("请等待编辑工具完成操作")
		return false
	}
	if id == pencodeTextEditor && isTextFile(file.Name) {
		file.editorID = id
	} else {
		found := false
		for _, editor := range model.customEditors {
			if editor.ID == id && editorMatch(editor, file.Name) != 0 {
				found = true
				break
			}
		}
		if !found {
			return false
		}
		file.editorID = id
	}
	file.editorGeneration = 0
	model.findOpen, model.replaceOpen = false, false
	model.resetHighlight()
	return true
}

func (model *workspaceModel) closeCustomEditors() {
	for file, document := range model.customDocuments {
		document.process.Close()
		delete(model.customDocuments, file)
	}
}

func validateExtensionView(view *peb.EditorView, visit func(*dom.Node, bool)) error {
	root, err := dom.Parse(view.HTML)
	if err != nil {
		return err
	}
	refs := make(map[string]bool)
	ids := make(map[string]bool)
	var validate func(*dom.Node, bool) error
	validate = func(node *dom.Node, hidden bool) error {
		if id, _ := node.Attr("id"); id != "" && !strings.HasPrefix(id, "pencode-extension-") {
			return fmt.Errorf("扩展元素 id 必须以 pencode-extension- 开头：%s", id)
		}
		if id, _ := node.Attr("id"); id != "" {
			if ids[id] {
				return fmt.Errorf("重复的 id：%s", id)
			}
			ids[id] = true
		}
		_, nodeHidden := node.Attr("hidden")
		hidden = hidden || nodeHidden
		if visit != nil {
			visit(node, hidden)
		}
		if ref, _ := node.Attr("data-ref"); ref != "" {
			if refs[ref] {
				return fmt.Errorf("重复的 data-ref：%s", ref)
			}
			refs[ref] = true
		}
		for _, child := range node.Children {
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
