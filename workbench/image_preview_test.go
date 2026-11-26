package workbench

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/syntax"
	"github.com/pencode-labs/pencode/protocol"
	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

func TestImagePreviewFormats(t *testing.T) {
	pixels := image.NewRGBA(image.Rect(0, 0, 8, 4))
	pixels.Set(1, 1, color.RGBA{R: 255, A: 255})
	for _, format := range []struct {
		encode func(*bytes.Buffer) error
		ext    string
	}{
		{func(b *bytes.Buffer) error { return png.Encode(b, pixels) }, ".png"},
		{func(b *bytes.Buffer) error { return jpeg.Encode(b, pixels, nil) }, ".jpg"},
		{func(b *bytes.Buffer) error { return jpeg.Encode(b, pixels, nil) }, ".jpeg"},
		{func(b *bytes.Buffer) error { return gif.Encode(b, pixels, nil) }, ".gif"},
		{func(b *bytes.Buffer) error { return bmp.Encode(b, pixels) }, ".bmp"},
		{func(b *bytes.Buffer) error { return tiff.Encode(b, pixels, nil) }, ".tiff"},
		{func(b *bytes.Buffer) error { return tiff.Encode(b, pixels, nil) }, ".tif"},
		{func(b *bytes.Buffer) error {
			// An 8 × 6 animated WebP; the viewer displays its first frame.
			data, err := base64.StdEncoding.DecodeString("UklGRoYAAABXRUJQVlA4WAoAAAASAAAABwAABQAAQU5JTQYAAAAAAAAAAABBTk1GKgAAAAEAAAAAAAMAAAMAAGQAAAJWUDhMEQAAAC8DwAAQDxDzH/MfjHmI6H84AEFOTUYoAAAAAAAAAAAABwAABQAAZAAAAFZQOEwPAAAALwdAAQAHENH//gciov8BAA==")
			b.Write(data)
			return err
		}, ".webp"},
		{func(b *bytes.Buffer) error {
			b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="8" height="4"><rect width="8" height="4" fill="red"/></svg>`)
			return nil
		}, ".svg"},
	} {
		t.Run(format.ext, func(t *testing.T) {
			var data bytes.Buffer
			if err := format.encode(&data); err != nil {
				t.Fatal(err)
			}
			preview, err := prepareImagePreview(data.Bytes(), format.ext)
			height := 4
			if format.ext == ".webp" {
				height = 6
			}
			if err != nil || preview.width != 8 || preview.height != height {
				t.Fatalf("preview=%+v err=%v", preview, err)
			}
			m, p := resourceTestModel(t)
			name := "图片" + strings.ToUpper(format.ext)
			resourceTestWrite(t, p, name, data.String())
			openProjectFileAndWait(t, m, p, name)
			if m.fileEditor(m.activeFile()) != &pencodeImageEditor || m.textEditorActive() {
				t.Fatal("image did not default to the built-in preview")
			}
			document := m.customDocument()
			defer m.closeCustomEditors()
			if err := awaitFileIO(t, m); err != nil {
				t.Fatal(err)
			}
			if document == nil || document.definition == nil || document.message != "" || len(document.images) != 1 {
				t.Fatalf("image did not load: %+v", document)
			}
			if allocs := testing.AllocsPerRun(100, func() { m.fileEditor(m.activeFile()); m.customDocument(); m.pollCustomEditors() }); allocs != 0 {
				t.Fatalf("idle image preview allocated %g times", allocs)
			}
		})
	}
}

func TestImagePreviewSVGEditSaveAndRelease(t *testing.T) {
	m, p := resourceTestModel(t)
	const initial = `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24"><rect width="24" height="24" fill="red"/></svg>`
	resourceTestWrite(t, p, "icon.svg", initial)
	openProjectFileAndWait(t, m, p, "icon.svg")
	file := m.activeFile()
	document := m.customDocument()
	defer m.closeCustomEditors()
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if !m.toggleEditorSplit() || !m.textEditorActive() {
		t.Fatal("SVG source and preview did not split")
	}
	updated := strings.Replace(initial, "red", "blue", 1)
	m.updateActiveContent(updated)
	m.pollCustomEditors()
	// Saving is independent of the outstanding preview render.
	m.saveActiveFile()
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if file.Dirty || document.savePending || !strings.Contains(string(singleImage(t, document.images)), "blue") || len(m.extensionImages) != 1 {
		t.Fatal("SVG edits were not saved and reconciled into the preview")
	}
	stored, err := os.ReadFile(filepath.Join(p.Path, "icon.svg"))
	if err != nil || string(stored) != updated {
		t.Fatal("saved SVG differs from the source", err)
	}
	if !m.selectEditor(pencodeTextEditor) || !m.textEditorActive() {
		t.Fatal("SVG did not switch to code")
	}
	m.pollCustomEditors()
	if len(m.extensionImages) != 0 {
		t.Fatal("switching to code retained preview bytes")
	}
	if !m.selectEditor(pencodeImageEditor.ID) || m.textEditorActive() {
		t.Fatal("SVG did not return to the preview")
	}
}

func singleImage(t *testing.T, images map[string][]byte) []byte {
	t.Helper()
	for _, data := range images {
		return data
	}
	t.Fatal("missing preview image")
	return nil
}

func TestImagePreviewRejectsStaleSVGAndClosedTabs(t *testing.T) {
	m, p := resourceTestModel(t)
	const initial = `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24"/>`
	resourceTestWrite(t, p, "icon.svg", initial)
	openProjectFileAndWait(t, m, p, "icon.svg")
	document := m.customDocument()
	defer m.closeCustomEditors()
	m.updateActiveContent(strings.Replace(initial, "24", "48", 1))
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if len(document.images) != 0 {
		t.Fatal("obsolete SVG result replaced the current edit")
	}
	m.pollCustomEditors()
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(singleImage(t, document.images)), `width="48"`) {
		t.Fatal("latest SVG did not render")
	}
	m.updateActiveContent(initial)
	m.pollCustomEditors()
	m.closeProjectTab(p, "icon.svg")
	m.pollCustomEditors()
	_ = awaitFileIO(t, m)
	if len(m.extensionImages) != 0 || len(m.customDocuments) != 0 {
		t.Fatal("closed tab retained or resurrected its preview")
	}
}

func TestImagePreviewInvalidData(t *testing.T) {
	for _, data := range []string{"broken", `<svg width="999999" height="999999"/>`} {
		if _, err := prepareImagePreview([]byte(data), ".svg"); err == nil {
			t.Fatal("invalid or oversized image was accepted")
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 8, 4))); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareImagePreview(data.Bytes()[:data.Len()-8], ".png"); err == nil {
		t.Fatal("truncated pixel data was accepted")
	}
}

func TestImagePreviewSVGUndoDuringLoadAndErrorRecovery(t *testing.T) {
	m, p := resourceTestModel(t)
	const initial = `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24"/>`
	resourceTestWrite(t, p, "icon.svg", initial)
	openProjectFileAndWait(t, m, p, "icon.svg")
	document := m.customDocument()
	defer m.closeCustomEditors()
	m.updateActiveContent(initial + " ")
	m.undoActiveContent(nil)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if document.definition == nil || document.message != "" {
		t.Fatal("undoing while the preview loaded left it pending")
	}
	m.updateActiveContent("<svg")
	m.pollCustomEditors()
	if err := awaitFileIO(t, m); err == nil || document.message == "" || len(document.images) != 0 || document.definition != nil {
		t.Fatal("invalid SVG did not clear the obsolete preview and show an error")
	}
	m.updateActiveContent(initial)
	m.pollCustomEditors()
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if document.definition == nil || document.message != "" || len(document.images) != 1 {
		t.Fatal("fixing the SVG did not recover the preview")
	}
}

func TestImagePreviewSVGHighlight(t *testing.T) {
	var document syntax.Document
	defer document.Close()
	theme, err := syntax.BuiltinTheme("dark-plus")
	if err != nil {
		t.Fatal(err)
	}
	var result pencodeHighlightResult
	processPencodeHighlight(&document, pencodeHighlightJob{path: "icon.SVG", source: `<svg width="24"><rect fill="red"/></svg>`, theme: theme}, &result)
	if result.err != nil || document.Language() != "xml" || len(result.spans) < 3 {
		t.Fatalf("SVG source was not highlighted: language=%q spans=%d err=%v", document.Language(), len(result.spans), result.err)
	}
}

func TestImagePreviewRetainedLayoutAndSwitching(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	p := model.activeProjectState()
	resourceTestWrite(t, p, "picture.svg", `<svg xmlns="http://www.w3.org/2000/svg" width="2048" height="1024"><rect width="2048" height="1024" fill="red"/></svg>`)
	openProjectFileAndWait(t, model, p, "picture.svg")
	model.customDocument()
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	step := func() {
		t.Helper()
		// File notifications can replace the document during a frame. Wait for
		// that preview too before asserting its retained layout.
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if err := awaitFileIO(t, model); err != nil {
				t.Fatal(err)
			}
			markPencodeRootDirtyByModel(model.fontResources, model)
			languageInteractionStep(t, app)
			if doc := model.customDocuments[model.activeFile()]; doc != nil && doc.inflight {
				continue
			}
			return
		}
		t.Fatal("image preview did not finish loading")
	}
	step()
	check := func() {
		t.Helper()
		body := editorContextBounds(t, model, "custom-editor")
		picture := editorContextBounds(t, model, "pencode-extension-image")
		if picture.X < body.X || picture.Y < body.Y || picture.X+picture.Width > body.X+body.Width+1 || picture.Y+picture.Height > body.Y+body.Height+1 {
			t.Fatalf("image exceeds preview bounds: image=%+v body=%+v", picture, body)
		}
	}
	check()
	editorContextClick(t, app, model, state, "editor-split")
	step()
	if !model.textEditorActive() {
		t.Fatal("split button did not expose the SVG source")
	}
	check()
	editorContextClick(t, app, model, state, "editor-text")
	step()
	if model.customDocuments[model.activeFile()] != nil || !model.textEditorActive() {
		t.Fatal("code button did not close the preview")
	}
	editorContextClick(t, app, model, state, "editor-image")
	step()
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	step()
	check()
	model.selectTheme("light-plus")
	step()
	check()
}

