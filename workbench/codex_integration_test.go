//go:build codex_integration

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

	"github.com/diiyw/onix/input"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/internal/extensionbuild"
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

func TestCodexHistorySwitchesConversations(t *testing.T) {
	t.Setenv("PENCODE_CODEX_PATH", codexFixtureExecutable(t))
	t.Setenv("PENCODE_CODEX_FIXTURE", "1")
	t.Setenv("PENCODE_CODEX_HISTORY_SWITCH", "1")
	testCodexStatusBarWorkspace(t, "")
}

func TestCodexModelMenuReopensDuringDismissalUpdate(t *testing.T) {
	t.Setenv("PENCODE_CODEX_PATH", codexFixtureExecutable(t))
	t.Setenv("PENCODE_CODEX_FIXTURE", "1")
	t.Setenv("PENCODE_CODEX_MODEL_MENU", "1")
	testCodexStatusBarWorkspace(t, "")
}

func TestCodexPermissionMenuAnchorsToButton(t *testing.T) {
	t.Setenv("PENCODE_CODEX_PATH", codexFixtureExecutable(t))
	t.Setenv("PENCODE_CODEX_FIXTURE", "1")
	t.Setenv("PENCODE_CODEX_PERMISSION_MENU", "1")
	testCodexStatusBarWorkspace(t, "")
}

