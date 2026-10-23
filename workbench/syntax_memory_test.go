package workbench

import (
	"slices"
	"testing"
	"time"

	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
)

func TestPencodeIdleParserReleaseAndReparse(t *testing.T) {
	file := &openFile{Path: "main.go", Content: "package demo\nvar Before = 1\n"}
	model := workspaceModel{files: map[string]*openFile{file.Path: file}, tabs: []string{file.Path}, activePath: file.Path}
	defer model.closeHighlightWorker()
	settlePencodeHighlight(t, &model)
	spans := slices.Clone(model.highlightScratch)
	revision := model.highlightRevision
	model.releaseIdleSyntaxMemory(model.syntaxMemoryDue)
	if model.highlightRevision <= revision || !slices.Equal(model.highlightScratch, spans) {
		t.Fatal("idle maintenance did not release the worker or lost paint spans")
	}
	if allocs := testing.AllocsPerRun(50, func() {
		model.highlighted = false
		renderPencodeHighlight(&model)
	}); allocs != 0 || model.highlightRequest.file != nil {
		t.Fatal("cached highlighting after idle parsed or allocated")
	}
	model.updateActiveContent("package demo\nfunc After() string { return \"edited\" }\n")
	model.highlighted = false
	settlePencodeHighlight(t, &model)
	var document syntax.Document
	defer document.Close()
	if err := document.Update(file.Path, file.Content); err != nil {
		t.Fatal(err)
	}
	var expected []scene.TextSpan2D
	for _, token := range document.Tokens(model.syntaxTheme, model.syntaxDefinitions) {
		expected = appendPencodeSpan(expected, scene.TextSpan2D{Start: token.Start, End: token.End, Color: pencodeSyntaxColor(token.Style.Foreground)})
	}
	if !slices.Equal(model.highlightScratch, expected) || slices.Equal(model.highlightScratch, spans) {
		t.Fatal("editing after idle reused stale paint spans")
	}
}

func TestPencodeIdleParserKeepsUncachedOrPendingDocument(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "uncached", true: "pending"}[pending], func(t *testing.T) {
			file := &openFile{Path: "main.go", Content: "package demo\nvar Value = 1\n"}
			model := workspaceModel{files: map[string]*openFile{file.Path: file}, tabs: []string{file.Path}, activePath: file.Path}
			defer model.closeHighlightWorker()
			settlePencodeHighlight(t, &model)
			revision := model.highlightRevision
			if pending {
				model.highlightPending = true
			} else {
				model.highlightCache.invalidate(file)
			}
			model.releaseIdleSyntaxMemory(model.syntaxMemoryDue.Add(time.Second))
			if model.highlightRevision != revision {
				t.Fatal("idle maintenance discarded a document without a usable paint cache")
			}
		})
	}
}

func TestPencodeSyntaxWorkerRecolorsWithoutReparseAndReleasesTree(t *testing.T) {
	var document syntax.Document
	defer document.Close()
	theme, _ := syntax.BuiltinTheme("monokai")
	job := pencodeHighlightJob{path: "main.go", source: "package demo\n// comment\n", theme: theme}
	var result pencodeHighlightResult
	processPencodeHighlight(&document, job, &result)
	tree := document.Tree()
	spans := slices.Clone(result.spans)
	resetPencodeHighlightResult(&result)
	job.theme, _ = syntax.BuiltinTheme("light-plus")
	processPencodeHighlight(&document, job, &result)
	if tree == nil || document.Tree() != tree || slices.Equal(spans, result.spans) {
		t.Fatal("worker reparsed the AST or failed to recolor")
	}
	resetPencodeHighlightResult(&result)
	if processPencodeHighlight(&document, pencodeHighlightJob{release: true}, &result) || document.Tree() != nil || result.spans != nil {
		t.Fatal("idle job retained its tree/buffer or published a render result")
	}
}
