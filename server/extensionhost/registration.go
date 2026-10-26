package extensionhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pencode-labs/pencode/peb"
	"github.com/pencode-labs/pencode/transport"
)

type Registration struct {
	Info          Installed
	Contributions peb.Contributions
	Diagnostics   string
}

// Register probes a package on the server. Appearance resources are interpreted
// by the GUI before an installation or enable transaction can be committed.
func Register(ctx context.Context, directory, id string, enable bool) (Registration, error) {
	var result Registration
	if !ValidID(id) {
		return result, fmt.Errorf("invalid extension id %q", id)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return result, err
	}
	defer root.Close()
	manifest, err := readManifest(root)
	if err != nil {
		return result, err
	}
	if manifest.ID != id {
		return result, fmt.Errorf("manifest id must match directory %q", id)
	}
	result.Info = Installed{ExtensionManifest: manifest, Path: directory}
	if manifest.Disabled && !enable {
		return result, nil
	}
	ctx, cancel := context.WithTimeout(ctx, StartupTimeout)
	defer cancel()
	var diagnostics diagnosticBuffer
	p, err := Open(root, directory, manifest, &diagnostics)
	if err != nil {
		return result, err
	}
	stop := context.AfterFunc(ctx, func() { _ = p.Close() })
	defer stop()
	result.Contributions, err = exchange(p)
	if err == nil {
		// The peer exits after Shutdown. Its final stdout must still be drained.
		_, err = io.Copy(io.Discard, p)
	}
	if err == nil {
		err = p.(interface{ Wait() error }).Wait()
	}
	_ = p.Close()
	result.Diagnostics = string(diagnostics.data[:diagnostics.used])
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil && result.Diagnostics != "" {
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(result.Diagnostics))
	}
	return result, err
}

func Open(root *os.Root, directory string, manifest Manifest, stderr io.Writer) (io.ReadWriteCloser, error) {
	if !ValidResourcePath(manifest.Entry, false) {
		return nil, fmt.Errorf("invalid extension entry %q", manifest.Entry)
	}
	entry, err := root.Open(filepath.FromSlash(manifest.Entry))
	if err != nil {
		return nil, err
	}
	info, err := entry.Stat()
	entry.Close()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("extension entry is not a regular file")
	}
	return transport.StartWithOutput(directory, filepath.Join(directory, filepath.FromSlash(manifest.Entry)), stderr, manifest.Args...)
}

func exchange(p io.ReadWriter) (peb.Contributions, error) {
	w, r := peb.Writer{Target: p}, peb.Reader{Source: p}
	hello, _ := peb.EncodeHello(nil, peb.Hello{Host: "pencode", Capabilities: peb.Capabilities})
	if err := w.Write(peb.Initialize, 1, hello); err != nil {
		return peb.Contributions{}, err
	}
	f, err := r.Read()
	if err != nil {
		return peb.Contributions{}, err
	}
	if f.ID != 1 {
		return peb.Contributions{}, peb.ErrProtocol
	}
	if f.Kind == peb.Failure {
		message, err := peb.DecodeFailure(f.Payload)
		if err != nil {
			return peb.Contributions{}, err
		}
		return peb.Contributions{}, errors.New(message)
	}
	if f.Kind != peb.Register {
		return peb.Contributions{}, peb.ErrProtocol
	}
	c, err := peb.DecodeContributions(f.Payload)
	if err != nil {
		return c, err
	}
	if err := validateDebugAdapters(c.DebugAdapters); err != nil {
		return c, err
	}
	return c, w.Write(peb.Shutdown, 0, nil)
}

type diagnosticBuffer struct {
	data [4096]byte
	used int
}

func (b *diagnosticBuffer) Write(p []byte) (int, error) {
	b.used += copy(b.data[b.used:], p)
	return len(p), nil
}
