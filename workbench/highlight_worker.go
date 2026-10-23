package workbench

import (
	"context"
	"fmt"
	"strings"

	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/projects/pencode/client"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
	engineui "github.com/diiyw/onix/ui"
)

type highlightDocument interface {
	Update(string, string) error
	Tokens(*syntax.Theme, *syntax.Definitions) []syntax.Token
	Release(bool)
}

type pencodeHighlightJob struct {
	backend           *client.Client
	file              *openFile // Identity only; the worker never dereferences UI state.
	sourceDoc         *sourceHighlight
	diffBase          string
	diff              bool
	path              string
	source            string
	lines, characters int
	counted           bool
	theme             *syntax.Theme
	definitions       *syntax.Definitions
	revision          uint64
	release           bool
	trim              bool
}

type pencodeHighlightResult struct {
	job          pencodeHighlightJob
	spans        []scene.TextSpan2D
	lineStarts   []int
	sourceStyles []string
	lines        int
	characters   int
	brackets     pencodeBracketBuilder
	lineInfo     string
	err          error
	diffView     sourceDiffView
	diffInline   sourceInlineDiffer
	diffErr      error
}

func resetPencodeHighlightResult(result *pencodeHighlightResult) {
	spans := result.spans[:0]
	lineStarts := result.lineStarts[:0]
	clear(result.sourceStyles)
	styles := result.sourceStyles[:0]
	// Returned diff storage is exclusively owned again. Release document and
	// inline-style references immediately while retaining its reusable capacity.
	clear(result.diffView.Rows)
	clear(result.diffView.changes)
	result.diffView.Rows = result.diffView.Rows[:0]
	result.diffView.changes = result.diffView.changes[:0]
	result.diffView.Blocks = result.diffView.Blocks[:0]
	brackets := pencodeBracketBuilder{items: result.brackets.items[:0], stack: result.brackets.stack[:0]}
	if cap(spans)+cap(brackets.items)+cap(brackets.stack) > pencodeHighlightCacheSpanLimit {
		spans = nil
		styles = nil
		brackets = pencodeBracketBuilder{}
	}
	*result = pencodeHighlightResult{spans: spans, lineStarts: lineStarts, sourceStyles: styles, brackets: brackets, diffView: result.diffView, diffInline: result.diffInline}
}

func processPencodeHighlight(document highlightDocument, job pencodeHighlightJob, result *pencodeHighlightResult) bool {
	if job.release {
		document.Release(job.trim)
		// Idle maintenance also drops reusable buffers for oversized documents.
		*result = pencodeHighlightResult{}
		return false
	}
	result.job = job
	result.err = document.Update(job.path, job.source)
	var tokens []syntax.Token
	if result.err == nil {
		tokens = document.Tokens(job.theme, job.definitions)
	}
	if job.counted && job.sourceDoc == nil {
		result.lines, result.characters = job.lines, job.characters
		if result.err == nil {
			for offset := 0; offset < len(job.source); {
				next := strings.IndexAny(job.source[offset:], "()[]{}")
				if next < 0 {
					break
				}
				offset += next
				result.brackets.add(job.source, tokens, offset, rune(job.source[offset]))
				offset++
			}
		}
	} else {
		result.lines = 1
		if job.sourceDoc != nil {
			result.lineStarts = append(result.lineStarts, 0)
		}
		for offset, character := range job.source {
			if job.sourceDoc == nil && result.err == nil {
				result.brackets.add(job.source, tokens, offset, character)
			}
			result.characters++
			if character == '\n' {
				result.lines++
				if job.sourceDoc != nil {
					result.lineStarts = append(result.lineStarts, offset+1)
				}
			}
		}

	}
	result.lineInfo = fmt.Sprintf("行 %d · %d 字符", result.lines, result.characters)
	if job.diff {
		var patch []byte
		patch, result.diffErr = job.backend.Diff(context.Background(), job.diffBase, job.source)
		if result.diffErr == nil {
			alignSourceDiffInto(&result.diffView, &result.diffInline, job.diffBase, job.source, string(patch))
		}
	}
	if result.err == nil {
		for _, token := range tokens {
			count := len(result.spans)
			result.spans = appendPencodeSpan(result.spans, scene.TextSpan2D{
				Start: token.Start, End: token.End, Color: pencodeSyntaxColor(token.Style.Foreground),
			})
			if job.sourceDoc != nil && len(result.spans) > count {
				result.sourceStyles = append(result.sourceStyles, "color:"+pencodeSyntaxColor(token.Style.Foreground).String())
			}
		}
	}
	return true
}

