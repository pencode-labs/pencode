package workbench

import (
	"html"
	"sort"
	"strconv"
	"strings"

	"unicode/utf8"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/html/dom"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/text/piecetree"
	htmlui "github.com/diiyw/onix/ui/html"
	nethtml "golang.org/x/net/html"
)

type pencodeRenderedFind struct {
	tree    *dom.Node
	content piecetree.Snapshot
	chunks  []pencodeRenderedFindChunk
	text    pencodeExtensionTextSelection
	dirty   bool
	reveal  bool
}

type pencodeRenderedFindChunk struct {
	node       *dom.Node
	start, end int
}

// Stable IDs allow layout queries for text whose paint entities are culled.
// Keep the authored element structure and whitespace intact.
func renderedFindTemplate(source, prefix string) string {
	z := nethtml.NewTokenizer(strings.NewReader(source))
	var b strings.Builder
	index := 0
	for {
		kind := z.Next()
		if kind == nethtml.ErrorToken {
			break
		}
		raw := z.Raw()
		if kind == nethtml.StartTagToken || kind == nethtml.SelfClosingTagToken {
			token := z.Token()
			id, searchable, tabindex := "", false, false
			for _, attr := range token.Attr {
				switch attr.Key {
				case "id":
					id = attr.Val
				case "data-pencode-searchable":
					searchable = true
				case "tabindex":
					tabindex = true
				}
			}
			if id == "" || searchable && !tabindex {
				value := strings.TrimSuffix(string(raw), ">")
				value = strings.TrimSuffix(value, "/")
				b.WriteString(value)
				if id == "" {
					b.WriteString(` id="`)
					b.WriteString(html.EscapeString(prefix + strconv.Itoa(index)))
					b.WriteByte('"')
					index++
				}
				if searchable && !tabindex {
					b.WriteString(` tabindex="-1"`)
				}
				if kind == nethtml.SelfClosingTagToken {
					b.WriteByte('/')
				}
				b.WriteByte('>')
				continue
			}
		}
		b.Write(raw)
	}
	return b.String()
}

func (f *pencodeRenderedFind) setTree(tree *dom.Node) {
	f.tree, f.dirty, f.reveal = tree, true, true
	clear(f.chunks)
	f.chunks = f.chunks[:0]
	surface := pencodeExtensionSurface{tree: tree}
	ref, _ := tree.Attr("data-ref")
	value := surface.selectableTextRanges(ref, func(node *dom.Node, start, end int) {
		parent := node.Parent
		for parent != tree && parent.Parent != nil {
			switch parent.Data {
			case "p", "pre", "h1", "h2", "h3", "h4", "h5", "h6", "li", "td", "th", "div":
				goto found
			}
			parent = parent.Parent
		}
	found:
		if n := len(f.chunks); n > 0 && f.chunks[n-1].node == parent {
			f.chunks[n-1].end = end
		} else {
			f.chunks = append(f.chunks, pencodeRenderedFindChunk{node: parent, start: start, end: end})
		}
	})
	f.content = snapshotText(value)
}

func (m *workspaceModel) renderedFind() *pencodeRenderedFind {
	file := m.activeFile()
	document := m.customDocuments[file]
	if document == nil || document.find == nil || m.sourceState().visible() || m.actions.ui.editor != nil ||
		m.fileEditor(file) != document.provider || m.textEditorActive() && m.findPreview != document {
		return nil
	}
	return document.find
}

func (f *pencodeRenderedFind) focus(world *module.Content) {
	id, _ := f.tree.Attr("id")
	for entity, focus := range world.Query[htmlui.Focusable]() {
		if focus.Ref().ID == id {
			world.Focus(entity)
			return
		}
	}
}

