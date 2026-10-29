package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
)

type Options struct {
	Root                  string
	InitializationOptions json.RawMessage
	Configuration         json.RawMessage
	// Handler receives server requests and notifications not handled internally.
	// Returning MethodNotFound is required for unsupported requests.
	Handler Handler
	// Capabilities augments the conservative built-in client capabilities.
	Capabilities map[string]any
}

type Client struct {
	*Conn
	Capabilities                        ServerCapabilities
	Encoding                            PositionEncoding
	Root                                URI
	ProcessDone                         <-chan struct{}
	options                             Options
	mu                                  sync.Mutex
	registrations                       map[string]Registration
	documentsMu                         sync.Mutex
	documents                           map[URI]document
	syncKind                            int
	openClose, save, saveText, willSave bool
	closeOnce                           sync.Once
}

type document struct {
	text, language string
	version        int
}

// Connect runs LSP negotiation over an owned remote stdio stream. Process
// creation, PATH lookup, and reaping belong to pencode-server.
func Connect(ctx context.Context, options Options, conn io.ReadWriteCloser) (*Client, error) {
	if len(options.InitializationOptions) > 0 && !json.Valid(options.InitializationOptions) || len(options.Configuration) > 0 && !json.Valid(options.Configuration) {
		_ = conn.Close()
		return nil, fmt.Errorf("lsp: invalid server options")
	}
	done := make(chan struct{})
	c := &Client{Encoding: UTF16, options: options, registrations: make(map[string]Registration), documents: make(map[URI]document), ProcessDone: done}
	c.Root = FileURI(options.Root)
	c.Conn = NewConn(conn, conn, func() { _ = conn.Close() }, c.handle)
	go func() { <-c.Conn.Done(); _ = conn.Close(); close(done) }()
	fail := func(err error) (*Client, error) { c.Conn.Close(); <-done; return nil, err }
	caps := defaultCapabilities()
	mergeCapabilities(caps, options.Capabilities)
	params := map[string]any{
		// The GUI PID is meaningless on another host; pipe lifetime owns the server.
		"processId": nil, "clientInfo": map[string]string{"name": "pencode", "version": "1"},
		"rootUri": c.Root, "capabilities": caps, "workspaceFolders": []WorkspaceFolder{{c.Root, filepath.Base(options.Root)}},
	}
	if len(options.InitializationOptions) > 0 {
		params["initializationOptions"] = options.InitializationOptions
	}
	var reply struct {
		Capabilities ServerCapabilities `json:"capabilities"`
	}
	if err := c.Call(ctx, "initialize", params, &reply); err != nil {
		return fail(err)
	}
	c.Capabilities = reply.Capabilities
	if raw := c.Capabilities["positionEncoding"]; raw != nil {
		if err := json.Unmarshal(raw, &c.Encoding); err != nil {
			return fail(err)
		}
		if c.Encoding != UTF8 && c.Encoding != UTF16 && c.Encoding != UTF32 {
			return fail(fmt.Errorf("lsp: unsupported position encoding %q", c.Encoding))
		}
	}
	if raw := c.Capabilities["textDocumentSync"]; len(raw) > 0 {
		var kind int
		if json.Unmarshal(raw, &kind) == nil {
			c.syncKind, c.openClose = kind, true
		} else {
			var sync struct {
				OpenClose bool            `json:"openClose"`
				Change    int             `json:"change"`
				WillSave  bool            `json:"willSave"`
				Save      json.RawMessage `json:"save"`
			}
			if err := json.Unmarshal(raw, &sync); err != nil {
				return fail(err)
			}
			c.syncKind, c.openClose, c.willSave = sync.Change, sync.OpenClose, sync.WillSave
			if len(sync.Save) > 0 && string(sync.Save) != "false" && string(sync.Save) != "null" {
				c.save = true
				var save struct {
					IncludeText bool `json:"includeText"`
				}
				_ = json.Unmarshal(sync.Save, &save)
				c.saveText = save.IncludeText
			}
		}
	}
	if c.syncKind < 0 || c.syncKind > 2 {
		return fail(fmt.Errorf("lsp: invalid textDocumentSync"))
	}
	if err := c.Notify(ctx, "initialized", struct{}{}); err != nil {
		return fail(err)
	}
	if len(options.Configuration) > 0 {
		if err := c.Notify(ctx, "workspace/didChangeConfiguration", map[string]any{"settings": options.Configuration}); err != nil {
			return fail(err)
		}
	}
	return c, nil
}

