package workbench

import (
	"iter"
	"path"
	"slices"
	"strings"
)

const (
	sourceChangeRowHeight   = 27
	sourceChangeGroupHeight = 32
	sourceChangeOverscan    = 8
)

type sourceTreeRow struct {
	File  *sourceFile // nil for directories
	Path  string
	Depth int
}

var sourceChangeGroups = [...]struct{ Kind, Label string }{
	{"merge", "Merge Changes"}, {"staged", "Staged Changes"}, {"changes", "Changes"},
}

type sourceRowRange struct{ First, End int }

type sourceTreeCache struct {
	files    *sourceFile
	rows     []sourceTreeRow
	count    int
	revision uint64
}

func (tree *sourceTreeCache) update(kind string, files []sourceFile, collapsed map[string]bool, revision uint64) []sourceTreeRow {
	first := sourceFirst(files)
	if tree.files != first || tree.count != len(files) || tree.revision != revision {
		tree.files, tree.count, tree.revision = first, len(files), revision
		previous := tree.rows
		tree.rows = previous[:0]
		if !collapsed["group:"+kind] {
			tree.rows = slices.AppendSeq(tree.rows, sourceTreeRows(kind, files, collapsed))
		}
		if len(previous) > len(tree.rows) {
			clear(previous[len(tree.rows):])
		}
	}
	return tree.rows
}

// Row indexes are rebuilt only when files or folding change. Scrolling visits
// three groups, regardless of how many files the repository contains.
func (s *pencodeSourceControl) changeRanges() (windows [3]sourceRowRange) {
	height := s.changesHeight
	if height <= 0 {
		height = 400
	}
	total := 0
	for i, group := range sourceChangeGroups {
		rows := s.changeTrees[i].update(group.Kind, s.Snapshot.Groups[i], s.Collapsed, s.TreeRevision)
		if len(s.Snapshot.Groups[i]) != 0 {
			total += sourceChangeGroupHeight + len(rows)*sourceChangeRowHeight
		}
	}
	position := min(s.changesPosition, max(0, float32(total)-height))
	// Keep a whole scroll band mounted; sub-row wheel and momentum updates
	// reuse native transforms without reconciling the panel again.
	position = float32(int(position) / (sourceChangeRowHeight * sourceChangeOverscan) * (sourceChangeRowHeight * sourceChangeOverscan))
	top := 0
	for i := range sourceChangeGroups {
		if len(s.Snapshot.Groups[i]) == 0 {
			continue
		}
		top += sourceChangeGroupHeight
		count := len(s.changeTrees[i].rows)
		first := min(count, max(0, int(position-float32(top))/sourceChangeRowHeight-sourceChangeOverscan))
		end := min(count, max(first, int(position+height-float32(top))/sourceChangeRowHeight+2*sourceChangeOverscan+1))
		windows[i] = sourceRowRange{First: first, End: end}
		top += count * sourceChangeRowHeight
	}
	return
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

// Sizing and rendering share the same cached traversal.
func (s *pencodeSourceControl) historyTree() []sourceTreeRow {
	return s.commitTree.update("history", s.CommitFiles, s.Collapsed, s.TreeRevision)
}
