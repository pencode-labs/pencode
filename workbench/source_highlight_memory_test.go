package workbench

import (
	"slices"
	"testing"

	"github.com/diiyw/onix/syntax"
)

type snapshotHighlightDocument struct {
	syntax.Document
	releases int
	hadTree  bool
}

func (d *snapshotHighlightDocument) Update(path, source string) error {
	d.hadTree = d.Tree() != nil
	return d.Document.Update(path, source)
}

func (d *snapshotHighlightDocument) Release(trim bool) {
	d.releases++
	d.Document.Release(trim)
}

func TestSourceSnapshotHighlightReleasesParser(t *testing.T) {
	theme, _ := syntax.BuiltinTheme("monokai")
	job := pencodeHighlightJob{path: "main.go", source: "package demo\nvar Value = 42\n", theme: theme}
	var document snapshotHighlightDocument
	defer document.Close()
	var result pencodeHighlightResult
	processPencodeHighlight(&document, job, &result)
	if document.Tree() == nil {
		t.Fatal("editable document lost its incremental tree")
	}
	expected := slices.Clone(result.spans)
	resetPencodeHighlightResult(&result)
	job.snapshot, job.sourceDoc = true, &sourceHighlight{}
	processPencodeHighlight(&document, job, &result)
	if document.hadTree || document.Tree() != nil || document.releases != 2 {
		t.Fatal("snapshot borrowed or retained a parser tree")
	}
	if len(expected) == 0 || !slices.Equal(expected, result.spans) || len(result.sourceStyles) != 0 || len(result.lineStarts) != 0 {
		t.Fatal("snapshot lost paint or built unused HTML line styles")
	}
	resetPencodeHighlightResult(&result)
	job.snapshot, job.lineStyles = false, true
	processPencodeHighlight(&document, job, &result)
	if document.Tree() == nil || len(result.spans) != len(result.sourceStyles) || len(result.lineStarts) != 3 {
		t.Fatal("merge/editor did not restore incremental parsing and HTML paint")
	}
}

func TestSourceDiffReusesFilePaintWithoutSkippingAlignment(t *testing.T) {
	const source = "package demo\nvar Value = 1\n"
	file := &openFile{Path: "main.go", Content: source}
	m := workspaceModel{files: map[string]*openFile{file.Path: file}, activePath: file.Path}
	defer m.closeHighlightWorker()
	settlePencodeHighlight(t, &m)
	tab := &sourceTab{Key: "changes", Kind: "changes", Path: file.Path, Local: source, Result: source, DiffContent: source}
	m.sourceControl.Tabs, m.sourceControl.Active = []*sourceTab{tab}, tab.Key
	renderPencodeHighlight(&m)
	cached, ok := m.highlightCache.get(file, m.syntaxTheme, m.syntaxDefinitions)
	if !ok || m.highlightRequest.sourceDoc != nil {
		t.Fatal("cached comparison submitted a parser job")
	}
	for side := range 2 {
		h := &tab.Highlight[side]
		if !h.ready || h.source != source || !slices.Equal(h.spans, cached.spans) || &h.spans[0] == &cached.spans[0] {
			t.Fatal("comparison did not copy cached paint into owned storage")
		}
	}
	if allocations := testing.AllocsPerRun(100, func() { renderPencodeHighlight(&m); m.pollHighlight() }); allocations != 0 {
		t.Fatalf("cached comparison allocated %g times", allocations)
	}
	// Matching cached text cannot replace the diff job after an edit: aligned
	// rows and revert-block offsets must still be rebuilt for the new content.
	tab.editResult(source + "var Added = 2\n")
	file.Content = tab.Result
	m.highlightCache.put(pencodeHighlightCacheEntry{file: file, source: file.Content, theme: m.syntaxTheme, definitions: m.syntaxDefinitions, spans: cached.spans})
	m.renderSourceHighlight()
	if !m.highlightRequest.diff || m.highlightRequest.snapshot {
		t.Fatal("file paint cache bypassed alignment or discarded the editable parser")
	}
	settleSourceHighlight(t, &m)
	if tab.DiffContent != tab.Result || len(tab.Blocks) != 1 {
		t.Fatal("cached edited text retained stale diff geometry")
	}
	m.syntaxTheme, _ = syntax.BuiltinTheme("light-plus")
	m.renderSourceHighlight()
	if m.highlightRequest.sourceDoc != &tab.Highlight[0] || !m.highlightRequest.snapshot {
		t.Fatal("theme change reused stale cached paint")
	}
	settleSourceHighlight(t, &m)
}
