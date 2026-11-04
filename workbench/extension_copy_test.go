package workbench

import (
	"html"
	"testing"

	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestExtensionCopyButtonUsesLocalClipboardAndRegionUpdates(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	var copied string
	state.SetClipboard(func(value string) bool { copied = value; return true }, nil)
	show := func(value string) {
		t.Helper()
		if err := m.applyExtensionUI("test", &extensions.ActionSession{}, peb.UI{ID: "copy", Kind: peb.EditorTab, View: &peb.EditorView{
			HTML:   `<section data-ref="message" data-pencode-region><button type="button" data-ref="copy" data-pencode-copy="` + html.EscapeString(value) + `">复制</button><button type="button" data-ref="disabled" disabled data-pencode-copy="disabled">不可用</button><div hidden><button data-ref="hidden" data-pencode-copy="hidden">隐藏</button></div><button data-ref="owned" data-pencode-copy="owned">自定义</button></section>`,
			Events: []peb.EditorBinding{{Ref: "owned", Kind: "click"}},
			CSS:    `button { width: 80px; height: 32px; }`,
		}}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(m.fontResources, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
	}
	const original = "**任务** <tag> & \"引用\"\n\n  code🙂"
	show(original)
	s := m.actions.ui.editor
	if len(s.copies) != 1 || s.copies["copy"] != original {
		t.Fatal("copy action ignored opt-in, disabled/hidden controls or explicit bindings", s.copies)
	}
	editorContextClick(t, app, m, state, s.prefix+"control-copy")
	if copied != original {
		t.Fatalf("copied %q, want exact source %q", copied, original)
	}
	show("changed\nmessage")
	s = m.actions.ui.editor
	props := pencodeExtensionSurfaceProps{Surface: s}
	event := htmlui.ComponentEventContext[pencodeExtensionSurfaceProps, struct{}]{Props: &props, Ref: "copy", Event: htmlui.Event{Kind: htmlui.EventClick}}
	if n := testing.AllocsPerRun(100, func() { s.definition.Behavior.HandleEvent(&event) }); n != 0 {
		t.Fatalf("warm copy action allocated %g", n)
	}
	if copied != "changed\nmessage" {
		t.Fatal("region patch retained stale clipboard text", copied)
	}
}
