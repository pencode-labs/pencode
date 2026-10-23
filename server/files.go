package server

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/diiyw/onix/projects/pencode/protocol"
)

func readServiceFile(root, name string, links bool, limit int) ([]byte, error) {
	if !filepath.IsLocal(name) {
		return nil, protocol.ErrProtocol
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	info, err := r.Lstat(name)
	if links && os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if links && info.Mode()&os.ModeSymlink != 0 {
		v, err := r.Readlink(name)
		return []byte(v), err
	}
	if links && (!info.Mode().IsRegular() || info.Size() > int64(limit)) {
		return nil, errors.New("file exceeds read budget")
	}
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return nil, errors.New("file exceeds read budget")
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if len(data) > limit {
		return nil, errors.New("file exceeds read budget")
	}
	return data, err
}
func saveServiceFile(root, name, before, after string) error {
	if !filepath.IsLocal(name) {
		return protocol.ErrProtocol
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	info, err := r.Lstat(name)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	mode := os.FileMode(0644)
	if info != nil {
		if !info.Mode().IsRegular() {
			return errors.New("result must be a regular file")
		}
		mode = info.Mode().Perm()
	}
	data, err := readServiceFile(root, name, true, protocol.MaxMessage-4096)
	if err != nil {
		return err
	}
	if string(data) != before {
		return errors.New("文件已被外部修改，请重新打开差异文件。")
	}
	var tmp string
	var f *os.File
	for n := 0; n < 8; n++ {
		tmp = filepath.Join(filepath.Dir(name), fmt.Sprintf(".pencode-save-%d-%d", time.Now().UnixNano(), n))
		f, err = r.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err == nil {
			break
		}
	}
	if err != nil {
		return err
	}
	defer r.Remove(tmp)
	err = f.Chmod(mode)
	if err == nil {
		_, err = f.WriteString(after)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return r.Rename(tmp, name)
}

func (s *services) file(op protocol.Operation, root string, d *protocol.Decoder, e *protocol.Encoder) error {
	switch op {
	case protocol.FileRead:
		name, flags, limit := d.String(), d.U32(), int(d.U32())
		if flags > 1 || limit <= 0 || limit > protocol.MaxMessage-4096 {
			return protocol.ErrProtocol
		}
		if err := d.Done(); err != nil {
			return err
		}
		data, err := readServiceFile(root, name, flags != 0, limit)
		if err != nil {
			return err
		}
		e.Bytes(data)
	case protocol.FileSave:
		name, before, after := d.String(), d.String(), d.String()
		if err := d.Done(); err != nil {
			return err
		}
		return saveServiceFile(root, name, before, after)
	}
	return nil
}
