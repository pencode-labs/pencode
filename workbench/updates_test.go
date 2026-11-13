package workbench

import (
	"errors"
	"strings"
	"testing"

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
	u := &pencodeUpdates{root: t.TempDir(), version: "v1.0.0"}
	m.session.updates = u
	markPencodeRootDirtyByModel(m.fontResources, m)
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	editorContextClick(t, app, m, state, "settings-about-nav")
	if !m.settingsAbout || m.settingsShortcuts || m.settingsExtensions {
		t.Fatal("about category was not selected")
	}
	assertAction := func(label string, disabled bool) {
		t.Helper()
		markPencodeRootDirtyByModel(m.fontResources, m)
		languageInteractionStep(t, app)
		editorContextBounds(t, m, "update-action")
		node, _ := m.fontResources.HTMLNode(m.surface, "update-action")
		if _, got := node.Attribute("disabled"); got != disabled {
			t.Fatalf("%s: disabled = %v, want %v", label, got, disabled)
		}
		markup, err := htmlui.Serialize(app.World, m.surface)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(markup, ">"+label+"</button>") {
			t.Fatalf("update action label %q not rendered", label)
		}
	}
	complete := func(result pencodeUpdateResult) {
		t.Helper()
		u.results = make(chan pencodeUpdateResult, 1)
		u.results <- result
		if !m.pollUpdates() {
			t.Fatal("update result did not change UI")
		}
	}
	assertAction("检查更新", false)
	u.busy = true
	assertAction("正在检查…", true)
	if !m.handleUpdateEvent("update-action") || u.results != nil {
		t.Fatal("busy action started another update")
	}
	complete(pencodeUpdateResult{err: errors.New("offline")})
	assertAction("检查更新", false)
	complete(pencodeUpdateResult{})
	assertAction("检查更新", false)
	complete(pencodeUpdateResult{release: updater.Release{Tag: "v1.1.0", URL: "download"}})
	assertAction("下载更新", false)
	u.busy = true
	assertAction("正在下载…", true)
	complete(pencodeUpdateResult{err: errors.New("offline")})
	assertAction("下载更新", false)
	complete(pencodeUpdateResult{release: u.release, stage: "staged"})
	assertAction("安装并重启", false)
	m.clearCloseRequest()
	assertAction("安装并重启", false)
	u.stage, u.root = "", ""
	u.release = updater.Release{}
	assertAction("检查更新", false)
	editorContextClick(t, app, m, state, "update-action")
	if u.busy || u.results != nil {
		t.Fatal("unsupported installation started an update")
	}
	markup, err := htmlui.Serialize(app.World, m.surface)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(markup, `data-ref="settings-feedback" role="status">无法检查更新：请先安装支持自动更新的发行包</div>`) {
		t.Fatal("unsupported update feedback is missing or hidden")
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
