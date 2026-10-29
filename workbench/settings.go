package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/pencode-labs/pencode/configuration"
	"github.com/pencode-labs/pencode/peb"
)

const (
	pencodeDefaultFontSize    = 13
	pencodeMinFontSize        = 10
	pencodeMaxFontSize        = 32
	pencodeDefaultUIFontSize  = 14
	pencodeMinUIFontSize      = 10
	pencodeMaxUIFontSize      = 20
	pencodeDefaultEditorWidth = 100
	pencodeMinEditorWidth     = 20
	pencodeMaxEditorWidth     = 100
	pencodeEditorWidthStep    = 5
	pencodeDefaultZoom        = 100
	pencodeMinZoom            = 70
	pencodeMaxZoom            = 150
)

type pencodeShortcutAction uint8

const (
	pencodeShortcutSave pencodeShortcutAction = iota
	pencodeShortcutUndo
	pencodeShortcutRedo
	pencodeShortcutFind
	pencodeShortcutReplace
	pencodeShortcutQuickOpen
	pencodeShortcutClose
	pencodeShortcutNext
	pencodeShortcutPrevious
	pencodeShortcutSidebar
	pencodeShortcutZoomIn
	pencodeShortcutZoomOut
	pencodeShortcutZoomReset
	pencodeShortcutSettings
	pencodeShortcutCount
)

type pencodeKeybinding struct {
	Key     input.KeyCode `json:"key"`
	Primary bool          `json:"primary,omitempty"`
	Control bool          `json:"control,omitempty"`
	Super   bool          `json:"super,omitempty"`
	Shift   bool          `json:"shift,omitempty"`
	Alt     bool          `json:"alt,omitempty"`
}

type pencodeShortcutDefinition struct {
	id, ref, name string
	defaultKey    pencodeKeybinding
}

var pencodeShortcutDefinitions = [...]pencodeShortcutDefinition{
	{"save", "shortcut-save", "保存文件", pencodeKeybinding{Key: input.KeyS, Primary: true}},
	{"undo", "shortcut-undo", "撤销", pencodeKeybinding{Key: input.KeyZ, Primary: true}},
	{"redo", "shortcut-redo", "重做", pencodeKeybinding{Key: input.KeyY, Primary: true}},
	{"find", "shortcut-find", "查找", pencodeKeybinding{Key: input.KeyF, Primary: true}},
	{"replace", "shortcut-replace", "替换", pencodeKeybinding{Key: input.KeyH, Primary: true}},
	{"quick-open", "shortcut-quick-open", "快速打开文件", pencodeKeybinding{Key: input.KeyP, Primary: true}},
	{"close", "shortcut-close", "关闭标签", pencodeKeybinding{Key: input.KeyW, Primary: true}},
	{"next", "shortcut-next", "下一个标签", pencodeKeybinding{Key: input.KeyTab, Control: true}},
	{"previous", "shortcut-previous", "上一个标签", pencodeKeybinding{Key: input.KeyTab, Control: true, Shift: true}},
	{"sidebar", "shortcut-sidebar", "切换侧栏", pencodeKeybinding{Key: input.KeyB, Primary: true}},
	{"zoom-in", "shortcut-zoom-in", "放大界面", pencodeKeybinding{Key: input.KeyEqual, Primary: true}},
	{"zoom-out", "shortcut-zoom-out", "缩小界面", pencodeKeybinding{Key: input.KeyMinus, Primary: true}},
	{"zoom-reset", "shortcut-zoom-reset", "重置缩放", pencodeKeybinding{Key: input.KeyNum0, Primary: true}},
	{"settings", "shortcut-settings", "打开设置", pencodeKeybinding{Key: input.KeyComma, Primary: true}},
}

type pencodeSettings struct {
	FontSize    int                                     `json:"fontSize"`
	UIFontSize  int                                     `json:"uiFontSize"`
	EditorWidth int                                     `json:"editorWidth"`
	CodeFont    string                                  `json:"codeFont"`
	UIFont      string                                  `json:"uiFont"`
	Theme       string                                  `json:"theme"`
	IconTheme   string                                  `json:"iconTheme"`
	Zoom        int                                     `json:"zoom"`
	Bindings    map[string]pencodeKeybinding            `json:"bindings"`
	Keymap      string                                  `json:"keymap"`
	Keymaps     map[string]map[string]pencodeKeybinding `json:"keymaps,omitempty"`
	Extensions  map[string]peb.SettingValues            `json:"extensions,omitempty"`
}

