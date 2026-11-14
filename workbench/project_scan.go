package workbench

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/protocol"
)

const (
	pencodeTreeBatchSize    = protocol.IndexPageSize
	pencodeTreeFrameBatches = 4
	pencodeTreeLoading      = "正在加载项目目录…"
	pencodeTreeReloading    = "正在从磁盘重新加载…"
)

// Only the foreground model owns project state. The worker transfers entries
// through two reusable buffers, bounding both memory and work per frame.
type pencodeTreeBatch struct {
	err     error
	entries [pencodeTreeBatchSize]treeEntry
	index   uint64
	count   int
	preview bool
	done    bool
}

type pencodeProjectScan struct {
	cancel       context.CancelFunc
	results      chan *pencodeTreeBatch
	buffers      chan *pencodeTreeBatch
	entries      []treeEntry
	repositories []string
	previous     uint64
	loaded       int
	initial      bool
	staged       bool
	preview      bool
}

func newBackendProjectScan(backend *client.Client, workspace protocol.Workspace, initial bool, previous uint64, repositories []string, wake func(), ready <-chan struct{}) *pencodeProjectScan {
	ctx, cancel := context.WithCancel(context.Background())
	scan := &pencodeProjectScan{cancel: cancel, initial: initial, previous: previous, results: make(chan *pencodeTreeBatch, 2), buffers: make(chan *pencodeTreeBatch, 2)}
	for range 2 {
		scan.buffers <- new(pencodeTreeBatch)
	}
	go func() {
		defer close(scan.results)
		if previous == 0 {
			// Root rows must not wait for recursive watch setup or indexing.
			entries, err := (client.Directories{Client: backend, Workspace: workspace.ID, Context: ctx}).ReadDir(".")
			if err == nil && scan.publishRoot(ctx, entries, wake) != nil {
				return
			}
		}
		if ready != nil {
			select {
			case <-ctx.Done():
				return
			case <-ready:
			}
		}
		if repositories == nil {
			var err error
			repositories, err = backend.GitRepositories(ctx, workspace.Path)
			if err == nil && repositories == nil {
				repositories = []string{}
			}
		}
		scan.repositories = repositories
		var id uint64
		var err error
		if previous != 0 {
			id, err = backend.RefreshIndex(ctx, workspace.Path, previous)
		} else {
			id, err = backend.OpenIndex(ctx, workspace.Path)
		}
		complete := false
		defer func() {
			if !complete && id != 0 {
				closeCtx, stop := context.WithTimeout(context.Background(), time.Second)
				defer stop()
				_ = backend.CloseIndex(closeCtx, id)
			}
		}()
		offset := 0
		var entries []protocol.IndexEntry
		for {
			var batch *pencodeTreeBatch
			select {
			case <-ctx.Done():
				return
			case batch = <-scan.buffers:
			}
			done, message := false, ""
			if err == nil {
				entries, done, message, err = backend.IndexPage(ctx, id, offset, entries[:0])
			}
			batch.index = id
			for _, entry := range entries {
				batch.entries[batch.count] = treeEntry{Path: entry.Path, Depth: entry.Depth, IsDir: entry.Directory, Openable: !entry.Directory && isTextFile(entry.Name())}
				batch.count++
			}
			markTreeGitIgnored(ctx, backend, workspace.Path, repositories, batch.entries[:batch.count])
			offset += len(entries)
			if message != "" && err == nil {
				err = fmt.Errorf("%s", message)
			}
			batch.done, batch.err = done || err != nil, err
			finished := batch.done
			success := finished && batch.err == nil
			select {
			case <-ctx.Done():
				return
			case scan.results <- batch:
				complete = success
				if wake != nil {
					wake()
				}
			}
			if finished {
				return
			}
		}
	}()
	return scan
}

// Decorations are resolved once per published page, never during rendering.
// The closest repository owns each entry, including submodules and worktrees.
func markTreeGitIgnored(ctx context.Context, backend *client.Client, root string, repositories []string, entries []treeEntry) {
	if len(repositories) == 0 || len(entries) == 0 {
		return
	}
	var names, owners, paths [pencodeTreeBatchSize]string
	var indices [pencodeTreeBatchSize]int
	var ignored [pencodeTreeBatchSize]bool
	for i, entry := range entries {
		names[i] = strings.TrimRight(root, "/") + "/" + entry.Path
		owners[i] = sourceRepositoryForPath(repositories, names[i])
	}
	for _, repository := range repositories {
		count := 0
		for i := range entries {
			if owners[i] == repository {
				paths[count], indices[count] = names[i], i
				count++
			}
		}
		if count == 0 {
			continue
		}
		matches, err := backend.CheckGitIgnore(ctx, repository, paths[:count], ignored[:0])
		if err != nil {
			continue // Missing Git or invalid metadata must not hide the file tree.
		}
		for i, match := range matches {
			entries[indices[i]].Ignored = match
		}
	}
}

