package workbench

import (
	"context"
	"strconv"

	"github.com/diiyw/onix/syntax"
	"github.com/pencode-labs/pencode/protocol"
)

const folderSearchPageSize = 12

type folderPreviewSource struct{ path, text string }
type folderPreviewJob struct {
	rows         [folderSearchPageSize]folderPreviewSource
	theme        *syntax.Theme
	definitions  *syntax.Definitions
	revision     uint64
	first, count int
}
type folderPreviewResult struct {
	job  folderPreviewJob
	rows [folderSearchPageSize][]syntax.Token
}
type folderSearchHighlight struct {
	worker  *latestWorker[folderPreviewJob, folderPreviewResult]
	cancel  context.CancelFunc
	request folderPreviewJob
	rows    [folderSearchPageSize][]syntax.Token
	ready   bool
}

func (h *folderSearchHighlight) tokens(row int) []syntax.Token {
	if h.ready {
		return h.rows[row]
	}
	return nil
}

func (m *workspaceModel) highlightFolderSearch() {
	s := &m.folderSearch
	h := &s.preview
	if len(s.rows) == 0 {
		h.request, h.ready = folderPreviewJob{}, false
		return
	}
	m.ensureSyntaxTheme()
	first := s.index / folderSearchPageSize * folderSearchPageSize
	job := folderPreviewJob{theme: m.syntaxTheme, definitions: m.syntaxDefinitions, revision: s.revision, first: first, count: min(folderSearchPageSize, len(s.rows)-first)}
	for i := range job.count {
		row := s.rows[first+i]
		file := &s.result.Files[row.file]
		job.rows[i] = folderPreviewSource{file.Path, file.Matches[row.match].Preview}
	}
	if job == h.request || !m.backendConnected() {
		return
	}
	if h.worker == nil {
		ctx, cancel := context.WithCancel(context.Background())
		h.cancel = cancel
		document := m.backend.NewSyntax(ctx)
		h.worker = newLatestWorker(func(job folderPreviewJob, result *folderPreviewResult) bool {
			result.job = job
			for i := range job.count {
				if ctx.Err() != nil {
					return false
				}
				row := job.rows[i]
				if document.Update(row.path, row.text) == nil {
					result.rows[i] = append(result.rows[i], document.Tokens(job.theme, job.definitions)...)
				}
			}
			// Only bounded paint data survives between pages; release the parser.
			document.Release(false)
			return true
		}, func(result *folderPreviewResult) {
			result.job = folderPreviewJob{}
			for i := range result.rows {
				clear(result.rows[i])
				result.rows[i] = result.rows[i][:0]
			}
		}, document.Close, m.backgroundWake())
	}
	h.request, h.ready, s.dirty = job, false, true
	h.worker.submit(job)
}

func (m *workspaceModel) pollFolderSearchHighlight() bool {
	s := &m.folderSearch
	h := &s.preview
	if h.worker == nil {
		return false
	}
	changed := false
	h.worker.poll(func(result *folderPreviewResult) {
		job := &result.job
		if !s.open || *job != h.request || job.revision != s.revision || job.first != s.index/folderSearchPageSize*folderSearchPageSize || job.theme != m.syntaxTheme || job.definitions != m.syntaxDefinitions {
			return
		}
		h.rows, result.rows = result.rows, h.rows
		h.ready, s.dirty, changed = true, true, true
	})
	return changed
}

func (m *workspaceModel) closeFolderSearchHighlight() {
	h := &m.folderSearch.preview
	if h.worker != nil {
		h.cancel()
		h.worker.close()
	}
	*h = folderSearchHighlight{}
}

// Split at both syntax and match boundaries, using UTF-8 byte offsets from
// the search result so repeated words and clipped lines mark the exact match.
func renderFolderSearchPreview(b *sourceVNodeView, match protocol.TextMatch, tokens []syntax.Token) {
	text := match.Preview
	start := max(0, match.Start-match.PreviewStart)
	end := min(len(text), match.End-match.PreviewStart)
	for offset, token := 0, 0; offset < len(text); {
		for token < len(tokens) && tokens[token].End <= offset {
			token++
		}
		next, css := len(text), ""
		if token < len(tokens) {
			if tokens[token].Start > offset {
				next = min(next, tokens[token].Start)
			} else {
				next, css = min(next, tokens[token].End), tokens[token].CSS
			}
		}
		if offset < start {
			next = min(next, start)
		} else if offset < end {
			next = min(next, end)
		}
		b.open("span", strconv.Itoa(offset))
		if css != "" {
			b.attr("style", css)
		}
		if offset >= start && offset < end {
			b.attr("class", "pencode-folder-match")
		}
		b.content(text[offset:next])
		b.close()
		offset = next
	}
}