func defaultCapabilities() map[string]any {
	return map[string]any{
		"general":   map[string]any{"positionEncodings": []PositionEncoding{UTF16, UTF8, UTF32}},
		"workspace": map[string]any{"configuration": true, "workspaceFolders": true},
		"window":    map[string]any{"workDoneProgress": true},
		"textDocument": map[string]any{
			"semanticTokens":  map[string]any{"requests": map[string]any{"range": true, "full": map[string]bool{"delta": true}}, "tokenTypes": SemanticTokenTypes, "tokenModifiers": SemanticTokenModifiers, "formats": []string{"relative"}, "overlappingTokenSupport": false, "multilineTokenSupport": false},
			"synchronization": map[string]any{"willSave": true, "didSave": true},
			"completion": map[string]any{
				"completionItem":     map[string]any{"snippetSupport": false, "documentationFormat": []string{"plaintext"}, "resolveSupport": map[string]any{"properties": []string{"detail", "documentation", "additionalTextEdits", "command"}}},
				"completionItemKind": map[string]any{"valueSet": []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25}},
			},
			"hover":         map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
			"signatureHelp": map[string]any{"signatureInformation": map[string]any{"documentationFormat": []string{"plaintext"}}},
			"definition":    map[string]any{"linkSupport": true}, "declaration": map[string]any{"linkSupport": true}, "typeDefinition": map[string]any{"linkSupport": true}, "implementation": map[string]any{"linkSupport": true},
			"documentSymbol":     map[string]any{"hierarchicalDocumentSymbolSupport": true},
			"codeAction":         map[string]any{"dataSupport": true, "resolveSupport": map[string]any{"properties": []string{"edit", "command"}}},
			"publishDiagnostics": map[string]any{"versionSupport": true, "relatedInformation": true, "tagSupport": map[string]any{"valueSet": []int{1, 2}}, "dataSupport": true},
			"rename":             map[string]any{"prepareSupport": true},
		},
	}
}
func mergeCapabilities(dst, src map[string]any) {
	for key, value := range src {
		if child, ok := value.(map[string]any); ok {
			if target, ok := dst[key].(map[string]any); ok {
				mergeCapabilities(target, child)
				continue
			}
		}
		dst[key] = value
	}
}

func (c *Client) handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "workspace/configuration":
		var p struct {
			Items []struct {
				Section string `json:"section"`
			} `json:"items"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ResponseError{Code: InvalidParams, Message: err.Error()}
		}
		c.mu.Lock()
		config := c.options.Configuration
		c.mu.Unlock()
		result := make([]json.RawMessage, len(p.Items))
		for i, item := range p.Items {
			result[i] = configurationSection(config, item.Section)
		}
		return result, nil
	case "workspace/workspaceFolders":
		return []WorkspaceFolder{{c.Root, filepath.Base(c.options.Root)}}, nil
	case "client/registerCapability":
		var p struct {
			Registrations []Registration `json:"registrations"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ResponseError{Code: InvalidParams, Message: err.Error()}
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, r := range p.Registrations {
			c.registrations[r.ID] = r
		}
		return nil, nil
	case "client/unregisterCapability":
		var p struct {
			Unregistrations []Registration `json:"unregisterations"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ResponseError{Code: InvalidParams, Message: err.Error()}
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, r := range p.Unregistrations {
			delete(c.registrations, r.ID)
		}
		return nil, nil
	case "window/workDoneProgress/create":
		return nil, nil
	}
	if c.options.Handler != nil {
		return c.options.Handler(ctx, method, params)
	}
	return nil, &ResponseError{Code: MethodNotFound, Message: method}
}
func configurationSection(config json.RawMessage, section string) json.RawMessage {
	if len(config) == 0 {
		return json.RawMessage("null")
	}
	if section == "" {
		return config
	}
	for _, part := range strings.Split(section, ".") {
		var object map[string]json.RawMessage
		if json.Unmarshal(config, &object) != nil {
			return json.RawMessage("null")
		}
		config = object[part]
		if len(config) == 0 {
			return json.RawMessage("null")
		}
	}
	return config
}
func (c *Client) Configure(ctx context.Context, configuration json.RawMessage) error {
	if !json.Valid(configuration) {
		return fmt.Errorf("lsp: invalid configuration")
	}
	c.mu.Lock()
	c.options.Configuration = append(json.RawMessage(nil), configuration...)
	c.mu.Unlock()
	return c.Notify(ctx, "workspace/didChangeConfiguration", map[string]any{"settings": configuration})
}
func (c *Client) Supports(method, capability string) bool {
	if c.Capabilities.Has(capability) {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.registrations {
		if r.Method == method {
			return true
		}
	}
	return false
}

// ProviderOptions includes dynamically registered trigger characters and
// resolve support, using the same provider selection as Supports.
func (c *Client) ProviderOptions(method, capability string) json.RawMessage {
	if c.Capabilities.Has(capability) {
		return c.Capabilities[capability]
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, registration := range c.registrations {
		if registration.Method == method {
			return registration.RegisterOptions
		}
	}
	return nil
}

// Shutdown performs shutdown -> response -> exit, flushes the final notification
// and reaps the process. A deadline also terminates unresponsive servers.
func (c *Client) Shutdown(ctx context.Context) error {
	var err error
	c.closeOnce.Do(func() {
		defer func() { c.Conn.Close(); <-c.ProcessDone }()
		if err = c.Call(ctx, "shutdown", nil, nil); err != nil {
			return
		}
		if err = c.Notify(ctx, "exit", nil); err != nil {
			return
		}
		err = c.Flush(ctx)
		if err != nil {
			return
		}
		select {
		case <-c.ProcessDone:
		case <-ctx.Done():
			err = ctx.Err()
		}
	})
	select {
	case <-c.ProcessDone:
	case <-ctx.Done():
		if err == nil {
			err = ctx.Err()
		}
	}
	return err
}
