package workbench

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/onix-engine/onix/font"
	"github.com/onix-engine/onix/html/css"
	"github.com/onix-engine/onix/html/dom"
	"github.com/onix-engine/onix/html/layout"
	"github.com/onix-engine/onix/html/style"
	"github.com/onix-engine/onix/syntax"
	"github.com/pencode-labs/pencode/peb"
)

func TestMarkdownPreviewCodeWhitespaceLayout(t *testing.T) {
	previewCSS, err := os.ReadFile("../extensions/markdown-preview/resources/editor.css")
	if err != nil {
		t.Fatal(err)
	}
	root, err := dom.Parse("<article class=\"markdown-preview\"><pre><code><span class=\"tok-keyword\">a</span>  <span>b</span>\n    <span>c</span>\n\n\t<span>d</span></code></pre><p><code><span>e</span>  <span>f</span></code></p></article>")
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := css.Parse(string(previewCSS))
	if err != nil {
		t.Fatal(err)
	}
	computed, err := style.Compute(root, sheet)
	if err != nil {
		t.Fatal(err)
	}
	face, err := font.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = face.Close() })
	box, err := layout.Build(root, computed, face, 800, 600)
	if err != nil {
		t.Fatal(err)
	}
	fragments := make(map[string]layout.TextFragment)
	var collect func(*layout.Box)
	collect = func(box *layout.Box) {
		for _, fragment := range box.TextFragments {
			if strings.Contains("abcdef", fragment.Text) && fragment.Text != "" {
				fragments[fragment.Text] = fragment
			}
		}
		for _, child := range box.Children {
			collect(child)
		}
	}
	collect(box)
	if len(fragments) != 6 {
		t.Fatalf("missing code text: %v", fragments)
	}
	a, b, c, d, e, f := fragments["a"], fragments["b"], fragments["c"], fragments["d"], fragments["e"], fragments["f"]
	if gap := b.Rect.X - a.Rect.X - a.Rect.Width; gap <= 0 || math.Abs(c.Rect.X-a.Rect.X-2*gap) > .01 {
		t.Fatalf("consecutive block spaces collapsed: gap=%g", gap)
	}
	if c.Rect.X <= a.Rect.X || d.Rect.X <= c.Rect.X {
		t.Fatalf("space or tab indentation collapsed: a=%v c=%v d=%v", a.Rect, c.Rect, d.Rect)
	}
	lineHeight := c.Rect.Y - a.Rect.Y
	if lineHeight <= 0 || math.Abs(d.Rect.Y-c.Rect.Y-2*lineHeight) > .01 {
		t.Fatalf("code newlines or blank line collapsed: a=%v c=%v d=%v", a.Rect, c.Rect, d.Rect)
	}
	if gap := f.Rect.X - e.Rect.X - e.Rect.Width; gap <= 0 {
		t.Fatalf("consecutive inline spaces collapsed: gap=%g", gap)
	}
}

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
	view := &peb.EditorView{HTML: `<article class="markdown-preview"><a data-ref="link" href="https://example.com">Link</a><table data-ref="table"><thead><tr><th data-ref="header">Title</th></tr></thead><tbody><tr><td data-ref="cell">Body</td></tr></tbody></table><pre><code><span data-ref="syntax-keyword" class="tok-keyword">return</span> <span data-ref="syntax-string" class="tok-string">value</span> <span data-ref="syntax-number" class="tok-number">42</span><span data-ref="syntax-comment" class="tok-comment">comment</span><span data-ref="syntax-type" class="tok-type">Type</span><span data-ref="syntax-function" class="tok-function">fn</span><span data-ref="syntax-operator" class="tok-operator">+</span></code></pre><div class="mermaid"><div class="diagram" style="width:200px;height:100px"><div class="diagram-node" data-ref="diagram-node" style="left:10px;top:10px;width:80px;height:42px"></div><div class="diagram-node diagram-decision" data-ref="diagram-decision" style="left:110px;top:10px;width:80px;height:42px"></div><span class="diagram-label diagram-edge-label" data-ref="diagram-edge" style="left:100px;top:70px">是</span><span class="diagram-label" data-ref="diagram-label" style="left:100px;top:50px;transform:translateX(-50%);">中文标签</span></div></div></article>`, CSS: string(previewCSS)}
	headingScales := [...]float64{2, 1.75, 1.5, 1.25, 1.125, 1}
	for i := range headingScales {
		view.HTML = strings.Replace(view.HTML, "</article>", fmt.Sprintf(`<h%d data-ref="heading-%d">标题 <span data-ref="heading-%d-text">文字</span></h%d></article>`, i+1, i+1, i+1, i+1), 1)
	}
	syntaxScopes := map[string]string{
		"syntax-keyword": "keyword", "syntax-string": "string", "syntax-number": "constant.numeric",
		"syntax-comment": "comment", "syntax-type": "entity.name.type", "syntax-function": "entity.name.function", "syntax-operator": "keyword.operator",
	}
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
					node.SetAttr("class", node.AttrValue("class")+" "+scope)
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
				if strings.HasPrefix(ref, "heading-") {
					level := int(ref[len("heading-")] - '1')
					wantSize := style.LengthPx(16 * headingScales[level])
					if value.FontSize != wantSize || value.FontWeight != style.Number(700) {
						t.Errorf("%s font = %v/%v, want %v/bold", ref, value.FontSize, value.FontWeight, wantSize)
					}
				}
				if scope := syntaxScopes[ref]; scope != "" {
					want := style.ColorValue(style.Color(theme.Resolve(scope).Foreground))
					if value.Color != want {
						t.Fatalf("%s color = %v, want editor syntax color %v", ref, value.Color, want)
					}
				}
				switch ref {
				case "diagram-label":
					want, err := style.ParseColor(fmt.Sprintf("color-mix(in srgb, %s 65%%, %s)", theme.Color("textLink.foreground").Hex(), theme.Color("editor.foreground").Hex()))
					if err != nil || value.Color != style.ColorValue(want) || value.FontWeight.Number != 600 {
						t.Fatalf("diagram label does not follow theme: %v, %v", value.Color, err)
					}
				case "diagram-decision":
					if value.BorderWidth.Top != style.LengthPx(0) || value.BackgroundColor.Color.A != 0 {
						t.Fatal("native decision surface hides the rasterized dashed outline")
					}
				case "diagram-node", "diagram-edge":
					if value.BorderWidth.Top != style.LengthPx(1) || value.BorderStyle.Top != style.Keyword("solid") || value.BorderRadius.TopLeft != (style.Radius{Horizontal: style.LengthPx(16), Vertical: style.LengthPx(16)}) {
						t.Fatalf("%s lost its rounded border: %v %v %v", ref, value.BorderWidth.Top, value.BorderStyle.Top, value.BorderRadius.TopLeft)
					}
					background := style.Color(theme.Color("editor.background"))
					if ref == "diagram-node" {
						var err error
						background, err = style.ParseColor(fmt.Sprintf("color-mix(in srgb, %s 22%%, %s)", theme.Color("button.background").Hex(), theme.Color("editor.background").Hex()))
						if err != nil {
							t.Fatal(err)
						}
					}
					if value.BackgroundColor != style.ColorValue(background) {
						t.Fatalf("%s background does not follow theme: %v, want %v", ref, value.BackgroundColor, background)
					}
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