func (model *workspaceModel) submitHighlight(file *openFile) {
	job := pencodeHighlightJob{file: file, path: file.Path, source: file.Content}
	if file.buffer.Matches(file.Content) {
		job.counted, job.lines, job.characters = true, file.buffer.Lines(), file.buffer.Runes()
	}
	model.submitHighlightJob(job)
}

func (model *workspaceModel) submitHighlightJob(job pencodeHighlightJob) {
	if !model.backendConnected() {
		return
	}
	if model.highlightWorker == nil {
		ctx, cancel := context.WithCancel(context.Background())
		model.highlightCancel = cancel
		document := model.backend.NewSyntax(ctx)
		model.highlightWorker = newLatestWorker(func(job pencodeHighlightJob, result *pencodeHighlightResult) bool {
			return processPencodeHighlight(document, job, result)
		}, resetPencodeHighlightResult, func() {
			document.Release(true)
		}, model.backgroundWake())
	}
	model.highlightRevision++
	job.backend = model.backend
	job.theme, job.definitions, job.revision = model.syntaxTheme, model.syntaxDefinitions, model.highlightRevision
	model.highlightRequest = job
	model.highlightWorker.submit(model.highlightRequest)
}

func (model *workspaceModel) pollHighlight() bool {
	if model.highlightWorker == nil {
		return false
	}
	changed := false
	model.highlightWorker.poll(func(result *pencodeHighlightResult) {
		job := result.job
		if job.revision == model.highlightRevision {
			// Even a rejected result has finished. An edit/undo during debounce
			// may restore the same source and must be allowed to submit it again.
			model.highlightRequest = pencodeHighlightJob{}
		}
		if job.sourceDoc != nil {
			changed = model.acceptSourceHighlight(result) || changed
			return
		}
		file := model.activeFile()
		if job.revision != model.highlightRevision || file == nil || file != job.file ||
			file.Path != job.path || file.Content != job.source || model.syntaxTheme != job.theme ||
			model.syntaxDefinitions != job.definitions || !model.textEditorActive() || model.highlightPending {
			return
		}
		model.highlightedPath, model.highlightedContent, model.highlighted = job.path, job.source, true
		model.highlightScratch, result.spans = result.spans, model.highlightScratch
		index := &model.editor.brackets
		index.source, index.paint = job.source, pencodeOverlayKey{}
		index.items, result.brackets.items = result.brackets.items, index.items
		model.highlightPaintRevision++
		model.lineCount, model.characterCount, model.lineInfo = result.lines, result.characters, result.lineInfo
		if result.err != nil {
			model.status = fmt.Sprintf("语法解析失败：%v", result.err)
		} else {
			if model.highlightCache == nil {
				model.highlightCache = &pencodeHighlightCache{}
			}
			model.highlightCache.put(pencodeHighlightCacheEntry{
				file: file, source: job.source, theme: job.theme, definitions: job.definitions,
				spans: model.highlightScratch, brackets: model.editor.brackets.items, lines: result.lines, characters: result.characters, lineInfo: result.lineInfo,
			})
		}
		// Retire references from the live field before returning the previous
		// paint buffer to the worker, which may start another parse immediately.
		if model.fontResources != nil {
			if value := module.ContentMut[scene.Text2D](model.fontResources, model.editor.text); value != nil {
				field, _ := module.ContentGet[engineui.TextField](model.fontResources, model.editor.control)
				model.editor.syncHighlight(value, field, model)
			}
		}
		model.scheduleSyntaxMemoryRelease()
		changed = true
	})
	return changed
}

func (model *workspaceModel) releaseSyntaxWorker(trim bool) {
	model.highlightRevision++
	model.highlightRequest = pencodeHighlightJob{}
	if model.highlightWorker != nil {
		model.highlightWorker.submit(pencodeHighlightJob{release: true, trim: trim})
	}
}

func (model *workspaceModel) closeHighlightWorker() {
	if model.highlightWorker != nil {
		if model.highlightCancel != nil {
			model.highlightCancel()
		}
		model.highlightWorker.close()
		model.highlightWorker = nil
	}
	model.highlightRequest = pencodeHighlightJob{}
}
