package workbench

import (
	"fmt"
	"net/url"
	"path"
	"testing"

	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
)

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
			if control == nil || control.ID != id || access.A.Ref().Value != path.Base(name) {
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
			t.Fatal("visible filename has no text visual")
		}
	}
	for range 2 {
		// Cross the paint overscan boundary in both directions, then keep the
		// target visible while another local update re-emits its clipping box.
		app.World.GetMut[scene.UIScroll](area).Position[1] = 0
		languageInteractionStep(t, app)
		bounds := editorContextBounds(t, m, id)
		app.World.GetMut[scene.UIScroll](area).Position[1] = viewport.Y + 24 - bounds.Y
		languageInteractionStep(t, app)
		check()
		m.sourceAction("sc-folder:changes:ui")
		languageInteractionStep(t, app)
		m.sourceAction("sc-folder:changes:ui")
		languageInteractionStep(t, app)
		bounds = editorContextBounds(t, m, id)
		app.World.GetMut[scene.UIScroll](area).Position[1] = viewport.Y + 24 - bounds.Y
		languageInteractionStep(t, app)
		check()
	}
}
