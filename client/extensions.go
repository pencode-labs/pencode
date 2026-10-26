package client

import (
	"bytes"
	"context"
	"io/fs"
	"path"
	"time"

	"github.com/pencode-labs/pencode/peb"
	"github.com/pencode-labs/pencode/protocol"
)

// Extension is a connection-scoped server registration. No executable or asset
// path is interpreted on the GUI host.
type Extension struct {
	client        *Client
	Handle        uint64
	Info          protocol.ExtensionInfo
	Contributions peb.Contributions
	Diagnostics   string
}

type ExtensionCatalog struct {
	Directory string
	Items     []protocol.ExtensionInfo
}

func (c *Client) ListExtensions(ctx context.Context) (ExtensionCatalog, error) {
	e := service(protocol.ExtensionList, "")
	d, err := c.execute(ctx, &e)
	if err != nil {
		return ExtensionCatalog{}, err
	}
	catalog := ExtensionCatalog{Directory: d.String()}
	n := d.U32()
	if n > 64 {
		return ExtensionCatalog{}, protocol.ErrProtocol
	}
	items := make([]protocol.ExtensionInfo, n)
	for i := range items {
		items[i] = protocol.DecodeExtension(&d)
	}
	catalog.Items = items
	return catalog, d.Done()
}

func (c *Client) extension(ctx context.Context, e *protocol.Encoder) (*Extension, error) {
	d, err := c.execute(ctx, e)
	if err != nil {
		return nil, err
	}
	x := &Extension{client: c, Handle: d.U64(), Info: protocol.DecodeExtension(&d)}
	contributions := d.Bytes()
	x.Diagnostics = d.String()
	if err := d.Done(); err != nil {
		return nil, err
	}
	x.Contributions, err = peb.DecodeContributions(contributions)
	return x, err
}

func (c *Client) LoadExtension(ctx context.Context, id string) (*Extension, error) {
	e := service(protocol.ExtensionLoad, "")
	e.String(id)
	return c.extension(ctx, &e)
}

func (c *Client) PrepareEnableExtension(ctx context.Context, id string) (*Extension, error) {
	e := service(protocol.ExtensionEnable, "")
	e.String(id)
	e.U32(1)
	return c.extension(ctx, &e)
}

func (c *Client) DisableExtension(ctx context.Context, id string) (protocol.ExtensionInfo, error) {
	e := service(protocol.ExtensionEnable, "")
	e.String(id)
	e.U32(0)
	d, err := c.execute(ctx, &e)
	if err != nil {
		return protocol.ExtensionInfo{}, err
	}
	info := protocol.DecodeExtension(&d)
	return info, d.Done()
}

func (c *Client) RemoveExtension(ctx context.Context, id string) error {
	e := service(protocol.ExtensionRemove, "")
	e.String(id)
	d, err := c.execute(ctx, &e)
	if err != nil {
		return err
	}
	return d.Done()
}

func (x *Extension) ReadResource(ctx context.Context, name string, limit int64) ([]byte, error) {
	if limit < 1 || limit > 32<<20 {
		return nil, fs.ErrInvalid
	}
	e := service(protocol.ExtensionResource, "")
	e.U64(x.Handle)
	e.String(name)
	e.U32(uint32(limit))
	d, err := x.client.execute(ctx, &e)
	if err != nil {
		return nil, err
	}
	data := d.Bytes()
	return data, d.Done()
}

// ReadWorkspaceFile reads a document asset through the workspace connection.
// Paths are resolved and confined to root by the workspace server.
func (x *Extension) ReadWorkspaceFile(ctx context.Context, root, name string, limit int) ([]byte, error) {
	if limit < 1 || limit > protocol.MaxMessage-4096 {
		return nil, fs.ErrInvalid
	}
	return x.client.readFile(ctx, root, name, false, limit)
}

// ReadResources batches names and pages the response at the server's byte
// budget. Large resources can never exceed a protocol message.
func (x *Extension) ReadResources(ctx context.Context, names []string, limit int64) (map[string][]byte, error) {
	if len(names) > 4096 || limit < 1 || limit > 32<<20 {
		return nil, protocol.ErrProtocol
	}
	resources := make(map[string][]byte, len(names))
	var e protocol.Encoder
	for len(names) > 0 {
		e = service(protocol.ExtensionResources, "")
		e.U64(x.Handle)
		e.U32(uint32(limit))
		e.U32(uint32(len(names)))
		for _, name := range names {
			e.String(name)
		}
		d, err := x.client.execute(ctx, &e)
		if err != nil {
			return nil, err
		}
		count := d.U32()
		if count == 0 || count > uint32(len(names)) {
			return nil, protocol.ErrProtocol
		}
		for i := range int(count) {
			name, data := d.String(), d.Bytes()
			if name != names[i] || int64(len(data)) > limit {
				return nil, protocol.ErrProtocol
			}
			if _, duplicate := resources[name]; duplicate {
				return nil, protocol.ErrProtocol
			}
			resources[name] = data
		}
		if err := d.Done(); err != nil {
			return nil, err
		}
		names = names[count:]
	}
	return resources, nil
}

