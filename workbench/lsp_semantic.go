package workbench

import (
	"encoding/json"
	"time"

	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/projects/pencode/lsp"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
)

type pencodeSemanticTokens struct {
	session            *pencodeLanguageSession
	requested, version int
	resultID           string
	data               []uint32
	tokens             []lsp.SemanticToken
	spans, merged      []scene.TextSpan2D
	theme              *syntax.Theme
	baseSource         string
	baseRevision       uint64
}
type pencodeSemanticProvider struct {
	Legend struct {
		TokenTypes     []string `json:"tokenTypes"`
		TokenModifiers []string `json:"tokenModifiers"`
	} `json:"legend"`
	Full  json.RawMessage `json:"full"`
	Range bool            `json:"range"`
}

func languageSymbolAction(doc *pencodeLanguageDocument, offset int) string {
	if doc == nil || doc.file == nil || doc.semantic == nil || doc.semantic.session == nil || doc.semantic.session.client == nil || doc.semantic.version != doc.version || doc.content != doc.file.Content {
		return "textDocument/definition"
	}
	var provider pencodeSemanticProvider
	if json.Unmarshal(doc.semantic.session.client.Capabilities["semanticTokensProvider"], &provider) != nil {
		return "textDocument/definition"
	}
	var declarationModifiers uint32
	for i, modifier := range provider.Legend.TokenModifiers {
		if i >= 32 {
			break
		}
		if modifier == "declaration" || modifier == "definition" {
			declarationModifiers |= 1 << i
		}
	}
	if declarationModifiers == 0 {
		return "textDocument/definition"
	}
	for _, token := range doc.semantic.tokens {
		if token.Start <= offset && offset < token.End {
			if token.Modifiers&declarationModifiers != 0 {
				return "textDocument/references"
			}
			break
		}
	}
	return "textDocument/definition"
}

func languageDefinitionIsClickedSymbol(job pencodeLanguageJob, encoding lsp.PositionEncoding, rows []pencodeLanguageRow) bool {
	for _, row := range rows {
		if row.location == nil || row.location.URI != job.uri {
			continue
		}
		start, err := lsp.OffsetAt(job.content, row.location.Range.Start, encoding)
		if err != nil {
			continue
		}
		end, err := lsp.OffsetAt(job.content, row.location.Range.End, encoding)
		if err == nil && start <= job.offset && job.offset < end {
			return true
		}
	}
	return false
}

