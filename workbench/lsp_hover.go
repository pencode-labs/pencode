package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/diiyw/onix/syntax"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/yuin/goldmark/v2/parser"
	goldmarkhtml "github.com/yuin/goldmark/v2/renderer/html"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var hoverMarkdownParser = parser.New()
var hoverMarkdownRenderer = goldmarkhtml.New()

type pencodeHoverJob struct {
	result          json.RawMessage
	body, path      string
	theme           *syntax.Theme
	definitions     *syntax.Definitions
	revision, token uint64
}
type pencodeHoverResult struct {
	job   pencodeHoverJob
	nodes []htmlui.VNode
}

func (model *workspaceModel) pollHoverMarkup() bool {
	if model.hoverWorker == nil {
		return false
	}
	changed := false
	model.hoverWorker.poll(func(result *pencodeHoverResult) {
		h := &model.languageUI.hover
		if result.job.revision != model.hoverRevision || result.job.token != h.token || !bytes.Equal(result.job.result, h.result) || result.job.theme != model.syntaxTheme || result.job.definitions != model.syntaxDefinitions {
			return
		}
		model.hoverApplied = result.job.revision
		h.nodes = result.nodes
		h.positioned = false
		changed = true
	})
	return changed
}
func (model *workspaceModel) highlightLanguageHover() {
	model.pollHoverMarkup()
	h := &model.languageUI.hover
	model.ensureSyntaxTheme()
	if h.theme == model.syntaxTheme && h.definitions == model.syntaxDefinitions {
		return
	}
	if model.hoverWorker == nil {
		ctx, cancel := context.WithCancel(context.Background())
		model.hoverCancel = cancel
		document := model.backend.NewSyntax(ctx)
		model.hoverWorker = newLatestWorker(func(job pencodeHoverJob, result *pencodeHoverResult) bool {
			worker := workspaceModel{hoverDocument: document, syntaxTheme: job.theme, syntaxDefinitions: job.definitions}
			worker.languageUI.hover = pencodeLanguageHover{result: job.result, body: job.body, file: &openFile{Path: job.path}}
			worker.buildLanguageHover()
			result.job = job
			result.nodes = worker.languageUI.hover.nodes
			return true
		}, func(result *pencodeHoverResult) { *result = pencodeHoverResult{} }, document.Close, model.backgroundWake())
	}
	h.theme, h.definitions = model.syntaxTheme, model.syntaxDefinitions
	h.nodes = []htmlui.VNode{htmlui.TextVNode(1, "pre", h.body)}
	h.positioned = false
	model.hoverRevision++
	job := pencodeHoverJob{result: h.result, body: h.body, theme: model.syntaxTheme, definitions: model.syntaxDefinitions, revision: model.hoverRevision, token: h.token}
	if h.file != nil {
		job.path = h.file.Path
	}
	model.hoverWorker.submit(job)
}
func (model *workspaceModel) closeHoverWorker() {
	if model.hoverWorker != nil {
		model.hoverCancel()
		model.hoverWorker.close()
		model.hoverWorker = nil
	}
}

// Hover markup and syntax styles are rebuilt only for a new response or theme.
func (model *workspaceModel) buildLanguageHover() {
	h := &model.languageUI.hover
	model.ensureSyntaxTheme()
	if h.theme == model.syntaxTheme && h.definitions == model.syntaxDefinitions {
		return
	}
	h.theme, h.definitions = model.syntaxTheme, model.syntaxDefinitions
	var value any
	if json.Unmarshal(h.result, &value) != nil {
		h.nodes = []htmlui.VNode{htmlui.TextVNode(1, "pre", h.body)}
		return
	}
	h.nodes = h.nodes[:0]
	model.appendLanguageHover(value)
}

