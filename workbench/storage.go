package workbench

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/protocol"
)

var storageRefs = [protocol.StorageKinds]string{"storage-large", "storage-history", "storage-profiles", "storage-search", "storage-other-profiles", "storage-extensions", "storage-tools", "storage-other"}

type storageResult struct {
	err    error
	report protocol.StorageReport
}

type storagePage struct {
	backend  *client.Client
	results  chan storageResult
	cancel   context.CancelFunc
	err      string
	labels   [protocol.StorageKinds]string
	total    string
	report   protocol.StorageReport
	selected protocol.StorageMask
	cleaning bool
	confirm  bool
	ready    bool
}

func storageBytes(n uint64) string {
	if n < 1024 {
		return strconv.FormatUint(n, 10) + " B"
	}
	value := float64(n)
	for _, unit := range [...]string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"} {
		value /= 1024
		if value < 1024 {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return strconv.FormatUint(n, 10) + " B"
}

func (m *workspaceModel) stopStorage() {
	if p := m.storage; p != nil && p.cancel != nil {
		p.cancel()
	}
	m.storage = nil
}

func (m *workspaceModel) requestStorage(clean protocol.StorageMask) {
	p := m.storage
	if !m.backendConnected() || p == nil || p.backend != m.backend || p.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, backend, wake := make(chan storageResult, 1), p.backend, m.backgroundWake()
	p.cancel, p.results, p.cleaning, p.confirm, p.err = cancel, ch, clean != 0, false, ""
	go func() {
		report, err := backend.DiskStorage(ctx, clean)
		ch <- storageResult{err, report}
		if wake != nil {
			wake()
		}
	}()
}

func (m *workspaceModel) pollStorage() bool {
	open := m.settingsOpen && m.settingsToolPage == diskStorageSettings
	p := m.storage
	if p != nil && (p.backend != m.backend || !open && !p.cleaning) {
		m.stopStorage()
		p = nil
	}
	if p == nil {
		if !open || !m.backendConnected() {
			return false
		}
		m.storage = &storagePage{backend: m.backend}
		m.requestStorage(0)
		return true
	}
	select {
	case r := <-p.results:
		p.cancel()
		p.cancel, p.results, p.confirm, p.cleaning, p.selected = nil, nil, false, false, 0
		if r.err != nil {
			p.ready, p.err = false, r.err.Error()
			return true
		}
		p.report, p.ready = r.report, true
		var total uint64
		for i, item := range r.report.Sizes {
			total += item.Bytes
			p.labels[i] = storageBytes(item.Bytes)
		}
		p.total = storageBytes(total)
		if r.report.Removed.Files != 0 {
			for _, project := range m.projects {
				h := &project.sourceControl.history
				h.invalidate(nil, true)
			}
		}
		return true
	default:
		return false
	}
}

func (m *workspaceModel) handleStorage(ref string, checked bool) bool {
	if !strings.HasPrefix(ref, "storage-") {
		return false
	}
	p := m.storage
	if p == nil || m.settingsToolPage != diskStorageSettings || !m.settingsOpen || p.backend != m.backend || p.cancel != nil || !m.backendConnected() {
		return true
	}
	switch ref {
	case "storage-refresh":
		m.requestStorage(0)
	case "storage-cancel":
		p.confirm = false
	case "storage-clean":
		if p.ready && p.selected != 0 {
			if p.confirm {
				m.requestStorage(p.selected)
			} else {
				p.confirm = true
			}
		}
	default:
		for k, key := range storageRefs {
			bit := protocol.StorageKind(k).Mask()
			if key == ref && p.ready && p.report.Sizes[k].Files > 0 && bit&protocol.CleanableStorage != 0 {
				p.confirm = false
				if checked {
					p.selected |= bit
				} else {
					p.selected &^= bit
				}
				break
			}
		}
	}
	return true
}

func (m *workspaceModel) renderStorage(v *htmlui.View) {
	if !m.settingsOpen {
		return
	}
	nav := "settings-storage-nav"
	v.Event(nav, htmlui.EventClick)
	open := m.settingsToolPage == diskStorageSettings
	setPanelVisible(v, "settings-storage-pane", open)
	if !open {
		v.Attr(nav, "class", "pencode-settings-nav-item")
		v.RemoveAttr(nav, "aria-current")
		return
	}
	v.Text("settings-page-title", m.tr("磁盘占用"))
	v.Attr(nav, "class", "pencode-settings-nav-item active")
	v.Attr(nav, "aria-current", "page")
	v.Attr("settings-appearance-nav", "class", "pencode-settings-nav-item")
	v.RemoveAttr("settings-appearance-nav", "aria-current")
	setPanelVisible(v, "settings-appearance-pane", false)
	p := m.storage
	connected := m.backendConnected()
	if p == nil || p.backend != m.backend {
		p = &storagePage{}
	}
	enabled := connected && p.cancel == nil
	host := m.tr("本机")
	if m.settingsRemote {
		host = m.tr("远程主机")
	}
	if p.report.Host != "" {
		host += " · " + p.report.Host
	}
	v.Text("storage-host", host)
	total := "—"
	if p.ready {
		total = p.total
	}
	v.Text("storage-total", total)
	var selected protocol.StorageSize
	for k, ref := range storageRefs {
		item, bit := p.report.Sizes[k], protocol.StorageKind(k).Mask()
		size := "—"
		if p.ready {
			size = p.labels[k]
		}
		v.Text(ref+"-size", size)
		if bit&protocol.CleanableStorage == 0 {
			continue
		}
		v.Event(ref, htmlui.EventChange)
		setContextMenuEnabled(v, ref, enabled && p.ready && item.Files != 0)
		if bit&p.selected != 0 {
			v.Attr(ref, "checked", "")
			selected.Files += item.Files
			selected.Bytes += item.Bytes
		} else {
			v.RemoveAttr(ref, "checked")
		}
	}
	for _, ref := range [...]string{"storage-refresh", "storage-clean", "storage-cancel"} {
		v.Event(ref, htmlui.EventClick)
	}
	setContextMenuEnabled(v, "storage-refresh", enabled)
	setContextMenuEnabled(v, "storage-clean", enabled && p.ready && p.selected != 0)
	setPanelVisible(v, "storage-cancel", p.confirm)
	action, status, selection := "清理所选项", "", ""
	switch {
	case !connected:
		status = m.tr("等待工作区连接")
	case p.cleaning:
		status = m.tr("正在清理…")
	case p.cancel != nil:
		status = m.tr("正在统计…")
	case p.err != "":
		status = p.err
	case p.report.Removed.Files != 0:
		status = fmt.Sprintf(m.tr("已清理 %d 个文件（%s）"), p.report.Removed.Files, storageBytes(p.report.Removed.Bytes))
	}
	if p.report.Problem != "" {
		status = strings.TrimSpace(status + " " + fmt.Sprintf(m.tr("部分项目处理失败：%s"), p.report.Problem))
	}
	if selected.Files != 0 {
		selection = fmt.Sprintf(m.tr("已选 %d 个文件（%s）"), selected.Files, storageBytes(selected.Bytes))
	}
	if p.confirm {
		action = "确认删除"
		status = m.tr("所选文件将永久删除。")
	}
	v.Text("storage-selection", selection)
	v.Text("storage-clean", m.tr(action))
	v.Text("storage-status", status)
	setPanelVisible(v, "storage-status", status != "")
}
