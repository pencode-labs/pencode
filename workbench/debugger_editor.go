package workbench

import (
	"math"
	"path"
	"slices"
	"strings"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	"github.com/pencode-labs/pencode/debugger"
)

type pencodeDebugPaint struct {
	dots      []ecs.Entity
	execution ecs.Entity
	file      *openFile
	project   *pencodeProject
	filename  string
}

func (m *workspaceModel) updateDebugPointer(world *module.Content, state *input.State) bool {
	if !state.PointerPressed() || !m.textEditorActive() || m.settingsOpen || m.quickOpen || m.findOpen || m.closing.action != pencodeCloseNone || m.fileContext.open || m.fileContext.dialog != "" || m.market != nil && m.market.open {
		return false
	}
	zoom := max(float32(m.settings.Zoom)/100, .01)
	pointer := state.Pointer()
	x, y := pointer.PressX/zoom, pointer.PressY/zoom
	node, ok := world.HTMLNode(m.surface, "editor-gutter")
	if !ok {
		return false
	}
	bounds, ok := node.Bounds()
	if !ok || x < float32(bounds.X) || x >= float32(bounds.X)+18 || y < float32(bounds.Y) || y >= float32(bounds.Y+bounds.Height) {
		return false
	}
	text := module.ContentMut[scene.Text2D](world, m.editor.text)
	if text == nil || text.LineHeight <= 0 {
		return false
	}
	transform, _ := module.ContentGet[scene.UITransform](world, m.editor.text)
	visual := int(math.Floor(float64((y - float32(bounds.Y) - transform.Translation.Y.Value) / text.LineHeight)))
	if visual < 0 || visual >= len(m.editor.sourceLines) {
		return false
	}
	for visual > 0 && m.editor.sourceLines[visual] == 0 {
		visual--
	}
	line := m.editor.sourceLines[visual]
	if line <= 0 {
		return false
	}
	m.toggleDebugBreakpoint(world, line)
	return true
}

func (paint *pencodeDebugPaint) sync(world *module.Content, m *workspaceModel) {
	file, project := m.activeFile(), m.activeProjectState()
	visible := m.screen == pencodeWorkspace && m.textEditorActive() && !m.settingsOpen && file != nil && project != nil
	used := 0
	defer func() {
		for _, entity := range paint.dots[used:] {
			module.ContentSetIfChanged(world, entity, scene.Visibility{})
		}
	}()
	if paint.execution != 0 {
		module.ContentSetIfChanged(world, paint.execution, scene.Visibility{})
	}
	if !visible || m.workbenchTheme == nil {
		return
	}
	if paint.file != file || paint.project != project {
		paint.file, paint.project = file, project
		paint.filename = path.Join(project.Path, file.Path)
	}
	text := module.ContentMut[scene.Text2D](world, m.editor.text)
	if text == nil || text.LineHeight <= 0 || text.Value != file.Content || !world.IsAlive(m.editor.gutter) {
		return
	}
	transform, _ := module.ContentGet[scene.UITransform](world, m.editor.text)
	stack, _ := module.ContentGet[scene.UIStackIndex](world, m.editor.text)
	bps := project.debugBreakpoints[file.Path]
	color := m.workbenchTheme.diagnosticColors[1]
	executionLine := 0
	if m.debug.project == project && m.debug.view.State == debugger.Stopped {
		for _, frame := range m.debug.view.Frames {
			if frame.ID == m.debug.view.FrameID && path.Clean(strings.ReplaceAll(frame.Source.Path, "\\", "/")) == paint.filename {
				executionLine = frame.Line
				break
			}
		}
	}
	for visual := m.editor.firstLine; visual < min(m.editor.lastLine, len(m.editor.sourceLines)); visual++ {
		line := m.editor.sourceLines[visual]
		if line == 0 {
			continue
		}
		y := transform.Translation.Y.Value + float32(visual)*text.LineHeight
		if index, exists := slices.BinarySearchFunc(bps, line, func(bp pencodeBreakpoint, line int) int { return bp.line - line }); exists {
			shape := scene.RoundedRectShape2D(color, 4)
			if m.debug.project == project && m.debug.session != nil && m.debug.view.State < debugger.Ended {
				verified := m.debug.view.Breakpoints[paint.filename]
				if file.Dirty || index >= len(verified) || !verified[index].Verified {
					shape = scene.OutlinedRoundedRectShape2D(graphics.ColorRGBA{}, color, 4, 1.5)
				}
			}
			entity := paint.entity(world, &paint.dots, &used, m.editor.gutter, stack)
			paint.sprite(world, entity, 5, y+(text.LineHeight-8)/2, 8, 8, shape)
		}
		if line == executionLine {
			if !world.IsAlive(paint.execution) {
				pool, count := []ecs.Entity(nil), 0
				paint.execution = paint.entity(world, &pool, &count, m.editor.control, stack-1)
			}
			size, _ := module.ContentGet[scene.Size2D](world, m.editor.control)
			background := m.workbenchTheme.diagnosticColors[2]
			background.A = .14
			module.ContentSetIfChanged(world, paint.execution, scene.Visibility{Visible: true})
			paint.sprite(world, paint.execution, 0, y, size.Width, text.LineHeight, scene.RoundedRectShape2D(background, 0))
		}
	}
}

func (paint *pencodeDebugPaint) entity(world *module.Content, pool *[]ecs.Entity, used *int, parent ecs.Entity, stack scene.UIStackIndex) ecs.Entity {
	if *used == len(*pool) {
		bundle := ecs.NewBundle(8)
		ecs.Put(&bundle, scene.UITransformFromXY(0, 0))
		ecs.Put(&bundle, scene.UIGlobalTransform{})
		ecs.Put(&bundle, scene.Parent{Entity: parent})
		ecs.Put(&bundle, scene.AnchorTopLeft)
		ecs.Put(&bundle, scene.Visibility{})
		ecs.Put(&bundle, scene.NewSprite2D())
		ecs.Put(&bundle, scene.Size2D{})
		ecs.Put(&bundle, stack)
		*pool = append(*pool, world.Spawn(bundle))
	}
	entity := (*pool)[*used]
	*used++
	module.ContentSetIfChanged(world, entity, scene.Visibility{Visible: true})
	module.ContentSetIfChanged(world, entity, stack)
	return entity
}

func (paint *pencodeDebugPaint) sprite(world *module.Content, entity ecs.Entity, x, y, width, height float32, shape scene.Shape2D) {
	module.ContentSetIfChanged(world, entity, scene.UITransformFromXY(x, y))
	module.ContentSetIfChanged(world, entity, scene.Size2D{Width: width, Height: height})
	sprite := scene.NewSprite2D()
	sprite.Width, sprite.Height, sprite.Shape = width, height, shape
	module.ContentSetIfChanged(world, entity, sprite)
}

func (paint *pencodeDebugPaint) dispose(world *module.Content) {
	for _, entity := range paint.dots {
		world.Despawn(entity)
	}
	world.Despawn(paint.execution)
	*paint = pencodeDebugPaint{}
}