func defaultPencodeSettings() pencodeSettings {
	settings := pencodeSettings{
		FontSize: pencodeDefaultFontSize, UIFontSize: pencodeDefaultUIFontSize,
		EditorWidth: pencodeDefaultEditorWidth,
		CodeFont:    defaultPencodeFont(runtime.GOOS), UIFont: defaultPencodeFont(runtime.GOOS), Zoom: pencodeDefaultZoom,
		Theme: "monokai", Keymap: "default",
	}
	settings.Bindings = make(map[string]pencodeKeybinding, len(pencodeShortcutDefinitions))
	for action, definition := range pencodeShortcutDefinitions {
		settings.Bindings[definition.id] = defaultPencodeBinding(pencodeShortcutAction(action), runtime.GOOS)
	}
	for _, entry := range pencodeExtraShortcuts {
		settings.Bindings[entry.id] = entry.defaultBinding()
	}
	return settings
}

func defaultPencodeBinding(action pencodeShortcutAction, goos string) pencodeKeybinding {
	if action == pencodeShortcutRedo && goos == "darwin" {
		return pencodeKeybinding{Key: input.KeyZ, Primary: true, Shift: true}
	}
	if action == pencodeShortcutReplace && goos == "darwin" {
		return pencodeKeybinding{Key: input.KeyF, Primary: true, Alt: true}
	}
	return pencodeShortcutDefinitions[action].defaultKey
}

func pencodeSettingsPath() (string, error) {
	directory, err := configuration.Directory()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "settings.json"), nil
}

func loadPencodeSettings() (pencodeSettings, string) {
	settings := defaultPencodeSettings()
	file, err := pencodeSettingsPath()
	if err != nil {
		return settings, ""
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return settings, file
	}
	return parsePencodeSettings(data), file
}

func parsePencodeSettings(data []byte) pencodeSettings {
	settings := defaultPencodeSettings()
	var saved pencodeSettings
	if json.Unmarshal(data, &saved) != nil {
		return settings
	}
	if saved.FontSize >= pencodeMinFontSize && saved.FontSize <= pencodeMaxFontSize {
		settings.FontSize = saved.FontSize
	}
	if saved.UIFontSize >= pencodeMinUIFontSize && saved.UIFontSize <= pencodeMaxUIFontSize {
		settings.UIFontSize = saved.UIFontSize
	}
	if saved.EditorWidth >= pencodeMinEditorWidth && saved.EditorWidth <= pencodeMaxEditorWidth {
		settings.EditorWidth = saved.EditorWidth
	}
	if saved.CodeFont != "" {
		settings.CodeFont = saved.CodeFont
	}
	if saved.UIFont != "" {
		settings.UIFont = saved.UIFont
	}
	if saved.Zoom >= pencodeMinZoom && saved.Zoom <= pencodeMaxZoom {
		settings.Zoom = saved.Zoom
	}
	if saved.Theme != "" {
		settings.Theme = saved.Theme
	}
	settings.Extensions = saved.Extensions
	settings.IconTheme = saved.IconTheme
	if saved.Keymap != "" {
		settings.Keymap = saved.Keymap
	}
	settings.Keymaps = saved.Keymaps
	for name, bindings := range settings.Keymaps {
		if name == "" || name == settings.Keymap {
			delete(settings.Keymaps, name)
			continue
		}
		settings.Keymaps[name] = resolvePencodeBindings(bindings)
	}
	if settings.Keymap != "default" && settings.Keymaps["default"] == nil {
		if settings.Keymaps == nil {
			settings.Keymaps = make(map[string]map[string]pencodeKeybinding)
		}
		settings.Keymaps["default"] = defaultPencodeSettings().Bindings
	}
	settings.Bindings = resolvePencodeBindings(saved.Bindings)
	return settings
}

func (model *workspaceModel) ensureSettings() {
	if model.settings.FontSize != 0 {
		return
	}
	model.settings = defaultPencodeSettings()
	model.refreshSettingsLabels()
}

