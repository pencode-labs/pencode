package workbench

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/projects/pencode/client"
	"github.com/diiyw/onix/projects/pencode/extensions"
	"github.com/diiyw/onix/projects/pencode/lsp"
	"github.com/diiyw/onix/projects/pencode/peb"
	"github.com/diiyw/onix/scene"
)

func TestPencodeLanguageExtensionProcess(t *testing.T) {
	if !slices.Contains(os.Args, "pencode-language-fixture") {
		return
	}
	command := os.Args[len(os.Args)-1]
	err := peb.ServeExtension(os.Stdin, os.Stdout, peb.Contributions{LanguageServers: []peb.LanguageServer{{
		ID: "gopls", Label: "Go", LanguageID: "go", Command: command,
		Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".go"}},
	}}}, peb.Handlers{})
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func installRemoteGoServer(t *testing.T, backend *client.Client, command string) *extensions.Bundle {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "go-lsp")
	if err := os.MkdirAll(filepath.Join(directory, "resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Base(executable)
	if err := os.Link(executable, filepath.Join(directory, entry)); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(extensions.Manifest{ID: "go-lsp", Entry: entry, Args: []string{
		"-test.run=^TestPencodeLanguageExtensionProcess$", "--", "pencode-language-fixture", command,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "resources", "extension.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	_, bundle, err := extensions.Install(t.Context(), backend, directory)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func languageTestModel(t *testing.T) (*workspaceModel, *pencodeProject, *openFile) {
	t.Helper()
	root := t.TempDir()
	file := &openFile{Path: "main.go", Name: "main.go", Content: "package demo\n\nfunc Add(a,b int) int{return a+b}\n"}
	file.savedContent = file.Content
	for name, content := range map[string]string{"main.go": file.Content, "other.go": "package demo\n", "go.mod": "module example.com/workbench\n\ngo 1.26.0\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	project := &pencodeProject{Path: root, Files: map[string]*openFile{file.Path: file}, Tabs: []string{file.Path}, ActivePath: file.Path}
	connectPencodeProject(t, project)
	model := &workspaceModel{backend: project.backend, projects: []*pencodeProject{project}, activeProjectIndex: 0, settings: defaultPencodeSettings()}
	t.Cleanup(model.stopFileIO)
	model.syncActiveProjectFields()
	model.languages = &pencodeLanguageState{sessions: make(map[pencodeLanguageKey]*pencodeLanguageSession), documents: make(map[*openFile]*pencodeLanguageDocument), events: make(chan pencodeLanguageEvent, 128)}
	return model, project, file
}
func TestLanguageWorkspaceEditStagesAllFilesAndVersions(t *testing.T) {
	model, project, file := languageTestModel(t)
	session := &pencodeLanguageSession{key: pencodeLanguageKey{root: project.Path}, client: &lsp.Client{Encoding: lsp.UTF16}}
	uri := lsp.FileURI(filepath.Join(project.Path, file.Path))
	model.languages.documents[file] = &pencodeLanguageDocument{file: file, content: file.Content, version: 3}
	version := 3
	change, _ := json.Marshal(lsp.TextDocumentEdit{TextDocument: lsp.VersionedTextDocumentIdentifier{URI: uri, Version: &version}, Edits: []lsp.TextEdit{{Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 8}, End: lsp.Position{Line: 0, Character: 12}}, NewText: "renamed"}}})
	invalid := lsp.WorkspaceEdit{DocumentChanges: []json.RawMessage{change, json.RawMessage(`{"kind":"delete","uri":"file:///unrelated"}`)}}
	if err := model.applyLanguageWorkspaceEdit(session, invalid, nil); err == nil || file.Content != file.savedContent || len(file.undo) != 0 {
		t.Fatalf("partial edit leaked: %s %v", file.Content, err)
	}
	version = 2
	stale, _ := json.Marshal(lsp.TextDocumentEdit{TextDocument: lsp.VersionedTextDocumentIdentifier{URI: uri, Version: &version}, Edits: []lsp.TextEdit{}})
	if err := model.applyLanguageWorkspaceEdit(session, lsp.WorkspaceEdit{DocumentChanges: []json.RawMessage{stale}}, nil); err == nil {
		t.Fatal("stale version accepted")
	}
	other, _ := json.Marshal(lsp.TextDocumentEdit{TextDocument: lsp.VersionedTextDocumentIdentifier{URI: lsp.FileURI(filepath.Join(project.Path, "other.go"))}, Edits: []lsp.TextEdit{{Range: lsp.Range{}, NewText: "// changed\n"}}})
	edit := lsp.WorkspaceEdit{DocumentChanges: []json.RawMessage{change, other}, Changes: map[lsp.URI][]lsp.TextEdit{uri: {{Range: lsp.Range{}, NewText: "ignored fallback"}}}}

	if err := model.applyLanguageWorkspaceEdit(session, edit, nil); err != nil {
		t.Fatal(err)
	}
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	if !file.Dirty || !strings.HasPrefix(file.Content, "package renamed") || len(file.undo) != 1 || len(project.Tabs) != 2 || !project.Files["other.go"].Dirty {
		t.Fatal("workspace edits did not enter normal document history")
	}
	disk, _ := os.ReadFile(filepath.Join(project.Path, "other.go"))
	if string(disk) != "package demo\n" {
		t.Fatal("edit bypassed save")
	}
}
func TestLanguageSemanticOverlay(t *testing.T) {
	red, blue, green := graphics.ColorFromRGBA(255, 0, 0, 255), graphics.ColorFromRGBA(0, 0, 255, 255), graphics.ColorFromRGBA(0, 255, 0, 255)
	base := []scene.TextSpan2D{{Start: 0, End: 10, Color: red}, {Start: 12, End: 20, Color: blue}}
	overlay := []scene.TextSpan2D{{Start: 2, End: 4, Color: green}, {Start: 6, End: 8, Color: green}, {Start: 9, End: 15, Color: green}}
	got := overlayLanguageSpans(nil, base, overlay)
	want := []struct{ start, end int }{{0, 2}, {2, 4}, {4, 6}, {6, 8}, {8, 9}, {9, 15}, {15, 20}}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i, span := range got {
		if span.Start != want[i].start || span.End != want[i].end {
			t.Fatalf("%+v", got)
		}
	}
	if base[0].Start != 0 || base[0].End != 10 {
		t.Fatal("overlay changed syntax cache")
	}
}
func TestLanguageWorkbenchGoplsAndExtensionUnload(t *testing.T) {
	command, err := exec.LookPath("gopls")
	if err != nil {
		t.Skip("gopls is not installed")
	}
	model, project, file := languageTestModel(t)
	bundle := installRemoteGoServer(t, model.backend, command)
	model.importLanguageServers(bundle.LanguageServers)
	defer model.closeLanguageServers()
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			model.pollLanguageServers(nil)
			if ready() {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("LSP did not settle", model.status, model.languages.panel.body)
	}
	wait(func() bool { doc := model.languages.documents[file]; return doc != nil && len(doc.servers) == 1 })
	session := model.languages.sessions[pencodeLanguageKey{project.Path, "extensions/go-lsp/gopls"}]
	model.runLanguageAction(nil, "textDocument/formatting", false)
	wait(func() bool { return strings.Contains(file.Content, "a, b int") })
	if !file.Dirty || len(file.undo) != 1 {
		t.Fatal("formatting bypassed normal editing")
	}
	if err := saveFileAndWait(t, model, project.Path, file); err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		doc := model.languages.documents[file]
		return doc != nil && doc.servers[session].saveRevision == file.saveRevision
	})
	model.removeLanguageServers("extensions/go-lsp/")
	select {
	case <-session.done:
	case <-time.After(3 * time.Second):
		t.Fatal("unloaded language server process leaked")
	}
	if len(model.languages.servers) != 0 || len(model.languages.sessions) != 0 {
		t.Fatal("unloaded server retained")
	}
}
func TestLanguageIdlePollingDoesNotAllocate(t *testing.T) {
	model, project, file := languageTestModel(t)
	server := &extensions.LanguageServer{LanguageServer: peb.LanguageServer{ID: "extensions/test/test", LanguageID: "go", Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".go"}}}}
	session := &pencodeLanguageSession{key: pencodeLanguageKey{project.Path, server.ID}, server: server, client: &lsp.Client{Capabilities: lsp.ServerCapabilities{}, Encoding: lsp.UTF16}, jobs: make(chan pencodeLanguageJob, 1), cancel: func() {}}
	doc := &pencodeLanguageDocument{file: file, project: project, root: project.Path, uri: lsp.FileURI(filepath.Join(project.Path, file.Path)), version: 1, content: file.Content, saved: file.savedContent, servers: map[*pencodeLanguageSession]pencodeLanguageSynced{session: {version: 1}}, diagnostics: make(map[*pencodeLanguageSession][]lsp.Diagnostic)}
	model.languages.servers = []*extensions.LanguageServer{server}
	model.languages.sessions[session.key] = session
	model.languages.documents[file] = doc
	if n := testing.AllocsPerRun(100, func() { model.pollLanguageServers(nil) }); n != 0 {
		t.Fatalf("idle LSP polling allocated %g", n)
	}
}
func TestLanguageResultCannotOverwriteChangedDocument(t *testing.T) {
	model, project, file := languageTestModel(t)
	session := &pencodeLanguageSession{key: pencodeLanguageKey{root: project.Path}, client: &lsp.Client{Encoding: lsp.UTF16}}
	doc := &pencodeLanguageDocument{file: file, content: file.Content, version: 1, seen: true}
	model.languages.panel.token = 7
	job := pencodeLanguageJob{document: doc, content: doc.content, version: 1, token: 7, method: "textDocument/formatting"}
	file.Content = "new host content"
	model.acceptLanguageResult(nil, pencodeLanguageEvent{session: session, job: job, result: json.RawMessage(`[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"stale"}]`)})
	if file.Content != "new host content" || !strings.Contains(model.languages.panel.body, "改变") {
		t.Fatal("stale result changed the host document")
	}
}

