package workbench

import (
	"strings"
	"testing"
	"time"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/remote"
)

func TestMarketplaceDirectoryButtonFollowsWorkspace(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	session.ConfigureConnection(nil, connection.Profile{})
	model.market.open = true
	model.market.directory = t.TempDir()
	for _, screen := range [...]pencodeScreen{pencodeWorkspace, pencodeLauncher} {
		model.screen = screen
		if err := app.Modules().UpdateFrame(1.0 / 60); err != nil {
			t.Fatal(err)
		}
		if err := app.Modules().Dispatch(); err != nil {
			t.Fatal(err)
		}
		model = session.active.model
		for _, remoteMode := range [...]bool{false, true, false} {
			profile := connection.Profile{}
			if remoteMode {
				profile.SSH = remote.Target{Host: "remote"}
			}
			session.connection.workspaceProfile = profile
			markPencodeRootDirtyByModel(model.fontResources, model)
			if err := model.fontResources.SyncHTML(); err != nil {
				t.Fatal(err)
			}
			node, ok := model.fontResources.HTMLNode(model.surface, "market-open-directory")
			if !ok {
				t.Fatal("missing directory button")
			}
			markup, err := node.Serialize()
			if err != nil {
				t.Fatal(err)
			}
			if hidden := strings.Contains(markup, " hidden"); hidden != remoteMode {
				t.Fatalf("directory button visibility in remote mode %v: %s", remoteMode, markup)
			}
			if remoteMode {
				model.handleMarketplaceEvent(&module.ContentEventContext[appProps, appState]{Ref: "market-open-directory"})
				if model.market.busy {
					t.Fatal("remote workspace started opening a server path locally")
				}
			}
		}
	}
}

func waitPencodeMarket(t *testing.T, model *workspaceModel) {
	t.Helper()
	if !model.market.busy {
		return
	}
	select {
	case <-model.market.done:
	case <-time.After(10 * time.Second):
		t.Fatal("marketplace operation did not finish")
	}
	if !model.pollMarketplace() {
		t.Fatal("marketplace result was not applied")
	}
}

