package workbench

import (
	"fmt"

	"github.com/pencode-labs/pencode/protocol"
)

func (f *openFile) text() bool {
	return f != nil && !f.unsupported && (f.Name == "" || isTextFile(f.Name) || f.forcedText || f.editorID == pencodeTextEditor)
}

func (f *openFile) openMode() protocol.DocumentMode {
	if f.forcedText || f.editorID == pencodeTextEditor {
		return protocol.DocumentTextForced
	}
	if f.text() {
		return protocol.DocumentText
	}
	return protocol.DocumentFile
}

func restoredFileMode(tab pencodeTabState) protocol.DocumentMode {
	if tab.ForcedText || tab.Editor == pencodeTextEditor {
		return protocol.DocumentTextForced
	}
	return protocol.FileMode(tab.Path)
}

// Reopening is a worker job bound to the original file, so a tab switch cannot
// apply the loaded bytes to a different document or overwrite intervening edits.
func (m *workspaceModel) requestTextEditor(f *openFile) error {
	if m.sourceState().visible() {
		return fmt.Errorf("%s", m.tr("请先切换到文件标签"))
	}
	p := m.activeProjectState()
	if m.fileIOMutating(p) {
		return fmt.Errorf("%s", m.tr("请等待文件操作完成"))
	}
	if m.fileIO != nil {
		for _, j := range m.fileIO.pending {
			if j.file == f && (j.kind == fileReopenText || j.kind == fileReload || j.kind == fileSave) {
				return fmt.Errorf("%s", m.tr("请等待文件操作完成"))
			}
		}
	}
	j := &fileIOJob{kind: fileReopenText, mode: protocol.DocumentTextForced, project: p, file: f, relative: f.Path, root: m.projectPath, baseline: f.snapshot()}
	if p != nil {
		j.workspace, j.root = p.workspace.ID, p.Path
	}
	if err := m.submitFileIO(j); err != nil {
		return err
	}
	m.setProjectStatus(p, m.tr("正在使用代码编辑器打开 ")+f.Name+"…")
	return nil
}

func (m *workspaceModel) acceptTextEditor(r *fileIOResult) {
	j, f := r.job, r.job.file
	if j.ctx.Err() != nil || !m.fileLive(j.project, j.relative, f) {
		return
	}
	if r.err != nil {
		m.setProjectStatus(j.project, m.tr("打开文件失败：")+r.err.Error())
		return
	}
	if f.Dirty || !f.snapshot().Equal(j.baseline) {
		return
	}
	f.closeDocument()
	f.document, r.document = r.document, nil
	f.initPage()
	f.contentSnapshot, f.savedContent = snapshotText(r.content), snapshotText(r.content)
	f.unsupported, f.editorSplit, f.forcedText = false, false, true
	f.editorID, f.editorGeneration = pencodeTextEditor, 0
	f.pencodeEditHistory = pencodeEditHistory{}
	m.highlightCache.remove(f)
	if m.activeFile() == f {
		m.findOpen, m.replaceOpen = false, false
		m.clearFindCache()
		m.resetHighlight()
	}
	m.setProjectStatus(j.project, "")
}
