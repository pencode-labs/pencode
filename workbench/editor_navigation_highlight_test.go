package workbench

import (
	"math"
	"strings"
	"testing"

	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	"github.com/pencode-labs/pencode/lsp"
)

func TestEditorNavigationLineBackground(t *testing.T) {
	app, m, _, _ := languageInteractionFixture(t)
	w, file := m.fontResources, m.activeFile()
	content := "first\r\n" + strings.Repeat("中🙂 target ", 30) + "\r\n\r\nlast\r\n"
	m.updateActiveContent(content)
	m.setEditorValueAndCaret(w, content, 0)
	languageInteractionStep(t, app)
	paint := &m.editor.navigationPaint
	jump := func(n fileNavigation) {
		t.Helper()
		n.origin = m.navigationLocation()
		m.finishFileNavigation(m.activeProjectState(), file, &n)
		for range 2 {
			languageInteractionStep(t, app)
		}
	}
	check := func(line int) {
		t.Helper()
		visible, _ := w.Get[scene.Visibility](paint.background)
		if !visible.Visible || m.activeContent() != content {
			t.Fatal("navigation background missing or document changed")
		}
		text := w.Mut[scene.Text2D](m.editor.text)
		origin, _ := w.Get[scene.UITransform](m.editor.text)
		size, _ := w.Get[scene.Size2D](m.editor.control)
		top, bottom, rows := float32(math.MaxFloat32), float32(0), 0
		for visual := range scene.TextLineCount2D(text) {
			start, _, rect, _ := scene.TextLineRange2D(text, visual)
			logical, _ := file.snapshotBuffer().Position(start)
			if logical == line {
				top = min(top, rect.Y+origin.Translation.Y.Value)
				bottom = max(bottom, rect.Y+rect.Height+origin.Translation.Y.Value)
				rows++
			}
		}
		if line == 1 && rows < 2 {
			t.Fatal("fixture did not wrap the target line")
		}
		background, _ := w.Get[scene.UITransform](paint.background)
		sprite, _ := w.Get[scene.Sprite2D](paint.background)
		textStack, _ := w.Get[scene.UIStackIndex](m.editor.text)
		controlStack, _ := w.Get[scene.UIStackIndex](m.editor.control)
		stack, _ := w.Get[scene.UIStackIndex](paint.background)
		color := m.workbenchTheme.link
		color.A *= .10
		if background.Translation.Y.Value != max(0, top) || sprite.Height != min(size.Height, bottom)-max(0, top) || sprite.Width != size.Width || sprite.Shape.Fill != color || stack >= textStack || stack <= controlStack {
			t.Fatalf("background does not cover target rows with the theme tint: transform=%+v sprite=%+v", background, sprite)
		}
	}
	jump(fileNavigation{line: 2})
	check(1)
	entity := paint.background
	if allocs := testing.AllocsPerRun(100, func() {
		w.Mut[scene.UITransform](m.editor.text).Translation.Y.Value -= .001
		paint.sync(w, m)
	}); allocs != 0 {
		t.Fatalf("scrolling navigation background allocated %g times", allocs)
	}
	check(1)
	// Moving within the destination line retains the hint; leaving it clears it.
	field := w.Mut[engineui.TextField](m.editor.control)
	field.Caret++
	paint.sync(w, m)
	check(1)
	field.Caret = 0
	paint.sync(w, m)
	if visible, _ := w.Get[scene.Visibility](entity); visible.Visible {
		t.Fatal("moving away left a stale navigation background")
	}
	jump(fileNavigation{selection: &lsp.Range{Start: lsp.Position{Line: 1, Character: 3}, End: lsp.Position{Line: 1, Character: 10}}, encoding: lsp.UTF16})
	check(1)
	jump(fileNavigation{line: 3})
	check(2) // Empty CRLF lines still receive a full-height background.
	if paint.background != entity {
		t.Fatal("repeated navigation did not reuse its background")
	}
	m.navigateEditorHistory(-1)
	for range 2 {
		languageInteractionStep(t, app)
	}
	check(1)
	m.updateActiveContent(content + "edited")
	paint.sync(w, m)
	if visible, _ := w.Get[scene.Visibility](entity); visible.Visible || paint.file != nil {
		t.Fatal("editing left a stale navigation background")
	}
}
