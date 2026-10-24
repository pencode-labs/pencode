package workbench

import (
	"fmt"
	"strings"

	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
	engineui "github.com/diiyw/onix/ui"
)

type pencodeTheme struct {
	id              string
	label           string
	theme           *syntax.Theme
	css             string
	icons           map[pencodeIconKey]string
	border, guide   graphics.ColorRGBA
	link            graphics.ColorRGBA
	bracket         graphics.ColorRGBA
	diagnosticError graphics.ColorRGBA
	editorStyle     engineui.StyleOverride[engineui.TextFieldStyle]
	selectStyle     engineui.HTMLSelectStyle
}

type pencodeIconKey struct{ source, foreground string }

var pencodeThemeIcons = [...]struct{ ref, source, foreground string }{
	{"status-source-icon", "icons/source-control.svg", "icon.foreground"},
	{"open-marketplace-icon", "icons/extensions.svg", "icon.foreground"},
	{"close-marketplace-icon", "icons/close.svg", "icon.foreground"},
	{"change-project-icon", "icons/folder-opened.svg", "icon.foreground"},
	{"open-settings-icon", "icons/settings.svg", "icon.foreground"},
	{"window-minimize-icon", "icons/minimize.svg", "icon.foreground"},
	{"window-maximize-icon", "icons/maximize.svg", "icon.foreground"},
	{"window-close-icon", "icons/close.svg", "icon.foreground"},
	{"close-settings-icon", "icons/close.svg", "icon.foreground"},
	{"open-project-icon", "icons/folder-opened.svg", "button.foreground"},
	{"collapse-directories-icon", "icons/list-collapse.svg", "icon.foreground"},
	{"reveal-active-file-icon", "icons/locate-fixed.svg", "icon.foreground"},
	{"reload-from-disk-icon", "icons/refresh-cw.svg", "icon.foreground"},
	{"find-previous-icon", "icons/chevron-right.svg", "icon.foreground"},
	{"find-next-icon", "icons/chevron-right.svg", "icon.foreground"},
	{"find-close-icon", "icons/close.svg", "icon.foreground"},
	{"output-clear-icon", "icons/clear-all.svg", "icon.foreground"},
	{"terminal-new-icon", "icons/add.svg", "icon.foreground"},
	{"panel-close-icon", "icons/close.svg", "icon.foreground"},
	{"lsp-close-icon", "icons/close.svg", "icon.foreground"},
	{"lsp-previous-icon", "icons/chevron-right.svg", "icon.foreground"},
	{"lsp-next-icon", "icons/chevron-right.svg", "icon.foreground"},
}

// Missing component colors inherit their parent surface before using the
// built-in light/dark palette. Explicit transparent colors remain transparent.
var pencodeColorParents = map[string]string{
	"titleBar.activeForeground":       "foreground",
	"sideBar.foreground":              "foreground",
	"sideBarSectionHeader.foreground": "sideBar.foreground",
	"sideBarSectionHeader.background": "sideBar.background",
	"tab.activeBackground":            "editor.background",
	"tab.activeForeground":            "editor.foreground",
	"tab.hoverForeground":             "tab.activeForeground",
	"editorWidget.foreground":         "foreground",
	"editorWidget.border":             "widget.border",
	"quickInput.background":           "editorWidget.background",
	"quickInput.foreground":           "foreground",
	"settings.headerForeground":       "foreground",
	"settings.dropdownBackground":     "dropdown.background",
	"settings.dropdownForeground":     "dropdown.foreground",
	"settings.dropdownBorder":         "dropdown.border",
	"icon.foreground":                 "foreground",
	"problemsErrorIcon.foreground":    "editorError.foreground",
	"problemsWarningIcon.foreground":  "editorWarning.foreground",
	"problemsInfoIcon.foreground":     "editorInfo.foreground",
	"badge.background":                "list.hoverBackground",
	"badge.foreground":                "foreground",
	"input.placeholderForeground":     "descriptionForeground",
}

