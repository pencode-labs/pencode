package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/diiyw/onix"
	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/input"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

func TestPencodeExtensionUIProcess(t *testing.T) {
	if !slices.Contains(os.Args, "pencode-ui-fixture") {
		return
	}
	var publisher *peb.ActionPublisher
	err := peb.ServeExtension(os.Stdin, os.Stdout, peb.Contributions{Actions: []peb.Action{{ID: "tools", Text: "Tools", Location: peb.TitleBarRight}}}, peb.Handlers{
		ActivateActions: func(_ context.Context, p *peb.ActionPublisher) { publisher = p },
		Action: func(c peb.ActionContext) error {
			return publisher.Show(peb.UI{ID: "tools", Title: "Tools", Kind: peb.ContextMenu, Anchor: c.ID, View: &peb.EditorView{HTML: `<button data-ref="open" role="menuitem" type="button">Open dialog</button><button data-ref="popup" role="menuitem" type="button">Open popup</button>`, Events: []peb.EditorBinding{{Ref: "open", Kind: "click"}, {Ref: "popup", Kind: "click"}}}})
		},
		UIEvent: func(event peb.UIEvent) error {
			fmt.Fprintf(os.Stderr, "UI %s %s %s %s %s\n", event.ID, event.Event.Kind, event.Reason, event.Context.ID, event.Event.Value)
			switch {
			case event.ID == "tools" && event.Event.Kind == "click" && event.Event.Ref == "popup":
				return publisher.Show(peb.UI{ID: "market", Title: "Extension market", Kind: peb.Popup, Placement: peb.UIPlacement{Mode: peb.PositionAbsolute, X: 96, Y: 120, Width: 420, Height: 280}, View: &peb.EditorView{HTML: `<label for="pencode-extension-query">Search</label><input id="pencode-extension-query" data-ref="query">`, Events: []peb.EditorBinding{{Ref: "query", Kind: "input"}, {Ref: "query", Kind: "focus"}, {Ref: "query", Kind: "blur"}}}})
			case event.ID == "market" && event.Event.Kind == "input":
				return publisher.Show(peb.UI{ID: "market", Title: "Search: " + event.Event.Value, Kind: peb.Popup, CloseOnOutsideClick: event.Event.Value == "dismiss", Placement: peb.UIPlacement{Mode: peb.PositionAbsolute, X: 260, Y: 180, Width: 420, Height: 280}})
			case event.ID == "tools" && event.Event.Kind == "click":
				return publisher.Show(peb.UI{ID: "form", Title: "Extension form", Kind: peb.Dialog, View: &peb.EditorView{HTML: `<form data-ref="form"><label for="pencode-extension-name">Name</label><input id="pencode-extension-name" data-ref="name"><button type="submit">Open panel</button></form>`, CSS: `input { width: 180px; }`, Events: []peb.EditorBinding{{Ref: "name", Kind: "input"}, {Ref: "form", Kind: "submit"}}}})
			case event.ID == "form" && event.Event.Kind == "input":
				// Retain the DOM while updating the host-owned title.
				return publisher.Show(peb.UI{ID: "form", Title: event.Event.Value, Kind: peb.Dialog})
			case event.ID == "form" && event.Event.Kind == "submit":
				if err := publisher.CloseUI("form"); err != nil {
					return err
				}
				return publisher.Show(peb.UI{ID: "result", Title: "Result", Kind: peb.Panel, View: &peb.EditorView{HTML: `<img src="resources/preview.svg" alt="Preview"><button data-ref="done" type="button">Done</button>`, Events: []peb.EditorBinding{{Ref: "done", Kind: "click"}}}})
			case event.ID == "result" && event.Event.Kind == "click":
				return publisher.CloseUI("result")
			}
			return nil
		},
	})
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestPencodeExtensionUISurfacesEventsAndCleanup(t *testing.T) {
	// Headless input/layout only; no editor window or screenshots.
	app, model, _, state := languageInteractionFixture(t)
	popupFrames := 0
	if err := app.AddSystem(onix.StageRender, ecs.SystemConfig{
		Name: "check_extension_popup_before_paint", After: []string{"sync_render_ui_transform"},
		System: func(*ecs.SystemContext) error {
			s := model.actions.ui.overlay
			if s == nil || s.ui.Kind != peb.Popup {
				return nil
			}
			popupFrames++
			bounds := editorContextBounds(t, model, s.rootID)
			viewport := editorContextBounds(t, model, "pencode-extension-overlays")
			placement := s.ui.Placement
			x, y := placement.X, placement.Y
			switch placement.Mode {
			case peb.PositionDefault:
				x += (viewport.Width - bounds.Width) / 2
				y += (viewport.Height - bounds.Height) / 2
			case peb.PositionAnchor:
				anchor := editorContextBounds(t, model, s.invocation.anchor)
				x += anchor.X - viewport.X
				y += anchor.Y + anchor.Height - viewport.Y
			}
			x = viewport.X + max(4, min(x, viewport.Width-bounds.Width-4))
			y = viewport.Y + max(4, min(y, viewport.Height-bounds.Height-4))
			if math.Abs(float64(bounds.X-x)) > .2 || math.Abs(float64(bounds.Y-y)) > .2 {
				t.Fatalf("paint saw popup at stale position: %+v, want (%g,%g)", bounds, x, y)
			}
			control, ok := model.fontResources.HTMLControl(model.surface, "pencode-extension-query")
			transform, positioned := ecs.Get[scene.UIGlobalTransform](app.World, control)
			if !ok || !positioned || transform.Matrix[12] < bounds.X || transform.Matrix[12] >= bounds.X+bounds.Width || transform.Matrix[13] < bounds.Y || transform.Matrix[13] >= bounds.Y+bounds.Height {
				t.Fatalf("paint saw input outside popup: transform=%+v popup=%+v", transform, bounds)
			}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "resources"), 0755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(executable, filepath.Join(directory, "fixture")); err != nil {
		source, err := os.Open(executable)
		if err != nil {
			t.Fatal(err)
		}
		defer source.Close()
		target, err := os.OpenFile(filepath.Join(directory, "fixture"), os.O_CREATE|os.O_WRONLY, 0755)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(target, source)
		closeErr := target.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	manifest, err := json.Marshal(extensions.Manifest{ID: "ui-fixture", Entry: "fixture", Args: []string{"-test.run=^TestPencodeExtensionUIProcess$", "--", "pencode-ui-fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "resources/extension.json"), manifest, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "resources/preview.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16"><rect width="16" height="16" fill="red"/></svg>`), 0644); err != nil {
		t.Fatal(err)
	}
	_, bundle, err := extensions.Install(context.Background(), model.session.backend, directory)
	if err != nil {
		t.Fatal(err)
	}
	model.importActions(bundle.Actions)
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			languageInteractionStep(t, app)
			if ready() {
				return
			}
			time.Sleep(time.Millisecond)
		}
		logs, _ := bundle.Actions.Output.Snapshot()
		t.Fatalf("UI did not settle: %s; logs=%s", model.extensionFeedback, logs)
	}
	wait(func() bool {
		_, ok := model.fontResources.HTMLControl(model.surface, "action-extensions/ui-fixture/tools")
		return ok
	})
	editorContextClick(t, app, model, state, "action-extensions/ui-fixture/tools")
	wait(func() bool { return model.actions.ui.overlay != nil })
	menu := model.actions.ui.overlay
	languageInteractionStep(t, app)
	if menu.ui.Kind != peb.ContextMenu || menu.invocation.context.ID != "tools" || menu.invocation.context.Path == "" {
		t.Fatalf("missing invocation: %+v", menu.invocation)
	}
	bounds := editorContextBounds(t, model, menu.rootID)
	root := editorContextBounds(t, model, "pencode-root")
	if bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.Width > root.Width || bounds.Y+bounds.Height > root.Height {
		t.Fatalf("menu outside viewport: %+v in %+v", bounds, root)
	}
	languageInteractionStep(t, app)
	if allocs := testing.AllocsPerRun(100, func() {
		model.syncExtensionUI(model.fontResources)
		model.updateExtensionUI(model.fontResources, state)
	}); allocs != 0 {
		t.Fatalf("idle menu allocated %g", allocs)
	}
	editorContextClick(t, app, model, state, menu.focus[0].id)
	wait(func() bool { return model.actions.ui.overlay != nil && model.actions.ui.overlay.ui.Kind == peb.Dialog })
	dialog := model.actions.ui.overlay
	languageInteractionStep(t, app)
	control, ok := model.fontResources.HTMLControl(model.surface, "pencode-extension-name")
	if !ok {
		t.Fatal("missing input")
	}
	// Keyboard traversal must stay in the dialog and visit controls in order.
	languageInteractionStep(t, app)
	for _, expected := range []int{1, 2, 0} {
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyTab})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyTab})
		languageInteractionStep(t, app)
		languageInteractionStep(t, app)
		if dialog.focused != expected {
			t.Fatalf("Tab should focus %d, got %d", expected, dialog.focused)
		}
	}
	definition := dialog.definition
	model.fontResources.SetControlValue(control, "Updated title")
	ecs.WriteMessage(app.World, htmlui.Event{Entity: model.surface, Target: control, Kind: htmlui.EventInput, Value: "Updated title", Bubbles: true})
	wait(func() bool { return dialog.ui.Title == "Updated title" })
	if dialog.definition != definition {
		t.Fatal("nil View replaced the retained DOM")
	}
	if same, ok := model.fontResources.HTMLControl(model.surface, "pencode-extension-name"); !ok || same != control {
		t.Fatal("input identity changed")
	}
	editorContextClick(t, app, model, state, dialog.focus[1].id)
	wait(func() bool { return model.actions.ui.panel != nil && model.actions.ui.overlay == nil })
	panel := model.actions.ui.panel
	languageInteractionStep(t, app)
	if model.bottom.active != pencodePanelExtension {
		t.Fatal("extension panel not selected")
	}
	if len(model.extensionImages["peb://ui-fixture/resources/preview.svg"]) == 0 {
		t.Fatal("missing panel image")
	}
	panelDefinition := panel.definition
	model.handleBottomPanelEvent(model.fontResources, "status-output", "")
	markPencodeRootDirtyByModel(model.fontResources, model)
	wait(func() bool {
		logs, _ := bundle.Actions.Output.Snapshot()
		return strings.Contains(logs, "UI result visibilitychange  tools hidden")
	})
	model.bottom.active = pencodePanelExtension
	markPencodeRootDirtyByModel(model.fontResources, model)
	wait(func() bool {
		logs, _ := bundle.Actions.Output.Snapshot()
		return strings.Contains(logs, "UI result visibilitychange  tools visible")
	})
	if panel.definition != panelDefinition {
		t.Fatal("panel switch replaced the view")
	}
	if allocs := testing.AllocsPerRun(100, func() { model.syncExtensionUI(model.fontResources) }); allocs != 0 {
		t.Fatalf("unchanged UI allocated %g", allocs)
	}
	editorContextClick(t, app, model, state, panel.focus[0].id)
	wait(func() bool { return model.actions.ui.panel == nil })
	if model.bottom.active != pencodePanelClosed {
		t.Fatal("closed panel remains visible")
	}
	// Dismissal reports a reason and allows reopening the same ID.
	editorContextClick(t, app, model, state, "action-extensions/ui-fixture/tools")
	wait(func() bool { return model.actions.ui.overlay != nil })
	menu = model.actions.ui.overlay
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEscape})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEscape})
	languageInteractionStep(t, app)
	if model.actions.ui.overlay != nil {
		t.Fatal("Escape did not dismiss menu")
	}
	wait(func() bool {
		logs, _ := bundle.Actions.Output.Snapshot()
		return strings.Contains(logs, "UI tools close escape tools")
	})
	// Clicking outside dismisses the menu without invoking an item.
	editorContextClick(t, app, model, state, "action-extensions/ui-fixture/tools")
	wait(func() bool { return model.actions.ui.overlay != nil })
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: 8, Y: 850})
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: 8, Y: 850})
	languageInteractionStep(t, app)
	if model.actions.ui.overlay != nil {
		t.Fatal("outside click did not dismiss menu")
	}
	wait(func() bool {
		logs, _ := bundle.Actions.Output.Snapshot()
		return strings.Contains(logs, "UI tools close outside tools")
	})
	// Rich nonmodal popups use workspace coordinates and retain inputs when moved.
	editorContextClick(t, app, model, state, "action-extensions/ui-fixture/tools")
	wait(func() bool { return model.actions.ui.overlay != nil })
	editorContextClick(t, app, model, state, model.actions.ui.overlay.focus[1].id)
	wait(func() bool { return model.actions.ui.overlay != nil && model.actions.ui.overlay.ui.Kind == peb.Popup })
	popup := model.actions.ui.overlay
	settle := func() {
		t.Helper()
		for range 4 {
			languageInteractionStep(t, app)
		}
	}
	settle()
	assertPosition := func(x, y float32) {
		t.Helper()
		b := editorContextBounds(t, model, popup.rootID)
		viewport := editorContextBounds(t, model, "pencode-extension-overlays")
		x, y = x+viewport.X, y+viewport.Y
		if math.Abs(float64(b.X-x)) > .2 || math.Abs(float64(b.Y-y)) > .2 {
			t.Fatalf("popup position = %+v, want (%g,%g)", b, x, y)
		}
	}
	assertPosition(96, 120)
	bounds = editorContextBounds(t, model, popup.rootID)
	if bounds.Width < 420 || bounds.Width > 422 || bounds.Height < 280 || bounds.Height > 282 {
		t.Fatalf("popup ignored size: %+v", bounds)
	}
	query, ok := model.fontResources.HTMLControl(model.surface, "pencode-extension-query")
	if !ok || !popup.ownsFocus(model.fontResources) {
		t.Fatal("popup search did not receive focus")
	}
	popupDefinition := popup.definition
	model.fontResources.SetControlValue(query, "retained query")
	ecs.WriteMessage(app.World, htmlui.Event{Entity: model.surface, Target: query, Kind: htmlui.EventInput, Value: "retained query", Bubbles: true})
	wait(func() bool { return popup.ui.Title == "Search: retained query" })
	settle()
	assertPosition(260, 180)
	if same, ok := model.fontResources.HTMLControl(model.surface, "pencode-extension-query"); !ok || same != query || popup.definition != popupDefinition {
		t.Fatal("moving popup replaced its input or view")
	}
	if field, ok := ecs.Get[engineui.TextField](app.World, query); !ok || field.Value != "retained query" {
		t.Fatalf("moving popup lost its input value: %+v", field)
	}
	// Tab can leave a nonmodal popup and return through native traversal.
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyTab})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyTab})
	settle()
	if popup.ownsFocus(model.fontResources) {
		t.Fatal("popup trapped Tab focus")
	}
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyLeftShift})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyTab})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyTab})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyLeftShift})
	settle()
	if model.fontResources.FocusedControl() != query {
		t.Fatal("Shift+Tab did not return to popup input")
	}
	// Underlying workbench controls remain interactive; outside clicks keep it open.
	editorContextClick(t, app, model, state, "status-output")
	settle()
	if model.bottom.active != pencodePanelOutput || model.actions.ui.overlay != popup || popup.ownsFocus(model.fontResources) {
		t.Fatal("popup blocked workbench interaction")
	}
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEscape})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEscape})
	settle()
	if model.actions.ui.overlay != popup {
		t.Fatal("popup handled Escape while workbench had focus")
	}
	move := func(placement peb.UIPlacement, anchor string) {
		t.Helper()
		description := popup.ui
		description.Placement, description.Anchor = placement, anchor
		if err := model.applyExtensionUI(popup.owner, popup.session, description); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(model.fontResources, model)
		settle()
	}
	move(peb.UIPlacement{Mode: peb.PositionAnchor, X: -500, Y: 8, Width: 420, Height: 280}, "tools")
	anchor := editorContextBounds(t, model, "action-extensions/ui-fixture/tools")
	bounds = editorContextBounds(t, model, popup.rootID)
	root = editorContextBounds(t, model, "pencode-extension-overlays")
	assertPosition(max(4, min(anchor.X-root.X-500, root.Width-bounds.Width-4)), max(4, min(anchor.Y-root.Y+anchor.Height+8, root.Height-bounds.Height-4)))
	move(peb.UIPlacement{Mode: peb.PositionAbsolute, X: 1000, Y: 600, Width: 420, Height: 280}, "")
	assertPosition(1000, 600)
	resize := func(width, height int) {
		model.app.GetViewport = func() (int, int) { return width, height }
		ecs.InsertResource(app.World, scene.ViewportSize{Width: float32(width), Height: float32(height), ScaleFactor: 1})
		for _, camera := range ecs.Query[scene.Camera](app.World).All {
			camera.Mut().UpdateViewport(width, height)
		}
		settle()
	}
	resize(1000, 600)
	bounds = editorContextBounds(t, model, popup.rootID)
	root = editorContextBounds(t, model, "pencode-extension-overlays")
	assertPosition(root.Width-bounds.Width-4, root.Height-bounds.Height-4)
	resize(1440, 900)
	assertPosition(1000, 600) // Recover requested coordinates after viewport expansion.
	move(peb.UIPlacement{Mode: peb.PositionAbsolute, X: -200, Y: -100, Width: 2400, Height: 1800}, "")
	bounds = editorContextBounds(t, model, popup.rootID)
	root = editorContextBounds(t, model, "pencode-extension-overlays")
	assertPosition(4, 4)
	if bounds.X+bounds.Width > root.X+root.Width || bounds.Y+bounds.Height > root.Y+root.Height {
		t.Fatalf("oversized popup escaped viewport: %+v in %+v", bounds, root)
	}
	move(peb.UIPlacement{}, "")
	bounds = editorContextBounds(t, model, popup.rootID)
	root = editorContextBounds(t, model, "pencode-extension-overlays")
	assertPosition((root.Width-bounds.Width)/2, (root.Height-bounds.Height)/2)
	if allocs := testing.AllocsPerRun(100, func() {
		model.syncExtensionUI(model.fontResources)
		model.updateExtensionUI(model.fontResources, state)
	}); allocs != 0 {
		t.Fatalf("idle popup allocated %g", allocs)
	}
	editorContextClick(t, app, model, state, popup.closeID)
	wait(func() bool {
		logs, _ := bundle.Actions.Output.Snapshot()
		return model.actions.ui.overlay == nil && strings.Contains(logs, "UI market close button tools")
	})
	editorContextClick(t, app, model, state, "action-extensions/ui-fixture/tools")
	wait(func() bool { return model.actions.ui.overlay != nil })
	editorContextClick(t, app, model, state, model.actions.ui.overlay.focus[1].id)
	wait(func() bool { return model.actions.ui.overlay != nil && model.actions.ui.overlay.ui.Kind == peb.Popup })
	settle()
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEscape})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEscape})
	wait(func() bool {
		logs, _ := bundle.Actions.Output.Snapshot()
		return model.actions.ui.overlay == nil && strings.Contains(logs, "UI market close escape tools")
	})
	// Toggle outside dismissal through the extension protocol without replacing
	// controls. Closing must work even after native focus leaves the popup.
	editorContextClick(t, app, model, state, "action-extensions/ui-fixture/tools")
	wait(func() bool { return model.actions.ui.overlay != nil })
	editorContextClick(t, app, model, state, model.actions.ui.overlay.focus[1].id)
	wait(func() bool { return model.actions.ui.overlay != nil && model.actions.ui.overlay.ui.Kind == peb.Popup })
	popup = model.actions.ui.overlay
	settle()
	query, _ = model.fontResources.HTMLControl(model.surface, "pencode-extension-query")
	setOutsidePolicy := func(value string, enabled bool) {
		t.Helper()
		definition := popup.definition
		model.fontResources.SetControlValue(query, value)
		ecs.WriteMessage(app.World, htmlui.Event{Entity: model.surface, Target: query, Kind: htmlui.EventInput, Value: value, Bubbles: true})
		wait(func() bool { return popup.ui.Title == "Search: "+value && popup.ui.CloseOnOutsideClick == enabled })
		settle()
		if same, ok := model.fontResources.HTMLControl(model.surface, "pencode-extension-query"); !ok || same != query || popup.definition != definition {
			t.Fatal("changing outside policy replaced popup controls")
		}
	}
	setOutsidePolicy("dismiss", true)
	editorContextClick(t, app, model, state, "pencode-extension-query")
	if model.actions.ui.overlay != popup {
		t.Fatal("inside click dismissed popup")
	}
	setOutsidePolicy("keep", false)
	editorContextClick(t, app, model, state, "status-output")
	settle()
	if model.actions.ui.overlay != popup || popup.ownsFocus(model.fontResources) {
		t.Fatal("disabled outside dismissal did not keep popup open")
	}
	setOutsidePolicy("dismiss", true)
	if popup.ownsFocus(model.fontResources) {
		t.Fatal("updating outside policy stole workbench focus")
	}
	bottomBefore := model.bottom.active
	editorContextClick(t, app, model, state, "status-output")
	wait(func() bool {
		logs, _ := bundle.Actions.Output.Snapshot()
		return model.actions.ui.overlay == nil && strings.Count(logs, "UI market close outside tools") == 1
	})
	if model.bottom.active == bottomBefore {
		t.Fatal("outside dismissal swallowed workbench click")
	}
	if control, ok := model.fontResources.HTMLControl(model.surface, "status-output"); !ok || model.fontResources.FocusedControl() != control {
		t.Fatal("outside dismissal moved focus away from clicked control")
	}
	if _, ok := model.fontResources.HTMLNode(model.surface, popup.rootID); ok {
		t.Fatal("outside dismissal retained popup DOM")
	}
	// Unloading an extension removes all retained surfaces and controls.
	editorContextClick(t, app, model, state, "action-extensions/ui-fixture/tools")
	wait(func() bool { return model.actions.ui.overlay != nil })
	model.removeActions("ui-fixture")
	markPencodeRootDirtyByModel(model.fontResources, model)
	languageInteractionStep(t, app)
	if len(model.actions.ui.surfaces) != 0 {
		t.Fatal("unload retained UI")
	}
	if _, ok := model.fontResources.HTMLNode(model.surface, menu.rootID); ok {
		t.Fatal("unload retained menu DOM")
	}
	if allocs := testing.AllocsPerRun(100, func() {
		model.syncExtensionUI(model.fontResources)
		model.updateExtensionUI(model.fontResources, state)
	}); allocs != 0 {
		t.Fatalf("idle UI allocated %g", allocs)
	}
	if popupFrames == 0 {
		t.Fatal("no popup frames reached the paint check")
	}
}
