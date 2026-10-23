package workbench

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
	htmlui "github.com/diiyw/onix/ui/html"
)

type pencodeExtensionState uint8

const (
	pencodeExtensionLoaded pencodeExtensionState = 1 + iota
	pencodeExtensionRemoved
	pencodeExtensionDisabled
)

func (model *workspaceModel) setExtensionState(id string, state pencodeExtensionState) {
	if model.extensionStates == nil {
		model.extensionStates = make(map[string]pencodeExtensionState)
	}
	model.extensionStates[id] = state
}

func (model *workspaceModel) removeExtension(id string) {
	model.setExtensionState(id, pencodeExtensionRemoved)
	model.unloadExtension(id)
}

func (model *workspaceModel) unloadExtension(id string) {
	model.removeActions(id)
	model.unloadContributions("extensions/"+id+"/", "peb://"+id+"/")
}

func (model *workspaceModel) unloadContributions(prefix, sourcePrefix string) {
	model.removeLanguageServers(prefix)
	model.removeEditors(prefix)
	selected, active := model.settings.Theme, "monokai"
	if model.workbenchTheme != nil {
		active = model.workbenchTheme.id
	}
	changedSettings := model.removeFonts(prefix)
	if strings.HasPrefix(selected, prefix) {
		selected = "monokai"
		changedSettings = true
	}
	if strings.HasPrefix(active, prefix) {
		active = "monokai"
	}
	model.workbenchTheme = nil
	themes := model.themes[:0]
	for _, theme := range model.themes {
		if strings.HasPrefix(theme.id, prefix) {
			continue
		}
		for key := range theme.icons {
			if strings.HasPrefix(key.source, sourcePrefix) {
				delete(theme.icons, key)
			}
		}
		themes = append(themes, theme)
	}
	clear(model.themes[len(themes):])
	model.themes = themes
	icons := model.fileIconThemes[:0]
	for _, theme := range model.fileIconThemes {
		if !strings.HasPrefix(theme.id, prefix) {
			icons = append(icons, theme)
		}
	}
	clear(model.fileIconThemes[len(icons):])
	model.fileIconThemes = icons
	for source := range model.extensionIcons {
		if strings.HasPrefix(source, sourcePrefix) {
			delete(model.extensionIcons, source)
		}
	}
	for source := range model.extensionImages {
		if strings.HasPrefix(source, sourcePrefix) {
			delete(model.extensionImages, source)
		}
	}
	if strings.HasPrefix(model.settings.IconTheme, prefix) {
		model.settings.IconTheme = ""
		model.fileIcons = nil
		changedSettings = true
	}
	model.rebuildThemeOptions()
	if !model.selectTheme(selected) {
		model.selectTheme(active)
		model.settings.Theme = selected
	}
	model.rebuildIconThemeOptions()
	model.selectIconTheme(model.settings.IconTheme)
	if model.session != nil {
		for view := range model.session.views {
			if view.icons == nil {
				continue
			}
			for source, texture := range view.icons.textures {
				if strings.HasPrefix(source, sourcePrefix) {
					if texture != nil {
						_ = texture.Dispose()
					}
					delete(view.icons.textures, source)
				}
			}
		}
	}
	if changedSettings && model.settingsPath != "" {
		model.saveSettings()
	}
}

// Immutable lookup tables keep matching independent of the total rule count.
// Filename suffixes are tested from the first dot to the last, longest first.
type pencodeFileIcons struct {
	id, label                string
	file, folder             string
	names, suffixes, folders map[string]string
}

func newPencodeFileIcons(t peb.IconTheme) *pencodeFileIcons {
	icons := &pencodeFileIcons{id: t.ID, label: t.Label, names: make(map[string]string), suffixes: make(map[string]string), folders: make(map[string]string)}
	for _, r := range t.Rules {
		switch r.Match {
		case peb.DefaultFile:
			icons.file = r.Path
		case peb.DefaultFolder:
			icons.folder = r.Path
		case peb.FileName:
			icons.names[r.Pattern] = r.Path
		case peb.FileSuffix:
			icons.suffixes[r.Pattern] = r.Path
		case peb.FolderName:
			icons.folders[r.Pattern] = r.Path
		}
	}
	return icons
}

func (icons *pencodeFileIcons) source(name string, directory bool) string {
	if icons == nil {
		return ""
	}
	if directory {
		if source := icons.folders[name]; source != "" {
			return source
		}
		return icons.folder
	}
	if source := icons.names[name]; source != "" {
		return source
	}
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			if source := icons.suffixes[name[i:]]; source != "" {
				return source
			}
		}
	}
	return icons.file
}

func (s *Session) startExtensions(model *workspaceModel) {
	if s.extensions != nil || s.backend == nil {
		return
	}
	if s.connection != nil && (s.connection.launch || s.connection.busy || s.connection.lostHandled || s.backend.Err() != nil) {
		return
	}
	s.extensions = extensions.Start(s.backend, model.backgroundWake(), &s.Output)
}

