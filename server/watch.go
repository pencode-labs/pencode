package server

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pencode-labs/pencode/internal/filewatch"
	"github.com/pencode-labs/pencode/protocol"
	"github.com/pencode-labs/pencode/server/workspace"
)

const (
	workspaceWatchInterval = 200 * time.Millisecond
)

type workspaceWatch struct {
	err         error // Published by closing done.
	cancel      context.CancelFunc
	done        chan struct{}
	watcher     *filewatch.Watcher
	directories map[string]bool // Value identifies Git metadata directories.
	files       [128]protocol.FileEvent
	tree        workspaceTreeChanges
	journal     fileEventJournal
	index       uint64 // Most recently started snapshot; guarded by services.mu.
	fileCount   int
	changes     protocol.WorkspaceChange // Owned by the native event loop.
	reset       bool
}

// Keep bounded directory hints separately from notifications: consuming a watch
// notification must not discard changes before the next index snapshot starts.
type workspaceTreeChanges struct {
	workspaceTreeDelta
	mu sync.Mutex
}

type workspaceTreeDelta struct {
	directories [128]string
	count       int
	full        bool
}

func (d *workspaceTreeDelta) contains(directory string) bool {
	for _, name := range d.directories[:d.count] {
		if name == directory {
			return true
		}
	}
	return false
}

func (t *workspaceTreeChanges) add(directory string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.full || t.contains(directory) {
		return
	}
	if t.count == len(t.directories) {
		t.workspaceTreeDelta = workspaceTreeDelta{full: true}
		return
	}
	t.directories[t.count] = directory
	t.count++
}

func (t *workspaceTreeChanges) invalidate() {
	t.mu.Lock()
	t.workspaceTreeDelta = workspaceTreeDelta{full: true}
	t.mu.Unlock()
}

func (t *workspaceTreeChanges) take() workspaceTreeDelta {
	t.mu.Lock()
	defer t.mu.Unlock()
	delta := t.workspaceTreeDelta
	t.workspaceTreeDelta = workspaceTreeDelta{}
	return delta
}

func (w *workspaceWatch) publish(changes protocol.WorkspaceChange) {
	if changes == 0 && w.fileCount == 0 && !w.reset {
		return
	}
	w.journal.publish(changes, w.files[:w.fileCount], w.reset)
	clear(w.files[:w.fileCount])
	w.fileCount = 0
	w.reset = false
}

func watchRelativePath(root, name string) (string, bool) {
	if root == "" {
		return "", false
	}
	if name == root {
		return ".", true
	}
	if strings.HasPrefix(name, root) && len(name) > len(root) && (os.IsPathSeparator(root[len(root)-1]) || os.IsPathSeparator(name[len(root)])) {
		offset := len(root)
		if os.IsPathSeparator(name[offset]) {
			offset++
		}
		return name[offset:], true
	}
	return "", false
}
func (w *workspaceWatch) record(event filewatch.Event) {
	relative, ok := watchRelativePath(w.journal.root, event.Name)
	if !ok {
		return
	}
	name := filepath.ToSlash(relative)
	for i := range w.fileCount {
		if w.files[i].Path == name {
			w.files[i].Op |= protocol.FileOperation(event.Op)
			w.files[i].Directory = w.files[i].Directory || event.Directory
			return
		}
	}
	if w.fileCount == len(w.files) {
		w.reset = true
		return
	}
	w.files[w.fileCount] = protocol.FileEvent{Path: name, Op: protocol.FileOperation(event.Op), Directory: event.Directory}
	w.fileCount++
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
	if filewatch.Recursive {
		if err := w.watcher.Add(root); err != nil {
			return err
		}
	}
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
		if !filewatch.Recursive {
			if err := w.watcher.Add(name); err != nil {
				return err
			}
		}
		if !metadata {
			w.tree.add(name)
		}
		w.directories[name] = metadata
		return nil
	})
}

