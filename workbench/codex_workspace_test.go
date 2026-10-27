package workbench

import (
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
	"github.com/pencode-labs/pencode/server"
)

func TestCodexStatusBarOpensWorkspaceWhenCLIUnavailable(t *testing.T) {
	t.Setenv("PENCODE_CODEX_PATH", filepath.Join(t.TempDir(), "missing-codex"))
	testCodexStatusBarWorkspace(t, "")
}

func TestCodexStatusBarReopensWorkspaceWhileStartupPending(t *testing.T) {
	t.Setenv("PENCODE_CODEX_PATH", codexFixtureExecutable(t))
	t.Setenv("PENCODE_CODEX_FIXTURE", "1")
	gate := filepath.Join(t.TempDir(), "start-codex")
	t.Setenv("PENCODE_CODEX_STARTUP_GATE", gate)
	testCodexStatusBarWorkspace(t, gate)
}

func TestCodexWorkspaceFollowsActiveProject(t *testing.T) {
	t.Setenv("PENCODE_CODEX_PATH", codexFixtureExecutable(t))
	t.Setenv("PENCODE_CODEX_FIXTURE", "1")
	t.Setenv("PENCODE_CODEX_HISTORY_PROJECTS", "1")
	testCodexStatusBarWorkspace(t, "")
}

func codexFixtureExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex-fixture")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	build := exec.Command("go", "test", "-c", "-o", path, ".")
	build.Dir = "../extensions/codex"
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Codex fixture: %v\n%s", err, output)
	}
	return path
}