func (model *workspaceModel) refreshSettingsLabels() {
	model.editorStyle = "font-size:" + strconv.Itoa(model.settings.FontSize) + "px"
	model.editorWidthLabel = strconv.Itoa(model.settings.EditorWidth) + "%"
	model.fontSizeLabel = strconv.Itoa(model.settings.FontSize) + " px"
	model.uiFontSizeLabel = strconv.Itoa(model.settings.UIFontSize) + " px"
	model.zoomLabel = strconv.Itoa(model.settings.Zoom) + "%"
	for action, definition := range pencodeShortcutDefinitions {
		model.shortcutLabels[action] = model.settings.Bindings[definition.id].label(runtime.GOOS)
		model.shortcutAriaLabels[action] = definition.name + "：" + model.shortcutLabels[action]
	}
}

func (model *workspaceModel) saveSettings() {
	if model.settingsRemote {
		model.saveRemoteSettings()
		return
	}
	if model.settingsPath == "" {
		model.settingsFeedback = "无法确定设置文件位置"
		return
	}
	data, err := json.MarshalIndent(model.settings, "", "  ")
	if err == nil {
		err = configuration.Save(context.Background(), filepath.Dir(model.settingsPath), append(data, '\n'))
	}
	if err != nil {
		model.settingsFeedback = fmt.Sprintf("保存设置失败：%v", err)
	} else {
		model.settingsFeedback = ""
	}
}

func (binding pencodeKeybinding) matches(state *input.State, goos string) bool {
	if binding.Key == 0 || state == nil || !state.KeyPressed(binding.Key) {
		return false
	}
	control := state.KeyHeld(input.KeyLeftControl) || state.KeyHeld(input.KeyRightControl)
	super := state.KeyHeld(input.KeyLeftSuper) || state.KeyHeld(input.KeyRightSuper)
	shift := state.KeyHeld(input.KeyLeftShift) || state.KeyHeld(input.KeyRightShift)
	alt := state.KeyHeld(input.KeyLeftAlt) || state.KeyHeld(input.KeyRightAlt)
	wantControl := binding.Control || binding.Primary && goos != "darwin"
	wantSuper := binding.Super || binding.Primary && goos == "darwin"
	return control == wantControl && super == wantSuper && shift == binding.Shift && alt == binding.Alt
}

func (binding pencodeKeybinding) label(goos string) string {
	var label strings.Builder
	if binding.Primary {
		if goos == "darwin" {
			label.WriteString("Cmd+")
		} else {
			label.WriteString("Ctrl+")
		}
	}
	if binding.Control {
		label.WriteString("Ctrl+")
	}
	if binding.Super {
		if goos == "darwin" {
			label.WriteString("Cmd+")
		} else {
			label.WriteString("Super+")
		}
	}
	if binding.Shift {
		label.WriteString("Shift+")
	}
	if binding.Alt {
		if goos == "darwin" {
			label.WriteString("Option+")
		} else {
			label.WriteString("Alt+")
		}
	}
	label.WriteString(pencodeKeyLabel(binding.Key))
	return label.String()
}

func pencodeKeyLabel(key input.KeyCode) string {
	if key == 0 {
		return "未设置"
	}
	for name, code := range pencodeShortcutKeys {
		if code == key {
			return name
		}
	}
	return "?"
}

func validPencodeKeybinding(binding pencodeKeybinding) bool {
	if binding.Key == 0 {
		return binding == (pencodeKeybinding{})
	}
	for _, code := range pencodeShortcutKeys {
		if code == binding.Key {
			return true
		}
	}
	return false
}

var pencodeCaptureKeys = func() []input.KeyCode {
	keys := make([]input.KeyCode, 0, len(pencodeShortcutKeys))
	for _, code := range pencodeShortcutKeys {
		keys = append(keys, code)
	}
	slices.Sort(keys)
	return keys
}()

