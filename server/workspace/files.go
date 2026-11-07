package workspace

import (
	"crypto/rand"
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

func (s *Store) Import(targetID uint64, filename, directory string, overwrite bool) (string, error) {
	filename = filepath.Clean(filename)
	if !filepath.IsAbs(filename) || !validPath(filepath.Base(filename)) || filepath.Dir(filename) == filename {
		return "", failure(protocol.Invalid, "无效的剪贴板文件路径")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(filename))
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return "", err
	}
	defer root.Close()
	source := &project{root: root, info: protocol.Workspace{Path: parent}}
	return s.transfer(source, targetID, filepath.Base(filename), directory, false, false, overwrite)
}

func (s *Store) Transfer(sourceID, targetID uint64, from, directory string, cut, duplicate bool) (string, error) {
	source, err := s.resource(sourceID, from)
	if err != nil {
		return "", err
	}
	return s.transfer(source, targetID, from, directory, cut, duplicate, false)
}

func (s *Store) transfer(source *project, targetID uint64, from, directory string, cut, duplicate, overwrite bool) (string, error) {
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
	if existing, err := target.root.Lstat(filepath.FromSlash(destination)); err == nil && os.SameFile(info, existing) && !duplicate {
		return "", fmt.Errorf("不能用文件自身覆盖原文件")
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
			if overwrite {
				break
			}
			return "", failure(protocol.Conflict, fmt.Sprintf("名称“%s”已存在", name))
		}
		ext := ""
		if !info.IsDir() {
			ext = path.Ext(name)
		}
		destination = path.Join(directory, fmt.Sprintf("%s 副本 %d%s", strings.TrimSuffix(name, ext), index, ext))
	}
	if cut && source == target && !overwrite {
		return destination, source.root.Rename(filepath.FromSlash(from), filepath.FromSlash(destination))
	}
	buffer := make([]byte, 32<<10)
	if err := copyResource(source.root, target.root, from, destination, info, buffer, 0, overwrite); err != nil {
		return "", err
	}
	if cut {
		if err := source.root.RemoveAll(filepath.FromSlash(from)); err != nil {
			return "", fmt.Errorf("已复制，但无法删除原文件：%w", err)
		}
	}
	return destination, nil
}

func copyResource(source, target *os.Root, from, to string, info fs.FileInfo, buffer []byte, depth int, overwrite bool) (err error) {
	if depth > 256 {
		return fmt.Errorf("目录嵌套超过复制预算")
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return fmt.Errorf("只能复制普通文件或文件夹")
	}
	existing, statErr := target.Lstat(filepath.FromSlash(to))
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	if statErr == nil {
		if os.SameFile(info, existing) {
			return fmt.Errorf("不能用文件自身覆盖原文件")
		}
		if !overwrite {
			return failure(protocol.Conflict, fmt.Sprintf("名称“%s”已存在", to))
		}
		if !existing.IsDir() && !existing.Mode().IsRegular() {
			return fmt.Errorf("不能覆盖特殊文件或符号链接：%s", to)
		}
	}
	if info.IsDir() {
		// Finish the new tree before replacing a file, so a read failure cannot
		// discard the resource the user has agreed to overwrite.
		if statErr == nil && !existing.IsDir() {
			staged := path.Join(path.Dir(to), ".pencode-paste-"+rand.Text())
			if err = copyResource(source, target, from, staged, info, buffer, depth, false); err != nil {
				return err
			}
			defer target.RemoveAll(filepath.FromSlash(staged))
			backup := staged + ".backup"
			if err = target.Rename(filepath.FromSlash(to), filepath.FromSlash(backup)); err != nil {
				return err
			}
			if err = target.Rename(filepath.FromSlash(staged), filepath.FromSlash(to)); err != nil {
				if restoreErr := target.Rename(filepath.FromSlash(backup), filepath.FromSlash(to)); restoreErr != nil {
					return fmt.Errorf("%w；原文件保留在 %s：%v", err, backup, restoreErr)
				}
				return err
			}
			return target.Remove(filepath.FromSlash(backup))
		}
		created := statErr != nil
		if created {
			if err = target.Mkdir(filepath.FromSlash(to), info.Mode().Perm()); err != nil {
				return err
			}
			defer func() {
				if err != nil {
					_ = target.RemoveAll(filepath.FromSlash(to))
				}
			}()
		}
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
				if err = copyResource(source, target, path.Join(from, child.Name()), path.Join(to, child.Name()), childInfo, buffer, depth+1, overwrite); err != nil {
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
	output := to
	if overwrite {
		output = path.Join(path.Dir(to), ".pencode-paste-"+rand.Text())
	}
	out, err := target.OpenFile(filepath.FromSlash(output), os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.CopyBuffer(out, in, buffer)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = target.Remove(filepath.FromSlash(output))
		return err
	}
	if overwrite {
		defer target.Remove(filepath.FromSlash(output))
		if statErr == nil && existing.IsDir() {
			if err = target.RemoveAll(filepath.FromSlash(to)); err != nil {
				return err
			}
		}
		err = target.Rename(filepath.FromSlash(output), filepath.FromSlash(to))
	}
	return err
}
