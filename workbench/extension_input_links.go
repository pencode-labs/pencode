package workbench

import (
	"strings"

	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/text/piecetree"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

type pencodeInputLink struct{ start, labelEnd, end int }

// Conceal syntax in native layout, keeping the source, IME and undo untouched.
// Each concealed delimiter reserves a one-pixel inset around the link label.
type pencodeInputLinks struct {
	source        piecetree.Snapshot
	composition   string
	links         []pencodeInputLink
	objects       []scene.TextInlineObject2D
	spans         []scene.TextSpan2D
	anchor, caret int
}

func (s *pencodeExtensionSurface) syncInputLinks(world *module.Content) (changed bool) {
	for i := range s.focus {
		f := &s.focus[i]
		if f.inputLinks == nil {
			continue
		}
		control, ok := world.HTMLControl(s.model.surface, f.id)
		if !ok {
			continue
		}
		field := world.Mut[engineui.TextField](control)
		visual, exists := world.FindPart[engineui.ValuePart, scene.Text2D](control)
		if field == nil || !exists {
			// A replaced HTML control may still need its native field/parts.
			// Finish reconciliation before painting instead of waiting for input.
			changed = true
			continue
		}
		if text := world.Mut[scene.Text2D](visual); text != nil {
			changed = f.inputLinks.update(field.Text()) || changed
			anchor, caret := field.Anchor, field.Caret
			if field.Composition == "" {
				f.inputLinks.snapSelection(field)
			}
			changed = f.inputLinks.paint(text, *field, s.model.workbenchTheme.link) || field.Anchor != anchor || field.Caret != caret || changed
		}
	}
	return changed
}

func (p *pencodeInputLinks) paint(text *scene.Text2D, field engineui.TextField, color graphics.ColorRGBA) (changed bool) {
	changed = p.update(field.Text())
	changed = p.composition != field.Composition || field.Composition != "" && (p.anchor != field.Anchor || p.caret != field.Caret) || changed
	p.composition, p.anchor, p.caret = field.Composition, field.Anchor, field.Caret
	source := p.source
	p.objects, p.spans = p.objects[:0], p.spans[:0]
	start, end := 0, 0
	if field.Composition != "" {
		start, end = source.ByteOffset(min(field.Anchor, field.Caret)), source.ByteOffset(max(field.Anchor, field.Caret))
	}
	for _, link := range p.links {
		shift := 0
		if field.Composition != "" && link.end > start {
			// Native IME display replaces the selection. Shift references after
			// it, and leave any replaced reference to the composition itself.
			if link.start < end {
				continue
			}
			shift = len(field.Composition) - (end - start)
		}
		p.spans = append(p.spans, scene.TextSpan2D{Start: link.start + 1 + shift, End: link.labelEnd + shift, Color: color})
		p.conceal(link.start+shift, link.start+1+shift)
		for offset := link.start + 1; offset < link.labelEnd; offset++ {
			if source.ByteAt(offset) == '\\' && offset+1 < link.labelEnd {
				p.conceal(offset+shift, offset+1+shift)
				offset++
			}
		}
		p.conceal(link.labelEnd+shift, link.end+shift)
	}
	changed = changed || len(text.InlineObjects) != len(p.objects)
	text.InlineObjects, text.Spans = p.objects, p.spans
	return changed
}

func (p *pencodeInputLinks) update(source piecetree.Snapshot) bool {
	if source == p.source {
		return false
	}
	p.links = parseInputLinks(p.links[:0], source.String())
	p.source = source
	return true
}

func (p *pencodeInputLinks) snapSelection(field *engineui.TextField) {
	start, end := p.source.ByteOffset(min(field.Anchor, field.Caret)), p.source.ByteOffset(max(field.Anchor, field.Caret))
	collapsed := start == end
	for _, link := range p.links {
		if collapsed && start > link.start && start < link.end {
			position := link.start
			if start > (link.start+1+link.labelEnd)/2 {
				position = link.end
			}
			field.Anchor, field.Caret = p.source.RuneOffset(position), p.source.RuneOffset(position)
			return
		}
		if !collapsed && start < link.end && end > link.start {
			start, end = min(start, link.start), max(end, link.end)
		}
	}
	if !collapsed {
		anchor, caret := p.source.RuneOffset(start), p.source.RuneOffset(end)
		if field.Anchor > field.Caret {
			anchor, caret = caret, anchor
		}
		field.Anchor, field.Caret = anchor, caret
	}
}

// Expand a native deletion selection before the normal input pipeline runs,
// preserving its undo history, clipboard handling and extension input event.
func (s *pencodeExtensionSurface) interceptInputLinkKeys(state *input.State) bool {
	w := s.model.fontResources
	for i := range s.focus {
		f := &s.focus[i]
		if f.inputLinks == nil {
			continue
		}
		control, ok := w.HTMLControl(s.model.surface, f.id)
		if !ok || w.FocusedControl() != control {
			continue
		}
		field := w.Mut[engineui.TextField](control)
		native, ok := w.Get[htmlui.Control](control)
		if field == nil || !ok || native.ReadOnly || native.Disabled || field.Composition != "" {
			return false
		}
		p := f.inputLinks
		p.update(field.Text())
		p.snapSelection(field)
		if field.Anchor != field.Caret {
			return false
		}
		caret := p.source.ByteOffset(field.Caret)
		backward, forward := state.KeyPressed(input.KeyBackspace), state.KeyPressed(input.KeyDelete)
		modified := state.KeyHeld(input.KeyLeftControl) || state.KeyHeld(input.KeyRightControl) || state.KeyHeld(input.KeyLeftAlt) || state.KeyHeld(input.KeyRightAlt) || state.KeyHeld(input.KeyLeftSuper) || state.KeyHeld(input.KeyRightSuper)
		for _, link := range p.links {
			end := link.end
			if end < p.source.Len() && p.source.ByteAt(end) == ' ' {
				end++
			}
			if backward && (caret == link.end || caret == end) || forward && caret == link.start {
				field.Anchor, field.Caret = p.source.RuneOffset(link.start), p.source.RuneOffset(end)
				return false
			}
			if modified {
				continue
			}
			position := -1
			if state.KeyPressed(input.KeyLeft) && caret == link.end {
				position = link.start
			} else if state.KeyPressed(input.KeyRight) && caret == link.start {
				position = link.end
			}
			if position >= 0 {
				field.Caret = p.source.RuneOffset(position)
				if !state.KeyHeld(input.KeyLeftShift) && !state.KeyHeld(input.KeyRightShift) {
					field.Anchor = field.Caret
				}
				state.SetTextInputSelection(min(field.Anchor, field.Caret), max(field.Anchor, field.Caret))
				return true
			}
		}
		return false
	}
	return false
}

func (p *pencodeInputLinks) conceal(start, end int) {
	p.objects = append(p.objects, scene.TextInlineObject2D{Start: start, End: end, Width: 1, Height: 1})
}

// Completion references have plain labels and URL-escaped destinations. Leave
// incomplete links, images and code as source while the user is composing them.
func parseInputLinks(dst []pencodeInputLink, value string) []pencodeInputLink {
	for offset := 0; offset < len(value); offset++ {
		switch value[offset] {
		case '\\':
			offset++
		case '`':
			end := offset + 1
			for end < len(value) && value[end] == '`' {
				end++
			}
			if next := strings.Index(value[end:], value[offset:end]); next >= 0 {
				length := end - offset
				offset = end + next + length - 1
			} else {
				offset = end - 1
			}
		case '[':
			if offset > 0 && value[offset-1] == '!' {
				continue
			}
			labelEnd := inputLinkEnd(value, offset, '[', ']')
			if labelEnd <= offset+1 || labelEnd+1 >= len(value) || value[labelEnd+1] != '(' {
				continue
			}
			end := inputLinkEnd(value, labelEnd+1, '(', ')')
			if end <= labelEnd+2 || strings.ContainsAny(value[labelEnd+2:end], " \t") {
				continue
			}
			dst = append(dst, pencodeInputLink{offset, labelEnd, end + 1})
			offset = end
		}
	}
	return dst
}

func inputLinkEnd(value string, start int, open, close byte) int {
	depth := 1
	for offset := start + 1; offset < len(value); offset++ {
		switch value[offset] {
		case '\\':
			offset++
		case '\n', '\r':
			return -1
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return offset
			}
		}
	}
	return -1
}
