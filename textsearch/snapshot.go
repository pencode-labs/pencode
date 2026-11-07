package textsearch

import (
	"io"
	"unicode"
	"unicode/utf8"

	"github.com/onix-engine/onix/text/piecetree"
)

type snapshotReader struct {
	source piecetree.Snapshot
	offset int
}

func (r *snapshotReader) ReadRune() (rune, int, error) {
	if r.offset == r.source.Len() {
		return 0, 0, io.EOF
	}
	value, width := r.source.DecodeRune(r.offset)
	r.offset += width
	return value, width, nil
}

func snapshotLastRune(source piecetree.Snapshot, end int) (rune, int) {
	var data [utf8.UTFMax]byte
	n := min(end, len(data))
	source.ReadAt(data[:n], int64(end-n))
	return utf8.DecodeLastRune(data[:n])
}

// FindSnapshot searches immutable pieces without materializing the document.
// Regexp readers preserve whole-document assertion and submatch semantics.
func (p *Pattern) FindSnapshot(content piecetree.Snapshot, dst []Match, start, end, limit int) []Match {
	clear(dst)
	dst = dst[:0]
	if p == nil || p.query == "" || limit == 0 {
		return dst
	}
	accept := func(match Match) bool {
		if match.Start < start || match.End > end {
			return false
		}
		if p.options.WholeWord {
			left, _ := snapshotLastRune(content, match.Start)
			right, _ := content.DecodeRune(match.End)
			if match.Start > 0 && word(left) || match.End < content.Len() && word(right) {
				return false
			}
		}
		dst = append(dst, match)
		return limit > 0 && len(dst) == limit
	}
	if p.re != nil && !p.literal {
		for position, previousEnd := 0, -1; position <= end; {
			base, re := position, p.re
			if position > 0 && p.after != nil {
				_, width := snapshotLastRune(content, position)
				base -= width
				re = p.after
			}
			reader := snapshotReader{source: content, offset: base}
			groups := re.FindReaderSubmatchIndex(&reader)
			if len(groups) == 0 {
				break
			}
			if re == p.after {
				groups = groups[2:]
			}
			for i, offset := range groups {
				if offset >= 0 {
					groups[i] += base
				}
			}
			if groups[0] > end {
				break
			}
			empty := groups[0] == groups[1]
			if (!empty || groups[0] != previousEnd) && accept(Match{Start: groups[0], End: groups[1], groups: groups}) {
				break
			}
			previousEnd = groups[1]
			if groups[1] > position {
				position = groups[1]
			} else if position < content.Len() {
				_, width := content.DecodeRune(position)
				position += width
			} else {
				break
			}
		}
		return dst
	}
	for offset := 0; offset < end; {
		first, last := p.snapshotIndex(content, offset, end)
		if first < 0 {
			break
		}
		offset = last
		if accept(Match{Start: first, End: last}) {
			break
		}
	}
	return dst
}

func (p *Pattern) snapshotIndex(content piecetree.Snapshot, start, end int) (int, int) {
	for start < end {
		if p.options.CaseSensitive {
			start = content.IndexByte(start, p.query[0])
			if start < 0 || start+len(p.query) > end {
				break
			}
			matched, offset := true, 0
			for part := range content.Chunks(start, start+len(p.query)) {
				if part != p.query[offset:offset+len(part)] {
					matched = false
					break
				}
				offset += len(part)
			}
			if matched {
				return start, start + len(p.query)
			}
			start++
			continue
		}
		offset, matched := start, true
		for _, want := range p.query {
			if offset >= end {
				matched = false
				break
			}
			got, width := content.DecodeRune(offset)
			if offset+width > end {
				matched = false
				break
			}
			if got != want {
				equal := false
				for fold := unicode.SimpleFold(got); fold != got; fold = unicode.SimpleFold(fold) {
					if fold == want {
						equal = true
						break
					}
				}
				if !equal {
					matched = false
					break
				}
			}
			offset += width
		}
		if matched {
			return start, offset
		}
		_, width := content.DecodeRune(start)
		start += width
	}
	return -1, -1
}
