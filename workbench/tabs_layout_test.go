package workbench

import (
	"math"
	"os"
	"strconv"
	"testing"

	"github.com/diiyw/onix/font"
	"github.com/diiyw/onix/internal/css"
	"github.com/diiyw/onix/internal/dom"
	"github.com/diiyw/onix/internal/layout"
	"github.com/diiyw/onix/internal/style"
	"github.com/diiyw/onix/syntax"
)

func TestPencodeLabelsCenterFontEdges(t *testing.T) {
	source, err := os.ReadFile("../resources/workbench/assets/ui/pencode.css")
	if err != nil {
		t.Fatal(err)
	}
	theme, err := syntax.BuiltinTheme("monokai")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparePencodeTheme("monokai", theme, string(source))
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := css.Parse(prepared.css)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"system"} {
		t.Run(name, func(t *testing.T) {
			face, err := font.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = face.Close() })
			bold := face
			for size := pencodeMinUIFontSize; size <= pencodeMaxUIFontSize; size++ {
				t.Run(strconv.Itoa(size), func(t *testing.T) {
					document, err := dom.Parse(`<html style="font-size:` + strconv.Itoa(size) + `px"><body><div class="pencode-root">
<button id="project" class="pencode-project-toggle"><img class="pencode-project-arrow"><span id="project-name" class="pencode-project-name">Pencode 项目</span></button>
<div id="tabs" class="pencode-tabs"><div class="pencode-tab active"><button class="pencode-tab-select"><img class="pencode-tab-icon"><span id="tab-name" class="pencode-tab-name">app_ecs.go</span></button><button class="pencode-tab-close"></button></div></div>
</div></body></html>`)
					if err != nil {
						t.Fatal(err)
					}
					styles, err := style.ComputeWithViewport(document, sheet, style.Viewport{Width: 800, Height: 100})
					if err != nil {
						t.Fatal(err)
					}
					root, err := layout.BuildWithFonts(document, styles, face, bold, 800, 100)
					if err != nil {
						t.Fatal(err)
					}
					boxes := make(map[string]*layout.Box)
					var visit func(*layout.Box)
					visit = func(box *layout.Box) {
						if id, ok := box.Node.Attr("id"); ok {
							boxes[id] = box
						}
						for _, child := range box.Children {
							visit(child)
						}
					}
					visit(root)
					for _, pair := range [][2]string{{"project-name", "project"}, {"tab-name", "tabs"}} {
						label, row := boxes[pair[0]], boxes[pair[1]]
						if label == nil || row == nil || len(label.TextFragments) == 0 {
							t.Fatalf("missing label or row: %v", pair)
						}
						bounds := row.BorderBox()
						wantCenter := bounds.Y + bounds.Height/2
						for _, fragment := range label.TextFragments {
							selected := face
							if fragment.Style.FontWeight.Number >= 600 {
								selected = bold
							}
							capHeight := float64(selected.CapHeight()) * fragment.Style.FontSize.LengthPx / float64(selected.Head.UnitsPerEm)
							if len(fragment.Glyphs) == 0 {
								t.Fatal("label has no glyphs")
							}
							if got := fragment.Glyphs[0].Y - capHeight/2; math.Abs(got-wantCenter) > .01 {
								t.Errorf("%s cap/baseline center = %v, want %v", pair[0], got, wantCenter)
							}
						}
					}
				})
			}
		})
	}
}

func TestPencodeTabLabelVerticalAlignment(t *testing.T) {
	_, session := newPencodeTestApp(t)
	model := session.active.model
	document, err := dom.Parse(`<html><body><div class="pencode-tabs"><div id="tab" class="pencode-tab active"><button class="pencode-tab-select"><img id="icon" class="pencode-tab-icon"><span id="name" class="pencode-tab-name">app_ecs.go</span></button><button id="close" class="pencode-tab-close"></button></div></div></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := css.Parse(model.workbenchTheme.css)
	if err != nil {
		t.Fatal(err)
	}
	styles, err := style.ComputeWithViewport(document, sheet, style.Viewport{Width: 800, Height: 100})
	if err != nil {
		t.Fatal(err)
	}
	root, err := layout.BuildWithFonts(document, styles, model.defaultFont.Face, model.defaultFont.Face, 800, 100)
	if err != nil {
		t.Fatal(err)
	}
	boxes := make(map[string]*layout.Box)
	var visit func(*layout.Box)
	visit = func(box *layout.Box) {
		if id, ok := box.Node.Attr("id"); ok {
			boxes[id] = box
		}
		for _, child := range box.Children {
			visit(child)
		}
	}
	visit(root)
	for _, id := range []string{"tab", "icon", "name", "close"} {
		if boxes[id] == nil {
			t.Fatalf("missing tab element %s", id)
		}
	}
	tab := boxes["tab"].BorderBox()
	wantCenter := tab.Y + tab.Height/2
	for _, id := range []string{"icon", "name", "close"} {
		box := boxes[id]
		bounds := box.BorderBox()
		if center := bounds.Y + bounds.Height/2; math.Abs(center-wantCenter) > 0.01 {
			t.Errorf("%s center = %v, want %v (bounds=%+v)", id, center, wantCenter, bounds)
		}
		for _, fragment := range box.TextFragments {
			if center := fragment.Rect.Y + fragment.Rect.Height/2; math.Abs(center-wantCenter) > 0.01 {
				t.Errorf("%s text center = %v, want %v (fragment=%+v)", id, center, wantCenter, fragment.Rect)
			}
		}
	}
}
