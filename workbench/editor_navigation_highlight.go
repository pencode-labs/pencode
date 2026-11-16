package workbench

import (
	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	"github.com/onix-engine/onix/text/piecetree"
	engineui "github.com/onix-engine/onix/ui"
)

type pencodeNavigationPaint struct {
	file            *openFile
	content         piecetree.Snapshot
	line, caretLine int
	background      ecs.Entity
}

func (paint *pencodeNavigationPaint) mark(file *openFile, target, caret int) {
	buffer := file.snapshotBuffer()
	paint.file, paint.content = file, file.snapshot()
	paint.line, _ = buffer.Position(buffer.ByteOffset(target))
	paint.caretLine, _ = buffer.Position(buffer.ByteOffset(caret))
}

func (paint *pencodeNavigationPaint) sync(world *module.Content, m *workspaceModel) {
	if paint.file == nil {
		return
	}
	hide := func() { world.SetIfChanged(paint.background, scene.Visibility{}) }
	file := m.activeFile()
	if file != paint.file || !m.textEditorActive() || file.snapshot() != paint.content {
		paint.file, paint.content = nil, piecetree.Snapshot{}
		hide()
		return
	}
	text := world.Mut[scene.Text2D](m.editor.text)
	if m.screen != pencodeWorkspace || m.settingsOpen || m.workbenchTheme == nil || text == nil || text.LineHeight <= 0 || text.Text() != paint.content || m.editor.file != file {
		hide()
		return
	}
	field, ok := world.Get[engineui.TextField](m.editor.control)
	buffer := file.snapshotBuffer()
	line, _ := buffer.Position(buffer.ByteOffset(field.Caret))
	if !ok || line != paint.caretLine || field.Composition != "" {
		paint.file, paint.content = nil, piecetree.Snapshot{}
		hide()
		return
	}
	start := buffer.LineStart(paint.line)
	end := paint.content.Len()
	if paint.line+1 < buffer.Lines() {
		end = buffer.LineStart(paint.line+1) - 1
	}
	if end > start && paint.content.ByteAt(end-1) == '\r' {
		end--
	}
	_, top, _, _, startOK := scene.TextCaretRect2D(text, paint.content, buffer.RuneOffset(start))
	_, bottom, _, height, endOK := scene.TextCaretRect2D(text, paint.content, buffer.RuneOffset(end))
	transform, _ := world.Get[scene.UITransform](m.editor.text)
	size, _ := world.Get[scene.Size2D](m.editor.control)
	top, bottom = max(0, top+transform.Translation.Y.Value), min(size.Height, bottom+height+transform.Translation.Y.Value)
	if !startOK || !endOK || bottom <= top || size.Width <= 0 {
		hide()
		return
	}
	stack, _ := world.Get[scene.UIStackIndex](m.editor.text)
	if !world.IsAlive(paint.background) {
		bundle := ecs.NewBundle(8)
		bundle.Put(scene.UITransformFromXY(0, 0))
		bundle.Put(scene.UIGlobalTransform{})
		bundle.Put(scene.Parent{Entity: m.editor.control})
		bundle.Put(scene.AnchorTopLeft)
		bundle.Put(scene.Visibility{})
		bundle.Put(scene.NewSprite2D())
		bundle.Put(scene.Size2D{})
		bundle.Put(stack - 1)
		paint.background = world.Spawn(bundle)
	}
	// Keep the tint above the editor surface and behind the code glyphs.
	color := m.workbenchTheme.link
	color.A *= .10
	world.SetIfChanged(paint.background, scene.Parent{Entity: m.editor.control})
	world.SetIfChanged(paint.background, stack-1)
	world.SetIfChanged(paint.background, scene.Visibility{Visible: true})
	world.SetIfChanged(paint.background, scene.UITransformFromXY(0, top))
	world.SetIfChanged(paint.background, scene.Size2D{Width: size.Width, Height: bottom - top})
	sprite := scene.NewSprite2D()
	sprite.Width, sprite.Height = size.Width, bottom-top
	sprite.Shape = scene.RoundedRectShape2D(color, 0)
	world.SetIfChanged(paint.background, sprite)
}
