// Package workspace owns filesystem roots and versioned documents without GUI
// dependencies. A Store belongs to one protocol connection and one goroutine.
package workspace

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/pencode-labs/pencode/protocol"
	"github.com/diiyw/onix/text/piecetree"
)

type project struct {
	info protocol.Workspace
	root *os.Root
}
type document struct {
	project *project
	path    string
	version uint64
	tree    piecetree.Tree
	disk    [sha256.Size]byte
}
type Store struct {
	next      uint64
	projects  map[uint64]*project
	documents map[uint64]*document
	bytes     int
}

func (s *Store) id() uint64 { s.next++; return s.next }
func failure(code protocol.Code, text string) error {
	return &protocol.Error{Code: code, Message: text}
}

func (s *Store) Open(directory string) (protocol.Workspace, error) {
	if strings.TrimSpace(directory) == "" {
		return protocol.Workspace{}, failure(protocol.Invalid, "请选择项目目录")
	}
	if directory == "~" || strings.HasPrefix(directory, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return protocol.Workspace{}, err
		}
		if directory == "~" {
			directory = home
		} else {
			directory = filepath.Join(home, filepath.FromSlash(directory[2:]))
		}
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return protocol.Workspace{}, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return protocol.Workspace{}, err
	}
	for _, p := range s.projects {
		if p.info.Path == absolute {
			return p.info, nil
		}
	}
	if len(s.projects) >= 32 {
		return protocol.Workspace{}, failure(protocol.Busy, "工作区数量超过连接预算")
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return protocol.Workspace{}, err
	}
	info := protocol.Workspace{ID: s.id(), Path: absolute, Name: filepath.Base(absolute)}
	if s.projects == nil {
		s.projects = make(map[uint64]*project)
		s.documents = make(map[uint64]*document)
	}
	s.projects[info.ID] = &project{info: info, root: root}
	return info, nil
}

func (s *Store) CloseProject(id uint64) error {
	p := s.projects[id]
	if p == nil {
		return failure(protocol.NotFound, "工作区已关闭")
	}
	for id, d := range s.documents {
		if d.project == p {
			s.bytes -= d.tree.Len()
			delete(s.documents, id)
		}
	}
	delete(s.projects, id)
	return p.root.Close()
}
func (s *Store) Close() {
	for id, p := range s.projects {
		_ = p.root.Close()
		delete(s.projects, id)
	}
	clear(s.documents)
	s.bytes = 0
}

func validPath(name string) bool {
	return fs.ValidPath(name) && name != "." && !strings.ContainsAny(name, "\\\x00")
}

func read(root *os.Root, name string, text bool) (string, error) {
	f, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", failure(protocol.Invalid, "不是普通文件")
	}
	if !text {
		return "", nil
	}
	if info.Size() > protocol.MaxMessage-1024 {
		return "", failure(protocol.Invalid, "文件超过 64 MiB 文档预算")
	}
	var b strings.Builder
	b.Grow(int(info.Size()))
	var buffer [32 << 10]byte
	for {
		n, err := f.Read(buffer[:])
		b.Write(buffer[:n])
		if b.Len() > protocol.MaxMessage-1024 {
			return "", failure(protocol.Invalid, "文件超过文档预算")
		}
		if err == io.EOF {
			return b.String(), nil
		}
		if err != nil {
			return "", err
		}
	}
}

func (s *Store) OpenDocument(projectID uint64, name string, text bool) (protocol.Document, error) {
	p := s.projects[projectID]
	if p == nil {
		return protocol.Document{}, failure(protocol.NotFound, "工作区已关闭")
	}
	if !validPath(name) {
		return protocol.Document{}, failure(protocol.Invalid, "无效的工作区资源路径")
	}
	if text && len(s.documents) >= 128 {
		return protocol.Document{}, failure(protocol.Busy, "打开文档数量超过连接预算")
	}
	content, err := read(p.root, name, text)
	if err != nil {
		return protocol.Document{}, err
	}
	if !text {
		return protocol.Document{}, nil
	}
	if s.bytes+len(content) > 128<<20 {
		return protocol.Document{}, failure(protocol.Busy, "打开文档超过 128 MiB 连接预算")
	}
	d := &document{project: p, path: name, version: 1, disk: sha256.Sum256([]byte(content))}
	d.tree.Reset(content)
	d.tree.Reserve(protocol.ChunkSize, 128)
	id := s.id()
	s.documents[id] = d
	s.bytes += len(content)
	return protocol.Document{ID: id, Version: 1, Content: content}, nil
}

