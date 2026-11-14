// Package workspace owns filesystem roots and versioned documents without GUI
// dependencies. A Store belongs to one protocol connection and one goroutine.
package workspace

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/onix-engine/onix/text/piecetree"
	"github.com/pencode-labs/pencode/protocol"
)

type project struct {
	root *os.Root
	info protocol.Workspace
}
type document struct {
	project *project
	paged   *pagedDocument
	path    string
	tree    piecetree.Tree
	version uint64
	disk    [sha256.Size]byte
}
type Store struct {
	projects  map[uint64]*project
	documents map[uint64]*document
	next      uint64
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
			if d.paged != nil {
				d.paged.close()
			}
			delete(s.documents, id)
		}
	}
	delete(s.projects, id)
	return p.root.Close()
}
func (s *Store) Close() {
	for _, d := range s.documents {
		if d.paged != nil {
			d.paged.close()
		}
	}
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

func openFile(root *os.Root, name string) (*os.File, fs.FileInfo, error) {
	f, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, failure(protocol.Invalid, "不是普通文件")
	}
	return f, info, nil
}

var errUnsupportedText = errors.New("binary or unsupported text encoding")

func read(root *os.Root, name string, mode protocol.DocumentMode) (string, [sha256.Size]byte, error) {
	f, info, err := openFile(root, name)
	if err != nil {
		return "", [sha256.Size]byte{}, err
	}
	defer f.Close()
	if mode == protocol.DocumentFile {
		return "", [sha256.Size]byte{}, nil
	}
	buffer := fileBuffers.Get().(*fileBuffer)
	defer fileBuffers.Put(buffer)
	// Reject common binary formats before allocating a file-sized string.
	if mode == protocol.DocumentText {
		n, err := f.ReadAt(buffer.data[:512], 0)
		if err != nil && err != io.EOF {
			return "", [sha256.Size]byte{}, err
		}
		if bytes.IndexByte(buffer.data[:n], 0) >= 0 {
			return "", [sha256.Size]byte{}, errUnsupportedText
		}
	}
	var b strings.Builder
	b.Grow(int(info.Size()))
	digest, err := buffer.read(f, &b)
	if err != nil {
		return "", digest, err
	}
	content := b.String()
	if mode == protocol.DocumentText && (strings.IndexByte(content, 0) >= 0 || !utf8.ValidString(content)) {
		return "", digest, errUnsupportedText
	}
	if mode == protocol.DocumentTextForced {
		content = strings.ToValidUTF8(content, "\uFFFD")
		if len(content) > protocol.MaxMessage-1024 {
			return "", digest, failure(protocol.Invalid, "解码后的文本超过文档预算")
		}
	}
	return content, digest, nil
}

func (s *Store) OpenDocument(projectID uint64, name string, mode protocol.DocumentMode) (protocol.Document, error) {
	p := s.projects[projectID]
	if p == nil {
		return protocol.Document{}, failure(protocol.NotFound, "工作区已关闭")
	}
	if !validPath(name) {
		return protocol.Document{}, failure(protocol.Invalid, "无效的工作区资源路径")
	}
	if mode > protocol.DocumentTextForced {
		return protocol.Document{}, protocol.ErrProtocol
	}
	if mode != protocol.DocumentFile && len(s.documents) >= 128 {
		return protocol.Document{}, failure(protocol.Busy, "打开文档数量超过连接预算")
	}
	if mode != protocol.DocumentFile {
		f, info, err := openFile(p.root, name)
		if err != nil {
			return protocol.Document{}, err
		}
		if info.Size() > protocol.TextPagingThreshold {
			paged := &pagedDocument{file: f, info: info, size: info.Size(), forced: mode == protocol.DocumentTextForced,
				pieces: []filePiece{{end: info.Size()}}}
			content, err := paged.readPage(0)
			if err != nil {
				paged.close()
				if err == errUnsupportedText {
					return protocol.Document{}, nil
				}
				return protocol.Document{}, err
			}
			id := s.id()
			s.documents[id] = &document{project: p, path: name, version: 1, paged: paged}
			return protocol.Document{ID: id, Version: 1, Content: content, Page: paged.page}, nil
		}
		f.Close()
	}
	content, digest, err := read(p.root, name, mode)
	if err == errUnsupportedText {
		return protocol.Document{}, nil
	}
	if err != nil {
		return protocol.Document{}, err
	}
	if mode == protocol.DocumentFile {
		return protocol.Document{}, nil
	}
	if s.bytes+len(content) > 128<<20 {
		return protocol.Document{}, failure(protocol.Busy, "打开文档超过 128 MiB 连接预算")
	}
	d := &document{project: p, path: name, version: 1, disk: digest}
	d.tree.Reset(content)
	id := s.id()
	s.documents[id] = d
	s.bytes += len(content)
	return protocol.Document{ID: id, Version: 1, Content: content}, nil
}

