package extensions

import (
	"testing"

	"github.com/pencode-labs/pencode/peb"
)

func TestRegionControlsRetainIdentityWhenEarlierControlsChange(t *testing.T) {
	backend, _ := actionFixture(t)
	remote, err := backend.LoadExtension(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	r := viewResources{remote: remote, cache: make(map[string]viewResource)}
	prepare := func(content string) string {
		t.Helper()
		view := peb.EditorView{HTML: `<main><section data-ref="activity" data-pencode-region>` + content + `</section><textarea data-ref="prompt">draft</textarea><aside data-ref="history" data-pencode-region><button data-ref="saved">Saved</button></aside></main>`}
		if _, err := r.prepare(t.Context(), &view, "composer"); err != nil {
			t.Fatal(err)
		}
		template, _, err := peb.SplitViewRegions(view.HTML)
		if err != nil {
			t.Fatal(err)
		}
		return template
	}
	before := prepare(`<button data-ref="toggle">Activity</button>`)
	after := prepare(`<button data-ref="toggle">Activity</button><button data-ref="details">Details</button>`)
	if before != after {
		t.Fatalf("region update changed surrounding control IDs:\nbefore: %s\nafter:  %s", before, after)
	}
}
