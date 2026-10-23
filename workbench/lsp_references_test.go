package workbench

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/pencode-labs/pencode/lsp"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
)

func TestLanguageReferencesGroupInterleavedFiles(t *testing.T) {
	model, project, _ := languageTestModel(t)
	panel := &model.languages.panel
	panel.method, panel.open = "textDocument/references", true
	first := lsp.FileURI(filepath.Join(project.Path, "ui", "html", "surface.go"))
	second := lsp.FileURI(filepath.Join(project.Path, "other", "surface.go"))
	root := lsp.FileURI(filepath.Join(project.Path, "main.go"))
	for name, content := range map[string]string{
		"ui/html/surface.go": strings.Repeat("// before\r\n", 8) + "\tfirstUse()\r\n" + strings.Repeat("// between\r\n", 12) + "\tsecondUse()",
		"other/surface.go":   strings.Repeat("// before\n", 4) + "\totherUse()\n",
	} {
		filename := filepath.Join(project.Path, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	locations := []lsp.Location{
		{URI: first, Range: lsp.Range{Start: lsp.Position{Line: 8, Character: 2}}},
		{URI: second, Range: lsp.Range{Start: lsp.Position{Line: 4, Character: 3}}},
		{URI: first, Range: lsp.Range{Start: lsp.Position{Line: 21, Character: 6}}},
		{URI: root},
	}
	raw, err := json.Marshal(locations)
	if err != nil {
		t.Fatal(err)
	}
	model.languageLocationRows(raw)
	settleReferences(t, model)
	if len(panel.referenceGroups) != 3 || len(panel.rows) != 3 {
		t.Fatalf("got %d groups, %d rows", len(panel.referenceGroups), len(panel.rows))
	}
	for i, want := range []struct{ name, path, count string }{
		{"surface.go", "ui/html", "2"},
		{"surface.go", "other", "1"},
		{"main.go", ".", "1"},
	} {
		group := &panel.referenceGroups[i]
		if group.expanded {
			t.Fatalf("group %d is expanded by default", i)
		}
		if group.header.label != want.name || group.header.fileName != want.name || group.header.path != want.path || group.header.count != want.count {
			t.Fatalf("group %d: %+v", i, group.header)
		}
	}
	model.chooseLanguageRow(nil, 0)
	if panel.rows[1].label != "firstUse()" || panel.rows[2].label != "secondUse()" || panel.referenceGroups[1].rows[0].label != "otherUse()" {
		t.Fatalf("incorrect grouped source lines: %+v", panel.rows)
	}
	for i, index := range []int{0, 2} {
		row := panel.referenceGroups[0].rows[i]
		if row.location == nil || *row.location != locations[index] || row.fileName != "" {
			t.Fatalf("grouped location changed: %+v", row)
		}
	}
	model.chooseLanguageRow(nil, 0)
	if !panel.open || len(panel.rows) != 3 || panel.referenceGroups[0].expanded || panel.rows[1].label != "surface.go" {
		t.Fatal("collapse did not keep the file headers and remaining locations")
	}
	if allocations := testing.AllocsPerRun(100, func() {
		model.chooseLanguageRow(nil, 0)
		model.chooseLanguageRow(nil, 0)
	}); allocations != 0 {
		t.Fatalf("expanding and collapsing allocated %g times", allocations)
	}
	model.chooseLanguageRow(nil, 0)
	if len(panel.rows) != 5 || panel.rows[2].location == nil || *panel.rows[2].location != locations[2] {
		t.Fatal("expanding lost a reference location")
	}
}

func TestLanguageReferenceCollapseKeepsPaginationSelection(t *testing.T) {
	model, project, _ := languageTestModel(t)
	panel := &model.languages.panel
	panel.method = "textDocument/references"
	locations := make([]lsp.Location, 15)
	for i := range locations {
		locations[i] = lsp.Location{URI: lsp.FileURI(filepath.Join(project.Path, fmt.Sprintf("file%d.go", i)))}
	}
	raw, _ := json.Marshal(locations)
	model.languageLocationRows(raw)
	settleReferences(t, model)
	const header = 12
	model.chooseLanguageRow(nil, header)
	if panel.selection != header || panel.page != 1 || len(panel.rows) != 16 || panel.rows[header].count != "1" {
		t.Fatalf("expansion moved selection: selection=%d page=%d rows=%d", panel.selection, panel.page, len(panel.rows))
	}
	model.chooseLanguageRow(nil, header)
	if panel.selection != header || panel.page != 1 || len(panel.rows) != 15 {
		t.Fatal("collapse did not preserve selection and pagination")
	}
}

func TestLanguageDefinitionsRemainUngrouped(t *testing.T) {
	model, project, _ := languageTestModel(t)
	panel := &model.languages.panel
	panel.method = "textDocument/definition"
	uri := lsp.FileURI(filepath.Join(project.Path, "main.go"))
	raw, _ := json.Marshal([]lsp.Location{{URI: uri}, {URI: uri, Range: lsp.Range{Start: lsp.Position{Line: 2}}}})
	model.languageLocationRows(raw)
	settleReferences(t, model)
	if len(panel.rows) != 2 || len(panel.referenceGroups) != 0 || panel.rows[0].location == nil || panel.rows[1].location == nil {
		t.Fatal("definition navigation was grouped")
	}
}

func TestLanguageReferenceGroupControlsAndNavigation(t *testing.T) {
	app, model, provider, _ := languageInteractionFixture(t)
	world := model.fontResources
	value := "package demo\nvar symbol = 1\nvar other = symbol\n"
	model.updateActiveContent(value)
	model.syncLanguageDocuments()
	panel := &model.languages.panel
	panel.method, panel.open, panel.session = "textDocument/references", true, provider
	uri := lsp.FileURI(filepath.Join(model.projectPath, model.activeFile().Path))
	raw, _ := json.Marshal([]lsp.Location{
		{URI: uri, Range: lsp.Range{Start: lsp.Position{Line: 1, Character: 4}, End: lsp.Position{Line: 1, Character: 10}}},
		{URI: uri, Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 12}, End: lsp.Position{Line: 2, Character: 18}}},
	})
	model.languageLocationRows(raw)
	settleReferences(t, model)
	step := func() {
		t.Helper()
		markPencodeRootDirtyByModel(world, model)
		languageInteractionStep(t, app)
	}
	controlTag := func(ref string) string {
		t.Helper()
		node, _ := world.HTMLNode(model.surface, "lsp-panel")
		markup, err := node.Serialize()
		if err != nil {
			t.Fatal(err)
		}
		index := strings.Index(markup, `data-ref="`+ref+`"`)
		if index < 0 {
			t.Fatal("missing grouped control", ref)
		}
		start := strings.LastIndex(markup[:index], "<")
		end := index + strings.Index(markup[index:], ">") + 1
		return markup[start:end]
	}
	step()
	header, _ := world.HTMLNode(model.surface, "lsp-panel-header")
	if _, visible := header.Bounds(); visible {
		t.Fatal("references retained the empty header and close button")
	}
	if panel.referenceGroups[0].rows[0].label != "var symbol = 1" || panel.referenceGroups[0].rows[1].label != "var other = symbol" {
		t.Fatal("reference previews did not use unsaved editor content")
	}
	for _, ref := range []string{"lsp-row-0-icon", "lsp-row-0-label", "lsp-row-0-path", "lsp-row-0-count", "lsp-row-0-toggle"} {
		if strings.Contains(controlTag(ref), " hidden") {
			t.Fatal("hidden grouped file control", ref)
		}
	}
	if tag := controlTag("lsp-row-0"); !strings.Contains(tag, `aria-expanded="false"`) || len(panel.rows) != 1 {
		t.Fatalf("file group is not collapsed by default: %s", tag)
	}
	if tag := controlTag("lsp-row-0-toggle"); !strings.HasPrefix(tag, "<img ") || !strings.Contains(tag, "icons/chevron-right.svg") {
		t.Fatalf("disclosure is not a themed SVG: %s", tag)
	}
	node, _ := world.HTMLNode(model.surface, "lsp-panel")
	markup, _ := node.Serialize()
	if !strings.Contains(markup, ">2</span>") {
		t.Fatal("missing rendered reference count")
	}
	model.chooseLanguageRow(world, 0)
	step()
	if !strings.Contains(controlTag("lsp-row-0"), `aria-expanded="true"`) {
		t.Fatal("expansion was not rendered")
	}
	markup, _ = node.Serialize()
	if strings.Count(markup, ">symbol</mark>") != 2 {
		t.Fatalf("reference ranges were not highlighted: %s", markup)
	}
	match := pencodeComputedTheme(t, *model.workbenchTheme, `<button class="pencode-lsp-reference-location"><mark data-ref="match">symbol</mark></button>`)["match"]
	if match == nil || match.BackgroundColor.Color.A == 0 {
		t.Fatal("reference highlight has no theme background")
	}
	background := pencodeSyntaxColor(syntax.Color(match.BackgroundColor.Color))
	checkHighlights := func(want int) {
		t.Helper()
		count := 0
		for entity, access := range module.ContentQuery[scene.Sprite2D](world) {
			sprite := access.Ref()
			visible, _ := module.ContentGet[scene.Visibility](world, entity)
			if visible.Visible && sprite.Shape.Fill == background && sprite.Width > 0 && sprite.Height > 0 {
				count++
			}
		}
		if count != want {
			t.Fatalf("painted %d reference highlight backgrounds, want %d", count, want)
		}
	}
	checkHighlights(2)
	model.chooseLanguageRow(world, 0)
	step()
	checkHighlights(0)
	if !strings.Contains(controlTag("lsp-row-0"), `aria-expanded="false"`) {
		t.Fatal("collapse was not rendered")
	}
	panel.method = "textDocument/definition"
	panel.rows = []pencodeLanguageRow{{label: "plain definition"}}
	step()
	markup, _ = node.Serialize()
	if !strings.Contains(markup, ">plain definition</span>") || strings.Contains(markup, ">symbol</mark>") {
		t.Fatalf("reused rows retained reference highlights: %s", markup)
	}
	panel.method = "textDocument/references"
	panel.rebuildReferenceRows(0)
	model.chooseLanguageRow(world, 0)
	step()
	checkHighlights(2)
	for _, ref := range []string{"lsp-row-1-icon", "lsp-row-1-path", "lsp-row-1-count", "lsp-row-1-toggle"} {
		if !strings.Contains(controlTag(ref), " hidden") {
			t.Fatal("reference location repeats file metadata", ref)
		}
	}
	model.chooseLanguageRow(world, 2)
	if panel.open || panel.navigate != model.activeFile() || panel.offset != strings.LastIndex(value, "symbol") {
		t.Fatalf("grouped reference did not navigate to its original location: open=%v offset=%d", panel.open, panel.offset)
	}
}

