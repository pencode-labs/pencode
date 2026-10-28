// Package textsearch shares find/replace semantics between the editor and server.
package textsearch

import (
	"regexp"
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
	p.re, err = regexp.Compile("(?m)" + expression)
	if err != nil {
		return nil, err
	}
	return p, err
}

func word(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

func (p *Pattern) boundary(content string, start, end int) bool {
	if !p.options.WholeWord {
		return true
	}
	left, _ := utf8.DecodeLastRuneInString(content[:start])
	right, _ := utf8.DecodeRuneInString(content[end:])
	return (start == 0 || !word(left)) && (end == len(content) || !word(right))
}

// FindAll reuses caller-owned storage. A negative limit retains every match.
func (p *Pattern) FindAll(content string, dst []Match, limit int) []Match {
	dst, _ = p.FindRange(content, dst, 0, len(content), limit, false)
	return dst
}

// FindRange keeps document-relative offsets and word/regexp boundary semantics.
// countAll counts matches beyond the storage limit without retaining them.
func (p *Pattern) FindRange(content string, dst []Match, start, end, limit int, countAll bool) ([]Match, int) {
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
	if p.re != nil {
		// Keep the original input for anchors, empty matches and capture offsets.
		// Increase the candidate budget only when filtering requires more matches.
		budget := limit
		if countAll || limit < 0 {
			budget = -1
		}
		visited := 0
		for {
			candidates := p.re.FindAllStringSubmatchIndex(content, budget)
			for _, groups := range candidates[visited:] {
				if groups[0] > end {
					return dst, count
				}
				if accept(Match{groups[0], groups[1], groups}) {
					return dst, count
				}
			}
			if budget < 0 || len(candidates) < budget {
				return dst, count
			}
			visited = len(candidates)
			budget *= 2
		}
	}
	for offset := 0; offset < end; {
		first, last := -1, -1
		if p.options.CaseSensitive {
			if i := strings.Index(content[offset:end], p.query); i >= 0 {
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
func indexFold(content, query string, start, end int) (int, int) {
	for start < end {
		offset, matched := start, true
		for _, want := range query {
			if offset >= end {
				matched = false
				break
			}
			got, size := utf8.DecodeRuneInString(content[offset:end])
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
		_, size := utf8.DecodeRuneInString(content[start:end])
		start += size
	}
	return -1, -1
}

func (p *Pattern) Replacement(content string, match Match, replacement string) string {
	if p.options.Regex {
		replacement = string(p.re.ExpandString(nil, replacementTemplate(replacement), content, match.groups))
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
