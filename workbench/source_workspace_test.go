package workbench

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/diiyw/onix"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

func sourceWait(t *testing.T, app *onix.App, m *workspaceModel) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for m.sourceState().Done != nil && time.Now().Before(deadline) {
		languageInteractionStep(t, app)
		time.Sleep(time.Millisecond)
	}
	// Interaction tests drive refresh explicitly between input events.
	m.sourceState().RefreshStarted, m.sourceState().RefreshPending = true, false
	languageInteractionStep(t, app)
	if m.sourceState().Done != nil {
		t.Fatal("Git job did not complete")
	}
	if notice := m.sourceState().Notice; notice != "" {
		t.Fatal(notice)
	}
}

func TestSourceControlStatusCountWithoutPanel(t *testing.T) {
	r := sourceTestRepository(t)
	name := "resources/main.go"
	sourceTestWrite(t, r, name, "package demo\n// base\n")
	sourceTestGit(t, r, "add", "--all")
	sourceTestGit(t, r, "commit", "-m", "base")
	sourceTestWrite(t, r, name, "package demo\n// staged\n")
	sourceTestGit(t, r, "add", "--all")
	sourceTestWrite(t, r, name, "package demo\n// working\n")
	sourceTestWrite(t, r, "resources/new.go", "package demo\n")
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", name)
	app, m, _, state := languageInteractionFixture(t)
	sourceWait(t, app, m)
	s := m.sourceState()
	if m.bottom.active == pencodePanelSourceControl || s.changeCountText != "2" || len(s.Snapshot.Groups[1]) != 1 || len(s.Snapshot.Groups[2]) != 2 {
		t.Fatalf("closed panel count=%q, files=%+v", s.changeCountText, s.Snapshot.Files)
	}
	if len(s.Snapshot.Commits) != 0 {
		t.Fatal("closed panel loaded commit history")
	}
	editorContextClick(t, app, m, state, "status-source-control")
	sourceWait(t, app, m)
	if len(s.Snapshot.Commits) != 1 || s.changeCountText != "2" {
		t.Fatal("opening the panel did not load history or changed the unique file count")
	}
	editorContextClick(t, app, m, state, "status-source-control")
	sourceTestGit(t, r, "reset", "--hard", "HEAD")
	if err := os.Remove(filepath.Join(r.Root, "resources/new.go")); err != nil {
		t.Fatal(err)
	}
	s.RefreshPending = true
	languageInteractionStep(t, app)
	sourceWait(t, app, m)
	if s.changeCountText != "0" || len(s.Snapshot.Commits) != 1 || s.Snapshot.Branch != "main" {
		t.Fatal("closed panel refresh did not clear the count or retain loaded history")
	}
}

