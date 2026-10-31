package workbench

import (
	"os"
	"strings"
	"testing"

	"github.com/diiyw/onix/scene"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestCodexFileChangesLayout(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	css, err := os.ReadFile("testdata/codex/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	width, height := 1440, 900
	m.app.GetViewport = func() (int, int) { return width, height }
	process := &extensions.ActionSession{}
	for _, tc := range []struct {
		width, height int
		collapsed     bool
	}{{1440, 900, false}, {1024, 768, false}, {640, 800, false}, {1440, 900, true}, {640, 800, true}} {
		width, height = tc.width, tc.height
		listClass, rows := "codex-changes-list", 16
		if tc.collapsed {
			listClass += " codex-changes-preview"
			rows = 2
		}
		app.World.InsertResource(scene.ViewportSize{Width: float32(width), Height: float32(height), ScaleFactor: 1})
		if err := m.applyExtensionUI("codex", process, peb.UI{ID: "composer", Kind: peb.EditorTab, View: &peb.EditorView{
			HTML: `<main class="codex-workspace" data-pencode-layout="fill"><section class="codex-chat-pane"><div class="codex-transcript"><article class="codex-chat"><section id="pencode-extension-reply" class="codex-message"><p>修改完成。</p></section><section id="pencode-extension-changes" class="codex-changes"><div class="codex-changes-header"><img src="resources/icons/pencil.svg" alt=""><div id="pencode-extension-changes-title" class="codex-changes-title"><span>已编辑 4 个文件</span><span class="codex-change-counts"><span class="codex-change-added">+48</span><span class="codex-change-removed">-11</span></span></div><button id="pencode-extension-view-changes" type="button">查看变更</button></div><div id="pencode-extension-changes-list" class="` + listClass + `"><button id="pencode-extension-change-row" class="codex-change-file" type="button"><span id="pencode-extension-change-path" class="codex-change-path">` + strings.Repeat("extensions/codex/long-path/", 20) + `agent.go</span><span id="pencode-extension-change-counts" class="codex-change-counts"><span id="pencode-extension-change-added" class="codex-change-added">+1234</span><span id="pencode-extension-change-removed" class="codex-change-removed">-567</span></span></button>` + strings.Repeat(`<button class="codex-change-file" type="button"><span class="codex-change-path">extensions/codex/agent_test.go</span><span class="codex-change-counts"><span class="codex-change-added">+11</span><span class="codex-change-removed">-4</span></span></button>`, rows) + `</div><button id="pencode-extension-changes-toggle" class="codex-changes-toggle" type="button" aria-expanded="true">收起文件<img src="resources/icons/chevron-down.svg" alt=""></button></section></article></div></section><aside class="codex-sidebar">历史会话</aside></main>`,
			CSS:  string(css),
		}}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(m.fontResources, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
		card := editorContextBounds(t, m, "pencode-extension-changes")
		reply := editorContextBounds(t, m, "pencode-extension-reply")
		title := editorContextBounds(t, m, "pencode-extension-changes-title")
		view := editorContextBounds(t, m, "pencode-extension-view-changes")
		list := editorContextBounds(t, m, "pencode-extension-changes-list")
		row := editorContextBounds(t, m, "pencode-extension-change-row")
		path := editorContextBounds(t, m, "pencode-extension-change-path")
		counts := editorContextBounds(t, m, "pencode-extension-change-counts")
		added := editorContextBounds(t, m, "pencode-extension-change-added")
		removed := editorContextBounds(t, m, "pencode-extension-change-removed")
		toggle := editorContextBounds(t, m, "pencode-extension-changes-toggle")
		if card.Width > reply.Width+1 || card.Y < reply.Y+reply.Height || card.Width < 200 || list.Height > 321 {
			t.Fatalf("change card overflow at %dx%d: card=%+v reply=%+v list=%+v", width, height, card, reply, list)
		}
		if title.Height < 30 || view.Width < 55 || title.X+title.Width > view.X+1 || view.X+view.Width > card.X+card.Width+1 {
			t.Fatalf("change header overlap at %dx%d: title=%+v view=%+v card=%+v", width, height, title, view, card)
		}
		if path.Width <= 0 || counts.Width < 65 || row.Height > 38 || path.X+path.Width > counts.X+1 || counts.X+counts.Width > row.X+row.Width+1 || added.Width < 30 || removed.Width < 20 || removed.X < added.X+added.Width {
			t.Fatalf("change row overlap at %dx%d: row=%+v path=%+v counts=%+v added=%+v removed=%+v", width, height, row, path, counts, added, removed)
		}
		if toggle.Height < 20 || toggle.Y < list.Y+list.Height-1 {
			t.Fatalf("change toggle overlaps list at %dx%d: toggle=%+v list=%+v", width, height, toggle, list)
		}
		node, ok := m.fontResources.HTMLNode(m.surface, "pencode-extension-changes-list")
		if !ok {
			t.Fatal("missing changed file list")
		}
		area, scrolls := node.ScrollContainer()
		if tc.collapsed {
			if list.Height < 100 || list.Height > 115 {
				t.Fatalf("collapsed preview must fit three rows: %+v", list)
			}
			if scrolls {
				if scroll := m.fontResources.Mut[scene.UIScroll](area); scroll != nil && (scroll.Horizontal || scroll.Vertical) {
					t.Fatal("collapsed preview must not scroll", scroll)
				}
			}
		} else {
			if !scrolls {
				t.Fatal("expanded list must have a scroll container")
			}
			scroll := m.fontResources.Mut[scene.UIScroll](area)
			colors, ok := m.fontResources.HTMLScrollStyle(m.surface, "pencode-extension-changes-list")
			want := pencodeSyntaxColor(m.workbenchTheme.theme.Color("scrollbarSlider.background"))
			if scroll == nil || !scroll.ScrollbarThin || !ok || !colors.HasThumbColor || colors.ThumbColor != want || !colors.HasTrackColor || colors.TrackColor.A != 0 {
				t.Fatalf("changed file scrollbar must follow theme: scroll=%+v colors=%+v", scroll, colors)
			}
		}
	}
}
