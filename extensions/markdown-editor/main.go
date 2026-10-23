// Markdown editor demonstrates a retained HTML/CSS editor with native input
// events and host-owned text edits, save and undo history.
package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"html"
	"os"

	"github.com/pencode-labs/pencode/peb"
	"github.com/yuin/goldmark"
)

//go:embed resources/editor.css
var editorCSS string

func markdownEditor(message peb.EditorMessage) (peb.EditorReply, error) {
	if message.Editor != "markdown" || message.Binary {
		return peb.EditorReply{}, fmt.Errorf("unsupported document")
	}
	if message.Event.Kind == "input" && message.Event.Ref == "source" {
		return peb.EditorReply{Content: &message.Event.Value}, nil
	}
	if message.Event.Kind != "open" && message.Event.Kind != "documentchange" && message.Event.Ref != "refresh" {
		return peb.EditorReply{}, nil
	}
	var preview bytes.Buffer
	if err := goldmark.Convert([]byte(message.Content), &preview); err != nil {
		return peb.EditorReply{}, err
	}
	// Content is escaped for the textarea's raw-text context. Goldmark's
	// default renderer omits raw HTML from the Markdown preview.
	return peb.EditorReply{View: &peb.EditorView{
		HTML:   `<div class="markdown-editor"><header><strong>Markdown</strong><button type="button" data-ref="refresh">刷新预览</button></header><div class="markdown-columns"><textarea data-ref="source" aria-label="Markdown 源码" spellcheck="false">` + html.EscapeString(message.Content) + `</textarea><article aria-label="Markdown 预览">` + preview.String() + `</article></div></div>`,
		CSS:    editorCSS,
		Events: []peb.EditorBinding{{Ref: "source", Kind: "input"}, {Ref: "refresh", Kind: "click"}},
	}}, nil
}

func main() {
	err := peb.ServeEditors(os.Stdin, os.Stdout, peb.Contributions{Editors: []peb.Editor{
		{ID: "markdown", Label: "Markdown 编辑与预览", Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".md"}, {Match: peb.FileSuffix, Pattern: ".markdown"}}},
	}}, markdownEditor)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
