package workbench

import (
	"errors"
	"strings"
	"testing"

	"github.com/onix-engine/onix/scene"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/internal/updater"
)

func TestUpdateResultsAndRetry(t *testing.T) {
	u := &pencodeUpdates{busy: true, results: make(chan pencodeUpdateResult, 1)}
	m := &workspaceModel{session: &Session{updates: u}}
	if m.pollUpdates() {
		t.Fatal("empty queue changed UI")
	}
	u.results <- pencodeUpdateResult{err: errors.New("offline")}
	if !m.pollUpdates() || u.busy || u.message != "更新失败：offline" {
		t.Fatalf("%+v", u)
	}
	u.results = make(chan pencodeUpdateResult, 1)
	u.results <- pencodeUpdateResult{release: updater.Release{Tag: "v1.1.0", URL: "download"}}
	if !m.pollUpdates() || u.release.Tag != "v1.1.0" || u.stage != "" {
		t.Fatalf("%+v", u)
	}
	u.busy = true
	u.results = make(chan pencodeUpdateResult, 1)
	u.results <- pencodeUpdateResult{err: errors.New("download failed")}
	if !m.pollUpdates() || u.busy || u.release.URL != "download" || u.stage != "" {
		t.Fatalf("failed download did not remain retryable: %+v", u)
	}
	u.results = make(chan pencodeUpdateResult, 1)
	u.results <- pencodeUpdateResult{release: u.release, stage: "staged"}
	if !m.pollUpdates() || u.stage != "staged" || u.approved {
		t.Fatalf("%+v", u)
	}
}

func TestUpdateRestartCancellation(t *testing.T) {
	u := &pencodeUpdates{stage: "staged", requested: true}
	m := &workspaceModel{session: &Session{updates: u}}
	m.clearCloseRequest()
	if u.requested || u.approved || u.stage != "staged" {
		t.Fatal("cancelled restart lost staging or approved update")
	}
	u.requested = true
	if !m.requestWindowClose() || !u.approved {
		t.Fatal("clean close did not approve restart")
	}
}

func TestUpdatePollingWarmAllocations(t *testing.T) {
	m := &workspaceModel{session: &Session{updates: &pencodeUpdates{}}}
	if n := testing.AllocsPerRun(100, func() { m.pollUpdates() }); n != 0 {
		t.Fatalf("idle polling allocated %g", n)
	}
}

func TestUpdateSettingsControls(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	m.settingsOpen = true
	m.session.updates = &pencodeUpdates{root: t.TempDir(), version: "v1.0.0", message: "更新已就绪，重启后完成安装", stage: "staged"}
	markPencodeRootDirtyByModel(m.fontResources, m)
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	editorContextClick(t, app, m, state, "settings-about-nav")
	if !m.settingsAbout || m.settingsShortcuts || m.settingsExtensions {
		t.Fatal("about category was not selected")
	}
	u := m.session.updates
	for _, test := range []struct {
		label    string
		release  string
		stage    string
		busy     bool
		disabled bool
	}{
		{label: "检查更新"},
		{label: "正在检查…", busy: true, disabled: true},
		{label: "下载更新", release: "download"},
		{label: "正在下载…", release: "download", busy: true, disabled: true},
		{label: "安装并重启", release: "download", stage: "staged"},
		{label: "检查更新"},
	} {
		u.release.URL, u.stage, u.busy = test.release, test.stage, test.busy
		markPencodeRootDirtyByModel(m.fontResources, m)
		languageInteractionStep(t, app)
		languageInteractionStep(t, app)
		editorContextBounds(t, m, "update-action")
		control, ok := m.fontResources.HTMLControl(m.surface, "update-action")
		if !ok {
			t.Fatal("missing update action")
		}
		button, _ := m.fontResources.Get[htmlui.Control](control)
		if button.Disabled != test.disabled {
			t.Fatalf("%s: disabled = %v", test.label, button.Disabled)
		}
		var label strings.Builder
		for _, access := range app.World.Query2[htmlui.ControlText, scene.Text2D]().All {
			if access.A.Ref().Control == control {
				label.WriteString(access.B.Ref().Text().String())
			}
		}
		if label.String() != test.label {
			t.Fatalf("update action label = %q, want %q", label.String(), test.label)
		}
		if test.busy {
			editorContextClick(t, app, m, state, "update-action")
			if u.results != nil || u.requested {
				t.Fatal("busy update action accepted a click")
			}
		}
	}
	for _, ref := range []string{"check-update", "download-update", "restart-update"} {
		if _, ok := m.fontResources.HTMLNode(m.surface, ref); ok {
			t.Fatalf("obsolete update button %s remains", ref)
		}
	}
	editorContextClick(t, app, m, state, "settings-appearance-nav")
	if m.settingsAbout {
		t.Fatal("about category remained selected after switching to appearance")
	}
}
