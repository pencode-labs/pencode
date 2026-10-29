package workbench

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/render"
	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/lsp"
)

func TestPencodeEditorDiagnosticsFromUnopenedProblem(t *testing.T) {
	for _, variant := range []string{"canonical URI", "escaped URI", "scrolled target"} {
		t.Run(variant, func(t *testing.T) {
			app, model, provider, _ := languageInteractionFixture(t)
			world, project := model.fontResources, model.activeProjectState()
			const filename = "unopened.go"
			content := "package demo\n\nfunc broken() { missing() }\n"
			line := 2
			if variant == "scrolled target" {
				content = strings.Repeat("// padding\n", 60) + content
				line += 60
			}
			resourceTestWrite(t, project, filename, content)
			uri := lsp.FileURI(filepath.Join(project.Path, filename))
			if variant == "escaped URI" {
				uri = lsp.URI(strings.Replace(string(uri), "unopened.go", "%75nopened.go", 1))
				if volume := filepath.VolumeName(project.Path); len(volume) == 2 && volume[1] == ':' {
					uri = lsp.URI(strings.Replace(string(uri), volume, strings.ToLower(volume[:1])+"%3a", 1))
				}
			}
			diagnostic := lsp.Diagnostic{Severity: 1, Message: "undefined: missing", Range: lsp.Range{
				Start: lsp.Position{Line: line, Character: 16}, End: lsp.Position{Line: line, Character: 23},
			}}
			raw, err := json.Marshal(lsp.PublishDiagnosticsParams{URI: uri, Diagnostics: []lsp.Diagnostic{diagnostic}})
			if err != nil {
				t.Fatal(err)
			}
			model.languages.events <- pencodeLanguageEvent{session: provider, method: "textDocument/publishDiagnostics", params: raw}
			model.bottom.active = pencodePanelProblems
			markPencodeRootDirtyByModel(world, model)
			languageInteractionStep(t, app)
			clicked := false
			for _, access := range world.Query[htmlui.Component[pencodeProblemProps, struct{}]]() {
				props := access.Get().Props
				if props.Problem.location.URI == uri {
					handleProblemEvent(&module.ContentEventContext[pencodeProblemProps, struct{}]{World: world, Props: &props, Ref: "problem"})
					clicked = true
				}
			}
			if !clicked {
				t.Fatal("missing problem row")
			}
			if err := awaitFileIO(t, model); err != nil {
				t.Fatal(err)
			}
			if model.activeFile() == nil || model.activeFile().Path != filename {
				t.Fatal("problem click did not open its file")
			}
			// The first editor layout can precede the next LSP document sync.
			model.editor.syncText(world, model.surface, model)
			model.editor.diagnostics.sync(world, model)
			assertPaint := func() {
				t.Helper()
				paint := &model.editor.diagnostics
				if len(paint.ranges) != 1 || len(paint.quads) == 0 || len(paint.waves) == 0 || len(paint.labels) == 0 {
					t.Fatalf("jump lost cached diagnostic: ranges=%d waves=%d labels=%d", len(paint.ranges), len(paint.waves), len(paint.labels))
				}
				label, _ := world.Get[scene.Text2D](paint.labels[0])
				if label.Value != diagnostic.Message {
					t.Fatalf("inline error = %q", label.Value)
				}
			}
			if len(model.editor.diagnostics.ranges) != 1 {
				t.Fatal("cached diagnostic requires an LSP document")
			}
			languageInteractionStep(t, app)
			// Later procedural UI creation must not renumber editor decorations.
			world.Spawn(ecs.NewBundle(0))
			for range 3 {
				languageInteractionStep(t, app)
			}
			assertPaint()
			paint := &model.editor.diagnostics
			textStack, _ := world.Get[scene.UIStackIndex](model.editor.text)
			for _, entity := range []ecs.Entity{paint.waves[0], paint.labels[0]} {
				stack, _ := world.Get[scene.UIStackIndex](entity)
				if stack <= textStack {
					t.Fatalf("diagnostic was moved behind the editor after idle frames: stack=%d text=%d", stack, textStack)
				}
			}
			if err := scene.SyncUIGlobalTransformSystem("").Run(app.World); err != nil {
				t.Fatal(err)
			}
			canvas := graphics.NewCanvas2D(nil, 1440, 900)
			graphics.BindFrame(app.World, canvas)
			scene.RegisterSpriteRenderer2D(app.World)
			scene.RegisterTextRenderer2D(app.World)
			if err := scene.ExtractRender2DSystem("").Run(app.World); err != nil {
				t.Fatal(err)
			}
			rendered, _ := app.World.GetResource[*scene.RenderWorld]()
			for _, entity := range []ecs.Entity{paint.quads[0], paint.waves[0], paint.labels[0]} {
				found := false
				for _, item := range rendered.Items2D {
					found = found || ecs.Entity(reflect.ValueOf(item).FieldByName("entity").Uint()) == entity
				}
				if !found {
					t.Fatalf("diagnostic visual %d was culled from the final render output", entity)
				}
			}
		})
	}
}

