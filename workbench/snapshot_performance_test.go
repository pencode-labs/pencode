package workbench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diiyw/onix/input"
)

func TestNativeTailSnapshotsPreserveUndoAndSave(t *testing.T) {
	m, p, g, unblock := delayedFileModel(t)
	original := strings.Repeat("中🙂 line\n", 10000)
	resourceTestWrite(t, p, "main.txt", original)
	openProjectFile(p, "main.txt")
	m.syncActiveProjectFields()
	file := m.activeFile()
	buffer := file.textBuffer(file.Content)
	appendText := func(text string) {
		t.Helper()
		next, _, _ := buffer.ApplyTextEditsInRange(file.Content, buffer.Runes(), buffer.Runes(), 0, true, []input.TextEdit{{Text: text}})
		if !m.updateActiveContent(next) {
			t.Fatal("edit was not committed")
		}
	}
	appendText("first")
	saved := file.Content
	g.pause.Store(true)
	if _, err := m.requestFileSave(p, file); err != nil {
		t.Fatal(err)
	}
	awaitFileGate(t, g)
	appendText("second")
	latest := file.Content
	if !m.undoActiveContent(nil) || file.Content != saved {
		t.Fatal("tail undo failed")
	}
	if !m.redoActiveContent(nil) || file.Content != latest {
		t.Fatal("tail redo failed")
	}
	unblock()
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(filepath.Join(p.Path, "main.txt"))
	if err != nil || string(disk) != saved || saved != original+"first" || latest != saved+"second" || !file.Dirty {
		t.Fatal("save or undo changed an older snapshot", err)
	}
}

func BenchmarkNativeTailEditFrame(b *testing.B) {
	for _, mode := range []string{"append", "delete"} {
		b.Run(mode, func(b *testing.B) {
			dir := b.TempDir()
			line := "let value = 42; // text line\n"
			initial := strings.Repeat(line, (2<<20)/len(line))
			if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte(initial), 0644); err != nil {
				b.Fatal(err)
			}
			b.Setenv("ONIX_PENCODE_PROFILE_PROJECT", dir)
			b.Setenv("ONIX_PENCODE_PROFILE_FILE", "main.txt")
			app, session := newPencodeTestApp(b)
			m := session.active.model
			file := m.activeFile()
			buffer := file.textBuffer(file.Content)
			edits := [1]input.TextEdit{{Text: "x"}}
			if mode == "delete" {
				edits[0] = input.TextEdit{Command: input.TextCommandBackspace}
			}
			editFrame := func() {
				end := buffer.Runes()
				if end == 0 {
					b.Fatal("benchmark exhausted document")
				}
				value, caret, _ := buffer.ApplyTextEditsInRange(file.Content, end, end, 0, true, edits[:])
				m.updateActiveContent(value)
				m.setEditorValueAndCaret(m.fontResources, value, caret)
				m.refreshRegion(m.fontResources, pencodeRefreshEditor)
				app.RequestRender()
				if _, err := app.Step(); err != nil {
					b.Fatal(err)
				}
			}
			for range 3 {
				editFrame()
			}
			b.ReportAllocs()
			for b.Loop() {
				editFrame()
			}
		})
	}
}
