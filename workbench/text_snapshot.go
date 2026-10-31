package workbench

import (
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/text/piecetree"
)

func snapshotText(value string) piecetree.Snapshot { return piecetree.FromString(value) }
func (f *openFile) content() string                { return f.contentSnapshot.String() }
func (f *openFile) snapshot() piecetree.Snapshot   { return f.contentSnapshot }
func (m *workspaceModel) activeSnapshot() piecetree.Snapshot {
	if f := m.activeFile(); f != nil {
		return f.snapshot()
	}
	return piecetree.Snapshot{}
}

func (f *openFile) snapshotBuffer() *input.TextBuffer {
	f.buffer.SyncSnapshot(f.snapshot())
	return &f.buffer
}
