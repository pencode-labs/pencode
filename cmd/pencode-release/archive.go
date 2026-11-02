package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Reuse the engine's packaged output, retaining its binary catalog, resource
// packages, platform icons, executable modes and macOS app signature.
func writeDistribution(destination, artifact, project string) (resultErr error) {
	file, err := os.CreateTemp(filepath.Dir(destination), ".pencode-release-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o644); err != nil {
		return errors.Join(err, file.Close())
	}
	var add func(*tar.Header, io.Reader) error
	var closeArchive func() error
	if strings.HasSuffix(destination, ".zip") {
		writer := zip.NewWriter(file)
		add = func(header *tar.Header, source io.Reader) error {
			entry := &zip.FileHeader{Name: header.Name, Method: zip.Deflate}
			entry.SetMode(header.FileInfo().Mode())
			entry.SetModTime(header.ModTime)
			target, err := writer.CreateHeader(entry)
			if err != nil {
				return err
			}
			_, err = io.Copy(target, source)
			return err
		}
		closeArchive = writer.Close
	} else {
		compressed := gzip.NewWriter(file)
		writer := tar.NewWriter(compressed)
		add = func(header *tar.Header, source io.Reader) error {
			if err := writer.WriteHeader(header); err != nil {
				return err
			}
			_, err := io.Copy(writer, source)
			return err
		}
		closeArchive = func() error { return errors.Join(writer.Close(), compressed.Close()) }
	}
	defer func() {
		resultErr = errors.Join(resultErr, closeArchive(), file.Close())
		if resultErr == nil {
			resultErr = os.Rename(file.Name(), destination)
		}
	}()
	if err := copyArtifact(artifact, add); err != nil {
		return err
	}
	for _, name := range []string{"LICENSE", "README.md", "README.zh-CN.md"} {
		if err := addFile(filepath.Join(project, name), path.Join("pencode", name), add); err != nil {
			return err
		}
	}
	// Keep icon notices readable without an Onix package reader.
	for _, name := range []string{"CODICONS-LICENSE.txt", "LUCIDE-LICENSE.txt"} {
		if err := addFile(filepath.Join(project, "resources", "icons", name), path.Join("pencode", "licenses", name), add); err != nil {
			return err
		}
	}
	return addFile(filepath.Join(project, "..", "onix", "LICENSE"), "pencode/licenses/ONIX-LICENSE.txt", add)
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
			return addFile(name, path.Join("pencode", filepath.ToSlash(relative)), add)
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
	header.ModTime = time.Unix(0, 0)
	if !info.Mode().IsRegular() {
		return add(header, strings.NewReader(""))
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	return errors.Join(add(header, file), file.Close())
}
