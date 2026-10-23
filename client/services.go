package client

import (
	"context"

	"github.com/diiyw/onix/projects/pencode/protocol"
)

func service(op protocol.Operation, root string) protocol.Encoder {
	var e protocol.Encoder
	e.U32(uint32(op))
	e.String(root)
	return e
}
func (c *Client) execute(ctx context.Context, e *protocol.Encoder) (protocol.Decoder, error) {
	p, err := c.request(ctx, protocol.Service, e.Buffer)
	return protocol.Decoder{Buffer: p}, err
}
