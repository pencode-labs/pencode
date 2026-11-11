package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Reuse the engine's packaged output, retaining its binary catalog, resource
// packages, platform icons, executable modes and macOS app signature.
func stageDistribution(destination, artifact, project, version string) error {
	add := func(header *tar.Header, source io.Reader) error {
		name := filepath.FromSlash(header.Name)
		if !filepath.IsLocal(name) || (name != "Pencode" && !strings.HasPrefix(name, "Pencode"+string(filepath.Separator))) {
			return fmt.Errorf("invalid artifact path %q", header.Name)
		}
		target := filepath.Join(destination, name)
		if header.FileInfo().IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if header.Typeflag == tar.TypeSymlink {
			resolved := path.Join(path.Dir(header.Name), header.Linkname)
			if path.IsAbs(header.Linkname) || !strings.HasPrefix(resolved, "Pencode/") {
				return fmt.Errorf("invalid artifact symlink %q", header.Name)
			}
			return os.Symlink(header.Linkname, target)
		}
		if !header.FileInfo().Mode().IsRegular() {
			return fmt.Errorf("unsupported artifact entry %q", header.Name)
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, header.FileInfo().Mode().Perm())
		if err != nil {
			return err
		}
		_, err = io.Copy(file, source)
		return errors.Join(err, file.Close())
	}
	if err := copyArtifact(artifact, add); err != nil {
		return err
	}
	for _, name := range []string{"LICENSE", "README.md", "README.zh-CN.md"} {
		if err := addFile(filepath.Join(project, name), path.Join("Pencode", name), add); err != nil {
			return err
		}
	}
	if err := add(&tar.Header{Name: "Pencode/pencode-version", Mode: 0o644, Size: int64(len(version) + 1)}, strings.NewReader(version+"\n")); err != nil {
		return err
	}
	// Keep icon notices readable without an Onix package reader.
	for _, name := range []string{"CODICONS-LICENSE.txt", "LUCIDE-LICENSE.txt"} {
		if err := addFile(filepath.Join(project, "resources", "icons", name), path.Join("Pencode", "licenses", name), add); err != nil {
			return err
		}
	}
	if err := addFile(filepath.Join(project, "workbench", "fonts", "NERD-FONTS-LICENSE.txt"), "Pencode/licenses/NERD-FONTS-LICENSE.txt", add); err != nil {
		return err
	}
	return addFile(filepath.Join(project, "..", "onix", "LICENSE"), "Pencode/licenses/ONIX-LICENSE.txt", add)
}

func copyArtifact(artifact string, add func(*tar.Header, io.Reader) error) error {
	switch {
	case strings.HasSuffix(artifact, ".zip"):
		archive, err := zip.OpenReader(artifact)
		if err != nil {
			return err
		}
		defer archive.Close()
		for _, entry := range archive.File {
			header, err := tar.FileInfoHeader(entry.FileInfo(), "")
			if err != nil {
				return err
			}
			header.Name = entry.Name
			source, err := entry.Open()
			if err != nil {
				return err
			}
			if err := errors.Join(add(header, source), source.Close()); err != nil {
				return err
			}
		}
		return nil
	case strings.HasSuffix(artifact, ".tar.gz"):
		file, err := os.Open(artifact)
		if err != nil {
			return err
		}
		defer file.Close()
		compressed, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer compressed.Close()
		archive := tar.NewReader(compressed)
		for {
			header, err := archive.Next()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if err := add(header, archive); err != nil {
				return err
			}
		}
	default: // Onix produces an already signed .app directory on macOS.
		return filepath.WalkDir(artifact, func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(filepath.Dir(artifact), name)
			if err != nil {
				return err
			}
			return addFile(name, path.Join("Pencode", filepath.ToSlash(relative)), add)
		})
	}
}

func addFile(source, name string, add func(*tar.Header, io.Reader) error) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	var link string
	if info.Mode()&os.ModeSymlink != 0 {
		link, err = os.Readlink(source)
		if err != nil {
			return err
		}
	}
	header, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return err
	}
	header.Name = name
	if !info.Mode().IsRegular() {
		return add(header, strings.NewReader(""))
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	return errors.Join(add(header, file), file.Close())
}
