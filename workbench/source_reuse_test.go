package workbench

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestSourceDiffBufferReuseAndOwnership(t *testing.T) {
	const left = "call(1, 2)\nunchanged\n"
	const first = "call(3, 4)\nunchanged\n"
	const next = "call(5, 6)\nunchanged\n"
	const patch = "@@ -1 +1 @@\n"
	tab := &sourceTab{Key: "changes", Kind: "changes", Local: left, Result: first}
	tab.applyDiffView(alignSourceDiff(left, first, patch))
	result := pencodeHighlightResult{diffView: alignSourceDiff(left, next, patch)}
	tab.Result = next
	model := workspaceModel{}
	model.sourceControl.Tabs, model.sourceControl.Active = []*sourceTab{tab}, tab.Key
	result.job = pencodeHighlightJob{sourceDoc: &tab.Highlight[1], source: next, diff: true}
	oldRows, oldOffsets, oldChanges := &tab.Rows[0], &tab.LineLayouts[0].Offsets[0], &tab.diffChanges[0]
	if !model.acceptSourceHighlight(&result) {
		t.Fatal("matching result rejected")
	}
	if &result.diffView.Rows[0] != oldRows || &result.diffView.Layouts[0].Offsets[0] != oldOffsets || &result.diffView.changes[0] != oldChanges {
		t.Fatal("accepted result did not return old tab storage")
	}
	expected := slices.Clone(tab.Rows)
	for i := range expected {
		expected[i].Changes = slices.Clone(expected[i].Changes)
	}
	offsets := slices.Clone(tab.LineLayouts[0].Offsets)
	for range 4 {
		alignSourceDiffInto(&result.diffView, &result.diffInline, left, first, patch)
		if !reflect.DeepEqual(tab.Rows, expected) || !slices.Equal(tab.LineLayouts[0].Offsets, offsets) {
			t.Fatal("worker reuse mutated published diff")
		}
	}
	if n := testing.AllocsPerRun(100, func() { alignSourceDiffInto(&result.diffView, &result.diffInline, left, first, patch) }); n != 0 {
		t.Fatalf("warm alignment allocated %g", n)
	}
	// Shrinking drops old line references and inline paint state.
	alignSourceDiffInto(&result.diffView, &result.diffInline, "same", "same", "")
	if len(result.diffView.Rows) != 1 || result.diffView.FirstChange != -1 || len(result.diffView.Blocks) != 0 || len(result.diffView.Rows[0].Changes) != 0 {
		t.Fatal("reused diff retained removed changes")
	}
}

func BenchmarkPerfSourceAlignReused(b *testing.B) {
	for _, lines := range []int{1000, 20000, 100000} {
		b.Run(strconv.Itoa(lines), func(b *testing.B) {
			base := strings.Repeat("let x = 42;\n", lines)
			value := "changed\n" + base
			var view sourceDiffView
			var inline sourceInlineDiffer
			alignSourceDiffInto(&view, &inline, base, value, "@@ -0,0 +1 @@\n")
			b.ReportAllocs()
			for b.Loop() {
				alignSourceDiffInto(&view, &inline, base, value, "@@ -0,0 +1 @@\n")
			}
			perfView = view
		})
	}
}