func (s *Store) Change(id, version uint64, start, end uint32, text string) (uint64, error) {
	d := s.documents[id]
	if d == nil {
		return 0, failure(protocol.NotFound, "文档已关闭")
	}
	if version != d.version {
		return 0, failure(protocol.VersionMismatch, "文档版本已改变")
	}
	a, b := int(start), int(end)
	if a > b || b > d.tree.Len() || d.tree.ByteOffset(d.tree.RuneOffset(a)) != a || d.tree.ByteOffset(d.tree.RuneOffset(b)) != b || !utf8.ValidString(text) {
		return 0, failure(protocol.Invalid, "无效的 UTF-8 编辑范围")
	}
	if d.tree.Len()-(b-a)+len(text) > protocol.MaxMessage-1024 {
		return 0, failure(protocol.Invalid, "文档超过内存预算")
	}
	if s.bytes-(b-a)+len(text) > 128<<20 {
		return 0, failure(protocol.Busy, "文档超过连接内存预算")
	}
	d.tree.Replace(a, b, text)
	s.bytes += len(text) - (b - a)
	if d.tree.RetainedBytes() > max(4<<20, d.tree.Len()*3) {
		d.tree.Compact()
	}
	d.version++
	return d.version, nil
}

func (s *Store) Save(id, version uint64) error {
	d := s.documents[id]
	if d == nil {
		return failure(protocol.NotFound, "文档已关闭")
	}
	if version != d.version {
		return failure(protocol.VersionMismatch, "保存版本已改变")
	}
	current, err := read(d.project.root, d.path, true)
	if err != nil {
		return err
	}
	if sha256.Sum256([]byte(current)) != d.disk {
		return failure(protocol.Conflict, "文件已被其他程序修改，请先比较或重新加载")
	}
	// Resolve within the opened root so saving an internal symlink updates its
	// target while an escaping link is rejected by os.Root.
	target, err := filepath.EvalSymlinks(filepath.Join(d.project.info.Path, filepath.FromSlash(d.path)))
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(d.project.info.Path, target)
	if err != nil || !filepath.IsLocal(relative) {
		return failure(protocol.Invalid, "保存路径超出工作区")
	}
	info, err := d.project.root.Stat(relative)
	if err != nil {
		return err
	}
	name := filepath.Join(filepath.Dir(relative), ".pencode-"+rand.Text())
	f, err := d.project.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer d.project.root.Remove(name)
	content := d.tree.String()
	_, err = io.WriteString(f, content)
	if err == nil {
		err = f.Chmod(info.Mode().Perm())
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := d.project.root.Rename(name, relative); err != nil {
		return err
	}
	d.disk = sha256.Sum256([]byte(content))
	return nil
}

func (s *Store) Move(id, projectID uint64, name string) error {
	d, p := s.documents[id], s.projects[projectID]
	if d == nil || p == nil {
		return failure(protocol.NotFound, "文档或工作区已关闭")
	}
	if !validPath(name) {
		return failure(protocol.Invalid, "无效的工作区资源路径")
	}
	_, err := read(p.root, name, false)
	if err != nil {
		return err
	}
	d.project, d.path = p, name
	return nil
}

func (s *Store) CloseDocument(id uint64) {
	if d := s.documents[id]; d != nil {
		s.bytes -= d.tree.Len()
		delete(s.documents, id)
	}
}

func (s *Store) ReadDirectory(id uint64, name string) ([]protocol.Entry, error) {
	p := s.projects[id]
	if p == nil {
		return nil, failure(protocol.NotFound, "工作区已关闭")
	}
	if name != "." && !validPath(name) {
		return nil, failure(protocol.Invalid, "无效的目录路径")
	}
	f, err := p.root.Open(filepath.FromSlash(name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// File.ReadDir preserves filesystem order. The UI scanner performs the one
	// required sort (directories first) rather than sorting twice.
	var result []protocol.Entry
	for {
		entries, err := f.ReadDir(256)
		if len(result)+len(entries) > 100000 {
			return nil, failure(protocol.Busy, "单个目录超过条目预算")
		}
		for _, entry := range entries {
			result = append(result, protocol.Entry{Name: entry.Name(), Mode: uint32(entry.Type())})
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func ErrorCode(err error) protocol.Code {
	var remote *protocol.Error
	if errors.As(err, &remote) {
		return remote.Code
	}
	if errors.Is(err, fs.ErrNotExist) {
		return protocol.NotFound
	}
	if errors.Is(err, protocol.ErrProtocol) {
		return protocol.Invalid
	}
	return protocol.IO
}
