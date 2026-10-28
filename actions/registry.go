// Package actions provides the shared workbench button registry for native code
// and extensions. Mutations are thread safe; callbacks run on the UI thread.
package actions

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/pencode-labs/pencode/peb"
)

type Definition struct {
	peb.Action
	Run func(peb.ActionContext) error
}

type Registry struct {
	mu       sync.Mutex
	entries  []Definition
	indices  map[string]int
	revision uint64
	wake     func()
}

func Validate(a peb.Action) error {
	if a.ID == "" || len(a.ID) > 512 || strings.ContainsFunc(a.ID, unicode.IsSpace) || strings.ContainsAny(a.ID, "\x00<>\"'") || a.Location > peb.Command || a.Text == "" && a.Tooltip == "" {
		return fmt.Errorf("invalid action %q", a.ID)
	}
	if a.Shortcut != "" {
		if _, err := peb.ParseShortcut(a.Shortcut); err != nil {
			return fmt.Errorf("invalid shortcut %q for action %q", a.Shortcut, a.ID)
		}
	}
	if a.Location == peb.OpenWith {
		if len(a.Selectors) == 0 {
			return fmt.Errorf("open-with action requires document selectors")
		}
		for _, rule := range a.Selectors {
			if (rule.Match != peb.FileName && rule.Match != peb.FileSuffix) || rule.Pattern == "" || rule.Pattern == "." || rule.Pattern == ".." || strings.ContainsAny(rule.Pattern, "/\\\u0000") || rule.Match == peb.FileSuffix && !strings.HasPrefix(rule.Pattern, ".") {
				return fmt.Errorf("invalid open-with selector %q", rule.Pattern)
			}
		}
	} else if a.Default || len(a.Selectors) != 0 {
		return fmt.Errorf("document associations require an open-with action")
	}
	for _, color := range [...]string{a.Color, a.IconColor} {
		if color == "" {
			continue
		}
		if len(color) != 7 && len(color) != 9 || color[0] != '#' {
			return fmt.Errorf("action color must be #RRGGBB or #RRGGBBAA")
		}
		for _, c := range color[1:] {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return fmt.Errorf("invalid action color")
			}
		}
	}
	if len(a.Text) > peb.MaxString || len(a.Tooltip) > peb.MaxString || len(a.Icon) > peb.MaxString || len(a.IconStyle) > 1024 {
		return fmt.Errorf("oversized action %q", a.ID)
	}
	return nil
}

func (r *Registry) SetWake(wake func()) { r.mu.Lock(); r.wake = wake; r.mu.Unlock() }
func (r *Registry) Register(d Definition) error {
	if err := Validate(d.Action); err != nil {
		return err
	}
	if d.Run == nil {
		return fmt.Errorf("action %q has no handler", d.ID)
	}
	r.mu.Lock()
	if _, ok := r.indices[d.ID]; ok {
		r.mu.Unlock()
		return fmt.Errorf("duplicate action %q", d.ID)
	}
	if r.indices == nil {
		r.indices = make(map[string]int)
	}
	d.Selectors = slices.Clone(d.Selectors)
	r.indices[d.ID] = len(r.entries)
	r.entries = append(r.entries, d)
	r.revision++
	wake := r.wake
	r.mu.Unlock()
	if wake != nil {
		wake()
	}
	return nil
}
func (r *Registry) Update(a peb.Action) error {
	if err := Validate(a); err != nil {
		return err
	}
	r.mu.Lock()
	i, ok := r.indices[a.ID]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("unknown action %q", a.ID)
	}
	if r.entries[i].Action.Equal(a) {
		r.mu.Unlock()
		return nil
	}
	a.Selectors = slices.Clone(a.Selectors)
	r.entries[i].Action = a
	r.revision++
	wake := r.wake
	r.mu.Unlock()
	if wake != nil {
		wake()
	}
	return nil
}
func (r *Registry) Remove(id string) { r.remove(id, false) }

// RemovePrefix removes an owner's buttons in a single pass.
func (r *Registry) RemovePrefix(prefix string) { r.remove(prefix, true) }
func (r *Registry) remove(prefix string, prefixMatch bool) {
	r.mu.Lock()
	n := 0
	for _, d := range r.entries {
		if d.ID == prefix || prefixMatch && strings.HasPrefix(d.ID, prefix) {
			delete(r.indices, d.ID)
			continue
		}
		r.indices[d.ID] = n
		r.entries[n] = d
		n++
	}
	changed := n != len(r.entries)
	clear(r.entries[n:])
	r.entries = r.entries[:n]
	if changed {
		r.revision++
	}
	wake := r.wake
	r.mu.Unlock()
	if changed && wake != nil {
		wake()
	}
}

// Snapshot copies only when the revision changes and reuses dst's capacity.
func (r *Registry) Snapshot(dst []peb.Action, revision uint64) ([]peb.Action, uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if revision == r.revision {
		return dst, revision
	}
	clear(dst)
	dst = dst[:0]
	for _, d := range r.entries {
		dst = append(dst, d.Action)
	}
	return dst, r.revision
}
func (r *Registry) Run(c peb.ActionContext) error {
	r.mu.Lock()
	i, ok := r.indices[c.ID]
	var run func(peb.ActionContext) error
	if ok && !r.entries[i].Disabled && !r.entries[i].Hidden {
		run = r.entries[i].Run
	}
	r.mu.Unlock()
	if run == nil {
		return fmt.Errorf("action %q is unavailable", c.ID)
	}
	return run(c)
}
