package workbench

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/diiyw/onix/html/css"
	"github.com/diiyw/onix/html/dom"
	"github.com/diiyw/onix/html/style"
	"github.com/diiyw/onix/syntax"
	"github.com/pencode-labs/pencode/peb"
)

func TestMarkdownPreviewStylesFollowWorkbenchTheme(t *testing.T) {
	workbenchCSS, err := os.ReadFile("../resources/workbench/assets/ui/pencode.css")
	if err != nil {
		t.Fatal(err)
	}
	previewCSS, err := os.ReadFile("../extensions/markdown-preview/resources/editor.css")
	if err != nil {
		t.Fatal(err)
	}
	model := &workspaceModel{}
	document := &pencodeCustomDocument{model: model}
	view := &peb.EditorView{HTML: `<article class="markdown-preview"><a data-ref="link" href="https://example.com">Link</a><table data-ref="table"><thead><tr><th data-ref="header">Title</th></tr></thead><tbody><tr><td data-ref="cell">Body</td></tr></tbody></table></article>`, CSS: string(previewCSS)}
	for _, id := range []string{"dark-plus", "light-plus", "monokai"} {
		t.Run(id, func(t *testing.T) {
			theme, err := syntax.BuiltinTheme(id)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := preparePencodeTheme(id, theme, string(workbenchCSS))
			if err != nil {
				t.Fatal(err)
			}
			model.workbenchTheme = &prepared
			if document.view == nil {
				err = document.setView(view)
			} else {
				err = document.syncTheme()
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(document.definition.CSS, "var(") {
				t.Fatal("unresolved preview theme colors", document.definition.CSS)
			}
			root, err := dom.Parse(view.HTML)
			if err != nil {
				t.Fatal(err)
			}
			// Mounting a scoped component adds this class to every element.
			scope := regexp.MustCompile(`onix-scope-[0-9a-f]+`).FindString(document.definition.CSS)
			if scope == "" {
				t.Fatal("preview stylesheet is not scoped")
			}
			var addScope func(*dom.Node)
			addScope = func(node *dom.Node) {
				if node.Type == dom.ElementNode {
					if node.Attributes == nil {
						node.Attributes = make(map[string]string)
					}
					node.Attributes["class"] += " " + scope
				}
				for _, child := range node.Children {
					addScope(child)
				}
			}
			addScope(root)
			sheet, err := css.Parse(document.definition.CSS)
			if err != nil {
				t.Fatal(err)
			}
			computed, err := style.ComputeWithViewport(root, sheet, style.Viewport{Width: 800, Height: 600})
			if err != nil {
				t.Fatal(err)
			}
			for node, value := range computed {
				ref, _ := node.Attr("data-ref")
				switch ref {
				case "link":
					linkColor := style.ColorValue(style.Color(theme.Color("textLink.foreground")))
					if value.Color != linkColor || value.FontWeight.Number != 600 || value.BorderWidth.Bottom != style.LengthPx(1) || value.BorderStyle.Bottom != style.Keyword("solid") || value.BorderColor.Bottom != linkColor {
						t.Fatalf("link has no visible click styling: color=%v weight=%v underline=%v", value.Color, value.FontWeight, value.BorderColor.Bottom)
					}
				case "table", "header", "cell":
					widths, borders, colors := [2]style.Value{value.BorderWidth.Right, value.BorderWidth.Bottom}, [2]style.Value{value.BorderStyle.Right, value.BorderStyle.Bottom}, [2]style.Value{value.BorderColor.Right, value.BorderColor.Bottom}
					if ref == "table" {
						widths, borders, colors = [2]style.Value{value.BorderWidth.Top, value.BorderWidth.Left}, [2]style.Value{value.BorderStyle.Top, value.BorderStyle.Left}, [2]style.Value{value.BorderColor.Top, value.BorderColor.Left}
					}
					for i := range widths {
						if widths[i] != style.LengthPx(1) || borders[i] != style.Keyword("solid") || colors[i] != style.ColorValue(style.Color(theme.Color("editorWidget.border"))) {
							t.Fatalf("%s border %d is not visible: %v %v %v", ref, i, widths[i], borders[i], colors[i])
						}
					}
				}
			}
		})
	}
}
