package workbench

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/diiyw/onix/input"
	"github.com/pencode-labs/pencode/lsp"
)

func TestLanguageCompletionIconsFollowSortedPages(t *testing.T) {
	app, model, provider, state := languageInteractionFixture(t)
	world := model.fontResources
	model.setEditorValueAndCaret(world, model.activeContent(), 0)
	model.runLanguageAction(world, "textDocument/completion", false)
	job := languageInteractionJob(t, provider, "textDocument/completion")
	kinds := []int{2, 3, 21, 6, 7, 8, 10, 14, 22, 20, 17, 19, 0, 999, -1}
	wantIcons := []string{"symbol-method", "square-function", "symbol-constant", "symbol-variable", "symbol-class", "symbol-interface", "symbol-property", "symbol-keyword", "symbol-structure", "symbol-enum-member", "file-text", "folder", "symbol-misc", "symbol-misc", "symbol-misc"}
	items := make([]lsp.CompletionItem, 0, len(kinds))
	for i := len(kinds) - 1; i >= 0; i-- {
		items = append(items, lsp.CompletionItem{Label: fmt.Sprintf("item%02d", i), SortText: fmt.Sprintf("%02d", i), Detail: "detail", Kind: kinds[i]})
	}
	raw, err := json.Marshal(map[string]any{"items": items})
	if err != nil {
		t.Fatal(err)
	}
	model.languages.events <- pencodeLanguageEvent{session: provider, method: "result", job: job, result: raw}
	languageInteractionStep(t, app)
	panel := &model.languages.panel
	if !panel.completion || len(panel.rows) != len(kinds) {
		t.Fatalf("completion rows = %d, want %d", len(panel.rows), len(kinds))
	}
	for start := 0; start < len(kinds); start += len(pencodeCompletionRefs) {
		panel.completionIndex = start
		markPencodeRootDirtyByModel(world, model)
		languageInteractionStep(t, app)
		for i, ref := range pencodeCompletionRefs {
			node, ok := world.HTMLNode(model.surface, ref)
			if !ok {
				t.Fatal("missing completion row", ref)
			}
			markup, err := node.Serialize()
			if err != nil {
				t.Fatal(err)
			}
			index := start + i
			if index >= len(kinds) {
				if !strings.Contains(markup, " hidden") {
					t.Fatal("unused completion row remained visible", ref)
				}
				continue
			}
			if panel.rows[index].completionKind != kinds[index] {
				t.Fatalf("row %d lost its completion kind", index)
			}
			if !strings.Contains(markup, "icons/"+wantIcons[index]+".svg?foreground=") || !strings.Contains(markup, fmt.Sprintf(">item%02d — detail</span>", index)) {
				t.Fatalf("row %d has the wrong themed icon or label: %s", index, markup)
			}
			icon, ok := world.HTMLNode(model.surface, pencodeCompletionDetailRefs[i].icon)
			if !ok {
				t.Fatal("missing completion icon", ref)
			}
			bounds, visible := icon.Bounds()
			if !visible || bounds.Width != 16 || bounds.Height != 16 {
				t.Fatalf("row %d icon bounds = %+v, visible=%v", index, bounds, visible)
			}
		}
	}
	if allocations := testing.AllocsPerRun(100, func() {
		for _, kind := range kinds {
			icon := languageCompletionIcon(kind)
			model.themedIcon(icon.source, icon.foreground)
		}
	}); allocations != 0 {
		t.Fatalf("completion icon lookup allocated %g times", allocations)
	}
	// A click on the image must still accept its parent option on the current page.
	icon, _ := world.HTMLNode(model.surface, pencodeCompletionDetailRefs[0].icon)
	bounds, _ := icon.Bounds()
	scale := float64(model.settings.Zoom) / 100
	x, y := float64(bounds.X+bounds.Width/2)*scale, float64(bounds.Y+bounds.Height/2)*scale
	want := "item12" + model.activeContent()
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
	if panel.completion || model.activeContent() != want {
		t.Fatalf("icon click failed to accept the current page's suggestion: completion=%v", panel.completion)
	}
}
