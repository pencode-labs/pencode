package workbench

import (
	"math"
	"strconv"
	"testing"

	"github.com/diiyw/onix/input"
)

func TestWorkbenchResize(t *testing.T) {
	for _, zoom := range []int{100, 150, 70} {
		t.Run(strconv.Itoa(zoom), func(t *testing.T) {
			app, model, _, state := languageInteractionFixture(t)
			world := model.fontResources
			model.setZoom(world, zoom)
			model.bottom.active = pencodePanelOutput
			markPencodeRootDirtyByModel(world, model)
			languageInteractionStep(t, app)
			languageInteractionStep(t, app)
			scale := float64(zoom) / 100
			drag := func(ref string, dx, dy float64, coalesced bool) {
				t.Helper()
				b := editorContextBounds(t, model, ref)
				x, y := float64(b.X+b.Width/2)*scale, float64(b.Y+b.Height/2)*scale
				state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
				if !coalesced {
					languageInteractionStep(t, app)
				}
				state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x + dx*scale, Y: y + dy*scale})
				if !coalesced {
					languageInteractionStep(t, app)
				}
				state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x + dx*scale, Y: y + dy*scale})
				languageInteractionStep(t, app)
				languageInteractionStep(t, app)
				if model.resize.target != pencodeResizeNone {
					t.Fatal("release left a resize active")
				}
			}
			before := editorContextBounds(t, model, "sidebar")
			editor := editorContextBounds(t, model, "editor-body")
			drag("sidebar-resize", 80, 0, false)
			after := editorContextBounds(t, model, "sidebar")
			nextEditor := editorContextBounds(t, model, "editor-body")
			if math.Abs(float64(after.Width-before.Width)-80) > 1 || math.Abs(float64(editor.Width-nextEditor.Width)-80) > 1 {
				t.Fatalf("sidebar drag did not resize both panes: sidebar %v -> %v, editor %v -> %v", before, after, editor, nextEditor)
			}
			drag("sidebar-resize", -40, 0, true)
			if next := editorContextBounds(t, model, "sidebar"); math.Abs(float64(next.Width-after.Width)+40) > 1 {
				t.Fatalf("coalesced drag did not use the press origin: %v -> %v", after, next)
			}
			drag("sidebar-resize", -1000, 0, false)
			if next := editorContextBounds(t, model, "sidebar"); next.Width < 220 || next.Width > 221 {
				t.Fatalf("sidebar minimum width: %v", next)
			}
			before = editorContextBounds(t, model, "bottom-panel")
			editor = editorContextBounds(t, model, "editor-body")
			handle := editorContextBounds(t, model, "panel-resize")
			if handle.Y < editor.Y+editor.Height-1 || handle.Y+handle.Height > before.Y+1 {
				t.Fatalf("horizontal handle is outside the gap: editor=%v handle=%v panel=%v", editor, handle, before)
			}
			drag("panel-resize", 0, -60, false)
			after = editorContextBounds(t, model, "bottom-panel")
			nextEditor = editorContextBounds(t, model, "editor-body")
			if math.Abs(float64(after.Height-before.Height)-60) > 1 || math.Abs(float64(editor.Height-nextEditor.Height)-60) > 1 {
				t.Fatalf("panel drag did not resize both panes: panel %v -> %v, editor %v -> %v", before, after, editor, nextEditor)
			}
			drag("panel-resize", 0, 30, true)
			if next := editorContextBounds(t, model, "bottom-panel"); math.Abs(float64(next.Height-after.Height)+30) > 1 {
				t.Fatalf("coalesced panel drag: %v -> %v", after, next)
			}
			model.sidebarHidden = true
			model.bottom.active = pencodePanelClosed
			markPencodeRootDirtyByModel(world, model)
			languageInteractionStep(t, app)
			languageInteractionStep(t, app)
			for _, ref := range []string{"sidebar-resize", "panel-resize"} {
				node, _ := world.HTMLNode(model.surface, ref)
				if b, ok := node.Bounds(); ok && b.Width > 0 && b.Height > 0 {
					t.Fatalf("hidden pane retained its handle: %s %v", ref, b)
				}
			}
		})
	}
}

func TestWorkbenchResizeIdleAllocations(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: 280, Y: 200})
	languageInteractionStep(t, app)
	world := model.fontResources
	if n := testing.AllocsPerRun(100, func() { model.updateWorkbenchResize(world, state) }); n != 0 {
		t.Fatalf("idle resize allocated %g times", n)
	}
	state.PressMouse(input.MouseLeft, 280, 200)
	state.EndFrame()
	for _, target := range []pencodeResizeTarget{pencodeResizeSidebar, pencodeResizeBottom} {
		model.resize = pencodeWorkbenchResize{target: target, origin: 280, size: 270}
		model.bottom.active = pencodePanelOutput
		model.updateWorkbenchResize(world, state)
		if model.resize.target != target {
			t.Fatal("stationary drag lost its target")
		}
		if n := testing.AllocsPerRun(100, func() {
			if model.updateWorkbenchResize(world, state) {
				t.Fatal("stationary drag requested another render")
			}
		}); n != 0 {
			t.Fatalf("stationary resize allocated %g times", n)
		}
	}
}