func TestCodexComposerCompletions(t *testing.T) {
	t.Setenv("PENCODE_CODEX_PATH", codexFixtureExecutable(t))
	t.Setenv("PENCODE_CODEX_FIXTURE", "1")
	t.Setenv("PENCODE_CODEX_REQUIRE_SKILL", "1")
	t.Setenv("PENCODE_CODEX_COMPLETION", "1")
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
	var err error
	build.Env, err = extensionbuild.Environment(t.TempDir(), "..", build.Dir)
	if err != nil {
		t.Fatal(err)
	}
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
	var err error
	build.Env, err = extensionbuild.Environment(t.TempDir(), "..", build.Dir)
	if err != nil {
		t.Fatal(err)
	}
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
	phase := "open workspace"
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
		t.Fatalf("Codex timed out during %s: %s; logs=%s", phase, model.extensionFeedback, logs)
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
	tabFound := false
	for _, access := range model.fontResources.Query[htmlui.Component[tabProps, struct{}]]() {
		props := access.Ref().Props
		if props.Surface == surface {
			tabFound = true
			if props.Icon != "peb://codex/resources/icons/chatgpt.svg" {
				t.Fatalf("Codex tab icon = %q", props.Icon)
			}
		}
	}
	if !tabFound {
		t.Fatal("Codex tab was not rendered")
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
	if os.Getenv("PENCODE_CODEX_COMPLETION") == "1" {
		name := "codex-context.txt"
		if err := os.WriteFile(filepath.Join(model.projectPath, name), []byte("context"), 0o600); err != nil {
			t.Fatal(err)
		}
		promptID := waitControl(surface, "prompt")
		editorContextClick(t, app, model, state, promptID)
		value := func() string {
			control, ok := model.fontResources.HTMLControl(model.surface, promptID)
			if ok {
				if field := model.fontResources.Mut[engineui.TextField](control); field != nil {
					return field.Text().String()
				}
			}
			return ""
		}
		focused := func() bool {
			control, ok := model.fontResources.HTMLControl(model.surface, promptID)
			return ok && model.fontResources.FocusedControl() == control
		}
		press := func(key input.KeyCode) {
			state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
			languageInteractionStep(t, app)
			state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
			languageInteractionStep(t, app)
		}
		state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "@codex-context"})
		phase = "open file suggestions"
		wait(func() bool {
			return strings.Contains(surface.view.HTML, `role="listbox"`) && focused()
		})
		press(input.KeyTab)
		phase = "accept file"
		wait(func() bool {
			return strings.Contains(surface.view.HTML, `data-ref="remove-file/0"`) && value() == "[@codex-context.txt](codex-context.txt) " && focused()
		})
		if value() != "[@codex-context.txt](codex-context.txt) " {
			t.Fatal("file completion did not replace mention", value())
		}
		control, _ := model.fontResources.HTMLControl(model.surface, promptID)
		if model.fontResources.FocusedControl() != control {
			t.Fatal("file completion lost input focus")
		}
		field := model.fontResources.Mut[engineui.TextField](control)
		field.Anchor, field.Caret = 0, field.Text().Runes()
		state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "/fixture-r"})
		phase = "open skill suggestions"
		wait(func() bool {
			return strings.Contains(surface.view.HTML, `role="option"`) && strings.Contains(surface.view.HTML, ">fixture-review</span>") && strings.Contains(surface.view.HTML, `class="codex-completion-icon"`) && !strings.Contains(surface.view.HTML, `class="codex-completion-label">/`) && value() == "/fixture-r" && focused()
		})
		press(input.KeyEnter)
		phase = "show selected skill chip"
		wait(func() bool {
			return value() == "" && strings.Contains(surface.view.HTML, `data-ref="remove-skill/0"`) && strings.Contains(surface.view.HTML, ">Fixture Review</span>") && focused()
		})
		control, _ = model.fontResources.HTMLControl(model.surface, promptID)
		field = model.fontResources.Mut[engineui.TextField](control)
		field.Anchor, field.Caret = 0, field.Text().Runes()
		// Text and Enter in one frame must choose the skill before the
		// asynchronously published menu has reached the workbench.
		state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "/fixture-r"})
		press(input.KeyEnter)
		phase = "complete skill before menu arrives"
		wait(func() bool {
			return value() == "" && strings.Contains(surface.view.HTML, `data-ref="remove-skill/0"`) && focused()
		})
		if strings.Contains(surface.view.HTML, `data-ref="remove-skill/1"`) || strings.Contains(surface.view.HTML, "[$fixture-review]") {
			t.Fatal("skill selection duplicated the chip or exposed Markdown")
		}
		if strings.Contains(surface.view.HTML, `role="listbox"`) {
			t.Fatal("skill completion retained its menu")
		}
		state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "Review current changes"})
		press(input.KeyEnter)
		phase = "send skill input to app-server"
		wait(func() bool { return strings.Contains(surface.view.HTML, "Thread permissions:") })
		wait(func() bool { return !strings.Contains(surface.view.HTML, `data-ref="remove-skill/0"`) && value() == "" })
		return
	}
	if os.Getenv("PENCODE_CODEX_PERMISSION_MENU") == "1" {
		permissionID := waitControl(surface, "permission-settings")
		editorContextClick(t, app, model, state, permissionID)
		wait(func() bool {
			return model.actions.ui.overlay != nil && strings.Contains(surface.view.HTML, `aria-label="权限设置" aria-expanded="true"`)
		})
		for range 4 {
			languageInteractionStep(t, app)
		}
		menu := editorContextBounds(t, model, model.actions.ui.overlay.rootID)
		anchor := editorContextBounds(t, model, permissionID)
		if math.Abs(float64(menu.X-anchor.X)) > .2 || math.Abs(float64(menu.Y+menu.Height+6-anchor.Y)) > .2 {
			t.Fatalf("permission menu is not above its button: menu=%+v anchor=%+v", menu, anchor)
		}
		return
	}
	if os.Getenv("PENCODE_CODEX_MODEL_MENU") == "1" {
		modelID := waitControl(surface, "model-settings")
		for range 3 {
			editorContextClick(t, app, model, state, modelID)
			wait(func() bool {
				return model.actions.ui.overlay != nil && strings.Contains(surface.view.HTML, `aria-label="选择模型" aria-expanded="true"`)
			})
			state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: 8, Y: 850})
			state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: 8, Y: 850})
			languageInteractionStep(t, app)
			if model.actions.ui.overlay != nil {
				t.Fatal("outside click did not dismiss the model menu")
			}
			// The close publication can arrive between pressing the button again
			// and releasing it. The native click target must survive that update.
			r := editorContextBounds(t, model, modelID)
			scale := float64(model.settings.Zoom) / 100
			x, y := float64(r.X+r.Width/2)*scale, float64(r.Y+r.Height/2)*scale
			state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
			languageInteractionStep(t, app)
			wait(func() bool {
				return strings.Contains(surface.view.HTML, `aria-label="选择模型" aria-expanded="false"`)
			})
			state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
			languageInteractionStep(t, app)
			wait(func() bool { return model.actions.ui.overlay != nil && model.actions.ui.overlay.ui.ID == "models" })
			state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEscape})
			state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEscape})
			languageInteractionStep(t, app)
			wait(func() bool {
				return model.actions.ui.overlay == nil && strings.Contains(surface.view.HTML, `aria-label="选择模型" aria-expanded="false"`)
			})
		}
		return
	}
	if os.Getenv("PENCODE_CODEX_HISTORY_SWITCH") == "1" {
		wait(func() bool { return strings.Contains(surface.view.HTML, `data-ref="history/saved"`) })
		for range 3 {
			editorContextClick(t, app, model, state, waitControl(surface, "history/saved"))
			wait(func() bool {
				return strings.Contains(surface.view.HTML, "Stored conversation") && strings.Contains(surface.view.HTML, `data-ref="history/saved" aria-current="true"`)
			})
			waitControl(surface, "prompt")
			if model.actions.ui.editor != surface {
				t.Fatal("history selection replaced the Codex editor tab")
			}
			previous, count := surface, len(model.actions.ui.surfaces)
			surface.send(peb.EditorEvent{Kind: "input", Ref: "prompt", Value: "keep this conversation draft"}, "")
			editorContextClick(t, app, model, state, waitControl(surface, "new"))
			wait(func() bool { return model.actions.ui.editor != previous })
			surface = model.actions.ui.editor
			if surface == nil || surface.ui.ID == previous.ui.ID || len(model.actions.ui.surfaces) != count+1 {
				t.Fatal("new conversation did not create a separate editor tab")
			}
			wait(func() bool { return strings.Contains(surface.view.HTML, "给 Codex 的任务") })
			if strings.Contains(surface.view.HTML, "Stored conversation") || strings.Contains(surface.view.HTML, "keep this conversation draft") {
				t.Fatal("new conversation inherited the previous transcript or draft")
			}
			if !strings.Contains(previous.view.HTML, "Stored conversation") {
				t.Fatal("new conversation overwrote the original tab")
			}
			waitControl(surface, "prompt")
		}
		return
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
		// Exercise the actual hover target and mouse click, not just the binding.
		renameID := waitControl(surface, "rename/"+first.Path+"/saved")
		row := editorContextBounds(t, model, waitControl(surface, "history/"+first.Path+"/saved"))
		scale := float64(model.settings.Zoom) / 100
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(row.X+12) * scale, Y: float64(row.Y+row.Height/2) * scale})
		languageInteractionStep(t, app)
		edit := editorContextBounds(t, model, renameID)
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(edit.X+edit.Width/2) * scale, Y: float64(edit.Y+edit.Height/2) * scale})
		languageInteractionStep(t, app)
		editorContextClick(t, app, model, state, renameID)
		wait(func() bool { return model.actions.ui.overlay != nil && model.actions.ui.overlay.ui.ID == "rename" })
		if edit.Width != 20 || edit.Height != 20 || math.Abs(float64(edit.Y+edit.Height/2-row.Y-row.Height/2)) > 1 {
			t.Fatalf("rename button must be a centered 20px square: row=%+v edit=%+v", row, edit)
		}
		rename := model.actions.ui.overlay
		editorContextClick(t, app, model, state, waitControl(rename, "rename-cancel"))
		wait(func() bool { return model.actions.ui.overlay == nil })
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
	startupFailed := func() bool {
		for _, candidate := range model.actions.ui.surfaces {
			if candidate.owner == "codex" && candidate.ui.Kind == peb.Notification && strings.Contains(candidate.view.HTML, "missing-codex") {
				return true
			}
		}
		return false
	}
	wait(startupFailed)
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
		wait(startupFailed)
		waitControl(surface, "prompt")
	}
}
