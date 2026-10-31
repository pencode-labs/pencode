package peb

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

func TestViewRegionSplitterMatchesFullSplit(t *testing.T) {
	var splitter ViewRegionSplitter
	views := []string{
		"",
		`<main><div data-ref="a" data-pencode-region>first</div><section data-ref="b" data-pencode-region>second</section></main>`,
		`<main><div data-ref="a" data-pencode-region><div>中🙂</div></div><section data-ref="b" data-pencode-region>second</section></main>`,
		`<main><div data-ref="a" data-pencode-region></div><section data-ref="b" data-pencode-region>second</section></main>`,
		`<main><div data-ref="a" data-pencode-region></div><section data-ref="b" data-pencode-region><p>third</p></section></main>`,
		`<main hidden><div data-ref="a" data-pencode-region>new</div><section data-ref="b" data-pencode-region>changed</section></main>`,
		`<section data-ref="outer" data-pencode-region><div data-ref="inner" data-pencode-region>nested</div></section>`,
		`<section data-ref="outer" data-pencode-region><div data-ref="inner" data-pencode-region>next</div></section>`,
		`<div>no regions</div>`,
		"",
	}
	for _, source := range views {
		for range 2 {
			template, regions, err := splitter.Split(source)
			wantTemplate, wantRegions, wantErr := SplitViewRegions(source)
			if err != nil || wantErr != nil || template != wantTemplate || !reflect.DeepEqual(regions, wantRegions) {
				t.Fatalf("source=%s: split differs: %v / %v", source, err, wantErr)
			}
		}
	}
	random := rand.New(rand.NewPCG(19, 31))
	fragments := []string{"", "中🙂", "<p>text</p>", "<div><div>nested</div></div>", "<textarea>&lt;/section&gt;</textarea>", "<script>const s = '</div>';</script>"}
	var contents [5]string
	for range 200 {
		for range 1 + random.IntN(2) {
			contents[random.IntN(len(contents))] = fragments[random.IntN(len(fragments))]
		}
		var source strings.Builder
		for i, content := range contents {
			fmt.Fprintf(&source, `<section data-ref="r%d" data-pencode-region>%s</section>`, i, content)
		}
		template, regions, err := splitter.Split(source.String())
		wantTemplate, wantRegions, wantErr := SplitViewRegions(source.String())
		if err != nil || wantErr != nil || template != wantTemplate || !reflect.DeepEqual(regions, wantRegions) {
			t.Fatal("incremental split differs from full parsing", err, wantErr)
		}
	}
	base := `<div data-ref="a" data-pencode-region>before</div>`
	_, before, _ := splitter.Split(base)
	held := before[0].HTML
	if _, _, err := splitter.Split(`<div data-ref="a" data-pencode-region>unclosed`); err == nil {
		t.Fatal("accepted unclosed region")
	}
	if splitter.source != base || held != "before" {
		t.Fatal("failed split changed the baseline or an immutable string")
	}
	_, after, err := splitter.Split(strings.Replace(base, "before", "after", 1))
	if err != nil || after[0].HTML != "after" || held != "before" {
		t.Fatal("later update changed a published string", err)
	}
}

func BenchmarkViewRegionSplitter(b *testing.B) {
	var history strings.Builder
	for i := range 500 {
		fmt.Fprintf(&history, `<section data-ref="history/%d" data-pencode-region><p>History</p></section>`, i)
	}
	prefix := history.String() + `<section data-ref="tail" data-pencode-region>`
	views := [2]string{prefix + `<p>A</p></section>`, prefix + `<p>B</p></section>`}
	var splitter ViewRegionSplitter
	_, _, _ = splitter.Split(views[0])
	i := 0
	b.ReportAllocs()
	for b.Loop() {
		i = 1 - i
		if _, _, err := splitter.Split(views[i]); err != nil {
			b.Fatal(err)
		}
	}
}
