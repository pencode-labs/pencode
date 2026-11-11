package workbench

import (
	"context"
	"os"
	"time"

	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/internal/updater"
)

type pencodeUpdateResult struct {
	err     error
	release updater.Release
	stage   string
}

type pencodeUpdates struct {
	cancel     context.CancelFunc
	results    chan pencodeUpdateResult
	release    updater.Release
	root       string
	executable string
	version    string
	message    string
	stage      string
	busy       bool
	requested  bool
	approved   bool
}

func (s *Session) initUpdates() {
	if s.updates != nil {
		return
	}
	u := &pencodeUpdates{version: "dev", message: "从 GitHub tag 检查版本更新"}
	u.executable, _ = os.Executable()
	var err error
	u.root, u.version, err = updater.Installation(u.executable)
	if err != nil {
		u.message = err.Error()
	}
	s.updates = u
}

func (m *workspaceModel) handleUpdateEvent(ref string) bool {
	if ref != "update-action" {
		return false
	}
	if m.session == nil {
		return true
	}
	m.session.initUpdates()
	u := m.session.updates
	if u.busy {
		return true
	}
	if u.stage != "" {
		if m.app != nil {
			u.requested = true
			if m.requestWindowClose() {
				m.app.RequestClose()
			}
		}
		return true
	}
	if u.root == "" {
		return true
	}
	check := u.release.URL == ""
	u.busy = true
	u.message = m.tr("正在检查 GitHub tag…")
	if !check {
		u.message = m.tr("正在下载并校验更新包…")
	}
	u.results = make(chan pencodeUpdateResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	u.cancel = cancel
	results, wake := u.results, m.backgroundWake()
	root, executable, version, release := u.root, u.executable, u.version, u.release
	go func() {
		defer cancel()
		c := updater.NewClient()
		r := pencodeUpdateResult{}
		if check {
			r.release, r.err = c.Check(ctx, version)
		} else {
			r.release = release
			r.stage, r.err = c.Stage(ctx, root, executable, release)
		}
		if ctx.Err() != nil {
			if r.stage != "" {
				_ = os.RemoveAll(r.stage)
			}
			r.stage = ""
			if r.err == nil {
				r.err = ctx.Err()
			}
		}
		results <- r
		if wake != nil {
			wake()
		}
	}()
	return true
}

func (m *workspaceModel) pollUpdates() bool {
	if m.session == nil || m.session.updates == nil {
		return false
	}
	u := m.session.updates
	select {
	case r := <-u.results:
		u.busy, u.results = false, nil
		if r.err != nil {
			u.message = m.tr("更新失败：") + r.err.Error()
			return true
		}
		u.release, u.stage = r.release, r.stage
		switch {
		case r.stage != "":
			u.message = m.tr("安装包已校验，安装并重启将打开系统安装程序")
		case r.release.Tag != "":
			u.message = m.tr("发现新版本：") + r.release.Tag
		default:
			u.message = m.tr("当前已是最新可用版本")
		}
		return true
	default:
		return false
	}
}

func (m *workspaceModel) renderUpdates(v *htmlui.View) {
	v.Event("settings-about-nav", htmlui.EventClick)
	v.Attr("settings-about-pane", "hidden", "")
	v.Attr("settings-about-nav", "class", "pencode-settings-nav-item")
	v.RemoveAttr("settings-about-nav", "aria-current")
	if m.settingsAbout && !m.settingsShortcuts && !m.settingsExtensions {
		v.Text("settings-page-title", m.tr("关于"))
		v.RemoveAttr("settings-about-pane", "hidden")
		v.Attr("settings-about-nav", "class", "pencode-settings-nav-item active")
		v.Attr("settings-about-nav", "aria-current", "page")
		v.Attr("settings-appearance-nav", "class", "pencode-settings-nav-item")
		v.RemoveAttr("settings-appearance-nav", "aria-current")
		v.Attr("settings-appearance-pane", "hidden", "")
	}
	if m.session == nil {
		return
	}
	m.session.initUpdates()
	u := m.session.updates
	v.Event("update-action", htmlui.EventClick)
	v.Text("update-version", u.version)
	v.Text("update-status", u.message)
	v.Attr("update-status", "title", u.message)
	label := "检查更新"
	switch {
	case u.stage != "":
		label = "安装并重启"
	case u.release.URL != "":
		if u.busy {
			label = "正在下载…"
		} else {
			label = "下载更新"
		}
	case u.busy:
		label = "正在检查…"
	}
	v.Text("update-action", m.tr(label))
	if u.busy || u.root == "" {
		v.Attr("update-action", "disabled", "")
	} else {
		v.RemoveAttr("update-action", "disabled")
	}
}

// FinishUpdate is called after the UI and backend have released their resources.
func (s *Session) FinishUpdate() error {
	u := s.updates
	if u == nil {
		return nil
	}
	if u.cancel != nil {
		u.cancel()
	}
	if u.busy {
		r := <-u.results
		u.busy = false
		if r.stage != "" {
			_ = os.RemoveAll(r.stage)
		}
	}
	if u.approved {
		return updater.Launch(u.stage)
	}
	if u.stage != "" {
		_ = os.RemoveAll(u.stage)
	}
	return nil
}
