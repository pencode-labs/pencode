package main

import (
	"context"
	"fmt"
	"html"
	"os"

	"github.com/diiyw/onix/projects/pencode/peb"
)

const menuHTML = `<button data-ref="dialog" type="button" role="menuitem">打开表单弹窗</button><button data-ref="popup" type="button" role="menuitem">打开自由定位浮窗</button><button data-ref="panel" type="button" role="menuitem">打开工作台 Panel</button>`
const formHTML = `<form data-ref="form"><label for="pencode-extension-demo-name">名称</label><input id="pencode-extension-demo-name" data-ref="name" name="name" placeholder="输入名称"><button type="submit">在 Panel 中显示</button></form>`
const formCSS = `form { display: flex; flex-direction: column; gap: 12px; } input { padding: 8px; } button { padding: 6px 12px; }`

func main() {
	var publisher *peb.ActionPublisher
	name := "Pencode"
	anchored := false
	closeOnOutsideClick := false
	popupPlacement := func() peb.UIPlacement {
		if anchored {
			return peb.UIPlacement{Mode: peb.PositionAnchor, X: -480, Y: 8, Width: 520, Height: 360}
		}
		return peb.UIPlacement{Mode: peb.PositionAbsolute, X: 80, Y: 100, Width: 520, Height: 360}
	}
	panel := func(c peb.ActionContext) error {
		label := name
		if c.Path != "" {
			label += " · " + c.Path
		}
		return publisher.Show(peb.UI{ID: "result", Title: "UI 示例", Kind: peb.Panel, Context: c, View: &peb.EditorView{
			HTML:   `<p>` + html.EscapeString(label) + `</p><button data-ref="refresh" type="button">刷新</button><button data-ref="close" type="button">关闭 Panel</button>`,
			CSS:    `button { margin-right: 8px; padding: 6px 12px; }`,
			Events: []peb.EditorBinding{{Ref: "refresh", Kind: "click"}, {Ref: "close", Kind: "click"}},
		}})
	}
	err := peb.ServeExtension(os.Stdin, os.Stdout, peb.Contributions{Actions: []peb.Action{{ID: "tools", Text: "UI 示例", Tooltip: "扩展菜单、弹窗与 Panel", Location: peb.TitleBarRight}}}, peb.Handlers{
		ActivateActions: func(_ context.Context, p *peb.ActionPublisher) { publisher = p },
		Action: func(c peb.ActionContext) error {
			return publisher.Show(peb.UI{ID: "menu", Title: "UI 示例操作", Anchor: c.ID, Kind: peb.ContextMenu, View: &peb.EditorView{HTML: menuHTML, Events: []peb.EditorBinding{{Ref: "dialog", Kind: "click"}, {Ref: "popup", Kind: "click"}, {Ref: "panel", Kind: "click"}}}})
		},
		UIEvent: func(event peb.UIEvent) error {
			switch event.Event.Kind {
			case "open", "close", "visibilitychange":
				fmt.Fprintf(os.Stderr, "%s: %s %s %s\n", event.ID, event.Event.Kind, event.Reason, event.Event.Value)
			case "input":
				if event.ID == "form" && event.Event.Ref == "name" {
					name = event.Event.Value
				}
				if event.ID == "floating" {
					fmt.Fprintf(os.Stderr, "搜索: %s\n", event.Event.Value)
				}
			case "change":
				if event.ID == "floating" && event.Event.Ref == "outside" {
					closeOnOutsideClick = event.Event.Checked
					return publisher.Show(peb.UI{ID: "floating", Title: "自由定位浮窗", Kind: peb.Popup, Placement: popupPlacement(), CloseOnOutsideClick: closeOnOutsideClick})
				}
			case "submit":
				if event.ID == "form" {
					if err := publisher.CloseUI("form"); err != nil {
						return err
					}
					return panel(event.Context)
				}
			case "click":
				switch event.ID {
				case "menu":
					if event.Event.Ref == "popup" {
						anchored = false
						closeOnOutsideClick = false
						return publisher.Show(peb.UI{ID: "floating", Title: "自由定位浮窗", Kind: peb.Popup, Placement: popupPlacement(), View: &peb.EditorView{
							HTML:   `<p>可像扩展市场一样显示丰富内容，同时继续操作工作区。</p><label for="pencode-extension-demo-query">搜索</label><input id="pencode-extension-demo-query" data-ref="query" placeholder="移动浮窗后保留输入"><label><input data-ref="outside" type="checkbox">点击外部关闭</label><button data-ref="move" type="button">切换坐标 / action 锚点</button>`,
							CSS:    `input[data-ref="query"] { display: block; width: 100%; padding: 8px; margin: 8px 0 16px; } button { padding: 6px 12px; }`,
							Events: []peb.EditorBinding{{Ref: "query", Kind: "input"}, {Ref: "outside", Kind: "change"}, {Ref: "move", Kind: "click"}},
						}})
					}
					if event.Event.Ref == "dialog" {
						return publisher.Show(peb.UI{ID: "form", Title: "输入名称", Kind: peb.Dialog, View: &peb.EditorView{HTML: formHTML, CSS: formCSS, Events: []peb.EditorBinding{{Ref: "name", Kind: "input"}, {Ref: "form", Kind: "submit"}}}})
					}
					if event.Event.Ref == "panel" {
						if err := publisher.CloseUI("menu"); err != nil {
							return err
						}
						return panel(event.Context)
					}
				case "floating":
					if event.Event.Ref == "move" {
						anchored = !anchored
						return publisher.Show(peb.UI{ID: "floating", Title: "自由定位浮窗", Kind: peb.Popup, Placement: popupPlacement(), CloseOnOutsideClick: closeOnOutsideClick})
					}
				case "result":
					if event.Event.Ref == "refresh" {
						return panel(event.Context)
					}
					if event.Event.Ref == "close" {
						return publisher.CloseUI("result")
					}
				}
			}
			return nil
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
