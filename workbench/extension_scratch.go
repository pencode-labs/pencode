package workbench

import (
	"slices"
	"sync"

	"github.com/diiyw/onix/html/dom"
)

// Only validation and region lookup storage is pooled; published DOM trees and
// view strings keep their original ownership.
type pencodeExtensionScratch struct {
	previous map[string]*pencodeExtensionRegion
	trees    map[string]*dom.Node
	refs     map[string]bool
	ids      map[string]bool
	regions  []pencodeExtensionRegion
}

var pencodeExtensionScratches = sync.Pool{New: func() any {
	return &pencodeExtensionScratch{
		previous: make(map[string]*pencodeExtensionRegion),
		trees:    make(map[string]*dom.Node),
		refs:     make(map[string]bool),
		ids:      make(map[string]bool),
	}
}}

func (s *pencodeExtensionScratch) release() {
	if max(len(s.previous), len(s.trees), len(s.refs), len(s.ids), cap(s.regions)) > 4096 {
		return
	}
	clear(s.previous)
	clear(s.trees)
	clear(s.refs)
	clear(s.ids)
	clear(s.regions)
	s.regions = s.regions[:0]
	pencodeExtensionScratches.Put(s)
}

func (s *pencodeExtensionScratch) resizeRegions(size int) []pencodeExtensionRegion {
	s.regions = slices.Grow(s.regions[:0], size)[:size]
	return s.regions
}
