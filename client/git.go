package client

import (
	"context"

	"github.com/pencode-labs/pencode/protocol"
)

func (c *Client) GitRepositories(ctx context.Context, root string) ([]string, error) {
	e := service(protocol.GitRepositories, root)
	d, err := c.execute(ctx, &e)
	if err != nil {
		return nil, err
	}
	count := d.U32()
	if count > 4096 || int(count) > len(d.Buffer)/4 {
		return nil, protocol.ErrProtocol
	}
	roots := make([]string, 0, count)
	for range count {
		roots = append(roots, d.String())
	}
	return roots, d.Done()
}

func (c *Client) Git(ctx context.Context, root string, args ...string) ([]byte, error) {
	e := service(protocol.GitCommand, root)
	e.U32(uint32(len(args)))
	for _, v := range args {
		e.String(v)
	}
	d, err := c.execute(ctx, &e)
	if err != nil {
		return nil, err
	}
	data := d.Bytes()
	if len(data) == 0 {
		data = nil
	}
	return data, d.Done()
}

// CheckGitIgnore uses Git's index and exclusion rules, including negation and
// global excludes. Paths are sent on stdin to avoid platform argv size limits.
func (c *Client) CheckGitIgnore(ctx context.Context, root string, paths []string, dst []bool) ([]bool, error) {
	if len(paths) == 0 {
		return dst[:0], nil
	}
	if len(paths) > protocol.IndexPageSize {
		return nil, protocol.ErrProtocol
	}
	e := service(protocol.GitCheckIgnore, root)
	e.U32(uint32(len(paths)))
	for _, name := range paths {
		e.String(name)
	}
	d, err := c.execute(ctx, &e)
	if err != nil {
		return nil, err
	}
	if d.U32() != uint32(len(paths)) {
		return nil, protocol.ErrProtocol
	}
	dst = dst[:0]
	for range paths {
		ignored := d.U32()
		if ignored > 1 {
			return nil, protocol.ErrProtocol
		}
		dst = append(dst, ignored != 0)
	}
	return dst, d.Done()
}
