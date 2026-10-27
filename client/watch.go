package client

import (
	"context"

	"github.com/pencode-labs/pencode/protocol"
)

// WatchWorkspace blocks until filesystem changes are available. The first call
// installs the recursive watch and requests an initial snapshot.
func (c *Client) WatchWorkspace(ctx context.Context, root string) (protocol.WorkspaceChange, error) {
	e := service(protocol.WorkspaceWatch, root)
	d, err := c.execute(ctx, &e)
	if err != nil {
		return 0, err
	}
	changes := protocol.WorkspaceChange(d.U32())
	return changes, d.Done()
}
