package peb

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/pencode-labs/pencode/textchange"
)

// ErrViewBase asks the session to request a full view through resync.
var ErrViewBase = errors.New("peb: missing view patch base")

type viewState struct {
	ui       UI
	template string
	regions  []ViewRegion
	scratch  []ViewRegion
}

type viewStream map[string]*viewState

func newViewState(ui UI) (*viewState, error) {
	if !validFullView(ui) {
		return nil, ErrProtocol
	}
	template, regions, err := SplitViewRegions(ui.View.HTML)
	if err != nil {
		return nil, err
	}
	refs := make(map[string]bool, len(regions))
	for _, r := range regions {
		if refs[r.Ref] {
			return nil, fmt.Errorf("peb: duplicate region %q", r.Ref)
		}
		refs[r.Ref] = true
	}
	// Resource preparation is allowed to rewrite its own view without changing
	// the raw transport base. Strings and published event lists are immutable.
	view := *ui.View
	view.Events = slices.Clone(view.Events)
	ui.View, ui.Patch = &view, nil
	return &viewState{ui: ui, template: template, regions: regions}, nil
}

// Patches cannot grow a reconstructed UI beyond the limits for a full frame.
// 100 bytes covers UI's fixed fields, string lengths and the view/event counts.
func validFullView(ui UI) bool {
	v := ui.View
	size := 100 + len(ui.ID) + len(ui.Title) + len(ui.Anchor) + len(ui.Context.ID) + len(ui.Context.ProjectPath) + len(ui.Context.Path) + len(v.HTML) + len(v.CSS)
	if len(v.HTML) > MaxDocument || len(v.CSS) > MaxDocument || len(v.Events) > MaxItems || !utf8.ValidString(v.HTML) || !utf8.ValidString(v.CSS) {
		return false
	}
	for _, event := range v.Events {
		if len(event.Ref) > MaxString || len(event.Kind) > MaxString || !utf8.ValidString(event.Ref) || !utf8.ValidString(event.Kind) {
			return false
		}
		size += 8 + len(event.Ref) + len(event.Kind)
	}
	return size <= MaxPayload
}

func (s viewStream) prepare(ui UI) (UI, *viewState, error) {
	if ui.Patch != nil {
		return ui, nil, fmt.Errorf("peb: Show manages patches automatically")
	}
	if ui.Close || ui.View == nil {
		return ui, nil, nil
	}
	old := s[ui.ID]
	if old == nil && len(s) >= 32 {
		return ui, nil, fmt.Errorf("peb: too many UI surfaces")
	}
	ui.ViewVersion = 1
	if old != nil && old.ui.ViewVersion != ^uint32(0) {
		ui.ViewVersion = old.ui.ViewVersion + 1
	}
	if old != nil && ui.ViewVersion != 1 && old.ui.View.CSS == ui.View.CSS && slices.Equal(old.ui.View.Events, ui.View.Events) {
		if next, patch := old.regionChange(ui); next != nil {
			ui.View, ui.Patch = nil, patch
			return ui, next, nil
		}
	}
	next, err := newViewState(ui)
	if err != nil {
		return ui, nil, err
	}
	if old == nil || ui.ViewVersion == 1 || len(next.regions) == 0 || old.template != next.template ||
		old.ui.View.CSS != ui.View.CSS || !slices.Equal(old.ui.View.Events, ui.View.Events) || len(old.regions) != len(next.regions) {
		return ui, next, nil
	}
	patch := &ViewPatch{Base: old.ui.ViewVersion}
	bytes := 8
	for i, r := range next.regions {
		if old.regions[i].Ref != r.Ref {
			return ui, next, nil
		}
		if old.regions[i].HTML != r.HTML {
			if !validRegionHTML(r.HTML, r.tag) {
				return ui, next, nil
			}
			patch.Regions = append(patch.Regions, ViewRegion{Ref: r.Ref, HTML: r.HTML})
			bytes += 8 + len(r.Ref) + len(r.HTML)
		}
	}
	if bytes < len(ui.View.HTML)+len(ui.View.CSS) {
		ui.View, ui.Patch = nil, patch
	}
	return ui, next, nil
}

