package workbench

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/onix-engine/onix/scene"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestExtensionFillPanelScrollsLogsBelowFixedHeader(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	width, height := 1440, 900
	m.app.GetViewport = func() (int, int) { return width, height }
	css, err := os.ReadFile("../extensions/docker/resources/docker.css")
	if err != nil {
		t.Fatal(err)
	}
	process := &extensions.ActionSession{}
	show := func(lines int) {
		t.Helper()
		app.World.InsertResource(scene.ViewportSize{Width: float32(width), Height: float32(height), ScaleFactor: 1})
		if err := m.applyExtensionUI("docker", process, peb.UI{ID: "logs", Title: "Docker 日志 · web", Kind: peb.Panel, View: &peb.EditorView{
			HTML: `<section class="docker-logs" data-pencode-layout="fill"><header id="pencode-extension-docker-header" class="docker-log-header"><strong class="docker-log-title">Docker 日志 · web</strong><span class="docker-log-hint docker-muted">最近 300 行 · 含时间戳</span><div class="docker-actions" role="group" aria-label="日志操作"><button class="docker-icon-button" data-ref="back" type="button" title="容器列表"><img data-ref="back-icon" data-pencode-icon="list-collapse" alt=""></button><button class="docker-icon-button" data-ref="pause" type="button" title="暂停自动刷新"><img data-ref="pause-icon" data-pencode-icon="debug-pause" alt=""></button><button class="docker-icon-button" data-ref="refresh-logs" type="button" title="刷新日志"><img data-ref="refresh-icon" data-pencode-icon="refresh-cw" alt=""></button></div></header><div class="docker-log-scroll" data-ref="log-scroll" data-pencode-scroll="end" data-pencode-scroll-key="web" data-pencode-selectable><pre data-ref="log-output" data-pencode-region>` + strings.Repeat("2026-11-06T00:00:00Z application output\n", lines) + `</pre></div></section>`,
			CSS:  string(css), Events: []peb.EditorBinding{{Ref: "back", Kind: "click"}, {Ref: "pause", Kind: "click"}, {Ref: "refresh-logs", Kind: "click"}},
		}}); err != nil {
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
			t.Fatalf("missing node %s", id)
		}
		area, ok := node.ScrollContainer()
		if !ok {
			return nil
		}
		return w.Mut[scene.UIScroll](area)
	}
	show(100)
	surface := m.actions.ui.panel
	logID := surface.prefix + "control-log-scroll"
	checkLayout := func() {
		t.Helper()
		panel := editorContextBounds(t, m, surface.rootID)
		pane := editorContextBounds(t, m, "extension-pane")
		header := editorContextBounds(t, m, "pencode-extension-docker-header")
		logs := editorContextBounds(t, m, logID)
		if math.Abs(float64(panel.Height-pane.Height)) > 1 || math.Abs(float64(header.Y-panel.Y)) > 1 || header.Height > 40 {
			t.Fatalf("panel must have one fixed title row: panel=%+v pane=%+v header=%+v", panel, pane, header)
		}
		if logs.Y < header.Y+header.Height-1 || logs.Y+logs.Height > panel.Y+panel.Height+1 {
			t.Fatalf("log viewport must fit below the header: logs=%+v header=%+v panel=%+v", logs, header, panel)
		}
		for _, id := range []string{"extension-pane", surface.contentID} {
			if scroll := scrollAt(id); scroll != nil && scroll.ContentSize.Y() > scroll.ViewportSize.Y()+1 {
				t.Fatalf("outer panel %s must not scroll: %+v", id, scroll)
			}
		}
		refresh := editorContextBounds(t, m, surface.prefix+"control-refresh-logs")
		close := editorContextBounds(t, m, "panel-close")
		if refresh.X+refresh.Width > close.X || refresh.Y < header.Y || refresh.Y+refresh.Height > header.Y+header.Height {
			t.Fatalf("header action overlaps close or leaves its row: refresh=%+v close=%+v header=%+v", refresh, close, header)
		}
	}
	checkLayout()
	scroll := scrollAt(logID)
	if scroll == nil || scroll.ContentSize.Y()-scroll.ViewportSize.Y() < 100 {
		t.Fatal("long logs must scroll inside their viewport")
	}
	header := editorContextBounds(t, m, "pencode-extension-docker-header")
	scroll.Position[1] = -100
	show(120)
	if scrollAt(logID).Position.Y() != -100 || editorContextBounds(t, m, "pencode-extension-docker-header") != header {
		t.Fatal("new output moved the header or a user reading earlier logs")
	}
	width, height = 800, 600
	show(140)
	checkLayout()
	if allocations := testing.AllocsPerRun(100, func() { m.syncExtensionUI(w) }); allocations != 0 {
		t.Fatalf("unchanged fill panel allocated %g times", allocations)
	}
}
