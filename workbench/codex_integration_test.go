//go:build codex_integration

package workbench

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
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

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	htmlui "github.com/onix-engine/onix/ui/html"
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

func TestCodexAsyncQuestionOffersNativeChoices(t *testing.T) {
	t.Setenv("PENCODE_CODEX_PATH", codexFixtureExecutable(t))
	t.Setenv("PENCODE_CODEX_FIXTURE", "1")
	t.Setenv("PENCODE_CODEX_ASYNC_QUESTION", "1")
	testCodexStatusBarWorkspace(t, "")
}

func TestCodexPastedImageThumbnailPreviewAndRemove(t *testing.T) {
	t.Setenv("PENCODE_CODEX_PATH", filepath.Join(t.TempDir(), "missing-codex"))
	t.Setenv("PENCODE_CODEX_IMAGE_PREVIEW", "1")
	testCodexStatusBarWorkspace(t, "")
}

func TestCodexSentImageOpensPreview(t *testing.T) {
	t.Setenv("PENCODE_CODEX_PATH", codexFixtureExecutable(t))
	t.Setenv("PENCODE_CODEX_FIXTURE", "1")
	t.Setenv("PENCODE_CODEX_SENT_IMAGE", "1")
	testCodexStatusBarWorkspace(t, "")
}