func compareTreeNames(a string, aDir bool, b string, bDir bool) int {
	if aDir != bDir {
		if aDir {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

func (scan *pencodeProjectScan) publishRoot(ctx context.Context, entries []fs.DirEntry, wake func()) error {
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return compareTreeNames(a.Name(), a.IsDir(), b.Name(), b.IsDir())
	})
	offset := 0
	for offset < len(entries) {
		var batch *pencodeTreeBatch
		select {
		case <-ctx.Done():
			return ctx.Err()
		case batch = <-scan.buffers:
		}
		batch.preview = true
		for offset < len(entries) && batch.count < len(batch.entries) {
			entry := entries[offset]
			offset++
			name := entry.Name()
			if skipProjectEntry(name) {
				continue
			}
			batch.entries[batch.count] = treeEntry{Path: name, IsDir: entry.IsDir(), Openable: !entry.IsDir() && isTextFile(name)}
			batch.count++
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case scan.results <- batch:
			if wake != nil {
				wake()
			}
		}
	}
	return ctx.Err()
}

// Keep unread root siblings after the indexed prefix, so a slow subtree cannot
// hide them. Only this small tail moves when a new index page arrives.
func (scan *pencodeProjectScan) appendBatch(project *pencodeProject, batch *pencodeTreeBatch) {
	if batch.preview {
		scan.preview = true
	}
	if batch.preview || !scan.preview {
		project.Entries = append(project.Entries, batch.entries[:batch.count]...)
		return
	}
	entries := project.Entries
	remaining := scan.loaded
	for _, entry := range batch.entries[:batch.count] {
		if entry.Depth != 0 {
			continue
		}
		for remaining < len(entries) && compareTreeNames(entries[remaining].Name(), entries[remaining].IsDir, entry.Name(), entry.IsDir) <= 0 {
			remaining++
		}
	}
	if batch.done && batch.err == nil {
		remaining = len(entries)
	}
	end := scan.loaded + batch.count
	length := end + len(entries) - remaining
	if length > len(entries) {
		entries = slices.Grow(entries, length-len(entries))[:length]
	}
	copy(entries[end:], entries[remaining:len(project.Entries)])
	copy(entries[scan.loaded:end], batch.entries[:batch.count])
	clear(entries[length:])
	project.Entries = entries[:length]
	scan.loaded = end
}

func (project *pencodeProject) stopScan() {
	if project.scan != nil {
		project.scan.cancel()
		project.scan = nil
	}
}

func (model *workspaceModel) startProjectScan(project *pencodeProject, initial bool) {
	model.startProjectSnapshot(project, initial, false)
}

func (model *workspaceModel) startProjectSnapshot(project *pencodeProject, initial, refresh bool) {
	project.stopScan()
	var previous uint64
	if refresh {
		previous = project.index
	} else {
		project.index = 0
	}
	if !refresh {
		model.quickRevision++
		clear(project.Entries)
		project.Entries = project.Entries[:0]
		project.tree.entriesChanged()
	}
	if !refresh && project == model.activeProjectState() {
		model.quickCount = -1
	}
	if !refresh {
		project.Status = pencodeTreeLoading
		if !initial {
			project.Status = pencodeTreeReloading
		}
	}
	var wake func()
	if model.app != nil {
		wake = model.app.RequestRender
		if wakeAt := model.app.WakeAt; wakeAt != nil {
			// A deadline survives a concurrent frame consuming its render
			// request, so results can wake an otherwise idle application.
			wake = func() { wakeAt(time.Now()) }
		}
	}
	var ready <-chan struct{}
	if project.watch != nil {
		ready = project.watch.ready
	}
	repositories := project.treeRepositories
	if !refresh {
		repositories = nil
	}
	project.scan = newBackendProjectScan(project.backend, project.workspace, initial, previous, repositories, wake, ready)
	project.scan.staged = refresh
}

func (model *workspaceModel) pollProjectScans() bool {
	changed, pending := false, false
	for _, project := range model.projects {
		scan := project.scan
		if scan == nil {
			continue
		}
	read:
		for range pencodeTreeFrameBatches {
			select {
			case batch, ok := <-scan.results:
				if !ok {
					break read
				}
				if scan.staged {
					if !batch.preview {
						scan.entries = append(scan.entries, batch.entries[:batch.count]...)
					}
				} else {
					scan.appendBatch(project, batch)
					project.tree.entriesChanged()
					changed = true
				}
				if batch.index != 0 && !scan.staged {
					project.index = batch.index
				}
				if !scan.staged && project == model.activeProjectState() {
					model.quickCount = -1
				}
				if batch.done {
					changed = true
					if batch.err == nil && !project.treePending {
						project.treeRepositories = scan.repositories
					}
					if scan.staged && batch.err == nil {
						project.Entries, project.index = scan.entries, batch.index
						project.tree.entriesChanged()
						if project == model.activeProjectState() {
							model.quickRevision++
						}
						if scan.previous != 0 {
							backend, previous := project.backend, scan.previous
							go func() {
								ctx, cancel := context.WithTimeout(context.Background(), time.Second)
								defer cancel()
								_ = backend.CloseIndex(ctx, previous)
							}()
						}
					}
					if batch.err != nil {
						project.Status = batch.err.Error()
					} else if project.Status == pencodeTreeLoading || project.Status == pencodeTreeReloading {
						project.Status = ""
						if !scan.initial {
							project.Status = model.tr("已从磁盘重新加载")
						}
					}
					project.stopScan()
				}
				*batch = pencodeTreeBatch{}
				scan.buffers <- batch
				if project.scan == nil {
					break read
				}
			default:
				break read
			}
		}
		pending = pending || (project.scan != nil && len(scan.results) != 0)
	}
	if changed {
		model.syncActiveProjectFields()
	}
	// Bounded processing may leave results queued for the next frame. Slow
	// filesystem reads need no polling; their worker schedules the next wake.
	if pending && model.app != nil && model.app.WakeAt != nil {
		model.app.WakeAt(time.Now())
	}
	return changed
}
