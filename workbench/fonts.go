package workbench

import (
	"errors"
	"runtime"
	"sort"
	"strings"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/font"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/projects/pencode/extensions"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/text"
	htmlui "github.com/diiyw/onix/ui/html"
)

type pencodeFont struct {
	label              string
	regular, bold      *text.Font
	source, boldSource font.SystemFont
}

func defaultPencodeFont(goos string) string {
	if goos == "darwin" || goos == "ios" {
		return "Menlo"
	}
	return "Courier New"
}

func (model *workspaceModel) loadFont(id string) bool {
	f, ok := model.fonts[id]
	if !ok {
		return false
	}
	if f.regular != nil {
		return true
	}
	face, err := f.source.Load()
	if err != nil {
		return false
	}
	f.regular = text.NewFont(face)
	if f.boldSource.Path == f.source.Path && f.boldSource.Index == f.source.Index {
		f.bold = f.regular
	} else if f.boldSource.Path != "" {
		if face, err := f.boldSource.Load(); err == nil {
			f.bold = text.NewFont(face)
		}
	}
	model.fonts[id] = f
	return true
}

func fontStyleDistance(source font.SystemFont, target int) int {
	distance := source.Weight - target
	if distance < 0 {
		distance = -distance
	}
	if source.Italic {
		distance += 1000
	}
	return distance
}

func (model *workspaceModel) resolveDefaultFont(goos string) string {
	preferred := [...]string{"Menlo", "Monaco", "Courier New"}
	if goos != "darwin" && goos != "ios" {
		preferred = [...]string{"Courier New", "Menlo", "Monaco"}
	}
	for _, id := range preferred {
		if model.loadFont(id) {
			return id
		}
	}
	ids := make([]string, 0, len(model.fonts))
	for id := range model.fonts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := model.fonts[ids[i]], model.fonts[ids[j]]
		if a.source.Monospace != b.source.Monospace {
			return a.source.Monospace
		}
		return ids[i] < ids[j]
	})
	for _, id := range ids {
		if model.loadFont(id) {
			return id
		}
	}
	return ""
}

func (model *workspaceModel) initializeFonts() error {
	if model.fonts == nil {
		model.fonts = make(map[string]pencodeFont)
	}
	if len(model.fonts) == 0 {
		installed, err := font.SystemFonts()
		if err != nil {
			return err
		}
		for _, source := range installed {
			f := model.fonts[source.Family]
			f.label = source.Family
			if f.source.Path == "" || fontStyleDistance(source, 400) < fontStyleDistance(f.source, 400) {
				f.source = source
			}
			if source.Weight >= 600 && (f.boldSource.Path == "" || fontStyleDistance(source, 700) < fontStyleDistance(f.boldSource, 700)) {
				f.boldSource = source
			}
			model.fonts[source.Family] = f
		}
	}
	model.defaultFontID = model.resolveDefaultFont(runtime.GOOS)
	if model.defaultFontID == "" {
		return errors.New("no usable system font")
	}
	model.defaultFont = model.fonts[model.defaultFontID].regular
	for _, selection := range []*string{&model.settings.CodeFont, &model.settings.UIFont} {
		if !model.loadFont(*selection) && !strings.HasPrefix(*selection, "extensions/") {
			*selection = model.defaultFontID
		}
	}
	return nil
}

func (model *workspaceModel) codeFont() *text.Font {
	if f, ok := model.fonts[model.settings.CodeFont]; ok && f.regular != nil {
		return f.regular
	}
	return model.defaultFont
}

func (model *workspaceModel) uiFont() (*text.Font, *text.Font) {
	if f, ok := model.fonts[model.settings.UIFont]; ok && f.regular != nil {
		return f.regular, f.bold
	}
	return model.defaultFont, nil
}

func (model *workspaceModel) applyUIFont() {
	if model.fontResources == nil {
		return
	}
	if surface := module.ContentMut[htmlui.Surface](model.fontResources, model.surface); surface != nil {
		surface.Font, surface.BoldFont = model.uiFont()
	}
}

// Options are rebuilt only when the registry changes. Selection edits reuse
// each option's reserved attribute slot, leaving steady UI sync allocation-free.
func (model *workspaceModel) rebuildFontOptions() {
	ids := make([]string, 0, len(model.fonts))
	for id := range model.fonts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	model.codeFontOptions = make([]htmlui.VNode, 0, len(ids))
	model.uiFontOptions = make([]htmlui.VNode, 0, len(ids))
	for _, id := range ids {
		f := model.fonts[id]
		model.codeFontOptions = append(model.codeFontOptions, appearanceOption(componentKey(id), id, f.label, false))
		model.uiFontOptions = append(model.uiFontOptions, appearanceOption(componentKey(id), id, f.label, false))
	}
	model.syncFontOptions()
}

