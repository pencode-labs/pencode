package client

import (
	"context"
	"errors"
	"io/fs"

	"github.com/pencode-labs/pencode/protocol"
)

// Directories supplies only directory enumeration to the existing scanner.
// File content is accessed through versioned Document APIs.
type Directories struct {
	Client    *Client
	Workspace uint64
	Context   context.Context
}

// BrowseDirectory enumerates project choices without opening a workspace.
func (c *Client) BrowseDirectory(ctx context.Context, directory string) (string, string, []string, error) {
	e := service(protocol.DirectoryBrowse, "")
	e.String(directory)
	d, err := c.execute(ctx, &e)
	if err != nil {
		return "", "", nil, err
	}
	current, parent, count := d.String(), d.String(), d.U32()
	if count > 100000 || int(count) > len(d.Buffer)/4 {
		return "", "", nil, protocol.ErrProtocol
	}
	names := make([]string, 0, count)
	for range count {
		names = append(names, d.String())
	}
	return current, parent, names, d.Done()
}

func (f Directories) ReadDir(name string) ([]fs.DirEntry, error) {
	e := protocol.Encoder{}
	e.U64(f.Workspace)
	e.String(name)
	p, err := f.Client.request(f.Context, protocol.DirectoryRead, e.Buffer)
	if err != nil {
		return nil, err
	}
	d := protocol.Decoder{Buffer: p}
	n := d.U32()
	if n > 100000 || int(n) > len(d.Buffer)/8 {
		return nil, protocol.ErrProtocol
	}
	entries := make([]fs.DirEntry, 0, n)
	for range n {
		entries = append(entries, directoryEntry{name: d.String(), mode: fs.FileMode(d.U32())})
	}
	return entries, d.Done()
}

type directoryEntry struct {
	name string
	mode fs.FileMode
}

func (e directoryEntry) Name() string               { return e.name }
func (e directoryEntry) Type() fs.FileMode          { return e.mode }
func (e directoryEntry) IsDir() bool                { return e.mode.IsDir() }
func (e directoryEntry) Info() (fs.FileInfo, error) { return nil, errors.ErrUnsupported }
