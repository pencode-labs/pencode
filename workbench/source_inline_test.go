package workbench

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
)

func TestSourceInlineChanges(t *testing.T) {
	for _, test := range []struct {
		name, left, right string
		want              [][2]string
	}{
		{"equal", "same", "same", nil},
		{"replace", "call(old)", "call(new)", [][2]string{{"old", "new"}}},
		{"multiple", "call(1, 2)", "call(3, 4)", [][2]string{{"1", "3"}, {"2", "4"}}},
		{"insert", "value = 1", "value = 10", [][2]string{{"", "0"}}},
		{"delete", "value = 10", "value = 1", [][2]string{{"0", ""}}},
		{"unicode", "\t值 = 你好 + 旧值", "\t值 = 你们 + 新值", [][2]string{{"好", "们"}, {"旧", "新"}}},
		{"emoji", "a😀z", "a😁z", [][2]string{{"😀", "😁"}}},
		{"added", "", "added", [][2]string{{"", "added"}}},
		{"removed", "removed", "", [][2]string{{"removed", ""}}},
		{"bounded", "prefix" + strings.Repeat("x", 1024) + "suffix", "prefix" + strings.Repeat("y", 1024) + "suffix", [][2]string{{strings.Repeat("x", 1024), strings.Repeat("y", 1024)}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var differ sourceInlineDiffer
			changes := differ.compare(test.left, test.right)
			var got [][2]string
			for _, change := range changes {
				got = append(got, [2]string{test.left[change.Start[0]:change.End[0]], test.right[change.Start[1]:change.End[1]]})
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("changed fragments = %q, want %q", got, test.want)
			}
			assertSourceInlineRanges(t, test.left, test.right, changes)
		})
	}
}

func assertSourceInlineRanges(t *testing.T, left, right string, changes []sourceInlineChange) {
	t.Helper()
	previous := [2]int{}
	for _, change := range changes {
		for side, value := range [...]string{left, right} {
			if change.Start[side] < previous[side] || change.End[side] < change.Start[side] || change.End[side] > len(value) || !utf8.ValidString(value[change.Start[side]:change.End[side]]) {
				t.Fatalf("invalid UTF-8 range %+v for %q", change, value)
			}
		}
		if left[previous[0]:change.Start[0]] != right[previous[1]:change.Start[1]] {
			t.Fatalf("unmarked difference in %q / %q: %+v", left, right, changes)
		}
		previous = change.End
	}
	if left[previous[0]:] != right[previous[1]:] {
		t.Fatalf("unmarked suffix in %q / %q: %+v", left, right, changes)
	}
}

func TestSourceInlineRangesPreserveUnchangedText(t *testing.T) {
	values := []string{""}
	for range 3 {
		for _, prefix := range append([]string(nil), values...) {
			for _, value := range []string{"a", "b", "你", "他", "\t"} {
				values = append(values, prefix+value)
			}
		}
	}
	var differ sourceInlineDiffer
	for _, left := range values {
		for _, right := range values {
			differ.changes = differ.changes[:0]
			assertSourceInlineRanges(t, left, right, differ.compare(left, right))
		}
	}
}

// Verify actual DOM marker geometry against native caret geometry, including
// aligned gaps, tabs and UTF-8. No window or screenshot is created.
func assertSourceInlinePaint(t *testing.T, model *workspaceModel, tab *sourceTab, texts [2]*scene.Text2D) {
	t.Helper()
	counts := [2]int{}
	for row, line := range tab.Rows {
		if row < tab.FirstRow || row >= tab.FirstRow+160 {
			continue
		}
		for index, change := range line.Changes {
			for side, text := range texts {
				if change.Start[side] == change.End[side] {
					continue
				}
				counts[side]++
				node, ok := model.fontResources.HTMLNode(model.surface, sourceInlineID(side, row, index))
				if !ok {
					t.Fatal("missing inline difference marker")
				}
				bounds, ok := node.Bounds()
				if !ok || bounds.Width <= 0 || bounds.Height != 20 {
					t.Fatalf("invisible inline difference: %+v", bounds)
				}
				number := line.LeftNumber
				if side == 1 {
					number = line.RightNumber
				}
				start, _, _, _ := scene.TextLineRange2D(text, number-1)
				caret := utf8.RuneCountInString(text.Value[:start+change.Start[side]])
				x, y, _, _, _ := scene.TextCaretRect2D(text, text.Value, caret)
				id := [...]string{"sc-code-left-input", "sc-code-right-input"}[side]
				input, _ := model.fontResources.HTMLNode(model.surface, id)
				origin, _ := input.Bounds()
				if math.Abs(float64(bounds.X-origin.X-x)) > .02 || math.Abs(float64(bounds.Y-origin.Y-y)) > .02 {
					t.Fatalf("marker does not match native caret: marker=%+v origin=%+v caret=(%g,%g)", bounds, origin, x, y)
				}
			}
		}
	}
	if counts[0] == 0 || counts[1] == 0 {
		t.Fatalf("missing removed/inserted text markers: %v", counts)
	}
	state := model.sourceState()
	if allocations := testing.AllocsPerRun(100, func() {
		for side, text := range texts {
			state.inlinePaint[side].sync(model.fontResources, model, tab, side, text)
		}
	}); allocations != 0 {
		t.Fatalf("idle inline paint allocated %g times", allocations)
	}
}

func TestSourceInlinePaint(t *testing.T) {
	r := sourceTestRepository(t)
	const left = "package demo\n\t// 中文 old(1, 2)\n\tvar Value = 1\n"
	const right = "package demo\n\t// 中文 new(3, 4)\n// inserted\n\tvar Value = 2\n"
	sourceTestWrite(t, r, "main.go", left)
	sourceTestGit(t, r, "add", "--all")
	sourceTestGit(t, r, "commit", "-m", "base")
	sourceTestWrite(t, r, "main.go", right)
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", "main.go")
	app, model, _, _ := languageInteractionFixture(t)
	model.sourceAction("sc-toggle")
	sourceWait(t, app, model)
	model.sourceAction("sc-open:changes:main.go")
	sourceWait(t, app, model)
	tab := model.sourceState().activeTab()
	settleSourceHighlight(t, model)
	model.sourceChanged()
	for range 3 {
		languageInteractionStep(t, app)
	}
	var texts [2]*scene.Text2D
	for side, id := range [...]string{"sc-code-left-input", "sc-code-right-input"} {
		control, _ := model.fontResources.HTMLControl(model.surface, id)
		entity, _ := module.ContentFindPart[engineui.ValuePart, scene.Text2D](model.fontResources, control)
		texts[side] = module.ContentRef[scene.Text2D](model.fontResources, entity)
	}
	assertSourceInlinePaint(t, model, tab, texts)
	// Suppress stale right-side ranges while an edited comparison is pending.
	tab.editResult(strings.Replace(right, "new(3, 4)", "next(5, 6)", 1))
	model.bindSourceDiffControls(model.fontResources)
	for _, line := range tab.Rows {
		for _, change := range line.Changes {
			if change.Start[1] != change.End[1] && change.Style[1] != "display:none" {
				t.Fatal("stale inline range survived an edit")
			}
		}
	}
	settleSourceHighlight(t, model)
	model.sourceChanged()
	for range 3 {
		languageInteractionStep(t, app)
	}
	assertSourceInlinePaint(t, model, tab, texts)
}
