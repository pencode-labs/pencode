package workbench

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onix-engine/onix/scene"
	"github.com/onix-engine/onix/syntax"
)

func TestTreeGitStatusPropagation(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	nested := root + "/scope/nested"
	roots := []string{root, nested}
	snapshot := sourceSnapshot{Repository: sourceRepository{Root: root}, Files: []sourceFile{
		{Path: "scope/mixed/new.go", Index: '?', Work: '?'},
		{Path: "scope/mixed/modified.go", Index: ' ', Work: 'M'},
		{Path: "scope/conflicted/file.go", Index: 'U', Work: 'U', Conflict: true},
		{Path: "scope/deleted/missing.go", Index: 'D', Work: ' '},
		{Path: "scope/renamed/new.go", Previous: "scope/previous/old.go", Index: 'R', Work: ' '},
		{Path: "outside/new.go", Previous: "scope/moved-out/old.go", Index: 'R', Work: ' '},
		{Path: "scope/added/new.go", Index: 'A', Work: ' '},
		{Path: "scope/added/edited.go", Index: 'A', Work: 'M'},
		{Path: "scope/typed/file.go", Index: ' ', Work: 'T'},
		{Path: "scope/new/file.go", Index: '?', Work: '?'},
		{Path: "scope/nested/file.go", Index: '?', Work: '?'},
		{Path: "scope-other/file.go", Index: ' ', Work: 'M'},
	}}
	var git pencodeTreeGit
	git.update(filepath.Join(root, "scope"), roots, map[string][]sourceFile{
		nested: {{Path: "file.go", Index: ' ', Work: 'M'}},
	}, &snapshot)
	for name, want := range map[string]treeGitStatus{
		"mixed": treeGitModified, "mixed/new.go": treeGitUntracked,
		"mixed/modified.go": treeGitModified,
		"conflicted":        treeGitConflict, "conflicted/file.go": treeGitConflict,
		"deleted": treeGitDeleted, "deleted/missing.go": treeGitDeleted,
		"renamed": treeGitRenamed, "renamed/new.go": treeGitRenamed,
		"previous": treeGitDeleted, "moved-out": treeGitDeleted,
		"added": treeGitModified, "added/new.go": treeGitAdded, "added/edited.go": treeGitModified,
		"new": treeGitUntracked, "typed": treeGitModified,
		"nested": treeGitModified, "nested/file.go": treeGitModified,
		"scope-other": treeGitClean, "clean": treeGitClean,
	} {
		if got := git.names[name]; got != want {
			t.Errorf("%s status=%d, want %d", name, got, want)
		}
	}
	if n := testing.AllocsPerRun(100, func() {
		if git.names["nested"].nameClass() != "pencode-tree-name git-modified" {
			t.Fatal("missing folder decoration")
		}
	}); n != 0 {
		t.Fatalf("warm decoration lookup allocated %g times", n)
	}
	// A Git action can replace one repository while preserving the others.
	snapshot.Files = nil
	git.update(filepath.Join(root, "scope"), roots, nil, &snapshot)
	if len(git.names) != 2 || git.names["nested"] != treeGitModified {
		t.Fatalf("partial repository refresh: %v", git.names)
	}
	git.update(filepath.Join(root, "scope"), roots[:1], nil, &snapshot)
	if len(git.names) != 0 {
		t.Fatalf("removed repository retained decorations: %v", git.names)
	}
}

