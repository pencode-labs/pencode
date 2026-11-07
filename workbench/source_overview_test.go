package workbench

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
)

func TestSourceOverviewGeometry(t *testing.T) {
	for _, test := range []struct{ height, viewport, content float32 }{
		{600, 600, 20000}, {600, 600, 10000000}, {600, 600, 200}, {10, 10, 20000}, {0, 0, 0},
	} {
		g := sourceOverviewLayout(test.height, test.viewport, test.content, -1e9)
		if g.top < 0 || g.thumb < 0 || g.top+g.thumb > g.height || math.IsNaN(float64(g.top)) {
			t.Fatalf("invalid overview geometry: %+v", g)
		}
		if got := g.scrollY(g.height); got != -g.limit {
			t.Fatalf("bottom offset = %g, want %g", got, -g.limit)
		}
		if got := g.scrollY(-100); got != 0 {
			t.Fatalf("top offset = %g", got)
		}
	}
	for _, rows := range []int{1, 1000, 1000000} {
		top, height := sourceOverviewMarkerBounds(sourceDiffBlock{FirstRow: rows - 1, EndRow: rows}, rows, 600)
		if height < 3 || top+height > 600 {
			t.Fatalf("EOF marker is clipped: top=%g height=%g", top, height)
		}
	}
}

