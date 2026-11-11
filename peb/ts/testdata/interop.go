// Generates Go-owned wire fixtures for the TypeScript SDK's interoperability test.
package main

import (
	"encoding/json"
	"os"

	"github.com/pencode-labs/pencode/peb"
)

type sample struct {
	Name string
	Data []byte
}

func main() {
	var samples []sample
	add := func(name string, data []byte, err error) {
		if err != nil {
			panic(err)
		}
		samples = append(samples, sample{name, data})
	}
	event := peb.EditorEvent{Kind: "input", Ref: "内容", Value: "你好 👩‍💻", InputType: "insertText", Data: "text", Checked: true, Selected: 2, Anchor: 3, Focus: 4}
	view := &peb.EditorView{HTML: `<p data-ref="内容">预览</p>`, CSS: "p { color: red; }", Events: []peb.EditorBinding{{Ref: "内容", Kind: "input"}}}
	action := peb.Action{ID: "open", Text: "打开", Tooltip: "预览", Icon: "resources/office.svg", Color: "red", IconColor: "blue", IconStyle: "outline", Shortcut: "Primary+Shift+O", Location: peb.OpenWith, Disabled: true, Hidden: true, Default: true, Selectors: []peb.DocumentSelector{{Pattern: ".docx", LanguageID: "word", Match: peb.FileSuffix}}}
	context := peb.ActionContext{ID: "open", ProjectPath: "/项目", Path: "/项目/测试.docx", Directory: true, Caret: 10, Anchor: 2}
	data, err := peb.EncodeHello(nil, peb.Hello{Host: "pencode", Capabilities: peb.Capabilities})
	add("Hello", data, err)
	data, err = peb.EncodeContributions(nil, peb.Contributions{
		LanguagePacks:   []peb.LanguagePack{{ID: "es", Label: "Español", Path: "resources/locales/es.json"}},
		Themes:          []peb.Theme{{ID: "theme", Label: "主题", Path: "resources/theme.json"}},
		IconThemes:      []peb.IconTheme{{ID: "icons", Label: "图标", Rules: []peb.IconRule{{Pattern: ".docx", Path: "resources/office.svg", Match: peb.FileSuffix}}}},
		Fonts:           []peb.Font{{ID: "font", Label: "字体", Path: "resources/font.ttf", BoldPath: "resources/bold.ttf"}},
		LanguageServers: []peb.LanguageServer{{ID: "ts", Label: "TS", LanguageID: "typescript", Command: "tsserver", Args: []string{"--stdio"}, Selectors: action.Selectors, InitializationOptions: `{"a":true}`, Configuration: `{"b":1}`}},
		Actions:         []peb.Action{action}, ActionService: true,
		DebugAdapters: []peb.DebugAdapter{{ID: "debug", Label: "调试", Command: "debug", Args: []string{"--test"}, Templates: "[]"}},
		Settings:      []peb.Setting{{ID: "mode", Label: "模式", Description: "选择模式", Default: json.RawMessage(`"auto"`), Options: []peb.SettingOption{{Value: "auto", Label: "自动"}}, Type: peb.SettingString}},
	})
	add("Contributions", data, err)
	data, err = peb.EncodeEditorMessage(nil, peb.EditorMessage{Editor: "office", Path: "/项目/测试.docx", Content: "\ufeffsource", Event: event, Binary: true})
	add("EditorMessage", data, err)
	content := ""
	data, err = peb.EncodeEditorReply(nil, peb.EditorReply{View: view, Content: &content})
	add("EditorReply", data, err)
	data, err = peb.EncodeEditorReply(nil, peb.EditorReply{})
	add("EmptyReply", data, err)
	data, err = peb.EncodeActionContext(nil, context)
	add("ActionContext", data, err)
	data, err = peb.EncodeActionChange(nil, peb.ActionChange{Action: action, Remove: true})
	add("ActionChange", data, err)
	data, err = peb.EncodeUI(nil, peb.UI{ID: "test", Title: "测试", Anchor: "open", Kind: peb.Popup, Placement: peb.UIPlacement{Mode: peb.PositionAnchorAbove, X: -12.5, Y: 24, Width: 800, Height: 600}, CloseOnOutsideClick: true, Context: context, View: view, ViewVersion: 1})
	add("UI", data, err)
	data, err = peb.EncodeUI(nil, peb.UI{ID: "tab", Title: "标签", Kind: peb.EditorTab, Busy: true, Unread: true, InterceptClose: true, ViewVersion: 2, Patch: &peb.ViewPatch{Base: 1, Regions: []peb.ViewRegion{{Ref: "body", HTML: "<p>更新</p>"}}}})
	add("UIPatch", data, err)
	data, err = peb.EncodeUIEvent(nil, peb.UIEvent{ID: "test", Reason: "button", Context: context, Event: event})
	add("UIEvent", data, err)
	data, err = peb.EncodeSettingValues(nil, peb.SettingValues{"limit": json.RawMessage("9223372036854775807")})
	add("Settings", data, err)
	data, err = peb.EncodeFileChanges(nil, peb.FileChanges{Root: "/项目", Files: []peb.FileEvent{{Path: "测试.docx", Op: peb.FileWritten | peb.FileRenamed, Directory: true}}, Reset: true})
	add("FileChanges", data, err)
	data, err = peb.EncodeLifecycleEvent(nil, peb.LifecycleEvent{ProjectPath: "/项目", Kind: peb.ProjectClosing})
	add("LifecycleEvent", data, err)
	data, err = peb.EncodeLifecycleReply(nil, peb.LifecycleReply{Message: "请保存"})
	add("LifecycleReply", data, err)
	data, err = peb.EncodeFailure(nil, "错误")
	add("Failure", data, err)
	if err := json.NewEncoder(os.Stdout).Encode(samples); err != nil {
		panic(err)
	}
}
