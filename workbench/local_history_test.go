package workbench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	"github.com/pencode-labs/pencode/protocol"
)

func TestLocalHistoryTreeMenuRestore(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	name := "src/main.go"
	filename := filepath.Join(root, filepath.FromSlash(name))
	const original = "package original\n"
	if err := os.WriteFile(filename, []byte(original), 0640); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", root)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", name)
	app, m, _, state := languageInteractionFixture(t)
	p := m.activeProjectState()
	s := &p.sourceControl
	h := &s.history
	editorContextClick(t, app, m, state, "status-source-control")
	sourceWait(t, app, m)
	if h.expanded || editorContextBounds(t, m, "sc-local-history").Width != 36 {
		t.Fatal("local history did not start collapsed")
	}
	// Give the restore target its own save interval.
	if err := p.backend.SaveSettings(t.Context(), []byte(`{"workbench.localHistory.mergeWindow":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := p.backend.SaveFile(t.Context(), root, name, original, original); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second + time.Millisecond)
	m.updateActiveContent("package edited\n")
	if err := saveFileAndWait(t, m, root, m.activeFile()); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{name, "src"} {
		fileContextRightClick(t, app, m, state, m.projectTreeRowID(p, scope))
		editorContextClick(t, app, m, state, "file-context-history")
		if err := awaitFileIO(t, m); err != nil {
			t.Fatal(err)
		}
		m.sourceChanged()
		languageInteractionStep(t, app)
		if !h.expanded || h.scope != scope || h.directory != (scope == "src") || len(h.rows) != 2 {
			t.Fatalf("scope %s: %+v", scope, h)
		}
	}
	row := h.rows[1]
	m.updateActiveContent("unsaved")
	editorContextClick(t, app, m, state, row.id)
	sourceWait(t, app, m)
	if h.selected != row.version {
		t.Fatal("click did not select historical version")
	}
	if tab := s.activeTab(); tab == nil || tab.Local != original || tab.Incoming != "unsaved" || !tab.Preview || tab.editable() || len(tab.Blocks) == 0 {
		t.Fatal("click did not compare the historical version against the unsaved buffer")
	}
	editorContextBounds(t, m, "sc-code-right-input")
	fileContextRightClick(t, app, m, state, row.id)
	editorContextClick(t, app, m, state, "sc-local-restore")
	if !m.activeFile().Dirty || m.activeContent() != "unsaved" || h.err == "" {
		t.Fatal("restore did not protect the unsaved buffer")
	}
	if err := saveFileAndWait(t, m, root, m.activeFile()); err != nil {
		t.Fatal(err)
	}
	fileContextRightClick(t, app, m, state, row.id)
	editorContextClick(t, app, m, state, "sc-local-restore")
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filename)
	if err != nil || string(data) != original || m.activeContent() != original || m.activeFile().Dirty {
		t.Fatalf("restore: disk %q, editor %q, history error %q, error %v", data, m.activeContent(), h.err, err)
	}
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	s.lastClick = ""
	editorContextClick(t, app, m, state, row.id)
	editorContextClick(t, app, m, state, row.id)
	sourceWait(t, app, m)
	if tab := s.activeTab(); tab == nil || tab.Local != original || tab.Incoming != "" || tab.Preview || len(tab.Blocks) == 0 {
		t.Fatal("double click did not pin a refreshed comparison against the deleted file")
	}
	if _, err := os.Stat(filename); !os.IsNotExist(err) {
		t.Fatalf("viewing history recreated the deleted file: %v", err)
	}
	editorContextClick(t, app, m, state, "sc-local-toggle")
	if h.expanded || h.menu {
		t.Fatal("history did not collapse")
	}
}

func TestLocalHistoryExpandedLayout(t *testing.T) {
	app, m := sourceChangesApp(t, 1)
	state, _ := app.World.GetResource[*input.State]()
	h := &m.sourceState().history
	for i := range protocol.FileHistoryPageSize {
		name := fmt.Sprintf("xiuxing-%03d/%s/20261125/1.log", i, strings.Repeat("long-directory/", 12))
		date := "2026-11-24 14:37:15.088"
		if i < 2 {
			date = "2026-11-25 14:37:15.088"
		}
		h.rows = append(h.rows, localHistoryRow{id: fmt.Sprintf("%s%d", localHistoryOpenPrefix, i), date: date, version: protocol.FileVersion{Path: name, Time: uint64(i + 1)}})
	}
	h.expanded = true
	h.revision++
	m.sourceChanged()
	for _, zoom := range []int{70, 100, 150} {
		m.setZoom(m.fontResources, zoom)
		for range 2 {
			languageInteractionStep(t, app)
		}
		panel := editorContextBounds(t, m, "sc-panel")
		history := editorContextBounds(t, m, "sc-local-history")
		toggle := editorContextBounds(t, m, "sc-local-toggle")
		title := editorContextBounds(t, m, "sc-local-title")
		row := editorContextBounds(t, m, h.rows[0].id)
		gap := history.X + history.Width - (toggle.X + toggle.Width)
		if history.Width < 100 || history.Width > 261 || history.X != panel.X || row.Width > history.Width || title.X+title.Width > toggle.X+1 || toggle.Width != 24 || gap < 0 || gap > 7 || row.Height < 34 {
			t.Fatalf("history layout at %d%%: panel=%+v history=%+v toggle=%+v row=%+v", zoom, panel, history, toggle, row)
		}
		// Inspect the rendered labels as well as their enclosing button.
		var nameY, pathY, clockY float32
		for entity, text := range m.fontResources.Query[scene.Text2D]() {
			value := text.Ref().Text().String()
			y := m.fontResources.Ref[scene.UIGlobalTransform](entity).Matrix[13] / (float32(zoom) / 100)
			if y >= row.Y-1 && y < row.Y+row.Height {
				switch {
				case value == "1.log":
					nameY = y
				case value == "14:37:15":
					clockY = y
				case strings.HasPrefix(value, "xiuxing-000"):
					pathY = y
				}
			}
		}
		if nameY == 0 || clockY < nameY-3 || clockY > nameY+3 || pathY-nameY < 13 || pathY+12 > row.Y+row.Height+1 {
			t.Fatalf("overlapping or clipped labels at %d%%: name y=%g, time y=%g, path y=%g, row=%+v", zoom, nameY, clockY, pathY, row)
		}
		edge := history.X + history.Width
		for _, id := range []string{"sc-repositories", "sc-changes", "sc-history"} {
			column := editorContextBounds(t, m, id)
			if column.X < edge-1 || column.X+column.Width > panel.X+panel.Width+1 {
				t.Fatalf("column %s at %d%%: %+v, previous edge %g, panel %+v", id, zoom, column, edge, panel)
			}
			edge = column.X + column.Width
		}
	}
	editorContextClick(t, app, m, state, "sc-local-title")
	if !h.expanded {
		t.Fatal("clicking the title collapsed local history")
	}
	const day = localHistoryDayPrefix + "2026-11-25"
	editorContextClick(t, app, m, state, day)
	header, _ := m.fontResources.HTMLNode(m.surface, day)
	if expanded, _ := header.Attribute("aria-expanded"); expanded != "false" {
		t.Fatal("clicking the date did not collapse its group")
	}
	for _, row := range h.rows[:2] {
		if _, ok := m.fontResources.HTMLNode(m.surface, row.id); ok {
			t.Fatal("collapsed day still contains a file row")
		}
	}
	editorContextBounds(t, m, h.rows[2].id)
	editorContextClick(t, app, m, state, day)
	editorContextBounds(t, m, h.rows[0].id)
	list, _ := m.fontResources.HTMLNode(m.surface, "sc-local-list")
	area, ok := list.ScrollContainer()
	if !ok {
		t.Fatal("populated history has no scroll container")
	}
	scroll := m.fontResources.Mut[scene.UIScroll](area)
	scroll.Position[1] = -3 * scroll.ViewportSize.Y()
	languageInteractionStep(t, app)
	if m.fontResources.Ref[scene.UIScroll](area).Position.Y() >= 0 {
		t.Fatal("history list did not scroll")
	}
	ctx, cancel := context.WithCancel(t.Context())
	job := &fileIOJob{kind: fileHistoryList, project: m.activeProjectState(), cancel: cancel}
	h.loading = job
	editorContextClick(t, app, m, state, "sc-local-toggle")
	if h.expanded || editorContextBounds(t, m, "sc-local-history").Width != 36 {
		t.Fatal("clicking the arrow did not collapse the populated list")
	}
	if h.loading != nil || ctx.Err() == nil {
		t.Fatal("collapsing did not cancel the pending list request")
	}
	m.acceptLocalHistory(&fileIOResult{job: job})
	if len(h.rows) != protocol.FileHistoryPageSize {
		t.Fatal("collapsed history accepted a stale result")
	}
	if list, ok := m.fontResources.HTMLNode(m.surface, "sc-local-list"); ok {
		if bounds, laidOut := list.Bounds(); laidOut {
			t.Errorf("collapsed history still lays out the list: %+v", bounds)
		}
	}
	if _, ok := m.fontResources.HTMLControl(m.surface, h.rows[0].id); ok {
		t.Error("collapsed history retains an actionable file row")
	}
	for entity, text := range m.fontResources.Query[scene.Text2D]() {
		value := text.Ref().Text().String()
		if value == "1.log" || value == "14:37:15" || strings.HasPrefix(value, "xiuxing-") || strings.HasPrefix(value, "2026-11-") {
			if visible := m.fontResources.Ref[scene.Visibility](entity); visible != nil && visible.Visible {
				t.Fatalf("collapsed history still paints %q", value)
			}
		}
	}
	editorContextClick(t, app, m, state, "sc-local-toggle")
	languageInteractionStep(t, app)
	if !h.expanded || editorContextBounds(t, m, "sc-local-history").Width <= 36 {
		t.Fatal("clicking the arrow did not reopen the history column")
	}
}
