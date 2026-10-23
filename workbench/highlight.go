package workbench

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/diiyw/onix/graphics"
	"github.com/pencode-labs/pencode/configuration"
	"github.com/diiyw/onix/syntax"
	htmlui "github.com/diiyw/onix/ui/html"
)

func pencodeSyntaxColor(color syntax.Color) graphics.ColorRGBA {
	return graphics.ColorFromRGBA(color.R, color.G, color.B, color.A)
}

func (model *workspaceModel) ensureSyntaxTheme() {
	if model.syntaxTheme == nil {
		model.syntaxTheme, _ = syntax.BuiltinTheme("monokai")
	}
}

func (model *workspaceModel) loadSyntaxSettings() error {
	var snapshot configuration.Snapshot
	if model.settingsPath != "" {
		var err error
		snapshot, err = configuration.Read(context.Background(), filepath.Dir(model.settingsPath))
		if err != nil {
			model.settingsFeedback = fmt.Sprintf("读取配置失败：%v", err)
		}
	}
	return model.loadSyntaxResources(snapshot)
}

func (model *workspaceModel) loadSyntaxResources(snapshot configuration.Snapshot) error {
	for _, id := range syntax.BuiltinThemeIDs {
		theme, err := syntax.BuiltinTheme(id)
		if err != nil {
			return err
		}
		prepared, err := preparePencodeTheme(id, theme, model.themeSourceCSS)
		if err != nil {
			return err
		}
		model.themes = append(model.themes, prepared)
	}

	names := make([]string, 0, len(snapshot.Files))
	for name := range snapshot.Files {
		if strings.HasPrefix(name, "themes/") && strings.Count(name, "/") == 1 {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, id := range names {
		theme, err := syntax.LoadTheme(snapshot, id)
		if err != nil {
			model.settingsFeedback = fmt.Sprintf("加载主题 %s 失败：%v", id, err)
			continue
		}
		prepared, err := preparePencodeTheme(id, theme, model.themeSourceCSS)
		if err != nil {
			return err
		}
		model.themes = append(model.themes, prepared)
	}
	if data, ok := snapshot.Files["highlights.json"]; ok {
		definitions, err := syntax.ParseDefinitions(data)
		if err != nil {
			model.settingsFeedback = fmt.Sprintf("加载高亮定义失败：%v", err)
		} else {
			model.syntaxDefinitions = syntax.DefaultDefinitions().WithOverrides(definitions)
		}
	}

	model.rebuildThemeOptions()
	requested := model.settings.Theme
	if !model.selectTheme(requested) {
		model.selectTheme("monokai")
		if strings.HasPrefix(requested, "extensions/") {
			model.settings.Theme = requested
		}
	}
	model.rebuildIconThemeOptions()
	return nil
}

func (model *workspaceModel) selectTheme(id string) bool {
	for index := range model.themes {
		theme := &model.themes[index]
		if theme.id != id {
			continue
		}
		if model.workbenchTheme == theme {
			return true
		}
		model.syntaxTheme = theme.theme
		model.workbenchTheme = theme
		model.settings.Theme = id
		// Retain the tree and captures; only the palette and text spans change.
		model.highlighted = false
		for i := range model.themeOptions {
			option := &model.themeOptions[i]
			option.Attributes = option.Attributes[:1]
			if option.Attributes[0].Value == id {
				option.Attributes = append(option.Attributes, htmlui.VNodeAttribute{Name: "selected"})
			}
		}
		model.refreshSettingsLabels()
		return true
	}
	return false
}
