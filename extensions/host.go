// Package extensions bridges server-owned extensions to GUI appearance and views.
package extensions

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"sync"
	"time"

	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/output"
	"github.com/pencode-labs/pencode/protocol"
)

const (
	StartupTimeout = 5 * time.Second
	maxWorkers     = 4
)

// Metadata uses the same domain types on both sides of the connection.
type Manifest = protocol.ExtensionManifest
type Installed = protocol.ExtensionInfo

type Result struct {
	ID     string
	Bundle *Bundle
	Err    error
}
type Host struct {
	Results <-chan Result
	cancel  context.CancelFunc
	done    chan struct{}
}

// Start discovers packages on the server. GUI workers import only immutable
// resources; no process creation or installed-directory I/O happens here.
func Start(backend *client.Client, wake func(), logs *output.Store) *Host {
	ctx, cancel := context.WithCancel(output.WithStore(context.Background(), logs))
	results := make(chan Result, maxWorkers)
	h := &Host{Results: results, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		defer close(results)
		publish := func(r Result) {
			select {
			case <-ctx.Done():
				r.Bundle.Close()
				return
			case results <- r:
				if wake != nil {
					wake()
				}
			}
		}
		catalog, err := backend.ListExtensions(ctx)
		if err != nil {
			publish(Result{Err: err})
			return
		}
		jobs := make(chan string)
		var workers sync.WaitGroup
		for range maxWorkers {
			workers.Go(func() {
				for id := range jobs {
					bundle, err := Load(ctx, backend, id)
					if bundle != nil || err != nil {
						publish(Result{ID: id, Bundle: bundle, Err: err})
					}
				}
			})
		}
		for _, item := range catalog.Items {
			if item.Disabled {
				continue
			}
			if item.Problem != "" {
				publish(Result{ID: item.ID, Err: fmt.Errorf("%s", item.Problem)})
				continue
			}
			select {
			case <-ctx.Done():
				close(jobs)
				workers.Wait()
				return
			case jobs <- item.ID:
			}
		}
		close(jobs)
		workers.Wait()
	}()
	return h
}

func (h *Host) Close() {
	if h != nil {
		h.cancel()
		<-h.done
		for result := range h.Results {
			result.Bundle.Close()
		}
	}
}

func Load(ctx context.Context, backend *client.Client, id string) (*Bundle, error) {
	remote, err := backend.LoadExtension(ctx, id)
	if err != nil {
		return nil, err
	}
	if remote.Info.Disabled {
		return nil, nil
	}
	return importRemote(ctx, remote)
}

func importRemote(ctx context.Context, remote *client.Extension) (*Bundle, error) {
	names := contributionResources(remote.Contributions)
	cache, err := remote.ReadResources(ctx, names, 32<<20)
	if err != nil {
		return nil, err
	}
	bundle, err := loadBundle(remote.FS(ctx, cache), remote.Info.ID, remote.Contributions)
	if err != nil {
		return nil, err
	}
	var channel *output.Channel
	if logs := output.FromContext(ctx); logs != nil {
		channel = logs.Channel("Extension: " + remote.Info.ID)
		if remote.Diagnostics != "" {
			_, _ = channel.Write([]byte(remote.Diagnostics))
		}
	}
	if bundle.Actions != nil {
		bundle.Actions.remote = remote
		bundle.Actions.Output = channel
	}
	for i := range bundle.Editors {
		bundle.Editors[i].remote = remote
		bundle.Editors[i].Output = channel
	}
	for i := range bundle.LanguageServers {
		bundle.LanguageServers[i].remote = remote
	}
	return bundle, nil
}

// Runtime stderr is drained on its own flow-controlled stream. Closing the
// proxy waits for both the server-owned child and this diagnostic reader.
func openExtensionProcess(ctx context.Context, remote *client.Extension, root string, logs *output.Channel) (*client.Process, *diagnosticBuffer, func(), error) {
	p, err := remote.OpenProcess(ctx, root)
	if err != nil {
		return nil, nil, nil, err
	}
	stderr := &diagnosticBuffer{output: logs}
	done := make(chan struct{})
	go func() { defer close(done); _, _ = io.Copy(stderr, p.Stderr()) }()
	stop := context.AfterFunc(ctx, func() { _ = p.Close() })
	closeProcess := func() { stop(); _ = p.Close(); <-done }
	return p, stderr, closeProcess, nil
}

func validID(id string) bool {
	if len(id) == 0 || len(id) > 128 || id == "." || id == ".." {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

// Always drain stderr, while bounding retained diagnostics.
type diagnosticBuffer struct {
	output *output.Channel
	data   [4096]byte
	used   int
}

func (b *diagnosticBuffer) Write(p []byte) (int, error) {
	if b.output != nil {
		_, _ = b.output.Write(p)
	}
	b.used += copy(b.data[b.used:], p)
	return len(p), nil
}

func readResource(source fs.FS, name string, limit int64) ([]byte, error) {
	if source, ok := source.(interface {
		ReadFileLimit(string, int64) ([]byte, error)
	}); ok {
		return source.ReadFileLimit(name, limit)
	}
	f, err := source.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("invalid or oversized resource %q", name)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = fmt.Errorf("oversized resource %q", name)
	}
	return data, err
}
