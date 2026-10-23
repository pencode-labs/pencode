package extensionhost

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
)

// Upload retains one open file and accepts ordered chunks. The containing
// connection serializes its use and owns cleanup, including abandoned uploads.
type Upload struct {
	Directory string
	root      *os.Root
	file      *os.File
	name      string
	offset    uint64
	bytes     int
	paths     map[string]bool
	sealed    bool
}

func Begin(directory string) (*Upload, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(directory, "_install-")
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(stage)
	if err != nil {
		os.RemoveAll(stage)
		return nil, err
	}
	return &Upload{Directory: stage, root: root, paths: make(map[string]bool)}, nil
}

func (u *Upload) Write(name string, offset uint64, mode uint32, final bool, data []byte) error {
	if u.sealed || !ValidResourcePath(name, false) || len(name) > 4096 || u.bytes+len(data) > MaxPackageBytes {
		return fmt.Errorf("invalid extension upload or package exceeds 256 MiB")
	}
	perm := os.FileMode(mode)
	if perm.IsDir() {
		if u.file != nil || offset != 0 || !final || len(data) != 0 || u.paths[name] || len(u.paths) >= MaxPackageFiles {
			return fmt.Errorf("invalid extension directory")
		}
		if err := u.root.Mkdir(name, 0o755); err != nil {
			return err
		}
		u.paths[name] = true
		return nil
	}
	if !perm.IsRegular() {
		return fmt.Errorf("extension contains a link or special file")
	}
	if u.file == nil {
		if offset != 0 || u.paths[name] || len(u.paths) >= MaxPackageFiles {
			return fmt.Errorf("invalid extension file sequence")
		}
		if err := u.root.MkdirAll(path.Dir(name), 0o755); err != nil {
			return err
		}
		f, err := u.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm.Perm()&0o755)
		if err != nil {
			return err
		}
		u.file, u.name, u.offset = f, name, 0
		u.paths[name] = true
	}
	if name != u.name || offset != u.offset {
		return fmt.Errorf("extension upload offset mismatch")
	}
	n, err := u.file.Write(data)
	u.offset += uint64(n)
	u.bytes += n
	if err != nil {
		return err
	}
	if final {
		err = u.file.Sync()
		closeErr := u.file.Close()
		u.file = nil
		if err == nil {
			err = closeErr
		}
	}
	return err
}

func (u *Upload) Prepare(ctx context.Context) (Registration, error) {
	if u.sealed || u.file != nil {
		return Registration{}, fmt.Errorf("incomplete extension upload")
	}
	u.sealed = true
	manifest, err := readManifest(u.root)
	if err != nil {
		return Registration{}, err
	}
	return Register(ctx, u.Directory, manifest.ID, false)
}

func (u *Upload) Publish(directory, id string) (string, error) {
	if !u.sealed || !ValidID(id) {
		return "", fmt.Errorf("extension is not prepared")
	}
	items, err := List(directory)
	if err != nil {
		return "", err
	}
	if len(items) >= MaxExtensions {
		return "", fmt.Errorf("at most %d extensions can be installed", MaxExtensions)
	}
	target := filepath.Join(directory, id)
	if _, err := os.Lstat(target); err == nil {
		return "", fmt.Errorf("extension %s is already installed", id)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Rename(u.Directory, target); err != nil {
		return "", err
	}
	u.root.Close()
	u.root, u.Directory = nil, ""
	return target, nil
}

func (u *Upload) Close() {
	if u.file != nil {
		u.file.Close()
		u.file = nil
	}
	if u.root != nil {
		u.root.Close()
		u.root = nil
	}
	if u.Directory != "" {
		os.RemoveAll(u.Directory)
		u.Directory = ""
	}
}
