package workbench

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	"github.com/pencode-labs/pencode/lsp"
)

func TestLanguageHistoryDiffTargetsDisplayedSide(t *testing.T) {
	app, m, normal, state := languageInteractionFixture(t)
	w, s := m.fontResources, m.sourceState()
	file := m.activeFile()
	original := file.snapshot()
	prefix := strings.Repeat("// context\n", 48)
	tab := &sourceTab{Key: "history", Kind: "history", Revision: "1234567890abcdef", Path: file.Path, Local: prefix + "var OldName = 1\n", Incoming: prefix + "// new line\nvar NewName = 2\n"}
	view, err := sourceDiffContext(t.Context(), m.backend, tab.Local, tab.Incoming)
	if err != nil {
		t.Fatal(err)
	}
	tab.initializeDiffView(view)
	s.RefreshStarted, s.RefreshPending = true, false
	s.Snapshot.Repository.Root = m.projectPath
	s.Tabs, s.Active, s.RestoreScroll = []*sourceTab{tab}, tab.Key, true
	m.sourceChanged()
	for range 4 {
		languageInteractionStep(t, app)
	}
	if tab.ScrollY >= 0 {
		t.Fatal("expected scrolled comparison")
	}
	uri := lsp.FileURI(filepath.Join(m.projectPath, tab.Path))
	for side, name := range []string{"OldName", "NewName"} {
		editor := &s.diffEditors[side]
		f := tab.languageFile(side)
		provider := m.languages.sessions[pencodeLanguageKey{m.projectPath, normal.server.ID, side + 1}]
		doc := m.languages.documents[f]
		if doc == nil || doc.uri != uri || doc.content != f.snapshot() || doc.writable() || m.languageProvider(doc, "textDocument/completion") != nil {
			t.Fatalf("wrong snapshot for side %d", side)
		}
		// Leave keyboard focus in the other pane when moving the pointer.
		w.Focus(s.diffEditors[1-side].control)
		rect := editorContextBounds(t, m, diffInputs[side])
		text, ox, oy := editor.languageEditorGeometry(w, rect)
		start := strings.Index(f.content(), name)
		x, y, _, h, ok := scene.TextCaretRect2D(text, f.snapshot(), f.snapshot().RuneOffset(start+2))
		if !ok {
			t.Fatal("missing comparison text layout")
		}
		scale := float64(m.settings.Zoom) / 100
		px, py := float64(ox+x+tab.ScrollX+1)*scale, float64(oy+y+tab.ScrollY+h/2)*scale
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: px, Y: py})
		languageInteractionStep(t, app)
		hover := &m.languageUI.hover
		if hover.file != f || hover.start != start {
			t.Fatalf("side %d hover matched wrong file or offset at %.1f,%.1f. got %d, want %d", side, px, py, hover.start, start)
		}
		hover.due = time.Now().Add(-time.Millisecond)
		languageInteractionStep(t, app)
		job := languageInteractionJob(t, provider, "textDocument/hover")
		if job.document != doc || job.content != f.snapshot() || job.uri != uri {
			t.Fatal("hover requested another version")
		}
		providerResult := pencodeLanguageEvent{session: provider, method: "result", job: job, result: json.RawMessage(`{"contents":"hover text"}`)}
		m.languages.events <- providerResult
		for range 3 {
			languageInteractionStep(t, app)
		}
		if !hover.open {
			t.Fatal("hover result was lost when the other pane had focus")
		}
		editorContextBounds(t, m, "lsp-hover")
		state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseRight, X: px, Y: py})
		state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseRight, X: px, Y: py})
		languageInteractionStep(t, app)
		if !m.languageUI.contextOpen || m.languageUI.contextFile != f {
			t.Fatal("context menu selected the wrong side")
		}
		editorContextClick(t, app, m, state, "lsp-context-7")
		job = languageInteractionJob(t, provider, "textDocument/references")
		if job.offset < start || job.offset >= start+len(name) || job.document != doc {
			t.Fatalf("wrong reference request: byte %d", job.offset)
		}
		pos, _ := lsp.PositionAt(doc.content, start, lsp.UTF16)
		end, _ := lsp.PositionAt(doc.content, start+len(name), lsp.UTF16)
		location := lsp.Location{URI: uri, Range: lsp.Range{Start: pos, End: end}}
		result, _ := json.Marshal([]lsp.Location{location})
		m.acceptLanguageResult(w, pencodeLanguageEvent{session: provider, job: job, result: result})
		if p := &m.languages.panel; len(p.referenceGroups) == 0 {
			t.Fatalf("missing references on pane %d: job token %d, panel token %d, body %q seen %v active %v focused %v", side, job.token, p.token, p.body, doc.seen, m.languageFile() == f, w.FocusedControl())
		}
		rows := m.languages.panel.referenceGroups[0].rows
		if !strings.Contains(rows[0].label, name) {
			t.Fatalf("reference preview %q", rows[0].label)
		}
		m.navigateLanguageLocation(w, location, nil)
		field, _ := w.Get[engineui.TextField](editor.control)
		if !field.Focused || field.Anchor != f.snapshot().RuneOffset(start) || field.Caret != f.snapshot().RuneOffset(start+len(name)) || s.activeTab() != tab {
			t.Fatal("navigation left the snapshot")
		}
		m.dismissLanguagePanel()
		m.sourceChanged()
		languageInteractionStep(t, app)
	}
	if n := testing.AllocsPerRun(100, func() { m.syncLanguageDocuments() }); n != 0 {
		t.Fatalf("warm snapshot sync allocated %g", n)
	}
	s.Active = ""
	m.syncLanguageDocuments()
	for _, f := range tab.languageFiles {
		if m.languages.documents[f] != nil {
			t.Fatal("closed diff retained document")
		}
	}
	if file.snapshot() != original || m.languages.documents[file].uri != uri {
		t.Fatal("diff changed the workspace document")
	}
}