func TestImagePreviewExternalRasterReload(t *testing.T) {
	m, p := resourceTestModel(t)
	write := func(width int) {
		t.Helper()
		var data bytes.Buffer
		if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, width, 4))); err != nil {
			t.Fatal(err)
		}
		resourceTestWrite(t, p, "picture.png", data.String())
	}
	write(8)
	openProjectFileAndWait(t, m, p, "picture.png")
	m.customDocument()
	defer m.closeCustomEditors()
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	write(16)
	m.distributeFileChanges(p, []protocol.FileEvent{{Path: "picture.png", Op: protocol.FileWritten}}, false)
	m.reloadExternalFiles(p)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	document := m.customDocument()
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if document.image == nil || document.image.width != 16 || document.image.height != 4 || len(m.extensionImages) != 1 {
		t.Fatal("external image write did not refresh the preview")
	}
}

func TestImagePreviewCancelsPendingTextChoice(t *testing.T) {
	m, p := resourceTestModel(t)
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 8, 4))); err != nil {
		t.Fatal(err)
	}
	resourceTestWrite(t, p, "picture.png", data.String())
	openProjectFileAndWait(t, m, p, "picture.png")
	if !m.selectEditor(pencodeTextEditor) || !m.selectEditor(pencodeImageEditor.ID) {
		t.Fatal("could not change the open mode")
	}
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if m.activeFile().editorID != pencodeImageEditor.ID || m.textEditorActive() {
		t.Fatal("late text open replaced the selected image preview")
	}
}

