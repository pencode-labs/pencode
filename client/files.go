package client

import (
	"context"

	"github.com/onix-engine/onix/text/piecetree"
	"github.com/pencode-labs/pencode/protocol"
)

func (c *Client) Create(ctx context.Context, workspace uint64, path string, directory bool) error {
	e := protocol.Encoder{}
	e.U64(workspace)
	e.String(path)
	if directory {
		e.U32(1)
	} else {
		e.U32(0)
	}
	_, err := c.request(ctx, protocol.ResourceCreate, e.Buffer)
	return err
}

// CreateDocument saves a new text file exclusively and returns its editing handle.
func (c *Client) CreateDocument(ctx context.Context, workspace uint64, path string, text piecetree.Snapshot) (*Document, error) {
	payload, err := c.requestEncoded(ctx, protocol.DocumentCreate, 16+len(path)+text.Len(), func(dst []byte) {
		e := protocol.Encoder{Buffer: dst[:0]}
		e.U64(workspace)
		e.String(path)
		e.U32(uint32(text.Len()))
		for part := range text.Chunks(0, text.Len()) {
			e.Buffer = append(e.Buffer, part...)
		}
	})
	if err != nil {
		return nil, err
	}
	d := protocol.Decoder{Buffer: payload}
	id, version := d.U64(), d.U64()
	if err := d.Done(); err != nil {
		return nil, err
	}
	return newDocument(c, id, version, text), nil
}

func (c *Client) Remove(ctx context.Context, workspace uint64, path string) error {
	e := protocol.Encoder{}
	e.U64(workspace)
	e.String(path)
	_, err := c.request(ctx, protocol.ResourceRemove, e.Buffer)
	return err
}
func (c *Client) Rename(ctx context.Context, workspace uint64, from, to string) error {
	e := protocol.Encoder{}
	e.U64(workspace)
	e.String(from)
	e.String(to)
	_, err := c.request(ctx, protocol.ResourceRename, e.Buffer)
	return err
}
func (c *Client) Transfer(ctx context.Context, source, target uint64, from, directory string, cut, duplicate bool) (string, error) {
	e := protocol.Encoder{}
	e.U64(source)
	e.U64(target)
	e.String(from)
	e.String(directory)
	var flags uint32
	if cut {
		flags |= 1
	}
	if duplicate {
		flags |= 2
	}
	e.U32(flags)
	p, err := c.request(ctx, protocol.ResourceTransfer, e.Buffer)
	if err != nil {
		return "", err
	}
	d := protocol.Decoder{Buffer: p}
	name := d.String()
	return name, d.Done()
}

// Import copies a native file on the server host into a workspace.
func (c *Client) Import(ctx context.Context, target uint64, source, directory string, overwrite bool) (string, error) {
	e := protocol.Encoder{}
	e.U64(target)
	e.String(source)
	e.String(directory)
	if overwrite {
		e.U32(1)
	} else {
		e.U32(0)
	}
	p, err := c.request(ctx, protocol.ResourceImport, e.Buffer)
	if err != nil {
		return "", err
	}
	d := protocol.Decoder{Buffer: p}
	name := d.String()
	return name, d.Done()
}

func (c *Client) readFile(ctx context.Context, root, name string, links bool, limit int) ([]byte, error) {
	e := service(protocol.FileRead, root)
	e.String(name)
	if links {
		e.U32(1)
	} else {
		e.U32(0)
	}
	e.U32(uint32(limit))
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
func (c *Client) ReadFile(ctx context.Context, root, name string) ([]byte, error) {
	return c.readFile(ctx, root, name, false, protocol.MaxMessage-4096)
}
func (c *Client) WorkFile(ctx context.Context, root, name string) ([]byte, error) {
	return c.readFile(ctx, root, name, true, protocol.FileComparisonLimit)
}

func (c *Client) SaveFile(ctx context.Context, root, name, before, after string) error {
	e := service(protocol.FileSave, root)
	e.String(name)
	e.String(before)
	e.String(after)
	d, err := c.execute(ctx, &e)
	if err != nil {
		return err
	}
	return d.Done()
}

// Diff encodes immutable editor text directly into the request buffer.
func (c *Client) Diff(ctx context.Context, left, right string) ([]byte, error) {
	e := service(protocol.DiffPatch, "")
	e.String(left)
	e.String(right)
	d, err := c.execute(ctx, &e)
	if err != nil {
		return nil, err
	}
	p := d.Bytes()
	return p, d.Done()
}

func (c *Client) FileHistory(ctx context.Context, root, scope string, recursive bool, before protocol.FileVersion) ([]protocol.FileVersion, bool, error) {
	e := service(protocol.FileHistoryList, root)
	e.String(scope)
	if recursive {
		e.U32(1)
	} else {
		e.U32(0)
	}
	e.String(before.Path)
	e.U64(before.Time)
	d, err := c.execute(ctx, &e)
	if err != nil {
		return nil, false, err
	}
	more, count := d.U32(), d.U32()
	if more > 1 || count > protocol.FileHistoryPageSize {
		return nil, false, protocol.ErrProtocol
	}
	versions := make([]protocol.FileVersion, count)
	for i := range versions {
		versions[i] = protocol.FileVersion{Path: d.String(), Time: d.U64()}
	}
	return versions, more != 0, d.Done()
}

func (c *Client) fileVersion(ctx context.Context, op protocol.Operation, root string, version protocol.FileVersion) (protocol.Decoder, error) {
	e := service(op, root)
	e.String(version.Path)
	e.U64(version.Time)
	return c.execute(ctx, &e)
}

func (c *Client) ReadFileHistory(ctx context.Context, root string, version protocol.FileVersion) ([]byte, error) {
	d, err := c.fileVersion(ctx, protocol.FileHistoryRead, root, version)
	if err != nil {
		return nil, err
	}
	data := d.Bytes()
	return data, d.Done()
}

func (c *Client) RestoreFileHistory(ctx context.Context, root string, version protocol.FileVersion) error {
	d, err := c.fileVersion(ctx, protocol.FileHistoryRestore, root, version)
	if err != nil {
		return err
	}
	return d.Done()
}
