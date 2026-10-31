package peb

import (
	"fmt"
	"io"
	"strings"

	"github.com/pencode-labs/pencode/textchange"
	"golang.org/x/net/html"
)

// ViewRegion replaces the children of an existing data-pencode-region element.
type ViewRegion struct {
	Ref, HTML  string
	start, end int
	tag        string
}

// ViewPatch is relative to exactly one previously received view version.
// Nested regions belong to their outer region and cannot be patched separately.
type ViewPatch struct {
	Base    uint32
	Regions []ViewRegion
}

// ViewRegionSplitter reuses region boundaries when an update changes only one
// region. The returned region slice is borrowed until the next successful Split.
// Strings remain immutable. A splitter must not be used concurrently.
type ViewRegionSplitter struct {
	source, template string
	regions          []ViewRegion
}

func (s *ViewRegionSplitter) Split(source string) (string, []ViewRegion, error) {
	if source == s.source {
		return s.template, s.regions, nil
	}
	start, oldEnd, newEnd := textchange.Between(s.source, source)
	shift := newEnd - oldEnd
	for _, region := range s.regions {
		if start < region.start || oldEnd > region.end || !validRegionHTML(source[region.start:region.end+shift], region.tag) {
			continue
		}
		for i := range s.regions {
			r := &s.regions[i]
			if r.start > region.end {
				r.start += shift
				r.end += shift
			} else if r.start == region.start {
				r.end += shift
			}
			r.HTML = source[r.start:r.end]
		}
		s.source = source
		return s.template, s.regions, nil
	}
	template, regions, err := SplitViewRegions(source)
	if err != nil {
		return "", nil, err
	}
	s.source, s.template, s.regions = source, template, regions
	return template, regions, nil
}

// SplitViewRegions separates outer region contents from their surrounding
// template. Nested regions remain part of their outer region's HTML.
func SplitViewRegions(source string) (string, []ViewRegion, error) {
	if !strings.Contains(source, "data-pencode-region") {
		return source, nil, nil
	}
	var template strings.Builder
	var regions []ViewRegion
	var tag, ref string
	depth, offset, start, copied := 0, 0, 0, 0
	tokens := html.NewTokenizer(strings.NewReader(source))
	for {
		kind := tokens.Next()
		position := offset
		offset += len(tokens.Raw())
		if kind == html.ErrorToken {
			if tokens.Err() != io.EOF {
				return "", nil, tokens.Err()
			}
			if depth != 0 {
				return "", nil, fmt.Errorf("扩展 UI 区域未闭合：%s", ref)
			}
			break
		}
		if kind != html.StartTagToken && kind != html.EndTagToken {
			continue
		}
		name, hasAttr := tokens.TagName()
		if depth != 0 {
			if string(name) == tag {
				if kind == html.StartTagToken {
					depth++
				} else {
					depth--
				}
			}
			if depth == 0 {
				regions = append(regions, ViewRegion{Ref: ref, HTML: source[start:position], start: start, end: position, tag: tag})
				copied = position
			}
			continue
		}
		if kind != html.StartTagToken {
			continue
		}
		region := false
		var refValue []byte
		for hasAttr {
			var key, value []byte
			key, value, hasAttr = tokens.TagAttr()
			switch string(key) {
			case "data-pencode-region":
				region = true
			case "data-ref":
				refValue = value
			}
		}
		if !region {
			continue
		}
		ref = string(refValue)
		if ref == "" {
			return "", nil, fmt.Errorf("扩展 UI 区域缺少 data-ref")
		}
		template.WriteString(source[copied:offset])
		tag, depth, start = string(name), 1, offset
	}
	if len(regions) == 0 {
		return source, nil, nil
	}
	template.WriteString(source[copied:])
	return template.String(), regions, nil
}

// Match the outer-container rules used by SplitViewRegions without scanning
// unchanged history. A fragment cannot close or leave open its own container.
func validRegionHTML(source, tag string) bool {
	if !strings.Contains(source, "<") {
		return true
	}
	tokens := html.NewTokenizer(strings.NewReader(source))
	var local [16]string
	stack := append(local[:0], tag)
	for {
		kind := tokens.Next()
		if kind == html.ErrorToken {
			return tokens.Err() == io.EOF && len(stack) == 1
		}
		if kind != html.StartTagToken && kind != html.EndTagToken && kind != html.SelfClosingTagToken {
			continue
		}
		name, _ := tokens.TagName()
		if kind == html.EndTagToken {
			if len(stack) == 1 || stack[len(stack)-1] != string(name) {
				return false
			}
			stack = stack[:len(stack)-1]
			continue
		}
		switch string(name) {
		case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
			continue
		}
		// Optional HTML end tags and non-void self-closing syntax require the
		// complete parser context; use a full view for those fragments.
		if kind == html.SelfClosingTagToken {
			return false
		}
		stack = append(stack, string(name))
	}
}
