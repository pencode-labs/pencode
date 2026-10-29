package workbench

import (
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/diiyw/onix"
	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

func settleSourceHighlight(t *testing.T, m *workspaceModel) {
	t.Helper()
	if m.backend == nil {
		m.backend = newPencodeBackend(t)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		renderPencodeHighlight(m)
		m.pollHighlight()
		tab := m.sourceState().activeTab()
		count := 2
		if tab.Kind == "merge" {
			count = 3
		}
		ready := true
		for side := range count {
			h := &tab.Highlight[side]
			ready = ready && h.ready && h.source == tab.sourceText(side) && h.theme == m.syntaxTheme && h.definitions == m.syntaxDefinitions
		}
		if ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("comparison highlighting did not complete")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSourceEditorHighlightSnapshots(t *testing.T) {
	const local = "package demo\n/* 中文\ncontinued */\nvar Value = 1\n"
	const incoming = "package demo\n// inserted\n/* 中文\ncontinued */\nvar Value = 2\n"
	tab := &sourceTab{Key: "merge::main.go", Path: "main.go", Kind: "merge", Local: local, Incoming: incoming, Result: local}
	m := &workspaceModel{sourceControl: pencodeSourceControl{Tabs: []*sourceTab{tab}, Active: tab.Key}}
	defer m.closeHighlightWorker()
	settleSourceHighlight(t, m)
	for side := range tab.Highlight {
		h := &tab.Highlight[side]
		if len(h.spans) == 0 || len(h.spans) != len(h.styles) {
			t.Fatalf("pane %d lacks syntax paint", side)
		}
		var b sourceVNodeView
		b.reset()
		line := 3
		if side == 1 {
			line = 4
		}
		h.renderLine(&b, "continued */", line)
		text, styled := "", false
		for _, node := range b.nodes() {
			text += node.Text
			styled = styled || len(node.Attributes) > 0
		}
		if text != "continued */" || !styled {
			t.Fatalf("pane %d lost multiline/UTF-8 highlighting: %q", side, text)
		}
	}
	obsolete := pencodeHighlightResult{job: pencodeHighlightJob{
		sourceDoc: &tab.Highlight[2], source: local, theme: m.syntaxTheme, definitions: m.syntaxDefinitions, revision: m.highlightRevision,
	}}
	tab.editResult(incoming)
	if m.acceptSourceHighlight(&obsolete) {
		t.Fatal("merge accepted stale paint after editing")
	}
	settleSourceHighlight(t, m)
	tab.undoResult(false)
	settleSourceHighlight(t, m)
	m.syntaxTheme, _ = syntax.BuiltinTheme("light-plus")
	settleSourceHighlight(t, m)
	if allocations := testing.AllocsPerRun(100, func() { renderPencodeHighlight(m); m.pollHighlight() }); allocations != 0 {
		t.Fatalf("idle comparison highlighting allocated %g times", allocations)
	}
}

func TestSourceEditorScrollBothDirections(t *testing.T) {
	var tab sourceTab
	left := &scene.UIScroll{ViewportSize: [2]float32{500, 400}, ContentSize: [2]float32{2500, 8000}}
	right := &scene.UIScroll{ViewportSize: left.ViewportSize, ContentSize: left.ContentSize}
	scrolls := [2]*scene.UIScroll{left, right}
	for _, leader := range scrolls {
		leader.Position = [2]float32{-500, -2000}
		leader.Momentum = [2]float32{2, 3}
		tab.syncScroll(scrolls, false)
		if left.Position != right.Position || tab.ScrollX != -500 || tab.ScrollY != -2000 {
			t.Fatalf("pane did not synchronize both axes: left=%v right=%v", left.Position, right.Position)
		}
		other := left
		if leader == left {
			other = right
		}
		if other.Momentum != [2]float32{} {
			t.Fatal("follower retained independent momentum")
		}
		leader.Position = [2]float32{-200, -1000}
		tab.syncScroll(scrolls, false)
		if left.Position != right.Position || tab.ScrollX != -200 || tab.ScrollY != -1000 {
			t.Fatal("reverse scrolling did not synchronize")
		}
	}
	// A narrower range must not pull the originating pane back on the next frame.
	right.ContentSize[0] = 800
	left.Position[0] = -1500
	tab.syncScroll(scrolls, false)
	tab.syncScroll(scrolls, false)
	if left.Position[0] != -1500 || right.Position[0] != -300 {
		t.Fatal("clamped follower was mistaken for input")
	}
	if allocations := testing.AllocsPerRun(100, func() {
		left.Position[1] = -1200
		tab.syncScroll(scrolls, false)
	}); allocations != 0 {
		t.Fatalf("scroll synchronization allocated %g times", allocations)
	}
}

func TestSourceEditorFirstChangeTabsAndNativeScroll(t *testing.T) {
	r := sourceTestRepository(t)
	const name = "main.go"
	prefix := "package demo\n" + strings.Repeat("// "+strings.Repeat("context ", 30)+"\n", 250)
	tail := strings.Repeat("// trailing context\n", 80)
	sourceTestWrite(t, r, name, prefix+"var Value = 1\n"+tail)
	sourceTestGit(t, r, "add", "--all")
	sourceTestGit(t, r, "commit", "-m", "base")
	sourceTestWrite(t, r, name, prefix+"var Value = 2\nvar AddedA = 1\nvar AddedB = 2\nvar AddedC = 3\n"+tail)
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", name)
	app, m, _, state := languageInteractionFixture(t)
	app.SetRenderMode(onix.RenderOnDemand)
	m.sourceAction("sc-toggle")
	sourceWait(t, app, m)
	m.sourceAction("sc-open:changes:" + name)
	sourceWait(t, app, m)
	s := m.sourceState()
	tab := s.activeTab()
	settleSourceHighlight(t, m)
	m.sourceChanged()
	languageInteractionStep(t, app)
	if m.editorHeader.filePath != name || m.editorHeader.fullPath != filepath.Join(r.Root, name) {
		t.Fatalf("comparison did not reuse file breadcrumbs: %+v", m.editorHeader)
	}
	header := editorContextBounds(t, m, "editor-header")
	tabs := editorContextBounds(t, m, "tabs")
	close := editorContextBounds(t, m, "sc-close-tab:"+tab.Key)
	if header.Height <= 0 || close.Y < tabs.Y || close.Y+close.Height > tabs.Y+tabs.Height {
		t.Fatal("comparison close button is outside the tab strip")
	}
	var scrolls [2]*scene.UIScroll
	for i, id := range [...]string{"sc-code-left", "sc-code-right"} {
		node, _ := m.fontResources.HTMLNode(m.surface, id)
		entity, ok := node.ScrollContainer()
		if !ok {
			t.Fatal("missing diff scroll container")
		}
		scrolls[i] = m.fontResources.Mut[scene.UIScroll](entity)
	}
	changeY := float32(251*20) + scrolls[0].Position[1]
	if changeY < 0 || changeY >= scrolls[0].ViewportSize[1] || tab.FirstRow == 0 {
		t.Fatalf("first change not revealed: y=%g viewport=%g firstRow=%d", changeY, scrolls[0].ViewportSize[1], tab.FirstRow)
	}
	assertSourceDiffGutterIcon(t, m)
	// Retained text geometry must keep both number columns fixed while the
	// native scroll position changes, without creating a window or screenshot.
	var numberEntities [2]ecs.Entity
	var numberPositions [2][2]float32
	label := strconv.Itoa(tab.Rows[251].LeftNumber)
	for entity, access := range m.fontResources.Query[scene.Text2D]() {
		if access.Ref().Value != label {
			continue
		}
		transform, ok := m.fontResources.Get[scene.UIGlobalTransform](entity)
		if !ok {
			continue
		}
		x, y := transform.Matrix[12], transform.Matrix[13]
		for side, id := range [...]string{"sc-code-left", "sc-code-right"} {
			bounds := editorContextBounds(t, m, id)
			if x >= bounds.X-50 && x < bounds.X && y >= bounds.Y && y < bounds.Y+bounds.Height {
				numberEntities[side], numberPositions[side] = entity, [2]float32{x, y}
			}
		}
	}
	for side, entity := range numberEntities {
		if entity == 0 {
			t.Fatalf("pane %d has no visible line number %s", side, label)
		}
	}
	scrolls[0].Position[0] = -300
	languageInteractionStep(t, app)
	for side, entity := range numberEntities {
		transform, _ := m.fontResources.Get[scene.UIGlobalTransform](entity)
		if transform.Matrix[12] != numberPositions[side][0] || transform.Matrix[13] != numberPositions[side][1] {
			t.Fatalf("pane %d line number moved horizontally: %v", side, transform.Matrix)
		}
	}
	scrolls[0].Position[1] -= 20
	languageInteractionStep(t, app)
	for side, entity := range numberEntities {
		transform, _ := m.fontResources.Get[scene.UIGlobalTransform](entity)
		if transform.Matrix[12] != numberPositions[side][0] || math.Abs(float64(transform.Matrix[13]-numberPositions[side][1]+20)) > .01 {
			t.Fatalf("pane %d line number lost vertical alignment: %v", side, transform.Matrix)
		}
	}
	for _, id := range []string{"sc-code-left", "sc-code-right"} {
		bounds := editorContextBounds(t, m, id)
		zoom := float64(m.settings.Zoom) / 100
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(bounds.X+100) * zoom, Y: float64(bounds.Y+100) * zoom})
		// Check the input frame itself, including reversal and virtual-window
		// replacement. Extra settling frames used to hide stale pane geometry.
		for _, wheel := range [][2]float64{{-2, -2}, {2, 2}, {-2, -12}, {2, 12}} {
			state.Enqueue(input.Event{Kind: input.EventMouseWheel, WheelX: wheel[0], WheelY: wheel[1]})
			languageInteractionStep(t, app)
			if scrolls[0].Position != scrolls[1].Position || scrolls[0].Position[0] >= 0 {
				t.Fatalf("native wheel is not synchronized: left=%v right=%v", scrolls[0].Position, scrolls[1].Position)
			}
			if tab.FirstRow != sourceFirstVisibleRow(tab.ScrollY) {
				t.Fatal("virtual row window lagged behind wheel input")
			}
			assertSourceDiffScrollGeometry(t, m)
		}
	}
	if allocations := testing.AllocsPerRun(100, func() { m.syncSourceScroll(m.fontResources) }); allocations != 0 {
		t.Fatalf("retained comparison scroll synchronization allocated %g times", allocations)
	}
	x, y := tab.ScrollX, tab.ScrollY
	m.selectProjectTab(m.activeProjectState(), name)
	m.sourceChanged()
	languageInteractionStep(t, app)
	m.sourceAction("sc-tab:" + tab.Key)
	languageInteractionStep(t, app)
	if tab.ScrollX != x || tab.ScrollY != y {
		t.Fatal("reactivating tab reset its reading position")
	}
	// Closing an inactive comparison must leave the selected file editor intact.
	m.selectProjectTab(m.activeProjectState(), name)
	m.sourceChanged()
	languageInteractionStep(t, app)
	editorContextClick(t, app, m, state, "sc-close-tab:"+tab.Key)
	if s.visible() || len(s.Tabs) != 0 || !m.textEditorActive() {
		t.Fatal("closing inactive comparison changed the active editor")
	}
}

func assertSourceDiffScrollGeometry(t *testing.T, m *workspaceModel) {
	t.Helper()
	tab := m.sourceState().activeTab()
	row := min(int(-tab.ScrollY)/20+1, len(tab.Rows)-1)
	for row < len(tab.Rows)-1 && (tab.Rows[row].LeftNumber == 0 || tab.Rows[row].RightNumber == 0) {
		row++
	}
	var numbers [2]bool
	var bounds [2]htmlui.Rect
	var labels [2]string
	for side, id := range [...]string{"sc-code-left", "sc-code-right"} {
		bounds[side] = editorContextBounds(t, m, id)
		control, _ := m.fontResources.HTMLControl(m.surface, id+"-input")
		entity, _ := m.fontResources.FindPart[engineui.ValuePart, scene.Text2D](control)
		transform := m.fontResources.Ref[scene.UIGlobalTransform](entity)
		if math.Abs(float64(transform.Matrix[13]-bounds[side].Y-tab.ScrollY)) > .01 {
			t.Fatalf("pane %d native text lagged behind scroll: y=%g scroll=%g", side, transform.Matrix[13], tab.ScrollY)
		}
		number := tab.Rows[row].LeftNumber
		if side == 1 {
			number = tab.Rows[row].RightNumber
		}
		text := m.fontResources.Ref[scene.Text2D](entity)
		if text.LineLayout.Offsets[number-1] != float32(row*20) {
			t.Fatalf("pane %d native line %d lost diff padding", side, number)
		}
		labels[side] = strconv.Itoa(number)
	}
	for entity, access := range m.fontResources.Query[scene.Text2D]() {
		for side, label := range labels {
			if access.Ref().Value != label {
				continue
			}
			global := m.fontResources.Ref[scene.UIGlobalTransform](entity)
			if global.Matrix[12] < bounds[side].X-50 || global.Matrix[12] >= bounds[side].X {
				continue
			}
			numbers[side] = true
			if math.Abs(float64(global.Matrix[13]-bounds[side].Y-tab.ScrollY-float32(row*20))) > .01 {
				t.Fatalf("pane %d line number %s lagged behind scroll: y=%g scroll=%g", side, label, global.Matrix[13], tab.ScrollY)
			}
		}
	}
	if !numbers[0] || !numbers[1] {
		t.Fatalf("visible row %d is missing line number paint: %v", row, numbers)
	}
}

// Check the retained paint and clip geometry without opening an editor window.
func assertSourceDiffGutterIcon(t *testing.T, m *workspaceModel) {
	t.Helper()
	gutter := editorContextBounds(t, m, "sc-diff-gutter")
	for entity, access := range m.fontResources.Query[scene.Sprite2D]() {
		sprite := access.Ref()
		if sprite.Texture == nil {
			continue
		}
		transform, ok := m.fontResources.Get[scene.UIGlobalTransform](entity)
		if !ok {
			continue
		}
		anchor, _ := m.fontResources.Get[scene.Anchor](entity)
		x, y, right, bottom, ok := scene.SpriteBounds2D(sprite, anchor, transform.Matrix, true)
		if !ok || x < gutter.X || right > gutter.X+gutter.Width || y < gutter.Y || bottom > gutter.Y+gutter.Height {
			continue
		}
		if clip, clipped := transform.ClipRect(); clipped && (x >= clip.X+clip.Width || right <= clip.X || y >= clip.Y+clip.Height || bottom <= clip.Y) {
			t.Fatalf("diff arrow is clipped: bounds=(%g,%g,%g,%g) clip=%+v", x, y, right, bottom, clip)
		}
		return
	}
	t.Fatal("no visible diff arrow in gutter")
}
