package workbench

import (
	"path/filepath"
	"strings"

	htmlui "github.com/diiyw/onix/ui/html"
)

type pencodeEditorHeader struct {
	projectPath string
	projectName string
	filePath    string
	fullPath    string
	icons       *pencodeFileIcons
	themeID     string
	segments    []htmlui.VNode
}

func (header *pencodeEditorHeader) sync(model *workspaceModel, filePath string) {
	themeID := ""
	if model.workbenchTheme != nil {
		themeID = model.workbenchTheme.id
	}
	if header.projectPath == model.projectPath && header.projectName == model.projectName &&
		header.filePath == filePath && header.icons == model.fileIcons && header.themeID == themeID {
		return
	}
	header.projectPath, header.projectName, header.filePath = model.projectPath, model.projectName, filePath
	header.icons, header.themeID = model.fileIcons, themeID
	header.fullPath = filepath.Join(model.projectPath, filepath.FromSlash(filePath))
	clear(header.segments)
	header.segments = header.segments[:0]
	header.appendSegment(model, model.projectName, true, false)
	for rest := filePath; rest != ""; {
		name, next, more := strings.Cut(rest, "/")
		header.appendSegment(model, name, more, !more)
		rest = next
	}
}

func (header *pencodeEditorHeader) appendSegment(model *workspaceModel, name string, directory, current bool) {
	source := "icons/file-text.svg"
	if directory {
		source = "icons/folder.svg"
	}
	if model.fileIcons != nil {
		if themed := model.fileIcons.source(name, directory); themed != "" {
			source = themed
		}
	}
	segment := htmlui.VNode{Key: uint64(len(header.segments) + 1), Tag: "li"}
	if current {
		segment.Attributes = []htmlui.VNodeAttribute{{Name: "aria-current", Value: "page"}}
	}
	children := make([]htmlui.VNode, 0, 3)
	if len(header.segments) != 0 {
		children = append(children, htmlui.VNode{Key: 1, Tag: "span", Text: "›", Attributes: []htmlui.VNodeAttribute{
			{Name: "class", Value: "pencode-path-separator"},
			{Name: "aria-hidden", Value: "true"},
		}})
	}
	children = append(children, htmlui.VNode{Key: 2, Tag: "img", Attributes: []htmlui.VNodeAttribute{
		{Name: "class", Value: "pencode-path-icon"},
		{Name: "src", Value: model.themedIcon(source, "icon.foreground")},
		{Name: "alt", Value: ""},
	}})
	children = append(children, htmlui.TextVNode(3, "span", name))
	segment.Children = children
	header.segments = append(header.segments, segment)
}

func (model *workspaceModel) renderEditorHeader(view *htmlui.View, file *openFile) {
	if file == nil {
		view.Attr("editor-header", "hidden", "")
		return
	}
	model.renderEditorPath(view, file.Path)
}

func (model *workspaceModel) renderEditorPath(view *htmlui.View, path string) {
	model.editorHeader.sync(model, path)
	view.RemoveAttr("editor-header", "hidden")
	view.Attr("editor-path", "title", model.editorHeader.fullPath)
	view.Keyed("editor-path-segments", model.editorHeader.segments)
}
