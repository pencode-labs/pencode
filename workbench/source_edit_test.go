package workbench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

func TestSourceRevertBlocksPreservesExactBytes(t *testing.T) {
	for _, test := range []struct{ name, left, right string }{
		{"replace", "a\nold\nz\n", "a\nnew\nz\n"},
		{"insert", "a\nz\n", "a\nx\ny\nz\n"},
		{"delete", "a\nx\ny\nz\n", "a\nz\n"},
		{"CRLF", "前\r\n旧\r\n后\r\n", "前\r\n新\r\n后\r\n"},
		{"newline", "a", "a\n"},
		{"new file", "", "new\n"},
		{"deleted file", "old\n", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			view, err := sourceDiffContext(t.Context(), newPencodeBackend(t), test.left, test.right)
			if err != nil {
				t.Fatal(err)
			}
			tab := &sourceTab{Kind: "changes", Local: test.left, Result: test.right}
			tab.applyDiffView(view)
			if len(tab.Blocks) != 1 || !tab.revertBlock(0) || tab.Result != test.left {
				t.Fatalf("revert=%q blocks=%+v", tab.Result, tab.Blocks)
			}
			if tab.revertBlock(0) {
				t.Fatal("reused stale block offsets")
			}
			if !tab.undoResult(false) || tab.Result != test.right || !tab.undoResult(true) || tab.Result != test.left {
				t.Fatal("block undo/redo did not preserve bytes")
			}
		})
	}
}

func TestSourceDiffNativeEditingCopyRevertAndSave(t *testing.T) {
	r := sourceTestRepository(t)
	const name = "main.go"
	const left = "package demo\nvar A = 1\n// a\n// b\n// c\nvar B = 2\n"
	const right = "package demo\nvar A = 10\nvar Added = 3\n// a\n// b\n// c\nvar B = 20\n"
	sourceTestWrite(t, r, name, left)
	sourceTestGit(t, r, "add", "--all")
	sourceTestGit(t, r, "commit", "-m", "base")
	sourceTestWrite(t, r, name, right)
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", name)
	app, m, _, state := languageInteractionFixture(t)
	m.sourceAction("sc-toggle")
	sourceWait(t, app, m)
	m.sourceAction("sc-open:changes:" + name)
	sourceWait(t, app, m)
	tab := m.sourceState().activeTab()
	settle := func() {
		t.Helper()
		settleSourceHighlight(t, m)
		m.sourceChanged()
		for range 2 {
			languageInteractionStep(t, app)
		}
	}
	settle()
	if len(tab.Blocks) != 2 {
		t.Fatalf("blocks=%+v", tab.Blocks)
	}
	leftControl, ok := m.fontResources.HTMLControl(m.surface, "sc-code-left-input")
	if !ok {
		t.Fatal("missing readonly editor")
	}
	rightControl, ok := m.fontResources.HTMLControl(m.surface, "sc-code-right-input")
	if !ok {
		t.Fatal("missing editable editor")
	}
	leftBinding, _ := m.fontResources.Get[htmlui.Control](leftControl)
	rightBinding, _ := m.fontResources.Get[htmlui.Control](rightControl)
	if !leftBinding.ReadOnly || rightBinding.ReadOnly {
		t.Fatal("wrong editor permissions")
	}
	if leftBinding.DefaultValue != "" || rightBinding.DefaultValue != "" || leftBinding.Value != left || rightBinding.Value != right {
		t.Fatal("comparison must bind live text without duplicating it in HTML layout")
	}
	valueEntity, _ := m.fontResources.FindPart[engineui.ValuePart, scene.Text2D](rightControl)
	value := m.fontResources.Ref[scene.Text2D](valueEntity)
	if value.Value != right || len(value.Spans) == 0 || value.LineLayout == nil {
		t.Fatal("native editor lost source text/highlighting/alignment")
	}
	leftTextEntity, _ := m.fontResources.FindPart[engineui.ValuePart, scene.Text2D](leftControl)
	leftText := m.fontResources.Ref[scene.Text2D](leftTextEntity)
	_, leftY, _, _, leftOK := scene.TextCaretRect2D(leftText, left, utf8.RuneCountInString(left[:strings.Index(left, "var B")]))
	_, rightY, _, _, rightOK := scene.TextCaretRect2D(value, right, utf8.RuneCountInString(right[:strings.Index(right, "var B")]))
	if !leftOK || !rightOK || leftY != rightY || leftY != 120 {
		t.Fatalf("aligned native caret geometry differs: left=%g right=%g", leftY, rightY)
	}
	var copied string
	state.SetClipboard(func(value string) bool { copied = value; return true }, func() (string, bool) { return "PASTE", true })
	m.fontResources.Focus(leftControl)
	field := m.fontResources.Mut[engineui.TextField](leftControl)
	field.Anchor, field.Caret = 0, 0
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "forbidden"})
	languageInteractionStep(t, app)
	if m.fontResources.Ref[engineui.TextField](leftControl).Value != left {
		t.Fatal("readonly side accepted input")
	}
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyLeftControl})
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyA})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyA})
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyC})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyC})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyLeftControl})
	languageInteractionStep(t, app)
	if copied != left {
		t.Fatalf("copied aligned filler instead of original source: %q", copied)
	}
	gutter := editorContextBounds(t, m, "sc-diff-gutter")
	button := editorContextBounds(t, m, "sc-revert-block:0")
	if gutter.Width < 20 || button.X < gutter.X || button.X+button.Width > gutter.X+gutter.Width+1 {
		t.Fatalf("button outside gutter: %+v %+v", gutter, button)
	}
	editorContextClick(t, app, m, state, "sc-revert-block:0")
	settle()
	if strings.Contains(tab.Result, "var Added") || !strings.Contains(tab.Result, "var A = 1\n") || !strings.Contains(tab.Result, "var B = 20\n") || len(tab.Blocks) != 1 {
		t.Fatalf("reverted wrong content: %q", tab.Result)
	}
	if !tab.undoResult(false) || tab.Result != right {
		t.Fatal("revert not undoable")
	}
	settle()
	m.fontResources.Focus(rightControl)
	field = m.fontResources.Mut[engineui.TextField](rightControl)
	field.Anchor, field.Caret = utf8.RuneCountInString(right), utf8.RuneCountInString(right)
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "// manual\n"})
	languageInteractionStep(t, app)
	if tab.Result != right+"// manual\n" {
		t.Fatalf("native edit not applied: %q", tab.Result)
	}
	settle()
	m.sourceAction("sc-save-diff")
	sourceWait(t, app, m)
	disk, err := os.ReadFile(filepath.Join(r.Root, name))
	if err != nil || string(disk) != tab.Result || tab.Saved != tab.Result {
		t.Fatalf("diff save failed: %q %v", disk, err)
	}
	staged := sourceTestGit(t, r, "diff", "--cached", "--", name)
	if staged != "" {
		t.Fatal("saving unexpectedly staged the file")
	}
}