// Streaming normally edits one message. Locate its byte delta before parsing:
// all other containers and their markup are already known to be unchanged.
func (old *viewState) regionChange(ui UI) (*viewState, *ViewPatch) {
	if !validFullView(ui) {
		return nil, nil
	}
	start, oldEnd, newEnd := textchange.Between(old.ui.View.HTML, ui.View.HTML)
	shift := newEnd - oldEnd
	index := -1
	for i, r := range old.regions {
		if start >= r.start && oldEnd <= r.end {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, nil
	}
	r := old.regions[index]
	changed := ui.View.HTML[r.start : r.end+shift]
	if !validRegionHTML(changed, r.tag) || len(changed)+len(r.Ref)+16 >= len(ui.View.HTML)+len(ui.View.CSS) {
		return nil, nil
	}
	regions := old.regionScratch()
	for i := range regions {
		if i == index {
			regions[i].HTML, regions[i].end = changed, r.end+shift
		} else if i > index {
			regions[i].start += shift
			regions[i].end += shift
		}
		regions[i].HTML = ui.View.HTML[regions[i].start:regions[i].end]
	}
	view := *ui.View
	view.Events = slices.Clone(view.Events)
	ui.View = &view
	return &viewState{ui: ui, template: old.template, regions: regions, scratch: old.regions}, &ViewPatch{Base: old.ui.ViewVersion, Regions: []ViewRegion{{Ref: r.Ref, HTML: changed}}}
}

func (s *viewState) regionScratch() []ViewRegion {
	s.scratch = slices.Grow(s.scratch[:0], len(s.regions))[:len(s.regions)]
	copy(s.scratch, s.regions)
	return s.scratch
}

func (s *viewStream) commit(id string, closed bool, state *viewState) {
	if closed {
		delete(*s, id)
	} else if state != nil {
		if *s == nil {
			*s = make(viewStream)
		}
		(*s)[id] = state
	}
}

func (s viewStream) metadata(ui UI) {
	if state := s[ui.ID]; state != nil {
		ui.View, ui.ViewVersion = state.ui.View, state.ui.ViewVersion
		state.ui = ui
	}
}

// ViewReceiver reconstructs raw views on the session I/O worker, before asset
// preparation and UI validation. It owns at most 32 surface bases. A new session
// starts empty and its publisher sends a full view first. Apply is transactional.
// Do not share a receiver across processes or access it concurrently.
type ViewReceiver struct{ views viewStream }

func (r *ViewReceiver) Apply(ui UI) (UI, error) {
	if !ui.valid() {
		return UI{}, ErrProtocol
	}
	if ui.Close {
		delete(r.views, ui.ID)
		return ui, nil
	}
	if ui.View != nil {
		if r.views[ui.ID] == nil && len(r.views) >= 32 {
			return UI{}, ErrProtocol
		}
		state, err := newViewState(ui)
		if err != nil {
			return UI{}, err
		}
		r.views.commit(ui.ID, false, state)
		return ui, nil
	}
	if ui.Patch == nil {
		r.views.metadata(ui)
		return ui, nil
	}
	old := r.views[ui.ID]
	if old == nil || old.ui.ViewVersion != ui.Patch.Base {
		return UI{}, ErrViewBase
	}
	changes := make(map[string]string, len(ui.Patch.Regions))
	for _, change := range ui.Patch.Regions {
		if _, exists := changes[change.Ref]; exists || change.Ref == "" {
			return UI{}, ErrProtocol
		}
		changes[change.Ref] = change.HTML
	}
	size, found := len(old.ui.View.HTML), 0
	for _, region := range old.regions {
		if html, changed := changes[region.Ref]; changed {
			if !validRegionHTML(html, region.tag) {
				return UI{}, ErrProtocol
			}
			size += len(html) - len(region.HTML)
			found++
		}
	}
	if found != len(changes) || size < 0 || size > MaxPayload-len(old.ui.View.CSS) {
		return UI{}, ErrProtocol
	}
	var b strings.Builder
	regions := old.regionScratch()
	if len(changes) != 0 {
		b.Grow(size)
		end := 0
		for i, region := range old.regions {
			b.WriteString(old.ui.View.HTML[end:region.start])
			html, changed := changes[region.Ref]
			if !changed {
				html = region.HTML
			}
			regions[i].start = b.Len()
			b.WriteString(html)
			regions[i].end, regions[i].HTML = b.Len(), html
			end = region.end
		}
		b.WriteString(old.ui.View.HTML[end:])
	}
	view := *old.ui.View
	if len(changes) != 0 {
		view.HTML = b.String()
	}
	ui.View, ui.Patch = &view, nil
	if !validFullView(ui) {
		return UI{}, ErrProtocol
	}
	// Keep region substrings on the new base so unchanged regions do not pin
	// earlier full views after repeated patches.
	for i := range regions {
		regions[i].HTML = view.HTML[regions[i].start:regions[i].end]
	}
	stateView := view
	stateView.Events = slices.Clone(view.Events)
	stateUI := ui
	stateUI.View = &stateView
	next := &viewState{ui: stateUI, template: old.template, regions: regions, scratch: old.regions}
	r.views.commit(ui.ID, false, next)
	return ui, nil
}

// handleViewEvent runs before extension callbacks. Closing drops the delta base;
// resync replays the latest complete view without requiring application state.
func (p *ActionPublisher) handleViewEvent(event UIEvent) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.action = event.Context
	if event.Event.Kind == "close" {
		delete(p.views, event.ID)
	}
	if event.Event.Kind != "resync" {
		return false, nil
	}
	state := p.views[event.ID]
	if state == nil {
		return true, nil
	}
	// Keep the old state on a write error; reset only the patch comparison.
	ui := state.ui
	ui.ViewVersion = state.ui.ViewVersion
	var err error
	p.data, err = EncodeUI(p.data, ui)
	if err == nil {
		err = p.writer.Write(UIUpdate, 0, p.data)
	}
	return true, err
}
