package workbench

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestCodexWorkspaceColumnsRetainInputAndFollowTranscript(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	css, err := os.ReadFile("testdata/codex/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	width, height := 1440, 900
	m.app.GetViewport = func() (int, int) { return width, height }
	process := &extensions.ActionSession{}
	show := func(key string, lines int) {
		t.Helper()
		app.World.InsertResource(scene.ViewportSize{Width: float32(width), Height: float32(height), ScaleFactor: 1})
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
		return w.Mut[scene.UIScroll](area)
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
	field := w.Mut[engineui.TextField](control)
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
	field = w.Mut[engineui.TextField](control)
	if field.Text().String() != "draft" || field.Anchor != 2 || field.Caret != 4 {
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
