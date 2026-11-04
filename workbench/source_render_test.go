package workbench

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"testing"

	"github.com/diiyw/onix"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
)

func sourceChangesApp(t testing.TB, count int) (*onix.App, *workspaceModel) {
	t.Helper()
	app, session := newPencodeTestApp(t)
	m := session.active.model
	m.app.GetViewport = func() (int, int) { return 1440, 900 }
	app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	for _, camera := range app.World.Query[scene.Camera]().All {
		camera.Mut().UpdateViewport(1440, 900)
	}
	s := m.sourceState()
	s.cancelRefresh()
	s.Snapshot = sourceChangesFixture(count)
	s.Snapshot.Repository = sourceRepository{Root: m.projectPath, Backend: m.backend}
	s.RepositoryError, s.Notice = "", ""
	s.RefreshStarted, s.RefreshPending, s.RepositoriesLoaded = true, false, true
	s.FollowPath = m.activePath
	m.bottom.active = pencodePanelSourceControl
	m.sourceChanged()
	for range 3 {
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
	}
	return app, m
}

func TestSourceControlChangesVirtualWindow(t *testing.T) {
	app, m := sourceChangesApp(t, 4000)
	w, s := m.fontResources, m.sourceState()
	state, _ := app.World.GetResource[*input.State]()
	list, _ := w.HTMLNode(m.surface, "sc-changes-list")
	area, ok := list.ScrollContainer()
	if !ok {
		t.Fatal("missing changes viewport")
	}
	for _, zoom := range []int{100, 150, 80} {
		m.setZoom(w, zoom)
		for range 3 {
			languageInteractionStep(t, app)
		}
		for _, target := range []struct{ group, row int }{{1, 0}, {1, 2000}, {2, 0}, {2, 4199}, {1, 1000}} {
			top := 32 + target.row*27
			if target.group == 2 {
				top += 32 + 4200*27
			}
			w.Mut[scene.UIScroll](area).Position[1] = -float32(top)
			languageInteractionStep(t, app)
			scroll := w.Ref[scene.UIScroll](area)
			if scroll.ContentSize.Y() != 2*(32+4200*27) {
				t.Fatalf("virtual list changed scroll extent: %+v", scroll)
			}
			mounted := 0
			for _, id := range s.panelView.clicks {
				if strings.HasPrefix(id, "sc-open:") || strings.HasPrefix(id, "sc-folder:") {
					mounted++
				}
			}
			limit := int(scroll.ViewportSize.Y())/27 + 3*sourceChangeOverscan + 4
			if mounted == 0 || mounted > limit || len(s.Handlers) > 3*limit+30 {
				t.Fatalf("unbounded rows or retired handlers: rows=%d handlers=%d limit=%d", mounted, len(s.Handlers), limit)
			}
			row := s.changeTrees[target.group].rows[target.row]
			id := "sc-open:" + sourceChangeGroups[target.group].Kind + ":" + url.PathEscape(row.Path)
			if row.File == nil {
				id = "sc-folder:" + sourceChangeGroups[target.group].Kind + ":" + row.Path
			}
			bounds := editorContextBounds(t, m, id)
			viewport := editorContextBounds(t, m, "sc-changes-list")
			y := bounds.Y + scroll.Position.Y()
			if bounds.Height != 27 || y < viewport.Y-.1 || y+bounds.Height > viewport.Y+viewport.Height+.1 {
				t.Fatalf("row geometry changed at zoom %d: row=%+v viewport=%+v scroll=%g", zoom, bounds, viewport, scroll.Position.Y())
			}
			if n := testing.AllocsPerRun(100, func() { m.syncSourceChangesScroll(w); s.changeRanges() }); n != 0 {
				t.Fatalf("stable scroll window allocated %g times", n)
			}
		}
	}
	// Scroll a folder into view and click its actual scrolled hit target.
	row := s.changeTrees[1].rows[1000]
	if row.File == nil {
		t.Fatal("fixture requires a file row")
	}
	// Use the folder instead so no synthetic file is read from the backend.
	folder := path.Dir(row.Path)
	id := "sc-folder:staged:" + folder
	w.Mut[scene.UIScroll](area).Position[1] += 15 * 27
	languageInteractionStep(t, app)
	bounds := editorContextBounds(t, m, id)
	scale := float64(m.settings.Zoom) / 100
	x, y := float64(bounds.X+30)*scale, float64(bounds.Y+w.Ref[scene.UIScroll](area).Position.Y()+13)*scale
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
	if !s.Collapsed["staged:"+folder] || len(s.changeTrees[1].rows) != 4180 {
		t.Fatalf("scrolled folder click did not fold its descendants: folded=%v rows=%d bounds=%+v scroll=%+v click=%g,%g", s.Collapsed, len(s.changeTrees[1].rows), bounds, w.Ref[scene.UIScroll](area), x, y)
	}
}

