package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func extract(archive, destination string) error {
	var total int64
	write := func(name string, mode os.FileMode, size int64, source io.Reader) error {
		name = filepath.FromSlash(name)
		if !filepath.IsLocal(name) || strings.Contains(name, "\\") && filepath.Separator != '\\' || strings.Contains(name, ":") {
			return errors.New("发行包包含非法路径")
		}
		parts := strings.Split(filepath.ToSlash(name), "/")
		rel, err := filepath.Rel("pencode", name)
		if parts[0] != "pencode" || err != nil || (rel != "." && !filepath.IsLocal(rel)) {
			return errors.New("发行包目录结构无效")
		}
		target := filepath.Join(destination, name)
		if mode.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !mode.IsRegular() || size < 0 || size > 2<<30 || total+size > 4<<30 {
			return errors.New("发行包包含不支持的文件或超过大小限制")
		}
		total += size
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm()|0o600)
		if err != nil {
			return err
		}
		_, err = io.CopyN(f, source, size)
		return errors.Join(err, f.Close())
	}
	if strings.HasSuffix(archive, ".zip") {
		z, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer z.Close()
		for _, entry := range z.File {
			r, err := entry.Open()
			if err != nil {
				return err
			}
			err = errors.Join(write(entry.Name, entry.Mode(), int64(entry.UncompressedSize64), r), r.Close())
			if err != nil {
				return err
			}
		}
		return nil
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	g, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer g.Close()
	t := tar.NewReader(g)
	for {
		h, err := t.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := write(h.Name, h.FileInfo().Mode(), h.Size, t); err != nil {
			return err
		}
	}
}