func (model *workspaceModel) scheduleLanguageSemantic() {
	state := model.languages
	if state == nil {
		return
	}
	doc := state.documents[model.activeFile()]
	if !model.textEditorActive() {
		doc = nil
	}
	if state.semanticDocument != doc {
		if previous := state.semanticDocument; previous != nil {
			previous.semantic = nil
		}
		state.semanticDocument = doc
	}
	if doc == nil {
		return
	}
	for _, server := range state.servers {
		session := state.sessions[pencodeLanguageKey{doc.root, server.ID}]
		if session == nil || session.client == nil || session.failed || !session.client.Capabilities.Has("semanticTokensProvider") || doc.servers[session].version != doc.version {
			continue
		}
		if doc.semantic != nil && doc.semantic.session == session && doc.semantic.requested == doc.version {
			return
		}
		var provider pencodeSemanticProvider
		if json.Unmarshal(session.client.Capabilities["semanticTokensProvider"], &provider) != nil || len(provider.Legend.TokenTypes) == 0 {
			return
		}
		if doc.semantic == nil || doc.semantic.session != session {
			doc.semantic = &pencodeSemanticTokens{session: session}
		}
		semantic := doc.semantic
		if semantic.requested == doc.version {
			return
		}
		if model.highlightPending && time.Now().Before(model.highlightDue) {
			if model.app != nil && model.app.WakeAt != nil {
				model.app.WakeAt(model.highlightDue)
			}
			return
		}
		method := "textDocument/semanticTokens/full"
		params := map[string]any{"textDocument": lsp.TextDocumentIdentifier{URI: doc.uri}}
		if len(provider.Full) == 0 || string(provider.Full) == "false" {
			if !provider.Range {
				return
			}
			method = "textDocument/semanticTokens/range"
			end, _ := lsp.PositionAt(doc.content, len(doc.content), session.client.Encoding)
			params["range"] = lsp.Range{End: end}
		} else if semantic.resultID != "" {
			var full struct {
				Delta bool `json:"delta"`
			}
			_ = json.Unmarshal(provider.Full, &full)
			if full.Delta {
				method += "/delta"
				params["previousResultId"] = semantic.resultID
			}
		}
		job := pencodeLanguageJob{document: doc, uri: doc.uri, content: doc.content, version: doc.version, method: method, params: params, semantic: true}
		select {
		case session.jobs <- job:
			semantic.requested = doc.version
		default:
		}
		return
	}
}
func (model *workspaceModel) acceptLanguageSemantic(event pencodeLanguageEvent) {
	job := event.job
	doc := job.document
	if doc == nil || doc.semantic == nil || doc.semantic.session != event.session || doc.version != job.version || doc.file.Content != job.content || !doc.seen || event.err != nil {
		return
	}
	var result struct {
		lsp.SemanticTokens
		Edits []lsp.SemanticTokensEdit `json:"edits"`
	}
	if json.Unmarshal(event.result, &result) != nil {
		return
	}
	semantic := doc.semantic
	data := result.Data
	if result.Edits != nil {
		var err error
		data, err = lsp.ApplySemanticDelta(semantic.data, result.Edits)
		if err != nil {
			return
		}
	}
	if len(data) > 5*pencodeHighlightCacheSpanLimit {
		return
	}
	var provider pencodeSemanticProvider
	_ = json.Unmarshal(event.session.client.Capabilities["semanticTokensProvider"], &provider)
	tokens, err := lsp.DecodeSemanticTokens(semantic.tokens, data, doc.content, event.session.client.Encoding, len(provider.Legend.TokenTypes))
	if err != nil {
		return
	}
	semantic.tokens = tokens
	semantic.data = data
	semantic.resultID = result.ResultID
	semantic.version = doc.version
	model.semanticPaintRevision++
	semantic.theme = nil
	semantic.baseSource = ""
}
func semanticScope(name string) string {
	switch name {
	case "namespace":
		return "entity.name.namespace"
	case "type", "class", "enum", "interface", "struct", "typeParameter":
		return "entity.name.type"
	case "parameter":
		return "variable.parameter"
	case "variable":
		return "variable.other"
	case "property":
		return "variable.other.property"
	case "enumMember":
		return "constant.other.enum"
	case "function", "method":
		return "entity.name.function"
	case "macro":
		return "entity.name.function.preprocessor"
	case "keyword", "modifier":
		return "keyword"
	case "comment":
		return "comment"
	case "string":
		return "string"
	case "number":
		return "constant.numeric"
	case "regexp":
		return "string.regexp"
	case "operator":
		return "keyword.operator"
	case "decorator":
		return "entity.name.function.decorator"
	}
	return "variable.other"
}
func (model *workspaceModel) languageHighlight(base []scene.TextSpan2D) []scene.TextSpan2D {
	state := model.languages
	if state == nil {
		return base
	}
	doc := state.documents[model.activeFile()]
	if doc == nil || doc.semantic == nil || doc.content != doc.file.Content || doc.semantic.version != doc.version {
		return base
	}
	semantic := doc.semantic
	// Parsing replaces temporary edit colors without changing the source text.
	if semantic.theme == model.syntaxTheme && semantic.baseSource == doc.content && semantic.baseRevision == model.highlightPaintRevision {
		return semantic.merged
	}
	if semantic.theme != model.syntaxTheme {
		var provider pencodeSemanticProvider
		_ = json.Unmarshal(semantic.session.client.Capabilities["semanticTokensProvider"], &provider)
		colors := make([]graphics.ColorRGBA, len(provider.Legend.TokenTypes))
		for i, name := range provider.Legend.TokenTypes {
			colors[i] = pencodeSyntaxColor(model.syntaxTheme.Resolve(semanticScope(name)).Foreground)
		}
		semantic.spans = semantic.spans[:0]
		for _, token := range semantic.tokens {
			semantic.spans = appendPencodeSpan(semantic.spans, scene.TextSpan2D{Start: token.Start, End: token.End, Color: colors[token.Type]})
		}
	}
	semantic.merged = overlayLanguageSpans(semantic.merged[:0], base, semantic.spans)
	semantic.theme = model.syntaxTheme
	semantic.baseSource = doc.content
	semantic.baseRevision = model.highlightPaintRevision
	return semantic.merged
}

// Both lists are sorted and disjoint. A single sweep splits syntax spans only
// where semantic spans override them, retaining punctuation and comments.
func overlayLanguageSpans(dst, base, overlay []scene.TextSpan2D) []scene.TextSpan2D {
	i, cursor := 0, 0
	for _, semantic := range overlay {
		for i < len(base) && base[i].End <= semantic.Start {
			span := base[i]
			span.Start = max(span.Start, cursor)
			if span.Start < span.End {
				dst = appendPencodeSpan(dst, span)
			}
			cursor = max(cursor, span.End)
			i++
		}
		if i < len(base) && base[i].Start < semantic.Start {
			prefix := base[i]
			prefix.Start = max(prefix.Start, cursor)
			prefix.End = semantic.Start
			if prefix.Start < prefix.End {
				dst = appendPencodeSpan(dst, prefix)
			}
		}
		dst = appendPencodeSpan(dst, semantic)
		cursor = semantic.End
		for i < len(base) && base[i].End <= cursor {
			i++
		}
	}
	for i < len(base) {
		span := base[i]
		span.Start = max(span.Start, cursor)
		if span.Start < span.End {
			dst = appendPencodeSpan(dst, span)
		}
		cursor = max(cursor, span.End)
		i++
	}
	return dst
}
