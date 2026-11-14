package workbench

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/onix-engine/onix/scene"
	"github.com/onix-engine/onix/syntax"
)

func TestTreeGitIgnoreScanAndLiveRefresh(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestWrite(t, r, ".gitignore", "cache/\n*.log\n!keep.log\n")
	for _, name := range []string{"cache/deep/file.go", "debug.log", "keep.log", "tracked.log", "scope/debug.log", "scope/main.go"} {
		sourceTestWrite(t, r, name, "")
	}
	sourceTestGit(t, r, "add", "-f", "tracked.log")
	// A nested repository with a .git file has its own exclusion rules.
	sourceTestGit(t, r, "init", "--separate-git-dir", filepath.Join(t.TempDir(), "metadata"), "nested")
	sourceTestWrite(t, r, "nested/.gitignore", "*.tmp\n")
	sourceTestWrite(t, r, "nested/debug.log", "")
	sourceTestWrite(t, r, "nested/build.tmp", "")
	m := &workspaceModel{backend: r.Backend, activeProjectIndex: -1}
	if err := loadProjectAndWait(t, m, r.Root); err != nil {
		t.Fatal(err)
	}
	p := m.activeProjectState()
	t.Cleanup(p.stopWatch)
	waitPencodeProjectScan(t, p, func() { m.pollProjectScans() })
	want := map[string]bool{
		"cache": true, "cache/deep": true, "cache/deep/file.go": true,
		"debug.log": true, "keep.log": false, "tracked.log": false,
		"nested": false, "nested/debug.log": false, "nested/build.tmp": true,
	}
	for name, ignored := range want {
		found := false
		for _, entry := range p.Entries {
			if entry.Path == name {
				found = true
				if entry.Ignored != ignored {
					t.Errorf("%s ignored=%v, want %v", name, entry.Ignored, ignored)
				}
			}
		}
		if !found {
			t.Errorf("missing tree entry %s", name)
		}
	}
	// A workspace inside a repository must also inherit its parent rules.
	if err := loadProjectAndWait(t, m, filepath.Join(r.Root, "scope")); err != nil {
		t.Fatal(err)
	}
	scope := m.activeProjectState()
	t.Cleanup(scope.stopWatch)
	waitPencodeProjectScan(t, scope, func() { m.pollProjectScans() })
	if len(scope.Entries) != 2 || !scope.Entries[0].Ignored || scope.Entries[1].Ignored {
		t.Fatalf("subdirectory workspace decorations: %+v", scope.Entries)
	}
	m.pollProjectWatches()
	sourceTestWrite(t, r, ".gitignore", "")
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.pollProjectScans()
		m.pollProjectWatches()
		updated := p.scan == nil
		for _, entry := range p.Entries {
			if entry.Path == "cache" || entry.Path == "debug.log" {
				updated = updated && !entry.Ignored
			}
		}
		if updated {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("editing .gitignore did not refresh tree decorations")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestIgnoredTreeNamesRenderWithThemeColor(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	p := m.activeProjectState()
	p.Entries = []treeEntry{
		{Path: "igndir", IsDir: true, Ignored: true},
		{Path: "ign.tmp", Ignored: true},
		{Path: "main.go", Openable: true},
	}
	p.tree.entriesChanged()
	computed := pencodeComputedTheme(t, *m.workbenchTheme, `<span data-ref="ignored" class="pencode-ignored-name"></span>`)
	ignoredColor := pencodeSyntaxColor(syntax.Color(computed["ignored"].Color.Color))
	for _, ignored := range []bool{true, false} {
		p.Entries[0].Ignored, p.Entries[1].Ignored = ignored, ignored
		markPencodeRootDirtyByModel(m.fontResources, m)
		languageInteractionStep(t, app)
		want := ignoredColor
		if !ignored {
			want = pencodeSyntaxColor(m.syntaxTheme.Color("sideBar.foreground"))
		}
		found := 0
		for _, access := range m.fontResources.Query[scene.Text2D]() {
			text := access.Ref()
			if name := text.Text().String(); name == "igndir" || name == "ign.tmp" {
				found++
				if text.Color != want {
					t.Errorf("%s ignored=%v color=%v, want %v", name, ignored, text.Color, want)
				}
			}
		}
		if found != 2 {
			t.Fatalf("rendered %d ignored names, want 2", found)
		}
	}
}
