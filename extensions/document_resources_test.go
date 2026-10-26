package extensions

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pencode-labs/pencode/peb"
)

func TestDocumentImagesUseWorkspaceAndNetworkCache(t *testing.T) {
	backend, _ := actionFixture(t)
	remote, err := backend.LoadExtension(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	workspace, err := backend.OpenWorkspace(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	root = workspace.Path
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16"><rect width="16" height="16" fill="red"/></svg>`
	if err := os.WriteFile(filepath.Join(root, "local image.svg"), []byte(svg), 0644); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		fmt.Fprint(w, svg)
	}))
	defer server.Close()
	resources := viewResources{remote: remote, cache: make(map[string]viewResource), root: root, document: filepath.ToSlash(filepath.Join(root, "docs", "README.md"))}
	markup := `<p>Body</p><img src="../local%20image.svg"><img src="` + server.URL + `/badge?style=flat"><img src="../local%20image.svg"><a href="guide.md">Guide</a><a href="https://example.com">Site</a>`
	view := peb.EditorView{HTML: markup}
	assets, err := resources.prepare(t.Context(), &view, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 2 || requests != 1 {
		t.Fatalf("assets=%d requests=%d: %s", len(assets), requests, view.HTML)
	}
	for source, data := range assets {
		if !strings.HasPrefix(source, "peb://test/document/") || !strings.HasSuffix(source, ".svg") || string(data) != svg {
			t.Fatalf("invalid document asset %q", source)
		}
	}
	if !strings.Contains(view.HTML, `data-ref="@peb-link-0"`) || !strings.Contains(view.HTML, `data-ref="@peb-link-1"`) {
		t.Fatal("links have no unique bindings", view.HTML)
	}
	// Markdown resolves relative destinations explicitly, including resources/
	// paths that would otherwise name the extension's own bundled assets.
	resolved := peb.EditorView{HTML: `<img src="` + (&url.URL{Path: filepath.ToSlash(filepath.Join(root, "local image.svg"))}).String() + `">`}
	if assets, err := resources.prepare(t.Context(), &resolved, ""); err != nil || len(assets) != 1 {
		t.Fatalf("resolved document image: assets=%d err=%v", len(assets), err)
	}
	server.Close()
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	for _, html := range []string{markup, view.HTML} {
		cached := peb.EditorView{HTML: html}
		assets, err := resources.prepare(context.Background(), &cached, "")
		if err != nil || len(assets) != 2 {
			t.Fatalf("cache needs I/O: assets=%d err=%v", len(assets), err)
		}
	}
}

func TestBrokenDocumentImagePreservesPreview(t *testing.T) {
	backend, _ := actionFixture(t)
	remote, err := backend.LoadExtension(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if _, err := backend.OpenWorkspace(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private.svg")
	if err := os.WriteFile(outside, []byte(`<svg width="16" height="16"/>`), 0644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	defer server.Close()
	resources := viewResources{remote: remote, cache: make(map[string]viewResource), root: root, document: filepath.ToSlash(filepath.Join(root, "README.md"))}
	for _, source := range []string{"missing.png", "bad%escape", "javascript:alert(1)", (&url.URL{Scheme: "file", Path: outside}).String(), server.URL + "/missing.png"} {
		view := peb.EditorView{HTML: `<p>Still visible</p><img alt="Missing" src="` + source + `">`}
		assets, err := resources.prepare(t.Context(), &view, "")
		if err != nil || len(assets) != 0 || strings.Contains(view.HTML, "src=") || !strings.Contains(view.HTML, "Still visible") || !strings.Contains(view.HTML, `alt="Missing"`) {
			t.Fatalf("%q broke preview: %s, %v", source, view.HTML, err)
		}
	}
}
