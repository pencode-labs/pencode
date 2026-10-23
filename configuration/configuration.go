// Package configuration owns the editor's user resources on the workspace host.
package configuration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"time"
)

const (
	MaxBytes   = 16 << 20
	MaxFiles   = 4096
	MaxEntries = 16384
)

type Snapshot struct {
	Directory string
	Files     map[string][]byte
}

func (s Snapshot) Open(name string) (fs.File, error) {
	data, err := s.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return &snapshotFile{Reader: bytes.NewReader(data), name: path.Base(name)}, nil
}

func (s Snapshot) ReadFile(name string) ([]byte, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrInvalid}
	}
	if data, ok := s.Files[name]; ok {
		return data, nil
	}
	return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
}

type snapshotFile struct {
	*bytes.Reader
	name string
}

func (f *snapshotFile) Close() error               { return nil }
func (f *snapshotFile) Stat() (fs.FileInfo, error) { return snapshotInfo{f.name, f.Size()}, nil }

type snapshotInfo struct {
	name string
	size int64
}

func (i snapshotInfo) Name() string     { return i.name }
func (i snapshotInfo) Size() int64      { return i.size }
func (snapshotInfo) Mode() fs.FileMode  { return 0o444 }
func (snapshotInfo) ModTime() time.Time { return time.Time{} }
func (snapshotInfo) IsDir() bool        { return false }
func (snapshotInfo) Sys() any           { return nil }

func Directory() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "pencode", "resources"), nil
}

func Read(ctx context.Context, directory string) (Snapshot, error) {
	s := Snapshot{Directory: directory, Files: make(map[string][]byte)}
	total := 0
	entries := 0
	read := func(name string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		file, err := os.Open(filepath.Join(directory, filepath.FromSlash(name)))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("configuration %s is not a regular file", name)
		}
		data, err := io.ReadAll(io.LimitReader(file, int64(MaxBytes-total)+1))
		if err != nil {
			return err
		}
		total += len(data)
		if total > MaxBytes || len(s.Files) >= MaxFiles {
			return fmt.Errorf("configuration exceeds resource budget")
		}
		s.Files[name] = data
		return nil
	}
	for _, name := range [...]string{"settings.json", "highlights.json"} {
		if err := read(name); err != nil {
			return s, err
		}
	}
	err := filepath.WalkDir(filepath.Join(directory, "themes"), func(name string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		entries++
		if entries > MaxEntries {
			return fmt.Errorf("configuration tree exceeds entry budget")
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if ext := filepath.Ext(name); ext != ".json" && ext != ".jsonc" {
			return nil
		}
		relative, err := filepath.Rel(directory, name)
		if err != nil {
			return err
		}
		return read(filepath.ToSlash(relative))
	})
	return s, err
}

// Save publishes a complete settings object; a failed write preserves the old file.
func Save(ctx context.Context, directory string, data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > MaxBytes || !json.Valid(data) || data[0] != '{' {
		return fmt.Errorf("invalid editor settings")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(directory, "settings.json"))
}
