package workbench

import (
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/onix-engine/onix/font"
	"github.com/onix-engine/onix/html/css"
	"github.com/onix-engine/onix/html/layout"
	"github.com/onix-engine/onix/html/parse"
	"github.com/onix-engine/onix/html/style"
	"github.com/onix-engine/onix/syntax"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestEditorTabsHaveNoGaps(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	project, filePath := model.activeProjectState(), model.activePath
	comparison := &sourceTab{Key: "changes::route.proto", Kind: "changes", Path: "route.proto"}
	model.sourceState().Tabs = []*sourceTab{comparison}
	process := &extensions.ActionSession{}
	for _, title := range []string{strings.Repeat("检查场景加入与退出的流程", 4), "Review"} {
		if err := model.applyExtensionUI("codex", process, peb.UI{ID: "composer", Title: title, Kind: peb.EditorTab, View: &peb.EditorView{HTML: "<p>Codex</p>"}}); err != nil {
			t.Fatal(err)
		}
		surface := model.actions.ui.surfaces[0]
		model.selectEditorTab(pencodeEditorTab{project: project, source: comparison})
		markPencodeRootDirtyByModel(model.fontResources, model)
		languageInteractionStep(t, app)
		file := editorContextBounds(t, model, fileTabID(project, filePath))
		extension := editorContextBounds(t, model, fileTabID(nil, surface.rootID))
		diff := editorContextBounds(t, model, "sc-tab-group:"+comparison.Key)
		if gap := extension.X - (file.X + file.Width); math.Abs(float64(gap)) > .01 {
			t.Errorf("file and extension tab gap = %g px for %q", gap, title)
		}
		if gap := diff.X - (extension.X + extension.Width); math.Abs(float64(gap)) > .01 {
			t.Errorf("extension and comparison tab gap = %g px for %q", gap, title)
		}
	}
}

func TestPencodeLabelsCenterFontEdges(t *testing.T) {
	source, err := os.ReadFile("../resources/workbench/assets/ui/pencode.css")
	if err != nil {
		t.Fatal(err)
	}
	theme, err := syntax.BuiltinTheme("monokai")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparePencodeTheme("monokai", theme, string(source))
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := css.Parse(prepared.css)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"system"} {
		t.Run(name, func(t *testing.T) {
			face, err := font.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = face.Close() })
			bold := face
			for size := pencodeMinUIFontSize; size <= pencodeMaxUIFontSize; size++ {
				t.Run(strconv.Itoa(size), func(t *testing.T) {
					document, err := parse.Parse(`<html style="font-size:` + strconv.Itoa(size) + `px"><body><div class="pencode-root">
<button id="project" class="pencode-project-tab-select"><span class="pencode-project-tab-icon"><span id="project-icon">P</span></span><span id="project-name" class="pencode-project-tab-name">Pencode 项目</span></button>
<div id="tabs" class="pencode-tabs"><div class="pencode-tab active"><button class="pencode-tab-select"><img class="pencode-tab-icon"><span id="tab-name" class="pencode-tab-name">app_ecs.go</span></button><button class="pencode-tab-close"></button></div></div>
<button class="pencode-lsp-result pencode-lsp-reference-location"><span id="reference-label" class="pencode-lsp-result-label"><span>func (b *Bundle) </span><mark id="reference-match">ValidateSceneFeatures</mark><span>(id SceneID)</span></span></button>
<div class="pencode-language-list" style="position:absolute;width:440px"><div class="pencode-language-tool"><div class="pencode-language-summary"><span id="tool-status" class="pencode-setting-description">未安装 · go · golang.org/x/tools/gopls</span></div></div></div>
</div></body></html>`)
					if err != nil {
						t.Fatal(err)
					}
					styles, err := style.ComputeWithViewport(document, sheet, style.Viewport{Width: 800, Height: 100})
					if err != nil {
						t.Fatal(err)
					}
					root, err := layout.BuildWithFonts(document, styles, face, bold, 800, 100)
					if err != nil {
						t.Fatal(err)
					}
					boxes := make(map[string]*layout.Box)
					var visit func(*layout.Box)
					visit = func(box *layout.Box) {
						if id, ok := box.Node.Attr("id"); ok {
							boxes[id] = box
						}
						for _, child := range box.Children {
							visit(child)
						}
					}
					visit(root)
					status := boxes["tool-status"]
					if status == nil || len(status.TextFragments) == 0 {
						t.Fatal("missing language tool status")
					}
					bounds := status.BorderBox()
					for _, fragment := range status.TextFragments {
						for _, glyph := range fragment.Glyphs {
							glyphFace, id := face.ResolveGlyph(glyph.Rune)
							ink, err := glyphFace.GlyphBounds(id)
							if err != nil {
								t.Fatal(err)
							}
							scale := fragment.Style.FontSize.LengthPx / float64(glyphFace.Head.UnitsPerEm)
							top, bottom := glyph.Y-float64(ink.YMax)*scale, glyph.Y-float64(ink.YMin)*scale
							if top < bounds.Y || bottom > bounds.Y+bounds.Height {
								t.Fatalf("status glyph %q spans %.2f..%.2f outside %+v", glyph.Rune, top, bottom, bounds)
							}
						}
					}
					for _, pair := range [][2]string{{"project-icon", "project"}, {"project-name", "project"}, {"tab-name", "tabs"}} {
						label, row := boxes[pair[0]], boxes[pair[1]]
						if label == nil || row == nil || len(label.TextFragments) == 0 {
							t.Fatalf("missing label or row: %v", pair)
						}
						bounds := row.BorderBox()
						wantCenter := bounds.Y + bounds.Height/2
						for _, fragment := range label.TextFragments {
							selected := face
							if fragment.Style.FontWeight.Number >= 600 {
								selected = bold
							}
							capHeight := float64(selected.CapHeight()) * fragment.Style.FontSize.LengthPx / float64(selected.Head.UnitsPerEm)
							if len(fragment.Glyphs) == 0 {
								t.Fatal("label has no glyphs")
							}
							if got := fragment.Glyphs[0].Y - capHeight/2; math.Abs(got-wantCenter) > .01 {
								t.Errorf("%s cap/baseline center = %v, want %v", pair[0], got, wantCenter)
							}
						}
					}
					label, match := boxes["reference-label"], boxes["reference-match"]
					if label == nil || match == nil {
						t.Fatal("missing reference preview")
					}
					baseline, fragments := 0.0, 0
					var checkReference func(*layout.Box, bool)
					checkReference = func(box *layout.Box, highlighted bool) {
						highlighted = highlighted || box == match
						for _, fragment := range box.TextFragments {
							if len(fragment.Glyphs) == 0 {
								continue
							}
							y := fragment.Glyphs[0].Y
							if fragments > 0 && math.Abs(y-baseline) > .01 {
								t.Errorf("reference text baselines differ: %g and %g", baseline, y)
							}
							baseline, fragments = y, fragments+1
							if highlighted {
								bounds := match.BorderBox()
								top := fragment.Rect.Y - bounds.Y
								bottom := bounds.Y + bounds.Height - fragment.Rect.Y - fragment.Rect.Height
								if top < 1.99 || bottom < 1.99 || math.Abs(top-bottom) > .01 {
									t.Errorf("reference highlight padding: top=%g bottom=%g", top, bottom)
								}
							}
						}
						for _, child := range box.Children {
							checkReference(child, highlighted)
						}
					}
					checkReference(label, false)
					if fragments < 3 {
						t.Fatal("missing reference text fragments")
					}
				})
			}
		})
	}
}