func TestSourceControlPencodeInteraction(t *testing.T) {
	r := sourceTestRepository(t)
	name := "resources/space name.go"
	sourceTestWrite(t, r, name, "package demo\n// original\n")
	sourceTestGit(t, r, "add", "--all")
	sourceTestGit(t, r, "commit", "-m", "base")
	sourceTestWrite(t, r, name, "package demo\n// changed\n")
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", name)
	app, m, _, state := languageInteractionFixture(t)
	editorContextClick(t, app, m, state, "status-source-control")
	if m.bottom.active != pencodePanelSourceControl {
		t.Fatal("Source Control status button did not open Git")
	}
	sourceWait(t, app, m)
	s := m.sourceState()
	if len(s.Snapshot.Files) != 1 || len(s.Snapshot.Commits) != 1 {
		t.Fatalf("snapshot=%+v", s.Snapshot)
	}
	message, _ := m.fontResources.HTMLNode(m.surface, "sc-message")
	stage, _ := m.fontResources.HTMLNode(m.surface, "sc-stage:"+url.PathEscape(name))
	for _, panel := range []pencodePanel{pencodePanelClosed, pencodePanelSourceControl} {
		m.bottom.active = panel
		m.sourceChanged()
		languageInteractionStep(t, app)
	}
	if current, ok := m.fontResources.HTMLNode(m.surface, "sc-message"); !ok || current != message {
		t.Fatal("reopening replaced the native commit input")
	}
	if current, ok := m.fontResources.HTMLNode(m.surface, "sc-stage:"+url.PathEscape(name)); !ok || current != stage || s.Handlers["sc-stage:"+url.PathEscape(name)].Node != stage {
		t.Fatal("reopening replaced the staging button or lost its binding")
	}
	area := editorContextBounds(t, m, "bottom-panel")
	panel := editorContextBounds(t, m, "sc-panel")
	if panel.Height < 200 || panel.Width > area.Width+1 {
		t.Fatalf("source layout area=%+v panel=%+v", area, panel)
	}
	editorContextClick(t, app, m, state, "sc-message")
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "reviewed"})
	languageInteractionStep(t, app)
	if s.CommitMessage != "reviewed" {
		t.Fatalf("native commit input=%q", s.CommitMessage)
	}
	clickFileAction := func(id string) {
		t.Helper()
		scale := float64(m.settings.Zoom) / 100
		kind := "changes"
		if strings.HasPrefix(id, "sc-unstage:") {
			kind = "staged"
		}
		for _, target := range []string{"sc-open:" + kind + ":" + url.PathEscape(name), id} {
			bounds := editorContextBounds(t, m, target)
			state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(bounds.X+bounds.Width/2) * scale, Y: float64(bounds.Y+bounds.Height/2) * scale})
			languageInteractionStep(t, app)
		}
		editorContextClick(t, app, m, state, id)
	}
	clickFileAction("sc-stage:" + url.PathEscape(name))
	sourceWait(t, app, m)
	if len(s.Snapshot.Groups[1]) != 1 || len(s.Snapshot.Groups[2]) != 0 {
		t.Fatal("staging did not move file")
	}
	clickFileAction("sc-unstage:" + url.PathEscape(name))
	sourceWait(t, app, m)
	if len(s.Snapshot.Groups[1]) != 0 || len(s.Snapshot.Groups[2]) != 1 {
		t.Fatal("unstaging did not move file")
	}
	editorContextClick(t, app, m, state, "sc-open:changes:"+url.PathEscape(name))
	sourceWait(t, app, m)
	if s.activeTab() == nil || s.activeTab().Kind != "changes" || m.textEditorActive() {
		t.Fatal("diff did not open as active editor tab")
	}
	left := editorContextBounds(t, m, "sc-code-left")
	right := editorContextBounds(t, m, "sc-code-right")
	if left.Width < 100 || right.X < left.X+left.Width-1 || right.Height < 100 {
		t.Fatalf("diff panes left=%+v right=%+v", left, right)
	}
	if len(s.activeTab().Rows) != 2 || s.activeTab().Rows[1].LeftKind != sourceDeleted || s.activeTab().Rows[1].RightKind != sourceAdded {
		t.Fatal(s.activeTab().Rows)
	}
	m.selectProjectTab(m.activeProjectState(), name)
	m.sourceChanged()
	languageInteractionStep(t, app)
	if !m.textEditorActive() || s.visible() {
		t.Fatal("ordinary file tab did not restore editor")
	}
	editorContextClick(t, app, m, state, "sc-tab:"+s.Tabs[0].Key)
	if s.activeTab() == nil {
		t.Fatal("comparison tab did not reactivate")
	}
	m.selectProjectTab(m.activeProjectState(), name)
	m.sourceChanged()
	languageInteractionStep(t, app)
	sourceWait(t, app, m)
	hash := s.Snapshot.Commits[0].Hash
	editorContextClick(t, app, m, state, "sc-commit:"+hash)
	sourceWait(t, app, m)
	if s.SelectedHash != hash || len(s.CommitFiles) != 1 {
		t.Fatal("commit file listing not selected")
	}
	commitBounds := editorContextBounds(t, m, "sc-commit:"+hash)
	filesBounds := editorContextBounds(t, m, "sc-commit-files")
	filesNode, _ := m.fontResources.HTMLNode(m.surface, "sc-commit-files")
	if _, ok := filesNode.ScrollContainer(); ok {
		t.Fatal("commit files must use the graph scrollbar instead of a nested scroll container")
	}
	commitEntity, _ := m.fontResources.HTMLControl(m.surface, "sc-commit:"+hash)
	if control, ok := m.fontResources.Get[htmlui.Control](commitEntity); !ok || control.PaddingBox.Y != 0 || control.PaddingBox.Height != commitBounds.Height {
		t.Fatal("commit button clips the graph before its row boundaries")
	}
	if filesBounds.Y != commitBounds.Y+commitBounds.Height || filesBounds.Height != float32(s.graphDetailsHeight()) {
		t.Fatalf("commit files are not inline below their message: commit=%+v files=%+v", commitBounds, filesBounds)
	}
	editorContextClick(t, app, m, state, "sc-commit:"+hash)
	if _, ok := m.fontResources.HTMLNode(m.surface, "sc-commit-files"); ok || s.SelectedHash != "" {
		t.Fatal("clicking the expanded commit did not collapse its files")
	}
	editorContextClick(t, app, m, state, "sc-commit:"+hash)
	sourceWait(t, app, m)
	editorContextClick(t, app, m, state, "sc-open:history:"+url.PathEscape(name))
	sourceWait(t, app, m)
	if s.activeTab().Kind != "history" || len(s.Tabs) != 2 {
		t.Fatal("history did not create a distinct tab")
	}
	m.selectProjectTab(m.activeProjectState(), name)
	m.sourceChanged()
	languageInteractionStep(t, app)
	sourceWait(t, app, m)
	bounds := editorContextBounds(t, m, "sc-commit:"+hash)
	zoom := float32(m.settings.Zoom) / 100
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64((bounds.X + 10) * zoom), Y: float64((bounds.Y + 10) * zoom)})
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseRight, X: float64((bounds.X + 10) * zoom), Y: float64((bounds.Y + 10) * zoom)})
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseRight, X: float64((bounds.X + 10) * zoom), Y: float64((bounds.Y + 10) * zoom)})
	languageInteractionStep(t, app)
	if !s.MenuOpen || s.MenuHash != hash {
		t.Fatal("right click did not open Git context menu")
	}
	editorContextClick(t, app, m, state, "sc-op:branch")
	if s.Prompt != "branch" {
		t.Fatal("context action did not prompt")
	}
	editorContextClick(t, app, m, state, "sc-prompt-value")
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "review-branch"})
	languageInteractionStep(t, app)
	if s.PromptValue != "review-branch" {
		t.Fatalf("native branch input=%q", s.PromptValue)
	}
	editorContextClick(t, app, m, state, "sc-prompt-confirm")
	sourceWait(t, app, m)
	if s.Snapshot.Branch != "review-branch" {
		t.Fatalf("branch=%s", s.Snapshot.Branch)
	}
	editorContextClick(t, app, m, state, "sc-stage:"+url.PathEscape(name))
	sourceWait(t, app, m)
	editorContextClick(t, app, m, state, "sc-create-commit")
	sourceWait(t, app, m)
	if len(s.Snapshot.Commits) != 2 || len(s.Snapshot.Files) != 0 || s.CommitMessage != "" {
		t.Fatal("commit did not refresh graph and clear input")
	}
	messageEntity, _ := m.fontResources.HTMLControl(m.surface, "sc-message")
	messageField, ok := m.fontResources.Get[engineui.TextField](messageEntity)
	if !ok || messageField.Text().String() != "" || messageField.Anchor != 0 || messageField.Caret != 0 {
		t.Fatalf("successful commit retained the native input value or selection: %+v", messageField)
	}
	m.app.GetViewport = func() (int, int) { return 960, 540 }
	app.World.InsertResource(scene.ViewportSize{Width: 960, Height: 540, ScaleFactor: 1})
	for _, camera := range app.World.Query[scene.Camera]().All {
		camera.Mut().UpdateViewport(960, 540)
	}
	m.sourceChanged()
	languageInteractionStep(t, app)
	panel = editorContextBounds(t, m, "sc-panel")
	status := editorContextBounds(t, m, "statusbar")
	if panel.Height < 100 || panel.Y+panel.Height > status.Y+1 {
		t.Fatalf("small viewport panel=%+v status=%+v", panel, status)
	}
	s.RefreshStarted, s.RefreshPending = true, false
	for range 3 {
		languageInteractionStep(t, app)
	}
	if n := testing.AllocsPerRun(100, func() {
		m.updateSourceControl(m.fontResources, nil)
		m.bindSourceControls(m.fontResources)
		m.syncTabScroll(m.fontResources)
	}); n != 0 {
		t.Fatalf("Git idle allocated %g times", n)
	}
}

