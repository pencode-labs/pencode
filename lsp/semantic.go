package lsp

import (
	"fmt"
	"sort"

	"github.com/diiyw/onix/text/piecetree"
)

var SemanticTokenTypes = []string{"namespace", "type", "class", "enum", "interface", "struct", "typeParameter", "parameter", "variable", "property", "enumMember", "event", "function", "method", "macro", "keyword", "modifier", "comment", "string", "number", "regexp", "operator", "decorator"}
var SemanticTokenModifiers = []string{"declaration", "definition", "readonly", "static", "deprecated", "abstract", "async", "modification", "documentation", "defaultLibrary"}

type SemanticTokens struct {
	ResultID string   `json:"resultId,omitempty"`
	Data     []uint32 `json:"data"`
}
type SemanticTokensEdit struct {
	Start       int      `json:"start"`
	DeleteCount int      `json:"deleteCount"`
	Data        []uint32 `json:"data,omitempty"`
}
type SemanticTokensDelta struct {
	ResultID string               `json:"resultId,omitempty"`
	Edits    []SemanticTokensEdit `json:"edits"`
}
type SemanticToken struct {
	Start, End      int
	Type, Modifiers uint32
}

func ApplySemanticDelta(previous []uint32, edits []SemanticTokensEdit) ([]uint32, error) {
	ordered := append([]SemanticTokensEdit(nil), edits...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Start < ordered[j].Start })
	size, last := len(previous), 0
	for _, edit := range ordered {
		if edit.Start < last || edit.Start < 0 || edit.Start > len(previous) || edit.DeleteCount < 0 || edit.DeleteCount > len(previous)-edit.Start {
			return nil, fmt.Errorf("lsp: invalid semantic token delta")
		}
		last = edit.Start + edit.DeleteCount
		size += len(edit.Data) - edit.DeleteCount
	}
	if size < 0 || size > MaxMessage/4 || size%5 != 0 {
		return nil, fmt.Errorf("lsp: invalid semantic token data length")
	}
	next := make([]uint32, 0, size)
	last = 0
	for _, edit := range ordered {
		next = append(next, previous[last:edit.Start]...)
		next = append(next, edit.Data...)
		last = edit.Start + edit.DeleteCount
	}
	return append(next, previous[last:]...), nil
}

// DecodeSemanticTokens resolves relative positions in one forward document scan.
// The client does not advertise overlapping or multiline token support.
func DecodeSemanticTokens[V piecetree.Value](dst []SemanticToken, data []uint32, value V, encoding PositionEncoding, types int) ([]SemanticToken, error) {
	text := piecetree.Text(value)
	dst = dst[:0]
	if len(data)%5 != 0 {
		return dst, fmt.Errorf("lsp: malformed semantic tokens")
	}
	cursor := 0
	position := Position{}
	tokenPosition := Position{}
	advance := func(target Position) (int, error) {
		if comparePosition(target, position) < 0 {
			return 0, fmt.Errorf("lsp: overlapping semantic tokens")
		}
		for comparePosition(position, target) < 0 {
			if cursor >= text.Len() {
				return 0, fmt.Errorf("lsp: semantic token beyond document")
			}
			r, size := text.DecodeRune(cursor)
			if r == '\n' || r == '\r' && cursor+1 < text.Len() && text.ByteAt(cursor+1) == '\n' {
				if position.Line == target.Line {
					return 0, fmt.Errorf("lsp: semantic token beyond line")
				}
				if r == '\r' {
					size++
				}
				position.Line++
				position.Character = 0
			} else {
				position.Character += units(r, size, encoding)
				if position.Line == target.Line && position.Character > target.Character {
					return 0, fmt.Errorf("lsp: semantic token splits character")
				}
			}
			cursor += size
		}
		return cursor, nil
	}
	for i := 0; i < len(data); i += 5 {
		if data[i] > 0 {
			tokenPosition.Line += int(data[i])
			tokenPosition.Character = int(data[i+1])
		} else {
			tokenPosition.Character += int(data[i+1])
		}
		if data[i+2] == 0 || data[i+3] >= uint32(types) {
			return dst, fmt.Errorf("lsp: invalid semantic token")
		}
		start, err := advance(tokenPosition)
		if err != nil {
			return dst, err
		}
		end, err := advance(Position{tokenPosition.Line, tokenPosition.Character + int(data[i+2])})
		if err != nil {
			return dst, err
		}
		dst = append(dst, SemanticToken{start, end, data[i+3], data[i+4]})
	}
	return dst, nil
}