func TestMarketplaceButtonOutsideWindowDragRegion(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	// The headless fixture starts with the adaptive viewport's 1x1 placeholder.
	model.app.GetViewport = func() (int, int) { return 1440, 900 }
	ecs.InsertResource(app.World, scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	for _, zoom := range [...]int{70, 100, 150} {
		model.setZoom(model.fontResources, zoom)
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
		width := pencodeViewportWidth
		if viewportWidth, _ := model.app.GetViewport(); viewportWidth > 0 {
			width = float64(viewportWidth)
		}
		scale := float64(zoom) / 100
		for _, id := range [...]string{"marketplace-button", "settings-button", "file-search"} {
			node, ok := model.fontResources.HTMLNode(model.surface, id)
			if !ok {
				t.Fatalf("missing %s", id)
			}
			bounds, ok := node.Bounds()
			if !ok {
				t.Fatalf("missing bounds for %s", id)
			}
			if id == "file-search" {
				center := (float64(bounds.X) + float64(bounds.Width)/2) * scale
				if delta := center - width/2; delta < -0.1 || delta > 0.1 {
					t.Fatalf("file search is not centered at zoom %d: center=%g, viewport width=%g", zoom, center, width)
				}
			}
			for _, fraction := range [...]float64{0.01, 0.5, 0.99} {
				x := (float64(bounds.X) + float64(bounds.Width)*fraction) * scale
				y := (float64(bounds.Y) + float64(bounds.Height)/2) * scale
				if pencodeWindowDragRegion(width, scale, x, y) {
					t.Fatalf("window drag captures %s at zoom %d, (%g, %g)", id, zoom, x, y)
				}
			}
		}
		if !pencodeWindowDragRegion(width, scale, max(110, 100*scale), 20*scale) {
			t.Fatalf("titlebar background no longer drags at zoom %d, viewport width %g", zoom, width)
		}
	}
}

func TestMarketplaceAndSettingsDismissOutsideClick(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	model.app.GetViewport = func() (int, int) { return 1440, 900 }
	ecs.InsertResource(app.World, scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	for _, camera := range ecs.Query[scene.Camera](app.World).All {
		camera.Mut().UpdateViewport(1440, 900)
	}
	state, _ := ecs.GetResource[*input.State](app.World)
	step := func() {
		t.Helper()
		markPencodeRootDirtyByModel(model.fontResources, model)
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
	}
	clickAt := func(x, y float64) {
		t.Helper()
		scale := float64(model.settings.Zoom) / 100
		state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x * scale, Y: y * scale})
		state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x * scale, Y: y * scale})
		step()
	}
	bounds := func(id string) htmlui.Rect {
		t.Helper()
		node, ok := model.fontResources.HTMLNode(model.surface, id)
		if !ok {
			t.Fatalf("missing %s", id)
		}
		rect, ok := node.Bounds()
		if !ok {
			t.Fatalf("missing bounds for %s", id)
		}
		return rect
	}
	for _, zoom := range [...]int{70, 100, 150} {
		model.setZoom(model.fontResources, zoom)
		step()
		button := bounds("marketplace-button")
		marketX, marketY := float64(button.X+button.Width/2), float64(button.Y+button.Height/2)
		clickAt(marketX, marketY)
		waitPencodeMarket(t, model)
		step()
		if !model.market.open {
			t.Fatalf("clicking the titlebar button did not open the marketplace at zoom %d", zoom)
		}
		panel := bounds("marketplace-panel")
		clickAt(float64(panel.X+panel.Width/2), float64(panel.Y+12))
		if !model.market.open {
			t.Fatal("clicking inside the marketplace dismissed it")
		}
		clickAt(marketX, marketY)
		if model.market.open {
			t.Fatal("clicking the marketplace button again did not close it")
		}
		clickAt(marketX, marketY)
		waitPencodeMarket(t, model)
		step()
		if !model.market.open {
			t.Fatalf("the marketplace did not reopen at zoom %d", zoom)
		}
		clickAt(float64(panel.X-30), float64(panel.Y+30))
		if model.market.open {
			t.Fatal("clicking outside the marketplace did not dismiss it")
		}

		settings := bounds("settings-button")
		clickAt(float64(settings.X+settings.Width/2), float64(settings.Y+settings.Height/2))
		if !model.settingsOpen {
			t.Fatalf("clicking the titlebar button did not open settings at zoom %d", zoom)
		}
		panel = bounds("settings-panel")
		entity, ok := model.fontResources.HTMLControl(model.surface, "theme-select")
		if !ok {
			t.Fatal("settings theme select was not mounted")
		}
		if control := module.ContentMut[htmlui.Control](model.fontResources, entity); control != nil {
			control.Open = true
		}
		clickAt(float64(panel.X-30), float64(panel.Y+30))
		if !model.settingsOpen {
			t.Fatal("an outside press dismissed settings while an option picker was open")
		}
		if control := module.ContentMut[htmlui.Control](model.fontResources, entity); control != nil {
			control.Open = false
		}
		clickAt(float64(panel.X-30), float64(panel.Y+30))
		if model.settingsOpen {
			t.Fatal("clicking outside the settings panel did not dismiss it")
		}
		clickAt(float64(settings.X+settings.Width/2), float64(settings.Y+settings.Height/2))
		if !model.settingsOpen {
			t.Fatalf("the settings panel did not reopen at zoom %d", zoom)
		}
		clickAt(float64(panel.X+panel.Width/2), float64(panel.Y+12))
		if !model.settingsOpen {
			t.Fatal("clicking inside the settings panel dismissed it")
		}
		clickAt(float64(settings.X+settings.Width/2), float64(settings.Y+settings.Height/2))
		if model.settingsOpen {
			t.Fatal("clicking the settings button again did not close it")
		}
	}
}

func TestMarketplaceStateFollowsRetainedViews(t *testing.T) {
	app, session := newPencodeTestApp(t)
	workspace := session.active
	model := workspace.model
	model.market.open = true
	model.market.filter = "markdown"
	model.startMarketOperation(pencodeMarketRefresh, "", "")
	model.screen = pencodeLauncher
	if err := app.Modules().UpdateFrame(1.0 / 60); err != nil {
		t.Fatal(err)
	}
	if err := app.Modules().Dispatch(); err != nil {
		t.Fatal(err)
	}
	launcher := session.active
	if launcher == workspace || launcher.model.market != model.market || launcher.model.market.filter != "markdown" {
		t.Fatal("scene switch lost the shared marketplace state")
	}
	waitPencodeMarket(t, launcher.model)
	if launcher.model.market.busy {
		t.Fatal("launcher did not consume the background operation")
	}
}
