package workbench

import (
	"fmt"
	"io"
	"strings"

	htmlui "github.com/diiyw/onix/ui/html"
	"golang.org/x/net/html"
)

// Regions retain the surrounding component while replacing their own children.
// Each data-pencode-region element must have a data-ref; nested regions belong
// to their outer region and are replaced with it.
type pencodeExtensionRegion struct {
	ref, html  string
	definition *htmlui.Definition[pencodeExtensionSurfaceProps, struct{}]
}

func splitExtensionRegions(source string) (string, []pencodeExtensionRegion, error) {
	if !strings.Contains(source, "data-pencode-region") {
		return source, nil, nil
	}
	var template strings.Builder
	var regions []pencodeExtensionRegion
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
				regions = append(regions, pencodeExtensionRegion{ref: ref, html: source[start:position]})
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