func TestSourceControlCommitMessageModes(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestWrite(t, r, "resources/main.go", "package demo\n")
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", "resources/main.go")
	app, m, _, state := languageInteractionFixture(t)
	editorContextClick(t, app, m, state, "status-source-control")
	sourceWait(t, app, m)
	s := m.sourceState()
	entity, _ := m.fontResources.HTMLControl(m.surface, "sc-message")
	if _, ok := m.fontResources.Get[engineui.Input](entity); !ok {
		t.Fatal("commit message must start as a single-line input")
	}
	compact := editorContextBounds(t, m, "sc-message")
	commit := editorContextBounds(t, m, "sc-create-commit")
	if commit.Width != compact.Width || commit.Y < compact.Y+compact.Height {
		t.Fatalf("commit button must fill the row below the message: input=%+v button=%+v", compact, commit)
	}
	editorContextClick(t, app, m, state, "sc-message")
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "修复内容"})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEnter})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEnter})
	languageInteractionStep(t, app)
	if s.MessageMultiline || s.CommitMessage != "修复内容" {
		t.Fatal("plain Enter expanded or changed the single-line message")
	}
	field := m.fontResources.Mut[engineui.TextField](entity)
	field.Anchor, field.Caret = 2, 4
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyLeftShift})
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEnter})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEnter})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyLeftShift})
	languageInteractionStep(t, app)
	entity, _ = m.fontResources.HTMLControl(m.surface, "sc-message")
	if _, ok := m.fontResources.Get[engineui.Textarea](entity); !ok {
		t.Fatal("Shift+Enter did not switch to a textarea")
	}
	field = m.fontResources.Mut[engineui.TextField](entity)
	if !field.Focused || field.Anchor != 3 || field.Caret != 3 || field.Text().String() != "修复\n" || s.CommitMessage != field.Text().String() {
		t.Fatalf("expansion lost selection, focus or message: %+v, message=%q", field, s.CommitMessage)
	}
	expanded := editorContextBounds(t, m, "sc-message")
	if expanded.Height <= compact.Height {
		t.Fatal("multiline input did not expand")
	}
	press := func(key input.KeyCode) {
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
		languageInteractionStep(t, app)
	}
	press(input.KeyEnter)
	threeLines := editorContextBounds(t, m, "sc-message")
	if s.CommitMessage != "修复\n\n" || threeLines.Height <= expanded.Height {
		t.Fatal("message height did not grow with its line count")
	}
	press(input.KeyBackspace)
	if bounds := editorContextBounds(t, m, "sc-message"); s.CommitMessage != "修复\n" || bounds.Height != expanded.Height {
		t.Fatal("deleting a newline did not shrink the multiline input")
	}
	press(input.KeyBackspace)
	entity, _ = m.fontResources.HTMLControl(m.surface, "sc-message")
	field = m.fontResources.Mut[engineui.TextField](entity)
	if s.MessageMultiline || s.CommitMessage != "修复" || !field.Focused || field.Anchor != 2 || field.Caret != 2 {
		t.Fatalf("deleting the last newline lost single-line content or focus: %+v", field)
	}
	if bounds := editorContextBounds(t, m, "sc-message"); bounds.Height != compact.Height {
		t.Fatal("deleting the last newline did not restore the compact height")
	}
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyLeftShift})
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEnter})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEnter})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyLeftShift})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "详细说明"})
	languageInteractionStep(t, app)
	if s.CommitMessage != "修复\n详细说明" {
		t.Fatalf("continued multiline input=%q", s.CommitMessage)
	}
	editorContextClick(t, app, m, state, "sc-create-commit")
	sourceWait(t, app, m)
	if !s.Snapshot.HasHead || len(s.Snapshot.Files) != 0 || s.CommitMessage != "" || s.MessageMultiline {
		t.Fatal("smart commit did not auto-stage or reset the input")
	}
	entity, _ = m.fontResources.HTMLControl(m.surface, "sc-message")
	if _, ok := m.fontResources.Get[engineui.Input](entity); !ok {
		t.Fatal("successful commit did not restore the single-line control")
	}
	if field, ok := m.fontResources.Get[engineui.TextField](entity); !ok || field.Text().String() != "" {
		t.Fatalf("successful multiline commit retained the native input value: %+v", field)
	}
	// Exercise the real layout with long subjects, refs and authors, without a
	// native window or screenshots. Text must truncate inside its graph row.
	c := &s.Snapshot.Commits[0]
	c.Subject = "long-subject-" + strings.Repeat("wide text ", 50)
	c.Author = strings.Repeat("author", 20)
	c.Refs = strings.Repeat("origin/topic ", 20)
	s.TreeRevision++
	m.sourceChanged()
	languageInteractionStep(t, app)
	graph := editorContextBounds(t, m, "sc-graph")
	row := editorContextBounds(t, m, "sc-commit:"+c.Hash)
	if row.X+row.Width > graph.X+graph.Width || row.Height != sourceGraphRowHeight {
		t.Fatalf("long message overflowed its graph: graph=%+v row=%+v", graph, row)
	}
	prefix, truncated := false, false
	for _, text := range app.World.Query[scene.Text2D]().All {
		value := text.Get().Text().String()
		prefix = prefix || strings.HasPrefix(value, "long-subject-")
		truncated = truncated || strings.HasSuffix(value, "…") && strings.Contains(c.Subject, strings.TrimSuffix(value, "…"))
	}
	if !prefix || !truncated {
		t.Fatal("long graph subject did not render an ellipsis")
	}
}