func TestSourceOverviewNavigation(t *testing.T) {
	for _, zoom := range []int{100, 150, 70} {
		t.Run(strconv.Itoa(zoom), func(t *testing.T) {
			app, m, _, state := languageInteractionFixture(t)
			m.setZoom(m.fontResources, zoom)
			var left, right strings.Builder
			for i := range 1000 {
				fmt.Fprintf(&left, "line %d\n", i)
				if i == 900 {
					right.WriteString("inserted\n")
				}
				switch i {
				case 100:
				case 500:
					right.WriteString("replacement\n")
				default:
					fmt.Fprintf(&right, "line %d\n", i)
				}
			}
			tab := &sourceTab{Key: "changes::overview.txt", Path: "overview.txt", Kind: "changes", Local: left.String(), Result: right.String(), Saved: right.String()}
			tab.initializeDiffView(alignSourceDiff(tab.Local, tab.Result, "@@ -101 +100,0 @@\n@@ -501 +500 @@\n@@ -900,0 +900 @@\n"))
			if len(tab.Blocks) != 3 {
				t.Fatalf("expected three changes, got %+v", tab.Blocks)
			}
			s := m.sourceState()
			s.Tabs, s.Active, s.RestoreScroll, s.RefreshStarted = []*sourceTab{tab}, tab.Key, true, true
			m.sourceChanged()
			for range 3 {
				languageInteractionStep(t, app)
			}
			bounds := editorContextBounds(t, m, "sc-diff-overview")
			code := editorContextBounds(t, m, "sc-code-right")
			panel := editorContextBounds(t, m, "sc-code-panel")
			if bounds.X < code.X+code.Width-1 || math.Abs(float64(bounds.X+bounds.Width-panel.X-panel.Width)) > 1 {
				t.Fatalf("overview is not at the right edge: overview=%+v code=%+v panel=%+v", bounds, code, panel)
			}
			var scrolls [2]*scene.UIScroll
			for i, id := range [...]string{"sc-code-left", "sc-code-right"} {
				node, _ := m.fontResources.HTMLNode(m.surface, id)
				area, _ := node.ScrollContainer()
				scrolls[i] = m.fontResources.Mut[scene.UIScroll](area)
			}
			scale := float64(zoom) / 100
			point := func(kind input.EventKind, x, y float32) {
				state.Enqueue(input.Event{Kind: kind, Button: input.MouseLeft, X: float64(x) * scale, Y: float64(y) * scale})
			}
			assertScroll := func(want float32) {
				t.Helper()
				if math.Abs(float64(tab.ScrollY-want)) > .02 || scrolls[0].Position[1] != scrolls[1].Position[1] || scrolls[0].Position[1] != tab.ScrollY {
					t.Fatalf("overview scroll is not synchronized: want=%g tab=%g left=%v right=%v", want, tab.ScrollY, scrolls[0].Position, scrolls[1].Position)
				}
				if tab.FirstRow != sourceFirstVisibleRow(tab.ScrollY) {
					t.Fatal("overview navigation left the virtual row window behind")
				}
			}
			// Each colored marker reveals its block, including outside the current window.
			for _, block := range tab.Blocks {
				top, height := sourceOverviewMarkerBounds(block, len(tab.Rows), bounds.Height)
				x, y := bounds.X+11, bounds.Y+top+height/2
				point(input.EventMouseDown, x, y)
				point(input.EventMouseUp, x, y)
				languageInteractionStep(t, app)
				assertScroll(-float32(max(0, block.FirstRow-3) * 20))
				if zoom == 100 {
					assertSourceDiffScrollGeometry(t, m)
				}
			}
			// Drag outside the rail and release; repeat with events coalesced into one frame.
			for _, coalesced := range []bool{false, true} {
				_, g, _ := m.sourceOverviewGeometry(m.fontResources, tab)
				x, y := bounds.X+2, bounds.Y+g.top+g.thumb/2
				point(input.EventMouseDown, x, y)
				if !coalesced {
					languageInteractionStep(t, app)
				}
				point(input.EventMouseMove, x-40, bounds.Y+bounds.Height+200)
				point(input.EventMouseUp, x-40, bounds.Y+bounds.Height+200)
				languageInteractionStep(t, app)
				assertScroll(-g.limit)
				if s.overview.dragTab != nil {
					t.Fatal("release left the overview drag active")
				}
				x, y = bounds.X+2, bounds.Y+bounds.Height-g.thumb/2
				point(input.EventMouseDown, x, y)
				point(input.EventMouseMove, x, bounds.Y-200)
				point(input.EventMouseUp, x, bounds.Y-200)
				languageInteractionStep(t, app)
				assertScroll(0)
			}
			// Wheel input continues to move the same thumb without rebuilding styles.
			point(input.EventMouseMove, code.X+100, code.Y+100)
			state.Enqueue(input.Event{Kind: input.EventMouseWheel, WheelY: -5})
			languageInteractionStep(t, app)
			_, g, _ := m.sourceOverviewGeometry(m.fontResources, tab)
			if tab.ScrollY >= 0 || g.top <= 0 {
				t.Fatal("wheel input did not move the overview viewport")
			}
			paintedThumb := false
			for entity, access := range m.fontResources.Query[scene.Sprite2D]() {
				sprite := access.Ref()
				if sprite.Width != 5 || sprite.Height != g.thumb {
					continue
				}
				global, _ := m.fontResources.Get[scene.UIGlobalTransform](entity)
				anchor, _ := m.fontResources.Get[scene.Anchor](entity)
				x, y, _, _, ok := scene.SpriteBounds2D(sprite, anchor, global.Matrix, true)
				if ok && x >= bounds.X*float32(scale) && x < (bounds.X+bounds.Width)*float32(scale) {
					paintedThumb = math.Abs(float64(y)-(float64(bounds.Y)+float64(g.top))*scale) < 1
				}
			}
			if !paintedThumb {
				t.Fatal("painted overview thumb does not follow the scroll position")
			}
			if n := testing.AllocsPerRun(100, func() { m.syncSourceOverview(m.fontResources); m.updateSourceOverview(m.fontResources, state) }); n != 0 {
				t.Fatalf("warm overview synchronization allocated %g times", n)
			}
			var b sourceVNodeView
			render := func() { b.reset(); m.renderSourceOverview(&b, tab); b.nodes() }
			render()
			if n := testing.AllocsPerRun(100, render); n != 0 {
				t.Fatalf("warm overview render allocated %g times", n)
			}
		})
	}
}
