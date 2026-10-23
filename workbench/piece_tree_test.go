package workbench

import (
	"strings"
	"testing"

	"github.com/diiyw/onix/input"
)

func TestPencodePieceTreeNativeEditHistory(t *testing.T) {
	file := &openFile{Path: "notes.txt", Name: "notes.txt", Content: "a界\r\nb", savedContent: "a界\r\nb"}
	model := workspaceModel{files: map[string]*openFile{file.Path: file}, activePath: file.Path}
	buffer := file.textBuffer(file.Content)
	value, caret, _ := buffer.ApplyTextEditsInRange(file.Content, 1, 2, 0, true, []input.TextEdit{{Text: "😀\n"}})
	if !model.updateActiveContent(value) || file.Content != "a😀\n\r\nb" || !file.Dirty {
		t.Fatalf("native content=%q dirty=%t", file.Content, file.Dirty)
	}
	if buffer != file.textBuffer(file.Content) || buffer.String() != file.Content || buffer.RetainedEditBytes() != len("😀\n") {
		t.Fatal("native edit rebuilt or duplicated the document")
	}
	if caret != 3 || len(file.undo) != 1 || file.undo[0].removed != "界" || file.undo[0].inserted != "😀\n" {
		t.Fatal("native delta history mismatch")
	}
	if !model.undoActiveContent(nil) || file.Content != file.savedContent || file.Dirty || buffer.String() != file.Content {
		t.Fatal("tree undo did not restore saved document")
	}
	if !model.redoActiveContent(nil) || file.Content != value || !file.Dirty || buffer.String() != value {
		t.Fatal("tree redo did not restore edited document")
	}
	model.undoActiveContent(nil)
	model.updateActiveContent("external\r\n更新\n")
	if buffer.String() != file.Content || model.redoActiveContent(nil) {
		t.Fatal("external snapshot did not update tree or discard redo")
	}
	if !model.undoActiveContent(nil) || file.Content != file.savedContent {
		t.Fatal("external snapshot undo failed")
	}
}

func TestPencodePieceTreeHistorySurvivesCompaction(t *testing.T) {
	initial := strings.Repeat("原文\r\n", 10000)
	file := &openFile{Path: "notes.txt", Name: "notes.txt", Content: initial, savedContent: initial}
	model := workspaceModel{files: map[string]*openFile{file.Path: file}, activePath: file.Path}
	model.updateActiveContent("small")
	if file.buffer.RetainedBytes() > 64<<10 {
		t.Fatal("deleted original retained")
	}
	if !model.undoActiveContent(nil) || file.Content != initial || !model.redoActiveContent(nil) || file.Content != "small" {
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
