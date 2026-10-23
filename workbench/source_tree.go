package workbench

import (
	"iter"
	"path"
	"slices"
	"strings"
)

type sourceTreeRow struct {
	Path  string
	File  *sourceFile // nil for directories
	Depth int
}

// Git paths are sorted, so shared directories can be emitted in one pass.
func sourceTreeRows(kind string, files []sourceFile, collapsed map[string]bool) iter.Seq[sourceTreeRow] {
	return func(yield func(sourceTreeRow) bool) {
		previous := ""
		for i := range files {
			file := &files[i]
			dir := path.Dir(file.Path)
			if dir == "." {
				dir = ""
			}
			hidden := false
			start, depth := 0, 0
			for start < len(dir) {
				end := strings.IndexByte(dir[start:], '/')
				if end < 0 {
					end = len(dir)
				} else {
					end += start
				}
				folder := dir[:end]
				shared := strings.HasPrefix(previous, folder) && (len(previous) == len(folder) || previous[len(folder)] == '/')
				if !shared {
					if !yield(sourceTreeRow{Path: folder, Depth: depth}) {
						return
					}
				}
				if collapsed[kind+":"+folder] {
					hidden = true
					break
				}
				start, depth = end+1, depth+1
			}
			previous = dir
			if !hidden && !yield(sourceTreeRow{Path: file.Path, File: file, Depth: depth}) {
				return
			}
		}
	}
}

// Retain visible history rows so sizing and rendering share the same traversal.
func (s *pencodeSourceControl) historyTree() []sourceTreeRow {
	first := sourceFirst(s.CommitFiles)
	if s.commitTreeFiles != first || s.commitTreeCount != len(s.CommitFiles) || s.commitTreeRevision != s.TreeRevision {
		s.commitTreeFiles, s.commitTreeCount, s.commitTreeRevision = first, len(s.CommitFiles), s.TreeRevision
		previous := s.commitTree
		s.commitTree = slices.AppendSeq(previous[:0], sourceTreeRows("history", s.CommitFiles, s.Collapsed))
		if len(previous) > len(s.commitTree) {
			clear(previous[len(s.commitTree):])
		}
	}
	return s.commitTree
}
