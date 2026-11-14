package workbench

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
	"github.com/pencode-labs/pencode/remote"
)

func TestMarketplaceDirectoryButtonFollowsWorkspace(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	mockMarketplace(t, session)
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
	app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
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
	mockMarketplace(t, session)
	model := session.active.model
	model.app.GetViewport = func() (int, int) { return 1440, 900 }
	app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	for _, camera := range app.World.Query[scene.Camera]().All {
		camera.Mut().UpdateViewport(1440, 900)
	}
	state, _ := app.World.GetResource[*input.State]()
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
		if control := model.fontResources.Mut[htmlui.Control](entity); control != nil {
			control.Open = true
		}
		clickAt(float64(panel.X-30), float64(panel.Y+30))
		if !model.settingsOpen {
			t.Fatal("an outside press dismissed settings while an option picker was open")
		}
		if control := model.fontResources.Mut[htmlui.Control](entity); control != nil {
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
	mockMarketplace(t, session)
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

func mockMarketplace(t *testing.T, session *Session) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"extensions":[]}`) }))
	t.Cleanup(server.Close)
	session.market.source = extensions.Marketplace{Source: server.URL}
}

func TestMarketplaceRefreshOnlyChecksUpdates(t *testing.T) {
	_, session := newPencodeTestApp(t)
	model := session.active.model
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "resources", "extension.json"), []byte(`{"id":"test","version":"1.0.0","entry":"test","disabled":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "test"), []byte("disabled fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := extensions.Install(t.Context(), session.backend, source); err != nil {
		t.Fatal(err)
	}
	catalog, err := session.backend.ListExtensions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var downloads, checks atomic.Int32
	var forbidden atomic.Bool
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/extensions.json":
			io.WriteString(w, `{"extensions":[{"name":"test","repository":"https://github.com/pencode-labs/test"}]}`)
		case "/repos/pencode-labs/test/releases/latest":
			checks.Add(1)
			if forbidden.Load() {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.WriteHeader(http.StatusForbidden)
				io.WriteString(w, `{"message":"API rate limit exceeded"}`)
				return
			}
			fmt.Fprintf(w, `{"tag_name":"v1.1.0","assets":[{"name":%q,"browser_download_url":%q}]}`, "test-v1.1.0-"+catalog.OS+"-"+catalog.Arch+".zip", server.URL+"/package.zip")
		case "/package.zip":
			downloads.Add(1)
			http.Error(w, "download fixture", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	model.market.source = extensions.Marketplace{Source: server.URL + "/extensions.json", API: server.URL, Client: server.Client()}
	for _, ref := range [...]string{"open-marketplace", "market-refresh"} {
		model.handleMarketplaceEvent(&module.ContentEventContext[appProps, appState]{Ref: ref})
		waitPencodeMarket(t, model)
		if len(model.market.items) != 1 || model.market.items[0].Version != "1.0.0" || model.market.feedback != "" {
			t.Fatal("update check changed the installed extension", model.market.items, model.market.feedback)
		}
		model.cacheMarketDetails(&model.market.items[0])
		if !model.market.download || !strings.Contains(model.market.details[3], "1.1.0") || downloads.Load() != 0 {
			t.Fatal("update check did not offer a manual update", model.market.details, downloads.Load())
		}
	}
	if checks.Load() != 2 {
		t.Fatalf("got %d release checks, want 2", checks.Load())
	}
	model.handleMarketplaceEvent(&module.ContentEventContext[appProps, appState]{Ref: "market-download"})
	waitPencodeMarket(t, model)
	if downloads.Load() != 1 || !strings.Contains(model.market.feedback, "HTTP 403") {
		t.Fatal("explicit update did not initiate the download", downloads.Load(), model.market.feedback)
	}
	logs, _ := model.outputStore().Channel(extensions.MarketplaceOutputChannelName).Snapshot()
	if !strings.Contains(logs, "download fixture") || !strings.Contains(logs, "[Error] "+model.market.feedback) {
		t.Fatal("download diagnostics did not reach Output", logs)
	}
	forbidden.Store(true)
	model.startMarketOperation(pencodeMarketRefresh, "", "")
	waitPencodeMarket(t, model)
	model.cacheMarketDetails(&model.market.items[0])
	if model.market.items[0].Version != "1.0.0" || !strings.Contains(model.market.details[3], "API rate limit exceeded") || model.market.download {
		t.Fatal("failed update check changed installed status", model.market.details)
	}
	logs, _ = model.outputStore().Channel(extensions.MarketplaceOutputChannelName).Snapshot()
	for _, detail := range [...]string{"[Info] GET ", "HTTP 403", "X-RateLimit-Remaining: 0", "API rate limit exceeded", "[Error] test:"} {
		if !strings.Contains(logs, detail) {
			t.Errorf("missing marketplace Output diagnostic %q in %s", detail, logs)
		}
	}
}

func TestMarketplaceUpdateCheckFailurePreservesInstalledStatus(t *testing.T) {
	_, session := newPencodeTestApp(t)
	model := session.active.model
	model.market.items = []extensions.Installed{{ExtensionManifest: extensions.Manifest{ID: "test", Version: "1.0.0"}}}
	model.market.listings = []extensions.Listing{{Name: "test", Problem: "HTTP 403"}}
	model.cacheMarketDetails(&model.market.items[0])
	status := model.market.details[3]
	if !strings.Contains(status, model.tr("已安装")) || !strings.Contains(status, fmt.Sprintf(model.tr("更新检查失败：%s"), "HTTP 403")) || model.market.download {
		t.Fatal("update check failure was confused with an installation failure", status)
	}
}

func TestMarketplaceDownloadActions(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	m.market.open = true
	m.market.listings = []extensions.Listing{{Name: "test", Version: "1.10.0", URL: "https://example.com/package.zip", Description: "Online extension"}}
	m.market.selected = "test"
	for _, screen := range [...]pencodeScreen{pencodeWorkspace, pencodeLauncher} {
		m.screen = screen
		if err := app.Modules().UpdateFrame(1.0 / 60); err != nil {
			t.Fatal(err)
		}
		if err := app.Modules().Dispatch(); err != nil {
			t.Fatal(err)
		}
		m = session.active.model
		for _, state := range []struct {
			version, label     string
			visible, installed bool
		}{
			{label: "安装扩展", visible: true},
			{version: "1.9.0", label: "更新扩展", visible: true, installed: true},
			{version: "1.10.0", label: "更新扩展", installed: true},
			{version: "1.11.0", label: "更新扩展", installed: true},
		} {
			m.market.items = nil
			if state.installed {
				m.market.items = []extensions.Installed{{ExtensionManifest: extensions.Manifest{ID: "test", Version: state.version}}}
			}
			m.market.revision++
			markPencodeRootDirtyByModel(m.fontResources, m)
			if err := m.fontResources.SyncHTML(); err != nil {
				t.Fatal(err)
			}
			node, ok := m.fontResources.HTMLNode(m.surface, "market-download")
			if !ok {
				t.Fatal("missing download button")
			}
			markup, err := node.Serialize()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(markup, m.tr(state.label)) || strings.Contains(markup, " hidden") == state.visible {
				t.Fatalf("unexpected download action for %s: %s", state.version, markup)
			}
			node, _ = m.fontResources.HTMLNode(m.surface, "market-uninstall")
			markup, err = node.Serialize()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(markup, " disabled") == state.installed {
				t.Fatalf("unexpected uninstall action: %s", markup)
			}
		}
	}
}

func TestMarketplaceUpdateReplacesLoadedContributions(t *testing.T) {
	_, session := newPencodeTestApp(t)
	model := session.active.model
	model.setExtensionState("test", pencodeExtensionLoaded)
	model.importExtensionSettings("test", "Test", []peb.Setting{{ID: "old", Label: "Old setting", Type: peb.SettingBoolean, Default: []byte("true")}})
	model.extensionIcons = map[string][]byte{"peb://test/resources/old.svg": []byte("old")}
	model.market.busy = true
	model.market.results = make(chan pencodeMarketResult, 1)
	model.market.results <- pencodeMarketResult{
		op: pencodeMarketUpdate, id: "test",
		items: []extensions.Installed{{ExtensionManifest: extensions.Manifest{ID: "test", Version: "1.1.0"}}},
		bundles: map[string]*extensions.Bundle{"test": {
			ID: "test", Label: "Test", Icons: map[string][]byte{"peb://test/resources/new.svg": []byte("new")},
			Settings: []peb.Setting{{ID: "new", Label: "New setting", Type: peb.SettingBoolean, Default: []byte("true")}},
		}},
	}
	if !model.pollMarketplace() || model.extensionStates["test"] != pencodeExtensionLoaded {
		t.Fatal("updated extension was not reloaded")
	}
	if len(model.extensionIcons["peb://test/resources/old.svg"]) != 0 || string(model.extensionIcons["peb://test/resources/new.svg"]) != "new" {
		t.Fatal("update retained old resources or discarded new contributions")
	}
	if len(model.extensionSettings) != 1 || model.extensionSettings[0].schema[0].ID != "new" {
		t.Fatal("update did not replace extension settings")
	}
}
