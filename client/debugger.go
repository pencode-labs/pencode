package client

import (
	"context"

	"github.com/pencode-labs/pencode/protocol"
)

// OpenDebugAdapter returns the same owned, flow-controlled stream for local
// and SSH workspaces. Delve selects an ephemeral loopback port on the server.
func (c *Client) OpenDebugAdapter(ctx context.Context, root, adapter, command, address string, args []string) (*Process, error) {
	e := service(protocol.DebugAdapterOpen, root)
	e.String(adapter)
	e.String(command)
	e.String(address)
	e.U32(uint32(len(args)))
	for _, arg := range args {
		e.String(arg)
	}
	d, err := c.execute(ctx, &e)
	if err != nil {
		return nil, err
	}
	return c.decodeProcess(&d, 0, 0)
}

func (c *Client) OpenDebugTerminal(ctx context.Context, root string, command protocol.TerminalCommand) (*Process, int, error) {
	e := service(protocol.DebugTerminalOpen, root)
	e.String(command.Directory)
	e.U32(uint32(len(command.Args)))
	for _, arg := range command.Args {
		e.String(arg)
	}
	e.U32(uint32(len(command.Env)))
	for key, value := range command.Env {
		e.String(key)
		if value == nil {
			e.U32(0)
		} else {
			e.U32(1)
			e.String(*value)
		}
	}
	d, err := c.execute(ctx, &e)
	if err != nil {
		return nil, 0, err
	}
	pid := int(d.U32())
	p, err := c.decodeProcess(&d, 80, 12)
	return p, pid, err
}
