package workbench

import (
	"encoding/binary"
	"errors"
	"hash/maphash"
	"path/filepath"
	"time"

	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/internal/windowstate"
)

type pencodeWindowProject struct {
	Path  string              `json:"path"`
	State pencodeProjectState `json:"state"`
	Loose bool                `json:"loose,omitempty"`
}

type pencodeWindowState struct {
	Projects []pencodeWindowProject `json:"projects,omitempty"`
	Profile  connection.Profile     `json:"profile,omitzero"`
	Active   int                    `json:"active"`
	Width    int                    `json:"width,omitempty"`
	Height   int                    `json:"height,omitempty"`
}

type pencodeWindow struct {
	due     time.Time
	store   *windowstate.Window[pencodeWindowState]
	restore *pencodeWindowState
	job     *fileIOJob
	seed    maphash.Seed
	saved   uint64
	next    int
	closed  bool
}

// ConfigureWindows claims this window's record and returns the additional
// window IDs to launch on a cold application start.
func (s *Session) ConfigureWindows(id string, restore bool) ([]string, error) {
	if s.stateDirectory == "" {
		return nil, errors.New("window state directory is unavailable")
	}
	store, others, err := windowstate.Open[pencodeWindowState](filepath.Join(s.stateDirectory, "windows"), id, restore)
	if err != nil {
		return nil, err
	}
	s.window = &pencodeWindow{store: store, seed: maphash.MakeSeed()}
	if store.Restored {
		s.window.restore = &store.Value
	}
	return others, nil
}

func (s *Session) RestoredWindowSize() (int, int) {
	if s.window != nil && s.window.restore != nil {
		return s.window.restore.Width, s.window.restore.Height
	}
	return 0, 0
}

// SetWindowOpener connects requests to the owning desktop application.
// The callback queues creation outside the current window's rendering frame.
func (s *Session) SetWindowOpener(open func([]string, connection.Profile) error) {
	s.openWindow = open
}

func (s *Session) OpenWindow(paths []string) error {
	return s.openWindowWithProfile(paths, connection.Profile{})
}

func (s *Session) openWindowWithProfile(paths []string, profile connection.Profile) error {
	if s.openWindow == nil {
		return errors.New("window opener is unavailable")
	}
	return s.openWindow(paths, profile)
}

// Advance one workspace at a time through the normal asynchronous operations.
// Retain the old snapshot until all restoration work has settled.
func (s *Session) pollWindowRestore(m *workspaceModel) bool {
	w := s.window
	if w == nil || w.restore == nil {
		return false
	}
	if c := s.connection; c != nil && (c.launch || c.busy || c.picker.busy) {
		return false
	}
	if w.job != nil && !w.job.finished {
		return false
	}
	if m.fileIO != nil {
		for _, job := range m.fileIO.pending {
			if job.kind == fileRestore || job.kind == fileOpenWorkspace {
				return false
			}
		}
	}
	if w.next < len(w.restore.Projects) {
		saved := &w.restore.Projects[w.next]
		if m.projectIndex(saved.Path) >= 0 {
			w.next++
			return true
		}
		profile := w.restore.Profile
		if profile.Remote() {
			if !m.connectionConnected(profile) {
				// Failed authentication waits for a manual retry, without
				// overwriting the saved window or retrying every frame.
				if s.connection == nil || s.connection.messageError {
					return false
				}
				m.beginConnection(profile, false)
				return true
			}
			if m.backend != s.connection.links[profile.Endpoint()].backend {
				m.openRecentRemoteProject(profile, saved.Path)
				return true
			}
		}
		w.job = &fileIOJob{kind: fileOpenWorkspace, root: saved.Path}
		if err := m.submitFileIO(w.job); err != nil {
			w.job = nil
			return false
		}
		w.next++
		return true
	}
	if active := w.restore.Active; active >= 0 && active < len(w.restore.Projects) {
		if index := m.projectIndex(w.restore.Projects[active].Path); index >= 0 {
			m.activateProject(m.projects[index])
		}
	}
	w.restore, w.job = nil, nil
	w.store.Value = pencodeWindowState{}
	return true
}

