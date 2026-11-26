package extensions

import (
	"sync"

	"github.com/onix-engine/onix/html/markup"
	"github.com/onix-engine/onix/html/parse"
	"github.com/onix-engine/onix/scene/node"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/peb"
	"github.com/pencode-labs/pencode/textchange"
)

// Raw and prepared ranges can differ when surrounding controls acquire IDs.
type preparedRegion struct {
	ref                                string
	images                             []regionImage
	start, end, outputStart, outputEnd int
	depth, focusStart, focusEnd        int
}

type regionImage struct{ source, uri string }

var regionParsers = sync.Pool{New: func() any { return new(parse.Parser) }}

// Flow containers in a body fragment give their contents the same parser
// context as an independent fragment. Tables, forms, foreign namespaces and
// formatting elements need the surrounding parser state.
func flowContainer(tag string) bool {
	switch tag {
	case "div", "section", "article", "main", "aside", "header", "footer", "nav":
		return true
	}
	return false
}

func editableRegions(source string) ([]preparedRegion, map[string]*preparedRegion) {
	_, parts, err := peb.SplitViewRegions(source)
	if err != nil || len(parts) == 0 {
		return nil, nil
	}
	regions := make([]preparedRegion, len(parts))
	byRef := make(map[string]*preparedRegion, len(parts))
	for i, part := range parts {
		if byRef[part.Ref] != nil {
			return nil, nil
		}
		start, end := part.Bounds()
		regions[i] = preparedRegion{ref: part.Ref, start: start, end: end, outputStart: start, outputEnd: end, depth: -1}
		byRef[part.Ref] = &regions[i]
	}
	return regions, byRef
}

func bindPreparedRegions(regions []preparedRegion, output string) []preparedRegion {
	_, parts, err := peb.SplitViewRegions(output)
	if err != nil || len(parts) != len(regions) {
		return nil
	}
	for i, part := range parts {
		if part.Ref != regions[i].ref {
			return nil
		}
		regions[i].outputStart, regions[i].outputEnd = part.Bounds()
	}
	return regions
}

func (v *preparedView) updateRegion(source string, remote *client.Extension, surfaceID string) bool {
	if len(v.regions) == 0 {
		return false
	}
	start, end, nextEnd := textchange.Between(v.source, source)
	shift := nextEnd - end
	for i, r := range v.regions {
		if r.depth < 0 || start < r.start || end > r.end {
			continue
		}
		body := source[r.start : r.end+shift]
		parser := regionParsers.Get().(*parse.Parser)
		if len(body) <= 256<<10 {
			defer func() { parser.Reset(); regionParsers.Put(parser) }()
		}
		root, err := parser.ParseTree(body)
		if err != nil || markup.HTMLWithSizeHint(root, len(body)) != body {
			return false
		}
		// Canonical serialization also proves the new fragment is balanced.
		// Ignored parser tokens and repairs cannot be spliced across a boundary.
		focus, image := r.focusStart, 0
		rewritten := false
		var prepare func(*node.Node, int) bool
		prepare = func(n *node.Node, depth int) bool {
			// Document roots and plaintext have context-sensitive parsing at EOF.
			if depth+2 >= parse.MaxNestingDepth {
				return false
			}
			if n.Type == node.ElementNode {
				switch n.Data {
				case "html", "head", "body", "plaintext":
					return false
				}
				rewritten = prepareSurfaceControl(n, remote, surfaceID, &focus) || rewritten
				if src, ok := n.Attr("src"); n.Data == "img" && ok {
					if image == len(r.images) || r.images[image].source != src {
						return false
					}
					rewritten = n.SetAttr("src", r.images[image].uri) || rewritten
					image++
				}
			}
			for _, child := range n.Children {
				if !prepare(child, depth+1) {
					return false
				}
			}
			return true
		}
		if !prepare(root, r.depth) || focus != r.focusEnd || image != len(r.images) {
			return false
		}
		output := body
		if rewritten {
			output = markup.HTMLWithSizeHint(root, len(body))
		}
		outputShift := len(output) - (r.outputEnd - r.outputStart)
		if !rewritten && v.markup == v.source {
			v.markup = source
		} else {
			v.markup = v.markup[:r.outputStart] + output + v.markup[r.outputEnd:]
		}
		v.source = source
		v.regions[i].end += shift
		v.regions[i].outputEnd += outputShift
		for j := i + 1; j < len(v.regions); j++ {
			v.regions[j].start += shift
			v.regions[j].end += shift
			v.regions[j].outputStart += outputShift
			v.regions[j].outputEnd += outputShift
		}
		return true
	}
	return false
}
