package workbench

import (
	"os"
	"runtime"
	"runtime/pprof"
	"testing"
)

// Set ONIX_PENCODE_PROFILE_PROJECT/FILE and ONIX_DIFF_PROFILE to reproduce a
// working-tree comparison and write a combined GUI/server heap profile. The
// backend uses the production protocol in-process, without a native window.
func TestPencodeDiffMemoryProfile(t *testing.T) {
	if os.Getenv("ONIX_DIFF_PROFILE") == "" {
		t.Skip("manual memory profile")
	}
	logMemory := func(label string) {
		runtime.GC()
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		t.Logf("%s heap=%.2f MB total=%.2f MB sys=%.2f MB", label, float64(stats.HeapAlloc)/(1<<20), float64(stats.TotalAlloc)/(1<<20), float64(stats.Sys)/(1<<20))
	}
	app, session := newPencodeTestApp(t)
	m := session.active.model
	logMemory("file")
	m.sourceAction("sc-toggle")
	sourceWait(t, app, m)
	m.sourceAction("sc-open:changes:" + m.activePath)
	sourceWait(t, app, m)
	logMemory("diff before highlight")
	settleSourceHighlight(t, m)
	m.sourceChanged()
	for range 3 {
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
	}
	logMemory("diff after highlight")
	f, err := os.Create(os.Getenv("ONIX_DIFF_PROFILE"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pprof.WriteHeapProfile(f); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
	}
}