func TestSourceControlPencodeMergeEditor(t *testing.T) {
	r := sourceTestRepository(t)
	name := "main.go"
	sourceTestWrite(t, r, name, "package demo\n// base\n")
	sourceTestGit(t, r, "add", "--all")
	sourceTestGit(t, r, "commit", "-m", "base")
	sourceTestGit(t, r, "switch", "-c", "incoming")
	sourceTestWrite(t, r, name, "package demo\n// incoming\n")
	sourceTestGit(t, r, "commit", "-am", "incoming")
	sourceTestGit(t, r, "switch", "main")
	sourceTestWrite(t, r, name, "package demo\n// local\n")
	sourceTestGit(t, r, "commit", "-am", "local")
	if _, err := r.command("merge", "incoming"); err == nil {
		t.Fatal("expected conflict")
	}
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
	app, m, _, state := languageInteractionFixture(t)
	editorContextClick(t, app, m, state, "status-source-control")
	sourceWait(t, app, m)
	s := m.sourceState()
	// File actions become visible only after hovering their row.
	scale := float64(m.settings.Zoom) / 100
	for _, id := range []string{"sc-open:merge:" + name, "sc-stage:" + name} {
		bounds := editorContextBounds(t, m, id)
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(bounds.X+bounds.Width/2) * scale, Y: float64(bounds.Y+bounds.Height/2) * scale})
		languageInteractionStep(t, app)
	}
	editorContextClick(t, app, m, state, "sc-stage:"+name)
	deadline := time.Now().Add(5 * time.Second)
	for s.Done != nil && time.Now().Before(deadline) {
		languageInteractionStep(t, app)
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(s.Notice, "合并") {
		t.Fatal("unresolved staging did not show merge warning", s.Notice)
	}
	editorContextClick(t, app, m, state, "sc-notice-close")
	editorContextClick(t, app, m, state, "sc-open:merge:"+name)
	sourceWait(t, app, m)
	tab := s.activeTab()
	if tab == nil || tab.Kind != "merge" {
		t.Fatal("merge editor not opened")
	}
	settleSourceHighlight(t, m)
	m.sourceChanged()
	languageInteractionStep(t, app)
	entity, ok := m.fontResources.HTMLControl(m.surface, "sc-merge-result")
	if field := m.fontResources.Mut[engineui.Textarea](entity); !ok || field == nil || !field.CaptureTab {
		t.Fatal("merge result not editable code textarea")
	}
	text, found := m.fontResources.FindPart[engineui.ValuePart, scene.Text2D](entity)
	if value := m.fontResources.Ref[scene.Text2D](text); !found || value == nil || len(value.Spans) == 0 || value.Text().String() != tab.Result {
		t.Fatal("native merge result lacks syntax highlighting")
	}
	left := editorContextBounds(t, m, "sc-code-left")
	result := editorContextBounds(t, m, "sc-merge-result")
	right := editorContextBounds(t, m, "sc-code-right")
	if left.Width < 100 || result.X < left.X+left.Width-1 || right.X < result.X+result.Width-1 {
		t.Fatalf("three panes: left=%+v result=%+v right=%+v", left, result, right)
	}
	editorContextClick(t, app, m, state, "sc-resolve:0:both")
	if sourceHasMarkers(tab.Result) || !strings.Contains(tab.Result, "// local\n// incoming") {
		t.Fatal(tab.Result)
	}
	before := tab.Result
	editorContextClick(t, app, m, state, "sc-merge-result")
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "// manual"})
	languageInteractionStep(t, app)
	if tab.Result == before || !strings.Contains(tab.Result, "// manual") {
		t.Fatal("native merge input not applied", tab.Result)
	}
	if !tab.undoResult(false) || tab.Result != before {
		t.Fatal("merge undo failed")
	}
	if !tab.undoResult(true) || !strings.Contains(tab.Result, "// manual") {
		t.Fatal("merge redo failed")
	}
	m.sourceChanged()
	languageInteractionStep(t, app)
	if m.requestWindowClose() {
		t.Fatal("window closed with unsaved merge")
	}
	languageInteractionStep(t, app)
	editorContextClick(t, app, m, state, "sc-notice-close")
	editorContextClick(t, app, m, state, "sc-save-merge")
	sourceWait(t, app, m)
	if s.unsaved() || len(s.Snapshot.Groups[0]) != 0 || len(s.Snapshot.Groups[1]) != 1 {
		t.Fatal("merge was not saved and staged")
	}
}