// Bounds are available even outside the paint window. Project them through
// the native scroll containers and workspace zoom to match live text runs.
func renderedChunkBounds(world *module.Content, m *workspaceModel, node *dom.Node) (htmlui.Rect, bool) {
	id, _ := node.Attr("id")
	retained, ok := world.HTMLNode(m.surface, id)
	if !ok {
		return htmlui.Rect{}, false
	}
	bounds, ok := retained.Bounds()
	if !ok {
		return bounds, false
	}
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		id, _ := parent.Attr("id")
		if retained, ok := world.HTMLNode(m.surface, id); ok {
			if area, ok := retained.ScrollContainer(); ok {
				scroll := world.Ref[scene.UIScroll](area)
				bounds.X += scroll.Position[0]
				bounds.Y += scroll.Position[1]
			}
		}
	}
	if retained, ok := world.HTMLNode(m.surface, "custom-editor"); ok {
		if area, ok := retained.ScrollContainer(); ok {
			scroll := world.Ref[scene.UIScroll](area)
			bounds.X += scroll.Position[0]
			bounds.Y += scroll.Position[1]
		}
	}
	transform := world.Ref[scene.UIGlobalTransform](m.surface)
	bounds.X = transform.Matrix[12] + bounds.X*transform.Matrix[0]
	bounds.Y = transform.Matrix[13] + bounds.Y*transform.Matrix[5]
	bounds.Width *= transform.Matrix[0]
	bounds.Height *= transform.Matrix[5]
	return bounds, true
}

func (f *pencodeRenderedFind) bind(world *module.Content, m *workspaceModel) bool {
	node, ok := world.HTMLNode(m.surface, "custom-editor")
	if !ok {
		return false
	}
	area, ok := node.ScrollContainer()
	if !ok {
		return false
	}
	valid := !f.dirty && f.text.area == area && len(f.text.spans) > 0
	if valid {
		for _, span := range f.text.spans {
			text := world.Ref[scene.Text2D](span.entity)
			transform := world.Ref[scene.UIGlobalTransform](span.entity)
			if text == nil || text.Value != span.value || transform == nil || transform.Matrix[12] != span.x || transform.Matrix[13] != span.y {
				valid = false
				break
			}
		}
	}
	if valid {
		return true
	}
	// Collection observes paint culling on every layout pass. Match each run
	// inside its authored block so repeated text cannot shift to another block.
	f.text.area = area
	surface := pencodeExtensionSurface{tree: f.tree, model: m}
	f.text.content = f.content.String()
	f.text.collectText(world, &surface)
	cursor, used := 0, 0
	content := f.content.String()
	for _, span := range f.text.spans {
		for j := cursor; j < len(f.chunks); j++ {
			chunk := &f.chunks[j]
			bounds, ok := renderedChunkBounds(world, m, chunk.node)
			if !ok || span.y+span.height <= bounds.Y || span.y >= bounds.Y+bounds.Height {
				continue
			}
			start := chunk.start
			if used > 0 {
				previous := f.text.spans[used-1]
				start = max(start, previous.offset+len(previous.value))
			}
			if start > chunk.end {
				continue
			}
			if index := strings.Index(content[start:chunk.end], span.value); index >= 0 {
				span.offset = start + index
				f.text.spans[used] = span
				used++
				cursor = j
				break
			}
		}
	}
	clear(f.text.spans[used:])
	f.text.spans = f.text.spans[:used]
	f.dirty = false
	return true
}

func (f *pencodeRenderedFind) revealChunk(world *module.Content, m *workspaceModel, offset int) bool {
	index := sort.Search(len(f.chunks), func(i int) bool { return f.chunks[i].end > offset })
	if index == len(f.chunks) {
		return false
	}
	bounds, ok := renderedChunkBounds(world, m, f.chunks[index].node)
	if !ok {
		return false
	}
	scroll := world.Mut[scene.UIScroll](f.text.area)
	transform := world.Ref[scene.UIGlobalTransform](f.text.area)
	y := (bounds.Y - transform.Matrix[13]) / transform.Matrix[5]
	previous := scroll.Position[1]
	scroll.Position[1] = max(-max(0, scroll.ContentSize[1]-scroll.ViewportSize[1]), min(0, previous-y+scroll.ViewportSize[1]/2))
	scroll.Momentum[1] = 0
	return previous != scroll.Position[1]
}