func TestLanguageQueuedSnapshotsCoalesceWithoutRetainingOldVersions(t *testing.T) {
	model, project, file := languageTestModel(t)
	server := &extensions.LanguageServer{LanguageServer: peb.LanguageServer{ID: "extensions/test/test", LanguageID: "go", Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".go"}}}}
	session := &pencodeLanguageSession{key: pencodeLanguageKey{project.Path, server.ID}, server: server, client: &lsp.Client{Encoding: lsp.UTF16}, jobs: make(chan pencodeLanguageJob, 64), cancel: func() {}}
	model.languages.servers = []*extensions.LanguageServer{server}
	model.languages.sessions[session.key] = session
	model.syncLanguageDocuments()
	for range 100 {
		file.Content += "a"
		model.syncLanguageDocuments()
	}
	if len(session.jobs) != 1 {
		t.Fatalf("queued %d snapshots", len(session.jobs))
	}
	job := (<-session.jobs).update.take()
	if job.content != file.Content || job.version != 101 {
		t.Fatalf("queued snapshot not current: %d", job.version)
	}
	file.Content += "b"
	model.syncLanguageDocuments()
	if len(session.jobs) != 1 {
		t.Fatal("consumed snapshot could not be replaced")
	}
}

func TestLanguageResultsUseNativeControls(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	model.importLanguageServers([]extensions.LanguageServer{{LanguageServer: peb.LanguageServer{ID: "extensions/test/other", Label: "Other", LanguageID: "other", Command: "unused", Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".other"}}}}})
	markPencodeRootDirtyByModel(model.fontResources, model)
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"status-problems", "status-output"} {
		if _, ok := model.fontResources.HTMLControl(model.surface, id); !ok {
			t.Fatal("missing language control", id)
		}
	}
	model.languages.panel.method = "textDocument/rename"
	model.languages.panel.open = true
	model.languages.panel.title = "重命名"
	markPencodeRootDirtyByModel(model.fontResources, model)
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	if _, ok := model.fontResources.HTMLControl(model.surface, "lsp-query"); !ok {
		t.Fatal("missing rename input")
	}
}

