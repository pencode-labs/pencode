package workbench

import (
	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPencodeOverlayCachesInvalidateReusedBuffers(t *testing.T) {
	const source = "()abc"
	file := &openFile{Path: "main.txt", contentSnapshot: snapshotText(source)}
	model := workspaceModel{files: map[string]*openFile{file.Path: file}, activePath: file.Path, highlightedContent: snapshotText(source), highlightScratch: []scene.TextSpan2D{{Start: 0, End: len(source), Color: graphics.White}}, workbenchTheme: &pencodeTheme{bracket: graphics.ColorFromRGB(255, 0, 0), link: graphics.ColorFromRGB(0, 0, 255)}}
	editor := &model.editor
	editor.brackets.set(source, []pencodeBracket{{Offset: 0, Pair: 1}, {Offset: 1, Pair: 0}})
	field := engineui.TextField{Value: source, Focused: true}
	text := scene.NewText2D(source)
	hover := &model.languageUI.hover
	hover.file, hover.content, hover.link, hover.start, hover.end = file, snapshotText(source), true, 2, 3
	editor.syncHighlight(&text, field, &model)
	if pencodeTestColour(text.Spans, 0) != graphics.ColorFromRGB(255, 0, 0) || pencodeTestColour(text.Spans, 2) != graphics.ColorFromRGB(0, 0, 255) {
		t.Fatal("missing overlays")
	}
	// Same link buffer, pair and source, but a different hovered range.
	hover.start, hover.end = 3, 4
	editor.syncHighlight(&text, field, &model)
	if pencodeTestColour(text.Spans, 2) != graphics.White || pencodeTestColour(text.Spans, 3) != graphics.ColorFromRGB(0, 0, 255) {
		t.Fatal("bracket cache retained old link paint")
	}
	model.highlightScratch[0].Color = graphics.ColorFromRGB(0, 255, 0)
	model.semanticPaintRevision++
	editor.syncHighlight(&text, field, &model)
	if pencodeTestColour(text.Spans, 2) != graphics.ColorFromRGB(0, 255, 0) {
		t.Fatal("semantic result did not invalidate reused base storage")
	}
	model.workbenchTheme = &pencodeTheme{bracket: graphics.ColorFromRGB(0, 0, 255), link: graphics.ColorFromRGB(255, 0, 0)}
	editor.syncHighlight(&text, field, &model)
	if pencodeTestColour(text.Spans, 0) != graphics.ColorFromRGB(0, 0, 255) || pencodeTestColour(text.Spans, 3) != graphics.ColorFromRGB(255, 0, 0) {
		t.Fatal("theme change retained old overlays")
	}
	hover.link = false
	editor.syncHighlight(&text, field, &model)
	if pencodeTestColour(text.Spans, 3) != graphics.ColorFromRGB(0, 255, 0) {
		t.Fatal("removing link retained its color")
	}
	if n := testing.AllocsPerRun(100, func() { editor.syncHighlight(&text, field, &model) }); n != 0 {
		t.Fatalf("stable overlays allocated %g", n)
	}
}

func TestPencodeCompositionCacheUTF8AndPaintRevisions(t *testing.T) {
	source := strings.Repeat("中文🙂\n", 10000)
	var buffer input.TextBuffer
	buffer.Sync(source)
	var editor pencodeEditor
	text := scene.NewText2D(source)
	field := engineui.TextField{Value: source, Source: snapshotText(source), Caret: utf8.RuneCountInString(source) - 1, Anchor: utf8.RuneCountInString(source) - 1, Composition: "输入", Buffer: &buffer}
	spans := []scene.TextSpan2D{{Start: 0, End: len(source), Color: graphics.White}}
	key := pencodeOverlayKey{source: snapshotText(source), base: &spans[0], count: len(spans), paint: 1}
	paint := func() { editor.syncPaint(&text, field, spans, key) }
	paint()
	prefix := buffer.ByteOffset(field.Caret)
	if text.Text().String() != source[:prefix]+field.Composition+source[prefix:] {
		t.Fatal("IME rune caret was treated as a byte offset")
	}
	want := rebasePencodeSpans(nil, spans, prefix, prefix, prefix+len(field.Composition))
	if !slices.Equal(text.Spans, want) {
		t.Fatal("IME spans differ from full rebasing")
	}
	if n := testing.AllocsPerRun(100, paint); n != 0 {
		t.Fatalf("stable IME paint allocated %g", n)
	}
	spans[0].Color = graphics.ColorFromRGB(0, 0, 255)
	key.paint++
	paint()
	if pencodeTestColour(text.Spans, 0) != graphics.ColorFromRGB(0, 0, 255) {
		t.Fatal("IME cache retained stale syntax paint")
	}
	field.Caret--
	field.Anchor = field.Caret
	paint()
	prefix = buffer.ByteOffset(field.Caret)
	if text.Text().String() != source[:prefix]+field.Composition+source[prefix:] {
		t.Fatal("IME cache retained previous caret")
	}
	field.Composition = ""
	paint()
	if text.Text().String() != source || !slices.Equal(text.Spans, spans) || editor.compositionSource.Len() != 0 {
		t.Fatal("IME commit retained preedit cache")
	}
}

func BenchmarkPencodeCachedCompositionPaint(b *testing.B) {
	source := strings.Repeat("中文🙂\n", 50000)
	var editor pencodeEditor
	var text scene.Text2D
	field := engineui.TextField{Value: source, Caret: utf8.RuneCountInString(source) - 1, Anchor: utf8.RuneCountInString(source) - 1, Composition: "输入"}
	spans := []scene.TextSpan2D{{Start: 0, End: len(source), Color: graphics.White}}
	key := pencodeOverlayKey{source: snapshotText(source), base: &spans[0], count: 1}
	editor.syncPaint(&text, field, spans, key)
	b.ReportAllocs()
	for b.Loop() {
		editor.syncPaint(&text, field, spans, key)
	}
}