func (s *Store) Change(id, version, offset uint64, start, end uint32, text string) (uint64, error) {
	d := s.documents[id]
	if d == nil {
		return 0, failure(protocol.NotFound, "文档已关闭")
	}
	if version != d.version {
		return 0, failure(protocol.VersionMismatch, "文档版本已改变")
	}
	if d.paged != nil {
		if err := d.paged.change(offset, start, end, text); err != nil {
			return 0, err
		}
		d.version++
		return d.version, nil
	}
	if offset != 0 {
		return 0, protocol.ErrProtocol
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
	if d.version == 1 {
		d.tree.Reserve(protocol.ChunkSize, 128)
	}
	d.tree.Replace(a, b, text)
	s.bytes += len(text) - (b - a)
	if d.tree.RetainedBytes() > max(4<<20, d.tree.Len()*3) {
		d.tree.Compact()
	}
	d.version++
	return d.version, nil
}

// SaveTemporaryPrefix identifies files staged for an atomic save.
const SaveTemporaryPrefix = ".pencode-save-"

func (s *Store) Save(id, version uint64) error {
	d := s.documents[id]
	if d == nil {
		return failure(protocol.NotFound, "文档已关闭")
	}
	if version != d.version {
		return failure(protocol.VersionMismatch, "保存版本已改变")
	}
	buffer := fileBuffers.Get().(*fileBuffer)
	defer fileBuffers.Put(buffer)
	if d.paged != nil {
		if err := d.paged.check(d.project.root, d.path); err != nil {
			return err
		}
	} else {
		current, _, err := openFile(d.project.root, d.path)
		if err != nil {
			return err
		}
		digest, err := buffer.read(current, nil)
		current.Close()
		if err != nil {
			return err
		}
		if digest != d.disk {
			return failure(protocol.Conflict, "文件已被其他程序修改，请先比较或重新加载")
		}
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
	name := filepath.Join(filepath.Dir(relative), SaveTemporaryPrefix+rand.Text())
	f, err := d.project.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer d.project.root.Remove(name)
	buffer.hash.Reset()
	size := int64(d.tree.Len())
	if d.paged != nil {
		size = d.paged.size
	}
	for offset := int64(0); offset < size; {
		n := int(min(int64(len(buffer.data)), size-offset))
		if d.paged != nil {
			_, err = d.paged.readAt(buffer.data[:n], offset)
		} else {
			_, err = d.tree.ReadAt(buffer.data[:n], offset)
		}
		if err != nil {
			break
		}
		if _, err = f.Write(buffer.data[:n]); err != nil {
			break
		}
		buffer.hash.Write(buffer.data[:n])
		offset += int64(n)
	}
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
	if d.paged != nil {
		if err := d.paged.check(d.project.root, d.path); err != nil {
			return err
		}
	}
	if err := d.project.root.Rename(name, relative); err != nil {
		return err
	}
	if p := d.paged; p != nil {
		file, info, err := openFile(d.project.root, relative)
		if err != nil {
			return err
		}
		p.close()
		p.file, p.info, p.edits, p.editEnd = file, info, nil, 0
		if cap(p.pieces) > pagedPieceSpareLimit {
			p.pieces = nil
		} else {
			p.pieces = p.pieces[:0]
		}
		if p.size > 0 {
			p.pieces = append(p.pieces, filePiece{end: p.size})
		}
	}
	buffer.hash.Sum(buffer.sum[:0])
	d.disk = buffer.sum
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
	_, _, err := read(p.root, name, protocol.DocumentFile)
	if err != nil {
		return err
	}
	d.project, d.path = p, name
	return nil
}

func (s *Store) CloseDocument(id uint64) {
	if d := s.documents[id]; d != nil {
		s.bytes -= d.tree.Len()
		if d.paged != nil {
			d.paged.close()
		}
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