func preparePencodeTheme(id string, theme *syntax.Theme, source string) (pencodeTheme, error) {
	defaultID := "dark-plus"
	if theme.Type == "light" {
		defaultID = "light-plus"
	}
	defaults, err := syntax.BuiltinTheme(defaultID)
	if err != nil {
		return pencodeTheme{}, err
	}
	color := func(key string) (syntax.Color, error) {
		for parent := key; parent != ""; parent = pencodeColorParents[parent] {
			if value, ok := theme.LookupColor(parent); ok {
				return value, nil
			}
		}
		switch key {
		case "editor.background", "editor.foreground", "editorGutter.background", "editorCursor.foreground":
			return theme.Color(key), nil
		}
		for parent := key; parent != ""; parent = pencodeColorParents[parent] {
			if value, ok := defaults.LookupColor(parent); ok {
				return value, nil
			}
		}
		decoration := func(dark, light syntax.Color) syntax.Color {
			if theme.Type == "light" {
				return light
			}
			return dark
		}
		switch key {
		case "problemsErrorIcon.foreground":
			return decoration(syntax.Color{R: 244, G: 135, B: 113, A: 255}, syntax.Color{R: 232, G: 17, B: 35, A: 255}), nil
		case "problemsWarningIcon.foreground":
			return decoration(syntax.Color{R: 204, G: 167, B: 0, A: 255}, syntax.Color{R: 191, G: 136, B: 3, A: 255}), nil
		case "problemsInfoIcon.foreground":
			return decoration(syntax.Color{R: 117, G: 190, B: 255, A: 255}, syntax.Color{R: 26, G: 133, B: 255, A: 255}), nil
		case "editorHint.foreground":
			return decoration(syntax.Color{R: 178, G: 178, B: 178, A: 255}, syntax.Color{R: 108, G: 108, B: 108, A: 255}), nil
		case "scmGraph.foreground1":
			return decoration(syntax.Color{R: 70, G: 166, B: 239, A: 255}, syntax.Color{R: 0, G: 109, B: 190, A: 255}), nil
		case "scmGraph.foreground2":
			return decoration(syntax.Color{R: 202, G: 102, B: 233, A: 255}, syntax.Color{R: 140, G: 54, B: 176, A: 255}), nil
		case "scmGraph.foreground3":
			return decoration(syntax.Color{R: 227, G: 161, B: 90, A: 255}, syntax.Color{R: 184, G: 100, B: 0, A: 255}), nil
		case "scmGraph.foreground4":
			return decoration(syntax.Color{R: 109, G: 196, B: 139, A: 255}, syntax.Color{R: 40, G: 122, B: 56, A: 255}), nil
		case "scmGraph.foreground5":
			return decoration(syntax.Color{R: 235, G: 115, B: 115, A: 255}, syntax.Color{R: 196, G: 57, B: 57, A: 255}), nil
		case "scmGraph.foreground6":
			return decoration(syntax.Color{R: 100, G: 199, B: 213, A: 255}, syntax.Color{R: 0, G: 127, B: 140, A: 255}), nil
		case "editor.findMatchHighlightBackground":
			return syntax.Color{R: 234, G: 92, A: 85}, nil
		case "diffEditor.insertedLineBackground":
			return syntax.Color{R: 40, G: 167, B: 69, A: 48}, nil
		case "diffEditor.removedLineBackground":
			return syntax.Color{R: 220, G: 53, B: 69, A: 48}, nil
		case "diffEditor.insertedTextBackground":
			return syntax.Color{R: 40, G: 167, B: 69, A: 96}, nil
		case "diffEditor.removedTextBackground":
			return syntax.Color{R: 220, G: 53, B: 69, A: 96}, nil
		// VS Code's default Git decoration colors; themes may still override them.
		case "gitDecoration.addedResourceForeground", "testing.iconPassed":
			return decoration(syntax.Color{R: 129, G: 184, B: 139, A: 255}, syntax.Color{R: 88, G: 124, B: 12, A: 255}), nil
		case "gitDecoration.modifiedResourceForeground":
			return decoration(syntax.Color{R: 226, G: 192, B: 141, A: 255}, syntax.Color{R: 137, G: 85, B: 3, A: 255}), nil
		case "gitDecoration.deletedResourceForeground":
			return decoration(syntax.Color{R: 199, G: 78, B: 57, A: 255}, syntax.Color{R: 173, G: 7, B: 7, A: 255}), nil
		case "gitDecoration.renamedResourceForeground":
			return decoration(syntax.Color{R: 115, G: 201, B: 145, A: 255}, syntax.Color{R: 0, G: 113, B: 0, A: 255}), nil
		case "gitDecoration.conflictingResourceForeground":
			return decoration(syntax.Color{R: 228, G: 103, B: 107, A: 255}, syntax.Color{R: 173, G: 7, B: 7, A: 255}), nil
		}
		return syntax.Color{}, fmt.Errorf("unknown workbench theme color %q", key)
	}
	var css strings.Builder
	var variables strings.Builder
	variables.WriteString("\n.pencode-root {")
	seenVariables := make(map[string]bool)
	css.Grow(len(source))
	// Resolve the CSS template once; frames and theme switches reuse the result.
	const prefix = "var(--vscode-"
	for {
		before, rest, found := strings.Cut(source, prefix)
		css.WriteString(before)
		if !found {
			break
		}
		name, after, closed := strings.Cut(rest, ")")
		if !closed {
			return pencodeTheme{}, fmt.Errorf("unclosed workbench theme color")
		}
		value, err := color(strings.ReplaceAll(name, "-", "."))
		if err != nil {
			return pencodeTheme{}, err
		}
		css.WriteString(value.Hex())
		if !seenVariables[name] {
			seenVariables[name] = true
			variables.WriteString("--vscode-")
			variables.WriteString(name)
			variables.WriteByte(':')
			variables.WriteString(value.Hex())
			variables.WriteByte(';')
		}
		source = after
	}
	variables.WriteByte('}')
	css.WriteString(variables.String())
	result := pencodeTheme{id: id, theme: theme, css: css.String(), icons: make(map[pencodeIconKey]string, len(pencodeThemeIcons)+6)}
	addIcon := func(key pencodeIconKey) error {
		if _, exists := result.icons[key]; exists {
			return nil
		}
		value, err := color(key.foreground)
		if err != nil {
			return err
		}
		result.icons[key] = key.source + "?foreground=" + value.Hex()[1:]
		return nil
	}
	for _, icon := range pencodeThemeIcons {
		if err := addIcon(pencodeIconKey{icon.source, icon.foreground}); err != nil {
			return pencodeTheme{}, err
		}
	}
	for _, severity := range pencodeProblemSeverities {
		if err := addIcon(severity.icon); err != nil {
			return pencodeTheme{}, err
		}
	}
	for _, name := range []string{"add", "remove", "ellipsis", "chevron-down", "git-commit", "git-branch", "git-fetch", "repo-pull", "repo-push", "git-merge-abort", "check", "circle-filled", "chevron-right", "arrow-right", "arrow-up", "folder", "file-text", "refresh-cw", "close", "discard"} {
		if err := addIcon(pencodeIconKey{"icons/" + name + ".svg", "icon.foreground"}); err != nil {
			return pencodeTheme{}, err
		}
	}
	// Retained rows and tabs select their foreground without rebuilding SVGs.
	for _, key := range [...]pencodeIconKey{
		{"icons/git-commit.svg", "button.foreground"},
		{"icons/refresh-cw.svg", "button.foreground"},
		{"icons/arrow-right.svg", "button.foreground"},
		{"icons/file-text.svg", "icon.foreground"},
		{"icons/file-text.svg", "list.activeSelectionForeground"},
		{"icons/file-text.svg", "tab.activeForeground"},
		{"icons/file-text.svg", "tab.inactiveForeground"},
		{"icons/close.svg", "tab.activeForeground"},
		{"icons/close.svg", "tab.inactiveForeground"},
	} {
		if err := addIcon(key); err != nil {
			return pencodeTheme{}, err
		}
	}
	border, err := color("window.activeBorder")
	if err != nil {
		return pencodeTheme{}, err
	}
	guide, err := color("editorRuler.foreground")
	if err != nil {
		return pencodeTheme{}, err
	}
	result.border, result.guide = pencodeSyntaxColor(border), pencodeSyntaxColor(guide)
	link, err := color("textLink.foreground")
	if err != nil {
		return pencodeTheme{}, err
	}
	result.link = pencodeSyntaxColor(link)
	errorColor, err := color("errorForeground")
	if err != nil {
		return pencodeTheme{}, err
	}
	if value, ok := theme.LookupColor("editorError.foreground"); ok {
		errorColor = value
	}
	result.diagnosticError = pencodeSyntaxColor(errorColor)
	result.bracket = result.link
	if value, ok := theme.LookupColor("editorBracketHighlight.foreground1"); ok {
		result.bracket = pencodeSyntaxColor(value)
	}
	selection, err := color("editor.selectionBackground")
	if err != nil {
		return pencodeTheme{}, err
	}
	result.editorStyle.Value = engineui.TextFieldStyle{
		Body:      engineui.NewStateStyle(engineui.ColorSurface(pencodeSyntaxColor(theme.Color("editor.background")))),
		Caret:     engineui.ColorSurface(pencodeSyntaxColor(theme.Color("editorCursor.foreground"))),
		Selection: engineui.ColorSurface(pencodeSyntaxColor(selection)),
	}
	activeBackground, err := color("list.activeSelectionBackground")
	if err != nil {
		return pencodeTheme{}, err
	}
	activeForeground, err := color("list.activeSelectionForeground")
	if err != nil {
		return pencodeTheme{}, err
	}
	active := engineui.ColorSurface(pencodeSyntaxColor(activeBackground))
	text := pencodeSyntaxColor(activeForeground)
	result.selectStyle = engineui.HTMLSelectStyle{
		Item: engineui.NewStateStyle(engineui.SurfaceStyle{}).WithHovered(active).WithPressed(active).WithSelected(active),
		Text: engineui.NewStateStyle(graphics.ColorRGBA{}).WithHovered(text).WithPressed(text).WithSelected(text),
	}
	return result, nil
}

func (model *workspaceModel) themedIcon(source, foreground string) string {
	if theme := model.workbenchTheme; theme != nil {
		if value, ok := theme.icons[pencodeIconKey{source, foreground}]; ok {
			return value
		}
	}
	return source
}

func (model *workspaceModel) syncWorkbenchTheme(world *module.Content) {
	theme := model.workbenchTheme
	if theme == nil {
		return
	}
	world.HTMLSetCSS(model.surface, theme.css)
	if current, ok := module.ContentGet[engineui.HTMLSelectStyle](world, model.surface); !ok || current.Item.Selected != theme.selectStyle.Item.Selected {
		_ = module.ContentInsert(world, model.surface, theme.selectStyle)
	}
	if border, ok := module.ContentGet[scene.UIBorder2D](world, model.windowBorder); ok && border.Shape.Border != theme.border {
		module.ContentMut[scene.UIBorder2D](world, model.windowBorder).Shape.Border = theme.border
	}
}
