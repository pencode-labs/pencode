package workbench

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/diiyw/onix/module"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/diiyw/onix/window"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/output"
)

type pencodeMarketOperation uint8

const (
	pencodeMarketRefresh pencodeMarketOperation = iota
	pencodeMarketInstall
	pencodeMarketUninstall
	pencodeMarketEnable
	pencodeMarketDisable
	pencodeMarketOpenDirectory
)

type pencodeMarketResult struct {
	op        pencodeMarketOperation
	directory string
	id        string
	items     []extensions.Installed
	bundles   map[string]*extensions.Bundle
	err       error
}

// UI state belongs to the session, so a scene switch does not lose an ongoing
// installation. Workers publish immutable results; only the UI mutates state.
type pencodeMarketplace struct {
	directory                  string // Backend path; only local workspaces can open it.
	open, busy                 bool
	filter, selected, feedback string
	items                      []extensions.Installed
	results                    chan pencodeMarketResult
	cancel                     context.CancelFunc
	done                       chan struct{}
	revision, rowsRevision     uint64
	rowsModel                  *workspaceModel
	rowsDefinition             *htmlui.Definition[pencodeMarketProps, struct{}]
	rows                       []htmlui.ComponentVNode
	details                    [5]string
	selectedItem               *extensions.Installed
}

type pencodeMarketProps struct {
	Model    *workspaceModel
	Item     *extensions.Installed
	Selected bool
}

func (m *pencodeMarketplace) stop() {
	if m == nil || m.cancel == nil {
		return
	}
	m.cancel()
	<-m.done
	m.cancel = nil
	select {
	case result := <-m.results:
		for _, bundle := range result.bundles {
			bundle.Close()
		}
	default:
	}
}

func (model *workspaceModel) backgroundWake() func() {
	if model.app == nil {
		return nil
	}
	if wake := model.app.WakeAt; wake != nil {
		return func() { wake(time.Now()) }
	}
	return model.app.RequestRender
}

