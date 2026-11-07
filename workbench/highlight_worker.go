package workbench

import (
	"context"
	"fmt"
	"strings"
	"unsafe"

	"github.com/onix-engine/onix/scene"
	"github.com/onix-engine/onix/syntax"
	"github.com/onix-engine/onix/text/piecetree"
	engineui "github.com/onix-engine/onix/ui"
	"github.com/pencode-labs/pencode/client"
)

type highlightDocument interface {
	UpdateSnapshot(string, piecetree.Snapshot) error
	Tokens(*syntax.Theme, *syntax.Definitions) []syntax.Token
	Close()
}

type pencodeHighlightJob struct {
	theme             *syntax.Theme
	file              *openFile // Identity only; the worker never dereferences UI state.
	sourceDoc         *sourceHighlight
	backend           *client.Client
	definitions       *syntax.Definitions
	documentText      piecetree.Snapshot
	diffBase          string
	path              string
	source            string
	revision          uint64
	lines, characters int
	lineStyles        bool
	counted           bool
	diff              bool
	snapshot          bool
	release           bool
}

type pencodeHighlightResult struct {
	err          error
	diffErr      error
	lineInfo     string
	diffInline   sourceInlineDiffer
	spans        []scene.TextSpan2D
	lineStarts   []int
	sourceStyles []string
	brackets     pencodeBracketBuilder
	job          pencodeHighlightJob
	diffView     sourceDiffView
	lines        int
	characters   int
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
	// Bound both exchanged result buffers, including storage returned by a tab.
	diffBytes := cap(result.diffView.Rows)*int(unsafe.Sizeof(sourceDiffRow{})) + cap(result.diffView.changes)*int(unsafe.Sizeof(sourceInlineChange{})) + cap(result.diffView.Blocks)*int(unsafe.Sizeof(sourceDiffBlock{}))
	for _, layout := range result.diffView.Layouts {
		diffBytes += cap(layout.Offsets) * int(unsafe.Sizeof(int(0)))
	}
	if diffBytes > 1<<20 {
		result.diffView = sourceDiffView{}
	}
	inline := &result.diffInline
	clear(inline.changes)
	inline.changes = inline.changes[:0]
	inlineBytes := cap(inline.matches)*4 + cap(inline.changes)*int(unsafe.Sizeof(sourceInlineChange{}))
	for _, runes := range inline.runes {
		inlineBytes += cap(runes) * int(unsafe.Sizeof(sourceInlineRune{}))
	}
	if inlineBytes > 1<<20 {
		*inline = sourceInlineDiffer{}
	}
	if cap(lineStarts) > pencodeHighlightCacheSpareLimit {
		lineStarts = nil
	}
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
		document.Close()
		// Idle maintenance also drops reusable buffers for oversized documents.
		*result = pencodeHighlightResult{}
		return false
	}
	source := job.documentText
	if job.file == nil {
		source = snapshotText(job.source)
	}
	result.job = job
	if job.snapshot {
		// Immutable panes have their own lexical state; discard the preceding
		// document before processing them and release state after painting.
		document.Close()
		defer document.Close()
	}
	result.err = document.UpdateSnapshot(job.path, source)
	var tokens []syntax.Token
	if result.err == nil {
		tokens = document.Tokens(job.theme, job.definitions)
	}
	if job.counted && job.sourceDoc == nil {
		result.lines, result.characters = job.lines, job.characters
		if result.err == nil {
			for offset := 0; offset < source.Len(); {
				next := indexSnapshotBracket(source, offset)
				if next < 0 {
					break
				}
				offset += next
				result.brackets.add(source, tokens, offset, rune(source.ByteAt(offset)))
				offset++
			}
		}
	} else {
		result.lines = 1
		if job.lineStyles {
			result.lineStarts = append(result.lineStarts, 0)
		}
		offset := 0
		for part := range source.Chunks(0, source.Len()) {
			for local, character := range part {
				position := offset + local
				if job.sourceDoc == nil && result.err == nil {
					result.brackets.add(source, tokens, position, character)
				}
				result.characters++
				if character == '\n' {
					result.lines++
					if job.lineStyles {
						result.lineStarts = append(result.lineStarts, position+1)
					}
				}
			}
			offset += len(part)
		}

	}
	result.lineInfo = fmt.Sprintf("行 %d · %d 字符", result.lines, result.characters)
	if job.diff {
		job.source = source.String()
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
			if job.lineStyles && len(result.spans) > count {
				result.sourceStyles = append(result.sourceStyles, "color:"+pencodeSyntaxColor(token.Style.Foreground).String())
			}
		}
	}
	return true
}

func (model *workspaceModel) submitHighlight(file *openFile) {
	job := pencodeHighlightJob{file: file, path: file.Path, documentText: file.snapshot()}
	if file.buffer.MatchesSnapshot(file.snapshot()) {
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
			document.Close()
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
			file.Path != job.path || file.snapshot() != job.documentText || model.syntaxTheme != job.theme ||
			model.syntaxDefinitions != job.definitions || !model.textEditorActive() || model.highlightPending {
			return
		}
		model.highlightedPath, model.highlightedContent, model.highlighted = job.path, job.documentText, true
		model.highlightScratch, result.spans = result.spans, model.highlightScratch
		index := &model.editor.brackets
		index.source, index.paint = job.documentText, pencodeOverlayKey{}
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
				file: file, source: job.documentText, theme: job.theme, definitions: job.definitions,
				spans: model.highlightScratch, brackets: model.editor.brackets.items, lines: result.lines, characters: result.characters, lineInfo: result.lineInfo,
			})
		}
		// Retire references from the live field before returning the previous
		// paint buffer to the worker, which may start another tokenization immediately.
		if model.fontResources != nil {
			if value := model.fontResources.Mut[scene.Text2D](model.editor.text); value != nil {
				field, _ := model.fontResources.Get[engineui.TextField](model.editor.control)
				model.editor.syncHighlight(value, field, model)
			}
		}
		model.scheduleSyntaxMemoryRelease()
		changed = true
	})
	return changed
}

func (model *workspaceModel) releaseSyntaxWorker() {
	model.highlightRevision++
	model.highlightRequest = pencodeHighlightJob{}
	if model.highlightWorker != nil {
		model.highlightWorker.submit(pencodeHighlightJob{release: true})
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

func indexSnapshotBracket(source piecetree.Snapshot, offset int) int {
	start := offset
	for part := range source.Chunks(offset, source.Len()) {
		if index := strings.IndexAny(part, "()[]{}"); index >= 0 {
			return offset - start + index
		}
		offset += len(part)
	}
	return -1
}
