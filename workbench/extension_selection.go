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
	"github.com/pencode-labs/pencode/peb"
)

type pencodeExtensionSelectable struct{ ref, id string }

type pencodeExtensionTextSpan struct {
	value          string
	offset         int
	entity, scroll ecs.Entity
	x, y, height   float32
}

type pencodeExtensionTextPoint struct{ span, rune int }

type pencodeExtensionTextSelection struct {
	lastClick         time.Time
	surface           *pencodeExtensionSurface
	view              *peb.EditorView
	content           string
	region            string
	key               string
	quads             []ecs.Entity
	spans             []pencodeExtensionTextSpan
	anchor, caret     pencodeExtensionTextPoint
	area              ecs.Entity
	focusTarget       ecs.Entity
	lastEntity        ecs.Entity
	lastX, lastY      float32
	dragging, dragged bool
}

// Read the authored text to retain spaces omitted by paint fragments and avoid
// inserting hard newlines at soft wraps. Region trees hold independently
// updated message contents.
func (s *pencodeExtensionSurface) selectableText(ref string) string {
	var b strings.Builder
	space, breaks := false, 0
	separate := func(count int) {
		if b.Len() > 0 {
			breaks = max(breaks, count)
		}
		space = false
	}
	flush := func() {
		if b.Len() > 0 {
			// Preformatted text may already supply the boundary's line breaks.
			value := b.String()
			trailing := len(value) - len(strings.TrimRight(value, "\n"))
			for range max(0, breaks-trailing) {
				b.WriteByte('\n')
			}
			if breaks == 0 && space && !strings.HasSuffix(value, "\n") && !strings.HasSuffix(value, "\t") {
				b.WriteByte(' ')
			}
		}
		space, breaks = false, 0
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
		_, preserve := node.Attr("data-pencode-preserve-whitespace")
		pre = pre || node.Data == "pre" || preserve
		boundary := 0
		if selected {
			switch node.Data {
			case "p", "pre", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "table":
				boundary = 2
			case "div", "section", "article", "li", "tr":
				boundary = 1
			case "br":
				flush()
				b.WriteByte('\n')
				return
			}
			if boundary > 0 {
				separate(boundary)
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
						space = breaks == 0
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
		cell := false
		for _, child := range children {
			if selected && node.Data == "tr" && (child.Data == "td" || child.Data == "th") {
				if cell {
					flush()
					b.WriteByte('\t')
				}
				cell = true
			}
			walk(child, selected, pre)
		}
		if selected && boundary > 0 {
			separate(boundary)
		}
	}
	walk(s.tree, false, false)
	return b.String()
}

func extensionTextContainer(world *module.Content, entity, area ecs.Entity) (ecs.Entity, bool) {
	var scroll ecs.Entity
	for entity != 0 {
		if entity == area {
			return scroll, true
		}
		if scroll == 0 && world.Has[scene.UIScroll](entity) {
			scroll = entity
		}
		parent, ok := world.Get[scene.Parent](entity)
		if !ok {
			break
		}
		entity = parent.Entity
	}
	return 0, false
}

// Treat each nested scroll container as a contiguous part of the document.
// Its clipped children can overlap later paragraphs in screen coordinates.
func extensionTextGroup(world *module.Content, entity, area ecs.Entity) ecs.Entity {
	group := entity
	for entity != 0 && entity != area {
		if world.Has[scene.UIScroll](entity) {
			group = entity
		}
		parent, ok := world.Get[scene.Parent](entity)
		if !ok {
			break
		}
		entity = parent.Entity
	}
	return group
}

func extensionTextOrder(world *module.Content, area ecs.Entity, a, b pencodeExtensionTextSpan) int {
	if a.scroll == b.scroll {
		if order := cmp.Compare(a.y, b.y); order != 0 {
			return order
		}
		return cmp.Compare(a.x, b.x)
	}
	left, right := extensionTextGroup(world, a.entity, area), extensionTextGroup(world, b.entity, area)
	if left == right {
		return extensionTextOrder(world, left, a, b)
	}
	x, y := world.Ref[scene.UIGlobalTransform](left), world.Ref[scene.UIGlobalTransform](right)
	if order := cmp.Compare(x.Matrix[13], y.Matrix[13]); order != 0 {
		return order
	}
	return cmp.Compare(x.Matrix[12], y.Matrix[12])
}

// Collect the rendered runs, so copying follows the visible Markdown text and
// includes code, links and tool output without changing their layout.
func (s *pencodeExtensionTextSelection) collect(world *module.Content, surface *pencodeExtensionSurface, ref string) {
	s.content = surface.selectableText(ref)
	s.view, s.key = surface.view, surface.scrollKey
	clear(s.spans)
	s.spans = s.spans[:0]
	for entity, values := range world.Query2[scene.Text2D, scene.UIGlobalTransform]() {
		text, transform := values.A.Ref(), values.B.Ref()
		if text.Value == "" {
			continue
		}
		scroll, inside := extensionTextContainer(world, entity, s.area)
		if !inside {
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
		s.spans = append(s.spans, pencodeExtensionTextSpan{entity: entity, scroll: scroll, value: text.Value, x: transform.Matrix[12], y: transform.Matrix[13], height: size.Height * transform.Matrix[5]})
	}
	slices.SortFunc(s.spans, func(a, b pencodeExtensionTextSpan) int {
		return extensionTextOrder(world, s.area, a, b)
	})
	// Inline fonts can have different top edges on the same visual row.
	for start := 0; start < len(s.spans); {
		end := start + 1
		for end < len(s.spans) && s.spans[end].scroll == s.spans[start].scroll && s.spans[end].y >= s.spans[start].y && s.spans[end].y < s.spans[start].y+s.spans[start].height*.6 {
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

func (s *pencodeExtensionTextSelection) hit(world *module.Content, x, y float32, dragging bool) (pencodeExtensionTextPoint, bool) {
	best, distance := -1, float32(math.MaxFloat32)
	var localX, localY float32
	for i, span := range s.spans {
		transform := world.Ref[scene.UIGlobalTransform](span.entity)
		size := world.Ref[scene.Size2D](span.entity)
		if transform == nil || size == nil {
			continue
		}
		matrix := transform.Matrix
		if matrix[0] <= 0 || matrix[5] <= 0 {
			continue
		}
		left, top, right, bottom := float32(0), float32(0), size.Width, size.Height
		if clip, ok := transform.ClipRect(); ok {
			if !dragging && (x < clip.X || x > clip.X+clip.Width || y < clip.Y || y > clip.Y+clip.Height) {
				continue
			}
			left, top = max(left, (clip.X-matrix[12])/matrix[0]), max(top, (clip.Y-matrix[13])/matrix[5])
			right, bottom = min(right, (clip.X+clip.Width-matrix[12])/matrix[0]), min(bottom, (clip.Y+clip.Height-matrix[13])/matrix[5])
			if right <= left || bottom <= top {
				continue
			}
		}
		lx, ly := (x-matrix[12])/matrix[0], (y-matrix[13])/matrix[5]
		dx, dy := max(left-lx, lx-right, 0), max(top-ly, ly-bottom, 0)
		// Tabs and blank lines have no painted run. Resolve whitespace to its
		// nearest caret, preferring the same visual row over horizontal distance.
		d := dy*10000 + dx
		if d < distance {
			best, distance = i, d
			localX, localY = max(left, min(lx, right)), max(top, min(ly, bottom))
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

func (s *pencodeExtensionTextSelection) offset(point pencodeExtensionTextPoint) int {
	span := s.spans[point.span]
	left, _ := pencodeSelectionBytes(span.value, point.rune, point.rune)
	return span.offset + left
}

func (s *pencodeExtensionTextSelection) point(offset int) pencodeExtensionTextPoint {
	for i, span := range s.spans {
		if offset <= span.offset+len(span.value) {
			return pencodeExtensionTextPoint{i, utf8.RuneCountInString(span.value[:max(0, offset-span.offset)])}
		}
	}
	last := len(s.spans) - 1
	return pencodeExtensionTextPoint{last, utf8.RuneCountInString(s.spans[last].value)}
}

// Region updates can replace entities or reflow their runs without changing
// the selected text. Rebind endpoints after layout, retaining their byte
// positions across edits outside the selected range.
func (s *pencodeExtensionTextSelection) refresh(world *module.Content) bool {
	if s.surface == nil || !s.surface.live() || s.key != s.surface.scrollKey || len(s.spans) == 0 {
		return false
	}
	if s.view == s.surface.view && s.valid(world) {
		return true
	}
	anchor, caret := s.offset(s.anchor), s.offset(s.caret)
	previous := s.content
	a, b := s.ordered()
	start := s.spans[a.span].offset
	end := s.spans[b.span].offset + len(s.spans[b.span].value)
	found := false
	for _, region := range s.surface.selectables {
		if region.id != s.region {
			continue
		}
		if node, ok := world.HTMLNode(s.surface.model.surface, region.id); ok {
			if area, ok := node.ScrollContainer(); ok {
				s.area = area
				s.collect(world, s.surface, region.ref)
				found = len(s.spans) > 0
			}
		}
		break
	}
	if !found {
		return false
	}
	prefix := 0
	for prefix < min(len(previous), len(s.content)) && previous[prefix] == s.content[prefix] {
		prefix++
	}
	if max(anchor, caret) > prefix {
		suffix := 0
		for suffix < min(len(previous), len(s.content))-prefix && previous[len(previous)-1-suffix] == s.content[len(s.content)-1-suffix] {
			suffix++
		}
		if min(anchor, caret) < len(previous)-suffix {
			// Independent regions can change on both sides of a selection in
			// one publication. Retain it only when its complete text runs still
			// have a unique match; repeated text must not move it elsewhere.
			context := previous[start:end]
			index := strings.Index(s.content, context)
			if context == "" || index < 0 || strings.Contains(s.content[index+1:], context) ||
				strings.Index(previous, context) != start || strings.Contains(previous[start+1:], context) {
				return false
			}
			anchor, caret = anchor+index-start, caret+index-start
		} else {
			shift := len(s.content) - len(previous)
			anchor, caret = anchor+shift, caret+shift
		}
	}
	s.anchor, s.caret = s.point(anchor), s.point(caret)
	return s.valid(world)
}

func (s *pencodeExtensionTextSelection) text() string {
	if len(s.spans) == 0 || s.anchor == s.caret {
		return ""
	}
	a, b := s.ordered()
	first, last := s.spans[a.span], s.spans[b.span]
	left, _ := pencodeSelectionBytes(first.value, a.rune, a.rune)
	_, right := pencodeSelectionBytes(last.value, b.rune, b.rune)
	if a.span == 0 && left == 0 && b.span == len(s.spans)-1 && right == len(last.value) {
		return s.content
	}
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

func extensionTextControlHit(world *module.Content, entity ecs.Entity, x, y float32) bool {
	target := world.Ref[scene.InputTarget2D](entity)
	transform := world.Ref[scene.UIGlobalTransform](entity)
	size := world.Ref[scene.Size2D](entity)
	if target == nil || target.PassThrough || target.Disabled || transform == nil || size == nil {
		return false
	}
	if visible, ok := world.Get[scene.Visibility](entity); ok && !visible.Visible {
		return false
	}
	if clip, ok := transform.ClipRect(); ok && (x < clip.X || x >= clip.X+clip.Width || y < clip.Y || y >= clip.Y+clip.Height) {
		return false
	}
	matrix := transform.Matrix
	if matrix[0] <= 0 || matrix[5] <= 0 {
		return false
	}
	lx, ly := (x-matrix[12])/matrix[0], (y-matrix[13])/matrix[5]
	return lx >= 0 && lx < size.Width && ly >= 0 && ly < size.Height
}

// Buttons, native fields and scrollbars own their pointer gestures. Links still
// allow dragging their text into a transcript selection.
func (s *pencodeExtensionSurface) textSelectionBlocked(world *module.Content, x, y float32) bool {
	for _, f := range s.focus {
		if f.selectText {
			continue
		}
		if control, ok := world.HTMLControl(s.model.surface, f.id); ok && extensionTextControlHit(world, control, x, y) {
			return true
		}
	}
	for entity := range world.Query[htmlui.ScrollbarPart]() {
		if extensionTextControlHit(world, entity, x, y) {
			return true
		}
	}
	return false
}

func (m *workspaceModel) updateExtensionSelection(world *module.Content, state *input.State) bool {
	s := &m.actions.ui.selection
	if m.closing.action != pencodeCloseNone || m.fileContext.dialog != "" || m.actions.ui.overlay != nil {
		return false
	}
	pointer := state.Pointer()
	if state.PointerPressed() {
		s.surface, s.dragging, s.dragged = nil, false, false
	selectRegion:
		for _, surface := range m.actions.ui.surfaces {
			if !surface.live() || len(surface.selectables) == 0 {
				continue
			}
			if surface.textSelectionBlocked(world, pointer.PressX, pointer.PressY) {
				s.lastClick = time.Time{}
				break
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
				if !m.pointerInsideRefs(world, pointer.PressX/zoom, pointer.PressY/zoom, []string{id}) {
					continue
				}
				s.area, s.region = area, id
				s.collect(world, surface, region.ref)
				point, ok := s.hit(world, pointer.PressX, pointer.PressY, false)
				if !ok {
					continue
				}
				s.surface, s.anchor, s.caret, s.dragging = surface, point, point, true
				if s.spans[point.span].entity == s.lastEntity && time.Since(s.lastClick) < 500*time.Millisecond && math.Abs(float64(pointer.PressX-s.lastX)) < 4 && math.Abs(float64(pointer.PressY-s.lastY)) < 4 {
					text := world.Mut[scene.Text2D](s.spans[point.span].entity)
					token := engineui.CodeTokenAt(text.Text(), point.rune)
					s.anchor.rune, s.caret.rune, s.dragging, s.dragged = token.RuneStart, token.RuneEnd, false, true
				}
				s.lastClick, s.lastEntity, s.lastX, s.lastY = time.Now(), s.spans[point.span].entity, pointer.PressX, pointer.PressY
				break selectRegion
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
		dx, dy := pointer.X-pointer.PressX, pointer.Y-pointer.PressY
		// A click, small pointer jitter or a layout update is not a drag.
		if s.dragged || dx*dx+dy*dy >= 16 {
			if point, ok := s.hit(world, pointer.X, pointer.Y, true); ok {
				s.caret = point
			}
			s.dragged = s.dragged || s.anchor != s.caret
		}
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
	if !s.refresh(world) {
		s.surface, s.content, s.view, s.dragging = nil, "", nil, false
		clear(s.spans)
		s.spans = s.spans[:0]
	}
	if s.surface != nil && s.anchor != s.caret {
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
				if parent, _ := world.Get[scene.Parent](entity); parent.Entity != span.entity {
					// Parent is protected; Insert performs the module's reparent operation.
					changed = world.Insert(entity, scene.Parent{Entity: span.entity}) == nil || changed
				}
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
