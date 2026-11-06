package workbench

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	engineui "github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestUnsupportedFileRequiresExplicitTextOpen(t *testing.T) {
	for _, name := range []string{"tool", "image.png", "unknown.custom", "fake.txt"} {
		t.Run(name, func(t *testing.T) {
			m, p := resourceTestModel(t)
			const raw = "header\x00\xfftail"
			resourceTestWrite(t, p, name, raw)
			openProjectFileAndWait(t, m, p, name)
			f := m.activeFile()
			if f == nil || f.document != nil || f.content() != "" || m.textEditorActive() {
				t.Fatal("unsupported file opened as source")
			}
			if _, err := m.requestFileSave(p, f); err == nil {
				t.Fatal("unconfirmed file was saveable")
			}
			if !m.selectEditor(pencodeTextEditor) {
				t.Fatal("explicit open rejected")
			}
			if err := awaitFileIO(t, m); err != nil {
				t.Fatal(err)
			}
			if !m.textEditorActive() || f.document == nil || f.content() != "header\x00\uFFFDtail" {
				t.Fatal("explicit open lost text", f.content())
			}
			data, _ := os.ReadFile(filepath.Join(p.Path, name))
			if string(data) != raw {
				t.Fatal("opening modified disk")
			}
			m.updateActiveContent("edited text")
			if !f.Dirty {
				t.Fatal("explicit text did not use host editing")
			}
			m.saveActiveFile()
			if err := awaitFileIO(t, m); err != nil {
				t.Fatal(err)
			}
			data, _ = os.ReadFile(filepath.Join(p.Path, name))
			if string(data) != "edited text" {
				t.Fatal("explicit text did not save")
			}
			if err := m.requestFileReload(p, false); err != nil {
				t.Fatal(err)
			}
			if err := awaitFileIO(t, m); err != nil {
				t.Fatal(err)
			}
			if !m.textEditorActive() || f.content() != "edited text" {
				t.Fatal("reload lost explicit choice")
			}
		})
	}
}

func TestExtensionHandlesUnknownFilesAndReevaluatesOnRemoval(t *testing.T) {
	m, p := resourceTestModel(t)
	resourceTestWrite(t, p, "asset.custom", "binary\x00data")
	openProjectFileAndWait(t, m, p, "asset.custom")
	f := m.activeFile()
	editor := extensions.Editor{Action: peb.Action{ID: "extensions/test/view", Text: "View", Location: peb.OpenWith, Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".custom"}}}}
	m.importEditors([]extensions.Editor{editor})
	if got := m.fileEditor(f); got == nil || got.ID != editor.ID || m.textEditorActive() {
		t.Fatal("registered viewer was not selected")
	}
	disabled := editor.Action
	disabled.Disabled = true
	m.updateEditorAction(disabled)
	if m.fileEditor(f) != nil {
		t.Fatal("disabled viewer selected")
	}
	m.updateEditorAction(editor.Action)
	if m.fileEditor(f) == nil {
		t.Fatal("enabled viewer not restored")
	}
	m.removeEditors("extensions/test/")
	if m.fileEditor(f) != nil || m.textEditorActive() {
		t.Fatal("removed viewer fell through to source")
	}
}

func TestExplicitTextOpenStaysWithOriginalTab(t *testing.T) {
	m, p := resourceTestModel(t)
	resourceTestWrite(t, p, "one.custom", "first")
	resourceTestWrite(t, p, "two.go", "package second")
	openProjectFileAndWait(t, m, p, "one.custom")
	first := m.activeFile()
	openProjectFileAndWait(t, m, p, "two.go")
	second := m.activeFile()
	m.selectProjectTab(p, first.Path)
	if !m.selectEditor(pencodeTextEditor) {
		t.Fatal("explicit open rejected")
	}
	m.selectProjectTab(p, second.Path)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if m.activeFile() != second || second.content() != "package second" || first.content() != "first" || first.editorID != pencodeTextEditor {
		t.Fatal("pending open changed the active tab")
	}
}

func TestUnsupportedFilePromptUsesRetainedClick(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	p := m.activeProjectState()
	resourceTestWrite(t, p, "unsupported.data", "hello")
	openProjectFileAndWait(t, m, p, "unsupported.data")
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	editorContextClick(t, app, m, state, "editor-open-text")
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	if !m.textEditorActive() || m.activeContent() != "hello" {
		t.Fatal("prompt click did not reopen the original bytes")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		field, ok := m.fontResources.Get[engineui.TextField](m.editor.control)
		if ok && field.Text().String() == "hello" {
			return
		}
		languageInteractionStep(t, app)
		time.Sleep(time.Millisecond)
	}
	t.Fatal("code editor control did not receive the opened bytes")
}

func TestExplicitTextOpenDoesNotResurrectClosedTab(t *testing.T) {
	m, p := resourceTestModel(t)
	resourceTestWrite(t, p, "closing.custom", "content")
	openProjectFileAndWait(t, m, p, "closing.custom")
	if !m.selectEditor(pencodeTextEditor) {
		t.Fatal("explicit open rejected")
	}
	m.closeProjectTab(p, "closing.custom")
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if p.Files["closing.custom"] != nil || len(p.Tabs) != 0 {
		t.Fatal("late result resurrected closed tab")
	}
}

func TestExplicitTextChoiceRestoresWithExtensionSplit(t *testing.T) {
	m, p := resourceTestModel(t)
	resourceTestWrite(t, p, "restore.custom", "text")
	openProjectFileAndWait(t, m, p, "restore.custom")
	if !m.selectEditor(pencodeTextEditor) {
		t.Fatal("explicit open rejected")
	}
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	f := m.activeFile()
	f.editorID, f.editorSplit = "extensions/test/preview", true
	s := newPencodeTestSession(t, m.backend)
	m.session, p.historyKey = s, p.Path
	m.saveProjectState(p)
	next := &workspaceModel{backend: m.backend, session: newSession(m.backend, "", s.stateDirectory), activeProjectIndex: -1}
	t.Cleanup(func() {
		for _, project := range next.projects {
			project.stopScan()
			project.stopWatch()
			for _, file := range project.Files {
				file.closeDocument()
			}
		}
	})
	if err := loadProjectAndWait(t, next, p.Path); err != nil {
		t.Fatal(err)
	}
	got := next.activeFile()
	if got == nil || got.content() != "text" || !got.forcedText || !got.editorSplit || got.editorID != f.editorID {
		t.Fatal("restoring extension split lost explicit text decoding")
	}
}
