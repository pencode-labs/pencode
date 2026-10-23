package lsp

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

type editOffset struct {
	TextEdit
	start, end, order int
}

func comparePosition(a, b Position) int {
	if a.Line < b.Line || a.Line == b.Line && a.Character < b.Character {
		return -1
	}
	if a == b {
		return 0
	}
	return 1
}

// ApplyTextEdits validates the entire edit set before creating a new string.
// All endpoints are resolved in a single forward scan. Sorting costs O(m log m)
// for m edits, and scanning/copying costs O(n) for n document bytes.
func ApplyTextEdits(content string, edits []TextEdit, encoding PositionEncoding) (string, error) {
	if len(edits) == 0 {
		return content, nil
	}
	offsets := make([]editOffset, len(edits))
	for i, edit := range edits {
		if edit.Range.Start.Line < 0 || edit.Range.Start.Character < 0 || comparePosition(edit.Range.End, edit.Range.Start) < 0 || !utf8.ValidString(edit.NewText) {
			return "", fmt.Errorf("lsp: invalid text edit")
		}
		offsets[i] = editOffset{TextEdit: edit, order: i}
	}
	sort.Slice(offsets, func(i, j int) bool {
		a, b := offsets[i], offsets[j]
		if order := comparePosition(a.Range.Start, b.Range.Start); order != 0 {
			return order < 0
		}
		if order := comparePosition(a.Range.End, b.Range.End); order != 0 {
			return order < 0
		}
		return a.order < b.order
	})
	cursor := 0
	var position Position
	advance := func(target Position) (int, error) {
		if comparePosition(target, position) < 0 {
			return 0, fmt.Errorf("lsp: overlapping text edits")
		}
		for comparePosition(position, target) < 0 {
			if cursor >= len(content) {
				return 0, fmt.Errorf("lsp: position beyond document")
			}
			r, size := utf8.DecodeRuneInString(content[cursor:])
			if r == '\n' || r == '\r' && cursor+1 < len(content) && content[cursor+1] == '\n' {
				if position.Line == target.Line {
					return 0, fmt.Errorf("lsp: position beyond line")
				}
				if r == '\r' {
					size++
				}
				position.Line++
				position.Character = 0
			} else {
				position.Character += units(r, size, encoding)
				if position.Line == target.Line && position.Character > target.Character {
					return 0, fmt.Errorf("lsp: position splits a character")
				}
			}
			cursor += size
		}
		return cursor, nil
	}
	size := len(content)
	for i := range offsets {
		edit := &offsets[i]
		var err error
		edit.start, err = advance(edit.Range.Start)
		if err != nil {
			return "", err
		}
		edit.end, err = advance(edit.Range.End)
		if err != nil {
			return "", err
		}
		size += len(edit.NewText) - (edit.end - edit.start)
	}
	if size < 0 || size > MaxMessage {
		return "", fmt.Errorf("lsp: edited document too large")
	}
	var out strings.Builder
	out.Grow(size)
	last := 0
	for _, edit := range offsets {
		out.WriteString(content[last:edit.start])
		out.WriteString(edit.NewText)
		last = edit.end
	}
	out.WriteString(content[last:])
	return out.String(), nil
}

// IncrementalChange computes one minimal replacement, without allocating or
// splitting UTF-8 runes. It also handles changes to line endings.
func IncrementalChange(old, next string, encoding PositionEncoding) (Range, string) {
	prefix := 0
	for prefix < len(old) && prefix < len(next) && old[prefix] == next[prefix] {
		prefix++
	}
	for prefix > 0 && (prefix < len(old) && !utf8.RuneStart(old[prefix]) || prefix < len(next) && !utf8.RuneStart(next[prefix])) {
		prefix--
	}
	// Keep CRLF atomic so neither endpoint points between CR and LF.
	if prefix > 0 && (prefix < len(old) && old[prefix-1] == '\r' && old[prefix] == '\n' || prefix < len(next) && next[prefix-1] == '\r' && next[prefix] == '\n') {
		prefix--
	}
	a, b := len(old), len(next)
	for a > prefix && b > prefix && old[a-1] == next[b-1] {
		a--
		b--
	}
	for a < len(old) && !utf8.RuneStart(old[a]) {
		a++
		b++
	}
	if a > 0 && a < len(old) && old[a-1] == '\r' && old[a] == '\n' {
		a++
		b++
	}
	start, _ := PositionAt(old, prefix, encoding)
	end, _ := PositionAt(old, a, encoding)
	return Range{start, end}, next[prefix:b]
}
