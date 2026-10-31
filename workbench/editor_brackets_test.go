package workbench

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
	engineui "github.com/diiyw/onix/ui"
)

func TestPencodeBracketIndex(t *testing.T) {
	for _, test := range []struct {
		name, path, source string
		pairs              int
	}{
		{"nested", "main.txt", "中文🙂({[value]})\n[]", 4},
		{"multiline", "main.txt", "(\r\n [\n {}\n ]\n)", 3},
		{"incomplete", "main.txt", "([]", 1},
		{"crossing", "main.txt", "([)]", 0},
		{"unmatched", "main.txt", ")][({", 0},
		{"go literals and comments", "main.go", "package p\nfunc f() {\n println(\")\", `]}`, '(') // )]}\n /* ([{ */\n}\n", 3},
		{"python strings and comments", "main.py", "def f():\n return [\"}\", '''([\n]'''] # )]\n", 2},
		{"javascript regexp", "main.js", "const f = () => { return /[)]/.test(\"}\"); }; // ]\n", 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			var document syntax.Document
			defer document.Close()
			theme, _ := syntax.BuiltinTheme("dark-plus")
			var result pencodeHighlightResult
			processPencodeHighlight(&document, pencodeHighlightJob{path: test.path, source: test.source, theme: theme, counted: true, lines: strings.Count(test.source, "\n") + 1, characters: utf8.RuneCountInString(test.source)}, &result)
			if result.err != nil {
				t.Fatal(result.err)
			}
			pairs := 0
			for i, bracket := range result.brackets.items {
				if bracket.Pair < 0 {
					continue
				}
				pair := result.brackets.items[bracket.Pair]
				if pair.Pair != i {
					t.Fatal("bracket pair is not bidirectional")
				}
				if i < bracket.Pair {
					pairs++
					if pencodeBracketMate(test.source[bracket.Offset]) != test.source[pair.Offset] {
						t.Fatal("matched different bracket types")
					}
				}
			}
			if pairs != test.pairs {
				t.Fatalf("pairs = %d, want %d; brackets = %+v", pairs, test.pairs, result.brackets.items)
			}
			resetPencodeHighlightResult(&result)
			if len(result.brackets.items) != 0 || len(result.brackets.stack) != 0 || result.brackets.token != 0 {
				t.Fatal("worker retained stale bracket state")
			}
		})
	}
}

func TestPencodeBracketHighlight(t *testing.T) {
	const source = "中文🙂(a[0]) + {}"
	file := &openFile{Path: "main.txt", contentSnapshot: snapshotText(source)}
	color := graphics.ColorFromRGBA(255, 200, 40, 255)
	model := workspaceModel{activePath: file.Path, files: map[string]*openFile{file.Path: file}, workbenchTheme: &pencodeTheme{bracket: color}}
	defer model.closeHighlightWorker()
	settlePencodeHighlight(t, &model)
	index := &model.editor.brackets
	base := []scene.TextSpan2D{{Start: 0, End: len(source), Color: graphics.White}}
	for _, test := range []struct {
		caret       int
		left, right string
	}{
		{3, "(", ")"}, {4, "(", ")"}, {5, "[", "]"}, {6, "[", "]"},
		{7, "[", "]"}, {8, "(", ")"}, {9, "(", ")"},
		{12, "{", "}"}, {13, "{", "}"}, {14, "{", "}"},
	} {
		field := engineui.TextField{Value: source, Focused: true, Caret: test.caret, Anchor: test.caret}
		got := index.highlight(field, &model, base)
		start, end := strings.Index(source, test.left), strings.Index(source, test.right)
		for offset := range source {
			want := graphics.White
			if offset == start || offset == end {
				want = color
			}
			if pencodeTestColour(got, offset) != want {
				t.Fatalf("caret %d: wrong color at byte %d: %+v", test.caret, offset, got)
			}
		}
	}
	for _, field := range []engineui.TextField{
		{Value: source, Focused: true, Caret: 0, Anchor: 0},
		{Value: source, Focused: true, Caret: 10, Anchor: 10},
		{Value: source, Focused: false, Caret: 3, Anchor: 3},
		{Value: source, Focused: true, Caret: 3, Anchor: 4},
		{Value: source, Focused: true, Caret: 3, Anchor: 3, Composition: "中"},
		{Value: source + "(", Focused: true, Caret: 3, Anchor: 3},
	} {
		if got := index.highlight(field, &model, base); !slices.Equal(got, base) {
			t.Fatalf("inactive, selected, composing or stale field retained bracket highlight: %+v", field)
		}
	}
	if allocations := testing.AllocsPerRun(100, func() {
		for caret := range utf8.RuneCountInString(source) + 1 {
			index.highlight(engineui.TextField{Value: source, Focused: true, Caret: caret, Anchor: caret}, &model, base)
		}
	}); allocations != 0 {
		t.Fatalf("moving caret allocated %g times", allocations)
	}
}

