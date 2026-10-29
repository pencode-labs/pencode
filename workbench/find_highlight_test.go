package workbench

import (
	"math"
	"testing"
	"testing/fstest"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
)

func TestFindHighlightBackgroundAndCurrentBorder(t *testing.T) {
	app, m, _, _ := languageInteractionFixture(t)
	world := m.fontResources
	const content = "\t中文🙂 22009 22009\n22009"
	m.updateActiveContent(content)
	m.setEditorValueAndCaret(world, content, 0)
	m.findOpen, m.findQuery = true, "22009"
	markPencodeRootDirtyByModel(world, m)
	languageInteractionStep(t, app)
	paint := &m.editor.findPaint
	assertPaint := func() ecs.Entity {
		t.Helper()
		backgrounds, borders := 0, 0
		var outline ecs.Entity
		textStack, _ := world.Get[scene.UIStackIndex](m.editor.text)
		for _, entity := range paint.quads {
			visible, _ := world.Get[scene.Visibility](entity)
			if !visible.Visible {
				continue
			}
			sprite, _ := world.Get[scene.Sprite2D](entity)
			stack, _ := world.Get[scene.UIStackIndex](entity)
			if sprite.Shape.BorderWidth > 0 {
				borders++
				outline = entity
				if sprite.Shape.Fill.A != 0 || sprite.Shape.Border != m.workbenchTheme.findColors[2] || stack <= textStack {
					t.Fatalf("current border must remain above native selection: %+v, stack=%d text=%d", sprite.Shape, stack, textStack)
				}
				text := world.Mut[scene.Text2D](m.editor.text)
				match := m.findMatches[m.findIndex]
				var widths [2]float32
				offsets := [2]int{match.Start, match.End}
				if !scene.MeasureTextLineOffsets2D(text, "\t中文🙂 22009 22009", offsets[:], widths[:]) {
					t.Fatal("missing match geometry")
				}
				transform, _ := world.Get[scene.UITransform](entity)
				if math.Abs(float64(transform.Translation.X.Value-paint.key.x-widths[0])) > .1 || math.Abs(float64(sprite.Width-(widths[1]-widths[0]))) > .1 {
					t.Fatal("border lost tab/Unicode match alignment")
				}
			} else {
				backgrounds++
				if stack >= textStack || sprite.Shape.Fill != m.workbenchTheme.findColors[0] && sprite.Shape.Fill != m.workbenchTheme.findColors[1] {
					t.Fatalf("match background must preserve code syntax colors: %+v", sprite.Shape)
				}
			}
		}
		if backgrounds != 3 || borders != 1 {
			t.Fatalf("backgrounds=%d borders=%d, want 3 and 1", backgrounds, borders)
		}
		return outline
	}
	outline := assertPaint()
	before, _ := world.Get[scene.UITransform](outline)
	m.advanceFind(world, 1)
	paint.sync(world, m)
	outline = assertPaint()
	after, _ := world.Get[scene.UITransform](outline)
	if after.Translation.X.Value <= before.Translation.X.Value {
		t.Fatal("next match did not move the border")
	}
	// A theme may change only the current match and border colors.
	theme := *m.workbenchTheme
	theme.findColors[1], theme.findColors[2] = theme.link, theme.bracket
	m.workbenchTheme = &theme
	paint.sync(world, m)
	assertPaint()
	world.Mut[scene.UITransform](m.editor.text).Translation.Y.Value -= 3
	paint.sync(world, m)
	scrolled, _ := world.Get[scene.UITransform](outline)
	if scrolled.Translation.Y.Value != after.Translation.Y.Value-3 {
		t.Fatal("border did not follow scrolling")
	}
	if allocations := testing.AllocsPerRun(100, func() { paint.sync(world, m) }); allocations != 0 {
		t.Fatal("warm highlight allocated", allocations)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		world.Mut[scene.UITransform](m.editor.text).Translation.Y.Value -= .001
		paint.sync(world, m)
	}); allocations != 0 {
		t.Fatal("scrolling highlight allocated", allocations)
	}
	m.closeFind(world)
	paint.sync(world, m)
	for _, entity := range paint.quads {
		if visible, _ := world.Get[scene.Visibility](entity); visible.Visible {
			t.Fatal("closed search left a highlight visible")
		}
	}
}

func TestFindHighlightThemeColors(t *testing.T) {
	for _, id := range syntax.BuiltinThemeIDs {
		t.Run(id, func(t *testing.T) {
			theme, err := syntax.BuiltinTheme(id)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := preparePencodeTheme(id, theme, "")
			if err != nil {
				t.Fatal(err)
			}
			for i, color := range prepared.findColors {
				if color.A == 0 {
					t.Fatalf("search decoration %d is invisible", i)
				}
			}
		})
	}
	t.Run("custom", func(t *testing.T) {
		theme, err := syntax.LoadTheme(fstest.MapFS{"find.json": &fstest.MapFile{Data: []byte(`{
  "colors": {
    "editor.findMatchHighlightBackground": "#11223344",
    "editor.findMatchBackground": "#55667788",
    "editor.findMatchBorder": "#aabbccdd"
  }
}`)}}, "find.json")
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := preparePencodeTheme("custom", theme, "")
		if err != nil {
			t.Fatal(err)
		}
		for i, key := range [...]string{"editor.findMatchHighlightBackground", "editor.findMatchBackground", "editor.findMatchBorder"} {
			if prepared.findColors[i] != pencodeSyntaxColor(theme.Color(key)) {
				t.Fatal("ignored custom search color", key)
			}
		}
	})
}
