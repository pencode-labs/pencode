package workbench

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/render"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

// Capture uploads in memory; no window, editor process or screenshot is used.
type pencodeIconDriver struct {
	render.Driver
	next        uint32
	pixels      map[render.TextureHandle][]byte
	descriptors map[render.TextureHandle]render.TextureDescriptor
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

func TestPencodeIconsRasterizeThemeColorsAndReuseTextures(t *testing.T) {
	theme, err := syntax.LoadTheme(fstest.MapFS{"resources/icons.json": &fstest.MapFile{Data: []byte(`{
  "type":"light", "colors":{
   "icon.foreground":"#2468ac80", "button.foreground":"#135790",
   "problemsErrorIcon.foreground":"#e81123", "problemsWarningIcon.foreground":"#bf8803",
   "problemsInfoIcon.foreground":"#1a85ff", "editorHint.foreground":"#6c6c6c",
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
	defer func() {
		for _, texture := range provider.textures {
			if texture != nil {
				_ = texture.Dispose()
			}
		}
	}()
	for key, source := range prepared.icons {
		texture, ok := provider.ResolveHTMLImage(source)
		if !ok || !texture.Valid() {
			t.Fatalf("decode %s", source)
		}
		if !texture.Mipmapped() || texture.MipLevels() < 3 {
			t.Fatalf("%s has no mip chain for its 4x raster", source)
		}
		if x, y := texture.RasterScale(); x != 4 || y != 4 {
			t.Fatalf("%s raster scale = %gx%g, want 4x4", source, x, y)
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
			cached, ok := provider.ResolveHTMLImage(source)
			if !ok || cached != texture {
				t.Fatal("cached icon texture changed")
			}
		}); allocations != 0 {
			t.Fatalf("cached %s allocated %g times", source, allocations)
		}
	}
	const preview = "peb://preview/resources/image.svg"
	provider.model = &workspaceModel{extensionImages: map[string][]byte{
		preview: []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="64" height="32"><rect width="64" height="32"/></svg>`),
	}}
	texture, ok := provider.ResolveHTMLImage(preview)
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
	other := &openFile{Path: "other.go", Name: "other.go", Content: "package demo\n"}
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
			texture, ok := provider.ResolveHTMLImage(model.themedIcon(key.source, key.foreground))
			if !ok {
				t.Fatalf("missing %v texture", key)
			}
			var found bool
			for _, access := range module.ContentQuery[scene.Sprite2D](model.fontResources) {
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
	css, err := os.ReadFile("../extensions/codex/resources/chat.css")
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
			texture, ok := provider.ResolveHTMLImage(model.themedIcon(icon.source, "icon.foreground"))
			if !ok {
				t.Fatalf("missing IDE texture for %s", icon.source)
			}
			if x, y := texture.RasterScale(); x != 4 || y != 4 || !texture.Mipmapped() {
				t.Fatalf("%s must use IDE 4x sampling and mipmaps", icon.source)
			}
			found := false
			for entity, access := range module.ContentQuery[scene.Sprite2D](model.fontResources) {
				parent, ok := module.ContentGet[scene.Parent](model.fontResources, entity)
				if !ok {
					continue
				}
				control, ok := module.ContentGet[htmlui.Control](model.fontResources, parent.Entity)
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
