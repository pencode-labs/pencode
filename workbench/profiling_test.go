package workbench

import (
	"testing"

	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/graphics"
	"github.com/onix-engine/onix/render"
	"github.com/pencode-labs/pencode/internal/profiling"
)

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
