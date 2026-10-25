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
	if query == "" || options.CaseSensitive && !options.Regex {
		return p, nil
	}
	expression := query
	if !options.Regex {
		expression = regexp.QuoteMeta(query)
	}
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

// A negative limit collects every match. Callers reuse dst on query changes.
func (p *Pattern) FindAll(content string, dst []Match, limit int) []Match {
	clear(dst)
	dst = dst[:0]
	if p == nil || p.query == "" || limit == 0 {
		return dst
	}
	if p.re != nil {
		// Word boundaries are checked in Unicode rather than Go's ASCII-only \b.
		n := limit
		if p.options.WholeWord {
			n = -1
		}
		for _, groups := range p.re.FindAllStringSubmatchIndex(content, n) {
			if p.boundary(content, groups[0], groups[1]) {
				dst = append(dst, Match{groups[0], groups[1], groups})
				if limit > 0 && len(dst) == limit {
					break
				}
			}
		}
		return dst
	}
	for offset := 0; offset <= len(content)-len(p.query); {
		i := strings.Index(content[offset:], p.query)
		if i < 0 {
			break
		}
		start := offset + i
		offset = start + len(p.query)
		if p.boundary(content, start, offset) {
			dst = append(dst, Match{Start: start, End: offset})
			if limit > 0 && len(dst) == limit {
				break
			}
		}
	}
	return dst
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
