package profiling

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	runtimepprof "runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/pencode-labs/pencode/internal/version"
)

func TestRecorderRotationAndShutdown(t *testing.T) {
	r, err := start(t.TempDir(), "test", options{
		sampleInterval: 10 * time.Millisecond, cpuInterval: 400 * time.Millisecond, cpuDuration: 50 * time.Millisecond,
		profileInterval: 250 * time.Millisecond, metricsInterval: 250 * time.Millisecond, profiles: 2, metricsFiles: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	graphics := Graphics{SampledAt: time.Now(), Backend: "test", InstanceBytes: 1234, BuffersAvailable: true}
	r.RecordGraphics(graphics)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(r.Directory(), "heap-000004.pprof")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("recorder did not rotate")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(r.Directory())
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	rows := 0
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".tmp") {
			t.Fatalf("unsealed profile: %s", name)
		}
		path := filepath.Join(r.Directory(), name)
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("permissions for %s = %o", name, info.Mode().Perm())
		}
		kind, _, _ := strings.Cut(name, "-")
		counts[kind]++
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, ".pprof") {
			p, err := profile.ParseData(data)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if err := p.CheckValid(); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			switch kind {
			case "cpu":
				if p.DurationNanos <= 0 || p.SampleType[1].Type != "cpu" {
					t.Fatalf("invalid CPU profile: %s", name)
				}
			case "heap":
				if len(p.SampleType) != 4 || p.SampleType[3].Type != "inuse_space" {
					t.Fatalf("invalid heap profile: %s", name)
				}
			case "resources":
				if name == "resources-final.pprof" && profileValue(t, p, "gpu_instance_buffers") != 1234 {
					t.Fatal("graphics snapshot was lost")
				}
			}
		}
		if strings.HasSuffix(name, ".jsonl") {
			decoder := json.NewDecoder(bytes.NewReader(data))
			for {
				var s sample
				err := decoder.Decode(&s)
				if err == io.EOF {
					break
				}
				if err != nil || s.Time.IsZero() || s.Go.Sys == 0 {
					t.Fatalf("invalid metric: %+v, %v", s, err)
				}
				rows++
			}
		}
	}
	if counts["cpu"] != 2 || counts["heap"] != 4 || counts["resources"] != 4 || counts["metrics"] != 2 || rows == 0 {
		t.Fatalf("retention/metrics: %v, rows=%d", counts, rows)
	}
}

