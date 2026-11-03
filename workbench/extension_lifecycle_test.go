package workbench

import (
	"strings"
	"testing"

	"github.com/pencode-labs/pencode/peb"
)

func TestExtensionCloseWaitsForAllAndCanBeCancelled(t *testing.T) {
	p := &pencodeProject{Path: "/project", Files: map[string]*openFile{}}
	m := &workspaceModel{projects: []*pencodeProject{p}}
	m.closing = pencodeCloseRequest{action: pencodeCloseProject, project: p, lifecycle: true, lifecycleToken: 7, lifecyclePending: map[string]bool{"one": true, "two": true}}
	m.resolveExtensionClose("one", 6, peb.LifecycleReply{Message: "stale"})
	if len(m.closing.lifecyclePending) != 2 {
		t.Fatal("stale reply resolved current request")
	}
	m.resolveExtensionClose("one", 7, peb.LifecycleReply{Message: "Codex still running"})
	m.resolveClose(true)
	if len(m.projects) != 1 || len(m.closing.lifecyclePending) != 1 {
		t.Fatal("closed before every extension replied")
	}
	m.resolveExtensionClose("two", 7, peb.LifecycleReply{})
	if !strings.Contains(m.closing.message, "Codex still running") || len(m.projects) != 1 {
		t.Fatal("confirmation missing or project closed early")
	}
	m.clearCloseRequest()
	m.resolveExtensionClose("two", 7, peb.LifecycleReply{Message: "late"})
	if m.closing.action != pencodeCloseNone || len(m.projects) != 1 {
		t.Fatal("late reply revived cancelled closure")
	}
}

func TestExtensionCloseCollectsNewEditsAndKeepsSavePrompt(t *testing.T) {
	for _, action := range []pencodeCloseAction{pencodeCloseWindow, pencodeCloseProject} {
		p := &pencodeProject{Path: "/project", Name: "project", Tabs: []string{"edited.go"}, Files: map[string]*openFile{"edited.go": {Path: "edited.go"}}}
		m := &workspaceModel{projects: []*pencodeProject{p}}
		m.closing = pencodeCloseRequest{action: action, project: p, lifecycle: true, lifecycleToken: 1, lifecyclePending: map[string]bool{"codex": true}}
		if action == pencodeCloseWindow {
			m.closing.project = nil
		}
		m.resolveExtensionClose("codex", 1, peb.LifecycleReply{Message: "Still running"})
		// A running extension edits a previously clean file while confirmation is open.
		p.Files["edited.go"].Dirty = true
		m.resolveClose(true)
		if m.closing.lifecycle || len(m.closing.targets) != 1 || !strings.Contains(m.closing.message, "是否保存") {
			t.Fatal("close approval skipped fresh unsaved edits", m.closing)
		}
		if len(m.projects) != 1 {
			t.Fatal("project closed before save decision")
		}
		m.clearCloseRequest()
	}
}

func TestIdleExtensionCloseStillChecksUnsavedFiles(t *testing.T) {
	p := &pencodeProject{Path: "/project", Name: "project", Tabs: []string{"edited.go"}, Files: map[string]*openFile{"edited.go": {Path: "edited.go", Dirty: true}}}
	m := &workspaceModel{projects: []*pencodeProject{p}}
	m.closing = pencodeCloseRequest{action: pencodeCloseWindow, lifecycle: true, lifecycleToken: 1, lifecyclePending: map[string]bool{"codex": true}}
	m.resolveExtensionClose("codex", 1, peb.LifecycleReply{})
	if m.closing.lifecycle || len(m.closing.targets) != 1 || !strings.Contains(m.closing.message, "edited.go") {
		t.Fatal("idle extension skipped save prompt")
	}
}
