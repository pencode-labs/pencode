package workbench

import (
	"cmp"
	"math"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/html/dom"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

type pencodeExtensionSelectable struct{ ref, id string }

type pencodeExtensionTextSpan struct {
	offset       int
	entity       ecs.Entity
	value        string
	x, y, height float32
}

type pencodeExtensionTextPoint struct{ span, rune int }

type pencodeExtensionTextSelection struct {
	region            string
	content           string
	surface           *pencodeExtensionSurface
	area              ecs.Entity
	focusTarget       ecs.Entity
	spans             []pencodeExtensionTextSpan
	anchor, caret     pencodeExtensionTextPoint
	quads             []ecs.Entity
	dragging, dragged bool
	lastClick         time.Time
	lastEntity        ecs.Entity
	lastX, lastY      float32
}

// Read the authored text to retain spaces omitted by paint fragments and avoid
// inserting hard newlines at soft wraps. Region trees hold independently
// updated message contents.
func (s *pencodeExtensionSurface) selectableText(ref string) string {
	var b strings.Builder
	space, newline := false, false
	flush := func() {
		if b.Len() > 0 {
			if newline {
				b.WriteByte('\n')
			} else if space {
				b.WriteByte(' ')
			}
		}
		space, newline = false, false
	}
	var walk func(*dom.Node, bool, bool)
	walk = func(node *dom.Node, selected, pre bool) {
		if _, hidden := node.Attr("hidden"); hidden {
			return
		}
		if hidden, _ := node.Attr("aria-hidden"); hidden == "true" {
			return
		}
		if node.Data == "input" || node.Data == "textarea" || node.Data == "img" {
			return
		}
		nodeRef, _ := node.Attr("data-ref")
		selected = selected || nodeRef == ref
		pre = pre || node.Data == "pre"
		block := false
		if selected {
			switch node.Data {
			case "p", "div", "section", "article", "pre", "li", "h1", "h2", "h3", "h4", "h5", "h6", "tr", "blockquote", "br":
				block = true
			}
			if block {
				space, newline = false, b.Len() > 0
			}
			if node.Type == dom.TextNode {
				if pre {
					flush()
					b.WriteString(node.Data)
					return
				}
				for _, r := range node.Data {
					switch r {
					case ' ', '\t', '\r', '\n', '\f':
						space = !newline
					default:
						flush()
						b.WriteRune(r)
					}
				}
			}
		}
		children := node.Children
		if _, region := node.Attr("data-pencode-region"); region {
			for _, part := range s.regions {
				if part.ref == nodeRef {
					children = part.tree.Children
					break
				}
			}
		}
		for _, child := range children {
			walk(child, selected, pre)
		}
		if selected && block {
			space, newline = false, b.Len() > 0
		}
	}
	walk(s.tree, false, false)
	return b.String()
}

func extensionTextDescendant(world *module.Content, entity, area ecs.Entity) bool {
	for entity != 0 {
		if entity == area {
			return true
		}
		parent, ok := world.Get[scene.Parent](entity)
		if !ok {
			return false
		}
		entity = parent.Entity
	}
	return false
}

// Collect the rendered runs, so copying follows the visible Markdown text and
// includes code, links and tool output without changing their layout.
func (s *pencodeExtensionTextSelection) collect(world *module.Content, surface *pencodeExtensionSurface, ref string) {
	s.content = surface.selectableText(ref)
	clear(s.spans)
	s.spans = s.spans[:0]
	for entity, values := range world.Query2[scene.Text2D, scene.UIGlobalTransform]() {
		text, transform := values.A.Ref(), values.B.Ref()
		if text.Value == "" || !extensionTextDescendant(world, entity, s.area) {
			continue
		}
		if visible, ok := world.Get[scene.Visibility](entity); ok && !visible.Visible {
			continue
		}
		if link, ok := world.Get[htmlui.ControlText](entity); ok {
			control, _ := world.Get[htmlui.Control](link.Control)
			if control.Tag == "input" || control.Tag == "textarea" {
				continue
			}
		}
		size, _ := world.Get[scene.Size2D](entity)
		s.spans = append(s.spans, pencodeExtensionTextSpan{entity: entity, value: text.Value, x: transform.Matrix[12], y: transform.Matrix[13], height: size.Height * transform.Matrix[5]})
	}
	slices.SortFunc(s.spans, func(a, b pencodeExtensionTextSpan) int {
		if order := cmp.Compare(a.y, b.y); order != 0 {
			return order
		}
		return cmp.Compare(a.x, b.x)
	})
	// Inline fonts can have different top edges on the same visual row.
	for start := 0; start < len(s.spans); {
		end := start + 1
		for end < len(s.spans) && s.spans[end].y < s.spans[start].y+s.spans[start].height*.6 {
			end++
		}
		slices.SortFunc(s.spans[start:end], func(a, b pencodeExtensionTextSpan) int { return cmp.Compare(a.x, b.x) })
		start = end
	}
	cursor, used := 0, 0
	for _, span := range s.spans {
		index := strings.Index(s.content[cursor:], span.value)
		if index < 0 {
			continue
		}
		span.offset = cursor + index
		cursor = span.offset + len(span.value)
		s.spans[used] = span
		used++
	}
	clear(s.spans[used:])
	s.spans = s.spans[:used]
}

func (s *pencodeExtensionTextSelection) hit(world *module.Content, x, y float32, nearest bool) (pencodeExtensionTextPoint, bool) {
	best, distance := -1, float32(math.MaxFloat32)
	var localX, localY float32
	for i, span := range s.spans {
		transform := world.Ref[scene.UIGlobalTransform](span.entity)
		size := world.Ref[scene.Size2D](span.entity)
		if transform == nil || size == nil {
			continue
		}
		if !nearest {
			if clip, ok := transform.ClipRect(); ok && (x < clip.X || x > clip.X+clip.Width || y < clip.Y || y > clip.Y+clip.Height) {
				continue
			}
		}
		matrix := transform.Matrix
		if matrix[0] <= 0 || matrix[5] <= 0 {
			continue
		}
		lx, ly := (x-matrix[12])/matrix[0], (y-matrix[13])/matrix[5]
		dx, dy := max(-lx, lx-size.Width, 0), max(-ly, ly-size.Height, 0)
		if !nearest && (dx > 0 || dy > 0) {
			continue
		}
		// Prefer the same visual row before choosing its nearest horizontal run.
		d := dy*10000 + dx
		if d < distance {
			best, distance, localX, localY = i, d, lx, ly
		}
	}
	if best < 0 {
		return pencodeExtensionTextPoint{}, false
	}
	text := world.Mut[scene.Text2D](s.spans[best].entity)
	return pencodeExtensionTextPoint{best, scene.TextCaretIndex2D(text, text.Text(), localX, localY)}, true
}

func (s *pencodeExtensionTextSelection) ordered() (pencodeExtensionTextPoint, pencodeExtensionTextPoint) {
	a, b := s.anchor, s.caret
	if a.span > b.span || a.span == b.span && a.rune > b.rune {
		a, b = b, a
	}
	return a, b
}

func (s *pencodeExtensionTextSelection) valid(world *module.Content) bool {
	if s.surface == nil || !s.surface.live() || !world.IsAlive(s.area) {
		return false
	}
	a, b := s.ordered()
	if len(s.spans) == 0 || b.span >= len(s.spans) {
		return false
	}
	for _, span := range s.spans[a.span : b.span+1] {
		text := world.Ref[scene.Text2D](span.entity)
		if text == nil || text.Value != span.value {
			return false
		}
	}
	return len(s.spans) > 0
}

func (s *pencodeExtensionTextSelection) text() string {
	if len(s.spans) == 0 || s.anchor == s.caret {
		return ""
	}
	a, b := s.ordered()
	first, last := s.spans[a.span], s.spans[b.span]
	left, _ := pencodeSelectionBytes(first.value, a.rune, a.rune)
	_, right := pencodeSelectionBytes(last.value, b.rune, b.rune)
	return s.content[first.offset+left : last.offset+right]
}

func (s *pencodeExtensionTextSelection) selectAll() {
	if len(s.spans) == 0 {
		return
	}
	s.anchor = pencodeExtensionTextPoint{}
	last := len(s.spans) - 1
	s.caret = pencodeExtensionTextPoint{last, utf8.RuneCountInString(s.spans[last].value)}
}

func (s *pencodeExtensionTextSelection) focus(world *module.Content, m *workspaceModel) {
	if !world.IsAlive(s.focusTarget) {
		bundle := ecs.NewBundle(2)
		bundle.Put(scene.Parent{Entity: m.surface})
		bundle.Put(htmlui.Focusable{Owner: m.surface, TabIndex: -1, Pointer: true})
		s.focusTarget = world.Spawn(bundle)
	}
	world.Focus(s.focusTarget)
}

func (m *workspaceModel) updateExtensionSelection(world *module.Content, state *input.State) bool {
	s := &m.actions.ui.selection
	if m.closing.action != pencodeCloseNone || m.fileContext.dialog != "" || m.actions.ui.overlay != nil {
		return false
	}
	pointer := state.Pointer()
	if state.PointerPressed() {
		s.surface, s.dragging, s.dragged = nil, false, false
		for _, surface := range m.actions.ui.surfaces {
			if !surface.live() {
				continue
			}
			for _, region := range surface.selectables {
				id := region.id
				node, ok := world.HTMLNode(m.surface, id)
				if !ok {
					continue
				}
				area, ok := node.ScrollContainer()
				if !ok {
					continue
				}
				zoom := max(float32(m.settings.Zoom)/100, .01)
				if !m.pointerInsideRefs(world, pointer.X/zoom, pointer.Y/zoom, []string{id}) {
					continue
				}
				s.area, s.region = area, id
				s.collect(world, surface, region.ref)
				point, ok := s.hit(world, pointer.X, pointer.Y, false)
				if !ok {
					continue
				}
				s.surface, s.anchor, s.caret, s.dragging = surface, point, point, !state.PointerReleased()
				if s.spans[point.span].entity == s.lastEntity && time.Since(s.lastClick) < 500*time.Millisecond && math.Abs(float64(pointer.X-s.lastX)) < 4 && math.Abs(float64(pointer.Y-s.lastY)) < 4 {
					text := world.Mut[scene.Text2D](s.spans[point.span].entity)
					token := engineui.CodeTokenAt(text.Text(), point.rune)
					s.anchor.rune, s.caret.rune, s.dragging, s.dragged = token.RuneStart, token.RuneEnd, false, true
				}
				s.lastClick, s.lastEntity, s.lastX, s.lastY = time.Now(), s.spans[point.span].entity, pointer.X, pointer.Y
				return false
			}
		}
	}
	if !s.valid(world) {
		s.surface, s.content, s.dragging = nil, "", false
		clear(s.spans)
		s.spans = s.spans[:0]
		return false
	}
	if s.dragging && (state.PointerHeld() || state.PointerReleased()) {
		if point, ok := s.hit(world, pointer.X, pointer.Y, true); ok {
			s.caret = point
		}
		s.dragged = s.dragged || s.anchor != s.caret
		if s.dragged {
			s.lastClick = time.Time{}
			s.focus(world, m)
		}
		if state.PointerReleased() {
			s.dragging = false
		}
	}
	if state.MousePressed(input.MouseRight) && s.anchor != s.caret {
		zoom := max(float32(m.settings.Zoom)/100, .01)
		if !m.pointerInsideRefs(world, pointer.X/zoom, pointer.Y/zoom, []string{s.region}) {
			return false
		}
		root, ok := world.HTMLNode(m.surface, "pencode-root")
		if !ok {
			return false
		}
		bounds, _ := root.Bounds()
		c := &m.inputContext
		*c = pencodeInputContext{selection: s, value: s.text(), open: true, focus: true, index: 1}
		c.x, c.y = clampLanguagePopup(pointer.X/zoom-bounds.X, pointer.Y/zoom-bounds.Y, bounds, 240, 160)
		markPencodeRootDirtyByModel(world, m)
		return true
	}
	if focused := world.FocusedControl(); focused != 0 {
		if _, ok := world.Get[engineui.TextField](focused); ok {
			return false
		}
	}
	if m.commandMatches("editor-copy", state, runtime.GOOS) && s.anchor != s.caret {
		state.SetClipboardText(s.text())
		return true
	}
	if m.commandMatches("editor-select-all", state, runtime.GOOS) {
		s.selectAll()
		return true
	}
	if m.uiKey(state, input.KeyEscape) {
		s.surface = nil
		return true
	}
	return false
}

// Keep highlights attached to their text runs, inheriting zoom, nested code
// scrolling and clipping. Reuse their entities on subsequent selections.
func (s *pencodeExtensionTextSelection) sync(world *module.Content, m *workspaceModel) (changed bool) {
	used := 0
	if s.valid(world) && s.anchor != s.caret {
		a, b := s.ordered()
		color := pencodeSyntaxColor(m.syntaxTheme.Color("editor.selectionBackground"))
		color.A = min(color.A, .55)
		for i := a.span; i <= b.span; i++ {
			span := s.spans[i]
			text := world.Mut[scene.Text2D](span.entity)
			start, end := 0, utf8.RuneCountInString(span.value)
			if i == a.span {
				start = a.rune
			}
			if i == b.span {
				end = b.rune
			}
			transform := world.Ref[scene.UIGlobalTransform](span.entity)
			clip, clipped := transform.ClipRect()
			for rect := range scene.TextSelectionRects2D(text, text.Text(), start, end, 0, 0) {
				if clipped {
					x, y := transform.Matrix[12]+rect.X*transform.Matrix[0], transform.Matrix[13]+rect.Y*transform.Matrix[5]
					if x+rect.Width*transform.Matrix[0] <= clip.X || x >= clip.X+clip.Width || y+rect.Height*transform.Matrix[5] <= clip.Y || y >= clip.Y+clip.Height {
						continue
					}
				}
				if used == len(s.quads) {
					s.quads = append(s.quads, 0)
				}
				entity := s.quads[used]
				if !world.IsAlive(entity) {
					bundle := ecs.NewBundle(8)
					bundle.Put(scene.UITransformFromXY(0, 0))
					bundle.Put(scene.UIGlobalTransform{})
					bundle.Put(scene.Parent{Entity: span.entity})
					bundle.Put(scene.AnchorTopLeft)
					bundle.Put(scene.Visibility{})
					bundle.Put(scene.Size2D{})
					bundle.Put(scene.UIStackIndex(0))
					bundle.Put(scene.NewSprite2D())
					entity = world.Spawn(bundle)
					s.quads[used] = entity
					changed = true
				}
				stack, _ := world.Get[scene.UIStackIndex](span.entity)
				sprite := scene.NewSprite2D()
				sprite.Width, sprite.Height, sprite.Shape = rect.Width, rect.Height, scene.RoundedRectShape2D(color, 0)
				changed = world.SetIfChanged(entity, scene.Parent{Entity: span.entity}) || changed
				changed = world.SetIfChanged(entity, scene.UITransformFromXY(rect.X, rect.Y)) || changed
				changed = world.SetIfChanged(entity, scene.Size2D{Width: rect.Width, Height: rect.Height}) || changed
				changed = world.SetIfChanged(entity, stack+1) || changed
				changed = world.SetIfChanged(entity, sprite) || changed
				changed = world.SetIfChanged(entity, scene.Visibility{Visible: true}) || changed
				used++
			}
		}
	}
	for _, entity := range s.quads[used:] {
		if world.IsAlive(entity) {
			changed = world.SetIfChanged(entity, scene.Visibility{}) || changed
		}
	}
	return changed
}