func (f *pencodeRenderedFind) point(offset int) pencodeExtensionTextPoint {
	index := sort.Search(len(f.text.spans), func(i int) bool {
		span := f.text.spans[i]
		return span.offset+len(span.value) > offset
	})
	index = min(index, len(f.text.spans)-1)
	span := f.text.spans[index]
	return pencodeExtensionTextPoint{index, utf8.RuneCountInString(span.value[:max(0, min(len(span.value), offset-span.offset))])}
}

// Reveal in every ancestor scroll area, including horizontally scrolled code
// and diagrams. Positions and transforms use the native negative scroll offset.
func (f *pencodeRenderedFind) revealMatch(world *module.Content, start, end int) (changed bool) {
	point := f.point(start)
	span := f.text.spans[point.span]
	text := world.Mut[scene.Text2D](span.entity)
	transform := world.Ref[scene.UIGlobalTransform](span.entity)
	x, y, width, height, ok := scene.TextCaretRect2D(text, text.Text(), point.rune)
	if !ok || transform == nil {
		return false
	}
	if end <= span.offset+len(span.value) {
		last := utf8.RuneCountInString(span.value[:max(0, end-span.offset)])
		if right, bottom, _, _, ok := scene.TextCaretRect2D(text, text.Text(), last); ok && bottom == y {
			width = max(width, right-x)
		}
	}
	target := [2]float32{transform.Matrix[12] + (x+width/2)*transform.Matrix[0], transform.Matrix[13] + (y+height/2)*transform.Matrix[5]}
	for entity := span.entity; entity != 0; {
		if scroll := world.Mut[scene.UIScroll](entity); scroll != nil {
			position := world.Ref[scene.UIGlobalTransform](entity)
			if position != nil {
				for axis, enabled := range [2]bool{scroll.Horizontal, scroll.Vertical} {
					if !enabled {
						continue
					}
					scale := position.Matrix[axis*5]
					if scale <= 0 {
						continue
					}
					delta := (target[axis]-position.Matrix[12+axis])/scale - scroll.ViewportSize[axis]/2
					previous := scroll.Position[axis]
					scroll.Position[axis] = max(-max(0, scroll.ContentSize[axis]-scroll.ViewportSize[axis]), min(0, previous-delta))
					scroll.Momentum[axis] = 0
					target[axis] += (scroll.Position[axis] - previous) * scale
					changed = scroll.Position[axis] != previous || changed
				}
			}
		}
		if entity == f.text.area {
			break
		}
		parent, ok := world.Get[scene.Parent](entity)
		if !ok {
			break
		}
		entity = parent.Entity
	}
	return changed
}

func (m *workspaceModel) syncRenderedFind(world *module.Content) (changed bool) {
	active := m.renderedFind()
	for _, document := range m.customDocuments {
		f := document.find
		if f == nil {
			continue
		}
		used := 0
		if f == active && m.findOpen && !m.settingsOpen && m.workbenchTheme != nil && f.bind(world, m) {
			m.refreshFindMatches()
			if f.reveal && len(m.findMatches) > 0 {
				match := m.findMatches[m.findIndex]
				found := false
				if len(f.text.spans) > 0 {
					point := f.point(match.Start)
					span := f.text.spans[point.span]
					found = match.Start >= span.offset && match.Start < span.offset+len(span.value)
				}
				if !found {
					f.dirty = f.revealChunk(world, m, match.Start)
					changed = f.dirty || changed
				} else {
					changed = f.revealMatch(world, match.Start, match.End) || changed
				}
			}
			f.reveal = f.dirty
			for _, span := range f.text.spans {
				text := world.Mut[scene.Text2D](span.entity)
				transform := world.Ref[scene.UIGlobalTransform](span.entity)
				first := sort.Search(len(m.findMatches), func(i int) bool { return m.findMatches[i].End >= span.offset })
				for i := first; i < len(m.findMatches) && m.findMatches[i].Start <= span.offset+len(span.value); i++ {
					match := m.findMatches[i]
					left, right := max(0, match.Start-span.offset), min(len(span.value), match.End-span.offset)
					if left >= right && match.Start != match.End {
						continue
					}
					start, end := utf8.RuneCountInString(span.value[:left]), utf8.RuneCountInString(span.value[:right])
					if start == end {
						point := f.point(match.Start)
						if f.text.spans[point.span].entity != span.entity {
							continue
						}
						if x, y, _, height, ok := scene.TextCaretRect2D(text, text.Text(), start); ok {
							changed = f.matchQuad(world, m, &used, i, span.entity, transform, scene.TextRangeRect2D{X: x, Y: y, Width: 2, Height: height}) || changed
						}
						continue
					}
					for rect := range scene.TextSelectionRects2D(text, text.Text(), start, end, 0, 0) {
						changed = f.matchQuad(world, m, &used, i, span.entity, transform, rect) || changed
					}
				}
			}
		}
		for _, entity := range f.text.quads[used:] {
			if world.IsAlive(entity) {
				changed = world.SetIfChanged(entity, scene.Visibility{}) || changed
			}
		}
	}
	return changed
}

