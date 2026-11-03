package client

import (
	"context"
	"sync"

	"github.com/pencode-labs/pencode/protocol"
)

// FileWatch is an independent cursor over the file service's bounded event
// journal. Cancellation leaves the cursor intact so retry never loses a batch.
type FileWatch struct {
	client  *Client
	root    string
	mu      sync.Mutex
	version uint64
}

func (c *Client) WatchFiles(root string) *FileWatch { return &FileWatch{client: c, root: root} }
func (w *FileWatch) Next(ctx context.Context, dst []protocol.FileEvent) (protocol.FileChanges, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	e := service(protocol.WorkspaceWatch, w.root)
	e.U64(w.version)
	d, err := w.client.execute(ctx, &e)
	if err != nil {
		return protocol.FileChanges{}, err
	}
	result := protocol.FileChanges{Version: d.U64(), Changes: protocol.WorkspaceChange(d.U32()), Reset: d.U32() != 0, Files: dst[:0]}
	count := d.U32()
	if count > 512 {
		return protocol.FileChanges{}, protocol.ErrProtocol
	}
	for range count {
		result.Files = append(result.Files, protocol.FileEvent{Path: d.String(), Op: protocol.FileOperation(d.U32()), Directory: d.U32() != 0})
	}
	if err = d.Done(); err != nil {
		return protocol.FileChanges{}, err
	}
	w.version = result.Version
	return result, nil
}
