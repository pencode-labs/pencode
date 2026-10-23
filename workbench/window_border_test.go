package workbench

import (
	"testing"

	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
)

func TestPencodeWindowBorderIgnoresZoomAndTracksResize(t *testing.T) {
	_, session := newPencodeTestApp(t)
	model := session.active.model
	content := model.fontResources
	model.app = nil
	border := model.windowBorder
	for _, viewport := range [][2]float32{{1440, 900}, {900, 600}} {
		model.viewportWidth, model.viewportHeight = viewport[0], viewport[1]
		for _, zoom := range []int{70, 100, 150} {
			model.settings.Zoom = zoom
			model.syncZoom(content)
			size, _ := module.ContentGet[scene.Size2D](content, border)
			transform, _ := module.ContentGet[scene.UITransform](content, border)
			outline, _ := module.ContentGet[scene.UIBorder2D](content, border)
			if size.Width != viewport[0] || size.Height != viewport[1] || transform.Scale.X() != 1 || transform.Scale.Y() != 1 {
				t.Fatalf("zoom %d changed window border bounds: size=%+v transform=%+v", zoom, size, transform)
			}
			if outline.Shape.BorderWidth != 1 || outline.Shape.Radius != 12 || outline.Shape.Fill != (graphics.ColorRGBA{}) {
				t.Fatalf("zoom %d changed window border geometry: %+v", zoom, outline.Shape)
			}
			if model.windowBorder != border {
				t.Fatal("window border was recreated")
			}
		}
	}
	if allocations := testing.AllocsPerRun(100, func() { model.syncZoom(content) }); allocations != 0 {
		t.Fatalf("window geometry synchronization allocated %g times", allocations)
	}
}
