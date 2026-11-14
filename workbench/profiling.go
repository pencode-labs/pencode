package workbench

import (
	"time"

	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/graphics"
	"github.com/onix-engine/onix/render"
	"github.com/pencode-labs/pencode/internal/profiling"
)

// ProfilingSystem samples renderer-owned state on the application thread. It
// never requests a frame: idle observations retain their original timestamp.
func (s *Session) ProfilingSystem(recorder *profiling.Recorder) ecs.SystemConfig {
	var next time.Time
	return ecs.SystemConfig{Name: "pencode_profile_graphics", After: []string{"render_cameras"}, System: func(context *ecs.SystemContext) error {
		now := time.Now()
		if now.Before(next) {
			return nil
		}
		next = now.Add(profiling.SampleInterval)
		canvas := graphics.CanvasFor(context.World)
		if canvas == nil {
			return nil
		}
		device := canvas.Device()
		if device == nil || device.Driver() == nil {
			return nil
		}
		// Include buffers allocated by the current frame, including its final
		// queued batch, before the app can become idle after its first render.
		canvas.DrawFrame()
		buffers := device.BufferMemoryStatistics()
		info := device.RendererInfo()
		_, available := device.Driver().(interface {
			BufferMemoryStatistics() render.BufferMemoryStatistics
		})
		value := profiling.Graphics{
			SampledAt: now, Backend: info.Backend, Device: info.Device, BuffersAvailable: available,
			InstanceBytes: buffers.InstanceBytes, StagingBytes: buffers.StagingBytes, ReadbackBytes: buffers.ReadbackBytes,
			NativeScratchBytes: buffers.NativeScratchBytes, GoScratchBytes: buffers.ScratchBytes,
		}
		for view := range s.views {
			if view.icons != nil {
				value.TextureCacheBytes += uint64(view.icons.bytes)
			}
		}
		recorder.RecordGraphics(value)
		return nil
	}}
}