func TestSourceDiffSnapshotsStayReadonly(t *testing.T) {
	for _, kind := range []string{"staged", "history"} {
		tab := &sourceTab{Key: kind, Kind: kind, Path: "main.go", Revision: strings.Repeat("a", 40)}
		m := &workspaceModel{sourceControl: pencodeSourceControl{Tabs: []*sourceTab{tab}, Active: tab.Key}}
		var b sourceVNodeView
		b.reset()
		m.renderSourceTabs(&b)
		readonly := 0
		for _, group := range b.groups {
			if group.node.Tag != "textarea" {
				continue
			}
			for _, attr := range group.node.Attributes {
				if attr.Name == "readonly" {
					readonly++
				}
			}
		}
		if readonly != 2 || tab.revertBlock(0) {
			t.Fatalf("%s snapshot permits editing", kind)
		}
	}
}

func TestSourceDiffRestoresFileEditorPointerAndInput(t *testing.T) {
	r := sourceTestRepository(t)
	const name = "main.go"
	const content = "package demo\nvar Value = 1\n"
	sourceTestWrite(t, r, name, content)
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", name)
	app, m, _, state := languageInteractionFixture(t)
	m.languages = nil
	s := m.sourceState()
	s.RefreshStarted, s.RefreshPending = true, false
	editor := m.editor.control
	for _, kind := range []string{"initial", "changes", "staged", "history"} {
		t.Run(kind, func(t *testing.T) {
			if kind != "initial" {
				tab := &sourceTab{Key: kind + "::" + name, Kind: kind, Path: name, Local: content, Result: content, Revision: strings.Repeat("a", 40)}
				s.Tabs, s.Active = []*sourceTab{tab}, tab.Key
				m.sourceChanged()
				languageInteractionStep(t, app)
				control, ok := m.fontResources.HTMLControl(m.surface, "editor")
				visibility := m.fontResources.Ref[scene.Visibility](control)
				field := m.fontResources.Ref[engineui.TextField](control)
				if !ok || control != editor || visibility == nil || visibility.Visible || field == nil || field.Focused {
					t.Fatal("comparison did not retain and deactivate the file editor")
				}
				m.selectProjectTab(m.activeProjectState(), name)
				m.sourceChanged()
				languageInteractionStep(t, app)
			}
			m.fontResources.Focus(0)
			r := editorContextBounds(t, m, "editor")
			scale := float64(m.settings.Zoom) / 100
			x, y := float64(r.X+45)*scale, float64(r.Y+10)*scale
			state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
			languageInteractionStep(t, app)
			state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
			languageInteractionStep(t, app)
			field := m.fontResources.Ref[engineui.TextField](m.editor.control)
			if !field.Focused || field.Caret == 0 {
				t.Fatalf("click did not place file editor caret: %+v", field)
			}
			before := m.activeContent()
			first, last := pencodeSelectionBytes(before, min(field.Anchor, field.Caret), max(field.Anchor, field.Caret))
			state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "x"})
			languageInteractionStep(t, app)
			if want := before[:first] + "x" + before[last:]; m.activeContent() != want {
				t.Fatalf("file editor content = %q, want %q", m.activeContent(), want)
			}
		})
	}
}
