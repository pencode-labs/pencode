package workbench

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceControlSubmoduleSummary(t *testing.T) {
	r, child := sourceTestRepository(t), sourceTestRepository(t)
	sourceTestWrite(t, child, "file.txt", "base\n")
	sourceTestGit(t, child, "add", ".")
	sourceTestGit(t, child, "commit", "-m", "base")
	base := sourceTestGit(t, child, "rev-parse", "HEAD")
	const path = "libs/child module"
	sourceTestGit(t, r, "-c", "protocol.file.allow=always", "submodule", "add", child.Root, path)
	check := func(kind, revision string, file sourceFile, wants ...string) {
		t.Helper()
		tab, err := r.openDiff(file, kind, revision)
		if err != nil {
			t.Fatal(err)
		}
		if tab.SubmoduleSummary == "" || tab.editable() || tab.Binary || len(tab.Rows) != 0 || tab.Result != tab.Saved {
			t.Fatalf("expected read-only submodule summary: %+v", tab)
		}
		for _, want := range wants {
			if !strings.Contains(tab.SubmoduleSummary, want) {
				t.Fatalf("summary missing %q: %s", want, tab.SubmoduleSummary)
			}
		}
	}
	file := sourceFile{Path: path, Index: 'A'}
	check("staged", "", file, "+Subproject commit "+base)
	sourceTestGit(t, r, "commit", "-m", "add submodule")
	added := sourceTestGit(t, r, "rev-parse", "HEAD")
	check("history", added, file, "+Subproject commit "+base)
	// Dirty tracked and untracked contents must never be read as a directory.
	sourceTestWrite(t, r, path+"/file.txt", "dirty\n")
	sourceTestWrite(t, r, path+"/untracked.txt", "untracked\n")
	file.Index, file.Work = ' ', 'M'
	check("changes", "", file, base+"-dirty")
	sourceTestWrite(t, r, path+"/file.txt", "base\n")
	check("changes", "", file, base+"-dirty")
	sourceTestWrite(t, r, path+"/file.txt", "dirty\n")
	nested := sourceRepository{Backend: r.Backend, Root: filepath.ToSlash(filepath.Join(r.Root, path))}
	if _, err := r.Backend.GitRepositories(t.Context(), r.Root); err != nil {
		t.Fatal(err)
	}
	sourceTestGit(t, nested, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-am", "update")
	head := sourceTestGit(t, nested, "rev-parse", "HEAD")
	check("changes", "", file, "-Subproject commit "+base, "+Subproject commit "+head+"-dirty")
	sourceTestGit(t, r, "add", path)
	file.Index = 'M'
	check("staged", "", file, "-Subproject commit "+base, "+Subproject commit "+head)
	sourceTestGit(t, r, "commit", "-m", "update submodule")
	check("history", sourceTestGit(t, r, "rev-parse", "HEAD"), file, base, head)
	sourceTestGit(t, r, "rm", "-f", path)
	file.Index = 'D'
	check("staged", "", file, "-Subproject commit "+head)
	sourceTestGit(t, r, "commit", "-m", "delete submodule")
	check("history", sourceTestGit(t, r, "rev-parse", "HEAD"), file, "-Subproject commit "+head)
}

func TestSourceControlSubmoduleSummaryRender(t *testing.T) {
	tab := &sourceTab{Key: "changes::child", Path: "child", Kind: "changes", SubmoduleSummary: "Submodule · child\n+Subproject commit abc-dirty\n"}
	m := &workspaceModel{}
	s := m.sourceState()
	s.Tabs, s.Active = []*sourceTab{tab}, tab.Key
	var b sourceVNodeView
	render := func() {
		b.reset()
		m.renderSourceTabs(&b)
		b.nodes()
	}
	render()
	found := false
	var text strings.Builder
	for _, group := range b.groups[:b.used] {
		if group.node.Tag == "textarea" || group.node.Tag == "button" {
			t.Fatal("submodule summary exposed editing controls")
		}
		found = found || group.node.Tag == "pre"
		if group.node.Tag == "span" {
			text.WriteString(group.node.Text)
		}
	}
	if !found || text.String() != tab.SubmoduleSummary {
		t.Fatal("missing submodule summary text")
	}
	if n := testing.AllocsPerRun(100, render); n != 0 {
		t.Fatalf("unchanged submodule summary allocated %g times", n)
	}
}