func TestTreeGitWatchRefreshAndRestore(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestWrite(t, r, ".gitignore", "ignored/\n")
	sourceTestWrite(t, r, "folder/deep/main.go", "original\n")
	sourceTestWrite(t, r, "deleted/only.go", "original\n")
	sourceTestGit(t, r, "add", ".")
	sourceTestGit(t, r, "commit", "-m", "base")
	// The nested repository stays independent of the selected source repository.
	sourceTestGit(t, r, "init", "-b", "main", "nested")
	sourceTestWrite(t, r, "nested/new.go", "new\n")
	m := &workspaceModel{backend: r.Backend, activeProjectIndex: -1}
	if err := loadProjectAndWait(t, m, r.Root); err != nil {
		t.Fatal(err)
	}
	p := m.activeProjectState()
	t.Cleanup(p.stopWatch)
	t.Cleanup(p.sourceControl.stop)
	waitPencodeProjectScan(t, p, func() { m.pollProjectScans() })
	wait := func(condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			m.pollProjectWatches()
			m.pollProjectScans()
			m.updateSourceControl(nil, nil)
			if p.sourceControl.RefreshStarted && p.sourceControl.Done == nil && condition() {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("tree Git refresh timed out: names=%v, notice=%q", p.treeGit.names, p.sourceControl.Notice)
			}
			time.Sleep(time.Millisecond)
		}
	}
	wait(func() bool { return p.treeGit.names["nested"] == treeGitUntracked })
	if p.sourceControl.Snapshot.Repository.Root != r.Root {
		t.Fatal("decorating the nested repository changed the source selection")
	}
	// Keep the first project visible while another project owns the editor.
	other := sourceTestRepository(t)
	sourceTestGit(t, other, "commit", "--allow-empty", "-m", "base")
	if err := loadProjectAndWait(t, m, other.Root); err != nil {
		t.Fatal(err)
	}
	active := m.activeProjectState()
	t.Cleanup(active.stopWatch)
	t.Cleanup(active.sourceControl.stop)
	waitPencodeProjectScan(t, active, func() { m.pollProjectScans() })
	sourceTestWrite(t, r, "folder/deep/main.go", "modified\n")
	sourceTestWrite(t, r, "new/deep/new.go", "new\n")
	sourceTestWrite(t, r, "ignored/file.go", "ignored\n")
	if err := os.Remove(filepath.Join(r.Root, "deleted/only.go")); err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		return p.treeGit.names["folder"] == treeGitModified &&
			p.treeGit.names["folder/deep"] == treeGitModified &&
			p.treeGit.names["new"] == treeGitUntracked &&
			p.treeGit.names["deleted"] == treeGitDeleted
	})
	if p.treeGit.names["ignored"] != treeGitClean {
		t.Fatal("ignored file decorated its parent")
	}
	sourceTestGit(t, r, "restore", ".")
	sourceTestGit(t, r, "add", "new")
	wait(func() bool {
		return p.treeGit.names["folder"] == treeGitClean &&
			p.treeGit.names["deleted"] == treeGitClean &&
			p.treeGit.names["new"] == treeGitAdded
	})
	sourceTestGit(t, r, "commit", "-m", "add new file")
	wait(func() bool { return p.treeGit.names["new"] == treeGitClean })
	if m.activeProjectState() != active {
		t.Fatal("background decorations changed the active project")
	}
}

func TestTreeGitFolderNamesRenderWithThemeColor(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	p := m.activeProjectState()
	p.Entries = []treeEntry{
		{Path: "changed", IsDir: true},
		{Path: "changed/deep", Depth: 1, IsDir: true},
		{Path: "changed/deep/file.go", Depth: 2, Openable: true},
		{Path: "clean", IsDir: true},
	}
	p.ExpandedDirs = map[string]bool{"changed": false}
	p.tree.entriesChanged()
	// Leave source discovery idle so the fixture's synthetic statuses persist.
	p.sourceControl.cancelRefresh()
	p.sourceControl.RefreshStarted, p.sourceControl.RefreshPending = true, false
	for _, status := range []treeGitStatus{treeGitUntracked, treeGitAdded, treeGitRenamed, treeGitDeleted, treeGitModified, treeGitConflict, treeGitClean} {
		p.treeGit.names = map[string]treeGitStatus{"changed": status, "changed/deep": status, "changed/deep/file.go": status}
		computed := pencodeComputedTheme(t, *m.workbenchTheme, `<span data-ref="name" class="`+status.nameClass()+`"></span>`)
		want := pencodeSyntaxColor(syntax.Color(computed["name"].Color.Color))
		if status == treeGitClean {
			want = pencodeSyntaxColor(m.syntaxTheme.Color("sideBar.foreground"))
		}
		for _, expanded := range []bool{false, true} {
			p.ExpandedDirs["changed"], p.ExpandedDirs["changed/deep"] = expanded, expanded
			p.tree.revision++
			markPencodeRootDirtyByModel(m.fontResources, m)
			languageInteractionStep(t, app)
			found := 0
			for _, access := range m.fontResources.Query[scene.Text2D]() {
				text := access.Ref()
				switch text.Text().String() {
				case "changed", "deep", "file.go":
					found++
					if text.Color != want {
						t.Errorf("status=%d expanded=%v name=%s color=%v, want %v", status, expanded, text.Text().String(), text.Color, want)
					}
				case "clean":
					if text.Color != pencodeSyntaxColor(m.syntaxTheme.Color("sideBar.foreground")) {
						t.Error("unaffected sibling changed color")
					}
				}
			}
			wantCount := 1
			if expanded {
				wantCount = 3
			}
			if found != wantCount {
				t.Fatalf("expanded=%v rendered %d decorated names, want %d", expanded, found, wantCount)
			}
		}
	}
}