func pencodeCapturedBinding(state *input.State, goos string) (pencodeKeybinding, bool) {
	for _, key := range pencodeCaptureKeys {
		if !state.KeyPressed(key) {
			continue
		}
		control := state.KeyHeld(input.KeyLeftControl) || state.KeyHeld(input.KeyRightControl)
		super := state.KeyHeld(input.KeyLeftSuper) || state.KeyHeld(input.KeyRightSuper)
		binding := pencodeKeybinding{
			Key: key, Shift: state.KeyHeld(input.KeyLeftShift) || state.KeyHeld(input.KeyRightShift),
			Alt: state.KeyHeld(input.KeyLeftAlt) || state.KeyHeld(input.KeyRightAlt),
		}
		if goos == "darwin" && super {
			binding.Primary = true
			binding.Control = control
		} else if goos != "darwin" && control {
			binding.Primary = true
			binding.Super = super
		} else {
			binding.Control, binding.Super = control, super
		}
		return binding, true
	}
	return pencodeKeybinding{}, false
}

func (model *workspaceModel) syncZoom(world *module.Content) {
	if model == nil || world == nil || !world.IsAlive(model.surface) {
		return
	}
	model.ensureSettings()
	zoom := float32(model.settings.Zoom) / 100
	if runtime.GOOS == "darwin" && model.app != nil && model.app.Context != nil && model.session != nil && model.session.active != nil && model.session.active.model == model {
		height := pencodeNativeTitlebarHeight(float64(model.settings.Zoom) / 100)
		if model.windowTitlebarHeight != height {
			model.app.Context.SetTitleBarHeight(height)
			model.windowTitlebarHeight = height
		}
	}
	width, height := float32(0), float32(0)
	if model.app != nil {
		viewportWidth, viewportHeight := model.app.GetViewport()
		width, height = float32(viewportWidth), float32(viewportHeight)
	}
	if (width <= 0 || height <= 0) && model.viewportWidth > 0 && model.viewportHeight > 0 {
		width, height = model.viewportWidth, model.viewportHeight
	}
	if width <= 0 || height <= 0 {
		if viewport, ok := world.Resource[scene.ViewportSize](); ok {
			width, height = viewport.Width, viewport.Height
		}
	}
	if width <= 0 || height <= 0 {
		if html, ok := world.Get[htmlui.Surface](model.surface); ok {
			width, height = html.Width, html.Height
		}
	}
	if width <= 0 || height <= 0 {
		return
	}
	model.viewportWidth, model.viewportHeight = width, height
	if size, ok := world.Get[scene.Size2D](model.windowBorder); ok && (size.Width != width || size.Height != height) {
		updated := world.Mut[scene.Size2D](model.windowBorder)
		updated.Width, updated.Height = width, height
	}
	pixelRatio := float32(1)
	if model.app != nil && model.app.Context != nil {
		pixelRatio = float32(model.app.Context.DevicePixelRatio())
	}
	radius := pencodeWindowBorderRadius(pixelRatio)
	if border, ok := world.Get[scene.UIBorder2D](model.windowBorder); ok && border.Shape.Radius != radius {
		world.Mut[scene.UIBorder2D](model.windowBorder).Shape.Radius = radius
	}
	htmlWidth, htmlHeight := float32(0), float32(0)
	if model.settings.Zoom != pencodeDefaultZoom {
		htmlWidth, htmlHeight = width/zoom, height/zoom
	} else if model.app == nil {
		htmlWidth, htmlHeight = width, height
	}
	if html, ok := world.Get[htmlui.Surface](model.surface); ok && (html.Width != htmlWidth || html.Height != htmlHeight) {
		updated := world.Mut[htmlui.Surface](model.surface)
		updated.Width, updated.Height = htmlWidth, htmlHeight
	}
	if transform, ok := world.Get[scene.UITransform](model.surface); ok && (transform.Scale.X() != zoom || transform.Scale.Y() != zoom) {
		updated := world.Mut[scene.UITransform](model.surface)
		updated.Scale = mgl32.Vec2{zoom, zoom}
	}
}

