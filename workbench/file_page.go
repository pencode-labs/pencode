package workbench

import (
	"fmt"
	"math"
	"strconv"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/protocol"
)

func (f *openFile) initPage() {
	f.page = protocol.DocumentPage{}
	f.pageDirty, f.pageLoading = false, false
	if f.document != nil {
		f.page = f.document.Page()
	}
	f.updatePageLabel()
}

func (f *openFile) updatePageLabel() {
	if !f.page.Paged {
		f.pageLabel = ""
		return
	}
	f.pageLabel = fmt.Sprintf("%d–%d / %d 字节 · 行号、查找与撤销限当前段", f.page.Offset, min(f.page.Size, f.page.Offset+uint64(f.snapshot().Len())), f.page.Size)
}

func (m *workspaceModel) requestFilePage(offset uint64, tail bool) error {
	f := m.activeFile()
	if f == nil || !f.page.Paged {
		return nil
	}
	if f.document == nil {
		return fmt.Errorf("%s", m.tr("大文件连接已断开，请重新加载"))
	}
	if f.pageLoading || m.fileIOMutating(m.activeProjectState()) {
		return fmt.Errorf("%s", m.tr("请等待文件操作完成"))
	}
	if m.fileIO != nil {
		for _, j := range m.fileIO.pending {
			if j.file == f || j.kind == fileReload && j.project == m.activeProjectState() {
				return fmt.Errorf("%s", m.tr("请等待文件操作完成"))
			}
		}
	}
	j := &fileIOJob{kind: fileReadPage, project: m.activeProjectState(), file: f, document: f.document, relative: f.Path, offset: offset, tail: tail}
	if err := m.submitFileIO(j); err != nil {
		return err
	}
	f.pageLoading = true
	m.setActiveStatus(m.tr("正在加载分段…"))
	return nil
}

func (m *workspaceModel) acceptFilePage(r *fileIOResult) {
	j, f := r.job, r.job.file
	if !m.fileLive(j.project, j.relative, f) || f.document != j.document {
		return
	}
	f.pageLoading = false
	if r.err != nil {
		m.setProjectStatus(j.project, m.tr("加载分段失败：")+r.err.Error())
		return
	}
	backward := r.page.Offset < f.page.Offset
	f.page = r.page
	f.pageDirty = f.Dirty
	f.contentSnapshot, f.savedContent = snapshotText(r.content), snapshotText(r.content)
	f.updatePageLabel()
	f.pencodeEditHistory = pencodeEditHistory{}
	f.editorView = pencodeEditorView{}
	if j.tail && backward {
		f.editorView.caret = f.snapshot().Runes()
		f.editorView.anchor = f.editorView.caret
		f.editorView.scrollY = math.MaxFloat32
	}
	m.highlightCache.remove(f)
	if m.activeFile() == f {
		m.findOpen, m.replaceOpen = false, false
		m.clearFindCache()
		m.resetHighlight()
		m.editor.file = nil
		m.editor.needsFocus = true
	}
	m.setProjectStatus(j.project, "")
}

func (m *workspaceModel) renderFilePage(view *htmlui.View) {
	f := m.activeFile()
	view.Attr("editor-page", "hidden", "")
	view.RemoveAttr("editor", "readonly")
	if f == nil || !f.page.Paged || !m.textEditorActive() || m.actions.ui.editor != nil {
		return
	}
	view.RemoveAttr("editor-page", "hidden")
	if f.pageLoading {
		view.Attr("editor", "readonly", "")
	}
	end := min(f.page.Size, f.page.Offset+uint64(f.snapshot().Len()))
	view.Text("editor-page-range", f.pageLabel)
	if f.pageLoading {
		view.Attr("editor-page-position", "disabled", "")
	} else {
		view.RemoveAttr("editor-page-position", "disabled")
	}
	for _, action := range [...]struct {
		ref      string
		disabled bool
	}{
		{"editor-page-first", f.page.Offset == 0},
		{"editor-page-prev", f.page.Offset == 0},
		{"editor-page-next", end >= f.page.Size},
		{"editor-page-last", end >= f.page.Size},
	} {
		if action.disabled || f.pageLoading {
			view.Attr(action.ref, "disabled", "")
		} else {
			view.RemoveAttr(action.ref, "disabled")
		}
	}
}

func (m *workspaceModel) handleFilePage(ref string) bool {
	f := m.activeFile()
	if f == nil || !f.page.Paged {
		return false
	}
	var offset uint64
	switch ref {
	case "editor-page-first":
	case "editor-page-prev":
		offset = f.page.Offset - min(f.page.Offset, protocol.TextPageSize)
	case "editor-page-next":
		offset = f.page.Offset + uint64(f.snapshot().Len())
	case "editor-page-last":
		offset = f.page.Size - min(f.page.Size, protocol.TextPageSize)
	default:
		return false
	}
	if err := m.requestFilePage(offset, false); err != nil {
		m.setActiveStatus(err.Error())
	}
	return true
}

// Cross a page boundary only in response to another wheel gesture at the edge.
// Merely opening a file never reads the next page.
func (m *workspaceModel) updateFilePageWheel(world *module.Content, state *input.State) {
	f := m.activeFile()
	pointer := state.Pointer()
	if f == nil || !f.page.Paged || f.pageLoading || pointer.WheelY == 0 || pointer.WheelX != 0 || !m.textEditorActive() || m.settingsOpen || m.quickOpen || m.findOpen || m.closing.action != pencodeCloseNone || m.fileContext.open || m.languageUI.contextOpen {
		return
	}
	zoom := max(float32(m.settings.Zoom)/100, .01)
	if !m.pointerInsideRefs(world, pointer.X/zoom, pointer.Y/zoom, []string{"editor"}) {
		return
	}
	area, ok := world.Get[engineui.Textarea](m.editor.control)
	value, textOK := world.Get[scene.Text2D](m.editor.text)
	link, linkOK := world.Get[htmlui.ControlText](m.editor.text)
	if !ok || !textOK || !linkOK {
		return
	}
	var offset uint64
	backward := pointer.WheelY > 0
	if backward {
		if area.ScrollY > 0 || f.page.Offset == 0 {
			return
		}
		offset = f.page.Offset - min(f.page.Offset, protocol.TextPageSize)
	} else {
		height := float32(scene.TextLineCount2D(&value)) * value.LineHeight
		if area.ScrollY < max(0, height-link.Content.Height)-1 || f.page.Offset+uint64(f.snapshot().Len()) >= f.page.Size {
			return
		}
		offset = f.page.Offset + uint64(f.snapshot().Len())
	}
	if err := m.requestFilePage(offset, backward); err != nil {
		return
	}
	// Returning to the preceding page keeps the viewport at its trailing edge.
	state.ConsumePointerWheel()
	markPencodeRootDirtyByModel(world, m)
}

func (m *workspaceModel) jumpFilePage(value string) {
	percent, err := strconv.ParseUint(value, 10, 8)
	f := m.activeFile()
	if err != nil || percent > 100 || f == nil || !f.page.Paged {
		return
	}
	offset := f.page.Size/100*percent + (f.page.Size%100)*percent/100
	if offset == f.page.Size {
		offset -= min(offset, protocol.TextPageSize)
	}
	if err := m.requestFilePage(offset, false); err != nil {
		m.setActiveStatus(err.Error())
	}
}