func TestLanguageWorkingDiffAcceptsCompletionIntoDraft(t *testing.T) {
	app, m, normal, state := languageInteractionFixture(t)
	w, s := m.fontResources, m.sourceState()
	f := m.activeFile()
	original := f.snapshot()
	value := "package demo\nvar _ = fmt."
	tab := &sourceTab{Key: "changes", Kind: "changes", Path: f.Path, Local: "package demo\n", Result: value, Saved: value}
	view, err := sourceDiffContext(t.Context(), m.backend, tab.Local, tab.Result)
	if err != nil {
		t.Fatal(err)
	}
	tab.initializeDiffView(view)
	s.RefreshStarted, s.RefreshPending = true, false
	s.Snapshot.Repository.Root = m.projectPath
	s.Tabs, s.Active = []*sourceTab{tab}, tab.Key
	m.sourceChanged()
	for range 3 {
		languageInteractionStep(t, app)
	}
	control := s.diffEditors[1].control
	w.Focus(control)
	field := w.Mut[engineui.TextField](control)
	field.Caret, field.Anchor = len(value), len(value)
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "P"})
	languageInteractionStep(t, app)
	if tab.Result != value+"P" || m.languageUI.completionFile != tab.languageFiles[1] {
		t.Fatalf("typed %q, automatic completion scheduled %t", tab.Result, m.languageUI.completionFile != nil)
	}
	m.languageUI.completionDue = time.Now().Add(-time.Millisecond)
	languageInteractionStep(t, app)
	provider := m.languages.sessions[pencodeLanguageKey{m.projectPath, normal.server.ID, 2}]
	job := languageInteractionJob(t, provider, "textDocument/completion")
	m.languages.events <- pencodeLanguageEvent{session: provider, method: "result", job: job, result: json.RawMessage(`[{"label":"Println"}]`)}
	for range 2 {
		languageInteractionStep(t, app)
	}
	editorContextBounds(t, m, "lsp-completion")
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEnter})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEnter})
	languageInteractionStep(t, app)
	if tab.Result != value+"Println" || f.snapshot() != original {
		t.Fatalf("completion result %q", tab.Result)
	}
	if !tab.undoResult(false) || tab.Result != value+"P" {
		t.Fatalf("undo result %q", tab.Result)
	}
}
