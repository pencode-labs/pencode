package workbench

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onix-engine/onix/module"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/onix-engine/onix/window"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/output"
)

type pencodeMarketOperation uint8

const (
	pencodeMarketRefresh pencodeMarketOperation = iota
	pencodeMarketInstall
	pencodeMarketDownload
	pencodeMarketUpdate
	pencodeMarketUninstall
	pencodeMarketEnable
	pencodeMarketDisable
	pencodeMarketOpenDirectory
)

type pencodeMarketResult struct {
	err         error
	marketError error
	listings    []extensions.Listing
	bundles     map[string]*extensions.Bundle
	directory   string
	id          string
	items       []extensions.Installed
	op          pencodeMarketOperation
}

// UI state belongs to the session, so a scene switch does not lose an ongoing
// installation. Workers publish immutable results; only the UI mutates state.
type pencodeMarketplace struct {
	rowsModel                  *workspaceModel
	rowsDefinition             *htmlui.Definition[pencodeMarketProps, struct{}]
	selectedItem               *extensions.Installed
	selectedListing            *extensions.Listing
	source                     extensions.Marketplace
	listings                   []extensions.Listing
	entries                    []extensions.Installed
	cancel                     context.CancelFunc
	done                       chan struct{}
	results                    chan pencodeMarketResult
	details                    [5]string
	filter, selected, feedback string
	directory                  string // Backend path; only local workspaces can open it.
	items                      []extensions.Installed
	rows                       []htmlui.ComponentVNode
	revision, rowsRevision     uint64
	open, busy, download       bool
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
		m.feedback = model.tr("服务未连接")
		return
	}
	m.busy, m.feedback = true, model.tr("正在读取扩展…")
	switch op {
	case pencodeMarketInstall, pencodeMarketDownload:
		m.feedback = model.tr("正在安装扩展…")
	case pencodeMarketUpdate:
		m.feedback = model.tr("正在更新扩展…")
	case pencodeMarketUninstall:
		m.feedback = model.tr("正在卸载扩展…")
	case pencodeMarketEnable:
		m.feedback = model.tr("正在启用扩展…")
	case pencodeMarketDisable:
		m.feedback = model.tr("正在禁用扩展…")
	case pencodeMarketOpenDirectory:
		m.feedback = model.tr("正在打开扩展目录…")
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
	marketSource := m.source
	var listing extensions.Listing
	if op == pencodeMarketDownload || op == pencodeMarketUpdate {
		for i := range m.listings {
			if m.listings[i].Name == id {
				listing = m.listings[i]
				break
			}
		}
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
		case pencodeMarketInstall, pencodeMarketDownload, pencodeMarketUpdate:
			var item extensions.Installed
			var bundle *extensions.Bundle
			if op == pencodeMarketInstall {
				item, bundle, r.err = extensions.Install(ctx, backend, source)
			} else {
				item, bundle, r.err = marketSource.Install(ctx, backend, listing, op == pencodeMarketUpdate)
			}
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
				r.listings, r.marketError = marketSource.List(ctx, catalog.OS, catalog.Arch)
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
			m.feedback = fmt.Sprintf(model.tr("操作失败：%v"), result.err)
			return true
		}
		if result.op == pencodeMarketOpenDirectory {
			m.feedback = ""
			return true
		}
		m.items, m.directory = result.items, result.directory
		if result.listings != nil {
			m.listings = result.listings
		}
		if result.op == pencodeMarketUpdate {
			model.unloadExtension(result.id)
			model.setExtensionState(result.id, 0)
		}
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
		for _, listing := range m.listings {
			found = found || listing.Name == m.selected
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
				loadError = fmt.Sprintf(model.tr("扩展 %s 加载失败：%v"), id, err)
				continue
			}
			model.setExtensionState(id, pencodeExtensionLoaded)
		}
		switch result.op {
		case pencodeMarketUninstall:
			if model.extensionStates[result.id] != pencodeExtensionRemoved {
				model.removeExtension(result.id)
			}
			m.feedback = model.tr("扩展已卸载")
		case pencodeMarketInstall, pencodeMarketDownload, pencodeMarketUpdate:
			m.selected, m.feedback = result.id, model.tr("扩展已安装")
			_, found = present[result.id]
			if result.op == pencodeMarketUpdate {
				m.feedback = model.tr("扩展已更新")
			}
		case pencodeMarketEnable:
			m.feedback = model.tr("扩展已启用")
		case pencodeMarketDisable:
			m.feedback = model.tr("扩展已禁用")
		default:
			m.feedback = ""
		}
		if result.marketError != nil {
			m.feedback = fmt.Sprintf(model.tr("扩展源加载失败：%v"), result.marketError)
		}
		if loadError != "" {
			m.feedback = loadError
		}
		if !found {
			m.selected = ""
			if len(m.listings) > 0 {
				m.selected = m.listings[0].Name
			} else if len(m.items) > 0 {
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
			model.clearFindCache()
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
		source, err := model.app.OpenFolder(window.FileDialogOptions{Title: model.tr("选择扩展目录（包含 resources/extension.json）"), DefaultPath: model.lastDirectory})
		if err != nil {
			m.feedback = fmt.Sprintf(model.tr("选择扩展失败：%v"), err)
		} else if source != "" {
			model.startMarketOperation(pencodeMarketInstall, source, "")
		}
	case "market-download":
		if m.selectedListing != nil && m.selectedListing.URL != "" && m.selectedListing.Problem == "" {
			op := pencodeMarketDownload
			if m.selectedItem != nil {
				if !m.selectedListing.NewerThan(m.selectedItem.Version) {
					return true
				}
				op = pencodeMarketUpdate
			}
			model.startMarketOperation(op, "", m.selectedListing.Name)
		}
	case "market-uninstall":
		if m.selectedItem != nil {
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
	view.Text("description", p.Item.Description)
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
	view.Event("market-download", htmlui.EventClick)
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
	for _, ref := range [...]string{"market-refresh", "market-install", "market-open-directory"} {
		if m.busy || ref == "market-open-directory" && m.directory == "" {
			view.Attr(ref, "disabled", "")
		} else {
			view.RemoveAttr(ref, "disabled")
		}
	}
	if m.rowsModel != model || m.rowsRevision != m.revision || m.rowsDefinition != definition {
		clear(m.rows[:cap(m.rows)])
		m.rows = m.rows[:0]
		clear(m.entries)
		m.entries = append(m.entries[:0], m.items...)
		for _, listing := range m.listings {
			installed := false
			for i := range m.items {
				if m.items[i].ID == listing.Name {
					installed = true
					break
				}
			}
			if !installed {
				m.entries = append(m.entries, extensions.Installed{ExtensionManifest: extensions.Manifest{ID: listing.Name, Name: listing.Name, Version: listing.Version, Description: listing.Description}})
			}
		}
		var selected *extensions.Installed
		for i := range m.entries {
			item := &m.entries[i]
			if item.ID == m.selected {
				selected = item
			}
			if m.filter != "" && !strings.Contains(strings.ToLower(item.Label()+" "+item.ID+" "+item.Description), m.filter) {
				continue
			}
			m.rows = append(m.rows, definition.ComponentChild(componentKey(item.ID), pencodeMarketProps{Model: model, Item: item, Selected: item.ID == m.selected}, struct{}{}))
		}
		model.cacheMarketDetails(selected)
		m.rowsModel, m.rowsRevision, m.rowsDefinition = model, m.revision, definition
	}
	view.Components("market-list", m.rows)
	downloadLabel := model.tr("安装扩展")
	if m.selectedItem != nil {
		downloadLabel = model.tr("更新扩展")
	}
	view.Text("market-download", downloadLabel)
	if m.download {
		view.RemoveAttr("market-download", "hidden")
	} else {
		view.Attr("market-download", "hidden", "")
	}
	if m.busy || !m.download {
		view.Attr("market-download", "disabled", "")
	} else {
		view.RemoveAttr("market-download", "disabled")
	}
	if m.busy || m.selectedItem == nil {
		view.Attr("market-uninstall", "disabled", "")
	} else {
		view.RemoveAttr("market-uninstall", "disabled")
	}
	label := model.tr("禁用扩展")
	if m.selectedItem != nil && m.selectedItem.Disabled {
		label = model.tr("启用扩展")
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

func (model *workspaceModel) cacheMarketDetails(selected *extensions.Installed) {
	m := model.market
	m.selectedItem, m.selectedListing, m.download = nil, nil, false
	if selected == nil {
		m.details = [5]string{model.tr("暂无扩展"), "", model.tr("刷新扩展市场，或从本地目录安装扩展。"), "", ""}
		return
	}
	for i := range m.items {
		if m.items[i].ID == selected.ID {
			m.selectedItem = &m.items[i]
			break
		}
	}
	for i := range m.listings {
		if m.listings[i].Name == selected.ID {
			m.selectedListing = &m.listings[i]
			break
		}
	}
	meta := selected.ID
	if selected.Version != "" {
		meta += " · " + selected.Version
	}
	if selected.Publisher != "" {
		meta += " · " + selected.Publisher
	}
	status := model.tr("已安装")
	if m.selectedItem == nil {
		status = model.tr("未安装")
	}
	if selected.Disabled {
		status = model.tr("已禁用")
	}
	if selected.Problem != "" {
		status = fmt.Sprintf(model.tr("扩展需要检查：%s"), selected.Problem)
	}
	if listing := m.selectedListing; listing != nil {
		if listing.Problem != "" {
			message := "版本信息获取失败：%s"
			if m.selectedItem != nil {
				message = "更新检查失败：%s"
			}
			status += " · " + fmt.Sprintf(model.tr(message), listing.Problem)
		} else if m.selectedItem != nil && listing.NewerThan(selected.Version) {
			status += " · " + fmt.Sprintf(model.tr("可更新至 %s"), listing.Version)
		}
	}
	m.download = m.selectedListing != nil && m.selectedListing.URL != "" && m.selectedListing.Problem == "" && (m.selectedItem == nil || m.selectedListing.NewerThan(selected.Version))
	m.details = [5]string{selected.Label(), meta, selected.Description, status, selected.Path}
}
