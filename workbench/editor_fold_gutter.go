package workbench

import (
	"math"

	"github.com/onix-engine/onix/graphics"
	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	"github.com/onix-engine/onix/ui/html"
)

const (
	foldIconSize    = 16
	foldGutterWidth = foldIconSize + 6 // Right padding before the gutter divider.
)

func gutterLayoutKey(text *scene.Text2D) pencodeGutterLayoutKey {
	key := pencodeGutterLayoutKey{value: text.Text(), font: text.Font, fontSize: text.FontSize, dpi: text.DPI, wrapWidth: text.WrapWidth, tabWidth: text.TabWidth, wrapMode: text.WrapMode}
	if text.LineLayout != nil {
		key.foldRevision = text.LineLayout.Revision
	}
	return key
}

// The folding lane sits between the numbers and the textarea. Keep collapsed
// controls visible after the pointer leaves the gutter, as in VS Code.
func (e *pencodeEditor) paintFoldIcons(world *module.Content, m *workspaceModel, bounds html.Rect, y, height float32, stack scene.UIStackIndex) {
	used := 0
	defer func() {
		for _, icon := range e.foldIcons[used:] {
			world.SetIfChanged(icon, scene.Visibility{})
		}
	}()
	file := m.activeFile()
	if file == nil || file.page.Paged || file.folds == nil || len(file.folds.ranges) == 0 {
		return
	}
	folds := file.folds
	inputState, _ := world.Resource[*input.State]()
	pointer := inputState.Pointer()
	zoom := max(float32(m.settings.Zoom)/100, .01)
	px, py := pointer.X/zoom, pointer.Y/zoom
	hover := bounds.X <= px && px < bounds.X+bounds.Width && bounds.Y <= py && py < bounds.Y+bounds.Height
	if !hover && len(folds.collapsed) == 0 {
		return
	}
	surface := world.Ref[html.Surface](m.surface)
	if surface == nil || surface.Images == nil || surface.Images.Provider == nil {
		return
	}
	var textures [2]*graphics.Texture
	for i, number := range e.sourceLines {
		if number == 0 {
			continue
		}
		if _, ok := foldAt(folds.ranges, number-1); !ok {
			continue
		}
		_, closed := foldAt(folds.collapsed, number-1)
		if hover && px >= bounds.X+bounds.Width-foldGutterWidth && int(math.Floor(float64((py-bounds.Y-y)/height))) == e.firstLine+i && !m.settingsOpen && !m.languageUI.contextOpen {
			inputState.SetCursor(input.CursorPointer)
		}
		if !hover && !closed {
			continue
		}
		index, name := 0, "icons/chevron-down.svg"
		if closed {
			index, name = 1, "icons/chevron-right.svg"
		}
		if textures[index] == nil {
			textures[index], _ = surface.Images.Provider.ResolveHTMLImage(m.themedIcon(name, "icon.foreground"), foldIconSize, foldIconSize)
		}
		if textures[index] == nil {
			continue
		}
		entity := editorSpriteEntity(world, &e.foldIcons, &used, e.gutter, stack)
		size := min(float32(foldIconSize), height)
		world.SetIfChanged(entity, scene.UITransformFromXY(bounds.Width-foldGutterWidth+(foldIconSize-size)/2, y+float32(e.firstLine+i)*height+(height-size)/2))
		sprite := scene.Sprite2DFromTexture(textures[index])
		sprite.Width, sprite.Height = size, size
		world.SetIfChanged(entity, sprite)
	}
}