func (model *workspaceModel) handleSettingsEvent(context *module.ContentEventContext[appProps, appState]) bool {
	model.ensureSettings()
	if model.handleKeymapEvent(context) {
		return true
	}
	for _, definition := range pencodeShortcutDefinitions {
		if context.Ref == definition.ref {
			model.settingsOpen = true
			model.settingsShortcuts = true
			model.settingsExtensions = false
			model.capturingShortcut = true
			model.captureAction = definition.id
			model.settingsFeedback = ""
			return true
		}
	}
	switch context.Ref {
	case "open-settings":
		if model.market != nil {
			model.market.open = false
		}
		if model.session != nil && model.session.connection != nil {
			model.session.connection.open, model.session.connection.formOpen = false, false
		}
		model.settingsOpen = !model.settingsOpen
		model.capturingShortcut = false
		return true
	case "close-settings":
		model.settingsOpen = false
		model.capturingShortcut = false
		return true
	case "settings-extensions-nav":
		model.settingsExtensionsExpanded = !model.settingsExtensionsExpanded
		model.settingsExtensions, model.settingsShortcuts, model.capturingShortcut = true, false, false
		model.settingsFeedback = ""
		return true
	case "reset-extension-settings":
		model.resetExtensionSettings()
		return true
	case "settings-appearance-nav", "settings-shortcuts-nav":
		model.settingsExtensions = false
		model.settingsShortcuts = context.Ref == "settings-shortcuts-nav"
		model.capturingShortcut = false
		model.settingsFeedback = ""
		return true
	case "font-decrease", "font-increase":
		delta := -1
		if context.Ref == "font-increase" {
			delta = 1
		}
		model.settings.FontSize = min(pencodeMaxFontSize, max(pencodeMinFontSize, model.settings.FontSize+delta))
		model.refreshSettingsLabels()
		model.saveSettings()
		return true
	case "editor-width-decrease", "editor-width-increase":
		delta := -pencodeEditorWidthStep
		if context.Ref == "editor-width-increase" {
			delta = pencodeEditorWidthStep
		}
		model.settings.EditorWidth = min(pencodeMaxEditorWidth, max(pencodeMinEditorWidth, model.settings.EditorWidth+delta))
		if textarea := context.World.Mut[engineui.Textarea](model.editor.control); textarea != nil {
			textarea.WrapWidthPercent = float32(model.settings.EditorWidth)
		}
		model.refreshSettingsLabels()
		model.saveSettings()
		return true
	case "ui-font-decrease", "ui-font-increase":
		delta := -1
		if context.Ref == "ui-font-increase" {
			delta = 1
		}
		model.settings.UIFontSize = min(pencodeMaxUIFontSize, max(pencodeMinUIFontSize, model.settings.UIFontSize+delta))
		model.refreshSettingsLabels()
		if html := context.World.Mut[htmlui.Surface](model.surface); html != nil {
			html.FontSize = float64(model.settings.UIFontSize)
		}
		model.saveSettings()
		return true
	case "code-font-select", "ui-font-select":
		if model.selectFont(context.Event.Value, context.Ref == "ui-font-select") {
			model.saveSettings()
		}
		return true
	case "theme-select":
		if model.selectTheme(context.Event.Value) {
			model.saveSettings()
		}
		return true
	case "icon-theme-select":
		if model.selectIconTheme(context.Event.Value) {
			model.saveSettings()
		}
		return true
	case "zoom-decrease", "zoom-increase":
		delta := -10
		if context.Ref == "zoom-increase" {
			delta = 10
		}
		model.settings.Zoom = min(pencodeMaxZoom, max(pencodeMinZoom, model.settings.Zoom+delta))
		model.refreshSettingsLabels()
		model.syncZoom(context.World)
		model.saveSettings()
		return true
	case "reset-shortcuts":
		model.settings.Bindings = defaultPencodeSettings().Bindings
		model.capturingShortcut = false
		model.refreshSettingsLabels()
		model.saveSettings()
		return true
	}
	return false
}

