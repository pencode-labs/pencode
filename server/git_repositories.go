package server

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pencode-labs/pencode/protocol"
)

// Discover on the backend so local and remote projects use the same filesystem.
// Git metadata and dependency/build trees are never traversed, nor are symlinks.
func repositoryRoots(ctx context.Context, root string) ([]string, error) {
	var roots []string
	add := func(directory string) {
		data, err := command(ctx, directory, []string{"rev-parse", "--show-toplevel"})
		if err == nil {
			name := strings.TrimSuffix(string(data), "\n")
			if !slices.Contains(roots, name) {
				roots = append(roots, name)
			}
		}
	}
	add(root) // A project can also be a directory inside a repository.
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return roots, err
	}
	// Only directory paths survive a batch. The order of entries in the
	// filesystem is immaterial since the much smaller root list is sorted.
	pending := []string{root}
	entries := 1
	for len(pending) != 0 {
		directory := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		f, err := os.Open(directory)
		if err != nil {
			if directory == root {
				return nil, err
			}
			continue
		}
		for {
			children, readErr := f.ReadDir(128)
			entries += len(children)
			if entries > 262144 || ctx.Err() != nil {
				f.Close()
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				return nil, errors.New("repository scan exceeds 262144 entry budget")
			}
			for _, entry := range children {
				switch entry.Name() {
				case ".git":
					if len(roots) >= 4096 {
						f.Close()
						return nil, errors.New("repository count exceeds budget")
					}
					if directory != root {
						add(directory) // Includes submodule and worktree .git files.
					}
				case "node_modules", "dist":
				default:
					if entry.IsDir() {
						pending = append(pending, filepath.Join(directory, entry.Name()))
					}
				}
			}
			if readErr != nil {
				f.Close()
				if directory == root && readErr != io.EOF {
					return nil, readErr
				}
				break
			}
		}
	}
	slices.Sort(roots)
	return roots, ctx.Err()
}

func (s *services) gitRepositories(ctx context.Context, root string, d *protocol.Decoder, e *protocol.Encoder) error {
	if err := d.Done(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	roots, err := repositoryRoots(ctx, root)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.roots[root] || ctx.Err() != nil {
		return &protocol.Error{Code: protocol.NotFound, Message: "workspace is closed"}
	}
	e.U32(uint32(len(roots)))
	for _, name := range roots {
		if !s.roots[name] {
			s.roots[name], s.derived[name] = true, root
		}
		e.String(name)
	}
	return nil
}
