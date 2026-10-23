package extensions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/diiyw/onix/projects/pencode/client"
	"github.com/diiyw/onix/projects/pencode/lsp"
	"github.com/diiyw/onix/projects/pencode/output"
	"github.com/diiyw/onix/projects/pencode/peb"
)

// LanguageServer is a validated contribution. A workspace activates one process
// per contribution and root, shared by all matching open documents.
type LanguageServer struct {
	Output *output.Channel
	peb.LanguageServer
	remote  *client.Extension
	LocalID string
}

func (s *LanguageServer) Match(name string) bool {
	for _, rule := range s.Selectors {
		if rule.Match == peb.FileName && name == rule.Pattern || rule.Match == peb.FileSuffix && strings.HasSuffix(name, rule.Pattern) {
			return true
		}
	}
	return false
}
func (s *LanguageServer) Start(ctx context.Context, root string, handler lsp.Handler) (*lsp.Client, error) {
	options := lsp.Options{
		Root:                  root,
		InitializationOptions: json.RawMessage(s.InitializationOptions), Configuration: json.RawMessage(s.Configuration), Handler: handler,
		Capabilities: map[string]any{
			"workspace": map[string]any{
				"applyEdit": true, "workspaceEdit": map[string]any{"documentChanges": true, "failureHandling": "textOnlyTransactional"},
				"semanticTokens": map[string]bool{"refreshSupport": true},
			},
			"window": map[string]any{
				"showMessage":  map[string]any{"messageActionItem": map[string]bool{"additionalPropertiesSupport": true}},
				"showDocument": map[string]bool{"support": true},
			},
		},
	}
	stream, err := s.remote.OpenLanguageServer(ctx, root, s.LocalID)
	if err != nil {
		return nil, err
	}
	logs := io.Writer(io.Discard)
	if s.Output != nil {
		logs = s.Output
	}
	go func() { _, _ = io.Copy(logs, stream.Stderr()) }()
	return lsp.Connect(ctx, options, stream)
}
func loadLanguageServers(id string, servers []peb.LanguageServer) ([]LanguageServer, error) {
	result := make([]LanguageServer, 0, len(servers))
	ids := make(map[string]bool, len(servers))
	for _, s := range servers {
		if !validID(s.ID) || ids[s.ID] || s.Label == "" || s.LanguageID == "" || strings.ContainsAny(s.LanguageID, "\x00\r\n") || s.Command == "" || strings.ContainsRune(s.Command, 0) || len(s.Selectors) == 0 {
			return nil, fmt.Errorf("invalid language server %q", s.ID)
		}
		ids[s.ID] = true
		for _, arg := range s.Args {
			if strings.ContainsRune(arg, 0) {
				return nil, fmt.Errorf("invalid server argument")
			}
		}
		for _, rule := range s.Selectors {
			if rule.Match != peb.FileName && rule.Match != peb.FileSuffix || !validRule(peb.IconRule{Match: rule.Match, Pattern: rule.Pattern}) {
				return nil, fmt.Errorf("invalid language selector %q", rule.Pattern)
			}
		}
		for _, options := range []string{s.InitializationOptions, s.Configuration} {
			if options != "" && !json.Valid([]byte(options)) {
				return nil, fmt.Errorf("invalid language server JSON options")
			}
		}
		localID := s.ID
		s.ID = "extensions/" + id + "/" + s.ID
		result = append(result, LanguageServer{LanguageServer: s, LocalID: localID})
	}
	return result, nil
}
