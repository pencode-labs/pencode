package workbench

import (
	"fmt"
	"math"
	"testing"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
)

func TestPencodeTabOverflowScroll(t *testing.T) {
	for _, zoom := range []int{100, 150} {
		t.Run(fmt.Sprint(zoom), func(t *testing.T) {
			app, session := newPencodeTestApp(t)
			model := session.active.model
			model.app.GetViewport = func() (int, int) { return 1440, 900 }
			app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
			for _, camera := range app.World.Query[scene.Camera]().All {
				camera.Mut().UpdateViewport(1440, 900)
			}
			world := model.fontResources
			project := model.activeProjectState()
			for i := range 20 {
				name := fmt.Sprintf("document-%02d.txt", i)
				project.Files[name] = &openFile{Path: name, Name: name}
				project.Tabs = append(project.Tabs, name)
			}
			model.settings.Zoom = zoom
			model.selectProjectTab(project, project.Tabs[len(project.Tabs)-1])
			markPencodeRootDirtyByModel(world, model)
			languageInteractionStep(t, app)
			scroll := world.Mut[scene.UIScroll](model.tabScroll.area)
			if scroll == nil || !scroll.Horizontal || scroll.ScrollbarSize != 0 {
				t.Fatalf("tabs must scroll horizontally without a scrollbar: %+v", scroll)
			}
			if scroll.ContentSize.X() <= scroll.ViewportSize.X() || scroll.Position.X() >= 0 {
				t.Fatalf("opening the last tab did not push older tabs left: %+v", scroll)
			}
			assertVisible := func() {
				t.Helper()
				viewport := editorContextBounds(t, model, "tabs")
				tab := editorContextBounds(t, model, "active-tab")
				scroll := world.Ref[scene.UIScroll](model.tabScroll.area)
				left := tab.X + scroll.Position.X() - viewport.X
				if left < -.01 || left+tab.Width > viewport.Width+.01 {
					t.Fatalf("active tab is clipped: left=%v width=%v viewport=%v", left, tab.Width, viewport.Width)
				}
			}
			assertVisible()
			state, _ := app.World.GetResource[*input.State]()
			viewport := editorContextBounds(t, model, "tabs")
			scale := float64(zoom) / 100
			state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(viewport.X+viewport.Width/2) * scale, Y: float64(viewport.Y+viewport.Height/2) * scale})
			wheel := func(x, y float64) {
				t.Helper()
				state.Enqueue(input.Event{Kind: input.EventMouseWheel, WheelX: x, WheelY: y})
				languageInteractionStep(t, app)
			}
			before := scroll.Position.X()
			wheel(0, 1)
			if got := world.Ref[scene.UIScroll](model.tabScroll.area).Position.X(); math.Abs(float64(got-before-48)) > .01 {
				t.Fatalf("vertical wheel position=%v, want %v", got, before+48)
			}
			languageInteractionStep(t, app)
			if got := world.Ref[scene.UIScroll](model.tabScroll.area).Position.X(); got != before+48 {
				t.Fatal("idle layout reset manual scrolling")
			}
			wheel(0, 10000)
			if got := world.Ref[scene.UIScroll](model.tabScroll.area).Position.X(); got != 0 {
				t.Fatalf("left boundary=%v, want 0", got)
			}
			wheel(-1, 0)
			if got := world.Ref[scene.UIScroll](model.tabScroll.area).Position.X(); got != -48 {
				t.Fatalf("horizontal wheel position=%v, want -48", got)
			}
			wheel(0, -10000)
			scroll = world.Mut[scene.UIScroll](model.tabScroll.area)
			if got, want := scroll.Position.X(), scroll.ViewportSize.X()-scroll.ContentSize.X(); math.Abs(float64(got-want)) > .01 {
				t.Fatalf("right boundary=%v, want %v", got, want)
			}
			model.selectProjectTab(project, project.Tabs[0])
			markPencodeRootDirtyByModel(world, model)
			languageInteractionStep(t, app)
			assertVisible()
			local, _ := world.Resource[*input.State]()
			local.ScrollMouse(0, -1)
			if allocations := testing.AllocsPerRun(50, func() {
				world.Mut[scene.UIScroll](model.tabScroll.area).Position[0] = -48
				model.syncTabScroll(world)
				model.updateTabWheel(world, local)
			}); allocations != 0 {
				t.Fatalf("tab scrolling allocated %g times", allocations)
			}
			before = world.Ref[scene.UIScroll](model.tabScroll.area).Position.X()
			state.Enqueue(input.Event{Kind: input.EventMouseMove, X: 5, Y: 5})
			wheel(0, -1)
			if got := world.Ref[scene.UIScroll](model.tabScroll.area).Position.X(); got != before {
				t.Fatal("wheel outside the tab strip moved tabs")
			}
			for _, name := range project.Tabs[1:] {
				delete(project.Files, name)
			}
			project.Tabs = project.Tabs[:1]
			markPencodeRootDirtyByModel(world, model)
			languageInteractionStep(t, app)
			scroll = world.Mut[scene.UIScroll](model.tabScroll.area)
			if scroll == nil || scroll.Horizontal || scroll.Position.X() != 0 {
				t.Fatalf("closing overflow tabs did not reset the scroll range: %+v", scroll)
			}
		})
	}
}
