package workbench

import (
	"slices"
	"strings"

	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
	"github.com/diiyw/onix/text/piecetree"
	engineui "github.com/diiyw/onix/ui"
)

// Entries follow source order; Pair indexes the matching entry, or is -1.
type pencodeBracket struct {
	Offset, Pair int
}

type pencodeBracketIndex struct {
	source piecetree.Snapshot
	items  []pencodeBracket
	spans  []scene.TextSpan2D
	paint  pencodeOverlayKey
}

// A paint revision is required as reusable span buffers can change in place.
type pencodeOverlayKey struct {
	source             piecetree.Snapshot
	base               *scene.TextSpan2D
	count              int
	paint, semantic    uint64
	theme              *pencodeTheme
	start, end         int
	linkStart, linkEnd int
	linked             bool
}

func pencodeOverlayPaintKey[V piecetree.Value](model *workspaceModel, inputSource V, base []scene.TextSpan2D, start, end int) pencodeOverlayKey {
	source := piecetree.Text(inputSource)
	key := pencodeOverlayKey{source: source, count: len(base), paint: model.highlightPaintRevision, semantic: model.semanticPaintRevision, theme: model.workbenchTheme, start: start, end: end}
	hover := &model.languageUI.hover
	if hover.link && hover.file == model.activeFile() && hover.content == source {
		key.linked, key.linkStart, key.linkEnd = true, hover.start, hover.end
	}
	if len(base) > 0 {
		key.base = &base[0]
	}
	return key
}

func (index *pencodeBracketIndex) set[V piecetree.Value](source V, items []pencodeBracket) {
	index.source = piecetree.Text(source)
	index.items = append(index.items[:0], items...)
	index.paint = pencodeOverlayKey{}
}

func pencodeBracketMate(character byte) byte {
	switch character {
	case '(':
		return ')'
	case '[':
		return ']'
	case '{':
		return '}'
	}
	return 0
}

func pencodeBracketIgnored(capture string) bool {
	return capture == "escape" || strings.HasPrefix(capture, "escape.") ||
		capture == "character" || strings.HasPrefix(capture, "character.") ||
		capture == "string" || strings.HasPrefix(capture, "string.") ||
		capture == "comment" || strings.HasPrefix(capture, "comment.")
}

// The syntax tokens and source are consumed together, so brackets in strings,
// character literals and comments never affect code nesting.
type pencodeBracketBuilder struct {
	items []pencodeBracket
	stack []int
	token int
}

func (builder *pencodeBracketBuilder) add(source string, tokens []syntax.Token, offset int, character rune) {
	if character != '(' && character != ')' && character != '[' && character != ']' && character != '{' && character != '}' {
		return
	}
	for builder.token < len(tokens) && tokens[builder.token].End <= offset {
		builder.token++
	}
	if builder.token < len(tokens) && tokens[builder.token].Start <= offset && pencodeBracketIgnored(tokens[builder.token].Capture) {
		return
	}
	current := len(builder.items)
	builder.items = append(builder.items, pencodeBracket{Offset: offset, Pair: -1})
	if pencodeBracketMate(byte(character)) != 0 {
		builder.stack = append(builder.stack, current)
		return
	}
	if len(builder.stack) == 0 {
		return
	}
	last := builder.stack[len(builder.stack)-1]
	if pencodeBracketMate(source[builder.items[last].Offset]) != byte(character) {
		// Do not create crossing pairs in incomplete or malformed code.
		builder.stack = builder.stack[:0]
		return
	}
	builder.stack = builder.stack[:len(builder.stack)-1]
	builder.items[last].Pair, builder.items[current].Pair = current, last
}

func (index *pencodeBracketIndex) highlight(field engineui.TextField, model *workspaceModel, base []scene.TextSpan2D) []scene.TextSpan2D {
	if !field.Focused || field.Caret != field.Anchor || field.Composition != "" || index.source != field.Text() || len(index.items) == 0 || model.workbenchTheme == nil {
		return base
	}
	offset := model.activeFile().snapshotBuffer().ByteOffset(field.Caret)
	item, found := slices.BinarySearchFunc(index.items, offset, func(bracket pencodeBracket, offset int) int {
		return bracket.Offset - offset
	})
	if !found {
		item--
		if item < 0 || index.items[item].Offset+1 != offset {
			return base
		}
	}
	pair := index.items[item].Pair
	if pair < 0 {
		return base
	}
	start, end := index.items[item].Offset, index.items[pair].Offset
	if start > end {
		start, end = end, start
	}
	color := model.workbenchTheme.bracket
	key := pencodeOverlayPaintKey(model, field.Text(), base, start, end)
	if key == index.paint {
		return index.spans
	}
	overlay := [...]scene.TextSpan2D{{Start: start, End: start + 1, Color: color}, {Start: end, End: end + 1, Color: color}}
	index.spans = overlayLanguageSpans(index.spans[:0], base, overlay[:])
	index.paint = key
	return index.spans
}
