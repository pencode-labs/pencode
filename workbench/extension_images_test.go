package workbench

import (
	"fmt"
	"testing"

	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestExtensionImagesFollowSurfaceUpdatesAndClose(t *testing.T) {
	_, session := newPencodeTestApp(t)
	model := session.active.model
	results := make(chan extensions.ActionResult, 1)
	process := &extensions.ActionSession{Results: results}
	if model.actions.sessions == nil {
		model.actions.sessions = make(map[string]*extensions.ActionSession)
	}
	model.actions.sessions["images"] = process
	t.Cleanup(func() { delete(model.actions.sessions, "images") })
	const source = "peb://images/resources/shared.svg"
	assets := map[string][]byte{source: []byte("validated image")}
	send := func(ui peb.UI, assets map[string][]byte) {
		t.Helper()
		results <- extensions.ActionResult{UI: &ui, Assets: assets}
		model.pollActions()
	}
	send(peb.UI{ID: "one", Kind: peb.Panel, View: &peb.EditorView{HTML: `<p>First</p>`}}, assets)
	send(peb.UI{ID: "two", Kind: peb.Notification, View: &peb.EditorView{HTML: `<p>Second</p>`}}, assets)
	if model.extensionImages[source].references != 2 {
		t.Fatal("two surfaces did not share the asset")
	}
	send(peb.UI{ID: "one", Kind: peb.Panel, Title: "Metadata only"}, nil)
	if model.extensionImages[source].references != 2 {
		t.Fatal("metadata-only update released the retained view's asset")
	}
	send(peb.UI{ID: "one", Kind: peb.Panel, View: &peb.EditorView{HTML: `<button id="editor">Invalid</button>`}}, map[string][]byte{"rejected": []byte("rejected")})
	if len(model.extensionImages) != 1 || model.extensionImages[source].references != 2 {
		t.Fatal("rejected view changed image ownership")
	}
	send(peb.UI{ID: "one", Kind: peb.Panel, View: &peb.EditorView{HTML: `<p>No image</p>`}}, nil)
	if model.extensionImages[source].references != 1 {
		t.Fatal("view replacement retained an unused image")
	}
	send(peb.UI{ID: "two", Kind: peb.Notification, Close: true}, nil)
	if len(model.extensionImages) != 0 {
		t.Fatal("closing the last surface retained the asset")
	}
	send(peb.UI{ID: "one", Kind: peb.Panel, View: &peb.EditorView{HTML: `<p>Image again</p>`}}, assets)
	model.clearExtensionUI("images")
	if len(model.extensionImages) != 0 {
		t.Fatal("unloading surfaces retained their images")
	}
}

func TestExtensionImagesSharedOwnershipAndReplacement(t *testing.T) {
	var model workspaceModel
	shared := []byte("shared")
	first := model.replaceExtensionImages(nil, map[string][]byte{"shared": shared, "old": []byte("old")})
	second := model.replaceExtensionImages(nil, map[string][]byte{"shared": shared})
	first = model.replaceExtensionImages(first, map[string][]byte{"new": []byte("new")})
	if len(model.extensionImages) != 2 || model.extensionImages["shared"].references != 1 || model.extensionImages["old"].data != nil {
		t.Fatal("replacement lost a shared asset or retained a removed asset", model.extensionImages)
	}
	first = model.replaceExtensionImages(first, nil)
	if len(model.extensionImages) != 1 || string(model.extensionImages["shared"].data) != "shared" || first != nil {
		t.Fatal("closing one view released another view's asset")
	}
	model.replaceExtensionImages(second, nil)
	if len(model.extensionImages) != 0 {
		t.Fatal("last owner retained image bytes")
	}
}

func TestExtensionImagesRepeatedPreviewClose(t *testing.T) {
	var model workspaceModel
	for i := range 20 {
		document := &pencodeCustomDocument{model: &model}
		document.images = model.replaceExtensionImages(nil, map[string][]byte{fmt.Sprintf("preview-%d", i): make([]byte, 1<<20)})
		document.close()
		if len(model.extensionImages) != 0 || document.images != nil {
			t.Fatalf("preview %d retained its image", i)
		}
	}
}

func TestExtensionImagesWarmUpdatesAllocateNothing(t *testing.T) {
	var model workspaceModel
	a := map[string][]byte{"shared": []byte("image"), "a": []byte("a")}
	b := map[string][]byte{"shared": a["shared"], "b": []byte("b")}
	current := model.replaceExtensionImages(nil, a)
	if n := testing.AllocsPerRun(100, func() {
		current = model.replaceExtensionImages(current, b)
		current = model.replaceExtensionImages(current, a)
	}); n != 0 {
		t.Fatalf("warm image ownership update allocated %g", n)
	}
	if model.extensionImages["shared"].references != 1 {
		t.Fatal("unchanged images gained references during updates")
	}
}

func TestExtensionImagesReleaseTextureAfterReconciliation(t *testing.T) {
	_, session := newPencodeTestApp(t)
	provider := session.active.icons
	model := provider.model
	const source = "peb://preview/document/test.svg"
	assets := model.replaceExtensionImages(nil, map[string][]byte{source: []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16"><rect width="16" height="16"/></svg>`)})
	texture, ok := provider.ResolveHTMLImage(source, 0, 0)
	if !ok {
		t.Fatal("preview texture was not decoded")
	}
	node := provider.content.SpawnNode(ui.NodeOptions{}, scene.Sprite2D{Texture: texture})
	provider.trimImages()
	model.replaceExtensionImages(assets, nil)
	provider.trimImages()
	if !texture.Valid() {
		t.Fatal("released a texture before its sprite was removed")
	}
	provider.content.Despawn(node)
	provider.trimImages()
	if texture.Valid() || provider.textures[source] != nil {
		t.Fatal("unreferenced preview texture survived reconciliation")
	}
	if n := testing.AllocsPerRun(100, provider.trimImages); n != 0 {
		t.Fatalf("idle cache maintenance allocated %g", n)
	}
}
