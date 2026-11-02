package workbench

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestCodexLiveChangesHover(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	css, err := os.ReadFile("testdata/codex/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	counts := `<span class="codex-change-counts"><span class="codex-change-added">+45</span><span class="codex-change-removed">-10</span></span>`
	if err := m.applyExtensionUI("codex", &extensions.ActionSession{}, peb.UI{ID: "composer", Kind: peb.EditorTab, View: &peb.EditorView{
		HTML: `<main class="codex-workspace" data-pencode-layout="fill"><section class="codex-chat-pane"><div class="codex-transcript"></div><section class="codex-composer"><div id="pencode-extension-live-wrapper" class="codex-live-changes"><div id="pencode-extension-live-anchor" class="codex-live-changes-anchor"><button id="pencode-extension-live-pill" data-ref="live-pill" type="button" class="codex-live-changes-pill">4 个文件已更改` + counts + `</button><div id="pencode-extension-live-popup" class="codex-live-changes-popover"><div id="pencode-extension-live-list" class="codex-live-changes-list">` + strings.Repeat(`<div class="codex-live-change"><span class="codex-change-path">extensions/codex/agent_test.go</span>`+counts+`</div>`, 20) + `</div></div></div></div><form id="pencode-extension-live-input" class="codex-input-card"><textarea class="codex-prompt">draft</textarea></form></section></section></main>`,
		CSS:  string(css),
	}}); err != nil {
		t.Fatal(err)
	}
	markPencodeRootDirtyByModel(m.fontResources, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	pill := editorContextBounds(t, m, "pencode-extension-live-pill")
	card := editorContextBounds(t, m, "pencode-extension-live-input")
	if pill.Y+pill.Height > card.Y || math.Abs(float64(pill.X+pill.Width/2-card.X-card.Width/2)) > 1 {
		t.Log(editorContextBounds(t, m, "pencode-extension-live-wrapper"), editorContextBounds(t, m, "pencode-extension-live-anchor"))
		t.Fatalf("pill must be centered above input: pill=%+v input=%+v", pill, card)
	}
	visible := func() bool {
		node, ok := m.fontResources.HTMLNode(m.surface, "pencode-extension-live-list")
		if !ok {
			return false
		}
		area, ok := node.ScrollContainer()
		if !ok {
			return false
		}
		scroll := m.fontResources.Mut[scene.UIScroll](area)
		return scroll != nil && !scroll.DisableInput
	}
	if visible() {
		t.Fatal("file list must start hidden")
	}
	move := func(x, y float32) {
		scale := float64(m.settings.Zoom) / 100
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(x) * scale, Y: float64(y) * scale})
		for range 3 {
			languageInteractionStep(t, app)
		}
	}
	move(pill.X+pill.Width/2, pill.Y+pill.Height/2)
	if !visible() {
		control, ok := m.fontResources.HTMLControl(m.surface, "pencode-extension-live-pill")
		t.Log("pill", pill, "pointer", state.Pointer(), "control", control, ok)
		inputState, _ := app.World.Get[scene.InputState2D](control)
		transform, _ := app.World.Get[scene.UITransform](control)
		t.Log("input", inputState, "transform", transform)
		t.Log("hover updated", htmlui.SetControlHover(app.World, control, true))
		languageInteractionStep(t, app)
		t.Fatalf("hover did not reveal file list (direct hover visible=%v)", visible())
	}
	popup := editorContextBounds(t, m, "pencode-extension-live-popup")
	list := editorContextBounds(t, m, "pencode-extension-live-list")
	if popup.Y+popup.Height > pill.Y+1 || list.Height > 241 || math.Abs(float64(popup.X+popup.Width/2-pill.X-pill.Width/2)) > 1 {
		t.Fatalf("hover list must be bounded and centered above pill: popup=%+v list=%+v pill=%+v", popup, list, pill)
	}
	move(list.X+list.Width/2, list.Y+list.Height/2)
	if !visible() {
		t.Fatal("hover list disappeared when pointer entered it")
	}
	move(card.X+20, card.Y+20)
	if visible() {
		t.Fatal("hover list stayed visible after pointer left")
	}
}

