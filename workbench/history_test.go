package workbench

import (
	"strings"
	"testing"
)

func checkPencodeHistory(t *testing.T, file *openFile) {
	t.Helper()
	size := 0
	for _, stack := range [][]pencodeEdit{file.undo, file.redo} {
		for _, edit := range stack {
			size += edit.bytes()
		}
		for _, edit := range stack[len(stack):cap(stack)] {
			if edit != (pencodeEdit{}) {
				t.Fatal("discarded history still holds strings")
			}
		}
	}
	if size != file.historyBytes || size > pencodeHistoryByteLimit || len(file.undo)+len(file.redo) > pencodeHistoryLimit {
		t.Fatalf("history budget: actual=%d charged=%d undo=%d redo=%d", size, file.historyBytes, len(file.undo), len(file.redo))
	}
}

func TestPencodeHistoryByteBudgetAndBranch(t *testing.T) {
	file := &openFile{Path: "notes.txt"}
	model := workspaceModel{files: map[string]*openFile{file.Path: file}, activePath: file.Path}
	chunk := strings.Repeat("a", pencodeHistoryByteLimit/3)
	for i := range 5 {
		model.updateActiveContent(strings.Repeat(string(rune('a'+i)), len(chunk)))
		checkPencodeHistory(t, file)
	}
	last := file.Content
	if !model.undoActiveContent(nil) {
		t.Fatal("budgeted latest edit could not be undone")
	}
	checkPencodeHistory(t, file)
	if !model.redoActiveContent(nil) || file.Content != last {
		t.Fatal("redo did not restore the latest value")
	}
	checkPencodeHistory(t, file)
	model.undoActiveContent(nil)
	model.updateActiveContent("branched")
	checkPencodeHistory(t, file)
	if len(file.redo) != 0 || model.redoActiveContent(nil) {
		t.Fatal("new edit retained the abandoned redo branch")
	}
	model.updateActiveContent(strings.Repeat("z", pencodeHistoryByteLimit+1))
	checkPencodeHistory(t, file)
	if len(file.undo) != 0 || model.undoActiveContent(nil) {
		t.Fatal("oversized edit left an undo gap")
	}
	model.updateActiveContent(file.Content + "!")
	if !model.undoActiveContent(nil) || strings.HasSuffix(file.Content, "!") {
		t.Fatal("history did not resume after an oversized edit")
	}
	checkPencodeHistory(t, file)
}

func TestPencodeHistoryCountBudget(t *testing.T) {
	var file openFile
	for i := range pencodeHistoryLimit * 2 {
		file.recordEdit(pencodeEdit{start: i, inserted: "x"})
		checkPencodeHistory(t, &file)
	}
	if len(file.undo) != pencodeHistoryLimit || file.undo[0].start != pencodeHistoryLimit {
		t.Fatal("history did not retain the newest edits")
	}
}
