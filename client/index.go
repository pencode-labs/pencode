package client

import (
	"context"

	"github.com/pencode-labs/pencode/protocol"
)

func (c *Client) OpenIndex(ctx context.Context, root string) (uint64, error) {
	return c.openIndex(ctx, root, 0)
}

// RefreshIndex reuses the previous snapshot and rereads directories reported by
// the native watcher. A missing or invalid snapshot is rebuilt in full.
func (c *Client) RefreshIndex(ctx context.Context, root string, previous uint64) (uint64, error) {
	return c.openIndex(ctx, root, previous)
}

func (c *Client) openIndex(ctx context.Context, root string, previous uint64) (uint64, error) {
	e := service(protocol.IndexOpen, root)
	e.U64(previous)
	d, err := c.execute(ctx, &e)
	if err != nil {
		return 0, err
	}
	id := d.U64()
	return id, d.Done()
}
func (c *Client) IndexPage(ctx context.Context, id uint64, offset int, dst []protocol.IndexEntry) ([]protocol.IndexEntry, bool, string, error) {
	e := service(protocol.IndexNext, "")
	e.U64(id)
	e.U32(uint32(offset))
	d, err := c.execute(ctx, &e)
	if err != nil {
		return dst[:0], false, "", err
	}
	done, message, count := d.U32() != 0, d.String(), d.U32()
	if count > protocol.IndexPageSize {
		return nil, false, "", protocol.ErrProtocol
	}
	dst = dst[:0]
	for range count {
		name, depth, directory := d.String(), d.U32(), d.U32()
		if depth > protocol.IndexDepthLimit || directory > 1 {
			return nil, false, "", protocol.ErrProtocol
		}
		dst = append(dst, protocol.IndexEntry{Path: name, Depth: uint16(depth), Directory: directory != 0})
	}
	return dst, done, message, d.Done()
}
func (c *Client) CloseIndex(ctx context.Context, id uint64) error {
	e := service(protocol.IndexClose, "")
	e.U64(id)
	d, err := c.execute(ctx, &e)
	if err != nil {
		return err
	}
	return d.Done()
}
func (c *Client) SearchPaths(ctx context.Context, id uint64, query string, preferred []string) ([]string, error) {
	e := service(protocol.PathSearch, "")
	e.U64(id)
	e.String(query)
	e.U32(uint32(len(preferred)))
	for _, v := range preferred {
		e.String(v)
	}
	d, err := c.execute(ctx, &e)
	if err != nil {
		return nil, err
	}
	n := d.U32()
	if n > 128 {
		return nil, protocol.ErrProtocol
	}
	paths := make([]string, n)
	for i := range paths {
		paths[i] = d.String()
	}
	return paths, d.Done()
}
