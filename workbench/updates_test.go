package workbench

import (
	"errors"
	"testing"

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
	for _, ref := range []string{"check-update", "download-update", "restart-update"} {
		editorContextBounds(t, m, ref)
	}
	editorContextClick(t, app, m, state, "settings-appearance-nav")
	if m.settingsAbout {
		t.Fatal("about category remained selected after switching to appearance")
	}
}
