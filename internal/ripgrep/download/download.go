// Package download prepares the pinned ripgrep archive for a release build.
package download

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const version = "16.0.0"
const releaseURL = "https://github.com/BurntSushi/ripgrep/releases/download/" + version + "/"

var archives = map[string]struct{ suffix, sha256 string }{
	"darwin/amd64":  {"x86_64-apple-darwin.tar.gz", "aa9183b0e0bfac03c5818895ad18b75001c402d230063898abfe5a58cd413caa"},
	"darwin/arm64":  {"aarch64-apple-darwin.tar.gz", "5a145656d5067144001bfce6648767fd3254599aa736f1ffdb7fb907760b8ca1"},
	"linux/amd64":   {"x86_64-unknown-linux-musl.tar.gz", "54268ab4643c00a290a29b0086066d64b3f56236dd805ec64629a86f9bd29fd0"},
	"linux/arm64":   {"aarch64-unknown-linux-musl.tar.gz", "374f1886be881e8e8683d7107278f7af1b6017fda75eb97de71f5783f42b0110"},
	"windows/amd64": {"x86_64-pc-windows-msvc.zip", "f418ae83e63046a94a7b5f3a44bf3a169f66e689164856ecee02547131c73931"},
	"windows/arm64": {"aarch64-pc-windows-msvc.zip", "3302ede09745d33ca2b287bf3c6434c54cb35c3b408a7d3a265939961add5a1a"},
}

// Prepare stores only the target archive under the ripgrep package's ignored
// bundle directory. Concurrent builds only see complete, verified archives.
func Prepare(packageDir, goos, arch string) error {
	target, ok := archives[goos+"/"+arch]
	if !ok {
		return fmt.Errorf("no ripgrep %s archive for %s/%s", version, goos, arch)
	}
	name := "ripgrep-" + version + "-" + target.suffix
	return fetch(filepath.Join(packageDir, "bundle", name), releaseURL+name, target.sha256)
}

func fetch(name, url, checksum string) error {
	if validArchive(name, checksum) {
		return nil
	}
	client := http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	dir := filepath.Dir(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".ripgrep-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	digest := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, digest), resp.Body)
	if err := errors.Join(err, tmp.Close()); err != nil {
		return err
	}
	if fmt.Sprintf("%x", digest.Sum(nil)) != checksum {
		return fmt.Errorf("ripgrep archive checksum mismatch for %s", url)
	}
	if err := os.Rename(tmp.Name(), name); err != nil && !validArchive(name, checksum) {
		return err
	}
	return nil
}

func validArchive(name, checksum string) bool {
	file, err := os.Open(name)
	if err != nil {
		return false
	}
	defer file.Close()
	digest := sha256.New()
	_, err = io.Copy(digest, file)
	return err == nil && fmt.Sprintf("%x", digest.Sum(nil)) == checksum
}