func (model *workspaceModel) handleShortcutKeys(world *module.Content, state *input.State, goos string) bool {
	if state == nil || state.Composing() {
		return false
	}
	if model.session != nil && model.session.connection != nil && model.session.connection.passwordRequest != nil {
		switch {
		case model.uiKey(state, input.KeyEscape):
			return model.connectionEvent("connection-password-cancel", "")
		case model.uiKey(state, input.KeyEnter):
			if !model.session.connection.passwordRequest.prompt.Confirm {
				return model.connectionEvent("connection-password-submit", "")
			}
		}
		return false
	}
	if model.session != nil && model.session.connection != nil && model.session.connection.open && model.session.connection.picker.open {
		switch {
		case model.uiKey(state, input.KeyEscape):
			return model.connectionEvent("connection-project-cancel", "")
		case model.uiKey(state, input.KeyEnter):
			if world != nil {
				if entity, ok := world.HTMLControl(model.surface, "connection-project-path"); ok {
					if field, ok := world.Get[engineui.TextField](entity); ok && field.Focused {
						return model.connectionEvent("connection-project-go", "")
					}
				}
			}
		}
		return false
	}
	if model.tabContext.open {
		model.languageUI.keysHandled = true
		return model.handleTabContextKeys(world, state)
	}
	if model.fileContext.open || model.fileContext.dialog != "" {
		model.languageUI.keysHandled = model.fileContext.open
		return model.handleFileContextKeys(world, state, goos) || model.handleActionShortcuts(world, state, goos)
	}
	if model.folderSearch.open {
		return model.folderSearchKeys(world, state)
	}
	if model.closing.action != pencodeCloseNone {
		if model.uiKey(state, input.KeyEscape) {
			model.clearCloseRequest()
			return true
		}
		return false
	}
	model.ensureSettings()
	if !model.capturingShortcut && model.debugShortcut(world, state) {
		return true
	}
	if handled, changed := model.portsShortcut(world, state); handled {
		return changed
	}
	if handled, changed := model.terminalShortcut(world, state); handled {
		return changed
	}
	if handled, changed := model.sourceShortcut(world, state, goos); handled {
		return changed
	}
	if model.market != nil && model.market.open {
		if model.uiKey(state, input.KeyEscape) {
			model.closeMarketplace()
			return true
		}
		return false
	}
	if model.capturingShortcut {
		if state.KeyPressed(input.KeyEscape) {
			model.capturingShortcut = false
			return true
		}
		if state.KeyPressed(input.KeyBackspace) {
			model.settings.Bindings[model.captureAction] = pencodeKeybinding{}
			model.capturingShortcut = false
			model.refreshSettingsLabels()
			model.saveSettings()
			return true
		}
		binding, ok := pencodeCapturedBinding(state, goos)
		if !ok {
			return false
		}
		if !validPencodeKeybinding(binding) {
			return true
		}
		if conflict := model.shortcutConflict(model.captureAction, binding, goos); conflict != "" {
			model.settingsFeedback = "快捷键已用于" + conflict
			return true
		}
		model.settings.Bindings[model.captureAction] = binding
		model.capturingShortcut = false
		model.refreshSettingsLabels()
		model.saveSettings()
		return true
	}
	if model.settingsOpen && model.uiKey(state, input.KeyEscape) {
		model.settingsOpen = false
		return true
	}
	if model.quickOpen {
		if model.uiKey(state, input.KeyEscape) {
			model.closeQuickOpen(world)
			return true
		}
		if model.uiKey(state, input.KeyEnter) {
			model.openQuickMatch(world, model.quickIndex)
			return true
		}
		if model.uiKey(state, input.KeyDown) {
			model.advanceQuickMatch(1)
			return true
		}
		if model.uiKey(state, input.KeyUp) {
			model.advanceQuickMatch(-1)
			return true
		}
	}
	if model.findOpen {
		if option := model.searchOptionKey(state); option >= 0 {
			toggleSearchOption(&model.findOptions, option)
			model.refreshFindMatches()
			model.findFromCaret(world)
			return true
		}
		if model.uiKey(state, input.KeyEscape) {
			model.closeFind(world)
			return true
		}
		if (model.uiKey(state, input.KeyEnter) || model.commandMatches("search-previous", state, goos) || model.commandMatches("search-replace-all", state, goos)) && (pencodeControlFocused(world, model.surface, "find-query") || pencodeControlFocused(world, model.surface, "replace-query")) {
			if pencodeControlFocused(world, model.surface, "replace-query") {
				model.replaceFindMatch(world, model.commandMatches("search-replace-all", state, goos))
				return true
			}
			step := 1
			if model.commandMatches("search-previous", state, goos) {
				step = -1
			}
			model.advanceFind(world, step)
			return true
		}
	}
	if model.textEditorActive() {
		if model.commandMatches("find-previous", state, goos) {
			model.advanceFind(world, -1)
			return true
		}
		if model.commandMatches("find-next", state, goos) {
			model.advanceFind(world, 1)
			return true
		}
	}
	if model.commandMatches("folder-find", state, goos) || model.commandMatches("folder-replace", state, goos) {
		model.openFolderSearch(model.activeProjectState(), "", model.commandMatches("folder-replace", state, goos))
		return true
	}
	if model.editorClipboardShortcut(world, state, goos) {
		return true
	}
	if model.handleLanguageKeys(world, state) {
		return true
	}
	if model.bottom.active != pencodePanelClosed && model.commandMatches("panel-close", state, goos) {
		model.handleBottomPanelEvent(world, "panel-close", "")
		return true
	}
	if model.commandMatches("editor-indent", state, goos) || model.commandMatches("editor-outdent", state, goos) {
		if model.indentEditorSelection(world, model.commandMatches("editor-outdent", state, goos)) {
			model.languageUI.keysHandled = true
			return true
		}
	}
	for action, definition := range pencodeShortcutDefinitions {
		if model.settings.Bindings[definition.id].matches(state, goos) {
			model.runShortcut(world, pencodeShortcutAction(action))
			return true
		}
	}
	return model.handleActionShortcuts(world, state, goos)
}

