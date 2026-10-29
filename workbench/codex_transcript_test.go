package workbench

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestCodexActivityExpansionRetainsTranscriptAtBottom(t *testing.T) {
	testCodexActivityExpansionAtBottom(t, false)
}

func TestCodexActivityExpansionRestoresRebuiltTranscriptBeforePaint(t *testing.T) {
	testCodexActivityExpansionAtBottom(t, true)
}

func testCodexActivityExpansionAtBottom(t *testing.T, rebuild bool) {
	t.Helper()
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	css, err := os.ReadFile("../extensions/codex/resources/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	m.app.GetViewport = func() (int, int) { return 1024, 768 }
	app.World.InsertResource(scene.ViewportSize{Width: 1024, Height: 768, ScaleFactor: 1})
	process := &extensions.ActionSession{}
	results := make(chan extensions.ActionResult, 1)
	process.Results = results
	m.actions.sessions = map[string]*extensions.ActionSession{"codex": process}
	t.Cleanup(func() { delete(m.actions.sessions, "codex") })
	draft := "draft"
	show := func(details string) {
		t.Helper()
		bindings := []peb.EditorBinding{{Ref: "activity/cmd", Kind: "click"}, {Ref: "prompt", Kind: "input"}}
		if details != "" {
			bindings = append(bindings, peb.EditorBinding{Ref: "item/cmd", Kind: "click"})
		}
		results <- extensions.ActionResult{UI: &peb.UI{ID: "composer", Kind: peb.EditorTab, View: &peb.EditorView{
			HTML: `<main class="codex-workspace" data-pencode-layout="fill"><section class="codex-chat-pane"><div class="codex-transcript" data-ref="transcript" data-pencode-scroll="end" data-pencode-scroll-key="task"><article class="codex-chat">` + strings.Repeat(`<section class="codex-message"><p>Earlier message</p></section>`, 40) + `<section class="codex-activity-group" data-ref="activity/cmd/region" data-pencode-region><button class="codex-activity-summary" data-ref="activity/cmd" type="button">运行了命令</button>` + details + `</section><section id="pencode-extension-last-reply" class="codex-message"><p>Completed</p></section></article></div><section class="codex-composer"><textarea data-ref="prompt" class="codex-prompt">` + draft + `</textarea></section></section></main>`,
			CSS:  string(css), Events: bindings,
		}}}
	}
	show("")
	for range 3 {
		languageInteractionStep(t, app)
	}
	sourceWait(t, app, m)
	s := m.actions.ui.editor
	root, _ := w.Get[htmlui.Component[appProps, appState]](m.root)
	editor, _ := w.Get[htmlui.Control](m.editor.control)
	tree, ok := w.HTMLNode(m.surface, m.projectTreeRowID(m.activeProjectState(), "main.go"))
	if !ok {
		t.Fatal("missing file tree row")
	}
	treeControl, ok := w.HTMLControl(m.surface, m.projectTreeRowID(m.activeProjectState(), "main.go"))
	if !ok {
		t.Fatal("missing file tree control")
	}
	treeRender, _ := w.Get[htmlui.Control](treeControl)
	definition := s.definition
	transcript, ok := w.HTMLNode(m.surface, s.prefix+"control-transcript")
	if !ok {
		t.Fatal("missing transcript")
	}
	area, ok := transcript.ScrollContainer()
	if !ok {
		t.Fatal("missing transcript scroll container")
	}
	reply, _ := w.HTMLNode(m.surface, "pencode-extension-last-reply")
	prompt, _ := w.HTMLControl(m.surface, s.prefix+"control-prompt")
	list := `<div class="codex-activity-list" data-ref="activity/cmd/list" data-pencode-scroll="preserve"><section class="codex-message codex-activity"><button class="codex-activity-summary" data-ref="item/cmd" type="button">已运行 go test ./...</button>`
	for toggle, details := range []string{
		list + `</section></div>`,
		list + `<div class="codex-activity-details"><pre><code>` + strings.Repeat("test output\n", 40) + `</code></pre></div></section></div>`,
		list + `</section></div>`,
		"",
	} {
		if rebuild {
			// A draft edited since the previous publication also changes the
			// outer template when the activity toggle publishes the view.
			draft += "."
		}
		show(details)
		// Check the first rendered frame as well as the settled layout.
		for frame := range 3 {
			languageInteractionStep(t, app)
			currentRoot, _ := w.Get[htmlui.Component[appProps, appState]](m.root)
			currentEditor, _ := w.Get[htmlui.Control](m.editor.control)
			currentTree, ok := w.HTMLNode(m.surface, m.projectTreeRowID(m.activeProjectState(), "main.go"))
			currentTreeRender, _ := w.Get[htmlui.Control](treeControl)
			if currentTreeRender.RenderRevision != treeRender.RenderRevision {
				t.Fatalf("tree control refreshed on toggle %d frame %d: %d -> %d", toggle, frame, treeRender.RenderRevision, currentTreeRender.RenderRevision)
			}
			if currentRoot.State.Revision != root.State.Revision || currentEditor.RenderRevision != editor.RenderRevision || !ok || currentTree != tree {
				t.Fatalf("activity toggle refreshed workbench on toggle %d frame %d: root %d -> %d, editor %d -> %d, tree retained %t", toggle, frame, root.State.Revision, currentRoot.State.Revision, editor.RenderRevision, currentEditor.RenderRevision, ok && currentTree == tree)
			}
			current, ok := w.HTMLNode(m.surface, s.prefix+"control-transcript")
			if !ok || !rebuild && (current != transcript || s.definition != definition) {
				t.Fatal("expanding activity rebuilt the transcript")
			}
			area, ok = current.ScrollContainer()
			if !ok {
				t.Fatal("missing updated scroll container")
			}
			if current, ok := w.HTMLNode(m.surface, "pencode-extension-last-reply"); !ok || !rebuild && current != reply {
				t.Fatal("expanding activity rebuilt an existing message")
			}
			if current, ok := w.HTMLControl(m.surface, s.prefix+"control-prompt"); !ok || !rebuild && current != prompt {
				t.Fatal("expanding activity rebuilt the composer")
			}
			if _, ok := w.HTMLControl(m.surface, s.prefix+"control-item/cmd"); ok != (details != "") {
				t.Fatal("activity expansion did not update the command control")
			}
			scroll, ok := w.Get[scene.UIScroll](area)
			if !ok || scroll.Position.Y() >= 0 || math.Abs(float64(scroll.Position.Y()+scroll.ContentSize.Y()-scroll.ViewportSize.Y())) > 1 {
				t.Fatalf("activity toggle lost the bottom position on frame %d: %+v", frame, scroll)
			}
			for entity, values := range app.World.Query2[htmlui.ScrollContent, scene.UITransform]().All {
				content, transform := values.A.Get(), values.B.Get()
				if content.Area == area && math.Abs(float64(transform.Translation.Y.Value-content.Y-scroll.Position.Y())) > 1 {
					t.Fatalf("paint saw stale scroll offset on toggle %d frame %d entity %v: y=%g, want %g", toggle, frame, entity, transform.Translation.Y.Value, content.Y+scroll.Position.Y())
				}
			}
			thumbFound := false
			for _, values := range app.World.Query3[htmlui.ScrollbarPart, scene.UITransform, scene.Size2D]().All {
				part := values.A.Get()
				if part.Area == area && part.Axis == htmlui.ScrollbarVertical && part.Kind == htmlui.ScrollbarThumb {
					thumbFound = true
					bottom := values.B.Get().Translation.Y.Value + values.C.Get().Height
					if math.Abs(float64(bottom-scroll.ViewportSize.Y())) > 1 {
						t.Fatalf("paint saw the scrollbar away from the bottom on toggle %d frame %d: bottom=%g viewport=%g", toggle, frame, bottom, scroll.ViewportSize.Y())
					}
				}
			}
			if !thumbFound {
				t.Fatal("missing transcript scrollbar thumb")
			}
			viewport := editorContextBounds(t, m, s.prefix+"control-transcript")
			last := editorContextBounds(t, m, "pencode-extension-last-reply")
			last.Y += scroll.Position.Y()
			if last.Y+last.Height < viewport.Y || last.Y+last.Height > viewport.Y+viewport.Height+1 {
				t.Fatalf("last message disappeared on frame %d: viewport=%+v message=%+v", frame, viewport, last)
			}
			painted := false
			for _, text := range app.World.Query[scene.Text2D]().All {
				painted = painted || text.Ref().Value == "Completed"
			}
			if !painted {
				t.Fatalf("last message was not mounted for painting on toggle %d frame %d", toggle, frame)
			}
		}
	}
	if allocs := testing.AllocsPerRun(100, func() { m.syncExtensionUI(w) }); allocs != 0 {
		t.Fatalf("unchanged extension UI allocated %g", allocs)
	}
}

func TestCodexTranscriptMessageLayout(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	css, err := os.ReadFile("../extensions/codex/resources/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	width, height := 1440, 900
	m.app.GetViewport = func() (int, int) { return width, height }
	process := &extensions.ActionSession{}
	for _, size := range [][2]int{{1440, 900}, {1024, 768}, {800, 600}, {640, 800}} {
		width, height = size[0], size[1]
		app.World.InsertResource(scene.ViewportSize{Width: float32(width), Height: float32(height), ScaleFactor: 1})
		if err := m.applyExtensionUI("codex", process, peb.UI{ID: "composer", Kind: peb.EditorTab, View: &peb.EditorView{
			HTML: `<main class="codex-workspace" data-pencode-layout="fill"><section class="codex-chat-pane"><div class="codex-transcript"><article class="codex-chat"><section id="pencode-extension-short-user" class="codex-message codex-user"><div class="codex-text">git pull</div></section><section id="pencode-extension-long-user" class="codex-message codex-user"><div class="codex-text">` + strings.Repeat("调整聊天列表布局，保留输入框。", 12) + `</div></section><section id="pencode-extension-assistant" class="codex-message"><p>我会检查当前分支，然后拉取更新。</p></section><section id="pencode-extension-activity" class="codex-message codex-activity"><button id="pencode-extension-summary" class="codex-activity-summary" type="button" aria-expanded="false"><img src="resources/icons/terminal.svg" alt=""><span id="pencode-extension-activity-label">运行了命令</span><span id="pencode-extension-activity-status" class="codex-activity-status codex-status">失败</span><img class="codex-activity-chevron" src="resources/icons/chevron-down.svg" alt=""></button></section></article></div></section><aside class="codex-sidebar">历史会话</aside></main>`,
			CSS:  string(css),
		}}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(m.fontResources, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
		short := editorContextBounds(t, m, "pencode-extension-short-user")
		long := editorContextBounds(t, m, "pencode-extension-long-user")
		assistant := editorContextBounds(t, m, "pencode-extension-assistant")
		activity := editorContextBounds(t, m, "pencode-extension-activity")
		summary := editorContextBounds(t, m, "pencode-extension-summary")
		label := editorContextBounds(t, m, "pencode-extension-activity-label")
		status := editorContextBounds(t, m, "pencode-extension-activity-status")
		if label.Width < 60 || status.Width < 20 || status.X < label.X+label.Width {
			t.Fatalf("activity labels were truncated or overlapped at %dx%d: label=%+v status=%+v summary=%+v", width, height, label, status, summary)
		}
		if short.Width <= 40 || short.Width >= assistant.Width/2 || long.Width > assistant.Width+1 || long.Height <= short.Height {
			t.Fatalf("message sizing at %dx%d: short=%+v long=%+v assistant=%+v", width, height, short, long, assistant)
		}
		if math.Abs(float64(short.X+short.Width-assistant.X-assistant.Width)) > 1 || math.Abs(float64(long.X+long.Width-short.X-short.Width)) > 1 {
			t.Fatalf("user bubbles are not aligned right at %dx%d: short=%+v long=%+v assistant=%+v", width, height, short, long, assistant)
		}
		if math.Abs(float64(activity.X-assistant.X)) > 1 || summary.Width <= 40 || summary.Height > 32 {
			t.Fatalf("activity is not a compact left-aligned row at %dx%d: assistant=%+v activity=%+v summary=%+v", width, height, assistant, activity, summary)
		}
	}
}

func TestCodexActivityGroupLayout(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	css, err := os.ReadFile("../extensions/codex/resources/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	width, height := 1440, 900
	m.app.GetViewport = func() (int, int) { return width, height }
	process := &extensions.ActionSession{}
	for _, size := range [][2]int{{1440, 900}, {640, 800}} {
		width, height = size[0], size[1]
		app.World.InsertResource(scene.ViewportSize{Width: float32(width), Height: float32(height), ScaleFactor: 1})
		row := `<section class="codex-message codex-activity"><button class="codex-activity-summary" type="button" aria-expanded="false"><img src="resources/icons/terminal.svg" alt=""><span>已运行 go test ./... -count=1 -timeout=60s</span><img class="codex-activity-chevron" src="resources/icons/chevron-down.svg" alt=""></button></section>`
		if err := m.applyExtensionUI("codex", process, peb.UI{ID: "composer", Kind: peb.EditorTab, View: &peb.EditorView{
			HTML: `<main class="codex-workspace" data-pencode-layout="fill"><section class="codex-chat-pane"><div class="codex-transcript"><article class="codex-chat"><section id="pencode-extension-group" class="codex-activity-group"><button id="pencode-extension-group-summary" class="codex-activity-summary" type="button" aria-expanded="true"><img src="resources/icons/pencil.svg" alt=""><span>编辑了文件、读取了文件、列出了文件、进行了搜索、运行了命令</span><span id="pencode-extension-group-status" class="codex-activity-status codex-status">失败</span><img class="codex-activity-chevron" src="resources/icons/chevron-down.svg" alt=""></button><div id="pencode-extension-activity-list" class="codex-activity-list" data-ref="activity/list" data-pencode-scroll="preserve"><section class="codex-message codex-activity"><button id="pencode-extension-command-summary" class="codex-activity-summary" type="button" aria-expanded="true"><img src="resources/icons/terminal.svg" alt=""><span id="pencode-extension-command-label">已运行 ` + strings.Repeat("go test ./... ", 30) + `</span><img class="codex-activity-chevron" src="resources/icons/chevron-down.svg" alt=""></button><div id="pencode-extension-command-details" class="codex-activity-details"><pre><code>` + strings.Repeat("test output\n", 8) + `</code></pre></div></section>` + strings.Repeat(row, 16) + `</div></section><section id="pencode-extension-group-reply" class="codex-message"><p>检查完成。</p></section></article></div></section><aside class="codex-sidebar">历史会话</aside></main>`,
			CSS:  string(css),
		}}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(m.fontResources, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
		group := editorContextBounds(t, m, "pencode-extension-group")
		summary := editorContextBounds(t, m, "pencode-extension-group-summary")
		status := editorContextBounds(t, m, "pencode-extension-group-status")
		list := editorContextBounds(t, m, "pencode-extension-activity-list")
		command := editorContextBounds(t, m, "pencode-extension-command-summary")
		label := editorContextBounds(t, m, "pencode-extension-command-label")
		details := editorContextBounds(t, m, "pencode-extension-command-details")
		reply := editorContextBounds(t, m, "pencode-extension-group-reply")
		if summary.Height > 32 || command.Height > 32 || status.Width < 20 || status.X+status.Width > summary.X+summary.Width+1 {
			t.Fatalf("activity summaries must remain compact at %dx%d: summary=%+v command=%+v status=%+v", width, height, summary, command, status)
		}
		if list.Height < 200 || list.Height > 241 || group.Height > 280 || reply.Y < group.Y+group.Height {
			t.Fatalf("activity list must scroll within the group at %dx%d: list=%+v group=%+v reply=%+v", width, height, list, group, reply)
		}
		if label.X+label.Width > command.X+command.Width+1 || details.X+details.Width > list.X+list.Width+1 || list.X+list.Width > group.X+group.Width+1 {
			t.Fatalf("activity content overflowed horizontally at %dx%d: group=%+v list=%+v command=%+v label=%+v details=%+v", width, height, group, list, command, label, details)
		}
	}
}
