package lsp

import (
	"context"
	"fmt"
	"unicode/utf8"
)

// SyncDocument sends didOpen once, then full or incremental changes according
// to the negotiated sync kind. Calls are ordered by the session worker.
func (c *Client) SyncDocument(ctx context.Context, uri URI, language, text string, version int) error {
	if !utf8.ValidString(text) {
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
			params := map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": language, "version": version, "text": text}}
			if err := c.Notify(ctx, "textDocument/didOpen", params); err != nil {
				return err
			}
		}
	} else if c.syncKind != 0 {
		change := map[string]any{"text": text}
		if c.syncKind == 2 {
			r, replacement := IncrementalChange(old.text, text, c.Encoding)
			change = map[string]any{"range": r, "text": replacement}
		}
		if err := c.Notify(ctx, "textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": version}, "contentChanges": []any{change}}); err != nil {
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
		if err := c.Notify(ctx, "textDocument/didClose", map[string]any{"textDocument": TextDocumentIdentifier{uri}}); err != nil {
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
	return c.Notify(ctx, "textDocument/willSave", map[string]any{"textDocument": TextDocumentIdentifier{uri}, "reason": 1})
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

func (c *Client) DidSaveText(ctx context.Context, uri URI, text string) error {
	if !c.save {
		return nil
	}
	params := map[string]any{"textDocument": TextDocumentIdentifier{uri}}
	if c.saveText {
		params["text"] = text
	}
	return c.Notify(ctx, "textDocument/didSave", params)
}

func (c *Client) DocumentVersion(uri URI) (int, bool) {
	c.documentsMu.Lock()
	defer c.documentsMu.Unlock()
	doc, exists := c.documents[uri]
	return doc.version, exists
}
