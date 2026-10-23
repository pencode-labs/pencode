package client

import (
	"context"

	"github.com/diiyw/onix/projects/pencode/protocol"
)

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
