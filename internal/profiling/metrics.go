package profiling

import (
	"io"
	"runtime"
	"time"

	"github.com/google/pprof/profile"
)

// Graphics is a partial resource inventory, not total GPU residency. Native
// scratch is CPU memory; Go scratch overlaps the Go heap. TextureCacheBytes is
// the workbench's logical texture size, excluding fonts and driver overhead.
// SampledAt remains unchanged while the UI is idle, making stale data explicit.
type Graphics struct {
	SampledAt          time.Time `json:"sampled_at"`
	Backend            string    `json:"backend"`
	Device             string    `json:"device"`
	InstanceBytes      uint64    `json:"instance_bytes"`
	StagingBytes       uint64    `json:"staging_bytes"`
	ReadbackBytes      uint64    `json:"readback_bytes"`
	NativeScratchBytes uint64    `json:"native_scratch_bytes"`
	GoScratchBytes     uint64    `json:"go_scratch_bytes"`
	TextureCacheBytes  uint64    `json:"texture_cache_bytes"`
	BuffersAvailable   bool      `json:"buffers_available"`
}

type processMemory struct {
	Error          string `json:"error,omitempty"`
	ResidentBytes  uint64 `json:"resident_bytes"`
	FootprintBytes uint64 `json:"footprint_bytes,omitempty"`
	PrivateBytes   uint64 `json:"private_bytes,omitempty"`
	CPUTimeNS      uint64 `json:"cpu_time_ns"`
}

type systemMemory struct {
	Error          string `json:"error,omitempty"`
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
}

// DRMMemory contains process-local DRM buffer residency. Shared buffers can
// overlap other processes; a missing metric is not a measured zero.
type drmMemory struct {
	Error         string `json:"error,omitempty"`
	ResidentBytes uint64 `json:"resident_bytes"`
	Available     bool   `json:"available"`
}

type sample struct {
	Time       time.Time        `json:"time"`
	System     systemMemory     `json:"system"`
	GPU        drmMemory        `json:"gpu_drm"`
	Process    processMemory    `json:"process"`
	Graphics   Graphics         `json:"graphics"`
	Go         runtime.MemStats `json:"go"`
	Goroutines int              `json:"goroutines"`
}

func (r *Recorder) sample() sample {
	s := sample{Time: time.Now(), Goroutines: runtime.NumGoroutine()}
	runtime.ReadMemStats(&s.Go)
	r.mu.Lock()
	s.Graphics = r.graphics
	r.mu.Unlock()
	s.Process, s.System = readMemory()
	s.GPU = readDRMMemory()
	return s
}

// Resource gauges have separate sample types so pprof never adds overlapping
// scopes (e.g. RSS and Go heap) together. They have no allocation call stacks;
// use the standard heap profile to locate Go allocation sites.
func (s sample) writeProfile(w io.Writer) error {
	p := &profile.Profile{
		TimeNanos:         s.Time.UnixNano(),
		DefaultSampleType: "process_resident",
		Comments:          []string{"Resource gauges, not allocation stacks. Select a sample_index; scopes overlap. Missing metrics are unavailable, not zero."},
	}
	values := make([]int64, 0, 20)
	add := func(name string, bytes uint64) {
		p.SampleType = append(p.SampleType, &profile.ValueType{Type: name, Unit: "bytes"})
		values = append(values, int64(bytes))
	}
	add("go_heap_alloc", s.Go.HeapAlloc)
	add("go_heap_inuse", s.Go.HeapInuse)
	add("go_heap_idle", s.Go.HeapIdle)
	add("go_heap_released", s.Go.HeapReleased)
	add("go_stack_inuse", s.Go.StackInuse)
	add("go_sys", s.Go.Sys)
	if s.Process.Error == "" {
		add("process_resident", s.Process.ResidentBytes)
		if s.Process.FootprintBytes != 0 {
			add("process_footprint", s.Process.FootprintBytes)
		}
		if s.Process.PrivateBytes != 0 {
			add("process_private", s.Process.PrivateBytes)
		}
	} else {
		p.DefaultSampleType = "go_heap_alloc"
		p.Comments = append(p.Comments, s.Process.Error)
	}
	if s.System.Error == "" {
		add("system_total", s.System.TotalBytes)
		add("system_available", s.System.AvailableBytes)
	}
	if s.GPU.Available {
		add("gpu_drm_resident", s.GPU.ResidentBytes)
	}
	if !s.Graphics.SampledAt.IsZero() {
		p.Comments = append(p.Comments, "Graphics last sampled: "+s.Graphics.SampledAt.Format(time.RFC3339Nano))
		if s.Graphics.BuffersAvailable {
			add("gpu_instance_buffers", s.Graphics.InstanceBytes)
			add("gpu_staging_buffers", s.Graphics.StagingBytes)
			add("gpu_readback_buffers", s.Graphics.ReadbackBytes)
			add("native_render_scratch", s.Graphics.NativeScratchBytes)
			add("go_render_scratch", s.Graphics.GoScratchBytes)
		}
		add("texture_cache_logical", s.Graphics.TextureCacheBytes)
	}
	p.Sample = []*profile.Sample{{Value: values}}
	return p.Write(w)
}