func TestLanguageReferenceHighlightRanges(t *testing.T) {
	const content = "// before\r\n\t\u3000call(\"😀中文<>&\", symbol, symbol) \t\r\n"
	for _, encoding := range []lsp.PositionEncoding{lsp.UTF8, lsp.UTF16, lsp.UTF32} {
		t.Run(string(encoding), func(t *testing.T) {
			start := strings.LastIndex(content, "symbol")
			from, _ := lsp.PositionAt(content, start, encoding)
			to, _ := lsp.PositionAt(content, start+len("symbol"), encoding)
			group := pencodeReferenceGroup{rows: []pencodeLanguageRow{{location: &lsp.Location{Range: lsp.Range{Start: from, End: to}}}}}
			starts := group.setPreviews(content, nil, encoding)
			row := &group.rows[0]
			if row.label != `call("😀中文<>&", symbol, symbol)` || row.matchStart != strings.LastIndex(row.label, "symbol") || row.label[row.matchStart:row.matchEnd] != "symbol" {
				t.Fatalf("incorrect trimmed reference: %+v", row)
			}
			if allocations := testing.AllocsPerRun(100, func() { starts = group.setPreviews(content, starts, encoding) }); allocations != 0 {
				t.Fatalf("reference range preparation allocated %g times", allocations)
			}
			row.location.Range.End = lsp.Position{Line: 2}
			group.setPreviews(content, starts, encoding)
			if row.label[row.matchStart:row.matchEnd] != "symbol)" {
				t.Fatalf("multiline range was not clipped to preview: %+v", row)
			}
			row.location.Range.End = from
			group.setPreviews(content, starts, encoding)
			if row.matchStart != row.matchEnd {
				t.Fatal("empty range retained its previous highlight")
			}
		})
	}
}

