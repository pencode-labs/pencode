package workbench

import (
	"time"

	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/graphics"
	"github.com/onix-engine/onix/render"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/internal/profiling"
)

func (model *workspaceModel) renderProfilingSettings(view *htmlui.View) {
	enabled := profiling.DefaultEnabled()
	if model.settings.Pprof != nil {
		enabled = *model.settings.Pprof
	}
	view.Event("pprof-enabled", htmlui.EventChange)
	if enabled {
		view.Attr("pprof-enabled", "checked", "")
		view.Attr("pprof-thumb", "class", "pencode-setting-switch-thumb active")
	} else {
		view.RemoveAttr("pprof-enabled", "checked")
		view.Attr("pprof-thumb", "class", "pencode-setting-switch-thumb")
	}
}

// ProfilingSystem samples renderer-owned state on the application thread. It
// never requests a frame: idle observations retain their original timestamp.
func (s *Session) ProfilingSystem(recorder *profiling.Recorder) ecs.SystemConfig {
	var next time.Time
	var targets []profiling.RenderTargetSize
	collectTarget := func(kind string, id, width, height uint32) {
		targets = append(targets, profiling.RenderTargetSize{Kind: kind, ID: id, Width: width, Height: height})
	}
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
		if profiling.DefaultEnabled() {
			if metal, ok := device.Driver().(interface {
				VisitRenderTargetSizes(func(string, uint32, uint32, uint32))
			}); ok {
				targets = targets[:0]
				metal.VisitRenderTargetSizes(collectTarget)
				value.RenderTargets = targets
			}
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
