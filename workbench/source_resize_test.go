package workbench

import (
	"math"
	"strconv"
	"testing"

	"github.com/diiyw/onix/input"
)

func TestSourceChangesResize(t *testing.T) {
	for _, zoom := range []int{100, 150, 70} {
		t.Run(strconv.Itoa(zoom), func(t *testing.T) {
			app, m := sourceChangesApp(t, 1)
			world := m.fontResources
			state, _ := app.World.GetResource[*input.State]()
			m.setZoom(world, zoom)
			for range 2 {
				languageInteractionStep(t, app)
			}
			scale := float64(zoom) / 100
			drag := func(dx float64, coalesced bool) {
				t.Helper()
				b := editorContextBounds(t, m, "sc-changes-resize")
				x, y := float64(b.X+b.Width/2)*scale, float64(b.Y+b.Height/2)*scale
				state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
				if !coalesced {
					languageInteractionStep(t, app)
					if m.resize.target != pencodeResizeSourceChanges {
						t.Fatal("separator did not start resizing")
					}
				}
				state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x + dx*scale, Y: y})
				if !coalesced {
					languageInteractionStep(t, app)
				}
				state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x + dx*scale, Y: y})
				for range 2 {
					languageInteractionStep(t, app)
				}
				if m.resize.target != pencodeResizeNone {
					t.Fatal("release left a resize active")
				}
			}
			before := editorContextBounds(t, m, "sc-changes")
			body := editorContextBounds(t, m, "sc-body")
			history := editorContextBounds(t, m, "sc-history")
			handle := editorContextBounds(t, m, "sc-changes-resize")
			if math.Abs(float64(before.Width-min(max(body.Width*.4, 190), 480))) > 1 {
				t.Fatalf("unexpected default changes width: changes=%+v body=%+v", before, body)
			}
			if handle.Width != 6 || handle.X < before.X+before.Width-1 || handle.X+handle.Width > history.X+1 || handle.Height != body.Height {
				t.Fatalf("separator is outside the boundary: changes=%+v handle=%+v history=%+v", before, handle, history)
			}
			drag(40, false)
			after := editorContextBounds(t, m, "sc-changes")
			nextHistory := editorContextBounds(t, m, "sc-history")
			if math.Abs(float64(after.Width-before.Width)-40) > 1 || math.Abs(float64(history.Width-nextHistory.Width)-40) > 1 {
				t.Fatalf("drag did not resize both panes: changes %v -> %v, history %v -> %v", before, after, history, nextHistory)
			}
			drag(-20, true)
			if next := editorContextBounds(t, m, "sc-changes"); math.Abs(float64(next.Width-after.Width)+20) > 1 {
				t.Fatalf("coalesced drag lost the press origin: %v -> %v", after, next)
			}
			drag(10000, false)
			after = editorContextBounds(t, m, "sc-changes")
			nextHistory = editorContextBounds(t, m, "sc-history")
			if math.Abs(float64(after.Width-(body.Width-206))) > 1 || nextHistory.Width < 200 {
				t.Fatalf("drag did not preserve graph minimum width: changes=%+v history=%+v body=%+v", after, nextHistory, body)
			}
			if zoom != 150 {
				m.setZoom(world, 150)
				for range 2 {
					languageInteractionStep(t, app)
				}
				narrowBody := editorContextBounds(t, m, "sc-body")
				narrowChanges := editorContextBounds(t, m, "sc-changes")
				if narrowChanges.Width > narrowBody.Width-206+1 || editorContextBounds(t, m, "sc-history").Width < 200 {
					t.Fatalf("chosen width squeezed the graph after zooming: changes=%+v body=%+v", narrowChanges, narrowBody)
				}
				m.setZoom(world, zoom)
				for range 2 {
					languageInteractionStep(t, app)
				}
			}
			drag(-10000, false)
			if next := editorContextBounds(t, m, "sc-changes"); next.Width != 190 {
				t.Fatalf("changes minimum width: %+v", next)
			}
			// The cached panel and a rebuilt panel must retain the chosen width.
			message, _ := world.HTMLNode(m.surface, "sc-message")
			for _, panel := range []pencodePanel{pencodePanelOutput, pencodePanelSourceControl} {
				m.bottom.active = panel
				m.sourceState().CommitMessage = "resize retained"
				m.sourceChanged()
				for range 2 {
					languageInteractionStep(t, app)
				}
			}
			if next := editorContextBounds(t, m, "sc-changes"); next.Width != 190 {
				t.Fatalf("reopening reset the chosen width: %+v", next)
			}
			if current, _ := world.HTMLNode(m.surface, "sc-message"); current != message {
				t.Fatal("resizing replaced the commit input")
			}
			state.PressMouse(input.MouseLeft, 0, 0)
			state.EndFrame()
			m.resize = pencodeWorkbenchResize{target: pencodeResizeSourceChanges, size: 190}
			if n := testing.AllocsPerRun(100, func() {
				if m.updateWorkbenchResize(world, state) {
					t.Fatal("stationary drag requested another render")
				}
			}); n != 0 {
				t.Fatalf("stationary source resize allocated %g times", n)
			}
			m.sourceState().Notice = "Modal open"
			if !m.updateWorkbenchResize(world, state) || m.resize.target != pencodeResizeNone {
				t.Fatal("source modal did not cancel resizing")
			}
		})
	}
}