func (w *workspaceWatch) event(ctx context.Context, event filewatch.Event) error {
	// Native event paths are already clean. Split without Dir's Windows
	// volume concatenation, preserving drive, UNC and Unix roots.
	parent, name := filepath.Split(event.Name)
	if strings.HasPrefix(name, workspace.SaveTemporaryPrefix) {
		return nil
	}
	if len(parent) > len(filepath.VolumeName(parent))+1 {
		parent = parent[:len(parent)-1]
	}
	metadata := w.directories[parent]
	base := w.journal.root
	for directory := parent; directory != ""; directory = filepath.Dir(directory) {
		if value, exists := w.directories[directory]; exists {
			metadata = value
			if metadata {
				base = directory
			}
			break
		}
		if directory == filepath.Dir(directory) {
			break
		}
	}
	if relative, ok := watchRelativePath(base, event.Name); ok {
		for part := range strings.SplitSeq(relative, string(filepath.Separator)) {
			if part == "node_modules" || part == "dist" || metadata && (part == "objects" || part == "logs" || part == "hooks") {
				return nil
			}
			if part == ".git" && name != ".git" {
				metadata = true
			}
		}
	}
	if skipWatchDirectory(name, metadata) || metadata && strings.HasSuffix(name, ".lock") {
		return nil
	}
	if !metadata {
		_, watchedDirectory := w.directories[event.Name]
		event.Directory = event.Directory || watchedDirectory
		if !event.Directory && event.Has(filewatch.Create) {
			if info, err := os.Lstat(event.Name); err == nil {
				event.Directory = info.IsDir()
			}
		}
		w.record(event)
	}
	changes := protocol.WorkspaceGitChanged
	if name == ".gitignore" || metadata && (name == "index" || name == "exclude" || name == "config") {
		changes |= protocol.WorkspaceTreeChanged | protocol.WorkspaceIgnoreChanged
	}
	gitPointer := name == ".git"
	if gitPointer {
		metadata = false
	}
	if !metadata && event.Has(filewatch.Create|filewatch.Remove|filewatch.Rename) {
		changes |= protocol.WorkspaceTreeChanged
		w.tree.add(parent)
		if gitPointer {
			changes |= protocol.WorkspaceRepositoriesChanged
		}
	}
	if event.Has(filewatch.Create) || gitPointer && event.Has(filewatch.Write) {
		var err error
		if gitPointer {
			changes |= protocol.WorkspaceRepositoriesChanged
			err = w.addGit(ctx, event.Name)
		} else if info, statErr := os.Lstat(event.Name); statErr == nil && info.IsDir() {
			if !metadata {
				changes |= protocol.WorkspaceRepositoriesChanged
			}
			err = w.add(ctx, event.Name, metadata)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if event.Has(filewatch.Remove | filewatch.Rename) {
		if _, watched := w.directories[event.Name]; watched {
			if !metadata {
				changes |= protocol.WorkspaceRepositoriesChanged
			}
			for directory := range w.directories {
				if directory == event.Name || strings.HasPrefix(directory, event.Name+string(filepath.Separator)) {
					_ = w.watcher.Remove(directory)
					delete(w.directories, directory)
				}
			}
		}
	}
	w.changes |= changes
	return nil
}

func (w *workspaceWatch) setup(ctx context.Context, root string) error {
	w.tree.invalidate()
	w.reset = true
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
	w.publish(protocol.WorkspaceTreeChanged | protocol.WorkspaceGitChanged | protocol.WorkspaceRepositoriesChanged)
	timer := time.NewTimer(workspaceWatchInterval)
	timer.Stop()
	defer timer.Stop()
	var batch <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			w.err = ctx.Err()
			return
		case <-batch:
			w.publish(w.changes)
			w.changes = 0
			batch = nil
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			if event.Name == root && event.Has(filewatch.Remove|filewatch.Rename) {
				w.err = &fs.PathError{Op: "watch", Path: root, Err: fs.ErrNotExist}
				return
			}
			if event.Op&(filewatch.Create|filewatch.Write|filewatch.Remove|filewatch.Rename|filewatch.Chmod) != 0 {
				w.err = w.event(ctx, event)
				if w.err != nil {
					return
				}
				if w.changes != 0 && batch == nil {
					timer.Reset(workspaceWatchInterval)
					batch = timer.C
				}
			}
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			if errors.Is(err, filewatch.ErrOverflow) {
				w.err = w.rebuild(ctx, root)
				timer.Stop()
				batch = nil
				if w.err != nil {
					return
				}
			} else {
				w.err = err
				return
			}
		}
	}
}

func (w *workspaceWatch) rebuild(ctx context.Context, root string) error {
	// Reconcile directories created or removed while native events were lost.
	_ = w.watcher.Close()
	watcher, err := filewatch.New()
	if err != nil {
		return err
	}
	w.watcher = watcher
	clear(w.directories)
	if err := w.setup(ctx, root); err != nil {
		return err
	}
	w.changes = 0
	w.publish(protocol.WorkspaceTreeChanged | protocol.WorkspaceGitChanged | protocol.WorkspaceRepositoriesChanged)
	return nil
}

func (s *services) watchWorkspace(ctx context.Context, root string, after uint64, e *protocol.Encoder) error {
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
		watcher, err := filewatch.New()
		if err != nil {
			s.mu.Unlock()
			return err
		}
		watchCtx, cancel := context.WithCancel(s.ctx)
		w = &workspaceWatch{cancel: cancel, watcher: watcher, done: make(chan struct{}), directories: make(map[string]bool)}
		s.next++
		w.journal = fileEventJournal{root: filepath.FromSlash(root), version: s.next << 32, base: s.next << 32, changed: make(chan struct{})}
		s.watches[root] = w
		s.background.Add(1)
		go func() { defer s.background.Done(); w.run(watchCtx, filepath.FromSlash(root)) }()
	}
	s.mu.Unlock()
	for {
		w.journal.mu.Lock()
		if w.journal.encode(after, e) {
			w.journal.mu.Unlock()
			return nil
		}
		changed := w.journal.changed
		w.journal.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.done:
			s.mu.Lock()
			if s.watches[root] == w {
				delete(s.watches, root)
				w.cancel()
			}
			s.mu.Unlock()
			if w.err != nil {
				return w.err
			}
			return fs.ErrClosed
		case <-changed:
		}
	}
}
