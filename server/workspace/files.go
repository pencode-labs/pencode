package workspace

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pencode-labs/pencode/protocol"
)

func (s *Store) resource(id uint64, name string) (*project, error) {
	p := s.projects[id]
	if p == nil {
		return nil, failure(protocol.NotFound, "工作区已关闭")
	}
	if !validPath(name) {
		return nil, failure(protocol.Invalid, "无效的工作区资源路径")
	}
	return p, nil
}
func (s *Store) occupied(p *project, name string) bool {
	for _, d := range s.documents {
		if d.project == p && d.path == name {
			return true
		}
	}
	return false
}
func (s *Store) Create(id uint64, name string, directory bool) error {
	p, err := s.resource(id, name)
	if err != nil {
		return err
	}
	if s.occupied(p, name) {
		return fmt.Errorf("名称“%s”已存在", path.Base(name))
	}
	if directory {
		return p.root.Mkdir(filepath.FromSlash(name), 0755)
	}
	f, err := p.root.OpenFile(filepath.FromSlash(name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	return f.Close()
}
func (s *Store) Remove(id uint64, name string) error {
	p, err := s.resource(id, name)
	if err != nil {
		return err
	}
	if _, err := p.root.Lstat(filepath.FromSlash(name)); err != nil {
		return err
	}
	return p.root.RemoveAll(filepath.FromSlash(name))
}
func (s *Store) Rename(id uint64, from, to string) error {
	p, err := s.resource(id, from)
	if err != nil {
		return err
	}
	if !validPath(to) {
		return failure(protocol.Invalid, "无效的工作区资源路径")
	}
	if from == to {
		return nil
	}
	if s.occupied(p, to) {
		return fmt.Errorf("名称“%s”已存在", path.Base(to))
	}
	if _, err := p.root.Lstat(filepath.FromSlash(to)); err == nil {
		return fmt.Errorf("名称“%s”已存在", path.Base(to))
	} else if !os.IsNotExist(err) {
		return err
	}
	return p.root.Rename(filepath.FromSlash(from), filepath.FromSlash(to))
}

func (s *Store) Transfer(sourceID, targetID uint64, from, directory string, cut, duplicate bool) (string, error) {
	source, err := s.resource(sourceID, from)
	if err != nil {
		return "", err
	}
	target := s.projects[targetID]
	if target == nil {
		return "", failure(protocol.NotFound, "工作区已关闭")
	}
	if directory != "" && directory != "." && !validPath(directory) {
		return "", failure(protocol.Invalid, "无效的目标目录")
	}
	name := path.Base(from)
	destination := path.Join(directory, name)
	if cut && source == target && from == destination {
		return destination, nil
	}
	info, err := source.root.Lstat(filepath.FromSlash(from))
	if err != nil {
		return "", err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return "", fmt.Errorf("只能复制普通文件或文件夹")
	}
	if info.IsDir() {
		original, err := filepath.EvalSymlinks(filepath.Join(source.info.Path, filepath.FromSlash(from)))
		if err != nil {
			return "", err
		}
		parent, err := filepath.EvalSymlinks(filepath.Join(target.info.Path, filepath.FromSlash(directory)))
		if err != nil {
			return "", err
		}
		if relative, err := filepath.Rel(original, parent); err == nil && (relative == "." || filepath.IsLocal(relative)) {
			return "", fmt.Errorf("不能将文件夹粘贴到自身或子文件夹中")
		}
	}
	for index := 1; ; index++ {
		if _, err := target.root.Lstat(filepath.FromSlash(destination)); os.IsNotExist(err) && !s.occupied(target, destination) {
			break
		} else if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if !duplicate {
			return "", fmt.Errorf("名称“%s”已存在", name)
		}
		ext := ""
		if !info.IsDir() {
			ext = path.Ext(name)
		}
		destination = path.Join(directory, fmt.Sprintf("%s 副本 %d%s", strings.TrimSuffix(name, ext), index, ext))
	}
	if cut && source == target {
		return destination, source.root.Rename(filepath.FromSlash(from), filepath.FromSlash(destination))
	}
	buffer := make([]byte, 32<<10)
	if err := copyResource(source.root, target.root, from, destination, info, buffer, 0); err != nil {
		return "", err
	}
	if cut {
		if err := source.root.RemoveAll(filepath.FromSlash(from)); err != nil {
			return "", fmt.Errorf("已复制，但无法删除原文件：%w", err)
		}
	}
	return destination, nil
}

func copyResource(source, target *os.Root, from, to string, info fs.FileInfo, buffer []byte, depth int) (err error) {
	if depth > 256 {
		return fmt.Errorf("目录嵌套超过复制预算")
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return fmt.Errorf("只能复制普通文件或文件夹")
	}
	if info.IsDir() {
		if err = target.Mkdir(filepath.FromSlash(to), info.Mode().Perm()); err != nil {
			return err
		}
		defer func() {
			if err != nil {
				_ = target.RemoveAll(filepath.FromSlash(to))
			}
		}()
		f, openErr := source.Open(filepath.FromSlash(from))
		if openErr != nil {
			return openErr
		}
		defer f.Close()
		for {
			children, readErr := f.ReadDir(256)
			for _, child := range children {
				childInfo, statErr := child.Info()
				if statErr != nil {
					return statErr
				}
				if err = copyResource(source, target, path.Join(from, child.Name()), path.Join(to, child.Name()), childInfo, buffer, depth+1); err != nil {
					return err
				}
			}
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				return readErr
			}
		}
	}
	in, err := source.Open(filepath.FromSlash(from))
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := target.OpenFile(filepath.FromSlash(to), os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.CopyBuffer(out, in, buffer)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = target.Remove(filepath.FromSlash(to))
	}
	return err
}
