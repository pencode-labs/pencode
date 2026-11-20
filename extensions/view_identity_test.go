package extensions

import (
	"strings"
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

func BenchmarkRepeatedSurfaceHTML(b *testing.B) {
	backend, _ := actionFixture(b)
	remote, err := backend.LoadExtension(b.Context(), "test")
	if err != nil {
		b.Fatal(err)
	}
	r := viewResources{remote: remote, cache: make(map[string]viewResource)}
	markup := strings.Repeat("<button>entry</button>", 128)
	initial := peb.EditorView{HTML: markup}
	if _, err := r.prepare(b.Context(), &initial, "panel"); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		v := peb.EditorView{HTML: markup}
		if _, err := r.prepare(b.Context(), &v, "panel"); err != nil {
			b.Fatal(err)
		}
	}
}

func TestPreparedViewReusesOnlyMatchingSurface(t *testing.T) {
	backend, _ := actionFixture(t)
	remote, err := backend.LoadExtension(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	r := viewResources{remote: remote, cache: make(map[string]viewResource)}
	const markup = "<button>Go</button>"
	v := peb.EditorView{HTML: markup}
	if _, err := r.prepare(t.Context(), &v, "first"); err != nil {
		t.Fatal(err)
	}
	first := v.HTML
	if n := testing.AllocsPerRun(100, func() {
		v.HTML = markup
		if _, err := r.prepare(t.Context(), &v, "first"); err != nil || v.HTML != first {
			t.Fatal(v.HTML, err)
		}
	}); n != 0 {
		t.Fatalf("warm prepare allocated %g times", n)
	}
	v.HTML = markup
	if _, err := r.prepare(t.Context(), &v, "second"); err != nil || v.HTML == first || !strings.Contains(v.HTML, "/second/") {
		t.Fatal(v.HTML, err)
	}
}
