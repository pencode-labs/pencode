package workbench

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/font"
	"github.com/onix-engine/onix/html/css"
	"github.com/onix-engine/onix/html/dom"
	"github.com/onix-engine/onix/html/layout"
	"github.com/onix-engine/onix/html/style"
	"github.com/onix-engine/onix/scene"
	"github.com/onix-engine/onix/text"
	htmlui "github.com/onix-engine/onix/ui/html"
)

func TestOfficeViewerSavedColumnLayout(t *testing.T) {
	previewCSS, err := os.ReadFile("../extensions/office-viewer/resources/viewer.css")
	if err != nil {
		t.Fatal(err)
	}
	face, err := font.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = face.Close() })
	for _, viewport := range []float64{480, 1174, 1920} {
		root, err := dom.Parse(`<section class="office-viewer"><header class="office-toolbar"><nav class="sheet-tabs"><button id="selected" aria-pressed="true">Sheet1</button></nav></header><article class="office-content workbook"><table class="sheet" style="width:1100px"><thead><tr><th style="width:48px"></th><th style="width:64px">A</th><th id="header" style="width:736px">B</th><th id="next-header" style="width:252px">C</th></tr></thead><tbody><tr style="height:21px"><th>1</th><td>id</td><td id="cell" style="text-align:center">` + strings.Repeat("[color=#338516]{actorName}[/color]", 5) + `</td><td id="next-cell"></td></tr><tr style="height:21px"><th>2</th><td>next</td><td id="wrapped" style="white-space:pre-wrap;overflow-wrap:break-word">` + strings.Repeat("wrapped text ", 40) + `</td><td></td></tr></tbody></table></article></section>`)
		if err != nil {
			t.Fatal(err)
		}
		sheet, err := css.Parse(`:root { --vscode-editorGroup-border:#444; font-size:14px; }` + string(previewCSS))
		if err != nil {
			t.Fatal(err)
		}
		computed, err := style.Compute(root, sheet)
		if err != nil {
			t.Fatal(err)
		}
		box, err := layout.Build(root, computed, face, viewport, 800)
		if err != nil {
			t.Fatal(err)
		}
		boxes := make(map[string]*layout.Box)
		var visit func(*layout.Box)
		visit = func(box *layout.Box) {
			if id, _ := box.Node.Attr("id"); id != "" {
				boxes[id] = box
			}
			for _, child := range box.Children {
				visit(child)
			}
		}
		visit(box)
		cell, header, next := boxes["cell"], boxes["header"], boxes["next-cell"]
		if cell == nil || header == nil || next == nil || boxes["wrapped"] == nil || boxes["selected"] == nil {
			t.Fatal("missing spreadsheet layout")
		}
		if math.Abs(cell.BorderBox().Width-736) > .01 || cell.BorderBox().X != header.BorderBox().X || cell.BorderBox().Width != header.BorderBox().Width {
			t.Fatalf("viewport %g: header and cell disagree: header=%+v cell=%+v", viewport, header.BorderBox(), cell.BorderBox())
		}
		if math.Abs(next.BorderBox().X-cell.BorderBox().X-cell.BorderBox().Width) > .01 || next.BorderBox().X != boxes["next-header"].BorderBox().X {
			t.Fatalf("viewport %g: blank gap between columns: cell=%+v next=%+v", viewport, cell.BorderBox(), next.BorderBox())
		}
		if len(cell.TextFragments) == 0 || cell.TextFragments[len(cell.TextFragments)-1].Line != 0 || cell.BorderBox().Height > 24 {
			t.Fatal("unwrapped long text expanded the row")
		}
		if wrapped := boxes["wrapped"]; len(wrapped.TextFragments) == 0 || wrapped.TextFragments[len(wrapped.TextFragments)-1].Line == 0 {
			t.Fatal("explicit cell wrapping was lost")
		}
		if next.Style.OverflowX != style.Keyword("visible") || next.Style.OverflowY != style.Keyword("visible") || cell.Style.OverflowX != style.Keyword("hidden") {
			t.Fatal("empty cells retained clipping or populated cells lost it")
		}
		if got := boxes["selected"].Style.BorderRadius.TopLeft; got.Horizontal != style.LengthPx(0) || got.Vertical != style.LengthPx(0) {
			t.Fatalf("sheet highlight radius = %v, want 0px", got)
		}
	}
}

func TestOfficeViewerEmptyCellClipLifecycle(t *testing.T) {
	previewCSS, err := os.ReadFile("../extensions/office-viewer/resources/viewer.css")
	if err != nil {
		t.Fatal(err)
	}
	world := ecs.NewWorld()
	surface := htmlui.New(`<section class="office-viewer"><article class="office-content workbook"><table class="sheet" style="width:200px"><tr><td id="cell"></td></tr></table></article></section>`)
	surface.CSS, surface.Font = string(previewCSS), text.DefaultFont()
	surface.Width, surface.Height = 320, 200
	bundle := ecs.NewBundle(1)
	bundle.Put(surface)
	owner := world.Spawn(bundle)
	clips := func() int {
		t.Helper()
		if err := htmlui.Sync(world); err != nil {
			t.Fatal(err)
		}
		count := 0
		for range world.Query[scene.ClipRect2D]().All {
			count++
		}
		return count
	}
	emptyClips := clips()
	cell, ok := htmlui.NodeByID(world, owner, "cell")
	if !ok {
		t.Fatal("missing spreadsheet cell")
	}
	before, ok := cell.Bounds()
	if !ok {
		t.Fatal("missing spreadsheet geometry")
	}
	for _, value := range []string{strings.Repeat("long cell content ", 20), "", "next value", ""} {
		cell.SetText(value)
		want := emptyClips
		if value != "" {
			want++
		}
		if got := clips(); got != want {
			t.Fatalf("content length %d: clips = %d, want %d", len(value), got, want)
		}
		if after, ok := cell.Bounds(); !ok || after != before {
			t.Fatal("changing cell content changed its fixed geometry")
		}
	}
}
