package workbench

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		markPencodeRootDirtyByModel(model.fontResources, model)
		languageInteractionStep(t, app)
		if err := awaitFileIO(t, model); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(model.fontResources, model)
		languageInteractionStep(t, app)
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
	if !strings.Contains(document.view.HTML, "16 × 4") || len(m.extensionImages) != 1 {
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
