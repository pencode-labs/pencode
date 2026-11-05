package workbench

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/render"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
	"github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/actions"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

// Capture uploads in memory; no window, editor process or screenshot is used.
type pencodeIconDriver struct {
	render.Driver
	pixels      map[render.TextureHandle][]byte
	descriptors map[render.TextureHandle]render.TextureDescriptor
	next        uint32
}

func (*pencodeIconDriver) RendererInfo() render.RendererInfo { return render.RendererInfo{} }
func (driver *pencodeIconDriver) CreateTexture(descriptor render.TextureDescriptor, pixels []byte) (render.TextureHandle, error) {
	driver.next++
	handle := render.NewHandle[render.TextureResource](driver.next, 1)
	driver.pixels[handle] = append([]byte(nil), pixels...)
	driver.descriptors[handle] = descriptor
	return handle, nil
}
func (*pencodeIconDriver) DestroyTexture(render.TextureHandle) error { return nil }
func (*pencodeIconDriver) Destroy()                                  {}

func TestCodexStatusBarIconRenders(t *testing.T) {
	app, session := newPencodeTestLauncher(t)
	launcher := session.active
	model := launcher.model
	const source = "peb://codex/resources/icons/chatgpt.svg"
	data, err := os.ReadFile("testdata/codex/icons/chatgpt.svg")
	if err != nil {
		t.Fatal(err)
	}
	if err := model.importExtension(&extensions.Bundle{ID: "codex", Icons: map[string][]byte{source: data}}); err != nil {
		t.Fatal(err)
	}
	const id = "extensions/codex/chat"
	if err := model.actionRegistry().Register(actions.Definition{Action: peb.Action{ID: id, Tooltip: "Codex Agent", Icon: source, Location: peb.StatusBar}, Run: func(peb.ActionContext) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	if err := loadProjectAndWait(t, model, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := app.Modules().UpdateFrame(1.0 / 60); err != nil {
			t.Fatal(err)
		}
		if err := app.Modules().Dispatch(); err != nil {
			t.Fatal(err)
		}
	}
	if session.active == launcher {
		t.Fatal("project did not activate the workspace")
	}
	model, provider := session.active.model, session.active.icons
	for _, theme := range []string{model.workbenchTheme.id, "dark-plus", "light-plus"} {
		model.selectTheme(theme)
		markPencodeRootDirtyByModel(model.fontResources, model)
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
		texture, ok := provider.ResolveHTMLImage(model.themedIcon(source, "icon.foreground"), 0, 0)
		if !ok {
			t.Fatal("missing Codex icon texture")
		}
		found := false
		for entity, access := range model.fontResources.Query[scene.Sprite2D]() {
			parent, ok := model.fontResources.Get[scene.Parent](entity)
			if !ok {
				continue
			}
			control, ok := model.fontResources.Get[htmlui.Control](parent.Entity)
			if ok && control.ID == "action-"+id && access.Ref().Texture == texture {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s did not render the Codex status bar icon", theme)
		}
	}
}

func TestPencodeIconsRasterizeThemeColorsAndReuseTextures(t *testing.T) {
	theme, err := syntax.LoadTheme(fstest.MapFS{"resources/icons.json": &fstest.MapFile{Data: []byte(`{
  "type":"light", "colors":{
   "icon.foreground":"#2468ac80", "button.foreground":"#135790", "textLink.foreground":"#315ac8",
   "symbolIcon.textForeground":"#2468ac80", "symbolIcon.methodForeground":"#652d90", "symbolIcon.functionForeground":"#652d90",
   "symbolIcon.constructorForeground":"#652d90", "symbolIcon.fieldForeground":"#0070c1", "symbolIcon.variableForeground":"#0070c1",
   "symbolIcon.classForeground":"#9e6a03", "symbolIcon.interfaceForeground":"#008070", "symbolIcon.moduleForeground":"#a06c00",
   "symbolIcon.propertyForeground":"#a06c00", "symbolIcon.unitForeground":"#a06c00", "symbolIcon.numberForeground":"#a06c00",
   "symbolIcon.enumeratorForeground":"#9e6a03", "symbolIcon.keywordForeground":"#af00db", "symbolIcon.snippetForeground":"#2468ac80",
   "symbolIcon.colorForeground":"#2468ac80", "symbolIcon.fileForeground":"#2468ac80", "symbolIcon.referenceForeground":"#2468ac80",
   "symbolIcon.folderForeground":"#9e6a03", "symbolIcon.enumeratorMemberForeground":"#0070c1", "symbolIcon.constantForeground":"#0070c1",
   "symbolIcon.structForeground":"#008070", "symbolIcon.eventForeground":"#9e6a03", "symbolIcon.operatorForeground":"#af00db", "symbolIcon.typeParameterForeground":"#008070",
   "problemsErrorIcon.foreground":"#e81123", "problemsWarningIcon.foreground":"#bf8803",
   "problemsInfoIcon.foreground":"#1a85ff", "editorHint.foreground":"#6c6c6c",
   "notificationsErrorIcon.foreground":"#d92233", "notificationsWarningIcon.foreground":"#ba8704", "notificationsInfoIcon.foreground":"#2b76ee",
   "list.activeSelectionForeground":"#cc00dd", "tab.activeForeground":"#112233", "tab.inactiveForeground":"#aabbcc",
   "debugIcon.startForeground":"#388e3c", "debugIcon.continueForeground":"#007acc",
   "debugIcon.pauseForeground":"#a06c00", "debugIcon.restartForeground":"#a06c00",
   "debugIcon.stepOverForeground":"#007acc", "debugIcon.stepIntoForeground":"#008070",
   "debugIcon.stepOutForeground":"#8544ad", "debugIcon.stopForeground":"#c42b2b", "debugIcon.breakpointForeground":"#c42b2b"
  }
 }`)}}, "resources/icons.json")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparePencodeTheme("icons", theme, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := prepared.icons[pencodeIconKey{"icons/arrow-right.svg", "icon.foreground"}]; !ok {
		t.Fatal("diff revert arrow is missing its theme color")
	}
	previous := render.ActiveDevice()
	driver := &pencodeIconDriver{
		pixels:      make(map[render.TextureHandle][]byte),
		descriptors: make(map[render.TextureHandle]render.TextureDescriptor),
	}
	device, err := render.NewDevice(driver)
	if err != nil {
		t.Fatal(err)
	}
	device.Activate()
	defer func() { device.Destroy(); previous.Activate() }()
	provider := pencodeIconProvider{root: filepath.Join("..", "resources", "workbench", "assets")}
	const codexIcon = "peb://codex/resources/icons/chatgpt.svg"
	data, err := os.ReadFile("testdata/codex/icons/chatgpt.svg")
	if err != nil {
		t.Fatal(err)
	}
	provider.model = &workspaceModel{extensionIcons: map[string][]byte{codexIcon: data}}
	prepared.icons[pencodeIconKey{codexIcon, "icon.foreground"}] = codexIcon + "?foreground=2468ac80"
	defer func() {
		for _, image := range provider.textures {
			if image.texture != nil {
				_ = image.texture.Dispose()
			}
		}
	}()
	for key, source := range prepared.icons {
		texture, ok := provider.ResolveHTMLImage(source, 0, 0)
		if !ok || !texture.Valid() {
			t.Fatalf("decode %s", source)
		}
		if !texture.Mipmapped() || texture.MipLevels() < 3 {
			t.Fatalf("%s has no mip chain for its raster", source)
		}
		if x, y := texture.RasterScale(); x != 1 || y != 1 {
			t.Fatalf("%s raster scale = %gx%g, want 1x1", source, x, y)
		}
		if descriptor := driver.descriptors[texture.RenderHandle()]; descriptor.Filter != render.TextureFilterLinearMipmapLinear {
			t.Fatalf("%s uploaded without mipmap filtering: %v", source, descriptor.Filter)
		}
		pixels := driver.pixels[texture.RenderHandle()]
		expected := theme.Color(key.foreground)
		visible := false
		var maxAlpha uint8
		for offset := 0; offset < len(pixels); offset += 4 {
			// Transparent texels must also carry the icon color, otherwise
			// mipmap and bilinear filtering darken the visible contour.
			if pixels[offset] != expected.R || pixels[offset+1] != expected.G || pixels[offset+2] != expected.B {
				t.Fatalf("%s raster color = %v, want %v", source, pixels[offset:offset+4], expected)
			}
			if pixels[offset+3] == 0 {
				continue
			}
			visible = true
			maxAlpha = max(maxAlpha, pixels[offset+3])
		}
		if !visible {
			t.Fatalf("%s has no visible pixels", source)
		}
		// Overlapping paths can accumulate opacity; an alpha theme must still
		// remain translucent after rasterization.
		if expected.A < 255 && maxAlpha == 255 {
			t.Fatalf("%s lost its theme opacity", source)
		}
		if allocations := testing.AllocsPerRun(100, func() {
			cached, ok := provider.ResolveHTMLImage(source, 0, 0)
			if !ok || cached != texture {
				t.Fatal("cached icon texture changed")
			}
		}); allocations != 0 {
			t.Fatalf("cached %s allocated %g times", source, allocations)
		}
	}
	const preview = "peb://preview/resources/image.svg"
	provider.model = &workspaceModel{extensionImages: map[string]pencodeExtensionImage{
		preview: {data: []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="64" height="32"><rect width="64" height="32"/></svg>`), references: 1},
	}}
	texture, ok := provider.ResolveHTMLImage(preview, 0, 0)
	if !ok || texture.Width != 64 || texture.Height != 32 {
		t.Fatal("editor preview inherited icon supersampling")
	}
	if x, y := texture.RasterScale(); x != 1 || y != 1 {
		t.Fatalf("editor preview raster scale = %gx%g", x, y)
	}
}

func TestPencodeIconsFollowSelectionAndTabState(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model, provider := session.active.model, session.active.icons
	project := model.activeProjectState()
	// Both files are real retained tabs and appear in the tree.
	other := &openFile{Path: "other.go", Name: "other.go", contentSnapshot: snapshotText("package demo\n")}
	project.Files[other.Path] = other
	project.Tabs = append(project.Tabs, other.Path)
	project.Entries = append(project.Entries, treeEntry{Path: other.Path, Name: other.Name, Openable: true})
	model.selectTheme("light-plus")
	step := func() {
		t.Helper()
		markPencodeRootDirtyByModel(model.fontResources, model)
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
	}
	assertIcons := func() {
		t.Helper()
		for _, key := range []pencodeIconKey{
			{"icons/file-text.svg", "list.activeSelectionForeground"},
			{"icons/file-text.svg", "icon.foreground"},
			{"icons/file-text.svg", "tab.activeForeground"},
			{"icons/file-text.svg", "tab.inactiveForeground"},
			{"icons/close.svg", "tab.activeForeground"},
			{"icons/close.svg", "tab.inactiveForeground"},
		} {
			texture, ok := provider.ResolveHTMLImage(model.themedIcon(key.source, key.foreground), 0, 0)
			if !ok {
				t.Fatalf("missing %v texture", key)
			}
			var found bool
			for _, access := range model.fontResources.Query[scene.Sprite2D]() {
				found = found || access.Ref().Texture == texture
			}
			if !found {
				t.Fatalf("no rendered icon uses %v", key)
			}
		}
	}
	step()
	assertIcons()
	model.selectTab(other.Path)
	step()
	assertIcons()
	model.selectTheme("dark-plus")
	step()
	assertIcons()
}

func TestExtensionUIReusesIDEIconTextures(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model, provider := session.active.model, session.active.icons
	css, err := os.ReadFile("testdata/codex/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	if err := model.applyExtensionUI("icons", &extensions.ActionSession{}, peb.UI{
		ID: "icons", Kind: peb.EditorTab, View: &peb.EditorView{
			HTML: `<main class="codex-workspace"><div class="codex-history-header"><button id="pencode-extension-edit-button" class="codex-icon-button"><img id="pencode-extension-edit" data-ref="edit" data-pencode-icon="edit" alt=""></button><div data-ref="region" data-pencode-region><button id="pencode-extension-refresh-button" class="codex-icon-button"><img id="pencode-extension-refresh" data-ref="refresh" data-pencode-icon="refresh-cw" alt=""></button></div></div></main>`,
			CSS:  string(css),
		},
	}); err != nil {
		t.Fatal(err)
	}
	for _, theme := range []string{"light-plus", "dark-plus"} {
		model.selectTheme(theme)
		markPencodeRootDirtyByModel(model.fontResources, model)
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
		for _, icon := range []struct{ id, source string }{
			{"pencode-extension-edit", "icons/edit.svg"},
			{"pencode-extension-refresh", "icons/refresh-cw.svg"},
		} {
			bounds := editorContextBounds(t, model, icon.id)
			if bounds.Width != 16 || bounds.Height != 16 {
				t.Fatalf("%s size = %gx%g", icon.id, bounds.Width, bounds.Height)
			}
			texture, ok := provider.ResolveHTMLImage(model.themedIcon(icon.source, "icon.foreground"), 0, 0)
			if !ok {
				t.Fatalf("missing IDE texture for %s", icon.source)
			}
			if x, y := texture.RasterScale(); x != 1 || y != 1 || !texture.Mipmapped() {
				t.Fatalf("%s must use display-sized sampling and mipmaps", icon.source)
			}
			found := false
			for entity, access := range model.fontResources.Query[scene.Sprite2D]() {
				parent, ok := model.fontResources.Get[scene.Parent](entity)
				if !ok {
					continue
				}
				control, ok := model.fontResources.Get[htmlui.Control](parent.Entity)
				if ok && control.ID == icon.id+"-button" {
					found = found || access.Ref().Texture == texture
				}
			}
			if !found {
				t.Fatalf("%s did not render the shared %s texture", theme, icon.source)
			}
		}
	}
}

func TestIconResolutionFollowsRenderedSize(t *testing.T) {
	app, session := newPencodeTestApp(t)
	provider := session.active.icons
	const source = "icons/close.svg?foreground=123456"
	small, ok := provider.ResolveHTMLImage(source, 14, 14)
	if !ok {
		t.Fatal("small icon")
	}
	beforeWidth, beforeHeight := small.Width, small.Height
	before := beforeWidth * beforeHeight
	layer := provider.content.SpawnNode(ui.NodeOptions{}, scene.SpriteLayers2D{Layers: []scene.SpriteLayer2D{{Sprite: scene.Sprite2D{Texture: small}}}})
	large, ok := provider.ResolveHTMLImage(source, 28, 28)
	if !ok {
		t.Fatal("large icon")
	}
	if large.Width*large.Height != before*4 {
		t.Fatalf("raster did not follow actual display size: %dx%d -> %dx%d", beforeWidth, beforeHeight, large.Width, large.Height)
	}
	if got, _ := provider.content.Get[scene.SpriteLayers2D](layer); got.Layers[0].Sprite.Texture != large {
		t.Fatal("resolution upgrade left a layered image with a retired texture")
	}
	if n := testing.AllocsPerRun(100, func() {
		cached, ok := provider.ResolveHTMLImage(source, 16, 16)
		if !ok || cached != large {
			t.Fatal("larger raster was not reused")
		}
	}); n != 0 {
		t.Fatalf("warm image lookup allocated %g", n)
	}
	app.RequestRender()
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
}
func TestImageCacheBoundsAndKeepsLiveSprites(t *testing.T) {
	app, session := newPencodeTestApp(t)
	provider := session.active.icons
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	var live *graphics.Texture
	for _, access := range provider.content.Query[scene.Sprite2D]() {
		if image := provider.images[access.Ref().Texture]; image != nil {
			live = image.texture
			break
		}
	}
	if live == nil {
		t.Fatal("missing live icon fixture")
	}
	layerLive, ok := provider.ResolveHTMLImage("icons/close.svg?foreground=ff00ff", 14, 14)
	if !ok {
		t.Fatal("layered image fixture")
	}
	provider.content.SpawnNode(ui.NodeOptions{}, scene.SpriteLayers2D{Layers: []scene.SpriteLayer2D{{Sprite: scene.Sprite2D{Texture: layerLive}}}})
	for i := range pencodeImageCacheEntries + 64 {
		source := fmt.Sprintf("icons/close.svg?foreground=%06x", i)
		if _, ok := provider.ResolveHTMLImage(source, 16, 16); !ok {
			t.Fatal(source)
		}
	}
	provider.trimImages()
	if len(provider.textures) > pencodeImageCacheEntries || provider.bytes > pencodeImageCacheBytes {
		t.Fatalf("cache exceeded budget: %d images %d bytes", len(provider.textures), provider.bytes)
	}
	if !live.Valid() || !layerLive.Valid() {
		t.Fatal("cache disposed a live sprite texture")
	}
	oldCount := len(provider.textures)
	provider.model.selectTheme("light-plus")
	provider.trimImages()
	if len(provider.textures) >= oldCount {
		t.Fatal("theme switch retained unused old variants")
	}
	if !live.Valid() || !layerLive.Valid() {
		t.Fatal("theme sweep disposed a retained sprite")
	}
}
