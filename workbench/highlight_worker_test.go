package workbench

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
)

func settlePencodeHighlight(t testing.TB, model *workspaceModel) {
	t.Helper()
	if model.backend == nil {
		model.backend = newPencodeBackend(t)
	}
	model.highlightPending = false
	renderPencodeHighlight(model)
	deadline := time.Now().Add(10 * time.Second)
	for !model.highlighted || model.highlightRequest.file != nil {
		model.pollHighlight()
		if time.Now().After(deadline) {
			t.Fatal("background highlighting did not complete")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPencodeHighlightRejectsObsoleteSnapshots(t *testing.T) {
	file := &openFile{Path: "main.go", Name: "main.go", contentSnapshot: snapshotText("package demo\n// 中文\n")}
	model := workspaceModel{backend: newPencodeBackend(t), files: map[string]*openFile{file.Path: file}, activePath: file.Path}
	defer model.closeHighlightWorker()
	model.ensureSyntaxTheme()
	entered, release := make(chan struct{}, 1), make(chan struct{})
	published := make(chan struct{}, 1)
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	model.highlightWorker = newLatestWorker(func(job pencodeHighlightJob, result *pencodeHighlightResult) bool {
		entered <- struct{}{}
		<-release
		result.job = job
		result.lineInfo = "obsolete"
		return true
	}, resetPencodeHighlightResult, nil, func() { published <- struct{}{} })
	renderPencodeHighlight(&model)
	waitPencodeScanSignal(t, entered)
	// Mutating and even clearing the file while parsing must not race: the
	// worker owns strings, and only compares the file pointer's identity.
	file.contentSnapshot = snapshotText("newer content")
	job := model.highlightRequest
	unblock()
	waitPencodeScanSignal(t, published)
	if model.pollHighlight() || model.lineInfo == "obsolete" {
		t.Fatal("obsolete content was accepted")
	}
	// Publish a known result synchronously to exercise every invalidation key.
	for _, invalidate := range []func(){
		func() { file.contentSnapshot = snapshotText("newer content") },
		func() { model.highlightRevision++ },
		func() { model.syntaxTheme = &syntax.Theme{} },
		func() { model.syntaxDefinitions = &syntax.Definitions{} },
		func() { model.files[file.Path] = &openFile{Path: file.Path, contentSnapshot: snapshotText(job.source)} },
		func() { *file = openFile{} },
	} {
		*file = openFile{Path: job.path, Name: job.path, contentSnapshot: snapshotText(job.source)}
		model.files[job.path] = file
		model.highlightRevision, model.syntaxTheme, model.syntaxDefinitions = job.revision, job.theme, job.definitions
		invalidate()
		model.highlightWorker.mu.Lock()
		model.highlightWorker.result = pencodeHighlightResult{job: job, lineInfo: "obsolete"}
		model.highlightWorker.ready = true
		model.highlightWorker.mu.Unlock()
		if model.pollHighlight() || model.lineInfo == "obsolete" {
			t.Fatal("obsolete source, theme, file or generation was accepted")
		}
	}
}

func TestPencodeBackgroundHighlightMatchesParser(t *testing.T) {
	file := &openFile{Path: "main.go", Name: "main.go", contentSnapshot: snapshotText("package demo\n// 中文😀\nvar Value = 42\n")}
	model := workspaceModel{files: map[string]*openFile{file.Path: file}, activePath: file.Path}
	defer model.closeHighlightWorker()
	settlePencodeHighlight(t, &model)
	var document syntax.Document
	defer document.Close()
	if err := document.Update(file.Path, file.content()); err != nil {
		t.Fatal(err)
	}
	var expected []scene.TextSpan2D
	for _, token := range document.Tokens(model.syntaxTheme, model.syntaxDefinitions) {
		expected = appendPencodeSpan(expected, scene.TextSpan2D{Start: token.Start, End: token.End, Color: pencodeSyntaxColor(token.Style.Foreground)})
	}
	if !slices.Equal(model.highlightScratch, expected) || model.characterCount != utf8.RuneCountInString(file.content()) || model.lineCount != strings.Count(file.content(), "\n")+1 {
		t.Fatal("background paint or Unicode document counts differ from parser")
	}
	if allocs := testing.AllocsPerRun(100, func() { renderPencodeHighlight(&model); model.pollHighlight() }); allocs != 0 {
		t.Fatalf("idle highlight submission/polling allocated %g times", allocs)
	}
}

func TestPencodeHighlightUndoDuringInFlightParseCanResubmit(t *testing.T) {
	const source = "package demo\nvar Value = 1\n"
	file := &openFile{Path: "main.go", Name: "main.go", contentSnapshot: snapshotText(source)}
	model := workspaceModel{backend: newPencodeBackend(t), files: map[string]*openFile{file.Path: file}, activePath: file.Path}
	defer model.closeHighlightWorker()
	entered, release, published := make(chan struct{}), make(chan struct{}), make(chan struct{}, 2)
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	var started sync.Once
	document := new(syntax.Document)
	model.highlightWorker = newLatestWorker(func(job pencodeHighlightJob, result *pencodeHighlightResult) bool {
		started.Do(func() { close(entered) })
		<-release
		return processPencodeHighlight(document, job, result)
	}, resetPencodeHighlightResult, document.Close, func() { published <- struct{}{} })
	renderPencodeHighlight(&model)
	waitPencodeScanSignal(t, entered)
	model.updateActiveContent(source + "// typing\n")
	model.updateActiveContent(source) // Undo back to the in-flight snapshot.
	unblock()
	waitPencodeScanSignal(t, published)
	if model.pollHighlight() || model.highlightRequest.file != nil {
		t.Fatal("result during debounce was accepted or left its request stuck")
	}
	model.highlightPending = false
	renderPencodeHighlight(&model)
	waitPencodeScanSignal(t, published)
	if !model.pollHighlight() || !model.highlighted || model.highlightedContent.String() != source || len(model.highlightScratch) == 0 {
		t.Fatal("undo prevented resubmission of the same source")
	}
}

// Hold the parser behind a gate to measure foreground submission independently
// of grammar speed. Alternating snapshots must stay bounded and allocate zero.
func BenchmarkPencodeHighlightForegroundSubmission(b *testing.B) {
	file := &openFile{Path: "main.go", Name: "main.go"}
	model := workspaceModel{backend: newPencodeBackend(b), files: map[string]*openFile{file.Path: file}, activePath: file.Path}
	defer model.closeHighlightWorker()
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	var started sync.Once
	model.highlightWorker = newLatestWorker(func(job pencodeHighlightJob, result *pencodeHighlightResult) bool {
		started.Do(func() { close(entered) })
		<-release
		return false
	}, resetPencodeHighlightResult, nil, nil)
	renderPencodeHighlight(&model)
	<-entered
	sources := [...]string{"package demo\nvar A = 1\n", "package demo\nvar B = 2\n"}
	index := 0
	b.ReportAllocs()
	for b.Loop() {
		file.contentSnapshot = snapshotText(sources[index])
		index ^= 1
		renderPencodeHighlight(&model)
	}
}
