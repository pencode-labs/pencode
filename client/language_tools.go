package client

import (
	"context"

	"github.com/pencode-labs/pencode/internal/languagetool"
	"github.com/pencode-labs/pencode/protocol"
)

// LanguageTools detects tools on this host after applying the optional action.
// Paths are user-selected executable overrides, keyed by catalog tool ID.
func (c *Client) LanguageTools(ctx context.Context, action, id string, paths map[string]string) (map[string]languagetool.Installation, error) {
	e := service(protocol.LanguageTools, "")
	e.String(action)
	e.String(id)
	e.U32(uint32(len(paths)))
	for tool, path := range paths {
		e.String(tool)
		e.String(path)
	}
	d, err := c.execute(ctx, &e)
	if err != nil {
		return nil, err
	}
	if int(d.U32()) != len(languagetool.Tools) {
		return nil, protocol.ErrProtocol
	}
	items := make(map[string]languagetool.Installation, len(languagetool.Tools))
	for range languagetool.Tools {
		id := d.String()
		items[id] = languagetool.Installation{Command: d.String(), Entry: d.String(), Problem: d.String(), Managed: d.U32() != 0}
	}
	return items, d.Done()
}
