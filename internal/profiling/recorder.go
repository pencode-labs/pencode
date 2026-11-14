// Package profiling records bounded, continuous development diagnostics without
// exposing a network endpoint or forcing garbage collection.
package profiling

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"sync"
	"time"

	"github.com/pencode-labs/pencode/internal/version"
)

const SampleInterval = 5 * time.Second

type options struct {
	sampleInterval  time.Duration
	profileInterval time.Duration
	metricsInterval time.Duration
	profiles        int
	metricsFiles    int
}

var defaults = options{SampleInterval, time.Minute, time.Hour, 60, 24}

type Recorder struct {
	err       error
	encoder   *json.Encoder
	done      chan struct{}
	stop      chan struct{}
	cpu       *cpuFile
	metrics   *os.File
	directory string
	graphics  Graphics
	options   options
	profile   int
	metricsID int
	once      sync.Once
	mu        sync.Mutex
}

// StopCPUProfile has no error result. Retain asynchronous write failures so a
// full disk cannot turn a truncated CPU stream into a published .pprof file.
type cpuFile struct {
	err error
	*os.File
}

func (f *cpuFile) Write(data []byte) (int, error) {
	n, err := f.File.Write(data)
	if f.err == nil {
		f.err = err
	}
	return n, err
}

// Start defaults to enabled for dev versions outside onix_release builds.
// PENCODE_DEBUG overrides that default; PENCODE_PROFILE_DIR selects the parent
// directory. Each process owns a unique subdirectory, including local servers.
func Start(role string) (*Recorder, error) {
	enabled := development && version.Value == "dev"
	if value, ok := os.LookupEnv("PENCODE_DEBUG"); ok {
		var err error
		enabled, err = strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("PENCODE_DEBUG: %w", err)
		}
	}
	if !enabled {
		return nil, nil
	}
	directory := os.Getenv("PENCODE_PROFILE_DIR")
	if directory == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		directory = filepath.Join(home, ".pencode", "profiles")
	}
	return start(directory, role, defaults)
}

func start(directory, role string, options options) (*Recorder, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(directory, fmt.Sprintf("%s-%s-%d-", time.Now().UTC().Format("20060102T150405Z"), role, os.Getpid()))
	if err != nil {
		return nil, err
	}
	r := &Recorder{directory: directory, options: options, stop: make(chan struct{}), done: make(chan struct{})}
	metadata := struct {
		Started time.Time `json:"started"`
		Role    string    `json:"role"`
		Version string    `json:"version"`
		OS      string    `json:"os"`
		Arch    string    `json:"arch"`
		PID     int       `json:"pid"`
	}{Started: time.Now(), Role: role, Version: version.Value, OS: runtime.GOOS, Arch: runtime.GOARCH, PID: os.Getpid()}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(directory, "session.json"), data, 0o600)
	}
	if err == nil {
		err = r.openMetrics()
	}
	if err == nil {
		err = r.startCPU()
	}
	if err != nil {
		if r.metrics != nil {
			_ = r.metrics.Close()
		}
		_ = os.RemoveAll(directory)
		return nil, err
	}
	go r.run()
	fmt.Fprintf(os.Stderr, "Pencode profiling (%s): %s\n", role, directory)
	return r, nil
}

func (r *Recorder) Directory() string { return r.directory }

// Close flushes the current CPU segment and final snapshots. It is safe to call
// repeatedly, or on a nil recorder when profiling is disabled.
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.once.Do(func() { close(r.stop) })
	<-r.done
	return r.err
}

// RecordGraphics receives a value snapshot from the rendering thread. No file
// I/O, profiling, or allocation occurs on that thread.
func (r *Recorder) RecordGraphics(value Graphics) {
	r.mu.Lock()
	r.graphics = value
	r.mu.Unlock()
}

func (r *Recorder) run() {
	defer close(r.done)
	defer func() {
		r.err = errors.Join(r.err, r.finishCPU(), r.snapshot("final"), r.metrics.Close())
		if r.err != nil {
			fmt.Fprintln(os.Stderr, "Pencode profiling:", r.err)
		}
	}()
	if r.err = r.snapshot("baseline"); r.err != nil {
		return
	}
	samples := time.NewTicker(r.options.sampleInterval)
	profiles := time.NewTicker(r.options.profileInterval)
	metrics := time.NewTicker(r.options.metricsInterval)
	defer samples.Stop()
	defer profiles.Stop()
	defer metrics.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-samples.C:
			r.err = r.encoder.Encode(r.sample())
		case <-profiles.C:
			r.err = r.rotate()
		case <-metrics.C:
			r.err = r.metrics.Close()
			if r.err == nil {
				r.metricsID++
				r.err = r.openMetrics()
			}
		}
		if r.err != nil {
			return
		}
	}
}

func (r *Recorder) openMetrics() error {
	file, err := os.OpenFile(r.path("metrics", r.metricsID, ".jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	r.metrics, r.encoder = file, json.NewEncoder(file)
	return r.removeOld("metrics", r.metricsID, r.options.metricsFiles, ".jsonl")
}

func (r *Recorder) startCPU() error {
	file, err := os.OpenFile(r.path("cpu", r.profile, ".pprof.tmp"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	cpu := &cpuFile{File: file}
	if err := pprof.StartCPUProfile(cpu); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return err
	}
	r.cpu = cpu
	return nil
}

func (r *Recorder) finishCPU() error {
	if r.cpu == nil {
		return nil
	}
	pprof.StopCPUProfile()
	file := r.cpu
	r.cpu = nil
	if err := errors.Join(file.err, file.Close()); err != nil {
		_ = os.Remove(file.Name())
		return err
	}
	if err := os.Rename(file.Name(), r.path("cpu", r.profile, ".pprof")); err != nil {
		return err
	}
	return r.removeOld("cpu", r.profile, r.options.profiles, ".pprof")
}

func (r *Recorder) rotate() error {
	if err := r.finishCPU(); err != nil {
		return err
	}
	r.profile++
	if err := r.startCPU(); err != nil {
		return err
	}
	name := fmt.Sprintf("%06d", r.profile)
	if err := r.snapshot(name); err != nil {
		return err
	}
	for _, kind := range [...]string{"heap", "resources"} {
		if err := r.removeOld(kind, r.profile, r.options.profiles, ".pprof"); err != nil {
			return err
		}
	}
	return nil
}

func (r *Recorder) snapshot(name string) error {
	sample := r.sample()
	if err := r.encoder.Encode(sample); err != nil {
		return err
	}
	if err := r.writeProfile("heap-"+name, pprof.WriteHeapProfile); err != nil {
		return err
	}
	return r.writeProfile("resources-"+name, sample.writeProfile)
}

func (r *Recorder) writeProfile(name string, write func(io.Writer) error) error {
	path := filepath.Join(r.directory, name+".pprof")
	file, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = errors.Join(write(file), file.Close())
	if err != nil {
		_ = os.Remove(file.Name())
		return err
	}
	return os.Rename(file.Name(), path)
}

func (r *Recorder) path(kind string, index int, extension string) string {
	return filepath.Join(r.directory, fmt.Sprintf("%s-%06d%s", kind, index, extension))
}

func (r *Recorder) removeOld(kind string, index, keep int, extension string) error {
	if index < keep {
		return nil
	}
	err := os.Remove(r.path(kind, index-keep, extension))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