func TestCPUWindowStopsBetweenCaptures(t *testing.T) {
	value := defaults
	value.cpuDuration = 20 * time.Millisecond
	value.cpuInterval = time.Hour
	value.profileInterval = 0
	r, err := start(t.TempDir(), "test", value)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(r.path("cpu", 0, ".pprof")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("CPU window did not stop")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A second profiler can start while the recorder remains open: the sampling
	// goroutine has actually stopped, rather than merely rotating output files.
	if err := runtimepprof.StartCPUProfile(io.Discard); err != nil {
		t.Fatalf("CPU profiler still running between captures: %v", err)
	}
	runtimepprof.StopCPUProfile()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMetricsOnlyRecorder(t *testing.T) {
	value := defaults
	value.cpuDuration, value.profileInterval = 0, 0
	r, err := start(t.TempDir(), "test", value)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(r.Directory())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("metrics-only recording created profile files: %v", files)
	}
	for _, file := range files {
		if file.Name() != "session.json" && file.Name() != "metrics-000000.jsonl" {
			t.Fatalf("unexpected metrics-only file: %s", file.Name())
		}
	}
}

func TestConfiguredIntervals(t *testing.T) {
	t.Setenv("PENCODE_PROFILE_CPU_DURATION", "2s")
	t.Setenv("PENCODE_PROFILE_CPU_INTERVAL", "10s")
	t.Setenv("PENCODE_PROFILE_HEAP_INTERVAL", "20s")
	value, err := configuredOptions()
	if err != nil || value.cpuDuration != 2*time.Second || value.cpuInterval != 10*time.Second || value.profileInterval != 20*time.Second {
		t.Fatalf("configured intervals: %+v, %v", value, err)
	}
	for _, invalid := range []string{"-1s", "bad", "11s"} {
		t.Setenv("PENCODE_PROFILE_CPU_DURATION", invalid)
		if _, err := configuredOptions(); err == nil {
			t.Fatalf("invalid duration accepted: %s", invalid)
		}
	}
	t.Setenv("PENCODE_PROFILE_CPU_DURATION", "0")
	t.Setenv("PENCODE_PROFILE_CPU_INTERVAL", "0")
	t.Setenv("PENCODE_PROFILE_HEAP_INTERVAL", "0")
	if value, err := configuredOptions(); err != nil || value.cpuDuration != 0 || value.profileInterval != 0 {
		t.Fatalf("metrics-only configuration: %+v, %v", value, err)
	}
}

func profileValue(t *testing.T, p *profile.Profile, name string) int64 {
	t.Helper()
	for i, kind := range p.SampleType {
		if kind.Type == name {
			return p.Sample[0].Value[i]
		}
	}
	t.Fatalf("missing profile metric %s", name)
	return 0
}

func TestResourceProfileAvailability(t *testing.T) {
	s := sample{Time: time.Now(), Go: runtime.MemStats{HeapAlloc: 100},
		Process: processMemory{ResidentBytes: 200}, System: systemMemory{TotalBytes: 1000, AvailableBytes: 400}}
	var data bytes.Buffer
	if err := s.writeProfile(&data); err != nil {
		t.Fatal(err)
	}
	p, err := profile.ParseData(data.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if profileValue(t, p, "process_resident") != 200 || profileValue(t, p, "go_heap_alloc") != 100 {
		t.Fatal("resource gauges changed")
	}
	for _, kind := range p.SampleType {
		if strings.HasPrefix(kind.Type, "gpu_") || kind.Type == "texture_cache_logical" {
			t.Fatalf("unavailable metric recorded as zero: %s", kind.Type)
		}
	}
	if len(p.Sample[0].Location) != 0 {
		t.Fatal("resource gauges must not fabricate allocation stacks")
	}
}

func TestStartControls(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PENCODE_PROFILE_DIR", root)
	t.Setenv("PENCODE_DEBUG", "0")
	r, err := Start("test")
	if err != nil || r != nil || r.Close() != nil {
		t.Fatalf("disabled recorder = %v, %v", r, err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("disabled profiling created files")
	}
	t.Setenv("PENCODE_DEBUG", "invalid")
	if _, err := Start("test"); err == nil {
		t.Fatal("invalid debug setting accepted")
	}
	t.Setenv("PENCODE_DEBUG", "1")
	r, err = Start("test")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	// An unset override must respect both the release tag and build version.
	if err := os.Unsetenv("PENCODE_DEBUG"); err != nil {
		t.Fatal(err)
	}
	old := version.Value
	t.Cleanup(func() { version.Value = old })
	for _, value := range []string{"dev", "v1.0.0"} {
		version.Value = value
		r, err := Start("test")
		if err != nil {
			t.Fatal(err)
		}
		if (r != nil) != (development && value == "dev") {
			t.Fatalf("unexpected profiling default for %s", value)
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRecordGraphicsNoAllocations(t *testing.T) {
	var r Recorder
	value := Graphics{SampledAt: time.Now(), InstanceBytes: 1024}
	if n := testing.AllocsPerRun(100, func() { r.RecordGraphics(value) }); n != 0 {
		t.Fatalf("graphics snapshot allocations = %v", n)
	}
}

func TestCPUWriteFailureDoesNotPublishProfile(t *testing.T) {
	r := &Recorder{directory: t.TempDir(), options: defaults}
	if err := r.startCPU(); err != nil {
		t.Fatal(err)
	}
	if err := r.cpu.File.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.finishCPU(); err == nil {
		t.Fatal("CPU write failure was lost")
	}
	files, err := os.ReadDir(r.Directory())
	if err != nil || len(files) != 0 {
		t.Fatalf("failed CPU profile was retained: %v, %v", files, err)
	}
}
