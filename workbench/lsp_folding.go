package workbench

import (
	"encoding/json"
	"time"

	"github.com/pencode-labs/pencode/lsp"
)

const foldingMethod = "textDocument/foldingRange"

func (m *workspaceModel) scheduleLanguageFolds() {
	if !m.textEditorActive() {
		return
	}
	doc := m.languages.documents[m.activeFile()]
	if doc == nil || doc.file.page.Paged {
		return
	}
	session := m.languageProvider(doc, foldingMethod)
	if session != doc.foldSession {
		doc.resetFoldProvider()
		doc.foldSession = session
	}
	if session == nil || doc.foldVersion == doc.version || m.highlightPending && time.Now().Before(m.highlightDue) {
		return
	}
	params := struct {
		Document lsp.TextDocumentIdentifier `json:"textDocument"`
	}{lsp.TextDocumentIdentifier{URI: doc.uri}}
	job := pencodeLanguageJob{method: foldingMethod, document: doc, content: doc.content, version: doc.version, uri: doc.uri, params: params, folding: true}
	select {
	case session.jobs <- job:
		doc.foldVersion, doc.foldPending = doc.version, true
	default:
	}
}

func (doc *pencodeLanguageDocument) resetFoldProvider() {
	if doc.foldSession != nil && doc.file.folds != nil {
		doc.file.folds.ready = false
	}
	doc.foldVersion, doc.foldSession, doc.foldPending = 0, nil, false
}

func (m *workspaceModel) acceptLanguageFolds(event pencodeLanguageEvent) {
	job := event.job
	doc := job.document
	if doc == nil || !doc.seen || m.languages.documents[doc.file] != doc || doc.foldSession != event.session ||
		doc.version != job.version || doc.file.snapshot() != job.content {
		return
	}
	doc.foldPending = false
	var ranges []editorFoldRange
	if event.err != nil || json.Unmarshal(event.result, &ranges) != nil || ranges == nil {
		return
	}
	buffer := doc.file.snapshotBuffer()
	doc.file.foldState().setRanges(normalizeFoldRanges(ranges, buffer.Lines()), buffer)
}