func (model *workspaceModel) syncFontOptions() {
	codeID, uiID := model.settings.CodeFont, model.settings.UIFont
	if _, ok := model.fonts[codeID]; !ok {
		codeID = model.defaultFontID
	}
	if _, ok := model.fonts[uiID]; !ok {
		uiID = model.defaultFontID
	}
	for i := range model.codeFontOptions {
		for _, selection := range [...]struct {
			option *htmlui.VNode
			id     string
		}{
			{&model.codeFontOptions[i], codeID}, {&model.uiFontOptions[i], uiID},
		} {
			option := selection.option
			option.Attributes = option.Attributes[:1]
			if option.Attributes[0].Value == selection.id {
				option.Attributes = append(option.Attributes, htmlui.VNodeAttribute{Name: "selected"})
			}
		}
	}
}

func (model *workspaceModel) selectFont(id string, ui bool) bool {
	if !model.loadFont(id) {
		return false
	}
	if ui {
		model.settings.UIFont = id
		model.applyUIFont()
	} else {
		model.settings.CodeFont = id
	}
	model.syncFontOptions()
	return true
}

func (model *workspaceModel) importFonts(fonts []extensions.Font) {
	if len(fonts) == 0 {
		return
	}
	if model.fonts == nil {
		model.fonts = make(map[string]pencodeFont)
	}
	// Shared resource paths share their text wrapper and glyph atlas as well.
	wrappers := make(map[*font.Face]*text.Font)
	wrap := func(face *font.Face) *text.Font {
		if face == nil {
			return nil
		}
		if f := wrappers[face]; f != nil {
			return f
		}
		f := text.NewFont(face)
		wrappers[face] = f
		return f
	}
	for _, f := range fonts {
		model.fonts[f.ID] = pencodeFont{label: f.Label, regular: wrap(f.Regular), bold: wrap(f.Bold)}
	}
	model.rebuildFontOptions()
	model.applyUIFont()
}

// Rebind all retained views before disposing a removed extension's atlases.
func (model *workspaceModel) removeFonts(prefix string) bool {
	removed := make(map[*text.Font]bool)
	for id, f := range model.fonts {
		if strings.HasPrefix(id, prefix) {
			removed[f.regular] = true
			if f.bold != nil {
				removed[f.bold] = true
			}
			delete(model.fonts, id)
		}
	}
	changed := false
	if strings.HasPrefix(model.settings.CodeFont, prefix) {
		model.settings.CodeFont, changed = model.defaultFontID, true
	}
	if strings.HasPrefix(model.settings.UIFont, prefix) {
		model.settings.UIFont, changed = model.defaultFontID, true
	}
	if len(removed) != 0 {
		rebind := func(view *workspaceModel) {
			world := view.fontResources
			if world == nil {
				return
			}
			// ContentQuery excludes suspended scenes. Set their retained surface
			// directly so Enter rebuilds HTML with a live face before rendering.
			if s := module.ContentMut[htmlui.Surface](world, view.surface); s != nil {
				if removed[s.Font] {
					s.Font = model.defaultFont
				}
				if removed[s.BoldFont] {
					s.BoldFont = nil
				}
			}
			if binding := module.ContentMut[htmlui.ControlTextFont](world, view.editor.control); binding != nil && removed[binding.Font] {
				binding.Font = model.defaultFont
			}
			for _, entity := range [...]ecs.Entity{view.editor.text, view.editor.numbers} {
				if value := module.ContentMut[scene.Text2D](world, entity); value != nil && removed[value.Font] {
					value.Font = model.defaultFont
				}
			}
			for _, access := range module.ContentQuery[htmlui.Surface](world) {
				s := access.Ref()
				if removed[s.Font] {
					s.Font = model.defaultFont
				}
				if removed[s.BoldFont] {
					s.BoldFont = nil
				}
			}
			for _, access := range module.ContentQuery[htmlui.ControlTextFont](world) {
				binding := access.Ref()
				if removed[binding.Font] {
					binding.Font = model.defaultFont
				}
			}
			for _, access := range module.ContentQuery[scene.Text2D](world) {
				value := access.Ref()
				if removed[value.Font] {
					value.Font = model.defaultFont
				}
			}
		}
		if model.session == nil {
			rebind(model)
		} else {
			for view := range model.session.views {
				rebind(view.model)
			}
		}
		for f := range removed {
			_ = f.Dispose()
		}
	}
	model.rebuildFontOptions()
	return changed
}

func (model *workspaceModel) disposeFonts() {
	seen := make(map[*text.Font]bool)
	for _, f := range model.fonts {
		for _, face := range [...]*text.Font{f.regular, f.bold} {
			if face != nil && !seen[face] {
				seen[face] = true
				_ = face.Dispose()
			}
		}
	}
	clear(model.fonts)
}
