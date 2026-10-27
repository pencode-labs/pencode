package workbench

import (
	"context"
	"sync/atomic"

	"github.com/pencode-labs/pencode/protocol"
)

type pencodeProjectWatch struct {
	cancel  context.CancelFunc
	pending atomic.Uint32
	errors  chan error
	ready   chan struct{}
}

func (m *workspaceModel) startProjectWatch(project *pencodeProject) {
	project.stopWatch()
	ctx, cancel := context.WithCancel(context.Background())
	w := &pencodeProjectWatch{cancel: cancel, errors: make(chan error, 1), ready: make(chan struct{})}
	project.watch = w
	backend, root, wake := project.backend, project.Path, m.backgroundWake()
	go func() {
		initialized := false
		defer func() {
			if !initialized {
				close(w.ready)
			}
		}()
		for {
			changes, err := backend.WatchWorkspace(ctx, root)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				w.errors <- err
				if wake != nil {
					wake()
				}
				return
			}
			if !initialized {
				// The initial scan waits for the native watch to be installed.
				close(w.ready)
				initialized = true
				changes &^= protocol.WorkspaceTreeChanged
			}
			if w.pending.Or(uint32(changes)) == 0 && wake != nil {
				wake()
			}
		}
	}()
}

func (project *pencodeProject) stopWatch() {
	if project.watch != nil {
		project.watch.cancel()
		project.watch = nil
	}
	project.treePending = false
}

func (m *workspaceModel) pollProjectWatches() bool {
	changed := false
	for _, project := range m.projects {
		if w := project.watch; w != nil {
			changes := protocol.WorkspaceChange(w.pending.Swap(0))
			project.treePending = project.treePending || changes&protocol.WorkspaceTreeChanged != 0
			if changes&protocol.WorkspaceGitChanged != 0 {
				project.sourceControl.RefreshPending = true
			}
			if changes&protocol.WorkspaceTreeChanged != 0 {
				project.sourceControl.RepositoriesLoaded = false
			}
			select {
			case err := <-w.errors:
				m.setProjectStatus(project, "文件事件监听失败："+err.Error())
				changed = true
			default:
			}
		}
		// Never restart a scan in flight. Keep one follow-up snapshot instead.
		if project.treePending && project.scan == nil && !m.quickOpen {
			project.treePending = false
			m.startProjectScan(project, false)
			changed = true
		}
	}
	return changed
}
