package workbench

import (
	"html"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/peb"
)

func extensionDiffFixture(t *testing.T, r sourceRepository) (*workspaceModel, *pencodeExtensionSurface, func()) {
	t.Helper()
	p := &pencodeProject{Path: r.Root, backend: r.Backend, Files: make(map[string]*openFile)}
	m := &workspaceModel{backend: r.Backend, projects: []*pencodeProject{p}}
	m.syncActiveProjectFields()
	s := &pencodeExtensionSurface{model: m, owner: "codex", ui: peb.UI{Kind: peb.EditorTab}, invocation: pencodeExtensionInvocation{context: peb.ActionContext{ProjectPath: r.Root}}, prefix: "pencode-extension-ui/diff/", contentID: "content"}
	t.Cleanup(p.sourceControl.stop)
	wait := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for p.sourceControl.Done != nil {
			m.pollSourceResult(&p.sourceControl, p)
			if time.Now().After(deadline) {
				t.Fatal("diff navigation did not complete")
			}
			time.Sleep(time.Millisecond)
		}
	}
	return m, s, wait
}

func TestExtensionDiffLinksUseSourceControlPreview(t *testing.T) {
	r := sourceTestRepository(t)
	const name = "src/道 & file.go"
	sourceTestWrite(t, r, name, "old\n")
	sourceTestGit(t, r, "add", "--all")
	sourceTestGit(t, r, "commit", "-m", "base")
	sourceTestWrite(t, r, name, "new\n")
	m, surface, wait := extensionDiffFixture(t, r)
	target := url.URL{Scheme: "pencode-diff", Path: "/" + strings.TrimPrefix(r.Root+"/"+name, "/")}
	if err := surface.setView(&peb.EditorView{HTML: `<button type="button" role="link" data-ref="file" data-pencode-href="` + html.EscapeString(target.String()) + `">File</button>`}); err != nil {
		t.Fatal(err)
	}
	m.actions.ui.editor = surface
	props := pencodeExtensionSurfaceProps{Surface: surface}
	surface.definition.Behavior.HandleEvent(&htmlui.ComponentEventContext[pencodeExtensionSurfaceProps, struct{}]{Props: &props, Ref: "file", Event: htmlui.Event{Kind: htmlui.EventClick}})
	wait()
	s := m.sourceState()
	tab := s.activeTab()
	if tab == nil || tab.Path != name || tab.Kind != "changes" || !tab.Preview || tab.Local != "old\n" || tab.Result != "new\n" || m.actions.ui.editor != nil {
		t.Fatalf("native link did not open the SCM preview: tab=%+v notice=%q", tab, s.Notice)
	}
	m.sourceAction("sc-open:changes:" + url.PathEscape(name))
	wait()
	if s.activeTab() != tab || len(s.Tabs) != 1 {
		t.Fatal("SCM and extension navigation did not reuse the same comparison")
	}
	sourceTestWrite(t, r, name, "latest\n")
	surface.openLink(target.String())
	wait()
	if len(s.Tabs) != 1 || s.activeTab().Result != "latest\n" || s.Notice != "" {
		t.Fatal("reopening the preview did not refresh current contents", s.Notice)
	}
}

func TestExtensionDiffLinksResolveCurrentGitStatus(t *testing.T) {
	r := sourceTestRepository(t)
	for _, name := range []string{"staged.go", "removed/file.go", "old.go"} {
		sourceTestWrite(t, r, name, "old\n")
	}
	sourceTestGit(t, r, "add", "--all")
	sourceTestGit(t, r, "commit", "-m", "base")
	sourceTestWrite(t, r, "staged.go", "staged\n")
	sourceTestGit(t, r, "add", "staged.go")
	sourceTestGit(t, r, "mv", "old.go", "renamed.go")
	if err := os.RemoveAll(filepath.Join(r.Root, "removed")); err != nil {
		t.Fatal(err)
	}
	sourceTestWrite(t, r, "new.go", "added\n")
	m, surface, wait := extensionDiffFixture(t, r)
	for _, tc := range []struct{ path, kind, left, right string }{
		{"staged.go", "staged", "old\n", "staged\n"},
		{"renamed.go", "staged", "old\n", "old\n"},
		{"removed/file.go", "changes", "old\n", ""},
		{"new.go", "changes", "", "added\n"},
	} {
		target := url.URL{Scheme: "pencode-diff", Path: "/" + strings.TrimPrefix(r.Root+"/"+tc.path, "/")}
		surface.openLink(target.String())
		wait()
		tab := m.sourceState().activeTab()
		if tab == nil || tab.Path != tc.path || tab.Kind != tc.kind || tab.Local != tc.left || tab.Incoming != tc.right || !tab.Preview {
			t.Fatalf("%s: tab=%+v notice=%q", tc.path, tab, m.sourceState().Notice)
		}
	}
}

func TestExtensionDiffLinksKeepLatestClickAcrossRepositories(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestWrite(t, r, "first.go", "first\n")
	child := sourceRepository{Root: filepath.ToSlash(filepath.Join(r.Root, "child")), Backend: r.Backend}
	sourceTestWrite(t, child, "last.go", "last\n")
	sourceTestGit(t, r, "-C", child.Root, "init", "-b", "main")
	m, surface, wait := extensionDiffFixture(t, r)
	for _, name := range []string{r.Root + "/first.go", child.Root + "/last.go"} {
		target := url.URL{Scheme: "pencode-diff", Path: "/" + strings.TrimPrefix(name, "/")}
		surface.openLink(target.String())
	}
	wait()
	s := m.sourceState()
	if tab := s.activeTab(); tab == nil || tab.Path != "last.go" || s.Snapshot.Repository.Root != child.Root || s.Notice != "" {
		t.Fatalf("latest click lost after repository discovery: tab=%+v notice=%q", tab, s.Notice)
	}
}

func TestExtensionDiffLinksRespectDirtyBuffersAndSelection(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestWrite(t, r, "file.go", "disk\n")
	m, surface, wait := extensionDiffFixture(t, r)
	target := url.URL{Scheme: "pencode-diff", Path: "/" + strings.TrimPrefix(r.Root+"/file.go", "/")}
	surface.openLink(target.String())
	m.sourceState().Selection++ // The user switched editors while discovery ran.
	wait()
	if len(m.sourceState().Tabs) != 0 {
		t.Fatal("late discovery opened a comparison after the user navigated away")
	}
	m.activeProjectState().Files["file.go"] = &openFile{Path: "file.go", Dirty: true}
	surface.openLink(target.String())
	wait()
	if s := m.sourceState(); len(s.Tabs) != 0 || !strings.Contains(s.Notice, "请先保存") {
		t.Fatal("diff navigation bypassed the SCM dirty buffer guard", s.Notice)
	}
}
