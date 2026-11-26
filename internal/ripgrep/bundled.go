//go:build onix_release || pencode_ripgrep

package ripgrep

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var (
	cacheMu          sync.Mutex
	cachedExecutable string
)

func executable() (string, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cachedExecutable != "" {
		if _, err := os.Stat(cachedExecutable); err == nil {
			return cachedExecutable, nil
		}
	}
	name, err := install()
	if err == nil {
		cachedExecutable = name
	}
	return name, err
}

func install() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "pencode", "ripgrep", binarySHA256)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	name := filepath.Join(dir, "rg")
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if validBinary(name) {
		return name, nil
	}
	tmp, err := os.CreateTemp(dir, ".rg-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	digest := sha256.New()
	err = extractBinary(io.MultiWriter(tmp, digest))
	if err = errors.Join(err, tmp.Chmod(0700), tmp.Close()); err != nil {
		return "", err
	}
	if fmt.Sprintf("%x", digest.Sum(nil)) != binarySHA256 {
		return "", errors.New("bundled ripgrep checksum mismatch")
	}
	if err := os.Rename(tmp.Name(), name); err != nil && !validBinary(name) {
		return "", err
	}
	return name, nil
}

func validBinary(name string) bool {
	file, err := os.Open(name)
	if err != nil {
		return false
	}
	defer file.Close()
	digest := sha256.New()
	_, err = io.Copy(digest, file)
	return err == nil && fmt.Sprintf("%x", digest.Sum(nil)) == binarySHA256
}

// The verified upstream archives contain rg in a single top-level directory.
func extractBinary(dst io.Writer) error {
	source := strings.NewReader(archiveData)
	if runtime.GOOS == "windows" {
		archive, err := zip.NewReader(source, source.Size())
		if err != nil {
			return err
		}
		for _, entry := range archive.File {
			if path.Base(entry.Name) == "rg.exe" && entry.Mode().IsRegular() {
				file, err := entry.Open()
				if err != nil {
					return err
				}
				defer file.Close()
				_, err = io.Copy(dst, file)
				return err
			}
		}
		return errors.New("rg.exe missing from bundled ripgrep archive")
	}
	compressed, err := gzip.NewReader(source)
	if err != nil {
		return err
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	for {
		entry, err := archive.Next()
		if err != nil {
			return err
		}
		if entry.Typeflag == tar.TypeReg && path.Base(entry.Name) == "rg" {
			_, err = io.Copy(dst, archive)
			return err
		}
	}
}

// CurrentCache identifies the extracted binary used by this build.
func CurrentCache(name string) bool { return name == binarySHA256 }