func testCodexStatusBarWorkspace(t *testing.T, startupGate string) {
	t.Helper()
	installed := t.TempDir()
	directory := filepath.Join(installed, "codex")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(directory, "resources"), os.DirFS("../extensions/codex/resources")); err != nil {
		t.Fatal(err)
	}
	entry := "bin/codex-agent"
	if runtime.GOOS == "windows" {
		entry += ".exe"
		if err := os.WriteFile(filepath.Join(directory, "resources", "extension.json"), []byte(`{"id":"codex","entry":"`+entry+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	build := exec.Command("go", "build", "-o", filepath.Join(directory, filepath.FromSlash(entry)), ".")
	build.Dir = "../extensions/codex"
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Codex extension: %v\n%s", err, output)
	}
	app, model, _, state := languageInteractionFixture(t)
	if startupGate != "" {
		// Match a workspace where Codex is the only editor tab.
		model.closeTab(model.activePath)
		markPencodeRootDirtyByModel(model.fontResources, model)
		languageInteractionStep(t, app)
	}
	a, b := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(t.Context(), b, server.Options{ExtensionDirectory: installed})
	}()
	backend, err := client.New(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { backend.Close(); <-done })
	bundle, err := extensions.Load(t.Context(), backend, "codex")
	if err != nil {
		t.Fatal(err)
	}
	model.importActions(bundle.Actions)
	t.Cleanup(func() { model.removeActions("codex") })
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			languageInteractionStep(t, app)
			if ready() {
				return
			}
			time.Sleep(time.Millisecond)
		}
		logs, _ := bundle.Actions.Output.Snapshot()
		t.Fatalf("Codex did not open: %s; logs=%s", model.extensionFeedback, logs)
	}
	button := "action-extensions/codex/chat"
	wait(func() bool {
		_, ok := model.fontResources.HTMLControl(model.surface, button)
		return ok
	})
	editorContextClick(t, app, model, state, button)
	wait(func() bool { return model.actions.ui.editor != nil })
	surface := model.actions.ui.editor
	if surface.ui.Kind != peb.EditorTab || surface.owner != "codex" {
		t.Fatal("Codex button did not open its editor tab")
	}
	waitControl := func(surface *pencodeExtensionSurface, ref string) string {
		t.Helper()
		id := ""
		for _, control := range surface.focus {
			if control.ref == ref {
				id = control.id
				break
			}
		}
		if id == "" {
			t.Fatalf("missing control %s", ref)
		}
		wait(func() bool {
			_, ok := model.fontResources.HTMLControl(model.surface, id)
			return ok
		})
		return id
	}
	if startupGate != "" {
		for range 2 {
			waitControl(surface, "prompt")
			editorContextClick(t, app, model, state, "file-tab-close:"+surface.rootID)
			if model.actions.ui.editor != nil || len(model.actions.ui.surfaces) != 0 {
				t.Fatal("closing the Codex tab did not remove it")
			}
			editorContextClick(t, app, model, state, button)
			wait(func() bool { return model.actions.ui.editor != nil })
			surface = model.actions.ui.editor
			waitControl(surface, "prompt")
			editorContextBounds(t, model, surface.rootID)
		}
		if err := os.WriteFile(startupGate, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		wait(func() bool { return strings.Contains(surface.view.HTML, "Saved task") })
		return
	}
	if os.Getenv("PENCODE_CODEX_HISTORY_PROJECTS") == "1" {
		first := model.activeProjectState()
		wait(func() bool { return strings.Contains(surface.view.HTML, `data-ref="history/`+first.Path+`/saved"`) })
		surface.send(peb.EditorEvent{Kind: "input", Ref: "prompt", Value: "first project draft"}, "")
		second := &pencodeProject{Path: t.TempDir(), Name: "Second project", Files: make(map[string]*openFile)}
		model.projects = append(model.projects, second)
		for _, project := range []*pencodeProject{second, first, second} {
			model.activateProject(project)
			markPencodeRootDirtyByModel(model.fontResources, model)
			wait(func() bool {
				return strings.Contains(surface.view.HTML, `data-ref="history/`+project.Path+`/saved"`)
			})
			other := first
			if project == first {
				other = second
				if !strings.Contains(surface.view.HTML, ">first project draft</textarea>") {
					t.Fatal("returning to the project lost its draft")
				}
			}
			if strings.Contains(surface.view.HTML, `data-ref="history/`+other.Path+`/saved"`) || model.actions.ui.editor != surface {
				t.Fatal("project switch mixed histories or replaced the Codex tab")
			}
		}
		// A history refresh for a hidden tab must preserve the selected file.
		model.actions.ui.editor = nil
		model.activateProject(first)
		markPencodeRootDirtyByModel(model.fontResources, model)
		wait(func() bool { return strings.Contains(surface.view.HTML, `data-ref="history/`+first.Path+`/saved"`) })
		if model.actions.ui.editor != nil {
			t.Fatal("project change revealed the hidden Codex tab")
		}
		if allocations := testing.AllocsPerRun(100, func() { model.syncExtensionProject(model.projectPath) }); allocations != 0 {
			t.Fatalf("unchanged project allocated: %g", allocations)
		}
		return
	}
	wait(func() bool { return strings.Contains(surface.view.HTML, "missing-codex") })
	file := "codex-context.txt"
	if err := os.WriteFile(filepath.Join(model.projectPath, file), []byte("additional context"), 0o600); err != nil {
		t.Fatal(err)
	}
	editorContextClick(t, app, model, state, waitControl(surface, "add-file"))
	wait(func() bool {
		picker := model.actions.ui.overlay
		return picker != nil && picker.ui.ID == "files" && strings.Contains(picker.view.HTML, file)
	})
	picker := model.actions.ui.overlay
	if picker.ui.Kind != peb.Dialog {
		t.Fatal("plus did not open the file picker")
	}
	entries, err := os.ReadDir(model.projectPath)
	if err != nil {
		t.Fatal(err)
	}
	for index, entry := range entries {
		if entry.Name() == file {
			editorContextClick(t, app, model, state, waitControl(picker, "file/"+strconv.Itoa(index)))
			break
		}
	}
	wait(func() bool {
		return model.actions.ui.overlay == nil && strings.Contains(surface.view.HTML, `data-ref="remove-file/0"`)
	})
	editorContextClick(t, app, model, state, waitControl(surface, "remove-file/0"))
	wait(func() bool { return !strings.Contains(surface.view.HTML, `data-ref="remove-file/0"`) })
	for range 2 {
		editorContextClick(t, app, model, state, "file-tab-close:"+surface.rootID)
		if model.actions.ui.editor != nil {
			t.Fatal("closing the Codex tab left it selected")
		}
		editorContextClick(t, app, model, state, button)
		wait(func() bool { return model.actions.ui.editor != nil })
		surface = model.actions.ui.editor
		wait(func() bool { return strings.Contains(surface.view.HTML, "missing-codex") })
		waitControl(surface, "prompt")
	}
}

