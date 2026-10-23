package workbench

import (
	"slices"
	"testing"

	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
)

func TestPencodeHighlightCachedSwitchDoesNotParseOrAllocate(t *testing.T) {
	a := &openFile{Path: "a/main.go", Content: "package first\n// 中文\nvar First = 1\n"}
	b := &openFile{Path: "b/main.go", Content: "package second\nfunc Second() {}\n"}
	model := workspaceModel{files: map[string]*openFile{a.Path: a, b.Path: b}, tabs: []string{a.Path, b.Path}}
	defer model.closeHighlightWorker()
	var expected [2][]scene.TextSpan2D
	for i, file := range []*openFile{a, b} {
		model.selectTab(file.Path)
		settlePencodeHighlight(t, &model)
		expected[i] = slices.Clone(model.highlightScratch)
	}
	for i, file := range []*openFile{a, b} {
		model.selectTab(file.Path)
		settlePencodeHighlight(t, &model)
		if model.highlightRequest.file != nil || !slices.Equal(model.highlightScratch, expected[i]) {
			t.Fatal("cached switch queued parsing or changed colors")
		}
	}
	if allocs := testing.AllocsPerRun(100, func() {
		model.selectTab(a.Path)
		renderPencodeHighlight(&model)
		model.selectTab(b.Path)
		renderPencodeHighlight(&model)
	}); allocs != 0 {
		t.Fatalf("cached switches allocated %g times", allocs)
	}
	model.selectTab(a.Path)
	settlePencodeHighlight(t, &model)
	model.updateActiveContent("package first\nvar Edited = \"new\"\n")
	if _, ok := model.highlightCache.get(a, model.syntaxTheme, model.syntaxDefinitions); ok {
		t.Fatal("edit retained a stale highlight cache entry")
	}
	model.highlighted = false // The debounce expiry requests fresh highlighting.
	settlePencodeHighlight(t, &model)
	if slices.Equal(model.highlightScratch, expected[0]) {
		t.Fatal("edit did not parse and replace cached colors")
	}
	model.discardTab(a.Path)
	if _, ok := model.highlightCache.get(a, model.syntaxTheme, model.syntaxDefinitions); ok {
		t.Fatal("closing a tab retained its cache entry")
	}
}

func TestPencodeHighlightCacheBudgetAndInvalidation(t *testing.T) {
	var cache pencodeHighlightCache
	var files [70]openFile
	theme, _ := syntax.BuiltinTheme("dark-plus")
	definitions := syntax.DefaultDefinitions()
	put := func(file *openFile, spans []scene.TextSpan2D) {
		cache.put(pencodeHighlightCacheEntry{file: file, source: file.Content, theme: theme, definitions: definitions, spans: spans})
	}
	large := make([]scene.TextSpan2D, pencodeHighlightCacheSpanLimit/2+1)
	put(&files[0], large)
	put(&files[1], large)
	if _, ok := cache.get(&files[0], theme, definitions); ok {
		t.Fatal("total span budget did not evict an older file")
	}
	if cache.spans != len(large) {
		t.Fatal("eviction did not update the span budget")
	}
	put(&files[2], make([]scene.TextSpan2D, pencodeHighlightCacheSpanLimit+1))
	if _, ok := cache.get(&files[2], theme, definitions); ok {
		t.Fatal("oversized highlight result was cached")
	}
	for i := range files {
		put(&files[i], []scene.TextSpan2D{{Start: i, End: i + 1}})
	}
	if _, ok := cache.get(&files[0], theme, definitions); ok {
		t.Fatal("document limit did not evict an older file")
	}
	last := &files[len(files)-1]
	entry, ok := cache.get(last, theme, definitions)
	if !ok || entry.spans[0].Start != len(files)-1 {
		t.Fatal("newest cached spans are missing or alias another result")
	}
	if _, ok := cache.get(last, &syntax.Theme{}, definitions); ok {
		t.Fatal("theme change reused stale colors")
	}
	if _, ok := cache.get(last, theme, &syntax.Definitions{}); ok {
		t.Fatal("definition change reused stale colors")
	}
	last.Content = "changed"
	if _, ok := cache.get(last, theme, definitions); ok {
		t.Fatal("source change reused stale colors")
	}
	for i := range files {
		cache.remove(&files[i])
	}
	if cache.spans != 0 {
		t.Fatal("removal retained span storage")
	}
	for _, entry := range cache.entries {
		if entry.file != nil || entry.source != "" || entry.spans != nil {
			t.Fatal("removal retained file data")
		}
	}
}