func TestPencodeEditorDiagnosticDecorations(t *testing.T) {
	app, model, provider, _ := languageInteractionFixture(t)
	world := model.fontResources
	file := model.activeFile()
	file.Content = "\t中文🙂 := sourceCommand(ctx)\nnext()\n"
	model.setEditorValueAndCaret(world, file.Content, 0)
	model.syncLanguageDocuments()
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	doc := model.languages.documents[file]
	start := strings.Index(file.Content, "sourceCommand")
	position, _ := lsp.PositionAt(file.Content, start, lsp.UTF16)
	end, _ := lsp.PositionAt(file.Content, start+len("sourceCommand"), lsp.UTF16)
	diagnostic := lsp.Diagnostic{Severity: 1, Message: "undefined: sourceCommand", Range: lsp.Range{Start: position, End: end}}
	publish := func(items []lsp.Diagnostic, version int) {
		t.Helper()
		raw, err := json.Marshal(lsp.PublishDiagnosticsParams{URI: doc.uri, Version: &version, Diagnostics: items})
		if err != nil {
			t.Fatal(err)
		}
		model.languages.events <- pencodeLanguageEvent{session: provider, method: "textDocument/publishDiagnostics", params: raw}
		languageInteractionStep(t, app)
	}
	publish([]lsp.Diagnostic{diagnostic}, doc.version)
	paint := &model.editor.diagnostics
	if len(paint.ranges) != 1 || paint.ranges[0].start != start || paint.ranges[0].end != start+len("sourceCommand") {
		t.Fatalf("UTF-16 diagnostic range = %+v", paint.ranges)
	}
	if len(paint.quads) != 1 || len(paint.waves) != 1 || len(paint.labels) != 1 {
		t.Fatalf("missing background, waves or label: quads=%d waves=%d labels=%d", len(paint.quads), len(paint.waves), len(paint.labels))
	}
	background, ok := world.Get[scene.Sprite2D](paint.quads[0])
	size, _ := world.Get[scene.Size2D](model.editor.control)
	if !ok || background.Width != size.Width || background.Shape.Fill.A != paint.key.colors[1].A*.1 {
		t.Fatalf("full row background = %+v", background)
	}
	label, _ := world.Get[scene.Text2D](paint.labels[0])
	if label.Value != diagnostic.Message || label.Color != paint.key.colors[1] || label.Font != model.codeFont() {
		t.Fatalf("inline error label = %+v", label)
	}
	up, _ := world.Get[scene.UITransform](paint.waves[0])
	wave, _ := world.Get[scene.Sprite2D](paint.waves[0])
	uMode, _ := wave.Texture.AddressModes()
	if up.Rotation != 0 || wave.Height != 3 || uMode != render.TextureAddressRepeat ||
		math.Abs(float64((wave.Texture.MaxU-wave.Texture.U)*6-wave.Width)) > .01 {
		t.Fatal("underline does not repeat VS Code's 6x3 decoration")
	}
	t.Run("GPU submission", func(t *testing.T) {
		driver := &pencodeDiagnosticDrawDriver{pencodeIconDriver: pencodeIconDriver{
			pixels: make(map[render.TextureHandle][]byte), descriptors: make(map[render.TextureHandle]render.TextureDescriptor),
		}}
		device, err := render.NewDevice(driver)
		if err != nil {
			t.Fatal(err)
		}
		defer device.Destroy()
		// Use the canvas's device explicitly, including textures decoded before it existed.
		canvas := graphics.NewCanvas2D(device, 1440, 900)
		canvas.BindTexture(wave.Texture)
		canvas.DrawTintedAffine([6]float32{wave.Width, 0, 0, 0, wave.Height, 0},
			[6]float32{wave.Texture.MaxU - wave.Texture.U, 0, wave.Texture.U, 0, 1, 0}, wave.Tint, 1)
		if err := canvas.FinishFrame(); err != nil {
			t.Fatal(err)
		}
		if driver.instances != 1 {
			t.Fatalf("submitted diagnostic instances = %d, want 1", driver.instances)
		}
		handle := wave.Texture.RenderHandleFor(device)
		if !handle.Valid() || handle != paint.waveTexture.RenderHandleFor(device) {
			t.Fatal("wave view lost its backing GPU texture")
		}
		if descriptor := driver.descriptors[handle]; descriptor.WrapU != render.TextureAddressRepeat || descriptor.Width != 24 || descriptor.Height != 12 {
			t.Fatalf("wave GPU texture = %+v", descriptor)
		}
	})
	text := world.Mut[scene.Text2D](model.editor.text)
	var offsets = [2]int{start, start + len("sourceCommand")}
	var widths [2]float32
	if !scene.MeasureTextLineOffsets2D(text, strings.TrimSuffix(strings.Split(file.Content, "\n")[0], "\r"), offsets[:], widths[:]) || math.Abs(float64(up.Translation.X.Value-paint.key.x-widths[0])) > .1 {
		t.Fatal("wave does not align with the diagnostic after tabs and Unicode")
	}
	if allocations := testing.AllocsPerRun(100, func() { paint.sync(world, model) }); allocations != 0 {
		t.Fatalf("unchanged diagnostics allocated %g times", allocations)
	}
	// Native scroll moves the wave and label together in the same layout pass.
	world.Mut[scene.UITransform](model.editor.text).Translation.Y.Value -= 5
	paint.sync(world, model)
	scrolled, _ := world.Get[scene.UITransform](paint.waves[0])
	if scrolled.Translation.Y.Value != up.Translation.Y.Value-5 {
		t.Fatal("diagnostic did not follow native scroll")
	}
	if allocations := testing.AllocsPerRun(100, func() {
		position := world.Mut[scene.UITransform](model.editor.text)
		position.Translation.Y.Value = -5 - position.Translation.Y.Value
		paint.sync(world, model)
	}); allocations != 0 {
		t.Fatalf("warm diagnostic scrolling allocated %g times", allocations)
	}
	for _, zoom := range []int{150, 70, 100} {
		model.setZoom(world, zoom)
		markPencodeRootDirtyByModel(world, model)
		languageInteractionStep(t, app)
		source, _ := world.Get[scene.Text2D](model.editor.text)
		label, _ := world.Get[scene.Text2D](paint.labels[0])
		if paint.key.layout.fontSize != source.FontSize || label.FontSize != source.FontSize || label.DPI != source.DPI {
			t.Fatalf("diagnostic did not follow zoom %d", zoom)
		}
	}
	publish(nil, doc.version-1)
	if visible, _ := world.Get[scene.Visibility](paint.root); !visible.Visible {
		t.Fatal("stale diagnostics cleared current decorations")
	}
	publish(nil, doc.version)
	if visible, _ := world.Get[scene.Visibility](paint.root); visible.Visible {
		t.Fatal("cleared diagnostics left decorations visible")
	}
	for _, entity := range append(append(slices.Clone(paint.quads), paint.labels...), paint.waves...) {
		if visible, _ := world.Get[scene.Visibility](entity); visible.Visible {
			t.Fatal("cleared diagnostics left a native visual visible")
		}
	}
	for _, severity := range []int{2, 3, 4, 0} {
		diagnostic.Severity = severity
		publish([]lsp.Diagnostic{diagnostic}, doc.version)
		if len(paint.ranges) != 1 || len(paint.quads) != 1 || len(paint.waves) != 1 || len(paint.labels) != 1 {
			t.Fatalf("severity %d did not reuse diagnostic decorations", severity)
		}
		color := model.workbenchTheme.diagnosticColors[severity]
		fill := color
		fill.A *= .1
		background, _ := world.Get[scene.Sprite2D](paint.quads[0])
		wave, _ := world.Get[scene.Sprite2D](paint.waves[0])
		label, _ := world.Get[scene.Text2D](paint.labels[0])
		if background.Shape.Fill != fill || wave.Tint != color || label.Color != color || label.Value != diagnostic.Message {
			t.Fatalf("severity %d has incorrect decoration colors: background=%+v wave=%+v label=%+v", severity, background.Shape.Fill, wave.Tint, label.Color)
		}
		for _, entity := range []ecs.Entity{paint.root, paint.quads[0], paint.waves[0], paint.labels[0]} {
			if visible, _ := world.Get[scene.Visibility](entity); !visible.Visible {
				t.Fatalf("severity %d decoration is hidden", severity)
			}
		}
		if allocations := testing.AllocsPerRun(100, func() {
			paint.sync(world, model)
			position := world.Mut[scene.UITransform](model.editor.text)
			position.Translation.Y.Value = -5 - position.Translation.Y.Value
			paint.sync(world, model)
		}); allocations != 0 {
			t.Fatalf("severity %d warm diagnostic rendering allocated %g times", severity, allocations)
		}
	}
	diagnostic.Severity = 1
	diagnostic.Range.End = diagnostic.Range.Start
	publish([]lsp.Diagnostic{diagnostic}, doc.version)
	if visible, _ := world.Get[scene.Visibility](paint.root); !visible.Visible {
		t.Fatal("empty diagnostic range was not marked")
	}
	model.settingsOpen = true
	paint.sync(world, model)
	if visible, _ := world.Get[scene.Visibility](paint.root); visible.Visible {
		t.Fatal("diagnostic remains visible outside the text editor")
	}
}

