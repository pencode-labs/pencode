package workbench

import (
	"strings"
	"testing"
	"time"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
	"github.com/pencode-labs/pencode/protocol"
)

func TestFolderSearchPreviewExactMatchAndSyntax(t *testing.T) {
	const text = `var 中文 = "target target"`
	start := strings.LastIndex(text, "target")
	match := protocol.TextMatch{Preview: text, PreviewStart: 100, Start: 100 + start, End: 106 + start}
	tokens := []syntax.Token{{Start: 0, End: 3, CSS: "color:#ff0000"}, {Start: strings.IndexByte(text, '"'), End: len(text), CSS: "color:#00ff00"}}
	var b sourceVNodeView
	b.reset()
	renderFolderSearchPreview(&b, match, tokens)
	nodes := b.nodes()
	if got := hoverNodeText(nodes); got != text {
		t.Fatalf("preview text changed: %q", got)
	}
	marked, before := "", ""
	for _, node := range nodes {
		for _, attr := range node.Attributes {
			if attr.Name == "class" && attr.Value == "pencode-folder-match" {
				marked += node.Text
			}
		}
		if marked == "" {
			before += node.Text
		}
	}
	if marked != "target" || before != text[:start] || hoverTokenStyle(nodes, "var") != "color:#ff0000" || hoverTokenStyle(nodes, "target") != "color:#00ff00" {
		t.Fatalf("incorrect match or syntax: marked=%q before=%q nodes=%+v", marked, before, nodes)
	}
}

