package workbench

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

func newPencodeProjectScan(readDir func(string) ([]fs.DirEntry, error), initial bool, wake func()) *pencodeProjectScan {
	ctx, cancel := context.WithCancel(context.Background())
	scan := &pencodeProjectScan{
		cancel: cancel, initial: initial,
		results: make(chan *pencodeTreeBatch, 2),
		buffers: make(chan *pencodeTreeBatch, 2),
	}
	for range cap(scan.buffers) {
		scan.buffers <- &pencodeTreeBatch{}
	}
	go scan.run(ctx, readDir, wake)
	return scan
}

func (scan *pencodeProjectScan) run(ctx context.Context, readDir func(string) ([]fs.DirEntry, error), wake func()) {
	defer close(scan.results)
	var batch *pencodeTreeBatch
	var scanErr error
	published := false
	acquire := func() error {
		if batch != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case batch = <-scan.buffers:
			return ctx.Err()
		}
	}
	publish := func() error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case scan.results <- batch:
			batch = nil
			published = true
			if wake != nil {
				wake()
			}
			return nil
		}
	}
	var walk func(string, int) error
	walk = func(directory string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		children, err := readDir(directory)
		if err != nil {
			// A disappearing or unreadable subtree must not hide its siblings.
			if scanErr == nil {
				scanErr = fmt.Errorf("读取项目目录 %s 失败：%w", directory, err)
			}
			return nil
		}
		slices.SortFunc(children, func(a, b fs.DirEntry) int {
			if aDir, bDir := a.IsDir(), b.IsDir(); aDir != bDir {
				if aDir {
					return -1
				}
				return 1
			}
			return strings.Compare(a.Name(), b.Name())
		})
		prefix := ""
		if directory != "." {
			prefix = directory + "/"
		}
		for _, child := range children {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := child.Name()
			if skipProjectEntry(name) {
				continue
			}
			if err := acquire(); err != nil {
				return err
			}
			relative := prefix + name
			isDir := child.IsDir()
			entry := treeEntry{Path: relative, Name: name, Depth: depth, IsDir: isDir, Openable: !isDir && isTextFile(name)}
			batch.entries[batch.count] = entry
			batch.count++
			// Publish the first directory before descending, even when the next
			// filesystem read is slow. Later results use full reusable batches.
			if batch.count == len(batch.entries) || (!published && isDir) {
				if err := publish(); err != nil {
					return err
				}
			}
			if isDir {
				if err := walk(relative, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(".", 0); err != nil {
		return
	}
	if err := acquire(); err != nil {
		return
	}
	batch.done, batch.err = true, scanErr
	if ctx.Err() == nil {
		_ = publish()
	}
}
