package workbench

import "github.com/diiyw/onix/scene"

func (tab *sourceTab) initializeDiffView(view sourceDiffView) {
	tab.applyDiffView(view)
	if view.FirstChange >= 0 {
		// Leave three context rows above the first edit.
		tab.ScrollY = -float32(max(0, view.FirstChange-3) * 20)
		tab.FirstRow = sourceFirstVisibleRow(tab.ScrollY)
	}
}

func (tab *sourceTab) applyDiffView(view sourceDiffView) {
	tab.Rows, tab.CodeColumns, tab.Blocks = view.Rows, view.CodeColumns, view.Blocks
	tab.DiffRevision++
	tab.LineLayouts = view.Layouts
	tab.diffChanges = view.changes
	for side := range tab.LineLayouts {
		tab.LineLayouts[side].Revision = tab.DiffRevision
	}
	tab.DiffContent = tab.sourceText(1)
}

func sourceCodeColumns(text string) int {
	columns := 0
	for _, ch := range text {
		switch {
		case ch == '\t':
			columns += 4 - columns%4
		case ch >= 0x1100:
			columns += 2
		default:
			columns++
		}
	}
	return columns
}

func sourceFirstVisibleRow(y float32) int {
	return max(0, int(-y)/20-10) / 20 * 20
}

// Track each pane's applied position separately: a shorter viewport can clamp
// its offset without being mistaken for new wheel/scrollbar input next frame.
func (tab *sourceTab) syncScroll(scrolls [2]*scene.UIScroll, restore bool) bool {
	position := [2]float32{tab.ScrollX, tab.ScrollY}
	changed := false
	for axis := range position {
		leader := -1
		if !restore {
			for i, scroll := range scrolls {
				if scroll != nil && scroll.Position[axis] != tab.scrollApplied[i][axis] {
					position[axis], leader = scroll.Position[axis], i
					break
				}
			}
		}
		if axis == 1 {
			for _, scroll := range scrolls {
				if scroll != nil {
					position[axis] = min(0, max(position[axis], -max(0, scroll.ContentSize[axis]-scroll.ViewportSize[axis])))
				}
			}
		}
		for i, scroll := range scrolls {
			if scroll == nil {
				continue
			}
			next := min(0, max(position[axis], -max(0, scroll.ContentSize[axis]-scroll.ViewportSize[axis])))
			changed = changed || next != scroll.Position[axis]
			scroll.Position[axis], tab.scrollApplied[i][axis] = next, next
			if i != leader {
				scroll.Momentum[axis] = 0
			}
		}
	}
	tab.ScrollX, tab.ScrollY = position[0], position[1]
	return changed
}

func (tab *sourceTab) diffStorage() sourceDiffView {
	return sourceDiffView{Rows: tab.Rows, Blocks: tab.Blocks, Layouts: tab.LineLayouts, changes: tab.diffChanges}
}