func TestSourceControlProjectIsolationAndLateResult(t *testing.T) {
	one := &pencodeProject{Path: "/one", Files: map[string]*openFile{"main.go": {Path: "main.go", Name: "main.go"}}, ActivePath: "main.go"}
	two := &pencodeProject{Path: "/two"}
	m := &workspaceModel{projects: []*pencodeProject{one, two}, activeProjectIndex: 0, projectPath: one.Path}
	t.Cleanup(func() { one.sourceControl.stop(); two.sourceControl.stop() })
	release, finished := make(chan struct{}), make(chan struct{})
	m.sourceJob(sourceQuery, func() sourceResult {
		<-release
		defer close(finished)
		return sourceResult{Tab: &sourceTab{Key: "changes::main.go", Kind: "changes", Path: "main.go"}}
	})
	m.selectProjectTab(one, "main.go")
	m.activeProjectIndex, m.projectPath = 1, two.Path
	close(release)
	<-finished
	deadline := time.Now().Add(time.Second)
	for one.sourceControl.Done != nil && time.Now().Before(deadline) {
		m.updateSourceControl(nil, nil)
		time.Sleep(time.Millisecond)
	}
	if len(one.sourceControl.Tabs) != 1 || one.sourceControl.visible() || two.sourceControl.visible() {
		t.Fatal("late comparison result stole selection or crossed projects")
	}
	if m.projectPath != two.Path || m.sourceState() != &two.sourceControl {
		t.Fatal("Git result activated another project")
	}
}

