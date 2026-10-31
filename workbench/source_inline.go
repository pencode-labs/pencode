package workbench

import (
	"strconv"
	"unicode/utf8"

	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
)

// Ranges use line-local UTF-8 byte offsets, independently of syntax spans.
type sourceInlineChange struct {
	Start, End [2]int
	Style      [2]string
}

type sourceInlineRune struct {
	value  rune
	offset int
}

// One scratch buffer serves every changed row in a comparison job.
type sourceInlineDiffer struct {
	runes   [2][]sourceInlineRune
	matches []uint32
	changes []sourceInlineChange
}

func (d *sourceInlineDiffer) compare(left, right string) []sourceInlineChange {
	if left == right {
		return nil
	}
	start := 0
	for start < min(len(left), len(right)) && left[start] == right[start] {
		start++
	}
	for start > 0 && start < len(left) && !utf8.RuneStart(left[start]) {
		start--
	}
	lend, rend := len(left), len(right)
	for lend > start && rend > start && left[lend-1] == right[rend-1] {
		lend--
		rend--
	}
	for lend < len(left) && !utf8.RuneStart(left[lend]) {
		lend++
		rend++
	}
	first := len(d.changes)
	change := sourceInlineChange{Start: [2]int{start, start}, End: [2]int{lend, rend}}
	if lend == start || rend == start {
		d.changes = append(d.changes, change)
		return d.changes[first:]
	}
	for side, value := range [...]string{left[start:lend], right[start:rend]} {
		d.runes[side] = d.runes[side][:0]
		for offset, r := range value {
			d.runes[side] = append(d.runes[side], sourceInlineRune{r, start + offset})
		}
		d.runes[side] = append(d.runes[side], sourceInlineRune{offset: start + len(value)})
	}
	n, m := len(d.runes[0])-1, len(d.runes[1])-1
	// Bound quadratic work for generated/minified lines. Common edges still
	// remain unmarked when the changed middle exceeds this budget.
	const maxCells = 256 * 1024
	if n+1 > maxCells/(m+1) {
		d.changes = append(d.changes, change)
		return d.changes[first:]
	}
	cells := (n + 1) * (m + 1)
	if cap(d.matches) < cells {
		d.matches = make([]uint32, cells)
	} else {
		d.matches = d.matches[:cells]
	}
	clear(d.matches[n*(m+1):])
	for i := n - 1; i >= 0; i-- {
		d.matches[i*(m+1)+m] = 0
		for j := m - 1; j >= 0; j-- {
			index := i*(m+1) + j
			if d.runes[0][i].value == d.runes[1][j].value {
				d.matches[index] = d.matches[index+m+2] + 1
			} else {
				d.matches[index] = max(d.matches[index+m+1], d.matches[index+1])
			}
		}
	}
	i, j := 0, 0
	for i < n || j < m {
		if i < n && j < m && d.runes[0][i].value == d.runes[1][j].value {
			i++
			j++
			continue
		}
		change = sourceInlineChange{Start: [2]int{d.runes[0][i].offset, d.runes[1][j].offset}}
		for i < n || j < m {
			if i < n && j < m && d.runes[0][i].value == d.runes[1][j].value {
				break
			}
			if i < n && (j == m || d.matches[(i+1)*(m+1)+j] >= d.matches[i*(m+1)+j+1]) {
				i++
			} else {
				j++
			}
		}
		change.End = [2]int{d.runes[0][i].offset, d.runes[1][j].offset}
		d.changes = append(d.changes, change)
	}
	return d.changes[first:]
}

type sourceInlinePaint struct {
	key     sourceInlinePaintKey
	offsets []int
	widths  []float32
}

type sourceInlinePaintKey struct {
	tab      *sourceTab
	revision uint64
	firstRow int
	layout   pencodeGutterLayoutKey
	spacing  float32
}

func sourceInlineID(side, row, change int) string {
	return "sc-inline-" + strconv.Itoa(side) + "-" + strconv.Itoa(row) + "-" + strconv.Itoa(change)
}

func (paint *sourceInlinePaint) sync(world *module.Content, model *workspaceModel, tab *sourceTab, side int, text *scene.Text2D) {
	key := sourceInlinePaintKey{
		tab: tab, revision: tab.DiffRevision, firstRow: tab.FirstRow,
		layout:  pencodeGutterLayoutKey{text.Text(), text.Font, text.FontSize, text.DPI, text.WrapWidth, text.TabWidth, text.WrapMode},
		spacing: text.LetterSpacing,
	}
	if paint.key == key {
		return
	}
	for row := min(tab.FirstRow, len(tab.Rows)); row < min(len(tab.Rows), tab.FirstRow+160); row++ {
		line := &tab.Rows[row]
		value := line.Left
		if side == 1 {
			value = line.Right
		}
		ready := side == 0 || text.Text().Equal(snapshotText(tab.DiffContent))
		if ready && len(line.Changes) > 0 {
			paint.offsets = paint.offsets[:0]
			for _, change := range line.Changes {
				paint.offsets = append(paint.offsets, change.Start[side], change.End[side])
			}
			if cap(paint.widths) < len(paint.offsets) {
				paint.widths = make([]float32, len(paint.offsets))
			} else {
				paint.widths = paint.widths[:len(paint.offsets)]
			}
			if !scene.MeasureTextLineOffsets2D(text, value, paint.offsets, paint.widths) {
				return // Retry after the native editor has its font/layout.
			}
		}
		for index := range line.Changes {
			change := &line.Changes[index]
			start, end := change.Start[side], change.End[side]
			if start == end {
				continue
			}
			style := "display:none"
			if ready {
				x, right := paint.widths[index*2], paint.widths[index*2+1]
				style = languageUnderlineStyle(change.Style[side], x, float32((row-tab.FirstRow)*20), right-x, 20)
			}
			change.Style[side] = style
			if node, ok := world.HTMLNode(model.surface, sourceInlineID(side, row, index)); ok {
				node.SetAttribute("style", style)
			}
		}
	}
	paint.key = key
}
