package extensions

import (
	"os"
	"path/filepath"
	"reflect"

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

func BenchmarkChangingSurfaceRegion(b *testing.B) {
	backend, _ := actionFixture(b)
	remote, err := backend.LoadExtension(b.Context(), "test")
	if err != nil {
		b.Fatal(err)
	}
	r := viewResources{remote: remote, cache: make(map[string]viewResource)}
	base := "<main>" + strings.Repeat("<p>Earlier message</p>", 500) + `<button data-ref="send">Send</button><section data-pencode-region="" data-ref="tail">`
	views := [...]string{base + "<p>First</p></section></main>", base + "<p>Second <strong>message</strong></p></section></main>"}
	i := 0
	b.ReportAllocs()
	for b.Loop() {
		i ^= 1
		view := peb.EditorView{HTML: views[i]}
		if _, err := r.prepare(b.Context(), &view, "chat"); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRegionUpdatesMatchFullResourcePreparation(t *testing.T) {
	backend, directory := actionFixture(t)
	for _, file := range []string{"one.svg", "two.svg"} {
		if err := os.WriteFile(filepath.Join(directory, "test", "resources", file), []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="2" height="2"></svg>`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	remote, err := backend.LoadExtension(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	r := viewResources{remote: remote, cache: make(map[string]viewResource)}
	const head = `<main><button>before</button><section data-pencode-region="" data-ref="a">`
	const mid = `</section><section data-pencode-region="" data-ref="b">`
	const tail = `</section><button>after</button></main>`
	const icon = `<img src="resources/one.svg">`
	content := "<button>copy</button>" + icon
	check := func(raw string, incremental bool) {
		t.Helper()
		old := r.last.regions
		view, fresh := peb.EditorView{HTML: raw}, peb.EditorView{HTML: raw}
		cold := viewResources{remote: remote, cache: make(map[string]viewResource)}
		got, err := r.prepare(t.Context(), &view, "chat")
		want, wantErr := cold.prepare(t.Context(), &fresh, "chat")
		if err != nil || wantErr != nil || view.HTML != fresh.HTML || !reflect.DeepEqual(got, want) {
			t.Fatalf("incremental view differs from full preparation: %v / %v\n%s\n%s", err, wantErr, view.HTML, fresh.HTML)
		}
		if incremental && (len(old) == 0 || len(r.last.regions) == 0 || &old[0] != &r.last.regions[0]) {
			t.Fatal("local update rebuilt the surrounding view")
		}
	}
	check(head+content+mid+"first"+tail, false)
	content = "<p>中🙂</p><button>copy again</button>" + icon
	check(head+content+mid+"first"+tail, true)
	check(head+content+mid+"<em>next</em>"+tail, true)
	content = strings.Replace(content, "one.svg", "two.svg", 1)
	check(head+content+mid+"<em>next</em>"+tail, false)
	check(head+content+"<input>"+mid+"<em>next</em>"+tail, false)
	check(head+content+"<input>"+mid+"<p>fixed"+tail, false)
}