func TestPencodeHighlightCacheReusesEditBuffer(t *testing.T) {
	var cache pencodeHighlightCache
	file := &openFile{Content: "before"}
	theme, _ := syntax.BuiltinTheme("monokai")
	spans := make([]scene.TextSpan2D, 128)
	put := func() {
		cache.put(pencodeHighlightCacheEntry{file: file, source: file.Content, theme: theme, spans: spans})
	}
	put()
	buffer := &cache.entries[file.highlightSlot].spans[0]
	cache.invalidate(file)
	if entry, ok := cache.get(file, theme, nil); ok || entry.source != "" {
		t.Fatal("invalidated entry retained source or remained valid")
	}
	file.Content = "after"
	if allocs := testing.AllocsPerRun(100, func() {
		cache.invalidate(file)
		put()
	}); allocs != 0 {
		t.Fatalf("editing a cached file allocated %g times", allocs)
	}
	spans = spans[:16]
	put()
	entry, ok := cache.get(file, theme, nil)
	if !ok || len(entry.spans) != 16 || &entry.spans[0] != buffer || cache.spans != 128 {
		t.Fatal("smaller result failed to reuse/account for retained capacity")
	}
	spans[0].End = 999
	if entry.spans[0].End == 999 {
		t.Fatal("cache aliases mutable highlight scratch")
	}
	cache.remove(file)
	if cache.spans != 0 {
		t.Fatal("close retained the edit buffer")
	}
}

func BenchmarkPencodeHighlightCacheUpdate(b *testing.B) {
	var cache pencodeHighlightCache
	file := &openFile{Content: "source"}
	spans := make([]scene.TextSpan2D, 2048)
	value := pencodeHighlightCacheEntry{file: file, source: file.Content, spans: spans}
	cache.put(value)
	b.ReportAllocs()
	for b.Loop() {
		cache.invalidate(file)
		cache.put(value)
	}
}

func TestPencodeHighlightCacheRetainedCapacityBudget(t *testing.T) {
	var cache pencodeHighlightCache
	var files [70]openFile
	spans := make([]scene.TextSpan2D, pencodeHighlightCacheSpanLimit)
	for i := range 400 {
		file := &files[i%len(files)]
		cache.invalidate(file)
		count := (i*7919 + 127) % len(spans)
		cache.put(pencodeHighlightCacheEntry{file: file, spans: spans[:count]})
		if i%3 == 0 {
			cache.put(pencodeHighlightCacheEntry{file: file, spans: spans[:count/8]})
		}
		retained := 0
		for slot, entry := range cache.entries {
			retained += cap(entry.spans)
			if entry.file != nil && int(entry.file.highlightSlot) != slot {
				t.Fatal("eviction left a file owning the wrong slot")
			}
		}
		if retained != cache.spans || retained > pencodeHighlightCacheSpanLimit {
			t.Fatalf("retained=%d charged=%d", retained, cache.spans)
		}
		if retained+cap(cache.spare) > pencodeHighlightCacheSpanLimit {
			t.Fatal("spare buffer exceeded the total retention budget")
		}
	}
}

func TestPencodeHighlightCacheRecyclesClosedFileBuffer(t *testing.T) {
	var cache pencodeHighlightCache
	var file openFile
	spans := make([]scene.TextSpan2D, 2048)
	cycle := func() {
		file = openFile{Content: "preview"}
		cache.put(pencodeHighlightCacheEntry{file: &file, source: file.Content, spans: spans})
		cache.remove(&file)
	}
	cycle()
	if allocs := testing.AllocsPerRun(100, cycle); allocs != 0 {
		t.Fatalf("reopening small previews allocated %g paint buffers", allocs)
	}
	for _, entry := range cache.entries {
		if entry.file != nil || entry.source != "" {
			t.Fatal("recycled buffer retained closed file data")
		}
	}
	if cap(cache.spare) != len(spans) || cache.spans != 0 {
		t.Fatal("closed file paint buffer was not recycled")
	}
}
