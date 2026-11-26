package workbench

import (
	"math"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestDockerDisabledActionIconFollowsTheme(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	css, err := os.ReadFile("../extensions/docker/resources/docker.css")
	if err != nil {
		t.Fatal(err)
	}
	const id = "pencode-extension-docker-disabled"
	view := &peb.EditorView{
		HTML: `<section class="docker-manager"><button type="button" class="docker-icon-button" title="启动 · web" aria-label="启动 · web" disabled><img id="` + id + `" data-ref="start-icon" data-pencode-icon="debug-start" alt=""></button></section>`,
		CSS:  string(css),
	}
	if err := m.applyExtensionUI("docker", &extensions.ActionSession{}, peb.UI{ID: "containers", Kind: peb.Popup, View: view}); err != nil {
		t.Fatal(err)
	}
	for _, theme := range []string{"dark-plus", "light-plus"} {
		m.selectTheme(theme)
		markPencodeRootDirtyByModel(m.fontResources, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
		icon, ok := m.fontResources.HTMLNode(m.surface, id)
		if !ok {
			t.Fatal("disabled action icon is missing")
		}
		source, _ := icon.Attribute("src")
		want := "icons/debug-start.svg?foreground=" + m.workbenchTheme.theme.Color("descriptionForeground").Hex()[1:]
		if source != want {
			t.Fatalf("disabled action icon in %s: got %s, want %s", theme, source, want)
		}
	}
}

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
	key := "web"
	show := func(lines int) {
		t.Helper()
		app.World.InsertResource(scene.ViewportSize{Width: float32(width), Height: float32(height), ScaleFactor: 1})
		if err := m.applyExtensionUI("docker", process, peb.UI{ID: "logs", Title: "Docker 日志 · " + key, Kind: peb.Panel, View: &peb.EditorView{
			HTML: `<section class="docker-logs" data-pencode-layout="fill"><header id="pencode-extension-docker-header" class="docker-log-header"><strong class="docker-log-title">Docker 日志</strong><select class="docker-log-container" data-ref="log-container" aria-label="选择日志容器"><option value="` + key + `" selected>` + key + `</option></select><span class="docker-log-hint docker-muted">最近 300 行 · 含时间戳</span><div class="docker-actions" data-ref="log-actions" data-pencode-region role="group" aria-label="日志操作"><button class="docker-icon-button" data-ref="pause" type="button" title="暂停自动刷新"><img data-ref="pause-icon" data-pencode-icon="debug-pause" alt=""></button><button class="docker-icon-button" data-ref="refresh-logs" type="button" title="刷新日志"><img data-ref="refresh-icon" data-pencode-icon="refresh-cw" alt=""></button></div></header><div class="docker-log-body" data-ref="log-output" data-pencode-region><textarea class="docker-log-terminal" data-ref="terminal" readonly wrap="off" data-pencode-terminal-output="` + key + `">` + strings.Repeat("2026-11-06T00:00:00Z application output\n", lines) + "\x1b[31;4;44m中文 e\u0301\x1b[0m\nold&#13;new &lt;log&gt;" + `</textarea></div></section>`,
			CSS:  string(css), Events: []peb.EditorBinding{{Ref: "log-container", Kind: "change"}, {Ref: "pause", Kind: "click"}, {Ref: "refresh-logs", Kind: "click"}},
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
	logID := surface.prefix + "control-terminal"
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
		picker := editorContextBounds(t, m, surface.prefix+"control-log-container")
		if picker.Width < 40 || picker.X < header.X || picker.X+picker.Width > refresh.X || picker.Y+picker.Height > header.Y+header.Height {
			t.Fatalf("container selector must fit in the header: picker=%+v header=%+v", picker, header)
		}
		close := editorContextBounds(t, m, "panel-close")
		if refresh.X+refresh.Width > close.X || refresh.Y < header.Y || refresh.Y+refresh.Height > header.Y+header.Height {
			t.Fatalf("header action overlaps close or leaves its row: refresh=%+v close=%+v header=%+v", refresh, close, header)
		}
	}
	checkLayout()
	terminal := func() *engineui.Textarea {
		t.Helper()
		control, ok := w.HTMLControl(m.surface, logID)
		if !ok {
			t.Fatal("terminal missing")
		}
		field := w.Ref[engineui.TextField](control)
		if field == nil {
			t.Fatal("terminal field missing")
		}
		if !strings.HasSuffix(field.Value, "中文 e\u0301\nnew <log>") {
			t.Fatalf("unexpected transcript: %q", field.Value[max(0, len(field.Value)-100):])
		}
		visual, _ := w.FindPart[engineui.ValuePart, scene.Text2D](control)
		paint := w.Ref[scene.Text2D](visual)
		if paint == nil || paint.Font != m.codeFont() || len(paint.Spans) < 2 {
			t.Fatal("missing terminal font or spans")
		}
		return w.Mut[engineui.Textarea](control)
	}
	area := terminal()
	if area.ScrollY < 100 {
		t.Fatalf("log must open at the end: %v", area.ScrollY)
	}
	header := editorContextBounds(t, m, "pencode-extension-docker-header")
	area.RestoreScroll(0, 100)
	show(120)
	if terminal().ScrollY != 100 || editorContextBounds(t, m, "pencode-extension-docker-header") != header {
		t.Fatalf("new log moved reading position: %v", terminal().ScrollY)
	}
	old := surface.terminals[0].paint
	key = "worker"
	show(180)
	if terminal().ScrollY <= 100 || surface.terminals[0].paint == old {
		t.Fatal("a different container must open at its own tail")
	}
	width, height = 800, 600
	show(140)
	checkLayout()
	if allocations := testing.AllocsPerRun(100, func() { m.syncExtensionUI(w) }); allocations != 0 {
		t.Fatalf("unchanged fill panel allocated %g times", allocations)
	}
	for _, theme := range []string{"dark-plus", "light-plus"} {
		m.selectTheme(theme)
		markPencodeRootDirtyByModel(w, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
		bar, ok := w.HTMLScrollStyle(m.surface, logID)
		want := pencodeSyntaxColor(m.workbenchTheme.theme.Color("scrollbarSlider.background"))
		if !ok || !bar.HasThumbColor || bar.ThumbColor != want || !bar.HasTrackColor || bar.TrackColor.A != 0 || terminal().BarSize != bar.BarSize {
			t.Fatalf("log scrollbar does not follow %s: %+v", theme, bar)
		}
	}
}

func TestExtensionTerminalSnapshotReplay(t *testing.T) {
	m := &workspaceModel{}
	var log extensionTerminal
	check := func(cols, rows int) {
		t.Helper()
		log.render(m, cols, rows)
		fresh := extensionTerminal{source: log.source}
		fresh.render(m, cols, rows)
		if log.value != fresh.value || !slices.Equal(log.spans, fresh.spans) || !slices.Equal(log.backgrounds, fresh.backgrounds) || !slices.Equal(log.underlines, fresh.underlines) {
			t.Fatalf("incremental transcript differs from replay at %dx%d", cols, rows)
		}
	}
	log.setSource("old\rnew\n\x1b[31;4")
	check(10, 3)
	log.setSource(log.source + "m中\xe2")
	check(10, 3)
	log.setSource(log.source + "\x82\xac")
	log.setSource(log.source + "\x1b[0m!\n")
	check(10, 3)
	log.setSource("replacement")
	log.setSource("replacement\n\x1b[44mcolored row")
	check(10, 3)
	check(5, 4)
	log.setSource(strings.Repeat("line\n", 2500))
	check(100, 24)
	if cap(log.screen.History) < 8192 {
		t.Fatal("fixture did not build a large scrollback")
	}
	log.setSource("short\n")
	check(100, 24)
	if cap(log.screen.History) != 0 {
		t.Fatal("short snapshot retained oversized scrollback")
	}
}
