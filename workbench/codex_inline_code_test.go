package workbench

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/onix-engine/onix/font"
	"github.com/onix-engine/onix/html/css"
	"github.com/onix-engine/onix/html/dom"
	"github.com/onix-engine/onix/html/layout"
	"github.com/onix-engine/onix/html/style"
)

func TestCodexInlineCodeSpacingAndWrapping(t *testing.T) {
	chatCSS, err := os.ReadFile("../extensions/codex/resources/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := css.Parse(string(chatCSS))
	if err != nil {
		t.Fatal(err)
	}
	face, err := font.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = face.Close() })
	for _, text := range []string{"conf_wi", "<strong>conf_wi</strong>", strings.Repeat("game-common/app/", 8)} {
		t.Run(text, func(t *testing.T) {
			root, err := dom.Parse(`<article class="codex-chat"><p id="paragraph" style="width:240px"><span id="before">当前使用</span><code id="code">` + text + `</code><span id="after">分支</span></p></article>`)
			if err != nil {
				t.Fatal(err)
			}
			computed, err := style.Compute(root, sheet)
			if err != nil {
				t.Fatal(err)
			}
			box, err := layout.Build(root, computed, face, 720, 600)
			if err != nil {
				t.Fatal(err)
			}
			var paragraph, code *layout.Box
			var before, after layout.TextFragment
			var visit func(*layout.Box)
			visit = func(box *layout.Box) {
				switch box.Node.AttrValue("id") {
				case "paragraph":
					paragraph = box
				case "code":
					code = box
				}
				for _, fragment := range box.TextFragments {
					switch fragment.Node.Parent.AttrValue("id") {
					case "before":
						before = fragment
					case "after":
						if after.Node == nil {
							after = fragment
						}
					}
				}
				for _, child := range box.Children {
					visit(child)
				}
			}
			visit(box)
			if paragraph == nil || code == nil || len(code.TextFragments) == 0 {
				t.Fatal("missing padded inline code layout")
			}
			bounds := code.BorderBox()
			if strings.HasPrefix(text, "game-common/") {
				if len(code.TextFragments) < 2 || code.TextFragments[len(code.TextFragments)-1].Line == 0 {
					t.Fatal("long inline code did not wrap")
				}
				if bounds.X-4 < paragraph.Content.X-.01 || bounds.X+bounds.Width+4 > paragraph.Content.X+paragraph.Content.Width+.01 {
					t.Fatalf("inline code margins overflow paragraph: code=%+v paragraph=%+v", bounds, paragraph.Content)
				}
				return
			}
			first := code.TextFragments[0]
			last := code.TextFragments[len(code.TextFragments)-1]
			for name, gap := range map[string]float64{
				"left margin":   bounds.X - before.Rect.X - before.Rect.Width,
				"right margin":  after.Rect.X - bounds.X - bounds.Width,
				"left padding":  first.Rect.X - bounds.X,
				"right padding": bounds.X + bounds.Width - last.Rect.X - last.Rect.Width,
			} {
				want := 4.0
				if strings.HasSuffix(name, "padding") {
					want = 6
				}
				if math.Abs(gap-want) > .01 {
					t.Errorf("%s = %g, want %g", name, gap, want)
				}
			}
			if math.Abs(first.Glyphs[0].Y-before.Glyphs[0].Y) > .01 || math.Abs(first.Glyphs[0].Y-after.Glyphs[0].Y) > .01 {
				t.Fatal("inline code no longer shares the surrounding text baseline")
			}
		})
	}
}
