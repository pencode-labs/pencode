package workbench

import (
	"path"
	"runtime"
	"slices"

	"github.com/onix-engine/onix/input"
)

type pencodeTreeSelection struct {
	paths  map[string]struct{}
	anchor string
}

func (s *pencodeTreeSelection) contains(relative string) bool {
	if s.paths == nil {
		return relative != "" && s.anchor == relative
	}
	_, ok := s.paths[relative]
	return ok
}

func (s *pencodeTreeSelection) multiple(relative string) bool {
	return len(s.paths) > 1 && s.contains(relative)
}

func (s *pencodeTreeSelection) set(relative string) bool {
	changed := s.paths != nil || s.anchor != relative
	*s = pencodeTreeSelection{anchor: relative}
	return changed
}

func (s *pencodeTreeSelection) toggle(relative string) {
	if s.paths == nil {
		switch s.anchor {
		case "":
			s.set(relative)
		case relative:
			s.set("")
		default:
			s.paths = map[string]struct{}{s.anchor: {}, relative: {}}
			s.anchor = relative
		}
		return
	}
	if s.contains(relative) {
		delete(s.paths, relative)
		s.retain(s.paths)
		return
	}
	s.paths[relative] = struct{}{}
	s.anchor = relative
}

func (p *pencodeProject) selectTreeRange(model *workspaceModel, relative string, additive bool) {
	s := &p.tree.selection
	rows := p.visibleTreeRows(model)
	first, last := -1, -1
	for i, index := range rows {
		if index < 0 {
			continue
		}
		switch p.Entries[index].Path {
		case relative:
			last = i
			if s.anchor == relative {
				first = i
			}
		case s.anchor:
			first = i
		}
		if first >= 0 && last >= 0 {
			break
		}
	}
	if first < 0 || last < 0 {
		s.set(relative)
		return
	}
	if s.paths == nil {
		s.paths = make(map[string]struct{}, max(first, last)-min(first, last)+1)
	} else if !additive {
		clear(s.paths)
	}
	for _, index := range rows[min(first, last) : max(first, last)+1] {
		if index >= 0 {
			s.paths[p.Entries[index].Path] = struct{}{}
		}
	}
	if len(s.paths) == 1 {
		s.set(relative)
	}
}

func treeSelectionModifiers(state *input.State) (toggle, extend bool) {
	left, right := input.KeyLeftControl, input.KeyRightControl
	if runtime.GOOS == "darwin" {
		left, right = input.KeyLeftSuper, input.KeyRightSuper
	}
	return state.KeyHeld(left) || state.KeyHeld(right), state.KeyHeld(input.KeyLeftShift) || state.KeyHeld(input.KeyRightShift)
}

func (s *pencodeTreeSelection) targets(relative string, rootsOnly bool) []string {
	if !s.multiple(relative) {
		return []string{relative}
	}
	paths := make([]string, 0, len(s.paths))
	for name := range s.paths {
		if rootsOnly {
			covered := false
			for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
				if s.contains(parent) {
					covered = true
					break
				}
			}
			if covered {
				continue
			}
		}
		paths = append(paths, name)
	}
	slices.Sort(paths)
	return paths
}

func (s *pencodeTreeSelection) prune(entries []treeEntry) {
	if s.paths == nil {
		if s.anchor != "" && !slices.ContainsFunc(entries, func(entry treeEntry) bool { return entry.Path == s.anchor }) {
			s.set("")
		}
		return
	}
	retained := make(map[string]struct{}, len(s.paths))
	for _, entry := range entries {
		if s.contains(entry.Path) {
			retained[entry.Path] = struct{}{}
			if len(retained) == len(s.paths) {
				return
			}
		}
	}
	s.retain(retained)
}

func (s *pencodeTreeSelection) relocate(destinations map[string]string, sameProject bool) {
	if s.paths == nil {
		if next, moved := resourceDestination(destinations, s.anchor); moved {
			if !sameProject {
				next = ""
			}
			s.set(next)
		}
		return
	}
	retained := make(map[string]struct{}, len(s.paths))
	for name := range s.paths {
		next, moved := resourceDestination(destinations, name)
		if !moved || sameProject && next != "" {
			retained[next] = struct{}{}
			if name == s.anchor {
				s.anchor = next
			}
		}
	}
	s.retain(retained)
}

func (s *pencodeTreeSelection) retain(retained map[string]struct{}) {
	s.paths = retained
	if !s.contains(s.anchor) {
		s.anchor = ""
		for name := range retained {
			s.anchor = name
			break
		}
	}
	if len(retained) < 2 {
		s.set(s.anchor)
	}
}