func TestSourceControlDiffWindowAndIdle(t *testing.T) {
	m := &workspaceModel{}
	s := m.sourceState()
	s.RefreshStarted, s.RefreshPending = true, false
	s.Active = "changes::large.go"
	tab := &sourceTab{Key: s.Active, Path: "large.go", Kind: "changes", Rows: make([]sourceDiffRow, 10000), FirstRow: 400}
	s.Tabs = []*sourceTab{tab}
	b := &s.view
	b.reset()
	m.renderSourceTabs(b)
	count := 0
	for _, group := range b.groups[:b.used] {
		if group.node.Tag == "code" {
			count++
		}
	}
	if count != 320 {
		t.Fatalf("diff should render two bounded windows, got %d code cells", count)
	}
	if n := testing.AllocsPerRun(100, func() { m.updateSourceControl(nil, nil) }); n != 0 {
		t.Fatalf("idle comparison allocated %g times", n)
	}
}

func TestSourceControlBottomPanelAndHistoryLoading(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestWrite(t, r, "main.go", "package demo\n")
	sourceTestGit(t, r, "add", "main.go")
	sourceTestGit(t, r, "commit", "-m", "base")
	for range 74 {
		sourceTestGit(t, r, "commit", "--allow-empty", "-m", "history")
	}
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", "main.go")
	app, m, _, state := languageInteractionFixture(t)
	editorContextClick(t, app, m, state, "status-output")
	outputBounds := editorContextBounds(t, m, "bottom-panel")
	file := m.activeFile()
	editorContextClick(t, app, m, state, "status-source-control")
	sourceWait(t, app, m)
	s := m.sourceState()
	if bounds := editorContextBounds(t, m, "bottom-panel"); bounds != outputBounds || !m.textEditorActive() || m.activeFile() != file {
		t.Fatalf("Git did not preserve Output geometry and current editor: output=%+v git=%+v", outputBounds, bounds)
	}
	if len(s.Snapshot.Commits) != sourceHistoryPageSize || !s.Snapshot.HasMore || s.RestoreGraph {
		t.Fatalf("first history batch: count=%d more=%t restore=%t", len(s.Snapshot.Commits), s.Snapshot.HasMore, s.RestoreGraph)
	}
	graphScroll := func() *scene.UIScroll {
		t.Helper()
		node, ok := m.fontResources.HTMLNode(m.surface, "sc-graph")
		if !ok {
			t.Fatal("missing graph")
		}
		area, ok := node.ScrollContainer()
		if !ok {
			t.Fatal("missing graph scroll container")
		}
		scroll := m.fontResources.Mut[scene.UIScroll](area)
		if scroll == nil || !scroll.ScrollbarThin || scroll.ScrollbarSize <= 0 {
			t.Fatal("graph must have native themed thin scrollbars")
		}
		return scroll
	}
	seen := make(map[string]bool)
	for _, want := range []int{64, 75} {
		scroll := graphScroll()
		scroll.Position[1] = -max(0, scroll.ContentSize.Y()-scroll.ViewportSize.Y())
		scroll.Momentum[1] = 0
		languageInteractionStep(t, app)
		if s.Done == nil {
			t.Fatal("scrolling to history end did not start an asynchronous page")
		}
		sourceWait(t, app, m)
		if len(s.Snapshot.Commits) != want || s.Snapshot.HasMore != (want < 75) {
			t.Fatalf("history batch: got %d commits, more=%t; want %d", len(s.Snapshot.Commits), s.Snapshot.HasMore, want)
		}
		rows := 0
		for _, id := range s.panelView.clicks {
			if strings.HasPrefix(id, "sc-commit:") {
				rows++
			}
		}
		if rows > max(24, s.GraphVisible) || rows >= len(s.Snapshot.Commits) || s.GraphFirst == 0 {
			t.Fatalf("graph did not render a bounded scroll window: rows=%d first=%d", rows, s.GraphFirst)
		}
	}
	for _, c := range s.Snapshot.Commits {
		if seen[c.Hash] {
			t.Fatal("duplicate history commit", c.Hash)
		}
		seen[c.Hash] = true
	}
	position := s.GraphScrollY
	m.refreshSourceControl()
	sourceWait(t, app, m)
	if len(s.Snapshot.Commits) != 75 || s.GraphScrollY != position {
		t.Fatal("unchanged refs discarded loaded history or scroll position")
	}
	// Escape dismisses a commit menu before it closes the shared panel.
	s.MenuHash, s.MenuOpen = s.Snapshot.Commits[0].Hash, true
	m.sourceChanged()
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEscape})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEscape})
	languageInteractionStep(t, app)
	if s.MenuOpen || m.bottom.active != pencodePanelSourceControl {
		t.Fatal("Escape did not dismiss only the Git context menu")
	}
	s.MenuOpen = true
	m.sourceChanged()
	languageInteractionStep(t, app)
	editorContextClick(t, app, m, state, "status-output")
	if m.bottom.active != pencodePanelOutput || s.MenuOpen {
		t.Fatal("switching to Output left a Git menu open")
	}
	editorContextClick(t, app, m, state, "status-source-control")
	sourceWait(t, app, m)
	sourceTestGit(t, r, "commit", "--allow-empty", "-m", "new tip")
	m.refreshSourceControl()
	sourceWait(t, app, m)
	if len(s.Snapshot.Commits) != sourceHistoryPageSize || !s.Snapshot.HasMore || s.GraphFirst != 0 || s.GraphScrollY != 0 {
		t.Fatal("new refs did not reset history pagination to its newest page")
	}
}
