package workbench

import (
	"math"
	"sort"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
)

type pencodeFindPaint struct {
	pencodeDiagnosticPaint
	index int
	theme *pencodeTheme
}

func (paint *pencodeFindPaint) sync(world *module.Content, m *workspaceModel) {
	text := module.ContentMut[scene.Text2D](world, m.editor.text)
	if !m.findOpen || m.settingsOpen || !m.textEditorActive() || text == nil || m.workbenchTheme == nil || text.Value != m.activeContent() {
		paint.hide(world)
		return
	}
	m.refreshFindMatches()
	if len(m.findMatches) == 0 {
		paint.hide(world)
		return
	}
	transform, _ := module.ContentGet[scene.UITransform](world, m.editor.text)
	size, _ := module.ContentGet[scene.Size2D](world, m.editor.control)
	stack, _ := module.ContentGet[scene.UIStackIndex](world, m.editor.text)
	colors := m.workbenchTheme.findColors
	key := pencodeDiagnosticPaintKey{
		layout: pencodeGutterLayoutKey{text.Value, text.Font, text.FontSize, text.DPI, text.WrapWidth, text.TabWidth, text.WrapMode},
		file:   m.activeFile(), revision: m.findRevision, x: transform.Translation.X.Value, y: transform.Translation.Y.Value,
		width: size.Width, height: size.Height, spacing: text.LetterSpacing, lineHeight: text.LineHeight, ink: text.InkOffsetY, stack: stack, color: colors[0],
	}
	if key == paint.key && paint.index == m.findIndex && paint.theme == m.workbenchTheme {
		return
	}
	if text.LineHeight <= 0 {
		return
	}
	if !world.IsAlive(paint.root) {
		bundle := ecs.NewBundle(7)
		ecs.Put(&bundle, scene.UITransformFromXY(0, 0))
		ecs.Put(&bundle, scene.UIGlobalTransform{})
		ecs.Put(&bundle, scene.Parent{Entity: m.editor.control})
		ecs.Put(&bundle, scene.AnchorTopLeft)
		ecs.Put(&bundle, scene.Visibility{})
		ecs.Put(&bundle, scene.ClipRect2D{})
		ecs.Put(&bundle, scene.Size2D{})
		paint.root = world.Spawn(bundle)
	}
	module.ContentSetIfChanged(world, paint.root, scene.Parent{Entity: m.editor.control})
	module.ContentSetIfChanged(world, paint.root, scene.Visibility{Visible: true})
	module.ContentSetIfChanged(world, paint.root, scene.ClipRect2D{Width: size.Width, Height: size.Height})
	module.ContentSetIfChanged(world, paint.root, size)
	used := 0
	count := scene.TextLineCount2D(text)
	first := max(0, int(math.Floor(float64(-key.y/text.LineHeight))))
	for line := first; line < count; line++ {
		start, end, rect, _ := scene.TextLineRange2D(text, line)
		y := key.y + rect.Y
		if y >= size.Height {
			break
		}
		matchIndex := sort.Search(len(m.findMatches), func(i int) bool { return m.findMatches[i].End >= start })
		for i := matchIndex; i < len(m.findMatches) && m.findMatches[i].Start <= end; i++ {
			match := m.findMatches[i]
			left, right := max(start, match.Start), min(end, match.End)
			if left >= right && match.Start != match.End {
				continue
			}
			var bounds scene.TextRangeRect2D
			if left < right {
				paint.underlines, bounds, _ = scene.AppendTextUnderlineRects2D(paint.underlines[:0], text, line, left, right, 1)
			} else {
				// Zero-width regex matches still need a visible marker.
				x, _, _, _, ok := scene.TextCaretRect2D(text, text.Value, m.activeFile().textBuffer(text.Value).RuneOffset(left))
				if !ok {
					continue
				}
				bounds.X, bounds.Width = x, 2
			}
			fill := colors[0]
			if i == m.findIndex {
				fill = colors[1]
			}
			paint.quad(world, &used, stack-1, key.x+bounds.X, y, max(2, bounds.Width), rect.Height, 0, fill)
			if i == m.findIndex {
				// Keep the outline above native selection without tinting the code.
				entity := pencodeDiagnosticEntity(&paint.pencodeDiagnosticPaint, world, &paint.quads, &used, stack+1, scene.NewSprite2D())
				module.ContentSetIfChanged(world, entity, scene.UITransformFromXY(key.x+bounds.X, y))
				module.ContentSetIfChanged(world, entity, scene.Size2D{Width: max(2, bounds.Width), Height: rect.Height})
				sprite := scene.NewSprite2D()
				sprite.Width, sprite.Height = max(2, bounds.Width), rect.Height
				sprite.Shape = scene.OutlinedRoundedRectShape2D(graphics.ColorRGBA{}, colors[2], 0, 1)
				module.ContentSetIfChanged(world, entity, sprite)
			}
		}
	}
	for _, entity := range paint.quads[used:] {
		module.ContentSetIfChanged(world, entity, scene.Visibility{})
	}
	paint.key, paint.index, paint.theme = key, m.findIndex, m.workbenchTheme
}
