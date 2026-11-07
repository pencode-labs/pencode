package workbench

import (
	"strings"
	"testing"

	"github.com/onix-engine/onix/syntax"
	"github.com/onix-engine/onix/text/piecetree"
)

func TestFindCacheReleasesClosedDocument(t *testing.T) {
	for _, project := range []bool{false, true} {
		file := &openFile{Path: "large.txt", contentSnapshot: snapshotText(strings.Repeat("word ", 1<<20))}
		m := workspaceModel{files: map[string]*openFile{file.Path: file}, tabs: []string{file.Path}, activePath: file.Path, findQuery: "word", findOpen: true}
		m.refreshFindMatches()
		if len(m.findMatches) != pencodeFindMatchLimit {
			t.Fatal("fixture did not populate matches")
		}
		if project {
			p := &pencodeProject{Files: m.files, Tabs: m.tabs, ActivePath: file.Path}
			m.projects = []*pencodeProject{p}
			m.discardProjectTab(p, file.Path)
		} else {
			m.discardTab(file.Path)
		}
		if m.findContent != (piecetree.Snapshot{}) || len(m.findMatches) != 0 || cap(m.findMatches) != 0 || m.findScope.file != nil || m.findCachedScope.file != nil {
			t.Fatal("closing retained document search state")
		}
		m.closeFind(nil)
		if m.findQuery != "word" {
			t.Fatal("closing lost the query")
		}
	}
}

func TestHighlightResultBoundsDiffStorage(t *testing.T) {
	base := strings.Repeat("line\n", 100000)
	var result pencodeHighlightResult
	alignSourceDiffInto(&result.diffView, &result.diffInline, base, "added\n"+base, "@@ -0,0 +1 @@\n")
	result.diffInline.compare(strings.Repeat("a", 510), strings.Repeat("b", 510))
	resetPencodeHighlightResult(&result)
	if cap(result.diffView.Rows) != 0 || cap(result.diffView.Layouts[0].Offsets) != 0 || cap(result.diffInline.matches) != 0 {
		t.Fatal("large comparison retained reusable storage")
	}
	alignSourceDiffInto(&result.diffView, &result.diffInline, "one\n", "two\n", "@@ -1 +1 @@\n")
	resetPencodeHighlightResult(&result)
	if allocs := testing.AllocsPerRun(100, func() {
		alignSourceDiffInto(&result.diffView, &result.diffInline, "one\n", "two\n", "@@ -1 +1 @@\n")
		resetPencodeHighlightResult(&result)
	}); allocs != 0 {
		t.Fatalf("warm comparison allocated %g", allocs)
	}
}

func TestHighlightAndFindKeepSnapshotUnmaterialized(t *testing.T) {
	base := "package demo\n" + strings.Repeat("var value = 42 // 中🙂\n", 10000)
	snapshot := snapshotText(base).Replace(strings.Index(base, "value"), strings.Index(base, "value")+5, "other")
	file := &openFile{Path: "main.go", contentSnapshot: snapshot}
	var document syntax.Document
	defer document.Close()
	theme, _ := syntax.BuiltinTheme("monokai")
	var result pencodeHighlightResult
	processPencodeHighlight(&document, pencodeHighlightJob{file: file, path: file.Path, documentText: snapshot, theme: theme}, &result)
	if result.err != nil || len(result.spans) == 0 {
		t.Fatal("snapshot highlighting failed", result.err)
	}
	m := workspaceModel{files: map[string]*openFile{file.Path: file}, activePath: file.Path, findQuery: "value"}
	m.refreshFindMatches()
	if len(m.findMatches) != pencodeFindMatchLimit || snapshot.Materialized() {
		t.Fatal("highlight or find materialized the document")
	}
}
