package workbench

import (
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

const maxNotifications = 8

type pencodeNotification struct {
	owner, key, closeID, actionID string
	session                       *extensions.ActionSession
	message                       peb.Notification
}

type pencodeNotificationProps struct {
	Model *workspaceModel
	Item  pencodeNotification
}

func (model *workspaceModel) showNotification(owner string, session *extensions.ActionSession, n peb.Notification) {
	state := &model.actions
	for i := range state.notifications {
		item := &state.notifications[i]
		if item.owner == owner && item.message.ID == n.ID {
			if n.Close {
				copy(state.notifications[i:], state.notifications[i+1:])
				state.notifications[len(state.notifications)-1] = pencodeNotification{}
				state.notifications = state.notifications[:len(state.notifications)-1]
			} else {
				item.message, item.session = n, session
			}
			return
		}
	}
	if n.Close {
		return
	}
	if len(state.notifications) == maxNotifications {
		copy(state.notifications, state.notifications[1:])
		state.notifications = state.notifications[:maxNotifications-1]
	}
	key := "notification/" + owner + "/" + n.ID
	state.notifications = append(state.notifications, pencodeNotification{owner: owner, key: key, closeID: key + "/close", actionID: key + "/action", session: session, message: n})
}

func (model *workspaceModel) clearNotifications(owner string) {
	items := model.actions.notifications
	kept := items[:0]
	for _, item := range items {
		if owner != "" && item.owner != owner {
			kept = append(kept, item)
		}
	}
	clear(items[len(kept):])
	model.actions.notifications = kept
}

func (model *workspaceModel) renderNotifications(view *htmlui.View) {
	state := &model.actions
	if len(state.notifications) == 0 {
		return
	}
	if state.notificationView == nil {
		var err error
		state.notificationView, err = htmlui.Define(htmlui.NewTemplate(`<section data-ref="message" class="pencode-notification" role="status" aria-live="polite"><header><strong data-ref="title"></strong><button data-ref="close" type="button" aria-label="关闭通知">关闭</button></header><p data-ref="body"></p><button data-ref="action" class="pencode-notification-action" type="button" hidden></button></section>`), "", htmlui.Behavior[pencodeNotificationProps, struct{}]{
			Render: func(c htmlui.RenderContext[pencodeNotificationProps, struct{}], target *htmlui.View) {
				item := c.Props.Item
				n := item.message
				target.Attr("message", "id", item.key)
				target.Text("title", n.Title)
				target.Text("body", n.Message)
				target.Attr("close", "id", item.closeID)
				target.Event("close", htmlui.EventClick)
				class, role, live := "pencode-notification", "status", "polite"
				if n.Error {
					class, role, live = "pencode-notification error", "alert", "assertive"
				}
				target.Attr("message", "class", class)
				target.Attr("message", "role", role)
				target.Attr("message", "aria-live", live)
				if n.Action != "" {
					target.RemoveAttr("action", "hidden")
					target.Attr("action", "id", item.actionID)
					target.Text("action", n.ActionText)
					target.Event("action", htmlui.EventClick)
				} else {
					target.Attr("action", "hidden", "")
				}
			},
			HandleEvent: func(c *htmlui.ComponentEventContext[pencodeNotificationProps, struct{}]) {
				model, item := c.Props.Model, c.Props.Item
				if model.closing.action != pencodeCloseNone || model.actions.ui.overlay != nil && model.actions.ui.overlay.ui.Kind == peb.Dialog {
					return
				}
				if c.Ref == "close" {
					model.showNotification(item.owner, nil, peb.Notification{ID: item.message.ID, Close: true})
				} else if c.Ref == "action" && item.session != nil && item.message.Action != "" {
					if !item.session.Submit(peb.ActionContext{ID: item.message.Action, ProjectPath: model.projectPath}) {
						return
					}
					model.showNotification(item.owner, nil, peb.Notification{ID: item.message.ID, Close: true})
				}
				markPencodeRootDirtyByModel(model.fontResources, model)
			},
		})
		if err != nil {
			model.extensionFeedback = err.Error()
			return
		}
	}
	for _, item := range state.notifications {
		htmlui.AppendComponentChild(view, "notifications", componentKey(item.key), state.notificationView, pencodeNotificationProps{Model: model, Item: item}, struct{}{})
	}
}
