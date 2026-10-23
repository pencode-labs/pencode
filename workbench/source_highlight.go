package workbench

import (
	"sort"
	"strconv"

	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
	engineui "github.com/diiyw/onix/ui"
)

// Comparison documents share the editor's background parser. Only immutable
// paint data stays with a tab; switching panes never retains additional trees.
type sourceHighlight struct {
	source      string
	theme       *syntax.Theme
	definitions *syntax.Definitions
	ready       bool
	revision    uint64
	spans       []scene.TextSpan2D
	styles      []string
	lineStarts  []int
}

func (t *sourceTab) sourceText(side int) string {
	switch side {
	case 0:
		return t.Local
	case 1:
		if t.editable() {
			return t.Result
		}
		return t.Incoming
	default:
		return t.Result
	}
}

func (m *workspaceModel) renderSourceHighlight() {
	tab := m.sourceState().activeTab()
	if tab == nil || tab.Binary {
		return
	}
	m.ensureSyntaxTheme()
	count := 2
	if tab.Kind == "merge" {
		count = 3
	}
	for side := range count {
		h := &tab.Highlight[side]
		source := tab.sourceText(side)
		if h.ready && h.source == source && h.theme == m.syntaxTheme && h.definitions == m.syntaxDefinitions {
			continue
		}
		job := m.highlightRequest
		if job.sourceDoc != h || job.source != source || job.theme != m.syntaxTheme || job.definitions != m.syntaxDefinitions {
			m.submitHighlightJob(pencodeHighlightJob{sourceDoc: h, path: tab.Path, source: source, diff: side == 1 && tab.editable() && tab.DiffContent != source, diffBase: tab.Local})
		}
		return // The single parser processes the active tab's panes in order.
	}
}

func (m *workspaceModel) acceptSourceHighlight(result *pencodeHighlightResult) bool {
	job := result.job
	tab := m.sourceState().activeTab()
	if tab == nil || job.revision != m.highlightRevision || job.theme != m.syntaxTheme || job.definitions != m.syntaxDefinitions {
		return false
	}
	for side := range tab.Highlight {
		h := &tab.Highlight[side]
		if job.sourceDoc != h || job.source != tab.sourceText(side) {
			continue
		}
		h.revision++
		h.source, h.theme, h.definitions, h.ready = job.source, job.theme, job.definitions, true
		h.spans = append(h.spans[:0], result.spans...)
		h.lineStarts = append(h.lineStarts[:0], result.lineStarts...)
		clear(h.styles)
		h.styles = append(h.styles[:0], result.sourceStyles...)
		if job.diff {
			if result.diffErr == nil {
				previous := tab.diffStorage()
				tab.applyDiffView(result.diffView)
				result.diffView = previous
				result.diffInline.changes = nil
			} else {
				m.sourceState().Notice = result.diffErr.Error()
			}
		}
		m.scheduleSyntaxMemoryRelease()
		return true
	}
	return false
}

// Source line numbers map the aligned diff rows back to original byte offsets,
// preserving multiline comments/strings and UTF-8 through inserted blank rows.
func (h *sourceHighlight) renderLine(b *sourceVNodeView, text string, number int) {
	if !h.ready || number <= 0 || number > len(h.lineStarts) {
		b.content(text)
		return
	}
	start := h.lineStarts[number-1]
	end, offset := start+len(text), start
	first := sort.Search(len(h.spans), func(i int) bool { return h.spans[i].End > start })
	for i := first; i < len(h.spans) && h.spans[i].Start < end; i++ {
		span := h.spans[i]
		from, to := max(offset, span.Start), min(end, span.End)
		if from > offset {
			b.text("span", strconv.Itoa(offset), text[offset-start:from-start])
		}
		if to > from {
			b.text("span", strconv.Itoa(from), text[from-start:to-start], "style", h.styles[i])
		}
		offset = max(offset, to)
	}
	if offset < end {
		b.text("span", strconv.Itoa(offset), text[offset-start:])
	}
}

func (m *workspaceModel) syncSourceMergePaint(world *module.Content) {
	s := m.sourceState()
	tab := s.activeTab()
	if tab == nil || tab.Kind != "merge" || tab.Binary {
		return
	}
	control, ok := world.HTMLControl(m.surface, "sc-merge-result")
	if !ok {
		return
	}
	entity, ok := module.ContentFindPart[engineui.ValuePart, scene.Text2D](world, control)
	if !ok {
		return
	}
	value := module.ContentMut[scene.Text2D](world, entity)
	field, _ := module.ContentGet[engineui.TextField](world, control)
	h := &tab.Highlight[2]
	var spans []scene.TextSpan2D
	if h.ready && h.source == field.Value && h.theme == m.syntaxTheme && h.definitions == m.syntaxDefinitions {
		spans = h.spans
	}
	paint := pencodeOverlayPaintKey(m, field.Value, spans, 0, 0)
	paint.paint = h.revision
	s.mergeEditor.syncPaint(value, field, spans, paint)
}
