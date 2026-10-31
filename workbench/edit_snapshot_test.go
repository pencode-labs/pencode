package workbench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diiyw/onix/input"
	engineui "github.com/diiyw/onix/ui"
)

func TestNativeMiddleEditKeepsSegmentedSnapshot(t *testing.T) {
	dir := t.TempDir()
	initial := strings.Repeat("let value = 42; // 中文🙂\r\n", 60000)
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", dir)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", "main.txt")
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	f := m.activeFile()
	before := f.snapshot()
	buffer := f.snapshotBuffer()
	middle := buffer.Runes() / 2
	start, end := buffer.ByteOffset(middle), buffer.ByteOffset(middle+1)
	if !w.Focus(m.editor.control) {
		t.Fatal("editor could not receive focus")
	}
	field := w.Mut[engineui.TextField](m.editor.control)
	field.Anchor, field.Caret = middle, middle+1
	state, _ := app.World.GetResource[*input.State]()
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "x"})
	app.RequestRender()
	languageInteractionStep(t, app)
	after := f.snapshot()
	if after == before || after.Len() != before.Len()-(end-start)+1 || after.Slice(start, start+1) != "x" {
		field = w.Mut[engineui.TextField](m.editor.control)
		changed, oldEnd, newEnd := before.ChangedRange(after)
		t.Fatalf("native snapshot event did not commit the edit: bytes=%d want=%d changed=%d:%d:%d want start=%d field=%d:%d fieldChanged=%v bufferChanged=%v focused=%v", after.Len(), before.Len()-(end-start)+1, changed, oldEnd, newEnd, start, field.Anchor, field.Caret, field.Text() != before, buffer.Snapshot() != before, w.FocusedControl())
	}
	if after.Materialized() {
		t.Fatal("native edit frame materialized the complete document")
	}
	if before.Slice(start, end) != initial[start:end] {
		t.Fatal("published base mutated")
	}
	if !m.undoActiveContent(w) || !f.snapshot().Equal(before) {
		t.Fatal("middle edit undo failed")
	}
	if !m.redoActiveContent(w) || !f.snapshot().Equal(after) {
		t.Fatal("middle edit redo failed")
	}
}

func TestMiddleEditSaveRetainsImmutableVersion(t *testing.T) {
	m, p, g, unblock := delayedFileModel(t)
	original := strings.Repeat("中🙂 line\r\n", 10000)
	resourceTestWrite(t, p, "main.txt", original)
	openProjectFile(p, "main.txt")
	m.syncActiveProjectFields()
	file := m.activeFile()
	buffer := file.snapshotBuffer()
	middle := buffer.Runes() / 2
	edit := func(text string) {
		t.Helper()
		next, _, _ := buffer.ApplyEdits(file.snapshot(), middle, middle+1, 0, true, []input.TextEdit{{Text: text}})
		if !m.applyFileSnapshot(file, p, next, true) {
			t.Fatal("edit was not committed")
		}
	}
	edit("A")
	saved := file.snapshot()
	g.pause.Store(true)
	if _, err := m.requestFileSave(p, file); err != nil {
		t.Fatal(err)
	}
	awaitFileGate(t, g)
	edit("B")
	latest := file.snapshot()
	if saved.Materialized() || latest.Materialized() {
		t.Fatal("save submission materialized document")
	}
	unblock()
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if saved.Materialized() || latest.Materialized() {
		t.Fatal("save or transport materialized document")
	}
	data, err := os.ReadFile(filepath.Join(p.Path, "main.txt"))
	if err != nil || !snapshotText(string(data)).Equal(saved) || !file.snapshot().Equal(latest) || !file.Dirty {
		t.Fatal("save lost its captured version or newer edits", err)
	}
	if !m.undoActiveContent(nil) || !file.snapshot().Equal(saved) || file.Dirty {
		t.Fatal("undo did not return to saved version")
	}
	if !m.redoActiveContent(nil) || !file.snapshot().Equal(latest) || !file.Dirty {
		t.Fatal("redo lost newer version")
	}
}
