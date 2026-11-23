//go:build darwin || linux

package server

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

type gitSearchPath struct {
	path       string
	candidates []string
}

var gitPaths atomic.Pointer[gitSearchPath]

// Cache candidate names, so an installation, removal or chmod takes effect
// even when PATH itself stays the same. Relative paths use os/exec's ErrDot rules.
func gitExecutable() string {
	path := os.Getenv("PATH")
	search := gitPaths.Load()
	if search == nil || search.path != path {
		search = &gitSearchPath{path: path}
		for _, dir := range filepath.SplitList(path) {
			if !filepath.IsAbs(dir) {
				search.candidates = nil
				break
			}
			search.candidates = append(search.candidates, filepath.Join(dir, "git"))
		}
		gitPaths.Store(search)
	}
	for _, candidate := range search.candidates {
		// Most entries are absent. Avoid the FileInfo and PathError that
		// os.Stat creates for every unsuccessful LookPath probe.
		err := unix.Access(candidate, unix.F_OK)
		if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR) {
			continue
		}
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate
		}
	}
	return "git"
}
