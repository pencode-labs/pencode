package server

import (
	"context"
	"errors"
	"io/fs"
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
	entries := 0
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if name == root {
				return err
			}
			return nil // An unreadable sibling must not hide other repositories.
		}
		entries++
		if entries > 262144 {
			return errors.New("repository scan exceeds 262144 entry budget")
		}
		if name == root {
			return nil
		}
		switch entry.Name() {
		case ".git":
			if len(roots) >= 4096 {
				return errors.New("repository count exceeds budget")
			}
			add(filepath.Dir(name)) // Includes .git files used by submodules/worktrees.
			if entry.IsDir() {
				return fs.SkipDir
			}
		case "node_modules", "dist":
			if entry.IsDir() {
				return fs.SkipDir
			}
		}
		return nil
	})
	slices.Sort(roots)
	return roots, err
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
