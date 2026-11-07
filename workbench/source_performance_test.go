package workbench

import (
	"fmt"
	"os"
	"testing"

	"github.com/onix-engine/onix/scene"
)

func BenchmarkPencodeSourceChangesScroll(b *testing.B) {
	for _, count := range []int{400, 4000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			app, m := sourceChangesApp(b, count)
			node, _ := m.fontResources.HTMLNode(m.surface, "sc-changes-list")
			area, _ := node.ScrollContainer()
			b.Logf("files=%d panel nodes=%d", count, m.sourceState().panelView.used)
			b.ReportAllocs()
			position := float32(0)
			for b.Loop() {
				scroll := m.fontResources.Mut[scene.UIScroll](area)
				position += 48
				if position > scroll.ContentSize.Y()-scroll.ViewportSize.Y() {
					position = 0
				}
				scroll.Position[1] = -position
				if _, err := app.Step(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Uses the same headless fixture as the editor benchmarks. Point
// ONIX_PENCODE_PROFILE_PROJECT at a real repository to measure its working tree.
func BenchmarkPencodeSourceControl(b *testing.B) {
	if os.Getenv("ONIX_PENCODE_PROFILE_PROJECT") == "" {
		r := sourceTestRepository(b)
		sourceTestWrite(b, r, "main.go", "package demo\n")
		sourceTestGit(b, r, "add", "main.go")
		sourceTestGit(b, r, "commit", "-m", "base")
		for range 39 {
			sourceTestGit(b, r, "commit", "--allow-empty", "-m", "history")
		}
		b.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
	}
	app, session := newPencodeTestApp(b)
	m := session.active.model
	r, err := discoverSourceRepository(m.backend, m.projectPath)
	if err != nil {
		b.Fatal(err)
	}
	snapshot, err := r.snapshot(sourceHistoryPageSize)
	if err != nil {
		b.Fatal(err)
	}
	s := m.sourceState()
	s.Snapshot, s.RefreshStarted = snapshot, true
	step := func() {
		b.Helper()
		if _, err := app.Step(); err != nil {
			b.Fatal(err)
		}
	}
	m.bottom.active = pencodePanelSourceControl
	m.sourceChanged()
	for range 3 {
		step()
	}
	b.Logf("changed files=%d commits=%d visible=%d nodes=%d", len(snapshot.Files), len(snapshot.Commits), s.GraphVisible, s.panelView.used)
	b.Run("Idle", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			step()
		}
	})
	b.Run("Redraw", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			m.sourceChanged()
			step()
		}
	})
	b.Run("Toggle", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			m.bottom.active = pencodePanelClosed
			m.sourceChanged()
			step()
			m.bottom.active = pencodePanelSourceControl
			m.sourceChanged()
			step()
		}
	})
	b.Run("OutputToggle", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			m.bottom.active = pencodePanelClosed
			m.sourceChanged()
			step()
			m.bottom.active = pencodePanelOutput
			m.sourceChanged()
			step()
		}
	})
}