func settleFolderPreview(t *testing.T, m *workspaceModel) {
	t.Helper()
	m.highlightFolderSearch()
	deadline := time.Now().Add(5 * time.Second)
	for !m.folderSearch.preview.ready {
		m.pollFolderSearchHighlight()
		if time.Now().After(deadline) {
			t.Fatal("preview highlight timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFolderSearchPreviewThemesPagesAndReuse(t *testing.T) {
	m := workspaceModel{backend: newPencodeBackend(t)}
	t.Cleanup(m.closeFolderSearchWorker)
	s := &m.folderSearch
	s.open, s.revision = true, 1
	s.result.Files = []protocol.SearchFile{{Path: "main.go", Matches: make([]protocol.TextMatch, 13)}}
	for i := range 13 {
		s.result.Files[0].Matches[i].Preview = "func example() bool { return true }"
		s.rows = append(s.rows, folderSearchRow{0, i})
	}
	previous := ""
	for _, id := range []string{"monokai", "dark-plus", "light-plus"} {
		m.syntaxTheme, _ = syntax.BuiltinTheme(id)
		settleFolderPreview(t, &m)
		var b sourceVNodeView
		b.reset()
		renderFolderSearchPreview(&b, s.result.Files[0].Matches[0], s.preview.tokens(0))
		style := hoverTokenStyle(b.nodes(), "func")
		if style == "" || style == previous {
			t.Fatalf("%s did not recolor code: %q", id, style)
		}
		previous = style
	}
	if n := testing.AllocsPerRun(100, m.highlightFolderSearch); n != 0 {
		t.Fatalf("cached preview allocated %g times", n)
	}
	m.advanceFolderSearch(folderSearchPageSize)
	settleFolderPreview(t, &m)
	if s.preview.request.first != 12 || s.preview.request.count != 1 || len(s.preview.rows[1]) != 0 {
		t.Fatal("last page retained earlier rows")
	}
	m.advanceFolderSearch(-folderSearchPageSize)
	settleFolderPreview(t, &m)
	if s.preview.request.first != 0 {
		t.Fatal("preview did not follow the current page")
	}
}

func TestFolderSearchRejectsStalePreview(t *testing.T) {
	m := workspaceModel{}
	s := &m.folderSearch
	s.open, s.revision, s.index = true, 2, folderSearchPageSize
	old := folderPreviewJob{revision: 1, first: 0}
	s.preview.request = old
	s.preview.worker = &latestWorker[folderPreviewJob, folderPreviewResult]{ready: true, result: folderPreviewResult{job: old}, reset: func(*folderPreviewResult) {}}
	if m.pollFolderSearchHighlight() || s.preview.ready || s.dirty {
		t.Fatal("an outdated search/page repainted the current results")
	}
}

func TestFolderSearchResultSpacingAndKeyboardReveal(t *testing.T) {
	app, m, _, _ := languageInteractionFixture(t)
	s := &m.folderSearch
	s.open, s.project, s.dirty = true, m.activeProjectState(), true
	s.result.Files = []protocol.SearchFile{{Path: "main.go", Matches: make([]protocol.TextMatch, folderSearchPageSize)}}
	for i := range folderSearchPageSize {
		s.result.Files[0].Matches[i] = protocol.TextMatch{Start: 4, End: 10, Preview: `var target = "value"`}
		s.rows = append(s.rows, folderSearchRow{0, i})
	}
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	first := editorContextBounds(t, m, "folder-search-result-0")
	second := editorContextBounds(t, m, "folder-search-result-1")
	if first.Height < 46 || first.Height > 50 || second.Y-first.Y-first.Height < 1 {
		t.Fatalf("result rows are compressed: first=%+v second=%+v", first, second)
	}
	// Check the emitted paint, not just the matching span's CSS class.
	computed := pencodeComputedTheme(t, *m.workbenchTheme, `<div class="pencode-folder-result active"><span class="pencode-folder-match" data-ref="match">target</span></div>`)
	want := pencodeSyntaxColor(syntax.Color(computed["match"].BackgroundColor.Color))
	rowControl, _ := m.fontResources.HTMLControl(m.surface, "folder-search-result-0")
	highlight := false
	for _, access := range ecs.Query2[scene.Sprite2D, scene.Parent](app.World).All {
		paint, parent := access.A.Ref(), access.B.Ref()
		if parent.Entity == rowControl && paint.Shape.Fill == want && paint.Width > 0 && paint.Width < 100 && paint.Height > 0 {
			highlight = true
		}
	}
	if !highlight {
		t.Fatal("matched text has no visible background layer")
	}
	icon := s.nodes[0].Children[0].Children[0]
	if icon.Tag != "img" {
		t.Fatal("result has no file icon")
	}
	m.fileIcons = &pencodeFileIcons{suffixes: map[string]string{".go": "icons/folder.svg"}}
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	icon = s.nodes[0].Children[0].Children[0]
	iconSource := ""
	for _, attr := range icon.Attributes {
		if attr.Name == "src" {
			iconSource = attr.Value
		}
	}
	if iconSource != m.themedIcon("icons/folder.svg", "icon.foreground") {
		t.Fatal("search result did not follow the file icon theme", iconSource)
	}
	list, _ := m.fontResources.HTMLNode(m.surface, "folder-search-results")
	area, ok := list.ScrollContainer()
	if !ok {
		t.Fatal("missing result scrollbar")
	}
	scroll := module.ContentMut[scene.UIScroll](m.fontResources, area)
	if !scroll.ScrollbarThin || scroll.ContentSize[1] <= scroll.ViewportSize[1] {
		t.Fatal("results should scroll without shrinking rows")
	}
	for range 11 {
		m.advanceFolderSearch(1)
	}
	markPencodeRootDirtyByModel(m.fontResources, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	last := editorContextBounds(t, m, "folder-search-result-11")
	viewport := editorContextBounds(t, m, "folder-search-results")
	if scroll.Position[1] >= 0 || last.Y+scroll.Position[1] < viewport.Y-1 || last.Y+last.Height+scroll.Position[1] > viewport.Y+viewport.Height+1 {
		t.Fatalf("keyboard selection is outside the viewport: row=%+v viewport=%+v scroll=%g", last, viewport, scroll.Position[1])
	}
}
