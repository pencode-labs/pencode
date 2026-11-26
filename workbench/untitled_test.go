package workbench

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/onix-engine/onix/input"
)

func TestUntitledKeyboardEditingAndFirstSave(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	p, world := m.activeProjectState(), m.fontResources
	key := func(k input.KeyCode, primary bool) {
		modifier := input.KeyLeftControl
		if runtime.GOOS == "darwin" {
			modifier = input.KeyLeftSuper
		}
		if primary {
			state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: modifier})
		}
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: k})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: k})
		if primary {
			state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: modifier})
		}
		languageInteractionStep(t, app)
		languageInteractionStep(t, app)
	}
	typeText := func(s string) {
		state.Enqueue(input.Event{Kind: input.EventTextInput, Text: s})
		languageInteractionStep(t, app)
	}
	key(input.KeyN, true)
	f := m.activeFile()
	if f == nil || !f.untitled() || f.Name != "Untitled-1" || !pencodeControlFocused(world, m.surface, "editor") {
		t.Fatalf("new editor did not receive focus: %q", m.activePath)
	}
	typeText("草稿🙂")
	key(input.KeyN, true)
	if m.activeFile() == f || m.activeContent() != "" || m.activeFile().Name != "Untitled-2" {
		t.Fatal("second shortcut did not create an independent empty editor")
	}
	key(input.KeyW, true)
	if m.activeFile() != f || !f.Dirty || f.content() != "草稿🙂" {
		t.Fatal("closing the empty editor lost the previous draft")
	}
	if _, err := os.Stat(filepath.Join(p.Path, f.Name)); !os.IsNotExist(err) {
		t.Fatalf("draft created a disk file: %v", err)
	}
	key(input.KeyS, true)
	if !pencodeControlFocused(world, m.surface, "save-as-path") {
		t.Fatal("first save did not focus the filename field")
	}
	key(input.KeyEscape, false)
	if !f.Dirty || m.closing.action != pencodeCloseNone || !pencodeControlFocused(world, m.surface, "editor") {
		t.Fatal("cancelling save did not return to the draft")
	}
	key(input.KeyS, true)
	typeText("笔记.txt")
	key(input.KeyEnter, false)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if m.closing.action != pencodeCloseNone || m.activeFile() != f || f.Dirty || f.Path != "笔记.txt" || len(f.undo) == 0 {
		t.Fatalf("save did not promote the same buffer: path=%q error=%q", f.Path, m.closing.error)
	}
	if data, err := os.ReadFile(filepath.Join(p.Path, f.Path)); err != nil || string(data) != "草稿🙂" {
		t.Fatalf("saved text %q, error %v", data, err)
	}
	typeText("!")
	key(input.KeyS, true)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(p.Path, f.Path)); err != nil || string(data) != "草稿🙂!" || f.Dirty || m.closing.action != pencodeCloseNone {
		t.Fatalf("second save failed: text=%q dirty=%t error=%v", data, f.Dirty, err)
	}
}

func TestUntitledSaveOnCloseAndCollision(t *testing.T) {
	m, p, _, _ := delayedFileModel(t)
	resourceTestWrite(t, p, "Untitled-1", "original")
	openProjectFile(p, "Untitled-1")
	m.syncActiveProjectFields()
	original := m.activeFile()
	m.newUntitled()
	draft := m.activeFile()
	m.updateActiveContent("draft")
	m.distributeFileChanges(p, nil, true)
	if state := m.captureProjectState(p); state.ActivePath == draft.Path || len(state.Tabs) != 1 || state.Tabs[0].Path != original.Path {
		t.Fatalf("project state captured a transient path: %+v", state)
	}
	m.closeEditorTabs(pencodeCloseTab, pencodeEditorTab{project: p, path: draft.Path})
	resourceTestWrite(t, p, "taken.txt", "")
	m.closing.savePath = "taken.txt"
	m.resolveClose(true)
	if err := awaitFileIO(t, m); err == nil || !m.namingUntitled() || !draft.Dirty {
		t.Fatalf("failed save lost draft: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(p.Path, "taken.txt")); err != nil || len(data) != 0 {
		t.Fatal("overwrote existing file")
	}
	m.closing.savePath = "notes.txt"
	m.resolveClose(true)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if len(p.Tabs) != 1 || m.activeFile() != original || m.closing.action != pencodeCloseNone {
		t.Fatal("save on close did not close the intended tab")
	}
	if data, err := os.ReadFile(filepath.Join(p.Path, "notes.txt")); err != nil || string(data) != "draft" {
		t.Fatalf("saved text %q, error %v", data, err)
	}
}

func TestUntitledFromLauncher(t *testing.T) {
	m, p, _, _ := delayedFileModel(t)
	m.projects, m.lastDirectory = nil, p.Path
	m.syncActiveProjectFields()
	m.handleShortcutKeys(nil, keymapState(pencodeBindingChord("Primary+N"), "darwin"), "darwin")
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if m.screen != pencodeWorkspace || m.activeFile() == nil || !m.activeFile().untitled() || !m.activeProjectState().Loose {
		t.Fatal("launcher shortcut did not open an untitled editor")
	}
}
