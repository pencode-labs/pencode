package workbench

import (
	"encoding/base64"
	"runtime"
	"strings"
	"testing"

	"github.com/diiyw/onix/input"
	engineui "github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestExtensionImagePasteIsOptInAndPreservesText(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	if err := m.applyExtensionUI("test", &extensions.ActionSession{}, peb.UI{ID: "test", Kind: peb.EditorTab, View: &peb.EditorView{
		HTML:   `<textarea data-ref="prompt" data-pencode-paste-images>任务</textarea><textarea data-ref="notes">笔记</textarea>`,
		Events: []peb.EditorBinding{{Ref: "prompt", Kind: "input"}, {Ref: "notes", Kind: "input"}},
	}}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		languageInteractionStep(t, app)
	}
	s := m.actions.ui.editor
	state := input.NewState()
	if runtime.GOOS == "darwin" {
		state.PressKey(input.KeyLeftSuper)
	} else {
		state.PressKey(input.KeyLeftControl)
	}
	state.PressKey(input.KeyV)
	reads := 0
	readImage := func() ([]byte, error) { reads++; return []byte("png"), nil }
	control, _ := w.HTMLControl(m.surface, s.focus[1].id)
	w.Focus(control)
	if s.interceptImagePaste(state, readImage) || reads != 0 {
		t.Fatal("ordinary input accessed image clipboard")
	}
	control, _ = w.HTMLControl(m.surface, s.focus[0].id)
	w.Focus(control)
	field := w.Mut[engineui.TextField](control)
	field.Anchor, field.Caret = 1, 1
	if s.interceptImagePaste(state, func() ([]byte, error) { return nil, nil }) {
		t.Fatal("text paste was intercepted")
	}
	if !s.interceptImagePaste(state, readImage) || reads != 1 || field.Value != "任务" || field.Anchor != 1 || field.Caret != 1 {
		t.Fatal("image paste lost text or selection")
	}
	if !w.Has[engineui.TextFieldKeyHandler](control) {
		t.Fatal("paste-only control did not install keyboard interception")
	}
	idle := input.NewState()
	if n := testing.AllocsPerRun(100, func() { s.interceptImagePaste(idle, readImage) }); n != 0 {
		t.Fatalf("idle image paste handling allocated %g", n)
	}
}

func TestClipboardImageEventCarriesImageAndSelection(t *testing.T) {
	value := peb.EditorEvent{Ref: "prompt", Value: "当前文本", Anchor: 1, Focus: 3}
	data := []byte("clipboard image bytes")
	event := clipboardImageEvent(value, data)
	if event.Kind != "paste" || event.InputType != "image/png" || event.Value != value.Value || event.Anchor != 1 || event.Focus != 3 || event.Ref != "prompt" {
		t.Fatalf("paste lost input snapshot: %+v", event)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(event.Data, "data:image/png;base64,"))
	if err != nil || string(decoded) != string(data) {
		t.Fatal("paste changed image bytes", err)
	}
	if _, err := peb.EncodeUIEvent(nil, peb.UIEvent{ID: "composer", Event: event}); err != nil {
		t.Fatal("paste did not fit extension protocol", err)
	}
	event = clipboardImageEvent(value, make([]byte, peb.MaxDocument))
	if event.InputType != "error" || event.Value != value.Value || event.Data == "" {
		t.Fatal("oversized image did not return a readable error")
	}
}