func (s *Session) windowProject(path string) *pencodeWindowProject {
	if s == nil || s.window == nil || s.window.restore == nil {
		return nil
	}
	for i := range s.window.restore.Projects {
		saved := &s.window.restore.Projects[i]
		if saved.Path == path {
			return saved
		}
	}
	return nil
}

func windowHash[T comparable](h *maphash.Hash, value T) {
	var data [8]byte
	binary.LittleEndian.PutUint64(data[:], maphash.Comparable(h.Seed(), value))
	_, _ = h.Write(data[:])
}

// No snapshots, JSON, or filesystem calls occur while state is unchanged.
func (w *pencodeWindow) fingerprint(m *workspaceModel) uint64 {
	var h maphash.Hash
	h.SetSeed(w.seed)
	windowHash(&h, m.activeProjectIndex)
	windowHash(&h, m.viewportWidth)
	windowHash(&h, m.viewportHeight)
	windowHash(&h, m.sidebarWidthStyle)
	windowHash(&h, m.sourceChangesWidthStyle)
	windowHash(&h, m.bottom.heightStyle)
	windowHash(&h, m.bottom.active)
	windowHash(&h, m.bottom.filter)
	if m.bottom.source != nil {
		windowHash(&h, m.bottom.source.Name)
	}
	if c := m.session.connection; c != nil {
		windowHash(&h, c.workspaceProfile)
	}
	for _, p := range m.projects {
		windowHash(&h, p.Path)
		windowHash(&h, p.Loose)
		windowHash(&h, p.sidebarCollapsed)
		windowHash(&h, p.ActivePath)
		windowHash(&h, p.AllDirectoriesCollapsed)
		windowHash(&h, p.tree.position)
		windowHash(&h, p.sourceControl.Snapshot.Repository.Root)
		windowHash(&h, p.sourceControl.RepositoriesWidthStyle)
		windowHash(&h, p.sourceControl.RepositoriesCollapsed)
		var expanded uint64
		for path, open := range p.ExpandedDirs {
			if open {
				expanded ^= maphash.String(w.seed, path)
			}
		}
		windowHash(&h, expanded)
		for _, path := range p.Tabs {
			windowHash(&h, path)
			if f := p.Files[path]; f != nil {
				windowHash(&h, f.editorID)
				windowHash(&h, f.editorView)
				windowHash(&h, f.forcedText)
				windowHash(&h, f.editorSplit)
				windowHash(&h, f.Preview)
			}
		}
	}
	return h.Sum64()
}

func (s *Session) checkpointWindow(m *workspaceModel) {
	w := s.window
	if w == nil || w.closed || w.restore != nil {
		return
	}
	if w.fingerprint(m) == w.saved {
		w.due = time.Time{}
		return
	}
	now := time.Now()
	if w.due.IsZero() {
		w.due = now.Add(300 * time.Millisecond)
		if m.app != nil && m.app.WakeAt != nil {
			m.app.WakeAt(w.due)
		}
		return
	}
	if now.Before(w.due) {
		return
	}
	state := pencodeWindowState{Active: m.activeProjectIndex, Width: int(m.viewportWidth), Height: int(m.viewportHeight)}
	if c := s.connection; c != nil {
		state.Profile = c.workspaceProfile
	}
	for _, p := range m.projects {
		state.Projects = append(state.Projects, pencodeWindowProject{Path: p.Path, Loose: p.Loose, State: m.captureProjectState(p)})
	}
	if err := w.store.Save(&state); err != nil {
		m.logOutput("pencode", m.tr("保存窗口状态失败：")+err.Error())
		w.due = now.Add(time.Second)
		if m.app != nil && m.app.WakeAt != nil {
			m.app.WakeAt(w.due)
		}
		return
	}
	w.saved, w.due = w.fingerprint(m), time.Time{}
}

func (s *Session) forgetWindow(m *workspaceModel) {
	if s == nil || s.window == nil || s.window.closed {
		return
	}
	if s.updates != nil && s.updates.requested {
		return
	}
	if err := s.window.store.Forget(); err != nil {
		m.logOutput("pencode", m.tr("移除窗口状态失败：")+err.Error())
		return
	}
	s.window.closed = true
}
