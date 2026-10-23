package client

import (
	"context"

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
	return c.readFile(ctx, root, name, true, 2<<20)
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