func (x *Extension) OpenProcess(ctx context.Context, root string) (*Process, error) {
	return x.openProcess(ctx, root, "")
}

func (x *Extension) OpenLanguageServer(ctx context.Context, root, id string) (*Process, error) {
	return x.openProcess(ctx, root, id)
}

func (x *Extension) openProcess(ctx context.Context, root, language string) (*Process, error) {
	op := protocol.ExtensionProcess
	if language != "" {
		op = protocol.ExtensionLanguageProcess
	}
	e := service(op, root)
	e.U64(x.Handle)
	if language != "" {
		e.String(language)
	}
	d, err := x.client.execute(ctx, &e)
	if err != nil {
		return nil, err
	}
	return x.client.decodeProcess(&d, 0, 0)
}

// Commit publishes a prepared package or persists a validated enable operation.
func (x *Extension) Commit(ctx context.Context) (protocol.ExtensionInfo, error) {
	e := service(protocol.ExtensionCommit, "")
	e.U64(x.Handle)
	d, err := x.client.execute(ctx, &e)
	if err != nil {
		return protocol.ExtensionInfo{}, err
	}
	info := protocol.DecodeExtension(&d)
	// Runtime metadata is immutable; the process and resources are resolved by
	// handle on the server, including after the staging directory is renamed.
	return info, d.Done()
}

func (c *Client) abortExtension(ctx context.Context, handle uint64) error {
	e := service(protocol.ExtensionAbort, "")
	e.U64(handle)
	d, err := c.execute(ctx, &e)
	if err != nil {
		return err
	}
	return d.Done()
}

func (x *Extension) Abort(ctx context.Context) error {
	return x.client.abortExtension(ctx, x.Handle)
}

type ExtensionUpload struct {
	client *Client
	handle uint64
}

func (c *Client) BeginExtension(ctx context.Context) (*ExtensionUpload, error) {
	e := service(protocol.ExtensionBegin, "")
	d, err := c.execute(ctx, &e)
	if err != nil {
		return nil, err
	}
	u := &ExtensionUpload{client: c, handle: d.U64()}
	return u, d.Done()
}

func (u *ExtensionUpload) Write(ctx context.Context, name string, offset uint64, mode uint32, final bool, data []byte) error {
	e := service(protocol.ExtensionWrite, "")
	e.U64(u.handle)
	e.String(name)
	e.U64(offset)
	e.U32(mode)
	if final {
		e.U32(1)
	} else {
		e.U32(0)
	}
	e.Bytes(data)
	d, err := u.client.execute(ctx, &e)
	if err != nil {
		return err
	}
	return d.Done()
}

func (u *ExtensionUpload) Prepare(ctx context.Context) (*Extension, error) {
	e := service(protocol.ExtensionPrepare, "")
	e.U64(u.handle)
	return u.client.extension(ctx, &e)
}

func (u *ExtensionUpload) Abort(ctx context.Context) error {
	return u.client.abortExtension(ctx, u.handle)
}

// FS lets appearance loaders consume remote resources through the standard Go
// filesystem interface. Each open returns bounded, memory-owned bytes.
func (x *Extension) FS(ctx context.Context, cache map[string][]byte) fs.FS {
	return extensionFS{x, ctx, cache}
}

type extensionFS struct {
	extension *Extension
	ctx       context.Context
	cache     map[string][]byte
}

func (f extensionFS) Open(name string) (fs.File, error) {
	data, err := f.ReadFileLimit(name, 32<<20)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return &resourceFile{Reader: *bytes.NewReader(data), name: path.Base(name)}, nil
}

func (f extensionFS) ReadFileLimit(name string, limit int64) ([]byte, error) {
	if data, ok := f.cache[name]; ok {
		if int64(len(data)) > limit {
			return nil, fs.ErrInvalid
		}
		return data, nil
	}
	return f.extension.ReadResource(f.ctx, name, limit)
}

type resourceFile struct {
	bytes.Reader
	name string
}

func (f *resourceFile) Close() error               { return nil }
func (f *resourceFile) Stat() (fs.FileInfo, error) { return f, nil }
func (f *resourceFile) Name() string               { return f.name }
func (f *resourceFile) Size() int64                { return f.Reader.Size() }
func (f *resourceFile) Mode() fs.FileMode          { return 0o444 }
func (f *resourceFile) ModTime() time.Time         { return time.Time{} }
func (f *resourceFile) IsDir() bool                { return false }
func (f *resourceFile) Sys() any                   { return nil }