func TestSourceControlFilePaintAfterScrolling(t *testing.T) {
	r := sourceTestRepository(t)
	for i := range 100 {
		sourceTestWrite(t, r, fmt.Sprintf("scene/file%03d.go", i), "package demo\n")
	}
	const name = "ui/html/component.go"
	sourceTestWrite(t, r, name, "package demo\n")
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", name)
	app, m, _, state := languageInteractionFixture(t)
	editorContextClick(t, app, m, state, "status-source-control")
	sourceWait(t, app, m)
	list, _ := m.fontResources.HTMLNode(m.surface, "sc-changes-list")
	area, ok := list.ScrollContainer()
	if !ok {
		t.Fatal("missing changes scroll container")
	}
	viewport := editorContextBounds(t, m, "sc-changes-list")
	id := "sc-open:changes:" + url.PathEscape(name)
	check := func() {
		t.Helper()
		bounds := editorContextBounds(t, m, id)
		scroll := app.World.GetRef[scene.UIScroll](area)
		y := bounds.Y + scroll.Position.Y()
		if y < viewport.Y || y+bounds.Height > viewport.Y+viewport.Height {
			t.Fatalf("test file is outside the scrollport: y=%g viewport=%+v", y, viewport)
		}
		found := false
		for entity, access := range app.World.Query2[scene.Text2D, htmlui.ControlText]().All {
			control := app.World.GetRef[htmlui.Control](access.B.Ref().Control)
			if control == nil || control.ID != id || access.A.Ref().Text().String() != path.Base(name) {
				continue
			}
			found = true
			global := app.World.GetRef[scene.UIGlobalTransform](entity)
			clip, clipped := global.ClipRect()
			if global.Matrix[13] < y || global.Matrix[13] >= y+bounds.Height || !clipped || clip.Width <= 0 || clip.Height <= 0 || clip.Y+clip.Height <= y || clip.Y >= y+bounds.Height {
				t.Fatalf("visible filename has stale geometry: row y=%g text y=%g clip=%+v", y, global.Matrix[13], clip)
			}
		}
		if !found {

			t.Fatalf("visible filename has no text visual: bounds=%+v scroll=%+v ranges=%+v diagnostics=%+v", bounds, scroll, m.sourceState().panelKey.Changes, htmlui.Diagnostics(app.World, m.surface))
		}
	}
	for range 2 {
		// Cross the paint overscan boundary in both directions, then keep the
		// target visible while another local update re-emits its clipping box.
		app.World.GetMut[scene.UIScroll](area).Position[1] = 0
		languageInteractionStep(t, app)
		scroll := app.World.GetMut[scene.UIScroll](area)
		scroll.Position[1] = -max(0, scroll.ContentSize.Y()-scroll.ViewportSize.Y())
		languageInteractionStep(t, app)
		check()
		m.sourceAction("sc-folder:changes:ui")
		languageInteractionStep(t, app)
		m.sourceAction("sc-folder:changes:ui")
		languageInteractionStep(t, app)
		scroll = app.World.GetMut[scene.UIScroll](area)
		scroll.Position[1] = -max(0, scroll.ContentSize.Y()-scroll.ViewportSize.Y())
		languageInteractionStep(t, app)
		check()
	}
}
