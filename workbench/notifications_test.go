package workbench

import (
	"testing"

	"github.com/pencode-labs/pencode/peb"
)

func TestNotificationsLayoutUpdateDismissAndCleanup(t *testing.T) {
	app, model, _, input := languageInteractionFixture(t)
	n := peb.Notification{ID: "tools", Title: "Go", Message: "工具安装失败", Error: true, Action: "tools", ActionText: "重试"}
	model.showNotification("go", nil, n)
	markPencodeRootDirtyByModel(model.fontResources, model)
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	root := editorContextBounds(t, model, "pencode-root")
	card := editorContextBounds(t, model, "notification/go/tools")
	status := editorContextBounds(t, model, "statusbar")
	if model.fontResources.FocusedControl() != model.editor.control {
		t.Fatal("notification took editor focus")
	}
	if card.Width < 200 || card.Height < 50 || card.X < root.Width/2 || card.X+card.Width > root.Width || card.Y+card.Height > status.Y || status.Y-card.Y-card.Height > 32 {
		t.Fatalf("notification not above bottom-right status bar: card=%+v root=%+v status=%+v", card, root, status)
	}
	if model.actions.ui.overlay != nil {
		t.Fatal("notification opened an extension overlay")
	}
	n.Message, n.Error, n.Action, n.ActionText = "已就绪", false, "", ""
	model.showNotification("go", nil, n)
	if len(model.actions.notifications) != 1 || model.actions.notifications[0].message != n {
		t.Fatal("update did not replace the message")
	}
	markPencodeRootDirtyByModel(model.fontResources, model)
	languageInteractionStep(t, app)
	editorContextClick(t, app, model, input, "notification/go/tools/close")
	if len(model.actions.notifications) != 0 {
		t.Fatal("close did not remove the message")
	}
	model.showNotification("go", nil, n)
	model.showNotification("other", nil, n)
	model.removeActions("go")
	if len(model.actions.notifications) != 1 || model.actions.notifications[0].owner != "other" {
		t.Fatal("extension removal did not clear only its notifications")
	}
	model.closeActions()
	if len(model.actions.notifications) != 0 {
		t.Fatal("disconnect retained notifications")
	}
}
