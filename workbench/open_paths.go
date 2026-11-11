package workbench

import (
	"fmt"
	"os"
	"path/filepath"
)

type pencodeOpenPath struct {
	directory string
	file      string
}

// OpenPaths queues local files and directories before the session starts.
// Files use their parent directory as the workspace and open as pinned tabs.
func (s *Session) OpenPaths(paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	opening := make([]pencodeOpenPath, 0, len(paths))
	for _, name := range paths {
		absolute, err := filepath.Abs(name)
		if err != nil {
			return err
		}
		info, err := os.Stat(absolute)
		if err != nil {
			return fmt.Errorf("打开 %q 失败：%w", name, err)
		}
		target := pencodeOpenPath{directory: absolute}
		if !info.IsDir() {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("无法打开非普通文件 %q", name)
			}
			target.directory, target.file = filepath.Dir(absolute), filepath.Base(absolute)
		}
		opening = append(opening, target)
	}
	s.opening = opening
	return nil
}

func (m *workspaceModel) pollOpenPaths() bool {
	s := m.session
	if s == nil || len(s.opening) == 0 || m.fileIO != nil && len(m.fileIO.pending) != 0 {
		return false
	}
	target := s.opening[0]
	s.opening[0] = pencodeOpenPath{}
	s.opening = s.opening[1:]
	if len(s.opening) == 0 {
		s.opening = nil
	}
	if err := m.loadPath(target.directory, target.file); err != nil {
		m.setActiveStatus(err.Error())
	}
	return true
}