func samePencodeBinding(a, b pencodeKeybinding, goos string) bool {
	if a.Key != b.Key || a.Shift != b.Shift || a.Alt != b.Alt {
		return false
	}
	aControl := a.Control || a.Primary && goos != "darwin"
	bControl := b.Control || b.Primary && goos != "darwin"
	aSuper := a.Super || a.Primary && goos == "darwin"
	bSuper := b.Super || b.Primary && goos == "darwin"
	return aControl == bControl && aSuper == bSuper
}

func (model *workspaceModel) runShortcut(world *module.Content, action pencodeShortcutAction) {
	if model.closing.action != pencodeCloseNone {
		return
	}
	model.lastFileClick = 0
	switch action {
	case pencodeShortcutSave:
		model.saveActiveFile()
	case pencodeShortcutUndo:
		model.undoActiveContent(world)
	case pencodeShortcutRedo:
		model.redoActiveContent(world)
	case pencodeShortcutFind:
		model.openFind(false)
	case pencodeShortcutReplace:
		model.openFind(true)
	case pencodeShortcutQuickOpen:
		model.openQuickOpen()
	case pencodeShortcutClose:
		file := model.activeFile()
		if file == nil {
			return
		}
		model.closeTab(file.Path)
		setPencodeEditorValue(world, model.surface, model.activeContent())
	case pencodeShortcutNext:
		model.switchEditorTab(world, 1)
	case pencodeShortcutPrevious:
		model.switchEditorTab(world, -1)
	case pencodeShortcutSidebar:
		model.sidebarHidden = !model.sidebarHidden
	case pencodeShortcutZoomIn:
		model.setZoom(world, model.settings.Zoom+10)
	case pencodeShortcutZoomOut:
		model.setZoom(world, model.settings.Zoom-10)
	case pencodeShortcutZoomReset:
		model.setZoom(world, pencodeDefaultZoom)
	case pencodeShortcutSettings:
		model.settingsOpen = !model.settingsOpen
	}
}

func (model *workspaceModel) setZoom(world *module.Content, zoom int) {
	zoom = min(pencodeMaxZoom, max(pencodeMinZoom, zoom))
	if model.settings.Zoom == zoom {
		return
	}
	model.settings.Zoom = zoom
	model.refreshSettingsLabels()
	model.syncZoom(world)
	model.saveSettings()
}

func (model *workspaceModel) switchEditorTab(world *module.Content, step int) {
	tabs, active := model.tabs, model.activePath
	if project := model.activeProjectState(); project != nil {
		tabs, active = project.Tabs, project.ActivePath
	}
	if len(tabs) < 2 {
		return
	}
	for index, path := range tabs {
		if path != active {
			continue
		}
		next := (index + step + len(tabs)) % len(tabs)
		model.selectTab(tabs[next])
		setPencodeEditorValue(world, model.surface, model.activeContent())
		return
	}
}
