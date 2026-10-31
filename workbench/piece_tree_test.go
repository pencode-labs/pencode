package workbench

import (
	"strings"
	"testing"

	"github.com/diiyw/onix/input"
)

func TestPencodePieceTreeNativeEditHistory(t *testing.T) {
	file := &openFile{Path: "notes.txt", Name: "notes.txt", contentSnapshot: snapshotText("a界\r\nb"), savedContent: snapshotText("a界\r\nb")}
	model := workspaceModel{files: map[string]*openFile{file.Path: file}, activePath: file.Path}
	buffer := file.textBuffer(file.content())
	value, caret, _ := buffer.ApplyTextEditsInRange(file.content(), 1, 2, 0, true, []input.TextEdit{{Text: "😀\n"}})
	if !model.updateActiveContent(value) || file.content() != "a😀\n\r\nb" || !file.Dirty {
		t.Fatalf("native content=%q dirty=%t", file.content(), file.Dirty)
	}
	if buffer != file.textBuffer(file.content()) || buffer.String() != file.content() || buffer.RetainedEditBytes() != len("😀\n") {
		t.Fatal("native edit rebuilt or duplicated the document")
	}
	if caret != 3 || len(file.undo) != 1 || file.undo[0].removed != "界" || file.undo[0].inserted != "😀\n" {
		t.Fatal("native delta history mismatch")
	}
	if !model.undoActiveContent(nil) || !file.snapshot().Equal(file.savedContent) || file.Dirty || buffer.String() != file.content() {
		t.Fatal("tree undo did not restore saved document")
	}
	if !model.redoActiveContent(nil) || file.content() != value || !file.Dirty || buffer.String() != value {
		t.Fatal("tree redo did not restore edited document")
	}
	model.undoActiveContent(nil)
	model.updateActiveContent("external\r\n更新\n")
	if buffer.String() != file.content() || model.redoActiveContent(nil) {
		t.Fatal("external snapshot did not update tree or discard redo")
	}
	if !model.undoActiveContent(nil) || !file.snapshot().Equal(file.savedContent) {
		t.Fatal("external snapshot undo failed")
	}
}

func TestPencodePieceTreeHistorySurvivesCompaction(t *testing.T) {
	initial := strings.Repeat("原文\r\n", 10000)
	file := &openFile{Path: "notes.txt", Name: "notes.txt", contentSnapshot: snapshotText(initial), savedContent: snapshotText(initial)}
	model := workspaceModel{files: map[string]*openFile{file.Path: file}, activePath: file.Path}
	model.updateActiveContent("small")
	if file.buffer.RetainedBytes() > 64<<10 {
		t.Fatal("deleted original retained")
	}
	if !model.undoActiveContent(nil) || file.content() != initial || !model.redoActiveContent(nil) || file.content() != "small" {
		t.Fatal("compaction broke delta history")
	}
	checkPencodeHistory(t, file)
}

func TestSourcePieceTreeNativeEditHistory(t *testing.T) {
	tab := sourceTab{Kind: "changes", Result: "a界\r\nb"}
	buffer := tab.textBuffer(tab.Result)
	next, _, _ := buffer.ApplyTextEditsInRange(tab.Result, 1, 2, 0, true, []input.TextEdit{{Text: "😀"}})
	tab.editResult(next)
	if tab.Result != "a😀\r\nb" || buffer.String() != tab.Result || buffer.RetainedEditBytes() != len("😀") {
		t.Fatal("comparison edit rebuilt its document")
	}
	if !tab.undoResult(false) || tab.Result != "a界\r\nb" || !tab.undoResult(true) || tab.Result != next {
		t.Fatal("comparison tree history failed")
	}
	tab.editResult("merge\n结果")
	if buffer.String() != tab.Result || !tab.undoResult(false) || tab.Result != next {
		t.Fatal("external comparison edit/history diverged")
	}
}
