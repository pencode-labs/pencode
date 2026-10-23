package workbench

import (
	"net/url"
	"slices"
	"strconv"
	"testing"

	"github.com/diiyw/onix/ui/html"
)

func TestSourceControlRenderCache(t *testing.T) {
	m := &workspaceModel{}
	m.bottom.active = pencodePanelSourceControl
	s := m.sourceState()
	s.Snapshot.Files = []sourceFile{{Path: "resources/main.go", Work: 'M'}}
	s.Snapshot.Groups[2] = s.Snapshot.Files
	s.Snapshot.Branch = "main"
	s.Snapshot.Commits = []sourceCommit{{Hash: "0123456789abcdef", Subject: "Base"}}
	render := func() {
		m.renderSourceControl(&s.panelView)
		m.renderSourceToolbar(&s.toolsView)
	}
	render()
	if n := testing.AllocsPerRun(100, render); n != 0 {
		t.Fatalf("unchanged Git views allocated %g times", n)
	}
	if slices.Contains(s.panelView.clicks, "sc-menu:0123456789abcdef") {
		t.Fatal("graph retained a per-commit menu button")
	}
	m.fileIcons = &pencodeFileIcons{folder: "extension:folder.svg", suffixes: map[string]string{".go": "extension:go.svg"}}
	render()
	folderIcon, fileIcon, arrowIcon := false, false, false
	for _, group := range s.panelView.groups {
		if group.node.Tag != "img" {
			continue
		}
		for _, attr := range group.attrs {
			if attr.Name == "src" {
				folderIcon = folderIcon || attr.Value == "extension:folder.svg"
				fileIcon = fileIcon || attr.Value == "extension:go.svg"
				arrowIcon = arrowIcon || attr.Value == "icons/chevron-down.svg"
			}
		}
	}
	if !folderIcon || !fileIcon || !arrowIcon {
		t.Fatal("file icon theme change did not refresh icons or changed the tree arrows")
	}
	if n := testing.AllocsPerRun(100, render); n != 0 {
		t.Fatalf("unchanged themed Git views allocated %g times", n)
	}
	stage := "sc-stage:" + url.PathEscape("resources/main.go")
	if !slices.Contains(s.panelView.clicks, stage) {
		t.Fatal("missing working tree action")
	}
	m.sourceAction("sc-group:changes")
	render()
	if slices.Contains(s.panelView.clicks, stage) || !slices.Contains(s.panelView.clicks, "sc-group:changes") {
		t.Fatal("collapsed changes group retained its children or lost its toggle")
	}
	m.sourceAction("sc-group:changes")
	render()
	if !slices.Contains(s.panelView.clicks, stage) {
		t.Fatal("expanded changes group did not restore its files")
	}
	m.sourceAction("sc-folder:changes:resources")
	render()
	if slices.Contains(s.panelView.clicks, stage) {
		t.Fatal("collapsed folder retained its file rows")
	}
	m.sourceAction("sc-folder:changes:resources")
	render()
	if !slices.Contains(s.panelView.clicks, stage) {
		t.Fatal("expanded folder did not restore its file rows")
	}
	s.Snapshot.Files = []sourceFile{{Path: "resources/main.go", Index: 'M'}}
	s.Snapshot.Groups[1], s.Snapshot.Groups[2] = s.Snapshot.Files, nil
	render()
	if slices.Contains(s.panelView.clicks, stage) || !slices.Contains(s.panelView.clicks, "sc-unstage:"+url.PathEscape("resources/main.go")) {
		t.Fatal("staging did not invalidate the file tree cache")
	}
}

