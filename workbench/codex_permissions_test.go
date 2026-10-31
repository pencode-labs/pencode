package workbench

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestPencodeCodexPermissionTextLayout(t *testing.T) {
	app, model, _, _ := languageInteractionFixture(t)
	css, err := os.ReadFile("testdata/codex/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []float32{336, 240} {
		t.Run(strconv.Itoa(int(width)), func(t *testing.T) {
			err := model.applyExtensionUI("codex", &extensions.ActionSession{}, peb.UI{
				ID: "permissions", Kind: peb.ContextMenu, Placement: peb.UIPlacement{Mode: peb.PositionAbsolute, X: 100, Y: 100, Width: width},
				View: &peb.EditorView{
					HTML: `<section class="codex-permission-menu"><button id="pencode-extension-codex-ask" data-ref="ask" class="codex-permission-option" type="button"><img class="codex-permission-icon"><span class="codex-permission-copy"><span class="codex-permission-label">请批准</span><span class="codex-permission-description">编辑外部文件和使用互联网时始终询问</span></span></button></section>`,
					CSS:  string(css),
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			markPencodeRootDirtyByModel(model.fontResources, model)
			for range 4 {
				languageInteractionStep(t, app)
			}
			control, ok := htmlui.ControlByID(app.World, model.surface, "pencode-extension-codex-ask")
			if !ok {
				t.Fatal("missing permission option")
			}
			var label, description scene.TextRangeRect2D
			for entity, access := range app.World.Query2[htmlui.ControlText, scene.Text2D]().All {
				if access.A.Ref().Control != control {
					continue
				}
				text := access.B.Ref()
				transform, _ := app.World.Get[scene.UITransform](entity)
				rect := scene.TextRangeRect2D{Y: transform.Translation.Y.Value, Height: text.LineHeight}
				switch {
				case strings.HasPrefix(text.Text().String(), "请"):
					label = rect
				case strings.HasPrefix(text.Text().String(), "编"):
					description = rect
				}
			}
			if label.Height == 0 || description.Height == 0 {
				t.Fatal("missing permission text fragments")
			}
			if description.Y < label.Y+label.Height-.01 {
				t.Fatalf("permission description overlaps label: label=%+v description=%+v", label, description)
			}
		})
	}
	styles := pencodeComputedTheme(t, *model.workbenchTheme, `<div class="pencode-root"><span data-ref="label">IDE</span><div class="pencode-extension-content"><span data-ref="text">Extension</span><kbd data-ref="key">Enter</kbd><span data-ref="custom" style="text-box-trim:trim-both">Custom</span></div></div>`)
	for ref, want := range map[string]string{"label": "trim-both", "text": "none", "key": "none", "custom": "trim-both"} {
		if styles[ref] == nil || styles[ref].TextBoxTrim.Keyword != want {
			t.Fatalf("%s text-box-trim should be %s", ref, want)
		}
	}
}
