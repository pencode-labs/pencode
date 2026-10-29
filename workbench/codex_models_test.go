package workbench

import (
	"math"
	"os"
	"testing"

	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestPencodeCodexModelEffortLayout(t *testing.T) {
	app, model, _, _ := languageInteractionFixture(t)
	css, err := os.ReadFile("../extensions/codex/resources/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	if err := model.applyExtensionUI("codex", &extensions.ActionSession{}, peb.UI{
		ID: "composer", Kind: peb.Panel,
		View: &peb.EditorView{
			HTML: `<section class="codex-composer"><button id="pencode-extension-codex-model" class="codex-model-select" type="button"><span>GPT-6.1 Sol</span><span class="codex-model-effort">高</span></button></section>`,
			CSS:  string(css),
		},
	}); err != nil {
		t.Fatal(err)
	}
	markPencodeRootDirtyByModel(model.fontResources, model)
	for range 4 {
		languageInteractionStep(t, app)
	}
	control, ok := htmlui.ControlByID(app.World, model.surface, "pencode-extension-codex-model")
	if !ok {
		t.Fatal("missing model button")
	}
	var effort, name scene.TextRangeRect2D
	for entity, access := range app.World.Query2[htmlui.ControlText, scene.Text2D]().All {
		if access.A.Ref().Control != control {
			continue
		}
		text := access.B.Ref()
		transform, _ := app.World.Get[scene.UITransform](entity)
		rect := scene.TextRangeRect2D{X: transform.Translation.X.Value, Y: transform.Translation.Y.Value, Height: text.LineHeight}
		switch {
		case text.Value == "高":
			effort = rect
		case text.Value == "GPT-6.1":
			name = rect
		}
	}
	if effort.Height == 0 || name.Height == 0 || effort.X <= name.X || math.Abs(float64(effort.Y-name.Y)) > .01 {
		t.Fatalf("effort must appear to the right of the model on the same line: effort=%+v model=%+v", effort, name)
	}
}
