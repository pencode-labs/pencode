package workbench

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/diiyw/onix"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/project"
	"github.com/diiyw/onix/resource"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/ui"
)

// ONIX_PENCODE_PROFILE_PROJECT optionally selects a real project for profiling.
// The default fixture keeps the benchmark reproducible without a native window.
func newPencodeTestApp(b testing.TB) (*onix.App, *Session) {
	b.Helper()
	b.Setenv("XDG_CONFIG_HOME", b.TempDir())
	b.Setenv("APPDATA", b.TempDir())
	var source fs.FS = os.DirFS("..")
	var plan *project.ProjectPlan
	var err error
	// Packaged resources let remote tests avoid reading encrypted source files.
	if packages := os.Getenv("ONIX_PERF_PACKAGES"); packages != "" {
		manager, openErr := resource.OpenManager(packages)
		if openErr != nil {
			b.Fatal(openErr)
		}
		b.Cleanup(func() { manager.Close() })
		source = manager
		plan, err = project.ReadModulePlan(source)
	} else {
		plan, err = project.CompileProject(source, ".")
	}
	if err != nil {
		b.Fatal(err)
	}
	session := NewSession(newPencodeBackend(b))
	session.quickHistory = loadPencodeQuickHistory(b.TempDir())
	registry := module.Registry{}
	if err := session.Register(&registry); err != nil {
		b.Fatal(err)
	}
	for _, id := range [...]project.SceneID{"main.json", "workspace.json"} {
		err := module.RegisterScene(&registry, id, func() struct{} { return struct{}{} }, module.Lifecycle[struct{}]{
			OnMessage: func(c *module.Context, _ *struct{}, message module.Message) error {
				if message.Topic == "pencode.navigate" {
					workspace, _ := module.Value[bool](message)
					target := project.SceneID("main.json")
					if workspace {
						target = "workspace.json"
					}
					return c.Switch(module.SceneSwitchRequest{Scene: target, Retain: true})
				}
				return nil
			},
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	app := onix.NewApp()
	b.Cleanup(app.Destroy)
	if err := onix.InstallSystems[onix.UIHTML](app); err != nil {
		b.Fatal(err)
	}
	if err := app.AddSystems(onix.StagePostUpdate, session.ScrollSystem(), session.LayoutSystem()); err != nil {
		b.Fatal(err)
	}
	if _, err := app.LoadModules(plan, registry, module.Options{Source: source}); err != nil {
		b.Fatal(err)
	}
	dir := os.Getenv("ONIX_PENCODE_PROFILE_PROJECT")
	if dir == "" {
		dir = b.TempDir()
		body := "package demo\n" + strings.Repeat("// text to keep the editor layout representative\n", 200)
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(body), 0644); err != nil {
			b.Fatal(err)
		}
	}
	if err := loadProjectAndWait(b, session.active.model, dir); err != nil {
		b.Fatal(err)
	}
	for range 3 {
		if err := app.Modules().UpdateFrame(1.0 / 60); err != nil {
			b.Fatal(err)
		}
		if err := app.Modules().Dispatch(); err != nil {
			b.Fatal(err)
		}
	}
	waitPencodeProjectScan(b, session.active.model.activeProjectState(), func() {
		if err := app.Modules().UpdateFrame(1.0 / 60); err != nil {
			b.Fatal(err)
		}
		if err := app.Modules().Dispatch(); err != nil {
			b.Fatal(err)
		}
	})
	file := os.Getenv("ONIX_PENCODE_PROFILE_FILE")
	if file == "" {
		file = "main.go"
	}
	openFileAndWait(b, session.active.model, file)
	markPencodeRootDirtyByModel(session.active.model.fontResources, session.active.model)
	app.SetTargetFPS(0)
	for range 3 {
		if _, err := app.Step(); err != nil {
			b.Fatal(err)
		}
	}
	settlePencodeHighlight(b, session.active.model)
	app.RequestRender()
	if _, err := app.Step(); err != nil {
		b.Fatal(err)
	}
	b.Logf("project=%s file=%s bytes=%d", dir, session.active.model.activePath, len(session.active.model.activeContent()))
	return app, session
}

func BenchmarkPencodeOpenFileFrame(b *testing.B) {
	app, _ := newPencodeTestApp(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := app.Step(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPencodeOpenFileIdle(b *testing.B) {
	app, _ := newPencodeTestApp(b)
	app.SetRenderMode(onix.RenderOnDemand)
	app.SetIdleEnabled(true)
	if _, err := app.Step(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := app.Step(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPencodeCachedFileSwitch(b *testing.B) {
	dir := b.TempDir()
	for _, name := range []string{"main.go", "other.go"} {
		body := "package demo\n" + strings.Repeat("// cached file switching\n", 100) + "var Value = 42\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			b.Fatal(err)
		}
	}
	b.Setenv("ONIX_PENCODE_PROFILE_PROJECT", dir)
	b.Setenv("ONIX_PENCODE_PROFILE_FILE", "main.go")
	app, session := newPencodeTestApp(b)
	model := session.active.model
	switchFile := func(name string) {
		openFileAndWait(b, model, name)
		markPencodeRootDirtyByModel(model.fontResources, model)
		app.RequestRender()
		if _, err := app.Step(); err != nil {
			b.Fatal(err)
		}
	}
	for _, name := range []string{"other.go", "main.go"} {
		switchFile(name)
		settlePencodeHighlight(b, model)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		switchFile("other.go")
		switchFile("main.go")
	}
}

func TestPencodeOpenFileIdleAndScroll(t *testing.T) {
	app, session := newPencodeTestApp(t)
	app.SetRenderMode(onix.RenderOnDemand)
	app.SetIdleEnabled(true)
	step := func() bool {
		t.Helper()
		rendered, err := app.Step()
		if err != nil {
			t.Fatal(err)
		}
		return rendered
	}
	step()
	if app.Modules().NeedsTick() {
		t.Fatal("idle workbench still requires continuous module ticks")
	}
	for range 10 {
		if step() {
			t.Fatal("unchanged open file rendered again")
		}
	}
	model := session.active.model
	editor := &model.editor
	textarea := module.ContentMut[ui.Textarea](model.fontResources, editor.control)
	textarea.ScrollY = 120
	app.RequestRender()
	if !step() {
		t.Fatal("scroll did not wake the app")
	}
	text, _ := module.ContentGet[scene.Text2D](model.fontResources, editor.text)
	transform, _ := module.ContentGet[scene.UITransform](model.fontResources, editor.text)
	numbers, _ := module.ContentGet[scene.UITransform](model.fontResources, editor.numbers)
	if textarea.ScrollY == 0 || editor.firstLine == 0 || numbers.Translation.Y.Value != transform.Translation.Y.Value+float32(editor.firstLine)*text.LineHeight {
		t.Fatal("line numbers lagged behind textarea scroll")
	}
	if step() {
		t.Fatal("scroll prevented the app returning to idle")
	}
	if allocs := testing.AllocsPerRun(100, func() { step() }); allocs != 0 {
		t.Fatalf("idle allocated %g times", allocs)
	}
	// Exercise the real one-shot app wake without waiting for wall-clock idle.
	spans := slices.Clone(model.highlightScratch)
	model.syntaxMemoryDue = time.Now().Add(-time.Millisecond)
	app.WakeAt(model.syntaxMemoryDue)
	step()
	if !model.syntaxMemoryDue.IsZero() || step() {
		t.Fatal("syntax reclamation failed to complete or kept the app rendering")
	}
	if model.highlightRequest.file != nil || !slices.Equal(spans, model.highlightScratch) {
		t.Fatal("idle reclamation retained the parser or changed visible highlighting")
	}
}

func TestPencodeSyntaxMemoryReleaseDeadline(t *testing.T) {
	var wake time.Time
	model := workspaceModel{app: &module.Host{WakeAt: func(deadline time.Time) { wake = deadline }}}
	model.scheduleSyntaxMemoryRelease()
	first := model.syntaxMemoryDue
	if first.IsZero() || wake != first {
		t.Fatal("syntax reclamation did not schedule an idle wake")
	}
	model.syntaxMemoryDue = first.Add(time.Second)
	model.releaseIdleSyntaxMemory(first)
	if wake != model.syntaxMemoryDue {
		t.Fatal("earlier wake lost the postponed reclamation deadline")
	}
	model.releaseIdleSyntaxMemory(model.syntaxMemoryDue)
	if !model.syntaxMemoryDue.IsZero() {
		t.Fatal("completed reclamation kept an active deadline")
	}
	wake = time.Time{}
	if allocs := testing.AllocsPerRun(100, func() { model.releaseIdleSyntaxMemory(first) }); allocs != 0 || !wake.IsZero() {
		t.Fatal("idle reclamation continued scheduling or allocating after completion")
	}
}
