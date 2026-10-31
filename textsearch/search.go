// Package textsearch shares find/replace semantics between the editor and server.
package textsearch

import (
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Options struct {
	CaseSensitive, WholeWord, Regex, PreserveCase bool
}

type Match struct {
	Start, End int
	groups     []int
}

type Pattern struct {
	query   string
	options Options
	re      *regexp.Regexp
	after   *regexp.Regexp
	literal bool
}

func Compile(query string, options Options) (*Pattern, error) {
	p := &Pattern{query: query, options: options}
	if query == "" || !options.Regex {
		return p, nil
	}
	expression := query
	if !options.CaseSensitive {
		expression = "(?i)" + expression
	}
	var err error
	expression = "(?m)" + expression
	p.re, err = regexp.Compile(expression)
	if err != nil {
		return nil, err
	}
	parsed, err := syntax.Parse(expression, syntax.Perl)
	if err != nil {
		return nil, err
	}
	if parsed.Op == syntax.OpLiteral && !slices.Contains(parsed.Rune, utf8.RuneError) {
		// A literal regexp has the same non-overlapping matches as plain search.
		// Keep the compiled regexp for replacement syntax, without allocating a
		// submatch slice for every occurrence while counting dense results.
		// RuneError stays on the regexp path to also match invalid UTF-8 bytes.
		p.literal, p.query = true, string(parsed.Rune)
		p.options.CaseSensitive = parsed.Flags&syntax.FoldCase == 0
	} else if needsLeftContext(parsed) {
		// Consume the preceding rune, then search lazily from the requested
		// position. Assertions still see that rune and the original input end;
		// an absolute beginning assertion cannot match after the consumed prefix.
		p.after, err = regexp.Compile(`(?s:\A..*?)(` + expression + `)`)
		if err != nil {
			return nil, err
		}
	}
	return p, nil
}

func needsLeftContext(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpBeginLine, syntax.OpBeginText, syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return true
	}
	for _, sub := range re.Sub {
		if needsLeftContext(sub) {
			return true
		}
	}
	return false
}

func (p *Pattern) nextMatch[T string | []byte](content T, position int) []int {
	base, re := position, p.re
	if position > 0 && p.after != nil {
		_, width := decodeLastRune(content[:position])
		base -= width
		re = p.after
	}
	var groups []int
	switch value := any(content).(type) {
	case string:
		groups = re.FindStringSubmatchIndex(value[base:])
	case []byte:
		groups = re.FindSubmatchIndex(value[base:])
	}
	if len(groups) == 0 {
		return nil
	}
	if re == p.after {
		groups = groups[2:] // Discard the wrapper's prefix, retaining original captures.
	}
	for i, offset := range groups {
		if offset >= 0 {
			groups[i] += base
		}
	}
	return groups
}

