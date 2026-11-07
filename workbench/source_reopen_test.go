package workbench

import (
	"net/url"
	"testing"
	"time"

	"github.com/onix-engine/onix/scene"
)

func TestSourceDiffReopenRevalidatesContentsAndRetainsPaint(t *testing.T) {
	r := sourceTestRepository(t)
	const name = "space name.go"
	const base = "package demo\nvar Value = 1\n"
	const modified = "package demo\nvar Value = 2\n"
	sourceTestWrite(t, r, name, base)
	sourceTestGit(t, r, "add", "--all")
	sourceTestGit(t, r, "commit", "-m", "base")
	sourceTestWrite(t, r, name, modified)
	m := &workspaceModel{backend: r.Backend, projectPath: r.Root}
	s := m.sourceState()
	t.Cleanup(s.stop)
	t.Cleanup(m.closeHighlightWorker)
	s.Snapshot = sourceTestSnapshot(t, r)
	reopen := func(kind string) *sourceTab {
		t.Helper()
		m.sourceAction("sc-open:" + kind + ":" + url.PathEscape(name))
		deadline := time.Now().Add(5 * time.Second)
		for s.Done != nil && time.Now().Before(deadline) {
			m.pollSourceResult(s, nil)
			time.Sleep(time.Millisecond)
		}
		if s.Done != nil || s.Notice != "" || s.activeTab() == nil {
			t.Fatalf("reopen failed: busy=%v notice=%q", s.Done != nil, s.Notice)
		}
		return s.activeTab()
	}
	tab := reopen("changes")
	settleSourceHighlight(t, m)
	leftPaint, rightPaint := &tab.Highlight[0].spans[0], &tab.Highlight[1].spans[0]
	rows := &tab.Rows[0]
	tab.ScrollY = -20
	for range 3 {
		if got := reopen("changes"); got != tab || &got.Rows[0] != rows ||
			&got.Highlight[0].spans[0] != leftPaint || &got.Highlight[1].spans[0] != rightPaint || got.ScrollY != -20 {
			t.Fatal("unchanged comparison discarded its paint, rows or view")
		}
	}
	// The status remains M: comparing status flags alone would miss this edit.
	const external = "package demo\nvar Value = 3\n"
	sourceTestWrite(t, r, name, external)
	if got := reopen("changes"); got == tab || got.Result != external || got.Local != base {
		t.Fatal("reopen missed an external working-tree edit")
	}
	tab = s.activeTab()
	// Change only the index while preserving the same working-tree contents.
	sourceTestWrite(t, r, name, modified)
	sourceTestGit(t, r, "add", "--all")
	sourceTestWrite(t, r, name, external)
	if got := reopen("changes"); got == tab || got.Local != modified || got.Result != external {
		t.Fatal("reopen retained a stale index baseline")
	}
	tab = reopen("staged")
	sourceTestGit(t, r, "commit", "-m", "new baseline")
	sourceTestWrite(t, r, name, external)
	sourceTestGit(t, r, "add", "--all")
	if got := reopen("staged"); got == tab || got.Local != modified || got.Incoming != external {
		t.Fatal("staged reopen retained stale HEAD or index contents")
	}
	// Draft comparisons must keep their edits even when the disk changes.
	tab = reopen("changes")
	tab.editResult("package draft\n")
	sourceTestWrite(t, r, name, base)
	if got := reopen("changes"); got != tab || got.Result != "package draft\n" {
		t.Fatal("reopen replaced unsaved comparison edits")
	}
}

func TestSourceDiffCloseDropsDocumentReferences(t *testing.T) {
	tab := &sourceTab{Key: "changes::file.go", Kind: "changes", Local: "before", Result: "after", Saved: "after"}
	m := &workspaceModel{}
	s := m.sourceState()
	s.Tabs, s.Active, s.gutterTab, s.diffPaneTab = []*sourceTab{tab}, tab.Key, tab, tab
	for side := range s.diffEditors {
		s.diffEditors[side].caretContent = snapshotText(tab.sourceText(side))
		s.inlinePaint[side].key.tab = tab
	}
	m.closeSourceTab(tab.Key)
	if len(s.Tabs) != 0 || s.Active != "" || s.gutterTab != nil || s.diffPaneTab != nil {
		t.Fatal("closed comparison remains referenced")
	}
	for side := range s.diffEditors {
		if s.diffEditors[side].caretContent.Len() != 0 || s.inlinePaint[side].key.tab != nil {
			t.Fatal("closed comparison retained native editor paint state")
		}
	}
}

func TestSourceDiffUnchangedResultDoesNotAllocate(t *testing.T) {
	tab := &sourceTab{Key: "changes::file.go", Kind: "changes", Local: "before", Incoming: "after", Result: "after", Saved: "after", Disk: "after"}
	tab.Highlight[0].spans = []scene.TextSpan2D{{End: 1}}
	next := *tab
	next.Highlight = [3]sourceHighlight{}
	m := &workspaceModel{}
	s := m.sourceState()
	s.Tabs, s.Done = []*sourceTab{tab}, make(chan sourceResult, 1)
	done := s.Done
	if n := testing.AllocsPerRun(100, func() {
		s.Done = done
		done <- sourceResult{Tab: &next}
		m.pollSourceResult(s, nil)
	}); n != 0 {
		t.Fatalf("accepting unchanged comparison allocated %g times", n)
	}
	if s.activeTab() != tab || len(tab.Highlight[0].spans) == 0 {
		t.Fatal("unchanged result replaced cached paint")
	}
}
