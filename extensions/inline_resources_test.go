package extensions

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/pencode-labs/pencode/peb"
)

func TestInlineImagesBecomeCachedAssets(t *testing.T) {
	backend, _ := actionFixture(t)
	remote, err := backend.LoadExtension(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		t.Fatal(err)
	}
	source := "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
	markup := `<img src="` + source + `"><img src="` + source + `">`
	r := viewResources{remote: remote, cache: make(map[string]viewResource)}
	view := peb.EditorView{HTML: markup}
	assets, err := r.prepare(t.Context(), &view, "composer")
	if err != nil || len(assets) != 1 || len(r.cache) != 1 || r.bytes != b.Len() || strings.Contains(view.HTML, "data:") {
		t.Fatal("inline image was not cached and rewritten", err)
	}
	for uri, data := range assets {
		if !strings.HasPrefix(uri, "peb://test/inline/") || !bytes.Equal(data, b.Bytes()) {
			t.Fatal("incorrect inline asset", uri)
		}
	}
	// Both fresh publications and retained, rewritten markup must reuse the cache.
	for _, markup := range []string{markup, view.HTML} {
		cached := peb.EditorView{HTML: markup}
		if assets, err := r.prepare(t.Context(), &cached, "composer"); err != nil || len(assets) != 1 || r.bytes != b.Len() {
			t.Fatal("repeated image was not reused", err)
		}
	}
	for _, src := range []string{"data:image/png;base64,invalid", "data:image/svg+xml;base64,PHN2Zy8+", "data:text/plain;base64,dGV4dA==", "peb://test/inline/unknown.png"} {
		view := peb.EditorView{HTML: `<img src="` + src + `">`}
		if _, err := r.prepare(t.Context(), &view, "composer"); err == nil {
			t.Fatal("invalid inline image accepted", src)
		}
	}
}
