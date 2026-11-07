package workbench

import (
	"errors"
	"runtime"
	"sort"
	"strings"

	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/font"
	"github.com/onix-engine/onix/scene"
	"github.com/onix-engine/onix/text"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
)

type pencodeFont struct {
	label              string
	regular, bold      *text.Font
	source, boldSource font.SystemFont
}

func defaultPencodeFont(goos string) string {
	if goos == "darwin" {
		return "SF Mono"
	}
	if goos == "windows" {
		return "Consolas"
	}
	if goos == "darwin" || goos == "ios" {
		return "Menlo"
	}
	return "Courier New"
}

func defaultPencodeUIFont(goos string) string {
	if goos == "windows" {
		return "Microsoft YaHei"
	}
	return defaultPencodeFont(goos)
}

// defaultUIFontSelection follows the font CoreText uses for native macOS UI.
// The system font catalog stores the same file path, so resolve that family
// back to the selectable font registry used by the workbench.
func defaultUIFontSelection() string {
	if runtime.GOOS == "darwin" {
		if path, err := font.DefaultFontPath(); err == nil {
			if installed, err := font.SystemFonts(); err == nil {
				if family := systemUIFontFamily(path, installed); family != "" {
					return family
				}
			}
		}
	}
	return defaultPencodeUIFont(runtime.GOOS)
}

func systemUIFontFamily(path string, installed []font.SystemFont) string {
	best, distance := "", int(^uint(0)>>1)
	fallback, fallbackDistance := "", int(^uint(0)>>1)
	for _, source := range installed {
		if source.Path != path {
			continue
		}
		d := fontStyleDistance(source, 400)
		if fallback == "" || d < fallbackDistance || d == fallbackDistance && source.Family < fallback {
			fallback, fallbackDistance = source.Family, d
		}
		if source.Monospace {
			continue
		}
		if best == "" || d < distance || d == distance && source.Family < best {
			best, distance = source.Family, d
		}
	}
	if best != "" {
		return best
	}
	return fallback
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
	f.regular = newPencodeTextFont(face)
	if f.boldSource.Path == f.source.Path && f.boldSource.Index == f.source.Index {
		f.bold = f.regular
	} else if f.boldSource.Path != "" {
		if face, err := f.boldSource.Load(); err == nil {
			f.bold = newPencodeTextFont(face)
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
	if goos == "darwin" && model.loadFont("SF Mono") {
		return "SF Mono"
	}
	if goos == "windows" && model.loadFont("Consolas") {
		return "Consolas"
	}
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
	if _, err := pencodeSymbols(); err != nil {
		return err
	}
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
	model.defaultUIFontID = model.defaultFontID
	if id := defaultUIFontSelection(); model.loadFont(id) {
		model.defaultUIFontID = id
	}
	for _, selection := range [...]struct {
		id       *string
		fallback string
	}{
		{&model.settings.CodeFont, model.defaultFontID},
		{&model.settings.UIFont, model.defaultUIFontID},
	} {
		if !model.loadFont(*selection.id) && !strings.HasPrefix(*selection.id, "extensions/") {
			*selection.id = selection.fallback
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
	f := model.fonts[model.defaultUIFontID]
	return f.regular, f.bold
}

func (model *workspaceModel) applyUIFont() {
	if model.fontResources == nil {
		return
	}
	if surface := model.fontResources.Mut[htmlui.Surface](model.surface); surface != nil {
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
		uiID = model.defaultUIFontID
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
		f := newPencodeTextFont(face)
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
		model.settings.UIFont, changed = model.defaultUIFontID, true
	}
	if len(removed) != 0 {
		rebind := func(view *workspaceModel) {
			world := view.fontResources
			if world == nil {
				return
			}
			// ContentQuery excludes suspended scenes. Set their retained surface
			// directly so Enter rebuilds HTML with a live face before rendering.
			if s := world.Mut[htmlui.Surface](view.surface); s != nil {
				if removed[s.Font] {
					s.Font, s.BoldFont = model.uiFont()
				}
				if removed[s.BoldFont] {
					s.BoldFont = nil
				}
			}
			if binding := world.Mut[htmlui.ControlTextFont](view.editor.control); binding != nil && removed[binding.Font] {
				binding.Font = model.defaultFont
			}
			for _, entity := range [...]ecs.Entity{view.editor.text, view.editor.numbers} {
				if value := world.Mut[scene.Text2D](entity); value != nil && removed[value.Font] {
					value.Font = model.defaultFont
				}
			}
			for _, access := range world.Query[htmlui.Surface]() {
				s := access.Ref()
				if removed[s.Font] {
					s.Font, s.BoldFont = model.uiFont()
				}
				if removed[s.BoldFont] {
					s.BoldFont = nil
				}
			}
			for _, access := range world.Query[htmlui.ControlTextFont]() {
				binding := access.Ref()
				if removed[binding.Font] {
					binding.Font = model.defaultFont
				}
			}
			for _, access := range world.Query[scene.Text2D]() {
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