func (f *pencodeRenderedFind) matchQuad(world *module.Content, m *workspaceModel, used *int, index int, parent ecs.Entity, transform *scene.UIGlobalTransform, rect scene.TextRangeRect2D) bool {
	if clip, clipped := transform.ClipRect(); clipped {
		x, y := transform.Matrix[12]+rect.X*transform.Matrix[0], transform.Matrix[13]+rect.Y*transform.Matrix[5]
		if x+rect.Width*transform.Matrix[0] <= clip.X || x >= clip.X+clip.Width || y+rect.Height*transform.Matrix[5] <= clip.Y || y >= clip.Y+clip.Height {
			return false
		}
	}
	colors := m.workbenchTheme.findColors
	color := colors[0]
	if index == m.findIndex {
		color = colors[1]
	}
	changed := f.quad(world, used, parent, rect, color, graphics.ColorRGBA{})
	if index == m.findIndex {
		changed = f.quad(world, used, parent, rect, graphics.ColorRGBA{}, colors[2]) || changed
	}
	return changed
}

func (f *pencodeRenderedFind) quad(world *module.Content, used *int, parent ecs.Entity, rect scene.TextRangeRect2D, color, border graphics.ColorRGBA) (changed bool) {
	if *used == len(f.text.quads) {
		f.text.quads = append(f.text.quads, 0)
	}
	entity := f.text.quads[*used]
	if !world.IsAlive(entity) {
		bundle := ecs.NewBundle(8)
		bundle.Put(scene.UITransformFromXY(0, 0))
		bundle.Put(scene.UIGlobalTransform{})
		bundle.Put(scene.Parent{Entity: parent})
		bundle.Put(scene.AnchorTopLeft)
		bundle.Put(scene.Visibility{})
		bundle.Put(scene.Size2D{})
		bundle.Put(scene.UIStackIndex(0))
		bundle.Put(scene.NewSprite2D())
		entity = world.Spawn(bundle)
		f.text.quads[*used] = entity
		changed = true
	}
	if previous, _ := world.Get[scene.Parent](entity); previous.Entity != parent {
		changed = world.Insert(entity, scene.Parent{Entity: parent}) == nil || changed
	}
	stack, _ := world.Get[scene.UIStackIndex](parent)
	sprite := scene.NewSprite2D()
	sprite.Width, sprite.Height = max(2, rect.Width), rect.Height
	if border.A == 0 {
		sprite.Shape = scene.RoundedRectShape2D(color, 0)
		stack--
	} else {
		sprite.Shape = scene.OutlinedRoundedRectShape2D(color, border, 0, 1)
		stack++
	}
	changed = world.SetIfChanged(entity, scene.UITransformFromXY(rect.X, rect.Y)) || changed
	changed = world.SetIfChanged(entity, scene.Size2D{Width: sprite.Width, Height: sprite.Height}) || changed
	changed = world.SetIfChanged(entity, stack) || changed
	changed = world.SetIfChanged(entity, sprite) || changed
	changed = world.SetIfChanged(entity, scene.Visibility{Visible: true}) || changed
	*used++
	return changed
}