func TestLanguageReferencePopupTracksActualHeight(t *testing.T) {
	app, model, provider, _ := languageInteractionFixture(t)
	world := model.fontResources
	content := strings.Repeat("// before\n", 60)
	model.updateActiveContent(content)
	model.setEditorValueAndCaret(world, content, 0)
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	body, _ := world.HTMLNode(model.surface, "editor-body")
	viewport, _ := body.Bounds()
	editor, _ := world.HTMLNode(model.surface, "editor")
	editorRect, _ := editor.Bounds()
	text, _, _ := model.languageEditorGeometry(world, editorRect)
	_, _, _, lineHeight, ok := scene.TextCaretRect2D(text, content, 0)
	if !ok || lineHeight <= 0 {
		t.Fatal("missing editor caret geometry")
	}
	line := int((viewport.Height - 150) / lineHeight)
	content = strings.Repeat("// before\n", line) + "var symbol = 1\n"
	offset := strings.Index(content, "symbol")
	model.updateActiveContent(content)
	model.setEditorValueAndCaret(world, content, offset)
	model.syncLanguageDocuments()
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	model.runLanguageAction(world, "textDocument/references", false)
	job := languageInteractionJob(t, provider, "textDocument/references")
	position, _ := lsp.PositionAt(content, offset, lsp.UTF16)
	locations := make([]lsp.Location, 10)
	for i := range locations {
		locations[i] = lsp.Location{URI: job.uri, Range: lsp.Range{Start: position, End: lsp.Position{Line: position.Line, Character: position.Character + 6}}}
	}
	raw, _ := json.Marshal(locations)
	model.languages.events <- pencodeLanguageEvent{session: provider, method: "result", job: job, result: raw}
	step := func() {
		t.Helper()
		markPencodeRootDirtyByModel(world, model)
		for range 3 {
			languageInteractionStep(t, app)
		}
	}
	step()
	panel := &model.languages.panel
	_, anchorY := model.editorPopupPoint(world, offset)
	if anchorY+280 <= viewport.Height || anchorY+80 >= viewport.Height {
		t.Fatalf("fixture does not distinguish actual and estimated height: anchor=%g viewport=%g", anchorY, viewport.Height)
	}
	popup, _ := world.HTMLNode(model.surface, "lsp-panel")
	checkBelowSymbol := func() {
		t.Helper()
		bounds, visible := popup.Bounds()
		if !visible || math.Abs(float64(bounds.Y-viewport.Y-anchorY)) > 2 {
			t.Fatalf("collapsed popup detached from symbol: bounds=%+v viewport=%+v anchor=%g", bounds, viewport, anchorY)
		}
	}
	checkBelowSymbol()
	model.chooseLanguageRow(world, 0)
	step()
	expanded, _ := popup.Bounds()
	if expanded.Y+expanded.Height > viewport.Y+viewport.Height-3 || panel.anchorY >= anchorY {
		t.Fatalf("expanded popup was not constrained to editor: %+v", expanded)
	}
	model.chooseLanguageRow(world, 0)
	step()
	checkBelowSymbol()
}

