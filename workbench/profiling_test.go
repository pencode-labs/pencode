package workbench

import (
	"testing"

	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/graphics"
	"github.com/onix-engine/onix/render"
	"github.com/pencode-labs/pencode/internal/profiling"
)

func TestProfilingSettingsControlAndPersistence(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	for _, screen := range []pencodeScreen{pencodeWorkspace, pencodeLauncher} {
		m.screen = screen
		if err := app.Modules().UpdateFrame(1.0 / 60); err != nil {
			t.Fatal(err)
		}
		if err := app.Modules().Dispatch(); err != nil {
			t.Fatal(err)
		}
		m = m.session.active.model
		m.settingsOpen = true
		m.settings.Pprof = new(false)
		markPencodeRootDirtyByModel(m.fontResources, m)
		languageInteractionStep(t, app)
		editorContextClick(t, app, m, state, "settings-about-nav")
		editorContextBounds(t, m, "pprof-enabled")
		if _, ok := m.fontResources.HTMLControl(m.surface, "pprof-enabled"); !ok {
			t.Fatal("missing pprof control")
		}
		for _, checked := range []bool{true, false} {
			editorContextClick(t, app, m, state, "pprof-enabled")
			saved, _ := loadPencodeSettings()
			if saved.Pprof == nil || *saved.Pprof != checked || m.settings.Pprof == nil || *m.settings.Pprof != checked {
				t.Fatalf("pprof setting not saved: %v", checked)
			}
			node, ok := m.fontResources.HTMLNode(m.surface, "pprof-enabled")
			if !ok {
				t.Fatal("missing rendered pprof control")
			}
			if _, got := node.Attribute("checked"); got != checked {
				t.Fatalf("pprof checkbox = %t, want %t", got, checked)
			}
		}
	}
}

type pencodeProfileDriver struct {
	render.Driver
	samples int
}

func (d *pencodeProfileDriver) BufferMemoryStatistics() render.BufferMemoryStatistics {
	d.samples++
	return render.BufferMemoryStatistics{InstanceBytes: 1024}
}

func (*pencodeProfileDriver) RendererInfo() render.RendererInfo {
	return render.RendererInfo{Backend: "test"}
}

func (*pencodeProfileDriver) Destroy() {}

func TestProfilingSystemSamplesOnApplicationThread(t *testing.T) {
	world := ecs.NewWorld()
	driver := &pencodeProfileDriver{}
	device, err := render.NewDevice(driver)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Destroy()
	graphics.BindFrame(world, graphics.NewCanvas2D(device, 100, 100))
	var s Session
	var recorder profiling.Recorder
	system := s.ProfilingSystem(&recorder)
	context := &ecs.SystemContext{World: world}
	if err := system.System(context); err != nil || driver.samples != 1 {
		t.Fatalf("initial graphics sample: %d, %v", driver.samples, err)
	}
	if n := testing.AllocsPerRun(100, func() {
		if err := system.System(context); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("warm profiling system allocations = %v", n)
	}
	if driver.samples != 1 {
		t.Fatal("graphics sampling was not throttled")
	}
}