func TestPencodeTabLabelVerticalAlignment(t *testing.T) {
	_, session := newPencodeTestApp(t)
	model := session.active.model
	document, err := parse.Parse(`<html><body><div class="pencode-tabs"><div id="tab" class="pencode-tab active"><button class="pencode-tab-select"><img id="icon" class="pencode-tab-icon"><span id="name" class="pencode-tab-name">app_ecs.go</span></button><button id="close" class="pencode-tab-close"></button></div></div></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := css.Parse(model.workbenchTheme.css)
	if err != nil {
		t.Fatal(err)
	}
	styles, err := style.ComputeWithViewport(document, sheet, style.Viewport{Width: 800, Height: 100})
	if err != nil {
		t.Fatal(err)
	}
	root, err := layout.BuildWithFonts(document, styles, model.defaultFont.Face, model.defaultFont.Face, 800, 100)
	if err != nil {
		t.Fatal(err)
	}
	boxes := make(map[string]*layout.Box)
	var visit func(*layout.Box)
	visit = func(box *layout.Box) {
		if id, ok := box.Node.Attr("id"); ok {
			boxes[id] = box
		}
		for _, child := range box.Children {
			visit(child)
		}
	}
	visit(root)
	for _, id := range []string{"tab", "icon", "name", "close"} {
		if boxes[id] == nil {
			t.Fatalf("missing tab element %s", id)
		}
	}
	tab := boxes["tab"].BorderBox()
	wantCenter := tab.Y + tab.Height/2
	for _, id := range []string{"icon", "name", "close"} {
		box := boxes[id]
		bounds := box.BorderBox()
		if center := bounds.Y + bounds.Height/2; math.Abs(center-wantCenter) > 0.01 {
			t.Errorf("%s center = %v, want %v (bounds=%+v)", id, center, wantCenter, bounds)
		}
		for _, fragment := range box.TextFragments {
			if center := fragment.Rect.Y + fragment.Rect.Height/2; math.Abs(center-wantCenter) > 0.01 {
				t.Errorf("%s text center = %v, want %v (fragment=%+v)", id, center, wantCenter, fragment.Rect)
			}
		}
	}
}
