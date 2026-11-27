package workbench

import (
	"sort"

	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/graphics"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	"github.com/pencode-labs/pencode/textsearch"
)

type pencodeFindPaint struct {
	theme *pencodeTheme
	pencodeDiagnosticPaint
	index       int
	viewportTop float32
}

func (paint *pencodeFindPaint) sync(world *module.Content, m *workspaceModel) {
	if !m.findOpen || !m.textEditorActive() || m.renderedFind() != nil {
		paint.hide(world)
		return
	}
	m.refreshFindMatches()
	paint.syncEditor(world, m, &m.editor, m.activeFile(), m.findMatches, m.findIndex, nil)
}

func (paint *pencodeFindPaint) syncEditor(world *module.Content, m *workspaceModel, editor *pencodeEditor, file *openFile, matches []textsearch.Match, index int, scroll *scene.UIScroll) {
	text := world.Mut[scene.Text2D](editor.text)
	if len(matches) == 0 || m.settingsOpen || m.workbenchTheme == nil || text == nil || text.Text() != file.snapshot() {
		paint.hide(world)
		return
	}
	transform, _ := world.Get[scene.UITransform](editor.text)
	size, _ := world.Get[scene.Size2D](editor.control)
	top, height := float32(0), size.Height
	if scroll != nil {
		top, height = -scroll.Position[1], scroll.ViewportSize[1]
	}
	stack, _ := world.Get[scene.UIStackIndex](editor.text)
	colors := m.workbenchTheme.findColors
	key := pencodeDiagnosticPaintKey{
		layout: gutterLayoutKey(text),
		file:   file, revision: m.findRevision, x: transform.Translation.X.Value, y: transform.Translation.Y.Value,
		width: size.Width, height: height, spacing: text.LetterSpacing, lineHeight: text.LineHeight, ink: text.InkOffsetY, stack: stack, color: colors[0],
	}
	parent, _ := world.Get[scene.Parent](paint.root)
	if key == paint.key && paint.index == index && paint.viewportTop == top && paint.theme == m.workbenchTheme && parent.Entity == editor.control {
		return
	}
	if text.LineHeight <= 0 {
		return
	}
	if !world.IsAlive(paint.root) {
		paint.dispose(world)
		bundle := ecs.NewBundle(7)
		bundle.Put(scene.UITransformFromXY(0, 0))
		bundle.Put(scene.UIGlobalTransform{})
		bundle.Put(scene.Parent{Entity: editor.control})
		bundle.Put(scene.AnchorTopLeft)
		bundle.Put(scene.Visibility{})
		bundle.Put(scene.ClipRect2D{})
		bundle.Put(scene.Size2D{})
		paint.root = world.Spawn(bundle)
	} else if parent.Entity != editor.control {
		// HTML can remount the textarea while the find bar remains open.
		if err := world.Insert(paint.root, scene.Parent{Entity: editor.control}); err != nil {
			return
		}
	}
	world.SetIfChanged(paint.root, scene.Visibility{Visible: true})
	world.SetIfChanged(paint.root, scene.ClipRect2D{Width: size.Width, Height: size.Height})
	world.SetIfChanged(paint.root, size)
	used := 0
	count := scene.TextLineCount2D(text)
	first := sort.Search(count, func(line int) bool {
		_, _, rect, _ := scene.TextLineRange2D(text, line)
		return key.y+rect.Y+rect.Height > top
	})
	for line := first; line < count; line++ {
		start, end, rect, _ := scene.TextLineRange2D(text, line)
		y := key.y + rect.Y
		if y >= top+height {
			break
		}
		matchIndex := sort.Search(len(matches), func(i int) bool { return matches[i].End >= start })
		for i := matchIndex; i < len(matches) && matches[i].Start <= end; i++ {
			match := matches[i]
			left, right := max(start, match.Start), min(end, match.End)
			if left >= right && match.Start != match.End {
				continue
			}
			var bounds scene.TextRangeRect2D
			if left < right {
				paint.underlines, bounds, _ = scene.AppendTextUnderlineRects2D(paint.underlines[:0], text, line, left, right, 1)
			} else {
				// Zero-width regex matches still need a visible marker.
				x, _, _, _, ok := scene.TextCaretRect2D(text, text.Text(), file.snapshotBuffer().RuneOffset(left))
				if !ok {
					continue
				}
				bounds.X, bounds.Width = x, 2
			}
			fill := colors[0]
			if i == index {
				fill = colors[1]
			}
			paint.quad(world, &used, stack-1, key.x+bounds.X, y, max(2, bounds.Width), rect.Height, 0, fill)
			if i == index {
				// Keep the outline above native selection without tinting the code.
				entity := pencodeDiagnosticEntity(&paint.pencodeDiagnosticPaint, world, &paint.quads, &used, stack+1, scene.NewSprite2D())
				world.SetIfChanged(entity, scene.UITransformFromXY(key.x+bounds.X, y))
				world.SetIfChanged(entity, scene.Size2D{Width: max(2, bounds.Width), Height: rect.Height})
				sprite := scene.NewSprite2D()
				sprite.Width, sprite.Height = max(2, bounds.Width), rect.Height
				sprite.Shape = scene.OutlinedRoundedRectShape2D(graphics.ColorRGBA{}, colors[2], 0, 1)
				world.SetIfChanged(entity, sprite)
			}
		}
	}
	for _, entity := range paint.quads[used:] {
		world.SetIfChanged(entity, scene.Visibility{})
	}
	paint.key, paint.index, paint.theme, paint.viewportTop = key, index, m.workbenchTheme, top
}