func word(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

func (p *Pattern) boundary[T string | []byte](content T, start, end int) bool {
	if !p.options.WholeWord {
		return true
	}
	left, _ := decodeLastRune(content[:start])
	right, _ := decodeRune(content[end:])
	return (start == 0 || !word(left)) && (end == len(content) || !word(right))
}

// FindAll reuses caller-owned storage. A negative limit retains every match.
func (p *Pattern) FindAll[T string | []byte](content T, dst []Match, limit int) []Match {
	dst, _ = p.FindRange(content, dst, 0, len(content), limit, false)
	return dst
}

// FindRange keeps document-relative offsets and word/regexp boundary semantics.
// countAll counts matches beyond the storage limit without retaining them.
func (p *Pattern) FindRange[T string | []byte](content T, dst []Match, start, end, limit int, countAll bool) ([]Match, int) {
	clear(dst)
	dst = dst[:0]
	if p == nil || p.query == "" || limit == 0 && !countAll {
		return dst, 0
	}
	count := 0
	accept := func(m Match) bool {
		if m.Start < start || m.End > end || !p.boundary(content, m.Start, m.End) {
			return false
		}
		count++
		if limit < 0 || len(dst) < limit {
			dst = append(dst, m)
		}
		return !countAll && limit > 0 && count == limit
	}
	if p.re != nil && !p.literal {
		for position, previousEnd := 0, -1; position <= end; {
			groups := p.nextMatch(content, position)
			if len(groups) == 0 || groups[0] > end {
				break
			}
			empty := groups[0] == groups[1]
			if (!empty || groups[0] != previousEnd) && accept(Match{groups[0], groups[1], groups}) {
				break
			}
			previousEnd = groups[1]
			if groups[1] > position {
				position = groups[1]
			} else if position < len(content) {
				_, width := decodeRune(content[position:])
				position += width
			} else {
				break
			}
		}
		return dst, count
	}
	for offset := 0; offset < end; {
		first, last := -1, -1
		if p.options.CaseSensitive {
			if i := index(content[offset:end], p.query); i >= 0 {
				first = offset + i
				last = first + len(p.query)
			}
		} else {
			first, last = indexFold(content, p.query, offset, end)
		}
		if first < 0 {
			break
		}
		offset = last
		if accept(Match{Start: first, End: last}) {
			break
		}
	}
	return dst, count
}

// Unicode simple folding includes equivalences with different UTF-8 lengths,
// such as K/K and S/ſ. Neither document nor query needs a lowercase copy.
func indexFold[T string | []byte](content T, query string, start, end int) (int, int) {
	for start < end {
		offset, matched := start, true
		for _, want := range query {
			if offset >= end {
				matched = false
				break
			}
			got, size := decodeRune(content[offset:end])
			if got != want {
				equal := false
				if got < utf8.RuneSelf && want < utf8.RuneSelf {
					equal = got|32 == want|32 && got|32 >= 'a' && got|32 <= 'z'
				} else {
					for fold := unicode.SimpleFold(got); fold != got; fold = unicode.SimpleFold(fold) {
						if fold == want {
							equal = true
							break
						}
					}
				}
				if !equal {
					matched = false
					break
				}
			}
			offset += size
		}
		if matched {
			return start, offset
		}
		_, size := decodeRune(content[start:end])
		start += size
	}
	return -1, -1
}

func (p *Pattern) Replacement(content string, match Match, replacement string) string {
	if p.options.Regex {
		groups := match.groups
		if p.literal {
			bounds := [2]int{match.Start, match.End}
			groups = bounds[:]
		}
		replacement = string(p.re.ExpandString(nil, replacementTemplate(replacement), content, groups))
	}
	if p.options.PreserveCase {
		replacement = preserveCase(content[match.Start:match.End], replacement)
	}
	return replacement
}

func preserveCase(original, replacement string) string {
	lower, upper := strings.ToLower(original), strings.ToUpper(original)
	if lower == upper {
		return replacement
	}
	if original == upper {
		return strings.ToUpper(replacement)
	}
	if original == lower {
		return strings.ToLower(replacement)
	}
	first, size := utf8.DecodeRuneInString(original)
	if unicode.IsUpper(first) && strings.ToLower(original[size:]) == original[size:] && replacement != "" {
		first, size = utf8.DecodeRuneInString(replacement)
		return string(unicode.ToUpper(first)) + strings.ToLower(replacement[size:])
	}
	return replacement
}

// Keep numeric captures distinct from following text, and recognize $$ before $&.
func replacementTemplate(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\\':
			if i+1 < len(value) {
				switch value[i+1] {
				case 'n':
					b.WriteByte('\n')
					i++
					continue
				case 'r':
					b.WriteByte('\r')
					i++
					continue
				case 't':
					b.WriteByte('\t')
					i++
					continue
				case '\\':
					b.WriteByte('\\')
					i++
					continue
				}
			}
		case '$':
			if i+1 < len(value) {
				next := value[i+1]
				if next == '$' {
					b.WriteString("$$")
					i++
					continue
				}
				if next == '&' {
					b.WriteString("${0}")
					i++
					continue
				}
				if next >= '0' && next <= '9' {
					end := i + 2
					for end < len(value) && value[end] >= '0' && value[end] <= '9' {
						end++
					}
					b.WriteString("${")
					b.WriteString(value[i+1 : end])
					b.WriteByte('}')
					i = end - 1
					continue
				}
			}
		}
		b.WriteByte(value[i])
	}
	return b.String()
}

// Replace applies the original ranges once, so inserted matches are never visited.
func (p *Pattern) Replace(content, replacement string, matches []Match) string {
	if len(matches) == 0 {
		return content
	}
	var b strings.Builder
	b.Grow(len(content))
	offset := 0
	template := replacement
	if p.options.Regex {
		template = replacementTemplate(replacement)
	}
	for _, match := range matches {
		b.WriteString(content[offset:match.Start])
		value := replacement
		if p.options.Regex {
			value = string(p.re.ExpandString(nil, template, content, match.groups))
		}
		if p.options.PreserveCase {
			value = preserveCase(content[match.Start:match.End], value)
		}
		b.WriteString(value)
		offset = match.End
	}
	b.WriteString(content[offset:])
	return b.String()
}