func (model *workspaceModel) appendLanguageHover(value any) {
	h := &model.languageUI.hover
	appendNode := func(node htmlui.VNode) {
		node.Key = uint64(len(h.nodes) + 1)
		h.nodes = append(h.nodes, node)
	}
	switch value := value.(type) {
	case []any:
		for _, item := range value {
			model.appendLanguageHover(item)
		}
	case map[string]any:
		if contents, ok := value["contents"]; ok {
			model.appendLanguageHover(contents)
			return
		}
		text, _ := value["value"].(string)
		if language, ok := value["language"].(string); ok {
			appendNode(htmlui.VNode{Tag: "pre", Children: []htmlui.VNode{model.languageHoverCode(language, text)}})
			return
		}
		if value["kind"] == "plaintext" {
			appendNode(htmlui.TextVNode(0, "pre", text))
			return
		}
		model.appendLanguageHover(text)
	case string:
		var markup bytes.Buffer
		document := hoverMarkdownParser.ParseStringSource(value)
		if err := hoverMarkdownRenderer.RenderStringSource(&markup, value, document); err != nil {
			appendNode(htmlui.TextVNode(0, "pre", value))
			return
		}
		// Goldmark escapes code and omits raw HTML by default. Building VNodes
		// from its HTML also preserves Markdown paragraphs, links and lists.
		nodes, err := html.ParseFragment(&markup, &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
		if err != nil {
			appendNode(htmlui.TextVNode(0, "pre", value))
			return
		}
		for _, node := range nodes {
			if child, ok := model.languageHoverNode(node); ok {
				appendNode(child)
			}
		}
	}
}

func (model *workspaceModel) languageHoverNode(node *html.Node) (htmlui.VNode, bool) {
	if node.Type == html.TextNode {
		if strings.TrimSpace(node.Data) == "" && (node.Parent == nil || node.Parent.Data == "div" || node.Parent.Data == "ul" || node.Parent.Data == "ol") {
			return htmlui.VNode{}, false
		}
		return htmlui.TextVNode(0, "span", node.Data), true
	}
	if node.Type != html.ElementNode {
		return htmlui.VNode{}, false
	}
	if node.Data == "code" && node.Parent != nil && node.Parent.Data == "pre" {
		language := ""
		for _, attribute := range node.Attr {
			if attribute.Key == "class" {
				language = strings.TrimPrefix(attribute.Val, "language-")
			}
		}
		code := ""
		if node.FirstChild != nil {
			code = node.FirstChild.Data
		}
		return model.languageHoverCode(language, code), true
	}
	result := htmlui.VNode{Tag: node.Data}
	for _, attribute := range node.Attr {
		result.Attributes = append(result.Attributes, htmlui.VNodeAttribute{Name: attribute.Key, Value: attribute.Val})
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if next, ok := model.languageHoverNode(child); ok {
			next.Key = uint64(len(result.Children) + 1)
			result.Children = append(result.Children, next)
		}
	}
	return result, true
}

func (model *workspaceModel) languageHoverCode(language, code string) htmlui.VNode {
	if language == "" && model.languageUI.hover.file != nil {
		language = model.languageUI.hover.file.Path
	}
	document := model.hoverDocument
	result := htmlui.TextVNode(1, "code", code)
	if err := document.Update(language, code); err != nil || document.Language() == "" {
		return result
	}
	result.Text = ""
	result.Attributes = []htmlui.VNodeAttribute{{Name: "style", Value: model.syntaxTheme.Resolve("source." + document.Language()).CSS()}}
	appendText := func(text, style string) {
		if text == "" {
			return
		}
		node := htmlui.TextVNode(uint64(len(result.Children)+1), "span", text)
		if style != "" {
			node.Attributes = []htmlui.VNodeAttribute{{Name: "style", Value: style}}
		}
		result.Children = append(result.Children, node)
	}
	offset := 0
	for _, token := range document.Tokens(model.syntaxTheme, model.syntaxDefinitions) {
		appendText(code[offset:token.Start], "")
		appendText(code[token.Start:token.End], token.CSS)
		offset = token.End
	}
	appendText(code[offset:], "")
	return result
}