func TestPencodeEditorDiagnosticSeverityPriority(t *testing.T) {
	app, model, provider, _ := languageInteractionFixture(t)
	world := model.fontResources
	file := model.activeFile()
	file.Content = "hint warning information error\n"
	model.setEditorValueAndCaret(world, file.Content, 0)
	model.syncLanguageDocuments()
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	doc := model.languages.documents[file]
	items := []lsp.Diagnostic{
		{Severity: 4, Message: "hint", Range: lsp.Range{End: lsp.Position{Character: 29}}},
		{Severity: 2, Message: "warning", Range: lsp.Range{Start: lsp.Position{Character: 5}, End: lsp.Position{Character: 12}}},
		{Severity: 3, Message: "information", Range: lsp.Range{Start: lsp.Position{Character: 13}, End: lsp.Position{Character: 24}}},
		{Severity: 1, Message: "error", Range: lsp.Range{Start: lsp.Position{Character: 25}, End: lsp.Position{Character: 30}}},
	}
	paint := &model.editor.diagnostics
	for _, severity := range []int{1, 2, 3, 4} {
		model.setProblems(provider, doc.uri, items)
		paint.sync(world, model)
		if len(paint.ranges) != len(items) || len(paint.quads) != 1 || len(paint.labels) != 1 || len(paint.waves) < len(items) {
			t.Fatalf("mixed severity decorations: ranges=%d backgrounds=%d labels=%d waves=%d", len(paint.ranges), len(paint.quads), len(paint.labels), len(paint.waves))
		}
		color := model.workbenchTheme.diagnosticColors[severity]
		fill := color
		fill.A *= .1
		background, _ := world.Get[scene.Sprite2D](paint.quads[0])
		label, _ := world.Get[scene.Text2D](paint.labels[0])
		message := [...]string{"", "error", "warning", "information", "hint"}[severity]
		if background.Shape.Fill != fill || label.Color != color || label.Value != message {
			t.Fatalf("row did not prioritize severity %d: background=%+v label=%+v", severity, background.Shape.Fill, label)
		}
		for i, diagnostic := range paint.ranges {
			wave, _ := world.Get[scene.Sprite2D](paint.waves[i])
			if wave.Tint != model.workbenchTheme.diagnosticColors[diagnostic.severity] {
				t.Fatalf("severity %d lost its underline color", diagnostic.severity)
			}
		}
		last, _ := world.Get[scene.Sprite2D](paint.waves[len(items)-1])
		if last.Tint != color {
			t.Fatal("overlapping underlines did not prioritize the most severe diagnostic")
		}
		for _, entity := range paint.waves[len(items):] {
			if visible, _ := world.Get[scene.Visibility](entity); visible.Visible {
				t.Fatal("removed diagnostic left a visible underline")
			}
		}
		// A palette update must repaint even when the error color is unchanged.
		model.workbenchTheme.diagnosticColors[severity].R *= .5
		paint.sync(world, model)
		label, _ = world.Get[scene.Text2D](paint.labels[0])
		if label.Color != model.workbenchTheme.diagnosticColors[severity] {
			t.Fatalf("severity %d retained a stale theme color", severity)
		}
		items = slices.DeleteFunc(items, func(item lsp.Diagnostic) bool { return item.Severity == severity })
	}
}

