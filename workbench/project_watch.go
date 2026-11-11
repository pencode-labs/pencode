package workbench

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pencode-labs/pencode/peb"
	"github.com/pencode-labs/pencode/protocol"
)

const (
	projectWatchRetryInitial = 100 * time.Millisecond
	projectWatchRetryMax     = 5 * time.Second
	projectWatchFailed       = "文件变化同步失败，将自动重试："
)

type pencodeProjectWatch struct {
	cancel    context.CancelFunc
	errors    chan error
	ready     chan struct{}
	files     [128]protocol.FileEvent
	fileCount int
	mu        sync.Mutex
	pending   atomic.Uint32
	reset     bool
}

func (m *workspaceModel) startProjectWatch(project *pencodeProject) {
	project.stopWatch()
	ctx, cancel := context.WithCancel(context.Background())
	w := &pencodeProjectWatch{cancel: cancel, errors: make(chan error, 1), ready: make(chan struct{})}
	project.watch = w
	backend, root, wake := project.backend, project.Path, m.backgroundWake()
	subscription := backend.WatchFiles(root)
	var events []protocol.FileEvent
	go w.run(ctx, func(ctx context.Context) (protocol.WorkspaceChange, error) {
		batch, err := subscription.Next(ctx, events[:0])
		if err == nil {
			events = batch.Files
			w.collect(batch)
		}
		return batch.Changes, err
	}, wake)
}

func (w *pencodeProjectWatch) run(ctx context.Context, next func(context.Context) (protocol.WorkspaceChange, error), wake func()) {
	initialized := false
	failed := false
	retryDelay := projectWatchRetryInitial
	defer func() {
		if !initialized {
			close(w.ready)
		}
	}()
	for {
		changes, err := next(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if !initialized {
				close(w.ready)
				initialized = true
			}
			if !failed {
				// Reconcile once on failure, then once after recovery.
				// Repeated retries do not keep rescanning an idle project.
				failed = true
				w.pending.Or(uint32(protocol.WorkspaceTreeChanged | protocol.WorkspaceGitChanged | protocol.WorkspaceRepositoriesChanged))
				select {
				case w.errors <- err:
				default:
				}
				if wake != nil {
					wake()
				}
			}
			if !waitProjectWatchRetry(ctx, retryDelay) {
				return
			}
			retryDelay = min(retryDelay*2, projectWatchRetryMax)
			continue
		}
		if failed {
			// Keep only the latest status if recovery beats the UI frame.
			select {
			case <-w.errors:
			default:
			}
			w.errors <- nil
			if wake != nil {
				wake()
			}
		}
		failed = false
		retryDelay = projectWatchRetryInitial
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
}

func waitProjectWatchRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
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
			var files [128]protocol.FileEvent
			w.mu.Lock()
			count, reset := w.fileCount, w.reset
			copy(files[:], w.files[:count])
			clear(w.files[:count])
			w.fileCount, w.reset = 0, false
			w.mu.Unlock()
			if count != 0 || reset {
				m.distributeFileChanges(project, files[:count], reset)
			}
			project.treePending = project.treePending || changes&protocol.WorkspaceTreeChanged != 0
			if changes&protocol.WorkspaceGitChanged != 0 {
				project.sourceControl.RefreshPending = true
			}
			if changes&protocol.WorkspaceRepositoriesChanged != 0 {
				project.sourceControl.RepositoriesLoaded = false
			}
			select {
			case err := <-w.errors:
				if err != nil {
					m.setProjectStatus(project, projectWatchFailed+err.Error())
					changed = true
				} else if strings.HasPrefix(project.Status, projectWatchFailed) {
					m.setProjectStatus(project, "")
					changed = true
				}
			default:
			}
		}
		m.reloadExternalFiles(project)
		// Never restart a scan in flight. Keep one follow-up snapshot instead.
		if project.treePending && project.scan == nil && !m.quickOpen {
			project.treePending = false
			m.startProjectSnapshot(project, false, true)
			changed = true
		}
	}
	return changed
}

func (w *pencodeProjectWatch) collect(batch protocol.FileChanges) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reset = w.reset || batch.Reset
	for _, file := range batch.Files {
		found := false
		for i := range w.fileCount {
			if w.files[i].Path == file.Path {
				w.files[i].Op |= file.Op
				w.files[i].Directory = w.files[i].Directory || file.Directory
				found = true
				break
			}
		}
		if found {
			continue
		}
		if w.fileCount == len(w.files) {
			w.reset = true
			continue
		}
		w.files[w.fileCount] = file
		w.fileCount++
	}
}

func (m *workspaceModel) distributeFileChanges(project *pencodeProject, files []protocol.FileEvent, reset bool) {
	for _, file := range project.Files {
		if file.Dirty {
			continue
		}
		if reset {
			file.externalChanged = true
			continue
		}
		for _, event := range files {
			if event.Path == file.Path || event.Directory && strings.HasPrefix(file.Path, event.Path+"/") {
				file.externalChanged = true
				break
			}
		}
	}
	var events [128]peb.FileEvent
	for i, file := range files {
		events[i] = peb.FileEvent{Path: file.Path, Op: peb.FileOperation(file.Op), Directory: file.Directory}
	}
	batch := peb.FileChanges{Root: project.Path, Reset: reset, Files: events[:len(files)]}
	for _, session := range m.actions.sessions {
		session.NotifyFiles(batch)
	}
	for _, document := range m.customDocuments {
		if document.project == project && document.process != nil {
			document.process.NotifyFiles(batch)
		}
	}
}

func (m *workspaceModel) reloadExternalFiles(project *pencodeProject) {
	if m.fileIO != nil {
		for _, job := range m.fileIO.pending {
			if job.project == project || job.target == project {
				return
			}
		}
	}
	var job *fileIOJob
	for _, name := range project.Tabs {
		file := project.Files[name]
		if file == nil || !file.externalChanged {
			continue
		}
		file.externalChanged = false
		if file.Dirty || !file.text() && m.fileEditor(file) != &pencodeImageEditor {
			continue
		}
		if job == nil {
			job = &fileIOJob{kind: fileReload, project: project, workspace: project.workspace.ID, external: true}
		}
		job.reload = append(job.reload, fileReloadResult{file: file, relative: name, before: file.content(), previous: file.document, mode: file.openMode()})
	}
	if job != nil {
		if err := m.submitFileIO(job); err != nil {
			for _, item := range job.reload {
				item.file.externalChanged = true
			}
			m.setProjectStatus(project, err.Error())
		}
	}
}