func TestPencodeBracketCache(t *testing.T) {
	a := &openFile{Path: "a.txt", contentSnapshot: snapshotText("()")}
	b := &openFile{Path: "b.txt", contentSnapshot: snapshotText("[]")}
	model := workspaceModel{files: map[string]*openFile{a.Path: a, b.Path: b}, tabs: []string{a.Path, b.Path}}
	defer model.closeHighlightWorker()
	for _, file := range []*openFile{a, b, a, b} {
		model.selectTab(file.Path)
		settlePencodeHighlight(t, &model)
		index := &model.editor.brackets
		if !index.source.Equal(file.snapshot()) || len(index.items) != 2 || index.items[0].Pair != 1 || index.items[1].Pair != 0 {
			t.Fatalf("switch did not restore bracket index: %+v", index)
		}
	}
	if allocations := testing.AllocsPerRun(100, func() {
		for _, file := range []*openFile{a, b} {
			model.selectTab(file.Path)
			renderPencodeHighlight(&model)
		}
	}); allocations != 0 {
		t.Fatalf("cached bracket switches allocated %g times", allocations)
	}
	model.updateActiveContent("[x]")
	model.highlighted = false
	settlePencodeHighlight(t, &model)
	if model.editor.brackets.items[1].Offset != 2 {
		t.Fatal("edit retained stale bracket offsets")
	}
	model.resetHighlight()
	if model.editor.brackets.source.Len() != 0 || model.editor.brackets.items != nil {
		t.Fatal("closing editor retained bracket source or index")
	}
}

func TestPencodeBracketCacheBudget(t *testing.T) {
	var cache pencodeHighlightCache
	var files [70]openFile
	spans := make([]scene.TextSpan2D, pencodeHighlightCacheSpanLimit)
	brackets := make([]pencodeBracket, pencodeHighlightCacheSpanLimit)
	for i := range 160 {
		file := &files[i%len(files)]
		count := (i*7919 + 127) % len(spans)
		cache.put(pencodeHighlightCacheEntry{file: file, spans: spans[:count], brackets: brackets[:len(brackets)-count]})
		cache.put(pencodeHighlightCacheEntry{file: file, spans: spans[:count/8], brackets: brackets[:count/8]})
		retained := cap(cache.spare)
		for _, entry := range cache.entries {
			retained += cap(entry.spans) + cap(entry.brackets)
		}
		if retained-cap(cache.spare) != cache.spans || retained > pencodeHighlightCacheSpanLimit {
			t.Fatalf("bracket cache exceeded budget: retained=%d charged=%d", retained, cache.spans)
		}
	}
	file := &files[0]
	brackets[0] = pencodeBracket{Offset: 1, Pair: -1}
	cache.put(pencodeHighlightCacheEntry{file: file, brackets: brackets[:1]})
	brackets[0].Offset = 99
	if cache.entries[file.highlightSlot].brackets[0].Offset != 1 {
		t.Fatal("cache aliases worker bracket memory")
	}
	cache.put(pencodeHighlightCacheEntry{file: file, spans: spans, brackets: brackets[:1]})
	if cache.entries[file.highlightSlot].file == file {
		t.Fatal("oversized combined paint and bracket result was cached")
	}
}

func TestPencodeBracketEditorPaint(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	world := model.fontResources
	const source = "package demo\nfunc f() {\n println(\"中文\", []int{1})\n}\n"
	model.updateActiveContent(source)
	model.highlighted = false // Complete parsing without waiting for edit debounce.
	settlePencodeHighlight(t, model)
	paint := func(caret int) []scene.TextSpan2D {
		t.Helper()
		model.setEditorValueAndCaret(world, source, caret)
		app.RequestRender()
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
		value, _ := world.Get[scene.Text2D](model.editor.text)
		return value.Spans
	}
	start, end := strings.Index(source, "{"), strings.LastIndex(source, "}")
	caret := utf8.RuneCountInString(source[:start])
	spans := paint(caret)
	if pencodeTestColour(spans, start) != model.workbenchTheme.bracket || pencodeTestColour(spans, end) != model.workbenchTheme.bracket {
		t.Fatal("native editor did not paint matching brackets")
	}
	spans = paint(caret + 1)
	if pencodeTestColour(spans, start) != model.workbenchTheme.bracket || pencodeTestColour(spans, end) != model.workbenchTheme.bracket {
		t.Fatal("moving after the opening bracket lost highlighting")
	}
	spans = paint(0)
	if !slices.Equal(spans, model.languageHighlight(model.highlightScratch)) {
		t.Fatal("moving away from brackets did not restore syntax colors")
	}
}
