package workbench

import (
	"fmt"
	"strconv"
)

const (
	sourceGraphLaneWidth  = 20
	sourceGraphColorCount = 6
)

var sourceGraphColorClasses = [sourceGraphColorCount]string{
	" sc-graph-color-0", " sc-graph-color-1", " sc-graph-color-2",
	" sc-graph-color-3", " sc-graph-color-4", " sc-graph-color-5",
}

func renderSourceGraphLanes(b *sourceVNodeView, c sourceCommit) int {
	x := c.Lane*sourceGraphLaneWidth + 12
	width := x + 6
	b.open("span", "graph", "class", "sc-lanes"+sourceGraphColorClasses[c.Color], "aria-hidden", "true")
	if c.Incoming {
		b.text("span", "incoming", "", "class", "sc-edge sc-edge-incoming", "style", fmt.Sprintf("left:%dpx", x-1))
	}
	for i, join := range c.Joins {
		x1, x2 := join.From*sourceGraphLaneWidth+12, join.To*sourceGraphLaneWidth+12
		width = max(width, max(x1, x2)+6)
		direction := "left"
		if x2 > x1 {
			direction = "right"
		}
		b.text("span", "join:"+strconv.Itoa(i), "", "class", "sc-edge-arc sc-edge-join-"+direction+sourceGraphColorClasses[join.Color], "style", fmt.Sprintf("left:%dpx;width:%dpx", min(x1, x2)-1, max(x1, x2)-min(x1, x2)+2))
	}
	for j, edge := range c.Edges {
		if edge.To < 0 {
			continue
		}
		x1, x2 := edge.From*sourceGraphLaneWidth+12, edge.To*sourceGraphLaneWidth+12
		width = max(width, max(x1, x2)+6)
		key := strconv.Itoa(j)
		colorClass := sourceGraphColorClasses[edge.Color]
		if x1 == x2 {
			class := "sc-edge sc-edge-outgoing"
			if edge.From != c.Lane {
				class = "sc-edge sc-edge-through"
			}
			b.text("span", "edge:"+key, "", "class", class+colorClass, "style", fmt.Sprintf("left:%dpx", x1-1))
			continue
		}
		if edge.From == c.Lane {
			direction := "right"
			if x2 < x1 {
				direction = "left"
			}
			b.text("span", "fork:"+key, "", "class", "sc-edge-arc sc-edge-fork-"+direction+colorClass, "style", fmt.Sprintf("left:%dpx;width:%dpx", min(x1, x2)-1, max(x1, x2)-min(x1, x2)+2))
			continue
		}
		b.text("span", "incoming:"+key, "", "class", "sc-edge sc-edge-incoming"+colorClass, "style", fmt.Sprintf("left:%dpx", x1-1))
		// Two rounded elbows meet at y=24, preserving vertical tangents at
		// the commit center (y=16) and the next row boundary (y=32).
		middle := (x1 + x2) / 2
		start, end, direction := x1-1, middle-1, "right"
		if x2 < x1 {
			start, end, direction = middle-1, x2-1, "left"
		}
		curveWidth := (max(x1, x2)-min(x1, x2))/2 + 2
		b.text("span", "start:"+key, "", "class", "sc-edge-curve sc-edge-start-"+direction+colorClass, "style", fmt.Sprintf("left:%dpx;width:%dpx", start, curveWidth))
		b.text("span", "end:"+key, "", "class", "sc-edge-curve sc-edge-end-"+direction+colorClass, "style", fmt.Sprintf("left:%dpx;width:%dpx", end, curveWidth))
	}
	b.attr("style", fmt.Sprintf("width:%dpx", width))
	// The filled dot overlaps the path without a background-colored halo.
	b.text("span", "dot", "", "class", "sc-dot", "style", fmt.Sprintf("left:%dpx", x-6))
	b.close()
	return width
}

func renderSourceGraphContinuation(b *sourceVNodeView, c sourceCommit, width int) {
	b.open("span", "continuation", "class", "sc-lanes sc-lanes-continuation", "aria-hidden", "true", "style", fmt.Sprintf("width:%dpx", width))
	for i, edge := range c.Edges {
		if edge.To >= 0 {
			b.text("span", strconv.Itoa(i), "", "class", "sc-edge"+sourceGraphColorClasses[edge.ToColor], "style", fmt.Sprintf("left:%dpx", edge.To*sourceGraphLaneWidth+11))
		}
	}
	b.close()
}

func (s *pencodeSourceControl) graphDetailsHeight() int {
	if s.SelectedHash == "" {
		return 0
	}
	return max(1, len(s.historyTree())) * 27
}

// One expanded commit keeps virtual offsets O(1), without per-row storage.
func (s *pencodeSourceControl) graphOffset(index int) int {
	offset := index * sourceGraphRowHeight
	if s.SelectedHash != "" && index > s.ExpandedCommit {
		offset += s.graphDetailsHeight()
	}
	return offset
}

func (s *pencodeSourceControl) graphCommitAt(offset int) int {
	if s.SelectedHash != "" {
		start := (s.ExpandedCommit + 1) * sourceGraphRowHeight
		if offset >= start {
			if offset < start+s.graphDetailsHeight() {
				return s.ExpandedCommit
			}
			offset -= s.graphDetailsHeight()
		}
	}
	return max(0, offset/sourceGraphRowHeight)
}

// Preserve the visible commit when a file list above the viewport changes size.
func (s *pencodeSourceControl) resizeGraphDetails(oldHeight int) {
	if oldHeight == s.graphDetailsHeight() {
		return
	}
	start := float32((s.ExpandedCommit + 1) * sourceGraphRowHeight)
	position, height := -s.GraphScrollY, float32(s.graphDetailsHeight())
	if position >= start+float32(oldHeight) {
		s.GraphScrollY -= height - float32(oldHeight)
		s.RestoreGraph = true
	} else if position > start+height {
		s.GraphScrollY = -(start + height)
		s.RestoreGraph = true
	}
}
