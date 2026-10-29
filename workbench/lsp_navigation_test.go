package workbench

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	engineui "github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/lsp"
)

func TestLanguageNavigationHighlightsEncodedRange(t *testing.T) {
	app, model, provider, _ := languageInteractionFixture(t)
	world := model.fontResources
	const content = "package demo\r\n// 😀中文 target\r\n// next 行\r\n"
	model.updateActiveContent(content)
	model.syncLanguageDocuments()
	languageInteractionStep(t, app)
	for _, encoding := range []lsp.PositionEncoding{lsp.UTF8, lsp.UTF16, lsp.UTF32} {
		for _, target := range []string{"中文", "中文 target\r\n// next 行", ""} {
			t.Run(string(encoding)+"/"+target, func(t *testing.T) {
				provider.client.Encoding = encoding
				model.languages.panel.session = provider
				start := strings.Index(content, "中文")
				end := start + len(target)
				from, err := lsp.PositionAt(content, start, encoding)
				if err != nil {
					t.Fatal(err)
				}
				to, err := lsp.PositionAt(content, end, encoding)
				if err != nil {
					t.Fatal(err)
				}
				location := lsp.Location{URI: lsp.FileURI(filepath.Join(model.projectPath, model.activePath)), Range: lsp.Range{Start: from, End: to}}
				for _, override := range []bool{false, true} {
					var selection *lsp.Range
					if override {
						r := location.Range
						selection = &r
						location.Range = lsp.Range{}
					}
					if !model.navigateLanguageLocation(world, location, selection) {
						t.Fatal("navigation was not accepted")
					}
					// A second frame catches focus or layout overwriting the selection.
					for range 2 {
						languageInteractionStep(t, app)
					}
					field := world.Ref[engineui.TextField](model.editor.control)
					wantCaret, wantAnchor := utf8.RuneCountInString(content[:start]), utf8.RuneCountInString(content[:end])
					if !field.Focused || field.Caret != wantCaret || field.Anchor != wantAnchor {
						t.Fatalf("override=%v: selected %d:%d focused=%v, want %d:%d", override, field.Caret, field.Anchor, field.Focused, wantCaret, wantAnchor)
					}
					if model.activeContent() != content {
						t.Fatal("navigation changed document content")
					}
				}
			})
		}
	}
}
