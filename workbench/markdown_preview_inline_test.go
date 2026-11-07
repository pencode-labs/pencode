package workbench

import (
	"os"
	"strings"
	"testing"

	"github.com/onix-engine/onix/font"
	"github.com/onix-engine/onix/html/css"
	"github.com/onix-engine/onix/html/dom"
	"github.com/onix-engine/onix/html/layout"
	"github.com/onix-engine/onix/html/style"
	"github.com/onix-engine/onix/syntax"
)

func TestMarkdownPreviewInlineCodePaddingAndWrapping(t *testing.T) {
	previewCSS, err := os.ReadFile("../extensions/markdown-preview/resources/editor.css")
	if err != nil {
		t.Fatal(err)
	}
	workbenchCSS, err := os.ReadFile("../resources/workbench/assets/ui/pencode.css")
	if err != nil {
		t.Fatal(err)
	}
	face, err := font.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = face.Close() })
	for _, id := range []string{"dark-plus", "light-plus", "monokai"} {
		theme, err := syntax.BuiltinTheme(id)
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := preparePencodeTheme(id, theme, string(workbenchCSS))
		if err != nil {
			t.Fatal(err)
		}
		for name, text := range map[string]string{"short": "return", "path": strings.Repeat("game-common/app/", 8)} {
			t.Run(id+"/"+name, func(t *testing.T) {
				root, err := dom.Parse(`<article class="markdown-preview"><p style="width:240px"><code id="code"><span class="tok-keyword">` + text + `</span></code> after</p></article>`)
				if err != nil {
					t.Fatal(err)
				}
				sheet, err := css.Parse(prepared.resolveCSS(string(previewCSS)))
				if err != nil {
					t.Fatal(err)
				}
				computed, err := style.Compute(root, sheet)
				if err != nil {
					t.Fatal(err)
				}
				box, err := layout.Build(root, computed, face, 800, 600)
				if err != nil {
					t.Fatal(err)
				}
				var code *layout.Box
				var visit func(*layout.Box)
				visit = func(box *layout.Box) {
					if id, _ := box.Node.Attr("id"); id == "code" {
						code = box
					}
					for _, child := range box.Children {
						visit(child)
					}
				}
				visit(box)
				if code == nil || len(code.TextFragments) == 0 {
					t.Fatal("missing inline code layout")
				}
				if code.Padding != (layout.Edges{Top: 1, Right: 6, Bottom: 1, Left: 6}) {
					t.Fatalf("code padding = %+v", code.Padding)
				}
				if code.BorderBox().Width > 240.01 {
					t.Fatalf("padded code overflows its paragraph: %+v", code.BorderBox())
				}
				if name == "path" && code.TextFragments[len(code.TextFragments)-1].Line == 0 {
					t.Fatal("long code path did not wrap")
				}
				if name == "short" {
					text := code.TextFragments[0]
					after := code.Parent.TextFragments[len(code.Parent.TextFragments)-1]
					if text.Glyphs[0].Y != after.Glyphs[0].Y {
						t.Fatalf("padded code lost its text baseline: code=%g after=%g", text.Glyphs[0].Y, after.Glyphs[0].Y)
					}
				}
				background := style.Color(theme.Color("editor.background"))
				color := code.Style.BackgroundColor.Color
				difference := func(a, b uint8) int { return max(int(a), int(b)) - min(int(a), int(b)) }
				if color.A != 255 || difference(color.R, background.R)+difference(color.G, background.G)+difference(color.B, background.B) < 36 {
					t.Fatalf("code background is too close to editor background: %v vs %v", color, background)
				}
			})
		}
	}
}
