package workbench

import (
	"context"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pencode-labs/pencode/internal/filewatch"
)

// Standalone tabs and files outside the project watch share a native watch
// per parent directory. Watching the parent also catches atomic replacement.
type localTabWatch struct {
	cancel context.CancelFunc
	files  map[string]bool // Project-relative name -> pending disk change.
	mu     sync.Mutex
}

func (p *pencodeProject) needsTabWatch(name string) bool {
	if strings.HasPrefix(name, untitledPrefix) {
		return false
	}
	if p.Loose {
		return true
	}
	dir, _ := path.Split(name)
	for part := range strings.SplitSeq(dir, "/") {
		if skipProjectEntry(part) {
			return true
		}
	}
	return false
}

func (m *workspaceModel) pollLocalTabWatches(p *pencodeProject) {
	if !m.backendConnected() || m.remoteWorkspace() {
		return
	}
	for _, name := range p.Tabs {
		if !p.needsTabWatch(name) {
			continue
		}
		dir, _ := path.Split(name)
		w := p.tabWatches[dir]
		if w == nil {
			if p.tabWatches == nil {
				p.tabWatches = make(map[string]*localTabWatch)
			}
			ctx, cancel := context.WithCancel(context.Background())
			w = &localTabWatch{cancel: cancel, files: map[string]bool{name: false}}
			p.tabWatches[dir] = w
			go w.run(ctx, filepath.Join(p.Path, filepath.FromSlash(dir)), dir, m.backgroundWake())
			continue
		}
		w.mu.Lock()
		if _, exists := w.files[name]; !exists {
			w.files[name] = true // Reconcile writes between the read and registration.
		}
		w.mu.Unlock()
	}
	for dir, w := range p.tabWatches {
		w.mu.Lock()
		for name, changed := range w.files {
			file := p.Files[name]
			if file == nil {
				delete(w.files, name)
			} else if changed {
				file.externalChanged = true
				w.files[name] = false
			}
		}
		empty := len(w.files) == 0
		w.mu.Unlock()
		if empty {
			w.cancel()
			delete(p.tabWatches, dir)
		}
	}
}

func (w *localTabWatch) notify(name string, wake func()) {
	w.mu.Lock()
	changed := false
	if name == "" {
		for file := range w.files {
			w.files[file] = true
		}
		changed = len(w.files) > 0
	} else if pending, exists := w.files[name]; exists && !pending {
		w.files[name], changed = true, true
	}
	w.mu.Unlock()
	if changed && wake != nil {
		wake()
	}
}

func (w *localTabWatch) run(ctx context.Context, directory, prefix string, wake func()) {
	delay := projectWatchRetryInitial
	for ctx.Err() == nil {
		watcher, err := filewatch.New()
		if err == nil {
			if watcher.Add(directory) == nil {
				delay = projectWatchRetryInitial
				w.notify("", wake)
				w.listen(ctx, watcher, directory, prefix, wake)
			}
			watcher.Close()
		}
		if !waitProjectWatchRetry(ctx, delay) {
			return
		}
		delay = min(delay*2, projectWatchRetryMax)
	}
}

func (w *localTabWatch) listen(ctx context.Context, watcher *filewatch.Watcher, directory, prefix string, wake func()) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-watcher.Errors:
			return
		case event := <-watcher.Events:
			if event.Name == directory && event.Has(filewatch.Remove|filewatch.Rename) {
				return
			}
			if filepath.Dir(event.Name) == directory {
				w.notify(prefix+filepath.Base(event.Name), wake)
			}
		}
	}
}