func TestLanguageReferenceSourceLines(t *testing.T) {
	content := "\tcall(\"中文 <>&\")\r\n\t\r\n  return value"
	group := pencodeReferenceGroup{}
	for _, line := range []int{2, 0, 1, 0, 3, -1} {
		group.rows = append(group.rows, pencodeLanguageRow{location: &lsp.Location{Range: lsp.Range{Start: lsp.Position{Line: line}}}})
	}
	starts := group.setPreviews(content, nil, lsp.UTF16)
	for i, want := range []string{"return value", `call("中文 <>&")`, "", `call("中文 <>&")`, "源码位置已失效", "源码位置已失效"} {
		if group.rows[i].label != want {
			t.Fatalf("source line %d: got %q, want %q", i, group.rows[i].label, want)
		}
	}
	if allocations := testing.AllocsPerRun(100, func() { starts = group.setPreviews(content, starts, lsp.UTF16) }); allocations != 0 {
		t.Fatalf("cached line index allocated %g times", allocations)
	}
}

func TestLanguageReferencesDismissOnlyOutsidePanel(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	world := model.fontResources
	for _, button := range []input.MouseButton{input.MouseLeft, input.MouseRight, input.MouseMiddle} {
		panel := &model.languages.panel
		panel.method, panel.open, panel.body = "textDocument/references", true, "请求中…"
		panel.anchorX, panel.anchorY = 100, 100
		markPencodeRootDirtyByModel(world, model)
		languageInteractionStep(t, app)
		node, _ := world.HTMLNode(model.surface, "lsp-panel")
		bounds, visible := node.Bounds()
		if !visible {
			t.Fatal("missing references popup")
		}
		click := func(x, y float64) {
			t.Helper()
			scale := float64(model.settings.Zoom) / 100
			state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: button, X: x * scale, Y: y * scale})
			state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: button, X: x * scale, Y: y * scale})
			languageInteractionStep(t, app)
		}
		click(float64(bounds.X+bounds.Width/2), float64(bounds.Y+bounds.Height/2))
		if !panel.open {
			t.Fatalf("button %v inside popup dismissed it", button)
		}
		click(1, 1)
		if panel.open {
			t.Fatalf("button %v outside popup did not dismiss it", button)
		}
	}
}