func TestCodexNewSessionKeepsActionConnection(t *testing.T) {
	t.Setenv("PENCODE_CODEX_PATH", filepath.Join(t.TempDir(), "missing-codex"))
	t.Setenv("PENCODE_CODEX_NEW_SESSION", "1")
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

func TestCodexWindowAndProjectCloseConfirmBackgroundWork(t *testing.T) {
	t.Setenv("PENCODE_CODEX_PATH", codexFixtureExecutable(t))
	t.Setenv("PENCODE_CODEX_FIXTURE", "1")
	t.Setenv("PENCODE_CODEX_LIFECYCLE", "1")
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

func TestCodexLiveFileChangesKeepStreaming(t *testing.T) {
	t.Setenv("PENCODE_CODEX_PATH", codexFixtureExecutable(t))
	t.Setenv("PENCODE_CODEX_FIXTURE", "1")
	gate := filepath.Join(t.TempDir(), "finish-turn")
	t.Setenv("PENCODE_CODEX_LIVE_CHANGES_GATE", gate)
	defer os.WriteFile(gate, nil, 0o600)
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
	if os.Getenv("PENCODE_CODEX_FIXTURE") == "1" {
		fixture := exec.CommandContext(t.Context(), os.Getenv("PENCODE_CODEX_PATH"), "app-server", "fixture-listen")
		input, err := fixture.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		fixture.Cancel = input.Close
		fixture.WaitDelay = 2 * time.Second
		output, err := fixture.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		fixture.Stderr = os.Stderr
		if err := fixture.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = input.Close()
			_ = fixture.Wait()
		})
		endpoint, err := bufio.NewReader(output).ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("PENCODE_CODEX_SERVER_URL", strings.TrimSpace(endpoint))
	}
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
	if os.Getenv("PENCODE_CODEX_ASYNC_QUESTION") == "1" {
		phase = "ask a structured async question"
		wait(func() bool { return strings.Contains(surface.view.HTML, `data-ref="history/saved"`) })
		editorContextClick(t, app, model, state, waitControl(surface, "history/saved"))
		wait(func() bool { return strings.Contains(surface.view.HTML, "Stored conversation") })
		surface.send(peb.EditorEvent{Kind: "input", Ref: "prompt", Value: "你 ask 工具问我一个问题"}, "")
		editorContextClick(t, app, model, state, waitControl(surface, "send"))
		wait(func() bool {
			return !surface.ui.Busy && strings.Contains(surface.view.HTML, `class="codex-question-card"`)
		})
		ref := ""
		for _, binding := range surface.view.Events {
			if strings.HasPrefix(binding.Ref, "question-") && strings.HasSuffix(binding.Ref, "/select/1") {
				ref = strings.TrimSuffix(binding.Ref, "select/1")
				break
			}
		}
		if ref == "" {
			t.Fatal("completed ask did not expose selectable controls")
		}
		choice := editorContextBounds(t, model, waitControl(surface, ref+"select/1"))
		prompt := editorContextBounds(t, model, waitControl(surface, "prompt"))
		if choice.Height < 20 || choice.Y < 0 || choice.Y+choice.Height > prompt.Y {
			t.Fatalf("async choice is not visible above composer: choice=%+v prompt=%+v", choice, prompt)
		}
		phase = "retain draft alongside async question"
		surface.send(peb.EditorEvent{Kind: "input", Ref: "prompt", Value: "保留草稿"}, "")
		phase = "select an async answer using native button"
		editorContextClick(t, app, model, state, waitControl(surface, ref+"select/1"))
		wait(func() bool { return strings.Contains(surface.view.HTML, `aria-pressed="true"`) })
		phase = "submit an async answer using native button"
		editorContextClick(t, app, model, state, waitControl(surface, ref+"next"))
		wait(func() bool {
			return !surface.ui.Busy && strings.Contains(surface.view.HTML, "收到回答：") && !strings.Contains(surface.view.HTML, `class="codex-question-card"`)
		})
		if !strings.Contains(surface.view.HTML, "实现一个功能") || !strings.Contains(surface.view.HTML, ">保留草稿</textarea>") {
			t.Fatal("native answer did not reach the model or preserve composer draft")
		}
		return
	}
	if os.Getenv("PENCODE_CODEX_VIEWED_IMAGE") != "" {
		phase = "open image viewing history"
		wait(func() bool { return strings.Contains(surface.view.HTML, `data-ref="history/saved"`) })
		editorContextClick(t, app, model, state, waitControl(surface, "history/saved"))
		wait(func() bool { return strings.Contains(surface.view.HTML, "已查看 2 张图像") })
		if strings.Contains(surface.view.HTML, `data-ref="view-image/`) {
			t.Fatal("viewed images must start collapsed")
		}
		phase = "expand viewed images"
		editorContextClick(t, app, model, state, waitControl(surface, "activity/view-one"))
		wait(func() bool { return strings.Contains(surface.view.HTML, `data-ref="view-image/view-two"`) })
		openID := waitControl(surface, "view-image/view-one")
		bounds := editorContextBounds(t, model, openID)
		if bounds.Width != 80 || bounds.Height != 80 {
			t.Fatalf("viewed thumbnail has invalid bounds: %+v", bounds)
		}
		phase = "open viewed image preview"
		editorContextClick(t, app, model, state, openID)
		wait(func() bool {
			return model.actions.ui.overlay != nil && model.actions.ui.overlay.ui.ID == "image-preview"
		})
		if len(model.actions.ui.overlay.images) == 0 {
			t.Fatal("viewed image preview has no image assets")
		}
		phase = "close preview and collapse viewed images"
		editorContextClick(t, app, model, state, model.actions.ui.overlay.closeID)
		wait(func() bool { return model.actions.ui.overlay == nil })
		editorContextClick(t, app, model, state, waitControl(surface, "activity/view-one"))
		wait(func() bool { return !strings.Contains(surface.view.HTML, `data-ref="view-image/`) })
		return
	}
	if os.Getenv("PENCODE_CODEX_IMAGE_PREVIEW") == "1" || os.Getenv("PENCODE_CODEX_SENT_IMAGE") == "1" {
		if os.Getenv("PENCODE_CODEX_SENT_IMAGE") == "1" {
			phase = "open saved conversation"
			wait(func() bool { return strings.Contains(surface.view.HTML, `data-ref="history/saved"`) })
			editorContextClick(t, app, model, state, waitControl(surface, "history/saved"))
			wait(func() bool { return strings.Contains(surface.view.HTML, "Stored conversation") })
		}
		phase = "paste image"
		image := image.NewNRGBA(image.Rect(0, 0, 320, 200))
		image.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
		var data bytes.Buffer
		if err := png.Encode(&data, image); err != nil {
			t.Fatal(err)
		}
		surface.send(peb.EditorEvent{Kind: "paste", Ref: "prompt", InputType: "image/png", Data: "data:image/png;base64," + base64.StdEncoding.EncodeToString(data.Bytes()), Value: "保留草稿"}, "")
		wait(func() bool { return strings.Contains(surface.view.HTML, `data-ref="preview-image/0"`) })
		openID := waitControl(surface, "preview-image/0")
		removeID := waitControl(surface, "remove-image/0")
		thumb := editorContextBounds(t, model, openID)
		remove := editorContextBounds(t, model, removeID)
		if thumb.Width < 70 || thumb.Height < 54 || remove.Width < 16 || remove.Height < 16 || remove.X < thumb.X || remove.X+remove.Width > thumb.X+thumb.Width {
			t.Fatalf("thumbnail or remove control has invalid bounds: thumbnail=%+v remove=%+v", thumb, remove)
		}
		icon := editorContextBounds(t, model, "pencode-extension-codex-remove-image-icon-0")
		if math.Abs(float64(icon.X+icon.Width/2-remove.X-remove.Width/2)) > .5 || math.Abs(float64(icon.Y+icon.Height/2-remove.Y-remove.Height/2)) > .5 {
			t.Fatalf("remove icon is not centered: icon=%+v button=%+v", icon, remove)
		}
		checkImage := func(s *pencodeExtensionSurface, width, height int) {
			t.Helper()
			provider := model.session.active.icons
			for source := range s.images {
				if !strings.Contains(source, "/inline/") {
					continue
				}
				texture, ok := provider.ResolveHTMLImage(source, 0, 0)
				if !ok || texture.LogicalWidth() != width || texture.LogicalHeight() != height {
					t.Fatal("image texture was not loaded at the expected size", source)
				}
				for _, access := range app.World.Query[scene.Sprite2D]().All {
					if access.Ref().Texture == texture {
						return
					}
				}
				for _, access := range app.World.Query[scene.SpriteLayers2D]().All {
					for _, layer := range access.Ref().Layers {
						if layer.Visible && layer.Sprite.Texture == texture && layer.Sprite.Width > 0 && layer.Sprite.Height > 0 {
							return
						}
					}
				}
				t.Fatal("image texture has no rendered image")
			}
			t.Fatal("inline image asset was not published")
		}
		checkImage(surface, 160, 100)
		phase = "open centered image preview"
		editorContextClick(t, app, model, state, openID)
		wait(func() bool {
			return model.actions.ui.overlay != nil && model.actions.ui.overlay.ui.ID == "image-preview"
		})
		preview := model.actions.ui.overlay
		for range 3 {
			languageInteractionStep(t, app)
		}
		checkImage(preview, 320, 200)
		bounds := editorContextBounds(t, model, preview.rootID)
		viewport, _ := app.World.GetResource[scene.ViewportSize]()
		if math.Abs(float64(bounds.X+bounds.Width/2)-float64(viewport.Width)/2) > 2 || math.Abs(float64(bounds.Y+bounds.Height/2)-float64(viewport.Height)/2) > 2 {
			t.Fatalf("image preview is not centered: bounds=%+v viewport=%+v", bounds, viewport)
		}
		phase = "close image preview"
		editorContextClick(t, app, model, state, preview.closeID)
		wait(func() bool { return model.actions.ui.overlay == nil })
		if !strings.Contains(surface.view.HTML, `data-ref="preview-image/0"`) {
			t.Fatal("closing preview removed the attachment")
		}
		if os.Getenv("PENCODE_CODEX_SENT_IMAGE") == "1" {
			phase = "send image and display above the message"
			editorContextClick(t, app, model, state, waitControl(surface, "send"))
			wait(func() bool {
				return strings.Contains(surface.view.HTML, `data-ref="preview-message-image/0/user"`) && strings.Contains(surface.view.HTML, `data-ref="accept/100"`)
			})
			for range 3 {
				languageInteractionStep(t, app)
			}
			if strings.Contains(surface.view.HTML, `data-ref="preview-image/0"`) {
				t.Fatal("successful send retained the draft image")
			}
			openID = waitControl(surface, "preview-message-image/0/user")
			thumb = editorContextBounds(t, model, openID)
			if thumb.Width != 80 || thumb.Height != 80 {
				t.Fatalf("sent thumbnail has invalid bounds: %+v", thumb)
			}
			checkImage(surface, 160, 100)
			phase = "open sent image preview"
			// The transcript has scrolled; click the painted control's position.
			control, _ := model.fontResources.HTMLControl(model.surface, openID)
			transform := model.fontResources.Ref[scene.UIGlobalTransform](control)
			x := float64(transform.Matrix[12] + transform.Matrix[0]*thumb.Width/2)
			y := float64(transform.Matrix[13] + transform.Matrix[5]*thumb.Height/2)
			state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
			state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
			languageInteractionStep(t, app)
			wait(func() bool {
				return model.actions.ui.overlay != nil && model.actions.ui.overlay.ui.ID == "image-preview"
			})
			for range 3 {
				languageInteractionStep(t, app)
			}
			checkImage(model.actions.ui.overlay, 320, 200)
			return
		}
		phase = "remove image"
		editorContextClick(t, app, model, state, removeID)
		wait(func() bool { return !strings.Contains(surface.view.HTML, `data-ref="preview-image/0"`) })
		if !strings.Contains(surface.view.HTML, "保留草稿</textarea>") {
			t.Fatal("removing the image lost the draft")
		}
		for source := range model.extensionImages {
			if strings.Contains(source, "/inline/") {
				t.Fatal("removing the last image retained its asset", source)
			}
		}
		return
	}
	if gate := os.Getenv("PENCODE_CODEX_LIVE_CHANGES_GATE"); gate != "" {
		phase = "open saved conversation"
		wait(func() bool { return strings.Contains(surface.view.HTML, `data-ref="history/saved"`) })
		editorContextClick(t, app, model, state, waitControl(surface, "history/saved"))
		wait(func() bool { return strings.Contains(surface.view.HTML, "Stored conversation") })
		surface.send(peb.EditorEvent{Kind: "input", Ref: "prompt", Value: "edit files"}, "")
		surface.send(peb.EditorEvent{Kind: "click", Ref: "send"}, "")
		phase = "show live file changes before completion"
		wait(func() bool {
			return surface.ui.Busy && strings.Contains(surface.view.HTML, "1 个文件已更改") && strings.Contains(surface.view.HTML, `data-ref="accept/100"`)
		})
		pillID := waitControl(surface, "live-changes")
		pill := editorContextBounds(t, model, pillID)
		prompt := editorContextBounds(t, model, waitControl(surface, "prompt"))
		if pill.Width < 100 || pill.Height < 20 || pill.Y+pill.Height > prompt.Y || pill.Y < 0 {
			t.Fatalf("live changes pill is not visible above composer: pill=%+v prompt=%+v", pill, prompt)
		}
		phase = "expand a live file diff inline"
		scale := float64(model.settings.Zoom) / 100
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(pill.X+pill.Width/2) * scale, Y: float64(pill.Y+pill.Height/2) * scale})
		for range 3 {
			languageInteractionStep(t, app)
		}
		rowID := waitControl(surface, "change-file/turn1/0")
		row, ok := model.extensionAnchorBounds(model.fontResources, rowID)
		if !ok {
			t.Fatal("live file row has no native bounds")
		}
		x, y := float64(row.X+row.Width/2)*scale, float64(row.Y+row.Height/2)*scale
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x, Y: y})
		for range 3 {
			languageInteractionStep(t, app)
		}
		control, _ := model.fontResources.HTMLControl(model.surface, rowID)
		t.Logf("live diff click: pill=%+v row=%+v pointer=(%v,%v) target=%+v bindings=%+v", pill, row, x, y, model.fontResources.Ref[scene.InputTarget2D](control), surface.bindings)
		state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
		state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
		wait(func() bool {
			return strings.Contains(surface.view.HTML, `class="codex-change-diff"`) && strings.Contains(surface.view.HTML, "+new") && strings.Contains(surface.view.HTML, "-old")
		})
		if model.actions.ui.editor != surface || len(model.actions.ui.surfaces) != 1 {
			t.Fatal("file click must keep the existing chat tab selected")
		}
		phase = "stream reply with live file changes"
		surface.send(peb.EditorEvent{Kind: "click", Ref: "accept/100"}, "")
		wait(func() bool {
			return surface.ui.Busy && strings.Contains(surface.view.HTML, "Streamed reply") && strings.Contains(surface.view.HTML, "1 个文件已更改")
		})
		phase = "complete turn and show final file changes"
		if err := os.WriteFile(gate, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		wait(func() bool {
			return !surface.ui.Busy && strings.Contains(surface.view.HTML, "Final reply") && strings.Contains(surface.view.HTML, "已编辑 1 个文件") && !strings.Contains(surface.view.HTML, `class="codex-live-changes"`)
		})
		phase = "toggle the completed file diff inline"
		editorContextClick(t, app, model, state, waitControl(surface, "change-file/turn1/0"))
		wait(func() bool { return !strings.Contains(surface.view.HTML, `class="codex-change-diff"`) })
		editorContextClick(t, app, model, state, waitControl(surface, "change-file/turn1/0"))
		wait(func() bool { return strings.Contains(surface.view.HTML, `class="codex-change-diff"`) })
		if model.actions.ui.editor != surface || len(model.actions.ui.surfaces) != 1 {
			t.Fatal("completed file click opened another tab")
		}
		return
	}
	if os.Getenv("PENCODE_CODEX_LIFECYCLE") == "1" {
		phase = "start running conversation"
		surface.send(peb.EditorEvent{Kind: "input", Ref: "prompt", Value: "wait"}, "")
		surface.send(peb.EditorEvent{Kind: "click", Ref: "send"}, "")
		wait(func() bool { return surface.ui.Busy && strings.Contains(surface.view.HTML, "Thread permissions:") })
		phase = "close running tab"
		editorContextClick(t, app, model, state, "file-tab-close:"+surface.rootID)
		wait(func() bool { return len(model.actions.ui.surfaces) == 0 })
		for _, projectClose := range []bool{false, true} {
			phase = fmt.Sprintf("confirm background work for project=%t", projectClose)
			project := model.activeProjectState()
			if projectClose {
				model.closeProject(project)
			} else if model.requestWindowClose() {
				t.Fatal("window closed without checking background work")
			}
			wait(func() bool {
				return model.closing.lifecycle && len(model.closing.lifecyclePending) == 0 && strings.Contains(model.closing.message, "1 个会话")
			})
			editorContextClick(t, app, model, state, "close-cancel")
			if model.closing.action != pencodeCloseNone || model.activeProjectState() != project {
				t.Fatal("cancel did not preserve project")
			}
		}
		phase = "reopen background work after cancelling close"
		editorContextClick(t, app, model, state, button)
		wait(func() bool { return model.actions.ui.editor != nil && model.actions.ui.editor.ui.Busy })
		return
	}
	if os.Getenv("PENCODE_CODEX_NEW_SESSION") == "1" {
		original, count := surface, len(model.actions.ui.surfaces)
		for index := 1; index <= 2; index++ {
			phase = "create session " + strconv.Itoa(index)
			editorContextClick(t, app, model, state, waitControl(surface, "new"))
			wait(func() bool {
				return model.actions.ui.editor == original && strings.Contains(surface.view.HTML, "新会话")
			})
			phase = "open new session permissions"
			editorContextClick(t, app, model, state, waitControl(surface, "permission-settings"))
			wait(func() bool {
				menu := model.actions.ui.overlay
				return menu != nil && menu.ui.ID == "permissions" && menu.ui.Anchor == "ui:composer#permission-settings"
			})
			editorContextClick(t, app, model, state, waitControl(model.actions.ui.overlay, "permission/full"))
			wait(func() bool {
				return model.actions.ui.overlay == nil && strings.Contains(surface.view.HTML, "完全访问</span>")
			})
		}
		if len(model.actions.ui.surfaces) != count || model.actions.ui.editor != original {
			t.Fatal("new session opened another tab")
		}
		return
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
		for _, token := range []string{"/", "@"} {
			control, _ := model.fontResources.HTMLControl(model.surface, promptID)
			field := model.fontResources.Mut[engineui.TextField](control)
			field.Anchor, field.Caret = 0, field.Text().Runes()
			state.Enqueue(input.Event{Kind: input.EventTextInput, Text: token})
			phase = "open " + token + " suggestions before dismissal"
			wait(func() bool {
				return strings.Contains(surface.view.HTML, `role="listbox"`) && value() == token && focused()
			})
			press(input.KeyEscape)
			phase = "dismiss " + token + " suggestions"
			wait(func() bool {
				control, ok := model.fontResources.HTMLControl(model.surface, promptID)
				return !strings.Contains(surface.view.HTML, `role="listbox"`) && ok && model.fontResources.Has[engineui.TextField](control)
			})
			control, _ = model.fontResources.HTMLControl(model.surface, promptID)
			field = model.fontResources.Mut[engineui.TextField](control)
			if !focused() || field.Anchor != 1 || field.Caret != 1 || !state.TextInputConfig().Active {
				t.Fatalf("dismissing %s suggestions lost input focus or caret: %+v", token, field)
			}
			state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "x"})
			phase = "type after dismissing " + token + " suggestions"
			wait(func() bool { return value() == token+"x" && focused() })
		}
		control, _ := model.fontResources.HTMLControl(model.surface, promptID)
		field := model.fontResources.Mut[engineui.TextField](control)
		field.Anchor, field.Caret = 0, field.Text().Runes()
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
		control, _ = model.fontResources.HTMLControl(model.surface, promptID)
		if model.fontResources.FocusedControl() != control {
			t.Fatal("file completion lost input focus")
		}
		field = model.fontResources.Mut[engineui.TextField](control)
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
		for iteration := range 3 {
			phase = fmt.Sprintf("history tab %d: select history", iteration)
			wait(func() bool { return strings.Contains(surface.view.HTML, `data-ref="history/saved"`) })
			editorContextClick(t, app, model, state, waitControl(surface, "history/saved"))
			wait(func() bool {
				return strings.Contains(surface.view.HTML, "Stored conversation") && strings.Contains(surface.view.HTML, `data-ref="history/saved" aria-current="true"`)
			})
			phase = fmt.Sprintf("history tab %d: find history prompt", iteration)
			waitControl(surface, "prompt")
			if model.actions.ui.editor != surface {
				t.Fatal("history selection replaced the Codex editor tab")
			}
			previous, count := surface, len(model.actions.ui.surfaces)
			surface.send(peb.EditorEvent{Kind: "input", Ref: "prompt", Value: "keep this conversation draft"}, "")
			editorContextClick(t, app, model, state, waitControl(surface, "new"))
			phase = fmt.Sprintf("history tab %d: switch to new conversation", iteration)
			wait(func() bool {
				return strings.Contains(surface.view.HTML, "新会话") && !strings.Contains(surface.view.HTML, "Stored conversation")
			})
			if model.actions.ui.editor != previous || len(model.actions.ui.surfaces) != count {
				t.Fatal("new conversation opened another editor tab")
			}
			if strings.Contains(surface.view.HTML, "keep this conversation draft") {
				t.Fatal("new conversation inherited the previous draft")
			}
			editorContextClick(t, app, model, state, waitControl(surface, "history/saved"))
			wait(func() bool { return strings.Contains(surface.view.HTML, ">keep this conversation draft</textarea>") })
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
