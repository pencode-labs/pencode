package workbench

import (
	"context"
	"fmt"
	"github.com/diiyw/onix/projects/pencode/protocol"
	"time"

	"github.com/diiyw/onix/projects/pencode/client"
)

const (
	pencodeTreeBatchSize    = 256
	pencodeTreeFrameBatches = 4
	pencodeTreeLoading      = "正在加载项目目录…"
	pencodeTreeReloading    = "正在从磁盘重新加载…"
)

// Only the foreground model owns project state. The worker transfers entries
// through two reusable buffers, bounding both memory and work per frame.
type pencodeTreeBatch struct {
	index   uint64
	entries [pencodeTreeBatchSize]treeEntry
	count   int
	done    bool
	err     error
}

type pencodeProjectScan struct {
	cancel  context.CancelFunc
	results chan *pencodeTreeBatch
	buffers chan *pencodeTreeBatch
	initial bool
}

func newBackendProjectScan(backend *client.Client, root string, initial bool, wake func()) *pencodeProjectScan {
	ctx, cancel := context.WithCancel(context.Background())
	scan := &pencodeProjectScan{cancel: cancel, initial: initial, results: make(chan *pencodeTreeBatch, 2), buffers: make(chan *pencodeTreeBatch, 2)}
	for range 2 {
		scan.buffers <- new(pencodeTreeBatch)
	}
	go func() {
		defer close(scan.results)
		id, err := backend.OpenIndex(ctx, root)
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
				batch.entries[batch.count] = treeEntry{Path: entry.Path, Name: entry.Name, Depth: entry.Depth, IsDir: entry.Directory, Openable: !entry.Directory && isTextFile(entry.Name)}
				batch.count++
			}
			offset += len(entries)
			if message != "" && err == nil {
				err = fmt.Errorf("%s", message)
			}
			batch.done, batch.err = done || err != nil, err
			complete = batch.done
			select {
			case <-ctx.Done():
				return
			case scan.results <- batch:
				if wake != nil {
					wake()
				}
			}
			if complete {
				return
			}
		}
	}()
	return scan
}

func (project *pencodeProject) stopScan() {
	if project.scan != nil {
		project.scan.cancel()
		project.scan = nil
	}
}

func (model *workspaceModel) startProjectScan(project *pencodeProject, initial bool) {
	project.stopScan()
	project.index = 0
	model.quickRevision++
	clear(project.Entries)
	project.Entries = project.Entries[:0]
	project.tree.revision++
	if project == model.activeProjectState() {
		model.quickCount = -1
	}
	project.Status = pencodeTreeLoading
	if !initial {
		project.Status = pencodeTreeReloading
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
	project.scan = newBackendProjectScan(project.backend, project.Path, initial, wake)
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
				project.Entries = append(project.Entries, batch.entries[:batch.count]...)
				if batch.index != 0 {
					project.index = batch.index
				}
				changed = true
				if project == model.activeProjectState() {
					model.quickCount = -1
				}
				if batch.done {
					if batch.err != nil {
						project.Status = batch.err.Error()
					} else if project.Status == pencodeTreeLoading || project.Status == pencodeTreeReloading {
						project.Status = ""
						if !scan.initial {
							project.Status = "已从磁盘重新加载"
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