func (m *workspaceModel) updateFoldPointer(world *module.Content, state *input.State) {
	if !state.PointerReleased() || !m.textEditorActive() || m.settingsOpen || m.quickOpen || m.folderSearch.open || m.tabContext.open || m.inputContext.open || m.closing.action != pencodeCloseNone || m.fileContext.open || m.fileContext.dialog != "" || m.languageUI.contextOpen || m.market != nil && m.market.open {
		return
	}
	file := m.activeFile()
	if file == nil || file != m.editor.file || file.folds == nil || file.page.Paged {
		return
	}
	node, ok := world.HTMLNode(m.surface, "editor-gutter")
	if !ok {
		return
	}
	b, ok := node.Bounds()
	text := world.Ref[scene.Text2D](m.editor.text)
	if !ok || text == nil || text.Text() != file.snapshot() || text.LineHeight <= 0 {
		return
	}
	p := state.Pointer()
	zoom := max(float32(m.settings.Zoom)/100, .01)
	transform, _ := world.Get[scene.UITransform](m.editor.text)
	row := func(x, y float32) int {
		x, y = x/zoom-b.X, y/zoom-b.Y
		if x < b.Width-foldGutterWidth || x >= b.Width || y < 0 || y >= b.Height {
			return -1
		}
		return int(math.Floor(float64((y - transform.Translation.Y.Value) / text.LineHeight)))
	}
	visual := row(p.X, p.Y)
	if visual < 0 || visual != row(p.PressX, p.PressY) {
		return
	}
	start, _, _, ok := scene.TextLineRange2D(text, visual)
	if !ok || start > 0 && text.Text().ByteAt(start-1) != '\n' {
		return
	}
	line, _ := file.snapshotBuffer().Position(start)
	m.toggleEditorFold(world, file, line)
}

func (m *workspaceModel) toggleEditorFold(world *module.Content, file *openFile, line int) {
	buffer := file.snapshotBuffer()
	field := world.Mut[engineui.TextField](m.editor.control)
	folds := file.folds
	if field == nil || field.Composition != "" || folds == nil || folds.source != file.snapshot() || !folds.toggle(line, buffer) {
		return
	}
	headerEnd := buffer.LineStart(line+1) - 1
	if headerEnd > 0 && file.snapshot().ByteAt(headerEnd-1) == '\r' {
		headerEnd--
	}
	if _, hidden := folds.hiddenAt(buffer.ByteOffset(field.Caret)); hidden {
		field.Caret = buffer.RuneOffset(headerEnd)
	}
	if _, hidden := folds.hiddenAt(buffer.ByteOffset(field.Anchor)); hidden {
		field.Anchor = buffer.RuneOffset(headerEnd)
	}
	m.editor.restoreFocus = true
	if m.app != nil && m.app.RequestRender != nil {
		m.app.RequestRender()
	}
}

// Vertical navigation skips collapsed bodies. The field and its selection
// continue to use the original document's rune offsets.
func (m *workspaceModel) moveAcrossFold(state *input.State) bool {
	key := input.KeyDown
	if state.KeyPressed(input.KeyUp) {
		key = input.KeyUp
	} else if !state.KeyPressed(key) {
		return false
	}
	file := m.editor.file
	if file == nil || file.folds == nil || len(file.folds.layout.Hidden) == 0 || m.fontResources == nil || state.KeyModifiers(key)&^input.ModifierShift != 0 {
		return false
	}
	field := m.fontResources.Mut[engineui.TextField](m.editor.control)
	if field == nil || field.Text() != file.folds.source {
		return false
	}
	buffer := file.snapshotBuffer()
	line, column := buffer.Position(buffer.ByteOffset(field.Caret))
	next := line + 1
	if key == input.KeyUp {
		next = line - 1
	}
	if next < 0 || next >= buffer.Lines() {
		return false
	}
	offset := buffer.LineStart(next)
	hidden, ok := file.folds.hiddenAt(offset)
	if !ok {
		return false
	}
	if key == input.KeyUp {
		next, _ = buffer.Position(hidden.Start - 1)
	} else if hidden.End <= buffer.Len() {
		next, _ = buffer.Position(hidden.End)
	} else {
		next = line
		column = buffer.Runes()
	}
	end := buffer.Len()
	if next+1 < buffer.Lines() {
		end = buffer.LineStart(next+1) - 1
	}
	if end > 0 && file.snapshot().ByteAt(end-1) == '\r' {
		end--
	}
	field.Caret = min(buffer.RuneOffset(buffer.LineStart(next))+column, buffer.RuneOffset(end))
	if !state.KeyHeld(input.KeyLeftShift) && !state.KeyHeld(input.KeyRightShift) {
		field.Anchor = field.Caret
	}
	return true
}
