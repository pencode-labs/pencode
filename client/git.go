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
