package peb

import (
	"reflect"
	"testing"
)

func TestEditorTabUIRoundTrip(t *testing.T) {
	v := UI{ID: "thread", Title: "工作会话", Kind: EditorTab, Busy: true, View: &EditorView{HTML: "<p>reply</p>", Events: []EditorBinding{}}}
	data, err := EncodeUI(nil, v)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeUI(data)
	if err != nil || !reflect.DeepEqual(got, v) {
		t.Fatal(got, err)
	}
	v.Placement.Width = 100
	if _, err = EncodeUI(nil, v); err == nil {
		t.Fatal("editor tab accepted overlay placement")
	}
	v.Placement = UIPlacement{}
	v.AutoCloseMS = 100
	if _, err = EncodeUI(nil, v); err == nil {
		t.Fatal("editor tab accepted notification expiry")
	}
}
