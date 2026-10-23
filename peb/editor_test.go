package peb

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestEditorPayloadsAndBounds(t *testing.T) {
	message := EditorMessage{Editor: "note", Path: "/workspace/a.note", Content: strings.Repeat("文", 30000), Event: EditorEvent{Kind: "input", Ref: "source", Value: "", Checked: true, Anchor: 2, Focus: 8}}
	data, err := EncodeEditorMessage(nil, message)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeEditorMessage(data)
	if err != nil || got != message {
		t.Fatalf("message: %+v %v", got, err)
	}
	clear(data)
	if got != message {
		t.Fatal("decoded document borrows the reader's buffer")
	}
	empty := ""
	for _, reply := range []EditorReply{{}, {Content: &empty}, {View: &EditorView{HTML: `<input data-ref="name">`, CSS: "input {color:red}", Events: []EditorBinding{{Ref: "name", Kind: "input"}}}, Content: &empty}} {
		data, err := EncodeEditorReply(data, reply)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeEditorReply(data)
		if err != nil || !reflect.DeepEqual(got, reply) {
			t.Fatalf("reply: %+v %v", got, err)
		}
		for n := range len(data) {
			if _, err := DecodeEditorReply(data[:n]); err == nil {
				t.Fatalf("accepted reply truncated at %d", n)
			}
		}
		if _, err := DecodeEditorReply(append(bytes.Clone(data), 0)); err == nil {
			t.Fatal("accepted trailing data")
		}
	}
	if _, err := DecodeEditorReply([]byte{2, 0, 0, 0, 0, 0, 0, 0}); err == nil {
		t.Fatal("accepted invalid optional flag")
	}
	message.Content = strings.Repeat("a", MaxDocument+1)
	if _, err := EncodeEditorMessage(data, message); err == nil {
		t.Fatal("accepted oversized document")
	}
}
