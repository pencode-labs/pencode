package server

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/pencode-labs/pencode/protocol"
)

const workspaceWatchInterval = time.Second

type workspaceWatch struct {
	cancel      context.CancelFunc
	done        chan struct{}
	signal      chan struct{}
	pending     atomic.Uint32
	err         error // Published by closing done.
	watcher     *fsnotify.Watcher
	directories map[string]bool // Value identifies Git metadata directories.
	lastTree    time.Time
	lastGit     time.Time
}

// Native events are coalesced into two bits. Excess events are dropped using
// event timestamps; there is no timer, polling loop, or unbounded event queue.
func (w *workspaceWatch) publish(changes protocol.WorkspaceChange, now time.Time) {
	if changes&protocol.WorkspaceTreeChanged != 0 {
		if !w.lastTree.IsZero() && now.Sub(w.lastTree) < workspaceWatchInterval {
			changes &^= protocol.WorkspaceTreeChanged
		} else {
			w.lastTree = now
		}
	}
	if changes&protocol.WorkspaceGitChanged != 0 {
		if !w.lastGit.IsZero() && now.Sub(w.lastGit) < workspaceWatchInterval {
			changes &^= protocol.WorkspaceGitChanged
		} else {
			w.lastGit = now
		}
	}
	if changes != 0 {
		w.pending.Or(uint32(changes))
		select {
		case w.signal <- struct{}{}:
		default:
		}
	}
}

func skipWatchDirectory(name string, metadata bool) bool {
	if metadata {
		return name == "objects" || name == "logs" || name == "hooks"
	}
	return name == "node_modules" || name == "dist"
}

func (w *workspaceWatch) addGit(ctx context.Context, name string) error {
	info, err := os.Stat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(string(data), "gitdir: ") {
			return nil
		}
		directory := strings.TrimSpace(string(data[len("gitdir: "):]))
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(filepath.Dir(name), directory)
		}
		name = filepath.Clean(directory)
	}
	if err := w.add(ctx, name, true); err != nil {
		return err
	}
	// Linked worktrees store shared refs in a separate common Git directory.
	if data, err := os.ReadFile(filepath.Join(name, "commondir")); err == nil {
		common := strings.TrimSpace(string(data))
		if !filepath.IsAbs(common) {
			common = filepath.Join(name, common)
		}
		return w.add(ctx, filepath.Clean(common), true)
	}
	return nil
}

func (w *workspaceWatch) add(ctx context.Context, root string, metadata bool) error {
	return filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if name == root {
				return err
			}
			return nil
		}
		if name != root && !metadata && entry.Name() == ".git" {
			if err := w.addGit(ctx, name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			if entry.IsDir() {
				return fs.SkipDir
			}
		}
		if !entry.IsDir() {
			return nil
		}
		if name != root && skipWatchDirectory(entry.Name(), metadata) {
			return fs.SkipDir
		}
		if _, exists := w.directories[name]; exists {
			return fs.SkipDir
		}
		if len(w.directories) >= 262144 {
			return errors.New("filesystem watch exceeds directory budget")
		}
		if err := w.watcher.Add(name); err != nil {
			return err
		}
		w.directories[name] = metadata
		return nil
	})
}

func (w *workspaceWatch) event(ctx context.Context, event fsnotify.Event) error {
	metadata := w.directories[filepath.Dir(event.Name)]
	name := filepath.Base(event.Name)
	if skipWatchDirectory(name, metadata) || metadata && strings.HasSuffix(name, ".lock") {
		return nil
	}
	changes := protocol.WorkspaceGitChanged
	if !metadata && event.Has(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) {
		changes |= protocol.WorkspaceTreeChanged
	}
	if event.Has(fsnotify.Create) {
		var err error
		if name == ".git" && !metadata {
			err = w.addGit(ctx, event.Name)
		} else if info, statErr := os.Lstat(event.Name); statErr == nil && info.IsDir() {
			err = w.add(ctx, event.Name, metadata)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if event.Has(fsnotify.Remove | fsnotify.Rename) {
		if _, watched := w.directories[event.Name]; watched {
			for directory := range w.directories {
				if directory == event.Name || strings.HasPrefix(directory, event.Name+string(filepath.Separator)) {
					_ = w.watcher.Remove(directory)
					delete(w.directories, directory)
				}
			}
		}
	}
	w.publish(changes, time.Now())
	return nil
}

func (w *workspaceWatch) setup(ctx context.Context, root string) error {
	if err := w.add(ctx, root, false); err != nil {
		return err
	}
	// Opening a project inside a repository still needs HEAD/index/ref events.
	for directory := root; ; directory = filepath.Dir(directory) {
		git := filepath.Join(directory, ".git")
		if _, err := os.Stat(git); err == nil {
			return w.addGit(ctx, git)
		}
		if directory == filepath.Dir(directory) {
			break
		}
	}
	return nil
}

func (w *workspaceWatch) run(ctx context.Context, root string) {
	defer close(w.done)
	defer func() {
		if w.watcher != nil {
			_ = w.watcher.Close()
		}
	}()
	w.err = w.setup(ctx, root)
	if w.err != nil {
		return
	}
	// Establish the watch before requesting snapshots, closing the startup gap.
	w.pending.Store(uint32(protocol.WorkspaceTreeChanged | protocol.WorkspaceGitChanged))
	w.signal <- struct{}{}
	for {
		select {
		case <-ctx.Done():
			w.err = ctx.Err()
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename|fsnotify.Chmod) != 0 {
				w.err = w.event(ctx, event)
				if w.err != nil {
					return
				}
			}
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				// Reconcile newly created directories after the native queue overflowed.
				_ = w.watcher.Close()
				w.watcher, w.err = fsnotify.NewWatcher()
				if w.err != nil {
					return
				}
				clear(w.directories)
				w.err = w.setup(ctx, root)
				if w.err != nil {
					return
				}
				w.publish(protocol.WorkspaceTreeChanged|protocol.WorkspaceGitChanged, time.Now())
			} else {
				w.err = err
				return
			}
		}
	}
}

func (s *services) watchWorkspace(ctx context.Context, root string, e *protocol.Encoder) error {
	s.mu.Lock()
	if !s.roots[root] {
		s.mu.Unlock()
		return &protocol.Error{Code: protocol.NotFound, Message: "workspace is closed"}
	}
	w := s.watches[root]
	if w == nil {
		if len(s.watches) >= 32 {
			s.mu.Unlock()
			return &protocol.Error{Code: protocol.Busy, Message: "filesystem watch budget exceeded"}
		}
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			s.mu.Unlock()
			return err
		}
		watchCtx, cancel := context.WithCancel(s.ctx)
		w = &workspaceWatch{cancel: cancel, watcher: watcher, signal: make(chan struct{}, 1), done: make(chan struct{}), directories: make(map[string]bool)}
		s.watches[root] = w
		s.background.Add(1)
		go func() { defer s.background.Done(); w.run(watchCtx, filepath.FromSlash(root)) }()
	}
	s.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.done:
			if w.err != nil {
				return w.err
			}
			return fs.ErrClosed
		case <-w.signal:
			if changes := w.pending.Swap(0); changes != 0 {
				e.U32(changes)
				return nil
			}
		}
	}
}