func TestLanguagePopupContentVisibility(t *testing.T) {
	app, model, _, _ := languageInteractionFixture(t)
	panel := &model.languages.panel
	rows := make([]pencodeLanguageRow, len(pencodeLanguageRowRefs)+1)
	for i := range rows {
		rows[i].label = "main.go:1:1"
	}
	for _, completion := range []bool{false, true} {
		panel.open, panel.completion = !completion, completion
		panel.method = "textDocument/references"
		for _, count := range []int{1, 12, 13, 1, 0} {
			panel.rows = rows[:count]
			for _, body := range []string{"", "请求中…", ""} {
				panel.body = body
				markPencodeRootDirtyByModel(model.fontResources, model)
				languageInteractionStep(t, app)
				for _, expected := range []struct {
					ref     string
					visible bool
				}{
					{"lsp-body", !completion && body != ""},
					{"lsp-pagination", !completion && count > len(pencodeLanguageRowRefs)},
					{"lsp-completion-pagination", completion && count > len(pencodeCompletionRefs)},
				} {
					node, ok := model.fontResources.HTMLNode(model.surface, expected.ref)
					if !ok {
						t.Fatal("missing popup content", expected.ref)
					}
					if _, visible := node.Bounds(); visible != expected.visible {
						t.Fatalf("completion=%v rows=%d body=%q: %s visible=%v, want %v", completion, count, body, expected.ref, visible, expected.visible)
					}
				}
			}
		}
	}
}
