package workbench

import (
	"os"
	"strings"
	"testing"

	"github.com/diiyw/onix/html/style"
	"github.com/diiyw/onix/syntax"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestNotificationSeverityUpdatesRetainContent(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	process := &extensions.ActionSession{}
	ui := peb.UI{ID: "severity", Title: "通知", Kind: peb.Notification, View: &peb.EditorView{HTML: `<button data-ref="retry">重试</button>`}}
	if err := model.applyExtensionUI("test", process, ui); err != nil {
		t.Fatal(err)
	}
	notice := model.actions.ui.surfaces[0]
	ui.View = nil
	for _, theme := range []string{"dark-plus", "light-plus"} {
		model.selectTheme(theme)
		markPencodeRootDirtyByModel(model.fontResources, model)
		for range 3 {
			languageInteractionStep(t, app)
		}
		definition := notice.definition
		for _, test := range []struct {
			severity                                   peb.NotificationSeverity
			class, icon, foreground, label, role, live string
		}{
			{peb.NotificationInfo, "info", "info", "notificationsInfoIcon.foreground", "信息", "status", "polite"},
			{peb.NotificationWarning, "warning", "warning", "notificationsWarningIcon.foreground", "警告", "status", "polite"},
			{peb.NotificationError, "error", "error", "notificationsErrorIcon.foreground", "错误", "alert", "assertive"},
			{peb.NotificationInfo, "info", "info", "notificationsInfoIcon.foreground", "信息", "status", "polite"},
		} {
			ui.Severity = test.severity
			if err := model.applyExtensionUI("test", process, ui); err != nil {
				t.Fatal(err)
			}
			markPencodeRootDirtyByModel(model.fontResources, model)
			for range 3 {
				languageInteractionStep(t, app)
			}
			if notice.definition != definition {
				t.Fatal("severity update rebuilt notification content")
			}
			node, ok := model.fontResources.HTMLNode(model.surface, notice.rootID)
			if !ok {
				t.Fatal("notification missing")
			}
			markup, err := node.Serialize()
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"pencode-notification-" + test.class, `role="` + test.role + `"`, `aria-live="` + test.live + `"`} {
				if !strings.Contains(strings.SplitN(markup, ">", 2)[0], want) {
					t.Fatalf("%s %s missing %q: %s", theme, test.class, want, markup)
				}
			}
			for _, want := range []string{`src="` + model.themedIcon("icons/"+test.icon+".svg", test.foreground) + `"`, `alt="` + test.label + `"`} {
				if !strings.Contains(markup, want) {
					t.Fatalf("%s %s icon missing %q: %s", theme, test.class, want, markup)
				}
			}
		}
	}
}

func TestNotificationThemeColors(t *testing.T) {
	source, err := os.ReadFile("../resources/workbench/assets/ui/pencode.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range syntax.BuiltinThemeIDs {
		t.Run(id, func(t *testing.T) {
			theme, err := syntax.BuiltinTheme(id)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := preparePencodeTheme(id, theme, string(source))
			if err != nil {
				t.Fatal(err)
			}
			computed := pencodeComputedTheme(t, prepared, `<main class="pencode-root"><section data-ref="info" class="pencode-extension-notification pencode-notification-info"></section><section data-ref="warning" class="pencode-extension-notification pencode-notification-warning"></section><section data-ref="error" class="pencode-extension-notification pencode-notification-error"></section></main>`)
			colors := make(map[style.Color]bool)
			for _, name := range []string{"info", "warning", "error"} {
				color := computed[name].BorderColor.Left.Color
				if color.A == 0 || colors[color] {
					t.Fatalf("%s has no distinct severity color: %+v", name, color)
				}
				colors[color] = true
				key := "notifications" + strings.ToUpper(name[:1]) + name[1:] + "Icon.foreground"
				icon := prepared.icons[pencodeIconKey{"icons/" + name + ".svg", key}]
				if !strings.HasSuffix(icon, "foreground="+syntax.Color(color).Hex()[1:]) {
					t.Fatalf("%s icon color differs from border: %s, %+v", name, icon, color)
				}
			}
		})
	}
}
