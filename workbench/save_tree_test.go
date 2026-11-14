package workbench

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/onix-engine/onix/scene"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/protocol"
)

func TestSavePreservesExplorer(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	p := m.activeProjectState()
	m.app.GetViewport = func() (int, int) { return 1440, 900 }
	app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	for i := range 60 {
		resourceTestWrite(t, p, fmt.Sprintf("item-%02d.go", i), "package demo\n")
	}
	resourceTestWrite(t, p, "folder/child.go", "package demo\n")
	m.startProjectScan(p, false)
	waitPencodeProjectScan(t, p, func() { languageInteractionStep(t, app) })
	// Drain setup notifications before measuring an ordinary save.
	deadline := time.Now().Add(5 * time.Second)
	until := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(until) || p.scan != nil || p.sourceControl.Done != nil {
		languageInteractionStep(t, app)
		if time.Now().After(deadline) {
			t.Fatal("initial snapshots did not complete")
		}
		time.Sleep(time.Millisecond)
	}
	m.updateActiveContent("package saved\n")
	m.refreshRegion(m.fontResources, pencodeRefreshEditor)
	settlePencodeHighlight(t, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	m.revealProject, m.revealPath = nil, ""
	scroll := m.fontResources.Mut[scene.UIScroll](p.tree.area)
	scroll.Position[1] = -300
	languageInteractionStep(t, app)
	rowID := m.projectTreeRowID(p, "item-15.go")
	row, ok := m.fontResources.HTMLControl(m.surface, rowID)
	if !ok {
		t.Fatal("missing visible explorer row")
	}
	// Geometry may be synchronized by a status-line reflow; the tree's render
	// callback itself must stay idle when only save bindings change.
	rowRenders := 0
	for _, component := range m.fontResources.Query[htmlui.Component[treeProps, struct{}]]() {
		definition := component.Ref().Definition
		render := definition.Behavior.Render
		definition.Behavior.Render = func(context htmlui.RenderContext[treeProps, struct{}], view *htmlui.View) {
			if context.Props.Path == "item-15.go" {
				rowRenders++
			}
			render(context, view)
		}
		t.Cleanup(func() { definition.Behavior.Render = render })
		break
	}
	index, revision := p.index, p.tree.revision
	state := keymapState(m.settings.Bindings["save"], runtime.GOOS)
	if !m.handleShortcutKeys(m.fontResources, state, runtime.GOOS) || m.fullRefresh {
		t.Fatal("save shortcut was not handled with a binding patch")
	}
	deadline = time.Now().Add(5 * time.Second)
	until = time.Now().Add(700 * time.Millisecond)
	for time.Now().Before(until) || m.activeFile().Dirty || len(m.fileIO.pending) != 0 {
		languageInteractionStep(t, app)
		if p.scan != nil || p.treePending || p.index != index || p.tree.revision != revision {
			t.Fatal("saving rebuilt the explorer snapshot")
		}
		if m.revealProject != nil || p.Collapsed || p.ExpandedDirs["folder"] {
			t.Fatal("saving changed explorer navigation or folding")
		}
		if got := m.fontResources.Ref[scene.UIScroll](p.tree.area).Position.Y(); got != -300 {
			t.Fatalf("saving moved the explorer: %v", got)
		}
		if time.Now().After(deadline) {
			t.Fatal("save did not complete")
		}
		time.Sleep(time.Millisecond)
	}
	next, ok := m.fontResources.HTMLControl(m.surface, rowID)
	if !ok || next != row || rowRenders != 0 {
		t.Fatal("saving rendered an unrelated explorer row")
	}
	for _, component := range m.fontResources.Query[htmlui.Component[tabProps, struct{}]]() {
		if props := component.Ref().Props; props.Project == p && props.Path == "main.go" && props.Dirty {
			t.Fatal("saved tab still shows the dirty indicator")
		}
	}
	data, err := os.ReadFile(filepath.Join(p.Path, "main.go"))
	if err != nil || string(data) != "package saved\n" {
		t.Fatalf("save did not reach disk: %q, %v", data, err)
	}
}

func TestProjectWatchDistinguishesReplacementAndStructure(t *testing.T) {
	for _, test := range []struct {
		name    string
		files   []protocol.FileEvent
		changes protocol.WorkspaceChange
		want    bool
	}{
		{"replacement", []protocol.FileEvent{{Path: "main.go", Op: protocol.FileCreated | protocol.FileWritten}}, 0, false},
		{"create", []protocol.FileEvent{{Path: "new.go", Op: protocol.FileCreated}}, 0, true},
		{"remove", []protocol.FileEvent{{Path: "main.go", Op: protocol.FileRemoved}}, 0, true},
		{"rename", []protocol.FileEvent{{Path: "main.go", Op: protocol.FileRenamed}}, 0, true},
		{"directory", []protocol.FileEvent{{Path: "main.go", Op: protocol.FileCreated, Directory: true}}, 0, true},
		{"ignore", []protocol.FileEvent{{Path: ".gitignore", Op: protocol.FileWritten}}, 0, true},
		{"mixed", []protocol.FileEvent{{Path: "main.go", Op: protocol.FileCreated}, {Path: "new.go", Op: protocol.FileCreated}}, 0, true},
		{"concurrent-index", []protocol.FileEvent{{Path: "main.go", Op: protocol.FileCreated}}, protocol.WorkspaceIgnoreChanged, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &pencodeProject{Entries: []treeEntry{{Path: "main.go"}}, watch: &pencodeProjectWatch{}}
			m := &workspaceModel{projects: []*pencodeProject{p}, quickOpen: true}
			if allocations := testing.AllocsPerRun(100, func() {
				p.treePending = false
				p.watch.collect(protocol.FileChanges{Files: test.files})
				p.watch.pending.Store(uint32(protocol.WorkspaceTreeChanged | test.changes))
				m.pollProjectWatches()
				if p.treePending != test.want {
					t.Fatalf("tree refresh=%v, want %v", p.treePending, test.want)
				}
			}); allocations != 0 {
				t.Fatalf("warm watch handling allocated %g times", allocations)
			}
		})
	}
}