func TestCodexWorkspaceColumnsRetainInputAndFollowTranscript(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	css, err := os.ReadFile("../extensions/codex/resources/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	width, height := 1440, 900
	m.app.GetViewport = func() (int, int) { return width, height }
	process := &extensions.ActionSession{}
	show := func(key string, lines int) {
		t.Helper()
		ecs.InsertResource(app.World, scene.ViewportSize{Width: float32(width), Height: float32(height), ScaleFactor: 1})
		err := m.applyExtensionUI("codex", process, peb.UI{ID: "composer", Title: key, Kind: peb.EditorTab, View: &peb.EditorView{
			HTML: `<main id="pencode-extension-codex-workspace" class="codex-workspace" data-pencode-layout="fill"><section id="pencode-extension-codex-chat-pane" class="codex-chat-pane"><div class="codex-transcript" data-ref="transcript" data-pencode-scroll="end" data-pencode-scroll-key="` + key + `"><article class="codex-chat">` + strings.Repeat(`<p>streamed output</p>`, lines) + `</article></div><section class="codex-composer"><form id="pencode-extension-codex-input-card" class="codex-input-card"><textarea data-ref="prompt" class="codex-prompt">draft</textarea><div class="codex-input-footer"><button type="button">+</button><button type="button">请批准</button><span class="codex-input-spacer"></span><button type="button">默认</button><button type="button">发送</button></div></form></section></section><aside id="pencode-extension-codex-sidebar" class="codex-sidebar"><section class="codex-history"><div class="codex-history-header"><h3>当前项目的会话</h3><button id="pencode-extension-codex-refresh" class="codex-icon-button" type="button" title="刷新会话" aria-label="刷新会话"><img src="resources/icons/refresh-cw.svg" alt=""></button></div><div class="codex-history-list" data-ref="history-list" data-pencode-scroll="preserve" data-pencode-scroll-key="project">` + strings.Repeat(`<button class="codex-history-row" type="button">Saved conversation</button>`, 60) + `</div></section></aside></main>`,
			CSS:  string(css), Events: []peb.EditorBinding{{Ref: "prompt", Kind: "input"}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(w, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
	}
	scrollAt := func(id string) *scene.UIScroll {
		t.Helper()
		node, ok := w.HTMLNode(m.surface, id)
		if !ok {
			t.Fatalf("scroll area %s missing", id)
		}
		area, ok := node.ScrollContainer()
		if !ok {
			t.Fatalf("%s has no scroll container", id)
		}
		return module.ContentMut[scene.UIScroll](w, area)
	}
	scroll := func() *scene.UIScroll { return scrollAt(m.actions.ui.editor.scrollTarget()) }
	checkScrollTheme := func(id string) {
		t.Helper()
		style, ok := w.HTMLScrollStyle(m.surface, id)
		want := pencodeSyntaxColor(m.workbenchTheme.theme.Color("scrollbarSlider.background"))
		if !ok || !style.HasThumbColor || style.ThumbColor != want || !style.HasTrackColor || style.TrackColor.A != 0 {
			t.Fatalf("scrollbar %s does not follow theme %s: %+v, want thumb %v and a transparent track", id, m.workbenchTheme.id, style, want)
		}
		if s := scrollAt(id); !s.ScrollbarThin || s.ScrollbarSize <= 0 {
			t.Fatalf("scrollbar %s does not use the IDE's thin style: %+v", id, s)
		}
	}
	atEnd := func() {
		t.Helper()
		s := scroll()
		if end := s.ContentSize.Y() - s.ViewportSize.Y(); end < 100 || math.Abs(float64(s.Position.Y()+end)) > 1 {
			t.Fatalf("transcript did not follow the latest output: %+v", s)
		}
	}
	show("first task", 100)
	atEnd()
	promptID := m.actions.ui.editor.prefix + "control-prompt"
	control, ok := w.HTMLControl(m.surface, promptID)
	if !ok || !w.Focus(control) {
		t.Fatal("input could not receive focus")
	}
	languageInteractionStep(t, app)
	field := module.ContentMut[engineui.TextField](w, control)
	field.Anchor, field.Caret = 2, 4
	prompt := editorContextBounds(t, m, promptID)
	historyID := m.actions.ui.editor.prefix + "control-history-list"
	scrollAt(historyID).Position[1] = -120
	scroll().Position[1] = -180
	show("first task", 120)
	if scroll().Position.Y() != -180 || scrollAt(historyID).Position.Y() != -120 {
		t.Fatal("streaming displaced a user reading earlier output or history")
	}
	control, ok = w.HTMLControl(m.surface, promptID)
	if !ok || w.FocusedControl() != control {
		t.Fatalf("streaming unfocused the input: control=%v focused=%v", control, w.FocusedControl())
	}
	field = module.ContentMut[engineui.TextField](w, control)
	if field.Value != "draft" || field.Anchor != 2 || field.Caret != 4 {
		t.Fatalf("streaming changed the draft or selection: %+v", field)
	}
	if current := editorContextBounds(t, m, promptID); current != prompt {
		t.Fatalf("transcript scrolling moved the input: before=%+v after=%+v", prompt, current)
	}
	s := scroll()
	s.Position[1] = -(s.ContentSize.Y() - s.ViewportSize.Y())
	show("first task", 140)
	atEnd()
	scroll().Position[1] = 0
	show("second task", 160)
	atEnd()
	if scrollAt(historyID).Position.Y() != -120 {
		t.Fatal("switching conversations reset the project history scroll")
	}
	if len(m.actions.ui.surfaces) != 1 || m.actions.ui.panel != nil || m.bottom.active == pencodePanelExtension {
		t.Fatal("chat created an extra tab or bottom panel")
	}
	for _, size := range [][2]int{{1440, 900}, {1024, 768}, {800, 600}, {640, 800}} {
		width, height = size[0], size[1]
		show("second task", 160)
		workspace := editorContextBounds(t, m, "pencode-extension-codex-workspace")
		transcript := editorContextBounds(t, m, m.actions.ui.editor.scrollTarget())
		sidebar := editorContextBounds(t, m, "pencode-extension-codex-sidebar")
		prompt := editorContextBounds(t, m, promptID)
		chatPane := editorContextBounds(t, m, "pencode-extension-codex-chat-pane")
		card := editorContextBounds(t, m, "pencode-extension-codex-input-card")
		if math.Abs(float64(card.X-chatPane.X-8)) > 1 || math.Abs(float64(chatPane.X+chatPane.Width-card.X-card.Width-8)) > 1 {
			t.Fatalf("composer side spacing is not 8px at %dx%d: card=%+v pane=%+v", width, height, card, chatPane)
		}
		if width <= 720 {
			if math.Abs(float64(workspace.X-transcript.X)) > 1 || math.Abs(float64(chatPane.Y+chatPane.Height-sidebar.Y)) > 1 || sidebar.Width < workspace.Width-1 {
				t.Fatalf("invalid stacked layout at %dx%d: workspace=%+v chatPane=%+v sidebar=%+v", width, height, workspace, chatPane, sidebar)
			}
		} else if transcript.Width < 100 || sidebar.Width < 240 || sidebar.Width > 341 || sidebar.Width >= transcript.Width || math.Abs(float64(transcript.X+transcript.Width-sidebar.X)) > 1 || math.Abs(float64(sidebar.X+sidebar.Width-workspace.X-workspace.Width)) > 1 {
			t.Fatalf("invalid columns at %dx%d: workspace=%+v transcript=%+v sidebar=%+v", width, height, workspace, transcript, sidebar)
		}
		if prompt.X < chatPane.X || prompt.X+prompt.Width > chatPane.X+chatPane.Width+1 || prompt.Y < chatPane.Y || prompt.Y+prompt.Height > chatPane.Y+chatPane.Height || prompt.Y < transcript.Y+transcript.Height-1 {
			t.Fatalf("input is not docked at the bottom of the chat pane at %dx%d: prompt=%+v pane=%+v transcript=%+v", width, height, prompt, chatPane, transcript)
		}
	}
	for _, theme := range []string{"dark-plus", "light-plus"} {
		m.selectTheme(theme)
		markPencodeRootDirtyByModel(w, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
		checkScrollTheme(m.actions.ui.editor.scrollTarget())
		checkScrollTheme(historyID)
	}
}