type pencodeDiagnosticDrawDriver struct {
	pencodeIconDriver
	instances int
}

func (*pencodeDiagnosticDrawDriver) BeginFrame(render.Color) error    { return nil }
func (*pencodeDiagnosticDrawDriver) SetBlend(render.BlendState) error { return nil }
func (driver *pencodeDiagnosticDrawDriver) DrawSprites(batch render.SpriteBatch) error {
	for _, draw := range batch.Draws {
		for slot := range render.SpriteTextureSlots {
			handle := draw.TextureAt(slot)
			if _, ok := driver.descriptors[handle]; !handle.Valid() || !ok {
				return fmt.Errorf("invalid diagnostic texture handle %s", handle)
			}
		}
	}
	driver.instances += len(batch.Instances)
	return nil
}

func TestPencodeEditorDiagnosticsWrappedRange(t *testing.T) {
	app, model, provider, _ := languageInteractionFixture(t)
	world := model.fontResources
	file := model.activeFile()
	file.Content = strings.Repeat("unknown value ", 50) + "\nnext()\n"
	model.setEditorValueAndCaret(world, file.Content, 0)
	model.syncLanguageDocuments()
	model.settings.EditorWidth = 20
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	doc := model.languages.documents[file]
	// The exclusive endpoint at the next line must not paint that line.
	model.setProblems(provider, doc.uri, []lsp.Diagnostic{{Severity: 1, Message: "first line\nfailed", Range: lsp.Range{End: lsp.Position{Line: 1}}}})
	model.editor.diagnostics.sync(world, model)
	paint := &model.editor.diagnostics
	text := world.Mut[scene.Text2D](model.editor.text)
	if scene.TextLineCount2D(text) <= 3 || len(paint.labels) < 2 {
		t.Fatal("wrapped diagnostic did not decorate multiple visual lines")
	}
	for _, entity := range paint.labels {
		label, _ := world.Get[scene.Text2D](entity)
		if label.Value != "first line failed" {
			t.Fatal("diagnostic message inserted a new line")
		}
	}
	// Each visible wrapped line gets one background and label.
	for line := range scene.TextLineCount2D(text) {
		start, _, rect, _ := scene.TextLineRange2D(text, line)
		if start == strings.Index(file.Content, "\n")+1 {
			for _, entity := range paint.labels {
				position, _ := world.Get[scene.UITransform](entity)
				if position.Translation.Y.Value == paint.key.y+rect.Y {
					t.Fatal("exclusive range endpoint highlighted the next line")
				}
			}
		}
	}
}
