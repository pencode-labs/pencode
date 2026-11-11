package workbench

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/onix-engine/onix/input"
	"github.com/pencode-labs/pencode/protocol"
)

// HandleFileDrop consumes external files before ordinary editor input.
func (s *Session) HandleFileDrop(dispatch *input.Dispatch) {
	if dispatch.Event.Kind == input.EventFilesDropped {
		s.OpenPaths(dispatch.Event.File)
		dispatch.Consume()
	}
}

// OpenPaths queues native drops, shell arguments and Finder document events.
// Call on the window thread; inputs received before the first scene are retained.
func (s *Session) OpenPaths(paths ...string) {
	for _, name := range paths {
		if name != "" {
			s.externalPaths = append(s.externalPaths, name)
		}
	}
	if s.active != nil && s.active.model.app != nil {
		s.active.model.app.RequestRender()
	}
}

func (s *Session) pollExternalPaths(m *workspaceModel) bool {
	if s.externalJob != nil && !s.externalJob.finished && s.externalJob.ctx.Err() == nil {
		return false
	}
	s.externalJob = nil
	if len(s.externalPaths) == 0 {
		return false
	}
	// Remote workspaces share one backend and extension host. Open local drops
	// in a local window without replacing that connection or its unsaved files.
	if c := s.connection; c != nil && (c.workspaceProfile.Remote() || c.busy || c.launch) {
		if err := openLocalWindow(s.externalPaths); err != nil {
			m.setActiveStatus(m.tr("打开本地窗口失败：") + err.Error())
		}
		clear(s.externalPaths)
		s.externalPaths = nil
		return true
	}
	name, err := filepath.Abs(s.externalPaths[0])
	s.externalPaths[0] = ""
	s.externalPaths = s.externalPaths[1:]
	if len(s.externalPaths) == 0 {
		s.externalPaths = nil
	}
	if len(s.externalPaths) != 0 && m.app != nil {
		m.app.RequestRender()
	}
	if err == nil {
		job := &fileIOJob{kind: fileOpenExternal, relative: name}
		// Prefer the deepest open workspace containing the requested file.
		for _, p := range m.projects {
			if len(p.Path) > len(job.root) && localPathWithin(p.Path, name) {
				job.root = p.Path
				job.project = p
			}
		}
		if p := job.project; p != nil {
			relative, _ := filepath.Rel(p.Path, name)
			relative = filepath.ToSlash(relative)
			if p.Files[relative] != nil {
				m.selectProjectTab(p, relative)
				pinProjectFile(p, relative)
				return true
			}
		}
		err = m.submitFileIO(job)
		if err == nil {
			s.externalJob = job
		}
	}
	if err != nil {
		m.setActiveStatus(m.tr("打开文件失败：") + err.Error())
	}
	return true
}

func localPathWithin(root, name string) bool {
	relative, err := filepath.Rel(root, name)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func openLocalWindow(paths []string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	args := make([]string, 1, len(paths)+1)
	args[0] = "--"
	for _, name := range paths {
		absolute, err := filepath.Abs(name)
		if err != nil {
			return err
		}
		args = append(args, absolute)
	}
	command := exec.Command(executable, args...)
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}

func executeExternalOpen(j *fileIOJob) (r fileIOResult) {
	r.job = j
	name, err := filepath.EvalSymlinks(j.relative)
	if err != nil {
		r.err = err
		return
	}
	info, err := os.Stat(name)
	if err != nil {
		r.err = err
		return
	}
	root := j.root
	switch {
	case info.IsDir():
		root = name
	case !info.Mode().IsRegular():
		r.err = fmt.Errorf("不是普通文件：%s", name)
		return
	case root == "" || !localPathWithin(root, name):
		root = filepath.Dir(name)
	}
	r.workspace, r.err = j.backend.OpenWorkspace(j.ctx, root)
	if r.err != nil || info.IsDir() {
		return
	}
	r.destination, r.err = filepath.Rel(r.workspace.Path, name)
	if r.err == nil {
		r.destination = filepath.ToSlash(r.destination)
		r.document, r.content, r.err = j.backend.OpenDocument(j.ctx, r.workspace.ID, r.destination, protocol.FileMode(r.destination))
	}
	return
}

func (m *workspaceModel) acceptExternalOpen(r *fileIOResult) {
	if r.job.ctx.Err() != nil || r.job.project != nil && !m.projectLive(r.job.project) {
		return
	}
	if r.err != nil {
		m.setActiveStatus(m.tr("打开文件失败：") + r.err.Error())
		if r.workspace.ID != 0 && m.projectIndex(r.workspace.Path) < 0 {
			m.queueWorkspaceRelease(r.job.backend, r.workspace.ID)
		}
		return
	}
	m.acceptWorkspace(r.job.backend, r.workspace)
	if r.destination == "" {
		return
	}
	p := m.activeProjectState()
	m.cancelFileOpens()
	r.job.project, r.job.relative, r.job.reveal = p, r.destination, true
	m.acceptFileOpen(r)
}