func (model *workspaceModel) pollExtensions(host *extensions.Host) bool {
	if host == nil {
		return false
	}
	changed := false
	// Bound each frame's imported results to the host's worker count.
	for range 4 {
		select {
		case result, ok := <-host.Results:
			if !ok {
				return changed
			}
			changed = true
			if model.extensionStates[result.ID] != 0 {
				result.Bundle.Close()
				continue
			}
			if result.Err != nil {
				model.extensionFeedback = fmt.Sprintf("扩展 %s 加载失败：%v", result.ID, result.Err)
				model.logOutput("Extension: "+result.ID, model.extensionFeedback)
				continue
			}
			if err := model.importExtension(result.Bundle); err != nil {
				result.Bundle.Close()
				model.extensionFeedback = fmt.Sprintf("扩展 %s 加载失败：%v", result.ID, err)
				model.logOutput("Extension: "+result.ID, model.extensionFeedback)
			} else {
				model.setExtensionState(result.ID, pencodeExtensionLoaded)
				model.logOutput("Extension: "+result.ID, "扩展已加载")
			}
		default:
			return changed
		}
	}
	if len(host.Results) != 0 && model.app != nil && model.app.WakeAt != nil {
		model.backgroundWake()()
	}
	return changed
}

func (model *workspaceModel) importExtension(bundle *extensions.Bundle) error {
	if bundle == nil {
		return nil
	}
	// Prepare every theme before publishing any part of a registration.
	prepared := make([]pencodeTheme, 0, len(bundle.Themes))
	for _, t := range bundle.Themes {
		theme, err := preparePencodeTheme(t.ID, t.Theme, model.themeSourceCSS)
		if err != nil {
			return err
		}
		theme.label = t.Label
		prepared = append(prepared, theme)
	}
	if model.extensionIcons == nil {
		model.extensionIcons = make(map[string][]byte)
	}
	for source, data := range bundle.Icons {
		model.extensionIcons[source] = data
	}
	selected, active := model.settings.Theme, model.workbenchTheme.id
	previousCount := len(model.themes)
	model.themes = append(model.themes, prepared...)
	for i := range model.themes {
		icons := bundle.Icons
		if i >= previousCount {
			icons = model.extensionIcons
		}
		model.themes[i].cacheExtensionIcons(icons)
	}
	model.rebuildThemeOptions()
	if !model.selectTheme(selected) {
		model.selectTheme(active)
		model.settings.Theme = selected
	}
	for _, t := range bundle.IconThemes {
		model.fileIconThemes = append(model.fileIconThemes, newPencodeFileIcons(t))
	}
	sort.Slice(model.fileIconThemes, func(i, j int) bool { return model.fileIconThemes[i].id < model.fileIconThemes[j].id })
	model.rebuildIconThemeOptions()
	model.selectIconTheme(model.settings.IconTheme)
	model.importLanguageServers(bundle.LanguageServers)
	model.importEditors(bundle.Editors)
	model.importFonts(bundle.Fonts)
	model.importActions(bundle.Actions)
	return nil
}

func (theme *pencodeTheme) cacheExtensionIcons(icons map[string][]byte) {
	if len(icons) == 0 {
		return
	}
	for _, foreground := range [...]string{"icon.foreground", "list.activeSelectionForeground", "tab.activeForeground", "tab.inactiveForeground"} {
		suffix := strings.TrimPrefix(theme.icons[pencodeIconKey{"icons/file-text.svg", foreground}], "icons/file-text.svg")
		for source := range icons {
			key := pencodeIconKey{source, foreground}
			if _, exists := theme.icons[key]; !exists {
				theme.icons[key] = source + suffix
			}
		}
	}
}

func appearanceOption(key uint64, id, label string, selected bool) htmlui.VNode {
	option := htmlui.VNode{Key: key, Tag: "option", Text: label, Attributes: make([]htmlui.VNodeAttribute, 1, 2)}
	option.Attributes[0] = htmlui.VNodeAttribute{Name: "value", Value: id}
	if selected {
		option.Attributes = append(option.Attributes, htmlui.VNodeAttribute{Name: "selected"})
	}
	return option
}

func (model *workspaceModel) rebuildThemeOptions() {
	// Preserve built-in ordering; user and extension IDs remain stable across runs.
	model.themeOptions = make([]htmlui.VNode, len(model.themes))
	active := ""
	if model.workbenchTheme != nil {
		active = model.workbenchTheme.id
	}
	for i, t := range model.themes {
		label := t.label
		if label == "" {
			label = t.theme.Name
		}
		model.themeOptions[i] = appearanceOption(componentKey(t.id), t.id, label, t.id == active)
	}
}

func (model *workspaceModel) rebuildIconThemeOptions() {
	model.iconThemeOptions = make([]htmlui.VNode, 1, len(model.fileIconThemes)+1)
	model.iconThemeOptions[0] = appearanceOption(componentKey("builtin-icons"), "", "内置", model.fileIcons == nil)
	for _, t := range model.fileIconThemes {
		model.iconThemeOptions = append(model.iconThemeOptions, appearanceOption(componentKey(t.id), t.id, t.label, model.fileIcons == t))
	}
}

func (model *workspaceModel) selectIconTheme(id string) bool {
	var selected *pencodeFileIcons
	if id != "" {
		for _, t := range model.fileIconThemes {
			if t.id == id {
				selected = t
				break
			}
		}
		if selected == nil {
			return false
		}
	}
	model.fileIcons, model.settings.IconTheme = selected, id
	for i := range model.iconThemeOptions {
		option := &model.iconThemeOptions[i]
		option.Attributes = option.Attributes[:1]
		if option.Attributes[0].Value == id {
			option.Attributes = append(option.Attributes, htmlui.VNodeAttribute{Name: "selected"})
		}
	}
	return true
}