func TestImagePreviewZoomGeometry(t *testing.T) {
	v := pencodeImageView{width: 2000, height: 1000, fit: true}
	v.layout(1048, 548)
	if v.scale != .5 {
		t.Fatalf("fit scale=%v", v.scale)
	}
	x, y := float32(600), float32(300)
	imageX := (x-v.viewportWidth/2-v.x)/v.scale + v.width/2
	imageY := (y-v.viewportHeight/2-v.y)/v.scale + v.height/2
	v.zoom(2, x, y)
	if gotX, gotY := (x-v.viewportWidth/2-v.x)/v.scale+v.width/2, (y-v.viewportHeight/2-v.y)/v.scale+v.height/2; gotX != imageX || gotY != imageY {
		t.Fatalf("zoom moved the pixel under the pointer: %v,%v -> %v,%v", imageX, imageY, gotX, gotY)
	}
	v.x, v.y = 1e6, -1e6
	v.layout(1048, 548)
	if v.x != (4000-1048)/2 || v.y != -(2000-548)/2 {
		t.Fatalf("pan escaped image bounds: %+v", v)
	}
	v.zoom(1e9, x, y)
	if v.scale != 32 {
		t.Fatal("maximum zoom was not clamped")
	}
	v.zoom(0, x, y)
	if v.scale != .01 || v.x != 0 || v.y != 0 {
		t.Fatal("minimum zoom was not clamped and centered")
	}
	v.action("image-fit")
	v.layout(548, 298)
	if v.scale != .25 || !v.fit {
		t.Fatal("fit did not follow viewport resizing")
	}
	v.action("image-actual")
	v.layout(1048, 548)
	if v.scale != 1 || v.fit || v.x != 0 || v.y != 0 {
		t.Fatal("1:1 did not survive viewport resizing")
	}
}

