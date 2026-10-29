package workbench

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/diiyw/onix/module"
	"github.com/pencode-labs/pencode/configuration"
	"github.com/pencode-labs/pencode/extensions"
)

func TestExtensionIconsCacheOnlyUsedThemeVariants(t *testing.T) {
	m := &workspaceModel{settings: defaultPencodeSettings()}
	if err := m.loadSyntaxResources(configuration.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	icons := make(map[string][]byte, 1000)
	for i := range 1000 {
		icons[fmt.Sprintf("peb://lazy/resources/%04d.svg", i)] = []byte("<svg/>")
	}
	if err := m.importExtension(&extensions.Bundle{ID: "lazy", Icons: icons}); err != nil {
		t.Fatal(err)
	}
	for _, theme := range m.themes {
		for key := range theme.icons {
			if strings.HasPrefix(key.source, "peb://lazy/") {
				t.Fatal("extension import expanded an unused icon", theme.id, key)
			}
		}
	}
	const source = "peb://lazy/resources/0000.svg"
	const foreground = "list.activeSelectionForeground"
	warm := func() string {
		return m.themedIcon(source, foreground)
	}
	first := warm()
	want := source + strings.TrimPrefix(m.themedIcon("icons/file-text.svg", foreground), "icons/file-text.svg")
	if first != want || first == source {
		t.Fatal("lazy icon lost theme color", first, want)
	}
	if n := testing.AllocsPerRun(100, func() { warm() }); n != 0 {
		t.Fatal("warm icon lookup allocated", n)
	}
	if !m.selectTheme("light-plus") || warm() != source+strings.TrimPrefix(m.themedIcon("icons/file-text.svg", foreground), "icons/file-text.svg") {
		t.Fatal("theme switch lost lazy icon color")
	}
	if m.themedIcon(source, "unknown.foreground") != source || m.themedIcon("peb://lazy/unknown.svg", foreground) != "peb://lazy/unknown.svg" {
		t.Fatal("unknown icon or foreground was synthesized")
	}
	m.unloadExtension("lazy")
	for _, theme := range m.themes {
		for key := range theme.icons {
			if strings.HasPrefix(key.source, "peb://lazy/") {
				t.Fatal("unload retained an icon variant")
			}
		}
	}
	if warm() != source {
		t.Fatal("unload retained a themed icon")
	}
	if err := m.importExtension(&extensions.Bundle{ID: "lazy", Icons: icons}); err != nil || warm() == source {
		t.Fatal("reimport did not restore lazy icons", err)
	}
}

// Run each mode in a separate process to exclude previously warmed global caches.
func TestPencodeLauncherMemoryProfile(t *testing.T) {
	mode := os.Getenv("PENCODE_LAUNCHER_MEMORY")
	if mode == "" {
		t.Skip("manual memory profile")
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	app, session := newPencodeTestLauncher(t)
	if mode == "preload" {
		if _, err := app.Modules().Manager().Preload(module.ScenePreloadRequest{Scene: "workspace.json"}); err != nil {
			t.Fatal(err)
		}
	}
	duration := time.Since(started)
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("mode=%s views=%d startup=%s retained=%.3f MiB", mode, len(session.views), duration, float64(int64(after.HeapAlloc)-int64(before.HeapAlloc))/(1<<20))
}

func TestPencodeExtensionIconMemoryProfile(t *testing.T) {
	if os.Getenv("PENCODE_ICON_MEMORY") == "" {
		t.Skip("manual memory profile")
	}
	m := &workspaceModel{settings: defaultPencodeSettings()}
	if err := m.loadSyntaxResources(configuration.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	icons := make(map[string][]byte, 1000)
	for i := range 1000 {
		icons[fmt.Sprintf("peb://memory/resources/%04d.svg", i)] = []byte("<svg/>")
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := m.importExtension(&extensions.Bundle{ID: "memory", Icons: icons}); err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		m.themedIcon(fmt.Sprintf("peb://memory/resources/%04d.svg", i), "icon.foreground")
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	variants := 0
	for _, theme := range m.themes {
		for key := range theme.icons {
			if strings.HasPrefix(key.source, "peb://memory/") {
				variants++
			}
		}
	}
	t.Logf("icons=%d variants=%d retained=%.3f MiB", len(icons), variants, float64(int64(after.HeapAlloc)-int64(before.HeapAlloc))/(1<<20))
}
