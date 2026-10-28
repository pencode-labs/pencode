package workbench

import (
	"github.com/diiyw/onix/html/dom"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/peb"
)

// Regions retain the surrounding component while replacing their own children.
// Each data-pencode-region element must have a data-ref; nested regions belong
// to their outer region and are replaced with it.
type pencodeExtensionRegion struct {
	ref, html  string
	tree       *dom.Node
	definition *htmlui.Definition[pencodeExtensionSurfaceProps, struct{}]
}

func splitExtensionRegions(source string) (string, []pencodeExtensionRegion, error) {
	template, parts, err := peb.SplitViewRegions(source)
	if err != nil {
		return "", nil, err
	}
	var regions []pencodeExtensionRegion
	if len(parts) != 0 {
		regions = make([]pencodeExtensionRegion, len(parts))
	}
	for i, part := range parts {
		regions[i] = pencodeExtensionRegion{ref: part.Ref, html: part.HTML}
	}
	return template, regions, nil
}
