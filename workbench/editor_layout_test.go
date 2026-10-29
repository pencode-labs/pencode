package workbench

import (
	"math"
	"strconv"
	"testing"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/scene"
)

func TestPencodeEditorCentersFontMetricsInRows(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model, world := session.active.model, session.active.model.fontResources
	const value = "Hg := sourceCommand(ctx)\nreturn value"
	model.updateActiveContent(value)
	model.setEditorValueAndCaret(world, value, 0)
	for _, family := range []string{model.defaultFontID, "Consolas", "Menlo"} {
		t.Run(family, func(t *testing.T) {
			if !model.selectFont(family, false) {
				t.Skip("font is not installed")
			}
			for _, zoom := range []int{70, 100, 150} {
				t.Run(strconv.Itoa(zoom), func(t *testing.T) {
					model.setZoom(world, zoom)
					markPencodeRootDirtyByModel(world, model)
					if _, err := app.Step(); err != nil {
						t.Fatal(err)
					}
					var segments [32]scene.TextRangeRect2D
					var baseline float32
					for _, entity := range []ecs.Entity{model.editor.text, model.editor.numbers} {
						text := world.Mut[scene.Text2D](entity)
						metrics := text.Font.GlyphCache(text.FontSize, text.DPI).Metrics()
						ascent, descent := float32(metrics.Ascent)/64, float32(metrics.Descent)/64
						count := scene.TextLineCount2D(text)
						if count == 0 {
							t.Fatal("missing editor rows")
						}
						for line := range count {
							start, end, row, ok := scene.TextLineRange2D(text, line)
							if !ok {
								t.Fatalf("entity %d line %d missing from %q", entity, line, text.Value)
							}
							_, underline, ok := scene.AppendTextUnderlineRects2D(segments[:0], text, line, start, end, 1)
							if !ok {
								t.Fatal("missing painted baseline")
							}
							paintedBaseline := underline.Y - 1
							top := paintedBaseline - ascent - row.Y
							bottom := row.Y + row.Height - paintedBaseline - descent
							if math.Abs(float64(top-bottom)) > .01 {
								t.Fatalf("entity %d line %d: font has unequal row padding: top=%v bottom=%v", entity, line, top, bottom)
							}
							if entity == model.editor.text && line == 0 {
								baseline = paintedBaseline
							}
							if entity == model.editor.numbers && line == 0 && paintedBaseline != baseline {
								t.Fatal("line numbers and code have different baselines")
							}
						}
					}
				})
			}
		})
	}
}