func TestSourceControlSyncButtonCache(t *testing.T) {
	m := &workspaceModel{}
	m.bottom.active = pencodePanelSourceControl
	s := m.sourceState()
	for _, test := range []struct {
		Sync    sourceSyncStatus
		Changes bool
		Label   string
		Busy    bool
		JobKind sourceJobKind
	}{
		{Label: "Commit"},
		{Sync: sourceSyncStatus{Behind: 1}, Label: "Sync Changes"},
		{Sync: sourceSyncStatus{Ahead: 2}, Label: "Sync Changes"},
		{Sync: sourceSyncStatus{Ahead: 2, Behind: 3}, Label: "Sync Changes"},
		{Sync: sourceSyncStatus{Ahead: 2, Behind: 3}, Changes: true, Label: "Commit"},
		{Sync: sourceSyncStatus{Ahead: 2}, Label: "Sync Changes", Busy: true, JobKind: sourceRefresh},
		{Sync: sourceSyncStatus{Ahead: 2}, Label: "Sync Changes", Busy: true, JobKind: sourceQuery},
		{Sync: sourceSyncStatus{Ahead: 2}, Label: "Sync Changes", Busy: true, JobKind: sourceWrite},
		{Sync: sourceSyncStatus{Ahead: 2}, Label: "Sync Changes", Busy: true, JobKind: sourceSync},
		{Sync: sourceSyncStatus{Ahead: 2}, Label: "Sync Changes"},
	} {
		s.Done, s.JobKind = nil, test.JobKind
		if test.Busy {
			s.Done = make(chan sourceResult, 1)
		}
		s.Snapshot.Sync = test.Sync
		s.Snapshot.Files = nil
		if test.Changes {
			s.Snapshot.Files = []sourceFile{{Path: "resources/main.go", Index: 'M', Work: ' '}}
		}
		m.renderSourceControl(&s.panelView)
		found := false
		for _, group := range s.panelView.groups {
			found = found || group.node.Tag == "span" && group.node.Text == test.Label
			if group.node.Tag == "button" {
				id, disabled := "", false
				for _, attr := range group.attrs {
					switch attr.Name {
					case "id":
						id = attr.Value
					case "disabled":
						disabled = true
					}
				}
				if id == "sc-create-commit" && disabled != (test.Busy || !test.Changes && test.Sync == (sourceSyncStatus{})) {
					t.Fatalf("incorrect disabled state for %q: %t", test.Label, disabled)
				}
				if id == "sc-create-commit" {
					spinning := slices.Contains(group.node.Children[0].Attributes, html.VNodeAttribute{Name: "class", Value: "sc-icon sc-sync-spinning"})
					busy := slices.Contains(group.attrs, html.VNodeAttribute{Name: "aria-busy", Value: "true"})
					if want := test.Busy && test.JobKind == sourceSync; spinning != want || busy != want {
						t.Fatalf("incorrect synchronization activity: spinning=%t, aria-busy=%t, want %t", spinning, busy, want)
					}
					for _, direction := range [...]struct {
						key, class string
						count      int
					}{
						{"incoming", "sc-sync-down", test.Sync.Behind},
						{"outgoing", "sc-sync-up", test.Sync.Ahead},
					} {
						present := false
						for _, child := range group.node.Children {
							if child.Key != sourceVNodeKey("span", direction.key) {
								continue
							}
							present = true
							if len(child.Children) != 2 || child.Children[0].Tag != "img" || child.Children[1].Text != strconv.Itoa(direction.count) || !slices.Contains(child.Children[0].Attributes, html.VNodeAttribute{Name: "class", Value: "sc-icon " + direction.class}) || !slices.Contains(child.Children[0].Attributes, html.VNodeAttribute{Name: "src", Value: "icons/arrow-right.svg"}) {
								t.Fatalf("incorrect synchronization icon or count for %s", direction.key)
							}
						}
						if present != (!test.Changes && direction.count > 0) {
							t.Fatalf("incorrect synchronization count visibility for %s", direction.key)
						}
					}
				}
			}
		}
		if !found {
			t.Fatalf("missing primary button label %q", test.Label)
		}
		if allocations := testing.AllocsPerRun(100, func() { m.renderSourceControl(&s.panelView) }); allocations != 0 {
			t.Fatalf("unchanged synchronization view allocated %g times", allocations)
		}
	}
}

func TestSourceControlGroupsCollapseIndependently(t *testing.T) {
	m := &workspaceModel{}
	m.bottom.active = pencodePanelSourceControl
	s := m.sourceState()
	s.Snapshot.Files = []sourceFile{
		{Path: "resources/conflict.go", Index: 'U', Work: 'U', Conflict: true},
		{Path: "resources/staged.go", Index: 'M', Work: ' '},
		{Path: "resources/changed.go", Index: ' ', Work: 'M'},
	}
	for i := range s.Snapshot.Groups {
		s.Snapshot.Groups[i] = s.Snapshot.Files[i : i+1]
	}
	groups := [...]string{"merge", "staged", "changes"}
	m.renderSourceControl(&s.panelView)
	for i, kind := range groups {
		m.sourceAction("sc-group:" + kind)
		m.renderSourceControl(&s.panelView)
		for j, other := range groups {
			id := "sc-open:" + other + ":" + url.PathEscape(s.Snapshot.Files[j].Path)
			if slices.Contains(s.panelView.clicks, id) != (j > i) {
				t.Fatalf("collapsing %s affected the wrong group: %s", kind, other)
			}
		}
	}
	for _, kind := range groups {
		m.sourceAction("sc-group:" + kind)
	}
	m.renderSourceControl(&s.panelView)
	for i, kind := range groups {
		if !slices.Contains(s.panelView.clicks, "sc-open:"+kind+":"+url.PathEscape(s.Snapshot.Files[i].Path)) {
			t.Fatalf("expanding %s did not restore its files", kind)
		}
	}
}

func TestSourceControlRefreshRetainsUnchangedViews(t *testing.T) {
	m := &workspaceModel{}
	s := m.sourceState()
	s.Snapshot = sourceSnapshot{Repository: sourceRepository{Root: "/repository"}, HistoryRefs: "refs", Files: []sourceFile{{Path: "main.go", Work: 'M'}}}
	s.Snapshot.Groups[2] = s.Snapshot.Files
	s.Snapshot.Commits = []sourceCommit{{Hash: "0123456789abcdef"}}
	files, commits := &s.Snapshot.Files[0], &s.Snapshot.Commits[0]
	next := s.Snapshot
	next.Files = slices.Clone(next.Files)
	next.Groups[2] = next.Files
	next.Commits = slices.Clone(next.Commits)
	s.Done = make(chan sourceResult, 1)
	s.Done <- sourceResult{Snapshot: &next}
	if !m.pollSourceResult(s, nil) || &s.Snapshot.Files[0] != files || &s.Snapshot.Commits[0] != commits || &s.Snapshot.Groups[2][0] != files {
		t.Fatal("unchanged refresh replaced cached file/history data")
	}
	next.Files = []sourceFile{{Path: "main.go", Index: 'M'}}
	next.Groups[1], next.Groups[2] = next.Files, nil
	next.HistoryRefs = "new refs"
	next.Commits = []sourceCommit{{Hash: "fedcba9876543210"}}
	s.Done = make(chan sourceResult, 1)
	s.Done <- sourceResult{Snapshot: &next}
	if !m.pollSourceResult(s, nil) || &s.Snapshot.Files[0] == files || &s.Snapshot.Commits[0] == commits || len(s.Snapshot.Groups[1]) != 1 {
		t.Fatal("changed refresh retained stale file/history data")
	}
}
