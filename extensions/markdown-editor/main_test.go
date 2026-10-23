package main

import (
	"strings"
	"testing"

	"github.com/pencode-labs/pencode/peb"
)

func TestMarkdownEditorRetainsInputAndEscapesSource(t *testing.T) {
	message := peb.EditorMessage{Editor: "markdown", Content: "# Hello\n</textarea><script>bad()</script>", Event: peb.EditorEvent{Kind: "open"}}
	reply, err := markdownEditor(message)
	if err != nil || reply.View == nil {
		t.Fatalf("open: %+v %v", reply, err)
	}
	if !strings.Contains(reply.View.HTML, "<h1>Hello</h1>") || strings.Contains(reply.View.HTML, "<script>") {
		t.Fatal("preview or escaping failed")
	}
	message.Event = peb.EditorEvent{Kind: "input", Ref: "source", Value: ""}
	reply, err = markdownEditor(message)
	if err != nil || reply.View != nil || reply.Content == nil || *reply.Content != "" {
		t.Fatal("input must clear the document without replacing the retained textarea")
	}
}
