package workbench

import (
	"net/url"
	"slices"
	"testing"
)

func TestSourceControlHistoryTree(t *testing.T) {
	m := &workspaceModel{}
	s := m.sourceState()
	s.SelectedHash, s.ExpandedCommit = "commit", 31
	s.CommitFiles = []sourceFile{
		{Path: "docs/manual/main.md", Index: 'A'},
		{Path: "docs/readme.md", Index: 'M'},
		{Path: "font/system.go", Index: 'M'},
		{Path: "projects/pencode/main.go", Index: 'M'},
		{Path: "root.go", Index: 'D'},
	}
	wantPaths := []string{"docs", "docs/manual", "docs/manual/main.md", "docs/readme.md", "font", "font/system.go", "projects", "projects/pencode", "projects/pencode/main.go", "root.go"}
	wantDepths := []int{0, 1, 2, 1, 0, 1, 0, 1, 2, 0}
	rows := s.historyTree()
	if len(rows) != len(wantPaths) {
		t.Fatalf("history tree has %d rows, want %d", len(rows), len(wantPaths))
	}
	for i, row := range rows {
		if row.Path != wantPaths[i] || row.Depth != wantDepths[i] {
			t.Fatalf("row %d: path=%q depth=%d", i, row.Path, row.Depth)
		}
	}
	if s.graphDetailsHeight() != 270 {
		t.Fatal("graph height must include every directory and file without an eight-row cap")
	}
	var view sourceVNodeView
	view.reset()
	m.sourceFileTree(&view, "history", "Commit files", s.CommitFiles, 30)
	if !slices.Contains(view.clicks, "sc-folder:history:docs/manual") || !slices.Contains(view.clicks, "sc-open:history:"+url.PathEscape("docs/manual/main.md")) {
		t.Fatal("history tree is missing folder toggles or file actions")
	}
	for _, group := range view.groups[:view.used] {
		if group.node.Tag == "h3" {
			t.Fatal("history files must not render a heading")
		}
		if group.node.Tag == "span" && group.node.Key == sourceVNodeKey("span", "name") && group.node.Text == "docs/manual/main.md" {
			t.Fatal("history tree file labels must use their basename")
		}
	}
	s.GraphScrollY = -float32(s.graphOffset(70))
	m.sourceAction("sc-folder:history:docs")
	if len(s.historyTree()) != 7 || s.graphDetailsHeight() != 189 || s.GraphScrollY != -float32(s.graphOffset(70)) || !s.RestoreGraph {
		t.Fatal("collapsing a directory did not resize the graph while preserving the visible commit")
	}
	m.sourceAction("sc-folder:history:docs")
	if len(s.historyTree()) != 10 || s.GraphScrollY != -float32(s.graphOffset(70)) {
		t.Fatal("expanding a directory did not restore all rows and preserve the visible commit")
	}
	if allocations := testing.AllocsPerRun(100, func() {
		_ = s.historyTree()
		_ = s.graphOffset(70)
		_ = s.graphCommitAt(2200)
	}); allocations != 0 {
		t.Fatalf("unchanged tree and graph sizing allocated %g times", allocations)
	}
}

func TestSourceControlHistoryTreeNestedCollapse(t *testing.T) {
	m := &workspaceModel{}
	s := m.sourceState()
	s.SelectedHash = "commit"
	s.CommitFiles = []sourceFile{
		{Path: "resources-extra/keep.go"},
		{Path: "resources/nested/a.go"},
		{Path: "resources/nested/b.go"},
		{Path: "resources/root.go"},
	}
	m.sourceAction("sc-folder:history:resources/nested")
	m.sourceAction("sc-folder:history:resources")
	rows := s.historyTree()
	if len(rows) != 3 || rows[0].Path != "resources-extra" || rows[1].Path != "resources-extra/keep.go" || rows[2].Path != "resources" {
		t.Fatal("collapsed directories hid a sibling with the same prefix or retained descendants")
	}
	m.sourceAction("sc-folder:history:resources")
	rows = s.historyTree()
	if len(rows) != 5 || rows[3].Path != "resources/nested" || rows[4].Path != "resources/root.go" {
		t.Fatal("reopening a parent lost the nested directory's collapsed state")
	}
	s.CommitFiles = []sourceFile{{Path: "new.go"}}
	if rows = s.historyTree(); len(rows) != 1 || rows[0].Path != "new.go" {
		t.Fatal("a newly loaded commit reused stale tree rows")
	}
}
