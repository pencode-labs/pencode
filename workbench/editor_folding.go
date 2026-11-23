package workbench

import (
	"cmp"
	"slices"
	"sort"
	"time"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	"github.com/onix-engine/onix/text/piecetree"
)

// Fold headers and inclusive ends use LSP's zero-based source line numbers.
type editorFoldRange struct {
	Start int `json:"startLine"`
	End   int `json:"endLine"`
}

type editorFolds struct {
	source            piecetree.Snapshot
	ranges, collapsed []editorFoldRange
	layout            scene.TextLineLayout2D
	ready             bool
}

func foldAt(ranges []editorFoldRange, line int) (int, bool) {
	return slices.BinarySearchFunc(ranges, line, func(r editorFoldRange, line int) int { return cmp.Compare(r.Start, line) })
}

// Keep one outermost range per header and only disjoint or nested regions.
func normalizeFoldRanges(ranges []editorFoldRange, lines int) []editorFoldRange {
	slices.SortFunc(ranges, func(a, b editorFoldRange) int {
		if a.Start == b.Start {
			return cmp.Compare(b.End, a.End)
		}
		return cmp.Compare(a.Start, b.Start)
	})
	var ends [32]int
	stack := ends[:0]
	result := ranges[:0]
	for _, r := range ranges {
		if r.Start < 0 || r.End <= r.Start || r.End >= lines || len(result) > 0 && result[len(result)-1].Start == r.Start {
			continue
		}
		for len(stack) > 0 && stack[len(stack)-1] < r.Start {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 && r.End > stack[len(stack)-1] {
			continue
		}
		stack = append(stack, r.End)
		result = append(result, r)
	}
	return result
}

func (f *openFile) foldState() *editorFolds {
	if f.folds == nil || f.folds.source != f.snapshot() {
		f.folds = &editorFolds{source: f.snapshot()}
	}
	return f.folds
}

func (s *editorFolds) setRanges(ranges []editorFoldRange, buffer *input.TextBuffer) {
	s.ranges = append(s.ranges[:0], ranges...)
	kept := s.collapsed[:0]
	for _, r := range s.collapsed {
		if i, ok := foldAt(ranges, r.Start); ok {
			kept = append(kept, ranges[i])
		}
	}
	s.collapsed, s.ready = kept, true
	s.rebuild(buffer)
}

func (s *editorFolds) rebuild(buffer *input.TextBuffer) {
	hidden := s.layout.Hidden[:0]
	last := -1
	for _, r := range s.collapsed {
		if r.Start <= last {
			continue
		}
		last = r.End
		end := s.source.Len() + 1
		if r.End+1 < buffer.Lines() {
			end = buffer.LineStart(r.End + 1)
		}
		hidden = append(hidden, scene.TextHiddenLines2D{Start: buffer.LineStart(r.Start + 1), End: end})
	}
	s.layout.Hidden = hidden
	s.layout.Revision++
}

func (s *editorFolds) toggle(line int, buffer *input.TextBuffer) bool {
	i, ok := foldAt(s.ranges, line)
	if !ok {
		return false
	}
	index, closed := foldAt(s.collapsed, line)
	if closed {
		s.collapsed = slices.Delete(s.collapsed, index, index+1)
	} else {
		s.collapsed = slices.Insert(s.collapsed, index, s.ranges[i])
	}
	s.rebuild(buffer)
	return true
}

func (s *editorFolds) hiddenAt(offset int) (scene.TextHiddenLines2D, bool) {
	i := sort.Search(len(s.layout.Hidden), func(i int) bool { return s.layout.Hidden[i].End > offset })
	if i < len(s.layout.Hidden) && s.layout.Hidden[i].Start <= offset {
		return s.layout.Hidden[i], true
	}
	return scene.TextHiddenLines2D{}, false
}

func (s *editorFolds) reveal(buffer *input.TextBuffer, caret int) {
	if len(s.layout.Hidden) == 0 {
		return
	}
	offset := buffer.ByteOffset(caret)
	if _, ok := s.hiddenAt(offset); !ok {
		return
	}
	line, _ := buffer.Position(offset)
	s.collapsed = slices.DeleteFunc(s.collapsed, func(r editorFoldRange) bool { return r.Start < line && line <= r.End })
	s.rebuild(buffer)
}

// Keep unaffected headers usable during debounce and move later regions by the
// edited line count. An edit intersecting a region opens it for recomputation.
func (s *editorFolds) edit(value piecetree.Snapshot, start, oldEnd int, buffer *input.TextBuffer) {
	if len(s.ranges) == 0 {
		s.source, s.ready = value, false
		return
	}
	first, _ := s.source.Position(start)
	last, _ := s.source.Position(oldEnd)
	delta := buffer.Lines() - s.source.Lines()
	beforeHeader := oldEnd == s.source.LineStart(last) && (start != oldEnd || delta > 0)
	shift := func(ranges []editorFoldRange) []editorFoldRange {
		kept := ranges[:0]
		for _, r := range ranges {
			if r.Start > last || r.Start == last && beforeHeader {
				r.Start += delta
				r.End += delta
			} else if r.End >= first {
				continue
			}
			kept = append(kept, r)
		}
		return kept
	}
	s.ranges, s.collapsed = shift(s.ranges), shift(s.collapsed)
	s.source, s.ready = value, false
	s.rebuild(buffer)
}

type indentFoldJob struct {
	file   *openFile
	source piecetree.Snapshot
}

type indentFoldLevel struct{ indent, index int }

type indentFoldResult struct {
	indentFoldJob
	ranges []editorFoldRange
	stack  []indentFoldLevel
}

func computeIndentFolds(job indentFoldJob, result *indentFoldResult) bool {
	result.indentFoldJob = job
	source := job.source
	previous, previousIndent, header := -1, 0, -1
	for start, line := 0, 0; start < source.Len(); line++ {
		end := source.IndexByte(start, '\n')
		if end < 0 {
			end = source.Len()
		}
		indent, pos := 0, start
		for pos < end {
			c := source.ByteAt(pos)
			if c == ' ' {
				indent++
			} else if c == '\t' {
				indent += len(pencodeIndent) - indent%len(pencodeIndent)
			} else {
				break
			}
			pos++
		}
		if pos < end && source.ByteAt(pos) != '\r' {
			for n := len(result.stack); n > 0 && result.stack[n-1].indent >= indent; n-- {
				result.ranges[result.stack[n-1].index].End = previous
				result.stack = result.stack[:n-1]
			}
			if previous >= 0 && indent > previousIndent {
				result.stack = append(result.stack, indentFoldLevel{previousIndent, len(result.ranges)})
				result.ranges = append(result.ranges, editorFoldRange{Start: header})
			}
			header = line
			// Allman-style declarations put the opening brace on its own line.
			if previous >= 0 && indent == previousIndent && source.ByteAt(pos) == '{' && (end-pos == 1 || end-pos == 2 && source.ByteAt(pos+1) == '\r') {
				header = previous
			}
			previous, previousIndent = line, indent
		}
		start = end + 1
	}
	for _, level := range result.stack {
		result.ranges[level.index].End = previous
	}
	return true
}

func (e *pencodeEditor) syncFolds(m *workspaceModel, text *scene.Text2D, file *openFile, caret, anchor int, composing bool) {
	text.LineLayout = nil
	if !composing {
		e.foldPreedit = piecetree.Snapshot{}
	}
	if file.page.Paged {
		return
	}
	state := file.foldState()
	waiting := false
	if m.languages != nil {
		if doc := m.languages.documents[file]; doc != nil {
			waiting = doc.foldPending && doc.content == state.source
		}
	}
	if e.foldJob.file != nil {
		e.foldWorker.poll(func(r *indentFoldResult) {
			if r.file == file && r.source == state.source && !state.ready && !waiting {
				state.setRanges(r.ranges, file.snapshotBuffer())
			}
			if r.indentFoldJob == e.foldJob {
				e.foldJob = indentFoldJob{}
			}
		})
	}
	if !state.ready && !waiting && state.source.Len() > 0 && (!m.highlightPending || !time.Now().Before(m.highlightDue)) {
		job := indentFoldJob{file, state.source}
		if job != e.foldJob {
			if e.foldWorker == nil {
				e.foldWorker = newLatestWorker(computeIndentFolds, func(r *indentFoldResult) {
					r.indentFoldJob = indentFoldJob{}
					r.ranges, r.stack = r.ranges[:0], r.stack[:0]
				}, nil, m.backgroundWake())
			}
			e.foldJob = job
			e.foldWorker.submit(job)
		}
	}
	if len(state.layout.Hidden) == 0 {
		return
	}
	buffer := file.snapshotBuffer()
	state.reveal(buffer, caret)
	if caret != anchor {
		state.reveal(buffer, anchor)
	}
	if len(state.layout.Hidden) == 0 {
		return
	}
	text.LineLayout = &state.layout
	if composing {
		// IME preedit replaces the selection only in the painted snapshot.
		// Translate folds after it and omit folds covered by that selection.
		layout := &e.compositionFolds
		if e.foldPreedit != text.Text() || layout.Revision != state.layout.Revision {
			start, end := buffer.ByteOffset(min(caret, anchor)), buffer.ByteOffset(max(caret, anchor))
			delta := text.Text().Len() - state.source.Len()
			layout.Hidden = layout.Hidden[:0]
			for _, h := range state.layout.Hidden {
				if h.Start >= end {
					h.Start += delta
					h.End += delta
				} else if h.End > start {
					continue
				}
				layout.Hidden = append(layout.Hidden, h)
			}
			layout.Revision = state.layout.Revision
			e.foldPreedit = text.Text()
		}
		text.LineLayout = layout
	}
}
