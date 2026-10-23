package extensions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/diiyw/onix/projects/pencode/peb"
)

func TestFontResourcesValidateAndShareFaces(t *testing.T) {
	directory := t.TempDir()
	resources := filepath.Join(directory, "resources")
	if err := os.Mkdir(resources, 0755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../../testdata/fonts/Noto_Sans_SC/static/NotoSansSC-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resources, "font.ttf"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resources, "bad.ttf"), []byte("broken"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resources, "large.ttf"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(filepath.Join(resources, "large.ttf"), maxFontBytes+1); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	f := peb.Font{ID: "multi", Label: "Multilingual", Path: "resources/font.ttf", BoldPath: "resources/font.ttf"}
	bundle, err := loadBundle(root.FS(), "fonts", peb.Contributions{Fonts: []peb.Font{f}})
	if err != nil {
		t.Fatal(err)
	}
	loaded := bundle.Fonts[0]
	defer loaded.Regular.Close()
	if loaded.ID != "extensions/fonts/multi" || loaded.Bold != loaded.Regular {
		t.Fatal("font ID or shared face was lost")
	}
	for _, r := range "Hello café 简体繁體中文 日本語ひらがなカタカナ" {
		if loaded.Regular.GlyphIndex(r) == 0 {
			t.Errorf("missing glyph %q", r)
		}
	}
	for name, change := range map[string]func(*peb.Font){
		"escape":             func(f *peb.Font) { f.Path = "resources/../../font.ttf" },
		"outside resources":  func(f *peb.Font) { f.Path = "font.ttf" },
		"unsupported format": func(f *peb.Font) { f.Path = "resources/font.woff2" },
		"corrupt face":       func(f *peb.Font) { f.Path = "resources/bad.ttf" },
		"corrupt bold":       func(f *peb.Font) { f.BoldPath = "resources/bad.ttf" },
		"oversized face":     func(f *peb.Font) { f.Path = "resources/large.ttf" },
		"empty label":        func(f *peb.Font) { f.Label = "" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := f
			change(&invalid)
			if _, err := loadBundle(root.FS(), "fonts", peb.Contributions{Fonts: []peb.Font{invalid}}); err == nil {
				t.Fatal("accepted invalid font")
			}
		})
	}
	if _, err := loadBundle(root.FS(), "fonts", peb.Contributions{Fonts: []peb.Font{f, f}}); err == nil {
		t.Fatal("accepted duplicate font ID")
	}
}
