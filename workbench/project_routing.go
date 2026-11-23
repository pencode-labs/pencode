package workbench

import (
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/pencode-labs/pencode/connection"
)

// A launching or recovering remote window belongs to its host even while the
// temporary local backend is still serving the launcher.
func (s *Session) windowProfile() connection.Profile {
	if s == nil {
		return connection.Profile{}
	}
	if s.window != nil && s.window.restore != nil {
		return s.window.restore.Profile
	}
	if c := s.connection; c != nil {
		if c.launchProfile.Remote() {
			return c.launchProfile
		}
		return c.workspaceProfile
	}
	return connection.Profile{}
}

func (m *workspaceModel) needsProjectWindow(target connection.Profile) bool {
	current := m.session.windowProfile()
	return !current.SameHost(target) && (current.Remote() || len(m.projects) != 0)
}

func (m *workspaceModel) setWindowOpenError(err error) {
	if err != nil {
		m.setActiveStatus(m.tr("新建窗口失败：") + err.Error())
	}
}

// SetProjectActivator connects lookups to the other windows on the UI thread.
func (s *Session) SetProjectActivator(activate func(*Session, connection.Profile, string) bool) {
	s.focusOtherProject = activate
}

func sameProjectPath(profile connection.Profile, a, b string) bool {
	if a == b {
		return true
	}
	// Remote Windows backends return native workspace paths and slash-separated picker paths.
	windows := runtime.GOOS == "windows" && !profile.Remote() || len(a) >= 3 && a[1] == ':' && (a[2] == '/' || a[2] == '\\') || strings.HasPrefix(a, `\\`)
	return windows && strings.EqualFold(strings.ReplaceAll(a, `\`, "/"), strings.ReplaceAll(b, `\`, "/"))
}

func (m *workspaceModel) activateOpenProject(profile connection.Profile, directory string) bool {
	if !m.session.windowProfile().SameHost(profile) {
		return false
	}
	for _, p := range m.projects {
		if sameProjectPath(profile, p.Path, directory) {
			if p == m.activeProjectState() && !p.Loose && m.screen == pencodeWorkspace {
				return true
			}
			if p.Loose {
				p.Loose = false
				m.rememberRecentProject(p.workspace)
				m.startProjectWatch(p)
				m.startProjectScan(p, true)
			}
			m.activateProject(p)
			setPencodeEditorValue(m.fontResources, m.surface, m.activeContent())
			markPencodeRootDirtyByModel(m.fontResources, m)
			return true
		}
	}
	if m.fileIO != nil {
		for _, job := range m.fileIO.pending {
			if job.kind == fileOpenWorkspace && sameProjectPath(profile, job.root, directory) {
				return true
			}
		}
	}
	return false
}

func (m *workspaceModel) focusOpenProject(profile connection.Profile, directory string) bool {
	if m.activateOpenProject(profile, directory) {
		return true
	}
	s := m.session
	return s != nil && s.focusOtherProject != nil && s.focusOtherProject(s, profile, directory)
}

// ActivateProject also recognizes requests queued before the first window frame.
func (s *Session) ActivateProject(profile connection.Profile, directory string) bool {
	if directory == "" || !s.windowProfile().SameHost(profile) {
		return false
	}
	restoring := false
	if w := s.window; w != nil && w.restore != nil {
		for i, p := range w.restore.Projects {
			if sameProjectPath(profile, p.Path, directory) {
				w.restore.Active = i
				restoring = true
				break
			}
		}
	}
	if s.active != nil && s.active.model.activateOpenProject(profile, directory) || restoring {
		return true
	}
	if !profile.Remote() {
		for _, name := range s.externalPaths {
			if sameProjectPath(profile, name, directory) {
				return true
			}
		}
		return s.externalJob != nil && !s.externalJob.finished && sameProjectPath(profile, s.externalJob.relative, directory)
	}
	if slices.Contains(s.remoteProjects, directory) {
		return true
	}
	c := s.connection
	return c != nil && c.launchProfile.Remote() && (c.launch || c.busy || c.picker.autoOpen) && sameProjectPath(profile, c.launchProfile.Directory, directory)
}

// LocalProjectRoot returns the most specific workspace containing a local path.
func (s *Session) LocalProjectRoot(name string) string {
	if s.windowProfile().Remote() {
		return ""
	}
	var root string
	consider := func(path string) {
		if len(path) > len(root) && localPathWithin(path, name) {
			root = path
		}
	}
	if s.active != nil {
		for _, p := range s.active.model.projects {
			consider(p.Path)
		}
	}
	for _, path := range s.externalPaths {
		consider(path)
	}
	if j := s.externalJob; j != nil && !j.finished {
		consider(j.relative)
	}
	return root
}

// Local project identities follow the backend's absolute, symlink-resolved roots.
func LocalOpenPath(name string) string {
	if name == "" {
		return ""
	}
	if absolute, err := filepath.Abs(name); err == nil {
		name = absolute
	}
	if resolved, err := filepath.EvalSymlinks(name); err == nil {
		return resolved
	}
	return name
}

// QueueRemoteProject adds another directory to the window already assigned to this host.
func (s *Session) QueueRemoteProject(profile connection.Profile) bool {
	if !profile.Remote() || profile.Directory == "" || !s.windowProfile().SameHost(profile) {
		return false
	}
	s.remoteProjects = append(s.remoteProjects, profile.Directory)
	return true
}

func (s *Session) pollRemoteProjects(m *workspaceModel) bool {
	if len(s.remoteProjects) == 0 || s.window != nil && s.window.restore != nil {
		return false
	}
	c := s.connection
	if c == nil || c.launch || c.busy || c.picker.busy || c.picker.autoOpen {
		return false
	}
	directory := s.remoteProjects[0]
	s.remoteProjects = slices.Delete(s.remoteProjects, 0, 1)
	m.openRecentProject(pencodeRecentProject{Path: directory, Profile: s.windowProfile()})
	return true
}
