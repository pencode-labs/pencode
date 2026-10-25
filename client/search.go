package client

import (
	"context"
	"github.com/pencode-labs/pencode/protocol"
	"github.com/pencode-labs/pencode/textsearch"
)

func (c *Client) SearchText(ctx context.Context, root, directory, query string, options textsearch.Options, documents []protocol.SearchDocument) (protocol.SearchResult, error) {
	e := service(protocol.TextSearch, root)
	e.String(directory)
	e.String(query)
	e.U32(protocol.SearchFlags(options))
	e.U32(uint32(len(documents)))
	for _, doc := range documents {
		e.String(doc.Path)
		e.String(doc.Content)
	}
	d, err := c.execute(ctx, &e)
	if err != nil {
		return protocol.SearchResult{}, err
	}
	r := protocol.SearchResult{Truncated: d.U32() != 0, Skipped: int(d.U32())}
	n := d.U32()
	if n > protocol.TextSearchFilesLimit {
		return r, protocol.ErrProtocol
	}
	r.Files = make([]protocol.SearchFile, n)
	total := 0
	for i := range r.Files {
		f := &r.Files[i]
		f.Path, f.Digest = d.String(), d.String()
		f.Count = int(d.U32())
		count := int(d.U32())
		total += count
		if total > protocol.TextSearchLimit {
			return r, protocol.ErrProtocol
		}
		f.Matches = make([]protocol.TextMatch, count)
		for j := range f.Matches {
			m := &f.Matches[j]
			m.Start, m.End, m.Line, m.Column = int(d.U32()), int(d.U32()), int(d.U32()), int(d.U32())
			m.Preview = d.String()
		}
	}
	return r, d.Done()
}
