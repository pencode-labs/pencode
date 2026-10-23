package workbench

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// A wider tree exposes per-row allocations hidden by the two-file fixture.
func BenchmarkPencodeTreeFileSwitch(b *testing.B) {
	dir := b.TempDir()
	for index := range 100 {
		name := fmt.Sprintf("file%03d.go", index)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package demo\nvar Value = 42\n"), 0644); err != nil {
			b.Fatal(err)
		}
	}
	b.Setenv("ONIX_PENCODE_PROFILE_PROJECT", dir)
	b.Setenv("ONIX_PENCODE_PROFILE_FILE", "file000.go")
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
	for range 3 {
		switchFile("file001.go")
		switchFile("file000.go")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		switchFile("file001.go")
		switchFile("file000.go")
	}
}