func (model *workspaceModel) startMarketOperation(op pencodeMarketOperation, source, id string) {
	m := model.market
	if m == nil || m.busy {
		return
	}
	if op == pencodeMarketOpenDirectory && (model.remoteWorkspace() || m.directory == "") {
		return
	}
	if model.session == nil || model.session.backend == nil {
		m.feedback = "服务未连接"
		return
	}
	m.busy, m.feedback = true, "正在读取扩展…"
	switch op {
	case pencodeMarketInstall:
		m.feedback = "正在安装扩展…"
	case pencodeMarketUninstall:
		m.feedback = "正在卸载扩展…"
	case pencodeMarketEnable:
		m.feedback = "正在启用扩展…"
	case pencodeMarketDisable:
		m.feedback = "正在禁用扩展…"
	case pencodeMarketOpenDirectory:
		m.feedback = "正在打开扩展目录…"
		source = m.directory
	}
	ctx, cancel := context.WithCancel(output.WithStore(context.Background(), model.outputStore()))
	m.cancel, m.done = cancel, make(chan struct{})
	if m.results == nil {
		m.results = make(chan pencodeMarketResult, 1)
	}
	backend, results, done, wake := model.session.backend, m.results, m.done, model.backgroundWake()
	known := make(map[string]bool, len(model.extensionStates))
	for id, state := range model.extensionStates {
		known[id] = state == pencodeExtensionLoaded
	}
	go func() {
		defer close(done)
		r := pencodeMarketResult{op: op, id: id, bundles: make(map[string]*extensions.Bundle)}
		published := false
		defer func() {
			if !published {
				for _, bundle := range r.bundles {
					bundle.Close()
				}
			}
		}()
		switch op {
		case pencodeMarketInstall:
			var item extensions.Installed
			var bundle *extensions.Bundle
			item, bundle, r.err = extensions.Install(ctx, backend, source)
			if r.err == nil {
				r.id = item.ID
				r.bundles[item.ID] = bundle
			}
		case pencodeMarketUninstall:
			r.err = backend.RemoveExtension(ctx, id)
		case pencodeMarketEnable, pencodeMarketDisable:
			var bundle *extensions.Bundle
			_, bundle, r.err = extensions.SetEnabled(ctx, backend, id, op == pencodeMarketEnable)
			if r.err == nil {
				r.bundles[id] = bundle
			}
		case pencodeMarketOpenDirectory:
			openCtx, stop := context.WithTimeout(ctx, 5*time.Second)
			r.err = openExternalCommand(openCtx, source).Run()
			stop()
		}
		if r.err == nil && op != pencodeMarketOpenDirectory {
			catalog, err := backend.ListExtensions(ctx)
			r.err = err
			r.items, r.directory = catalog.Items, catalog.Directory
			if r.err == nil && op == pencodeMarketRefresh {
				for i := range r.items {
					item := &r.items[i]
					if known[item.ID] || item.Disabled || item.Problem != "" {
						continue
					}
					bundle, err := extensions.Load(ctx, backend, item.ID)
					if err != nil {
						item.Problem = err.Error()
					} else {
						r.bundles[item.ID] = bundle
					}
					if ctx.Err() != nil {
						return
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case results <- r:
			published = true
			if wake != nil {
				wake()
			}
		}
	}()
}

func (model *workspaceModel) pollMarketplace() bool {
	m := model.market
	if m == nil || !m.busy {
		return false
	}
	select {
	case result := <-m.results:
		m.stop()
		m.busy = false
		if result.err != nil {
			for _, bundle := range result.bundles {
				bundle.Close()
			}
			m.feedback = fmt.Sprintf("操作失败：%v", result.err)
			return true
		}
		if result.op == pencodeMarketOpenDirectory {
			m.feedback = ""
			return true
		}
		m.items, m.directory = result.items, result.directory
		present := make(map[string]bool, len(m.items))
		found := false
		for _, item := range m.items {
			present[item.ID] = !item.Disabled
			found = found || item.ID == m.selected
			if item.Disabled {
				if model.extensionStates[item.ID] != pencodeExtensionDisabled {
					model.unloadExtension(item.ID)
				}
				model.setExtensionState(item.ID, pencodeExtensionDisabled)
			}
		}
		for id, state := range model.extensionStates {
			if _, exists := present[id]; state != pencodeExtensionRemoved && !exists {
				model.removeExtension(id)
			}
		}
		loadError := ""
		for id, bundle := range result.bundles {
			if bundle == nil || !present[id] || model.extensionStates[id] == pencodeExtensionLoaded {
				bundle.Close()
				continue
			}
			if err := model.importExtension(bundle); err != nil {
				bundle.Close()
				loadError = fmt.Sprintf("扩展 %s 加载失败：%v", id, err)
				continue
			}
			model.setExtensionState(id, pencodeExtensionLoaded)
		}
		switch result.op {
		case pencodeMarketUninstall:
			if model.extensionStates[result.id] != pencodeExtensionRemoved {
				model.removeExtension(result.id)
			}
			m.feedback = "扩展已卸载"
		case pencodeMarketInstall:
			m.selected, m.feedback = result.id, "扩展已安装"
			_, found = present[result.id]
		case pencodeMarketEnable:
			m.feedback = "扩展已启用"
		case pencodeMarketDisable:
			m.feedback = "扩展已禁用"
		default:
			m.feedback = ""
		}
		if loadError != "" {
			m.feedback = loadError
		}
		if !found {
			m.selected = ""
			if len(m.items) > 0 {
				m.selected = m.items[0].ID
			}
		}
		m.revision++
		return true
	default:
		return false
	}
}

func (model *workspaceModel) handleMarketplaceEvent(c *module.ContentEventContext[appProps, appState]) bool {
	m := model.market
	if m == nil {
		return false
	}
	switch c.Ref {
	case "open-marketplace":
		m.open = !m.open
		if m.open {
			model.settingsOpen, model.quickOpen, model.findOpen, model.capturingShortcut = false, false, false, false
			if model.session != nil && model.session.connection != nil {
				model.session.connection.open, model.session.connection.formOpen = false, false
			}
			model.startMarketOperation(pencodeMarketRefresh, "", "")
		}
	case "close-marketplace":
		model.closeMarketplace()
	case "market-search":
		m.filter = strings.ToLower(c.Event.Value)
		m.revision++
	case "market-refresh":
		model.startMarketOperation(pencodeMarketRefresh, "", "")
	case "market-open-directory":
		model.startMarketOperation(pencodeMarketOpenDirectory, "", "")
	case "market-install":
		if m.busy || model.app == nil || model.app.OpenFolder == nil {
			return true
		}
		source, err := model.app.OpenFolder(window.FileDialogOptions{Title: "选择扩展目录（包含 resources/extension.json）", DefaultPath: model.lastDirectory})
		if err != nil {
			m.feedback = fmt.Sprintf("选择扩展失败：%v", err)
		} else if source != "" {
			model.startMarketOperation(pencodeMarketInstall, source, "")
		}
	case "market-uninstall":
		if m.selected != "" {
			model.startMarketOperation(pencodeMarketUninstall, "", m.selected)
		}
	case "market-toggle":
		for i := range m.items {
			item := &m.items[i]
			if item.ID == m.selected {
				op := pencodeMarketDisable
				if item.Disabled {
					op = pencodeMarketEnable
				}
				model.startMarketOperation(op, "", item.ID)
				break
			}
		}
	default:
		return false
	}
	return true
}

func (model *workspaceModel) closeMarketplace() {
	if model.market == nil {
		return
	}
	model.market.open = false
	if model.fontResources != nil {
		if entity, ok := model.fontResources.HTMLControl(model.surface, "marketplace-button"); ok {
			model.fontResources.Focus(entity)
		}
	}
}

func renderPencodeMarketItem(c module.ContentRenderContext[pencodeMarketProps, struct{}], view *htmlui.View) {
	p := c.Props
	if p == nil || p.Item == nil {
		return
	}
	class := "pencode-market-item"
	if p.Selected {
		class += " selected"
	}
	view.Attr("item", "class", class)
	view.Attr("item", "aria-pressed", boolAttribute(p.Selected))
	view.Text("name", p.Item.Label())
	view.Text("publisher", p.Item.ID)
	view.Event("item", htmlui.EventClick)
}

func handlePencodeMarketItem(c *module.ContentEventContext[pencodeMarketProps, struct{}]) {
	p := c.Props
	if p == nil || p.Model == nil || p.Item == nil || p.Model.market == nil {
		return
	}
	p.Model.market.selected = p.Item.ID
	p.Model.market.revision++
	markPencodeRootDirtyByModel(c.World, p.Model)
}

func (model *workspaceModel) renderMarketplace(view *htmlui.View, definition *htmlui.Definition[pencodeMarketProps, struct{}]) {
	m := model.market
	view.Event("open-marketplace", htmlui.EventClick)
	view.Event("close-marketplace", htmlui.EventClick)
	view.Event("market-refresh", htmlui.EventClick)
	view.Event("market-open-directory", htmlui.EventClick)
	view.Event("market-install", htmlui.EventClick)
	view.Event("market-uninstall", htmlui.EventClick)
	view.Event("market-toggle", htmlui.EventClick)
	view.Event("market-search", htmlui.EventInput)
	if m == nil || !m.open {
		view.Attr("marketplace-panel", "hidden", "")
		view.Attr("open-marketplace", "aria-expanded", "false")
		return
	}
	view.RemoveAttr("marketplace-panel", "hidden")
	view.Attr("open-marketplace", "aria-expanded", "true")
	view.Attr("marketplace-panel", "aria-busy", boolAttribute(m.busy))
	view.Text("market-feedback", m.feedback)
	view.Text("market-directory", m.directory)
	if model.remoteWorkspace() {
		view.Attr("market-open-directory", "hidden", "")
	} else {
		view.RemoveAttr("market-open-directory", "hidden")
	}
	for _, ref := range [...]string{"market-refresh", "market-install", "market-uninstall", "market-open-directory"} {
		if m.busy || ref == "market-uninstall" && m.selected == "" || ref == "market-open-directory" && m.directory == "" {
			view.Attr(ref, "disabled", "")
		} else {
			view.RemoveAttr(ref, "disabled")
		}
	}
	if m.rowsModel != model || m.rowsRevision != m.revision || m.rowsDefinition != definition {
		clear(m.rows[:cap(m.rows)])
		m.rows = m.rows[:0]
		var selected *extensions.Installed
		for i := range m.items {
			item := &m.items[i]
			if item.ID == m.selected {
				selected = item
			}
			if m.filter != "" && !strings.Contains(strings.ToLower(item.Label()+" "+item.ID+" "+item.Description), m.filter) {
				continue
			}
			m.rows = append(m.rows, htmlui.ComponentChild(componentKey(item.ID), definition, pencodeMarketProps{Model: model, Item: item, Selected: item.ID == m.selected}, struct{}{}))
		}
		m.cacheDetails(selected)
		m.rowsModel, m.rowsRevision, m.rowsDefinition = model, m.revision, definition
	}
	view.Components("market-list", m.rows)
	label := "禁用扩展"
	if m.selectedItem != nil && m.selectedItem.Disabled {
		label = "启用扩展"
	}
	view.Text("market-toggle", label)
	if m.busy || m.selectedItem == nil {
		view.Attr("market-toggle", "disabled", "")
	} else {
		view.RemoveAttr("market-toggle", "disabled")
	}
	if len(m.rows) == 0 {
		view.RemoveAttr("market-empty", "hidden")
	} else {
		view.Attr("market-empty", "hidden", "")
	}
	for i, ref := range [...]string{"market-name", "market-meta", "market-description", "market-status", "market-path"} {
		view.Text(ref, m.details[i])
	}
}

func (m *pencodeMarketplace) cacheDetails(selected *extensions.Installed) {
	m.selectedItem = selected
	if selected == nil {
		m.details = [5]string{"暂无已安装扩展", "", "点击「从本地目录安装」，或将扩展复制到安装目录后刷新。", "", ""}
		return
	}
	meta := selected.ID
	if selected.Version != "" {
		meta += " · " + selected.Version
	}
	if selected.Publisher != "" {
		meta += " · " + selected.Publisher
	}
	status := "已安装"
	if selected.Disabled {
		status = "已禁用"
	}
	if selected.Problem != "" {
		status = "扩展需要检查：" + selected.Problem
	}
	m.details = [5]string{selected.Label(), meta, selected.Description, status, selected.Path}
}
