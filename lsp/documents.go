package lsp

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/diiyw/onix/text/piecetree"
)

// SyncDocument sends didOpen once, then full or incremental changes according
// to the negotiated sync kind. Calls are ordered by the session worker.
func (c *Client) SyncDocument[V piecetree.Value](ctx context.Context, uri URI, language string, value V, version int) error {
	text := piecetree.Text(value)
	if !validDocumentText(text) {
		return fmt.Errorf("lsp: document is not valid UTF-8")
	}
	c.documentsMu.Lock()
	defer c.documentsMu.Unlock()
	old, exists := c.documents[uri]
	if exists && version < old.version {
		return fmt.Errorf("lsp: document version moved backwards")
	}
	if exists && version == old.version && text == old.text {
		return nil
	}
	if exists && version <= old.version {
		return fmt.Errorf("lsp: changed document needs a newer version")
	}
	if !exists {
		if c.openClose {
			if err := c.notifyDocumentText(ctx, "textDocument/didOpen", uri, language, version, text, 0, text.Len(), nil); err != nil {
				return err
			}
		}
	} else if c.syncKind != 0 {
		start, end := 0, text.Len()
		var changed *Range
		var r Range
		if c.syncKind == 2 {
			prefix, a, b := old.text.ChangedRange(text)
			start, end = prefix, b
			r.Start, _ = PositionAt(old.text, prefix, c.Encoding)
			r.End, _ = PositionAt(old.text, a, c.Encoding)
			changed = &r
		}
		if err := c.notifyDocumentText(ctx, "textDocument/didChange", uri, "", version, text, start, end, changed); err != nil {
			return err
		}
	}

	c.documents[uri] = document{text, language, version}
	return nil
}
func (c *Client) CloseDocument(ctx context.Context, uri URI) error {
	c.documentsMu.Lock()
	defer c.documentsMu.Unlock()
	if _, exists := c.documents[uri]; !exists {
		return nil
	}
	if c.openClose {
		params := struct {
			TextDocument TextDocumentIdentifier `json:"textDocument"`
		}{TextDocumentIdentifier{uri}}
		if err := c.notify(ctx, "textDocument/didClose", params); err != nil {
			return err
		}
	}
	delete(c.documents, uri)
	return nil
}
func (c *Client) WillSave(ctx context.Context, uri URI) error {
	if !c.willSave {
		return nil
	}
	return c.notify(ctx, "textDocument/willSave", struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
		Reason       int                    `json:"reason"`
	}{TextDocumentIdentifier{uri}, 1})
}
func (c *Client) DidSave(ctx context.Context, uri URI) error {
	if !c.save {
		return nil
	}
	c.documentsMu.Lock()
	doc, exists := c.documents[uri]
	c.documentsMu.Unlock()
	if !exists {
		return fmt.Errorf("lsp: cannot save unopened document")
	}
	return c.DidSaveText(ctx, uri, doc.text)
}

func (c *Client) DidSaveText[V piecetree.Value](ctx context.Context, uri URI, value V) error {
	if !c.save {
		return nil
	}
	if c.saveText {
		text := piecetree.Text(value)
		return c.notifyDocumentText(ctx, "textDocument/didSave", uri, "", 0, text, 0, text.Len(), nil)
	}
	return c.notify(ctx, "textDocument/didSave", struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
	}{TextDocumentIdentifier{uri}})
}

func (c *Client) DocumentVersion(uri URI) (int, bool) {
	c.documentsMu.Lock()
	defer c.documentsMu.Unlock()
	doc, exists := c.documents[uri]
	return doc.version, exists
}

func validDocumentText(text piecetree.Snapshot) bool {
	for part := range text.Chunks(0, text.Len()) {
		if !utf8.ValidString(part) {
			return false
		}
	}
	return true
}
