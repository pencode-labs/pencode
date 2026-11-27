package workbench

import (
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	"github.com/onix-engine/onix/text/piecetree"
	"github.com/pencode-labs/pencode/textsearch"
)

// The original uses the ordinary find cache. Modified matches retain offsets
// in their own document and follow the original matches in navigation order.
type pencodeDiffFind struct {
	tab     *sourceTab
	content piecetree.Snapshot
	matches []textsearch.Match
	paint   [2]pencodeFindPaint
}

func (m *workspaceModel) releaseDiffFind() {
	if diff := m.findDiff; diff != nil {
		if m.fontResources != nil {
			for side := range diff.paint {
				diff.paint[side].dispose(m.fontResources)
			}
		}
		m.findDiff = nil
		m.findCountLabel = ""
	}
}

func (m *workspaceModel) syncDiffFind(world *module.Content) {
	diff := m.findDiff
	if diff == nil {
		return
	}
	if diff.tab != m.languageDiffTab() {
		m.clearFindCache()
		return
	}
	if !m.findOpen {
		return
	}
	m.refreshFindMatches()
	for side := range diff.paint {
		matches, index := m.findMatches, m.findIndex
		if side == 1 {
			matches, index = diff.matches, index-len(matches)
		}
		diff.paint[side].syncEditor(world, m, &m.sourceState().diffEditors[side], diff.tab.languageFile(side), matches, index, m.diffScroll(world, side))
	}
}

func (m *workspaceModel) revealDiffFind(world *module.Content, editor *pencodeEditor, content piecetree.Snapshot, anchor int) {
	scroll := m.diffScroll(world, m.sourceState().languageSide)
	text := world.Mut[scene.Text2D](editor.text)
	if scroll == nil || text == nil || text.Text() != content {
		return
	}
	x, y, _, h, ok := scene.TextCaretRect2D(text, content, anchor)
	if ok {
		scroll.Position[0] = -max(0, x-scroll.ViewportSize[0]/2)
		scroll.Position[1] = -max(0, y+(h-scroll.ViewportSize[1])/2)
		scroll.Momentum = [2]float32{}
		m.syncSourceScroll(world)
	}
}
