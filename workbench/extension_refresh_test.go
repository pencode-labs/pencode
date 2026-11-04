package workbench

import (
	"os"
	"testing"

	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestExtensionMessageUpdatesRetainWorkbench(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	m.app.GetViewport = func() (int, int) { return 1440, 900 }
	app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	for _, camera := range app.World.Query[scene.Camera]().All {
		camera.Mut().UpdateViewport(1440, 900)
	}
	w := m.fontResources
	css, err := os.ReadFile("testdata/codex/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan extensions.ActionResult, 1)
	process := &extensions.ActionSession{Results: results}
	m.actions.sessions = map[string]*extensions.ActionSession{"codex": process}
	t.Cleanup(func() { delete(m.actions.sessions, "codex") })
	show := func(title, text string, busy bool) {
		results <- extensions.ActionResult{UI: &peb.UI{ID: "composer", Title: title, Kind: peb.EditorTab, Busy: busy, View: &peb.EditorView{
			HTML:   `<main class="codex-workspace" data-pencode-layout="fill"><section class="codex-chat-pane"><div class="codex-transcript" data-ref="transcript" data-pencode-scroll="end"><article class="codex-chat"><section id="pencode-extension-earlier" data-ref="earlier" data-pencode-region><p>Earlier reply</p></section><section class="codex-message" data-ref="message" data-pencode-region><p>` + text + `</p></section></article></div><section class="codex-composer"><textarea class="codex-prompt" data-ref="prompt">draft</textarea></section></section><aside id="pencode-extension-history" data-ref="history" data-pencode-region>Saved history</aside></main>`,
			CSS:    string(css),
			Events: []peb.EditorBinding{{Ref: "prompt", Kind: "input"}},
		}}}
	}
	show("Chat", "First", false)
	for range 3 {
		languageInteractionStep(t, app)
	}
	sourceWait(t, app, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	root, ok := w.Get[htmlui.Component[appProps, appState]](m.root)
	if !ok || m.actions.ui.editor == nil {
		t.Fatal("workbench or extension tab was not mounted")
	}
	node := func(id string) htmlui.Node {
		t.Helper()
		n, ok := w.HTMLNode(m.surface, id)
		if !ok {
			t.Fatalf("missing node %q", id)
		}
		return n
	}
	treeID := m.projectTreeRowID(m.activeProjectState(), "main.go")
	tree := node(treeID)
	treeControl, _ := w.HTMLControl(m.surface, treeID)
	treeRender, ok := w.Get[htmlui.Control](treeControl)
	if !ok {
		t.Fatal("explorer control was not mounted")
	}
	surface := m.actions.ui.editor
	transcript := node(surface.prefix + "control-transcript")
	earlier := node("pencode-extension-earlier")
	history := node("pencode-extension-history")
	prompt, ok := w.HTMLControl(m.surface, surface.prefix+"control-prompt")
	if !ok {
		t.Fatal("composer input was not mounted")
	}
	for _, update := range []struct {
		title, text string
		busy        bool
	}{{"Chat", "Streaming", false}, {"Chat", "Started", true}, {"Generated title", "More", true}, {"Generated title", "Done", false}} {
		show(update.title, update.text, update.busy)
		for frame := range 3 {
			languageInteractionStep(t, app)
			currentRoot, _ := w.Get[htmlui.Component[appProps, appState]](m.root)
			currentTree, ok := w.HTMLNode(m.surface, treeID)
			currentRender, _ := w.Get[htmlui.Control](treeControl)
			if currentRoot.State.Revision != root.State.Revision || !ok || currentTree != tree || currentRender.RenderRevision != treeRender.RenderRevision {
				t.Fatalf("message update %q refreshed workbench on frame %d: root %d -> %d, tree render %d -> %d", update.text, frame, root.State.Revision, currentRoot.State.Revision, treeRender.RenderRevision, currentRender.RenderRevision)
			}
			currentTranscript, _ := w.HTMLNode(m.surface, surface.prefix+"control-transcript")
			currentEarlier, _ := w.HTMLNode(m.surface, "pencode-extension-earlier")
			currentHistory, _ := w.HTMLNode(m.surface, "pencode-extension-history")
			currentPrompt, _ := w.HTMLControl(m.surface, surface.prefix+"control-prompt")
			if currentTranscript != transcript || currentEarlier != earlier || currentHistory != history || currentPrompt != prompt {
				t.Fatal("message update replaced unrelated chat nodes")
			}
		}
		rendered := false
		for _, text := range app.World.Query[scene.Text2D]().All {
			rendered = rendered || text.Ref().Text().String() == update.text
		}
		if !rendered {
			t.Fatalf("message update %q was not rendered", update.text)
		}
		found := false
		for _, access := range w.Query[htmlui.Component[tabProps, struct{}]]() {
			props := access.Ref().Props
			if props.Surface == m.actions.ui.editor {
				found = props.Name == update.title && props.Busy == update.busy
			}
		}
		if !found {
			t.Fatal("tab metadata was not updated")
		}
	}
}