func TestImagePreviewZoomInteraction(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	p := model.activeProjectState()
	resourceTestWrite(t, p, "picture.svg", `<svg xmlns="http://www.w3.org/2000/svg" width="2048" height="1024"/>`)
	openProjectFileAndWait(t, model, p, "picture.svg")
	doc := model.customDocument()
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	markPencodeRootDirtyByModel(model.fontResources, model)
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	v := doc.image
	if v.scale <= 0 || v.scale >= 1 {
		t.Fatalf("large image was not fitted: %+v", v)
	}
	for _, zoom := range []int{100, 150} {
		model.settings.Zoom = zoom
		markPencodeRootDirtyByModel(model.fontResources, model)
		languageInteractionStep(t, app)
		editorContextClick(t, app, model, state, "pencode-extension-image-actual")
		picture := editorContextBounds(t, model, "pencode-extension-image")
		if v.scale != 1 || picture.Width != 2048 || picture.Height != 1024 {
			t.Fatalf("1:1 dimensions at UI zoom %d: %+v scale=%v", zoom, picture, v.scale)
		}
		bounds := editorContextBounds(t, model, "pencode-extension-image-container")
		uiScale := float64(zoom) / 100
		x, y := float64(bounds.X+bounds.Width/2)*uiScale, float64(bounds.Y+bounds.Height/2)*uiScale
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x, Y: y})
		state.Enqueue(input.Event{Kind: input.EventMouseWheel, WheelY: 1})
		languageInteractionStep(t, app)
		if math.Abs(float64(v.scale)-imageZoomStep) > .001 || state.Pointer().WheelY != 0 {
			t.Fatalf("wheel did not zoom and consume input at UI zoom %d: %+v", zoom, v)
		}
		state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
		languageInteractionStep(t, app)
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x + 30*uiScale, Y: y + 20*uiScale})
		languageInteractionStep(t, app)
		if math.Abs(float64(v.x)-30) > .01 || math.Abs(float64(v.y)-20) > .01 {
			t.Fatalf("drag did not pan at UI zoom %d: %+v", zoom, v)
		}
		state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x + 30*uiScale, Y: y + 20*uiScale})
		languageInteractionStep(t, app)
		editorContextClick(t, app, model, state, "pencode-extension-image-zoom-in")
		if math.Abs(float64(v.scale)-imageZoomStep*imageZoomStep) > .001 {
			t.Fatal("zoom-in button did not zoom")
		}
		editorContextClick(t, app, model, state, "pencode-extension-image-zoom-out")
		if math.Abs(float64(v.scale)-imageZoomStep) > .001 {
			t.Fatal("zoom-out button did not zoom")
		}
		editorContextClick(t, app, model, state, "pencode-extension-image-fit")
		if !v.fit || v.scale >= 1 || v.x != 0 || v.y != 0 {
			t.Fatal("fit button did not reset zoom and pan")
		}
	}
	before := v.scale
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: 0, Y: 0})
	state.Enqueue(input.Event{Kind: input.EventMouseWheel, WheelY: 1})
	languageInteractionStep(t, app)
	if v.scale != before {
		t.Fatal("wheel outside the preview changed its scale")
	}
	model.settingsOpen = true
	bounds := editorContextBounds(t, model, "pencode-extension-image-container")
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(bounds.X+bounds.Width/2) * 1.5, Y: float64(bounds.Y+bounds.Height/2) * 1.5})
	state.Enqueue(input.Event{Kind: input.EventMouseWheel, WheelY: 1})
	languageInteractionStep(t, app)
	if v.scale != before {
		t.Fatal("wheel over settings changed the image scale")
	}
	model.settingsOpen = false
	local := &input.State{}
	if n := testing.AllocsPerRun(100, func() {
		model.updateImagePointer(model.fontResources, local)
		model.syncImagePreview(model.fontResources)
	}); n != 0 {
		t.Fatalf("idle image zoom allocated %g times", n)
	}
}
