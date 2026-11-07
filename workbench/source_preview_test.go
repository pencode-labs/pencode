package workbench

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onix-engine/onix/input"
)

func TestSourceDiffPreviewInteraction(t *testing.T) {
	r := sourceTestRepository(t)
	const first = "docs/道.md"
	const second = "docs/盟.md"
	const third = "third.md"
	for _, name := range []string{first, second, third} {
		sourceTestWrite(t, r, name, "base\n")
	}
	sourceTestGit(t, r, "add", "--all")
	sourceTestGit(t, r, "commit", "-m", "base")
	for _, name := range []string{first, second, third} {
		sourceTestWrite(t, r, name, "changed\n")
	}
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", first)
	app, m, _, state := languageInteractionFixture(t)
	editorContextClick(t, app, m, state, "status-source-control")
	sourceWait(t, app, m)
	s := m.sourceState()
	open := func(name string) *sourceTab {
		t.Helper()
		editorContextClick(t, app, m, state, "sc-open:changes:"+url.PathEscape(name))
		sourceWait(t, app, m)
		tab := s.activeTab()
		if tab == nil || tab.Path != name {
			t.Fatalf("did not open %q", name)
		}
		return tab
	}
	preview := open(first)
	if !preview.Preview {
		t.Fatal("single click did not open a preview")
	}
	preview = open(second)
	if len(s.Tabs) != 1 || !preview.Preview {
		t.Fatal("single click did not reuse the preview slot")
	}
	node, ok := m.fontResources.HTMLNode(m.surface, "sc-tab-group:"+preview.Key)
	class, _ := node.Attribute("class")
	if !ok || !strings.Contains(class, "preview") {
		t.Fatal("comparison preview did not use the shared preview style")
	}
	// A native double click on the file fixes its comparison in place.
	s.lastClick = ""
	editorContextClick(t, app, m, state, "sc-open:changes:"+url.PathEscape(second))
	editorContextClick(t, app, m, state, "sc-open:changes:"+url.PathEscape(second))
	sourceWait(t, app, m)
	pinned := s.activeTab()
	if pinned != preview || pinned.Preview || len(s.Tabs) != 1 {
		t.Fatal("double click did not pin the existing comparison")
	}
	preview = open(first)
	if len(s.Tabs) != 2 || !preview.Preview || s.Tabs[0] != pinned {
		t.Fatal("opening another file replaced a pinned comparison")
	}
	editorContextClick(t, app, m, state, "sc-code-right-input")
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "edited"})
	languageInteractionStep(t, app)
	if preview.Preview || preview.Result == preview.Saved {
		t.Fatal("editing did not pin the comparison")
	}
	if !preview.undoResult(false) || preview.Result != preview.Saved || preview.Preview {
		t.Fatal("undoing to saved content restored preview status")
	}
	preview.editResult("saved edit\n")
	m.sourceAction("sc-save-diff")
	sourceWait(t, app, m)
	if preview.Result != preview.Saved || preview.Preview {
		t.Fatal("saving restored preview status")
	}
	last := open(third)
	if len(s.Tabs) != 3 || !last.Preview || s.Tabs[1] != preview {
		t.Fatal("opening another file replaced an edited comparison")
	}
	s.lastClick = ""
	for range 2 {
		editorContextClick(t, app, m, state, "sc-tab:"+last.Key)
	}
	if last.Preview {
		t.Fatal("double clicking the tab did not pin the comparison")
	}
}

func TestSourceDiffPreviewPendingClicks(t *testing.T) {
	for _, name := range []string{"single", "double"} {
		t.Run(name, func(t *testing.T) {
			double := name == "double"
			r := sourceTestRepository(t)
			for _, name := range []string{"first.go", "second.go", "last.go"} {
				sourceTestWrite(t, r, name, "package demo\n")
			}
			m := &workspaceModel{backend: r.Backend, projectPath: r.Root}
			s := m.sourceState()
			s.Snapshot = sourceTestSnapshot(t, r)
			t.Cleanup(s.stop)
			now := time.Now()
			m.sourceClick("sc-open:changes:first.go", now)
			if double {
				// The second click arrives before the foreground accepts the job.
				m.sourceClick("sc-open:changes:first.go", now.Add(time.Millisecond))
			}
			wait := func() {
				t.Helper()
				deadline := time.Now().Add(5 * time.Second)
				for s.Done != nil && time.Now().Before(deadline) {
					m.pollSourceResult(s, nil)
					time.Sleep(time.Millisecond)
				}
				if s.Done != nil || s.Notice != "" {
					t.Fatalf("open failed: busy=%v notice=%q", s.Done != nil, s.Notice)
				}
			}
			wait()
			if len(s.Tabs) != 1 || s.activeTab().Preview == double {
				t.Fatal("pending open lost the single/double click intent")
			}
			first := s.activeTab()
			m.sourceClick("sc-open:changes:second.go", now.Add(time.Second))
			m.sourceClick("sc-open:changes:first.go", now.Add(2*time.Second))
			m.sourceClick("sc-open:changes:last.go", now.Add(3*time.Second))
			if double {
				m.sourceClick("sc-open:changes:last.go", now.Add(3*time.Second+time.Millisecond))
			}
			wait()
			wantTabs := 1
			if double {
				wantTabs = 2
			}
			if s.activeTab().Path != "last.go" || s.activeTab().Preview == double || len(s.Tabs) != wantTabs {
				t.Fatalf("rapid clicks lost the last selection: tabs=%d active=%+v", len(s.Tabs), s.activeTab())
			}
			if double && (s.Tabs[0] != first || first.Preview) {
				t.Fatal("rapid clicks replaced the pinned comparison")
			}
		})
	}
}

func TestSourceDiffPreviewLateResultPreservesSelection(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		old := &sourceTab{Key: "changes::old.go", Kind: "changes", Path: "old.go", Preview: true}
		next := &sourceTab{Key: "changes::next.go", Kind: "changes", Path: "next.go", Preview: true}
		m := &workspaceModel{}
		s := m.sourceState()
		s.Tabs, s.Active = []*sourceTab{old}, old.Key
		s.OpenKey, s.OpenPinned = next.Key, pinned
		s.Selection = 2 // The user selected the old tab after the job started.
		s.Done = make(chan sourceResult, 1)
		s.Done <- sourceResult{Tab: next, Selection: 1}
		m.pollSourceResult(s, nil)
		if s.activeTab() != old || s.Tabs[0] != old || !old.Preview {
			t.Fatal("late comparison replaced the selected preview")
		}
		want := 1
		if pinned {
			want = 2
		}
		if len(s.Tabs) != want || pinned && next.Preview {
			t.Fatal("late comparison did not preserve the pin intent")
		}
	}
}
